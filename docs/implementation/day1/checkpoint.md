# checkpoint — Progress Artifact

Role covers two internal sub-components (Part A: Progress Tree / State
Checkpointing; Part B: Repository Checkpoint), kept separate per
`agents/checkpoint.md`. This wave, only one node was unblocked and assigned:
`checkpoint-b02`. `checkpoint-a01` and `checkpoint-b01` remain queued behind
`foundation-06` (core SQLite migration harness), which was not complete this
wave — per the frozen `EXECUTION_DAG.md`, both were correctly withheld
rather than started against an incomplete dependency.

```yaml
node: checkpoint-b02
status: completed
artifacts:
  - internal/gitx/runner.go        # domain.ProcessRunner exec.Command-backed impl (argv-only, never a shell string)
  - internal/gitx/client.go        # Client wrapping ProcessRunner with git-specific ops (Status)
  - internal/gitx/resolver.go      # RepoInfo + ResolveRepo: worktree root / git-dir / common-dir resolution via `git rev-parse --path-format=absolute`
  - internal/gitx/porcelain.go     # ParsePorcelainV2: `git status --porcelain=v2 -z` parser (changed/renamed/unmerged/untracked/ignored + branch headers)
  - internal/gitx/gitx_test.go     # shared repoBuilder test scaffolding (real temp git repos via ExecRunner, argv-only)
  - internal/gitx/resolver_test.go # main worktree, linked worktree, not-a-repo, nonexistent-path cases
  - internal/gitx/porcelain_test.go # tracked/staged/unstaged/untracked/rename/delete scenarios + parser fixture edge cases
validation:
  - "gofmt -l internal/gitx                                                     # empty output"
  - "go build ./internal/gitx/...                                               # OK"
  - "go vet ./internal/gitx/...                                                 # OK"
  - "go test ./internal/gitx/... -run 'Porcelain|Fingerprint|Resolver' -v       # PASS, all subtests green (Fingerprint has no matches yet — that's checkpoint-b03 scope)"
  - "go test ./internal/gitx/... -race -v                                       # PASS"
  - "go build ./... && go vet ./...                                             # whole-repo build/vet unaffected"
commit: 9b222d0
next_action: superseded by the Wave 2 entry below
assumptions:
  - "Status() pins --branch --untracked-files=all --find-renames on top of --porcelain=v2 -z so output is deterministic regardless of the caller's git config (status.renames, status.showUntrackedFiles). This is additive to the exact flag string in the DAG's validation command and does not change the parser's contract."
  - "ParsePorcelainV2 fails closed (returns a domain.Error with ErrCodeValidation) on any record shape it doesn't recognize, rather than silently skipping it — this parser feeds the Repository Checkpoint integrity boundary (Constitution §6), so an unintelligible status must not be treated as 'no changes.'"
  - "ResolveRepo requires git >= 2.31 for `rev-parse --path-format=absolute`; not verified against older git in this environment. Local git version used for tests: 2.37.3."
  - "Unmerged (conflict) entries and ignored entries are parsed by the porcelain layer (fixture-tested) but have no live-repo integration test in this wave, since provoking a real merge conflict/ignore rule in a throwaway temp repo added setup complexity out of proportion to checkpoint-b02's scope; they are covered by TestParsePorcelainV2Fixtures instead. checkpoint-b03/b04 should add a live-repo conflict case if the fingerprint or checkpoint-create logic branches on unmerged state."
blockers:
  - "checkpoint-a01 and checkpoint-b01 blocked pending foundation-06 (core SQLite migration harness) — not started this wave, per explicit wave assignment."
```

---

## Wave 2

Assigned node this wave: `checkpoint-b03` (Snapshot fingerprint), dependency
`checkpoint-b02` satisfied on this branch. Per explicit wave instruction, no
merge/rebase onto main was performed (ADR-041 touches only predictor domain
types, nothing `internal/gitx` depends on). `checkpoint-a01`, `checkpoint-b01`,
and `checkpoint-b04` remain unstarted, per assignment.

