// service.go: wires capture.go/verify.go/store.go together behind the
// frozen app.RepositoryCheckpointService contract (internal/app/ports.go),
// so the runtime role's Part A persist phase and Part B checkpoint-create
// (this node's own DAG "Consumed by" note) can depend on the interface
// without reaching into this package's internals.
package repocheckpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
)

// Service implements app.RepositoryCheckpointService. Restore is
// deliberately unimplemented here (returns ErrCodeUnavailable): ADD §19.6
// makes actual restore a stretch goal, and this wave's DAG scopes real
// restore to checkpoint-b08 ("RestoreDryRun") — Create and Verify are this
// node's (checkpoint-b04's) full stated scope.
type Service struct {
	git           *gitx.Client
	store         *Store
	clock         domain.Clock
	ids           domain.IDGenerator
	artifactsRoot string
	opts          CaptureOptions

	// resolveRepository maps a WorktreeID to the filesystem path Capture
	// should resolve via gitx, and to the RepositoryID the manifest
	// records. This indirection exists because internal/repocheckpoint
	// does not own the worktrees table (foundation does) and must not
	// reach into it directly — the caller supplies the mapping, matching
	// the same "depend on the frozen port, not another role's storage"
	// discipline as everywhere else in this wave.
	resolveWorktree func(ctx context.Context, worktreeID domain.WorktreeID) (WorktreeLocation, error)
}

// WorktreeLocation is what a caller's resolveWorktree callback returns:
// enough for Capture to find the working tree on disk and stamp the
// manifest's repository identity.
type WorktreeLocation struct {
	RepositoryID string
	Path         string
}

// NewService constructs a Service. artifactsRoot is the directory under
// which every checkpoint's own ArtifactsRoot/<id>/ directory is written
// (ADD §19.2's `<UserDataDir>/Auspex/repositories/<repo-id>/checkpoints/`
// layout — resolving the real UserDataDir is the caller's responsibility;
// this package only needs a root to write under, consistent with
// capture.go's CaptureRequest.ArtifactsRoot doc comment).
func NewService(
	git *gitx.Client,
	store *Store,
	clock domain.Clock,
	ids domain.IDGenerator,
	artifactsRoot string,
	resolveWorktree func(ctx context.Context, worktreeID domain.WorktreeID) (WorktreeLocation, error),
	opts CaptureOptions,
) *Service {
	return &Service{
		git:             git,
		store:           store,
		clock:           clock,
		ids:             ids,
		artifactsRoot:   artifactsRoot,
		opts:            opts,
		resolveWorktree: resolveWorktree,
	}
}

var _ app.RepositoryCheckpointService = (*Service)(nil)

// Create captures a new Repository Checkpoint and persists its row. It does
// not itself open a transaction — Store.Insert runs through
// sqlite.QuerierFromContext, so a caller that wants Create's row write
// joined with other writes (e.g. runtime's pause persist-phase sequence)
// can invoke this from inside its own TxRunner.WithTx callback and get a
// single transaction, matching CONTRACT_FREEZE.md's documented boundary
// for that multi-step sequence ("each step's own transaction boundary is
// defined by that step's owning service").
func (s *Service) Create(ctx context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
	loc, err := s.resolveWorktree(ctx, req.WorktreeID)
	if err != nil {
		return app.RepositoryCheckpoint{}, fmt.Errorf("repocheckpoint: resolve worktree %s: %w", req.WorktreeID, err)
	}

	checkpointID := domain.RepositoryCheckpointID(s.ids.NewID())

	captureReq := CaptureRequest{
		CheckpointID:  checkpointID,
		RepositoryID:  loc.RepositoryID,
		WorktreeID:    req.WorktreeID,
		TaskID:        req.TaskID,
		WorktreePath:  loc.Path,
		ArtifactsRoot: s.artifactsRoot,
	}

	result, err := Capture(ctx, s.git, s.clock, captureReq, s.opts)
	if err != nil {
		return app.RepositoryCheckpoint{}, err
	}

	if err := s.store.Insert(ctx, result.Row); err != nil {
		return app.RepositoryCheckpoint{}, err
	}

	return app.RepositoryCheckpoint{
		ID:      checkpointID,
		GitHead: result.Row.GitHead,
		Status:  string(result.Row.Status),
	}, nil
}

