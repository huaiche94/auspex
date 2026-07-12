package progress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/progress"
)

// --- Required test: same idempotency key returns same result -------------

func TestCompleteNode_SameIdempotencyKey_ReturnsSameResult(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-replay")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "replay-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}

	first, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Replayed {
		t.Fatalf("first call must not be reported as replayed")
	}

	second, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("second Run (replay): %v", err)
	}
	if !second.Replayed {
		t.Fatalf("second call with identical key+payload must be reported as replayed")
	}
	if first.Checkpoint.ID != second.Checkpoint.ID {
		t.Fatalf("replay must return the SAME checkpoint ID: first=%s second=%s", first.Checkpoint.ID, second.Checkpoint.ID)
	}
	if first.Manifest.IntegritySHA256 != second.Manifest.IntegritySHA256 {
		t.Fatalf("replay must return the same manifest digest")
	}

	// Replay must not create a second checkpoint row.
	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint after replay, got %d", len(rows))
	}
}

// --- Required test: conflicting idempotency payload rejected -------------

func TestCompleteNode_ConflictingPayload_SameKey_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path1 := writeMarkdownFile(t, "section1.md", "# X\n\nprose one\n")
	nodeID := domain.ProgressNodeID("node-conflict")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	if _, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "shared-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path1)},
	}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Same key, DIFFERENT artifact content (different file -> different
	// sha256) - a distinct payload masquerading under the same key.
	path2 := writeMarkdownFile(t, "section2.md", "# X\n\ncompletely different prose\n")
	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "shared-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-2", path2)},
	})
	if err == nil {
		t.Fatalf("expected conflict rejection for same key + different payload")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict, got %#v", err)
	}
}

// --- Must-reject: duplicate completion with CONFLICTING evidence, no ledger

func TestCompleteNode_AlreadyCompleted_DifferentKey_DifferentEvidence_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-already-done")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	if _, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-a",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// A totally different idempotency key AND genuinely different evidence
	// (a different file, so a different sha256) for the same,
	// already-completed node: this is neither a same-key replay (a04) nor a
	// duplicate-provider-event-with-identical-evidence (a07, see the
	// sibling test below) — it is a real conflicting completion attempt and
	// must reject (Constitution §6.6).
	path2 := writeMarkdownFile(t, "section2.md", "# X\n\ncompletely different prose\n")
	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-b-unrelated",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-2", path2)},
	})
	if err == nil {
		t.Fatalf("expected rejection for re-completing an already-completed node under a new key with different evidence")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict, got %#v", err)
	}
}
