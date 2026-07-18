# Missing Telemetry Report

> 🌐 English | [繁體中文](Missing_Telemetry_Report.zh-TW.md)

| Field | Value |
|---|---|
| Phase | 3.6 — Post Phase 2 Analysis |
| Status | Analysis only |
| Scope | Two categories: (A) **Product telemetry** — data Auspex's own predictor needs from a real coding-agent session, none of which has ever been captured because no live session has been run; (B) **Process telemetry** — data about implementing Auspex itself that this phase's execution could have captured but did not, per `Prediction_Error_Report.md` |

## A. Product telemetry (Auspex's predictor inputs)

### A1. Actual token usage per turn

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** No live Claude Code (or Codex) session has ever run
  against an `auspex` binary — the CLI (`runtime-b01`+) doesn't exist
  yet, so there is no integration point that could observe a real turn.
- **Provider limitation:** Per ADD §8.7's own capability matrix, even once
  integrated, exact per-turn usage is "partial/status-derived" for Claude
  native hooks/status and "stream dependent" for Claude managed
  stream-json — not guaranteed complete or exact in every mode.
- **Possible deterministic workaround:** ADD §14.7's prompt-token
  approximation formula (`ceil(ASCII/4.0) + ceil(CJK/1.5) +
  ceil(other-Unicode/2.5) + ceil(punctuation/3.0)`, `confidence=low`) —
  already specified, not yet wired to any live input.
- **Expected impact on prediction quality:** Critical. Without this, the
  Token Forecaster (`predictor-05b`, not built) has no ground truth to
  calibrate against, ever — every prediction it makes stays permanently
  at `Calibrated: false`.
- **Suggested future implementation:** `claude-provider-05` (persist
  telemetry) + a live status-line/hook integration are the direct
  prerequisites; no shortcut exists.

### A2. Remaining five-hour quota

- **Current status:** Estimated (only once live telemetry exists; right
  now, Unknown — no live session exists to estimate from)
