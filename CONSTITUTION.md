# Auspex Repository Constitution

> 🌐 English | [繁體中文](CONSTITUTION.zh-TW.md)

| Field | Value |
|---|---|
| Status | **Normative. Supreme process authority for this repository.** |
| Scope | How Auspex-the-project is built and governed — not what Auspex-the-product does at runtime (that is `docs/design/Auspex_ADD.md`'s domain). |
| Precedence | This document is more authoritative than `README.md` and `AGENTS.md`. If either ever contradicts this document, the Constitution wins and the other file is wrong and must be fixed. |
| Amendment | Only the `contract-integrator` role/architecture lead may amend this file, and only via the same ADR discipline defined in §3 below. |

This is not a README and not a contributor cheat-sheet. It is the set of
rules that make multi-agent, multi-session, multi-day work on this
repository converge instead of diverge. Read this before touching any file
in this repository, human or agent.

---

## 1. Single Source of Truth

Auspex has exactly one authoritative document per subject. Never treat
any other document, prior draft, PR description, comment, or conversation
as authoritative over the documents named below for their subject.

| Subject | Sole source of truth |
|---|---|
| Product architecture, domain model, requirements, roadmap | `docs/design/Auspex_ADD.md` |
| Architecture decisions that amend the ADD | Accepted ADRs under `docs/adr/` |
| Process, governance, invariants, who-can-modify-what | **This file** |
| Current execution phase's mechanics (topology, merge order, ownership map) | `docs/design/Auspex_Parallel_Execution_Plan.md` (or its successor for later phases) |
| A given role's mission, exclusive paths, deliverables, tests | Its file under `agents/` |
| Contributor/agent quick-reference | `AGENTS.md` |
| Project entry point / orientation | `README.md` |
| Repository markdown audit trail | `docs/repository_inventory.md` |

If two documents disagree, the higher one in this list wins, and the lower
one is a bug to be fixed — not a judgment call to make ad hoc each time it
comes up.

## 2. Document priority order (conflict resolution)

When code, an issue, a PR, a prompt, or an agent's own reasoning conflicts
with a governing document, resolve in this order:

```text
1. This Constitution                        (process/governance/invariants)
2. docs/design/Auspex_ADD.md + accepted ADRs          (architecture)
3. Current execution plan                    (this phase's mechanics)
4. agents/*.md                               (role-scoped operational detail)
5. AGENTS.md / README.md                     (summaries — must not contradict 1-4)
6. Everything else (comments, chat, memory)  (never authoritative)
```

No agent — human or AI — may alter a decision at a higher level because a
lower-level task is easier to implement a different way. If a lower-level
document turns out to need something the higher level forbids, that is a
signal to raise an ADR (§3), not to quietly diverge.

## 3. ADR rules

An Architecture Decision Record is **required** before changing:

- the production runtime language;
- the daemon transport;
- the SQLite schema in a backward-incompatible way;
- a provider integration contract;
- the checkpoint format or restore safety model;
- a State Checkpointing invariant;
- Graceful Pause / Auto-Resume semantics;
- a privacy default;
- public CLI/API/protocol compatibility;
- the OSS license;
- a prediction output changing from score to probability;
- this Constitution itself.

Rules:

1. ADRs live at `docs/adr/NNNN-title.md`, numbered sequentially.
2. Only the `contract-integrator` role (architecture lead) accepts an ADR. Any role may propose one.
3. An accepted ADR is immutable history. To change a decision, write a new ADR that supersedes the old one — never edit an accepted ADR's decision in place.
4. An ADR must state: context, the decision, and what it changes in `docs/design/Auspex_ADD.md` or this Constitution (if anything). If nothing in either document needs to change, the decision didn't need an ADR.
5. `docs/design/Auspex_ADD.md` may only be edited by `contract-integrator`, and only when a genuine contradiction requires it — with the corresponding ADR landing in the same change.

## 4. Who can modify what

1. Every role owns a disjoint set of paths, declared in its `agents/*.md` file and summarized in the current execution plan's shared-file policy section.
2. A role may only modify files inside its own declared paths.
3. Shared, cross-cutting files — `docs/design/Auspex_ADD.md`, this `CONSTITUTION.md`, `AGENTS.md`, `internal/domain/**`, `internal/app/ports.go`, `pkg/protocol/v1/**`, `docs/adr/**` — are owned exclusively by `contract-integrator`. No other role edits them, ever, including "just a typo fix."
4. If a role needs a change to a file it doesn't own, it requests the change through its progress artifact (`docs/implementation/vertical-slice/<role>.md` or the equivalent for a later phase) — it does not make the edit itself and does not wait idle; it works around the gap with a documented assumption until the owner responds.
5. No role may expand its own path ownership. Only `contract-integrator` may reassign path ownership, and only by updating the execution plan and the affected `agents/*.md` files in the same change.
6. `go.mod` and `go.sum` are owned only by `foundation`.
7. Migration file ranges are fixed per role (see the current execution plan §7); a role never writes a migration outside its assigned range.

## 5. When a new Provider may be added

Auspex integrates providers (Codex, Claude Code, and eventually others)
through the capability-based model in `docs/design/Auspex_ADD.md` §6.7 and §8. A new
provider integration may be added only when **all** of the following hold:

1. It implements the narrow provider interfaces in ADD §9.10 (`ProviderDetector`, `HookNormalizer`, `ManagedRunner`, etc.) — it does not require widening any existing interface into a God interface, and it does not require a new interface that only one provider implements without a documented reason.
2. Its `ProviderCapabilities` (ADD §8.6) are explicitly detected and declared at runtime — never assumed, never hardcoded as "this provider always supports X."
3. Missing capabilities degrade explicitly (ADD principle: "Capabilities are explicit," §1.6 rule 8) rather than silently pretending to work.
4. Fixture-backed contract tests exist for the new provider before its adapter merges — no adapter merges against a live, unrecorded account as its only test.
5. It does not require changing `internal/domain/**` — if it does, that is itself a signal an ADR is needed first (§3).
6. It does not fork or scrape a provider's native interactive TUI (ADD non-goal).
7. If adding it changes the shape of the provider integration contract itself (not just a new adapter behind the existing contract), an ADR is required (§3) before implementation begins, not after.

## 6. Progress Tree invariants

These are product invariants (`docs/design/Auspex_ADD.md` §1.3, §1.6, §6.4, FR-100–FR-110) and are non-negotiable in the implementation, not stylistic preferences:

1. **Progress Tree is the canonical durable task state.** Conversation context, chat memory, and an agent's own claim of "done" are never the source of truth.
2. **A node may not be marked `completed` without durable, validator-checked artifact evidence** — a real file, DB record, checksum, or Git snapshot. Text alone is insufficient (ADD principle 5: "Completed means evidenced").
3. **Every node completion creates a State Checkpoint in the same atomic operation.** A completed node with no corresponding checkpoint is a bug, not an acceptable gap.
4. **Node status values are the fixed enum** (`pending`, `ready`, `in_progress`, `checkpointing`, `paused`, `completed`, `failed`, `skipped`, `blocked`) — no role invents an ad hoc status.
5. **State writes are atomic, idempotent, and crash-recoverable.** A crash mid-write must never leave a node that looks completed but isn't backed by verified evidence.
6. **Duplicate completion with conflicting evidence is rejected**, not silently merged or overwritten.
7. This same discipline applies, by analogy, to the meta-level progress artifacts each role keeps under `docs/implementation/vertical-slice/<role>.md` while building Auspex itself: conversation-only progress does not count there either (current execution plan §9).

## 7. Rules every agent must follow

These generalize `AGENTS.md`'s "Required principles" into constitutional
status — if `AGENTS.md` and this section ever diverge, fix `AGENTS.md`:

1. Go is the only production runtime; TypeScript is isolated to the VS Code extension; Python is offline research only and the Go runtime never depends on it.
2. Raw prompts and tool output are not persisted by default.
3. Provider capability gaps are surfaced explicitly, never silently assumed away.
4. Undocumented transcripts are never parsed on a stable path.
5. Git and provider process execution use argv calls, never shell command strings.
6. Repository checkpoints are atomic and never silently commit the active branch.
7. Uncalibrated risk scores are never labeled as probabilities.
8. Graceful Pause's full guarantee applies only to managed execution; native-hook behavior is explicitly degraded, never silently claimed as equivalent.
9. Auto-resume is opt-in, workspace-scoped, permission-non-escalating, cancellable, audited, and re-verified before it runs (ADD §6.10).
10. An agent implements one milestone/phase at a time and does not add abstractions a later milestone would need but the current one doesn't.
11. An agent does not declare a task complete without the durable evidence required by §6 above.

## 8. Relationship to `docs/design/Auspex_ADD.md`

This Constitution does not compete with `docs/design/Auspex_ADD.md` for authority —
they govern different domains and neither subordinates the other:

- `docs/design/Auspex_ADD.md` is supreme for **what Auspex is and how it behaves at runtime** (architecture, domain model, requirements).
- This Constitution is supreme for **how the Auspex repository and its contributors/agents behave while building it** (process, ownership, invariant enforcement, governance).

Where a Progress Tree invariant (§6) is simultaneously a runtime behavior
*and* a development-process rule, this Constitution states it because it
constrains how agents must build the feature, while `docs/design/Auspex_ADD.md`
remains the canonical technical specification of the feature itself.
