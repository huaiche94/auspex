// Package risk implements the Predictor pipeline's Stage 4
// (ADR-041 / internal/app.RiskCombiner, predictor-07): combining the
// upstream Stage 1-3 outputs (domain.ScopeEstimate, domain.TokenForecast,
// domain.QuotaForecast) into the four named risk components plus an
// overall score, per ADD §16.1-16.2.
//
// # Components (ADD §16.1)
//
//   - quota_risk: Turn 或未來 horizon 內觸發 provider quota 的風險
//     (derived from QuotaForecast.ProjectedQuotaUsedP90).
//   - context_risk: Compaction、constraint loss 或 context failure 風險
//     (derived from QuotaForecast.ProjectedContextUsedP90 — same struct as
//     quota_risk, different field, per ADR-041's QuotaForecast doc comment:
//     "Both projections are produced together ... both feed RiskCombiner's
//     quota_risk and context_risk terms").
//   - completion_risk: 即使 quota/context 足夠，仍需要多輪或未滿足
//     acceptance criteria 的風險 (derived from ScopeEstimate). ADR-041's
//     "Terminology note" resolves a naming fork here:
//     Auspex_Predictor_Design_Supplement.md calls this term
//     "execution_risk = P(task_requires_multiple_turns)"; ADD §16.1/§16.2
//     names and formalizes the identical concept as "completion_risk" with
//     a complete formula. ADR-041 explicitly keeps the ADD's name as the
//     frozen term ("this ADR keeps the ADD's existing name —
//     completion_risk — as the frozen term ... renaming it would fork one
//     concept under two names, which Constitution §1 exists to prevent").
//     This package therefore uses completion_risk / CompletionRisk
//     throughout — never "execution_risk" — matching
//     CombineRiskResult.CompletionRisk's own frozen field name in
//     internal/app/ports.go.
//   - blast_radius_risk: 改動檔案、服務、schema、security boundary 或
//     migration 範圍超出預期的風險 (derived from ScopeEstimate).
//
// # Formula (ADD §16.2)
//
// This is a Version 1 (rule-based/deterministic, explainable) combiner per
// Auspex_Predictor_Design_Supplement.md's Evolution Roadmap, implementing
// ADD §16.2's "Initial explainable formula" verbatim:
//
//	quota_risk = sigmoid((projected_quota_p90 - 85) / 7)
//	context_risk = sigmoid((projected_context_p90 - 85) / 7)
//	completion_risk = clamp(0.10 + 0.04*files_changed_p90 + 0.0004*lines_changed_p90
//	  + 0.12*integration_tests + 0.15*migration + 0.10*cross_layer
//	  + 0.15*open_ended_scope + 0.20*recent_retry_rate + 0.10*recent_test_failure_rate
//	  + 0.10*unresolved_progress_blockers, 0, 1)
//	blast_radius_risk = clamp(0.05 + 0.03*files_changed_p90 + 0.15*cross_project
//	  + 0.20*migration + 0.15*security_sensitive + 0.10*public_api_change, 0, 1)
//	overall = max(quota, context, completion, blast_radius)
//
// # Cold-start contract
//
// CombineRiskRequest carries no independent calibration-gate signal of its
// own — every RuleRiskCombiner output's Calibrated/Confidence is derived
// honestly from its upstream inputs' own Calibrated/Confidence fields
// (never manufactured). Per agents/predictor.md's cold-start contract and
// Constitution §7 rule 7 ("Uncalibrated risk scores are never labeled as
// probabilities"): every domain.RiskComponent.Score this package produces
// is a 0-1 risk score, not a probability, unless every contributing input
// is itself Calibrated=true (unreachable this phase, since predictor-05's
// ScopeEstimator and predictor-05c's QuotaForecaster are both cold-start-
// only implementations as of this phase — see their own doc.go files).
package risk
