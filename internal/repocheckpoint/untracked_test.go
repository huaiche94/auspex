// untracked_test.go: checkpoint-b06's assigned hardening of the untracked
// archive policy (agents/checkpoint.md Part B deliverable #6: "Safe
// untracked archive policy with size/path/secret filters"). checkpoint-b04
// shipped the structural half (size/path/symlink caps, security_test.go);
// this file proves the "secret filters" half now wired into
// buildUntrackedArchive via internal/redact.
//
// Test names all carry "Untracked" so this node's own DAG validation
// command, `go test ./internal/repocheckpoint/... ./internal/redact/...
// -run Untracked`, selects exactly this file (plus internal/redact's own
// suite, matched by the second package in that command).
package repocheckpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
)

// zipContains reports whether a zip archive at path contains an entry
// named want.
func zipContains(t *testing.T, path, want string) bool {
	t.Helper()
	names, err := mustOpenZipNoFatal(path)
	if err != nil {
		// No archive at all (nothing was included) - definitionally does
		// not contain want.
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("open zip %s: %v", path, err)
	}
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestCapture_Untracked_SecretFilename_ExcludedByDefault is the required
// "secret-like file exclusion" test (by filename): a .env file among
// otherwise-ordinary untracked files must never end up in untracked.zip,
// and must be recorded in the skip ledger with the specific
// secret_filename reason.
func TestCapture_Untracked_SecretFilename_ExcludedByDefault(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write(".env", "DATABASE_PASSWORD=hunter2222\n")
	rb.write("notes.txt", "ordinary untracked content\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-1", "untracked.zip")
	if zipContains(t, zipPath, ".env") {
		t.Fatal(".env must never appear in untracked.zip")
	}
	if !zipContains(t, zipPath, "notes.txt") {
		t.Fatal("expected notes.txt (not secret-shaped) to be archived normally")
	}

	if result.Manifest.Recoverability.Level != repocheckpoint.RecoverabilityPartial {
		t.Fatalf("expected partial recoverability (a required-policy skip occurred), got %s", result.Manifest.Recoverability.Level)
	}

	skippedPath := filepath.Join(artifactsRoot, "cp-1", "skipped-files.json")
	skippedJSON, err := os.ReadFile(skippedPath)
	if err != nil {
		t.Fatalf("read skipped-files.json: %v", err)
	}
	if !containsSubstring(string(skippedJSON), `"secret_filename"`) {
		t.Fatalf("expected skipped-files.json to record secret_filename reason, got: %s", skippedJSON)
	}
	if !containsSubstring(string(skippedJSON), ".env") {
		t.Fatalf("expected skipped-files.json to name .env, got: %s", skippedJSON)
	}
}

// TestCapture_Untracked_SecretContent_ExcludedByDefault is the required
// "secret-like file exclusion" test (by content): an ordinarily-named file
// whose CONTENT matches a known secret shape must also be excluded, not
// just files with a suspicious name.
func TestCapture_Untracked_SecretContent_ExcludedByDefault(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("config.local.txt", "github_token: ghp_1234567890abcdefghijklmnopqrstuvwxyz12\n")
	rb.write("readme.txt", "This file has no secrets in it at all.\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-1", "untracked.zip")
	if zipContains(t, zipPath, "config.local.txt") {
		t.Fatal("config.local.txt contains a GitHub-token-shaped string and must not be archived")
	}
	if !zipContains(t, zipPath, "readme.txt") {
		t.Fatal("expected readme.txt (no secret content) to be archived normally")
	}

	skippedPath := filepath.Join(artifactsRoot, "cp-1", "skipped-files.json")
	skippedJSON, err := os.ReadFile(skippedPath)
	if err != nil {
		t.Fatalf("read skipped-files.json: %v", err)
	}
	if !containsSubstring(string(skippedJSON), `"secret_content"`) {
		t.Fatalf("expected skipped-files.json to record secret_content reason, got: %s", skippedJSON)
	}
	_ = result
}

// TestCapture_Untracked_MultipleSecretShapes_AllExcluded exercises several
// distinct ADD §27.8 content-detector shapes in one capture, proving the
// integration is not narrowly tuned to only one pattern.
func TestCapture_Untracked_MultipleSecretShapes_AllExcluded(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("a-private.key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\n-----END RSA PRIVATE KEY-----\n")
	rb.write("b-conn.txt", "DATABASE_URL=postgres://myuser:sup3rSecr3t@db.example.com:5432/mydb\n")
	rb.write("c-clean.txt", "nothing sensitive here\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-1", "untracked.zip")
	if zipContains(t, zipPath, "a-private.key") {
		t.Fatal("a-private.key must be excluded (matches *.key filename pattern)")
	}
	if zipContains(t, zipPath, "b-conn.txt") {
		t.Fatal("b-conn.txt must be excluded (matches password_connection_string content detector)")
	}
	if !zipContains(t, zipPath, "c-clean.txt") {
		t.Fatal("c-clean.txt has no secret shape and must be archived normally")
	}
}

// TestCapture_Untracked_NoSecrets_FullRecoverability proves the negative:
// a capture with genuinely no secret-shaped untracked files gets
// Recoverability.Level == complete, not accidentally downgraded.
func TestCapture_Untracked_NoSecrets_FullRecoverability(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("ordinary.txt", "just some notes\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if result.Manifest.Recoverability.Level != repocheckpoint.RecoverabilityComplete {
		t.Fatalf("expected complete recoverability with no secret-shaped files present, got %s", result.Manifest.Recoverability.Level)
	}
}

// TestCapture_Untracked_DisableSecretScan_OptOutHonored proves
// CaptureOptions.DisableSecretScan is a real, honored escape hatch (never
// silently ignored) — while also proving the DEFAULT (zero value) is
// scanning ENABLED, per this option's own documented "safe by default"
// contract.
func TestCapture_Untracked_DisableSecretScan_OptOutHonored(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write(".env", "SECRET=value\n")

	client := gitx.NewClient(gitx.ExecRunner{})

	// Default options: scanning enabled, .env excluded.
	defaultRoot := t.TempDir()
	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, defaultRoot, "cp-default"), repocheckpoint.CaptureOptions{}); err != nil {
		t.Fatalf("Capture (default options): %v", err)
	}
	if zipContains(t, filepath.Join(defaultRoot, "cp-default", "untracked.zip"), ".env") {
		t.Fatal("expected .env excluded under default options (scanning enabled)")
	}

	// Explicit opt-out: scanning disabled, .env included.
	optOutRoot := t.TempDir()
	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, optOutRoot, "cp-optout"), repocheckpoint.CaptureOptions{DisableSecretScan: true}); err != nil {
		t.Fatalf("Capture (DisableSecretScan): %v", err)
	}
	if !zipContains(t, filepath.Join(optOutRoot, "cp-optout", "untracked.zip"), ".env") {
		t.Fatal("expected .env to be archived when DisableSecretScan is explicitly set")
	}
}

