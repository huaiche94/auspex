// orphanscan_test.go: checkpoint-b07's required startup orphan-temp-dir
// scan tests, plus the -race proof that scanning is safe even while a
// concurrent capture is actively writing into its own temp directory.
// External test package (repocheckpoint_test) since ScanOrphanedTempDirs/
// CleanOrphanedTempDirs are this package's exported, production-facing
// API — no white-box access needed here, unlike atomicwrite_crash_test.go.
package repocheckpoint_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/repocheckpoint"
)

func TestAtomicWrite_OrphanScan_FindsOldOrphan(t *testing.T) {
	root := t.TempDir()
	orphan := filepath.Join(root, ".checkpoint-tmp-abc123")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Backdate the orphan's mtime so it is unambiguously old relative to
	// "now" passed to the scan.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	found, err := repocheckpoint.ScanOrphanedTempDirs(root, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("ScanOrphanedTempDirs: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 orphan found, got %d: %v", len(found), found)
	}
	if found[0].Path != orphan {
		t.Fatalf("expected orphan path %s, got %s", orphan, found[0].Path)
	}
}

func TestAtomicWrite_OrphanScan_IgnoresNonTempDirEntries(t *testing.T) {
	root := t.TempDir()
	// A real, already-committed checkpoint directory must never be treated
	// as an orphan, no matter how old it is.
	realCheckpoint := filepath.Join(root, "cp-real-1")
	if err := os.MkdirAll(realCheckpoint, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(realCheckpoint, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	// A stray regular file (not a directory) matching the temp prefix must
	// also be ignored — the mechanism only ever creates directories.
	strayFile := filepath.Join(root, ".checkpoint-tmp-not-a-dir")
	if err := os.WriteFile(strayFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(strayFile, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	found, err := repocheckpoint.ScanOrphanedTempDirs(root, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("ScanOrphanedTempDirs: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected 0 orphans (real checkpoint dir + non-dir stray file both ignored), got %d: %v", len(found), found)
	}
}

func TestAtomicWrite_OrphanScan_SkipsYoungTempDir(t *testing.T) {
	root := t.TempDir()
	fresh := filepath.Join(root, ".checkpoint-tmp-fresh")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// mtime defaults to "now" - well within minAge, simulating a capture
	// that is (from the scan's point of view) still plausibly in flight.

	found, err := repocheckpoint.ScanOrphanedTempDirs(root, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("ScanOrphanedTempDirs: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected a young temp dir to be skipped (not yet considered orphaned), got %d: %v", len(found), found)
	}
}

func TestAtomicWrite_OrphanScan_NonexistentRoot_NoError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	found, err := repocheckpoint.ScanOrphanedTempDirs(root, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("expected no error for a not-yet-created checkpoints root, got %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected 0 orphans for a nonexistent root, got %d", len(found))
	}
}

func TestAtomicWrite_CleanOrphanedTempDirs_RemovesOldOnly(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, ".checkpoint-tmp-old")
	young := filepath.Join(root, ".checkpoint-tmp-young")
	for _, d := range []string{old, young} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removed, err := repocheckpoint.CleanOrphanedTempDirs(root, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("CleanOrphanedTempDirs: %v", err)
	}
	if len(removed) != 1 || removed[0] != old {
		t.Fatalf("expected exactly [%s] removed, got %v", old, removed)
	}
	if _, statErr := os.Stat(old); !os.IsNotExist(statErr) {
		t.Fatalf("expected old orphan to be removed from disk")
	}
	if _, statErr := os.Stat(young); statErr != nil {
		t.Fatalf("expected young temp dir to remain untouched: %v", statErr)
	}
}

// TestAtomicWrite_OrphanScan_ConcurrentCapture_Race is the DAG's own
// explicit requirement: prove with -race that a startup orphan scan is
// safe to run even while a concurrent, genuinely in-progress capture is
// actively writing into its own temp directory (a fresh writeArtifactDir
// call, not a pre-existing fixture). The scan must not corrupt, block
// indefinitely, or be corrupted by the concurrent writer, and — because
// the capture's temp dir is fresh (age ~0), minAge keeps the scan from
// ever touching (or racing on) files the live capture is still writing.
func TestAtomicWrite_OrphanScan_ConcurrentCapture_Race(t *testing.T) {
	root := t.TempDir()

	// Seed one genuinely old orphan so the scan has real cleanup work to
	// do concurrently with the live capture below.
	oldOrphan := filepath.Join(root, ".checkpoint-tmp-preexisting")
	if err := os.MkdirAll(oldOrphan, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldOrphan, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: a real, live capture writing a genuinely new checkpoint
	// directory concurrently with the scan below.
	go func() {
		defer wg.Done()
		finalDir := filepath.Join(root, "cp-live")
		files := map[string][]byte{
			"manifest.json": []byte(`{"ok":true}`),
			"summary.md":    []byte("# Checkpoint\n"),
		}
		for i := 0; i < 20; i++ {
			// Each iteration targets a distinct finalDir (writeArtifactDir
			// refuses to overwrite an existing one), so this also exercises
			// many temp-dir create/rename cycles concurrently with the scan
			// loop below, not just one.
			dir := finalDir + "-" + string(rune('a'+i))
			if err := repocheckpoint.WriteArtifactDirForTest(dir, files); err != nil {
				t.Errorf("concurrent writeArtifactDir: %v", err)
				return
			}
		}
	}()

	// Goroutine 2: repeatedly scan (and clean) for orphans while the
	// capture above is running.
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if _, err := repocheckpoint.CleanOrphanedTempDirs(root, time.Minute, time.Now()); err != nil {
				t.Errorf("concurrent CleanOrphanedTempDirs: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	// The pre-seeded old orphan must have been cleaned up by one of the
	// concurrent scan passes.
	if _, statErr := os.Stat(oldOrphan); !os.IsNotExist(statErr) {
		t.Fatalf("expected the pre-existing old orphan to be cleaned up by a concurrent scan pass")
	}
	// Every one of the live capture's 20 final directories must have
	// completed successfully and fully (the scan's minAge=1m must never
	// have touched their fresh, in-flight temp dirs).
	for i := 0; i < 20; i++ {
		dir := filepath.Join(root, "cp-live-"+string(rune('a'+i)))
		if _, statErr := os.Stat(filepath.Join(dir, "manifest.json")); statErr != nil {
			t.Fatalf("expected concurrent capture %s to have completed fully: %v", dir, statErr)
		}
	}
}
