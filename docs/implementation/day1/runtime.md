# runtime — Progress Artifact

> **Wave 5 sections appended below the Wave 4 node log** — see "Wave 5"
> heading. Wave 5 completes six nodes in one pass: the full currently-
> unlocked frontier for this role (`runtime-a02`, `runtime-a06`,
> `runtime-b03`, `runtime-b04`, `runtime-b05`, `runtime-b08`). No new
> cross-role change requests this wave; Wave 4's foundation migrate_test.go
> change request was resolved before this wave started (confirmed: `go
> test ./internal/storage/sqlite/...` is fully green on this branch).

> **Wave 4 sections appended below the Wave 3 node log** — see "Wave 4"
> heading. Wave 4 adds `runtime-a01` (Part A's migration range 0050-0059,
> this role's first Part A node) and `runtime-b02` (app wiring), and
> includes one **cross-role change request to `foundation`** (stale exact
> count/version assertions in `internal/storage/sqlite/migrate_test.go`)
> that the merge integrator should read before merging this branch.

This is `runtime`'s first progress artifact. Per `agents/runtime.md`, this
role consolidates two internal sub-components — **Part A** (Graceful
Pause, Safe Points, Durable Scheduler) and **Part B** (Application
Orchestration, CLI, Local API). Wave 3's assigned node, `runtime-b01`, is
Part B only; Part A (`internal/pause/**`, `internal/scheduler/**`) is not
touched by this artifact and has no entry here yet.

## Handoff notes (Constitution §6.7 / agents/runtime.md "Handoff")

- **CLI package shape**: `internal/cli.NewRootCmd() *cobra.Command` is the
  single exported entry point, mirroring the constructor convention
  foundation-01 established directly in `cmd/preflight/main.go`
  (`newRootCmd()`/`newVersionCmd()`, unexported because that file is the
  binary's own package). Because `internal/cli` is a separate package
  intended for a future root-wiring step to import, `NewRootCmd` is
  exported; every other constructor in the package
  (`newVersionCmd`, `newHookCmd`, `newHookClaudeCmd`, `newInitCmd`,
  `newEvaluateCmd`, `newDecisionCmd`, `newCheckpointCmd`, `newProgressCmd`,
  `newStateCmd`, `newPauseCmd`, `newResumeCmd`, `newSchedulerCmd`,
  `newStatusCmd`, `newDoctorCmd`) stays unexported, matching the granularity
  of the ports/DTOs they will eventually call.
- **Stub error shape**: every command below `version` returns
  `notImplemented(command string) error` (`internal/cli/errors.go`), which
  builds the frozen `*domain.Error` (`internal/domain/errors.go`,
  `CONTRACT_FREEZE.md` "Error contract") with `Code: ErrCodeUnavailable`,
  `Retryable: true`, and `Details["command"]` set to the dotted command
  path (e.g. `"hook claude user-prompt-submit"`). `ErrCodeUnavailable` was
  chosen over `ErrCodeInternal` deliberately: the command surface itself is
  correct and will work once the corresponding service
  (`EvaluationService`, `ProgressTreeService`, `GracefulPauseService`, etc.
  — `internal/app/ports.go`) is wired by a later node (`runtime-b02`
  onward); this is an operational "not yet available," not a code defect.
  `version` is the sole exception — it has no service dependency
  (`internal/buildinfo.String()` only) and is fully real.
- **Command tree**: `NewRootCmd` registers all 18 P0 leaf commands named in
  `agents/runtime.md` Part B in one call, split across two files for
  readability — `internal/cli/root.go` (root + all commands except the
  `hook` subtree) and `internal/cli/hook.go` (`hook claude {statusline,
  user-prompt-submit, stop, stop-failure}`, kept separate because it is a
  three-level subtree, not a single command, and had enough of its own
  naming-convention context — see below — to warrant its own file and its
  own package doc paragraph).
- **`cmd/preflight/main.go` is untouched.** Per `agents/runtime.md`
  ("Do not edit `cmd/preflight/main.go`; the contract-integrator and
  foundation roles integrate root wiring. Add command constructors under
  owned paths.") and the Wave 3 task brief, this node only builds
  `internal/cli`'s constructors. `cmd/preflight/main.go` still wires only
  `version` (from foundation-01, Wave 1) and does not yet call
  `cli.NewRootCmd()` — that integration is explicitly out of scope for
  this role and belongs to `contract-integrator`/`foundation` in a later
  step. The DAG's validation command was run against the *existing*
  `cmd/preflight` binary (still `version`-only) to confirm `internal/cli`
  compiles cleanly into the module and does not break the existing build;
  `internal/cli`'s own `--help` behavior (the full P0 tree) is verified
  directly at the package level in `internal/cli/root_test.go`'s
  `TestHelpSucceeds`, since there is no owned binary target yet that wires
  the full tree.
- **Dependency requests**: none. Cobra (`github.com/spf13/cobra`) and
  `internal/buildinfo`/`internal/domain` were already available
  (foundation-01, Bootstrap); no new `go.mod` entry was needed.

## Naming-convention judgment call: kebab-case hook subcommands

`docs/implementation/day1/wave2-analysis/ADR_Recommendations.md` REC-03
documents a real, still-open discrepancy: `Preflight_ADD.md` Appendix E.3
spells Claude Code hook subcommands in PascalCase (e.g. `UserPromptSubmit`,
matching Claude's own hook-event-name casing), while `agents/runtime.md`'s
own P0 command list, this node's DAG validation command
(`docs/implementation/day1/EXECUTION_DAG.md` `runtime-b01`'s row), and the
Day-1 execution plan's demo script all independently use kebab-case
(`user-prompt-submit`). REC-03 explicitly names `runtime-b01`'s real CLI
command tree as the first place this decision becomes real, and recommends
resolving it via ADR before this node, not after — that ADR has not been
authored as of this commit.

This node follows **kebab-case** (`preflight hook claude
user-prompt-submit`, `stop-failure`), for two independent reasons:

1. Per Constitution §2's document priority order, `agents/runtime.md` (a
   role-scoped operational document, tier 4) is the most specific document
   that names this role's actual command surface, and it uses kebab-case
   verbatim. `Preflight_ADD.md` (tier 2) is architecturally senior in
   general, but Constitution §1's "one authoritative document per subject"
   table names no single sole source of truth for CLI subcommand string
   casing specifically, and three independently-authored frozen documents
   converging on kebab-case (vs. one on PascalCase) is itself evidence
   about which spelling the rest of the project actually built against
   (`integrations/claude/hooks.json`, per REC-03, already uses kebab-case
   too).
2. This node's own DAG validation command
   (`go build ./internal/cli/... && preflight --help`) does not
   independently test subcommand casing, but the task brief that assigned
   this node was explicit: use kebab-case, matching agents/runtime.md's own
   P0 list, and document the call rather than silently inventing a third
   answer.

**This is not a resolution of REC-03.** No ADR has been written; `runtime`
has no authority to accept one (Constitution §3.2 — only
`contract-integrator` accepts ADRs). If `Preflight_ADD.md` Appendix E.3 is
later confirmed as the intended casing via an accepted ADR, the fix is
mechanical: rename the four `Use` strings in
`internal/cli/hook.go`'s `newHookClaudeCmd` and update
`root_test.go`/`errors_test.go`'s path tables to match — no other file is
affected, since every stub command is otherwise identical regardless of
its `Use` string. Flagging this explicitly so a future wave doesn't have
to rediscover it: **REC-03 should still be raised as a real ADR** even
though this node made a documented, non-blocking judgment call to proceed
under kebab-case in the meantime.

## Node log

```yaml
node: runtime-b01
status: completed
artifacts:
  - internal/cli/doc.go
  - internal/cli/errors.go
  - internal/cli/errors_test.go
  - internal/cli/root.go
  - internal/cli/root_test.go
  - internal/cli/hook.go
validation:
  - "gofmt -l internal/cli   # empty output"
  - "go build ./internal/cli/...   # OK"
  - "go vet ./internal/cli/...   # OK"
  - "go test ./internal/cli/... -race -v   # all PASS"
  - "go build -o <tmp> ./cmd/preflight && <tmp> --help   # OK (existing version-only binary; unaffected by this package)"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: a6a3eaa
next_action: runtime-b02 (App wiring) — blocked/not started this wave per explicit instruction to stop once runtime-b01 is Validated; Part A (internal/pause/**, internal/scheduler/**) also not started this wave, out of scope per task brief
assumptions:
  - "Kebab-case for `preflight hook claude ...` subcommands — see the
    dedicated section above. Documented, not silent; REC-03 remains open
    and should still be resolved by an accepted ADR."
  - "Every command below `version` is an honest stub returning
    domain.Error{Code: ErrCodeUnavailable, Retryable: true} rather than
    any real behavior, per explicit task instruction: none of
    orchestrator/evaluation/checkpoint/pause services exist yet this wave,
    and the DAG's own validation command
    (`go build ./internal/cli/... && preflight --help`) only requires
    `go build` and `--help` to work, not working commands."
  - "internal/cli/root.go groups most P0 leaf commands (version, init,
    evaluate, decision, checkpoint, progress, state, pause, resume,
    scheduler, status, doctor) into a single file rather than one file per
    command. The DAG estimated 6 files/350 LOC for runtime-b01; one file
    per command (13 top-level constructors) would have produced far more
    files than that estimate for what is, this wave, structurally
    identical boilerplate per command (a Use/Short/RunE stub). `hook` was
    split out on its own because it is a three-level subtree with its own
    naming-convention discussion, which justified a dedicated file and
    package-doc paragraph the other commands don't need yet. This may be
    resplit into per-domain files (e.g. a checkpoint.go, a pause.go) once
    real business logic lands behind each command in runtime-b02 onward
    and the single-file grouping stops being the natural shape."
  - "NewRootCmd is exported (capital N) unlike foundation-01's
    unexported newRootCmd in cmd/preflight/main.go, because
    internal/cli is a separate package a future root-wiring step needs to
    import; cmd/preflight/main.go's own newRootCmd stays package-private
    since nothing outside that package needs it. Both conventions coexist
    correctly per Go visibility rules; this is not a contradiction of
    foundation's established pattern, just the same pattern applied at a
    package boundary that didn't exist yet when foundation-01 was written."
blockers: []
```

---

# Wave 4

Branch: `day1/runtime`, synced from `main` (Wave 3 integration state,
`664436d`) via fast-forward before any Wave 4 work — required so
foundation-06's migration engine + 0001-0004 core-schema files exist on
this branch. Assigned nodes, executed sequentially: `runtime-a01`
(Part A migrations 0050-0059), then `runtime-b02` (app wiring).

## runtime-a01 — Graceful Pause/Scheduler core migrations

### What shipped

- `internal/storage/sqlite/migrations/0050_pause_records.sql` —
  `pause_records` + `idx_pause_status` (ADD §12.2/§12.3).
- `internal/storage/sqlite/migrations/0051_wake_jobs.sql` — `wake_jobs` +
  `idx_wake_jobs_due`, including `UNIQUE(pause_id, job_kind)` (the
  schema-level exactly-once-wake anchor) and the column set the ADD §12.4
  lease query requires (`status`, `run_after`, `lease_owner`,
  `lease_expires_at`, `attempts`, `max_attempts`).
- `internal/storage/sqlite/migrations/0052_resume_attempts.sql` —
  `resume_attempts` audit-trail table.
- `internal/storage/sqlite/migrations_0050_pause_test.go` — this range's
  tests (all named `TestMigration0050_*` so the DAG's validation command
  `go test ./internal/storage/sqlite/... -run Migration0050` selects
  exactly these): embedded-file loading, apply-from-empty (tables +
  §12.3 indexes present), idempotent re-apply, FK enforcement into
  foundation's `tasks`/`provider_sessions` (reject unknown ids; full
  repository → worktree → task → pause cascade), `runway_forecast_id`
  NOT NULL, wake-job cascade + unique-kind, resume-attempt
  survives-wake-job (SET NULL) but not pause (CASCADE).

### Documented deviation from ADD §12.2 canonical FKs (needs contract-integrator's eye; mirrors the 0004_tasks.sql precedent)

ADD §12.2 declares `pause_records.turn_id/runway_forecast_id/
state_checkpoint_id/repository_checkpoint_id` as `REFERENCES` into
`turns` (claude-provider 0010-0019), `runway_forecasts` (predictor
0040-0049), `state_checkpoints` (checkpoint 0020-0029), and
`repository_checkpoints` (checkpoint 0030-0039). None of those migration
files exist yet. SQLite accepts forward FK declarations at CREATE time,
but with `PRAGMA foreign_keys = ON` it resolves *every* parent table on
*any* DML touching the child — **including cascade processing initiated
from `repositories`/`worktrees`/`tasks` deletes**. Empirically (first
draft of this node used the canonical FKs): foundation's own
`TestCoreMigrations_ForeignKeys_*` tests immediately failed with
`no such table: main.repository_checkpoints` on a plain
`DELETE FROM repositories`, i.e. the forward FKs would have poisoned
unrelated DML repo-wide and hard-blocked `runtime-a02` (pause state
machine, DAG-scheduled against runtime-a01 alone) on three other roles'
ranges.

Resolution: these four columns ship as plain `TEXT` pointers, exactly the
precedent foundation-06 set for `tasks.active_node_id` → `progress_nodes`
in `0004_tasks.sql`. FKs that *can* be enforced today (into `tasks`,
`provider_sessions`, and within this range `wake_jobs`/`resume_attempts` →
`pause_records`) are declared and tested. **Proposal to
contract-integrator:** once 0010-0049 have all landed, either (a) accept
the plain-pointer precedent permanently (consistent with 0004), or (b)
assign runtime a follow-up migration in its own range (0053+) that
recreates `pause_records` with the canonical FK set via SQLite's
copy-drop-rename pattern. Either way the decision belongs above this role;
this node did not silently pick (a) forever — it picked the only option
that keeps the repo's DML working today, and flagged the choice here.

### CHANGE REQUEST → foundation (Constitution §4.4 — not edited by runtime)

Three assertions in `internal/storage/sqlite/migrate_test.go`
(foundation's file) are over-constrained and fail the moment *any* later
role's migration range lands — which contradicts `migrate.go`'s own
design comment ("later roles' migrations … are picked up automatically
once present, with no change needed here"):

1. `TestAllMigrations_LoadsCoreSchemaFiles` asserts
   `len(migrations) == 4` — should filter to foundation's own 0000-0009
   range (the way `TestMigration0050_AllMigrationsIncludesPauseRange`
   filters to 0050-0059).
2. `TestCoreMigrations_FromEmptyDatabase` asserts `CurrentVersion == 4` —
   should assert `>= 4` or derive the expectation from `AllMigrations()`.
3. `TestCoreMigrations_ReopenFromFile_AppliesOnce` asserts
   `CurrentVersion == 4` — same fix.

Until foundation applies this mechanical fix, `go test
./internal/storage/sqlite/...` (full package, no `-run` filter) reports
these three failures on this branch. **No runtime-owned test fails**, and
the failures are assertion staleness, not behavior: foundation's
FK/cascade/unique behavioral tests all still pass against the combined
0001-0052 schema. Per Constitution §4.4 runtime did not edit the file and
did not wait idle; flagging here for foundation + the merge integrator.

### Node log

```yaml
node: runtime-a01
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0050_pause_records.sql
  - internal/storage/sqlite/migrations/0051_wake_jobs.sql
  - internal/storage/sqlite/migrations/0052_resume_attempts.sql
  - internal/storage/sqlite/migrations_0050_pause_test.go
validation:
  - "go test ./internal/storage/sqlite/... -run Migration0050   # all 6 PASS"
  - "gofmt -l internal/storage/sqlite   # empty"
next_action: runtime-a02 (pause state machine) — NOT this wave, per explicit scope
assumptions:
  - "Plain TEXT (no FK) for pause_records' four references into
    not-yet-landed migration ranges — see the deviation section above;
    decision (a)-vs-(b) escalated to contract-integrator."
  - "migrations_0050_pause_test.go lives in internal/storage/sqlite/
    (foundation's directory) because the DAG's validation command
    requires tests selectable there and migration SQL is not testable
    from any runtime-owned Go package; the file is named with this
    range's 0050 prefix and contains only runtime-range tests. If
    contract-integrator prefers a different ownership carve-out
    (e.g. adding the filename to runtime's exclusive paths), that is a
    one-line agents/runtime.md change — requested here rather than
    self-granted."
blockers:
  - "foundation's migrate_test.go stale exact-count assertions (see
    CHANGE REQUEST above) — does not block this node's validation
    command, but blocks a fully green `go test ./...` until foundation's
    3-line fix lands."
```

## runtime-b02 — App wiring (in-process composition layer)

### What shipped

- `internal/app/wiring/wiring.go` — the composition container:
  `Services` (one field per frozen service interface: `Evaluation`,
  `ProgressTree`, `StateCheckpoint`, `GracefulPause`,
  `RepositoryCheckpoint` — `internal/app/ports.go`), `New(Services)
  (*App, error)` (fail-closed construction: any nil field returns the
  frozen `domain.Error` with `ErrCodeValidation`, `Retryable: false`, and
  `Details["missing_services"]` naming every hole — a composition bug
  surfaces at startup, not as a nil-pointer panic in whichever handler
  first hits it), one accessor per service, and `App.RootCmd()` — the
  wiring→CLI seam that returns `internal/cli.NewRootCmd()`'s tree today
  and is where runtime-b03+ threads real services into individual command
  handlers.
- `internal/testutil/fakes/` — first population of this directory
  (agents/runtime.md: "coordinate with the qa role"): `doc.go`
  (pattern contract), `unconfigured.go` (shared nil-Func behavior), and
  one file per frozen service interface (`evaluation.go`,
  `progresstree.go`, `statecheckpoint.go`, `gracefulpause.go`,
  `repositorycheckpoint.go`). Pattern: `Fake<Interface>` struct with one
  optional `<Method>Func` field per method; compile-time
  `var _ app.X = (*FakeX)(nil)` assertions; calling an unconfigured
  method fails loud with `domain.Error{Code: ErrCodeUnavailable,
  Retryable: false, Details: {fake, method}}` rather than silently
  returning zero values. No call recording/counting machinery — tests
  needing it build it in their own closures (Constitution §7 rule 10:
  no abstractions this milestone doesn't need).
- `internal/app/wiring/wiring_test.go` — validates: construction with
  all-fakes succeeds; each single missing service fails closed with the
  right code/retryability/details; all-missing lists all five; accessors
  return the injected instances (identity); calls through the container
  reach the configured fake closure with arguments intact (pass-through
  plumbing, no re-interpretation); unconfigured fake methods fail loud
  through the container; `RootCmd()` yields the full 13-top-level-command
  P0 tree from runtime-b01.

### Handoff notes

- **For qa**: `internal/testutil/fakes` is intentionally minimal and
  additive-friendly. If integration tests need recording fakes, add
  behavior in test-local closures first; only promote shared machinery
  into this package if several suites independently need the same thing.
- **For contract-integrator/foundation (root wiring)**: the intended
  binary composition is `wiring.New(Services{...real impls...})` followed
  by `app.RootCmd()`. `cmd/preflight/main.go` remains untouched by this
  role per agents/runtime.md.
- **For runtime-b03+ (this role)**: replace `RootCmd`'s direct
  `cli.NewRootCmd()` call by passing `a.services`' interfaces into the
  cli constructors as they gain real handlers; callers that already go
  through `App.RootCmd()` see no change.

### Node log

```yaml
node: runtime-b02
status: completed
artifacts:
  - internal/app/wiring/wiring.go
  - internal/app/wiring/wiring_test.go
  - internal/testutil/fakes/doc.go
  - internal/testutil/fakes/unconfigured.go
  - internal/testutil/fakes/evaluation.go
  - internal/testutil/fakes/progresstree.go
  - internal/testutil/fakes/statecheckpoint.go
  - internal/testutil/fakes/gracefulpause.go
  - internal/testutil/fakes/repositorycheckpoint.go
validation:
  - "go test ./internal/app/wiring/...   # all PASS (DAG validation command)"
  - "go test ./internal/cli/... ./internal/app/wiring/... -race   # all PASS"
  - "gofmt -l internal/app/wiring internal/testutil   # empty"
  - "go vet ./internal/app/wiring/... ./internal/testutil/...   # OK"
  - "golangci-lint run ./...   # 0 issues, whole repo"
next_action: runtime-b03+ (real handler logic) and runtime-a02 (pause state machine) — NOT this wave, per explicit scope
assumptions:
  - "TxRunner and the ADR-041 predictor pipeline stages
    (ScopeEstimator/TokenForecaster/QuotaForecaster/RiskCombiner) are NOT
    fields of wiring.Services yet: the CLI's P0 commands consume the five
    high-level services only; pipeline stages are wired inside predictor's
    own EvaluationService implementation, and storage transactions are a
    per-service concern. Adding a field later is additive and
    non-breaking; adding it now would be speculative structure
    (Constitution §7 rule 10)."
  - "App.RootCmd() returning the still-stubbed runtime-b01 tree is the
    correct b02 shape: the DAG's validation command tests wiring
    construction, not handler behavior, and handler logic is explicitly
    runtime-b03+ scope."
blockers: []
```

---

# Wave 5

Branch: `day1/runtime`, synced from `main` (Wave 4 integration state,
`5470e4d`) via fast-forward merge before any Wave 5 work — clean, no
conflicts (this role only owns its own paths). Brings in
`foundation`'s migrate_test.go range-scoped-assertion fix, `checkpoint`'s
Part A/B core migrations (0020-0039), `predictor`'s quota forecaster
(`internal/predictor/quota`), and `claude-provider`'s telemetry event
store (`internal/telemetry/claude/store.go`).

Assigned nodes, executed sequentially with independent validate+commit
after each: `runtime-a02` (pause state transition validator) ->
`runtime-a06` (durable scheduler lease) -> `runtime-b03` (Evaluate
pipeline) -> `runtime-b04` (hook command handlers) -> `runtime-b05`
(checkpoint create orchestration) -> `runtime-b08` (status/doctor
commands) — Part A before Part B per the task brief, since both of
Part A's nodes were marked High risk (state-machine and concurrency
correctness) and Part B's four nodes are comparatively lower-risk
plumbing built on top of runtime-b02's existing wiring container.

## runtime-a02 — Pause state transition validator

### What shipped

- `internal/pause/doc.go` — package doc reconciling three documents'
  state-name vocabularies onto the twelve frozen `domain.PauseStatus`
  wire strings: agents/runtime.md's "Required state path" prose,
  `Preflight_ADD.md` §20.5's mermaid diagram, and the frozen enum
  itself (`internal/domain/status.go`, verified by
  `CONTRACT_FREEZE.md`). Several of the prose documents' named steps
  (`observing`/`Active`, `safe_point_reached`, `persisting`, `wake_due`,
  `EmergencyInterrupt`, `MinimalCheckpoint`) fold onto one frozen state
  each — documented explicitly rather than silently picked.
- `internal/pause/statemachine.go` — the explicit valid-transition
  table (P0 deliverable 1): `Event` vocabulary, `transitionTable` (every
  edge keyed by `(from, event)`), `terminalStates`, `Validate`/`Apply`/
  `IsTerminal`/`IsKnownState`/`ValidEvents`, and a `*TransitionError`
  type distinguishing unknown-state / terminal-state / no-edge
  rejections.
- `internal/pause/statemachine_test.go` — 17 tests covering the full
  nominal path, ADD §17.6's emergency skip-ahead, ADD §20.15's
  checkpoint-failure fail-closed rule, and every Part A required test
  provable at the state-machine level alone: unsafe-quota-reschedules,
  repo-overlap-blocks, cancel-wins-race-with-wake, provider-interrupt-
  failure-recoverable, plus terminal-state/unknown-state/invalid-edge
  rejection and full table-completeness structural checks.

### Node log

```yaml
node: runtime-a02
status: completed
artifacts:
  - internal/pause/doc.go
  - internal/pause/statemachine.go
  - internal/pause/statemachine_test.go
validation:
  - "gofmt -l internal/pause   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/...   # OK"
  - "go test ./internal/pause/... -run StateTransition -race -v   # 17/17 PASS"
  - "go test ./internal/pause/... -race -v   # all PASS (same 17 — StateTransition is the whole package this node)"
commit: 7b125fc
next_action: runtime-a06
assumptions:
  - "State-name reconciliation across agents/runtime.md prose, ADD
    §20.5's diagram, and the frozen 12-value domain.PauseStatus enum —
    documented in doc.go, not silently picked. No new PauseStatus value
    was invented (Constitution §6 rule 4)."
  - "Interrupting has no cancel edge (a provider interrupt signal
    already in flight cannot be cancelled out from under itself) —
    a deliberate narrowing, tested explicitly
    (TestStateTransition_InterruptingHasNoCancelEdge) so a future
    reader doesn't have to reverse-engineer the omission from the table."
blockers: []
```

## runtime-a06 — Durable scheduler lease

### What shipped

- `internal/scheduler/doc.go` — package doc mapping ADD §12.4's lease-
  claim transaction concept and §12.7's lease/retry parameters onto
  this store's design.
- `internal/scheduler/lease.go` — `Store` with `Schedule`/`Get`/`Claim`/
  `Renew`/`Complete`/`Fail`/`ReclaimExpired` against the `wake_jobs`
  table (runtime-a01's migration 0051). `Claim` reserves a single
  physical `*sql.Conn` (not a pooled `*sql.Tx`) and issues `BEGIN
  IMMEDIATE`/`COMMIT`/`ROLLBACK` directly on it, matching ADD §12.4's
  literal locking intent. `Claim`'s predicate is deliberately widened
  beyond ADD §12.4's literal `status='scheduled'` text to also match a
  `leased` row whose lease has expired, so "expired lease reclaimed"
  holds directly against `Claim`, not only via the separate
  `ReclaimExpired` restart-recovery sweep (ADD §28.3 step 2, which
  still exists for startup diagnostics).
- `internal/scheduler/lease_test.go` — 17 tests: schedule+claim,
  lease renewal/complete/fail-with-backoff/fail-exhausts-max-attempts
  (each including a wrong-owner-conflict case), expired lease reclaimed
  (via bare `Claim` and via explicit `ReclaimExpired`), validation, and
  the two concurrency proofs required by the DAG's stated risk:
  `TestLease_ConcurrentWorkersYieldOneClaim` (many goroutines racing one
  job, exactly one wins) and
  `TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce` (N jobs, M
  workers, every job claimed exactly once) — both run under `-race`.

### Two real bugs caught and fixed by this node's own tests, before commit

1. **Self-deadlock**: `Claim`'s original implementation re-fetched the
   newly-claimed job through the pooled `*sql.DB` (`s.Get`) while still
   holding its own reserved `*sql.Conn` open for the transaction. Under
   full pool saturation (many concurrent `Claim` callers,
   `internal/storage/sqlite.DB` caps the pool at 8 connections), the
   re-fetch's connection request could never be satisfied — every
   goroutine ended up blocked in `database/sql`'s connection-wait queue
   simultaneously. `TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce`
   hung indefinitely on first run (had to be killed via `kill -9` after
   ~4 minutes real time / <1s CPU time, the signature of a wait-queue
   deadlock, not a spin). Fixed by adding a `getJob(ctx, Querier, id)`
   helper `Claim` calls against its OWN reserved connection, before
   `COMMIT`, instead of going back to the pool.
2. **Expired-lease blind spot**: `Claim`'s original SELECT only matched
   `status = 'scheduled'`, so a `leased`-but-expired row (exactly the
   "duplicate workers / expired lease" scenario) was invisible to
   `Claim` until a separate `ReclaimExpired` call reset it first —
   `TestLease_ExpiredLeaseReclaimedByAnotherWorker` failed on first run
   (`second Claim: Found = false, want true`). Fixed by widening the
   SELECT/UPDATE predicate to also match a leased row whose
   `lease_expires_at` has passed (see "What shipped" above).

Both bugs were caught by this node's own required tests before any
commit was made — not discovered later by a sibling role or at
integration time.

### Node log

```yaml
node: runtime-a06
status: completed
artifacts:
  - internal/scheduler/doc.go
  - internal/scheduler/lease.go
  - internal/scheduler/lease_test.go
validation:
  - "gofmt -l internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/scheduler/...   # OK"
  - "go test ./internal/scheduler/... -run Lease -race -v   # 17/17 PASS"
  - "go test ./internal/scheduler/... -race -count=3   # stable across 3 runs, no flakes"
commit: d5948d9
next_action: runtime-b03
assumptions:
  - "Claim's SELECT/UPDATE predicate widened beyond ADD §12.4's literal
    text to also match expired-leased rows (not just scheduled ones) —
    documented deviation, justified by the required test's own name
    ('expired lease reclaimed') and by ADD §12.4 itself being labeled a
    concept (\"概念\"), not verbatim-mandatory SQL."
  - "wake_jobs.status values (scheduled/leased/done/dead) are this
    package's own vocabulary, per 0051_wake_jobs.sql's header leaving
    the column deliberately un-enumerated at the schema level for the
    owning role (this one) to define."
blockers: []
```

## runtime-b03 — Evaluate pipeline

### What shipped

- `internal/orchestrator/doc.go` — package doc scoping this node to
  agents/runtime.md Part B pipeline steps 1-6, and explaining why no new
  repository/worktree/session resolver port was invented (no frozen
  port exists yet for that; `EvaluateRequest` takes already-resolved
  IDs, the realistic shape for a hook handler or CLI command that
  already has them).
- `internal/orchestrator/evaluate.go` — `Evaluate(ctx, Deps,
  EvaluateRequest) (EvaluateResult, error)`: loads the Progress Tree
  (when a `TaskID` is given), loads usage observations via a narrow
  local `UsageObservationLoader` interface, snapshots Git state via
  `internal/gitx` (checkpoint role's public Git plumbing, consumed not
  owned), calls `app.EvaluationService.EvaluateTurn` then `.Decide`.
  Fail-open on the three operational-observation steps (Progress Tree/
  observations/Git snapshot — degrades `EvaluateResult`'s `Has*` flags
  without aborting); fail-closed on `EvaluateTurn`/`Decide` themselves
  (the pipeline's actual purpose, errors propagate as-is).
- `internal/orchestrator/evaluate_test.go` — 16 tests covering the
  happy path (both service calls made, in order), validation, nil-
  service fail-closed, both fail-closed propagation cases, and all
  three fail-open degradation cases (each with its own "error still
  degrades, doesn't abort" test plus a "value loads when present" test).

### Node log

```yaml
node: runtime-b03
status: completed
artifacts:
  - internal/orchestrator/doc.go
  - internal/orchestrator/evaluate.go
  - internal/orchestrator/evaluate_test.go
validation:
  - "gofmt -l internal/orchestrator   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/...   # OK"
  - "go test ./internal/orchestrator/... -run Evaluate -race -v   # 16/16 PASS"
commit: 38dc881
next_action: runtime-b04
assumptions:
  - "No new resolver port invented for repository/worktree/session
    resolution (Constitution §7 rule 10) — EvaluateRequest takes
    already-resolved IDs directly; documented in doc.go."
  - "internal/gitx (checkpoint role's Git plumbing) is consumed
    directly as a public package, not faked — it is not one of the
    frozen app ports this wave's fakes cover, and it already has its
    own real, tested implementation from checkpoint's earlier waves."
blockers: []
```

## runtime-b04 — Hook command handlers

### What shipped

- `internal/orchestrator/hooks.go` — `HandleStatusLine`/
  `HandleUserPromptSubmit`/`HandleStop`/`HandleStopFailure`: each
  parses via claude-provider-04's real, already-integrated parsers
  (`internal/providers/claude`, `internal/hooks/claude`), normalizes via
  claude-provider-04's real `Normalizer` (`internal/telemetry/claude`),
  best-effort persists via an injectable, nil-safe `EventPersister`, and
  (`HandleUserPromptSubmit` only) runs the evaluate pipeline
  (runtime-b03's collaborators) to render ADD §22.3's block/allow
  response shape. Every handler is fail-open on malformed stdin.
- `internal/orchestrator/hooks_test.go` — 16 tests against the real
  fixture files under `testdata/provider-events/claude/**`, including
  `TestHookHandlers_UserPromptSubmit_NeverSeesRawPromptText`, which
  asserts the hash `EvaluateTurn` receives is a 64-char hex digest, not
  the fixture's raw prompt text.
- `internal/cli/hook.go` — added exported `NewHookClaudeCmd(HookDeps)`,
  the real command tree, alongside the existing package-private stub
  tree (renamed `newHookClaudeCmd` -> `newHookClaudeStubCmd`, still used
  by standalone `NewRootCmd()`).
- `internal/app/wiring/wiring.go` — `RootCmd()` now replaces the stub
  `hook` subtree with `NewHookClaudeCmd`'s real one, built from a new
  optional `Services.Hooks` field (`HookSupport`: `Clock`/`IDs`/
  `Persister`/`TxRunner`) that falls back to real `domain.Clock`/
  `domain.IDGenerator` when left unset.

### Node log

```yaml
node: runtime-b04
status: completed
artifacts:
  - internal/orchestrator/hooks.go
  - internal/orchestrator/hooks_test.go
  - internal/cli/hook.go (modified)
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/app/wiring   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/...   # OK"
  - "go test ./internal/orchestrator/... -run HookHandlers -race -v   # 16/16 PASS"
  - "go test ./internal/cli/... ./internal/app/wiring/... -race   # all PASS"
commit: 624b27a
next_action: runtime-b05
assumptions:
  - "claude-provider-04's parsers/Normalizer are called directly (real,
    not faked), per the task brief's explicit instruction that they are
    already integrated."
  - "HookDeps.Evaluation is app.EvaluationService (fake this wave, see
    Fakes Used section below) — same dependency runtime-b03 already
    tracks, not a new gap."
  - "ADD §22.6's status-line compose-with-previous-command installer
    mechanism is out of scope this wave — HandleStatusLine
    normalizes+persists only; no internal/statusline package exists
    yet to own the composition step."
blockers: []
```

## runtime-b05 — Checkpoint create orchestration

### What shipped

- `internal/orchestrator/checkpoint.go` — `CheckpointCreate(ctx, Deps,
  Request) (Result, error)`: calls `app.StateCheckpointService.Create`
  THEN `app.RepositoryCheckpointService.Create`, in that order, never
  the reverse. Fails closed on either nil dependency (checked up front,
  before any call) and propagates either service's error as-is; a State
  failure means Repository is never even attempted.
- `internal/orchestrator/checkpoint_test.go` — 6 tests, the two most
  important being `TestCheckpointCreate_CallsStateBeforeRepository`
  (records actual call order through both fakes) and
  `TestCheckpointCreate_StateFailureNeverCallsRepository` (proves
  Repository is never reached at all when State fails — not called-
  then-ignored, never reached).
- `internal/cli/checkpoint.go` — `NewCheckpointCmd(CheckpointCreateDeps)`,
  reading `--task-id`/`--worktree-id` flags (no resolver port exists,
  same documented scope boundary as runtime-b03).
- `internal/app/wiring/wiring.go` — added a `replaceSubcommand` helper
  (refactored out of runtime-b04's inline hook-subtree-replacement loop
  so both nodes share one mechanism) and wired `checkpoint` through it.

### Node log

```yaml
node: runtime-b05
status: completed
artifacts:
  - internal/orchestrator/checkpoint.go
  - internal/orchestrator/checkpoint_test.go
  - internal/cli/checkpoint.go
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/app/wiring   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/...   # OK"
  - "go test ./internal/orchestrator/... -run CheckpointCreate -race -v   # 6/6 PASS"
  - "go test ./internal/cli/... ./internal/app/wiring/... ./internal/orchestrator/... -race   # all PASS"
commit: aa7130e
next_action: runtime-b08
assumptions:
  - "Both StateCheckpoint and RepositoryCheckpoint wired against fakes
    this wave (checkpoint-a04/b04 not integrated yet, per the task
    brief's explicit instruction to use fakes for both regardless of
    checkpoint-b04's in-progress sibling-branch status this wave)."
blockers: []
```

## runtime-b08 — Status/Doctor commands

### What shipped

- `internal/orchestrator/diagnostics.go` — `Status(ctx, StatusDeps,
  StatusRequest) (StatusResult, error)`: best-effort session/Progress-
  Tree summary, fail-open on a missing/failing ProgressTree dependency.
  No pause-status field: the frozen `GracefulPauseService` port has no
  passive read query (only state-transition actions), so a read command
  must not call one just to render a summary. `Doctor(ctx, DoctorDeps)
  DoctorResult`: DB reachable (`Conn().PingContext`) + migrated
  (`CurrentVersion > 0`), config loadable (narrow `ConfigLoader`
  interface), required directories present+writable (probed via a
  create-then-remove temp file, verified to leave no residue). Every
  check is independently optional (`CheckSkipped` when unwired); overall
  `Healthy` is false only if some check actually `CheckFail`s.
- `internal/orchestrator/diagnostics_test.go` — 12 tests, including
  `TestDoctor_DoesNotMutateFilesystem` (directory entry count unchanged
  before/after a Doctor run) and a real, migrated `*sqlite.DB` test
  proving the DB check's OK path against the actual embedded migration
  set, not just a fake.
- `internal/cli/diagnostics.go` — `NewStatusCmd`/`NewDoctorCmd`, both
  always exiting 0 with a stable schema-versioned JSON body regardless
  of whether individual checks failed (a failing doctor check is
  content in the report, not a command-execution error).
- `internal/app/wiring/wiring.go` — added `Services.Diagnostics`
  (`DiagnosticsSupport`: `DB`/`Config`/`RequiredDirs`, all optional) and
  wired both commands through `replaceSubcommand`. `*sqlite.DB`
  satisfies `orchestrator.DBPinger` structurally with no new dependency
  from `orchestrator` onto the `sqlite` package (verified via a
  throwaway compile-time assertion during development, not committed).

### Node log

```yaml
node: runtime-b08
status: completed
artifacts:
  - internal/orchestrator/diagnostics.go
  - internal/orchestrator/diagnostics_test.go
  - internal/cli/diagnostics.go
  - internal/cli/diagnostics_test.go
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/app/wiring   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/...   # OK"
  - "go test ./internal/cli/... -run 'Status|Doctor' -race -v   # 6/6 PASS"
  - "go test ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/... -race   # all PASS"
commit: deaf094
next_action: none — all six Wave 5 nodes complete; runtime-a03/a04/a05/a07/a08/a09/a10/a11/b06/b07/b09/b10 remain, out of scope this wave
assumptions:
  - "No fakes tracked for follow-up on this node: DBPinger/ConfigLoader
    are narrow interfaces this node owns outright, satisfied directly
    by *sqlite.DB and (once wiring supplies one) a real config loader —
    no sibling-role service dependency to swap later."
blockers: []
```

## Post-node whole-repo lint sweep

After all six nodes, `golangci-lint run ./...` found 11 issues, all in
this wave's own new files (errcheck x1, errorlint x5, nilerr x3,
staticcheck x1, unused x1). Fixed all 11 in a dedicated commit
(`90078c3`) separate from the six node commits, per the same
never-batch-unrelated-work discipline — this commit is cleanup of
already-committed work, not a seventh node. `golangci-lint run ./...`
now reports 0 issues, whole repo; `go test ./... -race` is fully green
across every package, including `internal/storage/sqlite` (confirming
Wave 4's foundation migrate_test.go change request was resolved before
this wave began, as the task brief stated).

## Fakes used this wave (tracked for integration)

Every one of these was explicitly authorized by the task brief as a
soft/fake-able dependency for this wave; each is called out here so a
later integration pass can find every place that still needs a real
implementation swapped in.

| Node | Fake used in place of | Where |
|---|---|---|
| runtime-b03 | `predictor-08`/`predictor-09` (Policy/Evaluation persistence) — `app.EvaluationService` | `internal/orchestrator/evaluate.go`'s `Deps.Evaluation`, wired to `fakes.FakeEvaluationService` in tests and (until predictor lands) in `wiring` |
| runtime-b04 | Same `app.EvaluationService` fake (UserPromptSubmit's block/allow decision) | `internal/orchestrator/hooks.go`'s `HookDeps.Evaluation` |
| runtime-b05 | `checkpoint-a04` (real `CompleteNode`/state-checkpoint atomic protocol) — `app.StateCheckpointService` | `internal/orchestrator/checkpoint.go`'s `Deps.StateCheckpoint` |
| runtime-b05 | `checkpoint-b04` (repository checkpoint; being built this same wave by a sibling teammate, not yet merged) — `app.RepositoryCheckpointService` | `internal/orchestrator/checkpoint.go`'s `Deps.RepositoryCheckpoint` |

Explicitly NOT fake this wave, per the task brief and verified directly
in this artifact's node logs:

- `claude-provider-04`'s hook payload parsers and Normalizer (`internal/
  providers/claude`, `internal/hooks/claude`, `internal/telemetry/
  claude`) — real, already integrated (Wave 2), called directly by
  `runtime-b04`'s hook handlers.
- `internal/gitx` (checkpoint role's Git plumbing, consumed by
  `runtime-b03`'s Evaluate pipeline) — real, already integrated.
- `runtime-a02`/`runtime-a06` (Part A) have no sibling-role
  dependencies at all — both are pure, self-contained state-machine/
  storage-layer nodes with nothing to fake.
- `runtime-b08`'s `DBPinger`/`ConfigLoader` — narrow interfaces this
  node owns outright; nothing to fake.

# Wave 6

Branch: `day1/runtime`, synced from `main` via `git fetch origin && git
merge origin/main` (fast-forward, clean — no conflicts, this role only
owns its own paths) before any Wave 6 work. Brings in Wave 5's integrated
state, including `checkpoint`'s real `checkpoint-b04` (repository
checkpoint) landing on `main` and `predictor`'s risk combiner
(`internal/predictor/risk`). Per the task brief, `runtime-b05`'s existing
internal fake for `checkpoint-b04` was deliberately left as-is this wave
(not this wave's assignment to swap) — noted here, not silently changed.

Assigned nodes, all Part A, executed sequentially with independent
validate+commit after each: `runtime-a03` (Observe debounce/hysteresis) ->
`runtime-a04` (RequestPause idempotency + safe-point coordinator) ->
`runtime-a07` (restart recovery of overdue/leased jobs). `runtime-a03`/
`runtime-a04` both build directly on `runtime-a02`'s state machine;
`runtime-a07` builds on `runtime-a06`'s scheduler lease store. No Part B
work this wave.

## runtime-a03 — Observe debounce/hysteresis

### What shipped

- `internal/pause/observe.go` — `Observer` (per-`domain.SessionID`
  debounce/hysteresis state) and `Observe(sessionID, forecast,
  observedAt) ObserveDecision`, implementing ADD §17.6/§20.2's exact
  parameters as an `ObserveConfig` (threshold 0.80, min interval 5s,
  quota freshness 30s, reset band 0.70) plus the independent ADD §17.6
  emergency trigger (used>=98% or P50 time-to-limit<=60s), each with its
  own `TriggerReason` (`TriggerReasonCalibrated` /
  `TriggerReasonEmergency`) so the two paths are distinguishable per the
  "day-one realism" requirement. Emergency is checked first,
  unconditionally, and does not consume/clear an in-progress calibrated
  arm. Hysteresis reset requires RiskScore to actually fall below 0.70 —
  an in-between non-qualifying sample does not clear the arm, so two
  qualifying samples separated by noise still correctly trigger.
- `internal/pause/observe_test.go` — 13 tests: the two required tests
  verbatim (two qualifying observations trigger; one spike does not)
  plus too-soon-stays-armed, hysteresis-band, stale-quota-sample,
  missing-`QuotaObservedAt`-fails-closed, uncalibrated-never-qualifies-
  calibrated-path, both emergency branches, emergency-does-not-consume-
  arm, per-session isolation, and `Reset`.

### Node log

```yaml
node: runtime-a03
status: completed
artifacts:
  - internal/pause/observe.go
  - internal/pause/observe_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/pause/... -run Observe -v   # 13/13 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -v   # all PASS"
commit: 8ff0190
next_action: runtime-a04
assumptions:
  - "TriggerReason is this package's own closed vocabulary (mirrors
    Event), not part of any frozen contract or predictor's
    domain.ReasonCode list (which explains a risk score's composition,
    not a pause trigger's decision) — no frozen enum was extended or
    reused out of scope."
  - "Emergency's 'limit reached' branch (ADD §17.6) is modeled via
    CurrentUsedPercent/EstimatedTimeToLimitP50Seconds only, since
    domain.RunwayForecast has no separate boolean field for a
    provider-reported hard limit; a future node wiring the real
    predictor-06 output can set CurrentUsedPercent to 100 (or a
    provider-supplied percent) to represent that case without an
    Observer signature change."
  - "Observer is per-process, keyed by domain.SessionID, with no
    persistence of its own — restart behavior for in-flight (armed but
    not yet fired) debounce state is out of scope for this node (a
    single missed arm after a crash just requires one more qualifying
    sample post-restart, which is a safe, conservative default, not a
    correctness gap)."
blockers: []
```

## runtime-a04 — RequestPause idempotency + safe-point coordinator

### What shipped

- `internal/pause/requestpause.go` — `PauseKey` (the natural
  `(TaskID, SessionID)` idempotency key — `pause_records` has no
  separate caller-supplied idempotency-key column, so the natural key
  serves the same role CONTRACT_FREEZE.md describes for
  `CompleteNodeRequest.IdempotencyKey`), a narrow internal `PauseStore`
  port (`FindActiveByKey`/`Insert`) deliberately declared in this
  package rather than `internal/app/ports.go` (an internal seam behind
  the already-frozen `GracefulPauseService` boundary, not a new
  cross-component contract), `RequestPause(ctx, store, ids, req)
  (RequestPauseResult, error)`, and `MemStore` (an in-memory reference/
  test `PauseStore` — the DAG's own note says no concrete store is
  required to begin this node; `runtime-a05` is expected to add a
  SQLite-backed `PauseStore` against the same interface).
- `internal/pause/safepoint.go` — `Boundary` vocabulary mapping ADD
  §20.4's exact safe/unsafe boundary lists, `SafePointCoordinator`
  interface plus `TurnBoundaryCoordinator` (the concrete turn/section-
  boundary implementation), and `PersistThenInterrupt` sequencing
  persist-then-interrupt against narrow `CheckpointPersister`/
  `Interrupter` seams — mirrors `runtime-b05`'s
  `internal/orchestrator.CheckpointCreate` ordering pattern (state
  before repository, early-return on the first error) one layer up, at
  the safe-point boundary instead of the checkpoint-role boundary. Only
  fakes are used for the checkpoint side this wave, per the DAG's
  explicit note and consistent with `runtime-b05`'s precedent.
- `internal/pause/requestpause_test.go` — 7 tests: first-call-creates,
  idempotent-replay-no-duplicate (many repeated calls converge on one
  record), replay-with-differing-reason-still-idempotent (emergency
  arriving mid-calibrated-pause does not fork a second record),
  fresh-cycle-allowed-once-prior-pause-terminal, per-key isolation,
  request validation, and store-error propagation.
- `internal/pause/safepoint_test.go` — 6 tests: the required test
  verbatim ("safe point persists checkpoints before interrupt",
  proven via call-order-recording fakes — not just "both were called"),
  persist-failure-never-reaches-interrupt, unsafe-boundary-rejected-
  before-either-collaborator-runs (every ADD §20.4 unsafe boundary plus
  an unrecognized one), every ADD safe boundary accepted, and
  input/nil-collaborator validation.

### Node log

```yaml
node: runtime-a04
status: completed
artifacts:
  - internal/pause/requestpause.go
  - internal/pause/requestpause_test.go
  - internal/pause/safepoint.go
  - internal/pause/safepoint_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/pause/... -run 'RequestPause|SafePoint' -v   # 13/13 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -v   # all PASS"
commit: d849d01
next_action: runtime-a07
assumptions:
  - "PauseStore is declared in internal/pause, not internal/app/ports.go
    — it is an implementation seam behind the already-frozen
    GracefulPauseService boundary (RequestPause/ReachSafePoint/etc.),
    not a new cross-component contract; adding it to ports.go would be
    the kind of speculative widening Constitution §7 rule 10 warns
    against before a real store actually needs a wider surface."
  - "A differing Reason on a RequestPause replay (e.g. emergency arriving
    while a calibrated pause is already in flight for the same key) is
    NOT treated as a conflict — unlike CONTRACT_FREEZE.md's
    CompleteNodeRequest.IdempotencyKey rule for a differing payload.
    Escalating an in-flight pause's urgency is a real, ADD-anticipated
    signal (ADD §17.6's emergency path exists precisely to skip ahead
    faster), not a caller error; any actual escalation policy (e.g.
    shortening the quiesce timeout) is left to a later node."
  - "CheckpointPersister/Interrupter (safepoint.go) are deliberately
    narrower than the frozen app.StateCheckpointService/
    app.TurnInterrupter — this node only needs to prove ordering, not
    wire the full real contracts; runtime-a05 (the full persist-phase
    orchestrator) is where the real adapters get built, per the DAG's
    scope split between this node and that one."
blockers: []
```

## runtime-a07 — Restart recovery of overdue/leased jobs

### What shipped

- `internal/scheduler/restart.go` — `Store.Restart(ctx)
  (RestartReport, error)`, intended to be called once at process
  startup before any worker calls `Claim`. Unlike `ReclaimExpired`
  (runtime-a06, which only releases a lease once `lease_expires_at` has
  actually passed — the correct behavior at any other time), `Restart`
  releases every `leased` row unconditionally: by definition every
  lease owner recorded in the DB before this call belongs to a
  now-dead previous process instance, so waiting out a stale lease's
  remaining TTL would only delay recovery for no benefit (ADD §28.3
  steps 2/8, §20.7's "on next daemon start process overdue jobs", crash
  matrix "wake job leased then daemon dies -> lease expiry reclaims",
  §29.6 scenario 11 "daemon restart rebuilds job"). `done`/`dead` rows
  are untouched (no resurrection of already-finished or
  already-exhausted work); `Restart` never claims or executes anything
  itself, relying on `Claim`'s existing `BEGIN IMMEDIATE` serialization
  to prevent duplicate execution once a row is claimable again.
  `RestartReport` (`RecoveredLeased`, `OverdueClaimable`) is returned
  for a future startup-report step (ADD §28.3 step 10) to consume.
- `internal/scheduler/restart_test.go` — 6 tests: the required test
  verbatim ("restart recovers wake job" — a leased-but-never-completed
  job whose lease has NOT yet expired, recovered and re-claimable by a
  fresh `Store` instance against the same underlying DB, with no
  duplicate execution proven via the Attempts count and a rejected
  stale `Complete` call), plus already-expired-lease coverage (proving
  `Restart` is a superset of `ReclaimExpired`, not a narrower
  replacement), done/dead-jobs-untouched, multi-job sweep, the
  `OverdueClaimable` count, and no-op-when-quiescent.

### Node log

```yaml
node: runtime-a07
status: completed
artifacts:
  - internal/scheduler/restart.go
  - internal/scheduler/restart_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/scheduler/... -run Restart -v   # 6/6 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -v   # all PASS"
commit: 6cce24a
next_action: runtime-a05 (persist phase orchestration) or runtime-a08 — NOT this wave, per explicit scope (three nodes only)
assumptions:
  - "Restart's unconditional-release semantics (ignoring
    lease_expires_at entirely) are correct ONLY at process-startup time,
    precisely because every existing lease owner is categorically dead
    by then — this is NOT a general replacement for ReclaimExpired's
    narrower, expiry-gated behavior, which remains correct and necessary
    for a lease expiring while the SAME daemon process keeps running.
    Both methods coexist on Store; Restart is documented as
    startup-only in its own doc comment so a future caller does not
    accidentally invoke it mid-run."
  - "RestartReport.OverdueClaimable is informational only (feeds a
    future startup-report step) — Restart does not itself claim or
    execute overdue jobs; that remains Claim's responsibility, called
    separately by whatever startup sequence wires this node in."
blockers: []
```

## Wave 6 cross-node observations

- All three nodes landed at or under their DAG estimates (each M/300
  points/~3-4h) with no rework and no blockers — the lowest-friction
  wave this role has had, consistent with all three nodes building
  directly on top of already-frozen, already-tested prior work
  (`runtime-a02`'s state machine, `runtime-a06`'s lease store) rather
  than integrating a new cross-role dependency.
- `runtime-a03` and `runtime-a04` both needed a small, explicitly-scoped
  package-local vocabulary (`TriggerReason`, `Boundary`) rather than
  reusing or extending a frozen enum — each was checked against
  CONTRACT_FREEZE.md and Constitution §6 rule 4 first to confirm it was
  this package's own bookkeeping, not a state value, before adding it.
- `runtime-a07`'s only real design decision — reclaiming every leased
  row unconditionally at restart, rather than reusing `ReclaimExpired`'s
  expiry-gated predicate — followed directly from reasoning about what
  "restart" categorically implies (every previous lease owner is dead)
  rather than from any new external dependency; documented explicitly in
  the node's own doc comment so a future reader does not mistake it for
  a redundant duplicate of `ReclaimExpired`.
- No new ADRs were required and no frozen contract needed a
  change-request escalation this wave. `checkpoint-b04` landing for real
  on `main` this wave did not require any change on this branch, per
  the task brief's explicit instruction to leave `runtime-b05`'s
  existing fake as-is until a future wave's integration step.
- Confirmed explicitly: this wave touched only `internal/pause/**` and
  `internal/scheduler/**` (Part A's exclusive paths) — no file under
  `internal/progress/**` (a sibling teammate's concurrent Wave 6 work on
  the distinctly-different `checkpoint-a04` node) or any Part B runtime
  path (`internal/orchestrator/**`, `internal/cli/**`,
  `internal/httpapi/**`, `internal/daemon/**`, `internal/app/wiring/**`,
  `internal/testutil/fakes/**`) was read for editing purposes or
  modified.

# Wave 7

Branch: `day1/runtime`, synced from `main` via `git fetch origin && git
merge origin/main` (fast-forward, clean — no conflicts) before any Wave 7
work, landing at `1440f4c`. Brings in Wave 6's integrated state: `checkpoint`'s
real `CompleteNode`/State Checkpoint work (`internal/progress`,
`internal/statecheckpoint`, migrations 0023-0024) and `predictor`'s real
Policy layer (`internal/policy`). Per the task brief, `checkpoint-a05`
(State Checkpoint service) and the frozen `app.StateCheckpointService`'s
real implementation are **not** part of this merge — they are sibling
teammates' concurrent work this same wave — so this wave still uses
`internal/testutil/fakes.FakeStateCheckpointService` for that one specific
dependency, exactly as instructed.

Assigned nodes, executed sequentially with independent validate+commit
after each: `runtime-a05` (persist phase orchestration) -> `runtime-b07`
(pause/resume/scheduler CLI wiring).

## runtime-a05 — Persist phase orchestration

### What shipped

- `internal/pause/persistphase.go` — `Persist(ctx, PersistDeps,
  PersistRequest) (PersistResult, error)`: sequences the five durable
  writes CONTRACT_FREEZE.md's "Transaction boundaries" section names —
  Progress Tree snapshot, State Checkpoint, Repository Checkpoint, Pause
  Record, Wake Job — in fixed order, each step idempotent-by-skip against
  a new `PersistProgress` field recorded on `PauseRecord`. `HaltAfter`/
  `HaltError` mirror `internal/progress.CompleteNode`'s own crash-injection
  vocabulary and technique exactly, per the task brief's explicit
  instruction to follow that precedent. A new `PersistPauseStore` interface
  (`GetProgress`/`SaveProgress`) is this file's only new storage seam;
  `pause.MemStore` implements it directly, extended with per-`PauseID`
  lookup (`findByID`) since `PersistProgress` is keyed by `PauseID`, not
  the `PauseKey` `MemStore`'s map already used.
- `internal/scheduler/lease.go` — added `Store.GetByPauseKind`, a
  read-only lookup by `(pauseID, kind)` — needed to recover an
  already-scheduled wake job's identity after a retried `Schedule` call
  hits the existing `UNIQUE(pause_id, job_kind)` constraint (the crash
  window between `Schedule`'s own commit and `Persist`'s bookkeeping of the
  resulting `WakeJobID`). Added here rather than left as a gap for a later
  node, since Part A owns `internal/scheduler` directly.
- `internal/pause/persistphase_test.go` — the required test verbatim
  ("crash after every phase resumes/reconciles correctly"): one test per
  phase boundary (`runToHalt` mirrors `internal/progress`'s own helper),
  each proving the halted state exposes exactly that phase's durable
  evidence and a subsequent `Persist` call resumes and completes without
  re-creating any already-durable checkpoint, plus a full reconciliation
  sweep across all five boundaries and validation/nil-dependency/unknown-
  pause-record fail-closed cases. The Repository Checkpoint step's tests
  build a REAL `internal/repocheckpoint.Service` against a real migrated
  temp-file SQLite database and a real temporary Git repository (skipped
  if `git` is unavailable) — no fake anywhere in that path, per the task
  brief.

### Design note: two backing stores for one conceptual pause record

`wake_jobs.pause_id` carries a real foreign key into the `pause_records`
SQL table (`0051_wake_jobs.sql`), but `PersistPauseStore` in these tests is
`pause.MemStore` — an in-memory store, not backed by that table. Every
crash-injection test therefore seeds BOTH: the in-memory record (this
package's own durable-progress bookkeeping) AND a matching real
`pause_records` row (so phase 5's `Schedule` call satisfies the FK). This
is flagged explicitly as a tracked gap for a later integration node (a real
SQLite-backed `PauseStore` implementing `PersistPauseStore` against the
same `pause_records` table `wake_jobs` already references), not silently
worked around.

### Node log

```yaml
node: runtime-a05
status: completed
artifacts:
  - internal/pause/persistphase.go
  - internal/pause/persistphase_test.go
  - internal/pause/requestpause.go (modified — PauseRecord.Persist field, MemStore.GetProgress/SaveProgress)
  - internal/scheduler/lease.go (modified — Store.GetByPauseKind)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/pause/... -run PersistPhase -race -v   # 10/10 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: f5b3205
next_action: runtime-b07
assumptions:
  - "State Checkpoint step uses internal/testutil/fakes.FakeStateCheckpointService
    (checkpoint-a05's real implementation is a sibling teammate's
    concurrent, not-yet-mergeable work this same wave, per the task
    brief's explicit instruction)."
  - "Progress Tree snapshot step calls the general
    app.ProgressTreeService.Snapshot method (also faked this wave via
    fakes.FakeProgressTreeService) rather than a dedicated snapshot-only
    port — no such narrower port exists in the frozen contract, and the
    task brief authorized a fake here regardless of checkpoint-a04's real
    integration status elsewhere in the codebase."
  - "Repository Checkpoint step uses the REAL internal/repocheckpoint.Service
    (checkpoint-b04, integrated on main since Wave 5) — no fake, per the
    task brief's explicit instruction."
  - "PersistPauseStore is a new interface distinct from PauseStore
    (requestpause.go) — kept separate because RequestPause's
    FindActiveByKey/Insert operate on PauseKey while persist-phase
    resumption operates on an already-known PauseID; PauseRecord itself
    is the single shared durable type both interfaces read/write."
  - "Two backing stores for one conceptual pause record during this wave's
    tests (in-memory PersistPauseStore + real SQL pause_records row) — see
    the dedicated design note above; tracked as a gap for a later
    integration node, not silently resolved."
blockers: []
```

## runtime-b07 — Pause/Resume/SchedulerRunOnce CLI+orchestrator wiring

### What shipped

- `internal/pause/lifecycle.go` — `Cancel` (applies `EventCancel` via the
  existing transition table, persists the result) and `Resume` (drives
  `WakePending -> Validating -> {Resuming -> Resumed | Sleeping |
  BlockedConflict}` from a caller-supplied verdict — `Valid`/`QuotaUnsafe`/
  `Conflict`, exactly one required). Resume's doc comment states explicitly
  that real resume validation (quota/repository/session/authorization
  checks, ADD §20.8) is `runtime-a08`'s not-yet-built scope; this node
  implements only the state-machine half, per Constitution §7 rule 3
  ("capability gaps are surfaced explicitly, never silently assumed
  away").
- `internal/pause/requestpause.go` — `PauseStore` gained `GetByID`/
  `UpdateStatus` (both implemented on `MemStore`), needed because
  Cancel/Resume take a caller-supplied `PauseID` (e.g. a `--pause-id` CLI
  flag) rather than the `PauseKey` `RequestPause`'s existing methods key
  on.
- `internal/orchestrator/pauselifecycle.go` — `PauseRequestCmd`,
  `PauseCancelCmd`, `ResumeCmd`, `SchedulerRunOnceCmd`: thin orchestration
  over this same role's own real `pause.RequestPause`/`Cancel`/`Resume` and
  `scheduler.Store.Claim` — no fake anywhere in this file, per the DAG's
  "now a hard dependency... same branch, no fake needed" note.
  `SchedulerRunOnceCmd` claims (does not execute) one due job per sweep —
  "run a single scheduler sweep and exit" names a claim step, not a full
  wake-to-resume pipeline (`runtime-a09`'s scope).
- `internal/cli/pause.go` — `NewPauseCmd`/`NewResumeCmd`/`NewSchedulerCmd`,
  the real Cobra constructors (schema-versioned JSON output, typed errors,
  no raw prompt/log leakage), replacing `root.go`'s stub tree the same way
  `NewCheckpointCmd`/`NewStatusCmd` did in earlier waves — `root.go` itself
  is untouched, per that same precedent (the standalone stub tree stays as
  the bare-`NewRootCmd()` fallback).
- `internal/app/wiring/wiring.go` — new optional `Services.PauseLifecycle`
  field (`orchestrator.PauseLifecycleDeps`); `RootCmd` swaps in the real
  `pause`/`resume`/`scheduler` command trees only when a `Store`/
  `WakeJobs` is actually configured, otherwise leaving the original stub
  tree mounted (mirrors `Diagnostics`' existing optional-field, all-skipped
  fallback precedent).

### Node log

```yaml
node: runtime-b07
status: completed
artifacts:
  - internal/pause/lifecycle.go
  - internal/pause/lifecycle_test.go
  - internal/pause/requestpause.go (modified — GetByID/UpdateStatus)
  - internal/pause/requestpause_test.go (modified — fakePauseStore stub methods)
  - internal/orchestrator/pauselifecycle.go
  - internal/orchestrator/pauselifecycle_test.go
  - internal/cli/pause.go
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/orchestrator/... -run 'PauseRequest|Resume|SchedulerRunOnce' -race -v   # 11/11 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "go test ./internal/app/wiring/... -race -v   # all PASS, including 4 new end-to-end CLI-tree tests"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: fdb5911
next_action: none — both Wave 7 nodes complete; runtime-a08/a09/a10/a11/b06/b09/b10 remain, out of scope this wave
assumptions:
  - "Resume's Valid/QuotaUnsafe/Conflict verdict is caller-supplied this
    wave, not independently computed — see lifecycle.go's package comment.
    ResumeCmdRequest defaults to Valid when neither --quota-unsafe nor
    --conflict is passed, keeping the common CLI case usable without
    requiring a08's not-yet-built checks; documented, not silent."
  - "SchedulerRunOnceCmd claims but does not execute/resume the claimed
    job — left leased for a later worker loop (a09) to actually drive
    through EventWakeDue/Resume; consistent with the command's own P0
    description naming a sweep, not a pipeline."
blockers: []
```

## Wave 7 cross-node observations

- `runtime-a05` was the wave's (and, per the DAG's own risk ranking,
  the role's) highest-risk node — crash-injection testing across FIVE
  independent write boundaries, two of them real cross-role services
  (Repository Checkpoint) rather than in-process fakes, required a
  heavier test harness (real SQLite DB + real temporary Git repository)
  than any prior Part A node needed. The `HaltAfter`/`HaltError`
  precedent transplanted from `internal/progress.CompleteNode` (Wave 6's
  sibling `checkpoint-a04` work, now on `main`) applied cleanly with no
  redesign — direct evidence the technique generalizes beyond the single
  package it was first proven in.
- A real, if narrow, gap was found and closed rather than deferred:
  `scheduler.Store` had no way to recover an already-scheduled job's
  identity after a retried `Schedule` call hit its own `UNIQUE(pause_id,
  job_kind)` constraint. Since Part A owns `internal/scheduler` directly,
  `GetByPauseKind` was added in the same node rather than punted to a
  future a06/a09 follow-up — the DAG's "no fake needed, same branch"
  principle for `runtime-b07`'s dependencies applies by the same logic to
  a same-role internal gap discovered mid-node.
- `runtime-b07` mirrors `runtime-b05`'s (Wave 5) wiring shape almost
  exactly (orchestrator file + test, CLI file, wiring.go diff) but for
  FOUR command surfaces instead of one `checkpoint create` — consistent
  with the Wave 5 lessons_learned observation that Part-B-shaped nodes'
  file count runs higher than the DAG's estimate once CLI+wiring are
  counted; this node's actual file count (9, including two modified
  pre-existing files) is in line with that established pattern, not a new
  surprise.
- No new ADRs, no change-request escalations, and no frozen-contract
  questions this wave. The one explicitly-tracked gap (`PersistPauseStore`
  as an in-memory store distinct from the real `pause_records` SQL table
  `wake_jobs` already references) is flagged in `runtime-a05`'s own
  section above for a later integration node, not silently resolved or
  left undiscoverable.
