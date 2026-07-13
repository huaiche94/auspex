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
  pending per `docs/implementation/vertical-slice/foundation.md`). Scaffolding CI
  jobs against paths that don't exist would violate Constitution §7 rule
  10 (no abstractions a later milestone needs but this one doesn't) —
  flagged here so whichever future qa/CI node adds those trees also
  extends `ci.yml`, rather than this gap being silently forgotten.
- **`security.yml`, `provider-contract.yml`, `release.yml`**: named in
  ADD §30.3 but out of scope for qa-01 (whose validation target is
  narrowly "CI green on a trivial PR (Ubuntu/macOS/Windows)" per the
  execution DAG). Not created this wave.
- **Governance docs** (qa-08): `SECURITY.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `GOVERNANCE.md` at the repository root are
  grounded in `Auspex_ADD.md` §30.7 (Governance) and §30.8 (Security
  disclosure) verbatim where the ADD specifies concrete content, and
  cross-reference each other rather than duplicating normative text —
  see that node's log below for exact provenance of each file's
  content and the LICENSE/NOTICE gap it surfaces as a cross-owner
  defect.

## Node log

```yaml
node: qa-04
status: completed
artifacts:
  - internal/integrationtest/duplicate_outoforder_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run 'Duplicate|OutOfOrder' -v   # 5/5 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./... -race   # whole repo, all packages PASS, zero regressions"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <recorded below>
next_action: none — qa-04 was this wave's full qa assignment; STOP per task instruction; report findings to lead for routing
assumptions:
  - "Before writing any test, did a repo-wide investigation (grep for any
    file importing both internal/telemetry/claude and internal/progress,
    or pkg/protocol/v1 and internal/progress; grep for
    adapter/bridge/consumer/dispatcher; read internal/orchestrator/hooks.go,
    internal/app/wiring/wiring.go, internal/app/ports.go in full) to
    confirm exactly how (if at all) a persisted claude-provider v1.Event
    currently drives internal/progress.CompleteNode.Run in production
    code. Finding: it does not, anywhere. See the findings section below
    and this file's own package doc comment
    (internal/integrationtest/duplicate_outoforder_test.go) for the full
    evidence trail. This determined the test design: real components
    driven together as far as the frozen contract (v1.Event,
    CompleteNodeInput, CompleteNodeRequest) actually allows, plus a
    clearly-labeled TEST-ONLY `deriveCompleteNodeInput` glue function
    standing in for the missing production adapter — never added as
    production code, per agents/qa.md's 'do not alter feature production
    code' rule."
  - "Scenario 1 (duplicate provider event, end-to-end): real
    claudehooks.ParseStop + claudetelemetry.Normalizer.NormalizeStop against
    the real testdata/provider-events/claude/stop/normal.json fixture,
    persisted via the real claudetelemetry.EventStore into a REAL on-disk
    (temp-file, not :memory:) SQLite database that also holds
    checkpoint-a07's progress_nodes/node_completions/state_checkpoints
    tables (the same DB a real process would use) — not two separate
    in-memory fakes. Verified EventStore-layer dedup
    (CountByIdempotencyKey==1, GetByEventID unchanged, the redelivered
    event's own EventID never separately stored) AND, using the derived
    CompleteNodeInput, that a second completion attempt keyed off the same
    real event's IdempotencyKey replays (Replayed=true, same checkpoint ID)
    rather than erroring or double-completing. A second test
    (DifferentChannel_DifferentKey_SameEvidence_Replayed) repeats this
    using a real StopFailure fixture and an independently-chosen second
    key, exercising checkpoint-a07's evidence-digest-based (not
    key-based) duplicate detection specifically."
  - "Scenario 2 (out-of-order delivery, end-to-end): a REAL Stop fixture
    event, parsed/normalized/persisted through the real pipeline exactly as
    internal/orchestrator/hooks.go's HandleStop does in production, used
    (via the same derive helper) as the trigger for a CHILD node's
    completion while its PARENT node was deliberately left at `pending`
    (never transitioned to in_progress) — modeling the parent's own
    in-progress signal having been delayed/lost relative to the child's
    completion signal. Confirmed: rejection with domain.ErrCodeConflict,
    Retryable=true (matching checkpoint-a07's documented semantics exactly,
    not merely 'some error'); the real persisted provider event remains
    durably stored despite the rejected completion (proving the two
    integrity boundaries — event persistence and node completion — are
    correctly independent); the child node remains in_progress, not
    corrupted; and a retry of the identical input succeeds once the parent
    is (realistically) moved to in_progress, proving the rejection was
    genuinely about ordering and not some other defect in the derived
    input. A companion test independently verifies the EventStore layer's
    own documented ordering-agnostic behavior (store.go: 'no mutable
    current-state row... persists correctly either way') by persisting a
    real turn.completed event before a real turn.started event and
    confirming both land as independent, correctly stored rows — proving
    the storage layer's permissiveness and CompleteNode's strictness about
    ordering are two deliberately different, non-contradictory behaviors
    at two different layers."
  - "All fixtures are real: testdata/provider-events/claude/{stop,
    stopfailure,userpromptsubmit}/*.json, read directly off disk and run
    through the real claudehooks.Parse*/claudetelemetry.Normalizer
    pipeline — no hand-built v1.Event values anywhere in this file except
    where explicitly and separately confirming the storage layer's
    ordering-agnostic contract."
  - "Test-double patterns (fixedClock/seqIDs-style, openTestDB, seedTask,
    newDocumentNode, newCompleteNodeHarness, moveNodeToInProgress) are
    small duplicates of the exact same helpers internal/progress's own
    test suite and qa-05's leakage_scanner_test.go already established —
    both are unexported to their own test packages, so re-declaring the
    same minimal shape here (prefixed qa04* to avoid collisions with
    qa-05's own same-named helpers in this package) follows the same
    precedent those files' own doc comments already documented for this
    kind of cross-file duplication."
blockers: []
findings:
  - severity: P1
    title: "No production code path connects a persisted claude-provider v1.Event to internal/progress.CompleteNode.Run — the two components qa-04 was asked to integrate-test are wired together only inside this test file's own TEST-ONLY glue, not in production"
    file: "internal/orchestrator/hooks.go (HandleStop/HandleUserPromptSubmit/HandleStopFailure/HandleStatusLine normalize+persist and stop there — HandleStop's own doc comment: 'Full Progress Tree/Git/artifact reconciliation... is outcome labeling depth beyond this node's scope'); internal/telemetry/claude/normalizer.go (no producer ever assigns Event.TaskID or Event.ProgressNodeID — every event's envelope() helper sets only SessionID); internal/progress/complete_node.go's CompleteNodeInput and internal/app/ports.go's CompleteNodeRequest (both frozen to exactly {NodeID, IdempotencyKey, Artifacts[, RepositoryCheckpointID]} — no v1.Event/EventID/EventType field anywhere); internal/progress/node_store.go's Node.ProviderNodeID field (stored and read back, confirmed via grep, but no code anywhere looks a node up BY ProviderNodeID); internal/app/wiring/wiring.go (wires no bridge between internal/telemetry/claude and internal/progress; Services.ProgressTree is still just the bare frozen interface, unimplemented, per that package's own doc comment)."
    reproduction: "go test ./internal/integrationtest/... -run TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists -v — parses+normalizes a real Stop fixture and asserts ev.TaskID==\"\" and ev.ProgressNodeID==\"\" (both true today); combined with a repo-wide grep (documented in this file's own package doc comment) for any file importing both internal/telemetry/claude and internal/progress (zero matches) or pkg/protocol/v1 and internal/progress (zero matches), and for any adapter/bridge/consumer/dispatcher file (none exist)."
    expected_invariant: "Auspex_ADD.md's Progress Tree is meant to be driven forward by real provider observations (a provider.turn.completed signal is exactly the kind of real-world event that should be able to trigger a node's completion) — Constitution §6.1 ('Progress Tree is the canonical durable task state... never an agent's own claim of done') implies SOME real signal must be able to drive a real completion, not just a test harness hand-constructing a CompleteNodeInput. Today, nothing does: the event pipeline (claude-provider) and the completion pipeline (checkpoint/progress) are both individually correct and individually well-tested, but there is a genuine missing middle layer between them. This is exactly the kind of integration-only gap qa-04 was chartered to find, per its own task brief ('an event type or field mapping mismatch, a case where claude-provider's real events don't actually carry information checkpoint's ordering-check logic expects')."
    owning_role: "contract-integrator (a new cross-component port/field is needed on the frozen v1.Event/CompleteNodeRequest contract, or a documented decision that TaskID/ProgressNodeID resolution happens via a different, not-yet-built lookup path — Constitution §4.2 reserves pkg/protocol/v1/** and internal/app/ports.go exclusively to this role) in coordination with claude-provider (would need to populate TaskID/ProgressNodeID on produced events once a resolution mechanism exists) and checkpoint (whichever role builds the actual consumer/adapter). Not routed as P0: every individual component this gap spans is itself correct and passes its own tests, no existing invariant is violated, and vertical-slice's frozen DAG never assigned any node the explicit job of building this adapter this wave — it is a forward-looking integration gap this node exists to surface, not a regression."
  - severity: P2
    title: "checkpoint-a07's duplicate/out-of-order semantics hold correctly when driven by REAL claude-provider events end-to-end, not just hand-built CompleteNodeInput values — no defect found in either component's own logic"
    file: "internal/telemetry/claude/store.go (claude-provider-05); internal/progress/idempotency.go, internal/progress/complete_node.go (checkpoint-a07)"
    reproduction: "N/A — not a defect. go test ./internal/integrationtest/... -run 'Duplicate|OutOfOrder' -v (this node's own suite, 5/5 passing) is the closure evidence: TestDuplicateProviderEvent_EndToEnd_StoredOnceAndCompletionReplayed and TestDuplicateProviderEvent_DifferentChannel_DifferentKey_SameEvidence_Replayed prove the duplicate case; TestOutOfOrderDelivery_EndToEnd_ChildCompletionBeforeParentStarted_Rejected and TestOutOfOrderDelivery_EndToEnd_EventStoreAcceptsEitherArrivalOrder prove the out-of-order case, including the retry-succeeds-once-parent-starts positive path and the EventStore-layer/CompleteNode-layer non-contradiction."
    expected_invariant: "Both upstream nodes' own unit tests already proved their own logic correct in isolation; qa-04's own DAG row exists specifically to prove they remain correct when the actual field flowing between them (a real, deterministically-digested IdempotencyKey derived from a real fixture, not a string literal) is used. Recorded per agents/qa.md's instruction to record 'any findings, even non-blocking ones.'"
    owning_role: "qa (this node) — informational; no action needed from claude-provider or checkpoint for this specific behavior."
```

```yaml
node: qa-05
status: completed
artifacts:
  - internal/integrationtest/leakage_scanner_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run LeakageScanner -v   # 6/6 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./... -race   # whole repo, all 33 packages PASS, zero regressions from merging origin/main (Waves 4/5/6)"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <recorded below>
next_action: none — qa-05 was this wave's full qa assignment; STOP per task instruction; report findings to lead for routing
assumptions:
  - "Built internal/integrationtest as a brand-new package (did not exist
    before this node) since qa's exclusive paths include it and no other
    qa node had touched it yet. All test doubles (fixedClock, seqIDs,
    checkpointRepoBuilder) are small duplicates of the same pattern
    already established by internal/telemetry/claude/normalizer_test.go
    and internal/repocheckpoint/helpers_test.go — those are unexported to
    their own test packages, so re-declaring the same minimal shape here
    (rather than trying to import an internal test helper across package
    boundaries, which Go does not allow) follows the precedent those
    packages' own doc comments already established for this kind of
    cross-file duplication."
  - "Scenario 1 (SQLite export): drove the REAL internal/telemetry/claude
    Normalizer + EventStore against a REAL on-disk temp-file SQLite
    database (sqlite.Open with a real path, not ':memory:'), reusing the
    exact needle strings from claude-provider-07's own
    allRawTextFixtures table (fixture_suite_test.go) so this node checks
    the same known-sensitive strings that node already proved absent at
    unit-test scope — qa-05's job is proving the claim also holds for the
    real on-disk file bytes, not re-deriving new needles. After
    PersistAll, forced `PRAGMA wal_checkpoint(FULL)` then Close(), then
    read the raw .db file (and, defensively, -wal/-shm sidecar paths in
    case a future caller's checkpoint discipline differs) via os.ReadFile
    directly — not through StoredEvent or any typed query — satisfying
    the task brief's 'read the file directly ... not just via typed
    queries' instruction."
  - "Scenario 2 (repository checkpoint): drove the REAL
    internal/repocheckpoint.Capture (the same entry point
    internal/repocheckpoint/untracked_test.go itself calls, not a mock)
    against a scratch Git repo with one untracked secret-shaped file
    (GitHub-token pattern) and one untracked prompt-adjacent free-text
    file, producing a real on-disk manifest.json/summary.md/patch.gz
    pair/untracked.zip under a temp ArtifactsRoot. Scanned every artifact
    type: decompressed gzip patches, zip entries (decompressed), and
    plain files (manifest.json/summary.md/skipped-files.json)."
  - "Reused internal/redact's exported Scan API (ScanContent for
    in-memory buffers already decompressed/extracted from an archive,
    ScanPath for the falsifiability check's standalone file) rather than
    reimplementing any pattern matching, per this node's explicit
    instruction to treat internal/redact as a read-only dependency."
  - "Falsifiability/negative-control test
    (TestLeakageScanner_Falsifiability_DetectsPlantedSecretInRawFile):
    planted a known sk-ant-... secret and a known prompt needle into a
    raw file written directly (bypassing the real pipeline, per the task
    brief's explicit instruction), then asserted both scanBytesForSecrets
    and scanBytesForNeedles (the exact functions every other test in this
    file relies on) detect it, plus independently asserted
    redact.ScanPath also matches. This is the proof the 'zero findings'
    result from the happy-path tests is meaningful rather than the
    scanner vacuously passing because it never actually checks anything."
  - "Sanity-checked (via a throwaway debug test, removed before the final
    commit) that the real DB export file is a substantive ~236 KiB
    SQLite file (not empty/near-empty) and that the checkpoint scenario
    produces all six expected artifact files including a non-trivial
    untracked.zip (253 bytes, containing scratch-notes.txt but excluding
    the secret-shaped file) and skipped-files.json (75 bytes, recording
    the skip) - confirming the scan targets are real, populated artifacts
    and not accidentally-empty stand-ins."
blockers: []
findings:
  - severity: P1
    title: "Secret-shaped content in a TRACKED file's staged/unstaged diff is never filtered by internal/redact — only the UNTRACKED-file archive is scanned"
    file: "internal/repocheckpoint/capture.go (Capture calls gitClient.DiffPatch for staged/unstaged content with no redact.Scan* call anywhere in that path); internal/repocheckpoint/untracked.go's buildUntrackedArchive is the ONLY call site that invokes internal/redact"
    reproduction: "go test ./internal/integrationtest/... -run TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered -v — stages a tracked file whose new content is a GitHub-token-shaped string (`ghp_...` 38-char suffix), runs a real repocheckpoint.Capture, then scans the resulting staged.patch.gz directly; the secret-shaped string is found verbatim, unfiltered, confirmed by the github_token detector firing on the decompressed patch bytes."
    expected_invariant: "Auspex_ADD.md §19.5/§27.8's secret-scan default policy and Constitution §7 rule 2 (raw prompts and sensitive content are not persisted/exposed by default) read most naturally as applying to everything a Repository Checkpoint captures, not only the untracked-file archive. A secret pasted into a tracked config file and staged (a completely ordinary accidental-commit scenario) currently survives, unredacted, in every checkpoint artifact from that point forward."
    owning_role: "checkpoint (Part B / repocheckpoint) — note this is NOT a new discovery: internal/repocheckpoint/untracked_test.go's own TestCapture_Untracked_SecretScan_NeverAppliesToTrackedDiffContent already documents this exact boundary as a deliberate, acknowledged scope decision by checkpoint-b06, explicitly naming 'a future qa-05-style scan of patch content' as the follow-up. This qa-05 finding is that follow-up: independently re-confirming the gap is real at the integration layer (not just asserted in a unit test's comment) and formally routing it for a scope decision — either an ADR-backed accepted risk (patches are diff-of-tracked-content, a different problem from untracked-file capture) or a follow-up node extending internal/redact's scan to patch content before it is written to staged.patch.gz/unstaged.patch.gz. Not treated as P0 here because it is a pre-existing, already-documented, already-shipped scope boundary, not a new regression, and because ADD §19.5's secret-scan bullet appears under the untracked-file-archive section specifically, not the patch-capture section — so it is plausibly in-scope-as-designed rather than a broken invariant. contract-integrator should confirm which reading is correct; until then this is P1 (must resolve before demo, since 'checkpoint before a high-risk turn' is exactly the scenario where a user's tracked-file secret could realistically be present)."
  - severity: P2
    title: "claude-provider-07's own privacy gate correctly scoped itself to package-level unit-test access; qa-05 is the first node to validate the real on-disk SQLite file/WAL and a real Repository Checkpoint artifact directory end-to-end for raw-prompt/secret leakage — no gap found, but this closes a previously-open scope item, recorded here for traceability rather than as a defect."
    file: "internal/telemetry/claude/fixture_suite_test.go (claude-provider-07); internal/redact/doc.go (checkpoint-b06)"
    reproduction: "N/A — not a defect. go test ./internal/integrationtest/... -run LeakageScanner -v (this node's own suite) is the closure evidence."
    expected_invariant: "Both upstream nodes' own documentation named this exact gap and named qa-05 as the closing node; recorded per agents/qa.md's instruction to record 'any findings, even non-blocking ones.'"
    owning_role: "qa (this node) — informational, no action needed from another role."
```

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
  - "task build   # go build -o bin/auspex ./cmd/auspex -> succeeds"
  - "task test   # go test -race ./... -> all 18 packages PASS (14 with tests, 4 no-test-files packages)"
  - "task test:short   # go test ./... (no -race, the Windows-job path) -> all PASS"
commit: c523650
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
    foundation-09 used locally per docs/implementation/vertical-slice/foundation.md).
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

```yaml
node: qa-08
status: completed
artifacts:
  - SECURITY.md
  - CONTRIBUTING.md
  - CODE_OF_CONDUCT.md
  - GOVERNANCE.md
validation:
  - "test -s SECURITY.md && test -s CONTRIBUTING.md && test -s CODE_OF_CONDUCT.md && test -s GOVERNANCE.md   # all four exist and are non-empty (113/136/140/124 lines respectively)"
  - "manual doc review against Auspex_ADD.md §30.7 (Governance) and §30.8 (Security disclosure), and against README.md's existing 'Contributing' section and Tech stack table, for contradictions -> none found"
  - "grep -rn \"CLA\" --include=*.md .   # only CONTRIBUTING.md/GOVERNANCE.md/Auspex_ADD.md mention it, all consistent ('no CLA')"
commit: a4ab0b2
next_action: none — qa-01 and qa-08 were this wave's full qa assignment; STOP per task instruction
assumptions:
  - "SECURITY.md's disclosure channel is a private GitHub Security
    Advisory with a 3-business-day acknowledgement target, verbatim from
    ADD §30.8 ('Private GitHub Security Advisory; ack target 3 business
    days') - no alternate channel (e.g. a security@ email alias) is
    invented since the ADD names exactly one mechanism and Constitution
    §1 treats the ADD as sole source of truth for this content."
  - "SECURITY.md also enumerates the qa.md role packet's own 'Security
    assertions' list (loopback/API auth, prompt text absent by default,
    bearer token/API key redaction, hook payload size limits, restrictive
    SQLite/artifact permissions, argv-only external commands, extraction
    path-traversal safety, opt-in auto-resume) as the project's current
    security posture / what a report should test against, since qa owns
    both this document and that assertion list, and a security policy
    that doesn't name what's actually verified would be thinner than the
    project's own existing internal bar."
  - "CONTRIBUTING.md requires DCO sign-off (`git commit -s`) and states
    'no CLA' per ADD §30.7 verbatim ('DCO sign-off; no CLA initially').
    It also requires reading CONSTITUTION.md, Auspex_ADD.md, and
    AGENTS.md before proposing changes and describes the
    milestone-gating rule, both copied from README.md's existing
    'Contributing' section (not contradicted, only expanded with the
    concrete local-task commands from ADD §30.2 and the DCO/license
    requirement README.md itself doesn't state) - README.md's own
    'Contributing' section was read first specifically to avoid
    introducing a second, divergent contribution process."
  - "CONTRIBUTING.md's local task list (`task fmt`, `task lint`, `task
    test`, `task build`) is the actually-existing Taskfile.yml subset of
    ADD §30.2's full list (`task bootstrap`, `task test:race`, `task
    test:e2e`, `task vscode:test`, `task research:test`, `task verify`
    are ADD-specified future targets that do not exist in Taskfile.yml
    yet - `vscode/` and `research/` trees don't exist yet either per
    README.md's repository layout). Documenting only the targets that
    actually run today avoids telling a new contributor to run a command
    that will fail; the not-yet-existing ADD-named targets are
    acknowledged in a footnote rather than silently omitted, so
    CONTRIBUTING.md doesn't quietly contradict ADD §30.2's eventual full
    list."
  - "License stated as Apache-2.0 in CONTRIBUTING.md/GOVERNANCE.md,
    matching README.md's Tech stack table ('License: Apache-2.0') - no
    LICENSE file exists in the repository root yet
    (docs/implementation/vertical-slice/foundation.md's foundation-09 log flags
    this as a known, not-yet-assigned gap: 'No LICENSE or NOTICE file was
    added even though both are named in foundation's exclusive-paths
    list'). LICENSE/NOTICE are foundation-owned paths per agents/qa.md's
    own exclusive-paths list (LICENSE/NOTICE are not in it), so creating
    them is out of scope here; CONTRIBUTING.md/GOVERNANCE.md state the
    license by name (consistent with README.md) without asserting the
    LICENSE file itself exists, and this gap is re-flagged below for
    contract-integrator/foundation."
  - "CODE_OF_CONDUCT.md is the Contributor Covenant v2.1 verbatim
    (standard, freely reusable, widely adopted text), per this node's
    instruction to adapt a standard well-known CoC since the ADD
    specifies no custom content for this file. Only the enforcement
    contact was filled in, pointed at the same private GitHub Security
    Advisory / repository-owner-contact channel SECURITY.md uses, since
    no separate conduct-specific contact (e.g. a conduct@ email) is
    named anywhere in the ADD or README and inventing one would be an
    unverifiable, likely-dead contact address."
  - "GOVERNANCE.md documents both maturity stages from ADD §30.7
    verbatim: Initial ('lead maintainer + public ADR/issue process') and
    Mature ('>=3 active maintainers; sensitive security/provider changes
    2 approvals; documented release authority; DCO sign-off; no CLA
    initially'), explicitly stated as the *current* stage being Initial
    (matching the repository's actual state: one lead, no maintainer
    team yet) and Mature as the documented future bar, not a
    currently-true claim. It also documents the ADR process from
    CONSTITUTION.md §3 (only contract-integrator accepts an ADR; any role
    may propose one) since governance-of-the-repository and
    governance-of-architecture-decisions are the same subject from a
    contributor's perspective and splitting them across two documents a
    reader has to cross-reference would be worse than one document
    citing CONSTITUTION.md as the source of truth for ADR mechanics
    (avoiding duplicating CONSTITUTION.md's normative text verbatim,
    which would risk drift - CONSTITUTION.md itself is supreme per its
    own §title and only contract-integrator may amend it)."
  - "ADD §30.9 (Privacy governance - changes to raw prompt retention,
    outbound telemetry, auto-resume default, state artifact content, or
    remote checkpoint require privacy review + ADR + changelog) is
    included in GOVERNANCE.md as a named special-review category distinct
    from ordinary sensitive-change review, verbatim from the ADD, since
    it is explicitly a governance rule (who must approve what kind of
    change) rather than a security-disclosure or contribution-process
    rule, so it belongs in GOVERNANCE.md rather than SECURITY.md or
    CONTRIBUTING.md."
blockers:
  - "LICENSE/NOTICE files (ADD §30.1's 'Required files' list) do not yet
    exist in the repository root. Not a qa-08 blocker (out of qa's
    exclusive paths entirely - foundation owns them per its own
    exclusive-paths list), but CONTRIBUTING.md/GOVERNANCE.md both name
    Apache-2.0 as the project license consistent with README.md, and a
    contributor who goes looking for the actual LICENSE file will not
    find one yet. Filed here as a cross-owner defect for
    foundation/contract-integrator per Constitution §4.4 (a role that
    needs a change to a file it doesn't own requests it through its
    progress artifact rather than making the edit itself) - this is a
    re-flag of the same gap foundation-09 already surfaced in its own
    progress artifact, not a new discovery."
```

```yaml
node: qa-05-followup
status: completed (test updated; not yet passable on this branch — see note)
artifacts:
  - internal/integrationtest/leakage_scanner_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -v   # 9/10 PASS, 1 EXPECTED FAIL (see below)"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: <recorded below>
next_action: none — this is a corrective task, not a new DAG node; STOP once committed. Re-validation of the updated test to an actual PASS happens once the lead merges vertical-slice/checkpoint into vertical-slice/qa; not this node's job to force that merge.
assumptions:
  - "checkpoint independently fixed this wave's qa-05 P1 finding
    ('Secret-shaped content in a TRACKED file's staged/unstaged diff is
    never filtered by internal/redact') via vertical-slice/checkpoint commit
    f981bde ('checkpoint: extend secret scanning to tracked-file diff
    content (fixes qa-05 P1 finding)'), adding
    internal/repocheckpoint/patchredact.go and wiring it into Capture
    (capture.go) right after the staged/unstaged DiffPatch calls, before
    archiving. Verified this independently by reading both files via
    `git show vertical-slice/checkpoint:internal/repocheckpoint/patchredact.go` and
    `git show vertical-slice/checkpoint:internal/repocheckpoint/capture.go`
    read-only (vertical-slice/checkpoint was never merged or checked out into this
    worktree; internal/repocheckpoint/** remains checkpoint's exclusive
    path, untouched here) — did not just trust the lead's claim."
  - "patchredact.go's redactPatchSecrets scans only '+'/'-'-prefixed line
    bodies of the staged/unstaged patch (explicitly excluding '+++'/'---'
    file-header lines, '@@ ... @@' hunk headers, and all context lines)
    using internal/redact.ScanContent, and on a match replaces the ENTIRE
    line body with a fixed, non-echoing placeholder constant:
    `redactedLinePlaceholder = \"[REDACTED: secret-shaped content removed
    by auspex checkpoint capture]\"`. Line prefix byte and trailing
    terminator are preserved. This was a deliberate redact-in-place
    design choice (over skip-with-manifest-annotation) specifically so
    checkpoint-b08's restore-dry-run (`git apply --check`) keeps working
    against the rest of the patch."
  - "Renamed TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered
    to TestLeakageScanner_SecretInTrackedFileDiff_NowFiltered, since the
    documented gap is no longer an accepted/known gap — it's now a
    confirmed-fixed invariant this test guards going forward. Flipped the
    assertion: the test now asserts (a) scanBytesForSecrets finds nothing
    in staged.patch.gz, (b) the raw secret string is not a verbatim
    substring of the patch either (belt-and-suspenders vs. the scanner
    itself), and (c) patchredact.go's exact redaction placeholder string
    IS present in the patch in the secret's place — a precise positive
    assertion, not just an absence check, confirming redact-in-place
    happened as designed rather than e.g. the whole patch/line being
    dropped some other way. Also added a lightweight assertPatchApplies
    helper that clones the scratch repo, writes the (decompressed)
    redacted patch to a file, and runs `git apply --check` against it,
    confirming redaction did not corrupt the patch's applicability — a
    sanity check only, not a re-test of checkpoint-b08's own
    restore-dry-run logic, which remains out of this node's scope."
  - "This test CANNOT pass on vertical-slice/qa alone right now, and that is
    expected, not a regression: internal/repocheckpoint/patchredact.go
    does not exist on this branch until the lead integrates
    vertical-slice/checkpoint into vertical-slice/qa (or both into main). Ran the full
    updated test locally to confirm: it fails with exactly 'secret-shaped
    content leaked into staged.patch.gz unredacted' (the github_token
    detector still fires, since this branch's Capture has no redaction
    step yet) — i.e., the new test correctly still detects the
    old/pre-fix behavior on this branch, and will flip to PASS once
    checkpoint's fix is actually present. All 9 other tests in
    internal/integrationtest (the 5 qa-04 duplicate/out-of-order tests
    plus the other 4 qa-05 leakage-scanner tests) pass unaffected."
  - "Did not touch internal/repocheckpoint/** or merge vertical-slice/checkpoint
    into vertical-slice/qa, per this task's explicit constraint — checkpoint's
    branch was inspected read-only via `git show
    vertical-slice/checkpoint:<path>` only."
