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
```

### checkpoint-b01: Repository Checkpoint core migration (0030)

```yaml
node: checkpoint-b01
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0030_repository_checkpoints.sql  # §12.2 verbatim except turn_id (see assumption)
  - internal/storage/sqlite/migrations_checkpoint_b_test.go             # checkpoint-owned, separate from Part A per agents/checkpoint.md
validation:
  - "gofmt -l internal/storage/sqlite -> empty"
  - "go test ./internal/storage/sqlite/... -run Migration0030 -v -> PASS (6 tests: range presence in AllMigrations, table creation from empty DB, unknown-worktree FK rejection, worktree-cascade + task-detach, turn_id plain-pointer writability without turns table, total_bytes NULL-means-unknown)"
  - "go build ./... && go vet ./... -> clean"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
  - "go test ./... -> green everywhere EXCEPT the 3 pre-documented foundation exact-count tests (see the §4.4 change request at the top of this Wave 4 section; failure message is now 'len(migrations) = 8, want 4')"
next_action: checkpoint-b04 (Repository Checkpoint create/verify) — now unblocked on the b01 side (b03 already done), NOT started this wave per explicit assignment
assumptions:
  - "DOCUMENTED SCHEMA DEVIATION: §12.2 declares turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL, but turns belongs to claude-provider's 0010-0019 range and does not exist yet; an FK to a missing table makes every write to repository_checkpoints fail under PRAGMA foreign_keys=ON until another role ships its schema. turn_id is therefore a plain nullable TEXT pointer, following foundation's identical precedent for tasks.active_node_id (0004_tasks.sql header). Converting it to a real FK later requires a new migration in this range once turns exists (released migrations are immutable, ADD §12.5) — recorded so checkpoint-b04 and contract-integrator's final review both see it."
  - "Only repository_checkpoints lands in this node, per the wave instruction. repository_snapshots and file_changes (which foundation's notes place in the 0030-0039 range) defer to whichever Part B node first needs them (file_changes also FKs turns, so it is doubly blocked); they will take 0031+."
  - "Same no-CHECK-constraint stance as 0020-0022 for status/recoverability enum columns; vocabulary belongs to checkpoint-b04's service layer."
blockers:
  - "Same three foundation exact-count test failures as checkpoint-a01 — single root cause, single requested fix, filed once at the top of this Wave 4 section."
```

Wave 4 pre-step note: the main merge (`git merge main`) resolved as a clean
fast-forward to `ca7062f` (this branch's prior work was already fully
integrated), and the whole repo built and tested green at that point —
the only test regressions on this branch afterward are the three
pre-documented foundation exact-count tests triggered by this wave's own
migration files, exactly as analyzed above.

---

## Wave 5

Assigned nodes this wave: `checkpoint-a02` (Part A: node state machine +
Progress Tree Go-level stores), `checkpoint-a03` (Part A: artifact
validators), `checkpoint-b04` (Part B: Repository Checkpoint create/verify)
— done sequentially with an independent validate+commit after each, per
explicit wave instruction. Pre-step: `git fetch origin && git merge
origin/main` — clean fast-forward to `5470e4d` (Wave 4's integrated state,
including foundation's fix for the exact-count migration test conflict this
role flagged in its Wave 4 entry above — confirmed resolved: `go test ./...`
was green immediately after the merge, before any new work started).

### checkpoint-a02: Progress Tree node state machine and stores

```yaml
node: checkpoint-a02
status: completed
artifacts:
  - internal/progress/statemachine.go       # ValidateTransition/IsTerminal/AllowedTransitions over the frozen ProgressNodeStatus enum
  - internal/progress/statemachine_test.go  # every valid edge + a wide invalid-edge matrix + terminal-state + table self-consistency check
  - internal/progress/node_store.go         # NodeStore: Insert/Get/ListByTask/TransitionStatus (optimistic concurrency, WHERE status=? AND version=?)/SetTimestamps
  - internal/progress/node_store_test.go    # CRUD, invalid-transition-rejected (store-enforced), stale-version conflict, 20-goroutine concurrent-transition race (exactly 1 winner)
  - internal/progress/edge_store.go         # EdgeStore over progress_edges: depends_on/relates_to, duplicate + self-referential rejection
  - internal/progress/edge_store_test.go
  - internal/progress/artifact_store.go     # ArtifactStore over artifacts: exact-duplicate-evidence rejected as conflict; differing sha256 for same (node,uri) deliberately NOT blocked here (documented, locked by a test) — that conflict policy is checkpoint-a04's CompleteNode job
  - internal/progress/artifact_store_test.go
  - internal/progress/helpers_test.go       # shared openTestDB/seedTask/fixedClock test scaffolding
  - schemas/progress-tree.schema.json       # wire schema for Progress Tree export/import (ADD Appendix A shape)
  - testdata/progress-trees/sample-task.json # fixture transcribed from ADD Appendix A, schema-validated
