# ADR Recommendations

| Field | Value |
|---|---|
| Phase | 3.9 — Post Wave 2 Analysis |
| Status | **Proposals only. None of these are approved. None are implemented. No file listed as "affected" has been touched by this document.** |
| Source | Findings from `Feature_Registry.md`, `Feature_Gap_Report.md`, `Wave2_Lessons.md`, and this conversation's own independent verification work |

Each recommendation below follows the same shape: problem, evidence,
affected packages, compatibility impact, recommendation. Approval and
implementation are separate future steps, explicitly not taken here.

---

## ADR-REC-01: Formalize repository/session feature lookup as a frozen `app` port

**Problem:** `predictor-05`'s `RuleScopeEstimator` needs repository- and
session-derived features to do useful work, but `app.EstimateScopeRequest`
(the frozen contract) carries only IDs. The role worked around this with
a package-local `FeatureSource` interface (`internal/predictor/scope/estimator.go`)
rather than editing `internal/app/ports.go`, per instruction. This was the
correct call under Wave 2's rules, but it means the feature-lookup
capability now lives entirely inside `predictor`'s own package, invisible
to `internal/app`'s cross-component contract — any future predictor tier
(Statistical, ML) or any other role needing the same repository/session
data would either reinvent the interface or import `predictor`'s internal
package directly, which is exactly the kind of accidental coupling the
narrow-ports discipline exists to prevent.

