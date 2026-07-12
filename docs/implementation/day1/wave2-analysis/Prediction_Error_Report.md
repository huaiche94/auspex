# Prediction Error Report

| Field | Value |
|---|---|
| Phase | 3.1 — Post Wave 2 Analysis |
| Scope | Every completed node, Bootstrap through Wave 2 (19 executed units) |
| Status | Analysis only. No implementation, DAG, or contract changes. |
| Generated | 2026-07-12 |

## 0. Method and honesty constraints

Every value below is labeled with one of: **Observed** (measured directly by
the lead via `git diff --stat`, `git show`, or a harness-reported usage
block), **Estimated** (a self-report from the executing agent, not
independently measured — e.g. "~20 min wall-clock" from a model with no
clock access), **Derived** (computed from other values), or **Unknown**
(no data exists; not fabricated).

Two structural facts constrain this entire report and recur in every row:

1. **The frozen DAG (`docs/implementation/day1/EXECUTION_DAG.md`) has no
   duration field and no token field anywhere, for any node.** Every
   `estimated_duration` and `estimated_token_usage` cell below is
   therefore `Unknown` — not a small number, not zero. This is the
   single largest finding of this report and is treated at length in
   Phase 3.6.
2. **No "files read" metric was captured by any teammate for any node.**
   Only "files changed" (files written/modified) was tracked. Tool-call
   counts (`tool_uses` in harness usage blocks) exist but conflate reads,
   writes, edits, and shell commands — reporting them as a "files read"
   proxy would be a fabrication by relabeling. `actual_files_read` is
   `Unknown` for every node.

Actual token usage is only `Observed` at **teammate-per-wave granularity**
(one number per background-agent invocation, covering every node that
invocation completed), never at per-node granularity, because the
sub-agents did not self-report a token breakdown per node and the harness
does not expose one. Where a node shares a teammate-wave token total with
sibling nodes, this is stated explicitly rather than divided evenly and
presented as if it were per-node data.

## 1. Node-by-node comparison

### Bootstrap (lead-executed, not a Wave)

| Metric | Estimated | Actual | Provenance (est. / act.) | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 24 (sum of contract-integrator-01..07 DAG rows) | 18 (`git show --stat 4262b4b`) | Observed / Observed | 6 | 25.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 1,650 (sum of DAG rows, treated as insertions) | 979 insertions (`git show --stat 4262b4b`) | Observed / Observed | 671 | 40.7% |
| Duration | Unknown (DAG has no field) | Unknown (no wall-clock instrumentation available; lead-executed inline, not timed) | Unknown / Unknown | — | — |
| Token usage | Unknown | Unknown (Bootstrap was executed directly by the lead conversation, not a sub-agent invocation — no isolated usage block exists) | Unknown / Unknown | — | — |
| Complexity | M (per-task average, contract-integrator-01..07) | L (self-assessed: "larger as one continuous unit than the sum of its parts suggested") | Observed / Derived | — | — |

### foundation-01

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 4 | 9 (`git show --stat 797c450`) | Observed / Observed | 5 | 125.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 120 | 303 insertions | Observed / Observed | 183 | 152.5% |
| Duration | Unknown | ~25 min (self-reported) | Unknown / Estimated | — | — |
| Token usage | Unknown | 63,688 (harness-observed, this teammate-invocation covered only this one node) | Unknown / Observed | — | — |
| Complexity | S | S (held, per self-assessment) | Observed / Derived | 0 | 0% |

### foundation-02

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 3 | 4 (`git show --stat 2820015`) | Observed / Observed | 1 | 33.3% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 180 | 489 insertions | Observed / Observed | 309 | 171.7% |
| Duration | Unknown | ~20 min (self-reported) | Unknown / Estimated | — | — |
| Token usage | Unknown | Shared with foundation-03/04/05/09 — this teammate's Wave 2 invocation totaled 191,727 across all 5 nodes; no per-node split exists | Unknown / Unknown (per-node) | — | — |
| Complexity | S | S (held) | Observed / Derived | 0 | 0% |

