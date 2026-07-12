package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/huaiche94/preflight/internal/domain"
)

// NumstatEntry is one parsed record of `git diff --numstat -z`.
type NumstatEntry struct {
	// Added and Deleted are line counts. They are 0 and meaningless when
	// Binary is true (git reports "-" for both sides of a binary change).
	Added   int
	Deleted int
	// Binary is true when git could not compute line counts because the
	// change is binary.
	Binary bool
	// Path is the current path of the changed file. OrigPath is the
	// rename/copy source path, set only for rename/copy records.
	Path     string
	OrigPath string
}

// DiffNumstat runs `git diff --numstat -z` in the given worktree directory
// and parses the result. With cached=true it reports index-vs-HEAD changes
// (`--cached`); with cached=false it reports worktree-vs-index changes.
//
// Flags are pinned so output is deterministic regardless of user
// configuration:
//
//   - --no-ext-diff: external diff drivers are never invoked (ADD §19.4);
//   - --find-renames: rename detection is on even if diff.renames is
//     disabled in the user's config, matching Client.Status.
//
// On an unborn branch (no commits yet), --cached diffs against the empty
// tree, so a staged-but-never-committed repository still reports its staged
// paths.
func (c *Client) DiffNumstat(ctx context.Context, worktreeDir string, cached bool) ([]NumstatEntry, error) {
	args := []string{"diff", "--numstat", "-z", "--no-ext-diff", "--find-renames"}
	if cached {
		args = append(args, "--cached")
	}
	res, err := c.run(ctx, worktreeDir, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitExitError("git diff --numstat", res)
	}
	return ParseNumstatZ(res.Stdout)
}

// ParseNumstatZ parses NUL-terminated `git diff --numstat -z` output.
//
// Record shapes (NUL-separated tokens):
//
//	<added> TAB <deleted> TAB <path>                    ordinary change
//	<added> TAB <deleted> TAB \x00 <src> \x00 <dst>     rename/copy
//
// where <added>/<deleted> are decimal line counts, or "-" for both sides of
// a binary change. Like ParsePorcelainV2, this parser feeds the Repository
// Checkpoint integrity boundary, so any record it does not recognize is
// rejected (fail closed) rather than silently skipped.
func ParseNumstatZ(out []byte) ([]NumstatEntry, error) {
	if len(out) == 0 {
		return nil, nil
	}

	var entries []NumstatEntry
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "" {
			if i == len(tokens)-1 {
				break // trailing NUL terminator
			}
			return nil, numstatError("empty record before end of output")
		}

		parts := strings.SplitN(tok, "\t", 3)
		if len(parts) != 3 {
			return nil, numstatError("malformed record: %q", tok)
		}
		e := NumstatEntry{}
		var err error
		e.Added, e.Deleted, e.Binary, err = parseNumstatCounts(parts[0], parts[1], tok)
		if err != nil {
			return nil, err
		}

		if parts[2] != "" {
			// Ordinary change: path is inline.
			e.Path = parts[2]
			entries = append(entries, e)
			continue
		}

		// Rename/copy: the counts token ends with a TAB and the source and
		// destination paths follow as their own NUL-terminated tokens.
		if i+2 >= len(tokens) || tokens[i+1] == "" || tokens[i+2] == "" {
			return nil, numstatError("rename/copy record missing NUL-separated source/destination paths: %q", tok)
		}
		e.OrigPath = tokens[i+1]
		e.Path = tokens[i+2]
		entries = append(entries, e)
		i += 2 // consumed the two path tokens
	}
	return entries, nil
}

// parseNumstatCounts parses the <added>/<deleted> fields. Git emits "-" for
// both fields of a binary change; a "-" on only one side is not a shape git
// produces and is rejected.
func parseNumstatCounts(added, deleted, tok string) (int, int, bool, error) {
	if added == "-" || deleted == "-" {
		if added != "-" || deleted != "-" {
			return 0, 0, false, numstatError("mixed binary/line-count fields in %q", tok)
		}
		return 0, 0, true, nil
	}
	a, err1 := strconv.Atoi(added)
	d, err2 := strconv.Atoi(deleted)
	if err1 != nil || err2 != nil || a < 0 || d < 0 {
		return 0, 0, false, numstatError("malformed line counts in %q", tok)
	}
	return a, d, false, nil
}

func numstatError(format string, args ...any) error {
	return &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   "gitx: numstat: " + fmt.Sprintf(format, args...),
		Retryable: false,
	}
}
