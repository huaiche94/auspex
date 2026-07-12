-- 0050_pause_records.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `pause_records` — one row per
-- Graceful Pause lifecycle (agents/runtime.md Part A; runtime's migration
-- range 0050-0059 per CONTRACT_FREEZE.md). `status` holds the frozen
-- PauseStatus enum wire strings (internal/domain/status.go): predicted →
-- requested → quiescing → checkpointing → interrupting → sleeping →
-- wake_pending → validating → resuming → resumed, plus blocked_conflict /
-- cancelled / failed. Transition validation lives in the pause state
-- machine (runtime-a02+, internal/pause/**), not in schema.
--
-- DELIBERATE DEVIATION from ADD §12.2's canonical FK set, following the
-- precedent foundation-06 set in 0004_tasks.sql (tasks.active_node_id):
-- four columns the canonical schema declares as REFERENCES point at tables
-- whose migrations belong to other roles' EARLIER ranges and do not exist
-- as files yet when this migration ships —
--
--   turn_id                  -> turns                  (claude-provider, 0010-0019)
--   state_checkpoint_id      -> state_checkpoints      (checkpoint Part A, 0020-0029)
--   repository_checkpoint_id -> repository_checkpoints (checkpoint Part B, 0030-0039)
--   runway_forecast_id       -> runway_forecasts       (predictor, 0040-0049)
--
-- SQLite accepts CREATE TABLE with forward FK references, but with
-- PRAGMA foreign_keys = ON it resolves EVERY parent table on ANY DML that
-- touches this table — including cascade processing triggered from
-- tasks/worktrees/repositories deletes. Declaring these four FKs before
-- their parents exist therefore breaks unrelated DML repo-wide (observed
-- directly against foundation's own FK-cascade tests) and would block the
-- pause state machine (runtime-a02, which the DAG schedules against this
-- node alone) on three other roles' migration ranges. So these four are
-- plain TEXT pointers here; the pause service (internal/pause/**, and the
-- owning services it calls per CONTRACT_FREEZE.md "Transaction
-- boundaries") is responsible for keeping them consistent. Restoring the
-- canonical constraints via a table-recreating migration later in this
-- range (0053+) once 0010-0049 have landed is proposed to
-- contract-integrator in docs/implementation/day1/runtime.md.
--
-- runway_forecast_id stays NOT NULL: a pause exists only because a
-- specific runway forecast predicted/justified it (ADD §20), and that
-- audit link is required even while the referential constraint cannot be
-- declared yet. state/repository checkpoint links are nullable — they are
-- filled in as the persist phase progresses, per CONTRACT_FREEZE.md: the
-- persist phase is a sequence of dependent writes, not one flat
-- transaction.
CREATE TABLE pause_records (
    id                       TEXT PRIMARY KEY,
    task_id                  TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    session_id               TEXT NOT NULL REFERENCES provider_sessions(id) ON DELETE CASCADE,
    turn_id                  TEXT,
    runway_forecast_id       TEXT NOT NULL,
    state_checkpoint_id      TEXT,
    repository_checkpoint_id TEXT,
    status                   TEXT NOT NULL,
    requested_at             TEXT NOT NULL,
    safe_point_at            TEXT,
    paused_at                TEXT,
    expected_reset_at        TEXT,
    auto_resume_enabled      INTEGER NOT NULL,
    cancelled_at             TEXT,
    failure_code             TEXT,
    metadata_json            TEXT NOT NULL DEFAULT '{}'
);

-- ADD §12.3 required index: the scheduler and status surfaces query pauses
-- by lifecycle state and upcoming reset time.
CREATE INDEX idx_pause_status
    ON pause_records(status, expected_reset_at);