### foundation-03

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 4 | 5 (`git show --stat 0164673`) | Observed / Observed | 1 | 25.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 250 | 563 insertions | Observed / Observed | 313 | 125.2% |
| Duration | Unknown | ~25 min (self-reported) | Unknown / Estimated | — | — |
| Token usage | Unknown | Shared Wave 2 total (see foundation-02) | Unknown / Unknown (per-node) | — | — |
| Complexity | M | S–M (self-assessed as simpler than M) | Observed / Derived | — | — |

### foundation-04 (reduced scope: `internal/lock` only)

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 6 (original full-scope DAG estimate — **not comparable**, see note) | 4 (`git show --stat 1ce3c50`) | Observed (stale) / Observed | N/A — not apples-to-apples | N/A |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 150 (original full-scope estimate — **not comparable**) | 370 insertions | Observed (stale) / Observed | N/A | N/A |
| Duration | Unknown | ~20 min (self-reported) | Unknown / Estimated | — | — |
| Token usage | Unknown | Shared Wave 2 total (see foundation-02) | Unknown / Unknown (per-node) | — | — |
| Complexity | S (reduced, no distinct re-estimate issued) | S (held) | Derived / Derived | 0 | 0% |

**Note:** the DAG's foundation-04 row was never re-issued with a reduced-scope estimate after `clock`/`idgen` were pulled forward into `foundation-01`. Comparing the stale 6-files/150-LOC full-scope number against the actual reduced-scope (`internal/lock`-only) output would compute a misleading error and is deliberately not reported as a percentage. This gap is itself a finding — see §2.

### foundation-05

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 5 | 6 (`git show --stat b0ef5a0`) | Observed / Observed | 1 | 20.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 350 | 1,138 insertions | Observed / Observed | 788 | 225.1% |
| Duration | Unknown | ~45 min (self-reported; longest node this wave) | Unknown / Estimated | — | — |
| Token usage | Unknown | Shared Wave 2 total (see foundation-02) | Unknown / Unknown (per-node) | — | — |
| Complexity | M | M (held; "High risk flag earned its keep," per self-assessment, independently corroborated by lead review of pragma tests) | Observed / Derived | 0 | 0% |

### foundation-09

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 6 | 11 (`git show --stat 2eac579`, includes 9 pre-existing files touched only for lint fixes) | Observed / Observed | 5 | 83.3% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 150 | 195 insertions, 18 deletions | Observed / Observed | 45 (insertions only) | 30.0% |
| Duration | Unknown | ~35 min (self-reported) | Unknown / Estimated | — | — |
| Token usage | Unknown | Shared Wave 2 total (see foundation-02) | Unknown / Unknown (per-node) | — | — |
| Complexity | XS | S (held slightly above XS) | Observed / Derived | — | — |

### claude-provider-01

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 8 | 7 (self-reported breakdown, verified by lead against actual worktree contents during Wave 1 review; the underlying commit `69462cc` bundles all of -01/-02/-03 together, see §0 combined-commit note) | Observed / Observed (verified) | 1 | 12.5% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 300 | Not separable — part of a 1,558-insertion combined commit across 29 files for -01/-02/-03; no per-node LOC split exists | Observed / Unknown (per-node) | — | — |
| Duration | Unknown | Unknown ("not tracked," per lessons-learned) | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 1 total — see §2 combined-invocation caveat below | Unknown / Unknown (per-node) | — | — |
| Complexity | M | M (held) | Observed / Derived | 0 | 0% |

### claude-provider-02

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 6 | 9 (self-reported, verified) | Observed / Observed (verified) | 3 | 50.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 300 | Not separable (see claude-provider-01) | Observed / Unknown (per-node) | — | — |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 1 total | Unknown / Unknown (per-node) | — | — |
| Complexity | M | M (held) | Observed / Derived | 0 | 0% |

