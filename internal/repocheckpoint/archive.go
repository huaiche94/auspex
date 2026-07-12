// archive.go: builds the untracked-file archive (ADD §19.2's
// `untracked.zip`) applying the safe untracked archive policy (Part B
// deliverable #6's minimal working slice: size/path/symlink filters this
// node needs for "create and verify" to be meaningful; the fuller policy —
// secret scanning, richer skip-reason reporting for qa's leakage scanner —
// is checkpoint-b06's scope, per this wave's brief).
package repocheckpoint

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/huaiche94/preflight/internal/gitx"
)

// UntrackedArchiveResult reports what buildUntrackedArchive actually did:
// the archive bytes (empty if nothing was included) plus the skip ledger
// ADD §19.5 requires ("skipped reasons recorded").
type UntrackedArchiveResult struct {
	// Data is the zip archive bytes. Nil/empty if no untracked files were
	// included (still a valid, complete checkpoint — an empty untracked
	// set is not a failure).
	Data []byte
	// IncludedCount is how many files were actually archived.
	IncludedCount int
	// Skipped records every candidate path this pass declined to
	// archive, with its reason (ADD §19.5).
	Skipped []SkippedFile
}

// SkippedFile is one entry of the skip ledger (ADD §19.2's
// `skipped-files.json`).
type SkippedFile struct {
	Path   string     `json:"path"`
	Reason SkipReason `json:"reason"`
}

// buildUntrackedArchive lists untracked files via gitx, validates each
// path through validateUntrackedPath, enforces the per-file/total-size/
// file-count caps, and zips the survivors. Every path decision (included
// or skipped, and why) is recorded — this function never silently drops a
// file without a reason ending up in the returned Skipped slice.
func buildUntrackedArchive(ctx context.Context, gitClient *gitx.Client, worktreeRoot string, maxFileBytes, maxTotalBytes int64, maxFileCount int) (UntrackedArchiveResult, error) {
	paths, err := gitClient.ListUntracked(ctx, worktreeRoot)
	if err != nil {
		return UntrackedArchiveResult{}, fmt.Errorf("repocheckpoint: list untracked files: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	result := UntrackedArchiveResult{}
	var totalBytes int64

	for _, rel := range paths {
		if result.IncludedCount >= maxFileCount {
			result.Skipped = append(result.Skipped, SkippedFile{Path: rel, Reason: SkipFileCountCap})
			continue
		}

		abs, reason, ok := validateUntrackedPath(worktreeRoot, rel)
		if !ok {
			result.Skipped = append(result.Skipped, SkippedFile{Path: rel, Reason: reason})
			continue
		}

		info, statErr := os.Stat(abs)
		if statErr != nil {
			result.Skipped = append(result.Skipped, SkippedFile{Path: rel, Reason: SkipUnreadable})
			continue
		}
		if info.Size() > maxFileBytes {
			result.Skipped = append(result.Skipped, SkippedFile{Path: rel, Reason: SkipOversizeFile})
			continue
		}
		if totalBytes+info.Size() > maxTotalBytes {
			result.Skipped = append(result.Skipped, SkippedFile{Path: rel, Reason: SkipTotalCapped})
			continue
		}

		if err := addFileToZip(zw, abs, rel, info.Mode()); err != nil {
			return UntrackedArchiveResult{}, fmt.Errorf("repocheckpoint: archive %s: %w", rel, err)
		}
		totalBytes += info.Size()
		result.IncludedCount++
	}

	if err := zw.Close(); err != nil {
		return UntrackedArchiveResult{}, fmt.Errorf("repocheckpoint: finalize untracked archive: %w", err)
	}
	if result.IncludedCount > 0 {
		result.Data = buf.Bytes()
	}
	return result, nil
}

// addFileToZip streams one file's content into the zip writer under its
// worktree-relative path (always forward-slash, per the zip spec),
// preserving the regular-file permission bits.
func addFileToZip(zw *zip.Writer, absPath, relPath string, mode os.FileMode) error {
	src, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	hdr := &zip.FileHeader{
		Name:   filepath.ToSlash(relPath),
		Method: zip.Deflate,
	}
	hdr.SetMode(mode)

	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}
