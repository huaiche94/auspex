-- 0023_state_checkpoints.sql
--
-- Auspex_ADD.md §18.8 / Appendix B canonical schema: `state_checkpoints`
-- — the durable State Checkpoint manifest row created in the same atomic
-- operation as every node completion (Constitution §6.3, ADD ADR-029
-- "State checkpoint at every semantic boundary"). Deferred from
-- checkpoint-a01 (0020-0022) to this node, per a01's own progress-artifact
-- note: "state_checkpoints ... is deferred to the phase that implements the
-- State Checkpoint manifest (checkpoint-a04); it will take 0023+." This is
-- that phase.
--
-- manifest_json carries the full Appendix B document (schema_version
-- auspex.state-checkpoint.v1); the columns below duplicate the fields a
-- caller needs to query/join on without parsing JSON, exactly mirroring
-- 0030_repository_checkpoints.sql's own manifest_json + queryable-column
-- split for the sibling Part B artifact.
--
-- task_id cascades (a checkpoint has no meaning once its task is gone);
-- active_node_id and repository_checkpoint_id are plain nullable pointers,
-- not FKs: active_node_id references progress_nodes but a node can be
-- deleted independently of historical checkpoints that mentioned it (same
-- "evidence outlives the row it evidenced" reasoning as 0022_artifacts'
-- progress_node_id column), and repository_checkpoint_id crosses into Part
-- B's own table (0030-0039 range) which this Part A migration range does
-- not FK into directly, per agents/checkpoint.md's "Cross-part boundary"
-- note (Part A stores references to Part B's checkpoints through frozen
-- ports, not a direct schema FK).
--
-- UNIQUE(task_id, progress_tree_version) is deliberately NOT enforced here:
-- ADD §18.9 reconciliation and manual/administrative checkpoint creation
-- can both legitimately produce more than one checkpoint at the same tree
-- version (e.g. a re-verify-triggered re-checkpoint); latest-by-created_at
-- is how callers pick the canonical one for a version, not a uniqueness
-- constraint.
CREATE TABLE state_checkpoints (
    id                       TEXT PRIMARY KEY,
    task_id                  TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    progress_tree_version    INTEGER NOT NULL,
    active_node_id           TEXT,
    completion_node_id       TEXT,
    repository_checkpoint_id TEXT,
    manifest_json            TEXT NOT NULL,
    integrity_sha256         TEXT NOT NULL,
    created_at               TEXT NOT NULL
);

-- ADD §12.3-style lookup index: "latest checkpoint for a task" (LoadLatest,
-- reconciliation on startup) is this table's single hottest query.
CREATE INDEX idx_state_checkpoints_task_created
    ON state_checkpoints(task_id, created_at);
