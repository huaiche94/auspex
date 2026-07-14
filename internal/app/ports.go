// Package app holds the frozen cross-component ports (ADD §9.9, §9.10).
// Interfaces here are intentionally narrow — do not widen one into a
// God interface that only a subset of implementations can satisfy
// (Constitution §4, agents/contract-integrator.md).
package app

import (
	"context"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
)

// --- Storage transaction boundary -----------------------------------------

// TxFunc runs inside a single storage transaction. Returning an error rolls
// the transaction back; the caller commits only on nil error.
type TxFunc func(ctx context.Context) error

type TxRunner interface {
	WithTx(ctx context.Context, fn TxFunc) error
}

// --- Evaluation / prediction / policy DTOs ---------------------------------

type EvaluateTurnRequest struct {
	SessionID  domain.SessionID
	TurnID     domain.TurnID
	Provider   string
	PromptHash string
}

type Evaluation struct {
	ID          domain.EvaluationID
	TurnID      domain.TurnID
	CreatedAt   time.Time
	Calibrated  bool
	Confidence  domain.Confidence
	ReasonCodes []domain.ReasonCode
}

type DecideRequest struct {
	EvaluationID domain.EvaluationID
}

type PolicyAction string

const (
	PolicyRun                 PolicyAction = "RUN"
	PolicyWarn                PolicyAction = "WARN"
	PolicyRequireConfirmation PolicyAction = "REQUIRE_CONFIRMATION"
	PolicyCheckpointAndRun    PolicyAction = "CHECKPOINT_AND_RUN"
	PolicySplit               PolicyAction = "SPLIT"
	PolicyPause               PolicyAction = "PAUSE"
	PolicyPauseAndAutoResume  PolicyAction = "PAUSE_AND_AUTO_RESUME"
	PolicyBlock               PolicyAction = "BLOCK"
)

type DecisionResult struct {
	ID     domain.DecisionID
	Action PolicyAction
}

type Authorization struct {
	ID                     string
	TurnID                 domain.TurnID
	PromptHash             string
	SnapshotFingerprint    string
	Decision               string
	RepositoryCheckpointID *domain.RepositoryCheckpointID
	IssuedAt               time.Time
	ExpiresAt              time.Time
	ConsumedAt             *time.Time
}

type ConsumeAuthorizationRequest struct {
	AuthorizationID string
	TurnID          domain.TurnID
	PromptHash      string
}

// EvaluationService is the frozen evaluate/decide/authorize contract
// (ADD §9.9).
type EvaluationService interface {
	EvaluateTurn(ctx context.Context, req EvaluateTurnRequest) (Evaluation, error)
	GetEvaluation(ctx context.Context, id domain.EvaluationID) (Evaluation, error)
	Decide(ctx context.Context, req DecideRequest) (DecisionResult, error)
	ConsumeAuthorization(ctx context.Context, req ConsumeAuthorizationRequest) (Authorization, error)
}

// --- Predictor pipeline ports (ADR-041) -------------------------------------
//
// Scope Estimator -> Token Forecaster -> Quota Forecaster -> Risk Combiner
// -> Policy. Runway Predictor (see GracefulPauseService.Observe) is
// independent of this chain — it answers a different question (is a quota
// limit imminent within a short horizon) using a live burn-rate model
// (ADD §15.4-15.5), not the per-turn forecast produced here.
//
// Each stage is a narrow, swappable interface: a Rule/Statistical/ML
// implementation of any one stage can replace it without touching the
// others (ADD §1.4; Auspex_Predictor_Design_Supplement.md's
// Version 1/2/3 evolutionary roadmap).

type EstimateScopeRequest struct {
	SessionID    domain.SessionID
	TaskID       *domain.TaskID
	RepositoryID domain.RepositoryID
}

