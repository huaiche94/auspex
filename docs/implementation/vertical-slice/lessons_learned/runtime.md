# Lessons Learned — runtime (Phase 3: runtime-b01)

> 🌐 English | [繁體中文](runtime.zh-TW.md)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-b01 | M | S/M — lighter than the M estimate suggested | 6 | 6 (doc.go, errors.go, errors_test.go, root.go, root_test.go, hook.go) | 350 LOC (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None — `internal/domain/errors.go`'s frozen `Error` shape and `internal/buildinfo.String()` were both already exactly sufficient; no new dependency or field was needed from a file this role doesn't own | None beyond the DAG's own 6-file estimate — `doc.go` carrying the naming-convention rationale (rather than a 7th standalone file) kept the count exactly at the estimate | None — the only real judgment call (kebab-case vs PascalCase hook subcommand casing, `ADR_Recommendations.md` REC-03) was pre-flagged by the task brief itself with an explicit answer to use, so it required documentation rather than open-ended research | An early draft of `root_test.go` added a defensive `var _ = cobra.Command{}` unused-import guard against a hypothetical future edit; caught immediately as unnecessary once the actual final import set was checked — cheap to avoid by writing the test table first and only importing what it needs, rather than importing defensively up front | Building every P0 command as a stub returning one shared `notImplemented(command string) error` helper, rather than N bespoke error constructions, made both the implementation and its test (`TestStubCommandsReturnNotImplemented`, table-driven over all 17 non-version leaf paths) nearly free — one assertion function, driven by the same path table used to assert tree registration. Recommend this "one typed-error constructor + one path table exercised by two different tests (tree-shape, stub-behavior)" pattern as the default whenever a future phase's node is a deliberate stub layer (e.g. if `checkpoint`/`predictor` ever need a similar honest-stub placeholder ahead of a dependency), rather than each stub command inventing its own ad hoc error value inline |

## Cross-node observations

- This was `runtime`'s first-ever node (no prior Phase 1/2 history to compare against, unlike
  `predictor`/`foundation`/`claude-provider`/`checkpoint`, all of which had earlier phases). The
  DAG's M/350-LOC/6-file estimate for `runtime-b01` proved accurate on file count and slightly
  generous on LOC (387 non-test / 561 total including tests) — consistent with the cross-role
  pattern already noted in `lessons_learned/predictor.md` that self-contained, dependency-light
  packages (no I/O, no concurrency, no cross-package wiring beyond already-frozen contracts) tend
  to land at or slightly under DAG estimates.
- The single most consequential non-code decision on this node was procedural, not technical:
  confirming *which* document was authoritative for hook-subcommand casing before writing any
  command `Use` strings, rather than picking one convention and discovering the conflict later via
  a failing review. Constitution §2's document priority order plus `ADR_Recommendations.md` REC-03
  together made this a five-minute lookup instead of a guess — worth noting as a positive case for
  keeping conflict history recorded in a findable place (`wave2-analysis/`) rather than only in
  chat/PR history that a fresh session (like this one, a first-ever assignment with no prior
  conversational context) cannot see.
- No blockers, unexpected dependencies, or scope surprises were severe enough to require raising a
  new ADR or deviating from the frozen contracts. `internal/domain/errors.go` and
  `internal/buildinfo` had everything needed without requesting an addition via this artifact.

# Lessons Learned — runtime (Phase 4: runtime-a01, runtime-b02)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a01 | S (DAG: 150 LOC, 3 files) | M — the SQL itself was S, but the forward-FK interaction with SQLite's cascade resolution turned a transcription task into a schema-design decision requiring an escalation write-up | 3 | 4 (3 .sql + 1 test file the DAG's validation command implies but the 3-file estimate didn't count) | 150 LOC | one continuous pass; the canonical-FK first draft was written, observed failing foundation's cascade tests, and rewritten — roughly doubling the node's effective LOC written vs. shipped | **Two undeclared ones.** (1) ADD §12.2's canonical `pause_records` FKs reference four tables from ranges 0010-0049 that don't exist yet; with `PRAGMA foreign_keys = ON`, SQLite resolves every parent on any DML *including cascades from repositories/worktrees/tasks*, so shipping canonical FKs breaks foundation's existing tests and all task-delete DML repo-wide. Resolved via the 0004_tasks.sql plain-pointer precedent, escalated in the progress artifact. (2) foundation's migrate_test.go asserts exact migration count/version (==4), so *any* role landing migrations breaks 3 of foundation's tests — change request filed, not self-fixed | migrations_0050_pause_test.go lives in internal/storage/sqlite/ (foundation's dir) — unavoidable given the DAG's validation command; ownership carve-out requested | foundation's 3 stale assertions leave `go test ./internal/storage/sqlite/...` (unfiltered) red until their mechanical fix lands; runtime's own validation command passes | The canonical-FK first draft was honest exploration, not waste — but the failure mode (cascade DML poisoning) could have been predicted by reading 0004_tasks.sql's active_node_id comment *before* writing SQL, which names the identical problem class | When a DAG assigns a role a migration range that FKs into another role's not-yet-landed range, the DAG row should say explicitly whether to (a) declare forward FKs, (b) plain-pointer like 0004, or (c) block on the parent range — every migration-owning role after foundation will hit this exact fork, and the answer materially changes both schema and test shape. Also: range-owned migration tests need a declared home; "tests live where the validation command points" should be written into the execution plan's shared-file policy |
| runtime-b02 | M (DAG: 300 LOC, 4 files) | S/M — pure composition plumbing against already-frozen interfaces; zero I/O, zero concurrency | 4 | 9 (wiring.go, wiring_test.go, + 7 fakes files: doc, unconfigured helper, one per service interface) — more files but similar total LOC; per-interface fake files chosen so qa can later evolve one service's double without touching the rest | 300 LOC | one continuous pass, no rework | None — internal/app/ports.go's five service interfaces were exactly sufficient as-written; no DTO field additions needed, no go.mod change (cobra already present) | The DAG's 4-file estimate implicitly assumed fakes would be fewer files; splitting per-interface was a deliberate structure choice, not scope growth | None | None worth noting — reading ports.go once, fully, before writing any fake avoided every signature mismatch a per-method copy-as-you-go approach would risk | The Fake<Interface> + <Method>Func + loud-unconfigured-error pattern (extending runtime-b01's "one typed-error constructor" lesson) made 5 interface doubles nearly mechanical; recommend it as the repo-wide default when qa or any role needs a double for a frozen port. Also: wiring.New's fail-closed nil-field validation caught its own test-writing typos twice — construction-time composition validation pays for itself immediately, keep it when real services replace fakes |

## Phase 4 cross-node observations

- The phase's single most expensive lesson was runtime-a01's: **a canonical schema is not
  automatically a shippable migration** when migration ranges land out of numeric order across
  roles. SQLite's forward-FK tolerance at CREATE time combined with strict whole-parent-set
  resolution at DML time means one role's "correct per the ADD" migration can silently break every
  other role's DML through cascade chains. The 0004_tasks.sql precedent (plain pointer + comment +
  escalation) turned out to be the general answer, and is now applied twice in this repo — it
  should probably be written down once, authoritatively (execution plan §7 or an ADR), instead of
  each role rediscovering it against a red test suite.
- Cross-role test coupling surfaced for the first time this phase: foundation's exact-count
  assertions over the shared AllMigrations() set broke on contact with any second role's files.
  Range-scoped assertions (each role asserts only its own 00X0-00X9 slice) is the obvious
  convention; filed as a change request rather than fixed in place, per Constitution §4.4.

