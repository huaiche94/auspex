-- 0046_predictions_model_effort.sql (#20 Phase 0, D-10)
--
-- Turn-level identity stamp: which provider/model/effort the evaluated
-- turn ran under, resolved at EvaluateTurn time from the session's
-- latest observed identity (provider_sessions.model/effort, kept fresh
-- by status-line ingest). Persisted per prediction row because model and
-- effort are turn-level variables (/model and /fast switch mid-session)
-- and calibration (#11) must stratify by them — a prediction without
-- these labels is unlabeled history that can never be recovered
-- (capture-before-model, D-10/D-12).
--
-- All columns nullable: unknown is not zero — a session whose identity
-- was never observed stamps NULL, never a fabricated default.
-- model_family is denormalized alongside model_id so cohort queries do
-- not depend on the pricing table's resolution rules of the day.
ALTER TABLE predictions ADD COLUMN provider TEXT;
ALTER TABLE predictions ADD COLUMN model_id TEXT;
ALTER TABLE predictions ADD COLUMN model_family TEXT;
ALTER TABLE predictions ADD COLUMN effort TEXT;
