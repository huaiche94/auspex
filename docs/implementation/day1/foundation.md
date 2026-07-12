# foundation — Progress Artifact

## Handoff notes (Constitution §6.7 / agents/foundation.md "Handoff")

- **DB constructor**: `sqlite.Open(ctx, path) (*DB, error)`
  (`internal/storage/sqlite/db.go`, foundation-05). Opens (creating if
  needed) a SQLite file via `modernc.org/sqlite` and applies Preflight's
  fixed pragmas (ADD §12.1) both via DSN `_pragma` query parameters (so
  every pooled connection gets them) and via an explicit `applyPragmas`
  exec on open (belt-and-braces against driver DSN-parsing differences).
  `path == ":memory:"` is supported for tests via a shared-cache DSN.
  `Open` does NOT create schema or run migrations — call `DB.Migrate`
  separately.
- **Transaction API**: `(*DB).WithTx(ctx, app.TxFunc) error` implements
  the frozen `app.TxRunner` port exactly (compile-time-asserted in
  `db.go` via `var _ app.TxRunner = (*DB)(nil)`). Because `app.TxFunc` is
  `func(ctx context.Context) error` — it does NOT receive a `*sql.Tx`
  parameter directly, and `internal/app/ports.go` is frozen and out of
  foundation's control to change — `WithTx` stores the active `*sql.Tx`
  in the `ctx` it passes to `fn` (an unexported `txKey{}` context key).
  Callers inside a `TxFunc` closure retrieve it via
  `sqlite.QuerierFromContext(ctx, db)`, which returns the active `*sql.Tx`
  if called from inside `WithTx`, or `db`'s plain `*sql.DB` pool
  otherwise — so storage code can be written once against the `Querier`
  interface (`ExecContext`/`QueryContext`/`QueryRowContext`) and works
  identically whether or not it happens to be running inside a
  transaction. **Any role implementing a store on top of this (e.g.
  `checkpoint`, `predictor`) should follow this
  `QuerierFromContext(ctx, db)` pattern rather than threading a `*sql.Tx`
  through their own function signatures.**
- **Migration naming convention**: `NNNN_name.sql` (4+ zero-padded
  digits, underscore, `[a-zA-Z0-9_]+` name, `.sql` extension), enforced
  by `migrate.go`'s `migrationFilePattern` regex and `LoadMigrationsFS`,
  matching `CONTRACT_FREEZE.md`'s foundation range (0000-0009) and ADD
  §12.5's `0001_name.sql` example. `LoadMigrationsFS(fsys fs.FS, root
  string)` reads every `*.sql` file directly under `root` (typically a
  `go:embed` of `internal/storage/sqlite/migrations`), parses filenames,
  and returns `[]Migration` sorted ascending by version — a duplicate
  version or malformed filename is a hard error (fail-closed, not
  skip-and-warn). `(*DB).Migrate(ctx, migrations)` applies every
  migration whose version exceeds the database's current highest applied
  version, each inside its own transaction, recording it in an
  auto-created `schema_migrations(version, name, applied_at)` table. If
  the database's current version is HIGHER than any version in the
  passed-in migration set (this binary is older than whatever last
  migrated the DB), `Migrate` returns `ErrSchemaNewerThanBinary` and
  applies nothing — callers MUST treat this as fail-closed/read-only per
  ADD §12.5. **No migration `.sql` files exist yet** — that is
  foundation-06, explicitly out of scope for foundation-05; `Migrate`
  against an empty/nil migration slice is a tested no-op that still
  creates `schema_migrations`.
- **Dependency requests**: none outstanding. `go.mod` now carries
  `github.com/google/uuid` (UUIDv7 IDGenerator), `github.com/spf13/cobra`
  (CLI), and `go.yaml.in/yaml/v3` (YAML config load — promoted from an
  indirect cobra dependency to a direct one in foundation-03; same API
  surface as the well-known `gopkg.in/yaml.v3`). No other role has
  requested a new dependency via its progress artifact as of this commit.
- **Config precedence/merge algorithm** (foundation-03): `internal/config.Load`
  takes an unordered slice of `Layer{Source, Bytes}` and merges them in the
  fixed order from ADD §26.1 (defaults < global_user_config < repo_config <
  repo_local < environment < cli_flags), regardless of caller-supplied
  order. Merge is a shallow top-level-key replace, not a recursive deep
  merge — no section has a typed, consumed shape yet to design a deep-merge
  algorithm against. `Config.Raw` is the generic decoded map; roles needing
  a specific section decode it themselves once they have a real consumer.
  `schema_version` must equal `preflight.config.v1` or Load errors.
  Unknown top-level fields warn by default and are collected in
  `Config.UnknownFields`; `Options.UnknownFieldPolicy = StrictUnknownFields`
  turns that into a Load error instead (ADD §26.2).

## Node log

```yaml
node: foundation-01
status: completed
artifacts:
  - cmd/preflight/main.go
  - cmd/preflight/main_test.go
  - internal/buildinfo/buildinfo.go
  - internal/clock/clock.go
  - internal/clock/clock_test.go
  - internal/idgen/idgen.go
  - internal/idgen/idgen_test.go
  - go.mod
  - go.sum
