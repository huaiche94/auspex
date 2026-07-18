# internal/evaluation/ — the EvaluateTurn pipeline: run, persist, decide, authorize

> 🌐 English | [繁體中文](README.zh-TW.md)

`Service` (`service.go`) implements the frozen `app.EvaluationService` contract
(`internal/app/ports.go`, ADD §9.9) — the evaluate/decide/authorize surface every provider hook
handler and runtime orchestrator calls through, never a concrete predictor/policy type directly.

Key entry points:

- `EvaluateTurn` runs the full ADR-041 chain (`pipeline.go`): Scope → Token → Quota → Risk via
  the four `app.*` pipeline ports (implemented by
  [`internal/predictor/{scope,token,quota,risk}`](../predictor/README.md)), then
  [`internal/policy`](../policy/README.md)'s `Decider`. It persists a `feature_vectors` row
  (migration 0040), a `predictions` row (0041), and a `policy_decisions` row (0043) inside one
  transaction — a prediction without its decision is a partial evaluation, not a valid state.
- `Decide` is read-back, not recompute: `app.DecideRequest` carries only an EvaluationID, so it
  returns the `policy_decisions` row `EvaluateTurn` already stored.
- `IssueAuthorization` / `ConsumeAuthorization` (migration 0044): one-time authorizations with
  exactly-once consumption, clock-bound expiry, and prompt/session binding enforced atomically in
  the storage layer (`authorization_test.go` covers the replay/binding hardening, predictor-10).
- `ForecastCard` / `LatestForecastCard` / `StatusLineText` (`forecastcard.go`): the issue-#14
  presenter that reads persisted rows back for the UserPromptSubmit hook, the statusline, and
  `auspex evaluate`. `Probability` is structurally nil (JSON null) until a calibration phase
  persists one, and surfaces label uncalibrated output as an estimate (Constitution principle #2).
- `DataSource` (`datasource.go`) is now an alias for the frozen `app.FeatureDataSource` port
  (ADR-044); `SQLDataSource` (`datasource_sql.go`) implements it over SQLite, feeding
  [`internal/features`](../features/README.md)-shaped DTOs to the pipeline stages.

The independent runway forecast ([`internal/predictor/runway`](../predictor/runway/README.md))
is never recomputed here — the DataSource supplies the most recent already-computed one so the
policy runway gate can be evaluated. Cost ranges come from
[`internal/pricing`](../pricing/README.md). No raw prompt text ever reaches this package — only
its hash (privacy contract). ADD sections cited in code live in
[Auspex_ADD.md](../../docs/design/Auspex_ADD.md). See `doc.go` for the package contract.
