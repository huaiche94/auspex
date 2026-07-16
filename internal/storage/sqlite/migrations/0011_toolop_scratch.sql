-- 0011_toolop_scratch.sql
--
-- Issue #67 slice 3a (ADR-052): per-turn tool-operation SCRATCH state for
-- the Claude Code PostToolUse hook. claude-provider's migration range
-- (0010-0019 per CONTRACT_FREEZE.md); 0011 is the range's next free slot.
--
-- Each PostToolUse hook invocation is a separate short-lived process, so
-- the "per-turn in-process aggregation" of
-- docs/backlog/token-cost-prediction-research.md §7.3/§7.4 needs a place
-- to carry ONE running counter between invocations: how many
-- file-touching tool operations (view = Read; modify = Edit / Write /
-- MultiEdit / NotebookEdit) the hook observed for the session's open
-- turn. The Stop hook folds this counter into the five per-turn
-- aggregates stamped on provider.turn.completed and then deletes the
-- session's rows (turn-close cleanup).
--
-- PRIVACY (ADR-052's binding invariant, §7.3/§7.8): raw file paths are
-- never persisted IN ANY FORM — not raw, not hashed. This table's shape
-- enforces that by construction: it has no column that could carry a
-- path or a digest of one. session_id/turn_id are Auspex-minted /
-- provider-envelope identifiers (the same values the events table already
-- carries); file_ops is a bare counter; updated_at is a timestamp kept so
-- a future GC can age out crash-orphaned rows. Path→ordinal interning
-- happens in process memory only (internal/telemetry/claude/toolops.go)
-- and is discarded — only aggregate counts ever reach durable storage.
--
-- This is SCRATCH, not telemetry: rows live for at most one turn on the
-- happy path (PostToolUse upserts, Stop/StopFailure delete). It is
-- deliberately NOT an event type — a per-tool-call event stream would
-- fight ADR-046 retention, and pkg/protocol/v1's EventType taxonomy is
-- closed (ADR-052: provider.tool.* stays unused).
CREATE TABLE toolop_scratch (
    session_id TEXT    NOT NULL,
    turn_id    TEXT    NOT NULL DEFAULT '',
    file_ops   INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT    NOT NULL,
    PRIMARY KEY (session_id, turn_id)
);
