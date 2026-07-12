// Package repocheckpoint implements checkpoint role Part B's Repository
// Checkpoint create/verify operations (agents/checkpoint.md Part B
// deliverable #4; Preflight_ADD.md §19; migrations/0030_repository_checkpoints.sql).
//
// A Repository Checkpoint captures exact working-tree evidence (patches +
// untracked file archive) before a pause or high-risk turn, WITHOUT ever
// mutating the active branch or working tree — capture is read-only Git
// plumbing (internal/gitx) plus filesystem writes strictly under the
// checkpoint's own artifact directory. This is Constitution §7 rule 6
// ("Repository checkpoints are atomic and never silently commit the active
// branch") and this node's own DAG risk note ("must never mutate the
// active branch") — the single hardest invariant in this package.
package repocheckpoint

import "time"

// ManifestSchemaVersion is the frozen wire schema-version string for a
// Repository Checkpoint manifest (CONTRACT_FREEZE.md,
// preflight.repository-checkpoint.v1; Preflight_ADD.md Appendix D).
const ManifestSchemaVersion = "preflight.repository-checkpoint.v1"

// Status is this package's checkpoint lifecycle vocabulary
// (repository_checkpoints.status, migrations/0030 — deliberately not
// CHECK-constrained, same immutable-DDL reasoning as every other role's
// status column in this schema).
type Status string

const (
	// StatusCapturing: a capture is in progress (artifacts are being
	// staged to a temp directory; not yet durable).
	StatusCapturing Status = "capturing"
	// StatusComplete: capture finished, artifacts committed atomically,
	// DB row written, all required files present.
	StatusComplete Status = "complete"
	// StatusPartial: capture finished but one or more required files were
	// skipped (ADD §19.5: "any required skipped file => partial
	// recoverability").
	StatusPartial Status = "partial"
	// StatusFailed: capture could not produce usable evidence (e.g. a
	// race was detected and the retry also failed).
	StatusFailed Status = "failed"
)

// Recoverability mirrors Appendix D's `recoverability.level` field: what a
// restore from this checkpoint could actually reconstruct.
type Recoverability string

const (
	RecoverabilityComplete Recoverability = "complete"
	RecoverabilityPartial  Recoverability = "partial"
	RecoverabilityNone     Recoverability = "none"
)

// RepositoryInfo is the manifest's `repository` block (Appendix D).
type RepositoryInfo struct {
	RepositoryID      string `json:"repository_id"`
	WorktreeID        string `json:"worktree_id"`
	GitHead           string `json:"git_head"`
	Branch            string `json:"branch"`
	RemoteFingerprint string `json:"remote_fingerprint,omitempty"`
}

// SnapshotInfo is the manifest's `snapshot` block: counts and hashes
// derived from the gitx.Fingerprint taken at capture time.
type SnapshotInfo struct {
	IndexDiffHash    string `json:"index_diff_hash"`
	WorktreeDiffHash string `json:"worktree_diff_hash"`
	StagedFiles      int    `json:"staged_files"`
	UnstagedFiles    int    `json:"unstaged_files"`
	UntrackedFiles   int    `json:"untracked_files"`
	LinesAdded       int    `json:"lines_added"`
	LinesDeleted     int    `json:"lines_deleted"`
}

// ArtifactFile is one entry of the manifest's `artifacts` array: a file
// this checkpoint wrote under its own artifact directory, with the digest
// and size needed to verify it later without trusting the filesystem.
type ArtifactFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// RecoverabilityInfo is the manifest's `recoverability` block.
type RecoverabilityInfo struct {
	Level            Recoverability `json:"level"`
	SkippedFileCount int            `json:"skipped_file_count"`
	Warnings         []string       `json:"warnings"`
}

// Manifest is the Go representation of manifest.json (ADD §19.2 artifact
// layout, Appendix D). It is the durable, checksummed record of exactly
// what a checkpoint captured; Verify recomputes and compares against it
// rather than trusting the DB row alone.
type Manifest struct {
	SchemaVersion  string             `json:"schema_version"`
	CheckpointID   string             `json:"checkpoint_id"`
	CreatedAt      time.Time          `json:"created_at"`
	Status         Status             `json:"status"`
	Repository     RepositoryInfo     `json:"repository"`
	Snapshot       SnapshotInfo       `json:"snapshot"`
	Artifacts      []ArtifactFile     `json:"artifacts"`
	Recoverability RecoverabilityInfo `json:"recoverability"`
}
