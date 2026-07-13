-- 0042_runway_forecasts.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `runway_forecasts`. Durable form
-- of the independent Runway Predictor's output (ADD §15.4-15.7,
-- predictor-06's domain.RunwayForecast; ADR-041 explicitly keeps this
-- outside the Scope->Token->Quota->Risk chain — see CONTRACT_FREEZE.md's
-- "Predictor pipeline ports (ADR-041)" section). Consumed directly by
-- `runtime`'s GracefulPauseService and by `policy_decisions` below.
--
-- session_id FKs into provider_sessions (0003, present on this branch) and
-- task_id FKs into tasks (0004, present) — both real, enforced FK
-- constraints. turn_id intentionally has NO FK constraint: `turns`
-- (claude-provider's 0010-0019 range) does not exist yet. See
-- 0040_feature_vectors.sql's note — a REFERENCES clause pointing at a
-- not-yet-existing table breaks unrelated cascading DELETEs elsewhere in
-- the schema (confirmed empirically: TestCoreMigrations_ForeignKeys_RepositoryToWorktree's
-- cascade DELETE FROM repositories failed with "no such table: main.turns"
-- once this table declared turn_id REFERENCES turns(id)), so it is omitted
-- here exactly as 0004_tasks.sql already established for its own
-- active_node_id/progress_nodes forward reference.
CREATE TABLE runway_forecasts (
    id                                    TEXT PRIMARY KEY,
    session_id                            TEXT NOT NULL REFERENCES provider_sessions(id) ON DELETE CASCADE,
    turn_id                               TEXT,
    task_id                               TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    limit_id                              TEXT NOT NULL,
    horizon_seconds                       INTEGER NOT NULL,
    hit_probability                       REAL,
    risk_score                            REAL NOT NULL,
    calibrated                            INTEGER NOT NULL,
    confidence                            TEXT NOT NULL,
    current_used_percent                  REAL,
    burn_rate_p50                         REAL,
    burn_rate_p90                         REAL,
    estimated_time_to_limit_p50_seconds   INTEGER,
    estimated_time_to_limit_p90_seconds   INTEGER,
    reason_codes_json                     TEXT NOT NULL,
    created_at                            TEXT NOT NULL
);
