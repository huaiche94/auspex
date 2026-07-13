# Auspex Development Methodology (PDM)

| Field | Value |
|---|---|
| Version | 1.0.0 |
| Status | Extracted from the Auspex project's own Phase 0-3 execution. Not yet applied to a second project — see §9. |
| Purpose | A versioned, referenceable development process for AI-agent-driven software projects, so future projects invoke it as `Follow Auspex Development Methodology v1.0` instead of restating this process as a several-hundred-line prompt each time. |
| Origin | This document is a distillation, not a new design — every rule below was exercised at least once on the Auspex project itself before being written down here. Where a rule exists because a specific failure occurred, that failure is cited as evidence, not asserted abstractly. |

## 0. What this document is for, and what it is not

This is a **process** methodology: how work moves from an idea to
integrated, validated code across multiple AI agents collaborating on one
codebase, over multiple sessions, without losing state, duplicating
effort, or silently drifting from an agreed architecture. It is not:

- An architecture template (that is project-specific — Auspex's own
  `Auspex_ADD.md` is an *example* of the artifact Phase 1 produces, not
  part of this methodology).
- A guarantee of correctness — it structures *how* verification happens
  (independent re-checking, evidence over self-report), not what "correct"
  means for any given project.
- A replacement for human judgment at approval gates. Every phase below
  ends in an explicit human decision point; this methodology defines what
  evidence should be in front of the human at that point, not what they
  should decide.

## 1. The phase sequence

```text
Phase 0 — Repository Discovery & Normalization
        ↓
Phase 1 — Architecture Freeze
        ↓
Phase 2 — Bootstrap (lead-only, pre-team)
        ↓
Phase 3 — Team Formation & Wave Execution
        ↓
Phase 4 — Review Gate
        ↓
Phase 5 — Integration
        ↓
Phase 6 — Post-Wave Analysis
        ↓
[loop back to Phase 3 for the next wave, or Phase 7 if analysis
 surfaces an architecture gap]
```

Phase 7 (Architecture Amendment) is not a fixed step in the sequence — it
is a side door any phase can open when evidence demands it, gated by the
same ADR discipline every other frozen-document change requires. §8 covers
it separately because it is triggered by evidence, not by schedule.

## 2. Phase 0 — Repository Discovery & Normalization

**Goal:** exactly one authoritative document per subject, before any
architecture or code work begins.

**Steps:**

1. Enumerate every document in the repository (not just the ones you
   expect to matter — this methodology exists partly because a stray
   `AgentGuard_Architecture.md`-style precursor document or a
   contradictory kickoff prompt can sit undetected in a repo indefinitely).
2. Classify each into exactly one of: **authoritative**, **supporting**,
   **obsolete**, **duplicate**, **archive** (a disposition, not a content
   type — archived material is kept, not deleted, per the rule below).
