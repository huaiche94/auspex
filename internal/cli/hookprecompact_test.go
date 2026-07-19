// hookprecompact_test.go: command-surface coverage for the issue-#114
// compaction hook leaves — the "JSON and errors" contract at the Cobra
// boundary (always `{}` on stdout, exit success, fail-open on malformed
// stdin) and the stub tree's registration.
package cli

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/orchestrator"
)

// Package-cli-local deterministic Clock/IDGenerator fakes (the cli_test
// package's fixedTestClock/seqTestIDs live in the external test package
// and are not visible here).
type hookTestClock struct{}

func (hookTestClock) Now() time.Time {
	return time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
}

type hookTestIDs struct{ n int }

func (g *hookTestIDs) NewID() string {
	g.n++
	return "cli-id-" + strconv.Itoa(g.n)
}

func hookCompactTestDeps() orchestrator.HookDeps {
	return orchestrator.HookDeps{Clock: hookTestClock{}, IDs: &hookTestIDs{}}
}

// runHookLeaf executes one leaf of a built hook subtree with the given
// stdin, returning stdout.
func runHookLeaf(t *testing.T, root *cobra.Command, args []string, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	root.SetArgs(args)
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return out.String()
}

func TestHookClaudePreCompact_AnswersNoOpJSON(t *testing.T) {
	cmd := NewHookClaudeCmd(hookCompactTestDeps())
	got := runHookLeaf(t, cmd, []string{"pre-compact"}, `{"session_id":"s","hook_event_name":"PreCompact","trigger":"auto"}`)
	if got != "{}\n" {
		t.Errorf("stdout = %q, want {}\\n", got)
	}
}

func TestHookClaudePreCompact_MalformedStdinStillAnswersJSON(t *testing.T) {
	cmd := NewHookClaudeCmd(hookCompactTestDeps())
	got := runHookLeaf(t, cmd, []string{"pre-compact"}, `{not json`)
	if got != "{}\n" {
		t.Errorf("stdout = %q, want {}\\n (fail-open)", got)
	}
}

func TestHookClaudePostCompact_AnswersNoOpJSON(t *testing.T) {
	cmd := NewHookClaudeCmd(hookCompactTestDeps())
	got := runHookLeaf(t, cmd, []string{"post-compact"}, `{"session_id":"s","hook_event_name":"PostCompact"}`)
	if got != "{}\n" {
		t.Errorf("stdout = %q, want {}\\n", got)
	}
}

func TestHookCodexPreCompact_AnswersNoOpJSON(t *testing.T) {
	cmd := NewHookCodexCmd(hookCompactTestDeps())
	got := runHookLeaf(t, cmd, []string{"pre-compact"}, `{"session_id":"s","hook_event_name":"PreCompact"}`)
	if got != "{}\n" {
		t.Errorf("stdout = %q, want {}\\n", got)
	}
}

func TestHookCodexPostCompact_AnswersNoOpJSON(t *testing.T) {
	cmd := NewHookCodexCmd(hookCompactTestDeps())
	got := runHookLeaf(t, cmd, []string{"post-compact"}, `{"session_id":"s","hook_event_name":"PostCompact"}`)
	if got != "{}\n" {
		t.Errorf("stdout = %q, want {}\\n", got)
	}
}

// TestHookCompactStubsRegistered confirms the bare NewRootCmd() tree
// carries the compaction leaves too (an unwired binary answers an honest
// "not yet available" instead of Cobra's unknown-command error).
func TestHookCompactStubsRegistered(t *testing.T) {
	root := NewRootCmd()
	paths := [][]string{
		{"hook", "claude", "pre-compact"},
		{"hook", "claude", "post-compact"},
		{"hook", "codex", "pre-compact"},
		{"hook", "codex", "post-compact"},
	}
	for _, path := range paths {
		cmd, remaining, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if len(remaining) != 0 {
			t.Fatalf("find %v: unresolved args %v", path, remaining)
		}
		if cmd.Name() != path[len(path)-1] {
			t.Fatalf("find %v: resolved to %q", path, cmd.Name())
		}
	}
}
