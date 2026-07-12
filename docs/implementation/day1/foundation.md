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
