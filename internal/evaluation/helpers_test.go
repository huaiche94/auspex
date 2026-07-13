package evaluation_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/features"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// --- fakes: deterministic Clock/IDGenerator, mirroring
// internal/scheduler/lease_test.go's own local-fake pattern (this
// codebase's established convention: every package builds its own small
// fakes rather than sharing a package-wide test double). ---

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var _ domain.Clock = (*fakeClock)(nil)

type sequentialIDs struct {
	counter atomic.Int64
	prefix  string
}

func (g *sequentialIDs) NewID() string {
	return fmt.Sprintf("%s-%d", g.prefix, g.counter.Add(1))
}

var _ domain.IDGenerator = (*sequentialIDs)(nil)

// --- test DB setup -----------------------------------------------------

func openMigratedDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
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

// --- fakeDataSource: a configurable, deterministic evaluation.DataSource ---

type fakeDataSource struct {
	repositoryID domain.RepositoryID
	taskID       *domain.TaskID

	classification features.Classification
	promptFeatures features.PromptFeatures

	repoFeatures features.RepositoryFeatures
	repoOK       bool

	sessFeatures features.SessionFeatures
	sessOK       bool

	progFeatures features.ProgressFeatures
	progOK       bool

	similarTokens []float64

	quotaObs   []domain.QuotaObservation
	contextObs domain.ContextObservation

	runway    domain.RunwayForecast
	hasRunway bool

	priorConfirmed bool

	// resolveErr, when non-nil, is returned by Resolve to exercise the
	// pipeline's error propagation path.
	resolveErr error

	// The remaining err fields, when non-nil, are returned by their
	// corresponding method, in place of a successful result — used by
	// predictor-11's adversarial fail-open/fail-closed suite
	// (pipeline_e2e_test.go) to simulate a failure at every possible
	// DataSource hand-off, not just Resolve (predictor-09's original
	// scope).
	classificationErr          error
	repositoryErr              error
	sessionErr                 error
	progressErr                error
	recentSimilarTurnTokensErr error
	quotaErr                   error
	contextErr                 error
	runwayForecastErr          error
	priorRunwayHitConfirmedErr error
}

func newFakeDataSource() *fakeDataSource {
	return &fakeDataSource{
		repositoryID: domain.RepositoryID("repo-1"),
		classification: features.Classification{
			Class:      features.TaskClassBugfixLocal,
			Confidence: domain.ConfidenceLow,
		},
		promptFeatures: features.PromptFeatures{},
	}
}

func (f *fakeDataSource) Resolve(_ context.Context, _ domain.SessionID) (evaluation.ResolvedSession, error) {
	if f.resolveErr != nil {
		return evaluation.ResolvedSession{}, f.resolveErr
	}
	return evaluation.ResolvedSession{RepositoryID: f.repositoryID, TaskID: f.taskID}, nil
}

func (f *fakeDataSource) Classification(_ context.Context, _ domain.SessionID, _ *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	if f.classificationErr != nil {
		return features.Classification{}, features.PromptFeatures{}, f.classificationErr
	}
	return f.classification, f.promptFeatures, nil
}

func (f *fakeDataSource) Repository(_ context.Context, _ domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	if f.repositoryErr != nil {
		return features.RepositoryFeatures{}, false, f.repositoryErr
	}
	return f.repoFeatures, f.repoOK, nil
}

func (f *fakeDataSource) Session(_ context.Context, _ domain.SessionID) (features.SessionFeatures, bool, error) {
	if f.sessionErr != nil {
		return features.SessionFeatures{}, false, f.sessionErr
	}
	return f.sessFeatures, f.sessOK, nil
}

func (f *fakeDataSource) Progress(_ context.Context, _ *domain.TaskID) (features.ProgressFeatures, bool, error) {
	if f.progressErr != nil {
		return features.ProgressFeatures{}, false, f.progressErr
	}
	return f.progFeatures, f.progOK, nil
}

func (f *fakeDataSource) RecentSimilarTurnTokens(_ context.Context, _ domain.SessionID, _ features.TaskClass) ([]float64, error) {
	if f.recentSimilarTurnTokensErr != nil {
		return nil, f.recentSimilarTurnTokensErr
	}
	return f.similarTokens, nil
}

