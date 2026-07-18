// reader.go: Reader, the assembler that turns a session id into a Snapshot
// by fanning out across the existing read stores and two local read-only
// SELECTs. See snapshot.go's package comment for the honesty invariants.
package sessionstatus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// EvaluationReader is the narrow slice of evaluation.SQLDataSource this
// package reads: session→task resolution plus the three per-session reads
// that layer already exposes and the risk read this phase added.
type EvaluationReader interface {
	Resolve(ctx context.Context, sessionID domain.SessionID) (evaluation.ResolvedSession, error)
	RunwayForecast(ctx context.Context, sessionID domain.SessionID) (domain.RunwayForecast, bool, error)
	Quota(ctx context.Context, sessionID domain.SessionID) ([]domain.QuotaObservation, error)
	LatestRisk(ctx context.Context, sessionID domain.SessionID) (evaluation.RiskSnapshot, bool, error)
}

// NodeLister/EdgeLister are the read slices of progress's stores.
type NodeLister interface {
	ListByTask(ctx context.Context, taskID domain.TaskID) ([]progress.Node, error)
}
type EdgeLister interface {
	ListByTask(ctx context.Context, taskID domain.TaskID) ([]progress.Edge, error)
}

// StateCheckpointReader/RepoCheckpointReader are the read slices of the two
// checkpoint stores.
type StateCheckpointReader interface {
	LoadLatest(ctx context.Context, taskID domain.TaskID) (statecheckpoint.Row, error)
}
type RepoCheckpointReader interface {
	Get(ctx context.Context, id domain.RepositoryCheckpointID) (repocheckpoint.Row, error)
}

// JobLister is the read slice of scheduler.Store — the whole wake-job queue,
// filtered here to a pause.
type JobLister interface {
	List(ctx context.Context) ([]scheduler.Job, error)
}

// Deps bundles the read sources Reader assembles from. Every field is
// read-only; the composition root injects the same concrete stores the rest
// of the container already constructed.
type Deps struct {
	DB               *sqlite.DB
	Evaluation       EvaluationReader
	Nodes            NodeLister
	Edges            EdgeLister
	StateCheckpoints StateCheckpointReader
	RepoCheckpoints  RepoCheckpointReader
	Jobs             JobLister
}

// Reader assembles per-session FR-162 snapshots.
type Reader struct {
	deps Deps
}

// NewReader constructs a Reader. DB is required (the pause / latest-session
// SELECTs need it); the other fields degrade gracefully to empty/null
// sections when nil, so a partially-wired composition still serves an honest
// (if sparser) snapshot rather than panicking.
func NewReader(deps Deps) *Reader {
	if deps.DB == nil {
		panic("sessionstatus: NewReader requires a non-nil *sqlite.DB")
	}
	return &Reader{deps: deps}
}

