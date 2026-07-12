-- 0051_wake_jobs.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `wake_jobs` — the durable
-- scheduler's work queue (agents/runtime.md Part A P0 deliverables 6-7:
-- claim/renew/complete/fail/retry lease semantics, restart recovery of
-- overdue/leased jobs). The lease-claim transaction shape this table must
-- support is specified in ADD §12.4 (BEGIN IMMEDIATE; SELECT due job WHERE
-- status = 'scheduled' AND run_after <= :now AND lease not held; UPDATE to
-- 'leased' with owner/expiry/attempts+1; COMMIT).
--
-- UNIQUE(pause_id, job_kind) is the exactly-once anchor: a pause has at
-- most one job of a given kind, so a duplicate scheduling attempt is a
-- constraint violation to be handled idempotently, not a second wake
-- (agents/runtime.md "duplicate wake exactly-once behavior").
--
-- ON DELETE CASCADE from pause_records: a wake job cannot outlive the
-- pause it would resume. Cancel-wins-race semantics (P0 deliverable 10)
-- are implemented above this schema by the state machine/scheduler
-- (runtime-a02+/a06), not by row deletion alone.
CREATE TABLE wake_jobs (
    id               TEXT PRIMARY KEY,
    pause_id         TEXT NOT NULL REFERENCES pause_records(id) ON DELETE CASCADE,
    job_kind         TEXT NOT NULL,
    status           TEXT NOT NULL,
    run_after        TEXT NOT NULL,
    lease_owner      TEXT,
    lease_expires_at TEXT,
    attempts         INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL,
    last_error       TEXT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE(pause_id, job_kind)
);

-- ADD §12.3 required index: the §12.4 lease query scans by
-- (status, run_after) to find the next due job.
CREATE INDEX idx_wake_jobs_due
    ON wake_jobs(status, run_after);
