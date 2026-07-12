-- 0003_provider_sessions.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `provider_sessions`. FKs into
-- worktrees (0002). This is the table every provider role's own migration
-- range (claude-provider 0010-0019 and later provider adapters) FKs its
-- turns/telemetry tables into, so its shape here is load-bearing for all of
-- them — see docs/implementation/day1/CONTRACT_FREEZE.md's migration-range
-- table and EXECUTION_DAG.md's foundation-06 risk note.
--
-- provider_session_id is the provider's own session identifier (e.g.
-- Claude Code's session id), nullable because it may not be known at
-- session-row-creation time for every invocation mode; the UNIQUE
-- constraint on (provider, provider_session_id) still holds meaningfully
-- once populated since SQLite treats NULLs as distinct for uniqueness
-- purposes.
CREATE TABLE provider_sessions (
    id                   TEXT PRIMARY KEY,
    worktree_id          TEXT NOT NULL REFERENCES worktrees(id) ON DELETE CASCADE,
    provider             TEXT NOT NULL,
    provider_session_id  TEXT,
    invocation_mode      TEXT NOT NULL,
    model                TEXT,
    provider_version     TEXT,
    permission_mode      TEXT,
    started_at           TEXT NOT NULL,
    ended_at             TEXT,
    metadata_json        TEXT NOT NULL DEFAULT '{}',
    UNIQUE(provider, provider_session_id)
);