### claude-provider-03

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 6 | 12 (self-reported, verified) | Observed / Observed (verified) | 6 | 100.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 250 | Not separable (see claude-provider-01) | Observed / Unknown (per-node) | — | — |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 1 total | Unknown / Unknown (per-node) | — | — |
| Complexity | M | M (held, "slightly more iteration") | Observed / Derived | 0 | 0% |

### claude-provider-04

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 6 | 5 (incl. progress docs; `git show --stat d4d2869` shows 3 code files, +2 for progress/lessons docs verified separately) | Observed / Observed | 1 | 16.7% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 400 | 689 insertions (`git show --stat d4d2869`) | Observed / Observed | 289 | 72.3% |
| Duration | Unknown | Unknown ("one continuous pass," no wall-clock given) | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 2 total — this teammate's invocation totaled 107,101 across -04 and -06; no per-node split exists | Unknown / Unknown (per-node) | — | — |
| Complexity | L | M–L (self-assessed as closer to M once the design question resolved) | Observed / Derived | — | — |

### claude-provider-06

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 3 | 5 (incl. progress docs; `git show --stat 0dbe22b` shows 3 code files, +2 docs) | Observed / Observed | 2 | 66.7% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 100 | 164 insertions | Observed / Observed | 64 | 64.0% |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 2 total (see claude-provider-04) | Unknown / Unknown (per-node) | — | — |
| Complexity | S | S (held) | Observed / Derived | 0 | 0% |

### checkpoint-b02

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 5 | 9 (`git show --stat 9b222d0`; self-reported as "4 impl + 3 test" = 7 — discrepancy is the 2 progress-doc files, confirmed via diff) | Observed / Observed | 4 | 80.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 400 | 1,095 insertions | Observed / Observed | 695 | 173.8% |
| Duration | Unknown | Unknown (interrupted mid-session, resumed; no clean wall-clock exists across the interruption boundary) | Unknown / Unknown | — | — |
| Token usage | Unknown | Two segments exist due to a harness-level interruption: first segment reported 495 tokens (implausibly low for the file-writing work actually done — flagged unreliable, see §0), resumed segment reported 76,382 tokens. Neither number alone is a trustworthy total. | Unknown / Unreliable-Observed | — | — |
| Complexity | L | L (held) | Observed / Derived | 0 | 0% |

