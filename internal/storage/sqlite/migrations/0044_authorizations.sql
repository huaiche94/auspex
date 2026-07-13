-- 0044_authorizations.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `authorizations`. Durable form
-- of app.Authorization (one-time authorization issuance/consumption,
-- agents/predictor.md deliverable #12; CONTRACT_FREEZE.md: "Authorization —
-- one-time; consumption is exactly-once, enforced by predictor at the
-- storage layer, not by this contract alone"). UNIQUE(turn_id) is the
-- storage-layer enforcement point for exactly-once issuance per turn;
-- exactly-once *consumption* is enforced by predictor-10's service logic
-- checking consumed_at IS NULL before consuming, inside the same
-- transaction (app.TxRunner.WithTx per CONTRACT_FREEZE.md's transaction
-- boundaries section) — not expressible as a table constraint alone.
--
-- turn_id and repository_checkpoint_id intentionally have NO FK constraint
-- at this migration: `turns` (claude-provider's 0010-0019 range) and
-- `repository_checkpoints` (checkpoint Part B's 0030-0039 range) do not
-- exist yet on this branch. See 0040_feature_vectors.sql's note — a
-- REFERENCES clause pointing at a not-yet-existing table breaks unrelated
-- cascading DELETEs elsewhere in the schema (confirmed empirically), so
-- both are omitted here exactly as 0004_tasks.sql already established for
-- its own forward reference. UNIQUE(turn_id) itself does not depend on the
-- FK and is unaffected.
CREATE TABLE authorizations (
    id                        TEXT PRIMARY KEY,
    turn_id                   TEXT NOT NULL,
    prompt_hash               TEXT NOT NULL,
    snapshot_fingerprint      TEXT NOT NULL,
    decision                  TEXT NOT NULL,
    repository_checkpoint_id  TEXT,
    issued_at                 TEXT NOT NULL,
    expires_at                TEXT NOT NULL,
    consumed_at               TEXT,
    UNIQUE(turn_id)
);