```yaml
node: checkpoint-b03
status: completed
artifacts:
  - internal/gitx/fingerprint.go      # Fingerprint struct (repo identity, worktree path, branch/HEAD, status entries, index+worktree numstat, untracked policy metadata) + canonical SHA-256 digest + Client.Fingerprint orchestration
  - internal/gitx/numstat.go          # NumstatEntry, Client.DiffNumstat (git diff [--cached] --numstat -z --no-ext-diff --find-renames), ParseNumstatZ fail-closed parser
  - internal/gitx/fingerprint_test.go # determinism/reversibility, worktree/staged/untracked/HEAD change detection, rename, binary, spaced paths, unborn branch, linked worktree, digest order-independence + per-field sensitivity, numstat parse fixtures + fail-closed rejections
validation:
  - "gofmt -l internal/gitx                              # empty output"
  - "go build ./internal/gitx/...                        # OK"
  - "go vet ./internal/gitx/...                          # OK"
  - "go test ./internal/gitx/... -run Fingerprint -v     # PASS, all subtests green (DAG validation command)"
  - "go test ./internal/gitx/... -race                   # PASS, full existing suite unchanged (no regressions)"
  - "go build ./... && go vet ./...                      # whole-repo build/vet unaffected"
commit: 0281b97
next_action: checkpoint-b04 (Repository Checkpoint create/verify) is now unblocked on the b03 side but still gated on checkpoint-b01 (migrations 0030-0039, itself gated on foundation-06) — not started this wave, per assignment
assumptions:
  - "Digest scope: covers FingerprintSchema, WorktreeRoot, CommonDir, IsLinkedWorktree, HeadOID, Branch, UntrackedPolicy, sorted status Entries, and sorted index/worktree numstat. Upstream/Ahead/Behind are carried as informational fields but deliberately EXCLUDED from the digest: they move on `git fetch` (remote-tracking refs), which does not change the local worktree/index/HEAD state that FR-149 resume validation protects — a background fetch must not invalidate a resume. If checkpoint-b04 or runtime needs remote-divergence in the identity, that is an additive schema bump (preflight.gitx.fingerprint.v2), not a breaking change."
  - "Canonical encoding is netstring-style length-prefixed fields hashed in fixed order, with Entries/numstat sorted by (Path, OrigPath[, Kind]) before hashing — digest is independent of git's emission order and immune to path-content boundary forgery (spaces/tabs/newlines in paths are covered by tests)."
  - "A Fingerprint is a point-in-time read composed of three git invocations (status + two numstats), not an atomic capture. Concurrent-mutation detection is the ADD §19.3 initial/final-fingerprint-compare protocol, which is checkpoint-b04/b07 scope; Fingerprint.Equal (digest compare, fail-closed on empty digest) is the primitive those nodes will use."
  - "ParseNumstatZ fails closed (domain ErrCodeValidation) on any unrecognized record shape, matching ParsePorcelainV2's integrity-boundary posture from checkpoint-b02."
  - "`git diff --cached --numstat` on an unborn branch (no commits) diffs against the empty tree — verified against git 2.37.3 and covered by TestFingerprintUnbornBranch, so fingerprinting a freshly-initialized staged repo works."
  - "app.Authorization.SnapshotFingerprint (frozen ports.go) is a plain string; Fingerprint.Digest (sha256 hex) satisfies it directly. No contract gap found; no ports.go change requested."
blockers:
  - "checkpoint-a01, checkpoint-b01 still blocked on foundation-06; checkpoint-b04 additionally on checkpoint-b01. None started this wave, per explicit assignment."
```

Final commit for checkpoint-b03: `0281b97` (code + docs), with this SHA
recorded in a follow-up commit, same pattern as Wave 1's `9b222d0`/`94be461`.

## Corrective commit (cross-role lint finding, not a new DAG node)

A full-tree integration lint pass (golangci-lint, errorlint) over the merged
Wave 2 tree flagged one issue in checkpoint-owned code:
`internal/gitx/resolver_test.go:128` used a direct type assertion
(`err.(*domain.Error)`) inside the `asDomainError` helper, which would fail on
wrapped errors. The helper now delegates to `errors.As`, preserving the exact
same test assertions on the unwrapped `*domain.Error` fields.

Validation re-run and green: `gofmt -l internal/gitx` (empty),
`go build ./internal/gitx/...`, `go vet ./internal/gitx/...`,
`go test ./internal/gitx/... -race`. golangci-lint is not installed in this
environment, so that specific check was skipped; the underlying pattern was
fixed per errorlint's rule. No other files touched; no DAG node started.

---

## Wave 4

Assigned nodes this wave: `checkpoint-a01` (Part A: Progress Tree core
migrations, 0020-0022) and `checkpoint-b01` (Part B: Repository Checkpoint
core migration, 0030). First Part A work for this role. Pre-step: merged
main (`ca7062f`, Wave 3 integration) into `day1/checkpoint` — clean
fast-forward, whole repo built and tested green before any new work.

