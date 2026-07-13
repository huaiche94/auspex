package lock_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/huaiche94/auspex/internal/lock"
)

func TestAcquire_Release_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l.Path() != path {
		t.Errorf("Path() = %q, want %q", l.Path(), path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("lock file not created: %v", err)
	}

	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file still exists after Release: err=%v", err)
	}
}

func TestAcquire_Release_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release should be a no-op, got: %v", err)
	}
}

// --- locked/busy behavior (agents/foundation.md "Required tests") ---------

func TestAcquire_HeldByLiveProcess_ReturnsErrLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	first, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })

	// The current test process is alive by construction, so a second
	// Acquire against the same path (still owned by our own live PID)
	// must be rejected as busy.
	_, err = lock.Acquire(path)
	if !errors.Is(err, lock.ErrLocked) {
		t.Errorf("second Acquire error = %v, want ErrLocked", err)
	}
}

func TestAcquire_StaleLock_FromDeadProcess_IsReclaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	deadPID := spawnAndWaitForExit(t)

	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPID)), 0o644); err != nil {
		t.Fatalf("seeding stale lock file: %v", err)
	}

	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over stale lock: %v", err)
	}
	defer func() { _ = l.Release() }()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading reclaimed lock file: %v", err)
	}
	if string(got) != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock file contents = %q, want current PID %d", got, os.Getpid())
	}
}

func TestAcquire_CorruptLockFile_IsReclaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatalf("seeding corrupt lock file: %v", err)
	}

	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over corrupt lock file: %v", err)
	}
	defer func() { _ = l.Release() }()
}

func TestAcquire_EmptyLockFile_IsReclaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seeding empty lock file: %v", err)
	}

	l, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire over empty lock file: %v", err)
	}
	defer func() { _ = l.Release() }()
}

func TestAcquire_ReacquireAfterRelease_Succeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auspex.lock")

	l1, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	l2, err := lock.Acquire(path)
	if err != nil {
		t.Fatalf("second Acquire after Release: %v", err)
	}
	defer func() { _ = l2.Release() }()
}

func TestAcquire_MissingParentDir_Errors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "auspex.lock")

	_, err := lock.Acquire(path)
	if err == nil {
		t.Fatal("expected error acquiring a lock under a nonexistent directory")
	}
}

// spawnAndWaitForExit starts and waits for a trivial short-lived child
// process, returning its PID after it has exited. The returned PID is
// guaranteed dead for the remainder of the test (barring PID reuse, which
// is not a concern on any supported CI/dev host within a single test's
// lifetime).
func spawnAndWaitForExit(t *testing.T) int {
	t.Helper()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", "exit 0")
	} else {
		cmd = exec.Command("true")
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawning short-lived process: %v", err)
	}
	return cmd.Process.Pid
}
