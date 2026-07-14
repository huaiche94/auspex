// hooks.go implements agents/runtime.md Part B's four P0 Claude Code hook
// commands (`auspex hook claude statusline|user-prompt-submit|stop|
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
//     UserPromptSubmitEvent fields ever touch this file's handlers; the
//     one deliberate raw-text entry point in this package is
//     evaluateprompt.go's EvaluatePrompt, which hashes immediately via
//     claudehooks.NewUserPromptSubmitEvent — see that file's own
//     privacy-boundary doc);
//   - returns the frozen domain.Error shape (code/message/retryable/
//     details) on internal failure, distinct from a hook's own semantic
//     "block" decision (which is not an error — see HandleUserPromptSubmit);
//   - always produces a syntactically valid provider hook response body
//     even when Auspex's own internal step failed, via each Response's
//     Fallback() method — "hook fallback remains syntactically valid when
//     Auspex fails" is proven by fallback_test.go's malformed-payload
//     cases (a fallback is a criterion this node's tests exercise
//     directly, not an aspiration).
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	claudeprovider "github.com/huaiche94/auspex/internal/providers/claude"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// EventPersister is a narrow interface over
// internal/telemetry/claude.EventStore's PersistAll — declared locally
// (not imported as a concrete *claudetelemetry.EventStore dependency) so
// hook handlers can run, and be tested, without a real SQLite-backed
// store wired in. A nil EventPersister is valid: persistence is skipped
// (fail-open per ADD §17.5's "telemetry unavailable -> fail open +
// warning" — a hook must never fail the user's actual prompt/turn because
// Auspex's own event log could not be written).
type EventPersister interface {
	PersistAll(ctx context.Context, runner app.TxRunner, evs []v1.Event) error
}

// ForecastCardSource reads back the issue-#14 forecast card for an
// evaluation (internal/evaluation/forecastcard.go). A narrow local
// interface over the concrete *evaluation.Service's two card methods —
// deliberately NOT the frozen app.EvaluationService (which has no card
// methods and must not be widened; ADR-043 keeps contract impact
// additive) — following the same only-the-real-service pattern
// decision.go's AuthorizationIssuer established: a fake can satisfy
// app.EvaluationService alone, but a card requires the real persisted
// prediction/policy rows only *evaluation.Service can read back.
type ForecastCardSource interface {
	// ForecastCard returns the card for one evaluation ID (the hook path:
	// HandleUserPromptSubmit just ran that evaluation).
	ForecastCard(ctx context.Context, id domain.EvaluationID) (evaluation.ForecastCard, error)
	// LatestForecastCard returns the most recent card linkable to a
	// session, ok=false on cold start (the statusline --emit-line path).
	LatestForecastCard(ctx context.Context, sessionID domain.SessionID) (evaluation.ForecastCard, bool, error)
}

