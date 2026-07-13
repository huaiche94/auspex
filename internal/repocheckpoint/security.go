// security.go: the shared security guards this package applies throughout
// capture (agents/checkpoint.md Part B "Security requirements"): reject
// path traversal and symlink escape, never include .git internals, cap
// artifact size and file count. These are pure, dependency-free checks so
// they are trivially unit-testable in isolation and impossible to
// accidentally bypass by forgetting to call them from a specific call site
// (every path that lands in an archive or a manifest goes through
// validateUntrackedPath first).
package repocheckpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huaiche94/auspex/internal/domain"
)

// DefaultMaxFileBytes is the untracked-file-inclusion cap (ADD §19.5: "per-
// file <= 5 MiB").
const DefaultMaxFileBytes = 5 * 1024 * 1024

// DefaultMaxTotalBytes is the untracked-archive-inclusion cap (ADD §19.5:
// "total <= 100 MiB").
const DefaultMaxTotalBytes = 100 * 1024 * 1024

// DefaultMaxFileCount caps the number of untracked files a single
// checkpoint will archive, independent of their total byte size (a large
// number of tiny files is still a resource-exhaustion vector this package
// must bound explicitly, per the Part B security requirement "cap artifact
// size and file count").
const DefaultMaxFileCount = 10000

// SkipReason explains why validateUntrackedPath (or the archive loop that
// calls it) declined to include a candidate untracked path, feeding
// Manifest.Recoverability.Warnings and the skipped-files record.
type SkipReason string

const (
	SkipPathTraversal SkipReason = "path_traversal"
	SkipSymlink       SkipReason = "symlink"
	SkipGitInternal   SkipReason = "git_internal"
	SkipOversizeFile  SkipReason = "oversize_file"
	SkipTotalCapped   SkipReason = "total_size_capped"
	SkipFileCountCap  SkipReason = "file_count_capped"
	SkipUnreadable    SkipReason = "unreadable"
	// SkipSecretFilename and SkipSecretContent are checkpoint-b06's
	// extension of this ledger (internal/redact): a candidate untracked
	// file whose name matches Auspex_ADD.md §27.8's exact name-pattern
	// list, or whose content matches one of internal/redact's content
	// detectors, is never archived by default. Two distinct reasons (not
	// one generic "secret") so a caller/operator can tell "we recognized
	// this AS a secrets file by name" from "we found a secret-shaped
	// string inside an otherwise ordinary file" — the two have different
	// false-positive profiles and a user auditing skipped-files.json
	// benefits from knowing which happened.
	SkipSecretFilename SkipReason = "secret_filename"
	SkipSecretContent  SkipReason = "secret_content"
)

