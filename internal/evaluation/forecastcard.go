// forecastcard.go: the issue-#14 per-prompt forecast surface's data +
// presenter layer, owned by this package because this package owns the
// persisted data it renders (predictions/policy_decisions, migrations
// 0041/0043). ForecastCard and the Service methods producing it are
// exported PACKAGE API, deliberately NOT part of the frozen
// app.EvaluationService contract (internal/app/ports.go is frozen; ADR-043
// requires contract impact to stay additive) — consumers that want a card
// depend on the concrete *evaluation.Service (or a caller-local narrow
// interface over these two methods, e.g. internal/orchestrator's
// ForecastCardSource), exactly the same only-the-real-service pattern
// IssueAuthorization already established for `decision allow`.
//
// # Read-back, not recompute
//
// The card is built by reading back the prediction + policy-decision rows
// EvaluateTurn already persisted (FR-172), not by re-running the pipeline
// or holding pipeline outputs in memory across calls. This is the honest
// minimal path issue #14 asks for: EvaluateTurn's frozen return DTO
// (app.Evaluation) has no room for scope/token/risk/action fields and must
// not be widened, the rows are written atomically in the same transaction
// as the evaluation itself, and a read-back presenter works identically
// for the three surfaces that need it — the UserPromptSubmit hook (card
// for the evaluation it just ran), the statusline (latest card for a
// session), and `auspex evaluate`.
//
// # Constitution principle #2 (uncalibrated score is never a probability)
//
// Every rendered surface labels the card "uncalibrated estimate" while
// Calibrated is false, and ForecastCard.Probability is nil (JSON null)
// unless a calibrated probability actually exists — which, this wave, it
// never does: neither migration 0041 nor 0043 persists one, so the field
// is structurally always nil until a calibration wave lands and persists
// it. That is the "cold-start emits probability: null" rule made
// load-bearing in the type, not just in prose.
package evaluation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// ForecastCard is the presenter DTO for one persisted evaluation: scope
// bands, token quantiles, estimated cost range, risk, and the policy
// action, plus the calibration labeling every surface must carry. All
// quantile fields are pointer-typed for the same reason the underlying
// prediction columns are nullable (ADD principle 1, "unknown is not
// zero"): a cold-start evaluation legitimately persists NULL scope/token
// quantiles, and the card renders those as unknown, never as 0.
type ForecastCard struct {
	EvaluationID domain.EvaluationID
	TurnID       domain.TurnID
	CreatedAt    time.Time

	// Scope bands (migration 0041 persists P50/P90 only; P80 scope
	// quantiles are not stored, so the card does not carry them).
	FilesReadP50    *int64
	FilesReadP90    *int64
	FilesChangedP50 *int64
	FilesChangedP90 *int64
	LinesChangedP50 *int64
	LinesChangedP90 *int64

	// Token forecast quantiles.
	TokensP50 *int64
	TokensP80 *int64
	TokensP90 *int64

	// Cost is the ADR-043 increment-1 estimate: token quantiles × price
	// table → a range, never a point. nil when no token forecast exists
	// (no forecast means no cost estimate, never a fabricated $0).
	Cost *pricing.CostRange

	// Risk. OverallRiskScore is a 0-1 score — per Constitution principle
	// #2 it MUST NOT be presented as a probability while Calibrated is
	// false, and every presenter method below labels it accordingly.
	OverallRiskScore float64
	ReasonCodes      []domain.ReasonCode
	Confidence       domain.Confidence
	Calibrated       bool

	// Probability is nil (JSON null) unless a calibrated probability was
	// actually produced and persisted — which no migration does this
	// wave, so it is structurally always nil today. See the file doc
	// comment's Constitution-principle-#2 section.
	Probability *float64

	// PolicyAction is the frozen policy action the Decider persisted for
	// this evaluation (policy_decisions.action, migration 0043).
	PolicyAction app.PolicyAction
}

// pricingTable returns the Service's configured price table, defaulting
// to pricing.DefaultTable(). Pricing is an optional field (unlike New's
// required dependencies) because a nil table has an obvious, correct
// default and every existing constructor call site keeps compiling.
func (s *Service) pricingTable() *pricing.Table {
	if s.Pricing != nil {
		return s.Pricing
	}
	return pricing.DefaultTable()
}

