// state_test.go: CLI-level tests for the REAL `auspex state show`
// command (issue #138; state.go), following progress_test.go's exact
// conventions: newTestRoot's production-accurate root configuration,
// internal/testutil/fakes doubles, typed-error and JSON-document asserts.
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

func stateShowCheckpoint() domain.StateCheckpoint {
	active := domain.ProgressNodeID("n2")
	repoCkpt := domain.RepositoryCheckpointID("rc-1")
	return domain.StateCheckpoint{
		ID:                     "sc-9",
		TaskID:                 "task-9",
		ProgressTreeVersion:    4,
		ActiveNodeID:           &active,
		CompletedNodeIDs:       []domain.ProgressNodeID{"n1"},
		NextAction:             domain.NextAction{Description: "resume node n2", NodeID: &active},
		RepositoryCheckpointID: &repoCkpt,
		CreatedAt:              time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
		IntegritySHA256:        "abc123",
	}
}

// TestStateShow_RequiresTask proves the real `state show` fails closed
// with a typed validation error when --task is omitted.
func TestStateShow_RequiresTask(t *testing.T) {
	root := newTestRoot(cli.NewStateCmd(&fakes.FakeStateCheckpointService{}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"state", "show"})

	err := root.Execute()
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("state show without --task: err = %v, want ErrCodeValidation", err)
	}
}

// TestStateShow_RendersLatestCheckpoint drives the real `state show`
// through the frozen LoadLatest port in both output modes.
func TestStateShow_RendersLatestCheckpoint(t *testing.T) {
	svc := &fakes.FakeStateCheckpointService{
		LoadLatestFunc: func(_ context.Context, taskID domain.TaskID) (domain.StateCheckpoint, error) {
			if taskID != "task-9" {
				t.Errorf("LoadLatest taskID = %q, want task-9", taskID)
			}
			return stateShowCheckpoint(), nil
		},
	}

	root := newTestRoot(cli.NewStateCmd(svc))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"state", "show", "--task", "task-9", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("state show --json: %v", err)
	}
	var doc struct {
		SchemaVersion          string  `json:"schema_version"`
		CheckpointID           string  `json:"checkpoint_id"`
		TaskID                 string  `json:"task_id"`
		ProgressTreeVersion    int64   `json:"progress_tree_version"`
		ActiveNodeID           *string `json:"active_node_id"`
		CompletedNodeCount     int     `json:"completed_node_count"`
		NextAction             string  `json:"next_action"`
		NextActionNodeID       *string `json:"next_action_node_id"`
		RepositoryCheckpointID *string `json:"repository_checkpoint_id"`
		CreatedAt              string  `json:"created_at"`
		IntegritySHA256        string  `json:"integrity_sha256"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != "auspex.state-show.v1" || doc.CheckpointID != "sc-9" || doc.TaskID != "task-9" ||
		doc.ProgressTreeVersion != 4 || doc.CompletedNodeCount != 1 ||
		doc.ActiveNodeID == nil || *doc.ActiveNodeID != "n2" ||
		doc.NextAction != "resume node n2" || doc.NextActionNodeID == nil || *doc.NextActionNodeID != "n2" ||
		doc.RepositoryCheckpointID == nil || *doc.RepositoryCheckpointID != "rc-1" ||
		doc.CreatedAt != "2026-07-29T10:00:00Z" || doc.IntegritySHA256 != "abc123" {
		t.Fatalf("state show JSON = %+v", doc)
	}

	root = newTestRoot(cli.NewStateCmd(svc))
	out.Reset()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"state", "show", "--task", "task-9"})
	if err := root.Execute(); err != nil {
		t.Fatalf("state show (human): %v", err)
	}
	for _, want := range []string{"checkpoint sc-9", "task task-9", "tree version 4", "1 completed node(s)", "active node n2", "resume node n2", "rc-1", "abc123"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human output %q missing %q", out.String(), want)
		}
	}
}

// TestStateShow_ServiceErrorSurfacesVerbatim proves a LoadLatest error
// (e.g. no checkpoint yet) surfaces as the service's own typed error,
// never a fabricated success.
func TestStateShow_ServiceErrorSurfacesVerbatim(t *testing.T) {
	svc := &fakes.FakeStateCheckpointService{
		LoadLatestFunc: func(_ context.Context, _ domain.TaskID) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "no state checkpoint for task", Retryable: false}
		},
	}
	root := newTestRoot(cli.NewStateCmd(svc))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"state", "show", "--task", "task-9"})

	err := root.Execute()
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("state show with erroring service: err = %v, want the service's ErrCodeNotFound verbatim", err)
	}
}
