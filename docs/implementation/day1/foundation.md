# foundation — Progress Artifact

## Handoff notes (Constitution §6.7 / agents/foundation.md "Handoff")

- **DB constructor**: not yet built. Deferred to foundation-05
  (`internal/storage/sqlite/db.go`), out of scope for this wave.
- **Transaction API**: not yet built. Deferred to foundation-05/06;
  must conform to `app.TxRunner.WithTx` frozen in `internal/app/ports.go`.
- **Migration naming convention**: not yet decided in code (no migration
  files exist yet). `CONTRACT_FREEZE.md` fixes foundation's numeric range
  as 0000-0009; the `NNNN_description.sql` filename shape referenced by
  `agents/foundation.md`'s exclusive-paths glob
  (`internal/storage/sqlite/migrations/0000-0009_*.sql`) is the only
  naming detail frozen so far. Deferred to foundation-06.
- **Dependency requests**: none outstanding. `go.mod` now carries
  `github.com/google/uuid` (UUIDv7 IDGenerator) and `github.com/spf13/cobra`
  (CLI). No other role has requested a new dependency via its progress
  artifact as of this commit.

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
commit: pending (recorded after commit; see git log for foundation-02)
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
