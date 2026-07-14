-- 0061_calibration_samples_identity.sql (#11, capture-before-model)
--
-- calibration_samples predates #20 Phase 0's identity stamp (migration
-- 0046), so the retention rollup was archiving predictions WITHOUT their
-- (provider, model_id, model_family, effort) labels — every retention
-- pass permanently stripped exactly the stratification keys M13
-- calibration (#11) needs, re-opening the unlabeled-history hole 0046
-- closed on the live table (D-10/D-12's capture-before-model rule).
--
-- Columns mirror 0046 exactly, including nullability: unknown is not
-- zero — a prediction stamped NULL archives as NULL. Samples archived
-- before this migration keep NULLs; they are honestly unlabeled and the
-- research pipeline reports them as such rather than guessing.
ALTER TABLE calibration_samples ADD COLUMN provider TEXT;
ALTER TABLE calibration_samples ADD COLUMN model_id TEXT;
ALTER TABLE calibration_samples ADD COLUMN model_family TEXT;
ALTER TABLE calibration_samples ADD COLUMN effort TEXT;