// ForecastCard reads back the persisted prediction + policy-decision rows
// for evaluation id and assembles the presenter card, computing the cost
// range from the persisted token quantiles via the pricing table
// (ADR-043). Returns ErrCodeNotFound (via getPrediction/
// getPolicyDecisionByPredictionID) when no such evaluation exists.
//
// The model identity is unknown at this layer — prediction rows persist
// no model column (migration 0041) — so the cost estimate always resolves
// to the pricing table's labeled DefaultFamily fallback; the CostRange
// carries that label so presenters can say which price assumption
// produced the number.
func (s *Service) ForecastCard(ctx context.Context, id domain.EvaluationID) (ForecastCard, error) {
	if id == "" {
		return ForecastCard{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: ForecastCard requires a non-empty EvaluationID",
			Retryable: false,
		}
	}

	row, err := getPrediction(ctx, s.DB, id)
	if err != nil {
		return ForecastCard{}, err
	}
	decisionRow, err := getPolicyDecisionByPredictionID(ctx, s.DB, id)
	if err != nil {
		return ForecastCard{}, err
	}
	reasons, err := unmarshalReasonCodes(row.ReasonCodesJSON)
	if err != nil {
		return ForecastCard{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return ForecastCard{}, &domain.Error{
			Code:      domain.ErrCodeIntegrity,
			Message:   "evaluation: predictions.created_at is not a valid timestamp",
			Retryable: false,
			Details:   map[string]string{"evaluation_id": string(id)},
		}
	}

	card := ForecastCard{
		EvaluationID:     row.ID,
		TurnID:           row.TurnID,
		CreatedAt:        createdAt,
		FilesReadP50:     row.FilesReadP50,
		FilesReadP90:     row.FilesReadP90,
		FilesChangedP50:  row.FilesChangedP50,
		FilesChangedP90:  row.FilesChangedP90,
		LinesChangedP50:  row.LinesChangedP50,
		LinesChangedP90:  row.LinesChangedP90,
		TokensP50:        row.TokenP50,
		TokensP80:        row.TokenP80,
		TokensP90:        row.TokenP90,
		OverallRiskScore: row.OverallRiskScore,
		ReasonCodes:      reasons,
		Confidence:       row.Confidence,
		Calibrated:       row.Calibrated,
		Probability:      nil, // always nil this wave — see the file doc comment (Constitution principle #2)
		PolicyAction:     app.PolicyAction(decisionRow.Action),
	}

	if card.TokensP50 != nil && card.TokensP90 != nil {
		if cr, ok := s.pricingTable().EstimateTurnCost("", *card.TokensP50, *card.TokensP90); ok {
			card.Cost = &cr
		}
	}
	return card, nil
}

// LatestForecastCard returns the most recent evaluation's card for a
// session, or ok=false when the session has no linkable evaluation yet —
// cold start, not an error, matching this package's DataSource
// discipline. This is the statusline --emit-line lookup (issue #14
// deliverable 4 / issue #12 friction #2).
//
// Session linkage: predictions are turn-scoped (migration 0041 carries no
// session column, by design), so this joins through the events table's
// provider.turn.started rows, whose turn_id the hook handler stamps at
// evaluation time (internal/orchestrator/hooks.go mints one TurnID and
// uses it for both the persisted event and EvaluateTurn). A session whose
// events were never persisted (no Persister wired) has no linkage and
// honestly returns ok=false.
func (s *Service) LatestForecastCard(ctx context.Context, sessionID domain.SessionID) (ForecastCard, bool, error) {
	if sessionID == "" {
		return ForecastCard{}, false, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: LatestForecastCard requires a non-empty SessionID",
			Retryable: false,
		}
	}

	q := sqlite.QuerierFromContext(ctx, s.DB)
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT p.id FROM predictions p
		JOIN events e ON e.turn_id = p.turn_id
		WHERE e.session_id = ? AND e.event_type = 'provider.turn.started'
		ORDER BY p.created_at DESC, p.rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ForecastCard{}, false, nil
	}
	if err != nil {
		return ForecastCard{}, false, fmt.Errorf("evaluation: LatestForecastCard: query predictions for session %s: %w", sessionID, err)
	}

	card, err := s.ForecastCard(ctx, domain.EvaluationID(id))
	if err != nil {
		return ForecastCard{}, false, err
	}
	return card, true, nil
}

// --- presenters --------------------------------------------------------

// calibrationLabel is the explicit uncalibrated-estimate wording every
// rendered card carries while Calibrated is false (Constitution principle
// #2: an uncalibrated score is never presented as a probability).
const calibrationLabel = "uncalibrated estimate — scores are not probabilities"

// maxContextReasonCodes caps how many reason codes AdditionalContext
// renders: the block is injected into a coding agent's context on every
// prompt, so it stays compact (issue #14: "~6 lines max") and cites only
// the top few explanations rather than the full merged set.
const maxContextReasonCodes = 3

