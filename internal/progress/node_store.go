// node_store.go: the Go domain-level store for progress_nodes
// (migrations/0020_progress_nodes.sql). checkpoint-a01 shipped the SQL
// schema only (its own DAG validation command targeted the migration
// engine, not this package, which did not exist yet); this file is the
// first thing in internal/progress that actually reads and writes that
// table.
//
// NodeStore never persists a status change without first calling
// ValidateTransition (statemachine.go) — that is this file's whole reason
// to sit between callers and raw SQL rather than callers issuing UPDATE
// statements directly.
package progress

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// Node is the Go-level representation of one progress_nodes row. JSON-typed
// columns (acceptance_json, next_action_json) are decoded into their
// structured Go shape here so callers never hand-parse JSON themselves.
type Node struct {
	ID             domain.ProgressNodeID
	TaskID         domain.TaskID
	ParentID       *domain.ProgressNodeID
	Ordinal        int64
	Kind           domain.ProgressNodeKind
	Title          string
	Description    *string
	Status         domain.ProgressNodeStatus
	Acceptance     []AcceptanceCriterion
	NextAction     *domain.NextAction
	ProviderNodeID *string
	Version        int64
	StartedAt      *string
	CompletedAt    *string
	UpdatedAt      string
}

// AcceptanceCriterion is one entry of a node's acceptance_json array (ADD
// §18.5's `acceptance:` list — heading_exists, minimum_bytes,
// required_subheadings, markdown_fences_balanced, and so on). It is
// intentionally a generic key/value shape rather than one Go type per
// criterion kind: the criterion vocabulary is owned by the artifact
// validator layer (checkpoint-a03), and baking specific criterion fields
// into the store's Go type would couple this package to that vocabulary
// the same way a CHECK constraint would have coupled the migration to it
// (see 0020's header comment on why status/kind are not CHECK-constrained).
type AcceptanceCriterion struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// ErrNotFound is returned by store lookups when no row matches. Wrapped in
// the frozen domain.Error shape so callers can branch on
// domain.Error.Code == domain.ErrCodeNotFound without importing this
// package's sentinel directly.
var ErrNotFound = &domain.Error{
	Code:      domain.ErrCodeNotFound,
	Message:   "progress: no matching row",
	Retryable: false,
}

// NodeStore is the Go domain-level CRUD layer over progress_nodes. It reads
// the active transaction (if any) from ctx via sqlite.QuerierFromContext,
// so callers running inside a TxRunner.WithTx callback get transactional
// writes automatically, matching the pattern established by
// internal/telemetry/claude's EventStore.
//
// Clock is the frozen domain.Clock port (internal/domain/clock.go), injected
// rather than this package calling time.Now() directly, so tests get
// deterministic updated_at values (same discipline as
// internal/telemetry/claude's Normalizer).
type NodeStore struct {
	db    *sqlite.DB
	clock domain.Clock
}

// NewNodeStore constructs a NodeStore bound to db, using clock for every
// updated_at it writes.
func NewNodeStore(db *sqlite.DB, clock domain.Clock) *NodeStore {
	return &NodeStore{db: db, clock: clock}
}

// now formats the store's clock reading as RFC 3339 UTC, matching every
// other TEXT timestamp column's wire format in this schema (ADD §12.2).
func (s *NodeStore) now() string {
	return s.clock.Now().UTC().Format(time.RFC3339)
}

// Insert creates a new progress_nodes row. It does not itself run through
// ValidateTransition: a freshly inserted node has no prior status to
// transition from, so the caller's chosen initial status (almost always
// domain.NodePending) is accepted as given. Callers that need to enforce
// "new nodes always start pending" do so at a higher layer (the plan
// upsert service, checkpoint-a04+), not here.
func (s *NodeStore) Insert(ctx context.Context, n Node) error {
	q := sqlite.QuerierFromContext(ctx, s.db)

	acceptanceJSON, err := marshalAcceptance(n.Acceptance)
	if err != nil {
		return err
	}
	nextActionJSON, err := marshalNextAction(n.NextAction)
	if err != nil {
		return err
	}

	_, err = q.ExecContext(ctx, `
		INSERT INTO progress_nodes (
			id, task_id, parent_id, ordinal, kind, title, description,
			status, acceptance_json, next_action_json, provider_node_id,
			version, started_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(n.ID), string(n.TaskID), nullableNodeID(n.ParentID), n.Ordinal,
		string(n.Kind), n.Title, nullableString(n.Description),
		string(n.Status), acceptanceJSON, nextActionJSON,
		nullableString(n.ProviderNodeID), n.Version,
		nullableString(n.StartedAt), nullableString(n.CompletedAt), n.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("progress: insert node %s: %w", n.ID, err)
	}
	return nil
}

// Get loads a single node by ID. Returns ErrNotFound (frozen domain.Error
// shape) if no row matches.
func (s *NodeStore) Get(ctx context.Context, id domain.ProgressNodeID) (Node, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, task_id, parent_id, ordinal, kind, title, description,
		       status, acceptance_json, next_action_json, provider_node_id,
		       version, started_at, completed_at, updated_at
		FROM progress_nodes WHERE id = ?`, string(id))
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("progress: get node %s: %w", id, err)
	}
	return n, nil
}

