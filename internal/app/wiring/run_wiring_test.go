// run_wiring_test.go: proves App.RootCmd swaps root.go's `run` stub for
// internal/cli.NewRunCmd's real handler (issue #8, ADD §8.1) — a separate
// file from wiring_test.go deliberately, so this addition never collides
// with concurrent wiring_test.go edits (the same reason the wiring.go
// change itself is a single appended block).
package wiring_test

import (
	"testing"

	"github.com/huaiche94/auspex/internal/app/wiring"
	"github.com/huaiche94/auspex/internal/cli"
)

// TestApp_RootCmd_RunIsRealNotStub distinguishes the real command from
// the stub structurally: the real `run` declares the managed flags
// (--provider-bin et al., cli/run.go); root.go's stub declares none.
// Executing the real command here would spawn a provider process, which
// is internal/managed's and internal/integrationtest's job, not this
// wiring test's.
func TestApp_RootCmd_RunIsRealNotStub(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()

	cmd, remaining, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatalf("Find(run): %v", err)
	}
	if len(remaining) != 0 || cmd.Name() != "run" {
		t.Fatalf("Find(run) resolved to %q (remaining %v)", cmd.Name(), remaining)
	}
	for _, flag := range []string{"provider", "session-id", "worktree-id", "task-id", "provider-bin"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("wired `run` command lacks --%s — the stub was not swapped for cli.NewRunCmd", flag)
		}
	}
}

// TestBareRootCmd_RunIsStub is the counterpart guard: the bare
// cli.NewRootCmd() tree (no wired services) must keep the honest
// notImplemented stub, flagless.
func TestBareRootCmd_RunIsStub(t *testing.T) {
	root := cli.NewRootCmd()
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatalf("Find(run): %v", err)
	}
	if cmd.Flags().Lookup("provider-bin") != nil {
		t.Error("bare tree's `run` declares --provider-bin — expected the flagless stub")
	}
}
