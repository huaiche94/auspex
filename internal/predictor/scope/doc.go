// Package scope implements the Predictor pipeline's Stage 1
// (ADR-041 / internal/app.ScopeEstimator): predicting what work a turn is
// expected to require — files read/changed and lines changed — from
// prompt, repository, session, and Progress-Tree derived features.
//
// This is a Version 1 (rule-based/heuristic) implementation per
// Preflight_Predictor_Design_Supplement.md's Evolution Roadmap: cold-start
// defaults keyed by task class (ADD §14.6), blended with empirical
// quantiles from recent session history (internal/predictor.EmpiricalQuantiles)
// once enough samples exist. It deliberately leaves ToolCalls/Verification/
// RetryLoops/Duration fields nil — those require signals (tool-call
// telemetry, verification-run telemetry) this wave does not yet have wired
// up, and forecast.go's own doc comment explicitly allows a Wave 2
// ScopeEstimator to populate only a subset of fields.
package scope
