# Auspex Vertical-Slice Contract Freeze

Status: **ACCEPTED** — Bootstrap stage, executed by the lead directly (see `CONSTITUTION.md` amendment pending re: Bootstrap-as-lead-only-prerequisite, approved by repository owner 2026-07-12).
Contract commit: `4262b4b`
Go module: `github.com/huaiche94/auspex`
Schema baseline: `auspex.event.v1` / `auspex.progress-tree.v1` / `auspex.state-checkpoint.v1` / `auspex.repository-checkpoint.v1` / `auspex.pause.v1` / `auspex.api.v1`

## Import paths

| Concern | Package |
|---|---|
| Domain entities | `github.com/huaiche94/auspex/internal/domain` |
| Cross-component ports | `github.com/huaiche94/auspex/internal/app` |
| Event protocol | `github.com/huaiche94/auspex/pkg/protocol/v1` |
| SQLite runtime | `github.com/huaiche94/auspex/internal/storage/sqlite` (not yet created — `foundation` role) |

## Schema-version strings

```text
auspex.event.v1
auspex.progress-tree.v1
auspex.state-checkpoint.v1
auspex.repository-checkpoint.v1
auspex.pause.v1
auspex.api.v1
```

Defined as constants in `pkg/protocol/v1/event.go` (`SchemaVersionEvent`, etc.), covered by `pkg/protocol/v1/event_test.go`.

## ID and idempotency rules

- All Auspex-owned entity IDs (`internal/domain/ids.go`) are opaque `string`-based types (`RepositoryID`, `WorktreeID`, `SessionID`, `TurnID`, `EvaluationID`, `PredictionID`, `DecisionID`, `TaskID`, `ProgressNodeID`, `StateCheckpointID`, `RepositoryCheckpointID`, `PauseID`, `WakeJobID`, `ResumeAttemptID`, `EventID`) — UUIDv7 at generation time (owned by `foundation`'s `internal/idgen`), never parsed for meaning.
- Event idempotency: `Event.IdempotencyKey` (`pkg/protocol/v1/event.go`) — deterministic per provider event identity where the provider gives a stable ID, else a content digest. Owning role (e.g. `claude-provider`) defines the exact digest algorithm; the field itself is frozen here.
- `CompleteNodeRequest.IdempotencyKey` (`internal/app/ports.go`) — same completion request replayed with the same key MUST return the same result; a different payload under the same key is a conflict, not a silent overwrite (Constitution §6).
- `Authorization` — one-time; consumption is exactly-once, enforced by `predictor` at the storage layer, not by this contract alone.

## Unknown/null semantics

- Optional numeric/measured fields (`UsageObservation`, `QuotaObservation`, `ContextObservation`, `RunwayForecast` in `internal/domain/usage.go`) use Go pointer types (`*int64`, `*float64`, `*time.Time`) — `nil` means **unknown**, never a substituted zero (ADD principle 1: "Unknown is not zero").
- `RunwayForecast.HitProbability` is `*float64` and is only meaningful when `Calibrated == true`; an uncalibrated forecast still reports `RiskScore` (always present, 0–1, never called a probability) — mirrors the ADD §5.1 FR-045 / cold-start contract in `agents/predictor.md`.
- `ProviderCapabilities` (`internal/domain/capability.go`) fields are plain `bool` — a provider adapter reporting `false` MUST mean "confirmed absent," not "not yet checked." An adapter that hasn't checked a capability yet must not call `Capabilities()` until it can answer definitively.

## Transaction boundaries

- `TxRunner.WithTx` (`internal/app/ports.go`) is the single frozen transaction-callback shape every storage-touching operation uses. A non-nil returned error rolls back.
- `ProgressTreeService.CompleteNode` MUST run its artifact-stage-and-verify, node-status-update, and State Checkpoint creation inside one `WithTx` call (or an equivalent outbox-pattern boundary) — partial completion is not a valid state (Constitution §6).
- `EvaluationService.ConsumeAuthorization` MUST be atomic with whatever action it authorizes (e.g. allowing a prompt through) — no window where the authorization is marked consumed but the allowed action didn't happen, or vice versa.
- `GracefulPauseService`'s persist phase (Progress Tree snapshot → State Checkpoint → Repository Checkpoint → Pause Record → Wake Job) is a sequence of dependent writes, not one flat transaction (it spans the `checkpoint` role's two parts) — each step's own transaction boundary is defined by that step's owning service; `runtime` is responsible for sequencing them and handling partial-sequence failure as a resumable state, not a silent gap.

