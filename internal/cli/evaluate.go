// evaluate.go: the REAL `auspex evaluate` command (issue #14
// deliverable 5) — the offline half of the per-prompt forecast surface.
// Shape: `auspex evaluate --session-id <id> [--prompt-file <path>|-]
// [--json]`. It runs a genuine evaluation through the same production
// path the UserPromptSubmit hook uses (internal/orchestrator's
// EvaluatePrompt -> evaluateSubmittedPrompt -> the real
// app.EvaluationService; shared code, not a duplicate pipeline) and
// prints the resulting forecast card — human-readable by default,
// schema-versioned JSON ("auspex.evaluate.v1", following `progress
// complete`'s "auspex.progress-complete.v1" precedent) with --json.
//
// Privacy (Constitution §7 rule 2): the prompt text read from
// --prompt-file/stdin is handed to orchestrator.EvaluatePrompt, which
// hashes it immediately (claudehooks.NewUserPromptSubmitEvent) — it is
// never logged, never echoed into any output or error, and never
// persisted; only the hash and size-derived features survive.
// internal/integrationtest's evaluate privacy test proves this against
// the real on-disk DB bytes, leakage-scanner style.
package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// NewEvaluateCmd builds the REAL `auspex evaluate` command, wired
// against deps (the same orchestrator.HookDeps the hook subtree uses —
// deliberately, so both surfaces share one evaluation path and one
// forecast-card source). This is the constructor
// internal/app/wiring.App.RootCmd() swaps in for root.go's `evaluate`
// stub, the same stub-then-swap pattern `hook`/`progress`/`checkpoint`
// already follow. Exported for the same reason as NewHookClaudeCmd:
// internal/app/wiring is a different package that needs to call it.
func NewEvaluateCmd(deps orchestrator.HookDeps) *cobra.Command {
	var sessionID, promptFile string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "evaluate",
		Short: "Evaluate the current turn and produce a risk decision",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "evaluate: --session-id is required", Retryable: false}
			}
			prompt, err := readPromptFile(cmd, promptFile)
			if err != nil {
				return err
			}

			result, err := orchestrator.EvaluatePrompt(cmd.Context(), deps, orchestrator.EvaluatePromptRequest{
				SessionID: domain.SessionID(sessionID),
				Prompt:    prompt,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				body, err := marshalOrError("evaluate", buildEvaluateOutput(result))
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}

			// Human mode: the same compact card block the hook injects as
			// additionalContext (one presenter, two surfaces — the numbers
			// a user sees offline are byte-identical to what the agent
			// sees in-session), plus one trailing line naming the IDs a
			// follow-up `decision allow/deny` needs. When the card itself
			// was unavailable (no ForecastCardSource wired, or the
			// read-back failed — EvaluatePromptResult.Card's documented
			// degrade), the evaluation still succeeded and is reported
			// honestly without fabricated numbers.
			if result.Card != nil {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.Card.AdditionalContext()); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Auspex forecast (uncalibrated estimate; forecast card unavailable): policy %s\n", result.Decision.Action); err != nil {
					return err
				}
			}
			_, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "evaluation %s (turn %s)\n", result.Evaluation.ID, result.Evaluation.TurnID)
			return writeErr
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to evaluate the next turn for")
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "File containing the prompt text to evaluate ('-' reads stdin); the text is hashed immediately and never persisted or logged")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON output")
	return cmd
}

