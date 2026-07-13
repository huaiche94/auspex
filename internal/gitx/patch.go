package gitx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
)

// DiffPatch runs a binary-safe `git diff --binary --full-index --no-ext-diff`
// in worktreeDir and returns the raw patch bytes unmodified. With
// cached=true it captures index-vs-HEAD (`--cached`, the "staged" patch);
// with cached=false it captures worktree-vs-index (the "unstaged" patch) —
// the same two diff scopes Client.DiffNumstat already reports counts for
// (ADD §19.4's fixed command list; §19.3 capture steps 5-6).
//
// --binary makes the patch reconstructable for binary files (git apply can
// replay it); --full-index emits full blob SHAs rather than abbreviated
// ones, so the patch is unambiguous even if the abbreviation length changes
// between the capturing and a later restoring Git version; --no-ext-diff
// matches DiffNumstat's own pinning (ADD §19.4) so an external diff driver
// configured in the user's global gitconfig can never influence what a
// checkpoint captures.
//
// An empty diff (no changes in the requested scope) is not an error: it
// returns a valid, empty (or near-empty, with just no hunks) patch and a
// nil error, matching `git diff`'s own exit-0-on-no-changes behavior.
func (c *Client) DiffPatch(ctx context.Context, worktreeDir string, cached bool) ([]byte, error) {
	args := []string{"diff", "--binary", "--full-index", "--no-ext-diff"}
	if cached {
		args = append(args, "--cached")
	}
	res, err := c.run(ctx, worktreeDir, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitExitError("git diff --binary", res)
	}
	return res.Stdout, nil
}

// ApplyCheckResult is ApplyCheck's outcome: whether patch would apply
// cleanly against worktreeDir's current index/working tree, and Git's own
// diagnostic text when it would not (e.g. which hunk/file failed and why —
// surfaced verbatim so a restore dry-run report can show the operator
// exactly what Git itself found, never a paraphrase this package might get
// wrong).
type ApplyCheckResult struct {
	WouldApply bool
	Detail     string
}

// ApplyCheck runs `git apply --check` against patch (a binary-safe patch of
// the shape Client.DiffPatch produces) in worktreeDir, WITHOUT applying it
// — `--check` is documented by Git as verifying the patch applies cleanly
// while making no changes to the index or working tree, exactly the
// read-only guarantee a restore dry-run (ADD §19.6) needs: "verify checksum;
// ... git apply --check; staged/unstaged separately; ... produce report."
//
// patch is written to a private temporary file first (domain.ProcessRunner
// has no stdin parameter — argv-only process calls, Constitution §7 rule
// 5 — so `git apply --check <path>` is the only way to hand Git the patch
// content without building a shell pipeline) and removed again before this
// function returns, success or failure; the temp file is created with
// os.CreateTemp under worktreeDir's own OS temp directory (not inside the
// worktree itself, so it can never be mistaken for a real untracked file by
// a concurrent `git status`).
//
// An empty patch (no changes in that scope — DiffPatch's own documented
// "not an error" case) trivially "would apply": there is nothing to apply,
// so ApplyCheck returns WouldApply:true without invoking Git at all.
func (c *Client) ApplyCheck(ctx context.Context, worktreeDir string, patch []byte, cached bool) (ApplyCheckResult, error) {
	if len(bytes.TrimSpace(patch)) == 0 {
		return ApplyCheckResult{WouldApply: true, Detail: "empty patch: nothing to apply"}, nil
	}

	f, err := os.CreateTemp("", "auspex-restore-dryrun-*.patch")
	if err != nil {
		return ApplyCheckResult{}, err
	}
	patchPath := f.Name()
	defer func() { _ = os.Remove(patchPath) }()

	if _, err := f.Write(patch); err != nil {
		_ = f.Close()
		return ApplyCheckResult{}, err
	}
	if err := f.Close(); err != nil {
		return ApplyCheckResult{}, err
	}

	args := []string{"apply", "--check", "--binary"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, filepath.ToSlash(patchPath))

	res, err := c.run(ctx, worktreeDir, args...)
	if err != nil {
		return ApplyCheckResult{}, err
	}
	if res.ExitCode != 0 {
		return ApplyCheckResult{WouldApply: false, Detail: strings.TrimSpace(string(res.Stderr))}, nil
	}
	return ApplyCheckResult{WouldApply: true}, nil
}

// ListUntracked runs `git ls-files --others --exclude-standard -z` in
// worktreeDir and returns the untracked, non-ignored file paths relative to
// worktreeDir (ADD §19.4's fixed command list). --exclude-standard applies
// the repository's own .gitignore/.git/info/exclude/global-excludes rules,
// matching the "exclude ignored" default policy (ADD §19.5).
func (c *Client) ListUntracked(ctx context.Context, worktreeDir string) ([]string, error) {
	res, err := c.run(ctx, worktreeDir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitExitError("git ls-files", res)
	}
	return splitNulTerminated(res.Stdout), nil
}

// splitNulTerminated splits NUL-terminated git output into a slice of
// paths, dropping the trailing empty token produced by the final
// terminator.
func splitNulTerminated(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	var paths []string
	start := 0
	for i, b := range out {
		if b == 0 {
			if i > start {
				paths = append(paths, string(out[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(out) {
		paths = append(paths, string(out[start:]))
	}
	return paths
}
