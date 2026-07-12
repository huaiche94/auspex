# Calibration Report

| Field | Value |
|---|---|
| Phase | 3.2 — Post Wave 2 Analysis |
| Source | `Prediction_Error_Report.md` (this document adds no new raw data) |
| Status | Observations only. No predictor coefficients, rules, or implementation modified. |
| Sample size | n=19 executed nodes (Bootstrap + 5 Foundation + 5 Claude Adapter + 2 Checkpoint + 5 Predictor), 1 repository, 1 provider (Claude, self-hosted agent execution — not a Claude Code end-user session), 1 wall-clock day |

## 0. Sample-size caveat, stated up front

n=19, all from one repository, one contributor, one execution environment,
across two waves of one day. Every observation below is a real, Observed
pattern in this specific dataset — none are fabricated — but none should
be treated as a general law about Preflight's future scope estimator
without many more waves of data across different repositories, task
classes, and contributors. Where a pattern looks strong, this report says
so; where n is too small to trust a direction, this report says that too.

## 1. Systematic under-estimation (files changed)

**Observed.** 14 of 17 comparable nodes (82.4%) actually changed *more*
files than the DAG estimated; only 2 changed fewer; 1 matched exactly.
Mean % error was +54.4% (Prediction_Error_Report.md §2). This is a
one-sided pattern, not noise scattered around zero.

**Most likely cause, per direct evidence in the lessons-learned data**:
the DAG's `Est. Files` column does not consistently distinguish
implementation files from test files. Multiple lessons-learned entries
independently converge on the same diagnosis without having seen each
other's report:

- `foundation-01`: "the estimator should assume test files are additional
  files, not folded into the impl estimate" (125% files error, 152.5% LOC
  error).
- `checkpoint-b02`: "the DAG's 'Est. Files' column conflates implementation
  and test files into one number... this row's '7 vs 5' delta is almost
  entirely a counting-convention mismatch, not scope growth."
- `foundation-09`: the largest single files-changed error (83.3%) is not
  scope growth at all — it is 9 pre-existing files touched only because
  turning on `golangci-lint` for the first time retroactively surfaced
  issues in earlier nodes' code. This is a different failure mode from
  the other two (a genuine blind spot in the DAG's task-decomposition
  model — a "config" node's true footprint includes every file its tool
  newly inspects, not just the files it creates) and should not be
  averaged together with the test-file-counting pattern without noting
  the distinction.

**Confidence**: High that the test/impl file-counting gap is real and
systematic (independently observed by 3+ different sub-agents with no
communication between them). Lower confidence on the exact correction
factor — the raw "actual/estimated" ratios in this sample range from 1.0×
(predictor-02, exact match) to 2.25× (foundation-05), so no single
multiplier fits.

## 2. Systematic under-estimation (LOC)

**Observed.** 13 of 13 comparable nodes (100%) had actual LOC exceed
estimated LOC. Mean % error +108.9% — actual insertions were, on average,
just over double the DAG's estimate. This is the single strongest
directional signal in the entire dataset: not one node came in at or under
its LOC estimate.

**Contributing factors, per direct evidence:**

1. **Test LOC is large relative to implementation LOC in this codebase's
   style.** Every node in this dataset wrote at least one test file, and
   several (`checkpoint-b02`, `checkpoint-b03`, `predictor-04`,
   `predictor-06`) have test files longer than their implementation file.
   The DAG's LOC estimate does not document whether it was meant to
   include test code at all.
2. **Privacy/leak-assertion tests add real, non-trivial LOC that has no
   DAG-visible line item.** `claude-provider`'s and `predictor`'s privacy
   tests (reflection-walk + JSON-marshal + `%+v`-format checks, per
   Constitution §7) are a recurring, deliberate, correctly-required cost
   that adds LOC without adding "scope" in the DAG's files/complexity
   sense.
3. **Property-based / sweep tests are LOC-heavy per unit of behavior
   covered.** `predictor-04`'s 2000-trial property test and `predictor-06`'s
   ~300-combination `TestScoreNeverCalibratedNeverPanics` sweep are each a
   few dozen lines of setup that exercise vastly more cases than an
   equivalent table-driven test would need — cheap to write, but not LOC-cheap.

**Confidence**: High that LOC is systematically under-estimated across
the whole sample (100% one-directional, not a marginal majority). Medium
confidence on the relative weight of the three contributing factors above
— they were not isolated experimentally, only observed qualitatively in
the lessons-learned narratives.

## 3. Systematic over-estimation

**Observed.** No metric in this dataset shows a systematic
*over*-estimation pattern. The only over-estimation-flavored observations
are two nodes where the **complexity label** (not files or LOC) was
self-assessed as lower than the DAG predicted:

