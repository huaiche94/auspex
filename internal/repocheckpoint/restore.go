// restore.go: the mutating half of ADD §19.6 Restore (issue #6, ADR-048)
// — the counterpart restoredryrun.go explicitly deferred ("actual restore
// is stretch/deferred"). RestoreApply replays a verified checkpoint's
// captured state onto its worktree:
//
//  1. staged patch  → `git apply --binary --index`  (index + worktree)
//  2. unstaged patch → `git apply --binary`          (worktree only)
//  3. untracked.zip  → extracted file-by-file under the same path-safety
//     discipline capture applied when building it (security.go), plus a
//     strict no-clobber rule.
//
// Safety invariants (ADR-048; Constitution non-negotiable #9):
//
//   - No ref mutation, ever. The only Git mutations are the two `git
//     apply` calls, which are incapable of moving HEAD, switching
//     branches, or creating commits. Restore never runs checkout, reset,
//     stash, commit, or any other ref-touching subcommand.
//   - Never delete, never overwrite extras. ADD §19.6's "never delete
//     extra files unless --exact" is enforced by construction: nothing
//     here deletes anything, and untracked extraction skips (and
//     reports) any destination path that already exists. No --exact mode
//     exists yet; when one is built it gets its own ADR-048 revision.
//   - Fail loudly on partial application. The dry-run gate (ApplyCheck on
//     both patches) runs before anything mutates, but the tree can
//     legitimately change between check and apply — if the unstaged
//     replay fails after the staged one landed, the returned error says
//     exactly which step applied and which did not, never a generic
//     failure that hides a half-restored tree.
package repocheckpoint

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/huaiche94/auspex/internal/gitx"
)

// SkipExistsNotOverwritten is restore's own addition to the SkipReason
// ledger: an untracked-archive entry whose destination path already
// exists in the worktree. Restore never overwrites (the write-side twin
// of §19.6's "never delete extra files unless --exact"), so the entry is
// skipped and reported rather than clobbering whatever is there now.
const SkipExistsNotOverwritten SkipReason = "exists_not_overwritten"

// RestoreApplyResult reports what RestoreApply actually did — the
// mutating counterpart of RestoreDryRunReport, with the same
// "every decision is disclosed, nothing is silently dropped" discipline
// as UntrackedArchiveResult's skip ledger.
type RestoreApplyResult struct {
	// StagedApplied/UnstagedApplied report which patch replays actually
	// ran to completion. Both true on success; on error they pin down
	// exactly how far the restore got (see the partial-application
	// invariant in the package comment).
	StagedApplied   bool
	UnstagedApplied bool

	// UntrackedRestored counts archive entries actually written;
	// UntrackedSkipped records every entry declined, with its reason
	// (path safety, no-clobber, caps) — same ledger shape capture uses.
	UntrackedRestored int
	UntrackedSkipped  []SkippedFile
}

// maxRestoreFileBytes / maxRestoreTotalBytes bound untracked extraction
// exactly as capture bounded archiving (DefaultMaxFileBytes/
// DefaultMaxTotalBytes): an archive produced under those caps can never
// legitimately exceed them at extraction time, so anything larger is by
// definition a tampered or corrupted artifact (a zip-bomb entry), not a
// bigger-than-expected legitimate file.
const (
	maxRestoreFileBytes  = DefaultMaxFileBytes
	maxRestoreTotalBytes = DefaultMaxTotalBytes
)

// RestoreApply performs the real restore of row's checkpoint onto
// worktreeRoot. Callers (Service.Restore) are responsible for the §19.6
// gate sequence first — checksum verification, identity check, dirty
// policy, and ApplyCheck on both patches via RestoreDryRun; this function
// is only the apply step and assumes those checks passed moments ago.
func RestoreApply(ctx context.Context, gitClient *gitx.Client, row Row, worktreeRoot string) (RestoreApplyResult, error) {
	result := RestoreApplyResult{}

	stagedPatch, unstagedPatch, err := loadCheckpointPatches(row.ArtifactRoot)
	if err != nil {
		return result, fmt.Errorf("repocheckpoint: RestoreApply: load patches: %w", err)
	}

	if err := gitClient.Apply(ctx, worktreeRoot, stagedPatch, gitx.ApplyToIndexAndWorktree); err != nil {
		return result, fmt.Errorf("repocheckpoint: RestoreApply: staged patch failed, nothing was applied: %w", err)
	}
	result.StagedApplied = true

	if err := gitClient.Apply(ctx, worktreeRoot, unstagedPatch, gitx.ApplyToWorktree); err != nil {
		return result, fmt.Errorf("repocheckpoint: RestoreApply: PARTIAL RESTORE — staged patch was applied to index+worktree but the unstaged patch failed; the tree is between states: %w", err)
	}
	result.UnstagedApplied = true

	restored, skipped, err := extractUntrackedArchive(row.ArtifactRoot, worktreeRoot)
	if err != nil {
		return result, fmt.Errorf("repocheckpoint: RestoreApply: PARTIAL RESTORE — both patches were applied but untracked extraction failed: %w", err)
	}
	result.UntrackedRestored = restored
	result.UntrackedSkipped = skipped
	return result, nil
}

