// Package token implements the Predictor pipeline's Stage 2
// (ADR-041 / internal/app.TokenForecaster): predicting total token cost of
// the upcoming turn from its Stage-1 ScopeEstimate, per ADD §15.1 (token
// decomposition) and §15.2 (initial token predictor: empirical quantiles
// once >=8 similar samples exist, else cold-start defaults, combined with a
// multiplier model using a geometric mean and caps to avoid multiplier
// explosion).
//
// This is a Version 1 (rule-based/heuristic) implementation per
// Preflight_Predictor_Design_Supplement.md's Evolution Roadmap. No durable
// historical telemetry store exists yet this wave (the same gap already
// noted for predictor-05/predictor-06's cold-start-only implementations),
// so RuleTokenForecaster's "count(similar) >= 8" branch is reachable only
// when a caller's FeatureSource actually supplies >=8 session samples;
// absent that, every result this wave is a cold-start default composed
// with the ADD §15.2 multiplier model, always Calibrated=false,
// Confidence<=ConfidenceLow (mirrors the discipline already established by
// predictor-04/predictor-05/predictor-06 and CONTRACT_FREEZE.md's
// cold-start contract).
//
// ADD §15.2's base-quantile description names only P50/P90; TokensP80 is
// not separately specified there. This package interpolates P80 as a
// log-space weighted blend between the computed P50 and P90 (documented in
// forecaster.go), rather than inventing an unrelated third empirical
// quantile — this is an explicit assumption, recorded in this role's
// progress artifact.
package token
