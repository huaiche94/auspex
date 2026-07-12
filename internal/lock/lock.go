// Package lock provides a single-machine, single-daemon advisory file lock.
//
// Preflight is a local-first modular monolith (ADD §1.4): exactly one
// daemon process per machine (or per runtime directory, for multi-user/
// multi-checkout setups) is meant to own a given SQLite database and
// runtime directory at a time. This package exists to give that daemon —
// and any short-lived CLI invocation that needs to assert "no other
// Preflight process is using this runtime directory right now" — a simple,
// crash-safe way to detect and prevent concurrent ownership.
//
// This is intentionally NOT a general-purpose distributed lock, NOT a
// network lock, and NOT a replacement for SQLite's own WAL/busy-timeout
// concurrency control (internal/storage/sqlite owns that, foundation-05).
// It is a single PID-file-style advisory lock scoped to one local
// filesystem path, which is all a single-machine local daemon needs
// (agents/foundation.md's "reduced scope" instruction for this node).
package lock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrLocked is returned by Acquire when another live process already holds
// the lock at the given path.
var ErrLocked = errors.New("lock: already held by another process")

// FileLock is an acquired advisory lock backed by a lock file on disk. The
// zero value is not usable; construct one via Acquire.
type FileLock struct {
	path string
	file *os.File
}

// Path returns the filesystem path of the lock file.
func (l *FileLock) Path() string {
	return l.path
}

// Release releases the lock and removes the lock file. Release is
// idempotent: calling it more than once, or on an already-released lock,
// returns nil.
func (l *FileLock) Release() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	// Best-effort remove; a missing file (e.g. already cleaned up by
	// another Release call, or removed out-of-band) is not an error.
	if rmErr := os.Remove(l.path); rmErr != nil && !os.IsNotExist(rmErr) {
		if err == nil {
			err = rmErr
		}
	}
	return err
}

// Acquire attempts to take an exclusive advisory lock at path, writing the
// current process's PID into the lock file. path's parent directory must
// already exist (this package does not create directories — callers using
// internal/paths already have a directory-creation story for the runtime
// dir this lock file typically lives under).
//
// If a lock file already exists, Acquire inspects it:
//   - if the file contains a PID that corresponds to a live process,
//     Acquire returns ErrLocked — another Preflight process genuinely
//     holds this lock;
//   - if the file is stale (its PID is not a live process, or the file is
//     empty/corrupt), Acquire treats it as an abandoned lock from a
//     process that crashed without cleaning up, removes it, and proceeds
//     to acquire fresh. This is the crash-safety property a single-daemon
//     local tool needs: a dead daemon must never permanently wedge the
//     lock for the machine.
func Acquire(path string) (*FileLock, error) {
	for {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			if _, werr := f.WriteString(strconv.Itoa(os.Getpid())); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("lock: writing pid to %s: %w", path, werr)
			}
			if serr := f.Sync(); serr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("lock: syncing %s: %w", path, serr)
			}
			return &FileLock{path: path, file: f}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("lock: creating %s: %w", path, err)
		}

		// A lock file already exists. Decide whether it's live or stale.
		stale, inspectErr := isStale(path)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if !stale {
			return nil, fmt.Errorf("%w: %s", ErrLocked, path)
		}

		// Stale: the owning PID is dead (or the file is unreadable/
		// corrupt). Remove it and retry the exclusive create. A
		// concurrent process could win this race, in which case the
		// next O_EXCL attempt above simply loops back into this same
		// staleness check against whatever is there now.
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return nil, fmt.Errorf("lock: removing stale lock %s: %w", path, rmErr)
		}
	}
}

// isStale reports whether the lock file at path was left behind by a
// process that is no longer running (or the file's contents cannot be
// interpreted as a live PID at all, which is treated the same way: it
// cannot possibly represent a currently-running owner).
func isStale(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Raced with a concurrent Release; nothing to consider
			// stale, but also nothing blocking us — the caller's next
			// O_EXCL attempt will just succeed.
			return true, nil
		}
		return false, fmt.Errorf("lock: reading %s: %w", path, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		// Unparseable contents can never represent a live PID, so this is
		// staleness, not an error to propagate.
		//nolint:nilerr // deliberate: a parse failure means "stale", not "propagate this error"
		return true, nil
	}

	return !processAlive(pid), nil
}
