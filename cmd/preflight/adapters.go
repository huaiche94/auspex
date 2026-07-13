// adapters.go holds the small interface-bridging glue that composes
// already-real, already-tested pieces from multiple roles into the
// package-local seams several of them declare (e.g. internal/pause's
// SessionContextResolver, internal/pause/resumevalidation's
// QuotaSnapshotReader/RepoFingerprintReader/SessionCapabilityReader,
// internal/predictor/token's FeatureSource). None of these seams are
// frozen internal/app ports — each owning package's own doc comment
// explains why it declared the interface locally instead: it is an
// internal implementation detail behind an already-frozen boundary, and
// the real implementation is explicitly documented as "a future wiring
// node's job, run by whichever role has the relevant tables in its
// exclusive paths" (internal/pause/service.go's SessionContextResolver
// doc comment, verbatim). That role is cmd/preflight: this file contains
// no new business logic, only DTO-shape translation and direct
// read-only SQL against tables owned by foundation/claude-provider/
// checkpoint, exactly mirroring the technique internal/evaluation.
// SQLDataSource already established for the analogous evaluation.
// DataSource seam.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/evaluation"
	"github.com/huaiche94/preflight/internal/features"
	"github.com/huaiche94/preflight/internal/gitx"
	"github.com/huaiche94/preflight/internal/pause"
	"github.com/huaiche94/preflight/internal/predictor/token"
	"github.com/huaiche94/preflight/internal/progress"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// treeReaderAdapter satisfies internal/statecheckpoint.TreeReader by
// composing checkpoint's own already-real, already-tested NodeStore and
// ArtifactStore — pure delegation, no new logic. TreeReader's two methods
// return the same information those stores already provide; this adapter
// only reshapes the field names/types (NodeSnapshot/ArtifactSnapshot,
// statecheckpoint's own narrow read-only view types) to match.
type treeReaderAdapter struct {
	nodes     *progress.NodeStore
	artifacts *progress.ArtifactStore
}

func (a treeReaderAdapter) ListNodes(ctx context.Context, taskID domain.TaskID) ([]statecheckpoint.NodeSnapshot, error) {
	nodes, err := a.nodes.ListByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]statecheckpoint.NodeSnapshot, len(nodes))
	for i, n := range nodes {
		out[i] = statecheckpoint.NodeSnapshot{ID: n.ID, Status: n.Status}
	}
	return out, nil
}

func (a treeReaderAdapter) ListArtifacts(ctx context.Context, taskID domain.TaskID) ([]statecheckpoint.ArtifactSnapshot, error) {
	nodes, err := a.nodes.ListByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var out []statecheckpoint.ArtifactSnapshot
	for _, n := range nodes {
		rows, err := a.artifacts.ListByNode(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			mediaType := ""
			if r.MediaType != nil {
				mediaType = *r.MediaType
			}
			out = append(out, statecheckpoint.ArtifactSnapshot{
				ID:               r.ID,
				URI:              r.URI,
				MediaType:        mediaType,
				Bytes:            r.Bytes,
				SHA256:           r.SHA256,
				ValidationStatus: string(r.ValidationStatus),
			})
		}
	}
	return out, nil
}

// tokenFeatureSourceAdapter satisfies internal/predictor/token.FeatureSource
// by wrapping the real evaluation.SQLDataSource. token.FeatureSource's
// Classification/Progress methods take only a SessionID (no *domain.TaskID
// parameter) — a narrower shape than evaluation.DataSource's — so this
// adapter resolves the SessionID's TaskID once via SQLDataSource.Resolve
// (the same real, already-tested method every other pipeline stage uses)
// and forwards it to SQLDataSource's own corresponding method. No new
// query logic: every field this returns comes from SQLDataSource's own
// already-verified queries.
type tokenFeatureSourceAdapter struct {
	source *evaluation.SQLDataSource
}

var _ token.FeatureSource = tokenFeatureSourceAdapter{}

func (a tokenFeatureSourceAdapter) Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	resolved, err := a.source.Resolve(ctx, sessionID)
	if err != nil {
		return features.Classification{}, features.PromptFeatures{}, err
	}
	return a.source.Classification(ctx, sessionID, resolved.TaskID)
}

