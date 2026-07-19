# internal/orchestrator/ — sequences provider-facing input into the frozen services

> 🌐 English | [繁體中文](README.zh-TW.md)

The layer between provider-facing input (CLI flags, a normalized hook event) and the frozen
cross-role ports ([`internal/app/ports.go`](../app/ports.go)) — it implements no prediction,
policy, checkpointing, or pause logic itself; it sequences calls into the services that do
(ADD §13 — [`docs/design/Auspex_ADD.md`](../../docs/design/Auspex_ADD.md)). See
[`doc.go`](doc.go) for the package contract; note it describes the original Evaluate-pipeline
node, and the package has since grown the surfaces below.

Entry points, one file per command family (each a `Deps` bundle + functions returning plain
structs for [`internal/cli/`](../cli/README.md) to render):

- [`evaluate.go`](evaluate.go) — `Evaluate`: the resolve → load → snapshot → evaluate → decide pipeline.
- [`decision.go`](decision.go) — `DecisionAllowCmd` / `DecisionDenyCmd`, wired to the real evaluation service with storage-backed exactly-once authorization consumption.
- [`hooks.go`](hooks.go) — the four Claude Code hook handlers (`statusline`, `user-prompt-submit`, `stop`, `stop-failure`) and `HookDeps`, with documented nil-safe degradation.
- [`evaluateprompt.go`](evaluateprompt.go) / [`managedgate.go`](managedgate.go) — `auspex evaluate` (fail-closed) and the `auspex run` pre-prompt gate (`EvaluateManagedPrompt`, consumed by [`internal/managed/`](../managed/README.md)); both share the hook path's `evaluateSubmittedPrompt` core rather than reimplementing it.
- [`pauselifecycle.go`](pauselifecycle.go) — `pause request` / `pause cancel` / `resume` / `scheduler run-once` (the run-once sweep claims one job and stops; execution is the daemon worker's job).
- [`daemon.go`](daemon.go) — `daemon run|status|stop|install|uninstall`, including the `com.auspex.daemon` LaunchAgent plist generation around [`internal/daemon/`](../daemon/README.md).
- [`checkpoint.go`](checkpoint.go), [`diagnostics.go`](diagnostics.go) (`status` / `doctor`), [`gc.go`](gc.go).
- [`sessionbootstrap.go`](sessionbootstrap.go) — lazy in-hook creation of repository/worktree/session rows (issue #17); [`correlate.go`](correlate.go) — fills `Event.TaskID` / `Event.ProgressNodeID` at hook-persist time when unambiguous (issue #1); [`openturn.go`](openturn.go), [`progresscomplete.go`](progresscomplete.go).
- [`autocheckpoint.go`](autocheckpoint.go) — `AutoCheckpointer`: the ADR-0054 automatic pre-turn checkpoint for `CHECKPOINT_AND_RUN` decisions (issue #116), shared by the UserPromptSubmit hook and the managed runner; fail-open by contract, gated by `state_checkpointing.on_checkpoint_and_run`.

Composition happens in [`internal/app/wiring/`](../app/wiring/README.md); this package
depends only on the interface types, never on concrete service implementations.
