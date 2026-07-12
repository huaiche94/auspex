// store.go: the Go domain-level store for repository_checkpoints
// (migrations/0030_repository_checkpoints.sql), following the same
// QuerierFromContext pattern established by internal/telemetry/claude's
// EventStore and internal/progress's stores in this same wave.
package repocheckpoint

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// Row is the Go-level representation of one repository_checkpoints row.
type Row struct {
	ID               domain.RepositoryCheckpointID
	WorktreeID       domain.WorktreeID
	TaskID           *domain.TaskID
	TurnID           *string
	Status           Status
	ArtifactRoot     string
	ManifestPath     string
	GitHead          string
	IndexDiffHash    string
	WorktreeDiffHash string
	Recoverability   Recoverability
	TotalBytes       *int64
	CreatedAt        string
	VerifiedAt       *string
	MetadataJSON     string
}

// ErrNotFound is returned when no repository_checkpoints row matches a
// lookup, using the frozen domain.Error shape (mirrors
// internal/progress.ErrNotFound's convention within this same wave).
var ErrNotFound = &domain.Error{
	Code:      domain.ErrCodeNotFound,
	Message:   "repocheckpoint: no matching row",
	Retryable: false,
}

// Store is the CRUD layer over repository_checkpoints.
type Store struct {
	db *sqlite.DB
}

// NewStore constructs a Store bound to db.
func NewStore(db *sqlite.DB) *Store {
	return &Store{db: db}
}

// Insert creates a new repository_checkpoints row.
func (s *Store) Insert(ctx context.Context, r Row) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO repository_checkpoints (
			id, worktree_id, task_id, turn_id, status, artifact_root,
			manifest_path, git_head, index_diff_hash, worktree_diff_hash,
			recoverability, total_bytes, created_at, verified_at, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.WorktreeID), nullableTaskID(r.TaskID), nullableStringPtr(r.TurnID),
		string(r.Status), r.ArtifactRoot, r.ManifestPath, r.GitHead,
		r.IndexDiffHash, r.WorktreeDiffHash, string(r.Recoverability),
		nullableInt64(r.TotalBytes), r.CreatedAt, nullableStringPtr(r.VerifiedAt),
		orDefaultJSON(r.MetadataJSON),
	)
	if err != nil {
		return fmt.Errorf("repocheckpoint: insert row %s: %w", r.ID, err)
	}
	return nil
}

// Get loads a single repository_checkpoints row by ID.
func (s *Store) Get(ctx context.Context, id domain.RepositoryCheckpointID) (Row, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, worktree_id, task_id, turn_id, status, artifact_root,
		       manifest_path, git_head, index_diff_hash, worktree_diff_hash,
		       recoverability, total_bytes, created_at, verified_at, metadata_json
		FROM repository_checkpoints WHERE id = ?`, string(id))
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, fmt.Errorf("repocheckpoint: get row %s: %w", id, err)
	}
	return r, nil
}

// SetVerified marks a row as verified at the given timestamp.
func (s *Store) SetVerified(ctx context.Context, id domain.RepositoryCheckpointID, verifiedAt string) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	res, err := q.ExecContext(ctx, `UPDATE repository_checkpoints SET verified_at = ? WHERE id = ?`, verifiedAt, string(id))
	if err != nil {
		return fmt.Errorf("repocheckpoint: set verified for %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repocheckpoint: set verified for %s: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRow(row interface {
	Scan(dest ...any) error
}) (Row, error) {
	var (
		r                                                        Row
		id, worktreeID, status, artifactRoot, manifestPath       string
		gitHead, indexDiffHash, worktreeDiffHash, recoverability string
		createdAt                                                string
		taskID, turnID, verifiedAt                               sql.NullString
		totalBytes                                               sql.NullInt64
		metadataJSON                                             sql.NullString
	)
	if err := row.Scan(
		&id, &worktreeID, &taskID, &turnID, &status, &artifactRoot,
		&manifestPath, &gitHead, &indexDiffHash, &worktreeDiffHash,
		&recoverability, &totalBytes, &createdAt, &verifiedAt, &metadataJSON,
	); err != nil {
		return Row{}, err
	}
	r.ID = domain.RepositoryCheckpointID(id)
	r.WorktreeID = domain.WorktreeID(worktreeID)
	if taskID.Valid {
		t := domain.TaskID(taskID.String)
		r.TaskID = &t
	}
	if turnID.Valid {
		t := turnID.String
		r.TurnID = &t
	}
	r.Status = Status(status)
	r.ArtifactRoot = artifactRoot
	r.ManifestPath = manifestPath
	r.GitHead = gitHead
	r.IndexDiffHash = indexDiffHash
	r.WorktreeDiffHash = worktreeDiffHash
	r.Recoverability = Recoverability(recoverability)
	if totalBytes.Valid {
		b := totalBytes.Int64
		r.TotalBytes = &b
	}
	r.CreatedAt = createdAt
	if verifiedAt.Valid {
		v := verifiedAt.String
		r.VerifiedAt = &v
	}
	r.MetadataJSON = metadataJSON.String
	return r, nil
}

func nullableTaskID(id *domain.TaskID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

func nullableStringPtr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func nullableInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func orDefaultJSON(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}