func (a tokenFeatureSourceAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.source.Session(ctx, sessionID)
}

func (a tokenFeatureSourceAdapter) Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error) {
	resolved, err := a.source.Resolve(ctx, sessionID)
	if err != nil {
		return features.ProgressFeatures{}, false, err
	}
	return a.source.Progress(ctx, resolved.TaskID)
}

func (a tokenFeatureSourceAdapter) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) ([]float64, error) {
	return a.source.RecentSimilarTurnTokens(ctx, sessionID, class)
}

// sessionContextResolverAdapter satisfies internal/pause.
// SessionContextResolver via a direct, read-only query against
// foundation's provider_sessions table (the same worktree_id column
// evaluation.SQLDataSource.Resolve already reads, for consistency) plus
// evaluation.SQLDataSource.Resolve for the TaskID half. PausedWorkPaths is
// left empty (nil): resumevalidation.go's own pathsOverlap treats an empty
// slice as "never overlaps" — a documented, safe default, not a silent
// failure, per that seam's own doc comment. A future wave that wants
// path-scoped overlap detection would populate this from the Progress
// Tree node's own recorded artifact paths.
type sessionContextResolverAdapter struct {
	db     *sqlite.DB
	source *evaluation.SQLDataSource
}

func (a sessionContextResolverAdapter) ResolveSessionContext(ctx context.Context, sessionID domain.SessionID) (pause.SessionContext, error) {
	resolved, err := a.source.Resolve(ctx, sessionID)
	if err != nil {
		return pause.SessionContext{}, err
	}
	if resolved.TaskID == nil {
		return pause.SessionContext{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   "cmd/preflight: session has no associated task to pause",
			Retryable: false,
			Details:   map[string]string{"session_id": string(sessionID)},
		}
	}

	q := sqlite.QuerierFromContext(ctx, a.db)
	var worktreeID string
	err = q.QueryRowContext(ctx, `SELECT worktree_id FROM provider_sessions WHERE id = ?`, string(sessionID)).Scan(&worktreeID)
	if errors.Is(err, sql.ErrNoRows) {
		return pause.SessionContext{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   "cmd/preflight: no provider_sessions row for session",
			Retryable: false,
			Details:   map[string]string{"session_id": string(sessionID)},
		}
	}
	if err != nil {
		return pause.SessionContext{}, fmt.Errorf("cmd/preflight: resolve session context %s: %w", sessionID, err)
	}

	return pause.SessionContext{
		TaskID:     *resolved.TaskID,
		WorktreeID: domain.WorktreeID(worktreeID),
	}, nil
}

// resolveWorktreeLocation satisfies both internal/repocheckpoint.Service's
// resolveWorktree callback and this file's own repoFingerprintReaderAdapter
// (both need the same worktree-id-to-filesystem-path lookup) via a direct,
// read-only query against foundation's worktrees table.
func resolveWorktreeLocation(ctx context.Context, db *sqlite.DB, worktreeID domain.WorktreeID) (root, repositoryID string, err error) {
	q := sqlite.QuerierFromContext(ctx, db)
	err = q.QueryRowContext(ctx, `SELECT root_path, repository_id FROM worktrees WHERE id = ?`, string(worktreeID)).Scan(&root, &repositoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   "cmd/preflight: no worktrees row for worktree",
			Retryable: false,
			Details:   map[string]string{"worktree_id": string(worktreeID)},
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("cmd/preflight: resolve worktree %s: %w", worktreeID, err)
	}
	return root, repositoryID, nil
}

// quotaSnapshotReaderAdapter satisfies internal/pause/resumevalidation.
// QuotaSnapshotReader by delegating to evaluation.SQLDataSource.Quota's
// already-real query against claude-provider's events table, picking the
// observation matching the requested limitID (resumevalidation.go's own
// CheckQuotaSafety only compares against the same-limit baseline, so this
// adapter's job is purely to find that one matching observation among
// SQLDataSource.Quota's result, not to add any new query logic).
type quotaSnapshotReaderAdapter struct {
	source *evaluation.SQLDataSource
}

