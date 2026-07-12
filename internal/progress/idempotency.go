// idempotency.go: the node_completions ledger (migrations/0024) CRUD and
// the check/record logic CompleteNode uses to satisfy ADD §18.12 /
// CONTRACT_FREEZE.md's idempotency rule: "same completion request replayed
// with the same key MUST return the same result; a different payload under
// the same key is a conflict, not a silent overwrite."
package progress

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// completionLedgerRow is the Go-level shape of one node_completions row.
type completionLedgerRow struct {
	NodeID            domain.ProgressNodeID
	TaskID            domain.TaskID
	IdempotencyKey    string
	PayloadDigest     string
	StateCheckpointID domain.StateCheckpointID
	CompletedNodeJSON string
}

// getCompletionLedger looks up the ledger row for nodeID, if any.
func getCompletionLedger(ctx context.Context, db *sqlite.DB, nodeID domain.ProgressNodeID) (completionLedgerRow, bool, error) {
	q := sqlite.QuerierFromContext(ctx, db)
	row := q.QueryRowContext(ctx, `
		SELECT node_id, task_id, idempotency_key, payload_digest,
		       state_checkpoint_id, completed_node_json
		FROM node_completions WHERE node_id = ?`, string(nodeID))

	var r completionLedgerRow
	var nodeIDStr, taskIDStr, key, digest, checkpointID, nodeJSON string
	err := row.Scan(&nodeIDStr, &taskIDStr, &key, &digest, &checkpointID, &nodeJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return completionLedgerRow{}, false, nil
	}
	if err != nil {
		return completionLedgerRow{}, false, fmt.Errorf("progress: lookup completion ledger for node %s: %w", nodeID, err)
	}
	r.NodeID = domain.ProgressNodeID(nodeIDStr)
	r.TaskID = domain.TaskID(taskIDStr)
	r.IdempotencyKey = key
	r.PayloadDigest = digest
	r.StateCheckpointID = domain.StateCheckpointID(checkpointID)
	r.CompletedNodeJSON = nodeJSON
	return r, true, nil
}

// insertCompletionLedger writes the ledger row for a freshly completed
// node. Called from inside CompleteNode's WithTx callback so it commits
// atomically with the node/checkpoint rows it references.
func insertCompletionLedger(ctx context.Context, db *sqlite.DB, clock domain.Clock, r completionLedgerRow) error {
	q := sqlite.QuerierFromContext(ctx, db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO node_completions (
			node_id, task_id, idempotency_key, payload_digest,
			state_checkpoint_id, completed_node_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(r.NodeID), string(r.TaskID), r.IdempotencyKey, r.PayloadDigest,
		string(r.StateCheckpointID), r.CompletedNodeJSON,
		clock.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			// The ledger's PRIMARY KEY is node_id alone: a second attempt
			// to insert for the same node inside a fresh transaction can
			// only happen if two concurrent CompleteNode calls both got
			// past the pre-transaction idempotency check (a benign,
			// expected race under -race testing) and both reached this
			// point — the node-status transition's own optimistic-
			// concurrency guard (TransitionStatus's WHERE status=? AND
			// version=?) is what actually arbitrates that race and would
			// have already failed the loser before this insert is
			// attempted, so reaching a UNIQUE violation here specifically
			// would indicate that guard was bypassed. Surfaced as a
			// conflict rather than panicking either way.
			return &domain.Error{
				Code:      domain.ErrCodeConflict,
				Message:   fmt.Sprintf("progress: completion ledger row for node %s already exists", r.NodeID),
				Retryable: false,
			}
		}
		return fmt.Errorf("progress: insert completion ledger for node %s: %w", r.NodeID, err)
	}
	return nil
}

// checkIdempotency implements CompleteNode's pre-mutation replay/conflict
// check. Returns (result, true, nil) if this exact request was already
// completed and its recorded result should be returned unchanged;
// (zero, false, nil) if no prior completion exists (caller should
// proceed); or a non-nil error (ErrCodeConflict) if the SAME
// idempotency key was used before with a DIFFERENT payload.
func (c *CompleteNode) checkIdempotency(ctx context.Context, taskID domain.TaskID, nodeID domain.ProgressNodeID, key, digest string) (CompleteNodeResult, bool, error) {
	ledger, found, err := getCompletionLedger(ctx, c.DB, nodeID)
	if err != nil {
		return CompleteNodeResult{}, false, err
	}
	if !found {
		return CompleteNodeResult{}, false, nil
	}

	if ledger.IdempotencyKey != key {
		// A different key entirely for an already-completed node: the
		// caller is not replaying, it is attempting a fresh completion of
		// a terminal node. Falls through to the generic
		// already-completed rejection in Run (checked right after this
		// call returns not-replayed).
		return CompleteNodeResult{}, false, nil
	}
	if ledger.PayloadDigest != digest {
		return CompleteNodeResult{}, false, &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   fmt.Sprintf("progress: idempotency key %q was already used for node %s with different evidence; conflicting payload rejected", key, nodeID),
			Retryable: false,
			Details: map[string]string{
				"node_id":         string(nodeID),
				"idempotency_key": key,
			},
		}
	}

	// Same key, same payload: return the exact prior result.
	var completed Node
	if err := json.Unmarshal([]byte(ledger.CompletedNodeJSON), &completed); err != nil {
		return CompleteNodeResult{}, false, fmt.Errorf("progress: unmarshal replayed node for %s: %w", nodeID, err)
	}
	checkpointRow, err := c.Checkpoints.Get(ctx, ledger.StateCheckpointID)
	if err != nil {
		return CompleteNodeResult{}, false, fmt.Errorf("progress: load replayed checkpoint %s for node %s: %w", ledger.StateCheckpointID, nodeID, err)
	}
	manifest, err := statecheckpoint.Unmarshal([]byte(checkpointRow.ManifestJSON))
	if err != nil {
		return CompleteNodeResult{}, false, fmt.Errorf("progress: unmarshal replayed manifest for node %s: %w", nodeID, err)
	}
	return CompleteNodeResult{Node: completed, Checkpoint: checkpointRow, Manifest: manifest, Replayed: true}, true, nil
}

// recordIdempotency writes the ledger row for this completion, inside the
// same transaction as the node/checkpoint rows it references.
func (c *CompleteNode) recordIdempotency(ctx context.Context, taskID domain.TaskID, nodeID domain.ProgressNodeID, key, digest string, checkpointID domain.StateCheckpointID, completed Node) error {
	nodeJSON, err := json.Marshal(completed)
	if err != nil {
		return fmt.Errorf("progress: marshal completed node %s for ledger: %w", nodeID, err)
	}
	return insertCompletionLedger(ctx, c.DB, c.Clock, completionLedgerRow{
		NodeID:            nodeID,
		TaskID:            taskID,
		IdempotencyKey:    key,
		PayloadDigest:     digest,
		StateCheckpointID: checkpointID,
		CompletedNodeJSON: string(nodeJSON),
	})
}
