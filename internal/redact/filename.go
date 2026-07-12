// filename.go: the exact "Name patterns" list from Preflight_ADD.md §27.8,
// transcribed verbatim (not a superset, not a subset):
//
//	.env
//	.env.*
//	*.pem
//	*.key
//	*.pfx
//	*.p12
//	id_rsa
//	id_ed25519
//	credentials.json
//	auth.json
//	secrets.*
package redact

import (
	"path/filepath"
)

// filenamePatterns is §27.8's list verbatim, as filepath.Match-compatible
// glob patterns matched against a path's base name only (never the full
// path — a file named config.json inside a directory called secrets is
// not itself named secrets.*, so this is deliberately base-name-scoped,
// matching how a human reads that ADD list).
var filenamePatterns = []string{
	".env",
	".env.*",
	"*.pem",
	"*.key",
	"*.pfx",
	"*.p12",
	"id_rsa",
	"id_ed25519",
	"credentials.json",
	"auth.json",
	"secrets.*",
}

// MatchesSecretFilename reports whether path's base name matches one of
// §27.8's exact name patterns. path may be an absolute path, a
// worktree-relative path, or a bare filename — only the final path
// component is compared.
func MatchesSecretFilename(path string) bool {
	base := filepath.Base(filepath.ToSlash(path))
	for _, pattern := range filenamePatterns {
		if ok, err := filepath.Match(pattern, base); err == nil && ok {
			return true
		}
	}
	return false
}

// FilenamePatterns returns a copy of the exact name-pattern list this
// package matches against, for callers (e.g. documentation, diagnostics)
// that want to display or log the active policy rather than hard-code
// their own copy of §27.8's list.
func FilenamePatterns() []string {
	out := make([]string, len(filenamePatterns))
	copy(out, filenamePatterns)
	return out
}

// Note on ".env.*": Go's filepath.Match treats "." as an ordinary rune
// (unlike shell globs, it does not require an explicit leading-dot
// opt-in), so ".env.local", ".env.production", etc. all match the literal
// pattern ".env.*" the same way "*.pem" matches "id.pem" — no special-
// casing is needed beyond the plain filepath.Match call above; see
// filename_test.go for the cases this covers.
