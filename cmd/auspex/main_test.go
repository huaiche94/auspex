package main

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/buildinfo"
)

func TestVersionCommandPrintsVersionString(t *testing.T) {
	root := newRootCmd()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute `auspex version`: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != buildinfo.String() {
		t.Fatalf("version command printed %q, want %q", got, buildinfo.String())
	}
	if got == "" {
		t.Fatal("version command printed an empty string")
	}
}

func TestRootCommandHasVersionSubcommand(t *testing.T) {
	root := newRootCmd()

	cmd, _, err := root.Find([]string{"version"})
	if err != nil {
		t.Fatalf("find version subcommand: %v", err)
	}
	if cmd.Name() != "version" {
		t.Fatalf("found command %q, want %q", cmd.Name(), "version")
	}
}

// TestRootContext_CancelledOnSIGTERM pins the issue-#88 fix: the root
// context's signal set must include SIGTERM (not just SIGINT), so that
// `kill -TERM <auspex>` cancels every context-scoped operation — most
// importantly the managed runner's exec.CommandContext provider child,
// which a missing signal handler previously orphaned. The signal is sent
// to this test process itself; while rootContext's NotifyContext
// subscription is live, SIGTERM is relayed to the context instead of
// terminating the process.
func TestRootContext_CancelledOnSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows has no SIGTERM: os.Process.Signal only supports Kill, so
		// a process cannot self-deliver SIGTERM to exercise this path. The
		// production signal set (os.Interrupt + syscall.SIGTERM) still
		// compiles and registers there; SIGTERM is simply never OS-delivered.
		t.Skip("SIGTERM is not deliverable via os.Process.Signal on windows")
	}

	ctx, stop := rootContext()
	defer stop()

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal(SIGTERM): %v", err)
	}

	select {
	case <-ctx.Done():
		// cancelled — the #88 path works.
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not cancel the root context (#88)")
	}
}
