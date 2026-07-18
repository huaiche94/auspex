# internal/predictor/scope/ — Stage 1: rule-based scope estimation for the upcoming turn

> 🌐 English | [繁體中文](README.zh-TW.md)

`RuleScopeEstimator` (`estimator.go`) implements the frozen `app.ScopeEstimator` port (ADR-041,
predictor-05): it predicts what work a turn is expected to require — files read/changed and lines
changed as P50/P80/P90 triples, plus boolean requirement flags (unit/integration tests,
cross-project, migration-likely, security-sensitive) — from prompt, repository, session, and
Progress-Tree features.

Inputs come through `FeatureSource`, a narrow consumer-side view of the frozen
`app.FeatureDataSource` port (ADR-044), satisfied in production by
[`internal/evaluation`](../../evaluation/README.md)'s `SQLDataSource`.

How the estimate is built:

- Cold-start base: the ADD §14.6 bootstrap table (`coldstart.go`), verbatim for the 8 classes the
  ADD names, plus a documented nearest-neighbor fallback for the other 8 §14.3 classes. The ADD is
  explicit that these are bootstrap values, not a universal benchmark.
- Empirical blend: once a session supplies recent-turn quantiles (the ADD §15.2 ">= 8 samples"
  gate, reused here as `MinSessionSamples`), they are averaged with the base — blended, not
  replaced. This raises Confidence to medium but never sets Calibrated=true.
- Widening-only adjustments: repository fan-out and long remaining critical path widen the P90
  tail; explicit paths named in the prompt floor the files-read estimate.
- `sortTriple` enforces P50 <= P80 <= P90 unconditionally, mirroring
  [`internal/predictor.Quantiles`](../README.md)' own guarantee.

`ToolCallsP50/P90`, `VerificationP50/P90`, `RetryLoopsP50/P90`, and `DurationP50/P90` are left
nil this phase — no tool-call or verification telemetry is wired up yet, and nil means unknown,
never zero.

The output `domain.ScopeEstimate` feeds [`token/`](../token/README.md) (multipliers) and
[`risk/`](../risk/README.md) (completion/blast-radius terms). ADD sections cited above live in
[Auspex_ADD.md](../../../docs/design/Auspex_ADD.md). See `doc.go` for the package contract.
