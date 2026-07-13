//go:build !windows

package lock

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid identifies a currently-running process
// on POSIX systems. os.FindProcess never fails on POSIX (it does not check
// existence), so liveness is determined by sending the null signal (0),
// which performs existence/permission checks without actually signaling
// the process, per kill(2).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// ESRCH: no such process -> definitively dead.
	// EPERM: process exists but we lack permission to signal it -> alive,
	// and per Auspex's local single-user daemon model this should not
	// occur for a lock file our own user created, but treat it as alive
	// out of caution rather than declaring another user's live process
	// stale.
	return errors.Is(err, syscall.EPERM)
}
