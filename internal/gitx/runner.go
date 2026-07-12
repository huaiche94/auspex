package gitx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"

	"github.com/huaiche94/preflight/internal/domain"
)

// ExecRunner is the exec.Command-backed implementation of
// domain.ProcessRunner. It executes argv directly and never constructs or
// invokes a shell command string (Constitution §7 rule 5, ADD Git-safety
// principle).
//
// Contract:
//   - A process that starts and exits (zero or non-zero) is NOT an error at
//     this layer; the exit code is reported in ProcessResult and interpreting
//     it is the caller's concern.
//   - A non-nil error means the process could not be run to completion at
//     all: binary not found, permission denied, or context
//     cancellation/timeout. On context cancellation the context's error is
//     returned so callers can distinguish it with errors.Is.
type ExecRunner struct{}

var _ domain.ProcessRunner = ExecRunner{}

// Run executes name with args in dir, capturing stdout and stderr.
func (ExecRunner) Run(ctx context.Context, dir string, name string, args ...string) (domain.ProcessResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result := domain.ProcessResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}
	if runErr == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.ExitCode = -1
		return result, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, runErr
}
