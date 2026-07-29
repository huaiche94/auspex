// sessionspent_sql_test.go: #141's session-spend ledger read + the
// full-pipeline envelope behavior, including its interaction with the
// #142 shadow machinery (an envelope PAUSE is a statistical
// enforcement-grade action, so shadow downgrades it with would_action
// recorded — the two Cost Guard slices composing is the point).
package evaluation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

func seedUsageEvent(t *testing.T, db *sqlite.DB, seq int, sessionID, turnID, payload string) {
	t.Helper()
	var turn any
	if turnID != "" {
		turn = turnID
	}
	if _, err := db.Conn().ExecContext(context.Background(), `INSERT INTO events
			(event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, payload_json)
		VALUES (?, 'auspex.event.v1', 'provider.usage.observed', ?, ?, 'test', 'claude', ?, ?, ?)`,
		fmt.Sprintf("ev-sb-%03d", seq),
		"2026-07-29T09:00:00Z", "2026-07-29T09:00:00Z",
		sessionID, turn, payload); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

func TestSQLDataSource_SessionSpentUSD(t *testing.T) {
	db := openMigratedDB(t)
	src := evaluation.NewSQLDataSource(db)
	ctx := context.Background()

	// Unknown: no usage telemetry at all.
	if spent, err := src.SessionSpentUSD(ctx, "sess-none"); err != nil || spent != nil {
		t.Fatalf("no-telemetry spent = (%v, %v), want (nil, nil)", spent, err)
	}

	// Managed per-turn costs sum; cumulative statusline max; max wins.
	seedUsageEvent(t, db, 1, "sess-sb", "turn-1", `{"total_cost_usd":1.5}`)
	seedUsageEvent(t, db, 2, "sess-sb", "turn-2", `{"total_cost_usd":2.5}`)
	seedUsageEvent(t, db, 3, "sess-sb", "", `{"total_cost_usd":3.0}`) // cumulative below the turn sum
	spent, err := src.SessionSpentUSD(ctx, "sess-sb")
	if err != nil {
		t.Fatalf("SessionSpentUSD: %v", err)
	}
	if spent == nil || *spent != 4.0 {
		t.Fatalf("spent = %v, want 4.0 (per-turn sum beats the lower cumulative)", spent)
	}

	seedUsageEvent(t, db, 4, "sess-sb", "", `{"total_cost_usd":9.75}`) // cumulative now above
	spent, err = src.SessionSpentUSD(ctx, "sess-sb")
	if err != nil {
		t.Fatalf("SessionSpentUSD: %v", err)
	}
	if spent == nil || *spent != 9.75 {
		t.Fatalf("spent = %v, want 9.75 (higher cumulative wins)", spent)
	}
}

// envelopeSpentSource wraps the pipeline fake with a fixed spend.
type envelopeSpentSource struct {
	*fakeDataSource
	spent *float64
}

func (s *envelopeSpentSource) SessionSpentUSD(_ context.Context, _ domain.SessionID) (*float64, error) {
	return s.spent, nil
}

func TestEvaluateTurn_SessionBudgetExhausted_Pauses_AndShadowDowngrades(t *testing.T) {
	spent := 12.0
	base := newFakeDataSource()
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "env"}
	svc, db := newTestService(t, clk, ids, base)
	svc.Source = &envelopeSpentSource{fakeDataSource: base, spent: &spent}
	svc.Policy = policy.Config{SessionBudgetUSD: 10}
	ctx := context.Background()

	// Enforce mode (shadow off): the envelope PAUSE is served as-is.
	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-env", TurnID: "turn-env", Provider: "claude-code", PromptHash: "sha256:e",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyPause {
		t.Fatalf("Action = %q, want PAUSE (session budget exhausted: $12 spent vs $10 envelope)", decision.Action)
	}

	// Shadow on: same condition serves WARN with would_action=PAUSE —
	// #142's machinery applies to the envelope with zero extra wiring.
	svc.ShadowEnforcement = true
	eval2, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-env", TurnID: "turn-env2", Provider: "claude-code", PromptHash: "sha256:e2",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn (shadow): %v", err)
	}
	decision2, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval2.ID})
	if err != nil {
		t.Fatalf("Decide (shadow): %v", err)
	}
	if decision2.Action != app.PolicyWarn {
		t.Fatalf("shadow Action = %q, want WARN", decision2.Action)
	}
	var wouldAction *string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT would_action FROM policy_decisions WHERE prediction_id = ?`, string(eval2.ID)).Scan(&wouldAction); err != nil {
		t.Fatalf("read would_action: %v", err)
	}
	if wouldAction == nil || *wouldAction != string(app.PolicyPause) {
		t.Fatalf("would_action = %v, want PAUSE", wouldAction)
	}
}

func TestEvaluateTurn_NoBudget_EnvelopeSilent(t *testing.T) {
	spent := 1000.0
	base := newFakeDataSource()
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "envoff"}
	svc, _ := newTestService(t, clk, ids, base)
	svc.Source = &envelopeSpentSource{fakeDataSource: base, spent: &spent}

	eval, err := svc.EvaluateTurn(context.Background(), app.EvaluateTurnRequest{
		SessionID: "sess-envoff", TurnID: "turn-envoff", Provider: "claude-code", PromptHash: "sha256:o",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := svc.Decide(context.Background(), app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action == app.PolicyPause {
		t.Fatal("undeclared envelope must never pause")
	}
}
