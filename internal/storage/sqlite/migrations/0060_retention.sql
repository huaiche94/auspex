-- 0060_retention.sql
--
-- ADR-046 tiered telemetry retention: the rollup + audit tables backing
-- `auspex gc` (internal/retention). First migration of the retention/gc
-- range (0060-0069 per CONTRACT_FREEZE.md's migration-range table, row
-- added by ADR-046 — retention is cross-cutting and owned by no
-- vertical-slice role, so it has its own range rather than borrowing one).
--
-- No FK constraints anywhere in this file, deliberately: these tables
-- exist precisely to OUTLIVE the raw rows they summarize (events,
-- predictions, provider_sessions rows are archived and deleted by the
-- retention pass in the same transaction that writes here), so a
-- REFERENCES clause would either block the delete or cascade away the
-- summary — both wrong. Referential meaning is documented per column
-- instead, following 0004_tasks.sql's plain-TEXT-pointer precedent.

-- usage_rollups_daily: per-(UTC day, provider, session, event type)
-- aggregate distilled from `events` rows before they are archived and
-- deleted. Composite PK per ADR-046. day is 'YYYY-MM-DD' derived from
-- events.occurred_at (already stored as RFC3339 UTC by
-- internal/telemetry/claude/store.go's formatTime).
--
-- Key normalization: events.provider/session_id are nullable, but a
-- rollup key needs total equality for the upsert to accumulate — so ''
-- in provider/session_id here is a documented key encoding meaning "the
-- event carried no value", never a claim that an empty string was
-- observed (unknown-is-not-zero applies to measurements; this is a
-- grouping key).
--
-- Aggregate columns carry ONLY what internal/telemetry/claude/
-- normalizer.go (the sole producer of persisted payloads) actually
-- writes, each nullable because no payload field is guaranteed present:
--   max_used_percent      — max payload "used_percent" that day: quota
--                           rows = rolling-window quota % (max across
--                           the five_hour/seven_day windows sharing the
--                           event_type — see ADR-046), context rows =
--                           context-window fill %.
--   max_used_tokens       — max payload "used_tokens"
--                           (provider.context.observed): context-window
--                           fill in tokens.
--   max_total_cost_usd    — max payload "total_cost_usd"
--                           (provider.usage.observed): session-CUMULATIVE
--                           cost gauge, so max = last observed value.
--   max_total_duration_ms — max payload "total_duration_ms", same
--                           cumulative-gauge semantics.
-- Per-turn token usage is deliberately ABSENT: no persisted payload
-- carries it today (ADR-046 "Calibration honesty" section). NULL means
-- "no event that day carried the field", never zero.
CREATE TABLE usage_rollups_daily (
    day                   TEXT NOT NULL,
    provider              TEXT NOT NULL,
    session_id            TEXT NOT NULL,
    event_type            TEXT NOT NULL,
    event_count           INTEGER NOT NULL,
    first_event_at        TEXT,
    last_event_at         TEXT,
    max_used_percent      REAL,
    max_used_tokens       INTEGER,
    max_total_cost_usd    REAL,
    max_total_duration_ms INTEGER,
    PRIMARY KEY (day, provider, session_id, event_type)
);

-- calibration_samples: one compact prediction-vs-actual pair per expired
-- `predictions` row, written before that row is archived and deleted —
-- this is what preserves M13 calibration's (issue #11) raw pairs across
-- archival (ADR-046's stated reason for rejecting TTL-only retention).
--
-- Predicted side: copied verbatim from predictions' real columns
-- (0041_predictions.sql) — token quantiles nullable there, nullable here.
--
-- Actual side — exactly what today's persisted events can honestly
-- supply, per ADR-046 "Calibration honesty":
--   actual_outcome       — 'completed' | 'failed' | 'interrupted', from
--                          the earliest provider.turn.completed/failed/
--                          interrupted event whose events.turn_id equals
--                          the prediction's turn_id.
--   actual_failure_class — payload "failure_class" on that event when it
--                          is a provider.turn.failed.
--   actual_outcome_at    — that event's occurred_at.
--   actual_known         — 1 iff such an event existed; 0 stores the
--                          honest cold start (ADD principle 1: unknown is
--                          not zero) with every actual_* column NULL.
--                          Today only provider.turn.started events are
--                          stamped with a turn_id (internal/orchestrator/
--                          hooks.go), so real Claude sessions currently
--                          yield actual_known = 0; the join upgrades
--                          automatically once outcome events gain turn
--                          correlation (issue #1).
-- Actual per-turn TOKEN usage has no column at all: no payload carries
-- it (statusline usage totals are session-cumulative and unattributable
-- to a turn); adding it later is a new migration in this range.
--
-- session_id is the turn's session recovered from the turn's own events
-- (predictions itself has no session column); NULL when no event for the
-- turn carried one. retention_run_id points at retention_runs.id
-- (plain-TEXT provenance pointer, no FK per the header note).
CREATE TABLE calibration_samples (
    prediction_id        TEXT PRIMARY KEY,
    turn_id              TEXT NOT NULL,
    session_id           TEXT,
    predictor_id         TEXT NOT NULL,
    predictor_version    TEXT NOT NULL,
    predicted_at         TEXT NOT NULL,
    token_p50            INTEGER,
    token_p80            INTEGER,
    token_p90            INTEGER,
    overall_risk_score   REAL NOT NULL,
    confidence           TEXT NOT NULL,
    calibrated           INTEGER NOT NULL,
    actual_known         INTEGER NOT NULL,
    actual_outcome       TEXT,
    actual_failure_class TEXT,
    actual_outcome_at    TEXT,
    retention_run_id     TEXT NOT NULL,
    created_at           TEXT NOT NULL
);

-- retention_runs: durable audit row for every non-dry-run retention pass,
-- success or failure (Constitution §6's evidence discipline applied to
-- gc itself). summary_json carries the per-table selected/deleted counts,
-- archive file paths + digests, rollup counts, and skip notes; error is
-- NULL on success. dry_run is INTEGER-as-boolean per 0004_tasks.sql's
-- auto_resume_enabled precedent — always 0 today, because `auspex gc
-- --dry-run` is documented (ADR-046) as truly side-effect-free and writes
-- no row here; the column exists so that decision is visible in the
-- schema and reversible without a migration.
CREATE TABLE retention_runs (
    id             TEXT PRIMARY KEY,
    ran_at         TEXT NOT NULL,
    retention_days INTEGER NOT NULL,
    dry_run        INTEGER NOT NULL,
    status         TEXT NOT NULL,
    summary_json   TEXT NOT NULL,
    error          TEXT
);
