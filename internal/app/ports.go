// Package app holds the frozen cross-component ports (ADD §9.9, §9.10).
// Interfaces here are intentionally narrow — do not widen one into a
// God interface that only a subset of implementations can satisfy
// (Constitution §4, agents/contract-integrator.md).
package app

import (
	"context"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
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
	ReasonCodes []string
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
