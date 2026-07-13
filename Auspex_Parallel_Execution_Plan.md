# Auspex Vertical-Slice Parallel Implementation Plan

## 1. Decision

Do **not** assign one agent per ADD chapter. Chapters 1–35 mix product constraints, domain contracts, provider adapters, runtime behavior, testing, and delivery concerns. A chapter-per-agent split would create duplicate types, incompatible schemas, and severe merge conflicts.

Use one contract/integration role and six bounded-context roles. Every role works in a dedicated Git worktree and owns a non-overlapping path set. Role definitions live under `agents/`, one semantically-named file per role (`agents/README.md` explains the convention); two of the seven roles (`checkpoint`, `runtime`) each cover what were originally two narrower bounded contexts, merged because their two halves are always consumed together in practice — see `agents/checkpoint.md` and `agents/runtime.md` for the internal Part A / Part B split each still preserves.

The day-one objective is a real vertical slice for Claude Code:

```text
Claude status-line / hook event
        ↓
normalized telemetry persisted in SQLite
        ↓
UserPromptSubmit auspex evaluation
        ↓
explainable risk decision
        ↓
ALLOW or BLOCK
        ↓
checkpoint + one-time authorization
        ↓
turn completion/failure outcome persisted
        ↓
pause state and durable wake job can be created/recovered
```

Day-one success is **not** “all ADD chapters implemented.” Success is proving the core loop while preserving the long-term architecture.

## 2. Day-one scope boundary

### Must merge

1. Repository bootstraps and builds on the target OS.
2. Shared domain/event/store contracts are frozen once.
3. Claude status-line and hook payloads are parsed from fixtures.
4. Quota/context/turn telemetry is stored idempotently.
5. A deterministic scope/risk predictor returns P50/P90 estimates, reason codes, confidence, and `calibrated=false` during cold start.
6. `UserPromptSubmit` can allow or block before the main turn.
7. High-risk flow can create State Checkpoint and Repository Checkpoint evidence.
8. Progress node completion is rejected without durable artifact evidence.
9. One-time authorization prevents prompt replay.
10. Pause state machine and durable wake job survive process restart in tests.
11. CLI exposes the vertical slice.
12. One end-to-end fixture test covers the entire flow.

### Explicitly deferred

- Chapter 21 full Codex integration.
- Chapter 25 VS Code extension.
- ML training, personalization, ONNX, and Python research runtime.
- Fully calibrated probability claims.
- Public package-manager release automation beyond basic CI scaffolding.
- OS wake-up from shutdown/suspend.
- Multi-provider routing.
- External provider plugin protocol.
- Production-grade automatic invocation of a sleeping Claude process if provider lifecycle support is not proven by fixture-backed tests.

## 3. Model allocation

Use Fable where correctness and state-machine reasoning dominate:

- `contract-integrator`: contract freeze and integration.
- `predictor`: predictor/policy.
- `runtime` Part A: pause/scheduler.
- Final architecture and race-condition review.

Use a cheaper coding model for deterministic implementation work when available:

- foundation/config/SQLite;
- Claude JSON parsing and hook fixtures;
- Git snapshot/checkpoint plumbing;
- CLI/API wiring (`runtime` Part B);
- CI and test harness.

If every worker must use Fable, do not give the complete 161 KB ADD to every worker. Give each worker only:

1. this common plan;
2. `CONTRACT_FREEZE.md` after `contract-integrator` produces it;
3. its assigned ADD chapters;
4. its role file from `agents/`.

## 4. Parallel topology

```text
                         ┌─────────────────────────────┐
                         │ contract-integrator          │
                         │ freeze types / ports / IDs   │
                         └──────────────┬──────────────┘
                                        │
                                        ▼
                              foundation
                              Go module / config / SQLite
                                        │
                    ┌───────────────────┼───────────────────┐
                    ▼                   ▼                   ▼
             claude-provider        checkpoint           predictor
             Telemetry/Hooks    State CP + Repo CP     Risk/Policy/Auth
                    │                   │                   │
                    └───────────────────┴───────────────────┘
                                        │
                                        ▼
                                    runtime
                        Pause+Scheduler, then CLI/API/Orchestration
                                        │
                                        ▼
                                       qa
                              Security/CI/E2E
                                        │
                                        ▼
                              contract-integrator
                              (final integration)
```

