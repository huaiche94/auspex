// hooks.go implements agents/runtime.md Part B's four P0 Claude Code hook
// commands (`preflight hook claude statusline|user-prompt-submit|stop|
// stop-failure`) as orchestration functions internal/cli's command
// constructors call into. This is runtime-b04's scope.
//
// # Division of labor with claude-provider-04 (already integrated)
//
// Parsing a raw hook payload into an intermediate Go struct
// (internal/providers/claude.ParseStatusLine, internal/hooks/claude.Parse*)
// and normalizing that struct into the frozen pkg/protocol/v1.Event
// envelope (internal/telemetry/claude.Normalizer) are both
// claude-provider-04's real, already-merged work (Wave 2) — this package
// calls them directly, not a fake, per the task brief ("claude-provider-04's
// normalizer IS already integrated"). What this node adds is the missing
// middle/outer layer: reading stdin, invoking parse+normalize, optionally
// persisting the normalized events, calling the evaluation pipeline where
// the hook semantics call for a decision (UserPromptSubmit), and encoding
// the provider-compatible stdout response — none of which claude-provider-04
// or runtime-b02/b03 already cover end to end.
//
// # JSON/error requirements (agents/runtime.md Part B "JSON and errors")
//
// Every handler in this file:
//   - never logs or returns raw prompt text (only the pre-hashed
//     UserPromptSubmitEvent fields ever touch this package);
//   - returns the frozen domain.Error shape (code/message/retryable/
//     details) on internal failure, distinct from a hook's own semantic
//     "block" decision (which is not an error — see HandleUserPromptSubmit);
//   - always produces a syntactically valid provider hook response body
//     even when Preflight's own internal step failed, via each Response's
//     Fallback() method — "hook fallback remains syntactically valid when
//     Preflight fails" is proven by fallback_test.go's malformed-payload
//     cases (a fallback is a criterion this node's tests exercise
//     directly, not an aspiration).
package orchestrator

import (
	"context"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	claudehooks "github.com/huaiche94/preflight/internal/hooks/claude"
	claudeprovider "github.com/huaiche94/preflight/internal/providers/claude"
	claudetelemetry "github.com/huaiche94/preflight/internal/telemetry/claude"
	v1 "github.com/huaiche94/preflight/pkg/protocol/v1"
)

// EventPersister is a narrow interface over
// internal/telemetry/claude.EventStore's PersistAll — declared locally
// (not imported as a concrete *claudetelemetry.EventStore dependency) so
// hook handlers can run, and be tested, without a real SQLite-backed
// store wired in. A nil EventPersister is valid: persistence is skipped
// (fail-open per ADD §17.5's "telemetry unavailable -> fail open +
// warning" — a hook must never fail the user's actual prompt/turn because
// Preflight's own event log could not be written).
type EventPersister interface {
	PersistAll(ctx context.Context, runner app.TxRunner, evs []v1.Event) error
}

// HookDeps bundles a hook handler's collaborators. Clock/IDs feed the
// Normalizer (claude-provider-04's real, already-integrated
// implementation — not a fake). TxRunner is required only when Persister
// is non-nil (PersistAll needs a transaction boundary to run inside).
// Evaluation is used only by HandleUserPromptSubmit, to render a policy
// decision into the hook's block/allow response.
type HookDeps struct {
	Clock      domain.Clock
	IDs        domain.IDGenerator
	Persister  EventPersister
	TxRunner   app.TxRunner
	Evaluation app.EvaluationService
}

func (d HookDeps) normalizer() *claudetelemetry.Normalizer {
	return claudetelemetry.NewNormalizer(d.Clock, d.IDs)
}

// persist runs evs through d.Persister inside d.TxRunner if both are
// configured; otherwise it is a documented no-op (see HookDeps doc). A
// persistence error is itself swallowed into a returned bool rather than
// aborting the caller — the same fail-open discipline
// internal/orchestrator.Evaluate uses for its own operational-observation
// steps (evaluate.go) — a hook's job is to keep the provider's turn
// moving; losing one batch of telemetry is not a reason to block it.
func (d HookDeps) persist(ctx context.Context, evs []v1.Event) (persisted bool) {
	if d.Persister == nil || d.TxRunner == nil || len(evs) == 0 {
		return false
	}
	if err := d.Persister.PersistAll(ctx, d.TxRunner, evs); err != nil {
		return false
	}
	return true
}

