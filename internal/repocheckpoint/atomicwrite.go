// atomicwrite.go: the staged temp-directory-to-final-directory write
// protocol (agents/checkpoint.md Part B deliverable #7's core mechanic;
// ADD §19.3 capture steps 3/9/12/13: temp directory, ..., fsync, atomic
// rename). A checkpoint's artifact directory either exists complete or
// does not exist at all from any external observer's point of view — there
// is no window where a reader sees a half-written checkpoint directory.
//
// checkpoint-b04 delivered the single-process atomic write itself (this
// file's core sequence below) and explicitly deferred two things to this
// node (checkpoint-b07, per checkpoint-b04's own lessons-learned note):
// cross-process crash-injection proof that an interrupt between temp-dir
// creation and the final rename never leaves a partial artifact visible at
// finalDir, and a startup scan that finds and cleans up orphaned temp
// directories such a crash leaves behind (orphanscan.go). haltAfter below
// is that crash-injection seam, mirroring internal/progress's
// CompleteNode.HaltAfter/HaltError pattern (checkpoint-a04) adapted to a
// free function rather than a method on a long-lived service value.
package repocheckpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// tempDirPrefix is the os.MkdirTemp pattern every staged checkpoint
// directory is created under, shared between writeArtifactDir (which
// creates them) and orphanscan.go's ScanOrphanedTempDirs (which finds and
// removes any left behind by an interrupted capture) — a single constant
// so the two halves of this mechanism cannot silently drift apart.
const tempDirPrefix = ".checkpoint-tmp-*"

// writeArtifactDirPhase names the points in writeArtifactDir's sequence a
// crash-injection test can stop at, exactly as internal/progress's Phase
// constants do for CompleteNode (checkpoint-a04). Not persisted anywhere;
// pure in-process test vocabulary layered over the durable state
// (temp-dir-exists-but-unrenamed vs. finalDir-exists) that actually
// distinguishes these points.
type writeArtifactDirPhase string

const (
	// phaseTempDirCreated: the temp directory exists but no file has been
	// written into it yet.
	phaseTempDirCreated writeArtifactDirPhase = "temp_dir_created"
	// phaseFilesWritten: every file has been written and fsynced into the
	// temp directory, but the temp directory itself has not yet been
	// fsynced or renamed — the exact "process killed after temp-dir
	// creation but before the final rename" window this node's DAG entry
	// names explicitly.
	phaseFilesWritten writeArtifactDirPhase = "files_written"
	// phaseRenamed: os.Rename has already succeeded (finalDir now exists,
	// tempDir no longer does) but the post-rename parent-directory fsync
	// has not run yet.
	phaseRenamed writeArtifactDirPhase = "renamed"
)

// writeArtifactDirHaltError is returned when haltAfter caused an
// intentional mid-protocol stop, simulating a process crash at exactly
// that point — mirrors internal/progress's *HaltError (checkpoint-a04)
// exactly, kept as a distinct type in this package rather than an imported
// one so internal/repocheckpoint has no dependency on internal/progress.
type writeArtifactDirHaltError struct {
	phase   writeArtifactDirPhase
	tempDir string
}

func (e *writeArtifactDirHaltError) Error() string {
	return fmt.Sprintf("repocheckpoint: writeArtifactDir halted after phase %q (fault injection)", e.phase)
}

// writeArtifactDir writes every entry in files (relative path -> content)
// into a fresh temp directory beside finalDir, fsyncs each file and the
// temp directory, then atomically renames the temp directory to finalDir.
// If finalDir already exists this fails closed (a checkpoint ID collision
// is a state-integrity bug, not something to silently overwrite) rather
// than clobbering existing evidence.
//
// On any failure the temp directory is removed before returning, so a
// failed capture never leaves an orphaned partial directory next to
// finalDir for a casual directory listing to mistake as real. This cleanup
// is itself a process-local `defer`, though — it cannot run if the process
// is killed outright (SIGKILL, power loss) rather than merely erroring
// out — which is exactly why orphanscan.go's startup sweep exists as a
// second, independent line of defense that does not depend on this
// function's own defer having had a chance to run.
func writeArtifactDir(finalDir string, files map[string][]byte) error {
	return writeArtifactDirWithHalt(finalDir, files, "")
}

