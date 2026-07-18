// forecastcard_test.go: issue #14's presenter/data layer tests — the
// card is a faithful read-back of what EvaluateTurn persisted, degrades
// honestly on cold-start/missing data (never renders zero for unknown),
// and every surface carries the Constitution-principle-#2 uncalibrated
// labeling (probability stays null).
package evaluation_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// TestForecastCard_ReadsBackPersistedEvaluation drives a REAL
// EvaluateTurn (the same production pipeline, cold-start inputs) and
// confirms ForecastCard reads back exactly the persisted row: matching
// IDs, the token quantiles the RuleTokenForecaster produced (nonzero
// cold-start defaults), a cost range derived from them via the pricing
// table, an uncalibrated label state, and the persisted policy action.
func TestForecastCard_ReadsBackPersistedEvaluation(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))
	svc, _ := newTestService(t, clk, &sequentialIDs{prefix: "id"}, newFakeDataSource())

	eval, err := svc.EvaluateTurn(context.Background(), app.EvaluateTurnRequest{
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		Provider:   "claude",
		PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	card, err := svc.ForecastCard(context.Background(), eval.ID)
	if err != nil {
		t.Fatalf("ForecastCard: %v", err)
	}

	if card.EvaluationID != eval.ID {
		t.Errorf("EvaluationID = %q, want %q", card.EvaluationID, eval.ID)
	}
	if card.TurnID != "turn-1" {
		t.Errorf("TurnID = %q, want turn-1", card.TurnID)
	}
	if card.TokensP50 == nil || *card.TokensP50 <= 0 {
		t.Fatalf("TokensP50 = %v, want the persisted nonzero cold-start forecast", card.TokensP50)
	}
	if card.TokensP90 == nil || *card.TokensP90 < *card.TokensP50 {
		t.Errorf("TokensP90 = %v, want >= TokensP50 %d", card.TokensP90, *card.TokensP50)
	}
	if card.Cost == nil {
		t.Fatal("Cost = nil, want an estimated range derived from the persisted token quantiles (ADR-043)")
	}
	if !(card.Cost.LowUSD < card.Cost.HighUSD) {
		t.Errorf("Cost = [%v, %v], want a genuine range, never a point (ADR-043)", card.Cost.LowUSD, card.Cost.HighUSD)
	}
	if card.Cost.ModelFamily != pricing.DefaultFamily {
		t.Errorf("Cost.ModelFamily = %q, want %q (prediction rows persist no model column)", card.Cost.ModelFamily, pricing.DefaultFamily)
	}
	if card.Calibrated {
		t.Error("Calibrated = true, want false (no calibration exists this phase)")
	}
	if card.Probability != nil {
		t.Errorf("Probability = %v, want nil — Constitution principle #2: cold-start/uncalibrated emits probability null, never a number", *card.Probability)
	}
	if card.PolicyAction == "" {
		t.Error("PolicyAction is empty, want the persisted policy_decisions.action")
	}
	if card.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want the persisted predictions.created_at")
	}
}

// TestForecastCard_Validation_And_NotFound covers the two error paths:
// an empty ID is a validation error, an unknown ID is the frozen
// not_found shape.
func TestForecastCard_Validation_And_NotFound(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))
	svc, _ := newTestService(t, clk, &sequentialIDs{prefix: "id"}, newFakeDataSource())

	var derr *domain.Error
	if _, err := svc.ForecastCard(context.Background(), ""); err == nil {
		t.Fatal("ForecastCard(\"\"): expected a validation error")
	} else if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("ForecastCard(\"\") error = %v, want ErrCodeValidation", err)
	}

	if _, err := svc.ForecastCard(context.Background(), "no-such-evaluation"); err == nil {
		t.Fatal("ForecastCard(unknown): expected not_found")
	} else if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("ForecastCard(unknown) error = %v, want ErrCodeNotFound", err)
	}
}