// ScopeEstimator is the pipeline's Stage 1: predicts what work a turn will
// require (ADD §14).
type ScopeEstimator interface {
	EstimateScope(ctx context.Context, req EstimateScopeRequest) (domain.ScopeEstimate, error)
}

type ForecastTokensRequest struct {
	SessionID domain.SessionID
	Scope     domain.ScopeEstimate
}

// TokenForecaster is the pipeline's Stage 2: predicts total token cost of
// the upcoming turn from its scope estimate (ADD §15.1-15.2).
type TokenForecaster interface {
	ForecastTokens(ctx context.Context, req ForecastTokensRequest) (domain.TokenForecast, error)
}

type ForecastQuotaRequest struct {
	SessionID     domain.SessionID
	TokenForecast domain.TokenForecast
	Quota         []domain.QuotaObservation
	Context       domain.ContextObservation
}

// QuotaForecaster is the pipeline's Stage 3: projects provider-quota and
// context-window position after the upcoming turn (ADD §15.3, §15.9). MAY
// use TokenForecast as a fallback input when the provider does not expose
// quota percentage directly; MUST NOT require it when the provider does.
type QuotaForecaster interface {
	ForecastQuota(ctx context.Context, req ForecastQuotaRequest) (domain.QuotaForecast, error)
}

type CombineRiskRequest struct {
	Scope         domain.ScopeEstimate
	TokenForecast domain.TokenForecast
	QuotaForecast domain.QuotaForecast
}

type CombineRiskResult struct {
	QuotaRisk       domain.RiskComponent
	ContextRisk     domain.RiskComponent
	CompletionRisk  domain.RiskComponent
	BlastRadiusRisk domain.RiskComponent
	OverallRisk     domain.RiskComponent
}

// RiskCombiner is the pipeline's Stage 4: combines scope, token forecast,
// and quota forecast into the four named risk components plus an overall
// score (ADD §16.1-16.2). Does not consume RunwayForecast — see the
// package-level comment above.
type RiskCombiner interface {
	Combine(ctx context.Context, req CombineRiskRequest) (CombineRiskResult, error)
}

// --- Progress Tree DTOs -----------------------------------------------------

type CreateTaskRequest struct {
	WorktreeID    domain.WorktreeID
	SessionID     *domain.SessionID
	ObjectiveHash string
}

type Task struct {
	ID     domain.TaskID
	Status string
}

type UpsertPlanRequest struct {
	TaskID domain.TaskID
}

type ProgressTree struct {
	TaskID  domain.TaskID
	Version int64
}

type StartNodeRequest struct {
	NodeID domain.ProgressNodeID
}

type ProgressNode struct {
	ID     domain.ProgressNodeID
	TaskID domain.TaskID
	Status domain.ProgressNodeStatus
	Kind   domain.ProgressNodeKind
}

type CompleteNodeRequest struct {
	NodeID         domain.ProgressNodeID
	IdempotencyKey string
	Artifacts      []domain.ArtifactRef
}

type FailNodeRequest struct {
	NodeID       domain.ProgressNodeID
	FailureClass domain.FailureClass
}

type ProgressTreeSnapshot struct {
	TaskID domain.TaskID
	Nodes  []ProgressNode
}

type ReconcileProgressRequest struct {
	TaskID domain.TaskID
}

type ReconcileResult struct {
	TaskID          domain.TaskID
	ReconciledNodes []domain.ProgressNodeID
}

// ProgressTreeService is the frozen Progress Tree contract (ADD §9.9).
// The Progress Tree is canonical task state (Constitution §6) — it does not
// import provider adapters directly, only normalized events.
type ProgressTreeService interface {
	CreateTask(ctx context.Context, req CreateTaskRequest) (Task, error)
	UpsertPlan(ctx context.Context, req UpsertPlanRequest) (ProgressTree, error)
	StartNode(ctx context.Context, req StartNodeRequest) (ProgressNode, error)
	CompleteNode(ctx context.Context, req CompleteNodeRequest) (ProgressNode, domain.StateCheckpoint, error)
	FailNode(ctx context.Context, req FailNodeRequest) (ProgressNode, error)
	Snapshot(ctx context.Context, taskID domain.TaskID) (ProgressTreeSnapshot, error)
	Reconcile(ctx context.Context, req ReconcileProgressRequest) (ReconcileResult, error)
}

