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
- **Governance docs** (qa-08): `SECURITY.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `GOVERNANCE.md` at the repository root are
  grounded in `Preflight_ADD.md` §30.7 (Governance) and §30.8 (Security
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
    expected_invariant: "Preflight_ADD.md's Progress Tree is meant to be driven forward by real provider observations (a provider.turn.completed signal is exactly the kind of real-world event that should be able to trigger a node's completion) — Constitution §6.1 ('Progress Tree is the canonical durable task state... never an agent's own claim of done') implies SOME real signal must be able to drive a real completion, not just a test harness hand-constructing a CompleteNodeInput. Today, nothing does: the event pipeline (claude-provider) and the completion pipeline (checkpoint/progress) are both individually correct and individually well-tested, but there is a genuine missing middle layer between them. This is exactly the kind of integration-only gap qa-04 was chartered to find, per its own task brief ('an event type or field mapping mismatch, a case where claude-provider's real events don't actually carry information checkpoint's ordering-check logic expects')."
    owning_role: "contract-integrator (a new cross-component port/field is needed on the frozen v1.Event/CompleteNodeRequest contract, or a documented decision that TaskID/ProgressNodeID resolution happens via a different, not-yet-built lookup path — Constitution §4.2 reserves pkg/protocol/v1/** and internal/app/ports.go exclusively to this role) in coordination with claude-provider (would need to populate TaskID/ProgressNodeID on produced events once a resolution mechanism exists) and checkpoint (whichever role builds the actual consumer/adapter). Not routed as P0: every individual component this gap spans is itself correct and passes its own tests, no existing invariant is violated, and Day-1's frozen DAG never assigned any node the explicit job of building this adapter this wave — it is a forward-looking integration gap this node exists to surface, not a regression."
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
    expected_invariant: "Preflight_ADD.md §19.5/§27.8's secret-scan default policy and Constitution §7 rule 2 (raw prompts and sensitive content are not persisted/exposed by default) read most naturally as applying to everything a Repository Checkpoint captures, not only the untracked-file archive. A secret pasted into a tracked config file and staged (a completely ordinary accidental-commit scenario) currently survives, unredacted, in every checkpoint artifact from that point forward."
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
  - "task build   # go build -o bin/preflight ./cmd/preflight -> succeeds"
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
  - "manual doc review against Preflight_ADD.md §30.7 (Governance) and §30.8 (Security disclosure), and against README.md's existing 'Contributing' section and Tech stack table, for contradictions -> none found"
  - "grep -rn \"CLA\" --include=*.md .   # only CONTRIBUTING.md/GOVERNANCE.md/Preflight_ADD.md mention it, all consistent ('no CLA')"
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
    It also requires reading CONSTITUTION.md, Preflight_ADD.md, and
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
    (docs/implementation/day1/foundation.md's foundation-09 log flags
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