**Evidence:** `Feature_Gap_Report.md` §1.1 (ranked #1, "closeable now,
high improvement") documents that `internal/gitx` already produces the
underlying data (dirty-file counts, worktree structure) with zero new
data-collection work needed — only a wiring gap remains, and that wiring
currently has nowhere canonical to live.

**Affected packages:** `internal/app/ports.go` (would gain new
port(s)/DTO(s)), `internal/predictor/scope/estimator.go` (would migrate
its local `FeatureSource` to satisfy the new frozen shape instead of its
own ad hoc one), `internal/gitx` (would gain a consumer, no changes to
`gitx` itself expected).

**Compatibility impact:** Additive only if done carefully — a new
interface in `ports.go` does not break `EvaluationService`,
`ProgressTreeService`, or any existing frozen type. Risk is narrow:
whatever shape is chosen now becomes a compatibility commitment per
Constitution §3's "public CLI/API/protocol compatibility" ADR trigger, so
it should be designed once, deliberately, not iterated in place after
other roles depend on it.

**Recommendation:** Worth a real ADR before Wave 3 assigns any node that
would consume this data (e.g., a future `predictor-05` follow-up, or
`predictor-05b`/`-05c`). Low implementation cost (S per
`Feature_Gap_Report.md`), meaningful leverage (closes the single
highest-ranked gap in that report).

---

## ADR-REC-02: Add duration and token-cost fields to the DAG task-table schema — or explicitly declare them permanently out of scope

**Problem:** `docs/implementation/vertical-slice/EXECUTION_DAG.md` has never had a
duration or token field, for any of its 84+ nodes, across two full waves.
This was independently flagged as a gap by 4 of 5 lessons-learned files
(`Wave2_Lessons.md` §1, issue #2) without those files having visibility
into each other. `Prediction_Error_Report.md` could not compute a single
duration or token error for any of the 19 nodes executed so far, for lack
of an estimate to compare against — not for lack of actual data (actual
data exists at partial granularity, per that report).

**Evidence:** `Prediction_Error_Report.md` §2 ("Nodes where
`estimated_duration` exists at all: 0 of 19"), `Calibration_Report.md`
§8 (names this as the #3 priority for improving future-wave confidence).

**Affected packages:** None (this is a documentation/process artifact,
`docs/implementation/vertical-slice/EXECUTION_DAG.md`, not Go code) — listed here
because the repository owner's Phase 2 instructions explicitly froze the
DAG file itself as requiring ADR approval to change, so even a
documentation-only change to its schema falls under this process.

**Compatibility impact:** None on running code. Some cost to whoever
maintains the DAG going forward (two more columns to fill in per node).

**Recommendation:** Two defensible paths, not a single obvious answer:
(a) add the fields and require future DAG authors to fill them in, even
approximately, so the estimator has *something* to be checked against; or
(b) explicitly document in the DAG's own header that duration/token are
out of scope for this artifact by design (e.g., because they are provider-
and model-dependent in a way LOC/files/complexity are not), which would
at least stop the same gap being independently rediscovered every wave.
Recommend (a) — a rough estimate that turns out wrong is more useful data
than a permanently blank field, per this project's own "Unknown is a
valid answer, but don't leave a gap uninvestigated" ethos.

---

## ADR-REC-03: Resolve the CLI hook-subcommand casing inconsistency

**Problem:** `Auspex_ADD.md` Appendix E.3 specifies Claude Code hook
subcommands in PascalCase (`auspex hook claude UserPromptSubmit`,
matching Claude's own hook-event-name casing). `agents/runtime.md`'s P0
command list, `docs/implementation/vertical-slice/EXECUTION_DAG.md`'s
`claude-provider-06` validation command, and
`Auspex_Parallel_Execution_Plan.md`'s demo script all
independently use kebab-case (`auspex hook claude user-prompt-submit`).
This was discovered by `claude-provider-06` and independently confirmed
by the lead reading the ADD text directly during Wave 2 review — it is
real, not a misread.

**Evidence:** `Wave2_Lessons.md` §1, issue #4; the underlying text
exists in `Auspex_ADD.md` lines ~6152-6157 (PascalCase) vs. three
other documents (kebab-case), all currently frozen.

**Affected packages:** `integrations/claude/hooks.json` (already built,
following kebab-case as a documented judgment call), and — not yet built
— `runtime-b01`'s real CLI command tree (`internal/cli`), which will need
to pick one convention when it actually implements `auspex hook claude
...` as a Cobra command.

**Compatibility impact:** Low if resolved now (nothing external depends
on either casing yet); would become a breaking CLI change if resolved
*after* `runtime-b01` ships with one casing and real users/scripts start
depending on it.

**Recommendation:** Resolve before `runtime-b01` is assigned, not after.
This is a small, cheap fix now and a compatibility-breaking fix later —
exactly the kind of decision worth making once, deliberately, while it's
still free.

---

## ADR-REC-04: Add an explicit `events` persistence table to the SQLite schema, or document that raw events are not durably persisted

**Problem:** `pkg/protocol/v1.Event` (the frozen normalized event
envelope, ADD §11) has no corresponding table in ADD §12.2's explicit
SQLite schema list. Every other major frozen type
(`turns`, `progress_nodes`, `state_checkpoints`, `pause_records`, etc.)
has a named table; `Event` does not.

**Evidence:** `Feature_Registry.md` §8b, flagged directly: "`events` table
implied by ADD §11 but not named in §12.2's explicit table list."

**Affected packages:** `Auspex_ADD.md` §12.2 (schema definition,
contract-integrator-owned), eventually `foundation-06`'s migration range
(0000-0009) or a feature-owning role's migration range, depending on
which role ends up owning event storage.

**Compatibility impact:** None yet (no migrations exist). Becomes a
schema-versioning question once `foundation-06` ships migrations without
an events table and a later wave wants to add one retroactively.

**Recommendation:** Resolve before `foundation-06` is assigned, for the
same "cheap now, expensive later" reason as ADR-REC-03. Two honest
options: add the table now, or explicitly decide (and document in the
ADD) that raw events are intentionally not durably persisted — only their
derived effects (turn records, usage observations, etc.) are — which
would itself be a legitimate, privacy-conscious design choice, but should
be a stated decision, not a silent gap.

---

## ADR-REC-05 (open question, not a firm recommendation): Should `RunwayForecast` support multiple concurrent windows the way `QuotaForecast` was designed to?

**Problem:** ADD §15.5 explicitly discusses multiple quota windows
("多 windows 取 `P_any = 1 - Π(1 - P_i)`... v1 預設取 max") and
`predictor-06`'s `CombineWindows` function (verified during Wave 2 review)
already implements a `max()` combination across windows. But
`domain.RunwayForecast` itself (ADR-041, frozen) is a single-window
struct — `CombineWindows` operates on a caller-supplied slice of them,
not on a frozen multi-window type. Meanwhile `QuotaForecast` (also
ADR-041) was deliberately kept to two scalar fields
(`ProjectedQuotaUsedP90`, `ProjectedContextUsedP90`), not an array, per
explicit instruction to avoid speculative multi-window complexity this
wave.

**Evidence:** Direct code reading during Wave 2 verification
(`internal/predictor/runway/runway.go`'s `CombineWindows`, `TestCombineWindowsTakesMax`).

**Affected packages:** `internal/domain/usage.go` (`RunwayForecast`),
`internal/domain/forecast.go` (`QuotaForecast`), if either shape changes.

**Compatibility impact:** Would be a breaking change to two already-frozen
types.

**Recommendation:** Not recommending a change — flagging as a design
question worth deliberately deciding (yes, keep single-window +
caller-side combination; or no, formalize multi-window as a first-class
type) the next time either type's shape is revisited for an unrelated
reason, rather than drifting further apart in convention without anyone
having chosen that outcome on purpose.

---

## Summary

| # | Recommendation | Urgency | Implementation cost if approved |
|---|---|---|---|
| REC-01 | Frozen feature-lookup port | Before next predictor node | S |
| REC-02 | DAG duration/token fields | Before Wave 3 planning uses estimates | XS (schema) / ongoing (fill-in cost) |
| REC-03 | CLI casing resolution | Before `runtime-b01` | XS |
| REC-04 | `events` table decision | Before `foundation-06` | XS (decision) / S (if adding the table) |
| REC-05 | Multi-window Runway (open question) | No urgency — revisit opportunistically | Unknown, not scoped |

None of these are approved. None are implemented. This document's sole
purpose is to make each decision visible and evidenced before it becomes
either forgotten or expensive to change.
