package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// CheckpointCreateDeps bundles CheckpointCreate's two collaborators — the
// checkpoint role's frozen Part A (state) and Part B (repository)
// services. Both are required; CheckpointCreate fails closed
// (ErrCodeUnavailable) if either is nil, rather than silently skipping a
// checkpoint stage.
type CheckpointCreateDeps struct {
	StateCheckpoint      app.StateCheckpointService
	RepositoryCheckpoint app.RepositoryCheckpointService
}

// CheckpointCreateRequest is `auspex checkpoint create`'s input.
type CheckpointCreateRequest struct {
	TaskID     domain.TaskID
	WorktreeID domain.WorktreeID
}

// CheckpointCreateResult bundles both checkpoints' results.
type CheckpointCreateResult struct {
	State      domain.StateCheckpoint
	Repository app.RepositoryCheckpoint
}

// CheckpointCreate implements agents/runtime.md Part B pipeline step 9:
// "`checkpoint create` calls Part A of the checkpoint role (state), then
// its Part B (repository), per the frozen transaction/orchestration
// contract."
//
// # Ordering is the whole point of this node (High risk per the DAG)
//
// State MUST be created before Repository, and MUST succeed before
// Repository is even attempted — never the reverse, and never both
// unconditionally. This mirrors CONTRACT_FREEZE.md's "Transaction
// boundaries" section on GracefulPauseService's persist phase: "a
// sequence of dependent writes... each step's own transaction boundary is
// defined by that step's owning service; runtime is responsible for
// sequencing them and handling partial-sequence failure as a resumable
// state, not a silent gap." The concrete failure mode this ordering
// prevents: if Repository were created first (or unconditionally) and
// State then failed, Auspex would be left with a Repository Checkpoint
// that has no corresponding State Checkpoint recording what task/node it
// belongs to — an orphaned repository checkpoint, exactly what this
// node's task brief calls out by name. By calling State first and
// returning immediately on its error, CheckpointCreate makes that
// orphaned-repository-checkpoint outcome structurally unreachable: the
// code path that would create a Repository Checkpoint is never even
// entered unless State already succeeded.
//
// A State Checkpoint failure is therefore fail-closed per
// CONTRACT_FREEZE.md's error contract ("State Checkpoint requested but
// failed -> fail closed", ADD §17.5): CheckpointCreate returns the
// State error as-is, with no Repository call attempted at all, and
// CheckpointCreateResult is not returned (zero value) — there is no
// partial-success shape for this operation's return value, only whole
// success or a propagated error naming which stage failed.
func CheckpointCreate(ctx context.Context, deps CheckpointCreateDeps, req CheckpointCreateRequest) (CheckpointCreateResult, error) {
	if req.TaskID == "" {
		return CheckpointCreateResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: CheckpointCreate requires a TaskID", Retryable: false,
		}
	}
	if req.WorktreeID == "" {
		return CheckpointCreateResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: CheckpointCreate requires a WorktreeID", Retryable: false,
		}
	}
	if deps.StateCheckpoint == nil {
		return CheckpointCreateResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: CheckpointCreate requires a non-nil StateCheckpointService", Retryable: false,
		}
	}
	if deps.RepositoryCheckpoint == nil {
		return CheckpointCreateResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: CheckpointCreate requires a non-nil RepositoryCheckpointService", Retryable: false,
		}
	}

	// Part A (state) FIRST. Its own error propagates verbatim and
	// RepositoryCheckpoint.Create is never called — see doc comment.
	stateCkpt, err := deps.StateCheckpoint.Create(ctx, app.CreateStateCheckpointRequest{TaskID: req.TaskID})
	if err != nil {
		return CheckpointCreateResult{}, err
	}

	// Part B (repository) SECOND, only reached because Part A succeeded.
	repoCkpt, err := deps.RepositoryCheckpoint.Create(ctx, app.CreateRepositoryCheckpointRequest{
		WorktreeID: req.WorktreeID,
		TaskID:     &req.TaskID,
	})
	if err != nil {
		// Per ADD §20.15 ("repo checkpoint fails -> policy decides;
		// default fail closed") this propagates as-is too: the caller
		// now has a State Checkpoint but no Repository Checkpoint — a
		// real, ADD-anticipated partial-sequence state (not the
		// orphaned-in-the-OTHER-direction bug this ordering prevents),
		// which the caller/a later reconciliation step handles per
		// CONTRACT_FREEZE.md's "resumable state, not a silent gap"
		// guidance. CheckpointCreate itself does not attempt cleanup or
		// retry here — that policy belongs above this orchestration
		// function, not inside it.
		return CheckpointCreateResult{}, err
	}

	return CheckpointCreateResult{State: stateCkpt, Repository: repoCkpt}, nil
}