- `predictor-04`: DAG said M, self-assessed S/M — "the monotonicity
  guarantee falls out for free from using a single sorted-array
  interpolation function... no separate monotonicity-enforcement logic to
  write or debug."
- `predictor-06`: DAG said L, self-assessed M — the lessons-learned
  entry gives a specific, falsifiable explanation: ADD §15.4-15.7
  structures the runway forecaster as two tiers of very different size
  (an uncalibrated threshold fallback vs. a full empirical bootstrap with
  EWMA and 1000 Monte Carlo draws), and this wave correctly built only
  the smaller tier. The DAG's "L" label describes the tier that was *not*
  built this wave.

**Interpretation**: this is not evidence that Preflight's estimator is
biased toward over-estimating complexity in general — it is evidence that
at least one DAG node (`predictor-06`) bundled two very differently-sized
pieces of work under one complexity label, and the wave correctly executed
only the smaller piece. This is a scope-definition issue, structurally
similar to `foundation-04`'s stale-estimate problem in
Prediction_Error_Report.md, not a calibration bias in the ordinary sense.

## 4. Repository-specific bias

**Unknown.** This entire dataset comes from exactly one repository
(`preflight` itself). There is no second repository to compare against,
so no repository-specific bias can be identified or ruled out.
`Missing_Telemetry_Report.md` (Phase 3.6) treats "cross-repository
calibration data" as a named gap.

## 5. Provider-specific bias

**Unknown**, with one important caveat stated explicitly: the "provider"
in this dataset is Claude (Sonnet/Fable models via this harness) acting as
the *implementing agent*, not Claude Code acting as the coding agent
Preflight is designed to *observe*. This dataset measures "how much does
it cost an AI agent to implement a Preflight component," which is a
different question from "how much does it cost a user's Claude Code
session to complete a coding task" — the latter is what Preflight's
predictor is ultimately meant to forecast. Do not read this report's
findings as calibration data for Preflight's *product* predictor without
first confirming this distinction is understood; that would be a
category error, not just a small-sample-size caveat.

## 6. Task-class bias

**Weak signal, low confidence, n too small to generalize.** The one
qualitative pattern worth recording: nodes involving a **cross-platform
concern requiring OS-conditional logic** (`foundation-02`'s Windows path
separators, `foundation-04`'s POSIX/Windows process-liveness split) both
surfaced a real bug or design fork that the DAG's complexity label did not
distinguish from same-OS work of the same nominal size. This is n=2 and
should not be treated as established without more data — flagged as a
hypothesis for Wave 3+ to test, not a conclusion.

No other task-class-correlated pattern was observed with enough repeated
instances (this sample has essentially one node per distinct task type)
to distinguish a task-class effect from ordinary node-to-node variance.

## 7. Summary table

| Bias type | Direction | Confidence | Primary evidence |
|---|---|---|---|
| Files changed | Under-estimated | High (direction), Medium (magnitude) | 82.4% of nodes exceeded estimate; 3+ independent lessons-learned entries name the same root cause |
| LOC | Under-estimated | High | 100% of comparable nodes exceeded estimate |
| Duration | Cannot assess — no estimate ever existed | N/A | 0 of 19 nodes had a DAG duration field |
| Token usage | Cannot assess — no estimate ever existed | N/A | 0 of 19 nodes had a DAG token field |
| Complexity | Two isolated over-estimates, not a pattern | Low | n=2, both explained by scope-bundling, not general bias |
| Repository-specific | Unknown | N/A | n=1 repository |
| Provider-specific | Unknown, and possibly a category error to ask | N/A | Dataset measures implementer cost, not Preflight's target (coding-agent turn cost) |
| Task-class | Weak hypothesis only | Very low | n=2 for the one pattern observed |

## 8. What would most improve confidence before Wave 3

In order of expected information gain per unit effort, per this dataset
alone (not a general research plan — see `Predictor_Improvement_Suggestions.md`
and `Wave3_Recommendation.md` for those):

1. Re-issue `foundation-04`'s DAG estimate for its actual (reduced) scope,
   so it stops distorting any future aggregate that includes it uncorrected.
2. Decide and document what the DAG's LOC column actually means
   (implementation-only vs. implementation+test) — this single definitional
   fix would make roughly half of this report's "error" computations
   either disappear or become meaningful, rather than measuring a
   category mismatch.
3. Capture real wall-clock timestamps (tool-call level, not self-reported)
   for at least one future wave, since 10 of 19 nodes in this sample have
   no duration data at all and the 9 that do are self-reported estimates
   from a model with no clock access.
