// Package managed implements the managed one-shot runner behind `auspex
// run` (ADD §8.1; GitHub issue #8, increment 1 — the MVP). It owns three
// concerns and deliberately nothing else:
//
//  1. the pre-prompt gate: the same production evaluate/decide path the
//     UserPromptSubmit hook runs (internal/orchestrator's
//     EvaluateManagedPrompt over the shared evaluateSubmittedPrompt core),
//     applied BEFORE the provider process exists — a BLOCK decision means
//     the provider is never spawned at all, which is the one enforcement
//     capability native hook mode can only approximate (ADD §8.3 vs §8.1);
//  2. provider subprocess lifecycle: spawning `claude -p <prompt>
//     --output-format stream-json --verbose` argv-only (Constitution §7
//     rule 5 — never a shell command string; internal/gitx.ExecRunner's
//     established discipline) and defensively parsing the resulting
//     stream-json lines (stream.go);
//  3. outcome attribution: normalizing the run's terminal outcome into the
//     frozen event envelope via internal/telemetry/claude's Normalizer
//     (NormalizeManagedRun) and best-effort persisting through the same
//     EventPersister/TxRunner seam the hook path uses, so a managed run's
//     telemetry lands in the same events table, keyed to one TurnID from
//     provider.turn.started through provider.usage.observed — ADD §8.7's
//     "exact completed usage: yes" cell for Claude managed stream-json.
//
// # Deliberate exclusions (issue #8's later increments, not this one)
//
//   - `auspex shell` (ADD §8.2 managed shell mode) — no prompt loop exists;
//     the CLI has no shell command yet and this increment adds none.
//   - Turn interrupt / safe-point pause (ADD §8.1 "safe-point pause",
//     §19-§20) — the spawned process runs to completion; context
//     cancellation kills it, nothing gracefully interrupts it.
//   - Daemon/event-stream integration — the runner is a plain in-process
//     CLI flow with no daemon dependency.
//   - Auto-resume / session resume (ADD §8.1 "verified auto-resume") — a
//     run is one shot; the provider session ID the stream reports is not
//     yet captured for resume.
//   - Continuous runway forecasting during the run (ADD §8.1) — the stream
//     is parsed for the terminal result only; per-message live usage is
//     skipped-but-counted, not modeled.
package managed
