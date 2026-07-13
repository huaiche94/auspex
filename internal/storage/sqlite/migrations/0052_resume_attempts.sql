-- 0052_resume_attempts.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `resume_attempts` — the audit
-- trail of every attempt to resume a paused session (agents/runtime.md
-- Part A P0 deliverable 8: resume validation — quota safe, repository
-- fingerprint compatible, session/provider capability valid,
-- authorization/consent valid; Constitution §7 rule 9: auto-resume is
-- audited and re-verified before it runs). One pause may accumulate many
-- attempts (unsafe quota reschedules, validation failures, retries), so
-- this is deliberately a separate append-style table rather than columns
-- on pause_records.
--
-- wake_job_id is nullable with ON DELETE SET NULL: a manual
-- `auspex resume` produces an attempt with no wake job, and an
-- attempt's audit row must survive its wake job — only the pause itself
-- (ON DELETE CASCADE) owns the attempt's lifetime.
--
-- repository_fingerprint_before/after and quota_used_percent record what
-- the validators actually observed at attempt time; NULL means unobserved
-- (unknown is not zero — CONTRACT_FREEZE.md "Unknown/null semantics").
CREATE TABLE resume_attempts (
    id                            TEXT PRIMARY KEY,
    pause_id                      TEXT NOT NULL REFERENCES pause_records(id) ON DELETE CASCADE,
    wake_job_id                   TEXT REFERENCES wake_jobs(id) ON DELETE SET NULL,
    status                        TEXT NOT NULL,
    provider_session_id           TEXT,
    repository_fingerprint_before TEXT,
    repository_fingerprint_after  TEXT,
    quota_used_percent            REAL,
    started_at                    TEXT NOT NULL,
    completed_at                  TEXT,
    failure_code                  TEXT,
    metadata_json                 TEXT NOT NULL DEFAULT '{}'
);