// readPromptFile resolves the --prompt-file flag: empty means no prompt
// (the evaluation runs on the empty prompt's hash — the same thing a
// hook payload with no prompt field yields), "-" reads the command's
// stdin, anything else is a file path. A read failure is a validation
// error naming only the PATH — never any file content.
func readPromptFile(cmd *cobra.Command, promptFile string) (string, error) {
	switch promptFile {
	case "":
		return "", nil
	case "-":
		b, err := readAllStdin(cmd)
		if err != nil {
			return "", &domain.Error{
				Code:      domain.ErrCodeValidation,
				Message:   "evaluate: reading prompt from stdin failed",
				Retryable: false,
			}
		}
		return string(b), nil
	default:
		b, err := os.ReadFile(promptFile)
		if err != nil {
			return "", &domain.Error{
				Code:      domain.ErrCodeValidation,
				Message:   "evaluate: cannot read --prompt-file",
				Retryable: false,
				Details:   map[string]string{"path": promptFile},
			}
		}
		return string(b), nil
	}
}

// evaluateOutput is `auspex evaluate --json`'s schema-versioned wire
// shape ("auspex.evaluate.v1"). Nullable-by-design fields use
// pointers so an unknown quantile serializes as JSON null, never 0 (ADD
// principle 1). Probability has NO omitempty deliberately: Constitution
// principle #2 requires cold-start/uncalibrated output to emit
// `"probability": null` explicitly — the absence of a probability is
// load-bearing information, not an omittable default.
type evaluateOutput struct {
	SchemaVersion string                  `json:"schema_version"`
	EvaluationID  string                  `json:"evaluation_id"`
	TurnID        string                  `json:"turn_id"`
	PolicyAction  string                  `json:"policy_action"`
	Label         string                  `json:"label"`
	Calibrated    bool                    `json:"calibrated"`
	Probability   *float64                `json:"probability"`
	Confidence    string                  `json:"confidence"`
	ReasonCodes   []string                `json:"reason_codes"`
	Scope         *evaluateScopeOutput    `json:"scope,omitempty"`
	Tokens        *evaluateTokensOutput   `json:"tokens,omitempty"`
	Duration      *evaluateDurationOutput `json:"duration,omitempty"`
	Cost          *evaluateCostOutput     `json:"cost,omitempty"`
	Context       *evaluateContextOutput  `json:"context,omitempty"`
	Risk          *evaluateRiskOutput     `json:"risk,omitempty"`
	CardAvailable bool                    `json:"card_available"`
}

type evaluateScopeOutput struct {
	FilesReadP50    *int64 `json:"files_read_p50"`
	FilesReadP90    *int64 `json:"files_read_p90"`
	FilesChangedP50 *int64 `json:"files_changed_p50"`
	FilesChangedP90 *int64 `json:"files_changed_p90"`
	LinesChangedP50 *int64 `json:"lines_changed_p50"`
	LinesChangedP90 *int64 `json:"lines_changed_p90"`
}

type evaluateTokensOutput struct {
	P50 *int64 `json:"p50"`
	P80 *int64 `json:"p80"`
	P90 *int64 `json:"p90"`
}

// evaluateDurationOutput is the #62 Phase-1 wall-clock forecast. Exposed in
// seconds to match the repo's other duration fields (runway_forecasts'
// *_seconds columns). Both bounds have NO omitempty for the same reason
// probability doesn't: an unknown estimate serializes as an explicit null,
// never 0 or an absent key (ADD principle 1). It is a cold-start,
// uncalibrated estimate — labeled as such, and NOT surfaced on the
// statusline until it responds to the prompt (#42) or is calibrated (#11).
type evaluateDurationOutput struct {
	P50Seconds *int64 `json:"p50_seconds"`
	P90Seconds *int64 `json:"p90_seconds"`
	Calibrated bool   `json:"calibrated"`
}

type evaluateCostOutput struct {
	LowUSD      float64 `json:"low_usd"`
	HighUSD     float64 `json:"high_usd"`
	ModelFamily string  `json:"model_family"`
	Source      string  `json:"source"`
	Estimate    bool    `json:"estimate"`
}

type evaluateRiskOutput struct {
	OverallScore float64 `json:"overall_score"`
}