# Lessons Learned — runtime (Phase 5: runtime-a02, runtime-a06, runtime-b03, runtime-b04, runtime-b05, runtime-b08)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a02 | L (DAG: 400 LOC, 4 files) | M — the transition table itself was mechanical once the state-name reconciliation across three documents was resolved; that reconciliation (not the code) was the real work | 4 | 3 (doc.go, statemachine.go, statemachine_test.go) | 400 LOC | one continuous pass, no rework needed | None new — CONTRACT_FREEZE.md's already-frozen 12-value domain.PauseStatus enum was exactly sufficient; the "dependency" was reconciling agents/runtime.md's prose path and ADD §20.5's diagram onto it, which is a reading/documentation task, not a code dependency | None beyond the DAG's 4-file estimate | None — Constitution §2's document priority order (frozen enum > prose) made the reconciliation a lookup, not a judgment call requiring escalation | None — reading CONTRACT_FREEZE.md's "Frozen state transitions" section fully before writing any table entry avoided inventing a 13th state, which the Constitution explicitly forbids | Recommend stating explicitly, in any future packet describing a state machine with multiple source documents (prose + diagram + frozen enum), which one wins on a naming mismatch — this node had to work that out from Constitution §2 first-principles rather than a packet-level pointer |
| runtime-a06 | L (DAG: 400 LOC, 4 files) | L — estimate held, but for a different reason than expected: the SQL/API surface was straightforward, while getting BEGIN IMMEDIATE's concurrency semantics actually correct through database/sql's connection pooling was the real difficulty, and cost two real bugs caught by the node's own tests before commit (see the progress artifact's dedicated section) | 4 | 3 (doc.go, lease.go, lease_test.go) | 400 LOC | one continuous pass, but with two full stop-diagnose-fix cycles mid-node (a hung `-race` test run killed after ~4 minutes wall clock, and a genuine test failure on first run) — see below | None — internal/storage/sqlite's existing WAL+busy_timeout pragmas and *sql.DB/*sql.Conn API were exactly sufficient; no new package or go.mod entry needed | None beyond the DAG's 4-file estimate | **Two real, self-caught bugs, not blockers from an external dependency**: (1) Claim's original re-fetch of a newly-claimed job went through the pooled *sql.DB while still holding its own reserved *sql.Conn open, which self-deadlocked every goroutine in database/sql's connection-wait queue once the pool (capped at 8) was saturated by concurrent Claim callers — caught by TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce hanging indefinitely (process had to be killed, not just a failing assertion). (2) Claim's original SELECT only matched status='scheduled', so an expired-but-still-leased row was invisible to it until a separate ReclaimExpired call ran first — caught by TestLease_ExpiredLeaseReclaimedByAnotherWorker failing outright on first run. Both fixed before any commit. | The hung-process diagnosis (checking `ps -o pid,stat,time` and noticing near-zero CPU time despite minutes of wall-clock time) was the key signal that distinguished "deadlock" from "just slow" — worth recording as a general debugging technique for any future `-race`-heavy concurrency node in this project, since a naive timeout-and-retry would have masked the real bug rather than exposing it | (1) Any node building a SQLite lease/lock primitive on top of database/sql's connection pool should budget explicit time for exactly this class of self-deadlock — a reserved *sql.Conn plus ANY code path that asks the pool for a second connection while the first is held is a latent hang, and it will not show up until concurrency tests actually saturate the pool. Recommend calling this out explicitly in agents/runtime.md or a shared "SQLite + Go patterns" note for any future role building similar primitives (e.g. checkpoint's own locking, if any). (2) When a DAG's validation command literally names a required-test phrase ("expired lease reclaimed", "duplicate workers yield one resume"), treat that phrase as the actual acceptance criterion and write the test FIRST, in the most literal reading possible — this node's second bug would have been caught even earlier if the expired-lease test had been written before the Claim implementation's SELECT clause, rather than after |
| runtime-b03 | M (DAG: 300 LOC, 3 files) | S/M — lighter than the M estimate; almost pure composition against already-frozen interfaces, similar in shape to runtime-b02's own S/M experience | 3 | 3 (doc.go, evaluate.go, evaluate_test.go) | 300 LOC | one continuous pass, no rework | None — internal/gitx (checkpoint's Git plumbing) and the frozen app.EvaluationService/app.ProgressTreeService ports were exactly sufficient; the only design decision (no new resolver port) was a scope-boundary call, not a missing dependency | None beyond the DAG's 3-file estimate | None | None — reading agents/runtime.md's exact 6-step pipeline list before writing EvaluateRequest's field set avoided a rewrite once the "no resolver port" scope boundary was decided | The fail-open/fail-closed split (operational observations vs. the actual decision steps) recurred as a clean, reusable pattern across THREE of this phase's four Part B nodes (b03, b04, b08) — worth promoting to an explicit, named convention in agents/runtime.md or a shared doc, since it was independently re-derived (correctly, consistently) three times this phase rather than referenced from one place |
| runtime-b04 | M (DAG: 350 LOC, 5 files) | M — estimate held; the real complexity was wiring FOUR distinct hook commands' worth of parse/normalize/evaluate/respond logic consistently, not any single piece of it | 5 | 5 (hooks.go, hooks_test.go, hook.go modified, wiring.go modified, wiring_test.go modified) — matches the DAG's file count once modified-not-new files are counted the same way | 350 LOC | one continuous pass, no rework | None — claude-provider-04's parsers/Normalizer were exactly sufficient and already integrated, per the task brief's explicit confirmation | None beyond the DAG's 5-file estimate | None | Building a `replaceSubcommand` refactor point in wiring.go one node early (this one) rather than waiting for a third node to need the same find-remove-rebuild-add pattern paid off immediately in runtime-b05/b08 (both reused it with zero duplication) | Recommend the "add the reusable wiring helper on the SECOND time you'd otherwise copy-paste a pattern, not the third" rule explicitly — this node's small proactive refactor (extracting replaceSubcommand while wiring just ONE more subtree, hook) made the next two nodes' wiring changes trivially small diffs instead of three independent copy-pasted loops |
| runtime-b05 | M (DAG: 300 LOC, 3 files) | M — estimate held; the ordering guarantee itself was simple to implement correctly (two sequential calls, early return on the first's error) but needed a genuinely convincing test, not just an implementation, given the High risk rating | 3 | 5 (checkpoint.go, checkpoint_test.go, cli/checkpoint.go, wiring.go modified, wiring_test.go modified) — one more file than the DAG's 3-file estimate; the CLI constructor and wiring changes were not separately budgeted, consistent with prior phases' observation that the DAG's file estimate undercounts CLI+wiring plumbing for orchestrator nodes | 300 LOC | one continuous pass, no rework | None — both StateCheckpointService and RepositoryCheckpointService fakes were exactly sufficient; no new fake method or DTO field was needed | cli/checkpoint.go, beyond the 3-file estimate (see actual_files_changed) | None | None — the ordering test (recording actual call sequence through both fakes, not just asserting both were eventually called) was written before the implementation, which caught nothing this time but would have caught an accidentally-swapped call order immediately had one existed | For any future "call service A then B, never the reverse" orchestration node, recommend requiring BOTH a call-order-recording test AND a "B's mock records whether it was called at all" test in the same node, as this one did — asserting only "both were called" is insufficient to prove ordering, and asserting only "the right error came back" is insufficient to prove B was never reached |
| runtime-b08 | S (DAG: 200 LOC, 3 files) | S — estimate held, lowest-risk node of the six as rated | 3 | 6 (diagnostics.go, diagnostics_test.go, cli/diagnostics.go, cli/diagnostics_test.go, wiring.go modified, wiring_test.go modified) — double the DAG's 3-file estimate; this is the clearest instance yet of the recurring pattern (also seen in b05) that the DAG's per-node file count does not budget separately for an orchestrator-layer file, its test, a CLI-layer file, its test, AND a wiring change — five distinct file "slots" for what the DAG counts as one node | 200 LOC | one continuous pass, no rework | None — *sqlite.DB satisfies the locally-declared DBPinger interface structurally with no adapter code needed, confirmed via a throwaway compile-time assertion (built and discarded, not committed) | cli/diagnostics.go, cli/diagnostics_test.go (see actual_files_changed) | None | The throwaway "does type X satisfy interface Y" compile-time check (a temp package built and immediately deleted) was cheap and avoided writing DBPinger's method set from a guess and discovering a mismatch only once wiring.go tried to use it | Recommend generalizing the "file count under-estimate for orchestrator+CLI+wiring nodes" observation (now seen 3 times: b03/b04's lighter case, b05, b08) into an explicit DAG estimation adjustment for any future Part-B-shaped node: budget orchestrator-file + orchestrator-test + cli-file + cli-test + wiring-diff as five slots, not folded into the same 3-4 file estimate used for pure-logic nodes |

