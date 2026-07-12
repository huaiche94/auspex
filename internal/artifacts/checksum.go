package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// ChecksumMatchesValidator checks that the SHA-256 digest of
// Candidate.Path's actual content equals Candidate.ExpectedSHA256
// (case-insensitive hex comparison; the artifacts.sha256 column and every
// digest this codebase produces are lowercase hex, but comparison here does
// not assume the caller normalized case first). This is the check behind
// "checksum matches" (agents/checkpoint.md Part A deliverable #3) and the
// one that makes "agent says complete" insufficient on its own — the file's
// bytes must actually hash to the value recorded as evidence.
type ChecksumMatchesValidator struct{}

// Kind returns this validator's stable identifier.
func (ChecksumMatchesValidator) Kind() string { return "checksum_matches" }

// Validate reads Candidate.Path and compares its SHA-256 digest against
// Candidate.ExpectedSHA256.
func (ChecksumMatchesValidator) Validate(_ context.Context, c Candidate) (Result, error) {
	if c.Path == "" {
		return Failed("checksum_matches: candidate has no Path"), nil
	}
	if c.ExpectedSHA256 == "" {
		return Failed("checksum_matches: candidate has no ExpectedSHA256"), nil
	}

	f, err := os.Open(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Failed(fmt.Sprintf("checksum_matches: %s does not exist", c.Path)), nil
		}
		return Result{}, fmt.Errorf("artifacts: checksum_matches: open %s: %w", c.Path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Result{}, fmt.Errorf("artifacts: checksum_matches: read %s: %w", c.Path, err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	expected := strings.ToLower(c.ExpectedSHA256)

	if actual != expected {
		return Failed(fmt.Sprintf("checksum_matches: %s sha256 mismatch: expected %s, got %s", c.Path, expected, actual)), nil
	}
	return Passed, nil
}

var _ Validator = ChecksumMatchesValidator{}
