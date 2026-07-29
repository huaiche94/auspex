// progress_test.go: CLI-level tests for the REAL `auspex progress
// complete` command (issue #1's explicit-completion half; progress.go),
// following this package's established conventions: commands are executed
// under newTestRoot's production-accurate root configuration
// (errorcontract_test.go — SilenceUsage/SilenceErrors: true, JSON error
// rendering applied), services are internal/testutil/fakes doubles, and
// error paths are asserted against both the returned *domain.Error and
// the rendered JSON envelope, exactly like the checkpoint-create tests.
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// completingProgressTree returns a FakeProgressTreeService whose
// CompleteNode succeeds, capturing the request it received into *captured.
func completingProgressTree(captured *app.CompleteNodeRequest) *fakes.FakeProgressTreeService {
	return &fakes.FakeProgressTreeService{
		CompleteNodeFunc: func(_ context.Context, req app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error) {
			if captured != nil {
				*captured = req
			}
			return app.ProgressNode{ID: req.NodeID, TaskID: "task-1", Status: domain.NodeCompleted, Kind: domain.NodeDocumentSection},
				domain.StateCheckpoint{ID: "sc-1", TaskID: "task-1"}, nil
		},
	}
}

// TestProgressComplete_HappyPath_JSON drives the full happy path in
// machine mode: required flags plus two repeatable --artifact specs, and
// asserts (a) the frozen app.CompleteNodeRequest the service received —
// caller-supplied idempotency key threaded through verbatim, kind=path
// specs mapped to domain.ArtifactRef with file: URIs — and (b) the
// schema-versioned JSON success output carrying the node status and state
// checkpoint ID.
func TestProgressComplete_HappyPath_JSON(t *testing.T) {
	var captured app.CompleteNodeRequest
	root := newTestRoot(cli.NewProgressCmd(orchestrator.ProgressCompleteDeps{
		ProgressTree: completingProgressTree(&captured),
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{
		"progress", "complete",
		"--node", "node-1",
		"--idempotency-key", "caller-key-1",
		"--artifact", "file=/tmp/section.md",
		"--artifact", "report=/tmp/report.md",
		"--json",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if captured.NodeID != "node-1" {
		t.Errorf("CompleteNodeRequest.NodeID = %q, want %q", captured.NodeID, "node-1")
	}
	if captured.IdempotencyKey != "caller-key-1" {
		t.Errorf("CompleteNodeRequest.IdempotencyKey = %q, want the caller-supplied %q", captured.IdempotencyKey, "caller-key-1")
	}
	if len(captured.Artifacts) != 2 {
		t.Fatalf("len(Artifacts) = %d, want 2", len(captured.Artifacts))
	}
	if captured.Artifacts[0].Kind != "file" || captured.Artifacts[0].URI != "file:/tmp/section.md" {
		t.Errorf("Artifacts[0] = %+v, want Kind=file URI=file:/tmp/section.md", captured.Artifacts[0])
	}
	if captured.Artifacts[1].Kind != "report" || captured.Artifacts[1].URI != "file:/tmp/report.md" {
		t.Errorf("Artifacts[1] = %+v, want Kind=report URI=file:/tmp/report.md", captured.Artifacts[1])
	}

	var success struct {
		SchemaVersion     string `json:"schema_version"`
		NodeID            string `json:"node_id"`
		NodeStatus        string `json:"node_status"`
		StateCheckpointID string `json:"state_checkpoint_id"`
	}
	if err := json.Unmarshal(out.Bytes(), &success); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (body=%s)", err, out.Bytes())
	}
	if success.SchemaVersion != "auspex.progress-complete.v1" {
		t.Errorf("SchemaVersion = %q, want %q", success.SchemaVersion, "auspex.progress-complete.v1")
	}
	if success.NodeID != "node-1" {
		t.Errorf("node_id = %q, want node-1", success.NodeID)
	}
	if success.NodeStatus != string(domain.NodeCompleted) {
		t.Errorf("node_status = %q, want %q", success.NodeStatus, domain.NodeCompleted)
	}
	if success.StateCheckpointID != "sc-1" {
		t.Errorf("state_checkpoint_id = %q, want sc-1", success.StateCheckpointID)
	}
	if bytes.Contains(out.Bytes(), []byte(cli.SchemaVersionError)) {
		t.Errorf("success output unexpectedly contains the error schema version: %s", out.Bytes())
	}
}

// TestProgressComplete_HappyPath_HumanMode confirms the default (no
// --json) output still names the two things the command exists to report:
// the node's resulting status and the state checkpoint ID.
func TestProgressComplete_HappyPath_HumanMode(t *testing.T) {
	root := newTestRoot(cli.NewProgressCmd(orchestrator.ProgressCompleteDeps{
		ProgressTree: completingProgressTree(nil),
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"progress", "complete", "--node", "node-1", "--idempotency-key", "k1", "--artifact", "file=/tmp/a.md"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"node-1", string(domain.NodeCompleted), "sc-1"} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Errorf("human output %q missing %q", out.String(), want)
		}
	}
}

// TestProgressComplete_ValidatorRejection_SurfacesTypedErrorContract
// drives the validator-rejection path: the service returns the typed
// *domain.Error internal/progress's validators produce (a validation
// rejection is the completion protocol's own considered output —
// Constitution §6.2's evidence gate), and the command must surface it
// through the SAME uniform contract as every other command: the returned
// Go error is the untouched *domain.Error AND stderr carries the
// schema-versioned JSON envelope with matching fields.
func TestProgressComplete_ValidatorRejection_SurfacesTypedErrorContract(t *testing.T) {
	rejection := &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   "progress: artifact /tmp/section.md failed validator heading_exists",
		Retryable: false,
		Details:   map[string]string{"node_id": "node-1", "validator": "heading_exists"},
	}
	root := newTestRoot(cli.NewProgressCmd(orchestrator.ProgressCompleteDeps{
		ProgressTree: &fakes.FakeProgressTreeService{
			CompleteNodeFunc: func(context.Context, app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error) {
				return app.ProgressNode{}, domain.StateCheckpoint{}, rejection
			},
		},
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"progress", "complete", "--node", "node-1", "--idempotency-key", "k1", "--artifact", "file=/tmp/section.md", "--json"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected the validator rejection to propagate as an error")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("returned error %v is not a *domain.Error", err)
	}
	if derr.Code != domain.ErrCodeValidation {
		t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeValidation)
	}

	env := decodeErrorEnvelope(t, out.Bytes())
	if env.SchemaVersion != cli.SchemaVersionError {
		t.Errorf("envelope SchemaVersion = %q, want %q", env.SchemaVersion, cli.SchemaVersionError)
	}
	if env.Code != domain.ErrCodeValidation {
		t.Errorf("envelope Code = %q, want %q", env.Code, domain.ErrCodeValidation)
	}
	if env.Message != rejection.Message {
		t.Errorf("envelope Message = %q, want the service's own %q", env.Message, rejection.Message)
	}
}

// TestProgressComplete_MissingRequiredFlags rejects an invocation missing
// either required flag with ErrCodeValidation before any service call —
// the same required-flag discipline checkpoint create/pause request
// already follow.
func TestProgressComplete_MissingRequiredFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing --node", []string{"progress", "complete", "--idempotency-key", "k1"}},
		{"missing --idempotency-key", []string{"progress", "complete", "--node", "node-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newTestRoot(cli.NewProgressCmd(orchestrator.ProgressCompleteDeps{
				ProgressTree: &fakes.FakeProgressTreeService{
					CompleteNodeFunc: func(context.Context, app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error) {
						t.Error("CompleteNode must not be called when a required flag is missing")
						return app.ProgressNode{}, domain.StateCheckpoint{}, nil
					},
				},
			}))
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			err := root.Execute()
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
				t.Fatalf("err = %v, want ErrCodeValidation", err)
			}
		})
	}
}