// writeArtifactDirWithHalt is writeArtifactDir's crash-injectable core.
// haltAfter, if non-empty, causes this function to return a
// *writeArtifactDirHaltError immediately after completing the named phase
// WITHOUT executing any later phase (including the deferred temp-dir
// cleanup below, which only fires on genuine errors, not on an
// intentional halt) — simulating a process that was killed at exactly
// that point, so a subsequent orphan scan has something real left on disk
// to find. Production callers (writeArtifactDir above) always pass "".
func writeArtifactDirWithHalt(finalDir string, files map[string][]byte, haltAfter writeArtifactDirPhase) (err error) {
	if _, statErr := os.Stat(finalDir); statErr == nil {
		return errIntegrity("repocheckpoint: artifact directory %s already exists; refusing to overwrite existing checkpoint evidence", finalDir)
	}

	parent := filepath.Dir(finalDir)
	if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
		return fmt.Errorf("repocheckpoint: create parent dir %s: %w", parent, mkErr)
	}

	tempDir, mkErr := os.MkdirTemp(parent, tempDirPrefix)
	if mkErr != nil {
		return fmt.Errorf("repocheckpoint: create temp dir: %w", mkErr)
	}
	defer func() {
		if err != nil {
			var halt *writeArtifactDirHaltError
			if errors.As(err, &halt) {
				// A halt is a SIMULATED crash: real production code never
				// reaches this branch (haltAfter is always "" outside
				// tests), and the whole point is that the process does NOT
				// get a chance to clean up after itself — that is what
				// orphanscan.go exists to recover from. Skip the ordinary
				// error-path cleanup so the temp directory is left behind
				// exactly as a real kill -9 would leave it.
				return
			}
			_ = os.RemoveAll(tempDir)
		}
	}()

	if haltAfter == phaseTempDirCreated {
		return &writeArtifactDirHaltError{phase: phaseTempDirCreated, tempDir: tempDir}
	}

	for rel, content := range files {
		// checkpoint-b09 security gate, defense in depth: every production
		// caller (capture.go) only ever populates files with a small fixed
		// set of literal names ("manifest.json", "untracked.zip", etc — never
		// anything derived from repository content), so this can never fire
		// on the real Capture path today. It is still checked here, not just
		// trusted, because writeArtifactDir is this package's own general
		// atomic-write primitive, not a Capture-only helper — a future
		// caller (or a mistake in a future edit to capture.go) that ever did
		// derive a files key from untrusted input must not be able to write
		// outside tempDir via a "../" segment or an absolute path, the same
		// posture safeArtifactPath (security.go) applies to manifest-read
		// paths and validateUntrackedPath applies to git-reported ones.
		if !safeRelativeName(rel) {
			return errIntegrity("repocheckpoint: refusing to write artifact %q: not a safe relative path", rel)
		}
		path := filepath.Join(tempDir, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return fmt.Errorf("repocheckpoint: create dir for %s: %w", rel, mkErr)
		}
		if writeErr := writeFileFsync(path, content); writeErr != nil {
			return fmt.Errorf("repocheckpoint: write %s: %w", rel, writeErr)
		}
	}

	if haltAfter == phaseFilesWritten {
		return &writeArtifactDirHaltError{phase: phaseFilesWritten, tempDir: tempDir}
	}

	if syncErr := syncDir(tempDir); syncErr != nil {
		return fmt.Errorf("repocheckpoint: fsync temp dir: %w", syncErr)
	}

	if renameErr := os.Rename(tempDir, finalDir); renameErr != nil {
		return fmt.Errorf("repocheckpoint: rename %s to %s: %w", tempDir, finalDir, renameErr)
	}

	if haltAfter == phaseRenamed {
		// The rename already succeeded and is durable — finalDir now
		// exists and tempDir does not — so this halt simulates a crash
		// AFTER the artifact is already fully, correctly committed but
		// before the best-effort parent-directory fsync below. Nothing for
		// an orphan scan to find or fix here; recorded purely so a test can
		// assert this window is also safe (finalDir fully present, no
		// dangling temp dir).
		return &writeArtifactDirHaltError{phase: phaseRenamed}
	}

	if syncErr := syncDir(parent); syncErr != nil {
		// The rename itself already succeeded and is durable per POSIX
		// rename semantics on most filesystems; failing to additionally
		// fsync the parent directory entry is a durability-of-metadata
		// concern, not a correctness one, so this is reported but the
		// checkpoint is still considered written.
		return fmt.Errorf("repocheckpoint: fsync parent dir after rename (checkpoint directory itself was renamed successfully): %w", syncErr)
	}
	return nil
}

// writeFileFsync writes content to path and fsyncs the file before
// closing, so the bytes are durable before this function returns (ADD
// §19.3 step 12 "fsync").
func writeFileFsync(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir fsyncs a directory's own metadata (its entry list), which on
// POSIX systems requires opening it like a file and calling Sync — needed
// after creating files inside it or renaming it into place, so the
// directory-entry change itself is durable, not just the file content.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	// Sync can return ENOTSUP/EINVAL on some platforms/filesystems for
	// directories (notably some non-journaled or network filesystems);
	// tolerate that rather than failing an otherwise-successful capture,
	// since the file-level fsyncs already ran.
	if err := d.Sync(); err != nil && !isDirSyncUnsupported(err) {
		return err
	}
	return nil
}
