package gitx

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/huaiche94/auspex/internal/domain"
)

// EntryKind identifies the record type of a porcelain v2 status entry. The
// values are the literal record-type bytes emitted by git.
type EntryKind byte

const (
	// KindChanged is an ordinary changed entry ("1").
	KindChanged EntryKind = '1'
	// KindRenamed is a renamed or copied entry ("2").
	KindRenamed EntryKind = '2'
	// KindUnmerged is an unmerged (conflict) entry ("u").
	KindUnmerged EntryKind = 'u'
	// KindUntracked is an untracked item ("?").
	KindUntracked EntryKind = '?'
	// KindIgnored is an ignored item ("!").
	KindIgnored EntryKind = '!'
)

// Unmodified is the porcelain v2 "unmodified" status code for either side of
// the XY pair.
const Unmodified byte = '.'

// BranchInfo carries the `--branch` header block of a porcelain v2 status.
type BranchInfo struct {
	// OID is the current commit hash, or "(initial)" before the first commit.
	OID string
	// Head is the current branch name, or "(detached)".
	Head string
	// Upstream is the upstream branch, empty if none is set.
	Upstream string
	// Ahead and Behind are the commit counts relative to Upstream; they are
	// only meaningful when HasAheadBehind is true.
	Ahead, Behind  int
	HasAheadBehind bool
}

// Entry is one parsed porcelain v2 status record.
type Entry struct {
	Kind EntryKind

	// Index (X) and Worktree (Y) are the staged/unstaged status codes
	// ('M', 'A', 'D', 'R', 'C', 'T', 'U', or Unmodified '.'). They are zero
	// for untracked/ignored entries, which carry no XY field.
	Index    byte
	Worktree byte

	// Submodule is the 4-character submodule state field ("N..." for a
	// non-submodule). Empty for untracked/ignored entries.
	Submodule string

	// ModeHead, ModeIndex, ModeWorktree are octal file modes in HEAD, the
	// index, and the worktree. For unmerged entries ModeWorktree is set and
	// the three conflict-stage modes live in ConflictModes.
	ModeHead     string
	ModeIndex    string
	ModeWorktree string

	// HashHead and HashIndex are the object names in HEAD and the index
	// (changed/renamed entries only).
	HashHead  string
	HashIndex string

	// RenameOp is 'R' or 'C' and RenameScore the similarity percentage, for
	// renamed/copied entries only.
	RenameOp    byte
	RenameScore int

	// ConflictModes and ConflictHashes are the stage 1-3 modes and object
	// names for unmerged entries only.
	ConflictModes  [3]string
	ConflictHashes [3]string

	// Path is the current path of the item. OrigPath is the pre-rename/copy
	// source path, set for renamed/copied entries only.
	Path     string
	OrigPath string
}

// Status is the parsed result of `git status --porcelain=v2 -z --branch`.
type Status struct {
	Branch  BranchInfo
	Entries []Entry
}

// ParsePorcelainV2 parses NUL-terminated `git status --porcelain=v2 -z`
// output. It accepts (and parses) `--branch` header records if present and
// ignores unrecognized `#` headers, per the documented forward-compatibility
// guidance. Unknown entry record types are rejected: this parser feeds the
// Repository Checkpoint integrity boundary, so an unintelligible status must
// fail closed rather than be silently dropped.
func ParsePorcelainV2(out []byte) (Status, error) {
	st := Status{}
	if len(out) == 0 {
		return st, nil
	}

	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "" {
			if i == len(tokens)-1 {
				break // trailing NUL terminator
			}
			return st, parseError("empty record before end of output")
		}
		switch tok[0] {
		case '#':
			parseBranchHeader(tok, &st.Branch)
		case byte(KindChanged):
			e, err := parseChanged(tok)
			if err != nil {
				return st, err
			}
			st.Entries = append(st.Entries, e)
		case byte(KindRenamed):
			if i+1 >= len(tokens) || tokens[i+1] == "" {
				return st, parseError("rename/copy record missing NUL-separated original path: %q", tok)
			}
			e, err := parseRenamed(tok, tokens[i+1])
			if err != nil {
				return st, err
			}
			st.Entries = append(st.Entries, e)
			i++ // consumed the origPath token
		case byte(KindUnmerged):
			e, err := parseUnmerged(tok)
			if err != nil {
				return st, err
			}
			st.Entries = append(st.Entries, e)
		case byte(KindUntracked), byte(KindIgnored):
			e, err := parsePathOnly(tok)
			if err != nil {
				return st, err
			}
			st.Entries = append(st.Entries, e)
		default:
			return st, parseError("unknown record type %q in %q", tok[0], tok)
		}
	}
	return st, nil
}