// TestProgressComplete_MalformedArtifactSpec rejects an --artifact value
// that is not kind=path, without echoing ambiguity into a service call.
func TestProgressComplete_MalformedArtifactSpec(t *testing.T) {
	for _, spec := range []string{"no-separator", "=path-only", "kind-only="} {
		t.Run(spec, func(t *testing.T) {
			root := newTestRoot(cli.NewProgressCmd(orchestrator.ProgressCompleteDeps{
				ProgressTree: &fakes.FakeProgressTreeService{},
			}))
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{"progress", "complete", "--node", "n1", "--idempotency-key", "k1", "--artifact", spec})

			err := root.Execute()
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
				t.Fatalf("err = %v, want ErrCodeValidation for malformed --artifact %q", err, spec)
			}
		})
	}
}

// TestProgressShow_RequiresTask proves the real `progress show` (issue
// #138 — the leaf that replaced the former stub) fails closed with a
// typed validation error when --task is omitted, never a zero-ID
// Snapshot call.
func TestProgressShow_RequiresTask(t *testing.T) {
	root := newTestRoot(cli.NewProgressCmd(orchestrator.ProgressCompleteDeps{
		ProgressTree: &fakes.FakeProgressTreeService{},
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"progress", "show"})

	err := root.Execute()
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("progress show without --task: err = %v, want ErrCodeValidation", err)
	}
}

// TestProgressShow_RendersSnapshot drives the real `progress show`
// through the frozen Snapshot port in both output modes: --json emits
// the auspex.progress-show.v1 document verbatim-parseable, human mode
// renders one line per node.
func TestProgressShow_RendersSnapshot(t *testing.T) {
	deps := orchestrator.ProgressCompleteDeps{
		ProgressTree: &fakes.FakeProgressTreeService{
			SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
				if taskID != "task-9" {
					t.Errorf("Snapshot taskID = %q, want task-9", taskID)
				}
				return app.ProgressTreeSnapshot{
					TaskID: taskID,
					Nodes: []app.ProgressNode{
						{ID: "n1", TaskID: taskID, Status: "completed", Kind: "step"},
						{ID: "n2", TaskID: taskID, Status: "in_progress", Kind: "step"},
					},
				}, nil
			},
		},
	}

	root := newTestRoot(cli.NewProgressCmd(deps))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"progress", "show", "--task", "task-9", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("progress show --json: %v", err)
	}
	var doc struct {
		SchemaVersion string `json:"schema_version"`
		TaskID        string `json:"task_id"`
		NodeCount     int    `json:"node_count"`
		Nodes         []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Kind   string `json:"kind"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != "auspex.progress-show.v1" || doc.TaskID != "task-9" || doc.NodeCount != 2 ||
		len(doc.Nodes) != 2 || doc.Nodes[0].ID != "n1" || doc.Nodes[1].Status != "in_progress" {
		t.Fatalf("progress show JSON = %+v", doc)
	}

	root = newTestRoot(cli.NewProgressCmd(deps))
	out.Reset()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"progress", "show", "--task", "task-9"})
	if err := root.Execute(); err != nil {
		t.Fatalf("progress show (human): %v", err)
	}
	for _, want := range []string{"task task-9", "2 node(s)", "n1", "n2", "in_progress"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human output %q missing %q", out.String(), want)
		}
	}
}