`runtime` may start coding immediately against frozen interfaces and fakes for both of its parts (it does not need to wait for `checkpoint` or `predictor` concrete implementations) — but its Part B (CLI/API/orchestration) genuinely depends on its own Part A (pause/scheduler) being far enough along to wire, since both now live in one role's sequence.

## 5. Chapter 1–35 ownership map

| ADD chapter | Primary owner | Day-one treatment |
|---|---|---|
| 1–8 | `contract-integrator` | Read as constraints; produce contract freeze and scope guardrails. |
| 9 | `contract-integrator` | Freeze domain values, IDs, statuses, service ports, provider capability types. |
| 10 | `foundation`, governed by `contract-integrator` | Bootstrap exact package layout; no speculative packages. |
| 11 | `contract-integrator` + `claude-provider` | `contract-integrator` freezes envelope; `claude-provider` implements Claude normalization/ingestion. |
| 12 | `foundation` + feature owners | `foundation` owns DB engine/core migration; each role owns its allocated migration range. |
| 13 | `runtime` (Part B) + `predictor` | `predictor` evaluates; `runtime` orchestrates authorization and outcome lifecycle. |
| 14–17 | `predictor` | Scope, token/quota/runway baseline, risk, policy. |
| 18 | `checkpoint` (Part A) | Progress Tree and State Checkpointing. |
| 19 | `checkpoint` (Part B) | Repository Checkpoint and recovery. |
| 20 | `runtime` (Part A) | Graceful Pause and durable wake jobs. |
| 21 | Deferred | No Codex production adapter tomorrow. Fixtures/interfaces only if needed. |
| 22 | `claude-provider` | Claude plugin/hooks/status-line/native event normalization. |
| 23–24 | `runtime` (Part B) | In-process first; thin daemon/API/CLI surface. |
| 25 | Deferred | No VS Code implementation tomorrow. |
| 26 | `foundation` | Config model and defaults needed by vertical slice only. |
| 27 | `contract-integrator` + `qa` | Privacy/security constraints and tests. |
| 28 | `runtime` (Part B) + `qa` | Typed errors, logging, recovery, doctor baseline. |
| 29 | Every role + `qa` | Unit tests owned locally; `qa` owns cross-package/E2E tests. |
| 30 | `foundation` + `qa` | Basic OSS files and CI; full release matrix deferred. |
| 31–32 | `contract-integrator` | Milestone acceptance and final DoD gate. |
| 33 | `contract-integrator` | ADR compliance; only `contract-integrator` edits accepted ADRs. |
| 34 | All | Execution and durable-progress contract. |
| 35 | Split by appendix | `claude-provider` owns Claude templates; `checkpoint` A/B/D; `runtime` C/F; `qa` test/reference validation. |

## 6. Contract-freeze gate

No feature role should invent a competing domain type. `contract-integrator` must first commit `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` and compileable skeletons for:

- UUIDv7-style ID aliases or wrappers;
- `Session`, `Turn`, `Task`, `ProgressNode`, `ArtifactReference`;
- `StateCheckpoint`, `RepositoryCheckpoint`, `PauseRecord`, `WakeJob`;
- `UsageObservation`, `QuotaObservation`, `ContextObservation`;
- `Evaluation`, `PredictionResult`, `PolicyDecision`, `Authorization`;
- event envelope and event-type constants;
- failure classes and typed error codes;
- service interfaces from ADD §9.9;
- provider capability and hook normalization types from ADD §9.10;
- `Clock` and `IDGenerator` interfaces;
- SQLite transaction boundary conventions;
- JSON/YAML field names and schema-version strings.

### Immutable day-one rules

1. Raw prompts are not persisted by default.
2. Uncalibrated scores are never labeled as probabilities.
3. State Checkpoint and Repository Checkpoint are distinct entities.
4. Progress Tree is canonical task state.
5. Node completion requires durable artifact evidence.
6. Pause full guarantees apply only to managed execution; native-hook behavior is degraded and explicit.
7. All persistence writes are idempotent by stable event/operation ID.
8. Clock, process execution, and ID generation are injectable.
9. Provider wire payloads never leak into domain/storage rows.
10. Operational observation failure may fail open; state-integrity failure fails closed.

