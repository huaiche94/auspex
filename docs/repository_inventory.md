# Auspex Repository Markdown Inventory

| Field | Value |
|---|---|
| Scope | Every `*.md` file in the repository (13 files found, all opened) |
| Purpose | Phase 0 repository normalization — classification only |
| Status | **Migration executed 2026-07-12.** See §6 for the execution log. This table now describes pre-migration state (kept for audit trail); current state is summarized in §6. |
| Generated | 2026-07-12 (updated 2026-07-12 after execution) |

---

## 1. Classification legend

| Status | Meaning |
|---|---|
| **authoritative** | The governing source of truth for its subject; conflicts resolve in its favor |
| **supporting** | Adds detail, execution mechanics, or scaffolding subordinate to an authoritative doc |
| **obsolete** | Superseded, contradicts current direction, or describes a prior product/plan |
| **duplicate** | Byte-identical or near-identical content that already exists canonically elsewhere |
| **archive** | Disposition (not a content type): keep for historical record, out of the active path |

---

## 2. Inventory table

| Filename | Purpose | Owner | Current Status | Should Keep? | Replacement | References |
|---|---|---|---|---|---|---|
| `Auspex_ADD.md` | Full Architecture Design Document + implementation spec for Auspex (vision, requirements, C4, domain model, schema, roadmap M0–M15, ADRs). Self-declared "source of truth." | A00 / architecture lead (per vertical-slice plan §7, exclusive-path list) | **authoritative** | Yes — as-is | — | References `AGENTS.md` (missing), `README.md` (missing), `docs/adr/**` (missing), full `docs/**`, `LICENSE`, `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`, `CHANGELOG.md` (all missing) |
| `Auspex_Parallel_Execution_Plan.md` | Vertical-slice execution plan: 9-agent (A00–A08) topology, ownership map, migration ranges, merge order, agent packets appendix. | A00 / architecture lead | **supporting** (subordinate to the ADD; execution mechanics, not architecture) | Yes — as-is | — | References `Auspex_ADD.md`, `AGENTS.md` (missing), `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` (missing), `docs/implementation/vertical-slice/A00.md`…`A08.md` (missing) |
| `agent-packets/00-contract-integrator.md` | Standalone copy of the A00 packet | A00 | **duplicate** — byte-identical to the `# A00 …` section embedded in `Auspex_Parallel_Execution_Plan.md` (verified via diff) | Conditional — see §3 | `Auspex_Parallel_Execution_Plan.md` "Agent Packets" section | Implicitly the whole plan (not self-contained) |
| `agent-packets/01-foundation-config-sqlite.md` | Standalone copy of the A01 packet | A01 | **duplicate** — byte-identical to embedded `# A01 …` section | Conditional — see §3 | same | same |
| `agent-packets/02-claude-telemetry-hooks.md` | Standalone copy of the A02 packet | A02 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/03-progress-state-checkpoint.md` | Standalone copy of the A03 packet | A03 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/04-repository-checkpoint.md` | Standalone copy of the A04 packet | A04 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/05-predictor-policy.md` | Standalone copy of the A05 packet | A05 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/06-graceful-pause-scheduler.md` | Standalone copy of the A06 packet | A06 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/07-runtime-cli-api.md` | Standalone copy of the A07 packet | A07 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/08-qa-security-ci.md` | Standalone copy of the A08 packet | A08 | **duplicate** — byte-identical | Conditional — see §3 | same | same |
| `agent-packets/README.md` | Titled "README" but is a full copy of `Auspex_Parallel_Execution_Plan.md` lines 1–363 (sections 1–13, everything except the Agent Packets appendix) | none (orphaned copy) | **duplicate** — byte-identical to the plan's main body (verified via diff, exit 0) | **No** | `Auspex_Parallel_Execution_Plan.md` | none — not linked from either governing doc |
| `agent-packets/CONTRACT_FREEZE_TEMPLATE.md` | Placeholder/scaffold for A00's real deliverable, with literal `<sha>`, `<module>`, `<version>` tokens; header says `Status: DRAFT` | A00 (intended) | **supporting** — template for a not-yet-produced authoritative artifact | Yes, but relocate | Becomes real content at `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` once A00 executes | Mirrors structure required by vertical-slice plan §6 |
| `AgentGuard_Architecture.md` | Architecture sketch for an earlier/differently-named product ("AgentGuard"): different module layout (`internal/telemetry`, `.agentguard/` state dir), different provider set (adds Gemini/Cursor/OpenCode), simple Phase 1/2/3 roadmap. Not referenced anywhere by the ADD or vertical-slice plan. | none (no current owner) | **obsolete** | Archive (do not delete) | `Auspex_ADD.md` | none inbound; none outbound to current docs |
| `execution_prompt.md` | A raw kickoff-prompt draft: "create an agent team with **exactly four teammates**," 2 waves, tmux split-pane. Directly conflicts with the canonical **nine**-agent (A00–A08) topology in the vertical-slice plan and agent-packets, and conflicts with the current live directive not to create teammates. | none (appears to be a working draft, not a governed doc) | **obsolete** (superseded/contradicted by the approved vertical-slice plan) | Archive (do not delete) | `Auspex_Parallel_Execution_Plan.md` §3–§8 | References `Auspex_ADD.md`, `Auspex_Parallel_Execution_Plan.md`, `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` (missing), `AGENTS.md` (missing) |

