# Feature Gap Report

| Field | Value |
|---|---|
| Phase | 3.7 (companion to `Feature_Registry.md`) |
| Scope | Every feature registered as `Unknown` or `Available (fixture-scoped only)` in `Feature_Registry.md` |
| Status | Analysis only — no implementation |

This report does not repeat the Feature Registry's identity/provenance
columns (Constitution-analogous rule: don't duplicate the canonical data
dictionary). It adds exactly what the registry does not carry: why each
gap exists, its impact, a suggested closing approach, a complexity
estimate, and an expected improvement — ranked so a future wave can
prioritize.

## Complexity scale used below

Reuses the DAG's own XS/S/M/L/XL convention (`EXECUTION_DAG.md` §1) for
consistency, applied here to a *closing this gap* unit of work, not a
DAG-node estimate — these are not new DAG nodes, just complexity signals
for Wave 3+ planning to draw on.

## 1. Critical-importance gaps

### 1.1 Repository Features wiring (§2 of the Registry)

- **Why missing:** Not a data gap — a wiring gap. `internal/gitx` already
  computes dirty-file/line counts and worktree structure (real, tested).
  `predictor-05`'s `FeatureSource.Repository()` method has no concrete
  implementation, only a test fake.
- **Impact:** `ScopeEstimate.FilesChangedP*`/`LinesChangedP*` — two of the
  Rule Predictor's most important outputs — currently never see real
  repository state, only cold-start defaults and session-history blends.
- **Suggested implementation:** A concrete `FeatureSource` implementation
  in a future predictor wave that calls into `internal/gitx`'s already-built
  primitives. No new gitx functionality needed — this is glue code.
- **Complexity:** S — the hard part (Git introspection) is done; this is
  adapter/wiring work.
- **Expected prediction improvement:** High. This is the single cheapest,
  highest-leverage gap in the entire registry: real data already exists
  one function-call away.

### 1.2 Actual token usage / turn (Registry §4, §5's `TokenForecast`)

- **Why missing:** No live coding-agent session has ever run against
  `preflight` (no CLI exists yet to observe one).
- **Impact:** Blocks Token Forecaster calibration permanently, at every
  tier (Rule/Statistical/ML) — there is no ground truth to calibrate
  against regardless of algorithm sophistication.
- **Suggested implementation:** Requires `runtime-b01`+ (CLI/hook
  integration) and `foundation-06`+`claude-provider-05` (persistence) —
  not a single gap, a dependency chain.
- **Complexity:** L — spans three roles' worth of not-yet-built
  infrastructure, not a single node.
- **Expected prediction improvement:** Critical, but not realizable
  incrementally — this is an all-or-nothing unlock (a Token Forecaster
  with partial telemetry is still uncalibrated per ADD §15.6's ≥20-sample
  gate).

### 1.3 State Checkpoint / Artifact producers (Registry §6)

- **Why missing:** Progress Tree and State Checkpointing (`checkpoint-a01`
  through `-a09`) are entirely unbuilt — blocked on `foundation-06`.
- **Impact:** Not a prediction-quality gap directly (Checkpoint Features
  don't feed the predictor per the Registry's own note), but blocks the
  Constitution's own "completed means evidenced" invariant from having
  any durable enforcement mechanism at all.
- **Suggested implementation:** Already fully specified as DAG nodes
  `checkpoint-a01`-`a09`; no new specification work needed, just
  execution once `foundation-06` unlocks them.
- **Complexity:** XL — `checkpoint-a04` (`CompleteNode` atomic protocol)
  is independently flagged elsewhere in this project's own documentation
  as the single most consequential task in the whole DAG.
- **Expected prediction improvement:** None directly (see Impact above) —
  listed as Critical importance for a different reason (product integrity,
  not prediction accuracy) and included here for registry completeness,
  not because closing it improves calibration.

## 2. High-importance gaps

### 2.1 Provider Features moving from fixture-scoped to live (Registry §4)

- **Why missing:** `claude-provider`'s parsers/normalizer are real and
  correctly tested, but have only ever parsed hand-constructed JSON, never
  a byte emitted by an actual Claude Code process.
- **Impact:** Every downstream consumer of `UsageObservation`/
  `QuotaObservation`/`ContextObservation` inherits the same "proven
  correct on fixtures, unproven on reality" status. This is the largest
  concentration of "looks done but isn't validated against the real world"
  risk in the current codebase.
- **Suggested implementation:** A live integration smoke test — run the
  real `claude` CLI (or a controlled proxy of it) once, capture real
  status-line/hook output, and confirm the existing parsers handle it
  without modification. This does not require the full CLI to be built
  first; it could be a narrow, one-off validation exercise.
- **Complexity:** M — mostly about safely obtaining a real sample, not
  about writing new code (the parsers already exist).
- **Expected prediction improvement:** Does not itself improve any
  prediction number, but converts an unverified assumption into a verified
  one before more infrastructure is built on top of it — a risk-reduction
  gap, not an accuracy gap.

### 2.2 Session Features (Registry §3)

