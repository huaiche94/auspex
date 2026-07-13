// decision_realauth_test.go proves runtime-b06's hard requirement: the
// REAL internal/evaluation.Service — not a fake — wired through
// DecisionAllowCmd/DecisionDenyCmd, exercising the required tests
// verbatim: "high-risk block and allow-once flow," "second authorization
// replay rejected," and "resubmitted prompt consumes authorization exactly
// once before allowing." Per the DAG's own note ("Hard dependency (not
// fake-able): real authorization semantics"), a fake app.EvaluationService
// can only ever simulate ConsumeAuthorization's exactly-once guarantee;
// this file proves it holds for real, storage-backed consumption, reached
// through this package's own orchestration layer.
//
// This mirrors internal/evaluation/helpers_test.go's own newTestService
// harness (real pipeline stages, real migrated SQLite DB) — duplicated
// here rather than imported, since that helper is unexported in package
// evaluation_test (a different package from this file's
// package orchestrator_test), exactly as that package's own doc comments
// anticipate for any future caller in a different package.
package orchestrator_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/evaluation"
	"github.com/huaiche94/preflight/internal/features"
	"github.com/huaiche94/preflight/internal/orchestrator"
	"github.com/huaiche94/preflight/internal/policy"
	"github.com/huaiche94/preflight/internal/predictor/quota"
	"github.com/huaiche94/preflight/internal/predictor/risk"
	"github.com/huaiche94/preflight/internal/predictor/scope"
	"github.com/huaiche94/preflight/internal/predictor/token"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
	"github.com/huaiche94/preflight/internal/testutil/fakes"
)

// --- deterministic Clock/IDGenerator, mirroring every other package's own
// local-fake convention (internal/scheduler/lease_test.go,
// internal/evaluation/helpers_test.go) rather than sharing one across
// packages. ---

type realauthClock struct{ t time.Time }

func (c realauthClock) Now() time.Time { return c.t }

type realauthIDs struct {
	n      int
	prefix string
}

func (g *realauthIDs) NewID() string {
	g.n++
	return g.prefix + "-" + itoaRealauth(g.n)
}

func itoaRealauth(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// --- real SQLite DB harness (mirrors internal/evaluation/helpers_test.go's
// openMigratedDB / internal/pause's own equivalent — this codebase's
// established per-package convention). ---

func openRealauthDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "preflight.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// --- fakeRealauthDataSource: a configurable evaluation.DataSource, tuned
// to drive the REAL pipeline stages (scope/token/quota/risk/policy — this
// wave's real implementations) to a specific, deliberate risk band, rather
// than mocking Combine/Decide's own output directly. See this package's
// task-scoped research: internal/predictor/risk/combiner.go's
// OverallRisk.Score = max(quota, context, completion, blastRadius); high
// completion/blast-radius terms come from features.PromptFeatures'
// security/migration/cross-layer/open-ended indicators plus large
// changed-file/line quantiles (internal/policy/coldstart.go:
// bandHighThreshold=0.65, bandCriticalThreshold=0.85).
type fakeRealauthDataSource struct {
	repositoryID domain.RepositoryID
	taskID       *domain.TaskID

	classification features.Classification
	promptFeatures features.PromptFeatures

	repoFeatures features.RepositoryFeatures
	sessFeatures features.SessionFeatures
	progFeatures features.ProgressFeatures

	quotaObs   []domain.QuotaObservation
	contextObs domain.ContextObservation
}

func (f *fakeRealauthDataSource) Resolve(_ context.Context, _ domain.SessionID) (evaluation.ResolvedSession, error) {
	return evaluation.ResolvedSession{RepositoryID: f.repositoryID, TaskID: f.taskID}, nil
}

func (f *fakeRealauthDataSource) Classification(_ context.Context, _ domain.SessionID, _ *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return f.classification, f.promptFeatures, nil
}

func (f *fakeRealauthDataSource) Repository(_ context.Context, _ domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return f.repoFeatures, true, nil
}

func (f *fakeRealauthDataSource) Session(_ context.Context, _ domain.SessionID) (features.SessionFeatures, bool, error) {
	return f.sessFeatures, true, nil
}

func (f *fakeRealauthDataSource) Progress(_ context.Context, _ *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return f.progFeatures, true, nil
}

func (f *fakeRealauthDataSource) RecentSimilarTurnTokens(_ context.Context, _ domain.SessionID, _ features.TaskClass) ([]float64, error) {
	return nil, nil
}

func (f *fakeRealauthDataSource) Quota(_ context.Context, _ domain.SessionID) ([]domain.QuotaObservation, error) {
	return f.quotaObs, nil
}

func (f *fakeRealauthDataSource) Context(_ context.Context, _ domain.SessionID) (domain.ContextObservation, error) {
	return f.contextObs, nil
}

func (f *fakeRealauthDataSource) RunwayForecast(_ context.Context, _ domain.SessionID) (domain.RunwayForecast, bool, error) {
	return domain.RunwayForecast{}, false, nil
}

func (f *fakeRealauthDataSource) PriorRunwayHitConfirmed(_ context.Context, _ domain.SessionID) (bool, error) {
	return false, nil
}

var _ evaluation.DataSource = (*fakeRealauthDataSource)(nil)

// realauthScopeAdapter/realauthTokenAdapter adapt *fakeRealauthDataSource to
// internal/predictor/scope.FeatureSource / internal/predictor/token.FeatureSource
// respectively — the two stages' own narrower/differently-shaped source
// interfaces, mirroring internal/evaluation/helpers_test.go's
// scopeSourceAdapter/tokenSourceAdapter precedent exactly (duplicated here
// since that file's adapters are unexported in a different package).
type realauthScopeAdapter struct{ src *fakeRealauthDataSource }

func (a realauthScopeAdapter) Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, sessionID, taskID)
}
func (a realauthScopeAdapter) Repository(ctx context.Context, repositoryID domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return a.src.Repository(ctx, repositoryID)
}
func (a realauthScopeAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, sessionID)
}
func (a realauthScopeAdapter) Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, taskID)
}