---

## 3. Note on the `agent-packets/0X-*.md` files specifically

These are **not** pure clutter. The vertical-slice plan itself (§3) instructs: *"If every worker must use Fable, do not give the complete 161 KB ADD to every worker. Give each worker only: this common plan; `CONTRACT_FREEZE.md`; its assigned ADD chapters; **its agent packet**."* — implying a standalone, per-agent file is the intended hand-off artifact so an isolated agent/worktree doesn't need the whole plan doc.

The problem is not their existence, it's that **two hand-maintained copies of the same text now exist** (the embedded section in the plan doc, and the standalone file), with no mechanism keeping them in sync. That is a drift risk, not a one-time redundancy.

`agent-packets/README.md` is different: it duplicates the entire plan body under a misleading filename and serves no distinct purpose — it is a straightforward duplicate with no operational role.

---

## 4. Referenced but missing (not yet created — flagged for awareness, not classified above)

These are named by the ADD and/or vertical-slice plan as required but do not exist anywhere in the repository:

`AGENTS.md`, `README.md`, `LICENSE`, `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`, `CHANGELOG.md`, `docs/adr/**`, `docs/implementation/vertical-slice/CONTRACT_FREEZE.md`, `docs/implementation/vertical-slice/A00.md`…`A08.md`, `docs/providers/claude/**`, `docs/security/**`.

The repository also has no `.git` directory (not yet an initialized Git repository), which is a prerequisite the vertical-slice plan assumes (worktrees, branches, commits).

---

## 5. Proposed Migration Plan (NOT executed — awaiting approval)

Ordered by risk, lowest first. Every step preserves history (archive/move, not delete) unless explicitly marked otherwise.

1. **Create `docs/archive/` and move the two obsolete files there, unmodified.**
   - `AgentGuard_Architecture.md` → `docs/archive/AgentGuard_Architecture.md`
   - `execution_prompt.md` → `docs/archive/execution_prompt.md`
   - Rationale: both are superseded by `Auspex_ADD.md` / `Auspex_Parallel_Execution_Plan.md`; archiving (not deleting) preserves the design history and avoids losing the earlier reasoning.

2. **Remove `agent-packets/README.md`.**
   - It is a pure duplicate of `Auspex_Parallel_Execution_Plan.md` (sections 1–13) with no distinct purpose and is not linked from anywhere.
   - Replace with a short, real README (a few lines) that: (a) states these files are per-agent hand-off packets extracted from `Auspex_Parallel_Execution_Plan.md`, (b) links back to that file as canonical, (c) lists the 9 packet filenames and their agent IDs.