### checkpoint-b03

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 3 | 5 (`git show --stat 0281b97`; 3 code files + 2 progress docs) | Observed / Observed | 2 | 66.7% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 250 | 876 insertions, 1 deletion | Observed / Observed | 626 | 250.4% |
| Duration | Unknown | ~20 min (self-reported) | Unknown / Estimated | — | — |
| Token usage | Unknown | 79,613 (harness-observed, this teammate's single-node Wave 2 invocation) | Unknown / Observed | — | — |
| Complexity | M | M (held) | Observed / Derived | 0 | 0% |

### predictor-02

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 3 | 3 (`git show --stat 4c22e0b`) | Observed / Observed | 0 | 0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 150 | 409 insertions | Observed / Observed | 259 | 172.7% |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 1 total — see §2 combined-invocation caveat | Unknown / Unknown (per-node) | — | — |
| Complexity | S | S (held) | Observed / Derived | 0 | 0% |

### predictor-03

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 5 | 4 (`git show --stat 6ed8657`) | Observed / Observed | 1 | 20.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 300 | 409 insertions | Observed / Observed | 109 | 36.3% |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 1 total | Unknown / Unknown (per-node) | — | — |
| Complexity | M | M (held, "slightly lighter than expected") | Observed / Derived | 0 | 0% |

### predictor-04

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 4 | 3 (`git show --stat 3bbd49f`) | Observed / Observed | 1 | 25.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 250 | 256 insertions | Observed / Observed | 6 | 2.4% |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Two segments due to interruption: first 772 tokens (unreliable, see §0), resumed segment 103,097 tokens, covering predictor-02/03/04 combined | Unknown / Unreliable-Observed | — | — |
| Complexity | M | S/M (self-assessed as simpler than M) | Observed / Derived | — | — |

### predictor-05

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 4 | 4 (self-reported: `doc.go, coldstart.go, estimator.go, estimator_test.go`; not independently re-diffed per-node since predictor-05/06 share one lead-side diff against the Wave 1 tip) | Observed (est.) / Estimated (self-reported, not independently split by lead) | 0 | 0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 300 | Not separable — predictor-05 and predictor-06 together total 1,424 insertions across 8 files (`git diff --stat 4f96d7f..HEAD` on `day1/predictor`); no independently-verified per-node split exists | Observed / Unknown (per-node) | — | — |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 2 total — this teammate's invocation totaled 138,172 across -05 and -06; no per-node split exists | Unknown / Unknown (per-node) | — | — |
| Complexity | M | M (held) | Observed / Derived | 0 | 0% |

### predictor-06

| Metric | Estimated | Actual | Provenance | Abs. error | % error |
|---|---|---|---|---|---|
| Files changed | 4 | 3 (self-reported: `doc.go, runway.go, runway_test.go`) | Observed (est.) / Estimated (self-reported) | 1 | 25.0% |
| Files read | Unknown | Unknown | Unknown / Unknown | — | — |
| LOC | 350 | Not separable (see predictor-05) | Observed / Unknown (per-node) | — | — |
| Duration | Unknown | Unknown | Unknown / Unknown | — | — |
| Token usage | Unknown | Shared Wave 2 total (see predictor-05) | Unknown / Unknown (per-node) | — | — |
| Complexity | L | M (self-assessed as lighter than L) | Observed / Derived | — | — |

## 2. Aggregate statistics (files and LOC only — the only metrics with complete paired data)

| Metric | Value | Provenance |
|---|---|---|
| Nodes with a fully paired (estimate + independently observed actual) files-changed comparison | 17 of 19 (all except foundation-04's stale-estimate case and predictor-05/06's non-independently-split case) | Derived |
| Mean absolute files-changed error | 2.06 files | Derived, computed over the 17 comparable rows |
| Mean % files-changed error | 54.4% | Derived, same 17 rows |
| Median % files-changed error | 33.3% | Derived |
| Direction of files-changed error | 14 of 17 rows actual > estimated (under-estimation); 2 rows actual < estimated; 1 row exact match | Derived |
| Mean % LOC error (rows with a valid comparison) | 108.9% | Derived, computed over 13 rows with a clean per-node LOC pairing (excludes combined-commit and non-split rows) |
| Direction of LOC error | 13 of 13 comparable rows: actual > estimated (100% under-estimation direction) | Derived |
| Nodes where `estimated_duration` exists at all | 0 of 19 | Observed (absence) |
| Nodes where `estimated_token_usage` exists at all | 0 of 19 | Observed (absence) |
| Nodes where `actual_duration` is a real self-report (not "Unknown"/"not tracked") | 9 of 19 | Observed |
| Nodes where `actual_token_usage` is Observed at any granularity (incl. shared-invocation totals) | 17 of 19 (all except Bootstrap, executed inline with no isolated usage block) | Observed |
| Nodes where `actual_token_usage` is Observed at **per-node** granularity | 2 of 19 (`foundation-01`, `checkpoint-b03` — the only two nodes that were the sole node in their teammate's invocation) | Observed |

## 3. What this report does NOT claim

- It does not compute a token-usage error, because no token estimate ever
  existed to compare against (§0). Any number here would be fabricated.
- It does not compute a duration error for the same reason.
- The LOC "estimate" in the DAG is a single number with no documented
  definition (gross insertions? net lines? implementation only, or
  implementation + tests?). This report treats it as comparable to git's
  "insertions" count, which is the closest available analog, but this is
  an interpretive choice, not a defined equivalence — flagged explicitly
  rather than silently assumed to be exact.
- Complexity "error" is not quantified numerically (XS/S/M/L/XL is an
  ordinal label, not a cardinal scale) — §1 reports whether the label was
  held, and Calibration_Report.md (Phase 3.2) discusses direction.
