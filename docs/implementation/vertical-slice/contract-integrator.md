# contract-integrator — Progress Artifact

Executed as the **Bootstrap stage** (lead-only, not a Wave 1 teammate task —
see `CONSTITUTION.md` pending amendment and repository owner's 2026-07-12
directive resolving the Wave 1 deadlock).

```yaml
node: bootstrap-01
status: completed
artifacts:
  - internal/domain/ids.go
  - internal/domain/measurement.go
  - internal/domain/status.go
  - internal/domain/status_test.go
  - internal/domain/failure.go
  - internal/domain/artifact.go
  - internal/domain/checkpoint.go
  - internal/domain/clock.go
  - internal/domain/errors.go
  - internal/domain/capability.go
  - internal/domain/usage.go
  - internal/app/ports.go
  - pkg/protocol/v1/event.go
  - pkg/protocol/v1/event_test.go
  - docs/implementation/vertical-slice/CONTRACT_FREEZE.md
  - go.mod
validation:
  - gofmt -l internal/domain internal/app pkg/protocol   # empty output
  - go build ./internal/domain/... ./internal/app/... ./pkg/protocol/...
  - go vet ./internal/domain/... ./internal/app/... ./pkg/protocol/...
  - go test ./internal/domain/... ./internal/app/... ./pkg/protocol/...
commit: 4262b4b
next_action: Commit Bootstrap, then spawn Wave 1 teammates (foundation, claude-provider, checkpoint, predictor) per repository owner's directive
assumptions:
  - CompleteNode's atomic transaction boundary is documented at the contract
    level (CONTRACT_FREEZE.md) but the actual state machine and transaction
    implementation belong to the checkpoint role, not this stage.
  - Request/response DTOs in internal/app/ports.go carry minimal fields
    sufficient to compile; owning roles may request additions through their
    own progress artifact rather than editing ports.go themselves.
  - Go toolchain upgraded 1.19.1 -> 1.26.5 via Homebrew (approved by
    repository owner) as an environment prerequisite, not a Bootstrap task.
blockers: []
```

## Stage 5: contract-integrator-final (Final integration gate)

