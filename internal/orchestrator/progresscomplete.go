// progresscomplete.go implements the explicit-completion half of the
// "explicit completion + event correlation" design that closes qa's P1
// finding (docs/implementation/vertical-slice/qa.md §Severity-ranked
// findings; GitHub issue #1): the orchestration function behind
// `auspex progress complete` (internal/cli/progress.go).
//
// This is a deliberately thin sequencing layer, like CheckpointCreate
// (checkpoint.go): all completion semantics — idempotency ledger,
// duplicate/conflicting-evidence rejection, artifact staging + validator
// verification, the atomic node-transition + State Checkpoint transaction
// — live in internal/progress.CompleteNode behind the frozen
// app.ProgressTreeService.CompleteNode port (Constitution §6; ADD §18.4,
// §18.7). Nothing here re-validates evidence or re-implements any of
// that; this function's whole job is the CLI-facing shape: validate the
// request is structurally complete, call the frozen port, and hand back
// the completed node plus its checkpoint for rendering.
//
// The IdempotencyKey is caller-supplied by design (frozen
// app.CompleteNodeRequest field; CONTRACT_FREEZE.md "same completion
// request replayed with the same key MUST return the same result") — the
// caller owns retry identity, so a re-run of the exact same command is a
// safe replay, never accidental duplicate work.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// ProgressCompleteDeps bundles ProgressComplete's single collaborator: the
// frozen Progress Tree service. Required; ProgressComplete fails closed
// (ErrCodeUnavailable) if nil rather than deferring a nil-pointer panic to
// the port call, matching CheckpointCreate's own dependency checks.
type ProgressCompleteDeps struct {
	ProgressTree app.ProgressTreeService
}

// ProgressCompleteRequest is `auspex progress complete`'s input — a
// direct mirror of the frozen app.CompleteNodeRequest field set (NO new
// fields; the port is frozen and this request adds nothing to it).
type ProgressCompleteRequest struct {
	NodeID         domain.ProgressNodeID
	IdempotencyKey string
	Artifacts      []domain.ArtifactRef
}

// ProgressCompleteResult bundles what the CLI renders: the node's
// post-completion state and the State Checkpoint the same atomic operation
// created (Constitution §6.3: "every node completion creates a State
// Checkpoint in the same atomic operation").
type ProgressCompleteResult struct {
	Node       app.ProgressNode
	Checkpoint domain.StateCheckpoint
}

// ProgressComplete validates req and calls the frozen
// app.ProgressTreeService.CompleteNode port. Every error from the port —
// validator rejections, conflicting-evidence conflicts, unknown node —
// propagates verbatim as the typed *domain.Error the service already
// constructs, so the CLI's uniform error contract (internal/cli/errors.go)
// renders it without translation.
func ProgressComplete(ctx context.Context, deps ProgressCompleteDeps, req ProgressCompleteRequest) (ProgressCompleteResult, error) {
	if req.NodeID == "" {
		return ProgressCompleteResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: ProgressComplete requires a NodeID", Retryable: false,
		}
	}
	if req.IdempotencyKey == "" {
		return ProgressCompleteResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: ProgressComplete requires an IdempotencyKey", Retryable: false,
		}
	}
	if deps.ProgressTree == nil {
		return ProgressCompleteResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: ProgressComplete requires a non-nil ProgressTreeService", Retryable: false,
		}
	}

	node, checkpoint, err := deps.ProgressTree.CompleteNode(ctx, app.CompleteNodeRequest{
		NodeID:         req.NodeID,
		IdempotencyKey: req.IdempotencyKey,
		Artifacts:      req.Artifacts,
	})
	if err != nil {
		return ProgressCompleteResult{}, err
	}
	return ProgressCompleteResult{Node: node, Checkpoint: checkpoint}, nil
}
