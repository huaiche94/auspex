# Historical Replay Report

> 🌐 English | [繁體中文](Historical_Replay_Report.zh-TW.md)

| Field | Value |
|---|---|
| Phase | 3.5 — Post Phase 2 Analysis |
| Status | **No replay was performed. See §1 for why, stated precisely rather than substituted with invented numbers.** |
| Generated | 2026-07-12 |

## 1. Why this report contains no P50/P80/P90 accuracy numbers

Phase 3.5 asks to replay "every historical telemetry record currently
collected" and compare a "Current Rule Predictor" against a "Proposed Rule
Predictor." Both preconditions for that comparison are absent, and
reporting fabricated numbers to fill the requested format would directly
violate this phase's own governing rule ("Unknown is preferred over
fabricated data"). Stated precisely:

### 1a. No historical telemetry records exist

A "telemetry record" in Auspex's own architecture is a persisted
`turns` / `turn_usage` / `quota_observations` / `context_observations` row
(ADD §12.2) produced by a real coding-agent turn and written durably to
SQLite. None of the infrastructure required to produce or store one
exists yet:

- `foundation-06` (SQLite migrations, core session/turn tables) — not
  built (blocked/deferred past Phase 2; `foundation-05` built only the
  engine, deliberately with zero migrations).
- `claude-provider-05` (idempotent persistence of normalized telemetry
  events into SQLite) — not built (blocked on `foundation-06`).
- `predictor-09` (evaluation persistence) — not built.
- No `auspex` binary has ever observed a real Claude Code turn. The
  Phase 1/2 execution *of Auspex's own development* is not telemetry in
  this sense — it is the work of building the predictor, not output the
  predictor produced.

Zero records exist to replay. This is not a small number; it is an
absence, and is reported as `Unknown`/`N/A` throughout this document
rather than as `0` (which would misleadingly imply a countable-but-empty
result set from a working pipeline, when in fact no pipeline that could
produce such records exists yet).

### 1b. No "Proposed Rule Predictor" exists to compare against "Current"

Phase 3 explicitly forbids modifying predictor coefficients or
implementation in this analysis pass (Phase 3.2, 3.4 both state this).
No alternate/proposed variant of the Rule Predictor was built in Phase 1,
Phase 2, or this analysis phase. There is exactly one implementation —
`RuleScopeEstimator` (`predictor-05`) and the runway `Scorer`
(`predictor-06`) — and nothing to diff it against. A "current vs.
proposed" comparison requires two things being compared; only one exists.

## 2. Per-metric status (as requested by Phase 3.5)

| Requested metric | Status | Provenance | Reason |
|---|---|---|---|
| P50 accuracy | Unknown | Unknown | No historical telemetry to compare predictions against (§1a) |
| P80 accuracy | Unknown | Unknown | Same as above |
| P90 accuracy | Unknown | Unknown | Same as above |
| Average token error | Unknown | Unknown | No token forecaster exists yet (`predictor-05b`, deferred past Phase 2 per ADR-041); no actual-token-usage ground truth exists either (see `Missing_Telemetry_Report.md`) |
| Average duration error | Unknown | Unknown | No duration forecast exists anywhere in the predictor pipeline (the DAG itself has no duration field for any node, let alone the predictor producing one — see `Prediction_Error_Report.md` §0) |
| Scope estimation accuracy | Unknown | Unknown | No historical *coding-agent turn* outcome data exists to score `ScopeEstimate` predictions against. (Note: `predictor-05`'s own unit tests confirm internal consistency — see §3 — which is a different, weaker claim than accuracy against real outcomes.) |
| False Positive Rate | Unknown | Unknown | No policy decisions (ALLOW/WARN/CHECKPOINT/PAUSE/BLOCK) have ever been issued against a real turn; FPR requires a labeled set of decisions with known-correct outcomes, which does not exist |
| False Negative Rate | Unknown | Unknown | Same as above |
| Checkpoint recommendation accuracy | Unknown | Unknown | `predictor-08` (Policy engine, the component that would issue a CHECKPOINT recommendation) is not built |

Every cell is `Unknown`, not `0%` or `N/A` used interchangeably — per this
phase's own rule, `Unknown` is the correct label when a value cannot be
observed, and it is used consistently rather than substituted with a
number that looks like a measurement.

## 3. What DOES exist: internal self-consistency evidence (not accuracy)

To avoid this report being purely negative, the following is real,
Observed evidence from this phase — but it is explicitly a different and
weaker claim than "prediction accuracy," and readers should not conflate
the two:

- `predictor-05`'s test suite (`internal/predictor/scope/estimator_test.go`,
  independently re-run and verified by the lead) confirms `P50 <= P80 <=
  P90` holds across 8 named scenarios covering cold-start, blended
  session-history, and explicit-file-path cases. This is a **structural
  guarantee**, not an accuracy measurement — it proves the output is
  internally well-ordered, not that the ordered values are close to any
  real outcome.
- `predictor-04`'s quantile utility (`internal/predictor/quantile.go`,
  Phase 1) was property-tested across 2000 random trials and never
  violated monotonicity or produced NaN/Inf. Same caveat: this proves the
  *math* is sound, not that it predicts anything real yet, since it has
  never been fed real coding-agent telemetry.
- `predictor-06`'s runway scorer was swept across ~300 input combinations
  (`TestScoreNeverCalibratedNeverPanics`) and always correctly reported
  `Calibrated: false` and `HitProbability: nil` in the absence of durable
  telemetry — this is a **correct refusal to overclaim**, exactly the
  cold-start-safe behavior the Constitution requires, but it is a
  guarantee about *what the predictor honestly says it doesn't know*, not
  a measurement of what it gets right.

## 4. What would make this report producible in a future phase

Listed here as a forward pointer only — not a recommendation to build any
of this now, and not an implementation plan:

1. `foundation-06` + `claude-provider-05` (durable telemetry persistence)
   must exist and must actually observe real Claude Code turns.
2. `predictor-09` (evaluation persistence) must exist so predictions
   themselves are durably recorded alongside the telemetry they predicted.
3. A minimum sample size of completed, outcome-known turns must
   accumulate (ADD §15.2 itself names `count(similar) >= 8` as its own
   internal cold-start-exit threshold — a reasonable first floor for any
   replay analysis, not just live prediction).
4. Only then does "current vs. proposed" comparison become meaningful,
   and only once an actual second (proposed) rule variant is deliberately
   authored as a comparison point.

This is covered in more detail, with named metrics and suggested
implementations, in `Missing_Telemetry_Report.md` (Phase 3.6).