## 7. Shared-file and dependency policy

### Files owned only by `contract-integrator`

```text
Auspex_ADD.md
AGENTS.md
internal/domain/**
internal/app/ports.go
pkg/protocol/v1/**
docs/adr/**
docs/implementation/vertical-slice/CONTRACT_FREEZE.md
```

### Files owned only by `foundation`

```text
go.mod
go.sum
cmd/auspex/main.go
internal/config/**
internal/paths/**
internal/buildinfo/**
internal/storage/sqlite/db.go
internal/storage/sqlite/migrate.go
internal/storage/sqlite/migrations/0000-0009_*.sql
Makefile
Taskfile.yml
.golangci.yml
```

No other role edits `go.mod` or `go.sum`. Dependency requests go into its progress file for `foundation`/`contract-integrator` to apply.

### Migration allocation

```text
0000–0009  foundation           core/session/config
0010–0019  claude-provider      telemetry/provider events
0020–0029  checkpoint (Part A)  progress/state checkpoints
0030–0039  checkpoint (Part B)  repository checkpoints
0040–0049  predictor            evaluations/predictions/authorizations
0050–0059  runtime (Part A)     pause/wake jobs
```

`runtime` Part B does not add schema unless `contract-integrator` explicitly assigns a range.

### Unit-test ownership

Each feature role writes unit tests under its own package. `qa` owns only:

- `internal/integrationtest/**`;
- `testdata/e2e/**`;
- cross-component race/restart/security tests;
- CI workflows.

## 8. Git worktree setup

Recommended branches:

```bash
git worktree add ../auspex-contract-integrator -b vertical-slice/contract-integrator
git worktree add ../auspex-foundation           -b vertical-slice/foundation
git worktree add ../auspex-claude-provider      -b vertical-slice/claude-provider
git worktree add ../auspex-checkpoint           -b vertical-slice/checkpoint
git worktree add ../auspex-predictor            -b vertical-slice/predictor
git worktree add ../auspex-runtime              -b vertical-slice/runtime
git worktree add ../auspex-qa                   -b vertical-slice/qa
```

`contract-integrator` lands the contract commit first. Every other branch rebases onto that exact commit before writing production code.

## 9. Durable coordination artifacts

Every role owns exactly one progress artifact:

```text
docs/implementation/vertical-slice/contract-integrator.md
docs/implementation/vertical-slice/foundation.md
docs/implementation/vertical-slice/claude-provider.md
docs/implementation/vertical-slice/checkpoint.md
docs/implementation/vertical-slice/predictor.md
docs/implementation/vertical-slice/runtime.md
docs/implementation/vertical-slice/qa.md
```

After every logical node, the role must write:

```yaml
node: predictor-03
status: completed
artifacts:
  - internal/predictor/heuristic/predictor.go
  - internal/predictor/heuristic/predictor_test.go
validation:
  - go test ./internal/predictor/...
commit: <sha>
next_action: predictor-04 implement policy reason codes
assumptions: []
blockers: []
```

Conversation-only progress does not count.

## 10. Merge order

Use this order even when implementation occurs in parallel:

1. `contract-integrator` contract freeze.
2. `foundation` core SQLite.
3. `claude-provider`, `checkpoint`, `predictor` in any order after tests pass.
4. `runtime` (Part A pause/scheduler, then Part B CLI/API/orchestration).
5. `qa` CI/E2E/security tests.
6. `contract-integrator` final reconciliation and architecture review.

`contract-integrator` should cherry-pick or merge **whole reviewed commits**, not copy generated code manually.

## 11. Final day-one demo

The final branch should demonstrate:

