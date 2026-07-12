package redact_test

import (
	"strings"
	"testing"

	"github.com/huaiche94/preflight/internal/redact"
)

// azureKeyBody is a syntactically valid-shaped (86 base64 chars + "==")
// Azure Storage account key body, matching the exact length Azure's real
// keys use, but obviously not a real key (fixture only).
const azureKeyBody = "odJFCrnl2edlBDdz1C5Jau2RJtBRnlWmTSHf6pWkLUyifDLkDmWJ6UuVTAIjvFu7WICPhDeOZIiBOB/Y6sHrFH=="

func TestScanContent_ADD278ContentDetectors(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantHit  bool
		wantName string
	}{
		{
			name:     "bearer token",
			content:  "curl -H \"Authorization: Bearer abcdEFGH1234567890_-.~\" https://api.example.com",
			wantHit:  true,
			wantName: "bearer_token",
		},
		{
			name:     "private key header RSA",
			content:  "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----\n",
			wantHit:  true,
			wantName: "private_key_header",
		},
		{
			name:     "private key header OPENSSH",
			content:  "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEA...\n-----END OPENSSH PRIVATE KEY-----\n",
			wantHit:  true,
			wantName: "private_key_header",
		},
		{
			name:     "github personal access token",
			content:  "GITHUB_TOKEN=ghp_1234567890abcdefghijklmnopqrstuvwxyz12",
			wantHit:  true,
			wantName: "github_token",
		},
		{
			name:     "github fine-grained pat",
			content:  "token: github_pat_11ABCDEFG0123456789abcdefghijklmnopqrstuvwxyz",
			wantHit:  true,
			wantName: "github_token",
		},
		{
			name:     "openai token",
			content:  "OPENAI_API_KEY=sk-1234567890abcdefghijklmnopqrstuv",
			wantHit:  true,
			wantName: "openai_token",
		},
		{
			name:     "anthropic token",
			content:  "ANTHROPIC_API_KEY=sk-ant-api03-1234567890abcdefghijklmnop",
			wantHit:  true,
			wantName: "anthropic_token",
		},
		{
			name:     "azure storage key",
			content:  "DefaultEndpointsProtocol=https;AccountName=foo;AccountKey=" + azureKeyBody + ";EndpointSuffix=core.windows.net",
			wantHit:  true,
			wantName: "azure_storage_key",
		},
		{
			name:     "jwt-like token",
			content:  "Set-Cookie: session=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			wantHit:  true,
			wantName: "jwt_like",
		},
		{
			name:     "password connection string url form",
			content:  "DATABASE_URL=postgres://myuser:sup3rSecr3t@db.example.com:5432/mydb",
			wantHit:  true,
			wantName: "password_connection_string",
		},
		{
			name:     "password key=value form",
			content:  `password = "hunter2222"`,
			wantHit:  true,
			wantName: "password_connection_string",
		},
		{
			name:     "pwd abbreviation form",
			content:  "pwd: correcthorsebatterystaple",
			wantHit:  true,
			wantName: "password_connection_string",
		},
		{
			name:    "ordinary prose, no secret",
			content: "This is just a README describing how the build works. No credentials here.",
			wantHit: false,
		},
		{
			name:    "empty password placeholder not flagged",
			content: `password=""`,
			wantHit: false,
		},
		{
			name:    "bearer word alone without token material",
			content: "The bearer of this document must sign below.",
			wantHit: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := redact.ScanContent([]byte(c.content))
			if c.wantHit && len(findings) == 0 {
				t.Fatalf("expected a finding for %q, got none", c.name)
			}
			if !c.wantHit && len(findings) != 0 {
				t.Fatalf("expected no finding for %q, got %+v", c.name, findings)
			}
			if c.wantHit {
				found := false
				for _, f := range findings {
					if f.Detector == c.wantName {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected detector %q among findings %+v", c.wantName, findings)
				}
			}
		})
	}
}

func TestScanContent_MultipleDetectorsInOneFile(t *testing.T) {
	content := strings.Join([]string{
		"GITHUB_TOKEN=ghp_1234567890abcdefghijklmnopqrstuvwxyz12",
		"OPENAI_API_KEY=sk-1234567890abcdefghijklmnopqrstuv",
		"DATABASE_URL=postgres://myuser:sup3rSecr3t@db.example.com:5432/mydb",
	}, "\n")

	findings := redact.ScanContent([]byte(content))
	if len(findings) < 3 {
		t.Fatalf("expected at least 3 findings for a file with 3 distinct secret shapes, got %d: %+v", len(findings), findings)
	}
}

func TestDetectors_ReturnsIndependentCopy(t *testing.T) {
	a := redact.Detectors()
	if len(a) == 0 {
		t.Fatal("expected a non-empty detector list")
	}
	a[0].Name = "mutated"
	b := redact.Detectors()
	if b[0].Name == "mutated" {
		t.Fatal("Detectors must return an independent copy")
	}
}

func TestDetector_FindDoesNotPanicOnEmptyContent(t *testing.T) {
	for _, d := range redact.Detectors() {
		if _, _, ok := d.Find(nil); ok {
			t.Fatalf("detector %s unexpectedly matched empty content", d.Name)
		}
	}
}