- **Why missing:** Entirely dependent on A5 (`Missing_Telemetry_Report.md`)
  — historical outcome/usage data that does not exist yet.
- **Impact:** Blocks the entire Version 2 "Statistical Predictor" tier
  (Design Supplement's own roadmap) — this feature group's stated purpose
  is exactly what a statistical predictor needs and a rule predictor
  doesn't strictly require.
- **Suggested implementation:** No shortcut — accumulate real turns first.
- **Complexity:** XL (same dependency chain as 1.2, not independently
  smaller).
- **Expected prediction improvement:** Critical, but only realizable after
  1.2 is closed — these two gaps share the same root blocker.

## 3. Medium-importance gaps

### 3.1 `ProviderCapabilities` real detection (Registry §4)

- **Why missing:** No `ProviderCapabilityReader` implementation exists;
  the type is Bootstrap-frozen but capability detection was never wired
  to any provider.
- **Impact:** Every capability-gated behavior in the ADD (degradation
  rules, ADD §8.8) currently has no runtime signal to gate on.
- **Suggested implementation:** A `claude-provider` deliverable — detect
  version/mode and populate the 19-field struct once, per session.
- **Complexity:** S — narrow, well-specified surface (ADD §8.6/§8.7
  already enumerate exactly what to detect).
- **Expected prediction improvement:** Low-medium directly on prediction
  *accuracy*, but meaningfully reduces the risk of a prediction being
  silently wrong because a capability was assumed rather than confirmed.

### 3.2 Repository dependency-graph fan-out (Registry §2, ADD §14.4)

- **Why missing:** Specified in the ADD, never implemented — no code
  calls `go list -deps -json` or `dotnet list ... reference` anywhere in
  this codebase yet.
- **Impact:** `blast_radius_risk`'s cross-layer/cross-project terms
  currently have no real signal to draw on once `RiskCombiner` is built.
- **Suggested implementation:** ADD §14.4 already specifies the exact
  commands and caching strategy; this is implementation of an existing
  spec, not new design work.
- **Complexity:** M.
- **Expected prediction improvement:** Medium — narrows one specific,
  named risk term, not a broad accuracy gain.

## 4. Low-importance gaps

### 4.1 Files-read tracking (Registry §1, §5)

- **Why missing:** No provider event distinguishes "read" from "changed"
  in this project's current capability matrix.
- **Impact:** `ScopeEstimate.FilesReadP*` stays permanently estimated,
  never ground-truthed, without provider-adapter-level work per provider.
- **Suggested implementation:** Per-provider tool-call-shape heuristic
  (see `Missing_Telemetry_Report.md` A3) — provider-adapter work, not
  predictor work.
- **Complexity:** M per provider (repeats for each new provider adapter).
- **Expected prediction improvement:** Low-medium — `FilesReadP*` is a
  named field but not currently consumed by any downstream formula this
  wave built (Token Forecaster, which would use it, is itself unbuilt).

### 4.2 Calibration statistics (ECE/Brier) (Registry §7)

- **Why missing:** Requires ≥20 real, labeled samples (ADD §15.6) —
  volume gap, not an implementation gap.
- **Impact:** Gates the transition from "risk score" to "calibrated
  probability" project-wide.
- **Suggested implementation:** No implementation shortcut exists or
  should exist — ADR-026/033 explicitly forbid working around this gate.
- **Complexity:** N/A — this is a data-volume threshold, not a build task.
- **Expected prediction improvement:** N/A directly; it is the metric that
  *measures* improvement for every calibrated tier, not a feature that
  produces improvement itself.

## 5. Ranked summary

| Rank | Gap | Importance | Complexity | Improvement if closed | Blocked on |
|---|---|---|---|---|---|
| 1 | Repository Features wiring (1.1) | Critical | S | High | Nothing — closeable now |
| 2 | Provider live-data validation (2.1) | High | M | Risk-reduction, not accuracy | Nothing — closeable now |
| 3 | `ProviderCapabilities` detection (3.1) | Medium | S | Low-medium | Nothing — closeable now |
| 4 | Repository dependency-graph fan-out (3.2) | Medium | M | Medium | Nothing — closeable now |
| 5 | Files-read tracking (4.1) | Low | M/provider | Low-medium | Provider adapter maturity |
| 6 | Actual token usage / Session Features (1.2, 2.2) | Critical | L/XL | Critical, but all-or-nothing | `runtime-b01`+, `foundation-06`, `claude-provider-05` |
| 7 | State Checkpoint producers (1.3) | Critical (product, not prediction) | XL | None (not a prediction gap) | `foundation-06` |
| 8 | Calibration statistics (4.2) | Critical | N/A (volume, not build) | N/A (a measurement, not a feature) | Gap #6 |

**Read this ranking carefully**: rank 1-4 are genuinely closeable in a
near-term wave with no other prerequisite. Rank 6-8 are not smaller
versions of the same kind of gap — they are blocked on multi-role
infrastructure that doesn't exist, and no amount of predictor-side
cleverness closes them faster. `Wave3_Recommendation.md` uses this
ranking directly.
