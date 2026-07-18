# checkpoint — 進度產出物（Progress Artifact）

> 🌐 [English](checkpoint.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

此角色涵蓋兩個內部子元件（Part A：Progress Tree／State Checkpointing；Part B：Repository Checkpoint），依 `agents/checkpoint.md` 的規定各自獨立處理。本波（phase）僅有一個節點被解除阻擋並獲指派：`checkpoint-b02`。`checkpoint-a01` 與 `checkpoint-b01` 仍在 `foundation-06`（核心 SQLite migration 骨架）後面排隊等候，該依賴項本波尚未完成——依照已凍結的 `EXECUTION_DAG.md`，兩者皆正確地被保留，而非在依賴項未完成的情況下貿然開始。

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
next_action: superseded by the Phase 2 entry below
assumptions:
  - "Status() pins --branch --untracked-files=all --find-renames on top of --porcelain=v2 -z so output is deterministic regardless of the caller's git config (status.renames, status.showUntrackedFiles). This is additive to the exact flag string in the DAG's validation command and does not change the parser's contract."
  - "ParsePorcelainV2 fails closed (returns a domain.Error with ErrCodeValidation) on any record shape it doesn't recognize, rather than silently skipping it — this parser feeds the Repository Checkpoint integrity boundary (Constitution §6), so an unintelligible status must not be treated as 'no changes.'"
  - "ResolveRepo requires git >= 2.31 for `rev-parse --path-format=absolute`; not verified against older git in this environment. Local git version used for tests: 2.37.3."
  - "Unmerged (conflict) entries and ignored entries are parsed by the porcelain layer (fixture-tested) but have no live-repo integration test in this phase, since provoking a real merge conflict/ignore rule in a throwaway temp repo added setup complexity out of proportion to checkpoint-b02's scope; they are covered by TestParsePorcelainV2Fixtures instead. checkpoint-b03/b04 should add a live-repo conflict case if the fingerprint or checkpoint-create logic branches on unmerged state."
blockers:
  - "checkpoint-a01 and checkpoint-b01 blocked pending foundation-06 (core SQLite migration harness) — not started this phase, per explicit phase assignment."
```

---

## Phase 2

本波指派節點：`checkpoint-b03`（Snapshot fingerprint），其依賴項 `checkpoint-b02` 在此分支上已滿足。依照本波明確指示，未對 main 執行任何 merge/rebase（ADR-041 僅涉及 predictor domain 型別，與 `internal/gitx` 沒有依賴關係）。`checkpoint-a01`、`checkpoint-b01` 與 `checkpoint-b04` 依指派仍未開始。

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
next_action: checkpoint-b04 (Repository Checkpoint create/verify) is now unblocked on the b03 side but still gated on checkpoint-b01 (migrations 0030-0039, itself gated on foundation-06) — not started this phase, per assignment
assumptions:
  - "Digest scope: covers FingerprintSchema, WorktreeRoot, CommonDir, IsLinkedWorktree, HeadOID, Branch, UntrackedPolicy, sorted status Entries, and sorted index/worktree numstat. Upstream/Ahead/Behind are carried as informational fields but deliberately EXCLUDED from the digest: they move on `git fetch` (remote-tracking refs), which does not change the local worktree/index/HEAD state that FR-149 resume validation protects — a background fetch must not invalidate a resume. If checkpoint-b04 or runtime needs remote-divergence in the identity, that is an additive schema bump (auspex.gitx.fingerprint.v2), not a breaking change."
  - "Canonical encoding is netstring-style length-prefixed fields hashed in fixed order, with Entries/numstat sorted by (Path, OrigPath[, Kind]) before hashing — digest is independent of git's emission order and immune to path-content boundary forgery (spaces/tabs/newlines in paths are covered by tests)."
  - "A Fingerprint is a point-in-time read composed of three git invocations (status + two numstats), not an atomic capture. Concurrent-mutation detection is the ADD §19.3 initial/final-fingerprint-compare protocol, which is checkpoint-b04/b07 scope; Fingerprint.Equal (digest compare, fail-closed on empty digest) is the primitive those nodes will use."
  - "ParseNumstatZ fails closed (domain ErrCodeValidation) on any unrecognized record shape, matching ParsePorcelainV2's integrity-boundary posture from checkpoint-b02."
  - "`git diff --cached --numstat` on an unborn branch (no commits) diffs against the empty tree — verified against git 2.37.3 and covered by TestFingerprintUnbornBranch, so fingerprinting a freshly-initialized staged repo works."
  - "app.Authorization.SnapshotFingerprint (frozen ports.go) is a plain string; Fingerprint.Digest (sha256 hex) satisfies it directly. No contract gap found; no ports.go change requested."
blockers:
  - "checkpoint-a01, checkpoint-b01 still blocked on foundation-06; checkpoint-b04 additionally on checkpoint-b01. None started this phase, per explicit assignment."
```

checkpoint-b03 的最終 commit：`0281b97`（程式碼＋文件），此 SHA 記錄於後續的一個 commit 中，與 Phase 1 的 `9b222d0`／`94be461` 模式相同。

## 修正性 commit（跨角色 lint 發現，非新增 DAG 節點）

對合併後的 Phase 2 樹進行整棵樹的 lint 檢查（golangci-lint、errorlint）時，在 checkpoint 所擁有的程式碼中發現一個問題：`internal/gitx/resolver_test.go:128` 在 `asDomainError` 輔助函式內使用了直接型別斷言（`err.(*domain.Error)`），這在遇到被包裝（wrapped）的錯誤時會失敗。該輔助函式現已改為委派給 `errors.As`，並保留對 unwrapped `*domain.Error` 欄位完全相同的測試斷言。

重新執行驗證並皆為綠燈：`gofmt -l internal/gitx`（空輸出）、`go build ./internal/gitx/...`、`go vet ./internal/gitx/...`、`go test ./internal/gitx/... -race`。此環境未安裝 golangci-lint，因此該項檢查被略過；但底層樣式已依 errorlint 的規則修正。未觸及其他任何檔案；未啟動任何 DAG 節點。

---

## Phase 4

本波指派節點：`checkpoint-a01`（Part A：Progress Tree 核心 migrations，0020-0022）與 `checkpoint-b01`（Part B：Repository Checkpoint 核心 migration，0030）。這是本角色第一次進行 Part A 的工作。前置步驟：將 main（`ca7062f`，Phase 3 整合）合併進 `vertical-slice/checkpoint`——為乾淨的 fast-forward，整個 repo 在開始任何新工作之前已建置並測試通過（綠燈）。

### 跨角色變更請求（Constitution §4.4）——foundation，請修正

只要新增任何 foundation 的 0001-0009 範圍以外的 migration 檔案，就會破壞 `internal/storage/sqlite/migrate_test.go` 中三個由 foundation 擁有的測試，因為這些測試斷言的是精確的完整內嵌 migration 集合，而非僅屬於 foundation 自己的子集合：

- `TestAllMigrations_LoadsCoreSchemaFiles` — 斷言 `len(migrations) == 4` 且清單精確等於 `{1,2,3,4}`；
- `TestCoreMigrations_FromEmptyDatabase` — 斷言 `CurrentVersion == 4`；
- `TestCoreMigrations_ReopenFromFile_AppliesOnce` — 斷言 `CurrentVersion == 4`。

這些測試目前在此分支上失敗（且日後對 predictor-01 的 0040 範圍、以及之後每一個範圍也都會失敗）。這與 migrate.go 本身文件化的設計相矛盾（「後續角色的 migrations……一旦存在即會自動被納入，此處無需任何變更」）。請求的修正方式（由 foundation 擁有，屬於機械性的單一編輯）：將斷言限縮至 foundation 自己的範圍，例如斷言 `AllMigrations()` 中屬於 0001-0009 的子集合等於預期的四筆，並斷言 `CurrentVersion >= 4`（或從 `AllMigrations()` 計算出預期的最大值）。依照 Constitution §4 以及本波明確指示（「不得觸碰其他角色的路徑」），checkpoint 並未編輯 `migrate_test.go`；這三個失敗維持原樣，並在此標記給 foundation／contract-integrator 於整合時處理。checkpoint 自身的驗證指令（如下）不受其影響，獨立通過。

### checkpoint-a01：Progress Tree 核心 migrations（0020-0022）

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
next_action: checkpoint-a02/a03 (Progress Tree service + artifact validators) — now unblocked, NOT started this phase per explicit assignment
assumptions:
  - "Only progress_nodes/progress_edges/artifacts land in this node, per the phase instruction. state_checkpoints (also §12.2, also range 0020-0029) is deferred to the phase that implements the State Checkpoint manifest (checkpoint-a05); it will take 0023+. The §12.3 index idx_state_checkpoints_task_created defers with it."
  - "Test file location: migrations_checkpoint_a_test.go lives in foundation's internal/storage/sqlite directory (package sqlite_test) because the DAG's frozen validation command targets ./internal/storage/sqlite/... — the tests cannot live anywhere else and still be selected. It is a NEW, clearly checkpoint-named file; no foundation file was edited. It reuses foundation's openTemp helper (same external test package). If contract-integrator prefers a different convention for per-role migration tests, this file moves wholesale."
  - "All test names carry the Migration0020 selector (the range lower bound stands for the whole a01 migration set) so the DAG's `-run Migration0020` command selects exactly these tests, including the 0021/0022 coverage."
  - "Enum-bearing TEXT columns (progress_nodes.status/kind, progress_edges.edge_kind, artifacts.validation_status) intentionally carry no CHECK constraints: released migrations are immutable (ADD §12.5), so enum vocabulary enforcement belongs to the service layer (checkpoint-a02/a03), not DDL."
  - "UNIQUE(task_id, parent_id, ordinal) does not deduplicate root-level ordinals (SQLite NULL-distinct semantics); §12.2 transcribed verbatim, root-ordinal uniqueness is checkpoint-a02's plan-upsert responsibility. Same NULL-distinct note applies to artifacts' UNIQUE(progress_node_id, uri, sha256) for detached rows."
blockers:
  - "Foundation's three exact-count migration tests fail with this node's files present — see the §4.4 change request above. Not a blocker for this node's own validation command."
```

### checkpoint-b01：Repository Checkpoint 核心 migration（0030）

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
  - "go test ./... -> green everywhere EXCEPT the 3 pre-documented foundation exact-count tests (see the §4.4 change request at the top of this Phase 4 section; failure message is now 'len(migrations) = 8, want 4')"
next_action: checkpoint-b04 (Repository Checkpoint create/verify) — now unblocked on the b01 side (b03 already done), NOT started this phase per explicit assignment
assumptions:
  - "DOCUMENTED SCHEMA DEVIATION: §12.2 declares turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL, but turns belongs to claude-provider's 0010-0019 range and does not exist yet; an FK to a missing table makes every write to repository_checkpoints fail under PRAGMA foreign_keys=ON until another role ships its schema. turn_id is therefore a plain nullable TEXT pointer, following foundation's identical precedent for tasks.active_node_id (0004_tasks.sql header). Converting it to a real FK later requires a new migration in this range once turns exists (released migrations are immutable, ADD §12.5) — recorded so checkpoint-b04 and contract-integrator's final review both see it."
  - "Only repository_checkpoints lands in this node, per the phase instruction. repository_snapshots and file_changes (which foundation's notes place in the 0030-0039 range) defer to whichever Part B node first needs them (file_changes also FKs turns, so it is doubly blocked); they will take 0031+."
  - "Same no-CHECK-constraint stance as 0020-0022 for status/recoverability enum columns; vocabulary belongs to checkpoint-b04's service layer."
blockers:
  - "Same three foundation exact-count test failures as checkpoint-a01 — single root cause, single requested fix, filed once at the top of this Phase 4 section."
```

Phase 4 前置步驟備註：main 的合併（`git merge main`）以乾淨的 fast-forward 方式解析至 `ca7062f`（此分支先前的工作當時已完全整合），且整個 repo 在該時間點建置與測試皆為綠燈——此後此分支上唯一的測試回歸，就是前述分析中、由本波自身 migration 檔案觸發的那三個已預先記錄在案的 foundation exact-count 測試。

---

## Phase 5

本波指派節點：`checkpoint-a02`（Part A：節點狀態機＋Progress Tree Go 層級的 stores）、`checkpoint-a03`（Part A：artifact 驗證器）、`checkpoint-b04`（Part B：Repository Checkpoint 的 create／verify）——依照本波明確指示，依序完成，每項各自獨立驗證＋commit。前置步驟：`git fetch origin && git merge origin/main`——乾淨的 fast-forward 至 `5470e4d`（Phase 4 的整合狀態，包含 foundation 針對本角色在上方 Phase 4 條目中提出的 exact-count migration 測試衝突所做的修正——已確認解決：合併後、在任何新工作開始之前，`go test ./...` 隨即呈現綠燈）。

### checkpoint-a02：Progress Tree 節點狀態機與 stores

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
next_action: checkpoint-a04 (CompleteNode atomic protocol) — the single highest-risk DAG node — is now unblocked on the a02 side (also needs a03 + contract-integrator-04); NOT started this phase per explicit assignment
assumptions:
  - "a01 shipped migrations only (0020-0022 SQL, per its own DAG validation command targeting ./internal/storage/sqlite/...); internal/progress did not exist before this node. a02 is therefore the first Go-level domain package over those tables, exactly as the phase brief anticipated."
  - "State machine extends CONTRACT_FREEZE.md's frozen backbone (pending->ready->in_progress->checkpointing->{completed|failed}) with documented, narrow additions only: pending/ready/paused/blocked -> skipped or blocked as side states; checkpointing -> in_progress for ADD §18.4's validation-fails recovery path; failed -> in_progress as the one explicit retry edge. completed and skipped are the only fully terminal states; failed has exactly one outbound edge (retry), which IsTerminal's doc comment states explicitly rather than leaving implicit."
  - "TransitionStatus's optimistic-concurrency guard (UPDATE ... WHERE status=? AND version=?) is deliberately NOT the full CompleteNode atomic protocol (stage/verify artifact evidence + node update + State Checkpoint creation in one transaction) — that orchestration, and its crash-injection tests, is checkpoint-a04's explicitly scoped job per the phase brief. This node's own concurrent-race test (20 goroutines) validates the store's own concurrency primitive in isolation, not the full completion protocol."
  - "ArtifactStore.Insert's duplicate-evidence conflict only fires on an EXACT (progress_node_id, uri, sha256) repeat (0022's own UNIQUE constraint); a different sha256 for the same (node, uri) is accepted at the store layer by design — surfacing THAT as a completion conflict is Constitution §6.6's concern and checkpoint-a04's job, not this store's. Locked by TestArtifactStore_DifferentSHA256_NotBlockedByStore so a04 doesn't have to rediscover the boundary."
blockers: none
```

### checkpoint-a03：Artifact validators（產出物驗證器）

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
  - testdata/checkpoints/state/add-section-18-valid.md              # REAL Auspex_ADD.md §18 verbatim (212 lines, 6 real fences)
  - testdata/checkpoints/state/add-section-18-unbalanced-fence.md    # targeted mutation: one closing fence removed
  - testdata/checkpoints/state/add-section-18-missing-heading.md     # targeted mutation: H1 removed
validation:
  - "gofmt -l internal/artifacts -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/artifacts/... -> OK"
  - "go test ./internal/artifacts/... -race -v -> PASS (all tests, including missing-heading-rejected and unbalanced-fence-rejected against the REAL fixtures, and validators-all-pass-together on the real valid fixture)"
  - "golangci-lint run ./internal/artifacts/... -> 0 issues"
commit: f34f12c
next_action: checkpoint-a04 — unblocked on the a03 side (also needs a02 + contract-integrator-04); NOT started this phase per explicit assignment
assumptions:
  - "Per the DAG note ('Needs real ADD-section fixtures'), fixtures are Auspex_ADD.md §18 transcribed verbatim via `sed`, not synthetic toy Markdown — the negative fixtures are targeted single-line mutations of that same real content, so the validators are proven against real-world Markdown structure (mixed yaml/mermaid/text fences, Chinese-language headings, nested list/prose structure), not a simplified stand-in."
  - "'Valid Markdown section completes and checkpoints' (required test) is scoped, per the phase brief, to 'validator returns success' — TestRegistry_ValidMarkdownSection_CompletesValidation runs file_exists+heading_exists+fence_balance together against the real fixture and asserts all three pass. The full completes-AND-checkpoints protocol (State Checkpoint creation) is checkpoint-a04's job."
  - "HeadingExistsValidator and FenceBalanceValidator share fence-tracking logic (the `fence{char,n}` type and CommonMark's >=-run-length close rule) to guarantee both validators agree on what counts as 'inside a fence' — verified by TestHeadingExists_HeadingTextInsideFence_NotCountedAsHeading, which would fail if the two validators disagreed."
blockers: none
```

### checkpoint-b04：Repository Checkpoint 的建立（create）與驗證（verify）

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
next_action: checkpoint-b05 (binary-safe patch edge cases), checkpoint-b06 (untracked archive + redact/secret-scan), checkpoint-b07 (atomic write -race hardening) — all now unblocked on b04; NOT started this phase per explicit assignment. Deliberately left as TODOs for those nodes (do NOT under-deliver b04's own scope, but do not duplicate theirs either):
  - "b05: DiffPatch's binary-safety is exercised by one test (TestCapture_BinaryFile / TestDiffPatch_BinaryFile_UsesBinaryDirective) proving the GIT-binary-patch directive appears; b05's own deeper edge cases (e.g. very large binary diffs, mixed binary+text in one patch, apply-round-trip verification) are NOT built here."
  - "b06: untracked archive policy here is STRUCTURAL only (size/path/symlink caps) — no content-based secret scanning exists yet (internal/redact/** is still an empty exclusive-path placeholder). skipped-files.json + Recoverability.Warnings give b06 a ready extension point (add a new SkipReason + a scan step in buildUntrackedArchive) rather than a redesign."
  - "b07: atomic write here is single-process-safe (temp-dir + fsync + atomic rename, verified by TestCapture_TempCleanup_NoOrphanOnFailure) but does NOT include cross-process crash-injection tests or a startup orphan-temp-dir scan — that hardening, and its own -race-named test target, is b07's explicit DAG scope."
assumptions:
  - "Capture never issues a Git subcommand capable of mutating repository state — grepped for write verbs; only Status/Fingerprint/DiffNumstat/DiffPatch/ListUntracked are called, all read-only. TestCapture_NeverMutatesActiveBranch asserts `git rev-parse HEAD` and `git status --porcelain` are byte-identical before and after a Capture call, as a regression guard on this exact invariant (Constitution §7 rule 6, this node's own DAG risk note)."
  - "Race detection (ADD §19.3 step 11) compares initial vs. final gitx.Fingerprint and fails closed (errIntegrity, ErrCodeIntegrity, Retryable:false) on any difference; the retry-once policy the ADD step also describes is explicitly left to checkpoint-b07 per the phase brief's scoping guidance, so this function performs exactly one attempt."
  - "Cross-platform directory-fsync handling (dirsync_unix.go/dirsync_windows.go, build-tag split) was added because Auspex's own ADD §30.4 release targets include windows/amd64+arm64, and a naive syscall.ENOTSUP reference would not compile there — verified via GOOS=windows go build ./... in addition to the native darwin build."
  - "Service.resolveWorktree is an injected callback (WorktreeLocation) rather than this package reaching into foundation's worktrees table directly — internal/repocheckpoint does not own that table and the phase's cross-part-boundary note (agents/checkpoint.md) already establishes that Part B does not reach into other roles' storage directly; the runtime role (or a future checkpoint node) supplies the real resolver."
blockers: none
```

---

## Phase 6

本波指派節點：`checkpoint-a04`（Part A：CompleteNode 原子協定——整個 DAG 中影響最重大的單一節點）、`checkpoint-b05`（Part B：binary-safe patch 產生的邊界情況）、`checkpoint-b06`（Part B：未追蹤檔案政策的機密過濾器＋`internal/redact`）——依照本波明確指示依序完成，每項各自獨立驗證＋commit，其中 a04 依 DAG 本身的風險註記獲得最完整的關注。前置步驟：`git fetch origin && git merge origin/main`——乾淨的 fast-forward 至 `abce1d0`（Phase 5 的整合狀態：其他角色的 predictor／runtime／orchestrator／pause／scheduler 工作，皆未觸及本角色的路徑）；合併後、在任何新工作開始之前，`go test ./...` 隨即呈現綠燈。

### checkpoint-a04：CompleteNode 原子（atomic）協定

```yaml
node: checkpoint-a04
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0023_state_checkpoints.sql  # deferred from a01; state_checkpoints table + idx_state_checkpoints_task_created
  - internal/storage/sqlite/migrations/0024_node_completions.sql   # NEW: durable idempotency ledger backing ADD §18.12's completion_key contract
  - internal/storage/sqlite/migrations_checkpoint_a04_test.go      # DDL-level constraint tests for both migrations (checkpoint-owned, foundation's directory, same convention as a01)
  - internal/statecheckpoint/manifest.go     # Manifest Go type mirroring ADD Appendix B exactly
  - internal/statecheckpoint/serialize.go    # Digest (SHA-256 over canonical JSON, excludes IntegritySHA256 itself)/Seal/Marshal/Unmarshal/Verify
  - internal/statecheckpoint/build.go        # Build(): assembles an unsealed Manifest from a caller-supplied snapshot (keeps internal/progress from reaching into a manifest-shape decision directly)
  - internal/statecheckpoint/store.go        # Store: Insert/Get/LoadLatest/ListByTask over state_checkpoints
  - internal/statecheckpoint/*_test.go       # serialize_test.go, store_test.go, fixture_gen_test.go (regenerable sample-manifest.json)
  - internal/progress/complete_node.go       # THE atomic protocol: idempotency check -> stage+verify artifacts -> one WithTx (node transition + artifact rows + checkpoint insert + ledger insert) -> publish events after commit
  - internal/progress/stager.go              # FileStager: content-addressed evidence copy (real checksum recomputed, never trusts caller's claim), atomic temp+fsync+rename
  - internal/progress/idempotency.go         # node_completions CRUD + checkIdempotency (replay/conflict) + recordIdempotency
  - internal/progress/reconcile.go           # Reconciler: startup check for orphaned staged evidence + checkpoint integrity re-verification (ADD §18.9)
  - internal/progress/complete_node*_test.go # complete_node_test.go, complete_node_idempotency_test.go, complete_node_crash_test.go, complete_node_race_test.go, complete_node_helpers_test.go
  - schemas/state-checkpoint.schema.json     # wire schema for the State Checkpoint manifest (ADD Appendix B shape)
  - testdata/checkpoints/state/sample-manifest.json  # generated from an ACTUAL Build+Seal+Marshal call, schema-conformant
validation:
  - "gofmt -l internal/progress internal/statecheckpoint internal/storage/sqlite -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/progress/... ./internal/statecheckpoint/... ./internal/storage/sqlite/... -> OK"
  - "go test ./internal/progress/... -run CompleteNode -race -v -> PASS (22 tests: valid-section-completes-and-checkpoints, missing-heading/unbalanced-fence rejected, no-artifacts/missing-file/changed-artifact rejected, violated-dependency rejected + satisfied-dependency allowed, invalid-transition rejected, 100-sequential-nodes-produce-100-verifiable-checkpoints, same-idempotency-key-replays, conflicting-payload-same-key rejected, already-completed-different-key rejected, crash injection at all 5 named phases + full reconciliation-after-crash sweep, concurrent-completion race both same-key and different-key variants)"
  - "go test ./internal/progress/... ./internal/statecheckpoint/... -race -count=3 -> PASS, stable across repeats (no flakiness)"
  - "go test ./... -race -> green whole-repo, zero regressions"
  - "golangci-lint run ./... -> 0 issues"
commit: 7eff177
next_action: checkpoint-a05 (State Checkpoint manifest — NOTE much of a05's nominal scope (manifest serialization/checksum) was ALREADY built here since CompleteNode structurally required it; a05 should verify what remains, likely just Snapshot/verify-API polish), checkpoint-a06/a07/a08/a09 — NOT started this phase per explicit assignment
assumptions:
  - "internal/app/ports.go has no EventPublisher/EventSink port yet (grepped: none exists anywhere in internal/app or pkg/protocol) — adding one is contract-integrator's call per Constitution §4, not this role's to make unilaterally. CompleteNode therefore declares its OWN narrow EventPublisher interface (internal/progress, satisfied trivially by NoopPublisher) rather than blocking on a contract change or reaching past the frozen ports.go; if a future contract freeze adds a matching app.EventPublisher port, this interface's method set is designed to be satisfied by it unchanged."
  - "State Checkpoint manifest lives in a NEW package (internal/statecheckpoint, exclusive path already granted to this role in agents/checkpoint.md) rather than inside internal/progress — mirrors the existing internal/artifacts seam CompleteNode also calls into, keeping 'orchestration' (internal/progress) separate from 'the thing being orchestrated' (manifest assembly/serialization) as two independently testable packages, per this role's own established pattern from a02/a03."
  - "Migrations 0023 (state_checkpoints, deferred from a01 by a01's own note) and 0024 (node_completions, new) both land in this node since CompleteNode cannot be built without either table — a01's deferral note anticipated exactly this."
  - "Idempotency has two layers: CompleteNodeRequest.IdempotencyKey (frozen by CONTRACT_FREEZE.md, caller-supplied) is checked against a durable ledger keyed by node_id (a node can only complete once, ever — PRIMARY KEY node_id on 0024); a second payloadDigest (this protocol's own SHA-256 over node_id + sorted artifact URI/sha256 pairs) detects a caller reusing the SAME key with DIFFERENT evidence, which the frozen key alone cannot distinguish from a legitimate replay."
  - "Concurrent-completion race resolution: NodeStore.TransitionStatus's existing optimistic-concurrency guard (checkpoint-a02, UPDATE...WHERE status=? AND version=?) is what actually arbitrates two concurrent Run() calls; CompleteNode adds a POST-conflict fallback (re-check the idempotency ledger before propagating a raw conflict) specifically for the same-key/same-payload case, so N concurrent identical requests all succeed (one does real work, the rest transparently replay) rather than N-1 of them failing with a conflict they did nothing wrong to deserve. Different-key concurrent attempts on the same node correctly resolve to exactly one winner and N-1 fail-closed losers."
  - "A genuine data race was found (via `-race`) in this node's OWN test double, not in CompleteNode: the first version of the concurrent-race test's deterministic seqIDGenerator used a bare `g.n++`, which is not concurrency-safe, unlike the real production idgen.UUIDv7 (stateless). Fixed with sync/atomic. Recorded here explicitly because it is exactly the kind of finding `-race` exists to catch, and because a fake/test-double concurrency bug can otherwise be mistaken for (or mask) a real one."
blockers: none
```

### checkpoint-b05：二進位安全（binary-safe）patch 產生的邊界情況

```yaml
node: checkpoint-b05
status: completed
artifacts:
  - internal/repocheckpoint/patch_test.go  # ONLY new file this node needed — no production code changes; gitx.Client.DiffPatch (checkpoint-b04) and repocheckpoint.Capture (checkpoint-b04) already implement binary-safe patch generation correctly, this node's job was proving it under real edge cases, not building new mechanism
validation:
  - "gofmt -l internal/repocheckpoint internal/gitx -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/repocheckpoint/... -> OK"
  - "go test ./internal/repocheckpoint/... -run Patch -race -v -> PASS (6 tests: plain-text apply-round-trip, binary-file byte-exact apply-round-trip, mixed binary+text changeset both-survive-round-trip, large diff across 50 files x 200 lines each with all 50 spot-checked, large single-file diff of 5000 changed lines, full Capture-to-gzipped-manifest-artifact round-trip)"
  - "go test ./internal/repocheckpoint/... ./internal/gitx/... -race -> PASS, zero regressions"
  - "golangci-lint run ./internal/repocheckpoint/... ./internal/gitx/... -> 0 issues"
commit: e571480
next_action: checkpoint-b07 (atomic write -race hardening, cross-process crash-injection) — NOT started this phase per explicit assignment
assumptions:
  - "The required 'apply round-trip' test (generate a patch, apply it to a fresh checkout, verify the result matches) is proven by cloning the source repo (via argv-only `git clone`/`git apply`, never a shell string) into a separate temp directory checked out at the SAME base commit the patch was diffed against, then comparing file bytes — a filesystem COPY rather than a real clone would not exercise `git apply`'s actual blob-resolution logic (--full-index's whole point) the same way."
  - "'Large diffs' is interpreted as two distinct shapes per this node's own brief ('very large binary diffs' implies large-content, and the DAG note separately says 'many files'): a many-files-many-lines changeset (50 files x 200 lines, patch header count asserted exactly) AND a single very-large-file diff (5000 changed lines) — both proven to apply cleanly, since a many-file bug (ordering/truncation across files) and a single-large-file bug (hunk-boundary miscalculation) are different failure modes."
  - "No production code in internal/gitx or internal/repocheckpoint needed to change — DiffPatch's existing --binary --full-index --no-ext-diff flags (checkpoint-b04) were already exactly correct for every edge case this node tested; this is a genuine 'the hard part was proving it, not building it' node, unlike most of this role's other nodes."
blockers: none
```

### checkpoint-b06：未追蹤（untracked）檔案政策的機密過濾器＋internal/redact

```yaml
node: checkpoint-b06
status: completed
artifacts:
  - internal/redact/doc.go           # package-level scope statement: exactly what this package covers and does NOT cover, written explicitly for qa-05's benefit (this node's own DAG note: "Feeds qa-05 leakage scanner")
  - internal/redact/filename.go      # MatchesSecretFilename: ADD §27.8's 11 name patterns verbatim, matched against base filename only
  - internal/redact/patterns.go      # 6 content-detector classes verbatim (bearer token, PEM private-key header, GitHub/OpenAI/Anthropic token shapes, Azure storage keys, JWT-like, password/connection-string), each one fixed regexp
  - internal/redact/scan.go          # ScanPath/ScanContent, 1 MiB per-file scan cap, Git's own NUL-byte binary heuristic to skip binary content
  - internal/redact/*_test.go        # filename_test.go, patterns_test.go, scan_test.go — 25 tests total, one subtest per ADD §27.8 detector plus boundary cases (empty file, nonexistent file, content-beyond-cap, binary-content-not-scanned)
  - internal/repocheckpoint/security.go   # +2 new SkipReason values: secret_filename, secret_content (distinct so an operator auditing skipped-files.json can tell which class fired)
  - internal/repocheckpoint/archive.go    # buildUntrackedArchive now takes a scanSecrets bool and calls internal/redact after the existing size caps, before archiving; a match is skipped with the specific new SkipReason, feeding the SAME skip-ledger/partial-recoverability machinery checkpoint-b04 already built (no redesign)
  - internal/repocheckpoint/capture.go     # CaptureOptions.DisableSecretScan: explicit, off-by-default opt-out (zero value = scanning ON)
  - internal/repocheckpoint/untracked_test.go  # 7 new tests: secret-filename exclusion, secret-content exclusion, multiple detector shapes in one capture, no-secrets-full-recoverability negative case, opt-out honored, and an explicit test documenting the scope boundary (secret scan applies to the untracked archive only, NOT to already-tracked diff content captured via DiffPatch)
validation:
  - "gofmt -l internal/repocheckpoint internal/redact -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/repocheckpoint/... ./internal/redact/... -> OK"
  - "go test ./internal/repocheckpoint/... ./internal/redact/... -run Untracked -v -> PASS (15 repocheckpoint tests incl. the 7 new secret-filter tests + all pre-existing path-safety tests; redact package reports 'no tests to run' for this exact filter since none of its own test names contain the literal substring \"Untracked\" — its full suite is exercised separately and is green)"
  - "go test ./internal/repocheckpoint/... ./internal/redact/... -race -v -> PASS, zero regressions (46 tests total across both packages)"
  - "go test ./... -race -> green whole-repo"
  - "golangci-lint run ./... -> 0 issues"
commit: ef59034
next_action: checkpoint-b07/b08/b09 — NOT started this phase per explicit assignment. Nothing else deliberately deferred from this node's own scope: filename + content detection are both real (not stubbed), wired into the actual archive path (not just a standalone library nobody calls), and the documented boundary (doc.go) tells qa-05 exactly what this layer does and does not catch.
assumptions:
  - "internal/redact implements EXACTLY Auspex_ADD.md §27.8's two lists (name patterns, content detectors) — transcribed, not invented or extended, so there is a single source of truth for what 'the secret filter policy' means and no drift between this package and the ADD. Detector regexes were validated against realistic-length synthetic fixtures (e.g. an 88-char base64 Azure key, matching Azure's real key length) rather than hand-wavy short placeholders, after an initial manual sanity check caught a too-short test fixture silently under-testing the Azure detector."
  - "Content scanning is capped at 1 MiB per file and skips content identified as binary via Git's own NUL-byte heuristic — both documented as explicit, non-silent boundaries in doc.go, not just implementation details; a secret past the cap or inside binary content is a known gap, not an unstated one."
  - "The secret scan applies ONLY to the untracked-file archive (ADD §19.5's own scope for the 'secret scan' bullet) — NOT to tracked-file diff content captured via DiffPatch (checkpoint-b04/b05's scope). This boundary is deliberate (redacting already-committed history is a different problem than filtering what NEW untracked evidence a checkpoint captures) and is locked in by an explicit test (TestCapture_Untracked_SecretScan_NeverAppliesToTrackedDiffContent) rather than left as an unstated implicit scope decision a later reader would have to infer."
  - "CaptureOptions.DisableSecretScan is a bool (not *bool) with scanning-enabled as its zero value — chosen so a caller that never touches this field (every existing call site, and any future one that doesn't know this option exists) gets the safe default, matching this same struct's existing size-cap fields' zero-value-safe pattern from checkpoint-b04."
blockers: none
```

Phase 6 三個節點全部完成後的最終驗證：`golangci-lint run ./...`（整個 repo）→ 0 issues；`go build ./...` → OK；`go test ./... -race` → 每個 package 皆為綠燈，本波三個節點皆無回歸（regression）。

---

## Phase 7

本波指派節點：`checkpoint-a05`（Part A：StateCheckpointService 實作）、`checkpoint-a07`（Part A：重複／失序 provider 事件處理）、`checkpoint-b07`（Part B：atomic-write 的跨行程 crash injection ＋啟動時孤兒暫存目錄掃描）——依照本波明確指示依序完成，每項各自獨立驗證＋commit。前置步驟：`git fetch origin && git merge origin/main`——乾淨的 fast-forward 至 `1440f4c`（Phase 6 的整合狀態，包含其他角色 predictor／runtime 的 pause／policy／scheduler 工作，皆未觸及本角色的路徑）；合併後、在任何新工作開始之前，`go test ./...` 隨即呈現綠燈。

### checkpoint-a05：StateCheckpointService 實作

```yaml
node: checkpoint-a05
status: completed
artifacts:
  - internal/statecheckpoint/service.go       # Service implementing app.StateCheckpointService: Create/LoadLatest/Verify
  - internal/statecheckpoint/service_test.go  # snapshot-current-tree-state, LoadLatest-returns-most-recent, Verify valid + tampered-manifest + not-found, empty-TaskID validation
validation:
  - "gofmt -l internal/statecheckpoint -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/statecheckpoint/... -> OK"
  - "go test ./internal/statecheckpoint/... -race -v -> PASS (18 tests total: 8 pre-existing manifest/serialize/store + 10 new Service tests)"
  - "golangci-lint run ./internal/statecheckpoint/... -> 0 issues"
  - "go test ./... -race -> green whole-repo, zero regressions"
commit: 26c6496
next_action: checkpoint-a06/a08/a09 — NOT started this phase per explicit assignment
assumptions:
  - "checkpoint-a04's own lessons-learned note anticipated this exactly: 'much of a05's nominal scope (manifest serialization/checksum) was ALREADY built here since CompleteNode structurally required it; a05 should verify what remains, likely just Snapshot/verify-API polish.' Confirmed on inspection: manifest.go/serialize.go/build.go/store.go (a04) already fully implement the manifest shape, digest, and CRUD; this node's actual scope was exactly the frozen app.StateCheckpointService port implementation itself, which did not exist as a concrete type yet."
  - "Create is a NEW, standalone ad hoc snapshot entry point (Service.Create), deliberately separate from CompleteNode's own inline checkpoint-on-completion transaction (complete_node.go, unchanged) — it exists for callers (e.g. a manual 'checkpoint now' request, or a future runtime persist-phase wiring) that need a checkpoint of the CURRENT Progress Tree state without also completing a node. Reuses the exact same Build/Seal/Marshal/Store primitives CompleteNode already proved correct."
  - "TreeReader (new interface, NodeSnapshot/ArtifactSnapshot narrow view types) is Service's injected seam for reading current Progress Tree state, deliberately NOT importing internal/progress directly — internal/progress already imports internal/statecheckpoint (complete_node.go), so the reverse import would be a cycle. Production wiring (a later integration step, out of this node's scope) supplies a real adapter over *progress.NodeStore/*progress.ArtifactStore; this node's own tests use an in-memory fake. Same 'injected callback rather than reach into another sub-component's storage directly' discipline checkpoint-b04 established for Service.resolveWorktree."
  - "Verify recomputes the manifest's digest from scratch and compares against the stored integrity_sha256 column, never trusting the stored value alone — mirrors internal/repocheckpoint's Service.Verify 'never trust a stored checksum alone' discipline (checkpoint-b04), applied here to Part A's manifest instead of Part B's artifacts. An unparseable manifest reports Valid:false (fail-closed) rather than propagating a plumbing error, matching this codebase's established fail-open-vs-fail-closed contract (CONTRACT_FREEZE.md: a state-integrity failure must fail closed, and Valid:false for an unparseable manifest IS the fail-closed answer)."
  - "Compile-time assertion: var _ app.StateCheckpointService = (*Service)(nil) in service.go, plus service_test.go additionally exercises Service through the app.StateCheckpointService interface type directly (not just the concrete type), so a signature drift would fail to compile in the test file too."
blockers: none
```

### checkpoint-a07：重複／失序（out-of-order）provider 事件處理

```yaml
node: checkpoint-a07
status: completed
artifacts:
  - internal/progress/idempotency.go               # checkDuplicateProviderEvent (key-independent, evidence-digest-based duplicate detection) + loadReplayedResult (shared replay-result reconstruction, extracted from a04's checkIdempotency)
  - internal/progress/complete_node.go              # checkParentOrdering + startedStatuses: rejects a completion signal for a child node whose parent has never reached a "started" status
  - internal/progress/complete_node_idempotency_test.go  # renamed/corrected: TestCompleteNode_AlreadyCompleted_DifferentKey_DifferentEvidence_Rejected now uses genuinely different evidence (its old fixture used IDENTICAL evidence, which is the wrong fixture for what it was meant to prove — see assumptions)
  - internal/progress/complete_node_provider_events_test.go  # NEW: duplicate-provider-event (different key, same evidence, triple-channel redelivery) + out-of-order (child-before-parent-started, parent-in-progress-allowed, parent-already-completed-allowed, root-node-no-parent-skipped)
validation:
  - "gofmt -l internal/progress -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/progress/... -> OK"
  - "go test ./internal/progress/... -run Idempotency -v -> PASS (DAG's frozen validation command; 1 test name matches the literal substring, unchanged from a04)"
  - "go test ./internal/progress/... -race -v -> PASS, full suite including all new a07 tests, zero regressions"
  - "golangci-lint run ./internal/progress/... -> 0 issues"
  - "go test ./... -race -> green whole-repo"
commit: 49efd06
next_action: feeds qa-04 (duplicate/out-of-order test) directly — public API is CompleteNode.Run itself (no new exported surface needed); qa-04 can drive the exact same scenarios (different-key-same-evidence redelivery, child-before-parent-started) through this same entry point. checkpoint-a06/a08/a09 NOT started this phase per explicit assignment.
assumptions:
  - "Scope boundary versus a04: a04 already fully proved idempotency-KEY matching (same key -> same result; conflicting payload under the same key -> rejected) — NOT re-tested here, per the phase brief's explicit instruction. a07's genuine increment is two things a04 did not cover: (1) duplicate detection independent of key (a provider redelivering the same event through a different channel that derives its own key), and (2) parent/child ordering (a04 only checked depends_on edges via checkDependencies, never parent_id)."
  - "Constitution §6.6 says 'duplicate completion with CONFLICTING evidence is rejected' — read literally, this means duplicate completion with IDENTICAL evidence is NOT a conflict, regardless of which idempotency key arrived with it. checkDuplicateProviderEvent implements exactly this: a key mismatch alone no longer auto-rejects; the evidence digest is compared first, and only a genuine digest mismatch rejects as a conflict. This is a real, deliberate behavior change from a04's original always-reject-on-key-mismatch posture, not a bug — a04's own test asserting the old behavior (TestCompleteNode_AlreadyCompleted_DifferentKey_Rejected) used IDENTICAL evidence for both calls, which was actually the correct fixture for the NEW test's assertion (replay, not reject) rather than the old one; renamed and given genuinely different evidence so it still exercises the real conflict path it was meant to guard."
  - "checkParentOrdering treats in_progress/checkpointing/completed/failed/paused as 'parent has started'; only pending/ready/blocked count as 'not started yet' and trigger the out-of-order rejection. Deliberately does NOT enforce strict start-then-finish ordering between parent and child (a parent legitimately completing slightly before a straggling child's own evidence finishes staging is an allowed race per the existing state machine, proven by TestCompleteNode_ChildCompletes_ParentAlreadyCompleted_Allowed) — the check is specifically about a parent that never started at all, matching the DAG's own framing ('before its parent's in-progress transition is recorded')."
  - "Out-of-order rejection uses ErrCodeConflict with Retryable:true (unlike the dependency-policy and idempotency-conflict rejections elsewhere in this file, which are Retryable:false) — a genuinely out-of-order signal is expected to resolve itself once the parent's own in-progress event catches up, so a caller retrying later is the correct, expected recovery path, not a permanent failure."
  - "No cross-role contract gap found; no ports.go change requested. Both changes are entirely internal to internal/progress's own orchestration logic, calling only stores/seams this role already owns."
blockers: none
```

### checkpoint-b07：原子寫入（atomic-write）的跨行程 crash injection ＋孤兒（orphan）掃描

```yaml
node: checkpoint-b07
status: completed
artifacts:
  - internal/repocheckpoint/atomicwrite.go        # writeArtifactDirWithHalt (crash-injection seam: phaseTempDirCreated/phaseFilesWritten/phaseRenamed + writeArtifactDirHaltError) added alongside the existing writeArtifactDir (now a thin wrapper calling the halt variant with ""); new tempDirPrefix constant shared with orphanscan.go
  - internal/repocheckpoint/atomicwrite_crash_test.go  # white-box (package repocheckpoint): crash at each of the 3 phases proving finalDir is never partially visible, normal-completion regression guard, retry-after-crash succeeds despite the leftover orphan
  - internal/repocheckpoint/orphanscan.go          # ScanOrphanedTempDirs/CleanOrphanedTempDirs: age-gated scan+cleanup of ".checkpoint-tmp-*" directories under a checkpoints root
  - internal/repocheckpoint/orphanscan_test.go     # finds-old-orphan, ignores-real-checkpoint-dirs-and-non-dir-stray-files, skips-young-temp-dir, nonexistent-root-no-error, clean-removes-old-only, and the required -race concurrent-capture test (20 real live captures racing 20 concurrent scan/clean passes)
  - internal/repocheckpoint/export_test.go         # standard Go export_test.go idiom: exposes writeArtifactDir under WriteArtifactDirForTest for the external test package's race test only; not part of the production API
validation:
  - "gofmt -l internal/repocheckpoint -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/repocheckpoint/... -> OK"
  - "go test ./internal/repocheckpoint/... -run Atomic -race -v -> PASS (10 tests: 5 crash-injection + 5 orphan-scan, all matching the DAG's frozen -run Atomic filter)"
  - "go test ./internal/repocheckpoint/... -race -v -> PASS, full suite (60 tests total), zero regressions"
  - "golangci-lint run ./internal/repocheckpoint/... -> 0 issues"
  - "go test ./... -race -> green whole-repo"
commit: 5f853d9
next_action: checkpoint-b08 (RestoreDryRun), checkpoint-b09 (path traversal/symlink security gate) — NOT started this phase per explicit assignment
assumptions:
  - "Crash injection follows internal/progress's CompleteNode.HaltAfter/HaltError pattern (checkpoint-a04) exactly, adapted from a struct field (CompleteNode is a long-lived service value) to a function parameter (writeArtifactDir is a free function with no receiver) — same 'named phase + halt hook as a first-class testing seam' philosophy, not a redesign."
  - "The existing error-path cleanup in writeArtifactDir (the `defer os.RemoveAll(tempDir)` on a non-halt error) is explicitly NOT triggered by a halt — a writeArtifactDirHaltError is a SIMULATED crash (the point is that real production code never gets a chance to run its own cleanup when genuinely killed), so the halt path skips that defer's cleanup on purpose, leaving a real orphan on disk for orphanscan.go to find, exactly mirroring what a real kill -9 would leave behind."
  - "ScanOrphanedTempDirs takes an explicit minAge + now(time.Time) rather than hardcoding a duration or using time.Now() internally, so (1) a startup check can use minAge=0 (nothing is in flight yet, process just started) while a long-running daemon integration can use a conservative minAge (e.g. a few minutes) to avoid racing a live capture, and (2) tests get deterministic, non-flaky age comparisons. This mirrors this role's own established Clock-injection discipline (domain.Clock everywhere else in this codebase) applied to a plain time.Time parameter here since this package doesn't thread domain.Clock through its free functions."
  - "Every temp directory is unconditionally safe to remove once past minAge: by construction, nothing durable (no DB row via internal/repocheckpoint.Store, no manifest.json under a FINAL path) ever references a .checkpoint-tmp-* path — only the post-rename finalDir is ever recorded anywhere durable. This was verified by inspection of every writer of repository_checkpoints and every reader of ArtifactsRoot in this package, not merely assumed."
  - "Retry-after-crash (TestAtomicWrite_RetryAfterCrash_SucceedsCleanly) confirms writeArtifactDir's existing finalDir-collision guard (checkpoint-b04) does not also block on a stale sibling temp dir — each call gets its own os.MkdirTemp-randomized name, so an orphan from a prior crash never prevents a fresh, correct retry for the same checkpoint ID. This is a load-bearing property orphanscan.go's cleanup depends on: retries must keep working even before a cleanup sweep ever runs."
  - "Out of this node's explicit scope, confirmed by re-reading capture.go's own doc comment: the retry-ONCE-on-race-detected POLICY (distinct from this node's crash/orphan scope) that an earlier draft of capture.go's comment also attributed to checkpoint-b07 is NOT built here — this phase's assignment prompt scoped b07 explicitly to 'atomic-write cross-process crash-injection and startup orphan-temp-dir scanning' only, matching checkpoint-b04's own lessons-learned note precisely. Flagging this for whichever later node (b08/b09, or a follow-up) is meant to own the race-retry-policy, since capture.go's in-code comment currently points at a b07 that did not build it."
blockers: none
```

Phase 7 三個節點全部完成後的最終驗證：`golangci-lint run ./...`（整個 repo）→ 0 issues；`go build ./...` → OK；`go vet ./...` → OK；`go test ./... -race` → 每個 package 皆為綠燈，本波三個節點皆無回歸。

---

## Phase 8

前置步驟：`git fetch origin && git merge origin/main`——乾淨的 fast-forward 至 `2b7c29c`（Phase 7 的整合狀態，包含 `qa` 的 leakage-scanner 整合測試套件，以及其他角色 `predictor`／`runtime` 的 evaluation／pause 工作，事先皆未觸及本角色自己的路徑）。

本波指派：一項修正性修復（qa-05 的 `TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered` 發現，此發現獨立確認了本角色自己在 `untracked_test.go` 中所記載的邊界註記）外加三個新的 DAG 節點——`checkpoint-a06`、`checkpoint-a08`、`checkpoint-b08`——依照本波明確指示依序完成，每項各自獨立驗證＋commit。

### 修正性修復：將機密掃描擴展至已追蹤檔案的 diff 內容

```yaml
node: corrective-qa05-p1
status: completed
artifacts:
  - internal/repocheckpoint/patchredact.go             # redactPatchSecrets: line-scoped secret redaction over a binary-safe patch (added/removed lines only, never context/header lines, so the patch stays git-apply-able)
  - internal/repocheckpoint/patchredact_internal_test.go  # unit coverage of the line-scope rules directly (added-line redacted, removed-line redacted, context-line never touched, file-header lines never mistaken for content, multi-hunk/multi-file isolation, empty-patch no-op)
  - internal/repocheckpoint/capture.go                 # wires redactPatchSecrets into both staged/unstaged patch generation before archiving; records a Recoverability.Warnings entry when redaction occurred (Level stays complete — a redacted patch is still fully present and applicable, not missing evidence)
  - internal/repocheckpoint/untracked_test.go           # TestCapture_Untracked_SecretScan_NeverAppliesToTrackedDiffContent (asserted the OLD gap as deliberate scope) renamed/rewritten to TestCapture_Untracked_SecretScan_AlsoAppliesToTrackedDiffContent (asserts the fix)
validation:
  - "gofmt -l internal/repocheckpoint internal/redact internal/gitx -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/repocheckpoint/... -> OK"
  - "go test ./internal/repocheckpoint/... -race -v -> PASS, full suite, zero regressions"
  - "golangci-lint run ./... (whole repo, run after all 4 items) -> 0 issues"
  - "go test ./internal/integrationtest/... -run LeakageScanner -race -v (read-only sanity check, qa-owned package not modified) -> TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered now FAILS as expected per its own comment ('if this now fails, either the gap has been fixed upstream ... do not silently adjust the assertion') — every other LeakageScanner test still passes. The gap that test documents is closed; updating its assertion is qa's to do in a future node, not this role's to touch."
commit: f981bde
next_action: checkpoint-a06 (this phase's Node 1)
assumptions:
  - "Design choice — redact-in-place, not skip-with-manifest-annotation. ADD §19.3's Capture sequence places 'secret/size/symlink filters' (step 8) generally between diff generation (5-6) and archival (9), not scoped narrowly to untracked files the way §19.5's own 'Untracked policy' section states its secret-scan bullet — read as license to close the gap for patch content too, not to treat the boundary as fixed. Redaction was chosen over skip-with-annotation because: (1) §19.6 Restore (dry-run and any future real restore) needs `git apply --check` against the staged/unstaged patches — skipping the whole patch on one detected secret line would leave NOTHING to restore-check for that entire scope, not just the sensitive line; (2) a staged/unstaged diff is one unified patch blob per side (unlike untracked files, individually excludable candidates in a zip), so skipping it would discard every unrelated legitimate change in the same patch, a much larger evidence loss than the untracked path's per-file skip."
  - "Redaction scope is deliberately narrow: only '+'/'-' line BODIES that individually match internal/redact's content detectors are replaced with a fixed placeholder; context lines (leading space) and all header lines (diff --git/index/---/+++/@@ hunk headers) are never touched, because context lines must match the target file exactly for `git apply` to locate a hunk — rewriting one would silently corrupt the patch's own applicability. This was verified, not just reasoned about: patch_test.go's existing apply-round-trip tests (checkpoint-b05, unmodified by this fix) still pass unchanged, proving ordinary (non-secret) patches round-trip exactly as before."
  - "Binary patches (`GIT binary patch` directives) are untouched by construction: a binary literal-diff line is never '+'/'-' prefixed content in the shape redactPatchSecrets scans, so no special-case was needed."
  - "IndexDiffHash/WorktreeDiffHash (manifest Snapshot block) are now computed from the REDACTED patch bytes, not the raw gitx.DiffPatch output — consistent with 'the hash should reflect what is actually archived,' and avoids a digest that could not be reproduced by re-hashing the checkpoint's own on-disk artifact."
blockers: none
```

### checkpoint-a06：針對 Service.Create crash window 的啟動時（startup）reconciliation

```yaml
node: checkpoint-a06
status: completed
artifacts:
  - internal/statecheckpoint/service.go        # adds Phase/HaltAfter/HaltError crash-injection seam to Service.Create (PhaseReadTree, PhaseSeal, PhaseInsert), mirroring internal/progress.CompleteNode's identical idiom
  - internal/statecheckpoint/reconcile.go      # Reconciler/NewReconciler: read-only startup scan over a task's state_checkpoints rows (parseable manifest, frozen SchemaVersion, non-empty digest, recomputed-digest match, manifest.TaskID/CheckpointID agree with the row's own columns)
  - internal/statecheckpoint/reconcile_test.go # crash-injection harness: halt at each of the 3 phases (individually + an exhaustive all-phases subtest loop), tampered-manifest positive-detection test, unknown-task empty-report test
validation:
  - "gofmt -l internal/statecheckpoint -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/statecheckpoint/... -> OK"
  - "go test ./internal/statecheckpoint/... -run Reconcile -race -v -> PASS (DAG's frozen validation command; 7 tests)"
  - "go test ./internal/statecheckpoint/... -race -v -> PASS, full suite, zero regressions"
  - "golangci-lint run ./... (whole repo, after all 4 items + 1 follow-up copyloopvar fix) -> 0 issues"
  - "go test ./... -race -> green whole-repo"
commit: 8ecc0cc (copyloopvar lint fix: 2ecd8f6)
next_action: checkpoint-a08 (this phase's Node 2)
assumptions:
  - "Traced exactly which crash windows exist in Create's own sequence rather than assuming a generic 'staged-artifact-vs-DB' shape (that is internal/progress.Reconciler's DIFFERENT, already-solved problem for CompleteNode, checkpoint-a04): Create has exactly ONE phase where durable state exists at all (PhaseInsert, a single SQL statement SQLite commits atomically — no half-inserted-row state is reachable, unlike internal/repocheckpoint's multi-file artifact writes). PhaseReadTree and PhaseSeal are both pure no-ops from a durability standpoint: nothing to reconcile because nothing was written."
  - "Reconcile is therefore a read-only integrity SCAN, not a repair mechanism — there is nothing for it to fix, by construction, since the only durable-state phase is one atomic statement. This mirrors internal/repocheckpoint.Verify and internal/progress.Reconciler both being diagnostic-only, never self-healing writers."
  - "Deliberately a SEPARATE Reconciler type from internal/progress.Reconciler (checkpoint-a04), not a shared one — the two target genuinely different crash windows (Part A's own node-completion staged-artifact/DB gap vs. this package's own Create-sequence gap) and forcing them into one abstraction would blur that boundary for no benefit."
  - "Crash-injection tests prove the negative exhaustively (every phase, not just one) rather than asserting a single happy path — the all-phases subtest loop is the direct 'genuine crash-injection harness proving no orphaned/dangling state survives startup' deliverable this node's own DAG risk note calls for."
blockers: none
```

### checkpoint-a08：Snapshot/LoadLatest/Verify APIs——Snapshot 增量部分

```yaml
node: checkpoint-a08
status: completed
artifacts:
  - internal/statecheckpoint/service.go       # adds Service.Snapshot(ctx, id) (domain.StateCheckpoint, error) — point-in-time read by ID, distinct from LoadLatest (task-scoped, latest-only) and Verify (pass/fail verdict only, no reconstructed state)
  - internal/statecheckpoint/snapshot_test.go # proves Snapshot returns an OLDER checkpoint's own state correctly even after a newer one exists for the same task, agrees with LoadLatest when asked for the latest ID, shares the frozen not-found contract, is consistent with (but distinct from) Verify
validation:
  - "gofmt -l internal/statecheckpoint -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/statecheckpoint/... -> OK"
  - "go test ./internal/statecheckpoint/... -run 'Snapshot|LoadLatest|Verify' -race -v -> PASS (DAG's frozen validation command; 13 tests, including a05's pre-existing LoadLatest/Verify coverage)"
  - "go test ./internal/statecheckpoint/... -race -v -> PASS, full suite, zero regressions"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
  - "go test ./... -race -> green whole-repo"
commit: 905c298
next_action: checkpoint-b08 (this phase's Node 3)
assumptions:
  - "Checked what a05 already delivered before assuming greenfield scope, per this phase's explicit instruction: LoadLatest and Verify were both already fully implemented and tested (checkpoint-a05). The genuine incremental gap this node's own validation filter (Snapshot|LoadLatest|Verify) implies is a 'give me the full state as of checkpoint X' point-in-time read by ID — neither existing method answers that (LoadLatest is task-scoped and always the newest row; Verify returns only {ID, Valid}, no reconstructed manifest)."
  - "Snapshot is NOT part of the frozen app.StateCheckpointService interface (Create/LoadLatest/Verify only) — added as a package-level method on the concrete *Service type instead, the same way Reconciler/NewReconciler (checkpoint-a06) are additions outside the frozen port. ports.go was not touched."
  - "Snapshot is a plain read, like LoadLatest — it does not itself recompute/verify the integrity digest; a caller wanting the fail-closed guarantee calls Verify separately against the same ID. Proven explicitly by a dedicated consistency test."
blockers: none
```

### checkpoint-b08：Restore dry-run（還原試跑）

```yaml
node: checkpoint-b08
status: completed
artifacts:
  - internal/gitx/patch.go                       # adds Client.ApplyCheck: `git apply --check [--cached] [--binary]` against a worktree, patch content written to a private temp file (domain.ProcessRunner has no stdin parameter) and removed before returning; empty patch trivially reports WouldApply:true without invoking git
  - internal/gitx/patch_test.go                   # ApplyCheck coverage: clean patch reset-to-base applies cleanly and mutates nothing, diverged/conflicting content correctly reports WouldApply:false with Git's own diagnostic detail, empty-patch no-op, unstaged (working-tree) scope
  - internal/repocheckpoint/restoredryrun.go      # RestoreDryRun: full ADD §19.6 check sequence minus anything that mutates (verify checksum via Verify; verify repo identity via a caller-supplied expectedRepositoryID vs. manifest.Repository.RepositoryID, deliberately NOT HEAD-position; report dirty-target as a fact, no AllowDirty input at this layer; git apply --check staged/unstaged separately; produce a report collecting every problem)
  - internal/repocheckpoint/restoredryrun_test.go # RestoreDryRun-level tests (clean/tampered/identity-mismatch/dirty-reported-not-vetoed/diverged-apply-check) + Service.Restore-level tests (dry-run succeeds not-applied, dirty rejected without AllowDirty, dirty permitted with AllowDirty, worktree-resolution error propagation)
  - internal/repocheckpoint/service.go            # Service.Restore (frozen app.RepositoryCheckpointService port method) replaces the old unconditional ErrCodeUnavailable stub: loads the row, resolves the worktree, runs RestoreDryRun, applies the AllowDirty policy decision, maps the report onto RestoreResult{ID, Applied:false always} + ErrCodeConflict-with-Details on any problem
  - internal/repocheckpoint/service_test.go       # TestService_Restore_NotImplemented replaced with TestService_Restore_UnknownCheckpoint_NotFound (Restore's real not-found behavior for an unknown ID)
validation:
  - "gofmt -l internal/repocheckpoint internal/gitx -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/repocheckpoint/... ./internal/gitx/... -> OK"
  - "go test ./internal/repocheckpoint/... -run RestoreDryRun -race -v -> PASS (DAG's frozen validation command; 9 tests: 5 RestoreDryRun-level + 4 Service.Restore-level, all matching the -run filter)"
  - "go test ./internal/repocheckpoint/... ./internal/gitx/... -race -v -> PASS, full suite, zero regressions"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
  - "go test ./... -race -> green whole-repo"
commit: bedd7a7
next_action: checkpoint-b09 (path traversal/symlink security gate) — NOT started this phase per explicit assignment; real restore (mutating) remains explicitly out of vertical-slice scope per this node's own DAG risk note
assumptions:
  - "git apply --check semantics required a genuine correction mid-node: an initial test wrote a patch, then immediately ran ApplyCheck against the SAME already-staged index the patch was generated from — which fails, correctly, because the index has already moved PAST the patch's own pre-image ('before' state no longer exists to match against). This is not a bug in ApplyCheck; it is the realistic restore scenario being asked the wrong question. Fixed by resetting the index/working tree back to the patch's own base before dry-running the check — the actual scenario a restore dry-run answers ('if the target were at the patch's base, would this still apply'), verified against real `git apply --check` behavior in a scratch repo before committing to the permanent test suite."
  - "RepositoryIdentityMatch checks manifest.Repository.RepositoryID against a caller-supplied expectedRepositoryID (freshly resolved for the SAME WorktreeID via the same resolveWorktree seam Service.Create uses), NOT GitHead — HEAD legitimately moves between capture and a later restore attempt, and a stale HEAD is exactly what the apply-check step already covers; conflating the two would make ordinary, expected drift look like an identity problem."
  - "WouldSucceed (the free function's own verdict) deliberately excludes the dirty-target check — RestoreDryRun has no AllowDirty policy input to decide that condition with. Service.Restore (which DOES receive AllowDirty via the frozen RestoreRepositoryCheckpointRequest) is where the dirty-target veto is actually applied: true only demotes a dirty finding from a blocking problem to a non-issue, every OTHER problem (checksum/identity/apply-check) still blocks regardless."
  - "RestoreResult{ID, Applied} has no room for a rich problem report (frozen, cannot be amended — ports.go is contract-integrator-owned) — Applied is always false (dry-run never mutates, whether or not the dry-run would have succeeded), and a dry-run that finds problems returns a non-nil ErrCodeConflict domain.Error with every problem individually keyed in Details, giving a caller actionable diagnostics without a new frozen type."
  - "A patch-artifact load/decompress failure (e.g. a tampered gzip stream) is reported as a dry-run PROBLEM in the returned report, not returned as a hard Go error — the same corruption checksum verification (step 1) already flagged; a dry-run's whole purpose is to report findings, not abort before producing one."
blockers: none
```

修正性修復以及 Phase 8 三個節點全部完成後的最終驗證：`golangci-lint run ./...`（整個 repo）→ 0 issues；`go build ./...` → OK；`go vet ./...` → OK；`go test ./... -race` → 每個 package 皆為綠燈，唯獨 `internal/integrationtest` 的 `TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered` 例外，此測試現在失敗——這是預期且刻意如此，因為本波的修正性修復正好補上了該測試所記載的缺口；依該測試自身的註解所述，更新其斷言是 qa 未來某個節點該做的事，不是本角色該碰的（該檔案由 qa 擁有，本波僅讀取，從未編輯）。

---

## Phase 9 ——最終關卡節點：checkpoint-a09、checkpoint-b09

前置步驟：`git fetch origin && git merge origin/main`——乾淨的 fast-forward 至 `36e7ffb`（Phase 8 的整合狀態，包含 `qa` 針對 LeakageScanner 測試的更新，以反映本角色自己對 tracked-diff 的 redaction 修復，以及其他角色 `predictor`／`runtime` 的 Phase 8 工作，皆未觸及本角色自己的路徑）。合併後、在任何新工作開始之前，`go test ./... -race` 隨即呈現綠燈（包含先前預期會失敗的 LeakageScanner 測試，現已因 qa 更新其斷言而通過）。

本波指派：`checkpoint-a09`（Part A 最終整合關卡），接著是 `checkpoint-b09`（Part B 最終安全關卡）——這是指派給本角色的最後兩個 DAG 節點，依照本波明確指示依序完成，每項各自獨立驗證＋commit。`agents/checkpoint.md`／`EXECUTION_DAG.md` 明確將兩者定位為跨切面（cross-cutting）的證明，用以證明 Phases 1-8 中已建置完成的整個技術堆疊能夠端到端地整合運作，而非新增功能——完成這兩者即代表本角色被指派的「整個」DAG 範疇（a01-a09、b01-b09）全部結束。

### checkpoint-a09：Part A 最終整合關卡

```yaml
node: checkpoint-a09
status: completed
artifacts:
  - internal/progress/complete_node_integration_test.go   # NEW file, package progress_test — ONLY new file this node needed; no production code changed anywhere in internal/progress or internal/statecheckpoint
validation:
  - "gofmt -l internal/progress internal/statecheckpoint -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/progress/... ./internal/statecheckpoint/... -> OK"
  - "go test ./internal/progress/... ./internal/statecheckpoint/... -race -v -> PASS (full suite, including 3 new TestA09_* integration tests)"
  - "go test ./internal/progress/... ./internal/statecheckpoint/... -race -count=3 -> PASS, stable across repeats (no flakiness)"
  - "go test ./... -race -> green whole-repo, zero regressions"
  - "golangci-lint run ./internal/progress/... ./internal/statecheckpoint/... -> 0 issues"
commit: (recorded below)
next_action: checkpoint-b09 (this phase's second and final node)
assumptions:
  - "Pure test-only node, deliberately: every earlier a04-a08 node already built the real production mechanism (CompleteNode's atomic protocol, Service.Create/Snapshot/LoadLatest/Verify, both packages' own Reconcilers); a09's job per its own DAG framing is proving those pieces compose correctly end to end using the REAL implementations, not adding a new one. No file outside this single new test file was touched."
  - "realTreeReader (test-local, in complete_node_integration_test.go, NOT production code) adapts the REAL *progress.NodeStore/*progress.ArtifactStore to statecheckpoint.TreeReader, deliberately replacing service_test.go's in-memory fakeTreeReader for this file's own tests — the whole point of this node is exercising the actual stack, not a stand-in. This is exactly the 'production wiring layer' internal/statecheckpoint's own doc comments (checkpoint-a05) anticipated a later integration step would supply; this node IS that integration proof, kept test-local since no wiring package/cmd exists yet in this role's own paths to hold it permanently."
  - "Proof 1 (100-node extension): reused the exact same 100-sequential-nodes shape as checkpoint-a04's original required test, but added a second pass over all 100 resulting checkpoint IDs calling Service.Snapshot (a08) and Service.Verify (a05) on each — a04's original test only called the package-level statecheckpoint.Unmarshal/Verify functions directly against the raw store row, never exercising the Service layer's own API surface a real caller (runtime, or a future CLI) would actually use. Also cross-checked LoadLatest agrees with the 100th checkpoint, and ran both Reconcilers once over the resulting history as a baseline (non-crash) sanity check reused by proof 3's crash scenario."
  - "Proof 2 (cross-package concurrent race): deliberately DIFFERENT nodes per writer goroutine (30 workers), not the same node — checkpoint-a04's own race tests already fully proved the same-node case (optimistic-concurrency arbitration via NodeStore.TransitionStatus). The genuine new shape here is concurrent completions of DISTINCT nodes racing against CONCURRENT reads: a background goroutine repeatedly calls both packages' Reconcilers AND Service.LoadLatest+Verify against the same, growing state_checkpoints table while writes are still landing. Every read either observes a consistent snapshot (zero violations, a Verify()-able latest checkpoint) or the frozen not-found error for 'no checkpoints yet' — asserted to never see a torn/inconsistent read across the whole run, then re-verified once more after wg.Wait() as a final, definitely-quiesced check."
  - "Proof 3 (two-reconciler-agreement): halts CompleteNode.Run via HaltAfter=PhaseVerifyArtifacts (evidence staged to disk, DB transaction never opened) after first completing 3 nodes cleanly, giving both Reconcilers real pre-existing history to reason about rather than an empty task. Constructs FRESH Reconciler struct values bound to the same *sqlite.DB/evidence dir after the halt (not reusing the harness's original struct values) to model a genuine process restart rather than the same long-lived object happening to still be around. internal/progress's Reconciler correctly finds exactly 1 orphaned staged artifact and 0 integrity violations; internal/statecheckpoint's Reconciler correctly reports CheckpointsScanned=3 (not 4 — the crash victim's row was never inserted) and 0 violations. Both conclusions describe the SAME crash window from two different vantage points (filesystem+artifacts table vs. state_checkpoints rows) without either reconciler knowing the other exists, and neither one falsely reports either a phantom 4th checkpoint or a broken pre-existing one. A subsequent retry with identical evidence content (proving FileStager's content-addressed idempotency) succeeds, and both reconcilers then re-converge to 0 orphans / 4 scanned / 0 violations, proving recovery is not merely non-contradictory but actually reaches a fully-reconciled end state."
  - "Genuine bug caught while building proof 3: the test's first draft used IDENTICAL literal Markdown content ('# X\\n\\nprose\\n') for every node's artifact, which meant every node's FileStager-staged evidence hashed to the SAME sha256 — the crash victim's own staged file was already 'referenced' by the 3 earlier checkpoints' manifests purely by content coincidence, so the orphan-detection assertion passed for the wrong reason (actually: failed with 0 orphans found, catching the flaw immediately). Fixed by a small uniqueMarkdown(nodeSuffix) helper giving every node's evidence genuinely distinct content/digest, which is exactly the kind of fixture-quality issue this role's own Phase 6 lessons-learned note (redact package's too-short Azure key fixture) already flagged as a recurring risk category worth calling out explicitly rather than silently patching."
  - "No cross-role contract gap found; no ports.go change requested. No production code in internal/progress or internal/statecheckpoint needed any change — every mechanism this node exercises (CompleteNode.Run, Service.Create/Snapshot/LoadLatest/Verify, both Reconcile methods) was already correct; this node's entire value is the proof, not a fix."
blockers: none
```

### checkpoint-b09：Part B 最終安全關卡

```yaml
node: checkpoint-b09
status: completed
artifacts:
  - internal/repocheckpoint/security.go                       # NEW: safeArtifactPath (manifest-declared artifact path validation) + safeRelativeName (writeArtifactDir files-map-key validation)
  - internal/repocheckpoint/verify.go                         # FIX: Verify now calls safeArtifactPath before joining a manifest artifact's Path onto ArtifactRoot, instead of joining it unchecked
  - internal/repocheckpoint/atomicwrite.go                    # HARDENING: writeArtifactDir now calls safeRelativeName on every files map key before writing, defense in depth
  - internal/repocheckpoint/capture.go                        # HARDENING: Capture now rejects a CheckpointID that is not a safe relative path segment before joining it onto ArtifactsRoot
  - internal/repocheckpoint/security_adversarial_internal_test.go  # NEW, white-box (package repocheckpoint): unit coverage of safeArtifactPath/safeRelativeName + writeArtifactDir's own defense-in-depth rejection
  - internal/repocheckpoint/security_adversarial_test.go       # NEW, external (package repocheckpoint_test): end-to-end adversarial fixtures through the real Capture/Verify pipeline
validation:
  - "gofmt -l internal/repocheckpoint internal/gitx -> empty"
  - "go build ./... -> OK"
  - "go vet ./internal/repocheckpoint/... ./internal/gitx/... -> OK"
  - "go test ./internal/repocheckpoint/... ./internal/gitx/... -race -v -> PASS (full suite, zero regressions, including every new adversarial test)"
  - "go test ./internal/repocheckpoint/... ./internal/gitx/... -race -count=2 -> PASS, stable (no flakiness)"
  - "go test ./... -race -> green whole-repo"
  - "golangci-lint run ./internal/repocheckpoint/... ./internal/gitx/... -> 0 issues"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
commit: (recorded below)
next_action: none — this was this role's LAST assigned DAG node (a01-a09, b01-b09 all complete)
assumptions:
  - "GENUINE SECURITY FINDING, not merely a test-writing exercise: while auditing every path-join in Part B for the adversarial fixtures this node's own DAG risk callout demands, found that Verify (verify.go) joined manifest.Artifacts[].Path directly onto row.ArtifactRoot with NO traversal/symlink validation at all — unlike validateUntrackedPath's identical treatment of git-reported untracked paths (security.go, checkpoint-b04). manifest.json is read fresh from disk on every Verify/RestoreDryRun call (RestoreDryRun's own checksum step calls Verify first) and is an ordinary file for as long as a checkpoint directory exists — nothing prevents it from later being hand-edited, corrupted, or restored from an untrusted source. Confirmed exploitable with a standalone reproduction BEFORE fixing it: a manifest with one artifact path rewritten to a '../'-laden relative path made Verify open, stat, and SHA-256 a file completely outside the checkpoint's own directory (its computed hash appeared in the mismatch report, proving the read happened). Fixed with a new safeArtifactPath guard (security.go) mirroring validateUntrackedPath's exact posture (no '..' segment, no absolute path, resolved path must stay under ArtifactRoot, neither the leaf nor any ancestor directory may be a symlink) applied to manifest-declared paths instead of git-reported ones. TestVerify_ManifestArtifactPathTraversal_Rejected (security_adversarial_test.go) is the permanent regression test, built by hand-tampering a REAL manifest.json produced by a REAL Capture call (not a synthetic fixture), and additionally asserts the secret file's actual content never leaks into the returned problem report."
  - "Two further defense-in-depth hardenings applied proactively (not confirmed independently exploitable through any current production caller, but closing the same class of gap before qa-06 or a future caller could reintroduce it): (1) writeArtifactDir (atomicwrite.go) now validates every files map key via a new safeRelativeName check before joining it under tempDir — today's only caller (capture.go) always supplies a small fixed set of literal names, but writeArtifactDir is this package's own general atomic-write primitive, not a Capture-only helper, so it should not rely on every future caller re-deriving the same safety property independently; (2) Capture itself (capture.go) now rejects a CheckpointID that is not a safe relative path segment before joining it onto ArtifactsRoot — CheckpointID is a public-API field, and while production wiring always supplies a domain.IDGenerator-produced opaque ID, this function's own contract does not otherwise prevent a caller from passing untrusted input. Both are unit-tested directly (security_adversarial_internal_test.go) AND proven not to break Capture's real files-map/CheckpointID usage (the full pre-existing suite stayed green with zero changes to any non-adversarial test)."
  - "Required-tests inventory reviewed against every earlier b02-b08 node before writing anything new (agents/checkpoint.md Part B's full list: tracked/staged/unstaged/untracked, rename/delete, binary file, spaces/newlines in path, nested worktree, concurrent mutation, temp cleanup, path traversal, oversize, secret-like file exclusion) — every one already has real coverage from b04-b08. This node's genuine increment, per its own DAG risk callout ('path traversal/symlink escape tests are a security gate'), is: (a) the manifest-path-traversal finding above; (b) a nested-directory-via-symlink escape (deeper than security_test.go's existing single top-level-symlink case) and a dangling-symlink case (target does not exist at all); (c) a malicious CheckpointID (both '../' and absolute-path shapes); (d) an embedded-NEWLINE-byte filename (a real, platform-permitted special-character shape distinct from the existing spaces-in-path test, proven to survive this package's NUL-terminated `-z` parsing); (e) a combined oversize-file-plus-symlink-escape single capture, proving the two independent guards both fire correctly together rather than one masking the other. Every fixture asserts the malicious path/content NEVER appears inside the produced archive/temp-directory tree, not merely that the call didn't error."
  - "Every adversarial fixture uses only argv-based gitx.Client calls (Constitution §7 rule 5) — the malicious inputs are filesystem-level (real symlinks via os.Symlink, a real hand-edited manifest.json, a crafted CheckpointID string) rather than attempted shell-command injection, since this whole codebase already never constructs a shell command string anywhere; the actual risk surface for this role is path-join safety, not command injection, which is exactly what this node's fixtures target."
  - "No cross-role contract gap found; no ports.go change requested. No schema/migration change — this node is pure Go-level hardening plus tests, entirely within internal/repocheckpoint (+ its own test files); internal/gitx was touched only by running its existing suite as a regression check, no gitx file was edited."
blockers: none
```

Phase 9 兩個節點全部完成後的最終驗證：`golangci-lint run ./...`（整個 repo）→ 0 issues；`go build ./...` → OK；`go vet ./...` → OK；`go test ./... -race` → 每個 package 皆為綠燈，零回歸。至此，`checkpoint` 角色曾被指派的所有 DAG 節點全數完成——Part A（a01-a09）與 Part B（b01-b09）皆已全部完成；依照 `EXECUTION_DAG.md`，本角色範疇內不再有其他節點。

---

## 修正性新增（Phase 12 之後，最終整合關卡）：真正的 `app.ProgressTreeService` adapter

這並非編號的 DAG 節點——如上方 Phase 9 條目所述，本角色所指派的整個 DAG 範疇（a01-a09、b01-b09）當時已全部完成。此條目記錄的是**最終整合關卡審查**（`contract-integrator-final`）中的一項發現，該發現被交辦給本角色處理，因為它正好落在本角色的專屬路徑（exclusive-path）工作範圍內，並非 lead 該直接實作的部分。

### 發現的問題

`internal/app/ports.go` 中已凍結的 `ProgressTreeService` 介面（7 個方法：`CreateTask`、`UpsertPlan`、`StartNode`、`CompleteNode`、`FailNode`、`Snapshot`、`Reconcile`）**在整個 repository 中都沒有任何具體的正式（production）實作**。在 a01-a09 的過程中，本角色已建置並徹底測試了該介面所需的每一個獨立元件——`NodeStore`、`EdgeStore`、`ArtifactStore`、節點狀態機（`ValidateTransition`／`IsTerminal`／`AllowedTransitions`）、`CompleteNode` 的原子協定，以及 `Reconciler`——但從未組裝出一個滿足這精確 7 方法合約的型別。在開始之前，已透過對整個 repo 執行 `grep -rn "var _ app.ProgressTreeService"` 確認：僅存在 `internal/testutil/fakes.FakeProgressTreeService`（一個測試替身）以及測試內臨時（ad hoc）的滿足方式。這正是為什麼 `cmd/auspex/main.go` 從未被接上真正服務的原因：組裝 app 的根（root）需要每一個已凍結 port 都有真正的實作，而這一個從未存在過。

### 實作內容

```yaml
node: checkpoint-corrective-01 (not a DAG node; Final-integration-gate finding)
status: completed
artifacts:
  - internal/progress/task_store.go   # NEW: minimal TaskStore CRUD over `tasks` (migrations/0004_tasks.sql) — grepped first; no other role owns task-row CRUD, and CreateTask is explicitly a ProgressTreeService responsibility per the frozen interface
  - internal/progress/service.go      # NEW: Service, the real app.ProgressTreeService implementation composing TaskStore/NodeStore/CompleteNode/Reconciler
  - internal/progress/service_test.go # NEW: compile-time interface assertion + integration-style delegation tests for all 7 methods
validation:
  - "gofmt -l internal/progress                    -> empty"
  - "go build ./...                                -> OK"
  - "go vet ./internal/progress/...                -> OK"
  - "go test ./internal/progress/... -race -v      -> PASS (full suite, zero regressions, including 15 new TestService_* tests)"
  - "go build ./... && go test ./... -race          -> green whole-repo, zero regressions"
  - "golangci-lint run ./...                        -> 0 issues"
commit: (recorded in the top-level commit for this addition)
next_action: none from this role — the lead (contract-integrator) wires Service into cmd/auspex/main.go / internal/app/wiring/** alongside the sibling GracefulPauseService adapter; both are out of this role's exclusive paths.
assumptions:
  - "This is composition and DTO-shape translation ONLY, per the finding's own framing — every piece of real logic (state transitions, atomicity, idempotency, crash recovery, dependency/parent-ordering checks) already existed and was already exhaustively tested; Service reimplements none of it. Each of the 7 methods is a thin adapter: CreateTask -> TaskStore.Insert; UpsertPlan -> NodeStore.ListByTask (for the current version) + TaskStore.SetActiveNodeAndVersion; StartNode -> NodeStore.Get + TransitionStatus + SetTimestamps; CompleteNode -> a direct, unmodified call to CompleteNode.Run (this package's own pre-existing atomic protocol type, distinct from the interface method of the same name) with a DTO translation at the boundary; FailNode -> NodeStore.Get + TransitionStatus to failed; Snapshot -> NodeStore.ListByTask; Reconcile -> Reconciler.Reconcile plus NodeStore.ListByTask to populate ReconciledNodes."
  - "TaskStore is new but narrowly scoped: just Insert/Get/SetActiveNodeAndVersion, enough for CreateTask and UpsertPlan's documented responsibility (0004_tasks.sql's own header comment: 'checkpoint's Progress Tree service is responsible for keeping [active_node_id] consistent with progress_nodes.id'). It does not own task lifecycle policy (status transitions beyond initial insert, auto_resume_enabled semantics) — out of scope for a composition-only fix."
  - "UpsertPlanRequest (frozen ports.go DTO) carries only a TaskID, not an actual node/edge plan payload — the frozen contract does not yet define a bulk-plan-upsert request shape. UpsertPlan therefore confirms the task exists and reports the tree's current version (NodeStore.ListByTask count, the same 'version = len(nodes)' convention statecheckpoint.Service.Create already established); a caller seeding actual nodes/edges alongside this call uses NodeStore.Insert/EdgeStore.Insert directly, exactly as this role's own a01-a09 tests already do. Widening the request DTO to accept a bulk plan body would require an ADR and contract-integrator's sign-off (Constitution §4/§3) — flagged here as a follow-up for contract-integrator to consider, not something this corrective addition unilaterally does."
  - "FailNode's frozen FailNodeRequest carries a FailureClass, but no migration in this role's owned 0020-0029 range added a failure_class column to progress_nodes — persisting it would need a new migration + likely an ADR discussion, out of scope for a composition-only fix. FailNode still performs the real, durable in_progress/ready/etc -> failed state transition the interface promises; FailureClass is validated as non-empty but not yet persisted as its own column. Documented here as a known, narrow gap for a future node to close, not a silently dropped contract term."
  - "app.ProgressTreeSnapshot (frozen DTO) carries only TaskID + Nodes today, not edges or artifacts — Snapshot returns exactly that shape; a caller needing edges/artifacts uses EdgeStore.ListByTask/ArtifactStore.ListByNode directly, which remain available as this package's own public API regardless of what the frozen Snapshot DTO currently carries."
  - "Reconcile's ReconciledNodes is populated as every node in the task NodeStore.ListByTask returns, since Part A's Reconciler.Reconcile (checkpoint-a04/a06/a09) operates task-scoped over the whole tree's durable state (staged-artifact-vs-DB crash windows + checkpoint integrity), not a subset — this is an honest DTO-shape translation of an already-task-scoped reconciliation pass, not new reconciliation logic."
  - "Required tests followed the brief exactly: a `var _ app.ProgressTreeService = (*Service)(nil)` compile-time assertion, plus integration-style tests proving each of the 7 methods delegates to (and does not diverge from) the underlying already-tested piece — e.g. TestService_CompleteNode_MatchesDirectCompleteNodeRun completes one node via Service.CompleteNode and a different node via CompleteNode.Run directly with equivalent inputs, then cross-checks the returned domain.StateCheckpoint against the durable row AND an independent statecheckpoint.Verify pass — not a from-scratch re-test of CompleteNode's own correctness (already proven exhaustively across a04-a09)."
  - "Did not touch internal/app/ports.go (frozen, contract-integrator-owned), go.mod/go.sum (foundation-owned), cmd/auspex/main.go, internal/app/wiring/**, or internal/pause/** (a sibling teammate's concurrent GracefulPauseService adapter work) — strictly within internal/progress/**, this role's own exclusive path."
blockers: none
```