validation:
  - "gofmt -l internal/progress -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/progress/... -> OK"
  - "go test ./internal/progress/... -race -v -> PASS (all tests, including the DAG's required invalid-transition-rejected and concurrent-completion-race)"
  - "golangci-lint run ./internal/progress/... -> 0 issues"
commit: 3557e61
next_action: checkpoint-a04 (CompleteNode atomic protocol) — the single highest-risk DAG node — is now unblocked on the a02 side (also needs a03 + contract-integrator-04); NOT started this wave per explicit assignment
assumptions:
  - "a01 shipped migrations only (0020-0022 SQL, per its own DAG validation command targeting ./internal/storage/sqlite/...); internal/progress did not exist before this node. a02 is therefore the first Go-level domain package over those tables, exactly as the wave brief anticipated."
  - "State machine extends CONTRACT_FREEZE.md's frozen backbone (pending->ready->in_progress->checkpointing->{completed|failed}) with documented, narrow additions only: pending/ready/paused/blocked -> skipped or blocked as side states; checkpointing -> in_progress for ADD §18.4's validation-fails recovery path; failed -> in_progress as the one explicit retry edge. completed and skipped are the only fully terminal states; failed has exactly one outbound edge (retry), which IsTerminal's doc comment states explicitly rather than leaving implicit."
  - "TransitionStatus's optimistic-concurrency guard (UPDATE ... WHERE status=? AND version=?) is deliberately NOT the full CompleteNode atomic protocol (stage/verify artifact evidence + node update + State Checkpoint creation in one transaction) — that orchestration, and its crash-injection tests, is checkpoint-a04's explicitly scoped job per the wave brief. This node's own concurrent-race test (20 goroutines) validates the store's own concurrency primitive in isolation, not the full completion protocol."
  - "ArtifactStore.Insert's duplicate-evidence conflict only fires on an EXACT (progress_node_id, uri, sha256) repeat (0022's own UNIQUE constraint); a different sha256 for the same (node, uri) is accepted at the store layer by design — surfacing THAT as a completion conflict is Constitution §6.6's concern and checkpoint-a04's job, not this store's. Locked by TestArtifactStore_DifferentSHA256_NotBlockedByStore so a04 doesn't have to rediscover the boundary."
