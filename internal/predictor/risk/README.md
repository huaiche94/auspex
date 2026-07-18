# internal/predictor/risk/ — Stage 4: explainable risk combiner over the Stage 1–3 outputs

> 🌐 English | [繁體中文](README.zh-TW.md)

`RuleRiskCombiner` (`combiner.go`) implements the frozen `app.RiskCombiner` port (ADR-041,
predictor-07). Stateless: `app.CombineRiskRequest` carries the three upstream outputs directly
([`scope/`](../scope/README.md)'s `ScopeEstimate`, [`token/`](../token/README.md)'s
`TokenForecast`, [`quota/`](../quota/README.md)'s `QuotaForecast`).

It computes ADD §16.2's "Initial explainable formula" verbatim (coefficients in `coldstart.go`,
named after the formula's own variables):

- quota_risk / context_risk = sigmoid((projected P90 − 85) / 7), from the two `QuotaForecast`
  projections. A nil projection scores the sigmoid midpoint 0.5 plus
  `QUOTA_UNKNOWN`/`CONTEXT_UNKNOWN` — never a fabricated 0 (ADD §16.3).
- completion_risk / blast_radius_risk: clamped linear formulas over `ScopeEstimate` fields; the
  terms with no frozen field (open-ended scope, retry/test-failure rate, progress blockers,
  public API change) are read as boolean indicators from `ScopeEstimate.ReasonCodes` — a
  documented bridge, since the frozen request cannot be widened. (ADR-041 fixes the name
  completion_risk; "execution_risk" is the same concept and is never used here.)
- overall = max of the four; its Confidence is the lowest of the four and its ReasonCodes the
  deduplicated union. Scores are always in [0,1]; NaN clamps to 1.0 (most conservative).

Invariant (Constitution §7 rule 7): uncalibrated scores are never probabilities. Every
`domain.RiskComponent.Score` is a 0–1 risk score, not a probability, unless every contributing
input is itself `Calibrated=true` — unreachable this phase, since Stages 1–3 are cold-start-only.
Calibrated/Confidence are propagated honestly from upstream, never manufactured, and on the
cold-start path the downstream probability surfaces
([`internal/policy`](../../policy/README.md)'s `Decision.Probability`,
[`internal/evaluation`](../../evaluation/README.md)'s `ForecastCard.Probability`) emit
probability null.

ADD sections cited above live in [Auspex_ADD.md](../../../docs/design/Auspex_ADD.md). See
`doc.go` for the package contract.