// TestCapture_Untracked_SecretScan_AlsoAppliesToTrackedDiffContent used to
// document a deliberate scope boundary ("the secret scan applies to the
// UNTRACKED archive only") — that boundary was in fact a real gap,
// independently confirmed by this file's own former note and qa-05's
// leakage-scanner integration test
// (internal/integrationtest/leakage_scanner_test.go,
// TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered). The
// corrective fix (patchredact.go) extends the secret scan to staged/
// unstaged patch content: a secret-shaped string staged into an
// already-tracked file must no longer survive verbatim into
// staged.patch.gz. This test now proves the closed gap instead of the open
// one.
func TestCapture_Untracked_SecretScan_AlsoAppliesToTrackedDiffContent(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("tracked-secret.txt", "placeholder\n")
	rb.git("add", "tracked-secret.txt")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("tracked-secret.txt", "github_token: ghp_1234567890abcdefghijklmnopqrstuvwxyz12\n")
	rb.git("add", "tracked-secret.txt")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// staged.patch.gz must still exist (the changeset's evidence is not
	// discarded) but must NOT carry the secret-shaped content verbatim —
	// the corrective fix redacts the offending added-line body in place
	// rather than dropping the whole patch.
	patchGzPath := filepath.Join(artifactsRoot, "cp-1", "staged.patch.gz")
	if _, err := os.Stat(patchGzPath); err != nil {
		t.Fatalf("expected staged.patch.gz to exist: %v", err)
	}
	patch := readGzip(t, patchGzPath)
	if strings.Contains(patch, "ghp_1234567890abcdefghijklmnopqrstuvwxyz12") {
		t.Fatalf("expected the secret-shaped token to be redacted from staged.patch.gz, got: %s", patch)
	}
	if !strings.Contains(patch, "tracked-secret.txt") {
		t.Fatalf("expected the patch to still reference the changed file, got: %s", patch)
	}
	if !strings.Contains(patch, "REDACTED") {
		t.Fatalf("expected a redaction placeholder in the patch, got: %s", patch)
	}
}

func containsSubstring(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
