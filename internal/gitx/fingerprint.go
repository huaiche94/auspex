package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"sort"
	"strconv"
)

// FingerprintSchema versions the canonical digest encoding. It is the first
// field hashed into every digest, so any future change to the encoding (new
// fields, different ordering) must bump this string — two digests are only
// comparable when they were computed under the same schema.
const FingerprintSchema = "auspex.gitx.fingerprint.v1"

// UntrackedPolicy records how untracked files were enumerated when a
// fingerprint was taken. It is part of the digest: two snapshots that
// enumerated untracked files under different policies are not comparable,
// because a difference in their entry sets could be a policy artifact rather
// than a real repository change (agents/checkpoint.md Part B, "untracked
// policy metadata").
type UntrackedPolicy struct {
	// Mode is the `--untracked-files` mode ("all": every untracked file is
	// listed individually, never collapsed into a directory summary).
	Mode string
	// IncludeIgnored is whether ignored entries were requested in the status.
	IncludeIgnored bool
	// FindRenames is whether rename detection was pinned on for both the
	// status and the numstat reads.
	FindRenames bool
}

// DefaultUntrackedPolicy is the policy pinned by Client.Status and
// Client.DiffNumstat: all untracked files listed individually, ignored files
// excluded, rename detection on.
func DefaultUntrackedPolicy() UntrackedPolicy {
	return UntrackedPolicy{Mode: "all", IncludeIgnored: false, FindRenames: true}
}

// Fingerprint is a deterministic snapshot of repository state, suitable for
// repository-checkpoint identity and change detection (ADD §19.3 capture
// steps 2/10, FR-149 resume validation). Two fingerprints with equal Digest
// values were taken over identical repository state as visible to Git:
// same worktree, same HEAD, same index/worktree status, same changed paths
// and line counts, enumerated under the same untracked policy.
//
// Digest covers: FingerprintSchema, repository identity (WorktreeRoot,
// CommonDir, IsLinkedWorktree), HeadOID, Branch, Untracked policy, Entries,
// IndexNumstat, and WorktreeNumstat. Upstream/Ahead/Behind are informational
// only and deliberately excluded: they move when remote-tracking refs are
// fetched, which does not change the local worktree/index/HEAD state that
// checkpoint comparison protects (a fetch must not invalidate a resume).
//
// A Fingerprint is assembled from multiple git invocations (status + two
// numstat reads), so it is a point-in-time read, not an atomic one. Callers
// that must detect concurrent mutation take a fingerprint before and after
// their critical section and compare digests — the ADD §19.3 capture
// protocol (checkpoint-b04/b07 scope).
type Fingerprint struct {
	// Schema is FingerprintSchema at the time the fingerprint was taken.
	Schema string

	// Repository identity (from ResolveRepo).
	WorktreeRoot     string
	CommonDir        string
	IsLinkedWorktree bool

	// HeadOID is the current commit hash, or "(initial)" on an unborn
	// branch. Branch is the current branch name, or "(detached)".
	HeadOID string
	Branch  string

	// Upstream/Ahead/Behind mirror the status branch headers. Informational:
	// NOT part of the digest (see type comment).
	Upstream       string
	Ahead, Behind  int
	HasAheadBehind bool

	// Entries is the full parsed porcelain v2 status (staged, unstaged,
	// unmerged, untracked — ignored only if the policy requested it).
	Entries []Entry

	// IndexNumstat is `git diff --cached --numstat` (index vs HEAD, or vs
	// the empty tree on an unborn branch). WorktreeNumstat is
	// `git diff --numstat` (worktree vs index).
	IndexNumstat    []NumstatEntry
	WorktreeNumstat []NumstatEntry

	// Untracked is the enumeration policy in effect for this snapshot.
	Untracked UntrackedPolicy

	// Digest is the SHA-256 hex digest of the canonical encoding of the
	// fields above (except the informational ones). It is stable across
	// processes and across the ordering of Entries/numstat slices.
	Digest string
}