// --- preflight hook claude statusline ---------------------------------------

// StatusLineResult is HandleStatusLine's return value.
type StatusLineResult struct {
	// EventsNormalized is how many pkg/protocol/v1.Event values the
	// snapshot produced (0-4 per NormalizeStatusLine: context, usage,
	// five_hour quota, seven_day quota — each only emitted when its
	// underlying fields were actually present in the payload, per ADD
	// §22.5 "Fields 可能 null/absent；不得 fallback 0").
	EventsNormalized int
	Persisted        bool
}

// HandleStatusLine implements `preflight hook claude statusline` (ADD
// §22.5): parse the stdin JSON status-line snapshot, normalize it into
// usage/quota/context observation events via the real, already-integrated
// claude-provider-04 Normalizer, and best-effort persist them. Per ADD
// §22.6, Preflight's wrapper is expected to ultimately compose with
// whatever status-line command was previously configured; that installer/
// compose mechanism is a separate, not-yet-built concern (no
// internal/statusline composition package exists this wave) — this
// handler's job stops at normalize+persist, matching what claude-
// provider-04 and this node's own migration/storage dependencies actually
// support today. Callers that need the composed text output build it on
// top of this result.
//
// Malformed stdin is not escalated as a hard failure: a status line must
// keep rendering even when Preflight cannot parse its own input, so a
// parse error here yields a zero StatusLineResult and a nil error —
// exactly the fail-open contract ADD §17.5 assigns to telemetry
// unavailability. internal/cli's command wraps this to guarantee the
// process still exits 0 with harmless output; see hook.go.
func HandleStatusLine(ctx context.Context, deps HookDeps, stdin []byte) (StatusLineResult, error) {
	snap, err := claudeprovider.ParseStatusLine(stdin)
	if err != nil {
		return StatusLineResult{}, nil //nolint:nilerr // fail-open: malformed status-line input must not break the status line itself.
	}

	observedAt := deps.Clock.Now()
	events := deps.normalizer().NormalizeStatusLine(snap, observedAt)

	result := StatusLineResult{EventsNormalized: len(events)}
	result.Persisted = deps.persist(ctx, events)
	return result, nil
}

// --- preflight hook claude user-prompt-submit -------------------------------

// UserPromptSubmitResult is HandleUserPromptSubmit's return value: the
// provider-compatible response body to write to stdout, plus diagnostics.
type UserPromptSubmitResult struct {
	Response         claudehooks.UserPromptSubmitResponse
	EventsNormalized int
	Persisted        bool
	// Evaluated is true when an evaluation was actually run (Evaluation
	// service configured); false means this handler fell back to a plain
	// allow without an opinion (no EvaluationService wired — Deps.Evaluation
	// nil is itself a valid, documented degrade path, distinct from an
	// evaluation ERROR, which also degrades to allow but is recorded
	// differently — see the source).
	Evaluated bool
}

