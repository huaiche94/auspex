# ADR-041 — Predictor pipeline gets an explicit Forecast layer

> 🌐 English | [繁體中文](0041-predictor-forecast-layer.zh-TW.md)

Status: Accepted
Date: 2026-07-12
Owner: contract-integrator (lead)
Approved by: repository owner, 2026-07-12

## Context

`Auspex_ADD.md` §2.2 already names `TokenForecast` and `QuotaForecast`
as fields of the canonical `AuspexDecision` struct, and §15.1–15.3/§15.9
already fully specify their formulas (token decomposition + multiplier
model; quota/context percentage-delta projection). But neither type was
ever added to the frozen contract layer during Bootstrap: `internal/domain`
has no `TokenForecast`, `QuotaForecast`, `RiskComponent`, or `DataQuality`
type, and `internal/app/ports.go`'s `Evaluation` struct is a thin shape
that does not carry any of them. `ScopeEstimate` (§14.1) was fully
specified in the ADD but likewise never implemented.

The frozen execution DAG (`docs/implementation/vertical-slice/EXECUTION_DAG.md`)
inherited the same gap: `predictor-05` (Scope Estimator) feeds directly
into `predictor-07` (Risk Combiner), with `predictor-06` (Runway Forecaster)
also listed as a `predictor-07` dependency. But `Auspex_ADD.md` §16.2's
own risk formulas need `projected_quota_p90` and `projected_context_p90` —
outputs of the quota-delta model (§15.3) and context projection (§15.9) —
neither of which any DAG node produces. `predictor-06`'s Runway Forecaster
answers a different question (imminent 10-minute quota-exhaustion hazard,
§15.5, consumed directly by Graceful Pause) and was never a valid source
for `quota_risk`/`context_risk`. The DAG's `predictor-07` dependency on
`predictor-06` was itself a byproduct of the same underspecified pipeline,
not a deliberate design choice — `Auspex_ADD.md` §7.3's own C4
Evaluation Components diagram shows the same conflation (`TOK --> RUNWAY
--> RISK`), which is corrected by this ADR.

This was discovered via `Auspex_Predictor_Design_Supplement.md` (a
companion design document identifying the same gap independently) before
any Phase 2 predictor implementation began. Per Constitution §6/§7 and this
project's own "no blind resume" discipline for architecture: a real gap
found before code is written is fixed at the contract layer, not patched
around at the implementation layer.

## Decision

Insert an explicit Forecast layer between Scope Estimation and Risk
Combination:

```text
Scope Estimator
      ↓
Token Forecast
      ↓
Quota Forecast (also produces the context-window projection)
      ↓
Risk Combiner
      ↓
Policy

Runway Predictor — independent, not part of this chain. Feeds Graceful
Pause directly (as it always correctly did — Auspex_ADD.md §7.4's
Continuity Components diagram already modeled this independence via
`Runway Hazard Monitor`; only §7.3's per-turn evaluation diagram and this
DAG's `predictor-07` edge incorrectly wired it into the risk path).
```

Four new narrow, swappable interfaces are frozen in `internal/app/ports.go`
(mirrored in `Auspex_ADD.md` §9.9), so a Rule/Statistical/ML
implementation of any single stage can replace it without touching the
others — the same evolutionary-roadmap intent already stated in
`Auspex_ADD.md` §1.4 and formalized in
`Auspex_Predictor_Design_Supplement.md`'s Version 1/2/3 roadmap:

```go
type ScopeEstimator interface {
    EstimateScope(context.Context, EstimateScopeRequest) (domain.ScopeEstimate, error)
}

type TokenForecaster interface {
    ForecastTokens(context.Context, ForecastTokensRequest) (domain.TokenForecast, error)
}

type QuotaForecaster interface {
    ForecastQuota(context.Context, ForecastQuotaRequest) (domain.QuotaForecast, error)
}

type RiskCombiner interface {
    Combine(context.Context, CombineRiskRequest) (CombineRiskResult, error)
}
```