### CROSS-ROLE CHANGE REQUEST (Constitution §4.4) — foundation, please fix

Adding ANY migration file outside foundation's 0001-0009 range breaks three
foundation-owned tests in `internal/storage/sqlite/migrate_test.go`, because
they assert the exact embedded migration set rather than foundation's own
subset:

- `TestAllMigrations_LoadsCoreSchemaFiles` — asserts `len(migrations) == 4`
  and the exact list `{1,2,3,4}`;
- `TestCoreMigrations_FromEmptyDatabase` — asserts `CurrentVersion == 4`;
- `TestCoreMigrations_ReopenFromFile_AppliesOnce` — asserts
  `CurrentVersion == 4`.

These now fail on this branch (and will fail for predictor-01's 0040 range
and every later range too). This contradicts migrate.go's own documented
design ("later roles' migrations ... are picked up automatically once
present, with no change needed here"). Requested fix (foundation-owned, one
mechanical edit): filter assertions to foundation's range, e.g. assert the
0001-0009 subset of `AllMigrations()` equals the expected four, and assert
`CurrentVersion >= 4` (or compute the expected max from `AllMigrations()`).
Per Constitution §4 and this wave's explicit instruction ("do NOT touch any
other role's paths"), checkpoint did NOT edit `migrate_test.go`; the three
failures are left in place and flagged here for foundation /
contract-integrator at integration time. checkpoint's own validation
commands (below) pass independently of them.

### checkpoint-a01: Progress Tree core migrations (0020-0022)

```yaml
node: checkpoint-a01
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0020_progress_nodes.sql   # §12.2 verbatim + §12.3 idx_progress_nodes_task_status
  - internal/storage/sqlite/migrations/0021_progress_edges.sql   # §12.2 verbatim
  - internal/storage/sqlite/migrations/0022_artifacts.sql        # §12.2 verbatim
  - internal/storage/sqlite/migrations_checkpoint_a_test.go      # checkpoint-owned test file (see assumption below)
validation:
  - "gofmt -l internal/storage/sqlite -> empty"
  - "go test ./internal/storage/sqlite/... -run Migration0020 -v -> PASS (11 tests: range presence in AllMigrations, table+index creation from empty DB, task-cascade, parent-subtree-cascade, sibling-ordinal uniqueness, unknown-task FK rejection, duplicate-edge PK rejection, edge node-cascade + unknown-endpoint rejection, artifact detach-on-node-delete + cascade-on-task-delete, duplicate-evidence rejection with different-digest-distinct, artifact unknown-task rejection)"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
next_action: checkpoint-a02/a03 (Progress Tree service + artifact validators) — now unblocked, NOT started this wave per explicit assignment
assumptions:
  - "Only progress_nodes/progress_edges/artifacts land in this node, per the wave instruction. state_checkpoints (also §12.2, also range 0020-0029) is deferred to the wave that implements the State Checkpoint manifest (checkpoint-a05); it will take 0023+. The §12.3 index idx_state_checkpoints_task_created defers with it."
  - "Test file location: migrations_checkpoint_a_test.go lives in foundation's internal/storage/sqlite directory (package sqlite_test) because the DAG's frozen validation command targets ./internal/storage/sqlite/... — the tests cannot live anywhere else and still be selected. It is a NEW, clearly checkpoint-named file; no foundation file was edited. It reuses foundation's openTemp helper (same external test package). If contract-integrator prefers a different convention for per-role migration tests, this file moves wholesale."
  - "All test names carry the Migration0020 selector (the range lower bound stands for the whole a01 migration set) so the DAG's `-run Migration0020` command selects exactly these tests, including the 0021/0022 coverage."
  - "Enum-bearing TEXT columns (progress_nodes.status/kind, progress_edges.edge_kind, artifacts.validation_status) intentionally carry no CHECK constraints: released migrations are immutable (ADD §12.5), so enum vocabulary enforcement belongs to the service layer (checkpoint-a02/a03), not DDL."
  - "UNIQUE(task_id, parent_id, ordinal) does not deduplicate root-level ordinals (SQLite NULL-distinct semantics); §12.2 transcribed verbatim, root-ordinal uniqueness is checkpoint-a02's plan-upsert responsibility. Same NULL-distinct note applies to artifacts' UNIQUE(progress_node_id, uri, sha256) for detached rows."
blockers:
  - "Foundation's three exact-count migration tests fail with this node's files present — see the §4.4 change request above. Not a blocker for this node's own validation command."