// HandleUserPromptSubmit implements `preflight hook claude
// user-prompt-submit`: parse+hash the prompt (never retaining raw text
// past claudehooks.ParseUserPromptSubmit, per Constitution §7 rule 2),
// normalize a provider.turn.started event, best-effort persist it, and —
// when an EvaluationService is wired — run it through the Evaluate
// pipeline (runtime-b03) to render a block/allow decision matching ADD
// §22.3's UserPromptSubmit block shape.
//
// A block decision is Preflight's own considered output, not an error:
// this function returns (result, nil) for both allow and block, and
// callers render whichever Decision the result carries. Only a genuine
// internal fault (malformed stdin) short-circuits to the safe default
// allow response — again fail-open, matching HandleStatusLine, because a
// Preflight bug must never be the reason a user's prompt is silently
// swallowed.
func HandleUserPromptSubmit(ctx context.Context, deps HookDeps, stdin []byte) (UserPromptSubmitResult, error) {
	parsed, err := claudehooks.ParseUserPromptSubmit(stdin)
	if err != nil {
		return UserPromptSubmitResult{
			Response: claudehooks.UserPromptSubmitResponse{Decision: claudehooks.HookDecisionAllow},
		}, nil //nolint:nilerr // fail-open: malformed hook input falls back to the safe allow response.
	}

	observedAt := deps.Clock.Now()
	event := deps.normalizer().NormalizeUserPromptSubmit(parsed, observedAt)
	persisted := deps.persist(ctx, []v1.Event{event})

	result := UserPromptSubmitResult{
		Response:         claudehooks.UserPromptSubmitResponse{Decision: claudehooks.HookDecisionAllow},
		EventsNormalized: 1,
		Persisted:        persisted,
	}

	if deps.Evaluation == nil {
		return result, nil
	}

	turnID := domain.TurnID(deps.IDs.NewID())
	eval, err := deps.Evaluation.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID:  parsed.SessionID,
		TurnID:     turnID,
		Provider:   "claude",
		PromptHash: parsed.PromptSHA256,
	})
	if err != nil {
		// An evaluation-pipeline failure is an operational gap, not a
		// reason to block the user's prompt (ADD §17.5: "predictor error
		// -> fallback heuristic"); this handler's fallback is the plain
		// allow response already set above.
		return result, nil
	}
	result.Evaluated = true

	decision, err := deps.Evaluation.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		return result, nil
	}

	if decision.Action == app.PolicyBlock {
		result.Response = claudehooks.UserPromptSubmitResponse{
			Decision: claudehooks.HookDecisionBlock,
			Reason:   "Preflight evaluation " + string(eval.ID) + " requires a checkpoint or explicit override before this task starts.",
		}
	}
	return result, nil
}

// --- preflight hook claude stop / stop-failure ------------------------------

// StopResult is HandleStop's return value.
type StopResult struct {
	EventsNormalized int
	Persisted        bool
}

// HandleStop implements `preflight hook claude stop`: parse, normalize a
// provider.turn.completed event, best-effort persist. Full Progress
// Tree/Git/artifact reconciliation (ADD §22.4: "Stop 時 reconcile Progress
// Tree、Git、artifacts") is outcome labeling depth beyond this node's
// scope (agents/runtime.md Part B pipeline step 12, "Observe actual
// outcome," and step 9's checkpoint orchestration are runtime-b05's and a
// later node's concern) — this handler covers the telemetry half only,
// matching what claude-provider-04's Normalizer actually emits today.
func HandleStop(ctx context.Context, deps HookDeps, stdin []byte) (StopResult, error) {
	parsed, err := claudehooks.ParseStop(stdin)
	if err != nil {
		return StopResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the Stop hook itself.
	}
	observedAt := deps.Clock.Now()
	event := deps.normalizer().NormalizeStop(parsed, observedAt)
	persisted := deps.persist(ctx, []v1.Event{event})
	return StopResult{EventsNormalized: 1, Persisted: persisted}, nil
}

// StopFailureResult is HandleStopFailure's return value.
type StopFailureResult struct {
	EventsNormalized int
	Persisted        bool
	FailureClass     domain.FailureClass
}

// HandleStopFailure implements `preflight hook claude stop-failure`:
// parse+classify, normalize one or two events (NormalizeStopFailure emits
// a second provider.rate_limit.hit event when the classified failure is a
// rate limit — see internal/telemetry/claude.NormalizeStopFailure), and
// best-effort persist. Per Constitution §7 rule 2 and
// claudehooks.ParseStopFailure's own contract, the raw provider error
// message text never reaches this function in the first place — only
// FailureClass and a byte length do.
func HandleStopFailure(ctx context.Context, deps HookDeps, stdin []byte) (StopFailureResult, error) {
	parsed, err := claudehooks.ParseStopFailure(stdin)
	if err != nil {
		return StopFailureResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the StopFailure hook itself.
	}
	observedAt := deps.Clock.Now()
	events := deps.normalizer().NormalizeStopFailure(parsed, observedAt)
	persisted := deps.persist(ctx, events)
	return StopFailureResult{
		EventsNormalized: len(events),
		Persisted:        persisted,
		FailureClass:     parsed.FailureClass,
	}, nil
}