// evaluateContextOutput is the ADR-043 increment-2 context-window block
// (DECISION_LOG.md D-08). projected_p90_used_percent has NO omitempty for
// the same reason probability doesn't: an unknown projection serializes
// as an explicit null, never 0 or an absent key (ADD principle 1). The
// two threshold booleans mirror the card's read-back-from-the-decision
// state (evaluation.ForecastCard.Context*ThresholdExceeded): true only
// when the policy engine actually recorded the D-08 threshold as crossed
// for this decision — a cold-start/low-confidence projection above 85%
// reports its percentage with both flags false, exactly the "thresholds
// active but confidence-gated" semantics D-08 fixed.
type evaluateContextOutput struct {
	ProjectedP90UsedPercent     *float64 `json:"projected_p90_used_percent"`
	WarnThresholdExceeded       bool     `json:"warn_threshold_exceeded"`
	CheckpointThresholdExceeded bool     `json:"checkpoint_threshold_exceeded"`
}

// evaluateUncalibratedLabel is the fixed labeling `--json` output carries
// while calibrated is false (Constitution principle #2: an uncalibrated
// score/range is never presented as a probability).
const evaluateUncalibratedLabel = "uncalibrated estimate"

func buildEvaluateOutput(result orchestrator.EvaluatePromptResult) evaluateOutput {
	out := evaluateOutput{
		SchemaVersion: "auspex.evaluate.v1",
		EvaluationID:  string(result.Evaluation.ID),
		TurnID:        string(result.Evaluation.TurnID),
		PolicyAction:  string(result.Decision.Action),
		Label:         evaluateUncalibratedLabel,
		Calibrated:    result.Evaluation.Calibrated,
		Probability:   nil, // null until a calibration wave persists one — Constitution principle #2
		Confidence:    string(result.Evaluation.Confidence),
		ReasonCodes:   reasonCodeStrings(result.Evaluation.ReasonCodes),
		CardAvailable: result.Card != nil,
	}
	if result.Evaluation.Calibrated {
		out.Label = "calibrated"
	}

	card := result.Card
	if card == nil {
		return out
	}
	out.Probability = card.Probability
	out.Scope = &evaluateScopeOutput{
		FilesReadP50:    card.FilesReadP50,
		FilesReadP90:    card.FilesReadP90,
		FilesChangedP50: card.FilesChangedP50,
		FilesChangedP90: card.FilesChangedP90,
		LinesChangedP50: card.LinesChangedP50,
		LinesChangedP90: card.LinesChangedP90,
	}
	out.Tokens = &evaluateTokensOutput{P50: card.TokensP50, P80: card.TokensP80, P90: card.TokensP90}
	out.Duration = &evaluateDurationOutput{
		P50Seconds: nanosToSeconds(card.DurationP50),
		P90Seconds: nanosToSeconds(card.DurationP90),
		Calibrated: card.Calibrated,
	}
	out.Context = &evaluateContextOutput{
		ProjectedP90UsedPercent:     card.ContextProjectedP90,
		WarnThresholdExceeded:       card.ContextWarnThresholdExceeded,
		CheckpointThresholdExceeded: card.ContextCheckpointThresholdExceeded,
	}
	if card.Cost != nil {
		out.Cost = &evaluateCostOutput{
			LowUSD:      card.Cost.LowUSD,
			HighUSD:     card.Cost.HighUSD,
			ModelFamily: card.Cost.ModelFamily,
			Source:      card.Cost.Source,
			Estimate:    true,
		}
	}
	out.Risk = &evaluateRiskOutput{OverallScore: card.OverallRiskScore}
	return out
}

// nanosToSeconds converts a nanosecond duration pointer to whole seconds,
// preserving nil (unknown is not zero — the #62 duration forecast is null
// when the estimator produced no value).
func nanosToSeconds(ns *int64) *int64 {
	if ns == nil {
		return nil
	}
	s := *ns / int64(time.Second)
	return &s
}

func reasonCodeStrings(codes []domain.ReasonCode) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = string(c)
	}
	return out
}
