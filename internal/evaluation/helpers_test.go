package evaluation_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/evaluation"
	"github.com/huaiche94/preflight/internal/features"
	"github.com/huaiche94/preflight/internal/policy"
	"github.com/huaiche94/preflight/internal/predictor/quota"
	"github.com/huaiche94/preflight/internal/predictor/risk"
	"github.com/huaiche94/preflight/internal/predictor/scope"
	"github.com/huaiche94/preflight/internal/predictor/token"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
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
	path := filepath.Join(dir, "preflight.db")
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
	return f.classification, f.promptFeatures, nil
}

func (f *fakeDataSource) Repository(_ context.Context, _ domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return f.repoFeatures, f.repoOK, nil
}

func (f *fakeDataSource) Session(_ context.Context, _ domain.SessionID) (features.SessionFeatures, bool, error) {
	return f.sessFeatures, f.sessOK, nil
}

func (f *fakeDataSource) Progress(_ context.Context, _ *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return f.progFeatures, f.progOK, nil
}

func (f *fakeDataSource) RecentSimilarTurnTokens(_ context.Context, _ domain.SessionID, _ features.TaskClass) ([]float64, error) {
	return f.similarTokens, nil
}

func (f *fakeDataSource) Quota(_ context.Context, _ domain.SessionID) ([]domain.QuotaObservation, error) {
	return f.quotaObs, nil
}

func (f *fakeDataSource) Context(_ context.Context, _ domain.SessionID) (domain.ContextObservation, error) {
	return f.contextObs, nil
}

func (f *fakeDataSource) RunwayForecast(_ context.Context, _ domain.SessionID) (domain.RunwayForecast, bool, error) {
	return f.runway, f.hasRunway, nil
}

func (f *fakeDataSource) PriorRunwayHitConfirmed(_ context.Context, _ domain.SessionID) (bool, error) {
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
