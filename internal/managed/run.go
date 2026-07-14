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
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// ProviderClaude is the one provider this increment supports (issue #8
// MVP; the Codex managed adapter is ADD M7, a different milestone).
const ProviderClaude = "claude"

// DefaultProviderBin is the provider binary spawned when Runner.
// ProviderBin is empty: the user's own `claude` CLI, resolved from PATH
// by os/exec exactly like any argv-only process launch.
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
type RunOutcome struct {
	TurnID       domain.TurnID
	EvaluationID domain.EvaluationID
	Decision     app.PolicyAction
	GateDegraded bool

	ExitCode        int
	Stream          StreamSummary
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
	bin := r.ProviderBin
	if bin == "" {
		bin = DefaultProviderBin
	}

	// Session bootstrap (issue #17) with the honest managed invocation
	// mode, BEFORE the gate: the gate's shared path re-bootstraps with
	// the hook default, but provider_sessions.invocation_mode is
	// first-observation-wins, so registering first is what makes the row
	// say managed_stream_json (see orchestrator.SessionBootstrap's field
	// doc). Nil-receiver-safe and fail-open by Bootstrap's own contract.
	if req.Dir != "" {
		r.Hooks.Bootstrapper.Bootstrap(ctx, orchestrator.SessionBootstrap{
			SessionID:      req.SessionID,
			Dir:            req.Dir,
			Provider:       claudetelemetry.Provider,
			InvocationMode: orchestrator.InvocationModeManagedStreamJSON,
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
	// The exact argv shape is ADD §22.1's supported managed path:
	// `claude -p <prompt> --output-format stream-json --verbose`.
	cmd := exec.CommandContext(ctx, bin, "-p", req.Prompt, "--output-format", "stream-json", "--verbose")
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
		outcome.EventsPersisted += r.persistTerminal(ctx, req, gate.TurnID, -1, StreamSummary{}, true)
		return outcome, &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "managed: starting provider binary failed",
			Retryable: true,
			Details:   map[string]string{"provider_bin": bin},
		}
	}

	outcome.Stream = readStream(stdout, relay)

	exitCode := 0
	if waitErr := cmd.Wait(); waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Started but could not be waited to completion (I/O fault,
			// kill without status): -1 per gitx.ExecRunner's convention —
			// an honest "no exit code observed", never a fabricated 0.
			exitCode = -1
		}
	}
	outcome.ExitCode = exitCode
	outcome.EventsPersisted += r.persistTerminal(ctx, req, gate.TurnID, exitCode, outcome.Stream, false)
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
	if req.Provider != ProviderClaude {
		return fail("managed: only provider \"claude\" is supported by this increment (issue #8 MVP)", map[string]string{"provider": req.Provider})
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

// persistTerminal normalizes the run's terminal outcome into events
// (internal/telemetry/claude.NormalizeManagedRun), best-effort correlates
// them (issue #1; nil-receiver-safe), and best-effort persists them
// through the same Persister/TxRunner seam the hook path uses — returning
// how many events were durably handed over (0 on any persistence
// degrade, mirroring HookDeps' own persist discipline: losing telemetry
// is never a reason to fail the user's finished run).
func (r *Runner) persistTerminal(ctx context.Context, req RunRequest, turnID domain.TurnID, exitCode int, stream StreamSummary, spawnFailed bool) int {
	o := claudetelemetry.ManagedRunOutcome{
		SessionID:   req.SessionID,
		TurnID:      turnID,
		WorktreeID:  req.WorktreeID,
		TaskID:      req.TaskID,
		ExitCode:    exitCode,
		SpawnFailed: spawnFailed,
		// The stream's own model declaration (system init line), "" when
		// never observed — the normalizer stamps it onto the usage event
		// so ADR-047's cohort ladder can family-label the token sample
		// without guessing.
		ModelID: stream.Model,
	}
	if res := stream.Result; res != nil {
		o.ResultSeen = true
		o.ResultSubtype = res.Subtype
		o.IsError = res.IsError
		o.DurationMs = res.DurationMs
		o.DurationAPIMs = res.DurationAPIMs
		o.NumTurns = res.NumTurns
		o.TotalCostUSD = res.TotalCostUSD
		o.ResultTextLen = res.ResultTextLen
		if u := res.Usage; u != nil {
			o.InputTokens = u.InputTokens
			o.OutputTokens = u.OutputTokens
			o.CacheReadInputTokens = u.CacheReadInputTokens
			o.CacheCreationInputTokens = u.CacheCreationInputTokens
		}
	}

	normalizer := claudetelemetry.NewNormalizer(r.Hooks.Clock, r.Hooks.IDs)
	events := normalizer.NormalizeManagedRun(o, r.Hooks.Clock.Now())
	r.Hooks.Correlator.Correlate(ctx, events)
	if r.Hooks.Persister == nil || r.Hooks.TxRunner == nil {
		return 0
	}
	if err := r.Hooks.Persister.PersistAll(ctx, r.Hooks.TxRunner, events); err != nil {
		return 0
	}
	return len(events)
}