3. Produce a written inventory (`docs/repository_inventory.md` in
   Auspex's case) before touching any file.
4. Get explicit approval on the inventory and a migration plan before
   executing any move/archive/delete.
5. Execute the migration: archive obsolete material with a short
   "superseded by X" banner rather than deleting it (git history is not
   always a safe substitute — a fresh repository may not even have `.git`
   initialized yet), rewrite duplicated content down to one canonical
   copy, update every cross-reference.
6. Re-audit after any subsequent restructuring (Auspex did this twice —
   once for the initial inventory, once again after consolidating 9 roles
   to 7 — the audit trail itself should accumulate, not be discarded and
   rewritten each time).

**Why this is Phase 0, not a "nice to have":** every later phase assumes
"the architecture doc" and "the process doc" are unambiguous singular
references. Skipping this step means every future agent has to
re-adjudicate document authority from scratch, which is exactly the
"several-hundred-line prompt every time" problem this methodology exists
to prevent.

## 3. Phase 1 — Architecture Freeze

**Goal:** a frozen architecture document, a frozen process constitution
(separate documents — see §3.1), and a machine-checkable execution DAG,
all cross-referenced and consistent.

### 3.1 Two documents, not one, and they are not interchangeable

- **The architecture document** (Auspex's `Auspex_ADD.md`) is
  supreme for *what the system is and how it behaves*.
- **The Repository Constitution** (Auspex's `CONSTITUTION.md`) is
  supreme for *how the project is built* — document precedence, ADR
  rules, path ownership, invariant enforcement, agent development rules.

Conflating these into one document was tried implicitly in Auspex's
early state (before the Constitution existed) and produced exactly the
failure this split fixes: process rules (who can edit what, when an ADR is
required) kept needing to be inferred from architecture prose instead of
being looked up directly. Write both, from the start, as separate
documents with an explicit statement of how they relate (Auspex's
Constitution §8 is the worked example).

### 3.2 The execution DAG

Every unit of implementation work becomes a DAG node with, at minimum:
ID, owner, dependencies, estimated complexity, estimated LOC, estimated
files, **estimated duration, estimated token cost**, validation command,
merge order, risk, blockers. Represent the graph both as a Mermaid
diagram and as a topologically sorted list — the diagram is for human
review, the sorted list is what an agent actually executes against.

Field semantics (defined here because ambiguity was itself a measured
error source — see Auspex's `Prediction_Error_Report.md` §3):

- **estimated LOC** = expected `git` insertions, implementation + tests
  together, excluding progress-artifact/lessons docs.
- **estimated files** = files created or modified by the node's own
  commits, split as `impl+test` (e.g. `3+2`) — conflating the two was the
  single most-repeated estimation error across Auspex's waves
  (`Wave2_Lessons.md` §1 issue #1).
- **estimated duration** = wall-clock minutes for a single agent
  invocation, estimate-only precision (band, not point).
- **estimated token cost** = output-token band for the executing agent.
  An explicit `n/a — declared out of scope` is allowed for either of the
  last two, but the cell must exist and say so.

**History:** Auspex's own DAG never had duration or token-cost fields
across 84+ nodes and twelve waves; 4 of 5 implementing roles
independently rediscovered the gap without cross-communication
(`Wave2_Lessons.md` §1, `ADR_Recommendations.md` REC-02). The schema
above resolves REC-02 for all planning after 2026-07-13 (issue #15); the
vertical-slice DAG is not retro-annotated — its estimates are historical
record.

**Measurement discipline (the estimate is only half of REC-02):** actuals
must be capturable per node, or the estimate can never be scored. Two
protocols, either satisfies:

1. **One node per agent invocation** where parallelism allows — the
   harness usage block then reports per-node tokens directly, and the
   lead's own tool-call timestamps bound duration (no agent self-report
   needed).
2. **Lead-side timing** otherwise — the lead records invocation start/end
   from its own clock and attributes the invocation's token total to the
   node *set*, explicitly marked as unsplit (never divided evenly and
   presented as per-node data — `Prediction_Error_Report.md` §0's
   fabrication rule).

### 3.3 Approval gate

Human review and explicit approval of the architecture doc, Constitution,
and DAG, together, before Phase 2 begins. None of the three should be
approved in isolation — a DAG approved against an architecture that later
changes is stale on arrival.

## 4. Phase 2 — Bootstrap

**This phase exists because of a specific, real deadlock, not as
speculative process design.** In Auspex's execution, Wave 1 could not
start: every root-node teammate's first task depended on frozen
domain/contract types existing, but creating those types was itself a DAG
role's job, and that role was never one of the named Wave 1 teammates, and
the lead was (at that point) instructed not to implement any production
code. Zero nodes were assignable under the rules as they stood.

**The fix, now generalized as a rule:** treat contract-freezing
(domain types, cross-component interfaces, event/protocol envelopes,
storage transaction conventions) as a **named, lead-only, pre-Wave stage**,
executed directly by the lead, not delegated to a teammate and not folded
into "Wave 1." This is not a workaround — it reflects that these
artifacts have no meaningful owner *other than* the lead until they exist,
since every other role's work is defined in terms of them.

**Steps:**

1. Lead freezes: shared identifiers/enums, cross-component interfaces
   (kept narrow — no God interfaces), the wire/event envelope, typed
   errors, injectable Clock/IDGenerator/ProcessRunner-style seams,
   storage transaction boundary conventions.
2. Lead writes a `CONTRACT_FREEZE.md`-equivalent: exact import paths,
   field names, JSON/wire names, status transition tables, ID/idempotency
   rules, unknown/null semantics, privacy defaults, transaction
   boundaries, migration ownership ranges.
3. Lead validates (build, vet, test) and commits.
4. Only after this commit does Phase 3 begin.

**Necessary environment prerequisites belong here too**, evidenced by
Auspex's own Bootstrap: a toolchain version mismatch (installed
runtime vs. the version the architecture pinned) and a build-file
existence gap (no module manifest existed, and creating one was itself
role-owned but blocked by the same deadlock this phase resolves) both had
to be resolved by the lead, with explicit human approval, before any
teammate could be spawned. Treat "can every assignable node's tooling
actually run" as part of Bootstrap's exit criteria, not an assumption.

## 5. Phase 3 — Team Formation & Wave Execution

**Goal:** parallel, path-isolated implementation with per-node evidence,
not batch work with a single end-of-wave report.

### 5.1 Team formation rules

- Spawn exactly the number of teammates approved — do not create more
  without explicit approval, do not substitute a teammate's assignment
  because another task looks easier.
- Every teammate gets a disjoint, exclusive path set. Verify this is
  actually disjoint (a simple cross-branch file-diff check) before
  spawning, not after.
- Only assign nodes whose dependencies are **actually complete** — merged
  and validated, not merely "assigned to someone else." A dependency on
  a not-yet-integrated sibling node is not satisfied.
- If fewer nodes are genuinely unlocked than teammates available, leave
  teammates idle. **Idle is valid.** Do not invent work to keep capacity
  busy — this was an explicit, repeated instruction in Auspex's own
  execution and is generalized here because the alternative (assigning
  something because someone is free) is exactly how scope creep and
  DAG-vs-reality drift begins.

### 5.2 Per-node execution discipline

Within one teammate's multi-node assignment, execute **strictly
sequentially**, not batched:

```text
for each assigned node, in dependency order:
    implement only this node
    run this node's validation command(s)
    update the progress artifact (durable, structured — not a chat claim)
    update the lessons-learned artifact (estimate vs. actual, honestly)
    commit (one commit per node, or per logical unit within a node)
    confirm the next node's dependencies are genuinely satisfied
    → only then proceed to the next node
```

**Why this matters, evidenced:** every session-interruption incident in
Auspex's execution (3 occurrences across two waves) was recoverable
with zero rework specifically because implementation artifacts were
already durable on disk, and node-scoped commits meant "what was actually
done" was independently verifiable rather than dependent on trusting an
interrupted agent's memory of its own progress.

### 5.3 What a teammate must never do

- Edit another teammate's or the lead's owned paths.
- Edit a frozen contract file to work around a gap — work around it
  locally within owned paths, and report the gap. (Auspex's `predictor`
  role hit this twice — an undersized request DTO and a missing response
  shape — and both times built a local, package-scoped workaround rather
  than editing `internal/app/ports.go`, correctly.)
- Continue into a later node "because there's idle capacity" once the
  assigned scope is done. Stopping at the assignment boundary is correct
  behavior, not underperformance.
- Merge its own branch into `main`, or into another teammate's branch.

## 6. Phase 4 — Review Gate

**Core rule: never accept a "complete" claim without independent
verification.** This is not paranoia for its own sake — across 19
completed nodes in Auspex's execution, independent lead re-verification
caught zero false-completion claims, but did catch (a) a formatting
discrepancy in a self-reported commit hash that turned out to be benign on
inspection, and (b) would have caught any case where a session
interruption silently produced an incomplete artifact, which is exactly
what happened three times and was correctly caught before being reported
upward as done.

For every node, before accepting it as Validated:

1. Re-run every validation command yourself — do not trust "tests pass"
   as reported; run the tests.
2. Diff the touched-files list against the role's declared exclusive
   paths — confirm zero out-of-scope edits.
3. Check for duplicate/competing type or interface definitions against
   the frozen contract — grep, don't assume.
4. Read at least one non-trivial claim's actual implementation, not just
   its test output. A test suite passing is necessary, not sufficient,
   evidence that the underlying property (e.g. "this digest is
   order-independent," "this call never uses a shell string") is real.
5. Confirm required artifacts (progress record, lessons-learned entry)
   exist with real content, not placeholders.

**If a node fails review:** return it to the *original* teammate with the
exact failed criterion and exact files in question. The lead does not fix
teammate-owned code. Do not re-run the whole wave — only the failed
node's owner re-executes, then re-enters the Review Gate.

## 7. Phase 5 — Integration

1. Confirm every branch's dependencies are present on the target and the
   working tree is clean before integrating.
2. Integrate in **frozen-DAG dependency order**, not alphabetical or
   convenience order. Where multiple branches have no dependency relation
   to each other, integrate in the DAG document's own listed order for
   consistency, not an arbitrary one.
3. Use a dedicated integration branch — do not merge candidate branches
   directly into `main` until whole-repository validation passes on the
   integration branch first.
4. After each individual merge: re-run the full validation suite before
   proceeding to the next merge, so a conflict or regression is
   attributable to the branch that introduced it.
5. After all merges: whole-repo format/build/vet/test (including race
   detection where supported), every individual node's own validation
   command re-run, privacy/leak scanning if the project has a privacy
   invariant, and explicit checks for: no ownership artifacts lost, no
   duplicate types introduced, no out-of-wave scope present.
6. Merge the integration branch into `main` as **one integration commit**,
   with a message that records the merge order and every branch/commit
   SHA integrated — this commit is the durable record of the wave, not
   the individual node commits (which remain reachable in branch history,
   not deleted).
7. Produce an Integration Report: order, SHAs, conflicts (if any) and
   their resolution, validation results, remaining risks, newly unlocked
   DAG nodes. Stop and wait for approval before planning the next wave.

## 8. Phase 6 — Post-Wave Analysis

**Core rule: `Unknown` is a valid, required answer. Never fabricate a
metric that was not actually observed.** Every reported value carries one
of four provenance labels — **Observed** (measured directly), **Estimated**
(a self-report, not independently measured), **Derived** (computed from
other labeled values, with its own confidence), or **Unknown** (no data
exists, stated plainly rather than omitted or invented).

This phase produces (adapt names/scope per project, but keep the shape):

1. **Prediction Error Report** — estimate vs. actual for every completed
   node, with absolute and percentage error where both sides of the
   comparison genuinely exist, and an explicit `Unknown` where they don't
   (e.g. Auspex's DAG never had duration/token estimates, so those
   error computations are `Unknown`/`N/A` for every node, not `0`).
2. **Calibration Report** — systematic bias analysis over the error
   report, with sample-size caveats stated up front, not buried. A
   pattern observed at n=2 is reported as a low-confidence hypothesis, not
   inflated into a "finding."
3. **Lessons Aggregation** — every role's lessons-learned file, merged,
   with recurring issues ranked by how many *independent* teammates
   surfaced them without cross-communication (independent convergence is
   the strongest signal this phase can produce).
4. **Improvement Suggestions** — recommendations only, explicitly not
   implemented in this phase. Every suggestion states whether it is
   evidence-based (cite the evidence) or speculative (say so).
5. **Historical Replay** (if applicable) — compare current vs. proposed
   predictor/heuristic behavior against real historical data. **If no
   real historical data exists yet, say so explicitly and do not
   substitute a fabricated comparison** — this was Auspex's own
   Phase 3.5 outcome: every requested accuracy metric was `Unknown`,
   correctly, because no live telemetry had ever been collected.
6. **Missing Telemetry Report** — for every metric a predictor/heuristic
   would need but cannot currently measure: why, any provider/environment
   limitation, a possible deterministic workaround, expected impact if
   left unfilled, and a suggested future implementation path.
7. **Feature Registry** — the canonical, versioned data dictionary for
   every feature/signal the system's decision-making draws on. This
   becomes the single source of truth going forward — no future feature
   is added to the decision-making logic without a corresponding registry
   entry. Every entry states data type, source, provenance, confidence,
   current availability (Available / Derived / Estimated / Unknown), and
   which prediction/decision tiers it's suitable for.
8. **Feature Gap Report** — a companion to the registry: for every
   missing or partially-available feature, why it's missing, impact,
   suggested closing approach, complexity, and expected improvement —
   ranked so the next wave can prioritize.
9. **Confidence Report** — a derived view over the registry (does not
   duplicate it), classifying features into high/medium/low/zero
   confidence and recommending which are suitable as future training
   labels, which remain auxiliary inputs, and which should explicitly
   never be used as training labels (e.g. a low-confidence heuristic
   proxy should not become a training target once real ground truth
   exists — training against a proxy for the label, once the real label
   is available, actively degrades a statistical/ML tier rather than
   helping it).
10. **ADR Recommendations** — proposed, not approved, not implemented.
    Each states problem, evidence, affected packages, compatibility
    impact, and recommendation.
11. **Next-Wave Recommendation** — newly unlocked nodes (checking real
    dependency satisfaction, not assumption — Auspex's own analysis
    found two nodes that had been unlocked since a *prior* wave and were
    simply never assigned, because the team roster never covered their
    owning role), per-node estimates with full provenance/confidence/
    uncertainty, and a ranking by (a) dependency-unlock value, (b)
    execution risk, (c) estimated duration, (d) merge complexity. **Do
    not assign teammates or execute anything in this phase** — it is
    planning input for the next explicit approval.

**Stop after producing these. Wait for approval before Phase 3 begins
again.**

## 9. Phase 7 — Architecture Amendment (triggered by evidence, not scheduled)

If Post-Wave Analysis (or any other phase) surfaces a genuine architecture
gap — evidenced, not speculative — do not silently patch around it and do
not silently redesign it either:

1. **Stop.** Report the inconsistency precisely: what's missing, why it
   matters, what it blocks.
2. Write a proposed ADR: context, decision, exactly what it changes in the
   frozen architecture/contract/DAG.
3. Get explicit approval — approve the ADR itself, not a stub or
   placeholder standing in for it. (Auspex's ADR-041, inserting a
   missing Token/Quota Forecast layer into the predictor pipeline, is the
   worked example: the gap was found via a companion design document,
   confirmed against the frozen architecture text directly rather than
   taken on faith, and only then implemented as a real, final contract —
   not a temporary stub later replaced.)
4. Cascade the change through every document that referenced the old
   shape (architecture doc, contract-freeze record, DAG — including
   correcting any DAG dependency edges that were themselves symptoms of
   the same gap, not just adding new nodes) before resuming normal wave
   execution.
5. Regenerate any wave plan that was built against the now-stale DAG,
   explicitly noting what changed and why the regenerated plan differs
   (or doesn't) from the one it replaces.

## 10. Invariants that hold across every phase

These are not phase-specific — violating any of them is a defect
regardless of which phase you're in:

1. **Single source of truth per subject**, always. Two documents
   disagreeing is a bug to fix, not a judgment call to make ad hoc each
   time it comes up.
2. **The lead orchestrates; the lead does not implement teammate-owned
   code.** Bootstrap (Phase 2) is the sole, explicit, narrow exception,
   because contract-freezing has no other owner at that point in the
   sequence.
3. **Evidence over self-report, always.** A "complete" claim requires
   durable evidence (files, passing validation, structured records) — a
   conversational claim of completion is not evidence.
4. **Unknown is preferred over fabrication**, in every report, every
   estimate, every metric. State what you don't know and why, rather than
   filling the gap with something plausible-looking.
5. **Path ownership is exclusive and enforced by verification, not just
   instruction.** Check the diff; don't assume the boundary held.
6. **Stopping at an assignment boundary is correct behavior.** Idle
   capacity is not a problem to solve by inventing scope.
7. **Frozen documents change only through the ADR process**, even when
   the change looks small, even when it's "obviously" correct. The
   process is what prevents "obviously correct" changes from silently
   accumulating into an undocumented architecture that no longer matches
   its own written description.

## 11. Versioning and how to invoke this methodology

This is **v1.0.0**. Future revisions should be versioned deliberately
(semantic-versioning-style: a breaking change to the phase sequence or
invariants is a major bump; an additive clarification is a minor bump) and
changes to this document itself should go through the same ADR-style
review any other frozen process document requires.

**To invoke this methodology for a new project**, a prompt needs exactly
one line:

```text
Follow Auspex Development Methodology v1.0.
```

This is understood to mean: execute the phase sequence in §1, respect
every invariant in §10, and produce the artifacts named in each phase
section — adapted to the new project's actual architecture and domain,
but following this process shape rather than re-deriving it from scratch
or re-explaining it in the prompt.

## 12. Explicit non-claims

This document does not claim: that this process is optimal, that it has
been validated on more than one project, that every rule generalizes
perfectly outside a Go/multi-agent/single-repository context, or that
following it guarantees a correct architecture. It claims only that every
rule in it was exercised at least once, for a real reason, on the
Auspex project, and is written down here so the next project doesn't
have to rediscover the same lessons through the same failures.