blockers: none
```

### checkpoint-a03: Artifact validators

```yaml
node: checkpoint-a03
status: completed
artifacts:
  - internal/artifacts/validator.go       # Validator interface, Candidate input shape, Result/Passed/Failed
  - internal/artifacts/file_exists.go
  - internal/artifacts/checksum.go        # SHA-256, case-insensitive hex compare
  - internal/artifacts/heading.go         # CommonMark-aware ATX heading match, skips lines inside fenced code blocks
  - internal/artifacts/fence_balance.go   # tracks fence char+run-length per CommonMark's >= close-length rule
  - internal/artifacts/registry.go        # Registry pre-populated with the 4 built-ins; Register rejects duplicate Kind
  - internal/artifacts/*_test.go          # one test file per validator + registry_test.go
  - testdata/checkpoints/state/add-section-18-valid.md              # REAL Preflight_ADD.md §18 verbatim (212 lines, 6 real fences)
  - testdata/checkpoints/state/add-section-18-unbalanced-fence.md    # targeted mutation: one closing fence removed
  - testdata/checkpoints/state/add-section-18-missing-heading.md     # targeted mutation: H1 removed
validation:
  - "gofmt -l internal/artifacts -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/artifacts/... -> OK"
  - "go test ./internal/artifacts/... -race -v -> PASS (all tests, including missing-heading-rejected and unbalanced-fence-rejected against the REAL fixtures, and validators-all-pass-together on the real valid fixture)"
  - "golangci-lint run ./internal/artifacts/... -> 0 issues"
commit: f34f12c
next_action: checkpoint-a04 — unblocked on the a03 side (also needs a02 + contract-integrator-04); NOT started this wave per explicit assignment
assumptions:
  - "Per the DAG note ('Needs real ADD-section fixtures'), fixtures are Preflight_ADD.md §18 transcribed verbatim via `sed`, not synthetic toy Markdown — the negative fixtures are targeted single-line mutations of that same real content, so the validators are proven against real-world Markdown structure (mixed yaml/mermaid/text fences, Chinese-language headings, nested list/prose structure), not a simplified stand-in."
  - "'Valid Markdown section completes and checkpoints' (required test) is scoped, per the wave brief, to 'validator returns success' — TestRegistry_ValidMarkdownSection_CompletesValidation runs file_exists+heading_exists+fence_balance together against the real fixture and asserts all three pass. The full completes-AND-checkpoints protocol (State Checkpoint creation) is checkpoint-a04's job."
  - "HeadingExistsValidator and FenceBalanceValidator share fence-tracking logic (the `fence{char,n}` type and CommonMark's >=-run-length close rule) to guarantee both validators agree on what counts as 'inside a fence' — verified by TestHeadingExists_HeadingTextInsideFence_NotCountedAsHeading, which would fail if the two validators disagreed."
blockers: none
```

### checkpoint-b04: Repository Checkpoint create and verify

```yaml
node: checkpoint-b04
status: completed
artifacts:
  - internal/gitx/patch.go                # NEW small addition to b02/b03's package: DiffPatch (binary-safe, --full-index --no-ext-diff) + ListUntracked (git ls-files --others --exclude-standard -z) — neither existed yet and b04 needed both for a meaningful create/verify
  - internal/gitx/patch_test.go
  - internal/repocheckpoint/capture.go     # Capture(): ADD §19.3 protocol end to end, read-only Git only, initial/final fingerprint race check (fail closed, errIntegrity)
  - internal/repocheckpoint/archive.go     # untracked zip archive with per-file/total/file-count caps + skip ledger
  - internal/repocheckpoint/security.go    # validateUntrackedPath: traversal-string, .git-internal, symlink (leaf + every ancestor dir) rejection
  - internal/repocheckpoint/atomicwrite.go + dirsync_unix.go/dirsync_windows.go  # temp-dir -> fsync -> atomic rename; refuses to overwrite an existing checkpoint ID; removes temp dir on any failure
  - internal/repocheckpoint/verify.go      # recomputes SHA-256 for every artifact against manifest.json; never trusts the DB row alone
  - internal/repocheckpoint/manifest.go, serialize.go  # Manifest Go type (ADD Appendix D shape) + JSON (de)serialization + summary.md renderer
  - internal/repocheckpoint/store.go       # Store CRUD over repository_checkpoints (migrations/0030)
  - internal/repocheckpoint/service.go     # Service implementing app.RepositoryCheckpointService; Restore returns explicit ErrCodeUnavailable (real restore is b08/ADD §19.6 stretch scope)
  - internal/repocheckpoint/*_test.go      # capture_test.go, security_test.go, security_internal_test.go, verify_test.go, store_test.go, service_test.go, helpers_test.go
  - schemas/repository-checkpoint.schema.json
  - testdata/checkpoints/repository/sample-manifest.json  # generated from an ACTUAL Capture run against a real temp repo, schema-validated
  - testdata/repositories/README.md        # documents why this role uses on-demand temp repos (internal/gitx + internal/repocheckpoint test helpers) rather than a frozen `.git` fixture tree
validation:
  - "gofmt -l internal/repocheckpoint internal/gitx -> empty"
  - "go build ./... (darwin, GOOS=linux, GOOS=windows) -> all OK"
  - "go vet ./internal/repocheckpoint/... ./internal/gitx/... -> OK"
  - "go test ./internal/repocheckpoint/... -race -v -> PASS (34 tests, incl. tracked/staged/unstaged/untracked, rename/delete, binary file, spaces-in-path, nested/linked worktree, concurrent-mutation race, temp-cleanup-on-failure, path traversal (string + symlink, leaf + ancestor dir), oversize, file/total-size caps)"
  - "golangci-lint run ./internal/repocheckpoint/... ./internal/gitx/... -> 0 issues"
  - "go test ./... -race -> green whole-repo, zero regressions from the internal/gitx addition"
commit: d692fd6
next_action: checkpoint-b05 (binary-safe patch edge cases), checkpoint-b06 (untracked archive + redact/secret-scan), checkpoint-b07 (atomic write -race hardening) — all now unblocked on b04; NOT started this wave per explicit assignment. Deliberately left as TODOs for those nodes (do NOT under-deliver b04's own scope, but do not duplicate theirs either):
  - "b05: DiffPatch's binary-safety is exercised by one test (TestCapture_BinaryFile / TestDiffPatch_BinaryFile_UsesBinaryDirective) proving the GIT-binary-patch directive appears; b05's own deeper edge cases (e.g. very large binary diffs, mixed binary+text in one patch, apply-round-trip verification) are NOT built here."
  - "b06: untracked archive policy here is STRUCTURAL only (size/path/symlink caps) — no content-based secret scanning exists yet (internal/redact/** is still an empty exclusive-path placeholder). skipped-files.json + Recoverability.Warnings give b06 a ready extension point (add a new SkipReason + a scan step in buildUntrackedArchive) rather than a redesign."
  - "b07: atomic write here is single-process-safe (temp-dir + fsync + atomic rename, verified by TestCapture_TempCleanup_NoOrphanOnFailure) but does NOT include cross-process crash-injection tests or a startup orphan-temp-dir scan — that hardening, and its own -race-named test target, is b07's explicit DAG scope."
assumptions:
  - "Capture never issues a Git subcommand capable of mutating repository state — grepped for write verbs; only Status/Fingerprint/DiffNumstat/DiffPatch/ListUntracked are called, all read-only. TestCapture_NeverMutatesActiveBranch asserts `git rev-parse HEAD` and `git status --porcelain` are byte-identical before and after a Capture call, as a regression guard on this exact invariant (Constitution §7 rule 6, this node's own DAG risk note)."
  - "Race detection (ADD §19.3 step 11) compares initial vs. final gitx.Fingerprint and fails closed (errIntegrity, ErrCodeIntegrity, Retryable:false) on any difference; the retry-once policy the ADD step also describes is explicitly left to checkpoint-b07 per the wave brief's scoping guidance, so this function performs exactly one attempt."
  - "Cross-platform directory-fsync handling (dirsync_unix.go/dirsync_windows.go, build-tag split) was added because Preflight's own ADD §30.4 release targets include windows/amd64+arm64, and a naive syscall.ENOTSUP reference would not compile there — verified via GOOS=windows go build ./... in addition to the native darwin build."
  - "Service.resolveWorktree is an injected callback (WorktreeLocation) rather than this package reaching into foundation's worktrees table directly — internal/repocheckpoint does not own that table and the wave's cross-part-boundary note (agents/checkpoint.md) already establishes that Part B does not reach into other roles' storage directly; the runtime role (or a future checkpoint node) supplies the real resolver."
blockers: none
```