// HookDeps bundles a hook handler's collaborators. Clock/IDs feed the
// Normalizer (claude-provider-04's real, already-integrated
// implementation — not a fake). TxRunner is required only when Persister
// is non-nil (PersistAll needs a transaction boundary to run inside).
// Evaluation is used only by HandleUserPromptSubmit, to render a policy
// decision into the hook's block/allow response. Correlator, when non-nil,
// annotates events with TaskID/ProgressNodeID before they are persisted
// (correlate.go — the issue #1 event-correlation component); nil keeps the
// pre-correlation behavior (events persisted with SessionID only), the
// same nil-is-a-documented-degrade convention Persister already uses.
// Forecast, when non-nil, renders the issue-#14 forecast card into
// UserPromptSubmit's additionalContext and the statusline's --emit-line
// output; nil (and any card-read error) degrades to exactly the
// pre-issue-#14 responses — the card is presentation on top of the hook
// contract, never a new failure mode for it. Bootstrapper, when non-nil,
// lazily registers the session's repositories/worktrees/provider_sessions
// chain from each hook payload's reported directory before events are
// persisted or an evaluation runs (issue #17, sessionbootstrap.go) — the
// missing write path that previously left SQLDataSource.Resolve
// permanently not_found in real native-hook sessions; nil skips
// registration entirely, exactly the pre-issue-#17 behavior.
type HookDeps struct {
	Clock        domain.Clock
	IDs          domain.IDGenerator
	Persister    EventPersister
	TxRunner     app.TxRunner
	Evaluation   app.EvaluationService
	Correlator   *EventCorrelator
	Forecast     ForecastCardSource
	Bootstrapper *SessionBootstrapper

	// OpenTurns optionally enables TURN correlation for terminal events
	// (issue #11; ADR-046's "the join upgrades automatically once outcome
	// events gain turn correlation"). The Stop/StopFailure hooks run in a
	// fresh process that never saw the turn's UserPromptSubmit, so their
	// events carry no TurnID of their own — this seam resolves the
	// session's most recently STARTED turn so the terminal event can be
	// stamped with it, which is what activates the prediction↔actual
	// outcome join (retention's calibration_samples, the calibration
	// export's actual side). nil disables stamping entirely — terminal
	// events persist with SessionID only, exactly the pre-#11 behavior.
	OpenTurns OpenTurnResolver
}

// OpenTurnResolver resolves a session's latest started turn. ok=false
// means "no started turn is known" (a session whose first observed hook
// is a Stop, a pre-Auspex session, or a resolver-side failure) — the
// caller stamps nothing, never a fabricated ID. Implementations must be
// fail-open: a lookup error is an ok=false, not a hook failure.
type OpenTurnResolver interface {
	LatestStartedTurn(ctx context.Context, sessionID domain.SessionID) (domain.TurnID, bool)
}

func (d HookDeps) normalizer() *claudetelemetry.Normalizer {
	return claudetelemetry.NewNormalizer(d.Clock, d.IDs)
}

// bootstrapSession runs the issue-#17 lazy session bootstrap
// (sessionbootstrap.go) for one hook invocation, before the caller
// persists events or evaluates — so SQLDataSource.Resolve (the evaluation
// pipeline's first step, and the correlator's session -> task lookup)
// can succeed from the session's very first hook onward. dir is the hook
// payload's own reported directory (cwd / statusline workspace), pointer-
// typed because every payload field that can be absent stays a pointer
// end to end (Constitution "unknown is not zero"): nil/empty means the
// payload carried no directory, and no row is fabricated. Bootstrap is
// nil-receiver-safe and returns only a bool, so this helper — like
// persist and Correlate above — can never turn a hook invocation into a
// failure (ADD §17.5 fail-open).
func (d HookDeps) bootstrapSession(ctx context.Context, sessionID domain.SessionID, dir *string, model, effort *string) {
	if dir == nil || *dir == "" {
		return
	}
	d.Bootstrapper.Bootstrap(ctx, SessionBootstrap{
		SessionID: sessionID,
		Dir:       *dir,
		Provider:  claudetelemetry.Provider,
		Model:     model,
		Effort:    effort,
	})
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
	// Best-effort correlation before the durable write (issue #1's event-
	// correlation design, correlate.go): populate TaskID/ProgressNodeID
	// where they resolve unambiguously, leave them empty everywhere else.
	// Correlate is nil-receiver-safe and never returns an error, so it can
	// never turn a persistable batch into a failed hook — the fail-open
	// contract this function's own doc comment describes extends to it.
	d.Correlator.Correlate(ctx, evs)
	if err := d.Persister.PersistAll(ctx, d.TxRunner, evs); err != nil {
		return false
	}
	return true
}

// --- auspex hook claude statusline ---------------------------------------

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

