# ADR-0056 — Codex App Server JSON-RPC stream as a parsed data source; notification mapping stays inside the closed EventType taxonomy (#9 M7 Phase 2)

> 🌐 English | [繁體中文](0056-codex-appserver-data-source.zh-TW.md)

Status: Accepted
Date: 2026-07-28
Owner: lead-executed (PROPOSAL — new-data-source commitment, owner sign-off at merge)
Tracking: issue #9 (M7 Phase 2); architecture per ADR-013 (App Server = primary managed path) and ADD §21.2/§21.6/§21.7; required by ADR-052 trigger 1 (a new data source is parsed); protocol verified against codex-cli 0.144.5 (`generate-json-schema` + live handshake)

## Context

ADR-013 decided the architecture — Codex's primary managed path is the
stable App Server, with `exec --json` as fallback — and #83/#87 shipped
the native-hook and exec-JSONL adapters. The App Server client (thread/
turn lifecycle, live event stream, `turn/interrupt`, `thread/resume`) is
the remaining M7 Phase 2 scope, and it introduces something the shipped
adapters did not: a **new parsed data source**, the App Server's
JSON-RPC stdio stream. ADR-052 trigger 1 makes parsing a new provider
source an ADR-level commitment, because a source carries privacy and
stability obligations independent of which fields ride on which events
(ADR-051 was the same shape of decision for the Stop-hook transcript).

Wire facts verified against the pinned binary (0.144.5): the protocol is
newline-delimited JSON-RPC 2.0 over stdio (no Content-Length framing);
responses omit the `jsonrpc` field; the server pushes unsolicited
notifications immediately after `initialize`; server→client REQUESTS
exist (approval asks carry both `id` and `method`). The protocol schema
is generated from the binary itself (`codex app-server
generate-json-schema`) — ADD §21.7's fixture anchor.

## Decision

1. **The App Server stream is an authorized parsed source** with the
   same privacy commitments as every provider source (Constitution §7):
   typed decode structs name only identifiers, numbers, and enumerated
   statuses. Text-bearing fields are measured, never retained — the
   turn error message decodes to its byte length (the codexstream
   precedent), `turn/diff/updated` to `{threadId, turnId, diffByteLen}`,
   `turn/plan/updated` to `{threadId, turnId, planSteps}`. Item bodies
   (an 18-variant content-bearing union) decode to `{id, type}` only.
   No raw frame is ever persisted.

2. **The transport client lives in `internal/providers/codex/appserver`**
   and owns exactly transport, correlation, and the typed stable subset
   (§21.7 discipline codified): unknown notification methods are
   delivered-and-countable, malformed lines and unroutable frames are
   counted (`Stats`), never fatal; a dispatch-buffer overflow drops and
   counts rather than stalling the read loop. Fixtures are the vendored
   generated schema (one authoritative pin + drift-tripwire test on the
   definitions the subset consumes) plus recorded wire transcripts;
   contract tests run against an in-process fake server (Constitution §5
   rule 4 — no live-account testing).

3. **The notification mapping stays inside the closed EventType
   taxonomy — no new EventTypes.** For the normalization slices that
   follow (recorded here so the taxonomy question is settled once):

   | App Server notification | Frozen EventType |
   |---|---|
   | `thread/tokenUsage/updated` | `provider.usage.observed` (live per-turn usage) |
   | `account/rateLimits/updated`, `account/rateLimits/read` | `provider.quota.observed` |
   | `turn/started` / `turn/completed` | `provider.turn.started` / `provider.turn.completed` |
   | `turn/completed` with status `interrupted` | `provider.turn.interrupted` |
   | `turn/completed` with status `failed` | `provider.turn.failed` |
   | `item/started` / `item/completed` (tool-shaped items) | `provider.tool.started` / `provider.tool.completed` |
   | `turn/diff/updated` | `provider.file_change.observed` (byte-length payload only) |
   | context-compaction signals | `provider.session.compacted` |
   | `turn/plan/updated` | **no event** — consumed as *proposed* Progress-Tree nodes per ADD §21.3 / ADR-027/028 (a provider plan is an observation, not an event-log fact) |

   Token vocabulary keeps the frozen semantic pin: codex
   `inputTokens` includes `cachedInputTokens`; normalization splits
   fresh input from cache reads and keeps `total_tokens` = fresh input +
   output — identical to the rollout/managed-exec normalizers, so the
   ADR-0055 cohort ladder consumes App Server samples with zero changes.

4. **Server→client requests are surfaced, never auto-approved.** The
   client delivers approval asks on a dedicated channel; whichever
   consumer wires them (managed runner slice) must answer explicitly or
   the turn stalls — a stalled turn is honest, a silent auto-approval is
   a policy decision this layer must not make.

5. **The reserved invocation mode `managed_app_server`** (ADD §16
   vocabulary, reserved in code since #87) is filled by the managed-run
   integration slice; this ADR fixes its meaning: a session whose events
   were produced by this client under a live App Server connection.

## Honest scope

- This ADR covers the SOURCE and the mapping vocabulary. The
  normalization into `pkg/protocol/v1` events, the managed-runner
  integration, capability flips, and the §21.6 interrupt/resume sequence
  are the following slices — each stays inside this mapping, or comes
  back for an amendment.
- `account/usage/read` is capability-gated upstream and not consumed
  this phase.
- Reconnect/backoff policy for a dropped App Server connection is
  deliberately unspecified here (implementation latitude; a dropped
  connection degrades to the exec-JSONL fallback path per ADR-013).

## Consequences

- Fixtures: `testdata/codex-schema` pins the generated protocol schema
  with a version note; `testdata/transcripts` records sanitized live
  exchanges; a schema-pin test fails visibly when a regeneration drops a
  consumed definition (§21.7 "missing required field ⇒ capability
  degraded" made test-visible).
- The frozen EventType list is untouched; ADR-052's closed-taxonomy rule
  is upheld by mapping, not extension.
- `CONTRACT_FREEZE.md` needs no amendment for this slice (no port
  change); the managed-runner slice that implements the reserved
  `app.ManagedRunner`/`LiveObserver`/`SessionResumer` ports will cite
  this ADR plus ADR-013.

## Alternatives considered

- **New EventTypes for plan/diff/live-usage notifications.** Rejected:
  every consumed notification maps semantically onto an existing type;
  plan updates are Progress-Tree observations by design (§21.3). Opening
  the closed taxonomy for convenience would trip ADR-052 trigger 4's
  spirit for no information gain.
- **Persist raw notification payloads for later mining.** Rejected
  outright: diff bodies and item content are user code and agent text —
  the §7 privacy defaults forbid it; numbers-only enrichment is the
  ADR-051 precedent this source follows.
- **Auto-approve server approval requests in the client.** Rejected:
  approval is policy, not transport; the client surfaces and the policy
  layer decides (fail toward a stalled-then-interrupted turn, never
  toward silent escalation — §6.10).
