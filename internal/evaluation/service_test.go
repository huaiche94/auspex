package evaluation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

func baseRequest() app.EvaluateTurnRequest {
	return app.EvaluateTurnRequest{
		SessionID:  domain.SessionID("sess-1"),
		TurnID:     domain.TurnID("turn-1"),
		Provider:   "claude-code",
		PromptHash: "sha256:deadbeef",
	}
}

// TestEvaluateTurn_StampsSessionIdentity (#20 Phase 0, migration 0046): a
// session whose identity was observed (provider_sessions.model/effort,
// kept fresh by statusline ingest via SessionBootstrapper) stamps
// provider/model_id/model_family/effort onto the prediction row — the
// calibration label #11 stratifies by — and the card's cost estimate
// resolves that model's price family instead of the default fallback.
func TestEvaluateTurn_StampsSessionIdentity(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC))
	svc, db := newTestService(t, clk, &sequentialIDs{prefix: "stamp"}, newFakeDataSource())
	ctx := context.Background()

	// Seed what SessionBootstrapper writes in production.
	exec(t, db, `INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		"repo-stamp", "/repo", "/repo/.git", "2026-07-14T00:00:00Z", "2026-07-14T00:00:00Z")
	exec(t, db, `INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"wt-stamp", "repo-stamp", "/repo", "/repo/.git", "2026-07-14T00:00:00Z", "2026-07-14T00:00:00Z")
	exec(t, db, `INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, model, effort, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sess-stamp", "wt-stamp", "claude", "native-hook", "claude-fable-5", "xhigh", "2026-07-14T00:00:00Z")

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-stamp", TurnID: "turn-stamp", Provider: "claude", PromptHash: "sha256:deadbeef",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	var provider, modelID, modelFamily, effort string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT provider, model_id, model_family, effort FROM predictions WHERE id = ?`, string(eval.ID),
	).Scan(&provider, &modelID, &modelFamily, &effort); err != nil {
		t.Fatalf("read back prediction stamp: %v", err)
	}
	if provider != "claude" || modelID != "claude-fable-5" || modelFamily != "fable" || effort != "xhigh" {
		t.Errorf("stamp = %s/%s/%s/%s, want claude/claude-fable-5/fable/xhigh", provider, modelID, modelFamily, effort)
	}

	if _, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	card, err := svc.ForecastCard(ctx, eval.ID)
	if err != nil {
		t.Fatalf("ForecastCard: %v", err)
	}
	if card.Cost == nil || card.Cost.ModelFamily != "fable" {
		t.Fatalf("Cost = %+v, want the fable price family resolved from the stamped model", card.Cost)
	}
}