type realauthTokenAdapter struct{ src *fakeRealauthDataSource }

func (a realauthTokenAdapter) Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, sessionID, nil)
}
func (a realauthTokenAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, sessionID)
}
func (a realauthTokenAdapter) Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, a.src.taskID)
}
func (a realauthTokenAdapter) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) ([]float64, error) {
	return a.src.RecentSimilarTurnTokens(ctx, sessionID, class)
}

func ptrF(v float64) *float64 { return &v }

// newHighRiskDataSource returns a fakeRealauthDataSource tuned to drive
// OverallRisk.Score into the critical band (>= 0.85 -> PolicyCheckpointAndRun,
// internal/policy/coldstart.go's bandCriticalThreshold) via large
// changed-file/line quantiles plus every completion/blast-radius risk flag
// this pipeline actually reads (security-sensitive, migration-likely,
// cross-layer, open-ended scope — see this file's own package comment for
// the exact formula this is derived from).
func newHighRiskDataSource() *fakeRealauthDataSource {
	return &fakeRealauthDataSource{
		repositoryID: domain.RepositoryID("repo-1"),
		classification: features.Classification{
			Class:      features.TaskClassSecuritySensitive,
			Confidence: domain.ConfidenceLow,
		},
		promptFeatures: features.PromptFeatures{
			ExplicitPathCount:   20,
			MentionsSecurity:    true,
			HasMigrateVerb:      true,
			CrossLayerIndicator: true,
			MentionsTests:       true,
			OpenEndedIndicator:  true,
		},
		repoFeatures: features.RepositoryFeatures{
			TrackedFileCount: 500,
			DirtyFileCount:   10,
			DirtyLineCount:   400,
			TargetDirFanOut:  20,
		},
		sessFeatures: features.SessionFeatures{
			ChangedFilesRecentP50: ptrF(40),
			ChangedFilesRecentP90: ptrF(90),
			ChangedLinesRecentP50: ptrF(3000),
			ChangedLinesRecentP90: ptrF(8000),
		},
		progFeatures: features.ProgressFeatures{
			CriticalPathLength: 50,
		},
		quotaObs: []domain.QuotaObservation{
			{ID: "q1", SessionID: "sess-1", Provider: "anthropic", LimitID: "five_hour",
				UsedPercent: ptrF(97), Reached: false, ObservedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)},
		},
		contextObs: domain.ContextObservation{
			ID: "c1", SessionID: "sess-1", UsedPercent: ptrF(95), ObservedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
		},
	}
}

