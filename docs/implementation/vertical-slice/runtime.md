# runtime — Progress Artifact

> **Wave 11 sections appended below the Wave 10 node log** — see "Wave 11"
> heading. Wave 11 completes `runtime-b10`, the final Part B
> integration/reliability gate and this role's LAST assigned DAG node —
> after this wave, every node ever assigned to `runtime` (`a01`-`a11`,
> `b01`-`b10`, 21 nodes total) is complete. Centerpiece: a real
> in-process-restart-same-SQLite-file proof (both a clean-shutdown restart
> and a genuine SIGKILLed-subprocess crash restart), closing one real,
> repeatedly-flagged gap it found along the way (`pause.PauseStore` had no
> SQLite-backed implementation, only the in-memory `MemStore` five prior
> Part A nodes had each separately deferred) plus one genuine Part B Tests
> gap ("CLI golden tests," never built by any prior b01-b09 node). No new
> ADRs, no cross-role change requests this wave.

> **Wave 10 sections appended below the Wave 9 node log** — see "Wave 10"
> heading. Wave 10 completes two nodes in sequence, each validated and
> committed independently: `runtime-a11` (the final Part A integration
> gate — full lifecycle crash-injection sweep + closing the one genuine
> gap this pass found, provider-interrupt-failure state-machine
> integration) and `runtime-b09` (uniform error contract + privacy gate
> audit across all P0 CLI commands, closing the JSON-error-rendering gap
> this pass found). No new ADRs, no cross-role change requests this wave.
> `runtime-b10` (this role's final node) remains for a future wave.

> **Wave 9 sections appended below the Wave 8 node log** — see "Wave 9"
> heading. Wave 9 completes three nodes in sequence, each validated and
> committed independently: `runtime-a09` (duplicate-wake exactly-once +
> cancel-wins-race), `runtime-a10` (provider interrupter/resumer fake
> contract tests), `runtime-b06` (decision allow/deny wired to the REAL
> `internal/evaluation.Service`, replacing runtime-b03's fake). No new ADRs,
> no cross-role change requests this wave.

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

`docs/implementation/vertical-slice/wave2-analysis/ADR_Recommendations.md` REC-03
documents a real, still-open discrepancy: `Preflight_ADD.md` Appendix E.3
spells Claude Code hook subcommands in PascalCase (e.g. `UserPromptSubmit`,
matching Claude's own hook-event-name casing), while `agents/runtime.md`'s
own P0 command list, this node's DAG validation command
(`docs/implementation/vertical-slice/EXECUTION_DAG.md` `runtime-b01`'s row), and the
vertical-slice execution plan's demo script all independently use kebab-case
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

Branch: `vertical-slice/runtime`, synced from `main` (Wave 3 integration state,
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

Branch: `vertical-slice/runtime`, synced from `main` (Wave 4 integration state,
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

Branch: `vertical-slice/runtime`, synced from `main` via `git fetch origin && git
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

Branch: `vertical-slice/runtime`, synced from `main` via `git fetch origin && git
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

# Wave 8

Branch: `vertical-slice/runtime`, synced from `main` via `git fetch origin && git
merge origin/main` (fast-forward, clean — no conflicts) before any Wave 8
work, landing at `2b7c29c`. Brings in Wave 7's integrated state: `checkpoint`'s
real `internal/statecheckpoint.Service` (`app.StateCheckpointService`) and
`internal/repocheckpoint`'s orphan-scan/crash-safety hardening, and
`predictor`'s real `internal/evaluation.Service`
(`app.EvaluationService`/`ConsumeAuthorization`, `predictor-09`). Per the
task brief, `predictor-10`'s authorization-hardening pass is a concurrent
sibling this same wave, not yet mergeable — this wave still uses
`internal/testutil/fakes.FakeEvaluationService` for the one
`ConsumeAuthorization` call this node makes, consistent with the
established fake-then-swap pattern.

Assigned node: `runtime-a08` (Resume validation).

## runtime-a08 — Resume validation

### What shipped

- `internal/pause/resumevalidation.go` — the real check
  `lifecycle.go`'s package comment named as its own explicit gap: four
  independently-swappable checkers, one per agents/runtime.md Part A
  deliverable 8 check, each returning a uniform `CheckResult{Pass, Reason,
  Detail}`:
  - `CheckQuotaSafety` (`QuotaSnapshotReader` seam): re-reads current quota
    for the same limit the pause-time baseline recorded and fails if it has
    gotten WORSE (higher `UsedPercent`, or a new transition into
    `Reached`) — never assumed safe on an unreadable comparison (nil
    `UsedPercent` on either side fails closed).
  - `CheckRepositoryCompatibility` (`app.RepositoryCheckpointService.Verify`
    — REAL, checkpoint-b04, integrated since Wave 5 — plus a package-local
    `RepoFingerprintReader` seam for the CURRENT repository state): first
    confirms the pause-time checkpoint itself still verifies intact, then
    compares its recorded `GitHead` against the current fingerprint. No
    change at all passes trivially; a change that overlaps the paused
    work's own files always blocks (`ReasonRepositoryOverlapBlocks`,
    regardless of policy); a non-overlapping ("unrelated") change is
    allowed or blocked per a caller-supplied `RepoChangePolicy`
    (`RepoChangePolicyAllowUnrelated` default, or `RepoChangePolicyBlockAny`).
  - `CheckSessionCapability` (`SessionCapabilityReader` seam): the provider
    session must currently report `Resumable`, plus an optional explicit
    `domain.ProviderCapabilities.SessionResume` requirement.
  - `CheckAuthorization` (`app.EvaluationService.ConsumeAuthorization` —
    FAKE this wave, see below): a rejected/expired/already-consumed
    authorization and a genuinely-unreachable authorization service are
    both failures, but with distinct reason codes
    (`ReasonAuthorizationInvalid` vs. `ReasonAuthorizationServiceUnavailable`)
    so a caller/audit trail can tell "we asked and it said no" apart from
    "we could not ask."

  `ValidateResume(ctx, ResumeValidationDeps, ResumeValidationRequest)
  (ResumeValidationResult, error)` orchestrates all four, in the fixed
  order quota → repository → session → authorization (cheapest/most-
  reschedulable first; authorization — the one-time, non-reversible
  resource — last). It does NOT stop at the first FAILING check (a caller
  building a full audit trail, or a human resolving `BlockedConflict` via
  ADD §20.9's UI, needs every check's own outcome); a downstream READ
  failure inside any one checker is reported as a failing `CheckResult`
  with an `_UNAVAILABLE` reason code, not a Go error, so it is exactly as
  visible in the result as any other rejection. A returned Go error is
  reserved strictly for a composition bug (nil dependency, missing
  `SessionID`) and aborts immediately, before running any check.
  `ResumeValidationResult.Verdict()` maps the four results onto
  `lifecycle.go`'s existing `ResumeRequest{Valid, QuotaUnsafe, Conflict}`
  three-way verdict: all-pass → `Valid`; quota failing ALONE (every other
  check passing) → `QuotaUnsafe` (reschedule, per the required test); any
  other failure (repository, session, or authorization, alone or combined
  with a quota failure) → `Conflict` (block) — a simultaneous quota +
  repository failure still blocks, it does not silently reschedule past an
  unresolved conflict.

  `RescheduleWakeJobOnQuotaUnsafe` proves "unsafe quota reschedules" at the
  scheduler-integration level, not just the pause-record state-machine
  level `lifecycle.go`'s existing `Resume` already covers: when
  `ValidateResume`'s verdict is `QuotaUnsafe`, it calls
  `scheduler.Store.Fail` on the associated wake job (via a narrow
  `WakeJobRescheduler` seam `*scheduler.Store` satisfies directly) — reusing
  `Fail`'s existing ADD §20.7 backoff-then-retry-or-dead machinery
  (runtime-a06) rather than inventing a second reschedule mechanism, since
  a quota-unsafe resume attempt IS a failed attempt from the wake job's own
  perspective. It is a no-op (does not call `Fail`) for a `Valid` or
  `Conflict` verdict. Driving this from the actual scheduler-claimed wake
  job in a full wake-to-resume pipeline (claim → validate → drive
  `EventWakeDue`/`Resume` → reschedule-or-block) remains `runtime-a09`'s
  scope, per the DAG (`runtime-a09` depends on `runtime-a08` and covers
  `DuplicateWake`/`Cancel`); this node proves the reschedule mechanism
  itself is correct and available.

- `internal/pause/resumevalidation_test.go` — 42 tests, all named
  `TestResumeValidation_*` (or `TestResumeValidationResult_*`) so the DAG's
  exact validation command (`-run ResumeValidation`) selects the whole
  file. Covers every one of agents/runtime.md's Part-A-deliverable-8
  required tests verbatim: "unsafe quota reschedules" (both at the
  `CheckQuotaSafety`/`Verdict()` level and, separately, proven directly
  against the scheduler via `RescheduleWakeJobOnQuotaUnsafe` actually
  calling `scheduler.Store.Fail`), "repo overlap blocks" (proven at both
  `CheckRepositoryCompatibility` and `ValidateResume` levels, and shown to
  hold regardless of policy), "unrelated repo change follows configured
  policy" (both policies, both levels) — plus every fail-closed case named
  in this node's own design brief: unknown/nil quota comparison, checkpoint-
  invalid, fingerprint-unreadable, session-not-resumable, capability-
  confirmed-absent, authorization-rejected vs. authorization-service-
  unavailable (distinct reason codes), nil-dependency validation for every
  one of the five `ResumeValidationDeps` fields, malformed-request
  validation, the "every check still runs after an earlier FAILURE (not an
  error)" ordering guarantee, and the "a downstream READ failure surfaces
  as a CheckResult, not a Go error, and does not block later checks"
  distinction this node's design deliberately draws.

### Design note: two failure channels, chosen deliberately

`ValidateResume`/each checker returns `(CheckResult, error)`, and the two
mean different things: a downstream service erroring when asked (quota
read fails, `Verify` errors, session/capability read fails,
`ConsumeAuthorization` errors) is captured as a FAILING `CheckResult` with
an `_UNAVAILABLE`-suffixed reason code — still fail-closed (the check does
not pass), but visible through the same channel a normal rejection uses, so
a future `resume_attempts` audit row (0052_resume_attempts.sql already has
exactly the right columns: `repository_fingerprint_before/after`,
`quota_used_percent`, `failure_code`) or a human resolving `BlockedConflict`
sees a labeled reason, not a generic error. A returned Go `error` is
reserved for a composition bug (nil dependency, malformed request) and
aborts before any check runs. An earlier draft conflated these two (treating
every checker error as an abort-everything signal); this node's own test
suite caught the inconsistency before commit (see Lessons Learned) and the
design was corrected to the two-channel split described here, which is also
what makes `RescheduleWakeJobOnQuotaUnsafe`'s failure-reason string
(`result.FirstFailure()`) meaningful — it always has something to report.

### Node log

```yaml
node: runtime-a08
status: completed
artifacts:
  - internal/pause/resumevalidation.go
  - internal/pause/resumevalidation_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/pause/... -run ResumeValidation -v   # 42/42 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -count=1   # all PASS"
  - "go build ./... && go test ./... -race -count=1   # all PASS, whole repo, zero regressions"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <recorded below>
next_action: runtime-a09 (duplicate-wake + cancel) — NOT this wave, per explicit scope; a09 is where a real scheduler-claimed wake job is driven through EventWakeDue -> ValidateResume -> Resume/RescheduleWakeJobOnQuotaUnsafe end to end
assumptions:
  - "app.EvaluationService.ConsumeAuthorization is FAKED this wave
    (internal/testutil/fakes.FakeEvaluationService) — predictor-10's
    authorization-hardening pass is a concurrent sibling this same wave,
    not yet mergeable, per the task brief's explicit instruction, consistent
    with the established fake-then-swap pattern (runtime-a05/b05 did the
    same for checkpoint-a05/b04 in earlier waves)."
  - "RepoFingerprintReader is this package's OWN narrow interface, not
    internal/gitx.Fingerprint directly — internal/pause does not take a
    compile-time dependency on checkpoint's Git plumbing package merely to
    declare a seam; a future integration node adapts a real
    gitx.Fingerprint onto RepoFingerprint (HeadOID + ChangedPaths)."
  - "QuotaSnapshotReader/SessionCapabilityReader are also this package's own
    narrow seams, not direct uses of the frozen app.QuotaReader or a
    claude-provider capability port — a future integration node adapts the
    real, wider signals behind these narrower interfaces, mirroring this
    package's existing CheckpointPersister/Interrupter (safepoint.go)
    precedent for 'declare the narrowest seam this node needs, let a later
    wiring node adapt the real thing.'"
  - "RescheduleWakeJobOnQuotaUnsafe requires the caller to already hold the
    wake job's lease (scheduler.Store.Fail's own precondition) — correct
    for the scheduler-driven wake pipeline (a09's scope) but not applicable
    to a manual `preflight resume` invocation that never claimed a lease;
    a manual resume's quota-unsafe verdict is still correctly reflected on
    the PAUSE RECORD via Resume/Verdict regardless."
  - "PausedWorkPaths (the paths RepositoryCompatibility's overlap check
    compares against) is caller-supplied on ResumeValidationRequest, not
    derived by this node — deriving 'which paths did the paused work
    touch' from the Progress Tree/repository checkpoint's own manifest is
    a future integration node's concern, not part of the validation LOGIC
    this node builds."
blockers: []
```

## Wave 8 cross-node observations

- This wave's one node closes the last explicitly-named gap in
  `lifecycle.go`'s `Resume` (its own package comment named runtime-a08 as
  the owner of "real resume validation," not yet built) — `Resume` itself
  was not modified; `ValidateResume`/`Verdict()` are additive, designed to
  feed `Resume`'s existing caller-supplied-verdict parameter without
  requiring any change to `lifecycle.go`'s frozen shape from prior waves.
- Consistent with this role's established practice, the one real judgment
  call with cross-wave consequence (the two-channel failure design: normal
  `CheckResult` failures vs. composition-bug Go errors) is documented
  explicitly in its own section above, not left implicit — a future reader
  extending any one checker should follow the same split rather than
  reintroducing the conflated version this node's own tests caught first.
- No new ADRs, no change-request escalations, and no frozen-contract
  questions this wave. `app.RepositoryCheckpointService.Verify` and
  `app.EvaluationService.ConsumeAuthorization`'s frozen signatures
  (`internal/app/ports.go`) were used exactly as declared, with no
  requested addition.

## Wave 9

Three nodes this wave, each validated and committed independently per the
task brief's explicit instruction (never batched): `runtime-a09`,
`runtime-a10`, `runtime-b06`. Merged `origin/main` first (fast-forward,
clean) to bring in Wave 8's integrated state: predictor's real, hardened
`ConsumeAuthorization` and checkpoint's completed Part A/B final gates.

### runtime-a09: duplicate wake exactly-once + cancel-wins-race

**The real bug this node found and fixed.** `lifecycle.go`'s `Cancel` and
`Resume` (runtime-b07, prior wave) were written as a single `GetByID`
followed by one or more unconditional `UpdateStatus` calls. That shape has
a genuine time-of-check-to-time-of-use gap: two concurrent callers acting
on the SAME `PauseID` (the split-brain scenario this node's task brief
names explicitly — a lease reclaimed after appearing expired, but the
original holder still alive and unaware) could both read the same starting
status and both durably "succeed," silently clobbering each other. This was
not a hypothetical: an early version of this node's own
`TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState` test (see
Lessons Learned) caught a *different*, more subtle issue in the test's own
assumption, which in turn confirmed the underlying fix was necessary and
correct.

**Fix**: added `PauseStore.CompareAndSwapStatus(ctx, id, expected, next)
(ok, found bool, err error)` to the `PauseStore` interface
(`requestpause.go`), implemented on `MemStore` under the store's existing
mutex (the in-memory reference implementation's own analogue of a real
SQLite-backed store's `UPDATE ... WHERE status = ?` conditional update —
mirrors `internal/scheduler.Store.Complete/Fail/Renew`'s own
`WHERE status = ? AND lease_owner = ?` pattern exactly, applied here to
`pause_records` instead of `wake_jobs`). `Cancel` and `Resume`
(`lifecycle.go`) were rewritten around a shared `applyCASVerb`/
`applyCASFrom`/`tryApplyCAS` retry-loop helper: every state transition is
now an atomic read-`Apply`-swap unit, retried on a lost race rather than
either clobbering a concurrent writer or silently giving up.

**New**: `wake.go`'s `Wake(ctx, store, WakeRequest{PauseID})` — the
scheduler-to-pause-state-machine bridge applying `EventWakeDue`
(`Sleeping -> WakePending`) that no code previously implemented at all
(confirmed by grep: `EventWakeDue` was referenced only in the transition
table and tests before this node). This closes the gap needed to build a
genuine, end-to-end "duplicate wake" test rather than only testing the
already-existing lease-claim exclusivity (`internal/scheduler`) in
isolation.

**Tests** (`internal/pause/wake_test.go`, `splitbrain_test.go`):
- `TestDuplicateWake_WorkersYieldOneResume` / `_WorkersAcrossManyPausesEachWokenOnce`
  — many goroutines (20, repeated 50 times) racing `Wake` on the same
  `PauseID` yield exactly one success, every losing call a genuine
  `*pause.TransitionError`; extended to N independently-sleeping pauses
  raced concurrently.
- `TestDuplicateWake_SplitBrainReclaimedLeaseOriginalHolderStillAlive_OnlyOneWakeSucceeds`
  — the literal split-brain scenario using a REAL `scheduler.Store`: worker
  A claims a short lease, the lease expires while A is still alive
  (unaware), worker B legitimately reclaims it per `scheduler.Store`'s own
  expired-lease rule, and both A and B concurrently call `Wake` for the
  same `PauseID` — exactly one succeeds, and the lease layer's own
  `Complete` independently confirms only B (the true current holder) can
  complete the job.
- `TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState`,
  `TestCancel_WinsAgainstAlreadyInFlightWake`,
  `TestCancel_CannotWinAfterResumeStarted` — prove cancel always wins a
  genuine race against wake (Sleeping and WakePending both have a Cancel
  edge), even when wake already landed first, right up until Resume
  actually starts (Resuming has no Cancel edge — ADD §20.11's race window
  closing exactly there, not before).
- `TestMemStore_CompareAndSwapStatus_*` — direct unit coverage of the new
  primitive, including a 25-goroutine/30-repeat concurrent-serialization
  proof independent of `Cancel`/`Resume`/`Wake`'s own semantics.

This coverage is designed to feed `qa-07`'s dedicated
`DoubleWorkerRace -race -count=20` stress test directly — the DAG names
`runtime-a09` as `qa-07`'s sole dependency.

```yaml
node: runtime-a09
status: completed
artifacts:
  - internal/pause/requestpause.go (CompareAndSwapStatus added to PauseStore + MemStore)
  - internal/pause/requestpause_test.go (fakePauseStore stub method added)
  - internal/pause/lifecycle.go (Cancel/Resume rewritten around CAS retry loop)
  - internal/pause/wake.go (new: Wake)
  - internal/pause/wake_test.go (new)
  - internal/pause/splitbrain_test.go (new)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/...   # OK"
  - "go test ./internal/scheduler/... ./internal/pause/... -run 'DuplicateWake|Cancel' -race -v   # all PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/... -race -v   # all PASS"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: e7d37be
next_action: runtime-a11 (Required tests — crash-after-every-phase, expired-lease-reclaim, XL) — NOT this wave
assumptions:
  - "PauseStore.CompareAndSwapStatus is a NEW interface method (not a
    frozen cross-component port — PauseStore itself is this package's own
    internal seam, per requestpause.go's existing doc comment) — widening
    it was in-bounds since only this package's own MemStore implements it
    today (confirmed by grep before making the change)."
  - "Wake does not itself claim or complete a scheduler lease — that
    remains internal/scheduler.Store's job; a future scheduler-worker loop
    composes Claim -> Wake -> ValidateResume/Resume -> Complete, with
    Wake's CAS guarantee covering only the pause-record-mutating middle
    step regardless of what the lease layer decided."
  - "splitbrain_test.go models the split-brain scenario against
    pause.MemStore (not a real SQLite-backed PauseStore), since no such
    adapter exists yet — the same documented gap persistphase_test.go's
    own seedPauseRecordRow comment already calls out (pause_records the
    SQL table vs. PauseStore the in-memory interface are two different
    backing stores today for what is conceptually one pause record)."
blockers: []
```

### runtime-a10: provider interrupter/resumer fake contract tests

Researched first whether `app.TurnInterrupter`/`app.SessionResumer`
(`internal/app/ports.go`) carry any additional frozen behavioral contract
beyond their bare method signatures — checked `Preflight_ADD.md` §9.10,
§20.6 Phase 4, §20.15, §28.4, `CONTRACT_FREEZE.md`, and
`agents/claude-provider.md`'s own stretch-goal section. Confirmed: none.
Both are deliberately narrow, single-method interfaces with no doc
comments in the frozen contract itself; ADD's only related guidance is
operational (§20.15: "provider interrupt times out -> kill managed
process, mark uncertain"; §28.4: "inspect provider, reconcile") — a
pause-package-level concern layered above these calls, not a second return
channel the interfaces themselves need.

Built the contract suite around exactly the properties `internal/pause`'s
own callers rely on: a well-formed call succeeds and returns internally
consistent data (`SessionResumer.Resume`'s `RunHandle.SessionID` must
match the request, never a silent substitution); a failure surfaces as a
plain returned error, never a panic (what "provider interrupt failure
leaves recoverable state" depends on one layer up — `EventInterruptFailed`
can only be applied to an ordinary error value); context cancellation is
respected unless an implementation explicitly opts out
(`SkipContextCancellation`).

**New**:
- `internal/testutil/fakes/provider.go` — `FakeTurnInterrupter`/
  `FakeSessionResumer`, following this package's existing Func-field
  convention exactly.
- `internal/testutil/fakes/providercontract.go` —
  `ProviderInterrupterContract`/`ProviderSessionResumerContract`, each
  taking a constructor closure plus an `ArrangeSuccess`/`ArrangeFailure`
  configuration, so any implementation — these fakes today, or a future
  real `claude-provider` signal-interruption/session-resume adapter (that
  role's own documented stretch goal) — runs the identical suite to prove
  itself compliant, without `runtime` writing bespoke tests per adapter.
- `internal/testutil/fakes/providercontract_test.go` — runs the suite
  against both fakes, including the unconfigured-fake default path and an
  explicit demonstration of the context-cancellation opt-out.

```yaml
node: runtime-a10
status: completed
artifacts:
  - internal/testutil/fakes/provider.go (new)
  - internal/testutil/fakes/providercontract.go (new)
  - internal/testutil/fakes/providercontract_test.go (new)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/...   # OK"
  - "go test ./internal/testutil/fakes/... -run ProviderContract -v -race   # all PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/... -race -v   # all PASS"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: e246ee1
next_action: none named for this node in the DAG ("None" in the Blockers column) — claude-provider's own future stretch-goal adapter is the natural future consumer of this suite, not a runtime follow-up
assumptions:
  - "Neither interface has additional frozen behavioral invariants beyond
    the bare signature — confirmed by direct research against
    Preflight_ADD.md/CONTRACT_FREEZE.md/agents/claude-provider.md before
    writing the suite, not assumed. The suite therefore deliberately tests
    only the properties internal/pause's own call sites actually rely on,
    not speculative invariants no document states."
  - "The suite is a function taking a constructor closure (newX func() X)
    plus an Arrange* configuration struct, not a fixed instance or a
    zero-config default — this is what lets a future real adapter's own
    contract test file supply its own success/failure arrangement without
    this package guessing at how a real adapter fails on demand."
blockers: []
```

### runtime-b06: decision allow/deny wired to real EvaluationService

Wired the REAL `internal/evaluation.Service` (predictor-09/10, both
integrated on `main`) into `preflight decision allow`/`decision deny`,
replacing runtime-b03's fake — a hard dependency per the DAG note, since
only real, storage-backed `ConsumeAuthorization` can prove the
replay-rejection guarantee end to end rather than merely simulating it.

**Design**: `agents/runtime.md`'s Part B pipeline steps 10/11 ("`decision
allow` issues one-time authorization" / "Resubmitted prompt consumes
authorization exactly once before allowing") describe two DIFFERENT
moments of one flow, not one call. `DecisionAllowCmd`
(`internal/orchestrator/decision.go`) selects between them by whether the
caller supplies an `AuthorizationID`:
- **Issue flow** (no `AuthorizationID`): reads the evaluation's `Decide`
  result, then issues a fresh one-time `app.Authorization` via a new local
  `AuthorizationIssuer` seam — `IssueAuthorization` is a real method on
  `*evaluation.Service` but deliberately NOT part of the frozen
  `app.EvaluationService` interface (confirmed by reading
  `internal/evaluation/service.go`'s own doc comment, which anticipates
  exactly this future caller), so this package declares its own narrow
  interface for the one extra method it needs, mirroring
  `evaluate.go`'s existing `UsageObservationLoader`/`GitSnapshotter`
  precedent.
- **Consume flow** (`AuthorizationID` supplied — the resubmission): calls
  the real `ConsumeAuthorization` directly with the caller's
  `TurnID`/`PromptHash`, never re-deriving a new decision or a new
  authorization.

`DecisionDenyCmd` reads back the decision via `Decide` (read-back, not
recompute, per `internal/evaluation/doc.go`'s own documented convention)
with no authorization side effect — there is no "un-authorization" to
revoke; simply never issuing/consuming one already achieves "denied."

**Wiring**: `internal/cli/decision.go` (`NewDecisionCmd`) and a new
`Services.Decision` field in `internal/app/wiring/wiring.go`, gated on
`Decision.Issuer != nil` (not on `Evaluation` alone — a fake can implement
`app.EvaluationService` perfectly well without also satisfying
`AuthorizationIssuer`, so `Issuer` is the correct, minimal signal that real
wiring is in place) — matches the established stub-until-wired convention
runtime-b05/b07 set for `checkpoint`/`pause`.

**Real-pipeline test harness** (`decision_realauth_test.go`): built a real
`*evaluation.Service` against real predictor pipeline stages (`scope`,
`token`, `quota`, `risk`, `policy`) and a migrated SQLite DB — no fake
`app.EvaluationService` anywhere in this file. A fake `DataSource`, tuned
against `internal/predictor/risk/combiner.go`'s actual scoring formula
(large changed-file/line quantiles plus every completion/blast-radius flag
the real pipeline reads: security-sensitive, migration-likely,
cross-layer, open-ended scope), reliably drives `OverallRisk.Score` to
1.0 (critical band, confirmed via direct inspection —
`app.PolicyCheckpointAndRun`), with a separate low-risk control fixture
confirmed to land on `app.PolicyRun`, proving the high-risk fixture is
testing something real rather than an artifact of every input being
flagged.

All four required tests proven, verbatim:
- **"high-risk block and allow-once flow"**: the high-risk fixture reaches
  `PolicyCheckpointAndRun`, and `DecisionAllowCmd`'s issue flow
  successfully issues a real, unconsumed `Authorization` for it.
- **"second authorization replay rejected"**: issue → consume (succeeds
  exactly once) → replay the SAME `AuthorizationID` a third time → rejected
  with `ErrCodeConflict`, against the real predictor-10-hardened
  `markAuthorizationConsumed` conditional update — not a fake merely
  asserting this would happen. Extended to a 20-attempt tight sequential
  replay loop (mirrors predictor-10's own hardening-test style) — exactly
  one success across all 20.
- **"resubmitted prompt consumes authorization exactly once before
  allowing"**: the consume-flow test proves this directly — the exact
  scenario the required test names.
- **"checkpoint failure does not issue authorization"**: modeled with the
  realistic caller sequence (a `checkpointThenDecisionAllow` helper calling
  the real `CheckpointCreate` first, short-circuiting on its error before
  ever reaching `DecisionAllowCmd`), with a spy `Issuer` that fails the
  test if invoked at all — proven by construction, not merely inferred.

```yaml
node: runtime-b06
status: completed
artifacts:
  - internal/orchestrator/decision.go (new: DecisionAllowCmd, DecisionDenyCmd, AuthorizationIssuer)
  - internal/orchestrator/decision_test.go (new: structural/validation coverage against fakes)
  - internal/orchestrator/decision_realauth_test.go (new: real evaluation.Service integration)
  - internal/cli/decision.go (new: NewDecisionCmd)
  - internal/app/wiring/wiring.go (Services.Decision field + RootCmd swap)
  - internal/app/wiring/wiring_test.go (decision wiring tests)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/...   # OK"
  - "go test ./internal/orchestrator/... -run 'DecisionAllow|ReplayRejected' -v   # all PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/... ./internal/app/wiring/... -race -v   # all PASS"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: e150b35
next_action: runtime-b09 (JSON/error contract across all P0 commands) — NOT this wave
assumptions:
  - "AuthorizationIssuer is a NEW local interface in internal/orchestrator
    (not internal/app/ports.go, which is contract-integrator-owned and not
    touched) — IssueAuthorization is real on *evaluation.Service but
    intentionally outside the frozen app.EvaluationService interface, per
    that package's own doc comment anticipating this exact caller."
  - "Services.Decision is gated on Issuer specifically, not on Evaluation
    being non-nil, since only the real concrete Service satisfies both
    seams simultaneously — a fake EvaluationService alone must not
    trigger the swap to a command path that assumes real authorization
    semantics."
  - "DecisionAllowRequest's SnapshotFingerprint/RepositoryCheckpointID are
    issue-flow-only, threaded verbatim from the caller (which is expected
    to have already run `checkpoint create` upstream) — this command does
    not itself create a checkpoint, mirroring CheckpointCreate's own
    two-step, not-blurred-together precedent."
blockers: []
```

## Wave 9 cross-node observations

- All three nodes were validated and committed independently, per the
  explicit task instruction — no batching. Each node's own validation
  command (from `EXECUTION_DAG.md`) was run and confirmed green before
  moving to the next; the full owned-package test suite plus a whole-repo
  `go build`/`go test` was additionally run after every single node, not
  just at the end of the wave.
- `runtime-a09` is the one node this wave whose own test suite caught a
  real design flaw in ITS OWN first draft (not in existing code) before
  commit — see Lessons Learned for the full account. The fix was to the
  test's assertion, not the implementation, but the exercise is exactly
  why the CAS retry-loop implementation itself was written and tested as
  rigorously as it was: a first naive implementation attempt (a plain
  `GetByID` + `UpdateStatus` sequence, i.e. what `Cancel`/`Resume` already
  looked like before this wave) would have failed a correctly-written
  version of the same test reliably, not flakily — this was verified
  directly (see Lessons Learned).
- `runtime-a10` and `runtime-b06` both required a dedicated research pass
  before writing any code — confirming, respectively, that no additional
  frozen behavioral contract exists for `TurnInterrupter`/`SessionResumer`
  beyond the bare signature, and reverse-engineering the real risk-scoring
  formula (`internal/predictor/risk/combiner.go`) well enough to build a
  fake `DataSource` that reliably drives the real pipeline into a specific
  risk band rather than merely asserting a policy action from a mocked
  `Decide` call. Both research passes are reflected in code comments at
  their exact point of relevance, not just in this progress artifact.
- No new ADRs, no cross-role change-request escalations, no frozen-contract
  questions this wave. `internal/app/ports.go`, `internal/domain/**`, and
  `internal/evaluation/**` were called, never modified, per the task's
  explicit boundary.

## Wave 10

Two sequential nodes, each independently validated and committed:
`runtime-a11` (the final Part A integration gate) and `runtime-b09`
(Part B's error-contract + privacy gate audit). Both are comprehensive
proof/audit nodes, not new-feature nodes — the task brief was explicit
that manufacturing busywork where nothing new is found is the wrong
outcome; both nodes instead did the research first, then closed exactly
the genuine gaps that research found, and reported precisely where no
gap existed.

### runtime-a11: full Part A lifecycle integration proof + interrupt-failure gap closure

A dedicated research agent audited every required test in
`agents/runtime.md`'s Part A "Required tests" list against ALL existing
coverage (`internal/pause/**`, `internal/scheduler/**`) before any code
was written, with file:line precision. Findings, verbatim by required
test:

- **"crash after every phase resumes/reconciles correctly"**: already
  fully covered for the 5 PERSIST sub-phases by `persistphase_test.go`'s
  existing `HaltAfter`/`HaltError` harness (runtime-a05). GENUINE GAP: no
  test crash-injected across the OTHER ~9 top-level lifecycle transitions
  (`Predicted->Requested` through `Resuming->Resumed`). Closed by
  `TestFullLifecycle_CrashAfterEveryTransition_ResumesOrReconciles` (a
  9-step sweep, "crashing" — re-reading fresh from the durable store —
  after every transition and asserting no lost/doubled work) plus
  `TestFullLifecycle_CrashDuringQuiescing_EmergencyShortCircuitReconciles`
  for the emergency short-circuit edge.
- **"restart recovers wake job"**: a07's `restart_test.go` proves this at
  the scheduler/lease level in isolation. GENUINE GAP: nothing composed
  `scheduler.Store.Restart` with `pause.Wake` and a real `ValidateResume`
  call in one flow. Closed by
  `TestFullLifecycle_RestartRecoversWakeJob_ThenReEntersResumeValidation`.
- **"unsafe quota reschedules" / "repo overlap blocks" / "unrelated repo
  change follows configured policy"**: a08's `resumevalidation_test.go`
  proves these at the `ValidateResume` function level directly. GENUINE
  GAP: nothing drove these through the FULL lifecycle (`RequestPause` ->
  persist transitions -> `Wake` -> `ValidateResume` -> `Resume` via
  `Verdict()`). Closed by `TestFullLifecycle_QuotaUnsafeReschedules_EndToEnd`,
  `TestFullLifecycle_RepoOverlapBlocks_EndToEnd`,
  `TestFullLifecycle_UnrelatedRepoChangeFollowsPolicy_EndToEnd` (both
  policy branches).
- **"duplicate workers yield one resume" / "expired lease reclaimed" /
  "cancel wins race with wake"**: a09 already proves these, including one
  real composition (`splitbrain_test.go` pairs a real `scheduler.Store`
  with `pause.Wake`). This node re-ran the equivalent races ONE LEVEL
  FURTHER down the lifecycle (through a real `ValidateResume`/`Resume`
  call) specifically to catch any interaction effect the narrower a09
  compositions could have missed — mirroring how a09 itself caught a real
  bug in earlier-wave code last wave. Result: **no new bug found; the
  CAS-based guarantees hold under the fuller composition, confirmed
  precisely, not assumed.** See
  `TestFullLifecycle_DuplicateWakeRace_ThroughFullValidateResume`,
  `TestFullLifecycle_ExpiredLeaseReclaimed_ThenFullValidateResume`,
  `TestFullLifecycle_CancelWinsRace_EvenDuringValidation`.
- **"provider interrupt failure leaves recoverable state"**: THE ONE
  GENUINE PRODUCTION-CODE GAP this node found. The transition-table edge
  (`{Interrupting, interrupt_failed} -> Failed`) and a bare-`Apply`-level
  test already existed, and runtime-a10 already built
  `FakeTurnInterrupter` — but no production code anywhere in
  `internal/pause` actually called a `TurnInterrupter` and applied the
  resulting event to a real `PauseRecord`. `safepoint.go`'s
  `PersistThenInterrupt` (runtime-a04) deliberately proves ordering only,
  by its own documented scope, and never touches `PauseStore`/`Apply`.
  **Fix**: new `internal/pause/interrupt.go` —
  `TurnInterrupterAdapter` (bridges the frozen `app.TurnInterrupter` onto
  this package's `PauseID`-keyed seam, exactly as `safepoint.go`'s own doc
  comment anticipated a later node would do) and `InterruptAndSleep`
  (drives `Interrupting -> {Sleeping | Failed}` via the same
  `CompareAndSwapStatus` discipline `lifecycle.go`/`wake.go` established).
  Proven by `TestFullLifecycle_ProviderInterruptFailure_LeavesRecoverableState`
  (the record durably lands at `Failed`, readable, never stuck at
  `Interrupting`) plus a success-path control test and a
  wrong-starting-state-rejected test.

**Two test-design bugs caught and fixed in this node's own first draft**
(not the implementation, consistent with this role's established
practice of treating a first-run test failure as "my assertion may encode
a wrong mental model" before assuming a real bug):
1. An over-strict "no earlier step's event can ever re-fire" assertion in
   the crash-sweep test failed on `Validating->Resuming`, because
   `EventResumeValid` legitimately has TWO edges in the transition table
   (`WakePending->Validating` and `Validating->Resuming`,
   `statemachine.go`) — re-derived the correct invariant (the reconciled
   status must equal exactly the immediately-preceding step's own output,
   and never regress to a status from two-or-more steps back) instead.
2. `TestFullLifecycle_RepoOverlapBlocks_EndToEnd` asserted
   `domain.PauseBlockedConflict` is terminal — it is deliberately NOT
   (`statemachine.go`'s `terminalStates` set excludes it; ADD §20.9's
   manual-resolution UI reaches it via a documented `EventCancel` edge).
   Corrected the assertion to check the real property: no AUTOMATIC event
   has an edge from `BlockedConflict`, only the documented manual
   `Cancel`.

```yaml
node: runtime-a11
status: completed
artifacts:
  - internal/pause/interrupt.go (new: TurnInterrupterAdapter, InterruptAndSleep)
  - internal/pause/fulllifecycle_test.go (new: 12 test functions)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/app/wiring internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/pause/... ./internal/scheduler/... -race   # PASS (DAG's literal validation command)"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "golangci-lint run ./internal/pause/...   # 0 issues"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: 084d002
next_action: runtime-b09 (error contract + privacy gate audit) — same wave, done next
assumptions:
  - "TurnInterrupterAdapter/InterruptAndSleep are new production code in
    internal/pause (this role's own exclusive path), not a widening of any
    frozen internal/app/ports.go interface — app.TurnInterrupter itself is
    untouched; the adapter satisfies pause.Interrupter (safepoint.go's own
    existing, narrower seam), exactly as that file's doc comment already
    anticipated a later node would do."
  - "This node's job (per the task brief) was to find and close GENUINE
    gaps only, not manufacture busywork — of the 9 required tests audited,
    5 were already fully proven and needed no new code; 3 needed a fuller
    lifecycle composition (new tests, no new production code, and no bugs
    found); exactly 1 (provider interrupt failure) needed new production
    code because no prior node had actually wired that call path."
blockers: []
```

### runtime-b09: uniform error contract + privacy gate audit across all P0 commands

A dedicated research agent audited every P0 command's error path, success
path, schema-versioning, and privacy handling against `agents/runtime.md`'s
"JSON and errors" contract before any code was written, with file:line
precision. Findings:

- Every real (non-stub) command (`checkpoint create`, `decision
  allow`/`deny`, `pause request`/`cancel`, `resume`, `scheduler run-once`,
  `status`, `doctor`) already constructed a `*domain.Error` internally on
  every error path, and already emitted its own schema-versioned JSON on
  success. This part of the contract was already correct — confirmed, not
  assumed.
- **THE genuine, fixable gap**: no command's typed error was ever
  serialized to JSON anywhere. Every command built the right typed Go
  value, but Cobra's own default error printer (`SilenceErrors: false`)
  flattened it to a bare `.Error()` plain-text line on stderr —
  `internal/cli/errors.go` had exactly one helper (`notImplemented`)
  before this node, no JSON-rendering path at all.
- **Fix**: `internal/cli/errors.go` gained `SchemaVersionError`
  (`"preflight.error.v1"`), `RenderErrorJSON` (any error -> the frozen
  envelope, degrading a non-`*domain.Error` to `ErrCodeInternal` rather
  than producing nothing), and `WithJSONErrorRendering` (walks a command
  tree, wraps every leaf's `RunE` to ALSO write the JSON envelope to
  stderr, returning the original error UNCHANGED so every existing
  `errors.As` caller/test keeps working — purely additive). Wired into
  both `cli.NewRootCmd()` and `internal/app/wiring.App.RootCmd()` (the
  latter re-applies it after every `replaceSubcommand` swap, since a
  freshly-built real subtree is unwrapped; an `Annotations` marker makes
  re-wrapping an already-wrapped leaf a safe no-op, so calling it twice
  never double-writes the envelope).
- **Real bug caught by this node's own test, not assumed away**: keeping
  `SilenceErrors: false` (an early draft's "purely additive, don't change
  existing behavior" instinct) directly violated "machine mode never
  emits decorative text" — Cobra's own plain-text line still printed
  AFTER the new JSON envelope on every single command, caught by
  `TestErrorContract_NoDecorativeTextOnAnyCommand` failing across the
  entire command tree on first run. Fix: `SilenceErrors: true` in
  `root.go` — the JSON envelope is a strictly better replacement, not an
  addition alongside Cobra's own text.
- **Known, pre-existing gaps, now documented as explicit checked tests**
  (not silently fixed, since fixing them is a bigger design call than
  this node's mandate): `init`/`evaluate`/`progress show`/`state show`
  have no real CLI constructor anywhere in the repository (permanent
  `notImplemented` stubs, confirmed by the research pass) —
  `TestErrorContract_KnownIncompleteCommands_AreStubsOnly` fails loudly if
  a future node adds a real one without updating this note.
  `version`'s success output is a bare string, not schema-versioned JSON
  — changing an already-integrated command's output shape was judged out
  of this audit's scope (Constitution §7 rule 10: no speculative changes
  beyond scope) — `TestErrorContract_VersionCommand_KnownGap_PlainStringNotJSON`
  documents this explicitly.
- `internal/httpapi` does not exist anywhere in the repository (confirmed:
  no directory, no files) — an explicit ADD/`agents/runtime.md` stretch
  goal not yet built ("HTTP daemon is secondary to a working CLI"). The
  DAG's validation command names
  `go test ./internal/httpapi/... ./internal/cli/... -run ErrorContract`;
  running it confirms the combination fails (exit 1) purely because
  `internal/httpapi` has no directory to `go test`, while
  `go test ./internal/cli/... -run ErrorContract` alone passes cleanly —
  this is the documented no-op the task brief anticipated, not a real gap,
  and no placeholder package was built to paper over it.
- **Privacy gate**: every command touching prompt-adjacent data
  (`decision allow`/`deny`'s `--prompt-hash`, hook `user-prompt-submit`)
  only ever threads a `PromptHash` (already a hash by the time it reaches
  this layer, `internal/app/ports.go`'s frozen field), never raw prompt
  text — confirmed by a grep audit (zero hits for any raw-prompt field
  crossing `internal/cli`/`internal/orchestrator`/`internal/app/wiring`)
  and proven directly by
  `TestErrorContract_NoRawPromptInAnyErrorOrOutput` (a canary-string sweep
  across all 18 P0 command paths) and
  `TestErrorContract_DecisionAllow_RealPath_NeverEchoesPromptHashAsRawText`
  (a real, not-stub, issue-flow test proving the canary never leaks into
  `decision allow`'s own JSON output, which — correctly — has no
  `prompt_hash` field to begin with).

```yaml
node: runtime-b09
status: completed
artifacts:
  - internal/cli/errorcontract_test.go (new: 10 test functions)
  - internal/cli/errors.go (SchemaVersionError, RenderErrorJSON, WithJSONErrorRendering)
  - internal/cli/root.go (SilenceErrors: true; wires WithJSONErrorRendering)
  - internal/app/wiring/wiring.go (re-applies WithJSONErrorRendering after replaceSubcommand)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/app/wiring internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/cli/... -run ErrorContract -v   # all PASS (httpapi half of the DAG's combined command is a confirmed no-op, not a real target — internal/httpapi does not exist)"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "golangci-lint run ./...   # 0 issues, whole repo"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: ad335b2
next_action: runtime-b10 (this role's final node) — NOT this wave
assumptions:
  - "internal/httpapi is out of vertical-slice scope per agents/runtime.md's own
    stretch-goal framing; the DAG's validation command referencing it is
    a documented no-op for this node, not a gap this node closes by
    building a placeholder package."
  - "WithJSONErrorRendering's returned Go error is intentionally UNCHANGED
    from today's behavior — every existing test that asserts on the
    returned error (errors_test.go, root_test.go, wiring_test.go) needed
    zero changes; the JSON envelope write to stderr is the only new
    behavior, confirmed by running the full existing suite unmodified
    after this change and seeing zero regressions."
  - "version's plain-string output and init/evaluate/progress-show/
    state-show's permanent-stub status are DOCUMENTED gaps, not silently
    fixed ones — both are checked by a dedicated test that fails loudly
    the moment either changes, per Constitution §6's evidence discipline
    applied to a known-gap claim, not just a completed-node claim."
blockers: []
```

## Wave 10 cross-node observations

- Both nodes were validated and committed independently, per the explicit
  task instruction — no batching. Each node's own DAG validation command
  was run and confirmed (or, for `runtime-b09`, confirmed as a documented
  no-op for the `internal/httpapi` half) before moving to the next; the
  full owned-package test suite plus a whole-repo `go build`/`go test`
  was additionally run after every single node, not just at the end of
  the wave.
- Both nodes are the same SHAPE as `checkpoint-a09`/`checkpoint-b09`/
  `predictor-11` last wave: comprehensive final-proof/audit nodes, not
  new-feature nodes. Both were explicitly instructed to report precisely
  where nothing new was found rather than manufacturing busywork — this
  wave, `runtime-a11` found exactly one genuine production-code gap
  (provider-interrupt-failure state-machine wiring) out of nine required
  tests audited, and `runtime-b09` found exactly one genuine, fixable gap
  (no JSON error-rendering layer) plus two pre-existing, out-of-scope
  gaps it documented rather than silently fixed.
- Both nodes' own test-writing caught a real bug in THIS SAME NODE'S first
  draft, not in prior-wave code — consistent with (but distinct from)
  `runtime-a09`'s Wave 9 precedent of finding a bug in an EARLIER node's
  code. `runtime-a11`'s crash-sweep test and `BlockedConflict`-terminal
  assertion were both corrected against `statemachine.go`'s actual
  transition table; `runtime-b09`'s `SilenceErrors: false` "purely
  additive" instinct was corrected to `true` once its own new
  decorative-text test caught Cobra's plain-text line still printing
  alongside the new JSON envelope. Both are instances of this role's
  established technique (Wave 9 lessons_learned): when a just-written test
  fails reliably on first run, default to "my assertion/design encodes a
  wrong mental model," verify directly, then fix precisely — never assume
  the first hypothesis (bug vs. test-modeling error vs. design error)
  without checking.
- No new ADRs, no cross-role change-request escalations, no frozen-contract
  questions this wave. `internal/app/ports.go`, `internal/domain/**`, and
  every other role's owned packages were called, never modified.
  `internal/pause/interrupt.go`'s new `TurnInterrupterAdapter` satisfies
  `pause.Interrupter` (this package's own internal seam, not a frozen
  port) exactly as `safepoint.go` already anticipated; no interface in
  `internal/app/ports.go` was widened or touched.
- `runtime-b10` (this role's final node) remains for a future wave, per
  the task's explicit instruction not to start it now.

# Wave 11

Branch: `vertical-slice/runtime`, synced from `main` via `git fetch origin && git
merge origin/main` (fast-forward, clean — no conflicts) before any Wave 11
work, landing at `b7346a4` (Wave 10 fully integrated). Assigned node:
`runtime-b10` — this role's FINAL vertical-slice node, per the task brief's explicit
statement that no further node exists for this role after this one.

## runtime-b10 — In-process restart, same SQLite file (final Part B gate)

### What this node had to prove, per its own DAG risk callout

`EXECUTION_DAG.md` names this node's risk explicitly: "includes
in-process-restart-same-SQLite-file test" — and states it is the gate for
`qa-02`'s E2E demo and `qa-03`'s dedicated `RestartSameDB` integration
test. The task brief's own four numbered requirements: (1) a full
`wiring.Services`/`App` built against a real on-disk file, run through a
realistic command sequence, discarded, and a brand-new instance built
against the SAME file proven to see all prior state and keep operating;
(2) the same guarantee holding even when the prior process's connection
was NOT cleanly closed (a genuine crash, not a graceful shutdown); (3)
building this strong enough that `qa` can build on top of it with
confidence rather than starting from scratch; (4) auditing agents/
runtime.md Part B's "Tests" list for any genuine remaining gap not already
covered by `b01`-`b09`, closing exactly what's real and nothing more.

### Two real, pre-existing production/test gaps found and closed — not
### manufactured busywork

Before writing any restart-proof code, this node researched (directly,
plus a background research agent cross-check that independently confirmed
the same findings) which `wiring.Services` fields have real, non-fake
implementations anywhere in the repository. Confirmed: `StateCheckpoint`
(`internal/statecheckpoint.Service`), `RepositoryCheckpoint`
(`internal/repocheckpoint.Service`), and `Evaluation`+`AuthorizationIssuer`
(`internal/evaluation.Service`) are all real and SQLite-backed. Two are
NOT: `ProgressTreeService` has no unified adapter anywhere (only
`internal/testutil/fakes.FakeProgressTreeService` — `checkpoint` role's own
gap, not this role's to close) and `GracefulPauseService` likewise has no
adapter over `internal/pause`'s free functions (a real gap in this role's
OWN Part A, but building a six-method adapter bridging `Observe`/
`RequestPause`/`ReachSafePoint`/`EnterSleep`/`Resume`/`Cancel` onto already-
existing, differently-shaped free functions is a substantial new
production feature, not a restart-safety proof — explicitly out of this
node's scope, and no P0 command this role owns actually calls
`GracefulPauseService` today; the CLI's `pause`/`resume`/`scheduler`
commands reach the pause subsystem through the narrower
`orchestrator.PauseLifecycleDeps.Store` seam instead).

That same research surfaced the ONE genuine, load-bearing, previously-
undiscovered gap this node closed with new production code:
**`pause.PauseStore` (the interface `PauseLifecycleDeps.Store` requires)
had no SQLite-backed implementation anywhere — only `pause.MemStore`, an
in-memory reference/test double.** Five prior Part A nodes across four
earlier waves (`runtime-a04`, `a05`, `a07`, `a09`) had each independently
noted this same gap in their own doc comments and deliberately deferred it
("a future integration node reconciles `PersistPauseStore` onto a real
SQLite-backed `PauseStore`," `persistphase_test.go`'s own
`seedPauseRecordRow` comment). Proving restart-safety for `pause request`/
`pause cancel`/`resume` while they still ran against an in-memory store
would have been dishonest — `MemStore` is discarded the instant its owning
`App` is, by construction, so no restart test built against it could prove
anything real. **Fix**: `internal/pause/sqlitestore.go`'s new
`SQLiteStore`, a real `PauseStore` implementation against the
already-existing `pause_records` table (migration 0050, this role's own
range) — `FindActiveByKey`/`Insert`/`GetByID`/`UpdateStatus` plus
`CompareAndSwapStatus` via the identical conditional-`UPDATE...WHERE`
idiom `internal/scheduler.Store.Complete`/`Fail`/`Renew` already
established for `wake_jobs`. Deliberately scoped to `PauseStore` only, NOT
`PersistPauseStore` (`GetProgress`/`SaveProgress`, `runtime-a05`'s own
narrower five-step-persist-phase interface) — reconciling that one remains
the same already-tracked, still-open gap it always was; widening this
node's scope to close it too would have been scope creep beyond "prove
restart safety of what exists" into a new persist-phase feature nobody
asked this node to build.

The second gap, per the task's item 4 audit: agents/runtime.md Part B's
"Tests" list names 9 items; 7 are already thoroughly covered by `b01`-`b09`
(confirmed directly, file:line, before concluding "no gap" — not assumed).
"process exit codes" is provably the same signal already exercised 38+
times across this package's own tests (`Execute()`'s returned error, which
`cmd/preflight/main.go` — not this role's path — mechanically converts to
`os.Exit(1)`); "no-TTY behavior" is structurally guaranteed already (zero
TTY-detection code exists anywhere in `internal/cli`/`internal/orchestrator`
— confirmed by grep — and every single existing test already drives every
command through a non-TTY `bytes.Buffer`). **"CLI golden tests" is a real,
previously-unclosed gap**: every existing success-path test decodes JSON
into a `map[string]any` and checks individual keys — an accidentally
added, removed, renamed, or reordered field in a real command's output
would pass every existing test silently. `claude-provider` already
established this exact convention for its own package
(`internal/hooks/claude/testdata/*.golden.json`,
`userpromptsubmit_test.go`'s `assertJSONEqual`) — this role's own CLI
surface had no equivalent. **Fix**: `internal/cli/golden_test.go` + three
new fixtures under `internal/cli/testdata/golden/` (this role's own
exclusive path), covering `checkpoint create` (nested two-service result),
`decision allow` issue flow (conditional-field result), and `doctor`
(slice-of-results, the shape most likely to silently gain/lose an
element) — structural (`reflect.DeepEqual` on decoded JSON, not literal
byte comparison, so insignificant formatting differences never cause
spurious failures) comparison against checked-in fixtures, with a
`PREFLIGHT_UPDATE_GOLDEN=1` escape hatch for a deliberate future output-
shape change (mirrors every golden-file testing setup's standard
convention). Verified the comparison actually catches a real regression
(temporarily corrupted a fixture, confirmed the test failed with a clear
diff, restored it) before considering this closed.

### The in-process-restart-same-SQLite-file test design, in full

`internal/app/wiring/restart_test.go`, two tests:

1. **`TestRestart_SameSQLiteFile_FullLifecycleSurvivesProcessRestart`**
   (the literal required test). Builds a real on-disk SQLite file (never
   `:memory:`), a real temporary Git repository, and a full `wiring.App`
   wired against real `StateCheckpoint`/`RepositoryCheckpoint`/
   `Evaluation`+`AuthorizationIssuer`/`PauseLifecycle` (this node's new
   `SQLiteStore`)/`scheduler.Store`/`Diagnostics` (fake only for
   `ProgressTree`/`GracefulPause`, per the documented, non-dishonest scope
   boundary above). Drives, through the ACTUAL cobra command tree
   (`App.RootCmd()`, freshly built per command call — see below for why),
   a realistic sequence: `checkpoint create` -> real `EvaluateTurn`+
   `Decide` -> `decision allow` issue flow -> `decision allow` consume
   flow -> `pause request` -> schedule+claim a real wake job -> `doctor`.
   Then closes that `*sqlite.DB` entirely and constructs a BRAND NEW
   `wiring.App`/`*sqlite.DB` pair against the SAME file path — proving,
   via the post-restart `App`'s own real commands (never by reading the DB
   directly, which would only prove the storage layer, already covered
   elsewhere): (a) `CurrentVersion` is identical before/after re-`Migrate`
   (no double-migration); (b) the pre-restart authorization, replayed
   post-restart, is still rejected (exactly-once consumption durable
   across a full App/DB rebuild, not merely an in-process invariant); (c)
   the pre-restart pause record is still readable AND mutable (`pause
   cancel` succeeds — proves no orphaned lock on `pause_records`); (d) a
   brand new wake job can be scheduled AND claimed post-restart (proves
   the scheduler's write+lock-acquire path, not just reads); (e) a wholly
   fresh `checkpoint create` and a wholly fresh `decision allow`
   issue-then-consume-then-replay-rejected cycle both succeed post-restart
   (proves every write path, not just previously-committed reads, is live
   again).

2. **`TestRestart_SameSQLiteFile_UncleanShutdown_UncommittedWriteDoesNotCorruptFile`**
   (requirement 2 — "even if the old process's SQLite connection wasn't
   cleanly closed"). This test's OWN FIRST DRAFT tried two same-process
   simulations (abandoning a `*sql.Tx` borrowed from the App's own pool
   before calling `db.Close()`; then a second, wholly separate
   `*sqlite.DB` simply never `Close()`d) and BOTH genuinely failed with a
   real `SQLITE_BUSY` on the very next `Migrate()` call — traced to a real
   Go-level fact, not a storage-layer bug: `database/sql.DB.Close()` is
   documented to "wait for all queries that have started processing... to
   finish" rather than force-closing an abandoned transaction, and
   `sql.Conn.Close()` on a connection with an open `*sql.Tx` outright
   DEADLOCKS (verified directly with a throwaway, built-and-deleted
   experiment before concluding this, not assumed). A same-process
   simulation cannot faithfully reproduce a real OS-level process death
   using `database/sql`'s public API alone — a genuinely different failure
   mode than any prior wave's node has hit. **Fix**: the standard Go
   idiom for exactly this situation — the test binary re-executes ITSELF
   as a real child OS process (`os/exec`, `os.Args[0]`,
   `-test.run=^TestZZZCrashWriterHelper$`), that child opens the same
   on-disk file, begins a real write transaction, executes it, signals
   readiness over stdout, then blocks; the parent sends a real `SIGKILL`
   once it reads the readiness signal. The OS — not this package's own
   bookkeeping — reclaims the child's file descriptors and whatever
   SQLite-level lock state they held, exactly as a genuine crash would.
   The surviving (parent) process then opens a fresh connection to the
   same file and proves: `Migrate` still succeeds (no BUSY, no hang); the
   killed writer's uncommitted UPDATE did NOT apply (`pause cancel`
   against the same pause ID succeeds, proving the record reads back at
   its pre-crash status, not the abandoned write's target status); a
   fresh `doctor` and a fresh `checkpoint create` both succeed (proves no
   residual lock survives into the new connection). `TestZZZCrashWriterHelper`
   self-skips under any normal `go test` invocation (gated on an env var
   the parent test alone sets), so it never pollutes or is counted as a
   real test in a normal run.

Both tests re-run 3-5x under `-race` with zero flakes before being
considered done (Wave 6's "process CPU time vs wall-clock" hang-diagnosis
technique and Wave 9's "temporary in-source instrumentation, confirmed
then deleted" technique were both reused during the crash-writer
investigation — a throwaway `busytest` experiment package, built inside
`internal/app/wiring` — the only path where such an experiment could
legally live — confirmed the `sql.Conn.Close()` deadlock precisely before
this node concluded a real subprocess was necessary, then was deleted
before commit, never left in the diff).

### Design note: a fresh `App.RootCmd()` per command call, not one reused tree

`execCmd`/`execCmdExpectError` (the test's own drive helpers) build a
BRAND NEW `a.RootCmd()` for every single command invocation, rather than
reusing one `*cobra.Command` tree across multiple `Execute()` calls —
this file's own first draft did the latter and found a real test-design
bug: cobra's `StringVar`-bound flag variables are captured once when a
command tree is built, so a flag OMITTED on a later `Execute()` call
against the SAME tree silently keeps whatever value an EARLIER call left
it at, rather than resetting to its default. A `decision allow` issue-flow
call that ran after an earlier `decision allow ... --authorization-id X`
call on the same reused tree was silently routed into the CONSUME flow
instead, because the stale flag value was still set. Building a fresh
`a.RootCmd()` per call sidesteps this entirely and is also the more
faithful restart-safety proof: every real invocation of the `preflight`
binary is its own fresh process building its own fresh cobra tree exactly
once, so a test that reuses one tree across calls is testing something the
real binary never actually does.

### Node log

```yaml
node: runtime-b10
status: completed
artifacts:
  - internal/pause/sqlitestore.go (new: SQLiteStore, a real PauseStore)
  - internal/pause/sqlitestore_test.go (new: unit + concurrent-CAS proof)
  - internal/app/wiring/restart_test.go (new: the two required restart tests)
  - internal/cli/golden_test.go (new: closes the "CLI golden tests" gap)
  - internal/cli/testdata/golden/checkpoint_create_success.golden.json (new)
  - internal/cli/testdata/golden/decision_allow_issue_success.golden.json (new)
  - internal/cli/testdata/golden/doctor_all_skipped_success.golden.json (new)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/pause internal/scheduler internal/app/wiring   # empty"
  - "go build ./...   # OK, whole repo"
  - "go vet ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/cli/... ./internal/orchestrator/... -race -v   # all PASS"
  - "go test ./internal/app/wiring/... ./internal/pause/... -race -v   # all PASS, including both restart tests and the new SQLiteStore suite"
  - "go build ./... && go test ./... -race   # all PASS, whole repo, zero regressions"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <recorded below>
next_action: NONE — this was runtime's FINAL assigned DAG node. Every node ever assigned to this role (a01-a11, b01-b10) is now complete.
assumptions:
  - "SQLiteStore implements PauseStore only, not PersistPauseStore — the
    latter (runtime-a05's own narrower persist-phase progress-bookkeeping
    interface) remains the same already-tracked, still-open gap it was
    before this node; reconciling it is out of this node's scope (proving
    restart safety of what exists, not building a new persist-phase
    feature)."
  - "GracefulPauseService and ProgressTreeService remain fake in this
    node's restart harness — neither has a real adapter anywhere in the
    repository (confirmed directly + via independent background-agent
    cross-check), and no P0 command this role owns calls
    GracefulPauseService at all (pause/resume/scheduler commands reach
    internal/pause through the narrower PauseLifecycleDeps.Store seam
    instead) — documented explicitly as a non-dishonest scope boundary,
    not silently papered over."
  - "The crash-writer subprocess technique (TestZZZCrashWriterHelper,
    os.Args[0] re-exec + SIGKILL) is the standard Go idiom for testing
    real process-crash recovery and was adopted only after two same-process
    simulation attempts were tried and found to give false results (a
    database/sql-level artifact, not a storage-layer bug) — documented in
    both the code comments and this artifact so a future reader does not
    have to rediscover why the simpler approach doesn't work."
  - "internal/httpapi, internal/daemon remain out of vertical-slice scope, unchanged
    from every prior wave's same observation — no code added there this
    node, consistent with agents/runtime.md's own stretch-goal framing."
blockers: []
```

## Wave 11 cross-node observations — and full role retrospective

Since this is `runtime`'s final vertical-slice node, this section closes out both
the wave and the entire role arc (Bootstrap was lead-only, per
`CONTRACT_FREEZE.md`; this role's own history runs Wave 3 through Wave 11,
21 total DAG nodes: `a01`-`a11`, `b01`-`b10`).

**This wave's own two findings**: consistent with every prior "final
gate"-shaped node this role has shipped (`runtime-a11`, `runtime-b09` in
Wave 10; `checkpoint-a09`/`checkpoint-b09`/`predictor-11` in the
cross-role Wave 10 pattern already noted), a comprehensive proof node
found approximately one real, genuine gap per sub-area audited — one in
Part A (`pause.PauseStore`'s missing SQLite backing, closed with new
production code) and one in Part B's own Tests checklist ("CLI golden
tests," closed with new test infrastructure) — never zero, never many.
This is now the fourth consecutive "final gate" node (`checkpoint-a09`/
`b09`/`predictor-11` in Wave 10 cross-role, `runtime-a11`/`b09` also Wave
10, now `runtime-b10` Wave 11) to land on exactly this shape, which is
strong, repeated evidence this pattern generalizes: a comprehensive
audit-then-close node in this project reliably surfaces a small, real,
countable number of genuine gaps — never a sign the underlying nodes were
built carelessly (they weren't; every gap found was already explicitly,
honestly documented by the node that deferred it), and never a sign
nothing was left to find (something always was, because true end-to-end
integration exercises interactions no single earlier node's narrower
scope could exercise).

**The crash-simulation lesson is this wave's one genuinely NEW technique**,
distinct from anything a prior wave's lessons_learned already named: Wave
5/9's "process CPU time vs wall-clock" (diagnosing a hang) and "temporary
in-source instrumentation, confirmed then deleted" (confirming a
hypothesis before trusting it) both apply here, but the actual FIX
required — re-executing the test binary as a real child process and
SIGKILLing it — is a new addition to this role's technique inventory,
worth naming explicitly for any FUTURE Preflight node (in this role or any
other) that needs to test real process-crash recovery rather than an
in-process approximation: **`database/sql`'s own connection-pool
bookkeeping makes an in-process crash simulation actively misleading, not
just weaker** — it can produce a FALSE positive failure (the abandoned-Tx
`SQLITE_BUSY`, an artifact of Go's own pool semantics, not the storage
layer under test) that looks exactly like a real crash-recovery bug until
traced to its actual source. Any future crash-recovery test in this
codebase should default to the subprocess+SIGKILL technique from the
start, not discover the same false-positive the hard way.

**Full role retrospective (Wave 3 through Wave 11, 21 nodes)**:

- **Sequencing discipline held for the entire arc.** Every wave that
  contained both Part A and Part B work sequenced Part A first
  (state-machine/concurrency-correctness risk) ahead of Part B
  (comparatively lower-risk plumbing built ON TOP of Part A), exactly as
  Wave 5 first established and every subsequent wave (6, 7, 9, 10)
  followed without deviation. This wave's own node (`b10`, pure Part B)
  is the natural capstone of that discipline: by the time Part B needed a
  final integration gate, Part A had already had 11 of its own nodes'
  worth of scrutiny (culminating in `runtime-a11`'s own full-lifecycle
  proof, Wave 10), so this node's real remaining risk was concentrated
  almost entirely in Part B's own plumbing plus the ONE cross-cutting gap
  (`PauseStore`'s SQLite backing) that had been honestly deferred, not
  hidden, since Wave 6.
- **The single most-repeated, most-validated technical lesson across the
  whole arc**: treat a just-written test's first-run failure as "my own
  assertion, design, or simulation technique may encode a wrong mental
  model" before assuming either "the system under test has a bug" or "the
  test is simply flaky" — and always get DIRECT evidence (a throwaway
  debug instrumentation, a process-state check, an isolated repro) before
  committing to any of the three explanations. This was independently
  re-derived and correctly applied in `runtime-a06` (Wave 5, a genuine
  self-deadlock bug), `runtime-a09` (Wave 9, a genuine TOCTOU race PLUS a
  wrong test assertion, back to back, correctly told apart), `runtime-a11`
  (Wave 10, two wrong test assertions, zero implementation bugs),
  `runtime-b09` (Wave 10, a genuine design-choice bug, not a test bug),
  and now `runtime-b10` (Wave 11, a wrong SIMULATION TECHNIQUE — a new
  sub-case none of the prior five instances were, since the bug was in
  neither the test's assertion nor the system under test, but in an
  invalid same-process crash-simulation methodology). Five distinct
  sub-cases of the same underlying discipline, each correctly diagnosed,
  none guessed.
- **The "comprehensive audit-then-close finds ~1 real gap" pattern**,
  first clearly named in Wave 10's cross-node observations, held a third
  and fourth time this wave (Part A's `PauseStore` gap, Part B's golden-
  test gap) — now confirmed across five total instances spanning two
  waves and, cross-role, at least three other roles' own equivalent final
  nodes. This is the arc's strongest piece of process evidence: Preflight's
  per-node, per-wave documented-gap discipline (Constitution §4.4's
  "request through the progress artifact, don't wait idle" + this role's
  own consistent practice of naming a deferred gap explicitly rather than
  silently skipping it) is what MADE this pattern possible — every gap
  this node and its Wave 10 predecessors found was already written down
  by an earlier node, findable, not undiscoverable technical debt.
- **No new ADRs were ever required across the entire 21-node arc**, and
  cross-role change requests were rare and small (Wave 4's foundation
  migrate_test.go staleness, resolved before Wave 5 began; no others).
  Every real design judgment call this role made across 21 nodes — the
  pause-record two-backing-stores gap, `PauseStore`'s scope boundary
  (internal seam vs. frozen port), the `CompareAndSwapStatus` primitive,
  the two-channel CheckResult-vs-error split, `SilenceErrors`, and now
  this node's SQLiteStore scope boundary and crash-simulation technique —
  was resolvable directly from already-frozen documents (Constitution
  §§2/6/7, `CONTRACT_FREEZE.md`, the relevant ADD sections) without
  escalation, a direct, load-bearing consequence of `agents/runtime.md`
  and the frozen contract being genuinely sufficient source material for
  an agent picking up this role fresh in any given wave, including this
  final one.
- **This role's own file-count-undercounting observation (first flagged
  Wave 5, reconfirmed Waves 7/9/10)** — that any Part-B-shaped node
  spanning orchestrator-logic + CLI-command + wiring-integration runs
  roughly 2-3x the DAG's naive per-node file estimate — did NOT recur this
  wave in the same shape, because `runtime-b10` deliberately added no new
  CLI command surface (it PROVED existing ones, plus one small new
  storage-layer file) — worth noting as the one wave where that
  particular estimation pattern did not apply, because the node's actual
  shape (integration-proof, not new-feature) was different in kind from
  every node the original observation was made against.

This closes `runtime`'s full vertical-slice DAG scope. No further node remains
assigned to this role.

---

# Final integration gate corrective addition — `pause.Service`

Branch: `day1/runtime`, synced from `main` via `git fetch origin && git
merge origin/main` (fast-forward, clean) before this work, bringing in
Wave 12 (`qa`'s final wave, plus the `LICENSE` file). This is **not a
numbered DAG node** — every DAG node ever assigned to this role (`a01`-
`a11`, `b01`-`b10`) was already complete as of Wave 11's commit `ef1c43d`.
This is a lead-identified finding from the Final integration gate review
(`contract-integrator-final`), routed to this role because it is squarely
this role's exclusive-path work, per the same principle Constitution §4
uses for ordinary DAG assignment (a role's own exclusive paths are where a
gap in that role's own contract gets closed, regardless of which review
pass surfaced it).

## What was missing, and why no prior node closed it

`internal/app/ports.go` freezes a six-method `GracefulPauseService`
interface (`Observe`, `RequestPause`, `ReachSafePoint`, `EnterSleep`,
`Resume`, `Cancel`). Every one of Part A's eleven nodes built and
exhaustively tested a real piece of the pause/resume machinery this
interface describes — the state transition validator (`a02`), `Observer`'s
debounce/hysteresis (`a03`), `RequestPause`'s idempotency (`a04`), the
safe-point coordinator and `PersistThenInterrupt` ordering (`a04`), the
five-phase `Persist` orchestrator (`a05`), the durable scheduler (`a06`/
`a07`), resume validation (`a08`), `Wake`/`Cancel`/`Resume`'s
compare-and-swap exactly-once discipline (`a09`), `InterruptAndSleep`
(`a11`), and a real `SQLiteStore` (`b10`) — but **no single node was ever
tasked with assembling all of them into one concrete type satisfying the
frozen interface's exact six-method shape.** `runtime-b10`'s own doc
comment (Wave 11 section above) explicitly named this as a real gap and
explicitly scoped it out: "building a six-method adapter bridging
`Observe`/`RequestPause`/`ReachSafePoint`/`EnterSleep`/`Resume`/`Cancel`
onto already-existing, differently-shaped free functions is a substantial
new production feature, not a restart-safety proof — explicitly out of
this node's scope." That deferred gap is exactly what the Final
integration gate review caught: `grep -rn "var _ app.GracefulPauseService"`
across the whole repository, before this addition, found only
`internal/testutil/fakes.FakeGracefulPauseService` (a test double) and one
inline test-local satisfaction — never a real, production implementation.
This is also the reason `cmd/preflight/main.go` was never wired to real
services: the application root cannot compose against a frozen port with
no concrete implementation, for any port.

## What this addition is — composition and DTO-shape translation only

`internal/pause.Service` (`internal/pause/service.go`) is the real,
concrete type. It reimplements no business logic: every state transition,
every debounce/hysteresis rule, every phase of `Persist`'s five-step
sequencing, every compare-and-swap race-safety guarantee, and every one of
`ValidateResume`'s four checks are called through unchanged from their
existing, already-tested implementations. `Service`'s own job is (a)
sequencing calls to those pieces in the right order per method, and (b)
translating between the frozen `app.*` DTOs (deliberately narrow —
`PauseRequest{SessionID, Reason}`, `SafePoint{PauseID, At}`,
`ResumeRequest{PauseID}`) and this package's own richer internal shapes
(`PauseKey{TaskID, SessionID}`, `RequestPauseRequest`,
`ResumeValidationRequest`, etc.).

Composing against the frozen shapes surfaced two genuine gaps that no
individual prior node could have found, because satisfying the full
six-method interface in one place is the first time anything in this role
needed every piece at once:

1. **`app.PauseRequest` carries no `TaskID`, `WorktreeID`, or paused-work
   file set** — only `{SessionID, Reason}`. This package's own `PauseKey`
   requires both `TaskID` and `SessionID`, and `Persist` additionally
   requires a `WorktreeID`. A repo-wide investigation (direct, plus an
   independent background research agent cross-check, mirroring
   `runtime-b10`'s own dual-verification technique) confirmed no frozen
   port anywhere resolves a `SessionID` to its active `TaskID`/`WorktreeID`
   — `internal/cli/pause.go`'s own doc comment independently names the
   identical gap for its own, differently-shaped CLI flags ("no resolver
   port exists yet"). `Service` closes this with its own narrow,
   explicitly-documented seam, `SessionContextResolver`, declared in
   `internal/pause` (not `internal/app/ports.go` — this role cannot freeze
   a new cross-component contract unilaterally) and left for a future
   wiring node with access to the `tasks`/`provider_sessions` tables (owned
   by other roles' exclusive paths) to implement for real. This mirrors
   `resumevalidation.go`'s own `QuotaSnapshotReader`/`RepoFingerprintReader`/
   `SessionCapabilityReader` precedent exactly.
2. **`PersistPauseStore` (`GetProgress`/`SaveProgress`) was never
   reconciled onto `SQLiteStore`** — `persistphase_test.go`'s own
   `seedPauseRecordRow` doc comment named this exact gap explicitly back in
   Wave 7 ("a future integration node reconciles `PersistPauseStore` onto a
   real SQLite-backed `PauseStore` against this same table"). This
   addition closes it: `SQLiteStore` (`internal/pause/sqlitestore.go`, this
   role's own exclusive path) now also implements `PersistPauseStore`,
   backed by `pause_records`' own `state_checkpoint_id`/
   `repository_checkpoint_id` columns (already present since migration
   0050) plus `metadata_json`'s existing free-form JSON slot (already used
   for `TriggerReason`) widened to a small, fixed four-key shape for the
   two boolean phase markers and the `WakeJobID` scalar. No new migration,
   no schema change — every field `PersistProgress` needs already had
   somewhere to live in the row `Insert` already creates. (One adjacent
   fix was required alongside this: `decodePauseMetadata`'s original
   strict-prefix/suffix match against the single-key `{"reason":"X"}` shape
   would have silently returned `""` the first time `SaveProgress` widened
   a row's `metadata_json` — caught before it shipped, `decodePauseMetadata`
   now uses the same general key-extractor `GetProgress` uses.)

Also required, and caught by `go vet` during this addition's own
validation pass (not by a test): the first draft of `NewService` took
`Service` itself by value, which copies `Service`'s embedded
`sync.Mutex`-guarded maps — a real bug `go vet`'s `copylocks` check exists
specifically to catch. Fixed by splitting `Service`'s plain-data
constructor input into its own `ServiceDeps` value type (no mutexes) and
having `NewService(deps ServiceDeps) *Service` build a fresh, heap-allocated
`Service` from it — `Service` itself is never copied after construction.

## Mapping each frozen method

- **`Observe`**: `internal/predictor/runway.Scorer.Score` produces the
  `domain.RunwayForecast` the frozen signature returns (the runway
  forecaster the ADD names as a separate concern from `Observer`'s
  consumption-side debounce/hysteresis — see `ports.go`'s own package
  comment on `RiskCombiner` not consuming `RunwayForecast`). Since the
  frozen `RuntimeObservation` carries only the current `QuotaObservation`
  sample, never history, and `runway.Scorer` is deliberately stateless
  (its own doc comment: "all history must be passed in via
  `ScoreRequest.Previous` by the caller"), `Service` tracks the most
  recent sample per `(SessionID, LimitID)` itself and supplies it as
  `Previous` on every call after the first. The resulting forecast is then
  fed through `Observer.Observe` for its side-effecting debounce/hysteresis
  bookkeeping; the resulting `ObserveDecision` (`Fire`/`Event`/`Reason`) is
  intentionally not surfaced (the frozen signature has no field for it) —
  a caller that needs to act on a fired trigger calls `RequestPause`
  separately, matching ADD §20.2/§20.3's framing of Observe as a
  continuous background recompute distinct from the request decision
  itself. `Observe` also remembers the latest quota sample per `SessionID`
  (regardless of `LimitID`) as `RequestPause`'s only available source for
  `ResumeValidationRequest.QuotaBaseline` later — the frozen
  `PauseRequest` itself carries no quota sample at all.
- **`RequestPause`**: resolves `SessionID` via `SessionContextResolver`
  (see gap 1 above), maps the frozen plain-string `Reason` onto this
  package's closed `TriggerReason` vocabulary, and delegates to the real,
  unchanged `pause.RequestPause` for the actual idempotent create-or-return
  logic — confirmed by test: a second call with the same `SessionID`/
  `Reason` returns the same `PauseID`, not a duplicate.
- **`ReachSafePoint`**: composes `PersistThenInterrupt`'s ordering
  guarantee (`safepoint.go`) with the real five-phase `Persist`
  (`persistphase.go`) as the `CheckpointPersister` half and
  `InterruptAndSleep` (`interrupt.go`, via `TurnInterrupterAdapter`) as the
  `Interrupter` half — so `Persist`'s five durable writes must fully
  succeed before the real `app.TurnInterrupter` is ever called, exactly
  per ADD §20.15. Also drives the pre-`Checkpointing` transitions
  (`Predicted`→`Requested`→`Quiescing`→`Checkpointing`) via the same
  `Apply`+`CompareAndSwapStatus` discipline `lifecycle.go` already
  established, since the frozen `SafePoint{PauseID, At}` DTO gives a
  caller no other hook to drive them. Confirmed by test both on the happy
  path (record reaches `Sleeping`, every Persist phase durably recorded)
  and the failure path (a `Persist` collaborator failing means the
  interrupter is never called and the record never advances past its
  pre-failure status).
- **`EnterSleep`**: by the time this method is reachable per the frozen
  state path, `ReachSafePoint` has already durably scheduled the wake job
  (`Persist`'s own phase 5) and driven the record to `Sleeping`
  (`InterruptAndSleep`) — so `EnterSleep`'s job is narrower than it might
  sound: report that already-scheduled `WakeJob`, looked up via
  `scheduler.Store.GetByPauseKind` (the same idempotent-recovery read
  `persistphase.go` already uses for its own crash-recovery path), not
  perform a fresh transition. Fails closed with a `TransitionError` if
  called before the record is actually `Sleeping`.
- **`Resume`**: runs the real `ValidateResume` checklist (quota, repository,
  session, authorization) BEFORE calling the state-machine `Resume`,
  mapping the outcome via `ResumeValidationResult.Verdict()` — exactly the
  composition `resumevalidation.go`'s own doc comment named as its
  documented gap since Wave 8 ("wiring a real check in is explicitly a08's
  job... **a future integration node's**"). Uses the `SessionContext` and
  `QuotaBaseline` `RequestPause`/`Observe` remembered for this `PauseID`.
  A quota-unsafe verdict additionally reschedules the underlying wake job
  via `RescheduleWakeJobOnQuotaUnsafe`. Confirmed by test on all three
  verdict paths: fully valid (reaches `Resumed`), quota-unsafe
  (reschedules to `Sleeping`, non-terminal), and repository-overlap
  (blocks at `BlockedConflict`).
- **`Cancel`**: the simplest mapping — the frozen signature is a bare
  `domain.PauseID` (no wrapper request), translated into
  `CancelRequest{PauseID}` and delegated to the real, unchanged
  `pause.Cancel`. Re-confirmed through the real `Service` (not just the
  bare function) that cancel still wins a race against a subsequent
  `Resume` call.

## Tests and validation

`internal/pause/service_test.go` adds the required
`var _ app.GracefulPauseService = (*pause.Service)(nil)` compile-time
assertion plus integration-style tests for each of the six methods,
reusing `fulllifecycle_test.go`'s own technique (drive the real
`Service` against a real, migrated SQLite DB via `openMigratedDB`/
`seedChain`, and the same `okQuotaReader`/`okRepoFingerprintReader`/
`okSessionReader`/`okEvaluations`/`fakeTurnInterrupter` doubles
`resumevalidation_test.go`/`fulllifecycle_test.go` already established)
rather than starting from scratch. Full validation, all green:

```text
gofmt -l internal/pause internal/scheduler         # clean
go build ./...                                     # clean
go vet ./internal/pause/...                         # clean (after the
                                                     #   ServiceDeps fix)
go test ./internal/pause/... -race -v               # all pass
go build ./... && go test ./... -race               # zero regressions,
                                                     #   whole repo
golangci-lint run ./...                             # 0 issues
```

## What remains explicitly out of scope

`SessionContextResolver` has no real implementation yet — that requires
the `tasks`/`provider_sessions` tables, owned by other roles' exclusive
paths, and is exactly the kind of capability gap Constitution §7 rule 3
requires be surfaced explicitly rather than silently assumed away. Wiring
a real `pause.Service` into `cmd/preflight/main.go`/
`internal/app/wiring/**` — including supplying a real
`SessionContextResolver` — is the lead's root-wiring integration work,
per this task's own explicit instruction, not this addition's.
