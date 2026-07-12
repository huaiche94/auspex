# runtime — Progress Artifact

This is `runtime`'s first progress artifact. Per `agents/runtime.md`, this
role consolidates two internal sub-components — **Part A** (Graceful
Pause, Safe Points, Durable Scheduler) and **Part B** (Application
Orchestration, CLI, Local API). Wave 3's assigned node, `runtime-b01`, is
Part B only; Part A (`internal/pause/**`, `internal/scheduler/**`) is not
touched by this artifact and has no entry here yet.

## Handoff notes (Constitution §6.7 / agents/runtime.md "Handoff")

- **CLI package shape**: `internal/cli.NewRootCmd() *cobra.Command` is the
  single exported entry point, mirroring the constructor convention
  foundation-01 established directly in `cmd/preflight/main.go`
  (`newRootCmd()`/`newVersionCmd()`, unexported because that file is the
  binary's own package). Because `internal/cli` is a separate package
  intended for a future root-wiring step to import, `NewRootCmd` is
  exported; every other constructor in the package
  (`newVersionCmd`, `newHookCmd`, `newHookClaudeCmd`, `newInitCmd`,
  `newEvaluateCmd`, `newDecisionCmd`, `newCheckpointCmd`, `newProgressCmd`,
  `newStateCmd`, `newPauseCmd`, `newResumeCmd`, `newSchedulerCmd`,
  `newStatusCmd`, `newDoctorCmd`) stays unexported, matching the granularity
  of the ports/DTOs they will eventually call.
- **Stub error shape**: every command below `version` returns
  `notImplemented(command string) error` (`internal/cli/errors.go`), which
  builds the frozen `*domain.Error` (`internal/domain/errors.go`,
  `CONTRACT_FREEZE.md` "Error contract") with `Code: ErrCodeUnavailable`,
  `Retryable: true`, and `Details["command"]` set to the dotted command
  path (e.g. `"hook claude user-prompt-submit"`). `ErrCodeUnavailable` was
  chosen over `ErrCodeInternal` deliberately: the command surface itself is
  correct and will work once the corresponding service
  (`EvaluationService`, `ProgressTreeService`, `GracefulPauseService`, etc.
  — `internal/app/ports.go`) is wired by a later node (`runtime-b02`
  onward); this is an operational "not yet available," not a code defect.
  `version` is the sole exception — it has no service dependency
  (`internal/buildinfo.String()` only) and is fully real.
- **Command tree**: `NewRootCmd` registers all 18 P0 leaf commands named in
  `agents/runtime.md` Part B in one call, split across two files for
  readability — `internal/cli/root.go` (root + all commands except the
  `hook` subtree) and `internal/cli/hook.go` (`hook claude {statusline,
  user-prompt-submit, stop, stop-failure}`, kept separate because it is a
  three-level subtree, not a single command, and had enough of its own
  naming-convention context — see below — to warrant its own file and its
  own package doc paragraph).
- **`cmd/preflight/main.go` is untouched.** Per `agents/runtime.md`
  ("Do not edit `cmd/preflight/main.go`; the contract-integrator and
  foundation roles integrate root wiring. Add command constructors under
  owned paths.") and the Wave 3 task brief, this node only builds
  `internal/cli`'s constructors. `cmd/preflight/main.go` still wires only
  `version` (from foundation-01, Wave 1) and does not yet call
  `cli.NewRootCmd()` — that integration is explicitly out of scope for
  this role and belongs to `contract-integrator`/`foundation` in a later
  step. The DAG's validation command was run against the *existing*
  `cmd/preflight` binary (still `version`-only) to confirm `internal/cli`
  compiles cleanly into the module and does not break the existing build;
  `internal/cli`'s own `--help` behavior (the full P0 tree) is verified
  directly at the package level in `internal/cli/root_test.go`'s
  `TestHelpSucceeds`, since there is no owned binary target yet that wires
  the full tree.
- **Dependency requests**: none. Cobra (`github.com/spf13/cobra`) and
  `internal/buildinfo`/`internal/domain` were already available
  (foundation-01, Bootstrap); no new `go.mod` entry was needed.

## Naming-convention judgment call: kebab-case hook subcommands

`docs/implementation/day1/wave2-analysis/ADR_Recommendations.md` REC-03
documents a real, still-open discrepancy: `Preflight_ADD.md` Appendix E.3
spells Claude Code hook subcommands in PascalCase (e.g. `UserPromptSubmit`,
matching Claude's own hook-event-name casing), while `agents/runtime.md`'s
own P0 command list, this node's DAG validation command
(`docs/implementation/day1/EXECUTION_DAG.md` `runtime-b01`'s row), and the
Day-1 execution plan's demo script all independently use kebab-case
(`user-prompt-submit`). REC-03 explicitly names `runtime-b01`'s real CLI
command tree as the first place this decision becomes real, and recommends
resolving it via ADR before this node, not after — that ADR has not been
authored as of this commit.

This node follows **kebab-case** (`preflight hook claude
user-prompt-submit`, `stop-failure`), for two independent reasons:

1. Per Constitution §2's document priority order, `agents/runtime.md` (a
   role-scoped operational document, tier 4) is the most specific document
   that names this role's actual command surface, and it uses kebab-case
   verbatim. `Preflight_ADD.md` (tier 2) is architecturally senior in
   general, but Constitution §1's "one authoritative document per subject"
   table names no single sole source of truth for CLI subcommand string
   casing specifically, and three independently-authored frozen documents
   converging on kebab-case (vs. one on PascalCase) is itself evidence
   about which spelling the rest of the project actually built against
   (`integrations/claude/hooks.json`, per REC-03, already uses kebab-case
   too).
2. This node's own DAG validation command
   (`go build ./internal/cli/... && preflight --help`) does not
   independently test subcommand casing, but the task brief that assigned
   this node was explicit: use kebab-case, matching agents/runtime.md's own
   P0 list, and document the call rather than silently inventing a third
   answer.

**This is not a resolution of REC-03.** No ADR has been written; `runtime`
has no authority to accept one (Constitution §3.2 — only
`contract-integrator` accepts ADRs). If `Preflight_ADD.md` Appendix E.3 is
later confirmed as the intended casing via an accepted ADR, the fix is
mechanical: rename the four `Use` strings in
`internal/cli/hook.go`'s `newHookClaudeCmd` and update
`root_test.go`/`errors_test.go`'s path tables to match — no other file is
affected, since every stub command is otherwise identical regardless of
its `Use` string. Flagging this explicitly so a future wave doesn't have
to rediscover it: **REC-03 should still be raised as a real ADR** even
though this node made a documented, non-blocking judgment call to proceed
under kebab-case in the meantime.

## Node log

```yaml
node: runtime-b01
status: completed
artifacts:
  - internal/cli/doc.go
  - internal/cli/errors.go
  - internal/cli/errors_test.go
  - internal/cli/root.go
  - internal/cli/root_test.go
  - internal/cli/hook.go
validation:
  - "gofmt -l internal/cli   # empty output"
  - "go build ./internal/cli/...   # OK"
  - "go vet ./internal/cli/...   # OK"
  - "go test ./internal/cli/... -race -v   # all PASS"
  - "go build -o <tmp> ./cmd/preflight && <tmp> --help   # OK (existing version-only binary; unaffected by this package)"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: a6a3eaa
next_action: runtime-b02 (App wiring) — blocked/not started this wave per explicit instruction to stop once runtime-b01 is Validated; Part A (internal/pause/**, internal/scheduler/**) also not started this wave, out of scope per task brief
assumptions:
  - "Kebab-case for `preflight hook claude ...` subcommands — see the
    dedicated section above. Documented, not silent; REC-03 remains open
    and should still be resolved by an accepted ADR."
  - "Every command below `version` is an honest stub returning
    domain.Error{Code: ErrCodeUnavailable, Retryable: true} rather than
    any real behavior, per explicit task instruction: none of
    orchestrator/evaluation/checkpoint/pause services exist yet this wave,
    and the DAG's own validation command
    (`go build ./internal/cli/... && preflight --help`) only requires
    `go build` and `--help` to work, not working commands."
  - "internal/cli/root.go groups most P0 leaf commands (version, init,
    evaluate, decision, checkpoint, progress, state, pause, resume,
    scheduler, status, doctor) into a single file rather than one file per
    command. The DAG estimated 6 files/350 LOC for runtime-b01; one file
    per command (13 top-level constructors) would have produced far more
    files than that estimate for what is, this wave, structurally
    identical boilerplate per command (a Use/Short/RunE stub). `hook` was
    split out on its own because it is a three-level subtree with its own
    naming-convention discussion, which justified a dedicated file and
    package-doc paragraph the other commands don't need yet. This may be
    resplit into per-domain files (e.g. a checkpoint.go, a pause.go) once
    real business logic lands behind each command in runtime-b02 onward
    and the single-file grouping stops being the natural shape."
  - "NewRootCmd is exported (capital N) unlike foundation-01's
    unexported newRootCmd in cmd/preflight/main.go, because
    internal/cli is a separate package a future root-wiring step needs to
    import; cmd/preflight/main.go's own newRootCmd stays package-private
    since nothing outside that package needs it. Both conventions coexist
    correctly per Go visibility rules; this is not a contradiction of
    foundation's established pattern, just the same pattern applied at a
    package boundary that didn't exist yet when foundation-01 was written."
blockers: []
```
