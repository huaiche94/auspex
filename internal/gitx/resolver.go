package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huaiche94/auspex/internal/domain"
)

// RepoInfo describes how a filesystem path maps onto a Git repository.
type RepoInfo struct {
	// WorktreeRoot is the absolute top-level directory of the working tree
	// that contains the resolved path.
	WorktreeRoot string
	// GitDir is the absolute git directory serving this worktree. For the
	// main worktree this is `<root>/.git`; for a linked worktree it is
	// `<commondir>/worktrees/<name>`.
	GitDir string
	// CommonDir is the absolute git common directory shared by all
	// worktrees of the repository (object store, refs, config).
	CommonDir string
	// IsLinkedWorktree is true when the path resolves to a linked worktree
	// (created with `git worktree add`) rather than the main worktree.
	IsLinkedWorktree bool
}

// ResolveRepo resolves the Git repository containing path. The path may be a
// directory or a regular file anywhere inside a working tree; it does not
// need to be the repository root.
//
// Errors (frozen domain error shape):
//   - ErrCodeNotFound: the path does not exist, or is not inside any Git
//     repository;
//   - ErrCodeValidation: the path is inside a Git directory but not a work
//     tree (e.g. a bare repository or inside .git itself) — Repository
//     Checkpoint operates on worktrees only;
//   - ErrCodeUnavailable: git itself could not be executed.
//
// Requires git >= 2.31 (--path-format=absolute).
func (c *Client) ResolveRepo(ctx context.Context, path string) (RepoInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return RepoInfo{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("gitx: cannot make path absolute: %v", err),
			Retryable: false,
		}
	}

	fi, err := os.Stat(abs)
	if err != nil {
		return RepoInfo{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("gitx: path does not exist: %s", abs),
			Retryable: false,
		}
	}
	dir := abs
	if !fi.IsDir() {
		dir = filepath.Dir(abs)
	}

	res, err := c.run(ctx, dir,
		"rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return RepoInfo{}, err
	}
	if res.ExitCode != 0 {
		stderr := strings.ToLower(string(res.Stderr))
		if strings.Contains(stderr, "not a git repository") {
			return RepoInfo{}, &domain.Error{
				Code:      domain.ErrCodeNotFound,
				Message:   fmt.Sprintf("gitx: path is not inside a git repository: %s", abs),
				Retryable: false,
			}
		}
		return RepoInfo{}, gitExitError("git rev-parse", res)
	}

	lines := strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n")
	if len(lines) != 3 || lines[0] == "" || lines[1] == "" || lines[2] == "" {
		return RepoInfo{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("gitx: unexpected rev-parse output resolving %s (bare repository or inside a git directory?)", abs),
			Retryable: false,
		}
	}

	info := RepoInfo{
		WorktreeRoot: filepath.Clean(lines[0]),
		GitDir:       filepath.Clean(lines[1]),
		CommonDir:    filepath.Clean(lines[2]),
	}
	info.IsLinkedWorktree = info.GitDir != info.CommonDir
	return info, nil
}