- **Provenance:** Unknown (would become Derived once a rolling-usage
  ledger exists, per ADD §15.9's "Realistic Case")
- **Why unavailable:** Same root cause as A1 — no live session.
- **Provider limitation:** ADD §8.7: quota exposure is "sidecar App
  Server" for Codex native hooks, "yes for supported subscribers" for
  Claude native hooks — not universal even when live.
- **Possible deterministic workaround:** ADD §15.9's rolling-usage ledger
  (`Σ tokens within last five hours`) with an `effective_limit` estimated
  from observed limit events rather than assumed fixed — already
  specified in the ADD, not yet implemented (no persistence layer exists
  to hold the ledger).
- **Expected impact on prediction quality:** Critical for the Quota
  Forecaster (`predictor-05c`, not built) and for Runway (`predictor-06`,
  built this phase but currently only exercised with synthetic test
  inputs, never a real quota observation).
- **Suggested future implementation:** `foundation-06` (SQLite migrations
  for `quota_observations`) is the direct prerequisite.

### A3. Actual files read (vs. files changed) during a real turn

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** No provider telemetry source in this project's
  capability matrix (ADD §8.6-8.7) currently distinguishes "files the
  agent read" from "files the agent changed" as a countable metric — only
  file-change events (`FileChangeEvents` capability) are named.
- **Provider limitation:** Providers generally don't emit a structured
  "file read" event distinct from tool-call logs; this would likely need
  to be inferred from tool-call payloads (e.g. a `Read`/`cat`/`grep`
  invocation), which is provider- and tool-schema-specific.
- **Possible deterministic workaround:** Parse tool-call events for
  known read-shaped tool names per provider, as a heuristic lower bound —
  not implemented, not validated.
- **Expected impact on prediction quality:** Medium-High. `ScopeEstimate`
  already has `FilesReadP50/P80/P90` fields (frozen, ADD §14.1) that
  `predictor-05` currently cannot populate from real data — it estimates
  them from cold-start defaults and session-history blending, never from
  ground truth, because no ground truth for this field has ever existed.
- **Suggested future implementation:** Define a provider-specific
  tool-call-to-"file read" mapping as part of each provider adapter
  (`claude-provider`, future `codex` adapter), not the predictor itself.

### A4. Actual duration of a real coding-agent turn

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** No live turn has ever been observed end-to-end.
- **Provider limitation:** None expected in principle — turn start/end
  timestamps are a basic capability most providers expose — this is
  purely an integration-not-built gap, not a provider constraint.
- **Possible deterministic workaround:** None needed once integrated;
  this is the one metric in this report with no inherent provider-side
  obstacle.
- **Expected impact on prediction quality:** High. `ScopeEstimate`
  already has `DurationP50`/`DurationP90` fields (ADD §14.1, frozen) that
  are currently always `nil` in every `predictor-05` output.
- **Suggested future implementation:** Straightforward once
  `runtime-b01`+ exists and observes a real turn's start/completion
  events.

### A5. Historical outcome labels (completed_normally / hit_usage_limit / required_compaction / user_interrupted / tool_failure / required_followup_turn)

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** No turn has ever completed (or failed) under
  Auspex's observation; these labels (named in
  `Auspex_Predictor_Design_Supplement.md`'s "Better Statistical
  Models" section) require a persisted, terminated turn to assign.
- **Provider limitation:** None expected — failure/completion signals are
  generally observable once integrated (`domain.FailureClass`, already
  frozen, already has the needed taxonomy: `FailureProviderRateLimit`,
  `FailureTimeout`, `FailureUserInterrupt`, etc.).
- **Possible deterministic workaround:** None needed; this is purely a
  "not yet collecting" gap once the persistence layer exists.
- **Expected impact on prediction quality:** Critical for any Version 2+
  (statistical/ML) predictor tier — these labels are the training targets
  named in the Predictor Design Supplement's own roadmap. The current
  Version 1 Rule Predictor does not need them to function (by design,
  cold-start-safe), but cannot improve past Version 1 without them.
- **Suggested future implementation:** `predictor-09` (evaluation
  persistence) + `predictor-10` (authorization, for turn/decision
  linkage) are the direct prerequisites.

### A6. Calibration statistics (ECE, Brier score)

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** ADD §15.6 requires ≥20 valid runway samples plus a
  held-out evaluation before `hit_probability` may even be computed as a
  true probability. Zero samples exist.
- **Provider limitation:** None — this is purely a volume/time gap.
- **Possible deterministic workaround:** None; the ADD deliberately
  forbids working around this (ADR-026/033: uncalibrated scores must
  never be presented as probabilities).
- **Expected impact on prediction quality:** This *is* the prediction
  quality metric for the calibrated tier — its absence is why every
  `RunwayForecast`/`TokenForecast`/`QuotaForecast` this phase's tests
  produce is correctly `Calibrated: false`.
- **Suggested future implementation:** Accumulate real samples via A1-A5
  above; no shortcut.

### A7. Repository dependency-graph fan-out (Go `go list -deps`, .NET project references)

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** ADD §14.4 specifies this (`go list -deps -json
  ./...`, `dotnet list <project> reference`) but no `predictor-05`
  deliverable this phase consumed it — `predictor-05`'s `FeatureSource`
  workaround interface (see `Wave2_Lessons.md` §1, issue #2b) has no
  dependency-graph field yet.
- **Provider limitation:** None — this is a local, deterministic
  repository-introspection call, not provider telemetry at all.
- **Possible deterministic workaround:** The ADD's own commands are
  already the workaround (cached, background-refreshed per §14.4) —
  simply not wired into any predictor input yet.
- **Expected impact on prediction quality:** Medium. Named explicitly in
  ADD §14.2 "Repository-derived" features as a signal for cross-layer/
  cross-project change detection, which feeds `blast_radius_risk`.
- **Suggested future implementation:** Extend the `FeatureSource`
  interface `predictor-05` introduced, or fold into a proper frozen
  `app` port once one exists (see `ADR_Recommendations.md`).

## B. Process telemetry (implementing Auspex itself)

### B1. Per-node token usage

- **Current status:** Observed only at teammate-per-phase granularity (10
  of 19 nodes share an invocation with sibling nodes and cannot be split)
- **Provenance:** Observed (aggregate) / Unknown (per-node)
- **Why unavailable:** The harness reports one usage total per background
  agent invocation, not a per-tool-call or per-node breakdown; teammates
  were not asked to self-report token counts (they cannot observe their
  own token usage from inside the conversation).
- **Provider limitation:** N/A — this is a harness/tooling capability
  gap, not a provider limitation in the product sense.
- **Possible deterministic workaround:** Assign exactly one node per
  background-agent invocation in future phases, trading parallelism
  efficiency for clean per-node attribution — a real tradeoff, not a free
  fix.
- **Expected impact on prediction quality:** Directly blocks
  `Prediction_Error_Report.md`'s per-node token-error computation for 17
  of 19 nodes (§2 of that report).
- **Suggested future implementation:** Either the harness exposes a
  per-turn token count teammates can self-report, or Phase 3 planning
  deliberately trades some parallelism for measurement cleanliness on a
  sampled subset of nodes.

### B2. Precise (non-self-reported) node duration

- **Current status:** Unknown for 10 of 19 nodes; Estimated
  (self-reported, not clock-instrumented) for the other 9
- **Provenance:** Estimated at best
- **Why unavailable:** No wall-clock timestamping tool was given to any
  teammate; "~20 min" style figures are the executing model's own
  approximation, not a measurement.
- **Provider limitation:** N/A
- **Possible deterministic workaround:** Have the lead record
  spawn-timestamp and completion-notification-timestamp per agent
  invocation (the lead does receive real timestamps via task
  notifications) — this was not done systematically this phase and is a
  process gap, not a tooling gap.
- **Expected impact on prediction quality:** Directly blocks any duration
  estimate in a future DAG (`Calibration_Report.md` §8 names this as a
  top improvement priority).
- **Suggested future implementation:** Lead records invocation
  start/end wall-clock time from its own tool-call timestamps in Phase 3,
  independent of teammate self-report.

### B3. Files read per node (process-scoped)

- **Current status:** Unknown
- **Provenance:** Unknown
- **Why unavailable:** Same as A3's product-scoped version — no teammate
  or tool tracked a distinct "files read" count separate from "files
  changed."
- **Provider limitation:** N/A
- **Possible deterministic workaround:** `tool_uses` counts exist in
  harness usage blocks but conflate Read/Write/Edit/Bash — not a valid
  substitute (see `Prediction_Error_Report.md` §0's explicit refusal to
  relabel this as a files-read proxy).
- **Expected impact on prediction quality:** Low-medium for process
  calibration specifically (files-changed is the more DAG-relevant
  signal); would matter more for product-scope A3.
- **Suggested future implementation:** Not prioritized — see A3 for the
  product-scoped version, which matters more.

## C. Summary table

| Metric | Category | Status | Blocks |
|---|---|---|---|
| Actual token usage/turn | Product | Unknown | Token Forecaster calibration |
| Remaining 5h quota | Product | Unknown (Estimated once live) | Quota Forecaster, Runway calibration |
| Files read/turn | Product | Unknown | `ScopeEstimate.FilesReadP*` ground truth |
| Turn duration | Product | Unknown | `ScopeEstimate.DurationP*` ground truth |
| Outcome labels | Product | Unknown | Any Version 2+ predictor tier |
| Calibration stats (ECE/Brier) | Product | Unknown | Probability-based auto-pause (ADR-033 gate) |
| Repo dependency fan-out | Product | Unknown | `blast_radius_risk` cross-layer/cross-project signal |
| Per-node token usage | Process | Observed (aggregate only) | `Prediction_Error_Report.md` per-node token error |
| Precise node duration | Process | Estimated (self-report) | Any future DAG duration field |
| Files read per node | Process | Unknown | Low priority |