func (f *fakeDataSource) Quota(_ context.Context, _ domain.SessionID) ([]domain.QuotaObservation, error) {
	if f.quotaErr != nil {
		return nil, f.quotaErr
	}
	return f.quotaObs, nil
}

func (f *fakeDataSource) Context(_ context.Context, _ domain.SessionID) (domain.ContextObservation, error) {
	if f.contextErr != nil {
		return domain.ContextObservation{}, f.contextErr
	}
	return f.contextObs, nil
}

func (f *fakeDataSource) RunwayForecast(_ context.Context, _ domain.SessionID) (domain.RunwayForecast, bool, error) {
	if f.runwayForecastErr != nil {
		return domain.RunwayForecast{}, false, f.runwayForecastErr
	}
	return f.runway, f.hasRunway, nil
}

func (f *fakeDataSource) PriorRunwayHitConfirmed(_ context.Context, _ domain.SessionID) (bool, error) {
	if f.priorRunwayHitConfirmedErr != nil {
		return false, f.priorRunwayHitConfirmedErr
	}
	return f.priorConfirmed, nil
}

var _ evaluation.DataSource = (*fakeDataSource)(nil)

// newTestService wires a real evaluation.Service against real pipeline
// stage implementations (scope/token/quota/risk/policy — this package's
// own sibling predictor packages, exactly as a production wiring layer
// would use them), a fresh migrated in-temp-file SQLite DB, and the given
// clock/DataSource so tests get deterministic, real end-to-end behavior
// rather than mocking the pipeline itself.
func newTestService(t *testing.T, clk domain.Clock, ids domain.IDGenerator, source *fakeDataSource) (*evaluation.Service, *sqlite.DB) {
	t.Helper()
	db := openMigratedDB(t)

	svc := evaluation.New(
		db,
		source,
		scope.NewRuleScopeEstimator(scopeSourceAdapter{src: source}),
		token.NewRuleTokenForecaster(tokenSourceAdapter{src: source}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clk,
		ids,
	)
	return svc, db
}

// scopeSourceAdapter/tokenSourceAdapter adapt *fakeDataSource to
// internal/predictor/scope.FeatureSource and
// internal/predictor/token.FeatureSource respectively. Both stages'
// FeatureSource interfaces are shaped similarly to evaluation.DataSource
// but not identically (e.g. scope.FeatureSource.Progress takes a
// *domain.TaskID while token.FeatureSource.Progress takes a
// domain.SessionID) — the same divergence already documented on each
// stage's own FeatureSource doc comment (each pipeline stage owns its own
// narrow bridge interface, per that established precedent) — so this test
// helper adapts explicitly per method rather than relying on struct
// embedding to paper over the signature mismatch.
type scopeSourceAdapter struct{ src *fakeDataSource }

func (a scopeSourceAdapter) Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, sessionID, taskID)
}
func (a scopeSourceAdapter) Repository(ctx context.Context, repositoryID domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return a.src.Repository(ctx, repositoryID)
}
func (a scopeSourceAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, sessionID)
}
func (a scopeSourceAdapter) Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, taskID)
}

type tokenSourceAdapter struct{ src *fakeDataSource }

func (a tokenSourceAdapter) Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, sessionID, nil)
}
func (a tokenSourceAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, sessionID)
}
func (a tokenSourceAdapter) Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, a.src.taskID)
}
func (a tokenSourceAdapter) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) ([]float64, error) {
	return a.src.RecentSimilarTurnTokens(ctx, sessionID, class)
}

// --- predictor-11: error-injecting pipeline-stage wrappers -----------------
//
// Each of the four ADR-041 pipeline stages (ScopeEstimator, TokenForecaster,
// QuotaForecaster, RiskCombiner) is a narrow, swappable app interface. To
// adversarially test "what happens when ANY single upstream stage fails"
// (this node's highest-risk required test — fail-open/fail-closed, per
// EXECUTION_DAG.md's predictor-11 risk callout), each stage is wrapped here
// so a test can force that one stage to return an error while every other
// stage runs for real — proving the failure is handled at exactly the
// hand-off it's injected at, not merely "some error happened somewhere."

