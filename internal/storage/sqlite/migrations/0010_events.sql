-- 0010_events.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `events`. This is claude-provider's
-- migration range (0010-0019 per CONTRACT_FREEZE.md's migration-range table)
-- and this is its first migration: the durable event log every normalized
-- pkg/protocol/v1.Event value (claude-provider-04's normalizer.go output) is
-- persisted into (claude-provider-05, this node).
--
-- Column set and types mirror pkg/protocol/v1.Event (pkg/protocol/v1/event.go,
-- frozen by contract-integrator) field-for-field:
--   SchemaVersion  -> schema_version
--   EventID        -> event_id (PK)
--   EventType      -> event_type
--   OccurredAt     -> occurred_at
--   ObservedAt     -> observed_at
--   Sequence       -> sequence (nullable: not every producer assigns one)
--   IdempotencyKey -> idempotency_key (nullable, but unique when present)
--   Source         -> source
--   Provider       -> provider
--   RepositoryID   -> repository_id (nullable: not every event is
--                     repository-scoped, e.g. a bare status-line snapshot)
--   WorktreeID     -> worktree_id (nullable, same reasoning)
--   SessionID      -> session_id
--   TurnID         -> turn_id (nullable: only turn-scoped events carry one)
--   TaskID         -> task_id (nullable)
--   ProgressNodeID -> progress_node_id (nullable)
--   Payload        -> payload_json (TEXT-encoded JSON; the frozen
--                     map[string]any has no fixed relational shape)
--
-- No FK constraints against repositories/worktrees/provider_sessions/tasks:
-- ADD §12.2's canonical `events` table declares none either (events is a
-- durable append-only log fed by every role, including roles whose owning
-- rows may not exist yet at write time -- e.g. this role's own
-- provider.turn.started event is emitted before any `turns` row exists,
-- since no role has created a `turns` table migration yet). Enforcing FKs
-- here would make this table's insert order depend on write-ordering
-- guarantees from tables this role does not own and does not control.
--
-- idx_events_idempotency is ADD §12.3's required index verbatim: a UNIQUE
-- index over idempotency_key, scoped with a WHERE clause so multiple NULLs
-- (events that never got an idempotency key) do not collide -- SQLite
-- already treats NULL as distinct from every other NULL in a UNIQUE index,
-- but the explicit WHERE also documents the intent and matches ADD's own
-- literal SQL. This unique index is the actual mechanism idempotent inserts
-- in this role's persistence layer (claude-provider-05) rely on: an
-- INSERT ... ON CONFLICT(idempotency_key) DO NOTHING (or equivalent)
-- against this index is what makes writing the same normalized event twice
-- a no-op rather than a duplicate row.
CREATE TABLE events (
    event_id         TEXT PRIMARY KEY,
    schema_version   TEXT NOT NULL,
    event_type       TEXT NOT NULL,
    occurred_at      TEXT NOT NULL,
    observed_at      TEXT NOT NULL,
    sequence         INTEGER,
    idempotency_key  TEXT,
    source           TEXT NOT NULL,
    provider         TEXT,
    repository_id    TEXT,
    worktree_id      TEXT,
    session_id       TEXT,
    turn_id          TEXT,
    task_id          TEXT,
    progress_node_id TEXT,
    payload_json     TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_events_task_time
    ON events(task_id, occurred_at);

CREATE UNIQUE INDEX idx_events_idempotency
    ON events(idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX idx_events_session_time
    ON events(session_id, occurred_at);