func (a quotaSnapshotReaderAdapter) ReadCurrentQuota(ctx context.Context, sessionID domain.SessionID, limitID string) (domain.QuotaObservation, error) {
	observations, err := a.source.Quota(ctx, sessionID)
	if err != nil {
		return domain.QuotaObservation{}, err
	}
	for _, obs := range observations {
		if obs.LimitID == limitID {
			return obs, nil
		}
	}
	return domain.QuotaObservation{}, &domain.Error{
		Code:      domain.ErrCodeNotFound,
		Message:   "cmd/preflight: no current quota observation for limit",
		Retryable: false,
		Details:   map[string]string{"session_id": string(sessionID), "limit_id": limitID},
	}
}

// repoFingerprintReaderAdapter satisfies internal/pause/resumevalidation.
// RepoFingerprintReader by composing the real gitx.Client.Fingerprint
// (checkpoint's own already-real, already-tested repository-fingerprint
// logic) with a worktree-id-to-path lookup. ChangedPaths is populated from
// gitx.Fingerprint's own numstat-derived changed-path list.
type repoFingerprintReaderAdapter struct {
	db  *sqlite.DB
	git *gitx.Client
}

func (a repoFingerprintReaderAdapter) ReadCurrentFingerprint(ctx context.Context, worktreeID domain.WorktreeID) (pause.RepoFingerprint, error) {
	root, _, err := resolveWorktreeLocation(ctx, a.db, worktreeID)
	if err != nil {
		return pause.RepoFingerprint{}, err
	}
	fp, err := a.git.Fingerprint(ctx, root)
	if err != nil {
		return pause.RepoFingerprint{}, fmt.Errorf("cmd/preflight: fingerprint worktree %s: %w", worktreeID, err)
	}
	changed := make([]string, 0, len(fp.Entries))
	for _, e := range fp.Entries {
		changed = append(changed, e.Path)
	}
	return pause.RepoFingerprint{HeadOID: fp.HeadOID, ChangedPaths: changed}, nil
}

// sessionCapabilityReaderStub satisfies internal/pause/resumevalidation.
// SessionCapabilityReader. Managed Claude session resume (actually driving
// a live provider process back into a running state) is explicitly a
// stretch goal never built in this vertical slice — claude-provider's own
// role doc says so verbatim ("Stretch: Managed stream-json runner, signal
// interruption, and session resume adapter... Do not compromise the P0
// hook path to complete these"), and runtime's own agents.md repeats it
// for the resume side ("Actual managed Claude resume is stretch and must
// not weaken state-machine tests"). This is not a gap this file should
// paper over with a fabricated "yes, resumable" answer: it honestly
// reports every session as NOT resumable, so ValidateResume's session
// check fails closed (a real capability gate the product needs, reported
// conservatively) rather than silently claiming a capability that does
// not exist in this codebase yet. A future wave that builds the real
// managed-resume adapter replaces this stub with a real
// SessionCapabilityReader; nothing else in this composition needs to
// change (app.GracefulPauseService's own signature is unaffected).
type sessionCapabilityReaderStub struct{}

func (sessionCapabilityReaderStub) ReadSessionCapability(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
	return pause.SessionCapabilitySnapshot{Resumable: false}, nil
}

// stubTurnInterrupter satisfies app.TurnInterrupter. Like
// sessionCapabilityReaderStub above, this stands in for a real managed
// provider-interrupt mechanism that is explicitly out of this vertical
// slice's scope (the same claude-provider/runtime stretch-goal notes
// apply). It fails closed (returns an error) rather than pretending to
// have interrupted a real running provider process, so
// GracefulPauseService.ReachSafePoint's own machinery correctly reports
// "interrupt failed, leave the pause record recoverable" (runtime-a11's
// own required test for exactly this scenario) instead of silently
// succeeding at nothing.
type stubTurnInterrupter struct{}

func (stubTurnInterrupter) Interrupt(_ context.Context, locator app.RunLocator) error {
	return &domain.Error{
		Code:      domain.ErrCodeUnavailable,
		Message:   "cmd/preflight: managed provider interrupt is not implemented in this vertical slice (stretch goal, see claude-provider/runtime role docs)",
		Retryable: false,
		Details:   map[string]string{"session_id": string(locator.SessionID), "turn_id": string(locator.TurnID)},
	}
}
