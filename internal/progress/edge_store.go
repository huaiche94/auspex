// edge_store.go: the Go domain-level store for progress_edges
// (migrations/0021_progress_edges.sql) — explicit dependency/relationship
// edges between Progress Tree nodes, beyond the parent/child shape already
// carried by progress_nodes.parent_id (see that migration's header).
package progress

import (
	"context"
	"fmt"
	"strings"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// EdgeKind is the frozen (for this package's own vocabulary) set of
// dependency-policy edge kinds this store enforces reads/writes for. Like
// progress_nodes.status, 0021's edge_kind column is deliberately not
// CHECK-constrained (immutable-DDL reasoning); this package still owns and
// validates against a defined vocabulary rather than accepting arbitrary
// strings, so a typo'd edge kind fails at write time, not silently at query
// time later.
type EdgeKind string

const (
	// EdgeDependsOn: from_node_id cannot complete until to_node_id is
	// completed (or skipped) — the ordinary blocking dependency.
	EdgeDependsOn EdgeKind = "depends_on"
	// EdgeRelatesTo: an informational, non-blocking relationship.
	EdgeRelatesTo EdgeKind = "relates_to"
)

var validEdgeKinds = map[EdgeKind]bool{
	EdgeDependsOn: true,
	EdgeRelatesTo: true,
}

// Edge is the Go-level representation of one progress_edges row.
type Edge struct {
	TaskID     domain.TaskID
	FromNodeID domain.ProgressNodeID
	ToNodeID   domain.ProgressNodeID
	Kind       EdgeKind
}

// EdgeStore is the Go domain-level CRUD layer over progress_edges.
type EdgeStore struct {
	db *sqlite.DB
}

// NewEdgeStore constructs an EdgeStore bound to db.
func NewEdgeStore(db *sqlite.DB) *EdgeStore {
	return &EdgeStore{db: db}
}

// Insert creates a new progress_edges row. Returns a validation error
// (ErrCodeValidation) for an unrecognized Kind, and a conflict error
// (ErrCodeConflict) if the exact (task, from, to, kind) edge already exists
// — the composite PRIMARY KEY (0021's schema) makes a duplicate insert a
// constraint violation rather than a silent second row (0021's own header
// comment); this method surfaces that as the frozen conflict error shape
// instead of a raw SQLite error leaking through.
func (s *EdgeStore) Insert(ctx context.Context, e Edge) error {
	if !validEdgeKinds[e.Kind] {
		return &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("progress: unknown edge kind %q", e.Kind),
			Retryable: false,
		}
	}
	if e.FromNodeID == e.ToNodeID {
		return &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("progress: self-referential edge rejected for node %s", e.FromNodeID),
			Retryable: false,
		}
	}

	q := sqlite.QuerierFromContext(ctx, s.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind)
		VALUES (?, ?, ?, ?)`,
		string(e.TaskID), string(e.FromNodeID), string(e.ToNodeID), string(e.Kind),
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return &domain.Error{
				Code:      domain.ErrCodeConflict,
				Message:   fmt.Sprintf("progress: edge %s -[%s]-> %s already exists", e.FromNodeID, e.Kind, e.ToNodeID),
				Retryable: false,
			}
		}
		return fmt.Errorf("progress: insert edge %s -[%s]-> %s: %w", e.FromNodeID, e.Kind, e.ToNodeID, err)
	}
	return nil
}

// DependenciesOf returns the node IDs that `nodeID` depends_on (i.e. must
// be completed or skipped before nodeID may complete). Only EdgeDependsOn
// edges are considered; EdgeRelatesTo edges are informational and never
// gate completion.
func (s *EdgeStore) DependenciesOf(ctx context.Context, taskID domain.TaskID, nodeID domain.ProgressNodeID) ([]domain.ProgressNodeID, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	rows, err := q.QueryContext(ctx, `
		SELECT to_node_id FROM progress_edges
		WHERE task_id = ? AND from_node_id = ? AND edge_kind = ?`,
		string(taskID), string(nodeID), string(EdgeDependsOn),
	)
	if err != nil {
		return nil, fmt.Errorf("progress: dependencies of node %s: %w", nodeID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.ProgressNodeID
	for rows.Next() {
		var to string
		if err := rows.Scan(&to); err != nil {
			return nil, fmt.Errorf("progress: scan dependency row for node %s: %w", nodeID, err)
		}
		out = append(out, domain.ProgressNodeID(to))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress: dependencies of node %s: %w", nodeID, err)
	}
	return out, nil
}

// ListByTask returns every edge for a task.
func (s *EdgeStore) ListByTask(ctx context.Context, taskID domain.TaskID) ([]Edge, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	rows, err := q.QueryContext(ctx, `
		SELECT task_id, from_node_id, to_node_id, edge_kind
		FROM progress_edges WHERE task_id = ?`, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("progress: list edges for task %s: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Edge
	for rows.Next() {
		var e Edge
		var taskIDStr, fromID, toID, kind string
		if err := rows.Scan(&taskIDStr, &fromID, &toID, &kind); err != nil {
			return nil, fmt.Errorf("progress: scan edge row for task %s: %w", taskID, err)
		}
		e.TaskID = domain.TaskID(taskIDStr)
		e.FromNodeID = domain.ProgressNodeID(fromID)
		e.ToNodeID = domain.ProgressNodeID(toID)
		e.Kind = EdgeKind(kind)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress: list edges for task %s: %w", taskID, err)
	}
	return out, nil
}

// isUniqueConstraintErr reports whether err is a SQLite UNIQUE/PRIMARY KEY
// constraint violation. modernc.org/sqlite wraps the underlying error in a
// way that does not expose a typed sentinel, so this checks the error
// string for the driver's documented constraint-violation substring — the
// same pragmatic approach errors package guidance recommends when a driver
// offers no typed error (this string is stable across modernc.org/sqlite
// releases; a change here would need the same driver-string check as any
// SQLite constraint-detection code, not a redesign).
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