// Snapshot assembles the FR-162 read-model for sessionID as of now. An empty
// sessionID resolves the most recent session. Returns a domain.Error with
// ErrCodeNotFound when the (resolved) session does not exist, or when an
// empty id was given and no session exists at all — the honest "nothing to
// report," which the HTTP surface maps to 404.
func (r *Reader) Snapshot(ctx context.Context, sessionID domain.SessionID, now time.Time) (*Snapshot, error) {
	sid := sessionID
	if sid == "" {
		latest, ok, err := r.latestSessionID(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &domain.Error{
				Code:      domain.ErrCodeNotFound,
				Message:   "sessionstatus: no sessions exist yet",
				Retryable: false,
			}
		}
		sid = latest
	}

	// Resolve doubles as the session-existence check: an unknown session id
	// surfaces ErrCodeNotFound here, which the caller maps to 404.
	resolved, err := r.deps.Evaluation.Resolve(ctx, sid)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{
		SessionID: string(sid),
		Quota:     Quota{AsOf: now, Windows: []QuotaWindow{}},
		Progress:  Progress{Nodes: []ProgressNode{}, Edges: []ProgressEdge{}},
	}

	// --- risk (session-joined prediction) ---
	if risk, ok, err := r.deps.Evaluation.LatestRisk(ctx, sid); err != nil {
		return nil, err
	} else if ok {
		snap.Risk = toRisk(risk)
	}

	// --- runway (direct session_id; may be null today) ---
	if rf, ok, err := r.deps.Evaluation.RunwayForecast(ctx, sid); err != nil {
		return nil, err
	} else if ok {
		snap.Runway = toRunway(rf)
	}

	// --- quota freshness (latest observation per limit) ---
	quotas, err := r.deps.Evaluation.Quota(ctx, sid)
	if err != nil {
		return nil, err
	}
	for _, qo := range quotas {
		snap.Quota.Windows = append(snap.Quota.Windows, toQuotaWindow(qo, now))
	}

	// --- progress tree + state/repo checkpoints (task-scoped) ---
	if resolved.TaskID != nil {
		tid := *resolved.TaskID
		taskStr := string(tid)
		snap.Progress.TaskID = &taskStr

		if r.deps.Nodes != nil {
			nodes, err := r.deps.Nodes.ListByTask(ctx, tid)
			if err != nil {
				return nil, err
			}
			for _, n := range nodes {
				snap.Progress.Nodes = append(snap.Progress.Nodes, toProgressNode(n))
			}
		}
		if r.deps.Edges != nil {
			edges, err := r.deps.Edges.ListByTask(ctx, tid)
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				snap.Progress.Edges = append(snap.Progress.Edges, toProgressEdge(e))
			}
		}

		ck, err := r.checkpointFor(ctx, tid)
		if err != nil {
			return nil, err
		}
		snap.Checkpoint = ck
	}

	// --- pause + its wake jobs (session-scoped) ---
	pause, ok, err := r.latestPause(ctx, sid)
	if err != nil {
		return nil, err
	}
	if ok {
		if r.deps.Jobs != nil {
			jobs, err := r.deps.Jobs.List(ctx)
			if err != nil {
				return nil, err
			}
			pause.WakeJobs = wakeJobsForPause(jobs, pause.ID)
		}
		snap.Pause = pause
	}

	return snap, nil
}

// checkpointFor returns the latest state checkpoint for a task plus its
// linked repository checkpoint (when it references one). Nil (not an error)
// when the task has no state checkpoint — the common state today.
func (r *Reader) checkpointFor(ctx context.Context, taskID domain.TaskID) (*Checkpoint, error) {
	if r.deps.StateCheckpoints == nil {
		return nil, nil
	}
	state, err := r.deps.StateCheckpoints.LoadLatest(ctx, taskID)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	ck := &Checkpoint{State: toStateCheckpoint(state)}
	if state.RepositoryCheckpointID != nil && r.deps.RepoCheckpoints != nil {
		repo, err := r.deps.RepoCheckpoints.Get(ctx, *state.RepositoryCheckpointID)
		if err != nil {
			// A dangling ref (repo checkpoint pruned) leaves Repository null,
			// not an error — the state checkpoint still stands on its own.
			if !isNotFound(err) {
				return nil, err
			}
		} else {
			ck.Repository = toRepoCheckpoint(repo)
		}
	}
	return ck, nil
}

// latestSessionID returns the most recently started session, ok=false when
// none exist. provider_sessions (foundation's table, migration 0001) has no
// owning Go store exposing this shape, so this reads it directly — the same
// documented pattern evaluation.SQLDataSource uses for cross-role tables.
func (r *Reader) latestSessionID(ctx context.Context) (domain.SessionID, bool, error) {
	q := sqlite.QuerierFromContext(ctx, r.deps.DB)
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT id FROM provider_sessions
		ORDER BY started_at DESC, rowid DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sessionstatus: latest session: %w", err)
	}
	return domain.SessionID(id), true, nil
}

