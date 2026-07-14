// restore_adversarial_internal_test.go: hostile-archive tests for
// extractUntrackedArchive (issue #6 / ADR-048). These target the
// extraction layer DIRECTLY (internal package test): on the real
// Service.Restore path a tampered untracked.zip is already rejected by
// checksum verification before extraction runs, so these tests prove the
// second, independent line of defense — a hostile archive that somehow
// reaches extraction still cannot write outside the worktree, follow or
// create symlinks, touch .git, or clobber existing files.
package repocheckpoint

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// writeHostileArchive writes a zip with the given entries into a fresh
// artifact dir and returns that dir. Entries map name -> content; names
// listed in symlinkEntries are written with a symlink mode header.
func writeHostileArchive(t *testing.T, entries map[string]string, symlinkEntries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	for name, target := range symlinkEntries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Store}
		hdr.SetMode(os.ModeSymlink | 0o777)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create symlink entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(target)); err != nil {
			t.Fatalf("write symlink entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	artifactRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactRoot, "untracked.zip"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write untracked.zip: %v", err)
	}
	return artifactRoot
}

func skipReasonsByPath(skipped []SkippedFile) map[string]SkipReason {
	m := make(map[string]SkipReason, len(skipped))
	for _, s := range skipped {
		m[s.Path] = s.Reason
	}
	return m
}

func TestExtractUntracked_HostilePaths_AllRejected_GoodEntrySurvives(t *testing.T) {
	worktree := t.TempDir()
	outside := t.TempDir()

	artifactRoot := writeHostileArchive(t,
		map[string]string{
			"../evil.txt":           "escaped",
			"sub/../../evil2.txt":   "escaped via clean",
			".git/hooks/post-merge": "#!/bin/sh\npwned\n",
			"nested/.git/config":    "[core]\n",
			"/abs.txt":              "absolute",
			"ok/good.txt":           "legit content\n",
		},
		map[string]string{
			"evil-link": filepath.Join(outside, "target"),
		},
	)

	restored, skipped, err := extractUntrackedArchive(artifactRoot, worktree)
	if err != nil {
		t.Fatalf("extractUntrackedArchive: %v", err)
	}

	if restored != 1 {
		t.Fatalf("restored = %d, want exactly the one legitimate entry", restored)
	}
	got, readErr := os.ReadFile(filepath.Join(worktree, "ok", "good.txt"))
	if readErr != nil || string(got) != "legit content\n" {
		t.Fatalf("legitimate entry not extracted correctly: %q err=%v", got, readErr)
	}

	reasons := skipReasonsByPath(skipped)
	wantReasons := map[string]SkipReason{
		"../evil.txt":           SkipPathTraversal,
		"sub/../../evil2.txt":   SkipPathTraversal,
		".git/hooks/post-merge": SkipGitInternal,
		"nested/.git/config":    SkipGitInternal,
		"/abs.txt":              SkipPathTraversal,
		"evil-link":             SkipSymlink,
	}
	for path, want := range wantReasons {
		if reasons[path] != want {
			t.Errorf("skip reason for %q = %q, want %q", path, reasons[path], want)
		}
	}

	// Nothing escaped: the traversal targets do not exist, .git was never
	// created, no symlink materialized.
	for _, mustNotExist := range []string{
		filepath.Join(worktree, "..", "evil.txt"),
		filepath.Join(worktree, "..", "evil2.txt"),
		filepath.Join(worktree, ".git"),
		filepath.Join(worktree, "evil-link"),
	} {
		if _, err := os.Lstat(mustNotExist); !os.IsNotExist(err) {
			t.Errorf("hostile artifact escaped containment: %s exists", mustNotExist)
		}
	}
}

func TestExtractUntracked_SymlinkedParentDirectory_Rejected(t *testing.T) {
	worktree := t.TempDir()
	outside := t.TempDir()

	// A pre-existing symlinked directory INSIDE the worktree pointing
	// outside it: an entry textually under the worktree would physically
	// land in `outside` if the parent walk were skipped.
	if err := os.Symlink(outside, filepath.Join(worktree, "linkdir")); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	artifactRoot := writeHostileArchive(t, map[string]string{
		"linkdir/inner.txt": "should never land outside",
	}, nil)

	restored, skipped, err := extractUntrackedArchive(artifactRoot, worktree)
	if err != nil {
		t.Fatalf("extractUntrackedArchive: %v", err)
	}
	if restored != 0 {
		t.Fatalf("restored = %d, want 0", restored)
	}
	reasons := skipReasonsByPath(skipped)
	if reasons["linkdir/inner.txt"] != SkipSymlink {
		t.Fatalf("skip reason = %q, want %q", reasons["linkdir/inner.txt"], SkipSymlink)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("extraction escaped through the symlinked parent: %v", entries)
	}
}

func TestExtractUntracked_OversizeEntry_SkippedByCap(t *testing.T) {
	worktree := t.TempDir()

	big := make([]byte, maxRestoreFileBytes+1)
	artifactRoot := writeHostileArchive(t, map[string]string{
		"big.bin":  string(big),
		"tiny.txt": "fine\n",
	}, nil)

	restored, skipped, err := extractUntrackedArchive(artifactRoot, worktree)
	if err != nil {
		t.Fatalf("extractUntrackedArchive: %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored = %d, want only the in-cap entry", restored)
	}
	reasons := skipReasonsByPath(skipped)
	if reasons["big.bin"] != SkipOversizeFile {
		t.Fatalf("skip reason for big.bin = %q, want %q", reasons["big.bin"], SkipOversizeFile)
	}
	if _, err := os.Lstat(filepath.Join(worktree, "big.bin")); !os.IsNotExist(err) {
		t.Fatal("oversize entry was written despite the cap")
	}
}

func TestExtractUntracked_MissingArchive_IsEmptySuccess(t *testing.T) {
	restored, skipped, err := extractUntrackedArchive(t.TempDir(), t.TempDir())
	if err != nil || restored != 0 || len(skipped) != 0 {
		t.Fatalf("missing untracked.zip must be an empty success, got restored=%d skipped=%v err=%v", restored, skipped, err)
	}
}
