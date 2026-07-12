package artifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/preflight/internal/artifacts"
)

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestChecksumMatches_CorrectDigest_Passes(t *testing.T) {
	content := "Preflight checkpoint artifact content."
	path := writeFixture(t, content)

	v := artifacts.ChecksumMatchesValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:           path,
		ExpectedSHA256: sha256Hex(content),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got: %v", res.Reasons)
	}
}

func TestChecksumMatches_CaseInsensitive_Passes(t *testing.T) {
	content := "Preflight checkpoint artifact content."
	path := writeFixture(t, content)

	v := artifacts.ChecksumMatchesValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:           path,
		ExpectedSHA256: strings.ToUpper(sha256Hex(content)),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass with uppercase hex digest, got: %v", res.Reasons)
	}
}

func TestChecksumMatches_WrongDigest_Rejected(t *testing.T) {
	path := writeFixture(t, "actual content")

	v := artifacts.ChecksumMatchesValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:           path,
		ExpectedSHA256: sha256Hex("different content"),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for mismatched digest")
	}
}

func TestChecksumMatches_MissingFile_Rejected(t *testing.T) {
	v := artifacts.ChecksumMatchesValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:           fixturePath(t, "does-not-exist.md"),
		ExpectedSHA256: sha256Hex("anything"),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for missing file")
	}
}

func TestChecksumMatches_EmptyExpected_Rejected(t *testing.T) {
	path := writeFixture(t, "content")
	v := artifacts.ChecksumMatchesValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path, ExpectedSHA256: ""})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for empty ExpectedSHA256")
	}
}

func TestChecksumMatches_RealADDFixture(t *testing.T) {
	path := fixturePath(t, "add-section-18-valid.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	v := artifacts.ChecksumMatchesValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:           path,
		ExpectedSHA256: sha256Hex(string(content)),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass against real fixture, got: %v", res.Reasons)
	}
}

func TestChecksumMatches_Kind(t *testing.T) {
	if got := (artifacts.ChecksumMatchesValidator{}).Kind(); got != "checksum_matches" {
		t.Fatalf("expected Kind() = checksum_matches, got %s", got)
	}
}
