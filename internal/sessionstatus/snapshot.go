// Package sessionstatus assembles the read-only, per-session FR-162 view the
// M6 daemon's HTTP surface (internal/httpapi, issue #10) serves so the VS
// Code companion can render risk, runway, quota freshness, the progress
// tree, checkpoints, and pause state — the six sections FR-162 (ADD §8.4)
// names but the original auspex.daemon.status.v1 payload never exposed.
//
// This package is a read-model assembler and nothing more: it issues no
// writes, owns no table, and holds no state beyond its injected stores. It
// reuses every existing read where one already exists — evaluation's
// RunwayForecast/Quota/LatestRisk/Resolve, progress's Node/EdgeStore
// ListByTask, statecheckpoint's LoadLatest, repocheckpoint's Get, and
// scheduler's List — rather than duplicating their SQL, and issues its own
// read-only SELECTs only for the two shapes no owning store exposes (latest
// pause_records row for a session, and latest provider_sessions row).
//
// # Honesty invariant (ADD §8.8, Constitution principle #1)
//
// Unknown is never zero. Absent sections (risk/runway/checkpoint/pause)
// serialize as JSON null via pointer fields; sections with a natural empty
// (quota windows, progress nodes/edges) serialize as empty arrays, never an
// error. Every optional scalar the source leaves unknown (used_percent,
// burn rates, reset times, ...) is a pointer that renders null. Runway may
// be empty today (no production path persists runway_forecasts yet); this
// serves null honestly rather than fabricating a forecast.
//
// # Numbers and ids only (FR-171 / Constitution §7)
//
// The payload carries scores, counts, ids, hashes, enum codes, and
// timestamps — never prompt or content text. Progress node title/description,
// checkpoint manifests, and filesystem paths (artifact_root/manifest_path)
// are deliberately omitted from the projections below.
package sessionstatus

import "time"

// Snapshot is the assembled per-session FR-162 read-model. httpapi wraps it
// in a schema-versioned envelope; the field shapes and their null/empty
// semantics live here.
type Snapshot struct {
	SessionID  string      `json:"session_id"`
	Risk       *Risk       `json:"risk"`       // null when the session has no linkable prediction yet
	Runway     *Runway     `json:"runway"`     // null when no runway_forecasts row exists (honest: may be empty today)
	Quota      Quota       `json:"quota"`      // always present; Windows empty when no quota events
	Progress   Progress    `json:"progress"`   // always present; Nodes/Edges empty when none
	Checkpoint *Checkpoint `json:"checkpoint"` // null when the session's task has no state checkpoint
	Pause      *Pause      `json:"pause"`      // null when the session has no pause record
}

// Risk is the most recent prediction's overall + component risk scores
// (predictions, migration 0041). Scores are 0-1 estimates; Calibrated=false
// means they are NOT probabilities (Constitution principle #2).
type Risk struct {
	OverallRiskScore     float64  `json:"overall_risk_score"`
	QuotaRiskScore       float64  `json:"quota_risk_score"`
	ContextRiskScore     float64  `json:"context_risk_score"`
	CompletionRiskScore  float64  `json:"completion_risk_score"`
	BlastRadiusRiskScore float64  `json:"blast_radius_risk_score"`
	Calibrated           bool     `json:"calibrated"`
	Confidence           string   `json:"confidence"`
	ReasonCodes          []string `json:"reason_codes"`
	TurnID               string   `json:"turn_id"`
	EvaluatedAt          string   `json:"evaluated_at"`
}

// Runway mirrors domain.RunwayForecast's queryable fields (runway_forecasts,
// migration 0042). Pointer fields are null when the source column is NULL.
type Runway struct {
	LimitID                        string     `json:"limit_id"`
	HorizonSeconds                 int64      `json:"horizon_seconds"`
	RiskScore                      float64    `json:"risk_score"`
	Calibrated                     bool       `json:"calibrated"`
	Confidence                     string     `json:"confidence"`
	CurrentUsedPercent             *float64   `json:"current_used_percent"`
	HitProbability                 *float64   `json:"hit_probability"`
	BurnRateP50                    *float64   `json:"burn_rate_p50"`
	BurnRateP90                    *float64   `json:"burn_rate_p90"`
	EstimatedTimeToLimitP50Seconds *int64     `json:"estimated_time_to_limit_p50_seconds"`
	EstimatedTimeToLimitP90Seconds *int64     `json:"estimated_time_to_limit_p90_seconds"`
	QuotaObservedAt                *time.Time `json:"quota_observed_at"`
	ReasonCodes                    []string   `json:"reason_codes"`
}