## Error contract

`internal/domain/errors.go` defines the frozen shape:

```go
type ErrorCode string
const (
    ErrCodeValidation ErrorCode = "validation"
    ErrCodeNotFound ErrorCode = "not_found"
    ErrCodeConflict ErrorCode = "conflict"
    ErrCodeUnauthorized ErrorCode = "unauthorized"
    ErrCodeIntegrity ErrorCode = "integrity"
    ErrCodeUnavailable ErrorCode = "unavailable"
    ErrCodeInternal ErrorCode = "internal"
)
type Error struct { Code ErrorCode; Message string; Retryable bool; Details map[string]string }
```

Fail-open vs fail-closed (Constitution §immutable-day-one-rule-10, from the vertical-slice plan): an **operational observation** failure (e.g. a quota read times out) MAY fail open — proceed with `Confidence: ConfidenceUnavailable`, not block the user. A **state-integrity** failure (e.g. a checkpoint's SHA-256 doesn't match, or a transaction partially applied) MUST fail closed — `ErrCodeIntegrity`, `Retryable: false`, and the caller must not proceed as if it succeeded.

## Privacy contract

- Raw prompt text is never a field on any type in `internal/domain` or `pkg/protocol/v1`. `EvaluateTurnRequest.PromptHash` (`internal/app/ports.go`) and `Authorization.PromptHash` are the only prompt-derived fields, and both are hashes, not text.
- `Event.Payload` (`pkg/protocol/v1/event.go`) is a normalized `map[string]any` populated by the owning provider role after redaction — the frozen contract does not itself enforce redaction; that is each provider role's responsibility per its own packet (e.g. `agents/claude-provider.md` §Privacy), verified by `qa`'s leakage scanner (`qa-05`).

## Migration ranges

- 0000–0009 `foundation`
- 0010–0019 `claude-provider`
- 0020–0029 `checkpoint` (Part A — progress/state)
- 0030–0039 `checkpoint` (Part B — repository)
- 0040–0049 `predictor`
- 0050–0059 `runtime` (Part A — pause/scheduler)

`runtime` Part B does not get a range; it does not add schema unless `contract-integrator` explicitly assigns one (`Auspex_Parallel_Execution_Plan.md` §7).

## Predictor pipeline ports (ADR-041)

Frozen 2026-07-12, amending the original Bootstrap contract. Full rationale:
`docs/adr/0041-predictor-forecast-layer.md`.

Four new narrow interfaces in `internal/app/ports.go`, each a distinct
pipeline stage, none implemented yet (contract only):

```text
ScopeEstimator.EstimateScope   -> domain.ScopeEstimate    (ADD §14)
TokenForecaster.ForecastTokens -> domain.TokenForecast     (ADD §15.1-15.2)
QuotaForecaster.ForecastQuota  -> domain.QuotaForecast     (ADD §15.3, §15.9)
RiskCombiner.Combine           -> CombineRiskResult        (ADD §16.1-16.2)
```

Pipeline order: Scope Estimator → Token Forecaster → Quota Forecaster →
Risk Combiner → Policy. **`GracefulPauseService.Observe` (Runway
Forecaster) is independent of this chain** — it is not a `RiskCombiner`
input and `RiskCombiner` is not one of its inputs. This was a real error
in the original Bootstrap-era DAG (`predictor-07` depended on
`predictor-06`); ADR-041 corrects it.

