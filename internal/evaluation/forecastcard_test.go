// forecastcard_test.go: issue #14's presenter/data layer tests — the
// card is a faithful read-back of what EvaluateTurn persisted, degrades
// honestly on cold-start/missing data (never renders zero for unknown),
// and every surface carries the Constitution-principle-#2 uncalibrated
// labeling (probability stays null).
package evaluation_test

import (
	"context"
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
		t.Error("Calibrated = true, want false (no calibration exists this wave)")
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

	if _, err := svc.ForecastCard(context.Background(), ""); err == nil {
		t.Fatal("ForecastCard(\"\"): expected a validation error")
	} else if derr, ok := err.(*domain.Error); !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("ForecastCard(\"\") error = %v, want ErrCodeValidation", err)
	}

	if _, err := svc.ForecastCard(context.Background(), "no-such-evaluation"); err == nil {
		t.Fatal("ForecastCard(unknown): expected not_found")
	} else if derr, ok := err.(*domain.Error); !ok || derr.Code != domain.ErrCodeNotFound {
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

// TestAdditionalContext_FullCard: the hook-injected block is compact (6
// lines), carries the uncalibrated labeling verbatim, and cites the
// numbers plus the policy action.
func TestAdditionalContext_FullCard(t *testing.T) {
	card := fullTestCard()
	got := card.AdditionalContext()

	lines := strings.Split(got, "\n")
	if len(lines) != 6 {
		t.Errorf("AdditionalContext has %d lines, want exactly 6 (issue #14: ~6 lines max):\n%s", len(lines), got)
	}
	for _, want := range []string{
		"uncalibrated estimate",
		"scores are not probabilities", // Constitution principle #2
		"eval-1",
		"~3–12 files changed",
		"~40–400 lines",
		"P50 8000 / P80 20000 / P90 45000",
		"~$0.02–$0.68 USD",
		"estimate",
		"0.42/1.00 overall",
		"LARGE_FILE_SCOPE",
		"policy: WARN",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AdditionalContext missing %q:\n%s", want, got)
		}
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

	if lines := strings.Split(got, "\n"); len(lines) != 6 {
		t.Errorf("cold-start AdditionalContext has %d lines, want 6:\n%s", len(lines), got)
	}
	for _, want := range []string{
		"scope: unknown (cold start)",
		"tokens: unknown (cold start)",
		"cost: unavailable (no token forecast)",
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

// TestStatusLineText covers the emit-line composition matrix: nil card
// (cold start), missing model, and the full line.
func TestStatusLineText(t *testing.T) {
	card := fullTestCard()

	cases := []struct {
		name  string
		model string
		card  *evaluation.ForecastCard
		want  string
	}{
		{"no model no card", "", nil, "pf✈"},
		{"model only", "Opus 4.1", nil, "pf✈ Opus 4.1"},
		{"full", "Opus 4.1", &card, "pf✈ Opus 4.1 | est P50 8000tok ~$0.02–0.68 | WARN"},
	}
	for _, tc := range cases {
		if got := evaluation.StatusLineText(tc.model, tc.card); got != tc.want {
			t.Errorf("%s: StatusLineText = %q, want %q", tc.name, got, tc.want)
		}
	}

	// A card without a token forecast contributes only its action —
	// never "P50 0tok".
	coldCard := evaluation.ForecastCard{PolicyAction: app.PolicyRun}
	if got, want := evaluation.StatusLineText("Sonnet 4", &coldCard), "pf✈ Sonnet 4 | RUN"; got != want {
		t.Errorf("cold card: StatusLineText = %q, want %q", got, want)
	}
}

// fullTestCard builds a fully-populated card with round numbers so the
// rendered output is predictable.
func fullTestCard() evaluation.ForecastCard {
	return evaluation.ForecastCard{
		EvaluationID:    "eval-1",
		TurnID:          "turn-1",
		CreatedAt:       time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC),
		FilesChangedP50: i64(3), FilesChangedP90: i64(12),
		LinesChangedP50: i64(40), LinesChangedP90: i64(400),
		TokensP50: i64(8000), TokensP80: i64(20000), TokensP90: i64(45000),
		Cost: &pricing.CostRange{
			LowUSD: 0.024, HighUSD: 0.675,
			ModelFamily: pricing.DefaultFamily, Source: pricing.SourceDefaultTable,
		},
		OverallRiskScore: 0.42,
		ReasonCodes:      []domain.ReasonCode{domain.ReasonLargeFileScope, domain.ReasonPredictionColdStart},
		Confidence:       domain.ConfidenceLow,
		Calibrated:       false,
		PolicyAction:     app.PolicyWarn,
	}
}

func i64(v int64) *int64 { return &v }