// HandleStatusLine implements `auspex hook claude statusline` (ADD
// §22.5): parse the stdin JSON status-line snapshot, normalize it into
// usage/quota/context observation events via the real, already-integrated
// claude-provider-04 Normalizer, and best-effort persist them. Per ADD
// §22.6, Auspex's wrapper is expected to ultimately compose with
// whatever status-line command was previously configured; that installer/
// compose mechanism is a separate, not-yet-built concern (no
// internal/statusline composition package exists this wave) — this
// handler's job stops at normalize+persist, matching what claude-
// provider-04 and this node's own migration/storage dependencies actually
// support today. Callers that need the composed text output build it on
// top of this result.
//
// Malformed stdin is not escalated as a hard failure: a status line must
// keep rendering even when Auspex cannot parse its own input, so a
// parse error here yields a zero StatusLineResult and a nil error —
// exactly the fail-open contract ADD §17.5 assigns to telemetry
// unavailability. internal/cli's command wraps this to guarantee the
// process still exits 0 with harmless output; see hook.go.
func HandleStatusLine(ctx context.Context, deps HookDeps, stdin []byte) (StatusLineResult, error) {
	_, result, _ := statusLineIngest(ctx, deps, stdin)
	return result, nil
}

// statusLineIngest is the shared parse+normalize+persist core behind
// HandleStatusLine and HandleStatusLineEmitLine, factored out (rather
// than one handler calling the other) because the emit-line variant also
// needs the parsed snapshot itself (model identity, session ID) which
// HandleStatusLine's result deliberately does not carry. parsedOK=false
// means the stdin was malformed — the same fail-open condition
// HandleStatusLine has always swallowed.
func statusLineIngest(ctx context.Context, deps HookDeps, stdin []byte) (claudeprovider.StatusLineSnapshot, StatusLineResult, bool) {
	snap, err := claudeprovider.ParseStatusLine(stdin)
	if err != nil {
		return claudeprovider.StatusLineSnapshot{}, StatusLineResult{}, false
	}

	// Issue #17: register the session before persisting, from the
	// snapshot's workspace directory. The statusline is the one hook
	// payload that carries a model identity, so this is also where
	// provider_sessions.model gets populated (issue #17 deliverable 3).
	deps.bootstrapSession(ctx, snap.SessionID, statusLineWorkspaceDir(snap), statusLineModel(snap), snap.EffortLevel)

	observedAt := deps.Clock.Now()
	events := deps.normalizer().NormalizeStatusLine(snap, observedAt)

	result := StatusLineResult{EventsNormalized: len(events)}
	result.Persisted = deps.persist(ctx, events)
	return snap, result, true
}

// HandleStatusLineEmitLine implements `auspex hook claude statusline
// --emit-line` (issue #14 deliverable 4, resolving issue #12's recorded
// friction #2: Claude Code's statusLine command must PRINT the display
// line — wiring the ingest-only handler directly blanks the user's status
// bar). It performs exactly HandleStatusLine's ingest (same parse, same
// normalize, same best-effort persist — statusLineIngest is the single
// shared implementation, so the two cannot drift) and additionally
// composes the one-line display text:
//
//	ax» <model> │ ◷ weekly ~<pct>% │ context [<bar>] <cur>% (p90 ≤<pct>%) │ ✓ RUN
//
// using the latest persisted evaluation for the session when one exists
// (deps.Forecast.LatestForecastCard), else just "ax» <model>" plus the
// weekly and measured-context segments when the snapshot carried them
// (those segments are live snapshot data, not card data). Every
// degradation is fail-open into a shorter line, never an error and never
// an empty line: malformed stdin renders bare "ax»", a missing model
// omits the model segment, a missing/errored card omits the forecast
// segments — a status line must keep rendering even when Auspex cannot
// parse its own input (the same ADD §17.5 discipline HandleStatusLine
// already documents).
func HandleStatusLineEmitLine(ctx context.Context, deps HookDeps, stdin []byte) (StatusLineResult, string, error) {
	snap, result, parsedOK := statusLineIngest(ctx, deps, stdin)
	if !parsedOK {
		return result, evaluation.StatusLineText(evaluation.StatusLineInput{}), nil
	}

	model := ""
	switch {
	case snap.ModelDisplayName != nil && *snap.ModelDisplayName != "":
		model = *snap.ModelDisplayName
	case snap.ModelID != nil && *snap.ModelID != "":
		model = *snap.ModelID
	}

	var card *evaluation.ForecastCard
	if deps.Forecast != nil {
		if c, ok, err := deps.Forecast.LatestForecastCard(ctx, snap.SessionID); err == nil && ok {
			card = &c
		}
		// err != nil / ok=false both degrade to the model-only line —
		// cold start and a card-read failure look identical here by
		// design; the status bar is no place for an error message.
	}
	// The context-measurement and weekly-limit inputs come straight from
	// the live snapshot (real observed data since #27), NOT from the
	// card — they render even when the session has no forecast yet. The
	// current context percent prefers the exact token ratio over the
	// provider's whole-percent rounding (D-14), mirroring the predictor's
	// own input preference.
	var ctxCurrent *float64
	if snap.ContextInputTokens != nil && snap.ContextWindowSize != nil && *snap.ContextWindowSize > 0 {
		total := *snap.ContextInputTokens
		if snap.ContextOutputTokens != nil {
			total += *snap.ContextOutputTokens
		}
		pct := float64(total) / float64(*snap.ContextWindowSize) * 100
		ctxCurrent = &pct
	} else if snap.ContextUsedPercent != nil {
		ctxCurrent = snap.ContextUsedPercent
	}
	return result, evaluation.StatusLineText(evaluation.StatusLineInput{
		Model:                  model,
		Card:                   card,
		ContextUsedPercent:     ctxCurrent,
		WeeklyLimitUsedPercent: snap.WeeklyLimitUsedPercent(),
	}), nil
}

