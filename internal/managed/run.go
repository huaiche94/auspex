// run.go: the managed one-shot run itself — gate, spawn, parse, persist,
// attribute (doc.go lists the package contract and this increment's
// deliberate exclusions). The CLI half lives in internal/cli/run.go; this
// file is the CLI-free core so the whole flow is unit-testable with an
// injected fake provider binary (Runner.ProviderBin) and the same
// in-memory HookDeps doubles the hook handlers' tests use.
package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// ProviderClaude is the claude managed one-shot provider (issue #8 MVP);
// ProviderCodex and the per-provider spec table live in provider.go.
const ProviderClaude = "claude"

// DefaultProviderBin is the provider binary spawned for ProviderClaude
// when Runner.ProviderBin is empty: the user's own `claude` CLI, resolved
// from PATH by os/exec exactly like any argv-only process launch.
const DefaultProviderBin = "claude"

// Runner executes managed one-shot runs. Hooks carries the SAME
// collaborator set the hook handlers use (internal/orchestrator.HookDeps)
// — deliberately, so the gate evaluation, event persistence, correlation,
// and forecast card all ride the exact production seams the hook path
// already proved, with nil fields degrading per HookDeps' own documented
// contracts. Clock and IDs are the only hard requirements (Run fails
// closed without them — a runner that cannot mint IDs or timestamps is a
// composition bug, not a degradable capability).
type Runner struct {
	Hooks orchestrator.HookDeps

	// ProviderBin overrides the spawned binary name/path (argv[0]);
	// empty means DefaultProviderBin. Exists so tests inject a fake
	// provider binary — never a shell string, always an exec argv
	// (Constitution §7 rule 5).
	ProviderBin string
}

// RunRequest is one managed one-shot run. Prompt is passed to the
// provider process as a single argv element and hashed (never persisted
// raw) for the gate evaluation; Dir is the working directory the provider
// runs in and the directory the issue-#17 session bootstrap resolves the
// repository from (empty skips both concerns' directory use). The three
// writers are optional human-surface outputs (nil discards): StreamRelay
// receives the provider's raw stream-json lines as they arrive,
// ProviderStderr receives the provider's own stderr, HumanLog receives
// the runner's own decision/degrade lines — the CLI points all three at
// stderr so stdout stays a pure machine surface for the attribution JSON.
type RunRequest struct {
	Provider   string
	SessionID  domain.SessionID
	WorktreeID domain.WorktreeID
	TaskID     *domain.TaskID
	Prompt     string
	Dir        string

	StreamRelay    io.Writer
	ProviderStderr io.Writer
	HumanLog       io.Writer
}

// RunOutcome is the data the CLI's `auspex.run.v1` attribution JSON is
// built from. EvaluationID/Decision are zero when GateDegraded (the gate
// pipeline errored and the run proceeded decisionless — see Run's
// fail-open contract); ExitCode is the provider process's own exit code
// (-1 when it ran but could not be waited to completion, matching
// internal/gitx.ExecRunner's convention). EventsPersisted counts events
// durably handed to the store across the whole run (the gate's
// provider.turn.started plus the terminal batch).
//
// Exactly one of the two stream summaries is populated, per the run's
// provider (the other stays its zero value): Stream for ProviderClaude,
// Codex for ProviderCodex — separate typed fields rather than a shared
// abstraction because the two providers' stream vocabularies genuinely
// differ (claude's result line carries cost/duration; codex's
// turn.completed carries tokens only) and collapsing them would either
// drop or fabricate fields.
type RunOutcome struct {
	TurnID       domain.TurnID
	EvaluationID domain.EvaluationID
	Decision     app.PolicyAction
	GateDegraded bool

	ExitCode        int
	Stream          StreamSummary
	Codex           CodexStreamSummary
	EventsPersisted int
}