// newRealEvaluationService wires a REAL *evaluation.Service against real
// pipeline stage implementations and a fresh migrated SQLite DB — no fake
// app.EvaluationService anywhere in this file, per the DAG's hard
// dependency note.
func newRealEvaluationService(t *testing.T, clk domain.Clock, ids domain.IDGenerator, source *fakeRealauthDataSource) *evaluation.Service {
	t.Helper()
	db := openRealauthDB(t)
	return evaluation.New(
		db,
		source,
		scope.NewRuleScopeEstimator(realauthScopeAdapter{src: source}),
		token.NewRuleTokenForecaster(realauthTokenAdapter{src: source}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clk,
		ids,
	)
}

// --- Required test: "high-risk block and allow-once flow" ------------------

// TestDecisionAllow_HighRiskFlowRequiresConfirmationThenIssuesAuthorization
// runs a genuinely high-risk turn through the REAL EvaluateTurn pipeline,
// confirms the real Decide read-back reports a high-risk action requiring
// confirmation/checkpoint (not a plain allow), and then proves
// DecisionAllowCmd's issue flow, wired against the SAME real Service (via
// the AuthorizationIssuer seam), successfully issues a real, storage-backed
// one-time Authorization for it — the "allow-once" half of the flow.
func TestDecisionAllow_HighRiskFlowRequiresConfirmationThenIssuesAuthorization(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, newHighRiskDataSource())
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyRequireConfirmation && decision.Action != app.PolicyCheckpointAndRun {
		t.Fatalf("Decide.Action = %q, want a high-risk action (%q or %q) — fixture did not drive risk high enough",
			decision.Action, app.PolicyRequireConfirmation, app.PolicyCheckpointAndRun)
	}

	deps := orchestrator.DecisionDeps{Evaluation: svc, Issuer: svc}
	result, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		EvaluationID:           eval.ID,
		TurnID:                 "turn-1",
		PromptHash:             "hash-1",
		SnapshotFingerprint:    "fp-1",
		RepositoryCheckpointID: nil,
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (issue flow): %v", err)
	}
	if !result.Issued || result.Consumed {
		t.Fatalf("result = %+v, want Issued=true Consumed=false", result)
	}
	if result.Authorization.ID == "" {
		t.Fatal("Authorization.ID is empty — a real Authorization must have a durable ID")
	}
	if result.Authorization.ConsumedAt != nil {
		t.Fatal("a freshly issued Authorization must not already be consumed")
	}
	if result.Decision.Action != decision.Action {
		t.Fatalf("DecisionAllowCmd's own Decide call reported %q, want it to match the earlier read-back %q", result.Decision.Action, decision.Action)
	}
}

// TestDecisionAllow_LowRiskFlowAllowsWithoutConfirmation is the low-risk
// control: a genuinely low-risk turn through the same REAL pipeline reports
// app.PolicyRun (plain allow) — proving the HIGH-risk fixture above is
// actually testing something real, not an artifact of every turn getting
// flagged regardless of input.
func TestDecisionAllow_LowRiskFlowAllowsWithoutConfirmation(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	source := &fakeRealauthDataSource{
		repositoryID: domain.RepositoryID("repo-1"),
		classification: features.Classification{
			Class:      features.TaskClassDocumentationShort,
			Confidence: domain.ConfidenceLow,
		},
		promptFeatures: features.PromptFeatures{ExplicitPathCount: 1},
		repoFeatures: features.RepositoryFeatures{
			TrackedFileCount: 50, DirtyFileCount: 0, TargetDirFanOut: 2,
		},
		sessFeatures: features.SessionFeatures{
			ChangedFilesRecentP50: ptrF(1), ChangedFilesRecentP90: ptrF(3),
			ChangedLinesRecentP50: ptrF(20), ChangedLinesRecentP90: ptrF(80),
		},
		progFeatures: features.ProgressFeatures{CriticalPathLength: 2},
		quotaObs: []domain.QuotaObservation{
			{ID: "q1", SessionID: "sess-1", Provider: "anthropic", LimitID: "five_hour",
				UsedPercent: ptrF(20), Reached: false, ObservedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)},
		},
		contextObs: domain.ContextObservation{
			ID: "c1", SessionID: "sess-1", UsedPercent: ptrF(15), ObservedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
		},
	}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, source)
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyRun {
		t.Fatalf("Decide.Action = %q, want %q (low-risk control fixture)", decision.Action, app.PolicyRun)
	}
}

