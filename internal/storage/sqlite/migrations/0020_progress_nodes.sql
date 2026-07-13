-- 0020_progress_nodes.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `progress_nodes` — the Progress
-- Tree node table, the canonical durable task state (Constitution §6.1).
-- First migration of checkpoint Part A's assigned range (0020-0029 per
-- CONTRACT_FREEZE.md), transcribed column-for-column from §12.2.
--
-- FKs into tasks (foundation's 0004, required, ON DELETE CASCADE) and into
-- itself for the parent/child tree shape (nullable — a root node has no
-- parent). This is also the table foundation's tasks.active_node_id column
-- points at informally (0004_tasks.sql deliberately carries no FK for it;
-- see that file's header) — the Progress Tree service (checkpoint-a02+) is
-- responsible for keeping tasks.active_node_id consistent with rows here.
--
-- status holds domain.ProgressNodeStatus wire strings (frozen enum,
-- internal/domain/status.go: pending/ready/in_progress/checkpointing/
-- paused/completed/failed/skipped/blocked — Constitution §6.4). kind holds
-- domain.ProgressNodeKind wire strings. Neither is a CHECK constraint:
-- transition/enum validation is the node state machine's job
-- (checkpoint-a02), and baking today's enum into released, immutable DDL
-- (ADD §12.5) would make any future additive enum value a schema change
-- instead of a service change.
--
-- Note on UNIQUE(task_id, parent_id, ordinal): SQLite treats NULLs as
-- distinct in UNIQUE constraints, so root-level nodes (parent_id IS NULL)
-- are NOT ordinal-deduplicated by this constraint. That matches the
-- canonical §12.2 schema verbatim; root-ordinal uniqueness is enforced by
-- the Progress Tree service's plan-upsert logic (checkpoint-a02), not DDL.
CREATE TABLE progress_nodes (
    id               TEXT PRIMARY KEY,
    task_id          TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    parent_id        TEXT REFERENCES progress_nodes(id) ON DELETE CASCADE,
    ordinal          INTEGER NOT NULL,
    kind             TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT,
    status           TEXT NOT NULL,
    acceptance_json  TEXT NOT NULL DEFAULT '[]',
    next_action_json TEXT,
    provider_node_id TEXT,
    version          INTEGER NOT NULL,
    started_at       TEXT,
    completed_at     TEXT,
    updated_at       TEXT NOT NULL,
    UNIQUE(task_id, parent_id, ordinal)
);

-- ADD §12.3 required index. Index creation stays with the role whose
-- migration range owns the indexed table (per foundation-06's documented
-- convention in docs/implementation/vertical-slice/foundation.md), so this lands
-- here rather than in foundation's 0001-0009 range.
CREATE INDEX idx_progress_nodes_task_status
    ON progress_nodes(task_id, status, ordinal);