type errInjectingScopeEstimator struct {
	inner app.ScopeEstimator
	err   error
}

func (e errInjectingScopeEstimator) EstimateScope(ctx context.Context, req app.EstimateScopeRequest) (domain.ScopeEstimate, error) {
	if e.err != nil {
		return domain.ScopeEstimate{}, e.err
	}
	return e.inner.EstimateScope(ctx, req)
}

type errInjectingTokenForecaster struct {
	inner app.TokenForecaster
	err   error
	// nilResult, when true, returns a zero-value (all-nil-equivalent)
	// domain.TokenForecast with a nil error — simulates a degraded stage
	// that fails open with an empty/unknown result rather than erroring,
	// per this node's "TokenForecaster returns all-nil" scenario.
	nilResult bool
}

func (e errInjectingTokenForecaster) ForecastTokens(ctx context.Context, req app.ForecastTokensRequest) (domain.TokenForecast, error) {
	if e.err != nil {
		return domain.TokenForecast{}, e.err
	}
	if e.nilResult {
		return domain.TokenForecast{}, nil
	}
	return e.inner.ForecastTokens(ctx, req)
}

type errInjectingQuotaForecaster struct {
	inner app.QuotaForecaster
	err   error
	// timeout, when true, returns a context.DeadlineExceeded-flavored
	// domain.Error to simulate this node's "QuotaForecaster times out"
	// scenario specifically (distinct from a generic err).
	timeout bool
}

func (e errInjectingQuotaForecaster) ForecastQuota(ctx context.Context, req app.ForecastQuotaRequest) (domain.QuotaForecast, error) {
	if e.timeout {
		return domain.QuotaForecast{}, &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "quota forecaster: simulated timeout",
			Retryable: true,
		}
	}
	if e.err != nil {
		return domain.QuotaForecast{}, e.err
	}
	return e.inner.ForecastQuota(ctx, req)
}

type errInjectingRiskCombiner struct {
	inner app.RiskCombiner
	err   error
}

func (e errInjectingRiskCombiner) Combine(ctx context.Context, req app.CombineRiskRequest) (app.CombineRiskResult, error) {
	if e.err != nil {
		return app.CombineRiskResult{}, e.err
	}
	return e.inner.Combine(ctx, req)
}

// testStages bundles the four real pipeline-stage implementations (the
// same ones newTestService wires by default) so a test can selectively
// substitute an error-injecting wrapper for exactly one stage while
// leaving the other three real, via newTestServiceWithStages.
type testStages struct {
	Scope  app.ScopeEstimator
	Tokens app.TokenForecaster
	Quota  app.QuotaForecaster
	Risk   app.RiskCombiner
}

// realStages builds the same real scope/token/quota/risk stage chain
// newTestService uses, against source, so a test can wrap exactly one of
// them with an error injector while leaving the rest identical to
// production wiring.
func realStages(source *fakeDataSource) testStages {
	return testStages{
		Scope:  scope.NewRuleScopeEstimator(scopeSourceAdapter{src: source}),
		Tokens: token.NewRuleTokenForecaster(tokenSourceAdapter{src: source}),
		Quota:  quota.NewRuleQuotaForecaster(),
		Risk:   risk.NewRuleRiskCombiner(),
	}
}

// newTestServiceWithStages is newTestService's more flexible sibling: it
// takes an explicit testStages bundle (typically built by realStages and
// then selectively wrapped with one errInjecting* type) instead of always
// constructing the real chain internally.
func newTestServiceWithStages(t *testing.T, clk domain.Clock, ids domain.IDGenerator, source *fakeDataSource, stages testStages) (*evaluation.Service, *sqlite.DB) {
	t.Helper()
	db := openMigratedDB(t)

	svc := evaluation.New(
		db,
		source,
		stages.Scope,
		stages.Tokens,
		stages.Quota,
		stages.Risk,
		policy.NewDecider(),
		clk,
		ids,
	)
	return svc, db
}
