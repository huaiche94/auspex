// complete_node_provider_events_test.go: checkpoint-a07's own required
// tests — duplicate and out-of-order PROVIDER event handling, as distinct
// from checkpoint-a04's idempotency-KEY matching (same key -> same result;
// conflicting payload under the same key -> rejected), already proven in
// complete_node_idempotency_test.go and NOT re-tested here.
//
// This file covers exactly the two scenarios named in this node's own
// scope: (1) a provider delivering a genuinely duplicate lifecycle signal
// through a different channel, arriving with a DIFFERENT caller-derived
// IdempotencyKey than the original delivery but IDENTICAL evidence, and
// (2) an out-of-order arrival — a completion signal for a child node
// reaching CompleteNode before its parent's own in_progress transition was
// ever recorded.
package progress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
)

// --- Duplicate provider event: different key, IDENTICAL evidence --------

// TestCompleteNode_DuplicateProviderEvent_DifferentKey_SameEvidence_Replayed
// is qa-04's primary anchor point: a provider (e.g. Claude Code) redelivers
// the same underlying "TaskCompleted" signal over a second channel, which
// independently derives its own idempotency key rather than sharing dedup
// state with the first delivery. Per Constitution §6.6 ("duplicate
// completion with CONFLICTING evidence is rejected"), duplicate completion
// with IDENTICAL evidence is not a conflict — it must replay the original
// result, not error, regardless of the key mismatch.
func TestCompleteNode_DuplicateProviderEvent_DifferentKey_SameEvidence_Replayed(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-dup-provider-event")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	// First delivery: channel A's own idempotency key.
	first, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "channel-a-key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err != nil {
		t.Fatalf("first delivery (channel A): %v", err)
	}
	if first.Replayed {
		t.Fatalf("first delivery must not be reported as replayed")
	}

	// Second delivery: channel B redelivers the SAME underlying event
	// (identical node, identical artifact evidence -> identical sha256) but
	// computed its own, unrelated idempotency key, since it doesn't share
	// dedup state with channel A. Must be recognized as a duplicate, not a
	// conflict.
	second, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "channel-b-key-completely-different",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1-redelivered", path)},
	})
	if err != nil {
		t.Fatalf("second delivery (channel B, duplicate event): unexpected rejection: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("second delivery with identical evidence under a different key must be reported as replayed")
	}
	if first.Checkpoint.ID != second.Checkpoint.ID {
		t.Fatalf("duplicate provider event must return the SAME checkpoint ID: first=%s second=%s", first.Checkpoint.ID, second.Checkpoint.ID)
	}
	if first.Manifest.IntegritySHA256 != second.Manifest.IntegritySHA256 {
		t.Fatalf("duplicate provider event must return the same manifest digest")
	}

	// Exactly one checkpoint row must exist — the duplicate delivery must
	// not have created a second one.
	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint after a duplicate provider event, got %d", len(rows))
	}
}

// TestCompleteNode_ThirdDeliveryChannel_SameEvidence_AlsoReplayed proves the
// duplicate-detection is not merely a two-delivery special case: a THIRD
// independently-keyed channel redelivering the same event must also replay
// cleanly, not be treated as "one retry is fine, a second is suspicious."
func TestCompleteNode_ThirdDeliveryChannel_SameEvidence_AlsoReplayed(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-triple-delivery")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	keys := []string{"webhook-key", "polling-key", "manual-resync-key"}
	var checkpointID domain.StateCheckpointID
	for i, key := range keys {
		result, err := cn.Run(ctx, progress.CompleteNodeInput{
			NodeID:         nodeID,
			IdempotencyKey: key,
			Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
		})
		if err != nil {
			t.Fatalf("delivery %d (key=%s): unexpected error: %v", i, key, err)
		}
		if i == 0 {
			checkpointID = result.Checkpoint.ID
			continue
		}
		if !result.Replayed {
			t.Fatalf("delivery %d (key=%s) must be reported as replayed", i, key)
		}
		if result.Checkpoint.ID != checkpointID {
			t.Fatalf("delivery %d (key=%s) returned a different checkpoint ID than the first delivery", i, key)
		}
	}

	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint after 3 redeliveries, got %d", len(rows))
	}
}

// --- Out-of-order arrival: child completes before parent ever started ---

