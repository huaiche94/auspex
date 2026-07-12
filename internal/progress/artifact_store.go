// artifact_store.go: the Go domain-level store for artifacts
// (migrations/0022_artifacts.sql) — durable, validator-checked evidence
// rows backing node completion ("Completed means evidenced," Constitution
// §6.2). Maps to domain.ArtifactRef (internal/domain/artifact.go), the
// frozen contract type app.CompleteNodeRequest.Artifacts already carries.
//
// This store persists artifact rows and their validation_status; it does
// NOT itself run validators (that is internal/artifacts, checkpoint-a03)
// and it does NOT itself implement the full stage/verify/commit CompleteNode
// protocol (that is checkpoint-a04) — this is deliberately just the CRUD
// seam those two build on, per this wave's scoped brief.
package progress

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// ValidationStatus is this package's vocabulary for artifacts.
// validation_status (deliberately not CHECK-constrained in 0022, same
// immutable-DDL reasoning as progress_nodes.status).
type ValidationStatus string

const (
	ValidationPending ValidationStatus = "pending"
	ValidationPassed  ValidationStatus = "passed"
	ValidationFailed  ValidationStatus = "failed"
)

// ArtifactRow is the Go-level representation of one artifacts row.
type ArtifactRow struct {
	ID               string
	TaskID           domain.TaskID
	NodeID           *domain.ProgressNodeID
	Kind             string
	URI              string
	MediaType        *string
	Bytes            int64
	SHA256           string
	ValidatorID      *string
	ValidationStatus ValidationStatus
	MetadataJSON     string
	CreatedAt        string
}

// ArtifactStore is the Go domain-level CRUD layer over artifacts.
type ArtifactStore struct {
	db *sqlite.DB
}

// NewArtifactStore constructs an ArtifactStore bound to db.
func NewArtifactStore(db *sqlite.DB) *ArtifactStore {
	return &ArtifactStore{db: db}
}

// Insert creates a new artifacts row. The same (progress_node_id, uri,
// sha256) tuple cannot exist twice (0022's UNIQUE constraint); a second
// Insert attempt for identical evidence returns the frozen conflict error
// shape rather than a raw driver error, so a caller (checkpoint-a04's
// CompleteNode) can distinguish "this exact evidence is already recorded"
// from an unrelated failure. A DIFFERENT sha256 for the same
// (node, uri) is NOT rejected by this constraint — surfacing that as a
// completion conflict is the CompleteNode protocol's job (Constitution
// §6.6), not this store's; this store only guards literal duplicate rows.
func (s *ArtifactStore) Insert(ctx context.Context, a ArtifactRow) error {
	if a.ValidationStatus == "" {
		a.ValidationStatus = ValidationPending
	}
	q := sqlite.QuerierFromContext(ctx, s.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO artifacts (
			id, task_id, progress_node_id, kind, uri, media_type, bytes,
			sha256, validator_id, validation_status, metadata_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, string(a.TaskID), nullableNodeID(a.NodeID), a.Kind, a.URI,
		nullableString(a.MediaType), a.Bytes, a.SHA256,
		nullableString(a.ValidatorID), string(a.ValidationStatus),
		orDefault(a.MetadataJSON, "{}"), a.CreatedAt,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return &domain.Error{
				Code:      domain.ErrCodeConflict,
				Message:   fmt.Sprintf("progress: artifact %s (uri=%s sha256=%s) already recorded for this node", a.ID, a.URI, a.SHA256),
				Retryable: false,
			}
		}
		return fmt.Errorf("progress: insert artifact %s: %w", a.ID, err)
	}
	return nil
}

// Get loads a single artifact row by ID.
func (s *ArtifactStore) Get(ctx context.Context, id string) (ArtifactRow, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, task_id, progress_node_id, kind, uri, media_type, bytes,
		       sha256, validator_id, validation_status, metadata_json, created_at
		FROM artifacts WHERE id = ?`, id)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactRow{}, ErrNotFound
	}
	if err != nil {
		return ArtifactRow{}, fmt.Errorf("progress: get artifact %s: %w", id, err)
	}
	return a, nil
}

// ListByNode returns every artifact row attached to a node, most recent
// first.
func (s *ArtifactStore) ListByNode(ctx context.Context, nodeID domain.ProgressNodeID) ([]ArtifactRow, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	rows, err := q.QueryContext(ctx, `
		SELECT id, task_id, progress_node_id, kind, uri, media_type, bytes,
		       sha256, validator_id, validation_status, metadata_json, created_at
		FROM artifacts WHERE progress_node_id = ?
		ORDER BY created_at DESC`, string(nodeID))
	if err != nil {
		return nil, fmt.Errorf("progress: list artifacts for node %s: %w", nodeID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ArtifactRow
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("progress: scan artifact row for node %s: %w", nodeID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress: list artifacts for node %s: %w", nodeID, err)
	}
	return out, nil
}

// SetValidationStatus updates the validation_status of an existing artifact
// row (e.g. pending -> passed once a validator confirms evidence).
func (s *ArtifactStore) SetValidationStatus(ctx context.Context, id string, status ValidationStatus) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	res, err := q.ExecContext(ctx, `UPDATE artifacts SET validation_status = ? WHERE id = ?`, string(status), id)
	if err != nil {
		return fmt.Errorf("progress: set validation status for artifact %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("progress: set validation status for artifact %s: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanArtifact(row interface {
	Scan(dest ...any) error
}) (ArtifactRow, error) {
	var (
		a                              ArtifactRow
		id, taskID, kind, uri, sha256  string
		validationStatus, createdAt    string
		nodeID, mediaType, validatorID sql.NullString
		metadataJSON                   sql.NullString
		bytesVal                       int64
	)
	if err := row.Scan(
		&id, &taskID, &nodeID, &kind, &uri, &mediaType, &bytesVal,
		&sha256, &validatorID, &validationStatus, &metadataJSON, &createdAt,
	); err != nil {
		return ArtifactRow{}, err
	}
	a.ID = id
	a.TaskID = domain.TaskID(taskID)
	if nodeID.Valid {
		n := domain.ProgressNodeID(nodeID.String)
		a.NodeID = &n
	}
	a.Kind = kind
	a.URI = uri
	if mediaType.Valid {
		m := mediaType.String
		a.MediaType = &m
	}
	a.Bytes = bytesVal
	a.SHA256 = sha256
	if validatorID.Valid {
		v := validatorID.String
		a.ValidatorID = &v
	}
	a.ValidationStatus = ValidationStatus(validationStatus)
	a.MetadataJSON = metadataJSON.String
	a.CreatedAt = createdAt
	return a, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
