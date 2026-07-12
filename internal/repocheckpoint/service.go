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

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/gitx"
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
// (ADD §19.2's `<UserDataDir>/Preflight/repositories/<repo-id>/checkpoints/`
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

// Restore is out of this node's scope (see the type doc comment above) —
// real restore is ADD §19.6's stretch goal, dry-run restore is
// checkpoint-b08's DAG node. Returning ErrCodeUnavailable here (rather than
// a panic or a silent no-op) keeps the frozen interface fully implemented
// while making the gap explicit to any caller, per Constitution §7 rule 3
// ("capability gaps are surfaced explicitly, never silently assumed
// away") applied to an internal capability, not just a provider one.
func (s *Service) Restore(_ context.Context, req app.RestoreRepositoryCheckpointRequest) (app.RestoreResult, error) {
	return app.RestoreResult{}, &domain.Error{
		Code:      domain.ErrCodeUnavailable,
		Message:   fmt.Sprintf("repocheckpoint: Restore is not implemented (checkpoint %s); real restore is checkpoint-b08's dry-run scope and a later stretch goal per ADD §19.6", req.ID),
		Retryable: false,
	}
}