// Run executes one managed one-shot run end to end: bootstrap the
// session (fail-open), gate the prompt, spawn the provider, parse its
// stream, persist the terminal events, and return the attribution data.
//
// Error posture, stage by stage (the same fail-open/fail-closed split
// hooks.go and evaluateprompt.go document for their surfaces):
//
//   - Request validation and missing Clock/IDs fail CLOSED — caller bugs.
//   - The gate's own pipeline error fails OPEN into a decisionless spawn
//     (ADD §17.5 "predictor error -> fallback"; the hook path's exact
//     posture): the user explicitly asked Auspex to run this prompt, and
//     an Auspex-side evaluation outage must not make the managed runner
//     unusable — that would push users back to unmanaged `claude -p`,
//     losing the telemetry entirely. The degrade is loud (HumanLog line,
//     GateDegraded in the attribution), never silent.
//   - A BLOCK decision fails CLOSED before any process is spawned: the
//     typed error is returned and the provider binary is never started —
//     the managed mode's whole enforcement advantage over native hooks
//     (ADD §8.1 vs §8.3).
//   - A spawn failure fails CLOSED (typed error) after best-effort
//     persisting a provider.turn.failed event, so the already-persisted
//     turn.started is not left dangling without a terminal event.
//   - A provider process that starts and exits — with ANY exit code — is
//     NOT a Run error (internal/gitx.ExecRunner's exact contract): the
//     exit code is attribution data, persisted and reported, and the
//     CLI's exit status stays "the managed run itself succeeded".
//   - Telemetry persistence failures fail OPEN into a lower
//     EventsPersisted count, per the hook path's persist discipline.
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunOutcome, error) {
	if err := r.validate(req); err != nil {
		return RunOutcome{}, err
	}
	humanLog := req.HumanLog
	if humanLog == nil {
		humanLog = io.Discard
	}
	// validate already proved the spec exists.
	spec, _ := specFor(req.Provider)
	bin := r.ProviderBin
	if bin == "" {
		bin = spec.defaultBin
	}

	// Session bootstrap (issue #17) with the honest provider and managed
	// invocation mode, BEFORE the gate: the gate's shared path
	// re-bootstraps with the hook default, but provider_sessions'
	// provider/invocation_mode are first-observation-wins, so registering
	// first is what makes the row say managed_stream_json under the run's
	// own provider (see orchestrator.SessionBootstrap's field doc).
	// Nil-receiver-safe and fail-open by Bootstrap's own contract.
	if req.Dir != "" {
		r.Hooks.Bootstrapper.Bootstrap(ctx, orchestrator.SessionBootstrap{
			SessionID:      req.SessionID,
			Dir:            req.Dir,
			Provider:       req.Provider,
			InvocationMode: spec.invocationMode,
		})
	}

	// Pre-prompt gate: the shared hook evaluation path (normalize ->
	// persist turn.started -> EvaluateTurn -> Decide), which also mints
	// the run's TurnID — valid even on the error path, so the terminal
	// events below always join the started event on one TurnID.
	var cwd *string
	if req.Dir != "" {
		d := req.Dir
		cwd = &d
	}
	gate, gateErr := orchestrator.EvaluateManagedPrompt(ctx, r.Hooks, orchestrator.ManagedPromptRequest{
		SessionID: req.SessionID,
		Provider:  req.Provider,
		Prompt:    req.Prompt,
		CWD:       cwd,
	})

	outcome := RunOutcome{TurnID: gate.TurnID}
	if gate.Persisted {
		outcome.EventsPersisted++
	}

	switch {
	case gateErr != nil:
		outcome.GateDegraded = true
		_, _ = fmt.Fprintf(humanLog, "auspex run: evaluation unavailable, proceeding without a policy decision (fail-open per ADD §17.5): %v\n", gateErr)
	case gate.Decision.Action == app.PolicyBlock:
		outcome.EvaluationID = gate.Evaluation.ID
		outcome.Decision = gate.Decision.Action
		return outcome, &domain.Error{
			Code:      domain.ErrCodeUnauthorized,
			Message:   "managed: Auspex evaluation " + string(gate.Evaluation.ID) + " blocked this prompt; the provider was not started. Create a checkpoint or issue an explicit override (auspex decision allow) before re-running.",
			Retryable: false,
			Details: map[string]string{
				"evaluation_id": string(gate.Evaluation.ID),
				"turn_id":       string(gate.TurnID),
				"policy_action": string(app.PolicyBlock),
			},
		}
	default:
		outcome.EvaluationID = gate.Evaluation.ID
		outcome.Decision = gate.Decision.Action
		// ADR-0054 (issue #116): CHECKPOINT_AND_RUN solidifies state
		// BEFORE the provider is spawned — automatic checkpoint pair via
		// the shared Hooks.AutoCheckpoint seam (nil = the advisory
		// pre-#116 behavior). The managed runner already knows its own
		// target, so it passes WorktreeID (and TaskID when the caller
		// supplied one) directly instead of resolving from the session.
		// Fail-open by AutoCheckpointer.Run's own contract: a degraded
		// checkpoint is a loud HumanLog line, never a refused spawn —
		// only BLOCK (above) refuses to spawn.
		if gate.Decision.Action == app.PolicyCheckpointAndRun {
			acpReq := orchestrator.AutoCheckpointRequest{
				SessionID:    req.SessionID,
				TurnID:       gate.TurnID,
				EvaluationID: gate.Evaluation.ID,
				PromptHash:   gate.PromptHash,
				WorktreeID:   req.WorktreeID,
			}
			if req.TaskID != nil {
				acpReq.TaskID = *req.TaskID
			}
			if acp := r.Hooks.AutoCheckpoint.Run(ctx, acpReq); acp.Attempted {
				_, _ = fmt.Fprintln(humanLog, "auspex run: "+acp.ContextLine())
			}
		}
		if gate.Card != nil {
			// The same forecast card the hook injects as
			// additionalContext (one presenter, every surface — issue
			// #14's discipline), so the user sees what the agent would
			// have seen, before the spawn.
			_, _ = fmt.Fprintln(humanLog, gate.Card.AdditionalContext())
		}
		_, _ = fmt.Fprintf(humanLog, "auspex run: decision %s (evaluation %s), spawning provider\n", gate.Decision.Action, gate.Evaluation.ID)
	}

	// Spawn: argv-only, never a shell string (Constitution §7 rule 5).
	// The exact per-provider argv shape lives on the spec (provider.go):
	// ADD §22.1's `claude -p <prompt> --output-format stream-json
	// --verbose`, ADD §21.8's `codex exec --json <prompt>`.
	cmd := exec.CommandContext(ctx, bin, spec.argv(req.Prompt)...)
	cmd.Dir = req.Dir

	// The stream relay (this goroutine, during readStream) and the
	// provider's stderr passthrough (an os/exec-internal copy goroutine,
	// for any non-*os.File writer) run CONCURRENTLY, and callers
	// routinely hand both the same underlying writer (the CLI passes
	// stderr for both). Both are therefore wrapped over ONE shared mutex,
	// and the wrapper deliberately implements only Write: io.Copy would
	// otherwise take its ReaderFrom fast path straight into e.g.
	// bytes.Buffer.ReadFrom, which snapshots the buffer length before
	// BLOCKING on the pipe read for the child's whole lifetime and then
	// truncates back to that snapshot on EOF — silently erasing every
	// relay line written meanwhile. Found by this package's own
	// integration test (relay output vanished from a shared test buffer),
	// not hypothetical.
	ioMu := &sync.Mutex{}
	var relay io.Writer
	if req.StreamRelay != nil {
		relay = lockedWriter{mu: ioMu, w: req.StreamRelay}
	}
	if req.ProviderStderr != nil {
		// Keep cmd.Stderr a true nil interface when unset (os/exec then
		// discards to devnull) rather than a non-nil wrapper of nil.
		cmd.Stderr = lockedWriter{mu: ioMu, w: req.ProviderStderr}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcome, &domain.Error{
			Code:      domain.ErrCodeInternal,
			Message:   "managed: opening provider stdout pipe failed",
			Retryable: false,
		}
	}

	if err := cmd.Start(); err != nil {
		// The provider never ran: persist the terminal turn.failed (so
		// the started event has its outcome) and fail closed with a
		// typed, retryable error naming only the binary — never the
		// prompt.
		outcome.ExitCode = -1
		outcome.EventsPersisted += r.persistTerminal(ctx, spec, req, gate.TurnID, -1, outcome, true)
		return outcome, &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "managed: starting provider binary failed",
			Retryable: true,
			Details:   map[string]string{"provider_bin": bin},
		}
	}

	spec.consume(stdout, relay, &outcome)

	exitCode := 0
	if waitErr := cmd.Wait(); waitErr != nil {
		var exitErr *exec.ExitError
		switch {
		case ctx.Err() != nil:
			// We killed the provider via context cancellation (SIGINT/
			// SIGTERM shutdown, deadline). The resulting exit code is
			// ours, not the provider's: unix SIGKILL already yields -1,
			// but windows' exec.CommandContext uses TerminateProcess(h, 1),
			// fabricating a 1 that would otherwise leak into the terminal
			// turn.failed payload. Normalise both to the honest "-1, no
			// exit code observed" so the contract holds on every OS.
			exitCode = -1
		case errors.As(waitErr, &exitErr):
			exitCode = exitErr.ExitCode()
		default:
			// Started but could not be waited to completion (I/O fault,
			// kill without status): -1 per gitx.ExecRunner's convention —
			// an honest "no exit code observed", never a fabricated 0.
			exitCode = -1
		}
	}
	outcome.ExitCode = exitCode
	outcome.EventsPersisted += r.persistTerminal(ctx, spec, req, gate.TurnID, exitCode, outcome, false)
	return outcome, nil
}

