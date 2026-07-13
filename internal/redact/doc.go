// Package redact implements checkpoint role Part B's content-based secret
// detection (agents/checkpoint.md Part B deliverable #6: "Safe untracked
// archive policy with size/path/secret filters"; Auspex_ADD.md §19.5
// "secret scan", §27.8 "Secret filters"). It is the real extension of
// checkpoint-b04's structural-only untracked-archive policy (size/path/
// symlink caps, internal/repocheckpoint/security.go) into the "secret scan"
// bullet that policy deliberately left unimplemented, per b04's own
// progress-artifact note: "content-based secret scanning (internal/
// redact/** still empty) — b04's untracked-archive policy is structural
// only ... skip-ledger gives b06 a ready extension point."
//
// # Scope and boundary (read this before relying on this package for a
// security guarantee)
//
// This package implements two of the three ADD §19.5/§27.8 detector
// classes, deliberately and explicitly:
//
//  1. Filename-pattern detection (§27.8's exact "Name patterns" list):
//     .env, .env.*, *.pem, *.key, *.pfx, *.p12, id_rsa, id_ed25519,
//     credentials.json, auth.json, secrets.* — matched against a
//     candidate's base filename, case-sensitively (matching the ADD's own
//     literal casing), before any file content is ever read.
//  2. Content-based pattern detection (§27.8's exact "Content detectors"
//     list): bearer token, private key header (PEM block), GitHub/OpenAI/
//     Anthropic API token shapes, Azure storage account keys, JWT-like
//     three-segment base64url tokens, and password/connection-string
//     patterns (e.g. postgres://user:pass@host, a `password=`/`pwd=`
//     assignment). Each detector is documented on its own constructor in
//     patterns.go with the exact regular expression it uses.
//
// This is explicitly, by design, "a reasonable, documented set of
// detectors, not an exhaustive commercial-grade scanner" (this node's own
// brief). It is pattern matching against known-shaped secrets, not
// entropy analysis, not a machine-learned classifier, and not a
// connection to any external secret-scanning service. Concretely, this
// package:
//
//   - DOES catch: the exact filename patterns above; well-known API key
//     prefixes (e.g. `ghp_`, `sk-ant-`, `sk-`); PEM-armored private key
//     blocks; JWT-shaped strings; Authorization: Bearer headers;
//     Azure-shaped storage account keys; connection strings with an
//     embedded plaintext password.
//   - DOES NOT catch: secrets with no recognizable shape (e.g. a random
//     32-byte hex string with no surrounding context, an org's own
//     internal or rotated credential format not in this list); secrets
//     split across multiple files or reconstructed at runtime; secrets
//     inside binary/compressed content (this package skips content
//     scanning for files it detects as binary — see IsLikelyBinary in
//     scan.go — since a compressed/encoded secret does not match a
//     plaintext pattern and scanning binary noise for these patterns
//     would only produce false positives); secrets that are semantically
//     sensitive but have no distinguishing lexical shape at all (e.g. an
//     internal customer ID that happens to be confidential).
//   - Every detector is a fixed regular expression run over file content
//     up to MaxContentScanBytes (scan.go) per file; content beyond that
//     cap is not scanned (documented, not silent — see scan.go).
//
// qa-05 (the leakage scanner, per EXECUTION_DAG.md's "Feeds qa-05 leakage
// scanner" note on this node) should treat this package's detectors as one
// layer of defense-in-depth, not the sole guarantee that no secret ever
// reaches a checkpoint artifact — qa-05's own scan of DB export/logs/
// manifests is the independent, cross-cutting check that catches what any
// single layer (including this one) might miss.
package redact
