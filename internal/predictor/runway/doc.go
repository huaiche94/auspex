// Package runway implements the ten-minute quota-exhaustion runway
// score/forecast (ADD §15.4-15.5), a predictor-06 deliverable.
//
// This is deliberately independent of the Scope -> Token -> Quota ->
// RiskCombiner pipeline (ADR-041): it answers "is any active quota window
// about to run out in the next H seconds", not "how risky is the upcoming
// turn". It is consumed directly by internal/app.GracefulPauseService.Observe
// (owned by the runtime role) — this package provides the scoring function
// runtime's Observe implementation calls into per runtime observation, not
// the pause-orchestration loop itself (predictor's boundary: "No provider
// JSON parsing, Git commands, checkpoint creation, or process
// interruption").
//
// Cold-start contract (ADD §15.6-15.7, agents/predictor.md): without a
// durable, calibrated burn-rate history (>=20 valid samples, held-out
// cohort evaluation, ECE<=0.08, Brier score recorded, quota-sample
// freshness), HitProbability stays nil and Calibrated stays false. A
// deterministic, explainable RiskScore (0-1, never presented as a
// probability) is always produced from the ADD §15.7 uncalibrated
// fallback thresholds, so policy still has a usable signal — this is the
// "correct first implementation under this frozen shape", not a stub.
package runway
