> **ARCHIVED — obsolete.** This early draft kickoff prompt specifies a
> four-teammate, two-phase team structure that conflicts with the approved
> nine-agent (A00–A08) topology in `Preflight_Day1_Parallel_Execution_Plan.md`
> and `agent-packets/`. It also predates the current directive not to spawn
> teammates during Phase 0. Kept for historical reference only; do not
> execute this prompt as-is.

> 🌐 English | [繁體中文](execution_prompt.zh-TW.md)

Create an agent team with exactly four teammates.

Do not spawn more than four teammates without asking me first.
Use tmux split-pane mode.
Use Fable for each teammate if Fable is available.

You are the team lead and integrator.
Do not implement teammate-owned production code yourself.
Do not declare completion until every required artifact and validation result exists.

The contract freeze commit is:

<INSERT_CONTRACT_COMMIT_SHA>

Read these files before creating tasks:

- Preflight_ADD.md
- Preflight_Day1_Parallel_Execution_Plan.md
- docs/implementation/day1/CONTRACT_FREEZE.md
- AGENTS.md

Create two execution phases.

Phase 1:
- foundation: A01 Foundation, Config, Paths, SQLite
- claude-adapter: A02 Claude Telemetry and Hooks
- state-checkpoint: A03 Progress Tree and State Checkpointing
- repository-checkpoint: A04 Git Observer and Repository Checkpoint

Phase 2:
- foundation handles A05 Predictor and Policy
- claude-adapter handles A06 Graceful Pause and Scheduler
- state-checkpoint handles A07 CLI and Application Orchestration
- repository-checkpoint handles A08 QA, Security, Reliability, and CI

Phase 2 tasks must remain blocked until their required Phase 1
dependencies are completed.

Strict ownership rules:

1. Every teammate may modify only its assigned paths.
2. Never modify another teammate's files.
3. Only foundation may modify go.mod or go.sum.
4. Only the lead may modify shared contracts.
5. Teammates must not run git add or git commit.
6. The lead is the only Git committer.
7. Each teammate must update its own progress artifact after every
   completed logical node.
8. A task is not complete unless its required files exist and its
   validation commands pass.
9. If a shared contract is insufficient, message the lead instead of
   changing it.
10. Wait for all Phase 1 teammates before integrating or starting Phase 2.

Before implementation, create the complete shared task graph and show me:

- task IDs
- dependencies
- assigned teammate
- owned paths
- validation command
- expected artifact

Require plan approval before any teammate writes code.
Only approve a plan when it respects the frozen contracts, migration
ranges, path ownership, and test requirements.