// Quota is the quota-freshness surface: the latest observation per limit
// window plus the age (AsOf − ObservedAt) so the extension can flag
// staleness. AsOf is the server clock at read time.
type Quota struct {
	AsOf    time.Time     `json:"as_of"`
	Windows []QuotaWindow `json:"windows"`
}

// QuotaWindow is one limit window's latest observation
// (provider.quota.observed events). UsedPercent/ResetsAt are null when the
// source event did not carry them. AgeSeconds is now − ObservedAt, clamped
// to be non-negative.
type QuotaWindow struct {
	LimitID     string     `json:"limit_id"`
	UsedPercent *float64   `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
	ObservedAt  time.Time  `json:"observed_at"`
	AgeSeconds  int64      `json:"age_seconds"`
}

// Progress is the progress-tree snapshot for the session's current task
// (progress_nodes/edges, migrations 0020/0021), keyed by TaskID. Empty
// arrays — not an error — when the task has no nodes (the common state
// today). Node title/description are omitted (FR-171 content exclusion);
// only structural ids, ordinals, kinds, statuses, versions, and lifecycle
// timestamps are carried.
type Progress struct {
	TaskID *string        `json:"task_id"`
	Nodes  []ProgressNode `json:"nodes"`
	Edges  []ProgressEdge `json:"edges"`
}

type ProgressNode struct {
	ID          string  `json:"id"`
	ParentID    *string `json:"parent_id"`
	Ordinal     int64   `json:"ordinal"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	Version     int64   `json:"version"`
	StartedAt   *string `json:"started_at"`
	CompletedAt *string `json:"completed_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type ProgressEdge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"`
}

// Checkpoint carries the latest state checkpoint for the session's task
// (state_checkpoints, migration 0023) and, when it references one, the
// linked repository checkpoint (repository_checkpoints, migration 0030).
// Manifests and filesystem paths are omitted (FR-171); only ids, hashes,
// statuses, counts, and timestamps are carried.
type Checkpoint struct {
	State      *StateCheckpoint `json:"state"`
	Repository *RepoCheckpoint  `json:"repository"` // null when the state checkpoint references none
}

type StateCheckpoint struct {
	ID                     string  `json:"id"`
	TaskID                 string  `json:"task_id"`
	ProgressTreeVersion    int64   `json:"progress_tree_version"`
	ActiveNodeID           *string `json:"active_node_id"`
	CompletionNodeID       *string `json:"completion_node_id"`
	RepositoryCheckpointID *string `json:"repository_checkpoint_id"`
	IntegritySHA256        string  `json:"integrity_sha256"`
	CreatedAt              string  `json:"created_at"`
}

type RepoCheckpoint struct {
	ID               string  `json:"id"`
	Status           string  `json:"status"`
	Recoverability   string  `json:"recoverability"`
	GitHead          string  `json:"git_head"`
	IndexDiffHash    string  `json:"index_diff_hash"`
	WorktreeDiffHash string  `json:"worktree_diff_hash"`
	TotalBytes       *int64  `json:"total_bytes"`
	CreatedAt        string  `json:"created_at"`
	VerifiedAt       *string `json:"verified_at"`
}

// Pause is the session's most recent pause record (pause_records, migration
// 0050) regardless of lifecycle state — the extension shows resumed and
// cancelled pauses too — plus the wake jobs scheduled against it (wake_jobs,
// migration 0051), which back FR-163's cancel. Nullable lifecycle timestamps
// render null until that phase is reached.
type Pause struct {
	ID                     string    `json:"id"`
	TaskID                 string    `json:"task_id"`
	TurnID                 *string   `json:"turn_id"`
	Status                 string    `json:"status"`
	AutoResumeEnabled      bool      `json:"auto_resume_enabled"`
	RunwayForecastID       string    `json:"runway_forecast_id"`
	StateCheckpointID      *string   `json:"state_checkpoint_id"`
	RepositoryCheckpointID *string   `json:"repository_checkpoint_id"`
	RequestedAt            string    `json:"requested_at"`
	SafePointAt            *string   `json:"safe_point_at"`
	PausedAt               *string   `json:"paused_at"`
	ExpectedResetAt        *string   `json:"expected_reset_at"`
	CancelledAt            *string   `json:"cancelled_at"`
	FailureCode            *string   `json:"failure_code"`
	WakeJobs               []WakeJob `json:"wake_jobs"`
}

// WakeJob mirrors the wake-job projection the /v1/scheduler/jobs endpoint
// serves, scoped here to the pause it belongs to.
type WakeJob struct {
	ID          string     `json:"id"`
	PauseID     string     `json:"pause_id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	RunAfter    time.Time  `json:"run_after"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	LeaseOwner  *string    `json:"lease_owner"`
	LeaseExpiry *time.Time `json:"lease_expires_at"`
	LastError   *string    `json:"last_error"`
}
