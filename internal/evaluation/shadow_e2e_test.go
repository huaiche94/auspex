// shadow_e2e_test.go: issue #142 / ADR-0057 §5 — shadow enforcement
// through the full evaluation pipeline against a real migrated DB.
// The scenario mirrors TestFullPipeline_DegradedRunwayNeverSilentlyAllows-
// WhenEmergency (the one production condition that reaches an
// enforcement-grade PAUSE today: the uncalibrated §17.6 emergency gate)
// and proves the shadow contract on it: the served AND persisted action
// downgrades to WARN, the original action survives in
// policy_decisions.would_action, and with shadow off the behavior is
// byte-identical to before.
package evaluation_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

func emergencyRunwaySource() *fakeDataSource {
	src := newFakeDataSource() // cold-start everywhere else
	src.runway = domain.RunwayForecast{
		Calibrated:         false,
		Confidence:         domain.ConfidenceLow,
		CurrentUsedPercent: ptrF64(99), // emergency: >= 98%
		RiskScore:          0.99,
	}
	src.hasRunway = true
	return src
}

func readWouldAction(t *testing.T, db interface {
	Conn() *sql.DB
}, evaluationID string) (action string, wouldAction *string) {
	t.Helper()
	row := db.Conn().QueryRowContext(context.Background(), `
		SELECT action, would_action FROM policy_decisions WHERE prediction_id = ?`, evaluationID)
	if err := row.Scan(&action, &wouldAction); err != nil {
		t.Fatalf("scan policy_decisions for %s: %v", evaluationID, err)
	}
	return action, wouldAction
}

func TestShadowEnforcement_EmergencyPauseDowngradesToWarn_WouldActionPersisted(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "shadow"}
	svc, db := newTestService(t, clk, ids, emergencyRunwaySource())
	svc.ShadowEnforcement = true
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-shadow", TurnID: "turn-shadow", Provider: "claude-code", PromptHash: "sha256:s",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	// Served action: WARN via Decide's read-back (read-back, not
	// recompute — one persisted row, one truth).
	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyWarn {
		t.Errorf("Decide Action = %q, want WARN", decision.Action)
	}

	// Persisted disclosure: the original PAUSE survives in would_action.
	action, wouldAction := readWouldAction(t, db, string(eval.ID))
	if action != string(app.PolicyWarn) {
		t.Errorf("persisted action = %q, want WARN", action)
	}
	if wouldAction == nil || *wouldAction != string(app.PolicyPause) {
		t.Errorf("persisted would_action = %v, want PAUSE", wouldAction)
	}
}

func TestShadowEnforcement_Off_EmergencyPauseUnchanged_WouldActionNull(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "noshadow"}
	svc, db := newTestService(t, clk, ids, emergencyRunwaySource())
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-noshadow", TurnID: "turn-noshadow", Provider: "claude-code", PromptHash: "sha256:n",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyPause {
		t.Errorf("Decision.Action = %q, want PAUSE (shadow off must not change enforcement)", decision.Action)
	}
	action, wouldAction := readWouldAction(t, db, string(eval.ID))
	if action != string(app.PolicyPause) {
		t.Errorf("persisted action = %q, want PAUSE", action)
	}
	if wouldAction != nil {
		t.Errorf("persisted would_action = %q, want NULL (never shadowed)", *wouldAction)
	}
}

func TestShadowEnforcement_NonEnforcementActionsUntouched(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "calm"}
	svc, db := newTestService(t, clk, ids, newFakeDataSource()) // cold start: no enforcement condition
	svc.ShadowEnforcement = true
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-calm", TurnID: "turn-calm", Provider: "claude-code", PromptHash: "sha256:c",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action == app.PolicyBlock || decision.Action == app.PolicyPause || decision.Action == app.PolicyPauseAndAutoResume {
		t.Fatalf("cold-start scenario unexpectedly produced enforcement action %q — test premise broken", decision.Action)
	}
	_, wouldAction := readWouldAction(t, db, string(eval.ID))
	if wouldAction != nil {
		t.Errorf("would_action = %q on a non-enforcement decision, want NULL", *wouldAction)
	}
}
