-- 0047_predictions_duration.sql (#62 Phase 1)
--
-- Per-turn wall-clock duration forecast: the Phase-1 cold-start estimate
-- the scope estimator now produces (domain.ScopeEstimate.DurationP50/P90,
-- nanoseconds), persisted per prediction row so predicted-vs-actual
-- duration pairs can accumulate for calibration (#11) against the real
-- total_duration_ms telemetry Claude Code already reports. Without these
-- columns a filled duration estimate is dropped at persist time and the
-- prediction is unlabeled history that can never be recovered
-- (capture-before-calibrate, D-10/D-12).
--
-- Both columns nullable: unknown is not zero — a scope estimator that left
-- duration nil (or a pre-#62 prediction) stamps NULL, never a fabricated
-- default. Stored in nanoseconds to match the domain field's unit exactly.
ALTER TABLE predictions ADD COLUMN duration_p50 INTEGER;
ALTER TABLE predictions ADD COLUMN duration_p90 INTEGER;
