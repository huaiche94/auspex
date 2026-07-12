package cli

import (
	"testing"
)

// TestRootCommandTreeHasP0Surface confirms every P0 command named in
// agents/runtime.md Part B is registered under the root command, using the
// kebab-case spelling this package standardized on (see doc.go).
func TestRootCommandTreeHasP0Surface(t *testing.T) {
	root := NewRootCmd()

	paths := [][]string{
		{"version"},
		{"init"},
		{"hook", "claude", "statusline"},
		{"hook", "claude", "user-prompt-submit"},
		{"hook", "claude", "stop"},
		{"hook", "claude", "stop-failure"},
		{"evaluate"},
		{"decision", "allow"},
		{"decision", "deny"},
		{"checkpoint", "create"},
		{"progress", "show"},
		{"state", "show"},
		{"pause", "request"},
		{"pause", "cancel"},
		{"resume"},
		{"scheduler", "run-once"},
		{"status"},
		{"doctor"},
	}

	for _, path := range paths {
		t.Run(joinPath(path), func(t *testing.T) {
			cmd, remaining, err := root.Find(path)
			if err != nil {
				t.Fatalf("find %v: %v", path, err)
			}
			if len(remaining) != 0 {
				t.Fatalf("find %v: unresolved args %v (command tree missing a level)", path, remaining)
			}
			if cmd.Name() != path[len(path)-1] {
				t.Fatalf("find %v: resolved to command %q, want %q", path, cmd.Name(), path[len(path)-1])
			}
		})
	}
}

// TestRootCommandUse confirms the root command's identity — "preflight
// --help" (this node's DAG validation command) depends on Use being set
// correctly for Cobra's default help output to be sensible.
func TestRootCommandUse(t *testing.T) {
	root := NewRootCmd()
	if root.Use != "preflight" {
		t.Fatalf("root.Use = %q, want %q", root.Use, "preflight")
	}
	if root.Short == "" {
		t.Fatal("root.Short is empty")
	}
}

// TestHelpSucceeds exercises exactly what the DAG validation command's
// `preflight --help` invocation exercises, at the package level rather
// than via a built binary: help must not fail even though every command
// underneath is a stub.
func TestHelpSucceeds(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("preflight --help: %v", err)
	}
}

// TestVersionCommandIsReal confirms `preflight version` is the one command
// in this tree that is NOT a stub (it has no service dependency).
func TestVersionCommandIsReal(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("preflight version: %v", err)
	}
}

func joinPath(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}