// AdditionalContext renders the compact human/agent-readable block the
// UserPromptSubmit hook injects as Claude Code additionalContext
// (hookSpecificOutput.additionalContext) — the surface where the coding
// agent literally sees the forecast before acting (issue #14 deliverable
// 3). Always at most 6 lines; missing data renders as an explicit
// "unknown (cold start)", never as zero (ADD principle 1).
func (c ForecastCard) AdditionalContext() string {
	lines := []string{
		fmt.Sprintf("Auspex forecast (%s; evaluation %s):", c.labelText(), c.EvaluationID),
		"  scope: " + c.scopeText(),
		"  tokens: " + c.tokensText(),
		"  cost: " + c.costText(),
		fmt.Sprintf("  risk: %.2f/1.00 overall%s", c.OverallRiskScore, c.topReasonsText()),
		"  policy: " + c.policyText(),
	}
	return strings.Join(lines, "\n")
}

// StatusLineText renders the one-line statusline display (issue #14
// deliverable 4; resolves issue #12 friction #2's ingest-only gap):
//
//	pf✈ <model> | est P50 <tokens>tok ~$<low>–<high> | <policy action>
//
// model may be empty (renders as bare "pf✈") and card may be nil (no
// persisted evaluation for the session yet — renders model only), so the
// status bar always has something to show; "est"/"~" mark every number as
// an estimate, and the full uncalibrated labeling lives on the card
// surfaces where there is room for it (AdditionalContext, `auspex
// evaluate`). Exported as a package function rather than a method so the
// nil-card fallback is one code path, not caller-side duplication.
func StatusLineText(model string, card *ForecastCard) string {
	head := "pf✈"
	if model != "" {
		head += " " + model
	}
	parts := []string{head}
	if card != nil {
		if card.TokensP50 != nil {
			seg := fmt.Sprintf("est P50 %dtok", *card.TokensP50)
			if card.Cost != nil {
				seg += fmt.Sprintf(" ~$%.2f–%.2f", card.Cost.LowUSD, card.Cost.HighUSD)
			}
			parts = append(parts, seg)
		}
		if card.PolicyAction != "" {
			parts = append(parts, string(card.PolicyAction))
		}
	}
	return strings.Join(parts, " | ")
}

func (c ForecastCard) labelText() string {
	if c.Calibrated {
		// No calibrated pipeline exists this wave; kept so the label can
		// never silently misreport if a calibration wave lands.
		return "calibrated"
	}
	return calibrationLabel
}

// scopeText prefers the changed-files/lines bands (the numbers a user
// acts on); a fully-unknown scope names the cold start explicitly.
func (c ForecastCard) scopeText() string {
	var segs []string
	if c.FilesChangedP50 != nil && c.FilesChangedP90 != nil {
		segs = append(segs, fmt.Sprintf("~%d–%d files changed", *c.FilesChangedP50, *c.FilesChangedP90))
	}
	if c.LinesChangedP50 != nil && c.LinesChangedP90 != nil {
		segs = append(segs, fmt.Sprintf("~%d–%d lines", *c.LinesChangedP50, *c.LinesChangedP90))
	}
	if c.FilesReadP50 != nil && c.FilesReadP90 != nil {
		segs = append(segs, fmt.Sprintf("~%d–%d files read", *c.FilesReadP50, *c.FilesReadP90))
	}
	if len(segs) == 0 {
		return "unknown (cold start)"
	}
	return strings.Join(segs, ", ") + " (P50–P90)"
}

func (c ForecastCard) tokensText() string {
	if c.TokensP50 == nil && c.TokensP80 == nil && c.TokensP90 == nil {
		return "unknown (cold start)"
	}
	var segs []string
	if c.TokensP50 != nil {
		segs = append(segs, fmt.Sprintf("P50 %d", *c.TokensP50))
	}
	if c.TokensP80 != nil {
		segs = append(segs, fmt.Sprintf("P80 %d", *c.TokensP80))
	}
	if c.TokensP90 != nil {
		segs = append(segs, fmt.Sprintf("P90 %d", *c.TokensP90))
	}
	return strings.Join(segs, " / ")
}

func (c ForecastCard) costText() string {
	if c.Cost == nil {
		return "unavailable (no token forecast)"
	}
	return fmt.Sprintf("~$%.2f–$%.2f USD (%s pricing, %s; estimate)",
		c.Cost.LowUSD, c.Cost.HighUSD, c.Cost.ModelFamily, c.Cost.Source)
}

func (c ForecastCard) topReasonsText() string {
	if len(c.ReasonCodes) == 0 {
		return ""
	}
	codes := c.ReasonCodes
	if len(codes) > maxContextReasonCodes {
		codes = codes[:maxContextReasonCodes]
	}
	strs := make([]string, len(codes))
	for i, code := range codes {
		strs[i] = string(code)
	}
	return " — " + strings.Join(strs, ", ")
}

func (c ForecastCard) policyText() string {
	if c.PolicyAction == "" {
		return "unknown"
	}
	return string(c.PolicyAction)
}
