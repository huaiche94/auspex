package redact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/preflight/internal/redact"
)

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

func TestScanPath_FilenameMatch_NoContentScanNeeded(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".env", []byte("SOME_VAR=ordinary_value_no_secret_shape"))

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if !result.Matched() {
		t.Fatalf("expected a filename-pattern match for .env, got %+v", result)
	}
	foundFilename := false
	for _, f := range result.Findings {
		if f.Detector == "filename" {
			foundFilename = true
		}
	}
	if !foundFilename {
		t.Fatalf("expected a 'filename' finding, got %+v", result.Findings)
	}
}

func TestScanPath_ContentMatch_OrdinaryFilename(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.go", []byte(`const token = "ghp_1234567890abcdefghijklmnopqrstuvwxyz12"`))

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if !result.Matched() {
		t.Fatalf("expected a content-pattern match, got %+v", result)
	}
}

func TestScanPath_BothFilenameAndContentMatch(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "secrets.yaml", []byte("github_token: ghp_1234567890abcdefghijklmnopqrstuvwxyz12\n"))

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if len(result.Findings) < 2 {
		t.Fatalf("expected both a filename and a content finding, got %+v", result.Findings)
	}
}

func TestScanPath_CleanFile_NoMatch(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "README.md", []byte("# Project\n\nThis project does things.\n"))

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if result.Matched() {
		t.Fatalf("expected no match for a clean file, got %+v", result.Findings)
	}
}

func TestScanPath_BinaryContent_ContentNotScanned(t *testing.T) {
	dir := t.TempDir()
	// Binary content that ALSO happens to contain a byte sequence
	// resembling "ghp_" - proves binary detection short-circuits content
	// scanning rather than coincidentally still matching.
	data := append([]byte("ghp_ignored-inside-binary-blob-1234567890"), 0x00, 0x01, 0x02, 0xff, 0xfe)
	path := writeFile(t, dir, "asset.bin", data)

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if result.Matched() {
		t.Fatalf("expected binary content to be skipped for content scanning, got %+v", result.Findings)
	}
}

func TestScanPath_NonexistentFile_ReturnsError(t *testing.T) {
	_, err := redact.ScanPath(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if err == nil {
		t.Fatal("expected an error for a nonexistent file")
	}
}

func TestScanPath_EmptyFile_NoMatchNoError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "empty.txt", []byte{})

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if result.Matched() {
		t.Fatalf("expected no match for an empty file, got %+v", result.Findings)
	}
}

func TestScanPath_ContentBeyondCap_NotScanned(t *testing.T) {
	dir := t.TempDir()
	// Padding of ordinary content past the cap, with the secret appended
	// only after MaxContentScanBytes - documents (and locks in) the
	// capped-scan boundary described in scan.go/doc.go: content beyond the
	// cap is not scanned, so this secret is NOT expected to be found.
	padding := make([]byte, redact.MaxContentScanBytes+1024)
	for i := range padding {
		padding[i] = 'a'
	}
	content := append(padding, []byte("\nghp_1234567890abcdefghijklmnopqrstuvwxyz12\n")...)
	path := writeFile(t, dir, "huge.txt", content)

	result, err := redact.ScanPath(path)
	if err != nil {
		t.Fatalf("ScanPath: %v", err)
	}
	if result.Matched() {
		t.Fatalf("expected content beyond MaxContentScanBytes to NOT be scanned (documented boundary), got %+v", result.Findings)
	}
}

func TestIsLikelyBinary(t *testing.T) {
	if redact.IsLikelyBinary([]byte("plain ordinary text, no NUL bytes here")) {
		t.Fatal("expected plain text to not be classified as binary")
	}
	if !redact.IsLikelyBinary([]byte{'a', 'b', 0x00, 'c'}) {
		t.Fatal("expected content containing a NUL byte to be classified as binary")
	}
	if redact.IsLikelyBinary(nil) {
		t.Fatal("expected empty content to not be classified as binary")
	}
}