3. **Resolve the `agent-packets/0X-*.md` duplication with a single source of truth.**
   - Two options, both reasonable — this needs your decision, not mine:
     - **Option A (recommended):** Keep `agent-packets/0X-*.md` as the canonical, hand-off-ready files (they're already used that way per vertical-slice plan §3). Replace the embedded "Agent Packets" section in `Auspex_Parallel_Execution_Plan.md` with short summaries plus a link to each `agent-packets/0X-*.md` file, so there is one place to edit each packet.
     - **Option B:** Keep the plan doc as the single canonical file (current behavior) and regenerate `agent-packets/0X-*.md` from it mechanically (e.g., a small script/Makefile target) instead of hand-maintaining both.
   - Until this is decided, no packet content should be edited in only one location.

4. **Relocate the contract-freeze template.**
   - Move `agent-packets/CONTRACT_FREEZE_TEMPLATE.md` → `docs/implementation/vertical-slice/CONTRACT_FREEZE_TEMPLATE.md` (or leave in place and have A00 copy it to `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` when contract freeze actually runs). Either is fine; flagging so it isn't forgotten once `docs/implementation/vertical-slice/` is scaffolded.

5. **Scaffold the missing files the ADD/vertical-slice plan assume exist** (§4 above), at minimum: `AGENTS.md`, `README.md`, `docs/adr/`, `docs/implementation/vertical-slice/`. This is listed for completeness; it is new-file creation, not migration of existing content, and may belong to a later phase rather than this normalization pass.

### Explicitly not proposed

- No deletion of any file's content — only moves/archival and one true duplicate removal (`agent-packets/README.md`), and even that is reproducible from git history... except **this is not yet a Git repository**, so deletion of `agent-packets/README.md` would be unrecoverable by version control. Recommend initializing Git before executing step 2, or moving it to `docs/archive/` instead of deleting it, to stay safe.
- No edits to `Auspex_ADD.md` (A00-exclusive per its own rules).
- No edits to file content/wording anywhere — this plan is moves/removals only.

---

## 6. Execution log (2026-07-12) — normalization approved and applied

The plan in §5 was approved and executed, with §5-step-3 resolved as **Option A**
(the recommended one). No Go code was touched; no teammates were created.

| Action | Result |
|---|---|
| `AgentGuard_Architecture.md` moved to `docs/archive/` | Done — file unmodified except a short "ARCHIVED — obsolete" notice prepended above its original title |
| `execution_prompt.md` moved to `docs/archive/` | Done — file unmodified except a short "ARCHIVED — obsolete" notice prepended above its original text |
| `agent-packets/README.md` rewritten | Done — no longer a duplicate of the plan; now a real, scoped README for the `agent-packets/` directory that names `agent-packets/0X-*.md` as canonical |
| `Auspex_Parallel_Execution_Plan.md` "Agent Packets" section (lines 366–1093) | Replaced with a short index table linking to `agent-packets/0X-*.md`. Those standalone files are now the **single source of truth** for packet content; the plan doc only summarizes and links. Sections 1–13 (lines 1–365) were left byte-for-byte unchanged. |
| `agent-packets/CONTRACT_FREEZE_TEMPLATE.md` | Left in place (not relocated) — still the correct staging location until A00 actually produces `docs/implementation/vertical-slice/CONTRACT_FREEZE.md`; `agent-packets/README.md` now documents its purpose |
| Root `README.md` | Created (did not exist before). Points to `Auspex_ADD.md` as sole architecture authority, links every other governing doc, states real project status (pre-M0). |
| Root `AGENTS.md` | Created (did not exist before), verbatim from `Auspex_ADD.md` Appendix G ("Initial AGENTS.md") — the ADD's own prescribed content, not invented text. |
| `docs/implementation/vertical-slice/`, `docs/adr/`, `LICENSE`, `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`, `CHANGELOG.md` | **Not created.** Out of scope for this normalization pass (no existing content to migrate); still flagged as gaps in §4 above. Belongs to milestone M0, not repository normalization. |
| Git repository | **Still not initialized.** Recommend `git init` + first commit before further changes, so future moves/deletes are recoverable through version control rather than relying on manual archival. |

### Resulting state: exactly one source of truth per subject

| Subject | Single source of truth |
|---|---|
| Architecture | `Auspex_ADD.md` |
| vertical-slice execution mechanics | `Auspex_Parallel_Execution_Plan.md` |
| Per-agent packet content | `agent-packets/0X-*.md` |
| Contributor/agent instructions | `AGENTS.md` |
| Project overview/entry point | `README.md` |
| Repository markdown audit | this file |
| Superseded/obsolete material | `docs/archive/` (kept, not deleted, not authoritative) |

---

## 7. Execution log (2026-07-12, later same day) — role restructuring + Constitution

The repository owner requested renaming the numbered `agent-packets/0X-*.md`
files to semantically-named files (an LLM handed `predictor.md` needs no
extra context to know what it owns; handed `05-predictor-policy.md` it
does), consolidated from 9 bounded contexts to 7, plus a new supreme
governance document. Executed:

| Action | Result |
|---|---|
| `agent-packets/` (9 numbered packets + README + template) | Moved to `docs/archive/agent-packets-v1/`, archived with a superseded-notice banner, **not deleted** — git history also preserves it independently now that the repo is committed |
| `agents/` (new) | 7 semantically-named role files + `README.md` + `CONTRACT_FREEZE_TEMPLATE.md`. `checkpoint.md` consolidates the old `03-progress-state-checkpoint.md` + `04-repository-checkpoint.md` (kept as internal Part A / Part B). `runtime.md` consolidates the old `06-graceful-pause-scheduler.md` + `07-runtime-cli-api.md` (same Part A / Part B treatment). `contract-integrator.md`, `foundation.md`, `claude-provider.md`, `predictor.md`, `qa.md` are straight renames of `00`, `01`, `02`, `05`, `08` respectively, with in-text "A0X" role references reworded to the new names. |
| `Auspex_Parallel_Execution_Plan.md` | Rewritten throughout — §1 role count (8→6 bounded-context roles + 1 integrator), §3 model allocation, §4 topology diagram, §5 ownership map, §7 shared-file/migration-allocation policy, §8 worktree setup, §9 coordination artifact paths, §10 merge order, and the trailing role index — all now reference the 7 role names instead of `A00`–`A08`. Sections 1–2, 6, 11–13 (scope boundary, contract-freeze gate, demo script, cut order, review prompt) were content-preserving; no product decision changed, only the labeling of who does what. |
| `CONSTITUTION.md` (new, repo root) | Supreme process authority: single-source-of-truth hierarchy, document precedence order, ADR rules, path-ownership rules, provider-addition criteria, Progress Tree invariants, and agent development rules — all sourced from existing `Auspex_ADD.md` normative content (§0, §6, §8, §9, FR-100–FR-110), not invented. Positioned above `README.md`/`AGENTS.md` in authority, alongside (not above) `Auspex_ADD.md` since they govern different domains (process vs. architecture — see `CONSTITUTION.md` §8). |
| `README.md`, `AGENTS.md` | Updated to point to `CONSTITUTION.md` first, and to reference `agents/` instead of `agent-packets/`. |
| `docs/implementation/vertical-slice/EXECUTION_DAG.md` | **Now stale** — its 83 task IDs (`A00-01`…`A08-09`) were generated against the old 9-role structure. Not regenerated in this pass; flagged as the next explicit step (a "Dependency Review" pass under the new 7-role boundaries, before any team is spawned), per the repository owner's own preferred process ordering: Inventory → Normalize → DAG → **Dependency Review** → Spawn Team → Wave 1 → Review → Wave 2. |
| Prior (unapproved, unspawned) 4-teammate mapping proposal | Superseded by the same staleness — Stage 2 previously fanned out to 4 parallel branches (`A02`/`A03`/`A04`/`A05`); under the new structure it fans out to 3 (`claude-provider`/`checkpoint`/`predictor`). Will be redone after DAG regeneration, not before. Nothing was ever spawned, so no rework of running agents is needed. |

### Resulting state: exactly one source of truth per subject (updated)

| Subject | Single source of truth |
|---|---|
| Process, governance, invariants | `CONSTITUTION.md` |
| Architecture | `Auspex_ADD.md` |
| vertical-slice execution mechanics | `Auspex_Parallel_Execution_Plan.md` |
| Per-role definition | `agents/*.md` |
| Contributor/agent instructions | `AGENTS.md` |
| Project overview/entry point | `README.md` |
| Repository markdown audit | this file |
| Superseded/obsolete material | `docs/archive/` (kept, not deleted, not authoritative) |
| Task-level execution DAG | `docs/implementation/vertical-slice/EXECUTION_DAG.md` — **stale, pending regeneration under the 7-role structure** |

No two markdown files in the repository now contain overlapping authoritative content.