New frozen types in `internal/domain/forecast.go`: `ScopeEstimate` (mirrors
ADD §14.1's field set exactly, pointer-typed numeric fields per the
unknown-is-not-zero rule below), `TokenForecast` (`TokensP50/P80/P90`),
`QuotaForecast` (`ProjectedQuotaUsedP90`, `ProjectedContextUsedP90` — both
projections in one type since they share a delta-projection technique and
both feed `RiskCombiner`), `RiskComponent` (`Score`, `Calibrated`,
`Confidence`, `ReasonCodes`), `DataQuality`. `ReasonCode` is now a typed
`string` enum backed by the ADD §16.4 constant list — `Evaluation.ReasonCodes`
changed from `[]string` to `[]domain.ReasonCode` (safe: no Wave 1 code
constructed or consumed that field).

Terminology: `Auspex_Predictor_Design_Supplement.md` calls the third
risk term "execution_risk"; the frozen contract keeps ADD §16.1's existing
name, `completion_risk` — same concept, one name, per Constitution §1.

Cold-start: `QuotaForecaster` implementations MAY produce a
deterministic current-observation-plus-default-delta estimate
(`Calibrated: false`, `Confidence: ConfidenceLow`) before durable
historical telemetry exists — same discipline already established for
`predictor-04`/`predictor-08`. This is not a stub to be later thrown away;
it is the correct first implementation under this frozen shape.

## Frozen state transitions

Enum sources (all in `internal/domain/status.go`, wire strings verified by `internal/domain/status_test.go`):

- `TurnStatus`: `pending → authorized → running → {pause_pending → pausing → paused → resuming} → {completed | failed | interrupted | blocked | cancelled}`
- `ProgressNodeStatus`: `pending → ready → in_progress → checkpointing → {completed | failed} `, with `paused`, `skipped`, `blocked` as side states reachable from `in_progress`/`ready`.
- `PauseStatus`: `predicted → requested → quiescing → checkpointing → interrupting → sleeping → wake_pending → validating → resuming → resumed`, with `blocked_conflict`, `cancelled`, `failed` as terminal/side states reachable per `agents/runtime.md`'s required state path.

Full per-role transition validation logic belongs to the owning role (`checkpoint` for node transitions, `runtime` for pause transitions) — this file freezes only the enum values and their wire strings, not the transition table implementation.

## What Bootstrap did NOT freeze (intentionally deferred to the owning role)

Per `agents/contract-integrator.md` "Out of scope": no Claude parser, predictor internals, checkpoint store internals, pause state-machine implementation, or CLI handlers exist yet. Request/response DTOs in `internal/app/ports.go` have minimal fields sufficient to compile and express the interface shape — owning roles MAY find they need additional fields; requests for additions go through the role's progress artifact per Constitution §4, not silent edits to `internal/app/ports.go`.

## Amendments

- **2026-07-13 — ADR-044 (REC-01): feature-lookup port frozen.**
  `app.FeatureDataSource` + `app.ResolvedSession` added to
  `internal/app/ports.go`, promoting `internal/evaluation.DataSource`'s
  shape verbatim into the frozen contract (that package now aliases the
  frozen types). `internal/predictor/scope.FeatureSource` and
  `internal/predictor/token.FeatureSource` remain as consumer-side narrow
  views of the same port (interface segregation, documented at each
  definition). This closes the "repository/session feature lookup"
  deferral in the section above; the rest of that section still applies.

- **2026-07-13 — ADR-045: product renamed Preflight → Auspex.** Every
  frozen schema-version string is re-prefixed (`preflight.error.v1` →
  `auspex.error.v1`, `preflight.event.v1` → `auspex.event.v1`,
  `preflight.evaluate.v1` → `auspex.evaluate.v1`, etc.), the module path
  becomes `github.com/huaiche94/auspex`, and the user-data directory
  becomes `auspex/`. Permissible solely because the project is
  pre-release with zero external consumers; after first public release
  this class of change is forbidden by this document's own
  schema-version rules. Historical documents in `docs/archive/` retain
  the old strings by design.
