-- 0022_artifacts.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `artifacts` — durable,
-- validator-checked evidence rows backing node completion ("Completed
-- means evidenced," Constitution §6.2). Checkpoint Part A range
-- (0020-0029 per CONTRACT_FREEZE.md), transcribed column-for-column from
-- §12.2. Maps to domain.ArtifactRef (internal/domain/artifact.go).
--
-- progress_node_id is nullable with ON DELETE SET NULL: evidence outlives
-- the node it evidenced — deleting a node (task-cascade or otherwise) must
-- never silently destroy the durable evidence trail, only detach it.
-- task_id, by contrast, cascades: when the whole task is deleted its
-- evidence goes with it.
--
-- UNIQUE(progress_node_id, uri, sha256) is the storage-level half of the
-- duplicate-completion rule (Constitution §6.6): the same evidence for the
-- same node is one row; the same URI with a DIFFERENT sha256 is a distinct
-- row that the CompleteNode protocol (checkpoint-a04) must surface as a
-- conflict, never silently merge. SQLite treats NULL progress_node_id as
-- distinct in UNIQUE constraints, so detached evidence rows do not
-- collide; the constraint is meaningful for the attached (non-NULL) case
-- this table exists to protect.
--
-- validation_status holds the artifact validator verdict wire strings
-- (checkpoint-a03 owns the validator vocabulary); deliberately not a CHECK
-- constraint, same immutable-DDL reasoning as 0020/0021.
CREATE TABLE artifacts (
    id                TEXT PRIMARY KEY,
    task_id           TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    progress_node_id  TEXT REFERENCES progress_nodes(id) ON DELETE SET NULL,
    kind              TEXT NOT NULL,
    uri               TEXT NOT NULL,
    media_type        TEXT,
    bytes             INTEGER NOT NULL,
    sha256            TEXT NOT NULL,
    validator_id      TEXT,
    validation_status TEXT NOT NULL,
    metadata_json     TEXT NOT NULL DEFAULT '{}',
    created_at        TEXT NOT NULL,
    UNIQUE(progress_node_id, uri, sha256)
);
