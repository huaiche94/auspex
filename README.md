# Preflight

**Preflight is a local-first predictive runtime guard for AI coding agents.**
Before and during each turn with a provider like Codex or Claude Code, it
estimates scope, token/quota consumption, completion likelihood, and
blast-radius risk, then applies policy to run, warn, checkpoint, split,
gracefully pause, or block that turn.

It answers a different question than checkpoint/resume/memory tools do:
not "how do we continue?" but **"should we even start this turn?"**

> **Project status: vertical slice complete (85/85 DAG nodes, Final gate passed).**
> Bootstrap (Stage-0 contract freeze), Waves 1-12, and the Final
> integration gate (`contract-integrator-final`, Stage 5,
> [#2](https://github.com/huaiche94/preflight/issues/2), closed) are all
> integrated on `main`. Every feature role completed its entire DAG scope,
> and the Final gate's own cross-role contradiction review found and
> closed the one real gap it exists to catch: `cmd/preflight/main.go` was
> still a stub, and two of five frozen service ports (plus predictor's
> `DataSource` seam) had never been assembled into real implementations —
> every individual piece was correct and tested, but the actual binary
> was never wired to any of it. Closed via three routed corrective
> additions plus the lead's own reserved root-composition work; see
> `docs/implementation/vertical-slice/contract-integrator.md`'s "Stage 5"
> section for the full report. One P1 remains open (no production adapter
> yet connects a persisted provider event to Progress Tree node
> completion — [#1](https://github.com/huaiche94/preflight/issues/1)),
> plus a groomed backlog of post-slice follow-ups
> ([#3](https://github.com/huaiche94/preflight/issues/3)-[#16](https://github.com/huaiche94/preflight/issues/16)).
> See the [Wave roadmap](#wave-roadmap) below and
> `docs/implementation/vertical-slice/EXECUTION_DAG.md` for task-level status.
> Milestone gating per `Preflight_ADD.md` §31 still applies.

## Source of truth

| Document | Role |
|---|---|
| [`CONSTITUTION.md`](CONSTITUTION.md) | **Supreme process authority.** Single-source-of-truth hierarchy, document precedence, ADR rules, path ownership, provider-addition criteria, Progress Tree invariants, and the rules every agent must follow. Read this first. |
| [`Preflight_ADD.md`](Preflight_ADD.md) | **The single authoritative architecture and implementation specification.** When code, issues, PRs, or comments conflict with it, this document (and accepted ADRs under `docs/adr/`) wins for architecture. |
| [`AGENTS.md`](AGENTS.md) | Contributor/agent quick-reference — required reading before any implementation work. |
| [`Preflight_Parallel_Execution_Plan.md`](Preflight_Parallel_Execution_Plan.md) | Subordinate execution plan for the first vertical-slice build: seven-role topology, ownership boundaries, merge order. |
| [`agents/`](agents/) | One canonical role definition per bounded context, linked from the plan above. |
| [`docs/adr/`](docs/adr/) | Accepted Architecture Decision Records — full-detail companions to the short entries in `Preflight_ADD.md` §33. |
| [`Preflight_Predictor_Design_Supplement.md`](Preflight_Predictor_Design_Supplement.md) | Predictor pipeline design detail (Scope/Token/Quota Forecast, Risk Estimation) — companion to `Preflight_ADD.md` §14-17, formalized by ADR-041. |
| [`docs/implementation/vertical-slice/`](docs/implementation/vertical-slice/) | Live vertical-slice execution status: `EXECUTION_DAG.md` (task-level DAG, amended by ADR-041), `CONTRACT_FREEZE.md`, per-role progress artifacts, lessons learned, and post-wave analyses. |
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

## What an AI-assisted session sees — and what it can do

When Preflight is wired into an AI coding session (today: Claude Code
native hook mode, wired per [`integrations/claude/`](integrations/claude/)),
it evaluates every prompt before it runs and keeps observing the session
while it runs. Every signal below is machine-readable (`--json`, FR-160),
so it serves two readers at once: the **human developer** deciding whether
to let a turn proceed, and the **coding agent itself**, which receives the
gate's decision through the hook response.

### Signals

| Signal | Produced by | Surfaces via |
|---|---|---|
| Task class + scope estimate (expected file/LOC bands) | prompt feature extraction (privacy-bounded — raw prompt is never retained) → rule scope estimator | `preflight evaluate`, UserPromptSubmit hook |
| Token forecast (P50/P80/P90 quantile bands) | Token Forecaster (`internal/predictor/token`, ADR-041) | same |
| Quota fit — will this turn fit the remaining window | Quota Forecaster (`internal/predictor/quota`) | same |
| Runway score — will the session survive the next ~10 minutes | status-line telemetry → runway scorer, consumed by pause Observe (debounce/hysteresis) | `preflight hook claude statusline`, pause observe loop |
| Risk score — **an uncalibrated score, never called a probability** (Constitution principle #2; cold-start policy emits `probability: null`) | risk combiner (`internal/predictor/risk`) | `preflight evaluate` |
| Policy decision — one of the eight frozen actions: `RUN`, `WARN`, `REQUIRE_CONFIRMATION`, `CHECKPOINT_AND_RUN`, `SPLIT`, `PAUSE`, `PAUSE_AND_AUTO_RESUME`, `BLOCK` | policy engine (`internal/policy`) | UserPromptSubmit hook response, `preflight decision` |
| Progress Tree node states — completion is evidence-backed, never the agent's own claim of "done" | `internal/progress` CompleteNode atomic protocol | `preflight status` |
| Checkpoint / pause / wake-job state | state + repository checkpoint stores, pause records | `preflight status` |
| Environment health (DB, migrations, paths, git) | diagnostics | `preflight doctor` |

### Actions

The per-prompt gate acts automatically: the policy action above returns as
the UserPromptSubmit hook response (allow, warn with context, require a
checkpoint first, or block), and the hook stays provider-compatible even
on internal failure. Beyond that, the human or the agent can invoke:

```text
preflight evaluate               estimate the current turn before running it
preflight decision allow|deny    consume a one-time authorization (replays are rejected)
preflight checkpoint create      State Checkpoint + Repository Checkpoint (never mutates the active branch)
preflight pause request|cancel   safe-point pause with a durable wake job
preflight resume                 re-verified resume (repo/quota/session/authorization re-checked first)
preflight scheduler run-once     execute due wake jobs (daemon-run automation: #7)
preflight status | doctor        session/checkpoint/pause state; environment health
preflight hook claude <event>    the four hook entrypoints Claude Code calls
                                 (user-prompt-submit, stop, stop-failure, statusline)
```

### Known limits today

- Provider events don't yet drive Progress Tree completion in production
  wiring ([#1](https://github.com/huaiche94/preflight/issues/1), the one
  open P1).
- Unattended auto-resume needs the M6 daemon
  ([#7](https://github.com/huaiche94/preflight/issues/7)); until then
  wake jobs fire via `scheduler run-once`.
- A richer in-session forecast card (tokens/cost/scope per prompt) is
  tracked in [#14](https://github.com/huaiche94/preflight/issues/14), and
  calibrated probabilities require real telemetry first
  ([#11](https://github.com/huaiche94/preflight/issues/11),
  [#12](https://github.com/huaiche94/preflight/issues/12)).

## How to use this repo

### Build and run

```bash
go build -o preflight ./cmd/preflight
./preflight version
./preflight doctor      # verifies DB connectivity + migration state
./preflight --help      # full command tree
```

Requires Go 1.26.x (see [Tech stack](#tech-stack)); no CGO, no external
services. The first run creates and migrates a SQLite database under the
OS user data directory (macOS: `~/Library/Application Support/preflight/`,
Linux: `$XDG_DATA_HOME/preflight/`), so `doctor` is a meaningful check
immediately after building.

### Wire it into Claude Code

Follow [`integrations/claude/`](integrations/claude/) — it ships the
`hooks.json`/`plugin.json` examples that route Claude Code's
UserPromptSubmit / Stop / StopFailure / statusline events through
`preflight hook claude <event>`. The [Signals](#signals) and
[Actions](#actions) sections above describe what you get once wired.

### Validate a change

Every wave of this repo's own build was gated on exactly these, and they
are the expected local pre-commit bar:

```bash
gofmt -l . && go build ./... && go vet ./...
go test ./... -race
golangci-lint run ./...
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs the same
across Ubuntu/macOS/Windows.

### Find your way around the docs

- **Contributing or running an agent against this repo:** read
  [`CONSTITUTION.md`](CONSTITUTION.md) (process rules), then
  [`Preflight_ADD.md`](Preflight_ADD.md) (architecture), then
  [`AGENTS.md`](AGENTS.md) (quick reference) — in that order, per the
  [Source of truth](#source-of-truth) table.
- **Understanding how the slice was built:** the
  [Wave roadmap](#wave-roadmap) below links every task group to its
  role's progress artifact and every wave to its integration commit;
  [`docs/implementation/vertical-slice/EXECUTION_DAG.md`](docs/implementation/vertical-slice/EXECUTION_DAG.md)
  is the task-level plan it executed.
- **Reusing the process on another project:**
  [`docs/methodology/Preflight_Development_Methodology.md`](docs/methodology/Preflight_Development_Methodology.md)
  distills the multi-agent, evidence-based process this repo was built
  with into a standalone, citable methodology.
- **What's next:** the [issue tracker](https://github.com/huaiche94/preflight/issues)
  holds the groomed post-slice backlog (open P1, security follow-ups,
  ADR-needed items, and roadmap milestones M6-M13).

## Wave roadmap

The vertical slice is 84 tasks + 1 final integration across 7 roles
(see `docs/implementation/vertical-slice/EXECUTION_DAG.md`, as amended by ADR-041).
Stages and task dependencies are canonical in that DAG; **waves** are the
integration rounds the work actually ships in. Waves 1–2 below are as
executed. Wave 3 onward is a provisional, dependency-derived grouping —
each wave is re-planned by the lead before it starts (see
`docs/implementation/vertical-slice/wave2-analysis/` for the inputs to Wave 3
planning) and must respect the DAG's stage and dependency order.

Each task-ID group below links to the owning role's progress artifact
(per-node status/artifact/validation logs); each commit hash links to the
integration commit on GitHub.

| Wave | Scope (task IDs) | Status |
|---|---|---|
| Bootstrap | [contract-integrator-01…07](docs/implementation/vertical-slice/contract-integrator.md) — contract freeze (Stage 0) | ✅ Integrated ([`940c5cb`](https://github.com/huaiche94/preflight/commit/940c5cb)) |
| Wave 1 | [foundation-01](docs/implementation/vertical-slice/foundation.md) · [claude-provider-01/02/03](docs/implementation/vertical-slice/claude-provider.md) · [checkpoint-b02](docs/implementation/vertical-slice/checkpoint.md) · [predictor-02/03/04](docs/implementation/vertical-slice/predictor.md) | ✅ Integrated ([`3fb37ce`](https://github.com/huaiche94/preflight/commit/3fb37ce)) |
| Wave 2 | [foundation-02/03/04(reduced)/05/09](docs/implementation/vertical-slice/foundation.md) · [claude-provider-04/06](docs/implementation/vertical-slice/claude-provider.md) · [checkpoint-b03](docs/implementation/vertical-slice/checkpoint.md) · [predictor-05/06](docs/implementation/vertical-slice/predictor.md) | ✅ Integrated ([`528b6ad`](https://github.com/huaiche94/preflight/commit/528b6ad)) |
| Wave 3 | [foundation-06/08](docs/implementation/vertical-slice/foundation.md) · [predictor-05b](docs/implementation/vertical-slice/predictor.md) · [runtime-b01](docs/implementation/vertical-slice/runtime.md) · [qa-01/08](docs/implementation/vertical-slice/qa.md) (ADR-041 Token Forecaster; first-ever nodes for **runtime** and **qa**, unassigned since Wave 1/Bootstrap respectively) | ✅ Integrated ([`ca7062f`](https://github.com/huaiche94/preflight/commit/ca7062f)) |
| Wave 4 | [foundation-07](docs/implementation/vertical-slice/foundation.md) · [claude-provider-05](docs/implementation/vertical-slice/claude-provider.md) · [checkpoint-a01/b01](docs/implementation/vertical-slice/checkpoint.md) · [predictor-01/05c](docs/implementation/vertical-slice/predictor.md) · [runtime-a01/b02](docs/implementation/vertical-slice/runtime.md) | ✅ Integrated ([`a0b10f2`](https://github.com/huaiche94/preflight/commit/a0b10f2)) — includes a corrective fix to `migrate_test.go`'s hardcoded migration-count assertions, confirmed necessary by 5 independent cross-role reports before any sibling role's migrations could coexist with foundation's in one tree |
| Wave 5 | [claude-provider-07](docs/implementation/vertical-slice/claude-provider.md) · [checkpoint-a02/a03/b04](docs/implementation/vertical-slice/checkpoint.md) · [predictor-07](docs/implementation/vertical-slice/predictor.md) · [runtime-a02/a06/b03/b04/b05/b08](docs/implementation/vertical-slice/runtime.md) | ✅ Integrated ([`dabaa9f`](https://github.com/huaiche94/preflight/commit/dabaa9f)) — the DAG's real unlocked frontier after Wave 4 was larger than originally guessed (six runtime nodes unlocked at once, no `predictor-05d` ever existed); `b03`/`b04`/`b05` still run against fakes for `predictor-08`/`predictor-09`/`checkpoint-a04`, swapped to real implementations at a later integration |
| Wave 6 | [checkpoint-a04/b05/b06](docs/implementation/vertical-slice/checkpoint.md) · [predictor-08](docs/implementation/vertical-slice/predictor.md) · [runtime-a03/a04/a07](docs/implementation/vertical-slice/runtime.md) | ✅ Integrated ([`f5f0f28`](https://github.com/huaiche94/preflight/commit/f5f0f28)) — checkpoint-a04 (CompleteNode atomic protocol) is now real, with crash-injection and concurrent-completion-race proofs independently re-verified; predictor-08's cold-start "probability: null" invariant independently traced to exactly two gated call sites |
| Wave 7 | [checkpoint-a05/a07/b07](docs/implementation/vertical-slice/checkpoint.md) · [predictor-09](docs/implementation/vertical-slice/predictor.md) · [runtime-a05/b07](docs/implementation/vertical-slice/runtime.md) · [qa-05](docs/implementation/vertical-slice/qa.md) | ✅ Integrated ([`25e3d40`](https://github.com/huaiche94/preflight/commit/25e3d40)) — qa's first Stage-4 node since Wave 3; found one real P1 (secret filtering doesn't cover tracked-file diffs, only untracked-file archives), not fixed here per qa's file-don't-fix boundary, routed to checkpoint |
| Wave 8 | [checkpoint-a06/a08/b08](docs/implementation/vertical-slice/checkpoint.md) · [predictor-10](docs/implementation/vertical-slice/predictor.md) · [runtime-a08](docs/implementation/vertical-slice/runtime.md) · [qa-04](docs/implementation/vertical-slice/qa.md) | ✅ Integrated ([`b5a1937`](https://github.com/huaiche94/preflight/commit/b5a1937)) — includes a corrective fix extending secret redaction to tracked-file diffs (closing Wave 7's P1), and predictor-10's adversarial audit found and fixed a real authorization prompt-binding bypass |
| Wave 9 | [checkpoint-a09/b09](docs/implementation/vertical-slice/checkpoint.md) · [predictor-11](docs/implementation/vertical-slice/predictor.md) · [runtime-a09/a10/b06](docs/implementation/vertical-slice/runtime.md) | ✅ Integrated ([`192e4b9`](https://github.com/huaiche94/preflight/commit/192e4b9)) — completes **checkpoint** (a01-a09/b01-b09) and **predictor** (01-11) entirely; found and fixed a real path-traversal vulnerability (checkpoint) and a real TOCTOU race (runtime) |
| Wave 10 | [runtime-a11 · runtime-b09](docs/implementation/vertical-slice/runtime.md) | ✅ Integrated ([`a249ca2`](https://github.com/huaiche94/preflight/commit/a249ca2)) — closed two genuine gaps: a missing TurnInterrupter-to-PauseRecord wiring path, and no CLI command ever serialized its typed error to JSON (Cobra's default printer flattened it to plain text) |
| Wave 11 | [runtime-b10](docs/implementation/vertical-slice/runtime.md) | ✅ Integrated ([`2fbc0c8`](https://github.com/huaiche94/preflight/commit/2fbc0c8)) — completes **runtime** entirely (a01-a11/b01-b10, 21 nodes across 9 waves); proved in-process restart on the same SQLite file, including a real OS-process SIGKILL crash test |
| Wave 12 | [qa-02/03/06/07/09](docs/implementation/vertical-slice/qa.md) | ✅ Integrated ([`a91c239`](https://github.com/huaiche94/preflight/commit/a91c239)) — completes **qa** entirely; the literal vertical-slice E2E demo runs real code end-to-end. Final report: no P0s, one open P1 (provider-event-to-node-completion wiring), fully documented |
| Final | [contract-integrator-final](docs/implementation/vertical-slice/contract-integrator.md) (Stage 5) | ✅ Integrated ([`3b6cfcb`](https://github.com/huaiche94/preflight/commit/3b6cfcb) + [`faca171`](https://github.com/huaiche94/preflight/commit/faca171)) — found and closed the composition gap the gate exists to catch: `cmd/preflight/main.go` was never wired to real services. See [`contract-integrator.md`](docs/implementation/vertical-slice/contract-integrator.md)'s Stage 5 section |

Wave 5 onward is intentionally not fixed in detail — each wave is
re-derived from the DAG's actual dependency edges once the prior wave
integrates (see `docs/implementation/vertical-slice/wave2-analysis/Wave3_Recommendation.md`
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
docs/implementation/    execution DAG, progress artifacts, wave analyses
docs/archive/           superseded documents
agents/                 vertical-slice role definitions (one file per bounded context)
```

## Contributing

Read `CONSTITUTION.md`, `Preflight_ADD.md`, and `AGENTS.md` in full before
proposing or implementing changes. Work is milestone-gated
(`Preflight_ADD.md` §31); do not implement ahead of the current milestone
or add speculative abstractions for future providers/features.