blockers: []
findings:
  - severity: informational
    title: "qa-05 P1 finding ('secret-shaped content in a TRACKED file's staged/unstaged diff is never filtered') is now fixed upstream by checkpoint (vertical-slice/checkpoint@f981bde, internal/repocheckpoint/patchredact.go) — this node's test updated to assert the corrected behavior; re-validated for real once the lead's integration merges vertical-slice/checkpoint and vertical-slice/qa together."
    file: "internal/integrationtest/leakage_scanner_test.go (this node); internal/repocheckpoint/patchredact.go, internal/repocheckpoint/capture.go (checkpoint, read-only reference only)"
    reproduction: "go test ./internal/integrationtest/... -run TestLeakageScanner_SecretInTrackedFileDiff_NowFiltered -v — on vertical-slice/qa alone (checkpoint's fix absent) this currently fails as expected with 'secret-shaped content leaked into staged.patch.gz unredacted'; once vertical-slice/checkpoint@f981bde is integrated, the same command is expected to PASS, asserting no raw secret survives in staged.patch.gz, checkpoint's exact redaction placeholder string is present instead, and the redacted patch remains git-apply-able."
    expected_invariant: "Once integrated, no secret-shaped content staged/unstaged into a tracked file survives unredacted into a Repository Checkpoint's patch artifacts, and the redacted patch remains structurally valid (git-apply-able) — closing this wave's qa-05 P1 finding."
    owning_role: "qa (this node) for the test; checkpoint (already delivered, per f981bde) for the fix; lead for final integration and re-validation."