// TestForecastCard_NoTokenForecast_NoCostEstimate: a degraded token stage
// (returns the zero TokenForecast — the "all-nil" fail-open scenario
// predictor-11 already exercises) persists 0-token quantiles, and the
// card must respond with Cost == nil ("no token forecast means no cost
// estimate, never a fabricated $0" — ADD principle 1 through ADR-043's
// lens), while everything else still reads back.
func TestForecastCard_NoTokenForecast_NoCostEstimate(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))
	source := newFakeDataSource()
	stages := realStages(source)
	stages.Tokens = errInjectingTokenForecaster{inner: stages.Tokens, nilResult: true}
	svc, _ := newTestServiceWithStages(t, clk, &sequentialIDs{prefix: "id"}, source, stages)

	eval, err := svc.EvaluateTurn(context.Background(), app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	card, err := svc.ForecastCard(context.Background(), eval.ID)
	if err != nil {
		t.Fatalf("ForecastCard: %v", err)
	}
	if card.Cost != nil {
		t.Errorf("Cost = %+v, want nil for a zero-token forecast", card.Cost)
	}
	if !strings.Contains(card.AdditionalContext(), "cost: unavailable (no token forecast)") {
		t.Errorf("AdditionalContext should name the missing cost estimate explicitly:\n%s", card.AdditionalContext())
	}
}

// TestLatestForecastCard_ColdStart: a session with no linkable evaluation
// (no events at all) returns ok=false with no error — cold start is a
// valid answer, not a failure (the DataSource discipline applied to the
// statusline lookup).
func TestLatestForecastCard_ColdStart(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))
	svc, _ := newTestService(t, clk, &sequentialIDs{prefix: "id"}, newFakeDataSource())

	card, ok, err := svc.LatestForecastCard(context.Background(), "sess-unknown")
	if err != nil {
		t.Fatalf("LatestForecastCard: %v", err)
	}
	if ok {
		t.Fatalf("ok = true (card %+v), want false for a session with no evaluations", card)
	}
}

// TestLatestForecastCard_JoinsThroughTurnStartedEvents proves the
// session -> prediction linkage the statusline --emit-line lookup relies
// on: predictions are turn-scoped, so the join goes through the
// provider.turn.started event whose turn_id the hook handler stamps
// (internal/orchestrator/hooks.go mints one TurnID for both the event
// and EvaluateTurn). Two evaluations for the same session must resolve
// to the LATEST one.
func TestLatestForecastCard_JoinsThroughTurnStartedEvents(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))
	svc, db := newTestService(t, clk, &sequentialIDs{prefix: "id"}, newFakeDataSource())
	ctx := context.Background()

	evalOld, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-old", Provider: "claude",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn(old): %v", err)
	}
	clk.Advance(1 * time.Minute)
	evalNew, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-new", Provider: "claude",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn(new): %v", err)
	}

	insertTurnStartedEvent(t, db, "ev-1", "sess-1", "turn-old", clk.Now().Add(-1*time.Minute))
	insertTurnStartedEvent(t, db, "ev-2", "sess-1", "turn-new", clk.Now())
	// A different session's event must never leak into this session's
	// lookup.
	insertTurnStartedEvent(t, db, "ev-3", "sess-other", "turn-old", clk.Now())

	card, ok, err := svc.LatestForecastCard(ctx, "sess-1")
	if err != nil {
		t.Fatalf("LatestForecastCard: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true after two linkable evaluations")
	}
	if card.EvaluationID != evalNew.ID {
		t.Errorf("EvaluationID = %q, want the latest %q (older was %q)", card.EvaluationID, evalNew.ID, evalOld.ID)
	}

	if _, _, err := svc.LatestForecastCard(ctx, ""); err == nil {
		t.Fatal("LatestForecastCard(\"\"): expected a validation error")
	}
}

