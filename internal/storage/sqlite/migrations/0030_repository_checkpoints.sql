-- 0030_repository_checkpoints.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `repository_checkpoints` — the
-- durable record of a Repository Checkpoint capture (ADD §19). First
-- migration of checkpoint Part B's assigned range (0030-0039 per
-- CONTRACT_FREEZE.md), transcribed column-for-column from §12.2 with one
-- documented deviation (turn_id, below).
--
-- FKs into worktrees (foundation's 0002, required, ON DELETE CASCADE) and
-- tasks (foundation's 0004, nullable, ON DELETE SET NULL — a repository
-- checkpoint is repo evidence in its own right and outlives the task that
-- requested it).
--
-- turn_id deviation: §12.2 declares
--   turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL
-- but `turns` belongs to claude-provider's 0010-0019 range and does not
-- exist yet. SQLite tolerates creating an FK to a missing table but fails
-- every subsequent write to this table until it exists — which would brick
-- checkpoint-b04 behind another role's schedule. Following the precedent
-- foundation set for tasks.active_node_id (0004_tasks.sql header): turn_id
-- is a plain TEXT pointer here, with referential consistency owned by the
-- Repository Checkpoint service. If a real FK is later wanted, that is a
-- new migration in this range once turns exists (released migrations are
-- immutable, ADD §12.5).
--
-- status holds the checkpoint lifecycle wire strings owned by the
-- create/verify service (checkpoint-b04); recoverability holds the ADD §19
-- recoverability classification. Neither is a CHECK constraint — enum
-- vocabulary lives in the service layer, not immutable DDL (same reasoning
-- as 0020-0022).
--
-- total_bytes is nullable: unknown is not zero (CONTRACT_FREEZE.md
-- unknown/null semantics) — a checkpoint whose artifact size was never
-- measured must not report 0 bytes.
CREATE TABLE repository_checkpoints (
    id                 TEXT PRIMARY KEY,
    worktree_id        TEXT NOT NULL REFERENCES worktrees(id) ON DELETE CASCADE,
    task_id            TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    turn_id            TEXT,
    status             TEXT NOT NULL,
    artifact_root      TEXT NOT NULL,
    manifest_path      TEXT NOT NULL,
    git_head           TEXT NOT NULL,
    index_diff_hash    TEXT NOT NULL,
    worktree_diff_hash TEXT NOT NULL,
    recoverability     TEXT NOT NULL,
    total_bytes        INTEGER,
    created_at         TEXT NOT NULL,
    verified_at        TEXT,
    metadata_json      TEXT NOT NULL DEFAULT '{}'
);
