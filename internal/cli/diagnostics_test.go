package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// --- auspex status --------------------------------------------------

func TestStatusCmd_RequiresSessionIDFlag(t *testing.T) {
	cmd := cli.NewStatusCmd(orchestrator.StatusDeps{})
	cmd.SetArgs([]string{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when --session-id is omitted")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T (%v), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeValidation {
		t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeValidation)
	}
}

func TestStatusCmd_ProducesValidJSONWithSessionID(t *testing.T) {
	cmd := cli.NewStatusCmd(orchestrator.StatusDeps{})
	cmd.SetArgs([]string{"--session-id", "sess-1"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", err, out.String())
	}
	if decoded["session_id"] != "sess-1" {
		t.Errorf("session_id = %v, want sess-1", decoded["session_id"])
	}
	if decoded["has_progress_tree"] != false {
		t.Errorf("has_progress_tree = %v, want false (no ProgressTree service configured)", decoded["has_progress_tree"])
	}
}

func TestStatusCmd_ReportsProgressTreeWhenTaskIDGiven(t *testing.T) {
	svc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			return app.ProgressTreeSnapshot{TaskID: taskID, Nodes: []app.ProgressNode{{ID: "n1"}}}, nil
		},
	}
	cmd := cli.NewStatusCmd(orchestrator.StatusDeps{ProgressTree: svc})
	cmd.SetArgs([]string{"--session-id", "sess-1", "--task-id", "task-1"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if decoded["has_progress_tree"] != true {
		t.Errorf("has_progress_tree = %v, want true", decoded["has_progress_tree"])
	}
	if decoded["progress_tree_task_id"] != "task-1" {
		t.Errorf("progress_tree_task_id = %v, want task-1", decoded["progress_tree_task_id"])
	}
}

// --- auspex doctor ----------------------------------------------------

func TestDoctorCmd_ProducesValidJSON_NoDepsConfigured(t *testing.T) {
	cmd := cli.NewDoctorCmd(orchestrator.DoctorDeps{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v (doctor must exit 0 even with nothing configured)", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", err, out.String())
	}
	if decoded["healthy"] != true {
		t.Errorf("healthy = %v, want true (all-skipped is not unhealthy)", decoded["healthy"])
	}
	checks, ok := decoded["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("checks = %v, want a non-empty array", decoded["checks"])
	}
}

func TestDoctorCmd_ReportsFailingRequiredDir(t *testing.T) {
	cmd := cli.NewDoctorCmd(orchestrator.DoctorDeps{RequiredDirs: []string{"/definitely/does/not/exist/auspex-doctor-test"}})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// doctor itself must still exit 0 — a failing check is content in the
	// report, not a command execution error (see diagnostics.go's
	// NewDoctorCmd doc comment).
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v, want nil error even when a check fails", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if decoded["healthy"] != false {
		t.Errorf("healthy = %v, want false (a missing required directory must fail the report)", decoded["healthy"])
	}
}

func TestDoctorCmd_Args_NoArgsAllowed(t *testing.T) {
	cmd := cli.NewDoctorCmd(orchestrator.DoctorDeps{})
	cmd.SetArgs([]string{"unexpected-arg"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected a usage error for an unexpected positional argument")
	}
}