## Phase 5 cross-node observations

- This phase completed the entire currently-unlocked frontier for this role in one pass (six
  nodes), the largest single-phase assignment this role has received. Sequencing Part A (both
  High-risk: state-machine and concurrency correctness) before Part B (four comparatively
  lower-risk plumbing nodes) worked as intended — both Part A nodes' tests caught real bugs
  before commit (runtime-a06's two self-caught concurrency bugs, in particular), while all four
  Part B nodes landed at or under their DAG estimates with no rework.
- The single most consequential technical lesson of the phase was runtime-a06's: **a lease
  primitive built on database/sql's connection pool can self-deadlock in a way that looks like a
  hang, not a failing assertion** — and the diagnostic technique that identified it (checking
  process CPU time vs. wall-clock time to distinguish "blocked" from "slow") is worth carrying
  forward as a named technique for any future concurrency-heavy node in this project, not just
  this one.
- The fail-open/fail-closed split (operational observations degrade gracefully; the actual
  decision/mutation steps propagate errors as-is) was independently re-derived correctly by three
  different nodes this phase (b03, b04, b08) from the same ADD §17.5 source material — strong
  evidence the pattern itself is sound, but also a sign it should be written down once,
  explicitly, rather than re-derived per node.
- A smaller, recurring estimation lesson: for any node whose deliverable spans
  orchestrator-logic + CLI-command + wiring-integration (as every Part B node this phase did), the
  DAG's file-count estimate consistently undercounts by roughly double, because it does not
  separately budget for the CLI-layer file/test and the wiring diff on top of the orchestrator
  file/test. This was visible in b05 and most starkly in b08 (3 estimated vs. 6 actual). Worth
  adjusting the DAG's estimation convention for this node shape going forward.
- No new ADRs were required and no frozen contract needed a change-request escalation this phase —
  every fake dependency was pre-authorized by the task brief, and every design judgment call
  (state-name reconciliation, no-new-resolver-port, Claim's widened predicate, the replaceSubcommand
  refactor) was resolvable from already-frozen documents (Constitution §2 priority order,
  CONTRACT_FREEZE.md, ADD sections) without needing to raise a new question to contract-integrator.

# Lessons Learned — runtime (Phase 6: runtime-a03, runtime-a04, runtime-a07)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a03 | M (DAG: 300 points, ~3h) | S/M — the debounce/hysteresis LOGIC was mechanical once ADD §17.6's exact numeric parameters (0.80 threshold, 5s spacing, 30s quota freshness, 0.70 reset band) were transcribed as named constants rather than inlined magic numbers | 2 (implied) | 2 (observe.go, observe_test.go) | 300 points | one continuous pass, no rework | None — internal/domain.RunwayForecast's existing pointer-typed fields (HitProbability, QuotaObservedAt, CurrentUsedPercent, EstimatedTimeToLimitP50Seconds) were exactly sufficient; no new domain field was needed | None beyond the 2-file estimate | None | None — writing the hysteresis-reset test (an in-between sample that stays >= 0.70 must NOT clear the arm) before finalizing resetsArm's boundary condition (< vs <=) avoided a one-line off-by-boundary bug that a less literal test would have missed | The task brief's explicit instruction to read agents/runtime.md's "Day-one realism" section (calibrated + emergency, distinct reason codes) BEFORE writing any code made the two-trigger-path design a lookup rather than a judgment call — recommend flagging this pattern (name the exact ADD subsection a debounce/threshold node must transcribe numeric parameters from) for any future node with hardcoded thresholds, since a numeric transcription error here would be a silent, hard-to-test-for bug rather than a compile-time one |
| runtime-a04 | M (DAG: 300 points, ~4h) | M — estimate held; RequestPause's idempotency logic itself was simple, but designing PauseStore's scope (internal seam vs. a new internal/app/ports.go interface) required deliberately checking Constitution §7 rule 10 before writing any code, to avoid speculative widening of the frozen ports file this role does not own | 3 (implied: requestpause + safepoint + one test) | 4 (requestpause.go, requestpause_test.go, safepoint.go, safepoint_test.go) — safepoint split into its own file/test pair rather than folding into requestpause.go, since the two deliverables (idempotency vs. ordering) have independent required tests and no shared state | 300 points | one continuous pass, no rework | None — runtime-b05's existing internal/orchestrator/checkpoint.go ordering pattern (state before repository, early-return on first error) transplanted directly onto the safe-point boundary with no new technique needed | None beyond the 4-file split explained above | None | None — reading internal/orchestrator/checkpoint_test.go's call-order-recording fake pattern once, before writing safepoint_test.go's own recordingPersister/recordingInterrupter, avoided reinventing (or under-specifying) the "assert order, not just presence" technique lessons_learned already flagged as a general recommendation from runtime-b05 | Confirms runtime-b05's own recommendation (Phase 5 lessons_learned) was correctly load-bearing a phase later: "require BOTH a call-order-recording test AND aB's-mock-records-whether-it-was-called-at-all test" transplanted cleanly to a new orchestration boundary (safe-point persist-then-interrupt) with zero rediscovery cost, because it was written down instead of left in one node's memory |
| runtime-a07 | M (DAG: 300 points, ~3h) | S — lighter than the M estimate; runtime-a06's existing ReclaimExpired gave Restart almost all the SQL shape it needed, so the only real design work was deciding the unconditional-vs-expiry-gated release semantics, not writing new query logic | 2 (implied) | 2 (restart.go, restart_test.go) | 300 points | one continuous pass, no rework | None — internal/scheduler's existing DB/Clock/IDGenerator seams and wake_jobs schema were exactly sufficient; no new migration or domain field needed | None beyond the 2-file estimate | None | None — re-reading ADD §28.3's startup-reconciliation list and the crash-consistency-matrix row for "wake job leased then daemon dies" BEFORE writing Restart's predicate avoided initially copying ReclaimExpired's expiry-gated WHERE clause verbatim, which would have technically compiled and passed a lease-already-expired test but silently failed the actual required test ("restart recovers wake job" with a NOT-yet-expired lease) | Recommend stating explicitly, in any future node that extends an existing lease/lock primitive with a "process restart" variant, whether the new behavior should be time-gated (like the primitive's normal-operation sweep) or unconditional (like this node) — the two are easy to conflate since they touch the same rows and same status values, but have different correctness justifications (elapsed time vs. categorical process death), and only one of them satisfies a required test literally named after "restart" |

## Phase 6 cross-node observations

- This was the fastest, lowest-friction phase this role has had: all three nodes are pure,
  self-contained additions on top of already-frozen, already-tested Part A prior work
  (`runtime-a02`'s state machine, `runtime-a06`'s lease store), with zero cross-role fakes needed
  and zero new files beyond each node's direct implementation + test pair (`runtime-a04`'s 4-file
  split was a deliberate two-concern separation, not scope creep).
