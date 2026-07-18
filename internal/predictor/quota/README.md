# internal/predictor/quota/ — Stage 3: deterministic quota and context-window projection

> 🌐 English | [繁體中文](README.zh-TW.md)

`RuleQuotaForecaster` (`forecaster.go`) implements the frozen `app.QuotaForecaster` port
(ADR-041, predictor-05c): it projects provider-quota position (ADD §15.3 delta model) and
context-window position (ADD §15.9) after the upcoming turn, producing both in one
`domain.QuotaForecast` since they share the same delta technique and both feed
[`risk/`](../risk/README.md)'s quota_risk/context_risk terms.

It is stateless — `app.ForecastQuotaRequest` already carries the current
`QuotaObservation`s/`ContextObservation` and the upstream Stage-2 `TokenForecast` from
[`token/`](../token/README.md), so no `FeatureSource` abstraction is needed here.

Mechanics:

- Quota: each limit window gets current used% + a default P90 delta (`coldstart.go`: 2/6
  percentage points at P50/P90 — this package's own documented bootstrap constants; ADD §15.3
  names none). The worst projected window drives the single scalar output. A window whose
  `ResetsAt` lands within a 10-minute turn horizon does not accumulate past the reset (§15.8).
- Context: default net growth is expressed in tokens (6k/20k at P50/P90, per decision D-14 —
  formerly window fractions, which ran an order of magnitude hot on 1M windows), converted to
  percentage points via the observed window size; the pre-D-14 fraction fallback applies only
  when the window size is unknown. Exact `UsedTokens/WindowTokens` is preferred over the
  provider's rounded `UsedPercent`.
- The default deltas are scaled by the token forecast relative to a 6000-token nominal turn,
  bounded to [0.5, 3.0] so one extreme forecast cannot erase or explode the default.
- Unknown stays unknown: a missing observation yields a nil projection plus
  `QUOTA_UNKNOWN`/`CONTEXT_UNKNOWN`, never a fabricated 0.

Every result this phase is `Calibrated=false`, Confidence low, with `PREDICTION_COLD_START` —
no empirical delta distribution exists yet, exactly the first implementation CONTRACT_FREEZE.md
licenses. ADD sections cited above live in
[Auspex_ADD.md](../../../docs/design/Auspex_ADD.md). See `doc.go` for the package contract.