// --- Required test: "second authorization replay rejected" + "resubmitted
// prompt consumes authorization exactly once before allowing" -------------

// TestDecisionAllow_ResubmittedPromptConsumesExactlyOnceThenReplayRejected
// is this node's highest-risk proof: issue a real authorization (the
// high-risk flow above), then simulate the resubmitted prompt consuming it
// through DecisionAllowCmd's consume flow — proving it succeeds exactly
// once — and then a THIRD call replaying the SAME AuthorizationID, proving
// the real predictor-10-hardened ConsumeAuthorization rejects it, reached
// through this orchestrator layer, not merely asserted against the
// evaluation package directly.
func TestDecisionAllow_ResubmittedPromptConsumesExactlyOnceThenReplayRejected(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, newHighRiskDataSource())
	ctx := context.Background()
	deps := orchestrator.DecisionDeps{Evaluation: svc, Issuer: svc}

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}

	// Call 1: issue.
	issueResult, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		EvaluationID: eval.ID, TurnID: "turn-1", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (issue): %v", err)
	}
	authID := issueResult.Authorization.ID
	if authID == "" {
		t.Fatal("issued Authorization has an empty ID")
	}

	// Call 2: the resubmitted prompt, consuming the authorization exactly
	// once — must succeed.
	consumeResult, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		TurnID: "turn-1", PromptHash: "hash-1", AuthorizationID: authID,
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (consume, first resubmission): %v — required test 'resubmitted prompt consumes authorization exactly once before allowing' depends on this succeeding", err)
	}
	if !consumeResult.Consumed || consumeResult.Issued {
		t.Fatalf("consumeResult = %+v, want Consumed=true Issued=false", consumeResult)
	}
	if consumeResult.Authorization.ConsumedAt == nil {
		t.Fatal("Authorization.ConsumedAt is nil after a successful consume — the real service must record consumption")
	}

	// Call 3: REPLAY — the same AuthorizationID presented again. Required
	// test "second authorization replay rejected": this MUST fail, proven
	// against the real, storage-backed exactly-once check
	// (predictor-10-hardened markAuthorizationConsumed's conditional
	// UPDATE ... WHERE consumed_at IS NULL), not a fake merely asserting
	// it would.
	_, err = orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		TurnID: "turn-1", PromptHash: "hash-1", AuthorizationID: authID,
	})
	if err == nil {
		t.Fatal("DecisionAllowCmd (consume, REPLAY) succeeded — a second consumption of the same authorization must be rejected")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("replay err = %v (%T), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeConflict {
		t.Errorf("replay err.Code = %q, want %q (already consumed)", derr.Code, domain.ErrCodeConflict)
	}
}

// TestDecisionAllow_ReplayRejectedAcrossManySequentialAttempts extends the
// single-replay proof to a tight sequential loop (mirrors predictor-10's
// own TestConsumeAuthorization_ReplayRejected_TightSequentialLoop, at this
// orchestrator layer instead) — only the FIRST of many resubmission
// attempts against the same AuthorizationID may ever succeed, regardless
// of how many times a caller (mis-)retries.
func TestDecisionAllow_ReplayRejectedAcrossManySequentialAttempts(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, newHighRiskDataSource())
	ctx := context.Background()
	deps := orchestrator.DecisionDeps{Evaluation: svc, Issuer: svc}

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	issueResult, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		EvaluationID: eval.ID, TurnID: "turn-1", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (issue): %v", err)
	}
	authID := issueResult.Authorization.ID

	successes := 0
	const attempts = 20
	for i := 0; i < attempts; i++ {
		_, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
			TurnID: "turn-1", PromptHash: "hash-1", AuthorizationID: authID,
		})
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successes across %d sequential consume attempts = %d, want exactly 1", attempts, successes)
	}
}