// lockedWriter serializes writes onto a possibly-shared underlying
// writer between the main goroutine's stream relay and os/exec's stderr
// copy goroutine (see the wrapping site in Run for the full WHY,
// including the io.Copy ReaderFrom fast path this type's Write-only
// method set deliberately blocks).
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (lw lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

// validate applies Run's fail-closed input contract. Detail maps carry
// identifiers only — never prompt text (Constitution §7 rule 2).
func (r *Runner) validate(req RunRequest) error {
	fail := func(msg string, details map[string]string) error {
		return &domain.Error{Code: domain.ErrCodeValidation, Message: msg, Retryable: false, Details: details}
	}
	if _, ok := specFor(req.Provider); !ok {
		return fail("managed: unsupported provider \""+req.Provider+"\" (this increment supports \"claude\" and \"codex\")", map[string]string{"provider": req.Provider})
	}
	if req.SessionID == "" {
		return fail("managed: SessionID is required", nil)
	}
	if req.WorktreeID == "" {
		return fail("managed: WorktreeID is required", nil)
	}
	if req.Prompt == "" {
		return fail("managed: prompt must not be empty", nil)
	}
	if r.Hooks.Clock == nil || r.Hooks.IDs == nil {
		return &domain.Error{
			Code:      domain.ErrCodeInternal,
			Message:   "managed: Runner requires Hooks.Clock and Hooks.IDs to be wired",
			Retryable: false,
		}
	}
	return nil
}

// persistTerminal normalizes the run's terminal outcome into events via
// the provider spec's own normalizer (provider.go; claude ->
// NormalizeManagedRun, codex -> NormalizeManagedExec), best-effort
// correlates them (issue #1; nil-receiver-safe), and best-effort persists
// them through the same Persister/TxRunner seam the hook path uses —
// returning how many events were durably handed over (0 on any
// persistence degrade, mirroring HookDeps' own persist discipline: losing
// telemetry is never a reason to fail the user's finished run).
func (r *Runner) persistTerminal(ctx context.Context, spec providerSpec, req RunRequest, turnID domain.TurnID, exitCode int, outcome RunOutcome, spawnFailed bool) int {
	events := spec.terminalEvents(r.Hooks.Clock, r.Hooks.IDs, req, turnID, exitCode, outcome, spawnFailed)
	r.Hooks.Correlator.Correlate(ctx, events)
	if r.Hooks.Persister == nil || r.Hooks.TxRunner == nil {
		return 0
	}
	if err := r.Hooks.Persister.PersistAll(ctx, r.Hooks.TxRunner, events); err != nil {
		return 0
	}
	return len(events)
}