validation:
  - "gofmt -l cmd internal/buildinfo internal/clock internal/idgen   # empty output"
  - "go build ./cmd/preflight/... ./internal/buildinfo/... ./internal/clock/... ./internal/idgen/..."
  - "go vet ./cmd/preflight/... ./internal/buildinfo/... ./internal/clock/... ./internal/idgen/..."
  - "go test ./internal/clock/... ./internal/idgen/... ./cmd/preflight/... -v   # all PASS"
  - "go build -o preflight ./cmd/preflight && ./preflight version   # prints 0.0.0-dev"
commit: 797c450
next_action: foundation-02 (blocked - not started this wave; OS-correct config/data/cache/runtime paths)
assumptions:
  - "internal/buildinfo.Version is a hardcoded \"0.0.0-dev\" string constant,
    not wired to ldflags/git describe. agents/foundation.md explicitly
    permits this for foundation-01 (\"a hardcoded ... is fine ... do not
    over-build\"); real release versioning is out of scope until release
    packaging (also explicitly out of scope per agents/foundation.md)."
  - "UUIDv7 chosen via github.com/google/uuid's NewV7(), matching
    CONTRACT_FREEZE.md's \"UUIDv7 at generation time (owned by foundation's
    internal/idgen)\" requirement. No alternative UUIDv7 library was
    evaluated since google/uuid is the de facto standard Go UUID package
    and already satisfies the requirement."
  - "Cobra was chosen over a plain flag-based stub per the task instruction
    (\"a stub preflight version command using Cobra now is preferable\")
    and matches Preflight_ADD.md's tech-stack table (CLI: Cobra, line ~192)."
  - "cmd/preflight/main_test.go was added even though agents/foundation.md's
    Required Tests list only names \"version command\" abstractly with no
    file path — treated as satisfying that requirement without needing a
    separate internal/cli package, since runtime (not foundation) owns the
    real CLI surface later (internal/cli per the DAG's runtime-b01)."
blockers: []
```

```yaml
node: foundation-02
status: completed
artifacts:
  - internal/paths/paths.go
  - internal/paths/env.go
  - internal/paths/fake_env_test.go
  - internal/paths/paths_test.go
validation:
  - "gofmt -l internal/paths   # empty output"
  - "go build ./internal/paths/..."
  - "go vet ./internal/paths/..."
  - "go test ./internal/paths/...   # PASS, 10 test cases"
commit: 2820015
next_action: foundation-03 (YAML config load and precedence)
assumptions:
  - "internal/paths resolves only the GLOBAL, non-repository-local
    directories (config/data/cache/runtime under the user's home or OS
    convention). Repository-local .preflight/config.yaml,
    .preflight/*.db, .preflight/checkpoints/, .preflight/runtime/ (ADD
    §26.3) are NOT resolved by this package — those are relative to a
    repository root, which is a different role's concern (repository
    scoping is out of scope for foundation per agents/foundation.md).
    This package supplies the \"global user config\" layer named in
    ADD §26.1's precedence chain and the base dirs a future
    repository-scoping helper can join a repo path onto."
  - "Env is a 2-method interface (Getenv, UserHomeDir) covering only what
    path resolution needs — deliberately not a full os.Getenv/os.Environ
    wrapper, to avoid over-widening per Constitution §4's God-interface
    warning (by analogy from the provider-interface rule)."
  - "Linux and all other non-darwin/non-windows GOOS values are resolved
    via one shared XDG Base Directory implementation (XDG_CONFIG_HOME /
    XDG_DATA_HOME / XDG_CACHE_HOME / XDG_RUNTIME_DIR with documented
    fallbacks), since Preflight's portability goal is POSIX-general, not
    Linux-only, and no ADD text calls out per-BSD path differences."
  - "macOS has no OS convention distinct from XDG_CONFIG_HOME for
    'config' vs 'data'; both map to ~/Library/Application Support/preflight,
    matching common Go CLI practice on macOS. No XDG_RUNTIME_DIR
    equivalent exists on macOS or when XDG_RUNTIME_DIR is unset on Linux;
    both fall back to a `run/` subdirectory of the cache dir."
  - "Windows paths are joined with a literal backslash via a small
    winJoin helper, NOT filepath.Join/path.Join — filepath.Join's
    separator follows the host GOOS (would silently produce forward
    slashes when the Windows-path-table test runs on macOS/Linux CI
    runners), and path.Join is hardcoded to forward slash. This was
    caught by the required Windows/macOS/Linux path-table tests
    (agents/foundation.md \"Required tests\") failing on first run on
    this darwin dev host, exactly the scenario the DAG's foundation-02
    risk note (\"Windows path behavior needs CI matrix\") anticipated —
    resolved without needing a CI matrix because the test fakes GOOS
    input rather than relying on the host's actual OS."
  - "No PREFLIGHT_* environment variable convention for overriding these
    directories is introduced yet (e.g. no PREFLIGHT_CONFIG_DIR) — ADD
    and CONTRACT_FREEZE.md do not name one, and inventing one now would
    be speculative surface agents/foundation.md's Constitution §7 rule 10
    (no abstractions a later milestone would need but this one doesn't)
    warns against. CLI flag / env overrides at the config-precedence
    level are foundation-03's concern (ADD §26.1), not this package's."
blockers: []
```

```yaml
node: foundation-03
status: completed
artifacts:
  - internal/config/config.go
  - internal/config/errors.go
  - internal/config/config_test.go
  - go.mod (go.yaml.in/yaml/v3 promoted to direct dependency)
  - go.sum
validation:
  - "gofmt -l internal/config   # empty output"
  - "go build ./internal/config/..."
  - "go vet ./internal/config/..."
  - "go test ./internal/config/...   # PASS, 15 test cases"
commit: 0164673
next_action: foundation-04, reduced scope (internal/lock only)
assumptions:
  - "Deliberately did NOT model ADD §26.4's full default-configuration tree
    (runtime/privacy/prediction/risk/state_checkpointing/
    repository_checkpoint/graceful_pause) as typed Go structs. As of this
    node zero packages in this repository read a single config field —
    predictor/policy/checkpoint/runtime business logic does not exist yet
    and is explicitly out of scope for foundation. Inventing typed structs
    for fields nothing consumes would violate Constitution §7 rule 10.
    Instead, Config.Raw is a generic decoded map; section names from ADD
    §26.4 are registered in knownTopLevelFields ONLY for unknown-field
    detection (so a real, ADD-documented section name is never flagged as
    unknown), without validating any section's internal shape. A later
    role that actually consumes a section (e.g. predictor reading
    `prediction:`) decodes Raw[\"prediction\"] into its own typed struct
    at that point — this package does not pre-guess that shape."
  - "Merge semantics are a shallow top-level-key replace, not a recursive
    deep merge. Example: if defaults sets `runtime: {a: 1, b: 2}` and
    repo_config sets `runtime: {a: 9}`, the merged result is `runtime: {a:
    9}` — b is dropped, not preserved — because repo_config's `runtime`
    key fully replaces defaults' `runtime` key. This is a real,
    documented limitation, not an oversight: no section has a concrete,
    consumed shape yet to design and test a correct deep-merge algorithm
    against, and building one speculatively risks getting the merge
    semantics wrong for a shape that doesn't exist yet. Flagged here per
    Constitution §4.4 so a future role (or contract-integrator) can
    request deep merge explicitly once a section actually needs it."
  - "go.yaml.in/yaml/v3 (not gopkg.in/yaml.v3) was already present as an
    indirect dependency of spf13/cobra and was promoted to a direct
    top-level dependency via `go mod tidy` rather than adding a second,
    competing YAML library. It is the actively maintained successor with
    an API-compatible surface to gopkg.in/yaml.v3 (same package name
    `yaml`, same Marshal/Unmarshal signatures)."
  - "LoadFile treats a missing file as an empty Layer (not an error),
    matching ADD §26.1: every layer below CLI flags/environment (global
    user config, repo config, repo local config) is optional. Callers
    that need to distinguish 'file legitimately absent' from 'path wrong'
    do so before calling LoadFile; this package does not guess intent."
  - "No CLI wiring (`preflight config show/validate`) was added — ADD's
    CLI/API section places those commands under `runtime`'s ownership
    (agents/foundation.md: 'the runtime role owns user-facing commands'),
    and foundation-03's own DAG row scope is the load/precedence library
    only."
blockers: []
```

```yaml
node: foundation-04
scope_note: >
  REDUCED SCOPE per task instruction. The DAG's original foundation-04 row
  ("Clock/IDGen/lock impls") is only partially this node's work: clock and
  idgen were already fully implemented under foundation-01 in Wave 1 (see
  that node's log above) and are NOT touched or reimplemented here. This
  node's actual new work is internal/lock only.
status: completed
artifacts:
  - internal/lock/lock.go
  - internal/lock/process_unix.go
  - internal/lock/process_windows.go
  - internal/lock/lock_test.go
validation:
  - "gofmt -l internal/lock   # empty output"
  - "go build ./internal/lock/..."
  - "go vet ./internal/lock/..."
  - "go test ./internal/lock/...   # PASS, 8 test cases"
commit: 1ce3c50
next_action: foundation-05 (SQLite engine)
assumptions:
  - "No Lock interface/type is frozen anywhere in internal/domain or
    internal/app/ports.go (confirmed by grep before starting this node) —
    the exact lock mechanism was explicitly foundation's call per the task
    instruction ('your call on the exact mechanism'). Chose a PID-file-
    style advisory lock (os.O_EXCL exclusive create + PID contents), not
    an OS-level flock/LockFileEx syscall, because: (1) it needs zero new
    dependencies (no golang.org/x/sys), (2) Preflight_ADD.md SS1.4 fixes
    the runtime architecture as a single-machine 'modular monolith', so
    only same-machine, not networked, mutual exclusion is required, and
    (3) it is trivially crash-recoverable (see next bullet), which matters
    more for a local daemon that can be killed at any point mid-turn than
    strict kernel-enforced exclusivity would."
  - "Stale-lock recovery: if a lock file exists but its recorded PID is not
    a live process, Acquire treats it as abandoned (e.g. left behind by a
    crashed daemon), removes it, and reacquires fresh rather than wedging
    the machine forever. This is a deliberate crash-safety property, not a
    weakening of the lock's guarantee — a purely kernel-level flock would
    give this for free (locks release automatically when the holding
    process dies), but the chosen O_EXCL design needed it built explicitly
    since a plain file's existence otherwise persists across a crash."
  - "processAlive is platform-specific (process_unix.go / process_windows.go
    via build tags) because POSIX and Windows have fundamentally different
    liveness-check primitives: POSIX uses the null signal (kill(pid, 0))
    since os.FindProcess never fails on POSIX; Windows' os.FindProcess
    actually opens a process handle and fails if the PID does not exist,
    so success alone is sufficient there. Both files build cleanly on
    darwin (this dev host) via the !windows/windows build constraints;
    the windows-tagged file's correctness rests on documented Go stdlib
    behavior (verified against Go's os package docs) since it cannot be
    exercised on this darwin host without a Windows CI matrix (`qa-01`)."
  - "internal/lock intentionally does not integrate with internal/storage/
    sqlite (foundation-05, not yet built) or wire an actual daemon
    single-instance guard into cmd/preflight — that wiring belongs to
    whichever later node actually starts a long-lived daemon process
    (runtime role, out of scope for foundation per agents/foundation.md).
    This node delivers the reusable primitive only."
blockers: []
```

```yaml
node: foundation-05
status: completed
artifacts:
  - internal/storage/sqlite/db.go
  - internal/storage/sqlite/migrate.go
  - internal/storage/sqlite/db_test.go
  - internal/storage/sqlite/migrate_test.go
  - go.mod (modernc.org/sqlite promoted to direct dependency)
  - go.sum
validation:
  - "gofmt -l internal/storage/sqlite   # empty output"
  - "go build ./internal/storage/sqlite/..."
  - "go vet ./internal/storage/sqlite/..."
  - "go test ./internal/storage/sqlite/...   # PASS, 24 test cases"
commit: b0ef5a0
next_action: foundation-09 (Makefile/Taskfile/lint config) — foundation-06
  (actual migration .sql files) is explicitly NOT in this wave's scope
assumptions:
  - "Pragmas (ADD SS12.1 / CONTRACT_FREEZE.md) are applied TWICE, by
    design, not redundantly by accident: (1) encoded as _pragma DSN query
    parameters on every Open() call, per modernc.org/sqlite's documented
    DSN convention, so every connection the pool subsequently opens gets
    them automatically; AND (2) executed explicitly via applyPragmas
    against the pool's first connection immediately after Open. This is
    belt-and-braces given the High-risk flag this node carries in
    EXECUTION_DAG.md ('WAL/busy-timeout/FK pragmas are load-bearing for
    every later role') — verified with tests that actually query each
    pragma's live value (PRAGMA journal_mode etc.), not merely that Open()
    returns no error, per the task instruction's explicit warning against
    that shortcut."
  - "TestBusyTimeout_ConcurrentWriteWaitsInsteadOfFailingImmediately
    exercises the busy_timeout pragma's REAL effect (a second writer waits
    behind an uncommitted transaction and succeeds once it commits,
    instead of failing instantly with SQLITE_BUSY) rather than only
    asserting the pragma's reported value — this is the 'locked/busy
    behavior' required test from agents/foundation.md."
  - "app.TxFunc's frozen signature (func(ctx context.Context) error, no
    *sql.Tx parameter) forced a context-based transaction handoff design:
    WithTx stores the active *sql.Tx under an unexported context key and
    callers retrieve it via the new QuerierFromContext(ctx, db) helper.
    This was not a free design choice — internal/app/ports.go is frozen
    and owned exclusively by contract-integrator (Constitution SS4.3), so
    foundation could not add a *sql.Tx parameter to TxFunc even if a
    'pass the tx directly' design would have been simpler. This pattern
    (QuerierFromContext) is the one every later role's store MUST use to
    stay compatible with the frozen WithTx boundary — documented in the
    Handoff section above and worth flagging explicitly to
    checkpoint/predictor/claude-provider/runtime, whichever role writes
    the first real store on top of this engine."
  - "modernc.org/sqlite (pure Go, no CGO) was added per Preflight_ADD.md
    SS1.4's explicit tech-stack decision, exactly as pre-authorized by the
    task instruction. go mod tidy pulled a substantial transitive tree
    (modernc.org/libc, cc/v4, ccgo/v4, etc.) — all standard for this
    driver's pure-Go C-transpilation approach, not a foundation design
    choice; no alternative driver was evaluated since ADD names this one
    specifically."
  - "LoadMigrationsFS is deliberately strict: a migration filename that
    doesn't match NNNN_name.sql, or two files claiming the same version,
    is a hard error, not a skip-with-warning. A silently-skipped or
    silently-reordered migration is a state-integrity bug per
    CONTRACT_FREEZE.md's fail-closed rule for state-integrity failures
    (as opposed to fail-open-able operational-observation failures), so
    this errs toward refusing to proceed rather than guessing intent."
  - "No internal/storage/sqlite/migrations/ directory and no actual .sql
    files were created — that is foundation-06's scope (Core migrations
    0000-0009), explicitly NOT assigned to foundation this wave per the
    task instruction. Migrate()/LoadMigrationsFS() are fully implemented
    and tested against synthetic in-test migrations (via testing/fstest
    and inline Migration structs) so foundation-06 has a ready, proven
    engine to point real .sql files at without needing further engine
    changes."
  - "SetMaxOpenConns(8) is a conservative, undocumented-in-ADD default —
    no later role's real concurrency profile exists yet to tune against,
    and ADD does not specify a connection pool size. Flagged here as an
    assumption a later role may need to revisit once real concurrent
    daemon+CLI+scheduler access patterns exist."
blockers: []
```

```yaml
node: foundation-09
status: completed
artifacts:
  - Taskfile.yml
  - Makefile
  - .golangci.yml
  - internal/lock/lock.go (lint fix: nilerr nolint annotation)
  - internal/lock/lock_test.go (lint fix: check Release() error in defers)
  - internal/storage/sqlite/db.go (lint fix: errorlint %w for rollback error)
  - internal/storage/sqlite/db_test.go (lint fix: check Close() error in
    defers; errors.Is instead of == for sentinel comparison)
  - internal/storage/sqlite/migrate.go (lint fix: errorlint %w for
    strconv error)
  - internal/storage/sqlite/migrate_test.go (lint fix: check Close()
    error in defers)
  - internal/clock/clock_test.go (lint fix: staticcheck QF1011 nolint
    annotation, assertion intentionally kept explicit)
  - internal/idgen/idgen_test.go (same as above)
validation:
  - "task lint   # go vet + golangci-lint run ./... -> 0 issues"
  - "task build   # go build -o bin/preflight ./cmd/preflight; ./bin/preflight version -> 0.0.0-dev"
  - "task test    # go test -race ./... -> all packages PASS"
  - "make lint && make build   # Makefile mirror verified equivalent to Taskfile"
commit: 2eac579
next_action: none — this was the last node assigned this wave (foundation-06,
  -07, -08 are explicitly out of scope; STOP per task instruction)
assumptions:
  - "Taskfile.yml is the richer/primary task runner (default task runs
    fmt+lint+test; separate fmt/fmt:fix, test/test:short, tidy, clean,
    run targets); Makefile is a deliberately thin, dependency-free mirror
    of the same target set for contributors/CI steps that only have
    `make`. agents/foundation.md's exclusive-paths glob names both files
    without stating a primary, so keeping them behaviorally equivalent
    (not divergent) was the safest reading, per the task instruction's
    'both files should exist and be consistent.'"
  - "Neither `task` (go-task/task) nor `golangci-lint` was preinstalled on
    this dev machine; `brew install` failed (outdated Xcode Command Line
    Tools blocking bottle installation, unrelated to this repo). Both
    were installed via `go install .../cmd/task@latest` and
    `go install .../cmd/golangci-lint@latest` into $(go env GOPATH)/bin
    instead — this does NOT touch the repository's own go.mod/go.sum
    (tool installation via `go install` of a separate module is
    independent of the current module's dependency graph), and both
    tools were then used to actually execute and verify `task lint` /
    `task build`, not merely written and assumed correct."
  - ".golangci.yml targets golangci-lint v2 config schema (`version: \"2\"`
    top-level key; linters.default: standard plus an explicit enable
    list; formatters block for gofmt/goimports) since v2 is the version
    that installed via `go install .../golangci-lint/v2/cmd/golangci-lint@latest`
    and is what actually ran during validation — an older v1-schema config
    would not have been validated against a real tool run."
  - "Running golangci-lint surfaced 16 real, fixable issues across files
    from THIS wave's earlier nodes (foundation-02 through -05) plus two
    from foundation-01 (Wave 1): unchecked defer'd Close()/Release()
    errors (errcheck), %v used where %w should wrap an error (errorlint),
    a direct == comparison on a sentinel error instead of errors.Is
    (errorlint), a deliberate-but-linter-suspicious nil return after a
    non-nil err check in internal/lock (nilerr, suppressed with a
    documented nolint since the behavior is intentional, not a bug), and
    two staticcheck QF1011 hints on Wave-1 test files where an explicit
    interface type on a var declaration is redundant with the RHS
    function's return type (suppressed with nolint since the explicit
    type is the test's documentation value, not an oversight). All were
    fixed rather than blanket-suppressed at the linter-config level,
    since agents/foundation.md's validation bar is `task lint` passing
    cleanly, not a permissive config that hides real signal."
  - "No LICENSE or NOTICE file was added even though both are named in
    foundation's exclusive-paths list and are currently missing from the
    repository entirely. This task's node list (foundation-02 through
    foundation-09) never assigns LICENSE/NOTICE creation to any node in
    this wave, and foundation-09's own validation command (`task lint &&
    task build`) does not depend on them existing. Creating them now
    would be scope creep beyond the assigned nodes, not a natural
    byproduct of Makefile/Taskfile/.golangci.yml work — flagged here so a
    future wave assigns it explicitly (owning role is foundation per the
    exclusive-paths list) rather than it being silently done or silently
    forgotten."
  - "bin/ (the build output directory task build/make build creates) is
    not committed and was removed after validation; no .gitignore entry
    for it was added since .gitignore is not in foundation's declared
    exclusive paths list — flagged as a gap another role or a future
    foundation node may want to close, not fixed here to avoid touching
    a file outside this role's ownership."
blockers: []
```
