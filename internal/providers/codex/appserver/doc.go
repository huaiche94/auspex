// Package appserver is the Codex App Server JSON-RPC client — the
// transport core of the ADR-013 primary managed path (ADD §21.2), built
// for issue #9 M7 Phase 2 under ADR-0056 (which records this as a new
// parsed data source per ADR-052 trigger 1).
//
// # Wire protocol (verified against codex-cli 0.144.5)
//
// `codex app-server` speaks newline-delimited JSON-RPC 2.0 over stdio —
// one JSON object per line, NO Content-Length framing. Three frame kinds
// arrive from the server, distinguished exactly as the read loop does:
//
//   - response:       has "id", no "method" — routed to the pending call
//   - notification:   has "method", no "id" — delivered on Notifications()
//   - server request: has both "id" and "method" (e.g. approval asks) —
//     delivered on ServerRequests(); the consumer answers via RespondTo
//
// The handshake is initialize (request) followed by the `initialized`
// notification (ADD §21.2), both sent by Initialize.
//
// # Honest scope (Slice A)
//
// This package owns transport, correlation, and the typed STABLE SUBSET
// of the protocol (§21.7): decode what Auspex consumes, tolerate and
// COUNT unknown notifications (Stats), never fail on unknown fields. It
// deliberately does NOT normalize into pkg/protocol/v1 events, does not
// persist anything, and is not wired into the managed runner — those are
// the following slices. No text content is decoded into any struct here
// beyond what a caller explicitly reads from a raw payload; the typed
// structs name only identifiers, numbers, and enumerated statuses
// (Constitution §7 privacy discipline, same rule as managed/codexstream).
//
// Fixtures: testdata/codex-schema pins the generated protocol schema
// (§21.7's `generate-json-schema` anchor); testdata/transcripts pins
// recorded wire exchanges from the real binary. Contract tests run
// against an in-process fake server speaking recorded frames — no test
// talks to a live account (Constitution §5 rule 4).
package appserver
