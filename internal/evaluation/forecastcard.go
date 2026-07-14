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
	"math"
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

	// ContextProjectedP90 is the ADR-043 increment-2 context projection
	// (predictions.projected_context_used_p90, migration 0045): the
	// Stage-3 forecast's projected P90 context-window utilization in
	// percent (0-100). nil means the forecaster had no usable context
	// observation — rendered as an explicit unknown, never 0 (ADD
	// principle 1). Like every other number on this card, it is an
	// estimate and inherits the card's uncalibrated labeling.
	ContextProjectedP90 *float64

	// ContextWarnThresholdExceeded / ContextCheckpointThresholdExceeded
	// are the persisted D-08 threshold state (DECISION_LOG.md D-08,
	// ADR-043 increment 2), read back from the policy decision's own
	// reason codes (policy_decisions.reason_codes_json carrying
	// CONTEXT_WARN_THRESHOLD_EXCEEDED /
	// CONTEXT_CHECKPOINT_THRESHOLD_EXCEEDED) — NOT recomputed by
	// comparing ContextProjectedP90 against threshold constants at
	// render time. Read-back keeps the card honest: the policy engine
	// only emits these codes when the projection also met its
	// confidence bar and the rule was enabled, so a cold-start
	// projection that happens to sit above 85% renders its percentage
	// WITHOUT a threshold claim, exactly matching the decision that was
	// actually made.
	ContextWarnThresholdExceeded       bool
	ContextCheckpointThresholdExceeded bool

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
// The cost estimate resolves against the model stamped on the prediction
// row (migration 0046, #20 Phase 0) — a turn evaluated while the session's
// identity was known prices at that model's family, and the CostRange
// carries the resolved family label so presenters say which price
// assumption produced the number. A row stamped before the identity was
// ever observed (NULL model_id) keeps the labeled DefaultFamily fallback.
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
		EvaluationID:        row.ID,
		TurnID:              row.TurnID,
		CreatedAt:           createdAt,
		FilesReadP50:        row.FilesReadP50,
		FilesReadP90:        row.FilesReadP90,
		FilesChangedP50:     row.FilesChangedP50,
		FilesChangedP90:     row.FilesChangedP90,
		LinesChangedP50:     row.LinesChangedP50,
		LinesChangedP90:     row.LinesChangedP90,
		TokensP50:           row.TokenP50,
		TokensP80:           row.TokenP80,
		TokensP90:           row.TokenP90,
		ContextProjectedP90: row.ProjectedContextUsedP90,
		OverallRiskScore:    row.OverallRiskScore,
		ReasonCodes:         reasons,
		Confidence:          row.Confidence,
		Calibrated:          row.Calibrated,
		Probability:         nil, // always nil this wave — see the file doc comment (Constitution principle #2)
		PolicyAction:        app.PolicyAction(decisionRow.Action),
	}

	// D-08 threshold state: read back from the persisted policy
	// decision's reason codes (see the field doc comment for why this is
	// never recomputed at render time).
	decisionReasons, err := unmarshalReasonCodes(decisionRow.ReasonCodesJSON)
	if err != nil {
		return ForecastCard{}, err
	}
	for _, rc := range decisionReasons {
		switch rc {
		case domain.ReasonContextWarnThresholdExceeded:
			card.ContextWarnThresholdExceeded = true
		case domain.ReasonContextCheckpointThresholdExceeded:
			card.ContextCheckpointThresholdExceeded = true
		}
	}

	if card.TokensP50 != nil && card.TokensP90 != nil {
		model := ""
		if row.ModelID != nil {
			model = *row.ModelID
		}
		if cr, ok := s.pricingTable().EstimateTurnCost(model, *card.TokensP50, *card.TokensP90); ok {
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
// 3). Always at most 7 lines (issue #14's "~6 lines max" budget, plus
// ADR-043 increment 2's context line — the context window is the one
// resource whose exhaustion mid-turn is catastrophic, so its projection
// earns a line of its own); missing data renders as an explicit
// "unknown (cold start)", never as zero (ADD principle 1).
func (c ForecastCard) AdditionalContext() string {
	lines := []string{
		fmt.Sprintf("Auspex forecast (%s; evaluation %s):", c.labelText(), c.EvaluationID),
		"  scope: " + c.scopeText(),
		"  tokens: " + c.tokensText(),
		"  cost: " + c.costText(),
		"  context: " + c.contextText(),
		fmt.Sprintf("  risk: %.2f/1.00 overall%s", c.OverallRiskScore, c.topReasonsText()),
		"  policy: " + c.policyText(),
	}
	return strings.Join(lines, "\n")
}

// ANSI escape codes used by StatusLineText — and by nothing else in this
// package: the statusline is the only surface Claude Code renders in the
// terminal (statusline.md documents ANSI color support), while the
// forecast card goes into the model's additionalContext and MUST stay
// plain text. We stick to the 16-color set on a single line because the
// same docs warn that complex escape sequences can garble the status bar.
const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

// StatusLineInput carries the per-render inputs StatusLineText composes.
// Model, ContextUsedPercent, and WeeklyLimitUsedPercent come from the
// LIVE statusline snapshot (measurements); Card carries the persisted
// per-turn forecast (predictions). Keeping them separate on the input is
// what lets the context segment show "measured → projected" honestly.
type StatusLineInput struct {
	Model string
	Card  *ForecastCard
	// ContextUsedPercent is the live measured context usage — prefer the
	// exact token ratio over the provider's whole-percent rounding
	// (D-14). nil means unknown, never zero.
	ContextUsedPercent *float64
	// WeeklyLimitUsedPercent is the live seven-day quota window (#27).
	WeeklyLimitUsedPercent *float64
}

// StatusLineText renders the one-line statusline display (issue #14
// deliverable 4; resolves issue #12 friction #2's ingest-only gap; icons
// and semantic colors are issue #29; the plain-language format is the
// owner's D-13 decision, issue #31; measured→projected split is D-14):
//
//	ax✈ <model> │ 🔮 probably (50%) < <n> tokens │ context [<bar>] <cur>% → worst-case ~<pct>% │ ◷ weekly limit ~<pct>% │ <policy scale>
//
// model may be empty (renders as bare "ax✈") and card may be nil (no
// persisted evaluation for the session yet), so the status bar always has
// something to show. "probably (50%)" is plain-language phrasing of the
// P50 quantile: the percentage is rendered DATA, not a fixed label, so
// when calibration (#11) can stand behind sharper quantiles the number
// tightens without a format change — and the parenthetical keeps the
// quantile visible while the full "uncalibrated estimate" labeling stays
// on the card surfaces (Constitution #2; D-13 records the tension). The
// cost range was dropped from the line by the same decision (still on the
// card / `auspex evaluate`). The context segment (ADR-043 increment 2)
// appears only when a projection was persisted — unknown contributes
// nothing rather than a fabricated 0% — and carries the D-08 threshold
// state ("(warn)"/"(checkpoint)"): textual suffixes stay alongside the
// yellow/red coloring so the signal survives grep and colorblindness
// alike. weeklyLimitUsedPercent is the LIVE seven-day quota window from
// the current snapshot (real data since #27), independent of the card so
// it renders even on forecast-cold sessions; it stays uncolored until
// #21's binding-constraint policy gives it honest thresholds. No
// animation by design — the command re-runs only per assistant message
// (300ms debounce, quiet when idle), so time-based frames would stutter
// rather than animate. Exported as a package function rather than a
// method so the nil-card fallback is one code path, not caller-side
// duplication.
func StatusLineText(in StatusLineInput) string {
	head := ansiCyan + "ax✈" + ansiReset
	if in.Model != "" {
		head += " " + in.Model
	}
	parts := []string{head}
	if in.Card != nil {
		if in.Card.TokensP50 != nil {
			parts = append(parts, fmt.Sprintf("🔮 probably (50%%) < %d tokens", *in.Card.TokensP50))
		}
		if in.Card.ContextProjectedP90 != nil {
			pct := *in.Card.ContextProjectedP90
			var seg string
			if in.ContextUsedPercent != nil {
				// Measurement first (one decimal — it is exact), then
				// the projection with its estimate marker: the reader
				// sees exactly how much of the number is prediction.
				seg = fmt.Sprintf("context %s %.1f%% → worst-case ~%.0f%%", contextBar(pct), *in.ContextUsedPercent, pct)
			} else {
				seg = fmt.Sprintf("context worst-case %s ~%.0f%%", contextBar(pct), pct)
			}
			switch {
			case in.Card.ContextCheckpointThresholdExceeded:
				seg = ansiRed + seg + " (checkpoint)" + ansiReset
			case in.Card.ContextWarnThresholdExceeded:
				seg = ansiYellow + seg + " (warn)" + ansiReset
			default:
				seg = ansiGreen + seg + ansiReset
			}
			parts = append(parts, seg)
		}
	}
	if in.WeeklyLimitUsedPercent != nil {
		parts = append(parts, fmt.Sprintf("◷ weekly limit ~%.0f%%", *in.WeeklyLimitUsedPercent))
	}
	if in.Card != nil && in.Card.PolicyAction != "" {
		parts = append(parts, policyScale(in.Card.PolicyAction))
	}
	return strings.Join(parts, ansiDim+" │ "+ansiReset)
}

// contextBar renders the projected worst-case usage as a 20-cell bar
// (one cell per 5%), e.g. [██████··············] — D-13 v2.1: the bar
// makes the runway legible without parsing the number.
func contextBar(pct float64) string {
	const cells = 20
	filled := int(math.Round(pct / 100 * cells))
	if filled < 0 {
		filled = 0
	}
	if filled > cells {
		filled = cells
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("·", cells-filled) + "]"
}

// policyScale renders ALL four policy actions in severity order with the
// active one lit (icon + color) and the rest dimmed — a first-time user
// sees where the current decision sits on the scale, not just a word
// (D-13 v2.1). An action outside the known scale renders alone as its
// raw string — never dropped, never mislabeled as part of the scale.
func policyScale(active app.PolicyAction) string {
	scale := []struct {
		action app.PolicyAction
		icon   string
		color  string
	}{
		{app.PolicyRun, "✓", ansiGreen},
		{app.PolicyWarn, "⚠", ansiYellow},
		{app.PolicyCheckpointAndRun, "⚑", ansiMagenta},
		{app.PolicyBlock, "✖", ansiRed},
	}
	known := false
	for _, s := range scale {
		if s.action == active {
			known = true
			break
		}
	}
	if !known {
		return string(active)
	}
	parts := make([]string, 0, len(scale))
	for _, s := range scale {
		if s.action == active {
			parts = append(parts, s.color+s.icon+" "+string(s.action)+ansiReset)
		} else {
			parts = append(parts, ansiDim+string(s.action)+ansiReset)
		}
	}
	return strings.Join(parts, "  ")
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

// contextText renders the ADR-043 increment-2 context-window line: the
// projected P90 utilization percentage plus, when the policy engine
// actually recorded one (see the ContextWarnThresholdExceeded field doc),
// the D-08 threshold state — e.g. "P90 ~91% of window (projected) — WARN
// threshold exceeded". A projection with no threshold marker means the
// thresholds did not fire for this decision (below both, gated by
// cold-start/low confidence, or disabled) — the "~" and the card-level
// uncalibrated label keep the number an estimate, never a measurement or
// a probability (Constitution principle #2).
func (c ForecastCard) contextText() string {
	if c.ContextProjectedP90 == nil {
		return "unknown (cold start)"
	}
	text := fmt.Sprintf("P90 ~%.0f%% of window (projected)", *c.ContextProjectedP90)
	switch {
	case c.ContextCheckpointThresholdExceeded:
		text += " — CHECKPOINT threshold exceeded"
	case c.ContextWarnThresholdExceeded:
		text += " — WARN threshold exceeded"
	}
	return text
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
