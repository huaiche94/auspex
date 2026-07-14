// run_test.go: command-surface coverage for `auspex run` (issue #8) —
// flag validation and the BLOCK-refuses-to-spawn contract at the Cobra
// boundary, with the same fakes-based HookDeps the other command tests
// use. The full happy path (real DB, real evaluation pipeline, compiled
// fake provider binary, persisted events, attribution JSON) lives in
// internal/integrationtest/managedrun_test.go.
package cli_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// newRunTestRoot builds a minimal root with the REAL run command wired
// against deps, wrapped exactly as wiring.App.RootCmd wraps it, so error
// rendering matches production.
func newRunTestRoot(deps orchestrator.HookDeps) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	root := &cobra.Command{Use: "auspex", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(cli.NewRunCmd(deps))
	root = cli.WithJSONErrorRendering(root)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	return root, &stdout, &stderr
}

func blockingEvaluationFake() *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-cli-block", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-cli-block", Action: app.PolicyBlock}, nil
		},
	}
}

func TestRunCmd_MissingRequiredFlags_ValidationError(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing session-id", []string{"run", "--worktree-id", "wt-1", "--", "do something"}},
		{"missing worktree-id", []string{"run", "--session-id", "sess-1", "--", "do something"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, stdout, _ := newRunTestRoot(orchestrator.HookDeps{})
			root.SetArgs(tc.args)
			err := root.Execute()
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
				t.Fatalf("err = %v, want *domain.Error with code %q", err, domain.ErrCodeValidation)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty on a validation error (machine surface stays clean)", stdout.String())
			}
		})
	}
}

func TestRunCmd_NoPromptArgs_IsAnArgsError(t *testing.T) {
	root, _, _ := newRunTestRoot(orchestrator.HookDeps{})
	root.SetArgs([]string{"run", "--session-id", "sess-1", "--worktree-id", "wt-1"})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute() = nil, want an error for a run with no prompt args")
	}
}

func TestRunCmd_BlockDecision_TypedErrorNoAttributionNoSpawn(t *testing.T) {
	// Clock/IDs are managed.Runner's two hard requirements; reuse this
	// test package's established doubles (evaluate_test.go's
	// fixedTestClock/seqTestIDs — same cli_test binary).
	deps := orchestrator.HookDeps{
		Evaluation: blockingEvaluationFake(),
		Clock:      fixedTestClock{},
		IDs:        &seqTestIDs{},
	}

	root, stdout, stderr := newRunTestRoot(deps)
	// A provider-bin that cannot exist: reaching the spawn would fail
	// with ErrCodeUnavailable, so the unauthorized assertion below also
	// proves no spawn was attempted.
	root.SetArgs([]string{
		"run",
		"--session-id", "sess-cli-1",
		"--worktree-id", "wt-cli-1",
		"--provider-bin", filepath.Join(t.TempDir(), "never-a-binary"),
		"--", "rewrite the auth layer",
	})

	err := root.Execute()
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %v (%T), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeUnauthorized {
		t.Errorf("err.Code = %q, want %q (BLOCK, not a spawn failure)", derr.Code, domain.ErrCodeUnauthorized)
	}
	if derr.Details["evaluation_id"] != "eval-cli-block" {
		t.Errorf("err.Details = %v, want the blocking evaluation named", derr.Details)
	}
	if bytes.Contains(stdout.Bytes(), []byte("auspex.run.v1")) {
		t.Errorf("stdout carries attribution JSON on a blocked run:\n%s", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(`"schema_version":"auspex.error.v1"`)) {
		t.Errorf("stderr lacks the typed error envelope:\n%s", stderr.String())
	}
	// The prompt itself must never appear in any output (Constitution §7
	// rule 2).
	for name, buf := range map[string]*bytes.Buffer{"stdout": stdout, "stderr": stderr} {
		if bytes.Contains(buf.Bytes(), []byte("rewrite the auth layer")) {
			t.Errorf("raw prompt leaked into %s:\n%s", name, buf.String())
		}
	}
}