```bash
auspex version
auspex init

# Feed a Claude status-line fixture.
auspex hook claude statusline < testdata/provider-events/claude/statusline-high-usage.json

# Evaluate a prompt without persisting raw prompt text.
auspex evaluate \
  --provider claude \
  --prompt-file testdata/e2e/high-risk-prompt.txt \
  --json

# Simulate UserPromptSubmit; high risk returns provider-compatible block output.
auspex hook claude user-prompt-submit \
  < testdata/provider-events/claude/user-prompt-submit-high-risk.json

# Create both state and repository evidence.
auspex checkpoint create --evaluation <id> --json

# Issue a one-time allow decision and consume it once.
auspex decision allow --evaluation <id> --json

# Persist a normal turn completion or rate-limit failure fixture.
auspex hook claude stop < testdata/provider-events/claude/stop.json
auspex hook claude stop-failure < testdata/provider-events/claude/stop-failure-rate-limit.json

# Exercise pause/wake durability without depending on wall-clock sleep.
auspex pause request --session <id> --reason runway --json
auspex scheduler run-once --at <timestamp> --json
auspex status --json
```

## 12. Cut order when integration slips

Cut features in this order:

1. HTTP daemon transport; keep in-process CLI.
2. SSE/live dashboard.
3. Actual provider-process auto-resume; keep durable wake state and fake resumer contract test.
4. Repository restore; keep create/verify.
5. Native status-line composition with pre-existing user command; document manual setup.
6. Full OSS release matrix.

Never cut:

- prompt privacy default;
- authorization replay protection;
- artifact evidence requirement;
- checkpoint atomicity;
- calibrated/uncalibrated distinction;
- pause state durability;
- provider payload fixtures and contract tests.

## 13. Final Fable review prompt

```text
Review the merged Auspex day-one vertical slice against Auspex_ADD.md.

Focus only on:
1. domain/schema contradictions;
2. raw-prompt or secret persistence;
3. authorization replay/staleness;
4. State Checkpoint atomicity and artifact evidence;
5. Repository Checkpoint race/path traversal handling;
6. quota/risk values incorrectly presented as calibrated probabilities;
7. pause/resume state-machine races, duplicate wake, stale lease, and repository conflict;
8. Claude hook output compatibility and unknown-field tolerance;
9. missing restart and idempotency tests.

Do not redesign the project or add future abstractions. Produce a severity-ranked
file-level report, then fix only P0/P1 findings and run the complete test suite.
```

---

# Agent Roles

The full text of each role definition lives as a standalone file under
`agents/`, so a single file can be handed to an isolated agent/worktree
without the rest of this plan (see §3 above and `agents/README.md`).

**`agents/*.md` is the single source of truth for role content.**
This section is an index only — edit role details there, not here, so the
two never drift out of sync.

| Role | File | Model | Mission |
|---|---|---|---|
| contract-integrator | [`agents/contract-integrator.md`](agents/contract-integrator.md) | Fable | Freeze compile-time/persistence contracts; integrate reviewed branches at the end. |
| foundation | [`agents/foundation.md`](agents/foundation.md) | Cheaper model; Fable for migration/recovery review | Buildable Go application foundation and the SQLite runtime every other package depends on. |
| claude-provider | [`agents/claude-provider.md`](agents/claude-provider.md) | Fable for hook semantics; cheaper model for parsers/fixtures | Fixture-backed Claude Code hook/status-line normalization into frozen Auspex events. |
| checkpoint | [`agents/checkpoint.md`](agents/checkpoint.md) | Fable | Progress Tree + State Checkpointing (Part A) **and** Repository Checkpoint (Part B); no completion without verified artifact evidence, and checkpoints without mutating the active branch. |
| predictor | [`agents/predictor.md`](agents/predictor.md) | Fable | Deterministic, explainable, cold-start-safe predictor/policy/authorization loop. |
| runtime | [`agents/runtime.md`](agents/runtime.md) | Fable for Part A (pause/scheduler); cheaper model for most of Part B (CLI/API) | Graceful Pause + durable wake scheduling (Part A) **and** CLI/API/orchestration wiring the vertical slice (Part B). |
| qa | [`agents/qa.md`](agents/qa.md) | Cheaper model for fixtures/CI; Fable for final adversarial review | Objective evidence that the vertical slice is safe, restartable, idempotent, provider-compatible. |

The contract-freeze template `contract-integrator` fills in when it produces
`docs/implementation/vertical-slice/CONTRACT_FREEZE.md` lives at
[`agents/CONTRACT_FREEZE_TEMPLATE.md`](agents/CONTRACT_FREEZE_TEMPLATE.md).

The prior numbered nine-role structure (`A00`–`A08`) is archived at
`docs/archive/agent-packets-v1/` for historical reference only.
