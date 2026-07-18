-- 0063_predictions_token_io_split.sql (#65 Phase 1, ADR-0053)
--
-- Per-turn input/output token decomposition: the split the token
-- forecaster now produces alongside the authoritative total
-- (domain.TokenForecast.Input/OutputTokensP50/P90), persisted per
-- prediction row so the forecast card can render distinct input and output
-- ranges on read-back (internal/evaluation/forecastcard.go rebuilds the
-- card from these columns, never from a live pipeline object). The INPUT
-- interval is structurally WIDER than the OUTPUT interval (Bai et al. 2026
-- direction: models predict input tokens worse); the relative widening is
-- an UNCALIBRATED STRUCTURAL DEFAULT gated on #11, never a fitted
-- coefficient (see ADR-0053 and internal/predictor/token).
--
-- Only P50/P90 per axis, matching the scope and duration bands
-- (0041/0047) which also persist P50/P90 only — the decomposition renders
-- as a P50-P90 range; P80 stays on the authoritative total (token_p80).
--
-- All four columns nullable: unknown is not zero — a forecaster that did
-- not split (or a pre-#65 prediction) stamps NULL, never a fabricated 0.
--
-- Migration NUMBER: 0063 was pre-assigned to this slice to avoid collision
-- with parallel work (highest existing was 0062). It sits in the 0060-0069
-- retention/gc band (CONTRACT_FREEZE.md "Migration ranges", ADR-046)
-- rather than predictor's 0040-0049, which is exhausted through 0047; the
-- band boundary is an allocation convenience, not a semantic claim — these
-- columns belong to the predictor's `predictions` table.
--
-- NOT propagated to calibration_samples/the research export this slice
-- (unlike 0062 did for duration): today's split is a DETERMINISTIC
-- structural transform of the already-archived token_p50/p90, so exporting
-- it would add no independent calibration signal and open no
-- unlabeled-history hole (#11 can reconstruct it from the total). The
-- export extension is deferred to the phase where a calibrated forecaster
-- estimates the axes INDEPENDENTLY — capture-before-model (D-10/D-12).
ALTER TABLE predictions ADD COLUMN token_input_p50 INTEGER;
ALTER TABLE predictions ADD COLUMN token_input_p90 INTEGER;
ALTER TABLE predictions ADD COLUMN token_output_p50 INTEGER;
ALTER TABLE predictions ADD COLUMN token_output_p90 INTEGER;
