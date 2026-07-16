// Package managed implements the managed one-shot runner behind `auspex
// run` (ADD §8.1; GitHub issue #8, increment 1 — the MVP; extended to
// codex by issue #9 M7 Phase 1, ADD §21.8). It owns three concerns and
// deliberately nothing else:
//
//  1. the pre-prompt gate: the same production evaluate/decide path the
//     UserPromptSubmit hook runs (internal/orchestrator's
//     EvaluateManagedPrompt over the shared evaluateSubmittedPrompt core),
//     applied BEFORE the provider process exists — a BLOCK decision means
//     the provider is never spawned at all, which is the one enforcement
//     capability native hook mode can only approximate (ADD §8.3 vs §8.1);
//  2. provider subprocess lifecycle: spawning the provider argv-only
//     (Constitution §7 rule 5 — never a shell command string; internal/
//     gitx.ExecRunner's established discipline) and defensively parsing
//     the resulting event stream — `claude -p <prompt> --output-format
//     stream-json --verbose` (stream.go) or `codex exec --json <prompt>`
//     (codexstream.go), per the provider spec table in provider.go;
//  3. outcome attribution: normalizing the run's terminal outcome into the
//     frozen event envelope via the provider's own telemetry normalizer
//     (internal/telemetry/claude.NormalizeManagedRun, internal/telemetry/
//     codex.NormalizeManagedExec) and best-effort persisting through the
//     same EventPersister/TxRunner seam the hook path uses, so a managed
//     run's telemetry lands in the same events table, keyed to one TurnID
//     from provider.turn.started through provider.usage.observed — ADD
//     §8.7's "exact completed usage: yes" cell for Claude managed
//     stream-json, and its §21.8 analog for codex exec.
//
// # Deliberate exclusions (issues #8/#9's later increments, not this one)
//
//   - `auspex shell` (ADD §8.2 managed shell mode) — no prompt loop exists;
//     the CLI has no shell command yet and this increment adds none.
//   - Turn interrupt / safe-point pause (ADD §8.1 "safe-point pause",
//     §19-§20, §21.8 "Managed pause in exec mode") — the spawned process
//     runs to completion; context cancellation kills it, nothing
//     gracefully interrupts it.
//   - Daemon/event-stream integration — the runner is a plain in-process
//     CLI flow with no daemon dependency; the codex app-server persistent
//     subscription (ADD §21.2) is a different milestone.
//   - Auto-resume / session resume (ADD §8.1 "verified auto-resume";
//     `codex exec resume`) — a run is one shot; the provider session/
//     thread ID the stream reports is captured as attribution data only,
//     never driven.
//   - Continuous runway forecasting during the run (ADD §8.1) — the stream
//     is parsed for the terminal result only; per-message live usage
//     (claude) and item.* progress (codex) are skipped-but-counted, not
//     modeled.
package managed