Executed as the **last DAG node** (lead-only, per `EXECUTION_DAG.md`'s own
entry: `deps: qa-09 | Stage 5 | Risk: High — last chance to catch cross-role
contradictions | Cannot start until qa's final report exists`), per
[issue #2](https://github.com/huaiche94/auspex/issues/2)'s scope.

### 1. Full `go test ./... -race` scan

Green across all 37 packages, re-confirmed multiple times across this
stage as each corrective addition landed. No flaky or skipped tests. See
`git log --oneline` for the sequence of integration commits, each of which
independently re-ran the full suite before merging.

### 2. Cross-role contradiction review — the review's central finding

Every individual role's work was independently verified as it landed
(waves 1-12), and every wave's whole-repo `go test ./... -race` passed.
But **passing per-wave composition tests is not the same as the actual
binary being wired to real services** — this stage's own risk note
("six roles each correctly individually does not mean the composition is
correct") predicted exactly the class of gap that turned out to be real:

`cmd/auspex/main.go` was still **foundation-01's original stub** —
only `auspex version` was wired. Investigation found three of the five
frozen `app.*` service interfaces (`ProgressTreeService`,
`GracefulPauseService`) plus `internal/evaluation`'s own local `DataSource`
seam had **no real, assembled production implementation anywhere in the
codebase** — confirmed via `grep -rn "var _ app.<X>Service"` finding only
`internal/testutil/fakes` doubles and test-local satisfactions. Every
individual piece (state machines, atomicity, idempotency, crash recovery,
security controls) was real and deeply tested; the final assembly step —
composing those pieces into the exact frozen interface shape, then wiring
`cmd/auspex/main.go` to construct and use them — was never a DAG-
numbered task and had fallen through the cracks of the wave-by-wave
process.

Closed via three corrective additions (routed to the owning role in each
case, never implemented by the lead directly, per Constitution §7):
- `internal/progress.Service` (checkpoint) — composes NodeStore/EdgeStore/
  ArtifactStore/CompleteNode/Reconciler into `app.ProgressTreeService`.
- `internal/pause.Service` (runtime) — composes the full pause lifecycle
  machinery into `app.GracefulPauseService`; found and fixed a real
  `sync.Mutex`-copy bug via `go vet` in the process.
- `internal/evaluation.SQLDataSource` (predictor) — a real, storage-backed
  `DataSource` querying tables across foundation/claude-provider/checkpoint
  (read-only); 7 of 9 methods real, 2 honestly cold-start-only where the
  frozen schema carries no backing signal.

The lead then wired `cmd/auspex/main.go` directly (this stage's own
reserved work, per `agents/*.md` and `internal/app/wiring`'s own doc
comment: "Root wiring is NOT this package's job... the contract-integrator/
foundation roles own composing this container into the binary") —
`cmd/auspex/wire.go` (composition root) and `cmd/auspex/adapters.go`
(small DTO-shape-translation seams the owning packages each documented as
"a future wiring node's job"). Two remaining seams — managed provider
interrupt and managed session resume — are wired to fail-closed stubs, not
fabricated real behavior, because both are explicit, repeatedly-documented
stretch goals never built in this vertical slice (claude-provider's and
runtime's own role docs say so verbatim).

Manually smoke-tested the compiled binary: `version`, `doctor` (real DB
connectivity, real migration count), `status`/`pause request` (real
validation errors), and a real SQLite foreign-key constraint correctly
rejecting a pause request against nonexistent task/session rows — proof
the real backing store is genuinely enforced end-to-end, not silently
accepted.

### 3. Race / security re-review

No new issues found beyond what earlier waves' own security sweeps
already surfaced and fixed. Summary of every real bug found and fixed
across the whole build (all independently verified by the lead before
integration, not self-reported):
- foundation-07: a TOCTOU race and a `SQLITE_BUSY` bootstrap race in the
  core SQLite engine.
- checkpoint-b09: a path-traversal vulnerability in Repository Checkpoint
  `Verify` (a tampered manifest could make it read arbitrary files outside
  the checkpoint directory).
- runtime-a09: a TOCTOU race in pause `Cancel`/`Resume` (fixed via
  compare-and-swap).
- predictor-10: an authorization prompt-binding bypass (the check was
  keyed on whether the *caller's request* supplied a prompt hash, not
  whether the *authorization row* was bound to one).
- runtime (this stage): a `sync.Mutex`-copy bug caught by `go vet` while
  building the `GracefulPauseService` adapter.

### 4. `CONTRACT_FREEZE.md` amendment audit

`git log --oneline -- internal/app/ports.go internal/domain/
pkg/protocol/v1/` shows exactly two commits across the entire project's
history: the original Bootstrap freeze and ADR-041 (the Token/Quota
Forecast layer insertion) — no other commit ever touched a frozen
contract file. ADR-041 is already fully documented in
`docs/adr/0041-predictor-forecast-layer.md` and reflected in this file's
own "Predictor pipeline ports (ADR-041)" section. No undocumented
amendments exist; the frozen contract layer held for the entire build.

### 5. Known, deliberately still-open items (not blockers for this gate)

- [Issue #1](https://github.com/huaiche94/auspex/issues/1) (P1,
  qa-04/qa-09): no production adapter connects a persisted claude-provider
  event to Progress Tree node completion — a genuinely separate gap from
  this stage's own finding (a *new* adapter that doesn't exist yet, not an
  *unassembled* existing one), correctly left open for a future wave.
- 14 further backlog issues (#3-#16) covering security follow-ups,
  ADR-needed recommendations, and post-slice roadmap milestones (M6-M13) —
  all explicitly out of this vertical slice's scope, filed for future
  planning.

```yaml
node: contract-integrator-final
status: completed
artifacts:
  - internal/progress/service.go (checkpoint, routed corrective addition)
  - internal/progress/task_store.go (checkpoint, routed corrective addition)
  - internal/pause/service.go (runtime, routed corrective addition)
  - internal/pause/sqlitestore.go (runtime, extended — PersistPauseStore)
  - internal/evaluation/datasource_sql.go (predictor, routed corrective addition)
  - cmd/auspex/main.go (lead, root wiring)
  - cmd/auspex/wire.go (lead, root wiring)
  - cmd/auspex/adapters.go (lead, root wiring)
  - docs/implementation/vertical-slice/contract-integrator.md (this section)
validation:
  - go build ./...                        # clean
  - go vet ./...                          # clean
  - gofmt -l . (excluding testdata/)      # empty
  - go test ./... -race                  # all 37 packages pass
  - golangci-lint run ./...              # 0 issues
  - manual smoke test of the compiled binary (version/doctor/status/pause request)
commit: 3b6cfcb
next_action: Retire the six vertical-slice/* branches and auspex-* worktrees (all merged); update README to slice-complete status
assumptions:
  - The three "real service assembly" gaps found during this stage are a
    process gap (no DAG node ever explicitly covered "assemble the frozen
    ports into main.go"), not a defect in any individual role's own work —
    every underlying piece was already correct and tested.
  - Managed provider interrupt/resume remain intentional stretch-scope
    stubs, not gaps, per claude-provider's and runtime's own role docs.
blockers: []
```