// validateUntrackedPath applies the path-safety checks every untracked
// candidate path must pass before its content is read into an archive:
//
//   - the resolved absolute path must stay within worktreeRoot (rejects
//     `../` traversal and any git-reported path that escapes the tree);
//   - the path must not enter a `.git` directory anywhere in its chain
//     (git ls-files --exclude-standard should never emit one, but this is
//     a defense-in-depth check, not a trust of that guarantee);
//   - the path must not be, or resolve through, a symlink (ADD §19.5 "no
//     symlink follow" — this package never dereferences a symlink to
//     capture its target's content, and a symlink entry itself is treated
//     as a skip, not archived as a special file type, for this first
//     working implementation; b06 owns the fuller untracked archive
//     policy and may add a documented symlink-as-metadata mode later).
//
// rel is the path as reported by `git ls-files` (worktree-relative,
// forward-slash separated, exactly as gitx.ListUntracked returns it).
func validateUntrackedPath(worktreeRoot, rel string) (string, SkipReason, bool) {
	if rel == "" {
		return "", SkipUnreadable, false
	}

	// Reject any path containing a literal ".." segment outright, before
	// even touching the filesystem — this is the traversal check that
	// must hold even if the path does not exist yet.
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", SkipPathTraversal, false
		}
		if seg == ".git" {
			return "", SkipGitInternal, false
		}
	}

	abs := filepath.Join(worktreeRoot, filepath.FromSlash(rel))
	cleanRoot := filepath.Clean(worktreeRoot)
	cleanAbs := filepath.Clean(abs)
	if cleanAbs != cleanRoot && !strings.HasPrefix(cleanAbs, cleanRoot+string(filepath.Separator)) {
		return "", SkipPathTraversal, false
	}

	// Symlink check: Lstat (not Stat) so a symlink is detected without
	// following it, satisfying "no symlink follow" even for the check
	// itself.
	info, err := os.Lstat(cleanAbs)
	if err != nil {
		return "", SkipUnreadable, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", SkipSymlink, false
	}

	// Walk every ancestor directory up to worktreeRoot and reject if any
	// is itself a symlink — a non-symlink leaf reached through a
	// symlinked parent directory still escapes the intended tree in
	// spirit (and, if the parent symlink points outside worktreeRoot, in
	// fact).
	dir := filepath.Dir(cleanAbs)
	for dir != cleanRoot && len(dir) >= len(cleanRoot) {
		dirInfo, err := os.Lstat(dir)
		if err != nil {
			return "", SkipUnreadable, false
		}
		if dirInfo.Mode()&os.ModeSymlink != 0 {
			return "", SkipSymlink, false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return cleanAbs, "", true
}

// safeArtifactPath resolves rel (a path FIELD READ FROM A MANIFEST FILE,
// e.g. Manifest.Artifacts[].Path or the fixed literal names Capture itself
// writes) against root (a checkpoint's own ArtifactRoot directory),
// rejecting anything that would resolve outside root — the same defense in
// depth validateUntrackedPath applies to git-reported untracked paths,
// applied here to manifest-declared artifact paths, which this package
// reads back from disk on every Verify/RestoreDryRun call and must
// therefore treat as untrusted input, not as "data we ourselves once wrote
// and can trust unconditionally." A manifest.json is an ordinary file on
// disk for as long as a checkpoint directory exists; nothing prevents it
// from later being hand-edited, corrupted, or restored from an untrusted
// source, so every reader of it re-validates rather than assuming the
// writer-side invariant (Capture only ever uses a small fixed set of
// literal names) still holds by the time it is read.
//
// Unlike validateUntrackedPath (which also rejects a bare ".git" segment,
// meaningful for paths inside a live Git worktree), this check is scoped to
// what matters for a path relative to an ArtifactRoot: no ".." segment, no
// absolute path, the resolved path must stay under root, and neither the
// resolved leaf nor any ancestor directory up to root may be a symlink
// (the same escape vector validateUntrackedPath defends against, applied
// here since a checkpoint's own artifact directory is not expected to ever
// legitimately contain a symlink).
func safeArtifactPath(root, rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	slashRel := filepath.ToSlash(rel)
	if filepath.IsAbs(rel) || strings.HasPrefix(slashRel, "/") {
		return "", false
	}
	for _, seg := range strings.Split(slashRel, "/") {
		if seg == ".." {
			return "", false
		}
	}

	cleanRoot := filepath.Clean(root)
	abs := filepath.Join(cleanRoot, filepath.FromSlash(rel))
	cleanAbs := filepath.Clean(abs)
	if cleanAbs != cleanRoot && !strings.HasPrefix(cleanAbs, cleanRoot+string(filepath.Separator)) {
		return "", false
	}

	// Symlink check on whatever already exists: an artifact file itself
	// being a symlink, or reached through a symlinked ancestor directory,
	// is rejected the same way validateUntrackedPath rejects both shapes.
	// A path component that does not exist yet (Lstat returns
	// IsNotExist) is not itself a traversal — the caller's own
	// os.Stat/os.ReadFile immediately after this call reports "missing"
	// exactly as it already does for any other absent artifact.
	if info, err := os.Lstat(cleanAbs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	dir := filepath.Dir(cleanAbs)
	for dir != cleanRoot && len(dir) >= len(cleanRoot) {
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return cleanAbs, true
}

// safeRelativeName reports whether rel is safe to join onto a destination
// directory without escaping it: non-empty, not absolute, and containing no
// ".." segment. Used by writeArtifactDir as defense in depth over the
// files map keys it writes (see its call site's doc comment for why this
// matters even though every current production caller only ever supplies
// fixed literal names).
func safeRelativeName(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	slashRel := filepath.ToSlash(rel)
	if strings.HasPrefix(slashRel, "/") {
		return false
	}
	for _, seg := range strings.Split(slashRel, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// errIntegrity is a small constructor for the ErrCodeIntegrity shape this
// package returns whenever a state-integrity invariant (as opposed to an
// ordinary operational failure) is violated — per CONTRACT_FREEZE.md's
// fail-open/fail-closed contract, state-integrity failures MUST fail
// closed with ErrCodeIntegrity, Retryable: false.
func errIntegrity(format string, args ...any) error {
	return &domain.Error{
		Code:      domain.ErrCodeIntegrity,
		Message:   fmt.Sprintf(format, args...),
		Retryable: false,
	}
}