// TestCompleteNode_ChildCompletesBeforeParentStarted_RejectedOutOfOrder is
// this node's second required scenario: a completion signal for a child
// node arrives before its parent's own in_progress transition was ever
// recorded. This must be rejected — accepting it would let the Progress
// Tree's canonical state (Constitution §6.1) become internally incoherent
// (a completed node whose parent never started).
func TestCompleteNode_ChildCompletesBeforeParentStarted_RejectedOutOfOrder(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	parentID := domain.ProgressNodeID("parent-never-started")
	childID := domain.ProgressNodeID("child-out-of-order")

	// Parent stays pending (never transitioned to in_progress) — the
	// out-of-order scenario this test exists to catch.
	parent := newDocumentNode(taskID, parentID, 1, domain.NodePending, "# Parent")
	insertNode(t, db, clock, parent)

	child := newDocumentNode(taskID, childID, 1, domain.NodePending, "# Child")
	child.ParentID = &parentID
	insertNode(t, db, clock, child)
	moveNodeToInProgress(t, db, clock, childID)

	path := writeMarkdownFile(t, "child-section.md", "# Child\n\nprose\n")
	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         childID,
		IdempotencyKey: "child-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err == nil {
		t.Fatalf("expected rejection: child completed before parent ever started")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict for out-of-order completion, got %#v", err)
	}
	if !derr.Retryable {
		t.Fatalf("expected out-of-order rejection to be Retryable (the parent may start later, resolving the ordering), got Retryable=false")
	}
}

// TestCompleteNode_ChildCompletes_ParentInProgress_Allowed is the paired
// positive case: once the parent HAS started (in_progress), the child's
// completion is accepted normally — this test guards against
// checkParentOrdering accidentally blocking the ordinary, correctly-ordered
// path.
func TestCompleteNode_ChildCompletes_ParentInProgress_Allowed(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	parentID := domain.ProgressNodeID("parent-started")
	childID := domain.ProgressNodeID("child-ordered")

	parent := newDocumentNode(taskID, parentID, 1, domain.NodePending, "# Parent")
	insertNode(t, db, clock, parent)
	moveNodeToInProgress(t, db, clock, parentID)

	child := newDocumentNode(taskID, childID, 1, domain.NodePending, "# Child")
	child.ParentID = &parentID
	insertNode(t, db, clock, child)
	moveNodeToInProgress(t, db, clock, childID)

	path := writeMarkdownFile(t, "child-section.md", "# Child\n\nprose\n")
	result, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         childID,
		IdempotencyKey: "child-key-ok",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err != nil {
		t.Fatalf("expected child completion to succeed once parent has started: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected child node to be completed, got status=%s", result.Node.Status)
	}
}

// TestCompleteNode_ChildCompletes_ParentAlreadyCompleted_Allowed covers the
// legitimate race this check must NOT block: a parent can validly complete
// slightly before a straggling child's own evidence finishes staging (ADD's
// state machine permits this), so a completed parent must still count as
// "started" for the child's own ordering check.
func TestCompleteNode_ChildCompletes_ParentAlreadyCompleted_Allowed(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	parentID := domain.ProgressNodeID("parent-finished-first")
	childID := domain.ProgressNodeID("child-straggler")

	parent := newDocumentNode(taskID, parentID, 1, domain.NodePending, "# Parent")
	insertNode(t, db, clock, parent)
	moveNodeToInProgress(t, db, clock, parentID)
	parentPath := writeMarkdownFile(t, "parent-section.md", "# Parent\n\nprose\n")
	if _, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         parentID,
		IdempotencyKey: "parent-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("parent-artifact", parentPath)},
	}); err != nil {
		t.Fatalf("complete parent: %v", err)
	}

	child := newDocumentNode(taskID, childID, 1, domain.NodePending, "# Child")
	child.ParentID = &parentID
	insertNode(t, db, clock, child)
	moveNodeToInProgress(t, db, clock, childID)

	childPath := writeMarkdownFile(t, "child-section.md", "# Child\n\nprose\n")
	result, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         childID,
		IdempotencyKey: "child-key-straggler",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("child-artifact", childPath)},
	})
	if err != nil {
		t.Fatalf("expected child completion to succeed when parent already completed: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected child node to be completed, got status=%s", result.Node.Status)
	}
}

// TestCompleteNode_RootNode_NoParent_OrderingCheckSkipped guards the
// no-parent case: a root node (ParentID == nil) has nothing to check and
// must complete normally.
func TestCompleteNode_RootNode_NoParent_OrderingCheckSkipped(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	nodeID := domain.ProgressNodeID("root-node")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "root-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err != nil {
		t.Fatalf("expected a root node (no parent) to complete without an ordering check: %v", err)
	}
}