// insertTurnStartedEvent writes a minimal provider.turn.started events row
// directly (this package's tests already query/write other roles' tables
// through the shared migrated DB the same way — the events schema is
// frozen, migration 0010).
func insertTurnStartedEvent(t *testing.T, db *sqlite.DB, eventID string, sessionID, turnID string, at time.Time) {
	t.Helper()
	_, err := db.Conn().ExecContext(context.Background(), `
		INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, payload_json)
		VALUES (?, 'v1', 'provider.turn.started', ?, ?, 'hook', 'claude', ?, ?, '{}')`,
		eventID, at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano), sessionID, turnID,
	)
	if err != nil {
		t.Fatalf("insert events row: %v", err)
	}
}

// --- presenter rendering -----------------------------------------------

// TestAdditionalContext_FullCard: the hook-injected block is compact (7
// lines — issue #14's ~6-line budget plus ADR-043 increment 2's context
// line), carries the uncalibrated labeling verbatim, and cites the
// numbers, the context projection with its D-08 threshold state, and the
// policy action.
func TestAdditionalContext_FullCard(t *testing.T) {
	card := fullTestCard()
	got := card.AdditionalContext()

	lines := strings.Split(got, "\n")
	if len(lines) != 8 {
		t.Errorf("AdditionalContext has %d lines, want exactly 8 (issue #14's ~6-line budget + the ADR-043 increment-2 context line + the #62 duration line):\n%s", len(lines), got)
	}
	for _, want := range []string{
		"uncalibrated estimate",
		"scores are not probabilities", // Constitution principle #2
		"eval-1",
		"~3–12 files changed",
		"~40–400 lines",
		"P50 8000 / P80 20000 / P90 45000",
		"time: ~45s–4m (P50–P90, uncalibrated)", // #62 Phase-1 duration line
		"~$0.02–$0.68 USD",
		"estimate",
		"context: P90 ~91% of window (projected) — WARN threshold exceeded", // ADR-043 increment 2 / D-08
		"0.42/1.00 overall",
		"LARGE_FILE_SCOPE",
		"policy: WARN",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AdditionalContext missing %q:\n%s", want, got)
		}
	}
}

// TestAdditionalContext_ContextThresholdStates: the context line's D-08
// threshold marker is read back from the card's persisted flags — a
// projection above 85% with NO recorded threshold decision (cold-start
// gated, or disabled) renders the percentage without a threshold claim,
// and the checkpoint marker outranks the warn marker.
func TestAdditionalContext_ContextThresholdStates(t *testing.T) {
	card := fullTestCard()

	card.ContextWarnThresholdExceeded = false
	card.ContextCheckpointThresholdExceeded = false
	if got := card.AdditionalContext(); !strings.Contains(got, "context: P90 ~91% of window (projected)") ||
		strings.Contains(got, "threshold exceeded") {
		t.Errorf("gated projection must render without a threshold claim:\n%s", got)
	}

	card.ContextProjectedP90 = ptrF64(97)
	card.ContextWarnThresholdExceeded = true
	card.ContextCheckpointThresholdExceeded = true
	if got := card.AdditionalContext(); !strings.Contains(got, "context: P90 ~97% of window (projected) — CHECKPOINT threshold exceeded") {
		t.Errorf("checkpoint marker should outrank warn:\n%s", got)
	}
}

// TestAdditionalContext_ColdStartDegradation: a card with no scope/token/
// cost data renders explicit unknowns — never a fabricated zero — and
// stays within the 6-line budget.
func TestAdditionalContext_ColdStartDegradation(t *testing.T) {
	card := evaluation.ForecastCard{
		EvaluationID: "eval-cold",
		Confidence:   domain.ConfidenceUnavailable,
		PolicyAction: app.PolicyRun,
	}
	got := card.AdditionalContext()

	if lines := strings.Split(got, "\n"); len(lines) != 8 {
		t.Errorf("cold-start AdditionalContext has %d lines, want 8:\n%s", len(lines), got)
	}
	for _, want := range []string{
		"scope: unknown (cold start)",
		"tokens: unknown (cold start)",
		"cost: unavailable (no token forecast)",
		"context: unknown (cold start)", // nil projection is an explicit unknown, never 0% (ADD principle 1)
		"uncalibrated estimate",
		"policy: RUN",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cold-start AdditionalContext missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "$") {
		t.Errorf("cold-start AdditionalContext must not invent a cost:\n%s", got)
	}
}

