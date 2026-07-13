// store.go: the Go domain-level CRUD layer over state_checkpoints
// (migrations/0023_state_checkpoints.sql). Mirrors
// internal/repocheckpoint/store.go's shape for its sibling Part B table —
// same "queryable columns duplicate a subset of manifest_json" split, same
// QuerierFromContext-based transactional-or-not access.
package statecheckpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// ErrNotFound mirrors internal/progress.ErrNotFound's shape for this
// package's own lookups, so callers get the same frozen domain.Error
// contract regardless of which store they're querying.
var ErrNotFound = &domain.Error{
	Code:      domain.ErrCodeNotFound,
	Message:   "statecheckpoint: no matching row",
	Retryable: false,
}

// Row is the Go-level representation of one state_checkpoints row.
// ManifestJSON is the full Appendix B document (Marshal's output); the
// other fields duplicate a queryable subset of it, exactly as
// repository_checkpoints does for its own manifest.
type Row struct {
	ID                     domain.StateCheckpointID
	TaskID                 domain.TaskID
	ProgressTreeVersion    int64
	ActiveNodeID           *domain.ProgressNodeID
	CompletionNodeID       *domain.ProgressNodeID
	RepositoryCheckpointID *domain.RepositoryCheckpointID
	ManifestJSON           string
	IntegritySHA256        string
	CreatedAt              string
}

// Store is the Go domain-level CRUD layer over state_checkpoints.
type Store struct {
	db *sqlite.DB
}

// NewStore constructs a Store bound to db.
func NewStore(db *sqlite.DB) *Store {
	return &Store{db: db}
}

// Insert creates a new state_checkpoints row. Called from inside
// CompleteNode's WithTx callback (checkpoint-a04) so the row commits
// atomically with the node status transition — this method has no
// transaction opinion of its own beyond honoring whatever ctx it is given,
// via QuerierFromContext, same as every other store in this codebase.
func (s *Store) Insert(ctx context.Context, r Row) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO state_checkpoints (
			id, task_id, progress_tree_version, active_node_id,
			completion_node_id, repository_checkpoint_id, manifest_json,
			integrity_sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.TaskID), r.ProgressTreeVersion,
		nullableNodeID(r.ActiveNodeID), nullableNodeID(r.CompletionNodeID),
		nullableRepoCheckpointID(r.RepositoryCheckpointID),
		r.ManifestJSON, r.IntegritySHA256, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("statecheckpoint: insert checkpoint %s: %w", r.ID, err)
	}
	return nil
}

// Get loads a single state checkpoint by ID.
func (s *Store) Get(ctx context.Context, id domain.StateCheckpointID) (Row, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, task_id, progress_tree_version, active_node_id,
		       completion_node_id, repository_checkpoint_id, manifest_json,
		       integrity_sha256, created_at
		FROM state_checkpoints WHERE id = ?`, string(id))
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, fmt.Errorf("statecheckpoint: get checkpoint %s: %w", id, err)
	}
	return r, nil
}

// LoadLatest returns the most recently created checkpoint for a task
// (ADD §18.9 reconciliation step 1, agents/checkpoint.md deliverable #8).
func (s *Store) LoadLatest(ctx context.Context, taskID domain.TaskID) (Row, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, task_id, progress_tree_version, active_node_id,
		       completion_node_id, repository_checkpoint_id, manifest_json,
		       integrity_sha256, created_at
		FROM state_checkpoints WHERE task_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, string(taskID))
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, fmt.Errorf("statecheckpoint: load latest checkpoint for task %s: %w", taskID, err)
	}
	return r, nil
}

// ListByTask returns every checkpoint for a task, oldest first — used by
// the 100-sequential-nodes test to assert exactly one verifiable
// checkpoint per completed node.
func (s *Store) ListByTask(ctx context.Context, taskID domain.TaskID) ([]Row, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	rows, err := q.QueryContext(ctx, `
		SELECT id, task_id, progress_tree_version, active_node_id,
		       completion_node_id, repository_checkpoint_id, manifest_json,
		       integrity_sha256, created_at
		FROM state_checkpoints WHERE task_id = ?
		ORDER BY created_at ASC, rowid ASC`, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("statecheckpoint: list checkpoints for task %s: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("statecheckpoint: scan checkpoint row for task %s: %w", taskID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("statecheckpoint: list checkpoints for task %s: %w", taskID, err)
	}
	return out, nil
}

func scanRow(row interface {
	Scan(dest ...any) error
}) (Row, error) {
	var (
		r                                               Row
		id, taskID, manifestJSON, integritySHA, created string
		activeNode, completionNode, repoCheckpoint      sql.NullString
		version                                         int64
	)
	if err := row.Scan(
		&id, &taskID, &version, &activeNode, &completionNode,
		&repoCheckpoint, &manifestJSON, &integritySHA, &created,
	); err != nil {
		return Row{}, err
	}
	r.ID = domain.StateCheckpointID(id)
	r.TaskID = domain.TaskID(taskID)
	r.ProgressTreeVersion = version
	if activeNode.Valid {
		n := domain.ProgressNodeID(activeNode.String)
		r.ActiveNodeID = &n
	}
	if completionNode.Valid {
		n := domain.ProgressNodeID(completionNode.String)
		r.CompletionNodeID = &n
	}
	if repoCheckpoint.Valid {
		c := domain.RepositoryCheckpointID(repoCheckpoint.String)
		r.RepositoryCheckpointID = &c
	}
	r.ManifestJSON = manifestJSON
	r.IntegritySHA256 = integritySHA
	r.CreatedAt = created
	return r, nil
}

func nullableNodeID(id *domain.ProgressNodeID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

func nullableRepoCheckpointID(id *domain.RepositoryCheckpointID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}
