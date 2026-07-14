// token.go: the loopback API bearer token (ADD §23.2 "256-bit bearer
// token", §27.5 "rotate per restart"; storage location is the owner's D-16
// decision — a 0600 file in the data dir, NOT Keychain and NOT an ephemeral
// runtime path, so a client (CLI `daemon status`, the future VS Code
// extension #10) discovers it at one stable path across daemon restarts
// while each restart still invalidates every previously-issued token by
// rewriting the file's content).
package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// TokenFileName is the bearer token's fixed basename inside the data dir
// (D-16). Full path: <data>/daemon.token.
const TokenFileName = "daemon.token"

// GenerateToken mints a fresh 256-bit random token, hex-encoded (64 chars —
// header-safe, no padding), and writes it to dir/daemon.token with 0600
// owner-only permissions (NFR-022). The write is O_TRUNC over any prior
// token: rotation IS the overwrite (§27.5). Returns the token value.
func GenerateToken(dataDir string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("daemon: generating token: %w", err)
	}
	token := hex.EncodeToString(raw)
	path := filepath.Join(dataDir, TokenFileName)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("daemon: writing token file %s: %w", path, err)
	}
	// WriteFile does not chmod an EXISTING file — a token file left behind
	// with looser permissions (e.g. restored from a backup) must not keep
	// them across a rotation.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("daemon: restricting token file %s: %w", path, err)
	}
	return token, nil
}

// VerifyToken compares a presented token against the expected one in
// constant time (subtle.ConstantTimeCompare), so a loopback-local attacker
// cannot binary-search the token byte-by-byte through timing. An empty
// expected token never matches anything — a daemon that failed to mint a
// token authenticates nobody rather than everybody.
func VerifyToken(expected, presented string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}
