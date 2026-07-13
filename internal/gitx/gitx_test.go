package gitx

// Shared test scaffolding: every Git invocation in test setup goes through
// the same argv-only domain.ProcessRunner implementation used in production
// (ExecRunner) — no shell strings, even in tests.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

// repoBuilder creates and mutates a real temporary Git repository for
// integration tests.
type repoBuilder struct {
	t      *testing.T
	dir    string
	runner domain.ProcessRunner
}

// newRepoBuilder creates a temp directory (symlink-resolved so path
// comparisons work on macOS, where /var is a symlink to /private/var) and
// initializes a Git repository with deterministic local config.
func newRepoBuilder(t *testing.T) *repoBuilder {
	t.Helper()
	rb := &repoBuilder{t: t, runner: ExecRunner{}}

	dir, err := os.MkdirTemp("", "auspex-gitx-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	rb.dir = resolved

	if _, err := rb.runner.Run(context.Background(), rb.dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	rb.git("init", "-q", "-b", "main")
	rb.git("config", "user.name", "Auspex Test")
	rb.git("config", "user.email", "test@auspex.invalid")
	rb.git("config", "commit.gpgsign", "false")
	return rb
}

// git runs a git command in the repo root and fails the test on a non-zero
// exit or execution failure.
func (rb *repoBuilder) git(args ...string) domain.ProcessResult {
	rb.t.Helper()
	return rb.gitIn(rb.dir, args...)
}

// gitIn runs a git command in an arbitrary directory.
func (rb *repoBuilder) gitIn(dir string, args ...string) domain.ProcessResult {
	rb.t.Helper()
	res, err := rb.runner.Run(context.Background(), dir, "git", args...)
	if err != nil {
		rb.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		rb.t.Fatalf("git %s: exit %d: %s", strings.Join(args, " "), res.ExitCode, res.Stderr)
	}
	return res
}

// write creates or overwrites a file at a path relative to the repo root.
func (rb *repoBuilder) write(rel, content string) {
	rb.t.Helper()
	abs := filepath.Join(rb.dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		rb.t.Fatalf("MkdirAll(%s): %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		rb.t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}

// remove deletes a file at a path relative to the repo root without telling
// Git (i.e. an unstaged deletion).
func (rb *repoBuilder) remove(rel string) {
	rb.t.Helper()
	if err := os.Remove(filepath.Join(rb.dir, rel)); err != nil {
		rb.t.Fatalf("Remove(%s): %v", rel, err)
	}
}
