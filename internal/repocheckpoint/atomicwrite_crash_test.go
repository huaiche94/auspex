// atomicwrite_crash_test.go: checkpoint-b07's required crash-injection
// proof — a capture interrupted mid-write (process killed after temp-dir
// creation but before the final rename) leaves no corrupted/partial final
// artifact at finalDir: the checkpoint directory is either fully
// written-then-renamed or entirely absent, never half-visible. White-box
// (package repocheckpoint, not repocheckpoint_test) because it needs
// writeArtifactDirWithHalt and the phase constants directly, the same
// convention security_internal_test.go already established for this
// package's other white-box tests.
package repocheckpoint

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// assertFinalDirNeverPartiallyVisible is the core invariant every halt
// point below must satisfy: finalDir either does not exist at all, or
// exists complete with every expected file present — there is no
// in-between state a concurrent reader could observe.
func assertFinalDirNeverPartiallyVisible(t *testing.T, finalDir string, expectedFiles map[string][]byte) {
	t.Helper()
	info, statErr := os.Stat(finalDir)
	if os.IsNotExist(statErr) {
		// Not renamed at all yet — the other half of the invariant (a
		// leftover temp dir, if any) is checked separately by each test.
		return
	}
	if statErr != nil {
		t.Fatalf("unexpected Stat error for %s: %v", finalDir, statErr)
	}
	if !info.IsDir() {
		t.Fatalf("finalDir %s exists but is not a directory", finalDir)
	}
	for rel, want := range expectedFiles {
		got, err := os.ReadFile(filepath.Join(finalDir, rel))
		if err != nil {
			t.Fatalf("finalDir %s exists but is missing/unreadable file %s: %v (a partially-visible checkpoint directory)", finalDir, rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("finalDir %s file %s content mismatch: a partially-visible or corrupted checkpoint directory", finalDir, rel)
		}
	}
}

// listTempDirs returns every ".checkpoint-tmp-*" entry directly under dir.
func listTempDirs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		matched, _ := filepath.Match(tempDirPrefix, e.Name())
		if matched {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestAtomicWrite_CrashAfterTempDirCreated_NoPartialFinalArtifact(t *testing.T) {
	parent := t.TempDir()
	finalDir := filepath.Join(parent, "cp-1")
	files := map[string][]byte{"manifest.json": []byte(`{"ok":true}`)}

	err := writeArtifactDirWithHalt(finalDir, files, phaseTempDirCreated)
	var halt *writeArtifactDirHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected a writeArtifactDirHaltError, got %v", err)
	}

	// finalDir must not exist at all — nothing was ever written into it.
	if _, statErr := os.Stat(finalDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected finalDir to not exist after a crash immediately post-mkdir, got stat err=%v", statErr)
	}
	assertFinalDirNeverPartiallyVisible(t, finalDir, files)

	// The temp directory itself IS left behind (this is the crash we are
	// simulating: the process died before its own cleanup defer could run)
	// — exactly the orphan orphanscan.go must be able to find.
	temps := listTempDirs(t, parent)
	if len(temps) != 1 {
		t.Fatalf("expected exactly 1 orphaned temp dir after a simulated crash, got %d: %v", len(temps), temps)
	}
	// The orphaned temp dir must be empty (no files written into it yet at
	// this halt point).
	entries, err := os.ReadDir(filepath.Join(parent, temps[0]))
	if err != nil {
		t.Fatalf("ReadDir orphan: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected the orphaned temp dir to be empty at phaseTempDirCreated, got %d entries", len(entries))
	}
}

func TestAtomicWrite_CrashAfterFilesWritten_NoPartialFinalArtifact(t *testing.T) {
	parent := t.TempDir()
	finalDir := filepath.Join(parent, "cp-2")
	files := map[string][]byte{
		"manifest.json": []byte(`{"ok":true}`),
		"summary.md":    []byte("# Checkpoint\n"),
	}

	err := writeArtifactDirWithHalt(finalDir, files, phaseFilesWritten)
	var halt *writeArtifactDirHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected a writeArtifactDirHaltError, got %v", err)
	}

	// finalDir must STILL not exist — the rename never happened, no matter
	// how much content had already been written into the temp directory.
	if _, statErr := os.Stat(finalDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected finalDir to not exist after a crash before rename, got stat err=%v", statErr)
	}
	assertFinalDirNeverPartiallyVisible(t, finalDir, files)

	// This time the orphaned temp dir DOES contain the fully-written
	// files (they were fsynced before this halt point) — proving content
	// durability is independent of whether the rename itself completed.
	temps := listTempDirs(t, parent)
	if len(temps) != 1 {
		t.Fatalf("expected exactly 1 orphaned temp dir, got %d: %v", len(temps), temps)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(parent, temps[0], rel))
		if err != nil {
			t.Fatalf("expected %s to be durably written in the orphaned temp dir: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("content mismatch for %s in orphaned temp dir", rel)
		}
	}
}

