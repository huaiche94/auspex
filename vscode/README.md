# Auspex — VS Code companion extension (MVP)

> 🌐 English | [繁體中文](README.zh-TW.md)

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
  error popup); the tooltip adds the current session's risk and runway
  (or an honest "unknown" when none exist yet).
- **Auspex activity-bar view** — the six FR-162 sections plus daemon
  status and the wake-job queue: **Status**, **Risk**, **Runway**,
  **Quota freshness**, **Progress**, **Checkpoints**, **Pause state**,
  **Scheduled wake jobs**. The session sections render the daemon's
  per-session read-model (`GET /v1/session/status`, schema
  `auspex.daemon.session_status.v1` — `internal/sessionstatus`). Wake
  jobs in `scheduled` status carry an inline **Cancel** button (FR-163),
  both in the queue section and under the pause record they belong to.
- **Commands** — `Auspex: Refresh`, `Auspex: Cancel Scheduled Resume`,
  `Auspex: Show Raw Status`.
- **Live updates** — subscribes to the daemon's SSE stream
  (`GET /v1/events/stream`) with exponential-backoff reconnect, plus a
  15 s poll as safety net. The daemon's broker keeps no event history
  (`internal/daemon/broker.go`), so there is no `Last-Event-ID` replay:
  each (re)connect re-reads current state from the status/jobs/session
  endpoints.

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

## Honesty rendering (FR-162)

The daemon now serves the full FR-162 per-session read-model:
`GET /v1/session/status` (most-recent session — the default view) and
`GET /v1/session/{id}/status`, schema `auspex.daemon.session_status.v1`
(`internal/sessionstatus/snapshot.go`). The former "not exposed by the
daemon API yet" placeholders are gone; the sections render real data
under the server's honesty invariant (ADD §8.8, Constitution §7):

- **Unknown is never zero.** Sections the server answers with JSON `null`
  (no prediction, no runway forecast, no checkpoint, no pause record) and
  a 404 for "no sessions exist yet" render as explicit
  "unknown / no data yet" items — never fabricated scores, percentages,
  or forecasts. Null optional scalars (`used_percent`, burn rates, reset
  times, …) render as "unknown" or are omitted, not shown as 0.
- **`calibrated: false` means estimates, not probabilities.** Risk and
  runway scores from an uncalibrated model are labelled
  "uncalibrated estimate", and `hit_probability` is renamed
  "hit estimate" in that case (Constitution principle #2).
- **Quota staleness is display-only.** The server computes each window's
  `age_seconds`; this extension flags a window "stale" above 5 minutes
  (`QUOTA_STALE_AFTER_SECONDS` in `src/sections.ts`) — a presentation
  choice, documented in the item tooltip, not a server judgement.

Still-true gaps, stated honestly in the UI: the **progress tree** is
usually empty today (nothing populates `progress_nodes` rows for most
sessions yet), **risk** is null until a prediction is linkable to the
session, and **runway** is null until a forecast row is persisted
(live in native-hook mode since PR #85). The payload carries numbers and
ids only — node titles, checkpoint manifests, and filesystem paths are
excluded server-side (FR-171).

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
  field-for-field from the Go handlers, including the populated,
  all-null, and malformed session-status shapes
  (`src/test/types.test.ts`);
- `src/sections.ts` — the FR-162 honesty rendering: unknown-vs-present
  for every session section, calibration labelling, quota staleness,
  progress hierarchy, cancel wiring (`src/test/sections.test.ts`);
- `src/client.ts` — `getSessionStatus` URL/auth/404 behaviour against a
  real loopback `node:http` server (`src/test/client.test.ts`).

Not covered by automated tests: `src/extension.ts` / `src/tree.ts`
(extension-host UI wiring — `tree.ts` is a thin mapping of the tested
`sections.ts` view-model onto `vscode.TreeItem`, exercised manually) and
the SSE network path in `src/client.ts` (smoke-tested against a real
daemon; see the PR's verification notes). An `@vscode/test-electron`
harness was deliberately left out of the MVP to keep the toolchain
minimal.

Run it from source: open `vscode/` in VS Code, `npm ci && npm run build`,
then press F5 ("Run Extension" — a standard extension-development host).