- The single most consequential technique reused from a prior phase was `runtime-b05`'s
  call-order-recording fake pattern (Phase 5 lessons_learned's explicit recommendation), applied
  directly to `runtime-a04`'s safe-point ordering test with no rediscovery cost — direct evidence
  that writing a technique down as a named recommendation (rather than leaving it in one node's
  memory) pays off across phases, not just within one.
- `runtime-a07` is this phase's one real "would have shipped a silent bug without the literal
  required-test reading" case: an initial instinct to reuse `ReclaimExpired`'s expiry-gated
  predicate for `Restart` would have compiled, passed an expired-lease test, and STILL failed the
  actual required test ("restart recovers wake job") for the more realistic case of a lease that
  has not yet expired at restart time. Re-deriving the predicate from what "restart" categorically
  implies (every existing lease owner is dead, full stop) rather than from the nearest existing
  code avoided this. Consistent with Phase 5's own lesson about treating a DAG's literal required-
  test phrase as the actual acceptance criterion and writing that test first.
- No new ADRs, no change-request escalations, and no frozen-contract questions this phase — every
  design judgment call (PauseStore's scope, TriggerReason/Boundary as package-local vocabulary,
  Restart's unconditional-release semantics) was resolvable directly from Constitution §6/§7,
  CONTRACT_FREEZE.md, and the relevant ADD sections (§17.6, §20.2, §20.4, §28.3, §29.6) without
  escalation.

# Lessons Learned — runtime (Phase 7: runtime-a05, runtime-b07)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a05 | XL (DAG: 500 points, ~4h) | XL — estimate held; this was genuinely the phase's hardest node, exactly as the DAG's "second highest-risk task in the whole DAG" framing predicted, because two of the five write boundaries needed a REAL cross-role service (Repository Checkpoint) under crash injection, not just a fake | 4 (implied: orchestrator file + test, 2 modified) | 4 (persistphase.go, persistphase_test.go, requestpause.go modified, lease.go modified) — matches once modified-not-new files are counted the same way prior phases have | 500 points | one continuous pass, no rework, but the heaviest single test-harness build this role has done (real migrated SQLite DB + real temporary Git repository + real repocheckpoint.Service, layered under 6 crash-injection tests) | One real, self-discovered gap: `scheduler.Store` had no lookup-by-(pause_id,job_kind) accessor, needed to recover an already-scheduled wake job's ID after a retried `Schedule` call hit its own UNIQUE constraint (the exact crash window between phase 5's own commit and this package's bookkeeping of the result) | None beyond the 4-file estimate | The first test run failed with a FOREIGN KEY constraint error (`wake_jobs.pause_id` references the real `pause_records` table, but this node's `PersistPauseStore` is an in-memory `pause.MemStore`) — required seeding a real `pause_records` row alongside the in-memory record in every test, and documenting the resulting two-backing-stores-for-one-record gap explicitly rather than silently working around it | None — the `HaltAfter`/`HaltError` crash-injection technique was read directly from `internal/progress/complete_node_crash_test.go` before writing any code, exactly as the task brief instructed, and transplanted with zero redesign needed | Confirms Phase 6's own lesson (treat a literal required-test phrase as the acceptance criterion) at a larger scale: "crash after every phase resumes/reconciles correctly" was read as literally requiring one test per phase boundary PLUS a full reconciliation sweep, not just "some crash test exists" — recommend continuing to name the exact precedent file/test to mirror (as this task brief did) whenever a new node's required test matches an existing pattern elsewhere in the codebase, since it turned a genuinely hard design problem (5 independent write boundaries, no flat transaction) into a mechanical application of an already-proven technique |
| runtime-b07 | M (DAG: 300 points, ~4h) | M — estimate held; four command surfaces instead of one made this feel larger than runtime-b05's single-command precedent, but each surface was individually simple (thin orchestration over already-real internals) | 3 (implied) | 9 (lifecycle.go, lifecycle_test.go, requestpause.go modified, requestpause_test.go modified, pauselifecycle.go, pauselifecycle_test.go, cli/pause.go, wiring.go modified, wiring_test.go modified) — roughly 3x the DAG's estimate, consistent with Phase 5's already-flagged observation that Part-B-shaped nodes (orchestrator+CLI+wiring) undercounted by the DAG, now confirmed again at a larger command-surface count | 300 points | one continuous pass, no rework | None — this was the first node where the DAG's dependency was explicitly "same branch, no fake needed" (both `internal/pause` and `internal/scheduler` are this same role's own prior work), and that held exactly as described: zero fakes used anywhere in pauselifecycle.go | requestpause.go/requestpause_test.go modified (PauseStore interface extension broke one existing test fake, `fakePauseStore`, requiring two new stub methods) — not counted in the DAG's 3-file estimate | None | None — reusing runtime-b05's exact CLI+wiring pattern (real orchestrator function, real Cobra constructor, `replaceSubcommand` in wiring.go) for four commands instead of one was mechanical once the pattern was internalized once | The "PauseStore interface extension breaks an existing test fake" cost (fixed in ~2 lines here) is itself a small, generalizable lesson: any node that widens an internal, package-owned interface (not just a frozen `internal/app/ports.go` one) should grep for every existing implementer INCLUDING test-only fakes before considering the change complete, since `go vet`/`go build` will catch it but only after the fact — recommend treating "grep for `var _ InterfaceName = ` across the whole module" as a standard last step before committing any interface-widening node |

## Phase 7 cross-node observations

- This phase's two nodes were sequenced Part A before Part B specifically because `runtime-b07`
  has a real, same-branch dependency on Part A internals (`pause.RequestPause`/`Cancel`/`Resume`,
  `scheduler.Store`) — `runtime-a05` didn't itself unlock anything `runtime-b07` needed (the two are
  independent additions to the same underlying packages), but building the persist-phase
  orchestration first kept the phase's highest-risk work at the front, consistent with every prior
  phase's stated sequencing rationale (Part A's state-machine/concurrency-correctness risk always
  precedes Part B's comparatively lower-risk plumbing).
- The phase's single biggest technical lesson was `runtime-a05`'s: coordinating a crash-injection
  proof across FIVE independent durable stores (two of them real, cross-role services) is
  materially harder to test than a single-transaction protocol, but the underlying TECHNIQUE
  (`HaltAfter`/`HaltError`, idempotent-skip-on-replay) generalizes without modification from
  `internal/progress.CompleteNode`'s single-transaction case to this node's multi-store case — the
  hard part was the test harness (real DB, real Git repo, seeding two backing stores for one
  conceptual record), not the core algorithm.
- A second, smaller lesson from `runtime-a05`: when a node's own required test needs a capability a
  sibling-owned package doesn't expose (here, `scheduler.Store` recovering a job by natural key
  after a constraint conflict), check whether the current role owns that package before treating it
  as a blocker to escalate — Part A owns both `internal/pause` and `internal/scheduler`, so the gap
  was closed directly in the same node, which is faster and leaves a cleaner trail than filing a
  cross-role change request for something already in scope.
- No new ADRs, no change-request escalations, and no frozen-contract questions this phase. The one
  explicitly-tracked gap (`PersistPauseStore`'s in-memory backing vs. the real `pause_records` SQL
  table) is deliberately left open for a later integration node rather than silently resolved,
  consistent with this role's established practice of documenting rather than hiding known
  incompleteness.

# Lessons Learned — runtime (Phase 8: runtime-a08)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a08 | L (DAG: 350 points, ~3h) | L — estimate held; the four checks themselves were each individually simple, but designing the repository-fingerprint check's own narrow seam (not a direct dependency on internal/gitx, mirroring safepoint.go's existing CheckpointPersister/Interrupter precedent) and getting the fail-closed error-vs-CheckResult split right took real design iteration | 2 (implied) | 2 (resumevalidation.go, resumevalidation_test.go) | 350 points | one continuous pass, with one real self-caught design inconsistency mid-node (see below) | None — app.RepositoryCheckpointService.Verify and app.EvaluationService.ConsumeAuthorization's frozen signatures were exactly sufficient; internal/domain.ReasonRepositoryChangedDuringSleep (already a frozen ReasonCode) was reused by citation, not duplicated, for the repo-overlap detail string | None beyond the 2-file estimate | None external — one internal design bug, caught by this node's own test suite before commit (see below) | An early draft's package doc claimed ValidateResume "stops at the first erroring checker" for ANY checker error, but the actual implementation (correctly) converts a downstream read failure into a failing CheckResult with an _UNAVAILABLE reason code rather than a Go error — the doc comment and the code disagreed. TestResumeValidation_ValidateResume_StopsAtFirstErroringDependency (written to match the STALE doc claim) failed against the correct code; re-reading what the code actually did (report a reason code, keep running later checks) rather than reflexively "fixing" the code to match the first draft's doc comment was the right call, and the test was rewritten to assert the actual, better behavior instead | When a node's own design doc comment and its own test disagree with the implementation, treat that as a signal to re-derive WHICH one is actually correct against the task's stated goals (here: "the last line before unattended code execution" implies fail-closed, but says nothing about which Go-level mechanism — error vs. typed result — should carry that failure; a typed CheckResult reason code is strictly MORE useful for an audit trail than an opaque error), rather than mechanically making the code match whichever was written first. Recommend stating this two-channel distinction (dependency-composition-bug errors vs. typed-result check failures) explicitly in agents/runtime.md for any future validation-gate-shaped node, since it is a design decision, not an implementation detail, and this node had to work it out from Constitution §6 (fail-closed, not fail-open, for state-integrity boundaries) applied together with the audit-trail requirement 0052_resume_attempts.sql's own schema implies (failure_code column) |

## Phase 8 cross-node observations

- This is the smallest-surface node this role has shipped by file count (2 files) despite its High
  risk rating and L size — consistent with the pattern that a pure-logic node with no cross-role
  fake wiring or CLI/wiring plumbing (this node touches neither `internal/orchestrator` nor
  `internal/cli`, unlike most of this role's Part B nodes) lands close to its point estimate without
  the file-count inflation Phase 5/7's lessons_learned flagged for orchestrator+CLI+wiring-shaped
  nodes.
- The one real lesson of the phase was catching a doc-comment/test/implementation three-way
  disagreement before commit, not after: writing the required test literally from the task brief's
  own required-test list ("unsafe quota reschedules," "repo overlap blocks," "unrelated repo change
  follows configured policy") first surfaced that an early doc-comment claim about error-vs-
  CheckResult handling did not match what the code (correctly) did, and fixing the DOC and the
  MISALIGNED TEST — not the already-correct code — was the right resolution once traced back to
  first principles (Constitution §6 fail-closed + the audit-trail schema's own failure_code column).
- Every dependency this node used was already real and mergeable (checkpoint-b04's
  RepositoryCheckpointService, integrated since Phase 5) except the one explicitly named as a fake in
  the task brief (predictor-10's authorization-hardening pass, a concurrent sibling this same phase)
  — no undeclared fake was needed anywhere in this node.
- No new ADRs, no change-request escalations, and no frozen-contract questions this phase. This
  node's three new narrow seams (QuotaSnapshotReader, RepoFingerprintReader,
  SessionCapabilityReader) are all package-local to internal/pause, following the same
  narrowest-seam-this-node-needs discipline established by safepoint.go/persistphase.go in earlier
  phases — none of them widen internal/app/ports.go, which remains untouched by this role.

# Lessons Learned — runtime (Phase 9: runtime-a09, runtime-a10, runtime-b06)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a09 | M (DAG: 300 points, ~3h) | M — estimate held, but for a reason the DAG's risk rating undersold: `lifecycle.go`'s existing `Cancel`/`Resume` (from `runtime-b07`, an EARLIER phase) had a real, shipped TOCTOU race (`GetByID` then unconditional `UpdateStatus`), not just a missing test — this node's job turned from "add tests for an existing correct guarantee" into "discover the guarantee didn't actually hold, then fix it" | 3 (implied: 2 new files + 1 modified) | 6 (requestpause.go modified, requestpause_test.go modified, lifecycle.go modified, wake.go new, wake_test.go new, splitbrain_test.go new) — double the implied estimate, entirely because fixing the TOCTOU race required widening `PauseStore` itself (`CompareAndSwapStatus`), which the DAG's node description did not anticipate since it assumed `Cancel`/`Resume` were already correct | 300 points | one continuous pass, with one real self-caught test-design bug mid-node (see below) | One undeclared, self-discovered one: `PauseStore` needed a new atomic primitive (`CompareAndSwapStatus`) that did not exist before this node — not because the DAG under-scoped Part A's interface, but because the pre-existing `Cancel`/`Resume` implementation (correct-looking, already merged, already tested for its OWN non-concurrent test cases) had never been exercised under genuine concurrency before this node's required tests demanded it | None beyond the 6-file total explained above | None external | **The single most valuable moment of this phase**: an early draft of `TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState` asserted "exactly one of Cancel/Wake succeeds" and failed reliably (not flakily) on the very first run. Investigating why (adding temporary debug prints, since the failure mode — both calls reporting success — looked at first like a real double-resume bug) revealed the ASSERTION was wrong, not the implementation: `WakePending` legitimately has an `EventCancel` edge (statemachine.go), so Wake-then-Cancel landing WakePending->Cancelled a moment later is "cancel wins" working AS INTENDED, not a violation. Rewriting the test's own property (Cancel must always eventually win THIS race; final status is always Cancelled) rather than weakening it to "match whatever happened" was the correct fix. The debug-print files were written to a real source file mid-investigation, confirmed the finding, and were deleted before commit — a real but small example of exactly the "throwaway compile-time/behavior check, built and discarded" technique Phase 5's `runtime-b08` lesson already named | This phase reinforces Phase 5/6's already-stated lesson from the OTHER direction: not only should a literal required-test phrase be treated as the acceptance criterion, but when a test you just wrote fails on the FIRST run (not flakily, reliably), the default hypothesis should be "my test's assertion encodes a wrong mental model of the system," checked BEFORE assuming "the implementation has a bug" — both are live possibilities, and this node found one real implementation bug (the TOCTOU race, confirmed genuine and fixed) and one test-modeling bug (the over-strict "exactly one succeeds" assertion, confirmed wrong and corrected) in the same session, back to back, and treating them identically (temporary debug instrumentation, then a principled fix, never a guess) is the reusable technique worth naming explicitly for any future concurrency-proof node |
| runtime-a10 | S (DAG: 200 points, ~3h) | S — estimate held exactly; the code itself (two fakes + a generic contract-suite function) was mechanical once the one real question (does either interface have MORE contract than its bare signature?) was answered | 3 (implied) | 3 (provider.go, providercontract.go, providercontract_test.go) — matches exactly | 200 points | one continuous pass, no rework, but preceded by a dedicated research pass (a background agent) before writing any code | None — internal/app/ports.go's TurnInterrupter/SessionResumer were exactly sufficient as declared; the research pass confirmed (rather than assumed) that no additional frozen behavioral contract exists for either, avoiding either under- or over-building the suite | None beyond the 3-file estimate | None | None — dispatching the "does ADD/CONTRACT_FREEZE.md/agents/claude-provider.md specify any behavior beyond the signature" question to a dedicated research pass BEFORE writing the contract suite avoided the much more expensive failure mode of writing a suite that either invented unstated invariants (over-fitting) or missed a real documented one (under-fitting) | For any future "write a reusable contract test suite for a frozen interface" node, recommend explicitly budgeting a research step (not just re-reading the interface's own doc comment) to confirm the SCOPE of a contract before writing tests for it — the interface signature alone is not sufficient evidence that no additional behavioral contract exists, and this node's confirmed-negative research result was itself valuable, citable evidence, not wasted effort |
| runtime-b06 | M (DAG: 300 points, ~3h) | M — estimate held; the orchestration logic itself (two-flow issue/consume split) was simple once the flow design was settled, but proving the required tests against the REAL evaluation pipeline (not a fake) required reverse-engineering the actual risk-scoring formula first, which the DAG's node description implicitly assumed would be straightforward | 3 (implied) | 6 (decision.go, decision_test.go, decision_realauth_test.go, cli/decision.go, wiring.go modified, wiring_test.go modified) — double the implied estimate, consistent with every prior Part-B-shaped node this role has shipped (Phase 5/7's already-flagged orchestrator+CLI+wiring undercounting pattern, confirmed again) | 300 points | one continuous pass, no rework, but preceded by a dedicated research pass (a second background agent) to reverse-engineer internal/predictor/risk/combiner.go's actual scoring formula well enough to build a fake DataSource that reliably drives the REAL pipeline to a specific risk band | A real, load-bearing one: `IssueAuthorization` exists on the concrete `*evaluation.Service` but is deliberately NOT part of the frozen `app.EvaluationService` interface — this was correctly anticipated by that package's own doc comment (read before writing any code), so it was a documented, not a discovered, gap; still required a new local `AuthorizationIssuer` seam in `internal/orchestrator`, mirroring the existing `UsageObservationLoader`/`GitSnapshotter` precedent | None beyond the 6-file total explained above | None external | The research agent's fixture math (specific `features.PromptFeatures`/`RepositoryFeatures`/`SessionFeatures` field values predicted to drive `OverallRisk.Score` to a specific band) was verified against the REAL pipeline on the first test run — both the high-risk fixture (confirmed via a temporary debug test, written and immediately deleted, to print the exact `PolicyAction` produced: `CHECKPOINT_AND_RUN`, i.e. the critical band, not merely the high band) and the low-risk control fixture (confirmed `PolicyRun`) matched the prediction exactly, with zero fixture-tuning iterations needed | Confirms `runtime-a10`'s same-phase lesson at a larger scale: reverse-engineering a REAL system's exact scoring/decision formula via a dedicated research pass, THEN writing fixtures against the verified formula, produced a working high-risk/low-risk fixture pair on the first attempt — recommend this "research the real formula's exact thresholds and field-to-score mapping before writing any fixture data" step as the default whenever a future node's required test depends on driving a real (not faked) scoring/classification pipeline into a specific band, rather than iteratively guessing field values and re-running |

## Phase 9 cross-node observations

- This phase's most important finding was that `runtime-a09`'s assigned "add tests for" framing
  undersold the actual work: the DAG describes `runtime-a09` as proving guarantees that
  `runtime-a07`/`runtime-a08` "already proved... generally" at the lease-claim level, but the
  PAUSE-level guarantee (`Cancel`/`Resume` in `lifecycle.go`, shipped in `runtime-b07`, an earlier
  phase) had never actually been proven under concurrency and, once tested for real, was found to
  have a genuine time-of-check-to-time-of-use race. This is worth naming as a general risk for any
  future DAG node phrased as "add required tests for X" against an already-merged, already-reviewed
  implementation: a node whose OWN job is "prove this holds" should budget for the possibility that
  it does not yet hold, not just for writing the proof.
- Both `runtime-a09` and (independently) the test-writing phase of `runtime-b06` produced a test
  that failed reliably on its first run, and in both cases the root cause was different from the
  first hypothesis: `runtime-a09`'s first failure was a wrong test assertion (not a system bug);
  `runtime-b06`'s fixture math worked correctly (no bug found there), but `runtime-a09`'s
  investigation technique — temporary, source-level debug instrumentation (print statements, or a
  throwaway test), written to disk, run, and deleted before commit, never left in the diff — was
  reused deliberately in `runtime-b06` to CONFIRM a fixture's exact output band with certainty
  rather than trusting the research agent's prediction blindly. Recommend naming this technique
  ("temporary in-source instrumentation, confirmed then deleted, never committed") explicitly as a
  standard debugging step for this project, alongside Phase 5's already-named "process CPU time vs.
  wall-clock time" technique for diagnosing hangs — both are the same underlying discipline (get
  real, direct evidence before either accepting or rejecting a hypothesis) applied to different
  failure shapes.
- `runtime-a10` and `runtime-b06` both benefited from dispatching a dedicated research pass (a
  background agent) before writing any code, for two different but structurally similar questions:
  "does this frozen interface have more contract than its signature" (a10) and "what exact field
  values drive this real scoring pipeline to a specific band" (b06). Both research passes returned
  confirmed, checkable findings (not just plausible guesses) that held up against direct
  verification once code was written — recommend continuing to treat "research the real system
  before writing a test that depends on its exact behavior" as the default for any future node in
  this category, rather than iterative trial-and-error against the real pipeline.
- No new ADRs, no cross-role change-request escalations, and no frozen-contract questions this
  phase. `internal/app/ports.go`, `internal/domain/**`, and `internal/evaluation/**` were called,
  never modified, exactly per each node's explicit boundary; the one interface widened
  (`pause.PauseStore`, adding `CompareAndSwapStatus`) is this role's own internal seam, not a frozen
  cross-component port, so it required no escalation — consistent with `runtime-a05`'s Phase 7
  precedent for the same class of in-bounds internal-interface change.

# Lessons Learned — runtime (Phase 10: runtime-a11, runtime-b09)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a11 | XL (DAG: 550 points, ~10h) | L/XL — lighter than the XL estimate in one sense (2 files, both new, no rework of prior nodes' own tests) but genuinely hard in the sense the DAG predicted: composing five-plus already-independently-correct packages into one lifecycle proof surfaced exactly one real gap that needed new production code, and finding it required auditing 9 required-test items individually first, not writing tests speculatively | 2 (implied: one integration test file) | 2 (interrupt.go new, fulllifecycle_test.go new) — lighter than the DAG's XL sizing would suggest by file count, though fulllifecycle_test.go itself is the largest single test file this role has written (12 test functions, ~700 LOC) | 550 points | one continuous pass, preceded by a dedicated research pass (a background agent), with two self-caught test-design bugs mid-node (see below) | None — every dependency this node composed (persistphase.go, lifecycle.go, wake.go, resumevalidation.go, scheduler.Store, testutil/fakes) was already real and already this role's own prior work; the one "new" piece (TurnInterrupterAdapter) is new PRODUCTION code this node itself identified as missing, not an external dependency gap | None beyond the 2-file total | None external | Two, both self-caught by this node's own first test run, both corrected against the actual transition table/design docs rather than guessed: (1) an over-strict "no earlier event can ever re-fire" assertion in the 9-step crash sweep failed because EventResumeValid legitimately has two distinct edges in the transition table — re-derived the correct invariant (status must equal the immediately-preceding step's own output, never regress further back) instead of weakening the check to "whatever happened"; (2) an assertion that BlockedConflict is terminal was wrong — it deliberately is not (ADD §20.9's manual Cancel edge) — checked directly against statemachine.go's terminalStates set rather than assumed from the name "Blocked" | Confirms Phase 9's lesson (runtime-a09) at a larger, whole-role scale: a node framed as "prove the ENTIRE stack composes correctly" should budget for finding that ONE required test's underlying production wiring genuinely does not exist yet (here: no code called TurnInterrupter and applied the resulting event to a real PauseRecord — safepoint.go's own PersistThenInterrupt deliberately stopped short of this, by documented design, in an EARLIER phase), not just for writing a proof of something already true. The dedicated pre-code research pass (auditing all 9 required tests against existing coverage with file:line precision) was what made it possible to say precisely "5 of 9 needed no new code, 3 needed a fuller composition test only, exactly 1 needed new production code" rather than a vaguer "reviewed everything, looks fine" |
| runtime-b09 | M (DAG: 250 points, ~3h) | M — estimate held on points but the DAG's own 3-file estimate undercounted for the same reason Phase 5/7's lessons already flagged for orchestrator+CLI+wiring-shaped nodes: a cross-cutting fix that touches the shared root-command-construction path (root.go, wiring.go) in addition to the new test file and errors.go is inherently more files than a single-command node | 3 (implied) | 4 (errorcontract_test.go new, errors.go modified, root.go modified, wiring.go modified) | 250 points | one continuous pass, preceded by a dedicated research pass (a second background agent), with one real self-caught bug mid-node (see below) | None — every real command's own error/success-path code was already correct and needed no changes; the fix was entirely in shared root-command plumbing (errors.go/root.go/wiring.go), which this role already owns | None beyond the 4-file total | None external | One real, load-bearing one: an early design instinct ("keep SilenceErrors: false so this JSON-rendering addition is purely additive, changing nothing about existing behavior") was itself wrong — it left Cobra's own plain-text error line printing ALONGSIDE the new JSON envelope, which directly violates "machine mode never emits decorative text," a requirement this same node was trying to satisfy. Caught immediately (100% failure rate, not flaky) by this node's own TestErrorContract_NoDecorativeTextOnAnyCommand on first run across every single command. Fixed by flipping SilenceErrors to true — the JSON envelope REPLACES Cobra's default text, it does not sit alongside it | The "purely additive, don't change existing behavior" instinct is usually right (and WAS right for the returned Go error value itself — every existing errors.As-based test needed zero changes) but is not automatically right for every dimension of a fix; this node's bug is a specific, nameable case of a more general lesson: when a fix's own stated GOAL (no decorative text) and a design choice made in service of a DIFFERENT goal (don't change existing behavior) conflict, write the test for the STATED GOAL first and let it adjudicate, rather than assuming both goals are simultaneously satisfiable by construction. Recommend treating "test the requirement you are actually trying to satisfy, not just the requirement you're trying to avoid breaking" as a named discipline for any future cross-cutting fix in a shared root/wiring file |

## Phase 10 cross-node observations

- Both nodes this phase are the same SHAPE as Phase 9's `runtime-a10`/Phase 8's `runtime-a08` and this
  role's own established pattern for `checkpoint-a09`/`checkpoint-b09`/`predictor-11`-class nodes:
  comprehensive final-proof/audit nodes rather than new-feature nodes, each preceded by a dedicated
  background-agent research pass BEFORE any code was written, exactly as the task brief instructed.
  Both research passes returned precise, file:line-cited findings that were independently spot-checked
  against the actual repository before being trusted, consistent with Phase 9's own established
  practice of verifying a research agent's report rather than accepting it blindly.
- The phase's single biggest validated finding across BOTH nodes: a comprehensive "does this already
  hold" node should expect to find approximately ONE genuine gap per pass, not zero and not many —
  `runtime-a11` found exactly one (provider-interrupt-failure production wiring, out of 9 required
  tests audited) and `runtime-b09` found exactly one (no JSON error-rendering layer, out of the full
  P0 command surface audited), each surrounded by several already-correct areas the research pass
  confirmed rather than needlessly re-built. This is worth naming as the expected shape of any future
  "prove the whole stack together" node in this project: budget for finding ONE real thing, not for
  finding nothing (which risks skipping past a real gap) or for finding many things (which would
  suggest the individual nodes leading up to it were not actually done correctly, which was not the
  case in either instance this phase).
- Both nodes' own test-writing caught a real bug in THIS SAME NODE'S own first draft (not in an
  earlier node's shipped code, unlike Phase 9's `runtime-a09` finding a real bug in Phase-7-vintage
  `lifecycle.go`) — consistent with, but a distinct sub-case of, this role's now well-established
  cross-phase technique: treat a test's first-run failure as "my own assertion or design choice may be
  wrong" before assuming either "the system under test is broken" or "the test itself is simply
  flaky," and verify directly rather than guessing. `runtime-a11`'s two fixes were both test-assertion
  corrections (checked against statemachine.go directly); `runtime-b09`'s one fix was a genuine
  design-choice correction (SilenceErrors) rather than a test-only fix — worth distinguishing these
  two sub-cases explicitly (wrong test vs. wrong implementation-adjacent design choice) as both are
  live possibilities a first-run failure can indicate, not just the two this role's Phase 9
  lessons_learned already named (wrong test vs. wrong implementation).
- No new ADRs, no cross-role change-request escalations, and no frozen-contract questions this phase.
  `internal/app/ports.go`, `internal/domain/**`, and every other role's owned packages were called,
  never modified. The one new production code this phase (`internal/pause/interrupt.go`'s
  `TurnInterrupterAdapter`/`InterruptAndSleep`) satisfies `pause.Interrupter`, an existing internal
  seam this role already owns (not a frozen port); `internal/cli/errors.go`'s new JSON-rendering
  helpers are entirely new code in an already-owned file, not a widening of any shared contract.
- `runtime-b10` (this role's final vertical-slice node) remains for a future phase — explicitly not started
  this phase, per the task's own instruction.

# Lessons Learned — runtime (Phase 11: runtime-b10 — FINAL NODE)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-b10 | L (DAG: 450 points, ~8h) | L/XL — the DAG's own "High risk" framing for the restart test held exactly, but for a reason the DAG's node description did not name: the hardest part was not building the restart proof itself, it was discovering that TWO same-process crash-simulation techniques both give FALSE results (a `database/sql` pool-bookkeeping artifact, not a real bug), which cost a full investigation cycle before landing on the correct technique (a real subprocess + SIGKILL) | 4 (implied: restart test file + one new store + its test + a golden test file) | 7 (sqlitestore.go, sqlitestore_test.go, restart_test.go, golden_test.go, 3 golden fixture files) — the fixture files are new territory for this role (no prior node shipped a `testdata/` fixture), not counted separately by the DAG's implied estimate | 450 points | one continuous pass, with two full stop-diagnose-fix cycles: (1) a cobra flag-state-leak bug in the test's OWN drive helper (reusing one `*cobra.Command` tree across multiple `Execute()` calls), found and fixed by switching to a fresh `RootCmd()` per call; (2) the crash-simulation methodology itself, which needed a real subprocess after two in-process attempts both produced a genuine but MISLEADING `SQLITE_BUSY` | Two, both self-discovered by direct research before writing any restart code, not assumed: (1) `pause.PauseStore` had no SQLite-backed implementation anywhere — five PRIOR nodes across four earlier phases had each independently deferred this exact gap in their own doc comments; closing it (`SQLiteStore`) was in-scope since `internal/pause` is this role's own exclusive path, mirroring `runtime-a05`'s Phase 7 precedent for "a same-role internal gap discovered mid-node is closed directly, not escalated." (2) "CLI golden tests" (agents/runtime.md Part B's own Tests list) had never been built by any of `b01`-`b09` — confirmed by grep (zero hits for "golden" anywhere under `internal/cli`), closed with a new `testdata/golden/` fixture convention mirroring `claude-provider`'s own already-established precedent for the same technique | `internal/cli/testdata/golden/*.golden.json` — a new file TYPE (checked-in JSON fixtures) this role has never shipped before in 10 prior phases | The two same-process crash-simulation attempts (documented in detail in both restart_test.go's own comments and this phase's progress-artifact section) were NOT wasted effort in the token-waste sense — each one produced a specific, falsifiable, and ultimately WRONG hypothesis about why `SQLITE_BUSY` occurred, and disproving each specifically (via a throwaway, built-and-deleted experiment isolating `sql.Conn.Close()`'s documented deadlock-on-open-Tx behavior) is what made the correct diagnosis (a Go `database/sql`-level artifact, not a SQLite or storage-layer bug) actually CERTAIN rather than merely plausible — consistent with this role's own established "confirm before trusting" discipline, applied here to a debugging problem instead of a fixture-tuning problem | This phase's restart-safety investigation surfaces a genuinely NEW, previously-unnamed technique for this project: **a same-process "abandon a `*sql.Tx` and let it dangle" simulation of a process crash is not a weaker version of a real crash test, it is a DIFFERENT and potentially MISLEADING test** — `database/sql`'s own pool/transaction bookkeeping (documented behavior: `DB.Close()` waits for in-flight queries to finish; `Conn.Close()` deadlocks on an open `Tx`) can produce a failure that looks exactly like a real SQLite-level lock-recovery bug but is actually an artifact of Go's own connection-lifecycle rules. | Recommend: any FUTURE Auspex node (this role or any other) that needs to test real process-crash recovery should default to the subprocess-re-exec-plus-SIGKILL technique (`os.Args[0]`, `-test.run=^Name$`, an env-var-gated helper `Test` function, a real `syscall.SIGKILL`) from the outset, rather than rediscovering — the hard way, as this node did — that the simpler in-process approximation gives false results for this specific class of test |

## Phase 11 cross-node observations (single-node phase — this role's LAST)

- This is `runtime`'s final assigned DAG node (`agents/runtime.md`'s full
  Part A + Part B scope, 21 nodes across 9 phases, is now 100% complete).
  Unlike every prior "final gate" node this role shipped (`checkpoint-a09`/
  `checkpoint-b09`/`predictor-11` cross-role, `runtime-a11`/`runtime-b09`
  same-role, all Phase 10), this node closed out not just Part B but the
  ENTIRE role's remaining DAG scope — no further work of any kind remains
  assigned to this role for vertical-slice.
- The "comprehensive audit-then-close node finds ~1 real gap per
  sub-area" pattern (first named Phase 10) held a THIRD and FOURTH time
  this phase: one real gap in Part A (`PauseStore`'s missing SQLite
  backing, a production-code fix) and one real gap in Part B's own Tests
  checklist ("CLI golden tests," a test-infrastructure fix) — never zero,
  never many, consistent with every "prove the whole stack" node this
  project has shipped across multiple roles.
- The crash-simulation-technique lesson (see the node's own "token waste
  observations" cell above) is this phase's one genuinely NEW addition to
  this role's technique inventory across the entire 9-phase arc — distinct
  from (though built on the same underlying discipline as) Phase 5's
  "process CPU time vs wall-clock" hang-diagnosis technique and Phase 9's
  "temporary in-source instrumentation, confirmed then deleted" technique.
  Recommend it be treated as this project's default going forward for any
  crash-recovery-shaped test, in any role, rather than being rediscovered
  per-node the way this phase had to rediscover it once.
- No new ADRs, no cross-role change-request escalations, no frozen-contract
  questions this phase — consistent with this role's unbroken record across
  all 9 phases (Phase 4's one foundation change request, resolved before
  Phase 5, remains the only cross-role escalation this role ever needed in
  its entire 21-node history). `internal/app/ports.go`, `internal/domain/**`,
  and every other role's owned packages were called, never modified.
  `internal/pause/sqlitestore.go`'s new `SQLiteStore` satisfies
  `pause.PauseStore`, this role's own internal seam (not a frozen port) —
  no interface in `internal/app/ports.go` was widened or touched, the same
  discipline this role maintained across all 21 nodes without exception.

## Full-arc retrospective (Phase 3 → Phase 11, all 21 nodes: a01-a11, b01-b10)

- **Estimation accuracy**: pure-logic Part A nodes with no cross-package
  wiring (`a02`, `a03`, `a06`'s core logic, `a07`, `a08`) consistently
  landed at or slightly under the DAG's point/file estimates. Every
  Part-B-shaped node spanning orchestrator+CLI+wiring (`b03`-`b09`)
  consistently ran 2-3x the DAG's naive file-count estimate, a pattern
  first flagged Phase 5 and reconfirmed in Phases 7, 9, and 10 without
  exception — worth writing into the DAG's own estimation convention for
  any future project using this same execution-plan shape, rather than
  re-discovering it once per Part-B-shaped node the way this role did five
  separate times.
- **Highest-risk nodes justified their risk rating every time**: `a05`
  (XL, "second highest-risk task in the whole DAG") needed the heaviest
  test harness this role ever built; `a06`, `a09`, and this final `b10`
  node each found genuine concurrency or process-lifecycle bugs (a
  self-deadlock, a real TOCTOU race, and a crash-simulation methodology
  bug respectively) that a lower-risk node's lighter testing bar would not
  have caught. The DAG's own risk labels were, across this role's entire
  history, a reliable predictor of where real bugs (not just LOC) would be
  found.
- **This role never required a single ADR across 21 nodes and 9 phases** —
  the strongest single piece of evidence in this role's own history that
  `agents/runtime.md` plus the frozen `CONTRACT_FREEZE.md`/`Auspex_ADD.md`
  contract was genuinely sufficient, phase over phase, for an agent picking
  up this role fresh each time (no persistent memory across phases) to make
  every real design judgment call correctly from already-published
  authority, rather than needing to invent or escalate new ground truth.
- **The single most-reused cross-phase technique**: "when a just-written
  test fails reliably on its first run, treat that as 'my own assertion,
  design, or methodology may be wrong' before assuming either 'the system
  has a bug' or 'the test is flaky,' and get direct evidence before
  choosing" — independently, correctly applied in at least six distinct
  instances across five different phases (`a06`, `a09` twice in one
  session, `a11` twice, `b09`, and now `b10`'s crash-simulation
  methodology bug), each time correctly distinguishing which of the
  several possible explanations was actually true rather than guessing.
  This is the one technique worth extracting into any future multi-phase,
  multi-agent project's own shared engineering-practice documentation,
  independent of Auspex's own domain.

# Lessons Learned — runtime (Final integration gate corrective addition: `pause.Service`)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-final-gracefulpause-service (not a DAG node) | M/L (lead-identified finding, no DAG estimate exists) | M — every piece of real logic already existed and was already tested; the actual work was composition, DTO-shape translation, and discovering/documenting two genuine, previously-deferred gaps, not new business logic | 2 (one new file, one existing file extended) | 4 (`internal/pause/service.go` new, `internal/pause/service_test.go` new, `internal/pause/sqlitestore.go` extended with `PersistPauseStore`, this doc + lessons_learned) | N/A | one continuous pass | None beyond this role's own already-owned packages (`internal/pause`, `internal/scheduler`) — no cross-role dependency was needed, confirmed by a background research agent's independent cross-check that no frozen port anywhere resolves `SessionID`->`TaskID` | None — no new file type, unlike `b10`'s golden-fixture precedent | Two real design gaps, both self-discovered and explicitly surfaced rather than silently papered over: (1) the frozen `app.PauseRequest`/`SafePoint`/`ResumeRequest` DTOs carry no `TaskID`/`WorktreeID`/quota-baseline — closed with a locally-declared `SessionContextResolver` seam (no real implementation yet, explicitly left for a future wiring node), mirroring `resumevalidation.go`'s own narrow-seam precedent; (2) `PersistPauseStore` was never reconciled onto `SQLiteStore` (named as a gap by five nodes since Phase 7) — closed directly since `internal/pause` is this role's own exclusive path | `go vet ./internal/pause/...` caught a real bug during this node's own validation pass, not a test: the first draft of `NewService(deps Service)` copied a `Service` value containing `sync.Mutex`-guarded maps. Fixed by splitting `Service`'s constructor input into a separate, mutex-free `ServiceDeps` value type — cheap once caught, but a reminder that `go vet` is part of the required validation gate for exactly this class of bug, not merely a formality to run after tests already pass | Confirms this role's own Phase-10/11-established pattern one more time, in a new shape: a comprehensive external review (this time from `contract-integrator-final`, not this role's own internal audit) found exactly one real, previously-deferred, already-self-documented gap (the missing `GracefulPauseService` adapter) rather than a swathe of undiscovered problems — because every prior node that touched this boundary (`a04`, `a05`, `a09`, `b10`) had already written down the gap explicitly rather than silently skipping it. Recommend continuing this project's standing discipline of naming a deferred gap in the doc comment of the node that finds it, even when closing it is out of that node's own scope — it is what made this corrective addition a fast, well-scoped composition task instead of an open-ended investigation |

## Final addition — cross-node observations

- This is confirmation, not a new instance, of `runtime`'s own
  most-repeated process lesson (Phase 11's retrospective, above): every
  real judgment call this addition needed — the `SessionContextResolver`
  seam's shape, the `PersistPauseStore` reconciliation's column mapping,
  the `ServiceDeps`/`Service` split — was resolvable directly from
  already-published authority (this package's own prior doc comments,
  Constitution §7 rule 3's "surface capability gaps explicitly," and
  `go vet`'s own diagnostic), without escalation and without a new ADR.
- Unlike every numbered DAG node this role shipped, this addition's
  starting point was a review finding rather than an execution-plan
  assignment — worth noting as a positive data point that this role's
  standing conventions (narrow internal seams for undeclared
  capabilities, explicit gap documentation in the node that finds a gap,
  reusing an existing integration-test technique rather than inventing a
  new one) generalized cleanly to a differently-sourced task, not just to
  the DAG's own pre-planned shape.
- This is, in the fullest sense, this role's actual final piece of Day-1
  work: every DAG node was already complete, and this corrective addition
  closes the one remaining concrete gap those nodes' own doc comments had
  already flagged. No further work of any kind is anticipated for this
  role.
