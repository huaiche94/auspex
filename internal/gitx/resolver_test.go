package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

func TestResolverMainWorktree(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("README.md", "hello\n")
	rb.git("add", "README.md")
	rb.git("commit", "-q", "-m", "initial")

	client := NewClient(ExecRunner{})

	// Subdirectory to prove resolution walks up to the repo root.
	sub := filepath.Join(rb.dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	info, err := client.ResolveRepo(context.Background(), sub)
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if info.WorktreeRoot != rb.dir {
		t.Errorf("WorktreeRoot = %q, want %q", info.WorktreeRoot, rb.dir)
	}
	if info.GitDir != info.CommonDir {
		t.Errorf("main worktree should have GitDir == CommonDir, got GitDir=%q CommonDir=%q", info.GitDir, info.CommonDir)
	}
	if info.IsLinkedWorktree {
		t.Errorf("main worktree should not report IsLinkedWorktree")
	}
	wantGitDir := filepath.Join(rb.dir, ".git")
	if info.GitDir != wantGitDir {
		t.Errorf("GitDir = %q, want %q", info.GitDir, wantGitDir)
	}
}

func TestResolverLinkedWorktree(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("README.md", "hello\n")
	rb.git("add", "README.md")
	rb.git("commit", "-q", "-m", "initial")

	linkedParent, err := os.MkdirTemp("", "auspex-gitx-linked-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(linkedParent) })
	linkedParent, err = filepath.EvalSymlinks(linkedParent)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	linkedDir := filepath.Join(linkedParent, "linked")

	rb.git("worktree", "add", "-q", "-b", "feature", linkedDir)

	client := NewClient(ExecRunner{})
	info, err := client.ResolveRepo(context.Background(), linkedDir)
	if err != nil {
		t.Fatalf("ResolveRepo: %v", err)
	}
	if info.WorktreeRoot != linkedDir {
		t.Errorf("WorktreeRoot = %q, want %q", info.WorktreeRoot, linkedDir)
	}
	if !info.IsLinkedWorktree {
		t.Errorf("linked worktree should report IsLinkedWorktree = true")
	}
	if info.GitDir == info.CommonDir {
		t.Errorf("linked worktree should have GitDir != CommonDir")
	}
	wantCommonDir := filepath.Join(rb.dir, ".git")
	if info.CommonDir != wantCommonDir {
		t.Errorf("CommonDir = %q, want %q", info.CommonDir, wantCommonDir)
	}
}

func TestResolverNotAGitRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "auspex-gitx-notrepo-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	client := NewClient(ExecRunner{})
	if _, err := client.runner.Run(context.Background(), dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	_, err = client.ResolveRepo(context.Background(), dir)
	if err == nil {
		t.Fatal("ResolveRepo: expected error for a non-repository path, got nil")
	}
	var domainErr *domain.Error
	if !asDomainError(err, &domainErr) {
		t.Fatalf("ResolveRepo error is not *domain.Error: %v (%T)", err, err)
	}
	if domainErr.Code != domain.ErrCodeNotFound {
		t.Errorf("Code = %q, want %q", domainErr.Code, domain.ErrCodeNotFound)
	}
}

func TestResolverNonexistentPath(t *testing.T) {
	client := NewClient(ExecRunner{})
	_, err := client.ResolveRepo(context.Background(), "/auspex-gitx-does-not-exist/nope")
	if err == nil {
		t.Fatal("ResolveRepo: expected error for a nonexistent path, got nil")
	}
	var domainErr *domain.Error
	if !asDomainError(err, &domainErr) {
		t.Fatalf("ResolveRepo error is not *domain.Error: %v (%T)", err, err)
	}
	if domainErr.Code != domain.ErrCodeNotFound {
		t.Errorf("Code = %q, want %q", domainErr.Code, domain.ErrCodeNotFound)
	}
}

// asDomainError is a small errors.As helper local to this test file that
// unwraps err into a *domain.Error, so the checks keep working even if the
// error is wrapped (e.g. via fmt.Errorf("...: %w", err)).
func asDomainError(err error, target **domain.Error) bool {
	return errors.As(err, target)
}
