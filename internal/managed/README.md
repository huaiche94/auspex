# internal/managed/ — the managed one-shot runner behind `auspex run`

> 🌐 English | [繁體中文](README.zh-TW.md)

The CLI-free core of `auspex run` (issue #8's MVP increment; ADD §8.1 —
[`docs/design/Auspex_ADD.md`](../../docs/design/Auspex_ADD.md); extended to codex by issue
#9 M7 Phase 1, ADD §21.8). See [`doc.go`](doc.go) for the package contract and the full
list of deliberate exclusions. Three concerns, nothing else:

1. **Pre-prompt gate** — the same production evaluate/decide path the `UserPromptSubmit` hook runs, via [`internal/orchestrator`](../orchestrator/README.md)'s `EvaluateManagedPrompt`, applied before the provider process exists: a BLOCK decision means the provider is never spawned.
2. **Provider subprocess lifecycle** — [`run.go`](run.go)'s `Runner.Run` spawns the provider argv-only (never a shell string) per [`provider.go`](provider.go)'s spec table: `claude -p <prompt> --output-format stream-json --verbose` or `codex exec --json <prompt>`. [`stream.go`](stream.go)/[`codexstream.go`](codexstream.go) parse the output defensively and fail-open — an unrecognized line becomes a skip count, never a crash. Result/message content is never retained (length-only), per Constitution §7's privacy rule.
3. **Outcome attribution** — the terminal outcome is normalized through the provider's own telemetry package (`internal/telemetry/claude`, `internal/telemetry/codex`) into the frozen event envelope and best-effort persisted through the same seams the hook path uses, keyed to one `TurnID`.

Supported providers are the rows of `provider.go`'s spec table: `ProviderClaude` and
`ProviderCodex`. The CLI half lives in [`internal/cli/run.go`](../cli/run.go). `testdata/`
holds the claude stream fixtures; the codex exec fixtures live with the other codex payload
fixtures under [`testdata/provider-events/codex/exec`](../../testdata/README.md).

Not implemented here (issues #8/#9's later increments): managed shell mode — `auspex shell`,
ADD §8.2, scheduled as ADD milestone M11 — plus turn interrupt / safe-point pause during a
run, daemon/event-stream/app-server integration, verified auto-resume (`codex exec resume`
included), and live per-message usage modeling. The spawned process runs to completion;
context cancellation kills it.
