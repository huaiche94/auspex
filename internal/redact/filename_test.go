package redact_test

import (
	"testing"

	"github.com/huaiche94/preflight/internal/redact"
)

func TestMatchesSecretFilename_ADD278NamePatterns(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".env", true},
		{".env.local", true},
		{".env.production", true},
		{"server.pem", true},
		{"nested/dir/server.pem", true},
		{"private.key", true},
		{"bundle.pfx", true},
		{"cert.p12", true},
		{"id_rsa", true},
		{"path/to/id_rsa", true},
		{"id_ed25519", true},
		{"credentials.json", true},
		{"auth.json", true},
		{"secrets.yaml", true},
		{"secrets.json", true},
		{"secrets.", true},

		// Must NOT match.
		{"id_rsa.pub", false}, // public key, not the private key file itself
		{"id_ed25519.pub", false},
		{"config.json", false},
		{"authentication.go", false}, // contains "auth" but is not auth.json
		{"my-secrets-notes.txt", false},
		{"notes.txt", false},
		{"README.md", false},
		{"environment.go", false}, // contains "env" but is not .env or .env.*
	}
	for _, c := range cases {
		got := redact.MatchesSecretFilename(c.path)
		if got != c.want {
			t.Errorf("MatchesSecretFilename(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestFilenamePatterns_ReturnsIndependentCopy(t *testing.T) {
	a := redact.FilenamePatterns()
	if len(a) == 0 {
		t.Fatal("expected a non-empty pattern list")
	}
	a[0] = "mutated"
	b := redact.FilenamePatterns()
	if b[0] == "mutated" {
		t.Fatal("FilenamePatterns must return an independent copy, not a shared slice")
	}
}
