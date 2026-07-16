// evaluateprompt.go: the orchestration behind `auspex evaluate`
// (issue #14 deliverable 5) — a REAL evaluation of caller-supplied prompt
// text through the exact same production path HandleUserPromptSubmit
// runs (hooks.go's evaluateSubmittedPrompt: normalize -> best-effort
// persist -> EvaluateTurn -> Decide), not a parallel reimplementation.
// The only differences from the hook are the entry shape (prompt text
// from a file/stdin instead of a hook payload) and the error posture:
// the hook fails open to an allow response because a Auspex bug must
// never swallow a user's prompt, while a CLI evaluation the user
// explicitly asked for fails CLOSED — an EvaluateTurn/Decide error
// surfaces as the typed *domain.Error the CLI's error contract renders,
// never a fabricated result (the same fail-open/fail-closed split
// evaluate.go's Evaluate already documents for the same two stages).
//
// # Privacy boundary (Constitution §7 rule 2; predictor-02's precedent)
//
// Raw prompt text enters exactly one call here —
// claudehooks.NewUserPromptSubmitEvent, which hashes it immediately, the
// same derivation ParseUserPromptSubmit applies to a real hook payload —
// and is never stored on any struct, logged, or returned. Everything
// downstream sees only PromptSHA256/byte-length/approx-tokens, identical
// to the hook path; internal/integrationtest's leakage-scanner-style
// privacy test drives this function end-to-end and scans the resulting
// on-disk DB bytes for the raw text.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// EvaluatePromptRequest is EvaluatePrompt's input. Prompt is consumed
// in-memory (hashed immediately — see the file doc comment); it may be
// empty, in which case the evaluation runs on the hash/features of the
// empty prompt, the same thing a hook payload with no prompt field
// produces (claudehooks.ParseUserPromptSubmit's own behavior).
type EvaluatePromptRequest struct {
	SessionID domain.SessionID
	Prompt    string
}

// EvaluatePromptResult bundles the persisted evaluation, its policy
// decision, and (when a ForecastCardSource is wired and the read-back
// succeeds) the issue-#14 forecast card. Card is nil — an explicit,
// documented degrade, not an error — when deps.Forecast is nil or the
// card read fails: the evaluation itself succeeded and was persisted, so
// the CLI still reports IDs and the policy action rather than discarding
// a real result because its presentation layer hiccuped.
type EvaluatePromptResult struct {
	Evaluation app.Evaluation
	Decision   app.DecisionResult
	Card       *evaluation.ForecastCard
	Persisted  bool
}

// EvaluatePrompt hashes req.Prompt, mints a synthetic TurnID via the
// injected IDGenerator, and runs the shared hook evaluation path (see the
// file doc comment). Validation errors, a missing EvaluationService, and
// EvaluateTurn/Decide failures all return typed *domain.Error values
// (fail-closed); only the forecast-card read-back degrades softly.
func EvaluatePrompt(ctx context.Context, deps HookDeps, req EvaluatePromptRequest) (EvaluatePromptResult, error) {
	if req.SessionID == "" {
		return EvaluatePromptResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "orchestrator: EvaluatePrompt requires a non-empty SessionID",
			Retryable: false,
		}
	}

	parsed := claudehooks.NewUserPromptSubmitEvent(req.SessionID, req.Prompt)
	pe, err := evaluateSubmittedPrompt(ctx, deps, parsed, claudetelemetry.Provider)
	if err != nil {
		return EvaluatePromptResult{}, err
	}

	result := EvaluatePromptResult{
		Evaluation: pe.evaluation,
		Decision:   pe.decision,
		Persisted:  pe.persisted,
	}
	if deps.Forecast != nil {
		if card, cardErr := deps.Forecast.ForecastCard(ctx, pe.evaluation.ID); cardErr == nil {
			result.Card = &card
		}
	}
	return result, nil
}
