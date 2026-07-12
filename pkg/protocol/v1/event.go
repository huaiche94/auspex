// Package v1 is the frozen public wire protocol. Provider wire payloads
// MUST NOT leak into these types unnormalized (Constitution §7).
package v1

import "time"

const (
	SchemaVersionEvent                = "preflight.event.v1"
	SchemaVersionProgressTree         = "preflight.progress-tree.v1"
	SchemaVersionStateCheckpoint      = "preflight.state-checkpoint.v1"
	SchemaVersionRepositoryCheckpoint = "preflight.repository-checkpoint.v1"
	SchemaVersionPause                = "preflight.pause.v1"
	SchemaVersionAPI                  = "preflight.api.v1"
)

// EventType is a closed, versioned taxonomy (ADD §11.3). New event types
// require a contract-integrator change, not ad hoc strings from feature code.
type EventType string

const (
	// Session / turn
	EventProviderSessionStarted   EventType = "provider.session.started"
	EventProviderSessionResumed   EventType = "provider.session.resumed"
	EventProviderSessionCompacted EventType = "provider.session.compacted"
	EventProviderSessionStopped   EventType = "provider.session.stopped"
	EventProviderTurnStarted      EventType = "provider.turn.started"
	EventProviderTurnCompleted    EventType = "provider.turn.completed"
	EventProviderTurnFailed       EventType = "provider.turn.failed"
	EventProviderTurnInterrupted  EventType = "provider.turn.interrupted"

	// Evaluation
	EventEvaluationRequested   EventType = "preflight.evaluation.requested"
	EventFeaturesExtracted     EventType = "preflight.features.extracted"
	EventPredictionCreated     EventType = "preflight.prediction.created"
	EventPolicyDecided         EventType = "preflight.policy.decided"
	EventUserDecisionRecorded  EventType = "preflight.user_decision.recorded"
	EventAuthorizationCreated  EventType = "preflight.authorization.created"
	EventAuthorizationConsumed EventType = "preflight.authorization.consumed"

	// Progress / state
	EventProgressTreeCreated            EventType = "progress.tree.created"
	EventProgressTreeReconciled         EventType = "progress.tree.reconciled"
	EventProgressNodeReady              EventType = "progress.node.ready"
	EventProgressNodeStarted            EventType = "progress.node.started"
	EventProgressNodeArtifactObserved   EventType = "progress.node.artifact_observed"
	EventProgressNodeCheckpointing      EventType = "progress.node.checkpointing"
	EventProgressNodeCompleted          EventType = "progress.node.completed"
	EventProgressNodeFailed             EventType = "progress.node.failed"
	EventStateCheckpointCreationStarted EventType = "state.checkpoint.creation.started"
	EventStateCheckpointCreated         EventType = "state.checkpoint.created"
	EventStateCheckpointFailed          EventType = "state.checkpoint.failed"
	EventStateCheckpointVerified        EventType = "state.checkpoint.verified"

	// Runtime / pause
	EventRunwayForecastUpdated        EventType = "runway.forecast.updated"
	EventPauseThresholdCrossed        EventType = "pause.threshold.crossed"
	EventPauseRequested               EventType = "pause.requested"
	EventPauseSafePointReached        EventType = "pause.safe_point.reached"
	EventPauseCheckpointStarted       EventType = "pause.checkpoint.started"
	EventPauseCheckpointCompleted     EventType = "pause.checkpoint.completed"
	EventPauseProviderInterrupted     EventType = "pause.provider.interrupted"
	EventPauseEntered                 EventType = "pause.entered"
	EventPauseWakeScheduled           EventType = "pause.wake.scheduled"
	EventPauseWakeTriggered           EventType = "pause.wake.triggered"
	EventPauseResumeValidationStarted EventType = "pause.resume.validation.started"
	EventPauseResumeBlocked           EventType = "pause.resume.blocked"
	EventPauseResumeStarted           EventType = "pause.resume.started"
	EventPauseResumeCompleted         EventType = "pause.resume.completed"
	EventPauseCancelled               EventType = "pause.cancelled"
	EventPauseFailed                  EventType = "pause.failed"

	// Tool / Git / verification
	EventProviderToolStarted        EventType = "provider.tool.started"
	EventProviderToolCompleted      EventType = "provider.tool.completed"
	EventProviderToolFailed         EventType = "provider.tool.failed"
	EventProviderFileChangeObserved EventType = "provider.file_change.observed"
	EventRepositorySnapshotObserved EventType = "repository.snapshot.observed"
	EventRepositoryDiffObserved     EventType = "repository.diff.observed"
	EventVerificationObserved       EventType = "verification.observed"

	// Usage
	EventProviderUsageObserved   EventType = "provider.usage.observed"
	EventProviderContextObserved EventType = "provider.context.observed"
	EventProviderQuotaObserved   EventType = "provider.quota.observed"
	EventProviderRateLimitHit    EventType = "provider.rate_limit.hit"
)

// Event is the normalized envelope every provider payload is translated
// into before it reaches domain/storage code (ADD §11.1). Raw provider
// payloads MUST be redacted before this struct is populated.
type Event struct {
	SchemaVersion  string
	EventID        string
	EventType      EventType
	OccurredAt     time.Time
	ObservedAt     time.Time
	Sequence       int64
	IdempotencyKey string
	Source         string
	Provider       string
	RepositoryID   string
	WorktreeID     string
	SessionID      string
	TurnID         string
	TaskID         string
	ProgressNodeID string
	Payload        map[string]any
}
