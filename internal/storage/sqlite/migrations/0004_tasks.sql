-- 0004_tasks.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `tasks`. FKs into
-- provider_sessions (0003, nullable — a task can outlive the session that
-- started it, per ON DELETE SET NULL) and worktrees (0002, required).
--
-- This is the table checkpoint's Progress Tree range (0020-0029) FKs
-- progress_nodes into, predictor's range (0040-0049) FKs runway_forecasts
-- into, and runtime's range (0050-0059) FKs pause_records into — per
-- EXECUTION_DAG.md's foundation-06 risk note, schema mistakes here cascade
-- to all three.
--
-- active_node_id intentionally has no FK constraint at this migration: the
-- progress_nodes table it would reference does not exist until
-- checkpoint's 0020-0029 range, and SQLite has no deferred cross-table FK
-- addition without recreating the table. The column is a plain TEXT
-- pointer; checkpoint's Progress Tree service is responsible for keeping it
-- consistent with progress_nodes.id once that table exists.
CREATE TABLE tasks (
    id                    TEXT PRIMARY KEY,
    session_id            TEXT REFERENCES provider_sessions(id) ON DELETE SET NULL,
    worktree_id           TEXT NOT NULL REFERENCES worktrees(id) ON DELETE CASCADE,
    objective_hash        TEXT NOT NULL,
    objective_text        TEXT,
    status                TEXT NOT NULL,
    progress_tree_version INTEGER NOT NULL DEFAULT 1,
    active_node_id        TEXT,
    auto_resume_enabled   INTEGER NOT NULL DEFAULT 0,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    completed_at          TEXT
);
