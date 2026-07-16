// codexhooks.go implements the `auspex hook codex
// {session-start,user-prompt-submit,stop}` orchestration functions (issue
// #9 Phase 1) that internal/cli's command constructors call into — the
// Codex CLI analog of hooks.go's Claude handlers, reusing the same
// HookDeps collaborators (persist/bootstrap/evaluate/forecast seams) so a
// Codex session gets the exact treatment a Claude session already gets.
//
// The handlers here are deliberately separate functions rather than a
// generalization of the claude ones: the claude handlers are coupled to
// claude's parsers and payload semantics (statusline snapshots, minted
// TurnIDs, StopFailure classification) in ways Codex does not share (no
// statusline hook, provider-native turn_ids, no StopFailure event), so the
// smallest correct surface is a sibling set following the same pattern —
// the shared machinery (HookDeps.persist, bootstrapSession's underlying
// Bootstrapper, stampOpenTurn) is reused as-is.
//
// Every handler follows hooks.go's JSON/error contract verbatim: fail-open
// on malformed stdin (a Auspex bug must never break the user's Codex
// session), never any raw prompt/response text past the parser's stack
// frame, and a block decision is response content, never an error.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func (d HookDeps) codexNormalizer() *codextelemetry.Normalizer {
	return codextelemetry.NewNormalizer(d.Clock, d.IDs)
}

// bootstrapCodexSession is bootstrapSession's codex twin: same nil-safe,
// fail-open lazy registration (issue #17), stamped with the codex provider
// identifier so provider_sessions rows honestly record which CLI drove the
// session. Codex hook payloads all carry a model, so rows here get their
// model populated from the very first hook (claude needs the statusline
// for that).
func (d HookDeps) bootstrapCodexSession(ctx context.Context, sessionID domain.SessionID, dir *string, model *string) {
	if dir == nil || *dir == "" {
		return
	}
	d.Bootstrapper.Bootstrap(ctx, SessionBootstrap{
		SessionID: sessionID,
		Dir:       *dir,
		Provider:  codextelemetry.Provider,
		Model:     model,
	})
}

// --- auspex hook codex session-start --------------------------------------

// CodexSessionStartResult is HandleCodexSessionStart's return value.
type CodexSessionStartResult struct {
	EventsNormalized int
	Persisted        bool
}

// HandleCodexSessionStart implements `auspex hook codex session-start`:
// parse the SessionStart payload, lazily register the session
// (issue #17's bootstrap, with the model this payload already carries),
// normalize the matching session-lifecycle event
// (started/resumed/compacted per the payload's source enum), and
// best-effort persist. Codex's session-start.command.output schema has no
// decision field at all, so the CLI layer answers `{}` unconditionally;
// this handler's job stops at telemetry.
func HandleCodexSessionStart(ctx context.Context, deps HookDeps, stdin []byte) (CodexSessionStartResult, error) {
	parsed, err := codexhooks.ParseSessionStart(stdin)
	if err != nil {
		return CodexSessionStartResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the hook.
	}
	deps.bootstrapCodexSession(ctx, parsed.SessionID, parsed.CWD, parsed.Model)
	observedAt := deps.Clock.Now()
	event := deps.codexNormalizer().NormalizeSessionStart(parsed, observedAt)
	persisted := deps.persist(ctx, []v1.Event{event})
	return CodexSessionStartResult{EventsNormalized: 1, Persisted: persisted}, nil
}

// --- auspex hook codex user-prompt-submit ----------------------------------

// CodexUserPromptSubmitResult is HandleCodexUserPromptSubmit's return
// value: the provider-compatible response body plus diagnostics — the
// field-for-field codex analog of UserPromptSubmitResult.
type CodexUserPromptSubmitResult struct {
	Response         codexhooks.UserPromptSubmitResponse
	EventsNormalized int
	Persisted        bool
	Evaluated        bool
}

