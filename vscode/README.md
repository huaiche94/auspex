# Auspex — VS Code companion extension (MVP)

Companion UI for the [Auspex](https://github.com/huaiche94/auspex) daemon
(issue #10; ADD §8.4, FR-162/163/164): live daemon status, the wake-job
queue, and one mutation — cancelling a scheduled resume.

> **Publisher note:** `publisher` is set to `auspex`, which is **not yet a
> registered VS Code Marketplace / Open VSX publisher** — registration is
> the owner-only placeholder action tracked in issue #18. Until then this
> extension is used from source / a locally packaged VSIX only.

## What it does

- **Status bar item** — daemon liveness and wake-job summary
  (`auspex: not running` is a *normal* state, rendered calmly, never an
  error popup).
- **Auspex activity-bar view** — sections for Status, Progress,
  Checkpoints, Pause state, and Scheduled wake jobs. Jobs in `scheduled`
  status carry an inline **Cancel** button (FR-163).
- **Commands** — `Auspex: Refresh`, `Auspex: Cancel Scheduled Resume`,
  `Auspex: Show Raw Status`.
- **Live updates** — subscribes to the daemon's SSE stream
  (`GET /v1/events/stream`) with exponential-backoff reconnect, plus a
  15 s poll as safety net. The daemon's broker keeps no event history
  (`internal/daemon/broker.go`), so there is no `Last-Event-ID` replay:
  each (re)connect re-reads current state from the status/jobs endpoints.

## How it connects (and what it will never touch)

Discovery mirrors the CLI's own probe order:

1. Resolve Auspex's per-OS runtime directory (`src/paths.ts`, a precise
   TypeScript mirror of the daemon's `internal/paths/paths.go`).
2. Read `<runtime>/daemon.json` (`internal/daemon/metadata.go`) — absent
   file means "daemon not running".
3. Read the bearer token from the metadata's `token_file`
   (`<data>/daemon.token`, 0600, rotated per daemon restart — D-16).
4. Call `http://<address>/v1/...` with `Authorization: Bearer <token>`.

**FR-164:** this extension reads **only Auspex's own files** (the two
above) and talks **only to the Auspex daemon's loopback API**. It does not
read any other extension's private state, does not touch provider
credentials, and contains no `vscode.extensions` state access.

## Honest gaps (FR-162)

The daemon's `GET /v1/status` currently serves version, uptime, and
per-status wake-job counts (`auspex.daemon.status.v1`,
`internal/httpapi/httpapi.go`). It does **not** yet expose risk scores,
runway/quota freshness, the progress-tree snapshot, checkpoints, or
pause-record state. Those FR-162 sections render as explicit
"not exposed by the daemon API yet" placeholders with tooltips naming the
gap — this extension does not invent endpoints or fabricate values. The
gaps are tracked as issue #10 follow-ups.

## Development

```bash
cd vscode
npm ci
npm run build       # tsc → out/
npm test            # builds, then scripts/run-tests.js (node --test with
                    # an explicit file list; fails loudly if zero test
                    # files are discovered — the script's comment explains
                    # the Node 20 vs 22 --test path-semantics difference)
```

Dependency versions are pinned **exactly** (no `^`/`~` floats), matching
the repository's CI pinning policy (see `.github/workflows/ci.yml`).

### Test coverage

Unit-tested with Node's built-in test runner (no VS Code download
required):

- `src/paths.ts` — every OS branch with injected env/home
  (`src/test/paths.test.ts`);
- `src/sse.ts` — SSE parsing of the exact daemon stream shapes, chunk
  splits, CRLF, heartbeats, backoff schedule (`src/test/sse.test.ts`);
- `src/types.ts` — response/metadata parsing against fixtures copied
  field-for-field from the Go handlers (`src/test/types.test.ts`).

Not covered by automated tests: `src/extension.ts` / `src/tree.ts`
(extension-host UI wiring — exercised manually) and the live network
paths in `src/client.ts` (smoke-tested against a real daemon; see the
PR's verification notes). An `@vscode/test-electron` harness was
deliberately left out of the MVP to keep the toolchain minimal.

Run it from source: open `vscode/` in VS Code, `npm ci && npm run build`,
then press F5 ("Run Extension" — a standard extension-development host).
