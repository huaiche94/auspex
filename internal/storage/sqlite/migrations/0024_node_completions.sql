-- 0024_node_completions.sql
--
-- Preflight_ADD.md §18.12 "Node idempotency" canonical mechanism:
--
--   completion_key = SHA256(task_id + node_id + artifact hashes +
--                            acceptance evidence hashes)
--
-- This table is the durable ledger CompleteNode (checkpoint-a04) consults
-- before doing any work: a replay of the SAME completion_key for a node
-- returns the same result (CONTRACT_FREEZE.md's CompleteNodeRequest.
-- IdempotencyKey rule); a DIFFERENT payload hash under a key that was
-- already used for that node is a conflict (Constitution §6.6 "duplicate
-- completion with conflicting evidence is rejected"), not a silent
-- overwrite.
--
-- One row per (node_id) that has ever completed: node_id is PRIMARY KEY
-- because a node can only ever be completed once in its lifetime (the
-- state machine's completed status is terminal, statemachine.go) — the
-- idempotency ledger's granularity matches the node's own one-shot
-- completion, not a general request-log. idempotency_key and
-- payload_digest are what a replay is checked against;
-- state_checkpoint_id and completed_node_json let CompleteNode return the
-- exact same (ProgressNode, StateCheckpoint) result on replay without
-- recomputing anything.
CREATE TABLE node_completions (
    node_id             TEXT PRIMARY KEY REFERENCES progress_nodes(id) ON DELETE CASCADE,
    task_id             TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    idempotency_key     TEXT NOT NULL,
    payload_digest      TEXT NOT NULL,
    state_checkpoint_id TEXT NOT NULL,
    completed_node_json TEXT NOT NULL,
    created_at          TEXT NOT NULL
);

-- Lookup by idempotency_key alone (a caller replaying a request may not
-- have the node_id handy in every call site, e.g. a provider webhook
-- retried with only its own idempotency key) needs its own index; it is
-- deliberately not UNIQUE by itself because the same literal key string
-- from two different, uncoordinated callers/nodes is not this table's
-- concern to prevent — only per-node replay/conflict is.
CREATE INDEX idx_node_completions_idempotency_key
    ON node_completions(idempotency_key);