// TestAdditionalContext_ReasonCodesCapped: only the top few reason codes
// render (the block goes into an agent's context on every prompt).
func TestAdditionalContext_ReasonCodesCapped(t *testing.T) {
	card := fullTestCard()
	card.ReasonCodes = []domain.ReasonCode{
		domain.ReasonLargeFileScope, domain.ReasonLargeLineScope,
		domain.ReasonMigrationLikely, domain.ReasonSecuritySensitive,
	}
	got := card.AdditionalContext()
	if strings.Contains(got, string(domain.ReasonSecuritySensitive)) {
		t.Errorf("AdditionalContext should cap reason codes at 3, got a 4th:\n%s", got)
	}
	if !strings.Contains(got, string(domain.ReasonMigrationLikely)) {
		t.Errorf("AdditionalContext should include the 3rd reason code:\n%s", got)
	}
}

// TestStatusLineText covers the emit-line composition matrix for the v4
// observation-first layout (#90 Phase A): the line leads with the worst
// quota window, runway, and today's spend + pace; the measured context
// follows; the card contributes ONLY the policy badge — every per-turn
// forecast fragment (context projection, worst-case bound, D-08 markers)
// was deliberately demoted off the bar (goldens updated in step with the
// flip, not accidentally).
func TestStatusLineText(t *testing.T) {
	card := fullTestCard()

	// Byte-exact ANSI pins (issue #29): the codes are written out
	// explicitly here — NOT imported from the package under test — so a
	// renderer regression cannot rewrite its own expectations.
	const (
		reset  = "\x1b[0m"
		dim    = "\x1b[2m"
		brand  = "\x1b[36max»" + reset // cyan
		sep    = dim + " │ " + reset   // dim separator
		green  = "\x1b[32m"
		yellow = "\x1b[33m"
		// D-15 (issue #41): the policy segment shows ONLY the active
		// action, lit with its icon and semantic color.
		badgeRun  = green + "✓ RUN" + reset
		badgeWarn = yellow + "⚠ WARN" + reset
		// 20-cell bar: the measured 26.8% → 5 filled cells.
		bar27 = "[█████···············]"
	)
	weekly := 31.4
	fiveHour := 42.5
	ctxCur := 26.8173
	// A fixed render instant so the reset rendering is deterministic:
	// 2026-07-16 (a Thursday) 09:00 UTC.
	now := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	sameDayReset := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
	laterReset := time.Date(2026, 7, 19, 2, 0, 0, 0, time.UTC) // Sunday
	projected := 3.7512
	cases := []struct {
		name string
		in   evaluation.StatusLineInput
		want string
	}{
		{"no model no card", evaluation.StatusLineInput{}, brand},
		{"model only", evaluation.StatusLineInput{Model: "Opus 4.1"}, brand + " Opus 4.1"},
		// #90: a card alone contributes only the policy badge — its
		// per-turn projection (91%, warn) renders nothing here.
		{"card contributes only the badge", evaluation.StatusLineInput{Model: "Opus 4.1", Card: &card},
			brand + " Opus 4.1" + sep + badgeWarn},
		// A measurement renders with or without a card — it is live
		// observation data; no per-turn parenthetical ever attaches.
		{"measured context without card", evaluation.StatusLineInput{Model: "Opus 4.1", ContextUsedPercent: &ctxCur},
			brand + " Opus 4.1" + sep + green + "context " + bar27 + " 26.8%" + reset},
		{"measured context with card", evaluation.StatusLineInput{Model: "Opus 4.1", Card: &card, ContextUsedPercent: &ctxCur},
			brand + " Opus 4.1" + sep + green + "context " + bar27 + " 26.8%" + reset + sep + badgeWarn},
		// The quota segment is observation data, independent of the card
		// (uncolored until #21 gives it honest thresholds). One window
		// renders as itself…
		{"single quota window", evaluation.StatusLineInput{Model: "Opus 4.1",
			QuotaWindows: []evaluation.QuotaWindowStatus{{LimitID: "seven_day", UsedPercent: &weekly}}},
			brand + " Opus 4.1" + sep + "◷ weekly ~31%"},
		// …and with several, the WORST (highest used-percent) window
		// leads — the binding constraint, not a fixed favorite.
		{"worst window wins", evaluation.StatusLineInput{Model: "Opus 4.1",
			QuotaWindows: []evaluation.QuotaWindowStatus{
				{LimitID: "five_hour", UsedPercent: &fiveHour},
				{LimitID: "seven_day", UsedPercent: &weekly},
			}},
			brand + " Opus 4.1" + sep + "◷ 5h ~42%"},
		// A same-local-day reset renders as clock time; a multi-day-away
		// reset renders as the weekday alone.
		{"reset same day", evaluation.StatusLineInput{Model: "Opus 4.1", Now: now,
			QuotaWindows: []evaluation.QuotaWindowStatus{{LimitID: "five_hour", UsedPercent: &fiveHour, ResetsAt: &sameDayReset}}},
			brand + " Opus 4.1" + sep + "◷ 5h ~42% (resets 18:00)"},
		{"reset later in the week", evaluation.StatusLineInput{Model: "Opus 4.1", Now: now,
			QuotaWindows: []evaluation.QuotaWindowStatus{{LimitID: "seven_day", UsedPercent: &weekly, ResetsAt: &laterReset}}},
			brand + " Opus 4.1" + sep + "◷ weekly ~31% (resets Sun)"},
		// A window with no measured percent cannot be the worst window —
		// no fabricated 0% (unknown is not zero).
		{"unmeasured windows contribute nothing", evaluation.StatusLineInput{Model: "Opus 4.1",
			QuotaWindows: []evaluation.QuotaWindowStatus{{LimitID: "five_hour", ResetsAt: &sameDayReset}}},
			brand + " Opus 4.1"},
		// Spend + pace: aggregation of today's cost actuals; the
		// extrapolation is labeled a pace with "~" (§7), targeting local
		// end-of-day.
		{"spend with pace", evaluation.StatusLineInput{Model: "Opus 4.1",
			Spend: &evaluation.SpendPaceStatus{TodayUSD: 1.4049, ProjectedEndOfDayUSD: &projected}},
			brand + " Opus 4.1" + sep + "today $1.40 · pace → ~$3.75 by 24:00"},
		// No honest rate → today's figure alone, no fabricated pace.
		{"spend without pace", evaluation.StatusLineInput{Model: "Opus 4.1",
			Spend: &evaluation.SpendPaceStatus{TodayUSD: 0}},
			brand + " Opus 4.1" + sep + "today $0.00"},
		// The full trio leads in order — quota, runway, spend — then the
		// measured context, then the demoted policy badge.
		{"observational trio leads", evaluation.StatusLineInput{Model: "Opus 4.1", Card: &card,
			ContextUsedPercent: &ctxCur, Now: now,
			QuotaWindows:             []evaluation.QuotaWindowStatus{{LimitID: "five_hour", UsedPercent: &fiveHour, ResetsAt: &sameDayReset}},
			RunwayTimeToLimitSeconds: ptrI64(480),
			Spend:                    &evaluation.SpendPaceStatus{TodayUSD: 1.4049, ProjectedEndOfDayUSD: &projected}},
			brand + " Opus 4.1" + sep +
				"◷ 5h ~42% (resets 18:00)" + sep +
				yellow + "⏳ runway ~8m" + reset + sep +
				"today $1.40 · pace → ~$3.75 by 24:00" + sep +
				green + "context " + bar27 + " 26.8%" + reset + sep +
				badgeWarn},
	}
	for _, tc := range cases {
		if got := evaluation.StatusLineText(tc.in); got != tc.want {
			t.Errorf("%s: StatusLineText = %q, want %q", tc.name, got, tc.want)
		}
	}

	// A card without a context projection contributes only its action —
	// same as any other card now (#90 made this the rule, not the edge).
	coldCard := evaluation.ForecastCard{PolicyAction: app.PolicyRun}
	if got, want := evaluation.StatusLineText(evaluation.StatusLineInput{Model: "Sonnet 4", Card: &coldCard}), brand+" Sonnet 4"+sep+badgeRun; got != want {
		t.Errorf("cold card: StatusLineText = %q, want %q", got, want)
	}

	// An action outside the known set renders alone as its raw string —
	// never dropped, never mislabeled.
	unknownCard := evaluation.ForecastCard{PolicyAction: app.PolicyAction("FUTURE_ACTION")}
	if got, want := evaluation.StatusLineText(evaluation.StatusLineInput{Model: "Sonnet 4", Card: &unknownCard}), brand+" Sonnet 4"+sep+"FUTURE_ACTION"; got != want {
		t.Errorf("unknown action: StatusLineText = %q, want %q", got, want)
	}
}

