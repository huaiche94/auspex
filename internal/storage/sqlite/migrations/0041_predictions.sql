-- 0041_predictions.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `predictions`. The durable form
-- of the predictor pipeline's per-turn output (app.Evaluation plus the
-- Stage 1-4 forecast/risk fields backing it — domain.ScopeEstimate,
-- domain.TokenForecast, domain.RiskComponent per ADR-041).
--
-- turn_id intentionally has NO FK constraint at this migration: `turns`
-- (claude-provider's 0010-0019 range) does not exist yet on this branch.
-- See 0040_feature_vectors.sql's note — a REFERENCES clause pointing at a
-- not-yet-existing table breaks unrelated cascading DELETEs elsewhere in
-- the schema (confirmed empirically), not merely "inert until populated",
-- so it is omitted here exactly as 0004_tasks.sql already established for
-- active_node_id/progress_nodes. turn_id remains NOT NULL (a prediction
-- without a turn is meaningless) but unconstrained by FK until
-- claude-provider's range lands.
--
-- calibrated is stored as INTEGER (SQLite has no native boolean), matching
-- runway_forecasts/policy_decisions/authorizations' own convention below
-- and 0004_tasks.sql's auto_resume_enabled precedent. reason_codes_json
-- holds the serialized []domain.ReasonCode; predictor's evaluation-
-- persistence layer (predictor-09) owns the exact JSON shape.
CREATE TABLE predictions (
    id                       TEXT PRIMARY KEY,
    turn_id                  TEXT NOT NULL,
    predictor_id             TEXT NOT NULL,
    predictor_version        TEXT NOT NULL,
    feature_set_version      TEXT NOT NULL,
    token_p50                INTEGER,
    token_p80                INTEGER,
    token_p90                INTEGER,
    files_read_p50           INTEGER,
    files_read_p90           INTEGER,
    files_changed_p50        INTEGER,
    files_changed_p90        INTEGER,
    lines_changed_p50        INTEGER,
    lines_changed_p90        INTEGER,
    quota_risk_score         REAL NOT NULL,
    context_risk_score       REAL NOT NULL,
    completion_risk_score    REAL NOT NULL,
    blast_radius_risk_score  REAL NOT NULL,
    overall_risk_score       REAL NOT NULL,
    confidence               TEXT NOT NULL,
    calibrated               INTEGER NOT NULL DEFAULT 0,
    reason_codes_json        TEXT NOT NULL,
    created_at               TEXT NOT NULL
);
