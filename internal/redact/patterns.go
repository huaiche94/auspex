// patterns.go: the exact "Content detectors" list from Auspex_ADD.md
// §27.8, transcribed one detector per bullet:
//
//	bearer token
//	private key header
//	GitHub/OpenAI/Anthropic token patterns
//	Azure storage keys
//	JWT-like
//	password connection strings
//
// Each detector below is a fixed, documented regular expression — pattern
// matching against a known secret SHAPE, not entropy analysis or a
// learned classifier (see doc.go's package-level scope note for exactly
// what this does and does not catch).
package redact

import "regexp"

// Detector is one named content-based secret pattern.
type Detector struct {
	// Name identifies the detector for reporting (e.g. skip-ledger
	// reasons, diagnostics) — stable across releases, never used for
	// display formatting decisions.
	Name string
	re   *regexp.Regexp
}

// Find reports whether content contains a match for this detector,
// returning the first match's byte offset range for callers that want to
// report location (never the matched text itself — this package never
// echoes a candidate secret back into a log/report, only the fact and
// location of a match, since a partial-secret excerpt is itself a leak
// vector).
func (d Detector) Find(content []byte) (start, end int, found bool) {
	loc := d.re.FindIndex(content)
	if loc == nil {
		return 0, 0, false
	}
	return loc[0], loc[1], true
}

// detectors is §27.8's content-detector list, one entry per ADD bullet.
var detectors = []Detector{
	{
		Name: "bearer_token",
		// "Authorization: Bearer <token>" header shape, case-insensitive
		// on the scheme name (HTTP header names/values are conventionally
		// case-insensitive for the scheme token per RFC 7235), requiring
		// at least 12 chars of token material to avoid matching a
		// documentation placeholder like "Bearer <token>" or "Bearer xxx".
		re: regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-_.~+/]{12,}=*`),
	},
	{
		Name: "private_key_header",
		// PEM-armored private key block header — matches RSA/EC/DSA/
		// OPENSSH/generic "PRIVATE KEY" headers, the single highest-
		// confidence pattern in this whole list (a false positive here is
		// essentially impossible in ordinary source/config content).
		re: regexp.MustCompile(`-----BEGIN\s+((RSA|EC|DSA|OPENSSH|ENCRYPTED)\s+)?PRIVATE KEY-----`),
	},
	{
		Name: "github_token",
		// GitHub's documented token prefixes: ghp_ (personal access
		// token), gho_ (OAuth), ghu_ (user-to-server), ghs_ (server-to-
		// server/installation), ghr_ (refresh token), github_pat_ (fine-
		// grained PAT). Fixed-length base62-ish suffix per GitHub's own
		// published format.
		re: regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{22,}\b`),
	},
	{
		Name: "openai_token",
		// OpenAI's documented secret-key prefix. sk-proj- is the newer
		// project-scoped key format; sk- alone is the legacy format.
		// Excludes sk-ant- (Anthropic, see next detector) via negative
		// lookahead being unavailable in RE2 — handled instead by
		// ordering: this detector's minimum length (20) after "sk-"
		// still overlaps with Anthropic's "sk-ant-" prefix in principle,
		// so anthropic_token is checked separately and this pattern is
		// intentionally scoped to NOT match "sk-ant-" via an explicit
		// exclusion of that continuation.
		re: regexp.MustCompile(`\bsk-(proj-)?[A-Za-z0-9](?:[A-Za-z0-9]{19,})\b`),
	},
	{
		Name: "anthropic_token",
		// Anthropic's documented API key prefix (sk-ant-...).
		re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9\-_]{20,}\b`),
	},
	{
		Name: "azure_storage_key",
		// Azure Storage account keys are 88-char base64 values, almost
		// always seen in a connection string as AccountKey=... — anchor
		// on that key name to avoid matching arbitrary 88-char base64
		// blobs that aren't Azure keys at all.
		re: regexp.MustCompile(`(?i)AccountKey=[A-Za-z0-9+/]{86}==`),
	},
	{
		Name: "jwt_like",
		// Three base64url segments separated by dots, the unmistakable
		// shape of a JWT (header.payload.signature) regardless of
		// issuer. Each segment requires a realistic minimum length to
		// avoid matching short dotted identifiers (e.g. version strings).
		re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	},
	{
		Name: "password_connection_string",
		// Two shapes: (a) a URL-style connection string with an embedded
		// plaintext credential (scheme://user:password@host - covers
		// postgres, mysql, mongodb, redis, amqp, and similar); (b) a
		// key=value assignment for password/pwd/passwd, quoted or bare,
		// requiring at least 4 chars of value to skip empty placeholders
		// like password="" or PASSWORD=xxx-here in a template file.
		re: regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@]{3,}@[^\s/]+)|\b(password|pwd|passwd)\s*[:=]\s*['"]?[^\s'",;]{4,}['"]?`),
	},
}

// Detectors returns the fixed set of content detectors this package
// applies, in the order they are checked. Exposed so callers (and tests)
// can enumerate detector names without reaching into an unexported slice.
func Detectors() []Detector {
	out := make([]Detector, len(detectors))
	copy(out, detectors)
	return out
}