// --- auspex hook claude user-prompt-submit -------------------------------

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

// HandleUserPromptSubmit implements `auspex hook claude
// user-prompt-submit`: parse+hash the prompt (never retaining raw text
// past claudehooks.ParseUserPromptSubmit, per Constitution §7 rule 2),
// normalize a provider.turn.started event, best-effort persist it, and —
// when an EvaluationService is wired — run it through the Evaluate
// pipeline (runtime-b03) to render a block/allow decision matching ADD
// §22.3's UserPromptSubmit block shape.
//
// A block decision is Auspex's own considered output, not an error:
// this function returns (result, nil) for both allow and block, and
// callers render whichever Decision the result carries. Only a genuine
// internal fault (malformed stdin) short-circuits to the safe default
// allow response — again fail-open, matching HandleStatusLine, because a
// Auspex bug must never be the reason a user's prompt is silently
// swallowed.
func HandleUserPromptSubmit(ctx context.Context, deps HookDeps, stdin []byte) (UserPromptSubmitResult, error) {
	parsed, err := claudehooks.ParseUserPromptSubmit(stdin)
	if err != nil {
		// fail-open: malformed hook input falls back to the safe allow
		// response rather than propagating the parse error.
		//nolint:nilerr // deliberate fail-open, see the function doc comment above.
		return UserPromptSubmitResult{
			Response: claudehooks.UserPromptSubmitResponse{Decision: claudehooks.HookDecisionAllow},
		}, nil
	}

	pe, err := evaluateSubmittedPrompt(ctx, deps, parsed)

	result := UserPromptSubmitResult{
		Response:         claudehooks.UserPromptSubmitResponse{Decision: claudehooks.HookDecisionAllow},
		EventsNormalized: 1,
		Persisted:        pe.persisted,
	}
	if err != nil {
		// No EvaluationService wired, or an evaluation-pipeline failure —
		// either way an operational gap, not a reason to block the user's
		// prompt (ADD §17.5: "predictor error -> fallback heuristic");
		// this handler's fallback is the plain allow response above.
		//nolint:nilerr // deliberate fail-open, see the function doc comment above.
		return result, nil
	}
	result.Evaluated = true

	// Issue #14 deliverable 3: render the forecast card into the hook
	// response's additionalContext so the coding agent literally sees the
	// scope/token/cost/risk estimate before acting. Strictly additive and
	// fail-open: a nil Forecast source or a card-read error leaves
	// additional empty, degrading to exactly the pre-issue-#14 response —
	// the card must never become a new way for the hook to fail.
	additional := ""
	if deps.Forecast != nil {
		if card, cardErr := deps.Forecast.ForecastCard(ctx, pe.evaluation.ID); cardErr == nil {
			additional = card.AdditionalContext()
		}
	}

	if pe.decision.Action == app.PolicyBlock {
		result.Response = claudehooks.UserPromptSubmitResponse{
			Decision:          claudehooks.HookDecisionBlock,
			Reason:            "Auspex evaluation " + string(pe.evaluation.ID) + " requires a checkpoint or explicit override before this task starts.",
			AdditionalContext: additional,
		}
	} else {
		result.Response.AdditionalContext = additional
	}
	return result, nil
}

