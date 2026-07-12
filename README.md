# Preflight

**Preflight is a local-first predictive runtime guard for AI coding agents.**
Before and during each turn with a provider like Codex or Claude Code, it
estimates scope, token/quota consumption, completion likelihood, and
blast-radius risk, then applies policy to run, warn, checkpoint, split,
gracefully pause, or block that turn.

It answers a different question than checkpoint/resume/memory tools do:
not "how do we continue?" but **"should we even start this turn?"**

> **Project status: Day-1 vertical slice in progress (Wave 2 of 9).**
> Bootstrap (Stage-0 contract freeze) and Wave 1 are integrated on `main`;
> Wave 2 is implemented on the per-role branches and undergoing
> integration and post-wave analysis. See the
> [Day-1 wave roadmap](#day-1-wave-roadmap) below and
> `docs/implementation/day1/EXECUTION_DAG.md` for task-level status.
> Milestone gating per `Preflight_ADD.md` §31 still applies.

## Source of truth

| Document | Role |
|---|---|
| [`CONSTITUTION.md`](CONSTITUTION.md) | **Supreme process authority.** Single-source-of-truth hierarchy, document precedence, ADR rules, path ownership, provider-addition criteria, Progress Tree invariants, and the rules every agent must follow. Read this first. |
| [`Preflight_ADD.md`](Preflight_ADD.md) | **The single authoritative architecture and implementation specification.** When code, issues, PRs, or comments conflict with it, this document (and accepted ADRs under `docs/adr/`) wins for architecture. |
| [`AGENTS.md`](AGENTS.md) | Contributor/agent quick-reference — required reading before any implementation work. |
| [`Preflight_Day1_Parallel_Execution_Plan.md`](Preflight_Day1_Parallel_Execution_Plan.md) | Subordinate execution plan for the first vertical-slice build: seven-role topology, ownership boundaries, merge order. |
| [`agents/`](agents/) | One canonical role definition per bounded context, linked from the plan above. |
| [`docs/adr/`](docs/adr/) | Accepted Architecture Decision Records — full-detail companions to the short entries in `Preflight_ADD.md` §33. |
| [`Preflight_Predictor_Design_Supplement.md`](Preflight_Predictor_Design_Supplement.md) | Predictor pipeline design detail (Scope/Token/Quota Forecast, Risk Estimation) — companion to `Preflight_ADD.md` §14-17, formalized by ADR-041. |
| [`docs/implementation/day1/`](docs/implementation/day1/) | Live Day-1 execution status: `EXECUTION_DAG.md` (task-level DAG, amended by ADR-041), `CONTRACT_FREEZE.md`, per-role progress artifacts, lessons learned, and post-wave analyses. |
| [`docs/repository_inventory.md`](docs/repository_inventory.md) | Audit of every markdown file in the repo and its authority/status. |
| [`docs/archive/`](docs/archive/) | Superseded documents, kept for historical reference, not for implementation. |

`CONSTITUTION.md` governs process; `Preflight_ADD.md` governs architecture —
see `CONSTITUTION.md` §8 for how the two relate. Do not treat any other
document, prior draft, or conversation as authoritative over either.

## Two core continuity guarantees

- **State Checkpointing** — no unit of work may be marked complete without
  durable, validator-checked evidence (file, DB record, checksum, Git
  snapshot).
- **Graceful Pause** — when a quota limit is calibrated-likely to hit soon,
  Preflight checkpoints state, interrupts at a safe point, and persists a
  durable wake job in SQLite; auto-resume is opt-in and re-verified before
  it runs.

See `Preflight_ADD.md` §1 for the full executive decision record.

## Day-1 wave roadmap

The Day-1 vertical slice is 84 tasks + 1 final integration across 7 roles
(see `docs/implementation/day1/EXECUTION_DAG.md`, as amended by ADR-041).
Stages and task dependencies are canonical in that DAG; **waves** are the
integration rounds the work actually ships in. Waves 1–2 below are as
executed. Wave 3 onward is a provisional, dependency-derived grouping —
each wave is re-planned by the lead before it starts (see
`docs/implementation/day1/wave2-analysis/` for the inputs to Wave 3
planning) and must respect the DAG's stage and dependency order.

| Wave | Scope (task IDs) | Status |
|---|---|---|
| Bootstrap | contract-integrator-01…07 — contract freeze (Stage 0) | ✅ Integrated (`940c5cb`) |
| Wave 1 | foundation-01 · claude-provider-01/02/03 · checkpoint-b02 · predictor-02/03/04 | ✅ Integrated (`3fb37ce`) |
| Wave 2 | foundation-02/03/04/05/09 · claude-provider-04/06 · checkpoint-b03 · predictor-05/06 | 🔄 In progress — implemented on role branches; integration + post-wave analysis under way |
| Wave 3 | foundation-06→07/08 · predictor-05b→05c→07→08 (ADR-041 forecast layer) · runtime-b01 · qa-01/08 | Planned — foundation-06 is the biggest unblocker (6 tasks across 4 roles hard-depend on it); completes **foundation** |
| Wave 4 | claude-provider-05→07 · checkpoint-a01→a02/a03 · checkpoint-b01→b04 · predictor-01→09→10→11 | Planned — completes **claude-provider** and **predictor** |
| Wave 5 | checkpoint-a04→a05/a07→a06/a08→a09 · checkpoint-b05/b06→b07→b08→b09 | Planned — completes **checkpoint**; contains checkpoint-a04, the single highest-risk task in the DAG |
| Wave 6 | runtime-a01→a02→a03/a04→a05 · runtime-a06→a07 · runtime-b02→b03/b04/b05/b08 | Planned — Stage 3 first half; Part B may start against fakes |
| Wave 7 | runtime-a08→a09/a10→a11 · runtime-b06/b07→b09→b10 | Planned — completes **runtime** (largest role, on the critical path) |
| Wave 8 | qa-02/03/04/05/06/07→09 | Planned — E2E demo, leakage scanner, security tests, final P0/P1/P2 report |
| Final | contract-integrator-final (Stage 5) | Planned — `go test ./... -race` + cross-role contradiction review; last gate |

`→` marks in-wave sequencing on a role's branch; `·` separates parallel
role branches within the same wave.

## Tech stack

- **Production runtime:** Go 1.26.x, single static binary, SQLite (WAL)
- **VS Code companion:** TypeScript (strict mode)
- **Research/offline only:** Python 3.12+ — never a runtime dependency
- **License:** Apache-2.0

## Repository layout (target — see `Preflight_ADD.md` §10 for the full tree)

```text
cmd/preflight/       entrypoint
internal/             application, domain, adapters
pkg/protocol/v1/      public wire protocol
vscode/                VS Code extension (not yet created)
research/              Python offline research (not yet created)
docs/adr/               accepted architecture decision records
docs/implementation/    Day-1 execution DAG, progress artifacts, wave analyses
docs/archive/           superseded documents
agents/                 Day-1 role definitions (one file per bounded context)
```

## Contributing

Read `CONSTITUTION.md`, `Preflight_ADD.md`, and `AGENTS.md` in full before
proposing or implementing changes. Work is milestone-gated
(`Preflight_ADD.md` §31); do not implement ahead of the current milestone
or add speculative abstractions for future providers/features.