// HandleCodexUserPromptSubmit implements `auspex hook codex
// user-prompt-submit`: parse+hash the prompt (raw text never survives
// codexhooks.ParseUserPromptSubmit's stack frame), normalize a
// provider.turn.started event stamped with Codex's own turn_id, best-effort
// persist it, and — when an EvaluationService is wired — run the same
// Evaluate pipeline the Claude hook runs to render a block/allow decision.
// The gate semantics mirror HandleUserPromptSubmit exactly: allow is the
// fail-open default for every internal failure, block is Auspex's own
// considered output and never an error, and the issue-#14 forecast card
// rides additionalContext on both outcomes.
func HandleCodexUserPromptSubmit(ctx context.Context, deps HookDeps, stdin []byte) (CodexUserPromptSubmitResult, error) {
	parsed, err := codexhooks.ParseUserPromptSubmit(stdin)
	if err != nil {
		//nolint:nilerr // deliberate fail-open, see the function doc comment above.
		return CodexUserPromptSubmitResult{
			Response: codexhooks.UserPromptSubmitResponse{Decision: codexhooks.HookDecisionAllow},
		}, nil
	}

	pe, err := evaluateCodexPrompt(ctx, deps, parsed)

	result := CodexUserPromptSubmitResult{
		Response:         codexhooks.UserPromptSubmitResponse{Decision: codexhooks.HookDecisionAllow},
		EventsNormalized: 1,
		Persisted:        pe.persisted,
	}
	if err != nil {
		// No EvaluationService wired, or an evaluation-pipeline failure —
		// an operational gap, not a reason to block the user's prompt
		// (ADD §17.5); fall back to the plain allow above.
		//nolint:nilerr // deliberate fail-open, see the function doc comment above.
		return result, nil
	}
	result.Evaluated = true

	additional := ""
	if deps.Forecast != nil {
		if card, cardErr := deps.Forecast.ForecastCard(ctx, pe.evaluation.ID); cardErr == nil {
			additional = card.AdditionalContext()
		}
	}

	if pe.decision.Action == app.PolicyBlock {
		result.Response = codexhooks.UserPromptSubmitResponse{
			Decision:          codexhooks.HookDecisionBlock,
			Reason:            "Auspex evaluation " + string(pe.evaluation.ID) + " requires a checkpoint or explicit override before this task starts.",
			AdditionalContext: additional,
		}
	} else {
		result.Response.AdditionalContext = additional
	}
	return result, nil
}