// TestDecisionAllow_ConsumeFlow_WrongTurnRejected proves the consume flow
// threads TurnID/PromptHash through to the real ConsumeAuthorization
// binding check (not just the AuthorizationID) — a resubmission claiming a
// different TurnID than the one the authorization was actually issued for
// must be rejected, exactly like predictor-10's own
// TestConsumeAuthorization_RejectsWrongSession/RejectsWrongPrompt prove at
// the evaluation-package layer.
func TestDecisionAllow_ConsumeFlow_WrongTurnRejected(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, newHighRiskDataSource())
	ctx := context.Background()
	deps := orchestrator.DecisionDeps{Evaluation: svc, Issuer: svc}

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	issueResult, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		EvaluationID: eval.ID, TurnID: "turn-1", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (issue): %v", err)
	}

	_, err = orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		TurnID: "turn-WRONG", PromptHash: "hash-1", AuthorizationID: issueResult.Authorization.ID,
	})
	if err == nil {
		t.Fatal("DecisionAllowCmd (consume, wrong TurnID) succeeded — must be rejected")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnauthorized {
		t.Fatalf("err = %v, want ErrCodeUnauthorized (wrong turn binding)", err)
	}
}

// --- Required test: "checkpoint failure does not issue authorization"
// (as applicable to this command's flow) -------------------------------
//
// DecisionAllowCmd's issue flow calls Decide THEN IssueAuthorization, in
// that order, propagating a Decide failure immediately (proven by the
// fake-based TestDecisionAllowCmd_IssueFlow_DecideErrorPropagatesNeverCallsIssuer
// in decision_test.go). Here, using the REAL service, "checkpoint failure"
// concretely means: an evaluation whose EvaluateTurn/Decide never
// completed (e.g. because the caller's own prerequisite `checkpoint
// create` step failed upstream and the caller therefore never obtained a
// valid EvaluationID at all) cannot reach IssueAuthorization — proven here
// by calling DecisionAllowCmd's issue flow against an EvaluationID that was
// never actually produced by a successful EvaluateTurn (the direct
// analogue of "the prerequisite step failed, so this ID does not
// legitimately exist"), and confirming IssueAuthorization is never reached.
func TestDecisionAllow_UnknownEvaluationIDNeverIssuesAuthorization(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, newHighRiskDataSource())
	ctx := context.Background()
	deps := orchestrator.DecisionDeps{Evaluation: svc, Issuer: svc}

	_, err := orchestrator.DecisionAllowCmd(ctx, deps, orchestrator.DecisionAllowRequest{
		EvaluationID: "eval-never-existed", TurnID: "turn-1", PromptHash: "hash-1",
	})
	if err == nil {
		t.Fatal("DecisionAllowCmd succeeded against an EvaluationID that was never produced by a real EvaluateTurn/checkpoint-gated flow")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want ErrCodeNotFound (Decide's own read-back correctly fails closed before any authorization could be issued)", err)
	}
}