// ListByTask returns every node for a task, ordered by parent then ordinal
// (root nodes first, each in their declared plan order) so callers get a
// deterministic, plan-shaped listing without re-sorting client-side.
func (s *NodeStore) ListByTask(ctx context.Context, taskID domain.TaskID) ([]Node, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	rows, err := q.QueryContext(ctx, `
		SELECT id, task_id, parent_id, ordinal, kind, title, description,
		       status, acceptance_json, next_action_json, provider_node_id,
		       version, started_at, completed_at, updated_at
		FROM progress_nodes
		WHERE task_id = ?
		ORDER BY COALESCE(parent_id, ''), ordinal`, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("progress: list nodes for task %s: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("progress: scan node row for task %s: %w", taskID, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress: list nodes for task %s: %w", taskID, err)
	}
	return out, nil
}

// TransitionStatus moves a node from its currently stored status to `to`,
// enforcing ValidateTransition and an optimistic-concurrency check on
// `version` in the same statement (WHERE id = ? AND version = ?) so a
// caller racing another writer gets a detectable no-row-updated result
// instead of silently clobbering a concurrent change. The node's version
// is incremented by exactly 1 on success.
//
// This does NOT read the current status from the DB and compare in Go
// first — the caller supplies `from` (typically from a Get it already
// performed) and this method verifies it via the state machine before
// issuing the UPDATE; the UPDATE's own WHERE clause is the authoritative
// concurrency guard against a status/version that changed between the
// caller's Get and this call.
func (s *NodeStore) TransitionStatus(ctx context.Context, id domain.ProgressNodeID, from, to domain.ProgressNodeStatus, expectedVersion int64) error {
	if err := ValidateTransition(from, to); err != nil {
		return err
	}

	q := sqlite.QuerierFromContext(ctx, s.db)
	res, err := q.ExecContext(ctx, `
		UPDATE progress_nodes
		SET status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND status = ? AND version = ?`,
		string(to), s.now(), string(id), string(from), expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("progress: transition node %s %s->%s: %w", id, from, to, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("progress: transition node %s: rows affected: %w", id, err)
	}
	if affected == 0 {
		return &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   fmt.Sprintf("progress: node %s was not in expected state (status=%s version=%d) when transition was attempted; concurrent writer likely", id, from, expectedVersion),
			Retryable: true,
			Details: map[string]string{
				"node_id": string(id),
				"from":    string(from),
				"to":      string(to),
			},
		}
	}
	return nil
}

// SetTimestamps updates started_at/completed_at independently of a status
// transition (e.g. StartNode setting started_at the same time it moves to
// in_progress). Nil pointers leave the corresponding column untouched.
func (s *NodeStore) SetTimestamps(ctx context.Context, id domain.ProgressNodeID, startedAt, completedAt *string) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	_, err := q.ExecContext(ctx, `
		UPDATE progress_nodes
		SET started_at = COALESCE(?, started_at),
		    completed_at = COALESCE(?, completed_at),
		    updated_at = ?
		WHERE id = ?`,
		nullableString(startedAt), nullableString(completedAt), s.now(), string(id),
	)
	if err != nil {
		return fmt.Errorf("progress: set timestamps for node %s: %w", id, err)
	}
	return nil
}

func scanNode(row interface {
	Scan(dest ...any) error
}) (Node, error) {
	var (
		n                                   Node
		parentID, description, providerNode sql.NullString
		startedAt, completedAt              sql.NullString
		acceptanceJSON, nextActionJSON      sql.NullString
		id, taskID, kind, title, status     string
		updatedAt                           string
		ordinal, version                    int64
	)
	if err := row.Scan(
		&id, &taskID, &parentID, &ordinal, &kind, &title, &description,
		&status, &acceptanceJSON, &nextActionJSON, &providerNode,
		&version, &startedAt, &completedAt, &updatedAt,
	); err != nil {
		return Node{}, err
	}

	n.ID = domain.ProgressNodeID(id)
	n.TaskID = domain.TaskID(taskID)
	if parentID.Valid {
		pid := domain.ProgressNodeID(parentID.String)
		n.ParentID = &pid
	}
	n.Ordinal = ordinal
	n.Kind = domain.ProgressNodeKind(kind)
	n.Title = title
	if description.Valid {
		d := description.String
		n.Description = &d
	}
	n.Status = domain.ProgressNodeStatus(status)
	acceptance, err := unmarshalAcceptance(acceptanceJSON.String)
	if err != nil {
		return Node{}, err
	}
	n.Acceptance = acceptance
	nextAction, err := unmarshalNextAction(nextActionJSON)
	if err != nil {
		return Node{}, err
	}
	n.NextAction = nextAction
	if providerNode.Valid {
		p := providerNode.String
		n.ProviderNodeID = &p
	}
	n.Version = version
	if startedAt.Valid {
		s := startedAt.String
		n.StartedAt = &s
	}
	if completedAt.Valid {
		c := completedAt.String
		n.CompletedAt = &c
	}
	n.UpdatedAt = updatedAt
	return n, nil
}

func marshalAcceptance(a []AcceptanceCriterion) (string, error) {
	if a == nil {
		a = []AcceptanceCriterion{}
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("progress: marshal acceptance: %w", err)
	}
	return string(b), nil
}

func unmarshalAcceptance(s string) ([]AcceptanceCriterion, error) {
	if s == "" {
		return nil, nil
	}
	var a []AcceptanceCriterion
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return nil, fmt.Errorf("progress: unmarshal acceptance_json: %w", err)
	}
	return a, nil
}

func marshalNextAction(na *domain.NextAction) (sql.NullString, error) {
	if na == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(na)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("progress: marshal next_action: %w", err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func unmarshalNextAction(s sql.NullString) (*domain.NextAction, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	var na domain.NextAction
	if err := json.Unmarshal([]byte(s.String), &na); err != nil {
		return nil, fmt.Errorf("progress: unmarshal next_action_json: %w", err)
	}
	return &na, nil
}

func nullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func nullableNodeID(id *domain.ProgressNodeID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}
