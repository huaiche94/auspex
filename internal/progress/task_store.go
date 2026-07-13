// task_store.go: the Go domain-level store for `tasks`
// (migrations/0004_tasks.sql, foundation's range). Grepped before writing
// this: no TaskStore/task-row CRUD exists anywhere else in the repository
// (foundation's own migration comment explicitly defers task-row ownership
// to "checkpoint's Progress Tree service" — 0004_tasks.sql's header: "the
// column is a plain TEXT pointer; checkpoint's Progress Tree service is
// responsible for keeping it consistent with progress_nodes.id once that
// table exists"). Task creation is explicitly app.ProgressTreeService's
// first frozen method (CreateTask), so owning this minimal CRUD wrapper is
// squarely this role's responsibility, not a duplication of another role's
// work.
//
// This is deliberately narrow: just enough CRUD for Service (service.go) to
// satisfy CreateTask and to look up a task's identity where needed. It does
// not attempt to own task lifecycle policy (status transitions beyond the
// initial insert, auto_resume_enabled semantics, etc.) — those belong to
// whichever later caller/role actually drives task-level state machine
// decisions; this store only persists and reads rows.
package progress

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// TaskStatus is this package's vocabulary for tasks.status (deliberately
// not CHECK-constrained in 0004, same immutable-DDL reasoning as
// progress_nodes.status). TaskOpen is the initial status a freshly created
// task starts in.
type TaskStatus string

const (
	TaskOpen      TaskStatus = "open"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
)

// TaskRow is the Go-level representation of one tasks row.
type TaskRow struct {
	ID                  domain.TaskID
	SessionID           *domain.SessionID
	WorktreeID          domain.WorktreeID
	ObjectiveHash       string
	ObjectiveText       *string
	Status              TaskStatus
	ProgressTreeVersion int64
	ActiveNodeID        *domain.ProgressNodeID
	AutoResumeEnabled   bool
	CreatedAt           string
	UpdatedAt           string
	CompletedAt         *string
}

// TaskStore is the Go domain-level CRUD layer over tasks.
type TaskStore struct {
	db    *sqlite.DB
	clock domain.Clock
}

// NewTaskStore constructs a TaskStore bound to db, using clock for every
// created_at/updated_at it writes.
func NewTaskStore(db *sqlite.DB, clock domain.Clock) *TaskStore {
	return &TaskStore{db: db, clock: clock}
}

func (s *TaskStore) now() string {
	return s.clock.Now().UTC().Format(time.RFC3339)
}

// Insert creates a new tasks row with status=open, progress_tree_version=1,
// and no active node yet (the Progress Tree's plan has not been upserted at
// task-creation time — that is UpsertPlan's job, a separate frozen method).
func (s *TaskStore) Insert(ctx context.Context, t TaskRow) error {
	if t.Status == "" {
		t.Status = TaskOpen
	}
	if t.ProgressTreeVersion == 0 {
		t.ProgressTreeVersion = 1
	}
	now := s.now()
	if t.CreatedAt == "" {
		t.CreatedAt = now
	}
	if t.UpdatedAt == "" {
		t.UpdatedAt = now
	}

	q := sqlite.QuerierFromContext(ctx, s.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO tasks (
			id, session_id, worktree_id, objective_hash, objective_text,
			status, progress_tree_version, active_node_id,
			auto_resume_enabled, created_at, updated_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(t.ID), nullableSessionID(t.SessionID), string(t.WorktreeID),
		t.ObjectiveHash, nullableString(t.ObjectiveText), string(t.Status),
		t.ProgressTreeVersion, nullableNodeID(t.ActiveNodeID),
		boolToInt(t.AutoResumeEnabled), t.CreatedAt, t.UpdatedAt,
		nullableString(t.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("progress: insert task %s: %w", t.ID, err)
	}
	return nil
}

// Get loads a single task by ID. Returns ErrNotFound (frozen domain.Error
// shape) if no row matches.
func (s *TaskStore) Get(ctx context.Context, id domain.TaskID) (TaskRow, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, session_id, worktree_id, objective_hash, objective_text,
		       status, progress_tree_version, active_node_id,
		       auto_resume_enabled, created_at, updated_at, completed_at
		FROM tasks WHERE id = ?`, string(id))
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRow{}, ErrNotFound
	}
	if err != nil {
		return TaskRow{}, fmt.Errorf("progress: get task %s: %w", id, err)
	}
	return t, nil
}

// SetActiveNodeAndVersion updates a task's active_node_id and
// progress_tree_version together — the pair UpsertPlan needs to keep in
// sync per 0004_tasks.sql's header comment ("checkpoint's Progress Tree
// service is responsible for keeping it consistent with progress_nodes.id
// once that table exists").
func (s *TaskStore) SetActiveNodeAndVersion(ctx context.Context, id domain.TaskID, activeNodeID *domain.ProgressNodeID, version int64) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	res, err := q.ExecContext(ctx, `
		UPDATE tasks
		SET active_node_id = ?, progress_tree_version = ?, updated_at = ?
		WHERE id = ?`,
		nullableNodeID(activeNodeID), version, s.now(), string(id),
	)
	if err != nil {
		return fmt.Errorf("progress: set active node/version for task %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("progress: set active node/version for task %s: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanTask(row interface {
	Scan(dest ...any) error
}) (TaskRow, error) {
	var (
		t                                      TaskRow
		id, worktreeID, objectiveHash, status  string
		createdAt, updatedAt                   string
		sessionID, objectiveText, activeNode   sql.NullString
		completedAt                            sql.NullString
		progressTreeVersion, autoResumeEnabled int64
	)
	if err := row.Scan(
		&id, &sessionID, &worktreeID, &objectiveHash, &objectiveText,
		&status, &progressTreeVersion, &activeNode,
		&autoResumeEnabled, &createdAt, &updatedAt, &completedAt,
	); err != nil {
		return TaskRow{}, err
	}
	t.ID = domain.TaskID(id)
	if sessionID.Valid {
		sid := domain.SessionID(sessionID.String)
		t.SessionID = &sid
	}
	t.WorktreeID = domain.WorktreeID(worktreeID)
	t.ObjectiveHash = objectiveHash
	if objectiveText.Valid {
		ot := objectiveText.String
		t.ObjectiveText = &ot
	}
	t.Status = TaskStatus(status)
	t.ProgressTreeVersion = progressTreeVersion
	if activeNode.Valid {
		n := domain.ProgressNodeID(activeNode.String)
		t.ActiveNodeID = &n
	}
	t.AutoResumeEnabled = autoResumeEnabled != 0
	t.CreatedAt = createdAt
	t.UpdatedAt = updatedAt
	if completedAt.Valid {
		c := completedAt.String
		t.CompletedAt = &c
	}
	return t, nil
}

func nullableSessionID(id *domain.SessionID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
