# qa — Progress Artifact

## Handoff notes (Constitution §6.7 / agents/qa.md)

- **CI entry point**: `.github/workflows/ci.yml` (qa-01). Calls into
  `Taskfile.yml` targets only (`task fmt`, `task lint`, `task build`,
  `task test`, `task test:short`) — it does not invoke `go`/
  `golangci-lint` directly, so any future change to how those checks run
  (e.g. a new lint rule, a new build flag) only needs to change
  `Taskfile.yml`/`Makefile`, not the workflow file, per this node's
  instruction not to duplicate or conflict with `foundation`'s task
  runner. `golangci-lint` itself is installed in CI via
  `golangci/golangci-lint-action@v6` (pinned major version, `version:
  latest` for the tool binary) rather than a bare `go install`, since the
  action also wires GitHub-native lint annotations or a compatible
  config format for `.golangci.yml`'s v2 schema.
- **Race detector platform split**: `task test` (`go test -race ./...`)
  runs on `ubuntu-latest` and `macos-latest`; `windows-latest` runs
  `task test:short` (no `-race`) instead. This is a deliberate,
  documented choice (see the workflow file's inline comment), not an
  oversight — reasoning is in the qa-01 node log's `assumptions` below.
- **No VS Code / JSON Schema / migration-test CI jobs yet**: ADD §30.3
  names `ci.yml` jobs for VS Code lint/test/build, JSON Schema checks,
  docs link/fence checks, and migration tests. None of those trees exist
  yet in this wave (no `vscode/`, no JSON Schema artifacts, no
  `internal/storage/sqlite/migrations/*.sql` — `foundation-06` is still
  pending per `docs/implementation/day1/foundation.md`). Scaffolding CI
  jobs against paths that don't exist would violate Constitution §7 rule
  10 (no abstractions a later milestone needs but this one doesn't) —
  flagged here so whichever future qa/CI node adds those trees also
  extends `ci.yml`, rather than this gap being silently forgotten.
- **`security.yml`, `provider-contract.yml`, `release.yml`**: named in
  ADD §30.3 but out of scope for qa-01 (whose validation target is
  narrowly "CI green on a trivial PR (Ubuntu/macOS/Windows)" per the
  execution DAG). Not created this wave.
## Node log

```yaml
node: qa-01
status: completed
artifacts:
  - .github/workflows/ci.yml
validation:
  - "python3 -c \"import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))\"   # YAML parses without error"
  - "actionlint .github/workflows/ci.yml   # 0 findings (installed via go install github.com/rhysd/actionlint/cmd/actionlint@latest)"
  - "task fmt   # PASS, no unformatted files"
  - "task lint   # go vet + golangci-lint run ./... -> 0 issues"
  - "task build   # go build -o bin/preflight ./cmd/preflight -> succeeds"
  - "task test   # go test -race ./... -> all 18 packages PASS (14 with tests, 4 no-test-files packages)"
  - "task test:short   # go test ./... (no -race, the Windows-job path) -> all PASS"
commit: PENDING_QA01_COMMIT
next_action: qa-08 (governance docs)
assumptions:
  - "Every command the workflow's `run:` steps invoke (`task fmt`, `task
    lint`, `task build`, `task test`, `task test:short`) is a real,
    already-existing target read directly out of Taskfile.yml before
    writing the workflow — none invented. `golangci-lint` itself is not
    called directly in any `run:` step; `task lint` depends on `vet` and
    then shells out to `golangci-lint run ./...` exactly as
    `.golangci.yml`/Taskfile.yml already define it, so the workflow adds
    zero new lint configuration or duplicate rules."
  - "lint/fmt run once on ubuntu-latest only, not across the full OS
    matrix. gofmt/go vet/golangci-lint operate on source text and Go AST,
    which do not vary by host OS (no OS-conditional Go source exists yet
    in this repository via build tags that would change lint output
    cross-platform) - foundation's internal/lock package does have
    process_unix.go/process_windows.go build-tag-split files, but both
    are still plain Go source parsed identically by gofmt/vet/lint
    regardless of which OS the *linter* itself runs on; only *compiling*
    each file requires the matching target OS, which is what the
    build-and-test matrix job (not the lint job) does. Running lint 3x
    for a trivial PR would be pure redundant cost with no additional
    signal, contrary to keeping a 'trivial PR green' check fast."
  - "Race detector: `-race` requires CGO_ENABLED=1 and a working C
    toolchain. ubuntu-latest and macos-latest GitHub-hosted runners ship
    gcc/clang by default, matching this node's task instruction
    ('Windows has some race-detector limitations historically ... run
    race only on ubuntu/macos if that's the safer choice, and document
    why'). windows-latest GitHub-hosted runners do carry a working MinGW
    gcc today, so `-race` would likely run there too, but it has a
    documented history of being the least reliable combination in CI
    (slower instrumentation, occasional flaky failures tied to
    Go-version/MinGW-version pairings, and Go's own release notes have
    called out narrower Windows race-detector support in some past
    versions) - given qa-01's validation bar is 'CI green on a trivial
    PR,' introducing a known flake source on one matrix leg for coverage
    this wave does not yet exploit (no concurrency-heavy code outside
    internal/lock/internal/storage/sqlite exists yet) was judged not
    worth it. Race coverage is not dropped project-wide: two of three
    matrix legs (ubuntu, macos) run the full `-race` suite every PR, and
    `task test:short` still runs the full non-race suite on Windows, so
    a Windows-only compile/logic regression would still be caught, just
    not a Windows-only data race."
  - "golangci-lint is installed in the lint job via
    `golangci/golangci-lint-action@v6` rather than `go install
    .../golangci-lint/v2/cmd/golangci-lint@latest` (the method
    foundation-09 used locally per docs/implementation/day1/foundation.md).
    The action is the documented, cached, GitHub-native way to run
    golangci-lint in Actions and natively understands the v2 config
    schema `.golangci.yml` already uses; `go install` would work too but
    would re-download/re-build the linter from source on every run with
    no built-in caching. `version: latest` keeps it on the v2 line
    (`.golangci.yml` starts with `version: \"2\"`) without hardcoding a
    version number the workflow would need manual upkeep for."
  - "`task`/`golangci-lint` were not preinstalled in this darmin dev
    worktree either (same gap foundation-09 documented on its own dev
    host) - both were available at $(go env GOPATH)/bin because this
    worktree shares the same GOPATH as the main checkout where
    foundation-09 installed them via `go install`; no new install was
    needed for this node beyond adding that directory to PATH for the
    validation session. `actionlint` (not previously installed by any
    role) was newly installed via `go install
    github.com/rhysd/actionlint/cmd/actionlint@latest` solely to
    strengthen this node's own YAML/schema validation beyond a bare
    `yaml.safe_load` parse - this is a one-off local validation tool, not
    a repository dependency, and is not referenced by go.mod, Taskfile.yml,
    or the workflow file itself."
  - "`arduino/setup-task@v2` is used to install the `task` binary inside
    the build-and-test job (needed there because that job calls `task
    build`/`task test`), while the lint job only needs `golangci-lint`
    (installed via its own action) plus `task fmt`/`task lint` - so
    `setup-task` is duplicated into both jobs rather than shared, since
    GitHub Actions jobs run on independent, ephemeral runners with no
    shared filesystem state between jobs in the same workflow run."
  - "No `security.yml`, `provider-contract.yml`, or `release.yml` created
    this node - ADD §30.3 names all four but qa-01's own DAG row scope
    (validation: 'CI green on a trivial PR (Ubuntu/macOS/Windows)') is
    the basic build/lint/test workflow only. The other three depend on
    infrastructure (a provider fixture corpus, a release/signing
    pipeline, govulncheck/CodeQL wiring) that doesn't exist yet this
    wave and is scope creep beyond this node."
blockers: []
```