// --- State Checkpoint DTOs ---------------------------------------------------

type CreateStateCheckpointRequest struct {
	TaskID domain.TaskID
}

type StateCheckpointVerification struct {
	ID    domain.StateCheckpointID
	Valid bool
}

type StateCheckpointService interface {
	Create(ctx context.Context, req CreateStateCheckpointRequest) (domain.StateCheckpoint, error)
	LoadLatest(ctx context.Context, taskID domain.TaskID) (domain.StateCheckpoint, error)
	Verify(ctx context.Context, id domain.StateCheckpointID) (StateCheckpointVerification, error)
}

// --- Graceful Pause DTOs ------------------------------------------------------

type RuntimeObservation struct {
	SessionID domain.SessionID
	Quota     domain.QuotaObservation
}

type PauseRequest struct {
	SessionID domain.SessionID
	Reason    string
}

type PauseRecord struct {
	ID     domain.PauseID
	Status domain.PauseStatus
}

type SafePoint struct {
	PauseID domain.PauseID
	At      time.Time
}

type WakeJob struct {
	ID       domain.WakeJobID
	PauseID  domain.PauseID
	RunAfter time.Time
}

type ResumeRequest struct {
	PauseID domain.PauseID
}

type ResumeResult struct {
	PauseID domain.PauseID
	Status  domain.PauseStatus
}

type GracefulPauseService interface {
	Observe(ctx context.Context, obs RuntimeObservation) (domain.RunwayForecast, error)
	RequestPause(ctx context.Context, req PauseRequest) (PauseRecord, error)
	ReachSafePoint(ctx context.Context, sp SafePoint) (PauseRecord, error)
	EnterSleep(ctx context.Context, id domain.PauseID) (WakeJob, error)
	Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error)
	Cancel(ctx context.Context, id domain.PauseID) error
}

// --- Repository Checkpoint DTOs -----------------------------------------------

type CreateRepositoryCheckpointRequest struct {
	WorktreeID domain.WorktreeID
	TaskID     *domain.TaskID
}

type RepositoryCheckpoint struct {
	ID      domain.RepositoryCheckpointID
	GitHead string
	Status  string
}

type RepositoryCheckpointVerification struct {
	ID    domain.RepositoryCheckpointID
	Valid bool
}

type RestoreRepositoryCheckpointRequest struct {
	ID         domain.RepositoryCheckpointID
	AllowDirty bool
}

type RestoreResult struct {
	ID      domain.RepositoryCheckpointID
	Applied bool
}

type RepositoryCheckpointService interface {
	Create(ctx context.Context, req CreateRepositoryCheckpointRequest) (RepositoryCheckpoint, error)
	Verify(ctx context.Context, id domain.RepositoryCheckpointID) (RepositoryCheckpointVerification, error)
	Restore(ctx context.Context, req RestoreRepositoryCheckpointRequest) (RestoreResult, error)
}

// --- Provider interfaces (ADD §9.10) — narrow, segregated by capability -----

type ProviderInstallation struct {
	Provider string
	Version  string
	Path     string
}

type RawHookEvent struct {
	Provider string
	Kind     string
	Payload  []byte
}

type HookResponse struct {
	Allow   bool
	Reason  string
	Payload map[string]any
}

type ProviderDetector interface {
	Detect(ctx context.Context) (ProviderInstallation, error)
}

type ProviderCapabilityReader interface {
	Capabilities(ctx context.Context, installation ProviderInstallation) (domain.ProviderCapabilities, error)
}

