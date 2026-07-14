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

// Service implements app.RepositoryCheckpointService: Create + Verify
// (checkpoint-b04), Restore dry-run (checkpoint-b08), and — since issue
// #6/ADR-048 ended the vertical-slice deferral — the real, mutating
// restore behind RestoreRepositoryCheckpointRequest.Apply.
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

// Restore evaluates whether restoring req.ID's checkpoint onto its
// worktree's CURRENT state would succeed (restoredryrun.go's full ADD
// §19.6 check sequence) and — when req.Apply is set and every check
// passed — performs the real restore (issue #6, ADR-048). The default
// req.Apply=false preserves checkpoint-b08's dry-run-only semantics
// exactly: nothing is ever mutated, Applied stays false.
//
// The gate mapping is unchanged from the dry-run era: no problems →
// success (Applied reports whether an apply then ran); one or more
// problems (ADD §19.6's own checksum/identity/dirty-target/apply-check
// failures) → a non-nil ErrCodeConflict error with every problem joined
// into Message and individually available in Details (frozen
// domain.Error shape), and NOTHING is applied regardless of req.Apply —
// the dry-run verdict is the apply step's hard precondition, not a
// parallel code path.
//
// AllowDirty, when true, downgrades a dirty-target finding from a
// problem to an informational note (ADD §19.6: "reject dirty target
// UNLESS safety checkpoint/force" — AllowDirty is this request's
// `force`). An APPLYING restore onto a dirty target additionally
// captures a safety checkpoint of the pre-restore state first — see the
// inline comment at the apply step for the full rationale.
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

	if !req.Apply {
		return app.RestoreResult{ID: req.ID, Applied: false}, nil
	}

	// --- Real restore (issue #6, ADR-048) --------------------------------
	// Every §19.6 gate above passed moments ago. One more safety layer
	// before mutating: a DIRTY target (reachable only via AllowDirty) is
	// about to have checkpoint state layered over someone's uncommitted
	// work — capture it first, unconditionally, so the pre-restore state
	// is recoverable by the same mechanism being exercised right now (ADD
	// §19.6's "safety checkpoint" arm of the dirty-target rule). A clean
	// target needs no safety net: its state is HEAD, which restore never
	// moves (gitx.Apply's own no-ref-mutation guarantee). A safety-capture
	// failure aborts the restore with nothing mutated — fail closed, never
	// "proceed uninsured."
	var safetyID *domain.RepositoryCheckpointID
	if report.WorktreeDirty {
		safety, err := s.Create(ctx, app.CreateRepositoryCheckpointRequest{
			WorktreeID: row.WorktreeID,
			TaskID:     row.TaskID,
		})
		if err != nil {
			return app.RestoreResult{ID: req.ID, Applied: false}, fmt.Errorf("repocheckpoint: Restore: safety checkpoint of dirty target failed, nothing was restored: %w", err)
		}
		safetyID = &safety.ID
	}

	applyResult, err := RestoreApply(ctx, s.git, row, loc.Path)
	if err != nil {
		// RestoreApply's error already states exactly how far it got
		// (nothing / staged only / patches-but-not-untracked); attach the
		// safety checkpoint handle when one exists so the operator's
		// recovery path is in the same message.
		if safetyID != nil {
			return app.RestoreResult{ID: req.ID, Applied: false, SafetyCheckpointID: safetyID}, fmt.Errorf("%w (pre-restore state is recoverable from safety checkpoint %s)", err, *safetyID)
		}
		return app.RestoreResult{ID: req.ID, Applied: false}, err
	}

	skipped := make([]string, 0, len(applyResult.UntrackedSkipped))
	for _, sf := range applyResult.UntrackedSkipped {
		skipped = append(skipped, fmt.Sprintf("%s: %s", sf.Path, sf.Reason))
	}
	return app.RestoreResult{
		ID:                 req.ID,
		Applied:            true,
		SafetyCheckpointID: safetyID,
		UntrackedSkipped:   skipped,
	}, nil
}