// Verify loads the checkpoint's row and manifest and confirms every
// captured artifact still matches its recorded digest.
func (s *Service) Verify(ctx context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return app.RepositoryCheckpointVerification{}, err
	}

	result, err := Verify(row)
	if err != nil {
		return app.RepositoryCheckpointVerification{}, err
	}

	if result.Valid {
		if err := s.store.SetVerified(ctx, id, s.clock.Now().UTC().Format(time.RFC3339)); err != nil {
			return app.RepositoryCheckpointVerification{}, err
		}
	}

	return app.RepositoryCheckpointVerification{
		ID:    id,
		Valid: result.Valid,
	}, nil
}

// Restore implements checkpoint-b08's dry-run scope: it evaluates whether
// restoring req.ID's checkpoint onto its worktree's CURRENT state would
// succeed (restoredryrun.go's full ADD §19.6 check sequence), but never
// mutates the working tree, index, or refs — actual restore-that-mutates
// remains explicitly out of vertical-slice scope (this node's own DAG risk note:
// "actual restore is stretch/deferred"), so RestoreResult.Applied is
// always false here, whether or not the dry-run would have succeeded.
//
// Mapping the dry-run's rich report onto the frozen, narrow
// RestoreResult{ID, Applied} shape (this role cannot add fields to it —
// ports.go is contract-integrator-owned): a dry-run that finds no problems
// returns RestoreResult{ID, Applied:false}, nil — a successful dry-run,
// nothing applied because dry-run never applies. A dry-run that finds one
// or more problems (ADD §19.6's own checksum/identity/dirty-target/
// apply-check failures) returns a non-nil ErrCodeConflict error instead of
// a bare false-y success, with every problem joined into Message and
// individually available in Details (frozen domain.Error shape,
// CONTRACT_FREEZE.md) — a caller gets actionable diagnostics without a new
// type this role is not permitted to add to the frozen contract.
// AllowDirty, when true, downgrades a dirty-target finding from a
// problem to an informational note (ADD §19.6: "reject dirty target
// UNLESS safety checkpoint/force" — AllowDirty is this request's `force`).
func (s *Service) Restore(ctx context.Context, req app.RestoreRepositoryCheckpointRequest) (app.RestoreResult, error) {
	row, err := s.store.Get(ctx, req.ID)
	if err != nil {
		return app.RestoreResult{}, err
	}

	loc, err := s.resolveWorktree(ctx, row.WorktreeID)
	if err != nil {
		return app.RestoreResult{}, fmt.Errorf("repocheckpoint: Restore: resolve worktree %s: %w", row.WorktreeID, err)
	}

	report, err := RestoreDryRun(ctx, s.git, row, loc.Path, loc.RepositoryID)
	if err != nil {
		return app.RestoreResult{}, err
	}

	// RestoreDryRun's own Problems never includes the dirty-target check
	// (it has no AllowDirty policy input) — this is the one place that
	// check's ADD §19.6 "unless safety checkpoint/force" condition is
	// actually applied: a dirty target is a problem UNLESS req.AllowDirty
	// says otherwise.
	problems := append([]string{}, report.Problems...)
	if report.WorktreeDirty && !req.AllowDirty {
		problems = append(problems, fmt.Sprintf("worktree is dirty (%d path(s) changed) and AllowDirty was not set", len(report.WorktreeDirtyPaths)))
	}

	if len(problems) > 0 {
		details := make(map[string]string, len(problems))
		for i, p := range problems {
			details[fmt.Sprintf("problem_%d", i)] = p
		}
		return app.RestoreResult{ID: req.ID, Applied: false}, &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   fmt.Sprintf("repocheckpoint: Restore dry-run for checkpoint %s found %d problem(s) that would prevent a real restore: %v", req.ID, len(problems), problems),
			Retryable: false,
			Details:   details,
		}
	}

	return app.RestoreResult{ID: req.ID, Applied: false}, nil
}
