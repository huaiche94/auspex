# internal/predictor/runway/ — ten-minute quota-exhaustion runway score, independent of the pipeline

> 🌐 English | [繁體中文](README.zh-TW.md)

`Scorer.Score` (`runway.go`) computes a `domain.RunwayForecast` per ADD §15.4–15.5: "is any
active quota window about to run out in the next H seconds" (`DefaultHorizon` = 600s). This is
deliberately NOT part of the Scope → Token → Quota → Risk chain (ADR-041): it is consumed by
`internal/app.GracefulPauseService.Observe` (owned by the runtime role), and
[`internal/policy`](../../policy/README.md) meets it only as a second, independent input.
[`internal/evaluation`](../../evaluation/README.md)'s `EvaluateTurn` never re-runs it — it only
reads back the most recent already-computed forecast.

Mechanics:

- Burn rate is the instantaneous Δused% / Δminutes between the two most recent observations for
  one limit window (`ScoreRequest.Current`/`Previous`). The Scorer is stateless; the caller owns
  observation history.
- ADD §15.4 outlier rules: interval < 2s not counted; negative delta treated as reset/correction;
  rate above a 50 pp/min sanity cap dropped as anomalous; samples staler than 5 min lower
  confidence. With a single interval, P50 = P90 (no spread is fabricated).
- Score comes from the ADD §15.7 uncalibrated fallback thresholds: current used >= 95% → 1.0;
  projected P90 >= 100% within horizon → 0.85; projected >= 95% → 0.65; otherwise a smooth
  headroom-scaled value. A reset landing inside the horizon overrides to low
  (headroom-available, §15.8). `Reached` from the provider is an immediate 1.0.

Cold-start contract (ADD §15.6–15.7): without a durable calibrated burn-rate history (>= 20
valid samples, held-out evaluation, ECE <= 0.08 — never met this phase), `HitProbability` stays
nil and `Calibrated` stays false; `RiskScore` is a deterministic 0–1 score, never presented as a
probability. ReasonCodes here are plain strings, matching the frozen (pre-ADR-041)
`RunwayForecast` shape.

ADD sections cited above live in [Auspex_ADD.md](../../../docs/design/Auspex_ADD.md). See
`doc.go` for the package contract.
