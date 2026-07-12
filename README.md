# Preflight

**Preflight is a local-first predictive runtime guard for AI coding agents.**
Before and during each turn with a provider like Codex or Claude Code, it
estimates scope, token/quota consumption, completion likelihood, and
blast-radius risk, then applies policy to run, warn, checkpoint, split,
gracefully pause, or block that turn.

It answers a different question than checkpoint/resume/memory tools do:
not "how do we continue?" but **"should we even start this turn?"**

> **Project status: Day-1 vertical slice in progress (Wave 8 of 9, integrated).**
> Bootstrap (Stage-0 contract freeze) and Waves 1-8 are integrated on
> `main`. Wave 7's P1 leakage finding (tracked-file-diff secrets not
> redacted) is fixed and independently confirmed. `qa-04` surfaced one
> new, non-blocking P1 (no production wiring yet connects provider events
> to Progress Tree node completion — routed for a future wave). Wave 9
> is being re-derived from the DAG's current dependency edges. See the
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
| Wave 2 | foundation-02/03/04(reduced)/05/09 · claude-provider-04/06 · checkpoint-b03 · predictor-05/06 | ✅ Integrated (`528b6ad`) |
| Wave 3 | foundation-06/08 · predictor-05b · runtime-b01 · qa-01/08 (ADR-041 Token Forecaster; first-ever nodes for **runtime** and **qa**, unassigned since Wave 1/Bootstrap respectively) | ✅ Integrated (`ca7062f`) |
| Wave 4 | foundation-07 · claude-provider-05 · checkpoint-a01/b01 · predictor-01/05c · runtime-a01/b02 | ✅ Integrated (`a0b10f2`) — includes a corrective fix to `migrate_test.go`'s hardcoded migration-count assertions, confirmed necessary by 5 independent cross-role reports before any sibling role's migrations could coexist with foundation's in one tree |
| Wave 5 | claude-provider-07 · checkpoint-a02/a03/b04 · predictor-07 · runtime-a02/a06/b03/b04/b05/b08 | ✅ Integrated (`dabaa9f`) — the DAG's real unlocked frontier after Wave 4 was larger than originally guessed (six runtime nodes unlocked at once, no `predictor-05d` ever existed); `b03`/`b04`/`b05` still run against fakes for `predictor-08`/`predictor-09`/`checkpoint-a04`, swapped to real implementations at a later integration |
| Wave 6 | checkpoint-a04/b05/b06 · predictor-08 · runtime-a03/a04/a07 | ✅ Integrated (`f5f0f28`) — checkpoint-a04 (CompleteNode atomic protocol) is now real, with crash-injection and concurrent-completion-race proofs independently re-verified; predictor-08's cold-start "probability: null" invariant independently traced to exactly two gated call sites |
| Wave 7 | checkpoint-a05/a07/b07 · predictor-09 · runtime-a05/b07 · qa-05 | ✅ Integrated (`25e3d40`) — qa's first Stage-4 node since Wave 3; found one real P1 (secret filtering doesn't cover tracked-file diffs, only untracked-file archives), not fixed here per qa's file-don't-fix boundary, routed to checkpoint |
| Wave 8 | checkpoint-a06/a08/b08 · predictor-10 · runtime-a08 · qa-04 | ✅ Integrated (`b5a1937`) — includes a corrective fix extending secret redaction to tracked-file diffs (closing Wave 7's P1), and predictor-10's adversarial audit found and fixed a real authorization prompt-binding bypass |
| Wave 9 | checkpoint-a09/b09 · predictor-11 · runtime-a09/a10/a11/b06/b09/b10 · qa-02/03/06/07/09 | Planned — re-derived from the DAG once Wave 8 lands; completes **runtime** and **checkpoint**, then qa's E2E demo/security suite and final P0/P1/P2 report |
| Final | contract-integrator-final (Stage 5) | Planned — `go test ./... -race` + cross-role contradiction review; last gate |

Wave 5 onward is intentionally not fixed in detail — each wave is
re-derived from the DAG's actual dependency edges once the prior wave
integrates (see `docs/implementation/day1/wave2-analysis/Wave3_Recommendation.md`
for the method), not planned far in advance against a DAG that keeps
changing shape as work lands.

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