// TestEvaluateTurn_UnknownIdentityStampsNull (#20 Phase 0): a session never
// observed resolves to NULL stamps — unknown is not zero, and identity
// resolution must never fail an evaluation.
func TestEvaluateTurn_UnknownIdentityStampsNull(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC))
	svc, db := newTestService(t, clk, &sequentialIDs{prefix: "nostamp"}, newFakeDataSource())
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, baseRequest())
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	var modelID, modelFamily, effort any
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT model_id, model_family, effort FROM predictions WHERE id = ?`, string(eval.ID),
	).Scan(&modelID, &modelFamily, &effort); err != nil {
		t.Fatalf("read back prediction: %v", err)
	}
	if modelID != nil || modelFamily != nil || effort != nil {
		t.Errorf("stamps = %v/%v/%v, want all NULL for an unobserved session", modelID, modelFamily, effort)
	}
}

func TestEvaluateTurn_PersistsAndReturnsFrozenEvaluationDTO(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	ctx := context.Background()
	got, err := svc.EvaluateTurn(ctx, baseRequest())
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	if got.ID == "" {
		t.Error("Evaluation.ID is empty")
	}
	if got.TurnID != domain.TurnID("turn-1") {
		t.Errorf("Evaluation.TurnID = %q, want turn-1", got.TurnID)
	}
	if !got.CreatedAt.Equal(clk.Now()) {
		t.Errorf("Evaluation.CreatedAt = %v, want %v", got.CreatedAt, clk.Now())
	}
	// Cold-start (no session/repo/progress history configured on
	// fakeDataSource): every stage should report Calibrated=false, per the
	// cold-start contract every stage in this pipeline already implements.
	if got.Calibrated {
		t.Error("Evaluation.Calibrated = true on a cold-start input, want false")
	}
}

func TestEvaluateTurn_RequiresSessionTurnProvider(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	ctx := context.Background()

	cases := []struct {
		name string
		req  app.EvaluateTurnRequest
	}{
		{"missing session", app.EvaluateTurnRequest{TurnID: "t1", Provider: "claude-code"}},
		{"missing turn", app.EvaluateTurnRequest{SessionID: "s1", Provider: "claude-code"}},
		{"missing provider", app.EvaluateTurnRequest{SessionID: "s1", TurnID: "t1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.EvaluateTurn(ctx, tc.req)
			if err == nil {
				t.Fatal("expected a validation error, got nil")
			}
			var domErr *domain.Error
			if !asDomainError(err, &domErr) {
				t.Fatalf("expected *domain.Error, got %T: %v", err, err)
			}
			if domErr.Code != domain.ErrCodeValidation {
				t.Errorf("Code = %q, want validation", domErr.Code)
			}
		})
	}
}

func TestEvaluateTurn_DeterministicForSameInputs(t *testing.T) {
	// Same fixed clock/IDs/DataSource inputs across two independent
	// Service instances (independent DBs) must produce the same
	// Calibrated/Confidence/ReasonCodes/risk-band decision — the required
	// "deterministic output for same inputs" test.
	fixedTime := time.Date(2026, 3, 1, 8, 30, 0, 0, time.UTC)

	run := func() (app.Evaluation, app.DecisionResult) {
		clk := newFakeClock(fixedTime)
		ids := &sequentialIDs{prefix: "eval"}
		svc, _ := newTestService(t, clk, ids, newFakeDataSource())
		ctx := context.Background()

		eval, err := svc.EvaluateTurn(ctx, baseRequest())
		if err != nil {
			t.Fatalf("EvaluateTurn: %v", err)
		}
		decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		return eval, decision
	}

	eval1, decision1 := run()
	eval2, decision2 := run()

	if eval1.Calibrated != eval2.Calibrated {
		t.Errorf("Calibrated differs across runs: %v vs %v", eval1.Calibrated, eval2.Calibrated)
	}
	if eval1.Confidence != eval2.Confidence {
		t.Errorf("Confidence differs across runs: %v vs %v", eval1.Confidence, eval2.Confidence)
	}
	if len(eval1.ReasonCodes) != len(eval2.ReasonCodes) {
		t.Fatalf("ReasonCodes length differs: %v vs %v", eval1.ReasonCodes, eval2.ReasonCodes)
	}
	for i := range eval1.ReasonCodes {
		if eval1.ReasonCodes[i] != eval2.ReasonCodes[i] {
			t.Errorf("ReasonCodes[%d] differs: %v vs %v", i, eval1.ReasonCodes[i], eval2.ReasonCodes[i])
		}
	}
	if decision1.Action != decision2.Action {
		t.Errorf("Decision.Action differs across runs: %v vs %v", decision1.Action, decision2.Action)
	}
}

func TestGetEvaluation_ReturnsPersistedEvaluation(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	ctx := context.Background()

	created, err := svc.EvaluateTurn(ctx, baseRequest())
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	got, err := svc.GetEvaluation(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEvaluation: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.TurnID != created.TurnID {
		t.Errorf("TurnID = %q, want %q", got.TurnID, created.TurnID)
	}
	if got.Calibrated != created.Calibrated {
		t.Errorf("Calibrated = %v, want %v", got.Calibrated, created.Calibrated)
	}
	if got.Confidence != created.Confidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, created.Confidence)
	}
}

func TestGetEvaluation_UnknownIDReturnsNotFound(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	ctx := context.Background()

	_, err := svc.GetEvaluation(ctx, domain.EvaluationID("does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for an unknown EvaluationID")
	}
	var domErr *domain.Error
	if !asDomainError(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeNotFound {
		t.Errorf("Code = %q, want not_found", domErr.Code)
	}
}

func TestGetEvaluation_RejectsEmptyID(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	_, err := svc.GetEvaluation(context.Background(), domain.EvaluationID(""))
	if err == nil {
		t.Fatal("expected a validation error for an empty EvaluationID")
	}
}

func TestDecide_ReadsBackPersistedPolicyDecision(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, baseRequest())
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.ID == "" {
		t.Error("DecisionResult.ID is empty")
	}
	if decision.Action == "" {
		t.Error("DecisionResult.Action is empty")
	}

	// A second Decide call for the same evaluation must read back the
	// same decision (not recompute a new one, not error) — proves
	// read-back semantics rather than mutating/duplicating state.
	decision2, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("second Decide: %v", err)
	}
	if decision2.ID != decision.ID || decision2.Action != decision.Action {
		t.Errorf("second Decide() = %+v, want identical to first %+v", decision2, decision)
	}
}

func TestDecide_UnknownEvaluationIDReturnsNotFound(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	_, err := svc.Decide(context.Background(), app.DecideRequest{EvaluationID: domain.EvaluationID("nope")})
	if err == nil {
		t.Fatal("expected an error for an unknown EvaluationID")
	}
	var domErr *domain.Error
	if !asDomainError(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeNotFound {
		t.Errorf("Code = %q, want not_found", domErr.Code)
	}
}

func TestDecide_RejectsEmptyEvaluationID(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	_, err := svc.Decide(context.Background(), app.DecideRequest{})
	if err == nil {
		t.Fatal("expected a validation error for an empty EvaluationID")
	}
}

func TestEvaluateTurn_PropagatesResolveError(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "eval"}
	src := newFakeDataSource()
	src.resolveErr = &domain.Error{Code: domain.ErrCodeUnavailable, Message: "boom"}
	svc, _ := newTestService(t, clk, ids, src)

	_, err := svc.EvaluateTurn(context.Background(), baseRequest())
	if err == nil {
		t.Fatal("expected EvaluateTurn to propagate a DataSource.Resolve error")
	}
}

// asDomainError is a small helper so tests can assert on *domain.Error's
// Code field regardless of whether the service wrapped it (it currently
// does not wrap validation/not-found errors, but tests should not assume
// that implementation detail). Uses errors.As, not a bare type assertion,
// so it still matches a wrapped *domain.Error.
func asDomainError(err error, target **domain.Error) bool {
	return errors.As(err, target)
}