func TestAtomicWrite_CrashAfterRename_FinalArtifactFullyVisible(t *testing.T) {
	parent := t.TempDir()
	finalDir := filepath.Join(parent, "cp-3")
	files := map[string][]byte{"manifest.json": []byte(`{"ok":true}`)}

	err := writeArtifactDirWithHalt(finalDir, files, phaseRenamed)
	var halt *writeArtifactDirHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected a writeArtifactDirHaltError, got %v", err)
	}

	// The rename already committed: finalDir must be FULLY present and
	// correct, and no temp dir should remain (os.Rename moved it, it did
	// not copy it).
	assertFinalDirNeverPartiallyVisible(t, finalDir, files)
	if _, statErr := os.Stat(finalDir); statErr != nil {
		t.Fatalf("expected finalDir to fully exist after a post-rename crash: %v", statErr)
	}
	temps := listTempDirs(t, parent)
	if len(temps) != 0 {
		t.Fatalf("expected no leftover temp dir after a post-rename crash, got %v", temps)
	}
}

func TestAtomicWrite_NoHalt_CompletesNormally(t *testing.T) {
	parent := t.TempDir()
	finalDir := filepath.Join(parent, "cp-normal")
	files := map[string][]byte{"manifest.json": []byte(`{"ok":true}`)}

	if err := writeArtifactDirWithHalt(finalDir, files, ""); err != nil {
		t.Fatalf("unexpected error with no halt requested: %v", err)
	}
	assertFinalDirNeverPartiallyVisible(t, finalDir, files)
	if _, statErr := os.Stat(finalDir); statErr != nil {
		t.Fatalf("expected finalDir to exist after a normal run: %v", statErr)
	}
	if temps := listTempDirs(t, parent); len(temps) != 0 {
		t.Fatalf("expected no leftover temp dir after a normal run, got %v", temps)
	}
}

// TestAtomicWrite_RetryAfterCrash_SucceedsCleanly proves the
// production retry path: after a simulated crash leaves an orphaned temp
// dir behind (but no finalDir), a fresh writeArtifactDir call for the SAME
// finalDir must still succeed — the orphan must not block a legitimate
// retry, since writeArtifactDir's own collision guard only checks finalDir
// itself, never pre-existing temp dirs (which get their own distinct
// randomized name from os.MkdirTemp every call).
func TestAtomicWrite_RetryAfterCrash_SucceedsCleanly(t *testing.T) {
	parent := t.TempDir()
	finalDir := filepath.Join(parent, "cp-retry")
	files := map[string][]byte{"manifest.json": []byte(`{"ok":true}`)}

	if err := writeArtifactDirWithHalt(finalDir, files, phaseFilesWritten); err == nil {
		t.Fatal("expected the first (halted) attempt to return a halt error")
	}
	if _, statErr := os.Stat(finalDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected no finalDir after the halted attempt")
	}

	if err := writeArtifactDir(finalDir, files); err != nil {
		t.Fatalf("expected the retry to succeed despite the orphaned temp dir from the halted attempt: %v", err)
	}
	assertFinalDirNeverPartiallyVisible(t, finalDir, files)

	// The orphan from the FIRST (halted) attempt is still there — retrying
	// does not implicitly clean it up; that is orphanscan.go's job, proven
	// separately.
	if temps := listTempDirs(t, parent); len(temps) != 1 {
		t.Fatalf("expected exactly 1 leftover orphan from the halted attempt after a successful retry, got %v", temps)
	}
}