// evaluateCodexPrompt is evaluateSubmittedPrompt's codex twin: the single
// path from a parsed (already privacy-safe) codex prompt event to a
// persisted evaluation + policy decision. One deliberate difference from
// claude's: the TurnID is Codex's own turn_id when the payload carried one
// (provider-stable — a re-delivered hook and its terminal Stop share it
// natively), minted only when absent so EvaluateTurn always gets one.
func evaluateCodexPrompt(ctx context.Context, deps HookDeps, parsed codexhooks.UserPromptSubmitEvent) (promptEvaluation, error) {
	deps.bootstrapCodexSession(ctx, parsed.SessionID, parsed.CWD, parsed.Model)

	observedAt := deps.Clock.Now()
	event := deps.codexNormalizer().NormalizeUserPromptSubmit(parsed, observedAt)

	pe := promptEvaluation{turnID: parsed.TurnID}
	if pe.turnID == "" {
		pe.turnID = domain.TurnID(deps.IDs.NewID())
		event.TurnID = string(pe.turnID)
	}
	pe.persisted = deps.persist(ctx, []v1.Event{event})

	if deps.Evaluation == nil {
		return pe, errNoEvaluationService
	}

	eval, err := deps.Evaluation.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID:  parsed.SessionID,
		TurnID:     pe.turnID,
		Provider:   codextelemetry.Provider,
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

// --- auspex hook codex stop -------------------------------------------------

// CodexStopResult is HandleCodexStop's return value.
type CodexStopResult struct {
	EventsNormalized int
	Persisted        bool
	// UsageExtracted is true when the rollout yielded a token snapshot
	// (i.e. the turn.completed event carries the per-turn actual and the
	// context/quota observations were emitted alongside).
	UsageExtracted bool
}

// HandleCodexStop implements `auspex hook codex stop`: parse, resolve the
// session rollout file, extract the numbers-only token/quota/context
// snapshot (codextelemetry.ReadRolloutSnapshot — ADR-051's fail-open
// discipline applied to the rollout), normalize the terminal event set
// (turn.completed with per-turn actuals + context.observed +
// quota.observed), and best-effort persist.
//
// Rollout resolution order: the payload's transcript_path when present
// (verified against codex-rs v0.144.4: it IS the rollout path), else a
// sessions-directory scan by session id (transcript_path is null until the
// rollout is materialized, and for remote threads). Either miss degrades to
// a bare turn.completed — never a hook failure.
//
// Codex payloads carry the turn's own id, so events are stamped directly;
// stampOpenTurn runs only as the fallback for a payload without one,
// keeping parity with claude's terminal-event correlation.
func HandleCodexStop(ctx context.Context, deps HookDeps, stdin []byte) (CodexStopResult, error) {
	parsed, err := codexhooks.ParseStop(stdin)
	if err != nil {
		return CodexStopResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the Stop hook.
	}
	deps.bootstrapCodexSession(ctx, parsed.SessionID, parsed.CWD, parsed.Model)

	var snap *codextelemetry.RolloutSnapshot
	if path, ok := resolveCodexRollout(parsed); ok {
		if s, ok := codextelemetry.ReadRolloutSnapshot(path); ok {
			snap = &s
		}
	}

	observedAt := deps.Clock.Now()
	events := deps.codexNormalizer().NormalizeStop(parsed, observedAt, snap)
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return CodexStopResult{
		EventsNormalized: len(events),
		Persisted:        persisted,
		UsageExtracted:   snap != nil,
	}, nil
}

// resolveCodexRollout picks the rollout file for a Stop payload:
// transcript_path verbatim when the payload carried one, else the
// sessions-directory scan fallback. ok=false when neither yields a path.
func resolveCodexRollout(parsed codexhooks.StopEvent) (string, bool) {
	if parsed.TranscriptPath != nil && *parsed.TranscriptPath != "" {
		return *parsed.TranscriptPath, true
	}
	dir, ok := codextelemetry.DefaultSessionsDir()
	if !ok {
		return "", false
	}
	return codextelemetry.FindRolloutPath(dir, string(parsed.SessionID))
}

// --- auspex hook codex status (Phase 1b) ------------------------------------

// CodexStatusReader resolves the latest observed Codex session for a
// directory plus its most recent context/quota observations — the DB
// read-back behind `auspex hook codex status`. ok=false means "no codex
// session is known for that directory" (or a reader-side failure): the
// caller renders the bare line, never an error. Implementations must be
// fail-open.
type CodexStatusReader interface {
	LatestCodexStatus(ctx context.Context, cwd string) (CodexStatusSnapshot, bool)
}

// CodexStatusSnapshot is the numbers-only display state
// CodexStatusReader resolves: the session identity plus the latest
// persisted context and weekly-quota measurements.
type CodexStatusSnapshot struct {
	SessionID domain.SessionID
	Model     string // "" when provider_sessions has none recorded
	// ContextUsedPercent is derived from the latest
	// provider.context.observed event's used/window tokens. nil when no
	// context observation exists (unknown is not zero).
	ContextUsedPercent *float64
	// WeeklyUsedPercent is the latest provider.quota.observed
	// used_percent for the secondary (weekly) window. nil when none.
	WeeklyUsedPercent *float64
}

// HandleCodexStatus implements `auspex hook codex status` (issue #9 Phase
// 1b): render the one-line
//
//	ax» <model> │ ◷ weekly ~<pct>% │ context [<bar>] <cur>% │ <verdict>
//
// status display for the most recent Codex session observed in cwd,
// reading ONLY already-persisted telemetry (no stdin — tmux cannot provide
// hook stdin, which is this command's entire reason to exist; Codex itself
// has no statusline hook to wire). The renderer is the SAME
// evaluation.StatusLineText the Claude statusline uses, so the two
// providers' lines are visually consistent, and the forecast-card verdict
// segment lights up through the same deps.Forecast seam when one is wired.
// Every degradation is fail-open into a shorter line, never an error and
// never an empty line — a status bar must keep rendering (the ADD §17.5
// discipline HandleStatusLineEmitLine documents).
func HandleCodexStatus(ctx context.Context, deps HookDeps, cwd string) (string, error) {
	if deps.CodexStatus == nil {
		return evaluation.StatusLineText(evaluation.StatusLineInput{}), nil
	}
	snap, ok := deps.CodexStatus.LatestCodexStatus(ctx, cwd)
	if !ok {
		return evaluation.StatusLineText(evaluation.StatusLineInput{}), nil
	}

	var card *evaluation.ForecastCard
	if deps.Forecast != nil {
		if c, cardOK, err := deps.Forecast.LatestForecastCard(ctx, snap.SessionID); err == nil && cardOK {
			card = &c
		}
	}
	return evaluation.StatusLineText(evaluation.StatusLineInput{
		Model:                  snap.Model,
		Card:                   card,
		ContextUsedPercent:     snap.ContextUsedPercent,
		WeeklyLimitUsedPercent: snap.WeeklyUsedPercent,
	}), nil
}
