# Agents

This directory contains role definitions.

Each file is independent.

Each role owns a bounded context.

Never modify another role's files.

Refer to `Auspex_ADD.md` for architecture.

## Roles

| File | Role | Consolidates (see `docs/archive/agent-packets-v1/` for the pre-merge, numbered history) |
|---|---|---|
| `contract-integrator.md` | Freezes compile-time/persistence contracts; integrates reviewed branches | (was `00-contract-integrator.md`) |
| `foundation.md` | Go module, config, paths, core SQLite runtime | (was `01-foundation-config-sqlite.md`) |
| `claude-provider.md` | Claude Code telemetry, hooks, provider normalization | (was `02-claude-telemetry-hooks.md`) |
| `checkpoint.md` | Progress Tree + State Checkpointing, **and** Repository Checkpoint | (was `03-progress-state-checkpoint.md` + `04-repository-checkpoint.md`) |
| `predictor.md` | Scope estimator, predictor, risk, policy, authorization | (was `05-predictor-policy.md`) |
| `runtime.md` | Graceful Pause + durable scheduler, **and** CLI/API/orchestration | (was `06-graceful-pause-scheduler.md` + `07-runtime-cli-api.md`) |
| `qa.md` | Cross-component QA, security, reliability, CI | (was `08-qa-security-ci.md`) |
| `CONTRACT_FREEZE_TEMPLATE.md` | Scaffold for the contract-integrator's `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` deliverable | (unchanged) |

Two roles (`checkpoint`, `runtime`) each cover what were previously two
separate packets. They stay one role because their two halves are always
consumed together in practice, but each file keeps the two halves in
clearly separated Part A / Part B sections — including separate exclusive
paths and separate migration ranges — so the internal boundary is still a
real seam, not a merge into one undifferentiated blob.

## Spawning

A packet file is meant to be handed, on its own, to an isolated
agent/worktree — it does not require the full `Auspex_ADD.md` or
`Auspex_Parallel_Execution_Plan.md` in context. To start a role:
give it this file's contents, its assigned `Auspex_ADD.md` chapters, and
`docs/implementation/vertical-slice/CONTRACT_FREEZE.md` once the contract-integrator
has produced it.

For overall context (vision, scope boundary, topology, merge order, cut
list), see `Auspex_Parallel_Execution_Plan.md` at the repository
root. For architecture, see `Auspex_ADD.md`. For project-wide governance
and precedence rules, see the Auspex Repository Constitution.
