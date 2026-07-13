// Package statecheckpoint implements checkpoint role Part A's State
// Checkpoint manifest (agents/checkpoint.md Part A deliverable #5;
// Auspex_ADD.md §18.8, Appendix B; migrations/0023_state_checkpoints.sql).
//
// A State Checkpoint is the canonical, durable, replayable snapshot of a
// task's Progress Tree at a semantic boundary (ADR-029: "state checkpoint
// at every semantic boundary" — completion of a node, above all others).
// This package owns the manifest's Go shape, its deterministic JSON
// serialization, and its integrity checksum; it does NOT itself decide
// *when* to create a checkpoint or run inside CompleteNode's transaction
// (that orchestration is internal/progress's CompleteNode protocol,
// checkpoint-a04) — mirroring how internal/artifacts is the pure
// validation seam CompleteNode calls into rather than a caller of it.
package statecheckpoint

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// SchemaVersion is the frozen wire schema-version string for a State
// Checkpoint manifest (CONTRACT_FREEZE.md, auspex.state-checkpoint.v1).
const SchemaVersion = "auspex.state-checkpoint.v1"

// ProgressTreeSummary mirrors Appendix B's `progress_tree` block: the
// Progress Tree's shape at the moment this checkpoint was taken.
type ProgressTreeSummary struct {
	Version          int64                   `json:"version"`
	ActiveNodeID     *domain.ProgressNodeID  `json:"active_node_id,omitempty"`
	CompletedNodeIDs []domain.ProgressNodeID `json:"completed_node_ids"`
	PausedNodeIDs    []domain.ProgressNodeID `json:"paused_node_ids"`
}

// ArtifactSummary mirrors one entry of Appendix B's `artifacts` array.
type ArtifactSummary struct {
	ID               string `json:"id"`
	URI              string `json:"uri"`
	MediaType        string `json:"media_type,omitempty"`
	Bytes            int64  `json:"bytes"`
	SHA256           string `json:"sha256"`
	ValidationStatus string `json:"validation_status"`
}

// RepositoryInfo mirrors Appendix B's `repository` block. Every field is
// optional: a checkpoint MAY be created before any repository evidence
// exists for the task (e.g. the very first node of a fresh task).
type RepositoryInfo struct {
	RepositoryID     string `json:"repository_id,omitempty"`
	WorktreeID       string `json:"worktree_id,omitempty"`
	GitHead          string `json:"git_head,omitempty"`
	Branch           string `json:"branch,omitempty"`
	IndexDiffHash    string `json:"index_diff_hash,omitempty"`
	WorktreeDiffHash string `json:"worktree_diff_hash,omitempty"`
}

// ProviderInfo mirrors Appendix B's `provider` block.
type ProviderInfo struct {
	Name           string `json:"name,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	InvocationMode string `json:"invocation_mode,omitempty"`
}

// QuotaObservationRef mirrors one entry of Appendix B's `quota.observations`.
type QuotaObservationRef struct {
	LimitID     string     `json:"limit_id"`
	UsedPercent float64    `json:"used_percent"`
	WindowSecs  int64      `json:"window_seconds,omitempty"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

// ContextInfo mirrors Appendix B's `context` block.
type ContextInfo struct {
	UsedPercent *float64 `json:"used_percent,omitempty"`
}

// NextActionInfo mirrors Appendix B's `next_action` block.
type NextActionInfo struct {
	NodeID      *domain.ProgressNodeID `json:"node_id,omitempty"`
	Description string                 `json:"description,omitempty"`
}

// ResumeInfo mirrors Appendix B's `resume` block.
type ResumeInfo struct {
	StrategyOrder  []string `json:"strategy_order,omitempty"`
	PermissionMode string   `json:"permission_mode,omitempty"`
}

// Manifest is the Go-level representation of a full State Checkpoint
// manifest document (Appendix B). IntegritySHA256 is deliberately excluded
// from the struct that gets hashed (see Digest) but IS a field here so a
// fully-populated Manifest can be serialized as the final wire document
// with its own checksum embedded, matching Appendix B's example verbatim.
type Manifest struct {
	SchemaVersion   string                   `json:"schema_version"`
	CheckpointID    domain.StateCheckpointID `json:"checkpoint_id"`
	TaskID          domain.TaskID            `json:"task_id"`
	CreatedAt       time.Time                `json:"created_at"`
	ProgressTree    ProgressTreeSummary      `json:"progress_tree"`
	Artifacts       []ArtifactSummary        `json:"artifacts"`
	Repository      RepositoryInfo           `json:"repository"`
	Provider        ProviderInfo             `json:"provider"`
	Quota           []QuotaObservationRef    `json:"quota,omitempty"`
	Context         ContextInfo              `json:"context"`
	NextAction      NextActionInfo           `json:"next_action"`
	Resume          ResumeInfo               `json:"resume"`
	IntegritySHA256 string                   `json:"integrity_sha256"`
}
