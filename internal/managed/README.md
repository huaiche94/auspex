# internal/managed/ — the managed one-shot runner behind `auspex run`

> 🌐 English | [繁體中文](README.zh-TW.md)

The CLI-free core of `auspex run` (issue #8's MVP increment; ADD §8.1 —
[`docs/design/Auspex_ADD.md`](../../docs/design/Auspex_ADD.md); extended to codex by issue
#9 M7 Phase 1, ADD §21.8; extended with the M10 Graceful Pause auto-trigger by issue #122).
See [`doc.go`](doc.go) for the package contract and the full list of deliberate exclusions.
Three concerns plus one optional trigger, nothing else:

1. **Pre-prompt gate** — the same production evaluate/decide path the `UserPromptSubmit` hook runs, via [`internal/orchestrator`](../orchestrator/README.md)'s `EvaluateManagedPrompt`, applied before the provider process exists: a BLOCK decision means the provider is never spawned.
2. **Provider subprocess lifecycle** — [`run.go`](run.go)'s `Runner.Run` spawns the provider argv-only (never a shell string) per [`provider.go`](provider.go)'s spec table: `claude -p <prompt> --output-format stream-json --verbose` or `codex exec --json <prompt>`. [`stream.go`](stream.go)/[`codexstream.go`](codexstream.go) parse the output defensively and fail-open — an unrecognized line becomes a skip count, never a crash. Result/message content is never retained (length-only), per Constitution §7's privacy rule.
3. **Outcome attribution** — the terminal outcome is normalized through the provider's own telemetry package (`internal/telemetry/claude`, `internal/telemetry/codex`) into the frozen event envelope and best-effort persisted through the same seams the hook path uses, keyed to one `TurnID`.
4. **Graceful Pause auto-trigger (optional; M10, issue #122)** — when `Runner.Pause` is armed, [`pausedrive.go`](pausedrive.go) observes the session's quota runway on ADD §20.3's 5-second heartbeat while the provider runs, feeds each forecast sample into `internal/pause`'s debounce/hysteresis trigger (ADD §17.6/§20.2), and on trigger drives the existing frozen pause lifecycle end to end: request → safe point → checkpoints → provider interrupt → sleeping with a durable wake job. The interrupt mechanism is a per-path `providerStopper`: graceful SIGINT with kill escalation for spawned provider processes, and — on the codex App Server path — ADD §21.6's protocol-level `turn/interrupt` with connection-teardown escalation, the interrupted terminal observed through the run loop itself (issue #9 slice C, [`appserver.go`](appserver.go)). Managed mode **only** — native-hook mode stays observe-only, because a hook cannot interrupt the provider's turn (`internal/orchestrator/runwaydrive.go`'s documented constraint). The calibrated 0.80 trigger is gated on calibration availability (no forecast is calibrated pre-M13), so only ADD §17.6's `emergency_uncalibrated` path can fire in production today; a trigger failure is logged and the run continues (fail toward continuing work).

Supported providers are the rows of `provider.go`'s spec table: `ProviderClaude` and
`ProviderCodex`. The CLI half lives in [`internal/cli/run.go`](../cli/run.go). `testdata/`
holds the claude stream fixtures; the codex exec fixtures live with the other codex payload
fixtures under [`testdata/provider-events/codex/exec`](../../testdata/README.md).

The codex App Server is the ADR-013 PRIMARY managed path ([`appserver.go`](appserver.go),
issue #9 Phase 2 slice B) with `codex exec --json` as the loud fallback; the auto-pause
trigger interrupts it at the protocol level (`turn/interrupt`, slice C above).

Not implemented here (issues #8/#9's later increments): managed shell mode — `auspex shell`,
ADD §8.2, scheduled as ADD milestone M11 — plus daemon integration, verified auto-resume
(§21.6 step 8's `thread/resume` + `turn/start` bootstrap included; the `thread_id` locator is
already persisted on `provider.session.started`), and live per-message usage modeling. An
uninterrupted spawned process runs to completion; context cancellation kills it.
