package artifacts

import (
	"context"
	"fmt"
	"os"
)

// FileExistsValidator checks that Candidate.Path names a regular file that
// actually exists and is readable. It is the minimal evidence check: every
// other file-backed validator in this package (ChecksumMatches,
// HeadingExists, FenceBalance) implicitly requires this to pass first, but
// each still performs its own os.Stat/Open rather than depending on this
// validator having run — validators in this package are independently
// callable, not a pipeline with implicit ordering.
type FileExistsValidator struct{}

// Kind returns this validator's stable identifier, stored as
// artifacts.validator_id.
func (FileExistsValidator) Kind() string { return "file_exists" }

// Validate reports whether Candidate.Path exists and is a regular file (not
// a directory, not a symlink to a missing target, not a special file).
func (FileExistsValidator) Validate(_ context.Context, c Candidate) (Result, error) {
	if c.Path == "" {
		return Failed("file_exists: candidate has no Path"), nil
	}
	info, err := os.Stat(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Failed(fmt.Sprintf("file_exists: %s does not exist", c.Path)), nil
		}
		return Result{}, fmt.Errorf("artifacts: file_exists: stat %s: %w", c.Path, err)
	}
	if info.IsDir() {
		return Failed(fmt.Sprintf("file_exists: %s is a directory, not a file", c.Path)), nil
	}
	if !info.Mode().IsRegular() {
		return Failed(fmt.Sprintf("file_exists: %s is not a regular file", c.Path)), nil
	}
	return Passed, nil
}

var _ Validator = FileExistsValidator{}
