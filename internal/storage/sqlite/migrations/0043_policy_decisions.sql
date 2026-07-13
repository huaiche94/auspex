-- 0043_policy_decisions.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `policy_decisions`. Durable form
-- of app.DecisionResult (ADD §17 Policy Engine), one row per
-- EvaluationService.Decide call. FKs into predictions (0041, this range)
-- and runway_forecasts (0042, this range) — both same-migration-range
-- tables, so no forward-reference concern here (unlike the claude-provider/
-- checkpoint-owned tables referenced elsewhere in this range).
CREATE TABLE policy_decisions (
    id                     TEXT PRIMARY KEY,
    prediction_id          TEXT REFERENCES predictions(id) ON DELETE CASCADE,
    runway_forecast_id     TEXT REFERENCES runway_forecasts(id) ON DELETE SET NULL,
    policy_version         TEXT NOT NULL,
    action                 TEXT NOT NULL,
    severity               TEXT NOT NULL,
    requires_confirmation  INTEGER NOT NULL,
    reason_codes_json      TEXT NOT NULL,
    decided_at             TEXT NOT NULL
);
