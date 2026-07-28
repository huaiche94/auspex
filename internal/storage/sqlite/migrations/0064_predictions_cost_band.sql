-- 0064_predictions_cost_band.sql (#66 item b / #42, ADR-0055)
--
-- Persist the cost band the evaluation pipeline computed for a prediction,
-- alongside the token quantiles it was derived from. Until ADR-0055 the
-- band was deterministic given (model_id, token_p50, token_p90) — both the
-- forecast card and the calibration export recomputed it via
-- pricing.Table.EstimateTurnCost and could never disagree with what the
-- pipeline showed. ADR-0055's four-class empirical band breaks that
-- determinism: the band now depends on the cohort's captured cost samples
-- AT EVALUATION TIME, which later reads cannot reproduce. Persisting the
-- band keeps the read-back surfaces (forecast card, calibration export)
-- byte-honest with what the policy stage and the user actually saw.
--
-- cost_source records WHICH estimator produced the band
-- (pricing.SourceDefaultTable or pricing.SourceFourClassEmpirical), so the
-- research pipeline can stratify residuals by estimator generation instead
-- of averaging the old band's known ~7-9x under-forecast into the new one.
--
-- All four columns nullable: unknown is not zero — a turn with no token
-- forecast has no cost band (never a fabricated $0), and rows predating
-- this migration stay NULL (read-back falls back to the legacy recompute,
-- which is exact for those rows because their bands WERE deterministic).
--
-- Migration NUMBER: 0064, next free in sequence; sits in the 0060-0069
-- band as an allocation convenience exactly like 0063 (the columns belong
-- to the predictor's `predictions` table; the band boundary is not a
-- semantic claim).
ALTER TABLE predictions ADD COLUMN cost_low_usd REAL;
ALTER TABLE predictions ADD COLUMN cost_high_usd REAL;
ALTER TABLE predictions ADD COLUMN cost_model_family TEXT;
ALTER TABLE predictions ADD COLUMN cost_source TEXT;