// latestPause returns the session's most recent pause record REGARDLESS of
// lifecycle state (resumed/cancelled records are shown too), ok=false when
// the session has none. internal/pause's store exposes only FindActiveByKey
// (hides terminal records, needs the full task+session key) and GetByID (by
// pause id) — neither gives "latest by session id, any status" — and that
// package is not this role's to extend, so this reads pause_records directly
// by its indexed session_id column, ordered by requested_at (its creation
// time). Numbers/ids/statuses/timestamps only; no metadata_json.
func (r *Reader) latestPause(ctx context.Context, sessionID domain.SessionID) (*Pause, bool, error) {
	q := sqlite.QuerierFromContext(ctx, r.deps.DB)
	var (
		id, taskID, status, requestedAt, runwayForecastID string
		autoResume                                        int64
		turnID, stateCkptID, repoCkptID                   sql.NullString
		safePointAt, pausedAt, expectedResetAt            sql.NullString
		cancelledAt, failureCode                          sql.NullString
	)
	err := q.QueryRowContext(ctx, `
		SELECT id, task_id, turn_id, runway_forecast_id, state_checkpoint_id,
		       repository_checkpoint_id, status, requested_at, safe_point_at,
		       paused_at, expected_reset_at, auto_resume_enabled, cancelled_at,
		       failure_code
		FROM pause_records WHERE session_id = ?
		ORDER BY requested_at DESC, rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(
		&id, &taskID, &turnID, &runwayForecastID, &stateCkptID,
		&repoCkptID, &status, &requestedAt, &safePointAt,
		&pausedAt, &expectedResetAt, &autoResume, &cancelledAt,
		&failureCode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("sessionstatus: latest pause for session %s: %w", sessionID, err)
	}
	return &Pause{
		ID:                     id,
		TaskID:                 taskID,
		TurnID:                 nullStr(turnID),
		Status:                 status,
		AutoResumeEnabled:      autoResume != 0,
		RunwayForecastID:       runwayForecastID,
		StateCheckpointID:      nullStr(stateCkptID),
		RepositoryCheckpointID: nullStr(repoCkptID),
		RequestedAt:            requestedAt,
		SafePointAt:            nullStr(safePointAt),
		PausedAt:               nullStr(pausedAt),
		ExpectedResetAt:        nullStr(expectedResetAt),
		CancelledAt:            nullStr(cancelledAt),
		FailureCode:            nullStr(failureCode),
		WakeJobs:               []WakeJob{},
	}, true, nil
}

// --- projections -----------------------------------------------------------

func toRisk(r evaluation.RiskSnapshot) *Risk {
	reasons := make([]string, len(r.ReasonCodes))
	for i, rc := range r.ReasonCodes {
		reasons[i] = string(rc)
	}
	return &Risk{
		OverallRiskScore:     r.OverallRiskScore,
		QuotaRiskScore:       r.QuotaRiskScore,
		ContextRiskScore:     r.ContextRiskScore,
		CompletionRiskScore:  r.CompletionRiskScore,
		BlastRadiusRiskScore: r.BlastRadiusRiskScore,
		Calibrated:           r.Calibrated,
		Confidence:           string(r.Confidence),
		ReasonCodes:          reasons,
		TurnID:               string(r.TurnID),
		EvaluatedAt:          r.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toRunway(f domain.RunwayForecast) *Runway {
	reasons := make([]string, len(f.ReasonCodes))
	copy(reasons, f.ReasonCodes)
	return &Runway{
		LimitID:                        f.LimitID,
		HorizonSeconds:                 f.HorizonSeconds,
		RiskScore:                      f.RiskScore,
		Calibrated:                     f.Calibrated,
		Confidence:                     string(f.Confidence),
		CurrentUsedPercent:             f.CurrentUsedPercent,
		HitProbability:                 f.HitProbability,
		BurnRateP50:                    f.BurnRateP50,
		BurnRateP90:                    f.BurnRateP90,
		EstimatedTimeToLimitP50Seconds: f.EstimatedTimeToLimitP50Seconds,
		EstimatedTimeToLimitP90Seconds: f.EstimatedTimeToLimitP90Seconds,
		QuotaObservedAt:                f.QuotaObservedAt,
		ReasonCodes:                    reasons,
	}
}

func toQuotaWindow(o domain.QuotaObservation, now time.Time) QuotaWindow {
	age := int64(now.Sub(o.ObservedAt).Seconds())
	if age < 0 {
		age = 0 // clock skew: an observation cannot be from the future
	}
	return QuotaWindow{
		LimitID:     o.LimitID,
		UsedPercent: o.UsedPercent,
		ResetsAt:    o.ResetsAt,
		ObservedAt:  o.ObservedAt,
		AgeSeconds:  age,
	}
}

func toProgressNode(n progress.Node) ProgressNode {
	return ProgressNode{
		ID:          string(n.ID),
		ParentID:    nodeIDPtr(n.ParentID),
		Ordinal:     n.Ordinal,
		Kind:        string(n.Kind),
		Status:      string(n.Status),
		Version:     n.Version,
		StartedAt:   n.StartedAt,
		CompletedAt: n.CompletedAt,
		UpdatedAt:   n.UpdatedAt,
	}
}

func toProgressEdge(e progress.Edge) ProgressEdge {
	return ProgressEdge{
		FromNodeID: string(e.FromNodeID),
		ToNodeID:   string(e.ToNodeID),
		Kind:       string(e.Kind),
	}
}

func toStateCheckpoint(row statecheckpoint.Row) *StateCheckpoint {
	return &StateCheckpoint{
		ID:                     string(row.ID),
		TaskID:                 string(row.TaskID),
		ProgressTreeVersion:    row.ProgressTreeVersion,
		ActiveNodeID:           nodeIDPtr(row.ActiveNodeID),
		CompletionNodeID:       nodeIDPtr(row.CompletionNodeID),
		RepositoryCheckpointID: repoCheckpointIDPtr(row.RepositoryCheckpointID),
		IntegritySHA256:        row.IntegritySHA256,
		CreatedAt:              row.CreatedAt,
	}
}

func toRepoCheckpoint(row repocheckpoint.Row) *RepoCheckpoint {
	return &RepoCheckpoint{
		ID:               string(row.ID),
		Status:           string(row.Status),
		Recoverability:   string(row.Recoverability),
		GitHead:          row.GitHead,
		IndexDiffHash:    row.IndexDiffHash,
		WorktreeDiffHash: row.WorktreeDiffHash,
		TotalBytes:       row.TotalBytes,
		CreatedAt:        row.CreatedAt,
		VerifiedAt:       row.VerifiedAt,
	}
}

// wakeJobsForPause projects the queue down to the jobs belonging to pauseID,
// mirroring the /v1/scheduler/jobs wire shape. Returns an empty (non-nil)
// slice so the JSON is [] not null.
func wakeJobsForPause(jobs []scheduler.Job, pauseID string) []WakeJob {
	out := []WakeJob{}
	for _, j := range jobs {
		if string(j.PauseID) != pauseID {
			continue
		}
		out = append(out, WakeJob{
			ID:          string(j.ID),
			PauseID:     string(j.PauseID),
			Kind:        j.Kind,
			Status:      j.Status,
			RunAfter:    j.RunAfter,
			Attempts:    j.Attempts,
			MaxAttempts: j.MaxAttempts,
			LeaseOwner:  j.LeaseOwner,
			LeaseExpiry: j.LeaseExpires,
			LastError:   j.LastError,
		})
	}
	return out
}

// --- small helpers ---------------------------------------------------------

func isNotFound(err error) bool {
	var derr *domain.Error
	return errors.As(err, &derr) && derr.Code == domain.ErrCodeNotFound
}

func nullStr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func nodeIDPtr(id *domain.ProgressNodeID) *string {
	if id == nil {
		return nil
	}
	v := string(*id)
	return &v
}

func repoCheckpointIDPtr(id *domain.RepositoryCheckpointID) *string {
	if id == nil {
		return nil
	}
	v := string(*id)
	return &v
}
