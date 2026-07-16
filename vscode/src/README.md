# vscode/src/ — TypeScript sources of the VS Code companion extension

> 🌐 English | [繁體中文](README.zh-TW.md)

What the extension does and how it connects to the daemon is
documented in [`../README.md`](../README.md); this is the file map.
Compiled by `tsc` to `out/` (`npm run build`,
[`../tsconfig.json`](../tsconfig.json)).

## Extension-host layer (imports `vscode`; exercised manually)

- `extension.ts` — activation, the status bar item, polling +
  SSE-driven refresh, and the command palette surface (FR-162/163/164).
- `tree.ts` — the Auspex activity-bar tree view. Renders only fields
  the daemon API actually serves; the FR-162 session sections (Risk,
  Runway, Quota freshness, Progress, Checkpoints, Pause state) are a
  thin mapping of `sections.ts`'s view-model onto `vscode.TreeItem`.

## Pure logic layer (no `vscode` import; unit-testable under plain Node)

- `client.ts` — daemon discovery (`daemon.json` metadata + bearer
  token file), authenticated HTTP against the loopback API, and the
  reconnecting SSE subscription. Node built-ins only.
- `sections.ts` — view-model builders for the FR-162 session sections,
  rendered from `GET /v1/session/status`
  (`auspex.daemon.session_status.v1`). Encodes the honesty rules:
  null → explicit "unknown / no data yet" items (never fabricated
  zeros), `calibrated:false` → scores labelled uncalibrated estimates.
- `paths.ts` — per-OS resolution of Auspex's user directories; a
  line-for-line TypeScript mirror of the Go daemon's
  `internal/paths/paths.go`, so extension and daemon agree on where
  `daemon.json` and `daemon.token` live.
- `sse.ts` — minimal Server-Sent Events parser for the daemon's
  `GET /v1/events/stream`.
- `types.ts` — TypeScript mirrors of the daemon's wire shapes
  (`internal/httpapi/httpapi.go` responses,
  `internal/sessionstatus/snapshot.go`'s per-session read-model,
  `internal/daemon/metadata.go`, and `pkg/protocol/v1`'s `Event` —
  whose SSE payload uses PascalCase keys because the Go struct has no
  json tags). Every field exists in the Go handlers; nothing invented.

Unit tests for the pure layer live in [`test/`](test/README.md).
