package evaluation

import (
	"context"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/features"
)

// DataSource resolves every input EvaluateTurn's pipeline needs beyond what
// the frozen app.EvaluateTurnRequest itself carries (SessionID, TurnID,
// Provider, PromptHash — CONTRACT_FREEZE.md's privacy contract: no raw
// prompt text ever reaches this package, only its hash). This is this
// package's own narrow, local interface, not a frozen internal/app port —
// mirrors internal/predictor/scope.FeatureSource and
// internal/predictor/token.FeatureSource's identical rationale exactly:
// Bootstrap deliberately deferred a repository/session/progress feature
// lookup port ("What Bootstrap did NOT freeze"), so this package depends on
// an abstraction it owns, satisfied by a test fake here and, in a later
// wave, by whatever concrete lookup a storage-backed role provides.
//
// A zero-value/ok=false return from any method means "not available yet"
// (cold-start), not an error — EvaluateTurn degrades to the same
// Confidence/Calibrated discipline every pipeline stage already uses for a
// missing input, per ADD principle 1 ("unknown is not zero").
type DataSource interface {
	// Resolve returns the RepositoryID and (optional) TaskID a session
	// belongs to. Needed because app.EstimateScopeRequest requires a
	// RepositoryID that EvaluateTurnRequest itself does not carry.
	Resolve(ctx context.Context, sessionID domain.SessionID) (ResolvedSession, error)

	// Classification returns the task classifier's output for the current
	// turn plus the prompt features it was derived from — the same two
	// values internal/predictor/scope.FeatureSource.Classification and
	// internal/predictor/token.FeatureSource.Classification each need.
	Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error)

	// Repository returns repository-derived features. ok=false means
	// cold-start.
	Repository(ctx context.Context, repositoryID domain.RepositoryID) (features.RepositoryFeatures, bool, error)

	// Session returns session-derived features (recent-turn quantiles,
	// retry rate, etc.). ok=false means cold-start.
	Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error)

	// Progress returns Progress-Tree-derived features for the current
	// task/node. ok=false means cold-start.
	Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error)

	// RecentSimilarTurnTokens returns raw total-token observations for
	// recent turns matching the token forecaster's own "similar" cohort
	// (ADD §15.2) — mirrors internal/predictor/token.FeatureSource's method
	// of the same name exactly.
	RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) ([]float64, error)

	// Quota returns the current quota observations for a session (Stage 3
	// input, app.ForecastQuotaRequest.Quota).
	Quota(ctx context.Context, sessionID domain.SessionID) ([]domain.QuotaObservation, error)

	// Context returns the current context-window observation for a session
	// (Stage 3 input, app.ForecastQuotaRequest.Context).
	Context(ctx context.Context, sessionID domain.SessionID) (domain.ContextObservation, error)

	// RunwayForecast returns the most recently computed independent Runway
	// Predictor output for a session (internal/predictor/runway.Scorer's
	// domain.RunwayForecast), if any. ok=false means none exists yet (a
	// brand-new session, or GracefulPauseService.Observe has not run) —
	// Decide's policy gate degrades to treating runway as not
	// pause-worthy, exactly like an uncalibrated/absent input anywhere
	// else in this pipeline. EvaluateTurn never computes a RunwayForecast
	// itself (ADR-041: Runway is independent of this chain, owned by
	// GracefulPauseService.Observe, a different role's frozen port).
	RunwayForecast(ctx context.Context, sessionID domain.SessionID) (domain.RunwayForecast, bool, error)

	// PriorRunwayHitConfirmed returns policy.DecideRequest's debounce bit
	// (ADD §17.6): whether the immediately preceding evaluation for this
	// session already saw a qualifying calibrated hit-probability. This
	// package is otherwise stateless per call, like every pipeline stage
	// it wires together — the caller (expected to be backed by this
	// package's own policy_decisions history) owns this one bit.
	PriorRunwayHitConfirmed(ctx context.Context, sessionID domain.SessionID) (bool, error)
}

// ResolvedSession is DataSource.Resolve's return value.
type ResolvedSession struct {
	RepositoryID domain.RepositoryID
	TaskID       *domain.TaskID
}