// Fingerprint resolves the repository containing path and takes a snapshot
// fingerprint of its current state. The path may be anywhere inside a
// working tree (it is resolved via ResolveRepo).
func (c *Client) Fingerprint(ctx context.Context, path string) (Fingerprint, error) {
	info, err := c.ResolveRepo(ctx, path)
	if err != nil {
		return Fingerprint{}, err
	}
	st, err := c.Status(ctx, info.WorktreeRoot)
	if err != nil {
		return Fingerprint{}, err
	}
	indexNS, err := c.DiffNumstat(ctx, info.WorktreeRoot, true)
	if err != nil {
		return Fingerprint{}, err
	}
	worktreeNS, err := c.DiffNumstat(ctx, info.WorktreeRoot, false)
	if err != nil {
		return Fingerprint{}, err
	}

	fp := Fingerprint{
		Schema:           FingerprintSchema,
		WorktreeRoot:     info.WorktreeRoot,
		CommonDir:        info.CommonDir,
		IsLinkedWorktree: info.IsLinkedWorktree,
		HeadOID:          st.Branch.OID,
		Branch:           st.Branch.Head,
		Upstream:         st.Branch.Upstream,
		Ahead:            st.Branch.Ahead,
		Behind:           st.Branch.Behind,
		HasAheadBehind:   st.Branch.HasAheadBehind,
		Entries:          st.Entries,
		IndexNumstat:     indexNS,
		WorktreeNumstat:  worktreeNS,
		Untracked:        DefaultUntrackedPolicy(),
	}
	fp.Digest = fp.ComputeDigest()
	return fp, nil
}

// Equal reports whether two fingerprints describe identical repository
// state. Two zero-value fingerprints are not equal: an empty digest means
// "no fingerprint", and comparing against it must fail closed.
func (f Fingerprint) Equal(o Fingerprint) bool {
	return f.Digest != "" && f.Digest == o.Digest
}

// ComputeDigest recomputes the canonical SHA-256 digest from the
// fingerprint's digest-covered fields. It does not read or modify f.Digest,
// so a consumer (e.g. repository-checkpoint verify) can check a stored
// fingerprint's integrity with ComputeDigest() == Digest.
//
// The encoding is length-prefixed (netstring-style: "<len>:<bytes>") per
// field in a fixed order, so paths containing spaces, tabs, or newlines can
// never collide with field boundaries. Entry and numstat slices are hashed
// in a canonical sort order, independent of the order git emitted them.
func (f Fingerprint) ComputeDigest() string {
	h := sha256.New()
	ws := func(s string) { writeNetstring(h, s) }
	wi := func(n int) { ws(strconv.Itoa(n)) }
	wb := func(b bool) {
		if b {
			ws("1")
		} else {
			ws("0")
		}
	}

	ws(f.Schema)
	ws(f.WorktreeRoot)
	ws(f.CommonDir)
	wb(f.IsLinkedWorktree)
	ws(f.HeadOID)
	ws(f.Branch)
	ws(f.Untracked.Mode)
	wb(f.Untracked.IncludeIgnored)
	wb(f.Untracked.FindRenames)

	entries := sortedEntries(f.Entries)
	wi(len(entries))
	for _, e := range entries {
		ws(string(e.Kind))
		ws(string(e.Index))
		ws(string(e.Worktree))
		ws(e.Submodule)
		ws(e.ModeHead)
		ws(e.ModeIndex)
		ws(e.ModeWorktree)
		ws(e.HashHead)
		ws(e.HashIndex)
		ws(string(e.RenameOp))
		wi(e.RenameScore)
		for _, m := range e.ConflictModes {
			ws(m)
		}
		for _, ch := range e.ConflictHashes {
			ws(ch)
		}
		ws(e.Path)
		ws(e.OrigPath)
	}

	for _, ns := range [2][]NumstatEntry{f.IndexNumstat, f.WorktreeNumstat} {
		ns = sortedNumstat(ns)
		wi(len(ns))
		for _, e := range ns {
			wi(e.Added)
			wi(e.Deleted)
			wb(e.Binary)
			ws(e.Path)
			ws(e.OrigPath)
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

// writeNetstring writes "<decimal len>:<bytes>" so that no field value can
// forge a field boundary. Writes to a hash.Hash never fail.
func writeNetstring(h hash.Hash, s string) {
	_, _ = io.WriteString(h, strconv.Itoa(len(s)))
	_, _ = io.WriteString(h, ":")
	_, _ = io.WriteString(h, s)
}

// sortedEntries returns a copy of entries in canonical order: by Path, then
// OrigPath, then Kind. Git already emits sorted output, but the digest must
// not depend on that implementation detail.
func sortedEntries(in []Entry) []Entry {
	out := append([]Entry(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].OrigPath != out[j].OrigPath {
			return out[i].OrigPath < out[j].OrigPath
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// sortedNumstat returns a copy of entries in canonical order: by Path, then
// OrigPath.
func sortedNumstat(in []NumstatEntry) []NumstatEntry {
	out := append([]NumstatEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].OrigPath < out[j].OrigPath
	})
	return out
}
