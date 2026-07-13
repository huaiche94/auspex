// Package quota implements the Predictor pipeline's Stage 3
// (ADR-041 / internal/app.QuotaForecaster): projecting provider-quota and
// context-window position after the upcoming turn, per ADD §15.3 (quota
// delta model) and §15.9 (context projection). Both projections are
// produced together in a single domain.QuotaForecast value, since they
// share the same delta-projection technique and both feed RiskCombiner's
// quota_risk/context_risk terms (ADD §16.2, ADR-041).
//
// This is a Version 1 (rule-based/deterministic) implementation per
// Auspex_Predictor_Design_Supplement.md's Evolution Roadmap
// ("RuleQuotaForecaster — Version 1 — deterministic delta model, §15.3").
//
// No durable historical telemetry store exists yet this wave — the same
// gap already established for predictor-05/predictor-05b/predictor-06's
// cold-start-only implementations, and explicitly anticipated by
// CONTRACT_FREEZE.md's "Predictor pipeline ports (ADR-041)" section:
// "QuotaForecaster implementations MAY produce a deterministic
// current-observation-plus-default-delta estimate ... before durable
// historical telemetry exists. This is not a stub to be later thrown
// away; it is the correct first implementation under this frozen shape."
// RuleQuotaForecaster therefore always returns Calibrated=false,
// Confidence<=ConfidenceLow this wave: §15.3's "依 provider/model/task
// class 計算 empirical P50/P90" step (empirical delta quantiles) has no
// samples to compute from, so every projection uses the documented
// default-delta constants in coldstart.go instead, exactly as this node's
// scope explicitly licenses.
package quota
