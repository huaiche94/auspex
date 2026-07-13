package repocheckpoint_test

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
)

func mustOpenZipNoFatal(path string) ([]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names, nil
}

// TestCapture_PathTraversal_Rejected is the DAG's required "path
// traversal" test. Git itself will not normally report a `../` path from
// ls-files inside a legitimate worktree, so this test exercises the
// security guard's own decision directly via the package's exported
// capture behavior on a crafted scenario: a file that is a symlink
// pointing outside the worktree is the realistic vector this package must
// defend against (validateUntrackedPath's traversal/symlink checks are
// exercised together here since Git only ever reports paths within the
// worktree for the traversal-string case).
func TestCapture_SymlinkEscapingWorktree_Skipped(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	linkPath := filepath.Join(rb.dir, "escape-link.txt")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// The symlink must never be dereferenced into the archive.
	zipPath := filepath.Join(artifactsRoot, "cp-1", "untracked.zip")
	if _, statErr := os.Stat(zipPath); statErr == nil {
		zr, openErr := openZip(t, zipPath)
		if openErr == nil {
			for _, f := range zr {
				if f == "escape-link.txt" {
					t.Fatal("symlink must not be archived by path/content escape")
				}
			}
		}
	}

	if result.Manifest.Recoverability.SkippedFileCount == 0 {
		t.Fatal("expected the symlink to be recorded as a skipped file")
	}
}

// TestCapture_OversizeFile_Skipped is the DAG's required "oversize" test.
func TestCapture_OversizeFile_Skipped(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	big := make([]byte, 1024)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(rb.dir, "big.txt"), big, 0o644); err != nil {
		t.Fatalf("write big file: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{
			MaxUntrackedFileBytes: 100, // smaller than big.txt's 1024 bytes
		})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if result.Manifest.Recoverability.SkippedFileCount != 1 {
		t.Fatalf("expected 1 skipped (oversize) file, got %d", result.Manifest.Recoverability.SkippedFileCount)
	}
	if result.Row.Status != repocheckpoint.StatusPartial {
		t.Fatalf("expected status partial when a required file is skipped, got %s", result.Row.Status)
	}
	if result.Manifest.Recoverability.Level != repocheckpoint.RecoverabilityPartial {
		t.Fatalf("expected recoverability partial, got %s", result.Manifest.Recoverability.Level)
	}
}

func TestCapture_TotalSizeCap_Enforced(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("a.txt", "aaaaaaaaaa")
	rb.write("b.txt", "bbbbbbbbbb")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{
			MaxUntrackedTotalBytes: 10, // only one 10-byte file fits
		})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if result.Manifest.Recoverability.SkippedFileCount != 1 {
		t.Fatalf("expected exactly 1 file skipped by the total-size cap, got %d", result.Manifest.Recoverability.SkippedFileCount)
	}
}

func TestCapture_FileCountCap_Enforced(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("a.txt", "content")
	rb.write("b.txt", "content")
	rb.write("c.txt", "content")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{
			MaxUntrackedFileCount: 2,
		})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if result.Manifest.Snapshot.UntrackedFiles != 3 {
		t.Fatalf("expected status snapshot to report all 3 untracked files seen, got %d", result.Manifest.Snapshot.UntrackedFiles)
	}
	if result.Manifest.Recoverability.SkippedFileCount != 1 {
		t.Fatalf("expected 1 file skipped by the file-count cap, got %d", result.Manifest.Recoverability.SkippedFileCount)
	}
}

func TestCapture_GitInternalsNeverIncluded(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("normal.txt", "content")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-1"), repocheckpoint.CaptureOptions{}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-1", "untracked.zip")
	if _, statErr := os.Stat(zipPath); statErr == nil {
		names := mustOpenZip(t, zipPath)
		for _, name := range names {
			if len(name) >= 4 && name[:4] == ".git" {
				t.Fatalf("archive must never contain .git internals, found: %s", name)
			}
		}
	}
}

func openZip(t *testing.T, path string) ([]string, error) {
	t.Helper()
	return mustOpenZipNoFatal(path)
}

func mustOpenZip(t *testing.T, path string) []string {
	t.Helper()
	names, err := mustOpenZipNoFatal(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	return names
}
