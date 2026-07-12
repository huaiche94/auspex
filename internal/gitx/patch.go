package gitx

import (
	"context"
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