// parseBranchHeader parses one `# branch.*` header. Unrecognized or
// malformed headers are ignored (headers are informational, not integrity
// data, and git documents that parsers should skip unknown ones).
func parseBranchHeader(tok string, b *BranchInfo) {
	fields := strings.Fields(tok)
	if len(fields) < 3 || fields[0] != "#" {
		return
	}
	switch fields[1] {
	case "branch.oid":
		b.OID = fields[2]
	case "branch.head":
		b.Head = fields[2]
	case "branch.upstream":
		b.Upstream = fields[2]
	case "branch.ab":
		if len(fields) != 4 {
			return
		}
		ahead, err1 := strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
		behind, err2 := strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
		if err1 != nil || err2 != nil {
			return
		}
		b.Ahead, b.Behind, b.HasAheadBehind = ahead, behind, true
	}
}

// parseChanged parses an ordinary changed entry:
//
//	1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
func parseChanged(tok string) (Entry, error) {
	parts := strings.SplitN(tok, " ", 9)
	if len(parts) != 9 || parts[8] == "" {
		return Entry{}, parseError("malformed changed entry: %q", tok)
	}
	xy, err := parseXY(parts[1], tok)
	if err != nil {
		return Entry{}, err
	}
	if len(parts[2]) != 4 {
		return Entry{}, parseError("malformed submodule field in %q", tok)
	}
	return Entry{
		Kind:         KindChanged,
		Index:        xy[0],
		Worktree:     xy[1],
		Submodule:    parts[2],
		ModeHead:     parts[3],
		ModeIndex:    parts[4],
		ModeWorktree: parts[5],
		HashHead:     parts[6],
		HashIndex:    parts[7],
		Path:         parts[8],
	}, nil
}

// parseRenamed parses a renamed/copied entry; in -z mode the original path
// follows as its own NUL-terminated token:
//
//	2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>\x00<origPath>
func parseRenamed(tok, origPath string) (Entry, error) {
	parts := strings.SplitN(tok, " ", 10)
	if len(parts) != 10 || parts[9] == "" {
		return Entry{}, parseError("malformed rename/copy entry: %q", tok)
	}
	xy, err := parseXY(parts[1], tok)
	if err != nil {
		return Entry{}, err
	}
	if len(parts[2]) != 4 {
		return Entry{}, parseError("malformed submodule field in %q", tok)
	}
	rc := parts[8]
	if len(rc) < 2 || (rc[0] != 'R' && rc[0] != 'C') {
		return Entry{}, parseError("malformed rename/copy score %q in %q", rc, tok)
	}
	score, err := strconv.Atoi(rc[1:])
	if err != nil {
		return Entry{}, parseError("malformed rename/copy score %q in %q", rc, tok)
	}
	return Entry{
		Kind:         KindRenamed,
		Index:        xy[0],
		Worktree:     xy[1],
		Submodule:    parts[2],
		ModeHead:     parts[3],
		ModeIndex:    parts[4],
		ModeWorktree: parts[5],
		HashHead:     parts[6],
		HashIndex:    parts[7],
		RenameOp:     rc[0],
		RenameScore:  score,
		Path:         parts[9],
		OrigPath:     origPath,
	}, nil
}

// parseUnmerged parses an unmerged (conflict) entry:
//
//	u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
func parseUnmerged(tok string) (Entry, error) {
	parts := strings.SplitN(tok, " ", 11)
	if len(parts) != 11 || parts[10] == "" {
		return Entry{}, parseError("malformed unmerged entry: %q", tok)
	}
	xy, err := parseXY(parts[1], tok)
	if err != nil {
		return Entry{}, err
	}
	if len(parts[2]) != 4 {
		return Entry{}, parseError("malformed submodule field in %q", tok)
	}
	return Entry{
		Kind:           KindUnmerged,
		Index:          xy[0],
		Worktree:       xy[1],
		Submodule:      parts[2],
		ConflictModes:  [3]string{parts[3], parts[4], parts[5]},
		ModeWorktree:   parts[6],
		ConflictHashes: [3]string{parts[7], parts[8], parts[9]},
		Path:           parts[10],
	}, nil
}

// parsePathOnly parses an untracked ("? <path>") or ignored ("! <path>")
// entry.
func parsePathOnly(tok string) (Entry, error) {
	if len(tok) < 3 || tok[1] != ' ' {
		return Entry{}, parseError("malformed untracked/ignored entry: %q", tok)
	}
	return Entry{Kind: EntryKind(tok[0]), Path: tok[2:]}, nil
}

func parseXY(xy, tok string) ([2]byte, error) {
	if len(xy) != 2 {
		return [2]byte{}, parseError("malformed XY field %q in %q", xy, tok)
	}
	return [2]byte{xy[0], xy[1]}, nil
}

func parseError(format string, args ...any) error {
	return &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   "gitx: porcelain v2: " + fmt.Sprintf(format, args...),
		Retryable: false,
	}
}