// extractUntrackedArchive writes untracked.zip's entries back into
// worktreeRoot. A checkpoint with no archived untracked files has no
// untracked.zip at all (capture only writes the artifact when it archived
// something) — that is a normal empty result, not an error.
//
// Every entry passes validateRestorePath (the not-yet-existing-path
// variant of capture's validateUntrackedPath) before a single byte is
// written, plus the no-clobber check and the same size caps capture
// enforced. A hostile archive (traversal names, symlink entries, bomb
// entries) therefore degrades to skip-ledger entries and cap errors, never
// writes outside worktreeRoot.
func extractUntrackedArchive(artifactRoot, worktreeRoot string) (restored int, skipped []SkippedFile, err error) {
	zipPath := filepath.Join(artifactRoot, "untracked.zip")
	zipBytes, readErr := os.ReadFile(zipPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("read %s: %w", zipPath, readErr)
	}

	zr, zipErr := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if zipErr != nil {
		return 0, nil, fmt.Errorf("open untracked archive: %w", zipErr)
	}

	var totalBytes int64
	for _, entry := range zr.File {
		rel := entry.Name

		// Directory entries carry no content; real directories are
		// created as needed for file entries below. Skipping them (with
		// no ledger entry — they are structure, not data) also refuses a
		// crafted archive the chance to pre-create a hostile directory
		// layout that file validation would then trust.
		if entry.FileInfo().IsDir() {
			continue
		}
		// Only regular files are ever extracted. Capture never archives
		// symlinks or special files (validateUntrackedPath skips them),
		// so any non-regular entry here is a crafted archive probing for
		// a symlink-write primitive — skip and disclose.
		if !entry.FileInfo().Mode().IsRegular() {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: SkipSymlink})
			continue
		}

		abs, reason, ok := validateRestorePath(worktreeRoot, rel)
		if !ok {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: reason})
			continue
		}

		// No-clobber: restore never overwrites a path that exists now
		// (regular file, symlink, directory — anything). Lstat, not
		// Stat, so a symlink at the destination is detected as existing
		// rather than followed to its target.
		if _, statErr := os.Lstat(abs); statErr == nil {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: SkipExistsNotOverwritten})
			continue
		}

		if entry.UncompressedSize64 > uint64(maxRestoreFileBytes) {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: SkipOversizeFile})
			continue
		}
		if totalBytes+int64(entry.UncompressedSize64) > maxRestoreTotalBytes {
			skipped = append(skipped, SkippedFile{Path: rel, Reason: SkipTotalCapped})
			continue
		}

		written, writeErr := writeArchiveEntry(entry, abs)
		if writeErr != nil {
			return restored, skipped, fmt.Errorf("extract %s: %w", rel, writeErr)
		}
		totalBytes += written
		restored++
	}
	return restored, skipped, nil
}

// writeArchiveEntry streams one validated zip entry to abs, enforcing the
// per-file byte cap against the ACTUAL decompressed stream (the zip
// header's UncompressedSize64 is attacker-controlled metadata — the check
// above is a fast pre-filter, this LimitReader is the real bound).
func writeArchiveEntry(entry *zip.File, abs string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return 0, err
	}

	src, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()

	// O_EXCL: the no-clobber Lstat above is advisory; this makes it
	// atomic — if anything created the path in between, fail rather
	// than truncate it.
	perm := entry.FileInfo().Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	dst, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return 0, err
	}

	written, copyErr := io.Copy(dst, io.LimitReader(src, maxRestoreFileBytes+1))
	closeErr := dst.Close()
	if copyErr == nil && written > maxRestoreFileBytes {
		copyErr = fmt.Errorf("entry decompresses past the %d byte cap (header claimed %d)", maxRestoreFileBytes, entry.UncompressedSize64)
	}
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		// Remove the partial file so a failed extraction never leaves a
		// truncated impostor where the checkpoint's real content should
		// be. The tree already has a disclosed partial-restore error on
		// the way up; leaving broken bytes behind would compound it.
		_ = os.Remove(abs)
		return 0, copyErr
	}
	return written, nil
}

// validateRestorePath is validateUntrackedPath's variant for destination
// paths that do not exist yet: identical segment rules (no "..", no
// ".git" anywhere in the chain) and containment check, but the leaf is
// required NOT to exist (no-clobber is checked separately by the caller)
// and the ancestor-symlink walk only inspects ancestors that already
// exist — the ones extraction would create itself are made fresh by
// MkdirAll and cannot be pre-existing symlinks.
func validateRestorePath(worktreeRoot, rel string) (string, SkipReason, bool) {
	if rel == "" {
		return "", SkipUnreadable, false
	}
	if filepath.IsAbs(rel) || (len(rel) > 1 && rel[1] == ':') {
		// Absolute paths (POSIX or Windows drive-letter) can never be a
		// worktree-relative archive member Capture wrote.
		return "", SkipPathTraversal, false
	}
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
	if cleanAbs == cleanRoot || !strings.HasPrefix(cleanAbs, cleanRoot+string(filepath.Separator)) {
		return "", SkipPathTraversal, false
	}

	// Walk the EXISTING ancestors up to worktreeRoot: a destination
	// reached through a pre-existing symlinked directory would escape the
	// tree even though its own textual path stays inside.
	dir := filepath.Dir(cleanAbs)
	for dir != cleanRoot && len(dir) >= len(cleanRoot) {
		dirInfo, err := os.Lstat(dir)
		if err == nil && dirInfo.Mode()&os.ModeSymlink != 0 {
			return "", SkipSymlink, false
		}
		// A missing ancestor is fine — MkdirAll creates it as a real
		// directory at write time; any other Lstat error surfaces at
		// write time too.
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return cleanAbs, "", true
}