// TestDecisionAllow_CheckpointCreateFailureSequencing_NeverIssuesAuthorization
// exercises the literal, full-pipeline shape of the required test
// "checkpoint failure does not issue authorization": a realistic caller
// sequence is EvaluateTurn -> Decide (PolicyCheckpointAndRun) ->
// CheckpointCreate (this package's own runtime-b05 command, State then
// Repository) -> only THEN DecisionAllowCmd's issue flow, threading the
// resulting RepositoryCheckpointID through. If CheckpointCreate fails, a
// correctly-written caller never reaches the DecisionAllowCmd call at all
// — proven directly here (not merely inferred): this test's own control
// flow stops at CheckpointCreate's error and asserts IssueAuthorization is
// never invoked, using a spy Issuer that fails the test if called.
func TestDecisionAllow_CheckpointCreateFailureSequencing_NeverIssuesAuthorization(t *testing.T) {
	clk := realauthClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	svc := newRealEvaluationService(t, clk, &realauthIDs{prefix: "id"}, newHighRiskDataSource())
	ctx := context.Background()

	eval, err := svc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: "sess-1", TurnID: "turn-1", Provider: "claude", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := svc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyCheckpointAndRun {
		t.Fatalf("Decide.Action = %q, want %q (this test's fixture is the same critical-band one as the flow test above)", decision.Action, app.PolicyCheckpointAndRun)
	}

	// CheckpointCreate fails at its FIRST step (State) — mirrors
	// checkpoint_test.go's own TestCheckpointCreate_StateFailureNeverCallsRepository
	// precedent for the ordering guarantee itself; this test's own concern
	// is what happens ONE LEVEL UP, at the caller sequencing this
	// orchestrator's own commands.
	wantCheckpointErr := &domain.Error{Code: domain.ErrCodeUnavailable, Message: "state checkpoint store down", Retryable: true}
	checkpointDeps := orchestrator.CheckpointCreateDeps{
		StateCheckpoint: &fakes.FakeStateCheckpointService{
			CreateFunc: func(context.Context, app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				return domain.StateCheckpoint{}, wantCheckpointErr
			},
		},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{
			CreateFunc: func(context.Context, app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
				t.Fatal("RepositoryCheckpoint.Create was called despite State already failing")
				return app.RepositoryCheckpoint{}, nil
			},
		},
	}
	_, checkpointErr := orchestrator.CheckpointCreate(ctx, checkpointDeps, orchestrator.CheckpointCreateRequest{
		TaskID: "task-1", WorktreeID: "wt-1",
	})
	if !errors.Is(checkpointErr, error(wantCheckpointErr)) {
		t.Fatalf("CheckpointCreate err = %v, want the exact injected State failure", checkpointErr)
	}

	// A correctly-written caller stops here: CheckpointCreate failed, so it
	// never calls DecisionAllowCmd's issue flow at all. Model that exact
	// caller shape directly (rather than merely asserting it in prose): a
	// small helper mirrors what a real CLI/daemon call site does — call
	// CheckpointCreate, and only proceed to DecisionAllowCmd if it
	// succeeded — wired against a spy Issuer that fails the test if ever
	// invoked, then drive it with THIS test's failing checkpointDeps.
	spyIssuer := &fakeAuthorizationIssuer{
		issueFunc: func(context.Context, domain.TurnID, string, string, string, *domain.RepositoryCheckpointID) (app.Authorization, error) {
			t.Fatal("IssueAuthorization must never be called when the prerequisite CheckpointCreate step failed")
			return app.Authorization{}, nil
		},
	}
	deps := orchestrator.DecisionDeps{Evaluation: svc, Issuer: spyIssuer}

	err = checkpointThenDecisionAllow(ctx, checkpointDeps, deps, orchestrator.CheckpointCreateRequest{
		TaskID: "task-1", WorktreeID: "wt-1",
	}, orchestrator.DecisionAllowRequest{
		EvaluationID: eval.ID, TurnID: "turn-1", PromptHash: "hash-1",
	})
	if !errors.Is(err, error(wantCheckpointErr)) {
		t.Fatalf("checkpointThenDecisionAllow err = %v, want the exact CheckpointCreate failure propagated, with DecisionAllowCmd/IssueAuthorization never reached", err)
	}
}

// checkpointThenDecisionAllow models the realistic caller sequence this
// required test's name describes: call CheckpointCreate, and only proceed
// to DecisionAllowCmd's issue flow (threading the resulting
// RepositoryCheckpointID through, per DecisionAllowRequest's own doc
// comment on why this command does not create a checkpoint itself) if it
// succeeded. A CheckpointCreate failure short-circuits and returns
// immediately, exactly like this package's own CheckpointCreate/Evaluate/
// PersistPhase precedents all do at their own internal step boundaries.
func checkpointThenDecisionAllow(ctx context.Context, checkpointDeps orchestrator.CheckpointCreateDeps, decisionDeps orchestrator.DecisionDeps, checkpointReq orchestrator.CheckpointCreateRequest, decisionReq orchestrator.DecisionAllowRequest) error {
	checkpointResult, err := orchestrator.CheckpointCreate(ctx, checkpointDeps, checkpointReq)
	if err != nil {
		return err
	}
	decisionReq.RepositoryCheckpointID = &checkpointResult.Repository.ID
	_, err = orchestrator.DecisionAllowCmd(ctx, decisionDeps, decisionReq)
	return err
}
