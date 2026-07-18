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

	// Input/output token decomposition (#65 Phase 1, ADR-0053; migration
	// 0063): the split of the total token forecast into distinct input and
	// output P50-P90 ranges, the INPUT range structurally WIDER than the
	// output range (models predict input tokens worse — Bai et al. 2026
	// direction only). nil means the persisted prediction carried no split
	// (cold-start forecaster that did not populate it, or a pre-#65 row) —
	// rendered as an explicit absence, never 0. Uncalibrated like every
	// other number on this card; the widening is a structural default, not
	// a fitted coefficient.
	InputTokensP50  *int64
	InputTokensP90  *int64
	OutputTokensP50 *int64
	OutputTokensP90 *int64

	// DurationP50/P90 are the #62 Phase-1 wall-clock duration forecast in
	// nanoseconds (predictions.duration_p50/p90, migration 0047). nil means
	// the scope estimator left duration unknown — rendered as an explicit
	// unknown, never 0. Cold-start and uncalibrated like every other number
	// here; deliberately surfaced on this card and in `evaluate`, NOT on the
	// statusline until it responds to the prompt (#42) or is calibrated
	// (#11) — see the #62 statusline gate.
	DurationP50 *int64
	DurationP90 *int64

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
		InputTokensP50:      row.TokenInputP50,
		InputTokensP90:      row.TokenInputP90,
		OutputTokensP50:     row.TokenOutputP50,
		OutputTokensP90:     row.TokenOutputP90,
		DurationP50:         row.DurationP50,
		DurationP90:         row.DurationP90,
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
// 3). At most 8 lines (issue #14's "~6 lines max" budget, plus ADR-043
// increment 2's context line — the context window is the one resource
// whose exhaustion mid-turn is catastrophic — and the #62 Phase-1 duration
// line); missing data renders as an explicit "unknown (cold start)", never
// as zero (ADD principle 1).
func (c ForecastCard) AdditionalContext() string {
	lines := []string{
		fmt.Sprintf("Auspex forecast (%s; evaluation %s):", c.labelText(), c.EvaluationID),
		"  scope: " + c.scopeText(),
		"  tokens: " + c.tokensText(),
		"  time: " + c.durationText(),
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

// QuotaWindowStatus is one observed rolling quota window as the
// statusline consumes it: the provider's own limit id plus the latest
// measured used-percent and reset time. Both measurements follow the
// nil-means-unknown rule; a window with no measured percent can never be
// the rendered "worst" window (there is nothing honest to rank it by).
type QuotaWindowStatus struct {
	LimitID     string
	UsedPercent *float64
	ResetsAt    *time.Time
}

// SpendPaceStatus is the aggregated "today's spend + pace" observation
// (issue #90 Phase A; the aggregation lives in internal/pace). TodayUSD
// is pure aggregation of captured cost actuals for the current local
// day; ProjectedEndOfDayUSD is the observed rate linearly stretched to
// local midnight — a labeled pace, never a forecast promise (§7). A nil
// SpendPaceStatus on the input means NO cost data was observed today and
// the whole segment is omitted (unknown is not zero — never "$0.00 by
// default").
type SpendPaceStatus struct {
	TodayUSD float64
	// ProjectedEndOfDayUSD is nil when no honest rate exists (zero spend
	// or no elapsed observation window) — the segment then shows today's
	// figure alone, without a fabricated extrapolation.
	ProjectedEndOfDayUSD *float64
}

// StatusLineInput carries the per-render inputs StatusLineText composes.
// Model, ContextUsedPercent, and QuotaWindows come from LIVE observations
// (the statusline snapshot for Claude, the DB read-back for Codex);
// Spend/RunwayTimeToLimitSeconds are aggregations over persisted
// observations. Card carries the persisted per-turn evaluation — as of
// the #90 Phase A priority flip it contributes ONLY the policy badge:
// every per-turn forecast fragment (context P90 projection, worst-case
// bound, D-08 threshold markers) stays on the card surfaces
// (additionalContext / `auspex evaluate`), off the bar.
type StatusLineInput struct {
	Model string
	Card  *ForecastCard
	// ContextUsedPercent is the live measured context usage — prefer the
	// exact token ratio over the provider's whole-percent rounding
	// (D-14). nil means unknown, never zero.
	ContextUsedPercent *float64
	// QuotaWindows carries EVERY observed rolling quota window; the
	// renderer leads with the worst (highest used-percent) one. Empty
	// contributes no quota segment.
	QuotaWindows []QuotaWindowStatus
	// Spend is today's observed spend + pace for this line's provider.
	// nil (no cost data today) omits the segment entirely.
	Spend *SpendPaceStatus

	// Now is the render instant (the caller's injected clock), used only
	// to decide how a quota reset renders (same local day → clock time,
	// else weekday). The zero value degrades to the weekday form — never
	// a wall-clock read inside the renderer.
	Now time.Time

	// RunwayTimeToLimitSeconds is the independent Runway Predictor's
	// uncalibrated P50 estimate of seconds until the binding quota window is
	// exhausted at the observed burn rate (M10, read back from
	// runway_forecasts by the hook path). nil — the common case — renders no
	// runway segment: it is set only when a forecast exists AND projects
	// exhaustion within the horizon, so a session with headroom stays quiet.
	// The rendered value is always labeled with a leading "~" (an estimate,
	// never a probability — Constitution §7); the whole line is uncalibrated
	// by construction this wave.
	RunwayTimeToLimitSeconds *int64
}

// StatusLineText renders the one-line statusline display. v4 layout is
// the issue-#90 Phase A "aggregate-first" priority flip: the line LEADS
// with the observational trio — worst quota window, runway ETA when
// within-horizon, today's spend + pace — because those are the things
// that are actually predictable at useful precision; the measured
// context fill follows, and the per-turn surface is demoted to the
// trailing policy badge alone:
//
//	ax» <model> │ ◷ 5h ~<pct>% (resets <when>) │ ⏳ runway ~<eta> │ today $<x> · pace → ~$<y> by 24:00 │ context [<bar>] <cur>% │ ✓ RUN
//
// model may be empty (renders as bare "ax»") and every segment is
// independently optional, so the status bar always has something to show
// and never fabricates a number for a measurement it does not have.
//
// What the flip removed, deliberately (#90; goldens updated in step):
// the v3 context segment's per-turn projection fragments — the
// "(p90 ≤N%)" parenthetical, the projection-only "worst-case" wording,
// and the D-08 "(warn)"/"(checkpoint)" markers — all derived from the
// per-turn forecast whose cold-start error PR #79 measured at 7–9×
// median. They remain on the card surfaces (AdditionalContext,
// `auspex evaluate`), where they are labeled uncalibrated estimates; the
// bar now shows only what was measured. The context segment renders the
// live measurement (one decimal — it is exact; the bar tracks it) and
// nothing else; no measurement contributes no segment rather than a
// fabricated 0%.
//
// The quota segment leads with the WORST observed window (highest
// used-percent — the binding constraint), labeled with a human name
// ("5h"/"weekly") and its reset moment when observed. It stays uncolored
// until #21's binding-constraint policy gives it honest thresholds. The
// spend segment's extrapolation is always labeled "pace" with a "~"
// (§7): it is today's observed average rate stretched to local midnight,
// not a prediction of what the user will do. No animation by design —
// the command re-runs only per assistant message (300ms debounce, quiet
// when idle), so time-based frames would stutter rather than animate.
// Exported as a package function rather than a method so the nil-card
// fallback is one code path, not caller-side duplication.
func StatusLineText(in StatusLineInput) string {
	head := ansiCyan + "ax»" + ansiReset
	if in.Model != "" {
		head += " " + in.Model
	}
	parts := []string{head}
	if w, ok := worstQuotaWindow(in.QuotaWindows); ok {
		seg := fmt.Sprintf("◷ %s ~%.0f%%", quotaWindowLabel(w.LimitID), *w.UsedPercent)
		if w.ResetsAt != nil {
			seg += " (resets " + quotaResetText(*w.ResetsAt, in.Now) + ")"
		}
		parts = append(parts, seg)
	}
	if in.RunwayTimeToLimitSeconds != nil {
		// Uncalibrated runway hint (M10): only present when the forecast
		// projects exhaustion within the horizon, so this segment is itself
		// the warning — lit yellow, with an hourglass and a "~" estimate
		// label so it never reads as a calibrated countdown (§7).
		parts = append(parts, ansiYellow+"⏳ runway ~"+runwayETAText(*in.RunwayTimeToLimitSeconds)+ansiReset)
	}
	if in.Spend != nil {
		seg := fmt.Sprintf("today $%.2f", in.Spend.TodayUSD)
		if in.Spend.ProjectedEndOfDayUSD != nil {
			// "by 24:00" is ISO 8601's end-of-day: the pace targets local
			// midnight, the fixed day-level horizon pacing is about.
			seg += fmt.Sprintf(" · pace → ~$%.2f by 24:00", *in.Spend.ProjectedEndOfDayUSD)
		}
		parts = append(parts, seg)
	}
	if in.ContextUsedPercent != nil {
		cur := *in.ContextUsedPercent
		parts = append(parts, ansiGreen+fmt.Sprintf("context %s %.1f%%", contextBar(cur), cur)+ansiReset)
	}
	if in.Card != nil && in.Card.PolicyAction != "" {
		parts = append(parts, policyBadge(in.Card.PolicyAction))
	}
	return strings.Join(parts, ansiDim+" │ "+ansiReset)
}

// worstQuotaWindow picks the window with the highest measured
// used-percent — the binding constraint the user actually needs to watch
// (§15.5's conservative worst-window discipline applied to display).
// Windows without a measured percent cannot be ranked and are skipped;
// ok=false when nothing measurable exists. Ties break toward the earlier
// window in the (LimitID-sorted, per the producers) input for a
// deterministic render.
func worstQuotaWindow(windows []QuotaWindowStatus) (QuotaWindowStatus, bool) {
	var worst QuotaWindowStatus
	found := false
	for _, w := range windows {
		if w.UsedPercent == nil {
			continue
		}
		if !found || *w.UsedPercent > *worst.UsedPercent {
			worst, found = w, true
		}
	}
	return worst, found
}

// quotaWindowLabel maps known provider limit ids to the short human
// names the crowded bar can afford; an unknown id renders as itself — a
// new window must never be dropped or mislabeled while waiting for a
// name (the same open-set rule the #21 parser follows).
func quotaWindowLabel(limitID string) string {
	switch limitID {
	case "five_hour", "primary": // claude / codex 5-hour rolling windows
		return "5h"
	case "seven_day", "secondary": // claude / codex weekly windows
		return "weekly"
	}
	return limitID
}

// quotaResetText renders a window's reset moment relative to now: a
// clock time ("18:00") when the reset lands on the same local calendar
// day, else the weekday ("Thu") — day-level precision is all a
// multi-day-away reset deserves on a status bar. A zero now (no clock
// injected) degrades to the weekday form rather than reading the wall
// clock here.
func quotaResetText(resetsAt, now time.Time) string {
	loc := time.Local
	if !now.IsZero() {
		loc = now.Location()
	}
	local := resetsAt.In(loc)
	if !now.IsZero() {
		ny, nm, nd := now.In(loc).Date()
		ry, rm, rd := local.Date()
		if ny == ry && nm == rm && nd == rd {
			return local.Format("15:04")
		}
	}
	return local.Format("Mon")
}

// runwayETAText renders a seconds count as a compact, legible estimate —
// "45s", "8m", or "2h" — rounding to the largest whole unit so the crowded
// status bar shows one token, not a precise-looking timestamp the
// uncalibrated forecast cannot justify. Negative inputs clamp to "0s".
func runwayETAText(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%dh", seconds/3600)
	}
}

// contextBar renders the segment's headline percentage — the live
// measurement when one exists, else the projection — as a 20-cell bar
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

// policyBadge renders ONLY the active policy action, lit with its icon
// and semantic color — D-15 (issue #41) dropped D-13 v2.1's full
// severity scale: one word reads faster on a crowded bar. An action
// outside the known set renders as its raw string — never dropped,
// never mislabeled.
func policyBadge(active app.PolicyAction) string {
	known := []struct {
		action app.PolicyAction
		icon   string
		color  string
	}{
		{app.PolicyRun, "✓", ansiGreen},
		{app.PolicyWarn, "⚠", ansiYellow},
		{app.PolicyCheckpointAndRun, "⚑", ansiMagenta},
		{app.PolicyBlock, "✖", ansiRed},
	}
	for _, s := range known {
		if s.action == active {
			return s.color + s.icon + " " + string(active) + ansiReset
		}
	}
	return string(active)
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
	total := strings.Join(segs, " / ")
	if io := c.tokenSplitText(); io != "" {
		return total + " " + io
	}
	return total
}

// tokenSplitText renders the #65 Phase-1 input/output decomposition as two
// P50-P90 ranges (input first — the dominant, harder-to-predict axis),
// e.g. "(input ~1500-4500 / output ~1500-3000, input wider — uncalibrated)".
// The parenthetical makes the input range's greater width visible, which is
// the whole point of the split (ADR-0053). Empty when neither axis has a
// usable range, so a cold-start row that carried no split renders the total
// alone rather than an empty "()". Each axis needs both bounds to render as
// a range; a half-populated axis is omitted rather than fabricating a bound.
func (c ForecastCard) tokenSplitText() string {
	var parts []string
	if c.InputTokensP50 != nil && c.InputTokensP90 != nil {
		parts = append(parts, fmt.Sprintf("input ~%d-%d", *c.InputTokensP50, *c.InputTokensP90))
	}
	if c.OutputTokensP50 != nil && c.OutputTokensP90 != nil {
		parts = append(parts, fmt.Sprintf("output ~%d-%d", *c.OutputTokensP50, *c.OutputTokensP90))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " / ") + ", input wider — uncalibrated)"
}

// durationText renders the #62 Phase-1 wall-clock estimate as a rounded
// P50–P90 range. Values are rounded to a coarse unit (see humanizeDuration)
// because these are cold-start estimates and minute-level precision would
// falsely imply calibration.
func (c ForecastCard) durationText() string {
	if c.DurationP50 == nil && c.DurationP90 == nil {
		return "unknown (cold start)"
	}
	if c.DurationP50 != nil && c.DurationP90 != nil {
		return fmt.Sprintf("~%s–%s (P50–P90, uncalibrated)",
			humanizeDuration(*c.DurationP50), humanizeDuration(*c.DurationP90))
	}
	// Only one bound known: report it rather than fabricating the other.
	if c.DurationP50 != nil {
		return fmt.Sprintf("~%s P50 (uncalibrated)", humanizeDuration(*c.DurationP50))
	}
	return fmt.Sprintf("~%s P90 (uncalibrated)", humanizeDuration(*c.DurationP90))
}

// humanizeDuration formats a nanosecond count into a coarse, human string
// (rounded to 5s under 90s, to whole minutes under an hour, else h+m) so a
// cold-start estimate never reads with false precision like "37s".
func humanizeDuration(ns int64) string {
	d := time.Duration(ns)
	switch {
	case d < 90*time.Second:
		r := d.Round(5 * time.Second)
		if r < 5*time.Second {
			r = 5 * time.Second
		}
		return fmt.Sprintf("%ds", int(r/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
	default:
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh%dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
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