```

## Wave (Stage 4 completion) — qa-02, qa-03, qa-06, qa-07, qa-09

This wave assigned qa its ENTIRE remaining DAG scope: qa-02 (the vertical-slice
demo), qa-03 (restart-same-DB, multi-role), qa-06 (independent malicious
fixtures), qa-07 (scheduler double-worker race, integration scope), and
qa-09 (this final report). Merged `origin/main` first (fast-forward,
clean) — Waves 8-11 integrated, meaning claude-provider, checkpoint,
predictor, and runtime had ALL completed their entire DAG scope by the
time this wave began, so every dependency qa-02 named ("ALL NOW
INTEGRATED") was genuinely real, nothing needed to be faked. Each node
below was validated and committed independently, per the explicit task
instruction — no batching.

```yaml
node: qa-02
status: completed
artifacts:
  - internal/integrationtest/e2e_highrisk_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run E2EHighRisk -v   # 1/1 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: abad4d9
next_action: qa-03 (this wave's next assigned node)
assumptions:
  - "Designed ONE coherent 'risky turn' narrative rather than six
    disconnected sub-tests, per the task brief's explicit preference: a
    single simulated session whose status-line shows real quota/context
    pressure (testdata/provider-events/claude/statusline/high_usage.json —
    98.85% context used, 97.3% five-hour quota used) plausibly motivates
    the SAME session's next prompt being evaluated as high-risk, needing a
    checkpoint, a one-time allow, a Stop outcome, and then (since the
    quota pressure never actually improved) a Graceful Pause with full
    wake recovery — six steps that tell one story, not six independent
    fixtures stapled together."
  - "Step 2 (prompt auspex block) reuses runtime-b06's own documented
    technique (internal/orchestrator/decision_realauth_test.go's
    newHighRiskDataSource) for reliably driving the REAL predictor
    pipeline (scope/token/quota/risk/policy, all real) into the critical
    risk band: large changed-file/line quantiles plus every completion/
    blast-radius flag internal/predictor/risk/combiner.go actually reads
    (security-sensitive, migration-likely, cross-layer, open-ended scope).
    Verified this lands on PolicyCheckpointAndRun (as it did in this run)
    or PolicyRequireConfirmation, either accepted as 'high-risk enough' per
    that same precedent's own test."
  - "Step 3 uses BOTH of checkpoint's real completion paths, not just one:
    (a) internal/progress.CompleteNode for a real Progress Tree node
    (Constitution Sec6.3's atomic node-completion + State Checkpoint), and
    (b) a real orchestrator.CheckpointCreate call (statecheckpoint.Service
    + repocheckpoint.Service against a real scratch Git repo) for the
    STANDALONE current-state checkpoint a PolicyCheckpointAndRun decision
    actually requires immediately before allowing the turn to proceed —
    these are two deliberately different checkpoint entry points
    (statecheckpoint's own package doc comment: Create is 'a STANDALONE,
    ad hoc snapshot entry point,' not a wrapper around CompleteNode's
    path), and this scenario exercises both for real rather than
    conflating them."
  - "No production ProgressTreeService/GracefulPauseService adapter exists
    yet (confirmed via the same grep runtime-b10's own restart_test.go doc
    comment already documents: only internal/testutil/fakes implements
    either full port) — this scenario therefore drives Progress Tree node
    completion via the real internal/progress.CompleteNode directly (the
    same, real, already-integrated component qa-04's own test file
    exercises) and the pause lifecycle via the real internal/pause free
    functions directly (Apply/CompareAndSwapStatus/InterruptAndSleep/Wake/
    ValidateResume/Resume, the same technique
    internal/pause/fulllifecycle_test.go's own runFullLifecycleToSleeping
    helper uses), rather than waiting on a port adapter that is not this
    wave's scope to build. This is real production code throughout, not a
    fake standing in for a gap — it is simply reached one layer below the
    not-yet-existing unified port, exactly as runtime-b10's own restart
    test does for the same two services."
  - "The one-time allow flow (step 4) and the resume flow (step 6) both
    issue and consume a REAL, storage-backed Authorization via the SAME
    real evaluation.Service and orchestrator.DecisionAllowCmd runtime-b06
    built — replay-rejected proven twice in this file (once for the
    original turn's authorization, implicitly proven again by the resume
    flow's own fresh issue-then-ValidateResume-consume sequence)."
  - "Pause/wake recovery (step 6) uses the REAL SQLite-backed
    pause.SQLiteStore (runtime-b10), not pause.NewMemStore() — this is
    the same durability guarantee qa-03 separately stress-tests across an
    actual restart; this node's own point is proving the FULL lifecycle
    composes in one realistic flow, which it does."
blockers: []
findings:
  - severity: informational
    title: "The vertical-slice demo composes cleanly end to end across every real
      role's work — no defect found in this pass."
    file: "internal/integrationtest/e2e_highrisk_test.go"
    reproduction: "N/A — not a defect. go test ./internal/integrationtest/... -run E2EHighRisk -v is the closure evidence."
    expected_invariant: "The literal vertical-slice demo scenario (status-line -> auspex block -> checkpoint -> one-time allow -> Stop -> pause/wake recovery) works end-to-end against real implementations throughout."
    owning_role: "qa (this node) — informational; no action needed from another role."
```

```yaml
node: qa-03
status: completed
artifacts:
  - internal/integrationtest/restart_sameDB_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run RestartSameDB -v   # 1/1 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: a1d376a
next_action: qa-06 (this wave's next assigned node)
assumptions:
  - "Built on runtime-b10's own in-process-restart-same-SQLite-file
    technique (internal/app/wiring/restart_test.go: open a real on-disk,
    temp-file-not-:memory: DB; drive real work through it; discard EVERY
    in-process Go value including the *sqlite.DB itself; open a BRAND NEW
    *sqlite.DB against the SAME file path; re-migrate and confirm
    idempotence; prove state survived AND remains writable) rather than
    duplicating it from scratch, per the task's explicit instruction —
    but scoped this node's OWN independence at the fixture level: distinct
    task/session/worktree/repo IDs, a dedicated qa03-prefixed ID
    generator and Git scratch repo, and a distinct low-risk DataSource
    literal, so this is a genuinely separate exercise of the guarantee."
  - "This node's INCREMENT over runtime-b10's own proof (which already
    covers claude-provider-04's normalizer output indirectly via hook
    commands, checkpoint's State/Repository services, predictor's
    evaluation service, and this role's own pause/scheduler stores,
    driven through the wiring.App/cobra CLI layer) is proving the SAME
    multi-role coexistence WITHOUT the wiring/CLI layer at all — every
    role's own real constructor (claudetelemetry.NewEventStore,
    progress.NewNodeStore/CompleteNode, statecheckpoint.NewService,
    repocheckpoint.NewService, evaluation.New, pause.NewSQLiteStore,
    scheduler.NewStore) called directly against the same shared file,
    confirming the multi-role-coexistence guarantee does not depend on
    wiring.App's own specific composition order or any CLI-layer behavior
    — a genuinely different (lower) integration layer than runtime-b10's
    own top-to-bottom command-driven proof."
  - "Verified BOTH halves of restart-safety for every role's storage: (a)
    pre-restart state is READABLE post-restart through a brand-new
    Service/Store instance (event GetByEventID, node Get, state
    checkpoint Snapshot+Verify, repository checkpoint Verify, pause
    GetByID, wake job Claim-of-the-pre-existing-job), and (b) the write
    path is fully LIVE post-restart, not just reads of old rows (a fresh
    CheckpointCreate, a fresh EvaluateTurn/Decide/IssueAuthorization/
    ConsumeAuthorization cycle, a fresh RequestPause+Schedule+Claim) —
    proving no orphaned lock survives the restart on any of the five
    roles' own tables sharing this one file."
  - "The Authorization issued pre-restart (issued, not yet consumed,
    before the restart in this scenario) has its exactly-once guarantee
    (predictor-10's hardened ConsumeAuthorization) explicitly proven
    DURABLE across the restart: consumed successfully post-restart via a
    brand-new *evaluation.Service instance, then a replay attempt against
    the SAME authorization (still post-restart) is rejected — proving the
    exactly-once state itself (not just the service instance enforcing
    it) survived the restart."
blockers: []
findings:
  - severity: informational
    title: "Multi-role state coexistence across a restart holds cleanly —
      no defect found in this pass."
    file: "internal/integrationtest/restart_sameDB_test.go"
    reproduction: "N/A — not a defect. go test ./internal/integrationtest/... -run RestartSameDB -v is the closure evidence."
    expected_invariant: "claude-provider's events, checkpoint's Progress Tree/State/Repository checkpoints, predictor's evaluations/authorizations, and runtime's pause/scheduler records all survive a genuine process restart when they coexist in the same SQLite file, and every role's real service can still both read old state and write fresh state afterward."
    owning_role: "qa (this node) — informational; no action needed from another role."
```

```yaml
node: qa-06
status: completed
artifacts:
  - internal/integrationtest/malicious_fixture_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run 'PathTraversal|Symlink|MaliciousFixture' -v   # 3/3 PASS (one with 2 sub-tests)"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: 4d81590
next_action: qa-07 (this wave's next assigned node)
assumptions:
  - "Read checkpoint-b09's own final report in docs/implementation/vertical-slice/checkpoint.md
    in full before writing anything, to identify EXACTLY what that node's
    own adversarial audit already covered (its own genuine finding: Verify
    joined manifest.Artifacts[].Path directly onto ArtifactRoot with no
    traversal/symlink guard at all, fixed via a new safeArtifactPath check;
    plus two proactive hardenings: writeArtifactDir's files-map-key
    validation, and Capture's own CheckpointID validation) so this node's
    own fixtures target genuinely different attack shapes rather than
    re-deriving the same three findings under a new file name."
  - "Every scenario in this file calls ONLY the real, frozen
    app.RepositoryCheckpointService port (repocheckpoint.NewService) —
    never internal/repocheckpoint's own package-internal free functions
    (Capture/Verify/RestoreDryRun) directly, which is exactly what
    checkpoint-b09's OWN adversarial tests call (white-box, package
    repocheckpoint/repocheckpoint_test). This is the genuinely different,
    external, black-box vantage point the task brief asked for: the same
    surface a real caller (runtime's orchestrator.CheckpointCreate, or
    qa-02's own E2E scenario) actually sees."
  - "Scenario 1 (chained double-symlink escape): a two-hop symlink chain
    (evidence.txt -> sub/hop1 -> an outside secret file), as opposed to
    checkpoint-b09's own single-hop symlink and symlinked-PARENT-directory
    cases (both already covered by that node's own suite) — confirmed the
    escape never appears in the archive AND a legitimate untracked sibling
    file DOES appear, proving this is a real, working capture rather than
    a vacuous 'everything got skipped' pass."
  - "Scenario 2 (tampered-manifest path traversal): targets the
    staged.patch.gz artifact specifically, not the untracked.zip entry
    checkpoint-b09's own regression test (TestVerify_ManifestArtifactPathTraversal_Rejected)
    targets — proving the safeArtifactPath fix generalizes across artifact
    KINDS, not merely the one entry b09 happened to test. Additionally
    drives it through the real Service.Restore port (dry-run), one layer
    further than b09's own Verify-only regression test, confirming the fix
    holds through the full capture -> archive -> verify -> restore-dry-run
    pipeline as this node's own task brief explicitly names. Confirmed
    Restore returns ErrCodeConflict with a traversal-specific problem in
    Details, and the secret file's content never leaks into either
    Details or Message."
  - "Scenario 3 (malicious CheckpointID): reached through a MALICIOUS
    domain.IDGenerator via the real Service.Create seam (modeling a
    compromised/buggy ID-generation dependency), rather than
    checkpoint-b09's own test, which hand-supplies a literal CheckpointID
    string directly to the free Capture function — a different attack
    surface (a caller-supplied dependency misbehaving, vs. a
    directly-malicious literal argument). Two distinct traversal shapes
    tried (a './..'-prefixed nested traversal, and a nested traversal
    buried after an ordinary-looking prefix segment), neither identical to
    b09's own two cases ('../../escape-checkpoint-id' and a bare absolute
    path)."
  - "No new defect found — every scenario confirms checkpoint-b09's fixes
    hold from this external vantage point and against these independently-
    designed attack shapes. This is the expected, successful outcome of an
    independent-verification node, not a rubber stamp: three genuinely
    different attack constructions were built and run, not merely
    re-imported."
blockers: []
findings:
  - severity: informational
    title: "checkpoint-b09's path-traversal/symlink fixes hold from an
      independent, external, black-box vantage point using genuinely
      different attack fixtures — no new defect found."
    file: "internal/integrationtest/malicious_fixture_test.go; internal/repocheckpoint/security.go, verify.go, capture.go, atomicwrite.go (checkpoint, read-only reference only)"
    reproduction: "N/A — not a defect. go test ./internal/integrationtest/... -run 'PathTraversal|Symlink|MaliciousFixture' -v (3/3 passing) is the closure evidence."
    expected_invariant: "Path traversal, symlink escape, and malicious-input guards in the repository checkpoint pipeline hold when exercised independently, through the real service port, with fixtures qa itself designed rather than reused from checkpoint's own suite."
    owning_role: "qa (this node) — informational; no action needed from another role."
```

```yaml
node: qa-07
status: completed
artifacts:
  - internal/integrationtest/scheduler_doubleworker_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run DoubleWorkerRace -v   # 2/2 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go test ./internal/integrationtest/... -run DoubleWorkerRace -race -count=20   # PASS, stable, ~90s, no flakiness across 20 outer repetitions (each containing its own internal 20-attempt loop for the single-job scenario — effectively 400 race trials for that scenario alone)"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: 09b2be8
next_action: qa-09 (this wave's final node — the report you are reading)
assumptions:
  - "The DAG's own validation command for this row
    (`go test ./internal/scheduler/... -run DoubleWorkerRace -race
    -count=20`) targets internal/scheduler/..., which is runtime's
    EXCLUSIVE path, not qa's — qa cannot edit anything under
    internal/scheduler/** or internal/pause/** (agents/qa.md's own
    exclusive-paths list does not include either). Per this wave's
    explicit routing instruction, built an INDEPENDENT test in
    internal/integrationtest instead, calling only runtime's real,
    already-exported APIs (scheduler.NewStore/Schedule/Claim/Get,
    pause.NewSQLiteStore, pause.Wake) — zero edits to either excluded
    package."
  - "runtime-a09 already proved this exact race at the package level twice
    over: internal/scheduler/lease_test.go's own
    TestLease_ConcurrentWorkersYieldOneClaim (real on-disk SQLite, but
    scheduler-layer only) and internal/pause/wake_test.go's own
    TestDuplicateWake_WorkersYieldOneResume (state-machine layer only,
    explicitly against pause.NewMemStore() by that file's own documented
    design choice, not the real SQLite-backed store). This node's genuine
    increment is COMPOSING both real, on-disk-SQLite-backed layers into
    ONE race: N workers each attempt the full realistic sequence a
    production scheduler worker performs — Claim, then (only if won)
    Wake against the claimed job's own real PauseID — proving the SEAM
    between two independently-proven exactly-once guarantees introduces
    no new race window, a question neither upstream test actually answers
    on its own."
  - "Extended to a second scenario (many independent jobs raced by many
    workers concurrently, mirroring lease_test.go's own
    TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce pattern but
    with the Wake step composed in), confirming the per-job/per-pause
    exactly-once guarantee holds under CROSS-contention (workers racing
    for ANY of several due jobs, not just one shared job), not merely as
    an artifact of a single point of contention."
  - "Per the DAG's own risk callout ('flaky-by-nature; needs repeated
    runs'), ran BOTH an internal repeated-race loop (an explicit 20-
    attempt loop inside the single-job test, mirroring wake_test.go's own
    documented 'qa-07's own -count=20 repeated-race discipline' 50-attempt
    precedent) AND the outer `go test -count=20` the DAG names literally
    — stable across all repetitions, no flakiness observed."
  - "Every worker goroutine's panic is caught and reported explicitly
    (recover() + t.Errorf, not a silent goroutine crash that could hang
    wg.Wait() forever or mask a real failure as a test timeout instead of
    an actionable error)."
blockers: []
findings:
  - severity: informational
    title: "The composed scheduler-claim + pause-wake double-worker race
      holds correctly — no new race window at the seam between the two
      independently-proven exactly-once guarantees."
    file: "internal/integrationtest/scheduler_doubleworker_test.go; internal/scheduler/lease.go, internal/pause/wake.go (runtime, read-only reference only)"
    reproduction: "N/A — not a defect. go test ./internal/integrationtest/... -run DoubleWorkerRace -race -count=20 (stable across 20 repetitions) is the closure evidence."
    expected_invariant: "Exactly one worker's full Claim-then-Wake sequence succeeds per due job, under concurrent contention, whether racing for a single shared job or many independent jobs at once, using the real on-disk SQLite-backed stores for both the scheduler and pause layers together."
    owning_role: "qa (this node) — informational; no action needed from another role."
```

## qa-09: Final report (severity-ranked, full role scope)

This is qa's final assigned DAG node. Per `agents/qa.md`'s "Final report"
section and this wave's own task instructions, this report is
comprehensive across the ENTIRE qa role's vertical-slice scope — qa-01 through
qa-09 — not just this wave's four nodes. `go test ./... -race` was
re-run as this node's own required validation (see "Full-repo test
health" below); `golangci-lint run ./...` (whole repo) was re-run and is
clean.

### Full-repo test health (qa-09's own validation)

```text
go build ./...                 -> OK, no errors
golangci-lint run ./...        -> 0 issues
go test ./... -race            -> ALL 34 packages PASS (32 with real
                                   tests, 2 no-test-files packages:
                                   internal/app, internal/buildinfo)
```

Full per-package `go test ./... -race` output (this run):

```text
ok  	github.com/huaiche94/auspex/cmd/auspex	1.449s
?   	github.com/huaiche94/auspex/internal/app	[no test files]
ok  	github.com/huaiche94/auspex/internal/app/wiring	11.339s
ok  	github.com/huaiche94/auspex/internal/artifacts	1.968s
?   	github.com/huaiche94/auspex/internal/buildinfo	[no test files]
ok  	github.com/huaiche94/auspex/internal/cli	3.271s
ok  	github.com/huaiche94/auspex/internal/clock	2.010s
ok  	github.com/huaiche94/auspex/internal/config	2.547s
ok  	github.com/huaiche94/auspex/internal/domain	1.422s
ok  	github.com/huaiche94/auspex/internal/evaluation	127.047s
ok  	github.com/huaiche94/auspex/internal/features	1.542s
ok  	github.com/huaiche94/auspex/internal/gitx	26.602s
ok  	github.com/huaiche94/auspex/internal/hooks/claude	1.719s
ok  	github.com/huaiche94/auspex/internal/idgen	1.904s
ok  	github.com/huaiche94/auspex/internal/integrationtest	(cached)
ok  	github.com/huaiche94/auspex/internal/lock	2.823s
ok  	github.com/huaiche94/auspex/internal/orchestrator	10.818s
ok  	github.com/huaiche94/auspex/internal/paths	1.901s
ok  	github.com/huaiche94/auspex/internal/pause	39.852s
ok  	github.com/huaiche94/auspex/internal/policy	1.658s
ok  	github.com/huaiche94/auspex/internal/predictor	1.969s
ok  	github.com/huaiche94/auspex/internal/predictor/quota	1.498s
ok  	github.com/huaiche94/auspex/internal/predictor/risk	1.608s
ok  	github.com/huaiche94/auspex/internal/predictor/runway	1.403s
ok  	github.com/huaiche94/auspex/internal/predictor/scope	1.455s
ok  	github.com/huaiche94/auspex/internal/predictor/token	1.471s
ok  	github.com/huaiche94/auspex/internal/progress	43.714s
ok  	github.com/huaiche94/auspex/internal/providers/claude	1.335s
ok  	github.com/huaiche94/auspex/internal/redact	37.609s
ok  	github.com/huaiche94/auspex/internal/repocheckpoint	46.425s
ok  	github.com/huaiche94/auspex/internal/scheduler	26.277s
ok  	github.com/huaiche94/auspex/internal/statecheckpoint	25.590s
ok  	github.com/huaiche94/auspex/internal/storage/sqlite	16.658s
ok  	github.com/huaiche94/auspex/internal/telemetry/claude	9.739s
ok  	github.com/huaiche94/auspex/internal/testutil/fakes	1.283s
ok  	github.com/huaiche94/auspex/pkg/protocol/v1	1.269s
```

Zero regressions, zero flaky failures observed across this run or the
dedicated `-count=20` stress run for qa-07's own scheduler race (above).

### Severity-ranked findings (P0 / P1 / P2)

Per `agents/qa.md`: `P0 blocks merge`, `P1 must fix before demo`, `P2
documented follow-up`. Each entry names exact file, reproduction,
expected invariant, and owning role, per that same section's
requirement.

**P0 — blocks merge: none.**

No P0 finding exists anywhere in qa's vertical-slice scope as of this report. No
invariant this role is chartered to verify (idempotency, restart safety,
path-traversal/symlink safety, secret/raw-prompt leakage, race safety,
governance-doc presence, CI green) is currently violated by a real,
reproducible defect that would make merging the current `vertical-slice/qa` branch
unsafe.

**P1 — must fix before demo:**

1. **No production code path connects a persisted claude-provider
   `v1.Event` to `internal/progress.CompleteNode.Run`** (originally found
   by qa-04, still unresolved as of this report).
   - File: `internal/orchestrator/hooks.go` (HandleStop/
     HandleUserPromptSubmit/HandleStopFailure/HandleStatusLine normalize
     and persist a provider event and stop there); `internal/telemetry/claude/normalizer.go`
     (no producer ever assigns `Event.TaskID`/`Event.ProgressNodeID` —
     confirmed unchanged this wave: both fields remain plain `string`
     fields on `pkg/protocol/v1.Event`, unset by every real normalizer
     call path); `internal/progress/complete_node.go`'s
     `CompleteNodeInput` and `internal/app/ports.go`'s
     `CompleteNodeRequest` (still frozen to exactly `{NodeID,
     IdempotencyKey, Artifacts[, RepositoryCheckpointID]}` — no
     `v1.Event`/`EventID`/`EventType` field anywhere, confirmed by
     re-reading `internal/app/ports.go` this wave); `internal/app/wiring/wiring.go`
     (still wires no bridge between `internal/telemetry/claude` and
     `internal/progress`/a real `app.ProgressTreeService` — confirmed via
     runtime-b10's own `restart_test.go` package doc comment, which
     independently reconfirms this exact gap: "ProgressTree is
     checkpoint's Part A gap... no single type implementing the full
     7-method `app.ProgressTreeService` port").
   - Reproduction: `go test ./internal/integrationtest/... -run TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists -v`
     — still passes, i.e. the gap is still real (a real normalized Stop
     event's `TaskID`/`ProgressNodeID` are still empty strings). Also
     reconfirmed this wave via qa-02's own end-to-end scenario: driving
     the literal vertical-slice demo required this test's own file to complete a
     Progress Tree node via `internal/progress.CompleteNode` directly,
     using a hand-built `CompleteNodeInput` rather than any real event-
     driven trigger — the exact same gap, now also visibly load-bearing
     in the highest-stakes demo scenario, not just an abstract finding.
   - Expected invariant: some real provider observation should be able to
     drive a real Progress Tree node completion end-to-end (Constitution
     Sec6.1's "Progress Tree is the canonical durable task state... never
     an agent's own claim of done" implies a real signal must be able to
     drive it forward) — today nothing does; the event pipeline and the
     completion pipeline are each individually correct and individually
     well-tested, but the middle connecting them does not exist in
     production code.
   - Owning role: `contract-integrator` (a new cross-component port/field
     is needed on the frozen `v1.Event`/`CompleteNodeRequest` contract, or
     a documented decision that this resolution happens via a different,
     not-yet-built lookup path — Constitution Sec4.2 reserves
     `pkg/protocol/v1/**` and `internal/app/ports.go` exclusively to this
     role) in coordination with `claude-provider` (would need to populate
     `TaskID`/`ProgressNodeID` once a resolution mechanism exists) and
     `checkpoint`/`runtime` (whichever role builds the actual consumer/
     adapter, and the still-missing unified `app.ProgressTreeService`/
     `app.GracefulPauseService` adapters runtime-b10's own doc comment
     independently flags as real, still-open gaps in this same area).
   - Why P1, not P0: every individual component this gap spans (claude-
     provider's normalizer, checkpoint's CompleteNode, runtime's hook
     handlers) is itself correct and passes its own tests; no existing
     invariant is violated; vertical-slice's frozen DAG never assigned any node the
     explicit job of building this adapter this wave. It is a real,
     forward-looking integration gap, not a regression — but it is
     squarely in the path of "the literal vertical-slice demo" (qa-02) actually
     working end-to-end in a REAL deployed system (as opposed to this
     test suite's own test-only glue standing in for it), so it should be
     resolved before any live demo, not merely tracked as a someday
     nice-to-have.

**P2 — documented follow-up:**

1. **LICENSE/NOTICE files do not exist in the repository root** (originally
   flagged by qa-08, re-confirmed still absent this wave).
   - File: repository root (no `LICENSE`/`NOTICE` file exists;
     `foundation`'s own exclusive-paths list names them, and
     `foundation-09`'s own progress artifact already flagged this gap
     independently).
   - Reproduction: `ls LICENSE NOTICE 2>&1` at the repository root — both
     report "No such file or directory."
   - Expected invariant: `Auspex_ADD.md` §30.1's "Required files" list
     and `CONTRIBUTING.md`/`GOVERNANCE.md` (both qa-08's own deliverables)
     name Apache-2.0 as the project license by name, consistent with
     `README.md`'s Tech stack table — but no actual `LICENSE` file backs
     that claim yet.
   - Owning role: `foundation` (path ownership) / `contract-integrator`
     (final sign-off) — out of qa's own exclusive paths entirely, filed
     here as a re-flag per Constitution Sec4.4, not a new discovery.
   - Not P1: this is a documentation/compliance completeness gap, not a
     safety, correctness, or security defect in the running system; it
     does not block a functional demo, only final OSS release hygiene.

2. **checkpoint's Repository Checkpoint patch capture only redacts
   secret-shaped content on `+`/`-` line bodies, never on `.git`-tracked
   binary-diff headers or filenames themselves** (a scope note, not a
   regression — recorded for traceability).
   - File: `internal/repocheckpoint/patchredact.go` (redacts only
     "+"/"-"-prefixed line bodies of staged/unstaged patches, by design,
     per that file's own doc comment, so `git apply --check` keeps
     working against the rest of the patch).
   - Reproduction: N/A — no test in this suite constructs a secret-shaped
     FILENAME or a secret-shaped binary-diff header; this is a
     theoretical residual surface, not a confirmed leak.
   - Expected invariant: Constitution Sec7 rule 2's "raw prompts and
     sensitive content are not persisted by default" applied maximally
     would also cover a secret-shaped filename appearing in a patch
     header — currently out of scope for the redaction pass, which is a
     defensible, deliberate design choice (documented at the fix site)
     rather than an oversight.
   - Owning role: `checkpoint` (if this is ever judged worth closing) /
     `qa` (a future dedicated test, if the lead decides this residual
     surface warrants one). Recorded here as a documented follow-up per
     this report's own instruction to be "comprehensive... not a rubber
     stamp," not because a concrete exploit was demonstrated.

### Summary of all nine qa nodes' own outcomes, for one-stop reference

| Node | Deliverable | Outcome |
|---|---|---|
| qa-01 | Cross-platform CI | Completed; documented, deliberate Windows-race-detector platform split; no defect |
| qa-02 | E2E high-risk Claude fixture flow (vertical-slice demo) | Completed; one coherent real end-to-end scenario passes; surfaces the qa-04 P1 gap as load-bearing (see above) |
| qa-03 | Restart same-DB test | Completed; multi-role state (5 roles' storage) survives a real restart in one shared file; no defect |
| qa-04 | Duplicate/out-of-order event test | Completed; found and routed the **P1** finding above (still open) |
| qa-05 | Raw-prompt/secret leakage scanner | Completed; found a **P1** (secret-shaped content in tracked-file diffs unfiltered), **routed and FIXED** by checkpoint (`f981bde`), re-verified passing this wave |
| qa-06 | Path traversal/symlink/malicious fixture tests | Completed; independent adversarial fixtures confirm checkpoint-b09's own fix (a real P1/security finding THAT node found and fixed itself) holds from an external vantage point; no new defect |
| qa-07 | Scheduler double-worker/lease race test | Completed; independent integration-scope composition of the race across both real SQLite-backed layers; no defect |
| qa-08 | Support-bundle/doctor privacy baseline (via governance docs) | Completed; flagged the **P2** LICENSE/NOTICE gap above (still open, not qa-owned) |
| qa-09 | Final report + `go test ./...` evidence | This report |

### Closing statement

This completes the qa role's **entire vertical-slice DAG scope** —
`docs/implementation/vertical-slice/EXECUTION_DAG.md`'s qa-01 through qa-09, all
nine nodes, are now `status: completed`. No further qa-owned DAG node
remains. The one open **P1** (the missing provider-event-to-node-
completion adapter) and one open **P2** (LICENSE/NOTICE) above are both
already correctly routed to their owning roles per Constitution Sec4.4 —
neither is a qa-owned fix, and per `agents/qa.md`'s hard rule this report
does not attempt to fix either. The lead's own Final integration gate
(`contract-integrator-final`) is the only remaining node for the whole
project; this report is intended to give it a precise, honest punch list
rather than a rubber stamp.

