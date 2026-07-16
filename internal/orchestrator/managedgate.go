// managedgate.go: the pre-prompt gate behind `auspex run` (issue #8's
// managed one-shot MVP, ADD §8.1) — the third entry point into hooks.go's
// shared evaluateSubmittedPrompt path, alongside the UserPromptSubmit hook
// (fail-open) and `auspex evaluate` (fail-closed). It exists for the same
// reason evaluateprompt.go does: the managed runner must run the SAME
// production normalize -> best-effort persist -> EvaluateTurn -> Decide
// sequence a hook runs, never a parallel reimplementation, so the gate
// decision `auspex run` enforces before spawning the provider is exactly
// the decision the hook path would have rendered for the same prompt.
//
// # Why not reuse EvaluatePrompt or HandleUserPromptSubmit directly
//
// EvaluatePrompt (evaluateprompt.go) discards the minted TurnID when the
// evaluation pipeline errors — correct for an offline evaluation, where a
// failed evaluation IS the whole result, but fatal for the managed runner:
// its fail-open degrade path (mirroring the hook's ADD §17.5 posture) still
// spawns the provider and must stamp the SAME TurnID onto the terminal
// provider.turn.completed/failed events that the already-persisted
// provider.turn.started event carries, or the run's outcome attribution
// splits across two turn IDs. HandleUserPromptSubmit returns only the
// provider-compatible hook response (block/allow), not the EvaluationID/
// PolicyAction the runner's `auspex.run.v1` attribution JSON reports. This
// function returns both, plus the TurnID, on every path — including the
// error path, exactly like evaluateSubmittedPrompt itself.
//
// # CWD (issue #17 linkage)
//
// Unlike EvaluatePrompt (whose synthesized event deliberately carries no
// cwd — an offline evaluation targets a session some hook already
// registered), the managed runner DOES know the directory the provider is
// about to run in, so req.CWD threads it onto the synthesized event and
// evaluateSubmittedPrompt's own bootstrapSession call registers the
// session's repositories/worktrees/provider_sessions chain from it —
// making `auspex run` self-sufficient on a fresh database, the same way a
// native hook session is.
//
// # Privacy boundary (Constitution §7 rule 2)
//
// Identical to evaluateprompt.go's: raw prompt text enters exactly one
// call here — claudehooks.NewUserPromptSubmitEvent, which hashes it
// immediately — and is never stored on any struct, logged, or returned.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// ManagedPromptRequest is EvaluateManagedPrompt's input. Prompt is
// consumed in-memory (hashed immediately — see the file doc comment). CWD
// is the directory the managed provider process will run in; nil/empty
// skips the issue-#17 session bootstrap exactly like a hook payload with
// no cwd field (unknown is not zero — no row is fabricated). Provider is
// the managed run's provider identifier, stamped onto the persisted
// turn.started event and handed to EvaluateTurn (issue #9 M7: a codex
// managed run's gate telemetry must say codex); empty defaults to
// claudetelemetry.Provider, preserving every pre-M7 caller's exact
// behavior.
type ManagedPromptRequest struct {
	SessionID domain.SessionID
	Provider  string
	Prompt    string
	CWD       *string
}

// ManagedPromptResult carries everything the managed runner needs from
// the gate, on success AND on pipeline error: TurnID/Persisted are valid
// even when EvaluateManagedPrompt returns a non-nil error (they describe
// the provider.turn.started event that was already normalized and
// best-effort persisted before the pipeline ran — the same guarantee
// evaluateSubmittedPrompt's own promptEvaluation documents), so the
// runner's fail-open degrade path can keep the run's whole event trail on
// one TurnID. Evaluation/Decision/Card are only meaningful when the
// returned error is nil.
type ManagedPromptResult struct {
	TurnID    domain.TurnID
	Persisted bool

	Evaluation app.Evaluation
	Decision   app.DecisionResult
	// Card is the issue-#14 forecast card read back for this evaluation
	// when a ForecastCardSource is wired; nil is the same documented soft
	// degrade EvaluatePromptResult.Card has (presentation only — never a
	// new failure mode for the gate).
	Card *evaluation.ForecastCard
}

// EvaluateManagedPrompt hashes req.Prompt and runs the shared hook
// evaluation path (hooks.go's evaluateSubmittedPrompt — see the file doc
// comment). The error posture is the CALLER's choice by construction:
// this function itself neither fails open nor closed, it returns the
// pipeline error verbatim alongside the always-valid TurnID/Persisted so
// `auspex run` can apply the managed-mode posture (fail open to a
// decisionless spawn, per internal/managed.Runner's own doc) while a
// future caller with different stakes could fail closed — the same split
// HandleUserPromptSubmit vs. EvaluatePrompt already embodies over the
// same shared core.
func EvaluateManagedPrompt(ctx context.Context, deps HookDeps, req ManagedPromptRequest) (ManagedPromptResult, error) {
	if req.SessionID == "" {
		return ManagedPromptResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "orchestrator: EvaluateManagedPrompt requires a non-empty SessionID",
			Retryable: false,
		}
	}

	provider := req.Provider
	if provider == "" {
		provider = claudetelemetry.Provider
	}

	// The event construction (hash-immediately) is claude's helper for
	// EVERY provider: NewUserPromptSubmitEvent only derives the
	// provider-independent hash/size/feature trio from the prompt text —
	// there is nothing claude-specific to leak, and the honest provider
	// id is stamped downstream (see evaluateSubmittedPrompt's doc).
	parsed := claudehooks.NewUserPromptSubmitEvent(req.SessionID, req.Prompt)
	parsed.CWD = req.CWD

	pe, err := evaluateSubmittedPrompt(ctx, deps, parsed, provider)
	result := ManagedPromptResult{TurnID: pe.turnID, Persisted: pe.persisted}
	if err != nil {
		return result, err
	}
	result.Evaluation = pe.evaluation
	result.Decision = pe.decision

	if deps.Forecast != nil {
		if card, cardErr := deps.Forecast.ForecastCard(ctx, pe.evaluation.ID); cardErr == nil {
			result.Card = &card
		}
	}
	return result, nil
}
