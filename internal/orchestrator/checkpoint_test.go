package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

func baseCheckpointRequest() orchestrator.CheckpointCreateRequest {
	return orchestrator.CheckpointCreateRequest{TaskID: "task-1", WorktreeID: "wt-1"}
}

// --- The core ordering guarantee (this node's High-risk requirement) ------

// TestCheckpointCreate_CallsStateBeforeRepository is the test that proves
// this node's entire reason for existing: State Checkpoint MUST be
// created before Repository Checkpoint, never the reverse. A shared
// recorder observes the order both fakes are actually invoked in.
func TestCheckpointCreate_CallsStateBeforeRepository(t *testing.T) {
	var callOrder []string

	stateSvc := &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			callOrder = append(callOrder, "state")
			if req.TaskID != "task-1" {
				t.Errorf("StateCheckpoint.Create TaskID = %q, want task-1", req.TaskID)
			}
			return domain.StateCheckpoint{ID: "sc-1", TaskID: req.TaskID}, nil
		},
	}
	repoSvc := &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			callOrder = append(callOrder, "repository")
			if req.WorktreeID != "wt-1" {
				t.Errorf("RepositoryCheckpoint.Create WorktreeID = %q, want wt-1", req.WorktreeID)
			}
			if req.TaskID == nil || *req.TaskID != "task-1" {
				t.Errorf("RepositoryCheckpoint.Create TaskID = %v, want task-1", req.TaskID)
			}
			return app.RepositoryCheckpoint{ID: "rc-1", GitHead: "deadbeef", Status: "complete"}, nil
		},
	}

	result, err := orchestrator.CheckpointCreate(context.Background(), orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      stateSvc,
		RepositoryCheckpoint: repoSvc,
	}, baseCheckpointRequest())
	if err != nil {
		t.Fatalf("CheckpointCreate: %v", err)
	}

	if len(callOrder) != 2 || callOrder[0] != "state" || callOrder[1] != "repository" {
		t.Fatalf("call order = %v, want [state, repository] — state MUST be called before repository", callOrder)
	}
	if result.State.ID != "sc-1" {
		t.Errorf("result.State.ID = %q, want sc-1", result.State.ID)
	}
	if result.Repository.ID != "rc-1" {
		t.Errorf("result.Repository.ID = %q, want rc-1", result.Repository.ID)
	}
}

// TestCheckpointCreate_StateFailureNeverCallsRepository proves the
// fail-closed half of the ordering guarantee: when State Checkpoint
// creation fails, RepositoryCheckpoint.Create must NEVER be invoked at
// all — not called-then-ignored, not called-and-rolled-back, simply never
// reached. This is what makes an orphaned repository checkpoint
// (a repository checkpoint with no corresponding state checkpoint)
// structurally unreachable through this code path.
func TestCheckpointCreate_StateFailureNeverCallsRepository(t *testing.T) {
	repositoryCalled := false
	wantErr := &domain.Error{Code: domain.ErrCodeIntegrity, Message: "state checkpoint hash mismatch", Retryable: false}

	stateSvc := &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{}, wantErr
		},
	}
	repoSvc := &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			repositoryCalled = true
			return app.RepositoryCheckpoint{}, nil
		},
	}

	result, err := orchestrator.CheckpointCreate(context.Background(), orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      stateSvc,
		RepositoryCheckpoint: repoSvc,
	}, baseCheckpointRequest())

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the exact StateCheckpoint.Create error propagated", err)
	}
	if repositoryCalled {
		t.Fatal("RepositoryCheckpoint.Create was called despite StateCheckpoint.Create failing — orphaned repository checkpoint risk")
	}
	if result.State.ID != "" || result.Repository.ID != "" {
		t.Errorf("result = %+v, want zero value on a failed CheckpointCreate", result)
	}
}

// TestCheckpointCreate_RepositoryFailurePropagatesWithStateAlreadyDone
// proves the OTHER partial-sequence outcome ADD §20.15 anticipates
// ("repo checkpoint fails -> policy decides; default fail closed"): if
// State succeeds but Repository then fails, the error still propagates
// (fail closed), but State was legitimately already created (this is not
// the bug the ordering prevents — it's the accepted, ADD-anticipated
// resumable partial state CONTRACT_FREEZE.md describes).
func TestCheckpointCreate_RepositoryFailurePropagatesWithStateAlreadyDone(t *testing.T) {
	stateCreated := false
	wantErr := &domain.Error{Code: domain.ErrCodeIntegrity, Message: "git repository busy", Retryable: true}

	stateSvc := &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			stateCreated = true
			return domain.StateCheckpoint{ID: "sc-1", TaskID: req.TaskID}, nil
		},
	}
	repoSvc := &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			return app.RepositoryCheckpoint{}, wantErr
		},
	}

	result, err := orchestrator.CheckpointCreate(context.Background(), orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      stateSvc,
		RepositoryCheckpoint: repoSvc,
	}, baseCheckpointRequest())

	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the exact RepositoryCheckpoint.Create error propagated", err)
	}
	if !stateCreated {
		t.Fatal("StateCheckpoint.Create was never called")
	}
	if result.State.ID != "" {
		t.Errorf("result.State.ID = %q, want empty (CheckpointCreate returns zero-value result on any error, even though the state checkpoint itself really was created upstream)", result.State.ID)
	}
}

// --- Validation / nil-dependency fail-closed tests -------------------------

func TestCheckpointCreate_ValidatesRequiredFields(t *testing.T) {
	deps := orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      &fakes.FakeStateCheckpointService{},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{},
	}
	cases := []orchestrator.CheckpointCreateRequest{
		{TaskID: "", WorktreeID: "wt-1"},
		{TaskID: "task-1", WorktreeID: ""},
	}
	for i, req := range cases {
		_, err := orchestrator.CheckpointCreate(context.Background(), deps, req)
		if err == nil {
			t.Errorf("case %d: expected a validation error, got nil", i)
			continue
		}
		var derr *domain.Error
		if !errors.As(err, &derr) {
			t.Errorf("case %d: err = %T, want *domain.Error", i, err)
			continue
		}
		if derr.Code != domain.ErrCodeValidation {
			t.Errorf("case %d: Code = %q, want %q", i, derr.Code, domain.ErrCodeValidation)
		}
	}
}

func TestCheckpointCreate_NilStateCheckpointServiceFailsClosed(t *testing.T) {
	deps := orchestrator.CheckpointCreateDeps{
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{},
	}
	_, err := orchestrator.CheckpointCreate(context.Background(), deps, baseCheckpointRequest())
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T, want *domain.Error", err)
	}
	if derr.Code != domain.ErrCodeUnavailable {
		t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeUnavailable)
	}
}

func TestCheckpointCreate_NilRepositoryCheckpointServiceFailsClosed(t *testing.T) {
	stateCalled := false
	deps := orchestrator.CheckpointCreateDeps{
		StateCheckpoint: &fakes.FakeStateCheckpointService{
			CreateFunc: func(_ context.Context, _ app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				stateCalled = true
				return domain.StateCheckpoint{ID: "sc-1"}, nil
			},
		},
	}
	_, err := orchestrator.CheckpointCreate(context.Background(), deps, baseCheckpointRequest())
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T, want *domain.Error", err)
	}
	if derr.Code != domain.ErrCodeUnavailable {
		t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeUnavailable)
	}
	// This is checked up front (both deps validated before any call), so
	// State must not have been called either — consistent fail-closed
	// behavior regardless of which dependency is missing.
	if stateCalled {
		t.Error("StateCheckpoint.Create was called despite RepositoryCheckpointService being nil")
	}
}