type HookNormalizer interface {
	NormalizeHook(ctx context.Context, raw RawHookEvent) ([]any, HookResponse, error)
}

type RunRequest struct {
	Provider string
	Prompt   string
}

type RunHandle struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
}

type ManagedRunner interface {
	Start(ctx context.Context, req RunRequest) (RunHandle, error)
}

type ProviderEvent struct {
	Kind    string
	Payload []byte
}

type LiveObserver interface {
	Observe(ctx context.Context, handle RunHandle) (<-chan ProviderEvent, error)
}

type RunLocator struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
}

type TurnInterrupter interface {
	Interrupt(ctx context.Context, locator RunLocator) error
}

type ResumeProviderRequest struct {
	SessionID domain.SessionID
}

type SessionResumer interface {
	Resume(ctx context.Context, req ResumeProviderRequest) (RunHandle, error)
}

type QuotaRequest struct {
	SessionID domain.SessionID
	Provider  string
}

type QuotaReader interface {
	ReadQuota(ctx context.Context, req QuotaRequest) ([]domain.QuotaObservation, error)
}

// --- Feature lookup (frozen 2026-07-13 per ADR-044 / REC-01) ----------------

// ResolvedSession is FeatureDataSource.Resolve's return value.
type ResolvedSession struct {
	RepositoryID domain.RepositoryID
	TaskID       *domain.TaskID
}

// FeatureDataSource is the frozen repository/session/progress feature-lookup
// port (ADR-044, closing wave2-analysis REC-01). It resolves every input the
// evaluation pipeline needs beyond what EvaluateTurnRequest itself carries.
// The privacy contract holds here as everywhere: no raw prompt text, only
// derived features and hashes.
//
// A zero-value/ok=false return from any method means "not available yet"
// (cold-start), not an error — consumers degrade per ADD principle 1
// ("unknown is not zero"). Consumers that need only a subset SHOULD keep
// depending on their own narrow package-local view of this port
// (internal/predictor/scope.FeatureSource, internal/predictor/token.
// FeatureSource) rather than importing the full interface — the freeze fixes
// the canonical shape, not the consumption pattern.
type FeatureDataSource interface {
	// Resolve returns the RepositoryID and (optional) TaskID a session
	// belongs to.
	Resolve(ctx context.Context, sessionID domain.SessionID) (ResolvedSession, error)

	// Classification returns the task classifier's output for the current
	// turn plus the prompt features it was derived from.
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
	// recent turns matching the token forecaster's "similar" cohort
	// (ADD §15.2), selected via the provider/model/effort fallback
	// ladder, plus which ladder rung answered (#20 Phase 1, ADR-047 —
	// the amendment that widened this method's return from a bare
	// []float64 so rung selection is reason-codeable).
	RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) (features.SimilarTurnTokens, error)

	// Quota returns the current quota observations for a session (Stage 3
	// input, ForecastQuotaRequest.Quota).
	Quota(ctx context.Context, sessionID domain.SessionID) ([]domain.QuotaObservation, error)

	// Context returns the current context-window observation for a session
	// (Stage 3 input, ForecastQuotaRequest.Context).
	Context(ctx context.Context, sessionID domain.SessionID) (domain.ContextObservation, error)

	// RunwayForecast returns the most recently computed independent Runway
	// Predictor output for a session, if any. ok=false means none exists
	// yet. Evaluation never computes a RunwayForecast itself (ADR-041:
	// Runway is owned by GracefulPauseService.Observe).
	RunwayForecast(ctx context.Context, sessionID domain.SessionID) (domain.RunwayForecast, bool, error)

	// PriorRunwayHitConfirmed returns the policy debounce bit (ADD §17.6):
	// whether the immediately preceding evaluation for this session already
	// saw a qualifying calibrated hit-probability.
	PriorRunwayHitConfirmed(ctx context.Context, sessionID domain.SessionID) (bool, error)
}
