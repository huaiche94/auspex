// atomicwrite.go: the staged temp-directory-to-final-directory write
// protocol (agents/checkpoint.md Part B deliverable #7's core mechanic;
// ADD §19.3 capture steps 3/9/12/13: temp directory, ..., fsync, atomic
// rename). A checkpoint's artifact directory either exists complete or
// does not exist at all from any external observer's point of view — there
// is no window where a reader sees a half-written checkpoint directory.
//
// Full crash-recovery orphan-scan hardening across process restarts is
// left to a later node's scope (mirroring internal/progress's own staged
// protocol split between checkpoint-a02's stores and checkpoint-a04's
// crash-injection tests) — this file delivers the single-process atomic
// write itself, which is what "create and verify" concretely requires.
package repocheckpoint

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeArtifactDir writes every entry in files (relative path -> content)
// into a fresh temp directory beside finalDir, fsyncs each file and the
// temp directory, then atomically renames the temp directory to finalDir.
// If finalDir already exists this fails closed (a checkpoint ID collision
// is a state-integrity bug, not something to silently overwrite) rather
// than clobbering existing evidence.
//
// On any failure the temp directory is removed before returning, so a
// failed capture never leaves an orphaned partial directory next to
// finalDir for a casual directory listing to mistake as real.
func writeArtifactDir(finalDir string, files map[string][]byte) (err error) {
	if _, statErr := os.Stat(finalDir); statErr == nil {
		return errIntegrity("repocheckpoint: artifact directory %s already exists; refusing to overwrite existing checkpoint evidence", finalDir)
	}

	parent := filepath.Dir(finalDir)
	if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
		return fmt.Errorf("repocheckpoint: create parent dir %s: %w", parent, mkErr)
	}

	tempDir, mkErr := os.MkdirTemp(parent, ".checkpoint-tmp-*")
	if mkErr != nil {
		return fmt.Errorf("repocheckpoint: create temp dir: %w", mkErr)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tempDir)
		}
	}()

	for rel, content := range files {
		path := filepath.Join(tempDir, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return fmt.Errorf("repocheckpoint: create dir for %s: %w", rel, mkErr)
		}
		if writeErr := writeFileFsync(path, content); writeErr != nil {
			return fmt.Errorf("repocheckpoint: write %s: %w", rel, writeErr)
		}
	}

	if syncErr := syncDir(tempDir); syncErr != nil {
		return fmt.Errorf("repocheckpoint: fsync temp dir: %w", syncErr)
	}

	if renameErr := os.Rename(tempDir, finalDir); renameErr != nil {
		return fmt.Errorf("repocheckpoint: rename %s to %s: %w", tempDir, finalDir, renameErr)
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