// errNoEvaluationService is evaluateSubmittedPrompt's typed error for a
// nil Evaluation dependency. HandleUserPromptSubmit swallows it (nil
// EvaluationService is a documented degrade path there); EvaluatePrompt
// propagates it (a CLI evaluation with no service is a real,
// user-visible composition gap, not something to silently allow past).
var errNoEvaluationService = &domain.Error{
	Code:      domain.ErrCodeUnavailable,
	Message:   "orchestrator: no EvaluationService wired",
	Retryable: false,
}

// promptEvaluation is evaluateSubmittedPrompt's result: the shared
// normalize -> persist -> evaluate -> decide outcome both
// HandleUserPromptSubmit (fail-open) and EvaluatePrompt (fail-closed)
// consume. persisted is valid even when the returned error is non-nil —
// the telemetry write happens before, and independently of, the
// evaluation itself, exactly as it always has.
type promptEvaluation struct {
	turnID     domain.TurnID
	persisted  bool
	evaluation app.Evaluation
	decision   app.DecisionResult
}

// evaluateSubmittedPrompt runs the single production path from a parsed
// (already privacy-safe: hash/length/approx-tokens only) prompt event to
// a persisted evaluation + policy decision. Shared verbatim by the
// UserPromptSubmit hook and `auspex evaluate` (issue #14 deliverable
// 5's "share code, don't duplicate"), so an offline evaluation is the
// same evaluation a hook would have produced.
//
// One TurnID is minted and used for BOTH the persisted
// provider.turn.started event and EvaluateTurn — stamping the event
// (event.TurnID) is what links this session's events to the turn-scoped
// prediction row (migration 0041 has no session column), which is exactly
// the linkage evaluation.(*Service).LatestForecastCard's statusline query
// joins on. Before issue #14 the event carried no turn_id and the minted
// TurnID existed only on the prediction row, leaving persisted
// evaluations unreachable from their session.
func evaluateSubmittedPrompt(ctx context.Context, deps HookDeps, parsed claudehooks.UserPromptSubmitEvent) (promptEvaluation, error) {
	// Issue #17: lazily register this session's repositories/worktrees/
	// provider_sessions chain from the payload's cwd BEFORE persisting or
	// evaluating — EvaluateTurn's very first pipeline step is
	// SQLDataSource.Resolve(sessionID), which needs these rows to exist.
	// The model stays nil: a UserPromptSubmit payload carries no model
	// field, and unknown is not zero (Constitution/ADD principle 1) — the
	// statusline hook fills provider_sessions.model when it observes one.
	// On the `auspex evaluate` path (EvaluatePrompt), parsed.CWD is nil
	// (the event is synthesized from prompt text, not a hook payload), so
	// this is a documented no-op there: an offline evaluation targets a
	// session some hook already registered, or honestly fails not_found.
	deps.bootstrapSession(ctx, parsed.SessionID, parsed.CWD, nil, nil)

	observedAt := deps.Clock.Now()
	event := deps.normalizer().NormalizeUserPromptSubmit(parsed, observedAt)

	pe := promptEvaluation{turnID: domain.TurnID(deps.IDs.NewID())}
	event.TurnID = string(pe.turnID)
	pe.persisted = deps.persist(ctx, []v1.Event{event})

	if deps.Evaluation == nil {
		return pe, errNoEvaluationService
	}

	eval, err := deps.Evaluation.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID:  parsed.SessionID,
		TurnID:     pe.turnID,
		Provider:   "claude",
		PromptHash: parsed.PromptSHA256,
	})
	if err != nil {
		return pe, err
	}
	pe.evaluation = eval

	decision, err := deps.Evaluation.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		return pe, err
	}
	pe.decision = decision
	return pe, nil
}

