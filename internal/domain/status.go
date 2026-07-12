package domain

type TurnStatus string

const (
	TurnPending      TurnStatus = "pending"
	TurnAuthorized   TurnStatus = "authorized"
	TurnRunning      TurnStatus = "running"
	TurnPausePending TurnStatus = "pause_pending"
	TurnPausing      TurnStatus = "pausing"
	TurnPaused       TurnStatus = "paused"
	TurnResuming     TurnStatus = "resuming"
	TurnCompleted    TurnStatus = "completed"
	TurnFailed       TurnStatus = "failed"
	TurnInterrupted  TurnStatus = "interrupted"
	TurnBlocked      TurnStatus = "blocked"
	TurnCancelled    TurnStatus = "cancelled"
)

type ProgressNodeStatus string

const (
	NodePending       ProgressNodeStatus = "pending"
	NodeReady         ProgressNodeStatus = "ready"
	NodeInProgress    ProgressNodeStatus = "in_progress"
	NodeCheckpointing ProgressNodeStatus = "checkpointing"
	NodePaused        ProgressNodeStatus = "paused"
	NodeCompleted     ProgressNodeStatus = "completed"
	NodeFailed        ProgressNodeStatus = "failed"
	NodeSkipped       ProgressNodeStatus = "skipped"
	NodeBlocked       ProgressNodeStatus = "blocked"
)

type ProgressNodeKind string

const (
	NodeDocumentSection ProgressNodeKind = "document_section"
	NodeCodeChange      ProgressNodeKind = "code_change"
	NodeTest            ProgressNodeKind = "test"
	NodeMigration       ProgressNodeKind = "migration"
	NodeInvestigation   ProgressNodeKind = "investigation"
	NodeDecision        ProgressNodeKind = "decision"
	NodeComposite       ProgressNodeKind = "composite"
)

type PauseStatus string

const (
	PausePredicted       PauseStatus = "predicted"
	PauseRequested       PauseStatus = "requested"
	PauseQuiescing       PauseStatus = "quiescing"
	PauseCheckpointing   PauseStatus = "checkpointing"
	PauseInterrupting    PauseStatus = "interrupting"
	PauseSleeping        PauseStatus = "sleeping"
	PauseWakePending     PauseStatus = "wake_pending"
	PauseValidating      PauseStatus = "validating"
	PauseResuming        PauseStatus = "resuming"
	PauseResumed         PauseStatus = "resumed"
	PauseBlockedConflict PauseStatus = "blocked_conflict"
	PauseCancelled       PauseStatus = "cancelled"
	PauseFailed          PauseStatus = "failed"
)
