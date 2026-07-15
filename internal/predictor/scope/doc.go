// Package scope implements the Predictor pipeline's Stage 1
// (ADR-041 / internal/app.ScopeEstimator): predicting what work a turn is
// expected to require — files read/changed and lines changed — from
// prompt, repository, session, and Progress-Tree derived features.
//
// This is a Version 1 (rule-based/heuristic) implementation per
// Auspex_Predictor_Design_Supplement.md's Evolution Roadmap: cold-start
// defaults keyed by task class (ADD §14.6), blended with empirical
// quantiles from recent session history (internal/predictor.EmpiricalQuantiles)
// once enough samples exist. Since #62 it also populates DurationP50/P90 —
// a Phase-1 cold-start wall-clock estimate derived from the scope quantiles
// (duration.go), uncalibrated and gated for statusline use on #11/#42. It
// deliberately leaves ToolCalls/Verification/RetryLoops nil — those require
// tool-call and verification-run telemetry this implementation does not yet
// have wired up, and forecast.go's own doc comment explicitly allows a
// ScopeEstimator to populate only a subset of fields.
package scope