// --- auspex hook claude stop / stop-failure ------------------------------

// StopResult is HandleStop's return value.
type StopResult struct {
	EventsNormalized int
	Persisted        bool
}

// HandleStop implements `auspex hook claude stop`: parse, normalize a
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
	// Issue #17: Stop payloads DO carry a cwd (claudehooks.StopEvent.CWD),
	// so a session whose first observed hook is a Stop — e.g. Auspex
	// installed mid-session — still gets registered rather than staying
	// invisible to Resolve until its next prompt.
	deps.bootstrapSession(ctx, parsed.SessionID, parsed.CWD, nil, nil)
	observedAt := deps.Clock.Now()
	event := deps.normalizer().NormalizeStop(parsed, observedAt)
	events := []v1.Event{event}
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return StopResult{EventsNormalized: 1, Persisted: persisted}, nil
}

// stampOpenTurn fills TurnID on every event in evs that lacks one, using
// the session's most recently STARTED turn (issue #11 turn correlation —
// see HookDeps.OpenTurns). Sessions are turn-serial (Claude Code runs one
// turn at a time), so the latest started turn is by construction the one
// a terminal hook is reporting on. Two disclosed edge semantics:
//
//   - A turn.started orphaned by a crash (no terminal event ever fired)
//     is superseded the moment the NEXT turn starts — latest-started
//     matching therefore never mis-attributes a stop backward across a
//     newer turn; the orphan simply stays outcome-less (honest).
//   - A re-entrant Stop (stop_hook_active) stamps the SAME turn a second
//     time; downstream joins take the EARLIEST terminal event per turn
//     (retention's lookupTurnOutcomes), so the duplicate is inert.
//
// Fail-open like every other hook seam: nil resolver or ok=false stamps
// nothing, and an already-populated TurnID is never overwritten (the
// managed one-shot path mints and stamps its own).
func (d HookDeps) stampOpenTurn(ctx context.Context, sessionID domain.SessionID, evs []v1.Event) {
	if d.OpenTurns == nil {
		return
	}
	needed := false
	for i := range evs {
		if evs[i].TurnID == "" {
			needed = true
			break
		}
	}
	if !needed {
		return
	}
	turnID, ok := d.OpenTurns.LatestStartedTurn(ctx, sessionID)
	if !ok || turnID == "" {
		return
	}
	for i := range evs {
		if evs[i].TurnID == "" {
			evs[i].TurnID = string(turnID)
		}
	}
}

// StopFailureResult is HandleStopFailure's return value.
type StopFailureResult struct {
	EventsNormalized int
	Persisted        bool
	FailureClass     domain.FailureClass
}

// HandleStopFailure implements `auspex hook claude stop-failure`:
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
	// Issue #17: StopFailure payloads carry a cwd too — same reasoning as
	// HandleStop above.
	deps.bootstrapSession(ctx, parsed.SessionID, parsed.CWD, nil, nil)
	observedAt := deps.Clock.Now()
	events := deps.normalizer().NormalizeStopFailure(parsed, observedAt)
	// Turn correlation covers the failure path too: both the turn.failed
	// event AND the companion rate_limit.hit (when emitted) belong to the
	// same just-ended turn.
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return StopFailureResult{
		EventsNormalized: len(events),
		Persisted:        persisted,
		FailureClass:     parsed.FailureClass,
	}, nil
}
