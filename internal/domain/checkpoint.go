package domain

import "time"

type NextAction struct {
	Description string
	NodeID      *ProgressNodeID
}

type StateCheckpoint struct {
	ID                     StateCheckpointID
	TaskID                 TaskID
	ProgressTreeVersion    int64
	ActiveNodeID           *ProgressNodeID
	CompletedNodeIDs       []ProgressNodeID
	NextAction             NextAction
	RepositorySnapshotID   string
	ProviderSessionID      string
	ProviderTurnID         string
	LatestQuotaIDs         []string
	LatestContextID        string
	RepositoryCheckpointID *RepositoryCheckpointID
	CreatedAt              time.Time
	IntegritySHA256        string
}
