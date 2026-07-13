// archive.go: builds the untracked-file archive (ADD §19.2's
// `untracked.zip`) applying the safe untracked archive policy. checkpoint-
// b04 shipped the structural half (size/path/symlink filters); this file
// now also applies the "secret scan" bullet of ADD §19.5's default policy
// via internal/redact (checkpoint-b06), the fuller policy + richer
// skip-reason reporting qa-05's leakage scanner consumes downstream.
package repocheckpoint

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/redact"
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
// file-count caps, applies internal/redact's secret scan (unless
// scanSecrets is false), and zips the survivors. Every path decision
// (included or skipped, and why) is recorded — this function never
// silently drops a file without a reason ending up in the returned
// Skipped slice.
//
// The secret scan runs AFTER the size caps (a candidate that is already
// going to be skipped as oversize has no reason to also pay for a content
// read) but BEFORE the file is added to the archive — a secret-shaped
// file is skipped and reported the same as any other policy rejection,
// never partially archived first and filtered after the fact.
func buildUntrackedArchive(ctx context.Context, gitClient *gitx.Client, worktreeRoot string, maxFileBytes, maxTotalBytes int64, maxFileCount int, scanSecrets bool) (UntrackedArchiveResult, error) {
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

		if scanSecrets {
			if skipReason, skip := scanForSecrets(abs); skip {
				result.Skipped = append(result.Skipped, SkippedFile{Path: rel, Reason: skipReason})
				continue
			}
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

// scanForSecrets applies internal/redact to one already-size-validated
// candidate file, returning the specific SkipReason (filename vs. content)
// and true if it should be excluded from the archive. A scan I/O error is
// treated as "skip, unreadable" rather than aborting the whole capture —
// consistent with this function's existing os.Stat error handling above:
// an untracked file this package cannot safely read is omitted, not a
// reason to fail the entire checkpoint.
func scanForSecrets(abs string) (SkipReason, bool) {
	result, err := redact.ScanPath(abs)
	if err != nil {
		return SkipUnreadable, true
	}
	if !result.Matched() {
		return "", false
	}
	for _, f := range result.Findings {
		if f.Detector == "filename" {
			return SkipSecretFilename, true
		}
	}
	return SkipSecretContent, true
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
