// Package gitx provides the Git plumbing layer for the Repository
// Checkpoint sub-component (checkpoint role, Part B): repository/worktree
// resolution and `git status --porcelain=v2 -z` invocation and parsing.
//
// Every Git invocation goes through domain.ProcessRunner with argv only —
// this package never builds or executes a shell command string
// (Constitution §7 rule 5).
package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/huaiche94/auspex/internal/domain"
)

// Client wraps a domain.ProcessRunner with Git-specific operations.
type Client struct {
	runner domain.ProcessRunner
	gitBin string
}

// NewClient returns a Client that invokes the `git` binary found on PATH
// through the supplied ProcessRunner.
func NewClient(runner domain.ProcessRunner) *Client {
	return &Client{runner: runner, gitBin: "git"}
}

// Status runs `git status --porcelain=v2 -z` in the given worktree directory
// and parses the result. Flags are pinned so output is deterministic
// regardless of user configuration:
//
//   - --untracked-files=all: untracked files are listed individually, never
//     collapsed into a directory entry (a checkpoint fingerprint needs paths,
//     not directory summaries);
//   - --find-renames: rename detection is on even if status.renames=false in
//     the user's config;
//   - --branch: branch headers are included so callers get HEAD/OID identity
//     from the same atomic status read.
func (c *Client) Status(ctx context.Context, worktreeDir string) (Status, error) {
	res, err := c.run(ctx, worktreeDir,
		"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--find-renames")
	if err != nil {
		return Status{}, err
	}
	if res.ExitCode != 0 {
		return Status{}, gitExitError("git status", res)
	}
	return ParsePorcelainV2(res.Stdout)
}

// run invokes git with argv-only arguments via the ProcessRunner. A non-nil
// error means git could not be executed at all (mapped to
// domain.ErrCodeUnavailable); non-zero exits are returned in the result for
// the caller to interpret.
func (c *Client) run(ctx context.Context, dir string, args ...string) (domain.ProcessResult, error) {
	res, err := c.runner.Run(ctx, dir, c.gitBin, args...)
	if err != nil {
		return res, &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   fmt.Sprintf("gitx: failed to execute git %s: %v", strings.Join(args, " "), err),
			Retryable: true,
		}
	}
	return res, nil
}

// gitExitError maps a non-zero git exit into the frozen domain error shape.
func gitExitError(op string, res domain.ProcessResult) error {
	return &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   fmt.Sprintf("gitx: %s exited %d: %s", op, res.ExitCode, strings.TrimSpace(string(res.Stderr))),
		Retryable: false,
		Details: map[string]string{
			"exit_code": fmt.Sprintf("%d", res.ExitCode),
		},
	}
}
