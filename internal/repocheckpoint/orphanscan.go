// orphanscan.go: startup scan for orphaned checkpoint temp directories
// (checkpoint-b07's own scope, per checkpoint-b04's lessons-learned note:
// "atomic write here is single-process-safe ... but does NOT include ...
// a startup orphan-temp-dir scan"). writeArtifactDir's temp-dir cleanup
// (atomicwrite.go) is a process-local `defer` — it cannot run if the
// process is killed outright rather than merely erroring out, so a
// ".checkpoint-tmp-*" directory can genuinely be left behind next to
// ArtifactsRoot/<checkpoint-id>/ forever unless something else finds and
// removes it. This file is that something else, run once at startup
// (mirrors internal/progress's own Reconciler, checkpoint-a04, which plays
// the equivalent role for Part A's staged-artifact-vs-DB crash window).
package repocheckpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OrphanedTempDir describes one leftover ".checkpoint-tmp-*" directory
// ScanOrphanedTempDirs found under a checkpoints root.
type OrphanedTempDir struct {
	// Path is the temp directory's full path.
	Path string
	// ModTime is the directory's own last-modified time, so a caller can
	// decide whether an orphan is "obviously stale" (e.g. hours old) versus
	// possibly belonging to a capture that is still genuinely in flight
	// right now — ScanOrphanedTempDirs itself does not make that judgment
	// (see its own doc comment on the minAge parameter).
	ModTime time.Time
}

// ScanOrphanedTempDirs walks root (a checkpoints directory — the same
// ArtifactsRoot a Service/Capture caller already passes to Create, i.e. the
// parent of every ArtifactsRoot/<checkpoint-id>/ final directory) for
// leftover ".checkpoint-tmp-*" entries and removes every one whose
// modification time is at least minAge old.
//
// minAge exists because a genuinely in-progress capture also has a
// ".checkpoint-tmp-*" directory on disk for the (very short) window between
// MkdirTemp and the final rename — this function is meant to run once at
// process startup, before any new capture has had a chance to begin, but a
// caller integrating this into a long-running daemon (rather than a
// fresh-process startup check) should still pass a conservative minAge
// (e.g. a few minutes) so a temp dir that is merely young is never treated
// as orphaned out from under a concurrent capture actively writing into
// it — this is exactly what the required -race test proves.
//
// A temp directory is safe to delete unconditionally once it IS considered
// orphaned: by construction (atomicwrite.go), nothing ever references a
// ".checkpoint-tmp-*" path from a committed DB row or a completed
// manifest — only the FINAL renamed path is ever recorded anywhere durable
// — so removing an orphaned temp dir can never invalidate evidence a
// caller still needs.
func ScanOrphanedTempDirs(root string, minAge time.Duration, now time.Time) ([]OrphanedTempDir, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("repocheckpoint: scan orphaned temp dirs under %s: %w", root, err)
	}

	var found []OrphanedTempDir
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		matched, err := filepath.Match(tempDirPrefix, e.Name())
		if err != nil {
			// tempDirPrefix is a fixed, compile-time-correct pattern (this
			// package's own constant); a match error here would mean that
			// constant itself is malformed, which is a programmer error to
			// surface loudly, not a per-entry condition to skip past
			// silently.
			return nil, fmt.Errorf("repocheckpoint: match temp dir pattern %q against %q: %w", tempDirPrefix, e.Name(), err)
		}
		if !matched {
			continue
		}
		info, err := e.Info()
		if err != nil {
			if os.IsNotExist(err) {
				// Removed by a concurrent capture (or a concurrent scan)
				// between ReadDir and Info — no longer there to report or
				// remove, not an error.
				continue
			}
			return nil, fmt.Errorf("repocheckpoint: stat %s: %w", e.Name(), err)
		}
		if now.Sub(info.ModTime()) < minAge {
			// Too young: possibly a capture actively in flight right now.
			// Leave it alone this pass; a later scan (or process restart)
			// will pick it up once it is old enough to be unambiguously
			// abandoned.
			continue
		}
		found = append(found, OrphanedTempDir{
			Path:    filepath.Join(root, e.Name()),
			ModTime: info.ModTime(),
		})
	}
	return found, nil
}

// CleanOrphanedTempDirs runs ScanOrphanedTempDirs and removes every orphan
// it finds, returning the paths actually removed. A removal failure for
// one entry does not stop the sweep for the others — every orphan is
// independent, so one unremovable directory (e.g. a permissions issue)
// should not mask cleanup of every other genuinely-removable one; the
// first error encountered (if any) is still returned alongside whatever
// partial progress was made, so a caller can log/retry rather than the
// failure being silent.
func CleanOrphanedTempDirs(root string, minAge time.Duration, now time.Time) ([]string, error) {
	orphans, err := ScanOrphanedTempDirs(root, minAge, now)
	if err != nil {
		return nil, err
	}
	var (
		removed  []string
		firstErr error
	)
	for _, o := range orphans {
		if rmErr := os.RemoveAll(o.Path); rmErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("repocheckpoint: remove orphaned temp dir %s: %w", o.Path, rmErr)
			}
			continue
		}
		removed = append(removed, o.Path)
	}
	return removed, firstErr
}