Four new frozen domain types back these interfaces (`internal/domain/forecast.go`):
`domain.ScopeEstimate` (mirrors the struct already specified in ADD §14.1,
verbatim field set, adapted to pointer-typed numeric fields for
unknown-is-not-zero per ADD principle 1 — the ADD's own pseudocode used
plain `int`, which this ADR corrects for consistency with every other
frozen measurement type), `domain.TokenForecast` (P50/P80/P90, newly
specified — ADD §15.1–15.2 had the formula but no struct), `domain.QuotaForecast`
(`ProjectedQuotaUsedP90`, `ProjectedContextUsedP90` — both projections in
one type, since §15.3 and §15.9 use the same delta-projection technique and
both feed `RiskCombiner`), `domain.RiskComponent` (one named risk term —
`Score`, `Calibrated`, `Confidence`, `ReasonCodes`), `domain.DataQuality`
(overall trust signal, independent of any single component's confidence).

A `domain.ReasonCode` type (`string`-based, closed enum) is also
introduced, backed by the ~28 constants already listed in ADD §16.4. The
existing (Phase-1-frozen but not yet consumed by any merged code)
`Evaluation.ReasonCodes` field changes from `[]string` to
`[]domain.ReasonCode` to use it — safe because no Phase 1 code constructs
or reads that field yet.

### Corrected DAG dependency edges

- New node `predictor-05b` (Token Forecaster): depends on `predictor-05`.
- New node `predictor-05c` (Quota Forecaster): depends on `predictor-05b`.
  Cold-start-safe for Phase 2 — a deterministic current-observation-plus-default-delta
  estimate is acceptable pending full empirical calibration once
  `claude-provider-05` (durable telemetry persistence) and `foundation-06`
  (SQLite) land in a later phase, consistent with the existing cold-start
  contract already established for `predictor-04`/`predictor-08`.
- `predictor-07` (Risk Combiner): dependency corrected from
  `predictor-05, predictor-06` to `predictor-05, predictor-05c` — `predictor-06`
  (Runway) removed as a dependency; it was never a valid input to risk
  combination.
- `predictor-08` (Policy): dependency corrected from `predictor-07` to
  `predictor-07, predictor-06` — Policy consumes both the combined risk
  score and the independent runway hit-probability directly (this matches
  `agents/predictor.md`'s existing "Initial policy suggestion" list, which
  already referenced the calibrated ten-minute hit probability as a
  distinct policy input).
- `predictor-11` (Required tests): dependency list extended to include
  `predictor-05b`, `predictor-05c`.

### Terminology note

`Auspex_Predictor_Design_Supplement.md`'s "Risk Estimation" section
calls the third risk term `execution_risk = P(task_requires_multiple_turns)`.
`Auspex_ADD.md` §16.1 already names and formalizes the same concept as
`completion_risk` ("即使 quota/context 足夠，仍需要多輪或未滿足 acceptance
criteria 的風險"), with a complete formula in §16.2. This ADR keeps the
ADD's existing name — `completion_risk` — as the frozen term, since it is
already implemented in formula form and renaming it would fork one
concept under two names, which Constitution §1 (single source of truth)
exists to prevent. `blast_radius_risk` (ADD's fourth component, not named
in the Supplement's shorter list) is retained unchanged; nothing in this
ADR removes it.

## Consequences

- `internal/domain/forecast.go` and `internal/app/ports.go` gain new frozen
  types/interfaces (contract only — no implementation yet, per explicit
  instruction; "approve the ADR, not require a stub first").
- `Auspex_ADD.md` §7.3's C4 diagram, §9.9's interface list, and §33's
  ADR list are updated to reflect this decision.
- `CONTRACT_FREEZE.md` gains a new section documenting the four interfaces,
  the reason-code taxonomy, and the `Evaluation.ReasonCodes` type change.
- The execution DAG gains two new predictor nodes and three corrected
  dependency edges; total remaining predictor task count for Phase 2+
  increases by 2 (from 6 remaining after Phase 1 to 8).
- The Phase 2 predictor assignment proposed before this ADR is superseded —
  regenerated in the same change that lands this ADR.
- No Phase 1 code is affected. No migration, schema, checkpoint format,
  privacy default, or public protocol compatibility changes. This ADR does
  not fall under any of Constitution §3's mandatory-ADR triggers on its own
  merits (it implements, rather than changes, an already-committed ADD
  decision) — it is written anyway because the repository owner's Phase 2
  directive freezes `Auspex_ADD.md`, `CONTRACT_FREEZE.md`, and the DAG
  pending explicit ADR approval, which is what this document provides.