// TestStatusLineText_ContextBarFill: the 20-cell bar tracks the measured
// percentage — one cell per 5%, rounded (D-13 v2.1) — and clamps rather
// than overflowing on out-of-range measurements.
func TestStatusLineText_ContextBarFill(t *testing.T) {
	for pct, bar := range map[float64]string{
		0:   "[····················]",
		5:   "[█···················]",
		32:  "[██████··············]",
		50:  "[██████████··········]",
		91:  "[██████████████████··]",
		100: "[████████████████████]",
		130: "[████████████████████]", // clamped, never overflows
	} {
		p := pct
		if got := evaluation.StatusLineText(evaluation.StatusLineInput{Model: "M", ContextUsedPercent: &p}); !strings.Contains(got, "context "+bar+" ") {
			t.Errorf("pct %.0f: line %q missing bar %q", pct, got, bar)
		}
	}
}

// fullTestCard builds a fully-populated card with round numbers so the
// rendered output is predictable, including the ADR-043 increment-2
// context projection with a recorded D-08 warn-threshold state.
func fullTestCard() evaluation.ForecastCard {
	return evaluation.ForecastCard{
		EvaluationID:    "eval-1",
		TurnID:          "turn-1",
		CreatedAt:       time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC),
		FilesChangedP50: i64(3), FilesChangedP90: i64(12),
		LinesChangedP50: i64(40), LinesChangedP90: i64(400),
		TokensP50: i64(8000), TokensP80: i64(20000), TokensP90: i64(45000),
		DurationP50: i64(int64(45 * time.Second)), DurationP90: i64(int64(4 * time.Minute)),
		Cost: &pricing.CostRange{
			LowUSD: 0.024, HighUSD: 0.675,
			ModelFamily: pricing.DefaultFamily, Source: pricing.SourceDefaultTable,
		},
		ContextProjectedP90:          ptrF64(91),
		ContextWarnThresholdExceeded: true,
		OverallRiskScore:             0.42,
		ReasonCodes:                  []domain.ReasonCode{domain.ReasonLargeFileScope, domain.ReasonPredictionColdStart},
		Confidence:                   domain.ConfidenceLow,
		Calibrated:                   false,
		PolicyAction:                 app.PolicyWarn,
	}
}

func i64(v int64) *int64 { return &v }
