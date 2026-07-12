package progress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/progress"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
)

func fixedClockAt(t time.Time) domain.Clock { return fixedClock{t} }

// --- Required test: valid Markdown section completes and checkpoints ----

func TestCompleteNode_ValidMarkdownSection_CompletesAndCheckpoints(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", validMarkdown)
	nodeID := domain.ProgressNodeID("node-valid")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# 20. Graceful Pause and Auto Resume"))
	moveNodeToInProgress(t, db, clock, nodeID)

	result, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected node completed, got %s", result.Node.Status)
	}
	if result.Checkpoint.ID == "" {
		t.Fatalf("expected a checkpoint ID")
	}
	if result.Manifest.IntegritySHA256 == "" {
		t.Fatalf("expected a sealed manifest with a non-empty integrity checksum")
	}
	if len(result.Manifest.Artifacts) != 1 || result.Manifest.Artifacts[0].ValidationStatus != "passed" {
		t.Fatalf("expected manifest to record one passed artifact, got %+v", result.Manifest.Artifacts)
	}

	// Checkpoint must be independently verifiable (statecheckpoint.Verify).
	stored, err := cn.Checkpoints.Get(ctx, result.Checkpoint.ID)
	if err != nil {
		t.Fatalf("Get checkpoint: %v", err)
	}
	if stored.IntegritySHA256 != result.Manifest.IntegritySHA256 {
		t.Fatalf("stored checkpoint digest mismatch")
	}
}

// --- Required test: missing heading or unbalanced fence rejected --------

func TestCompleteNode_MissingHeading_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", missingHeadingMarkdown)
	nodeID := domain.ProgressNodeID("node-missing-heading")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# 20. Graceful Pause and Auto Resume"))
	moveNodeToInProgress(t, db, clock, nodeID)

	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err == nil {
		t.Fatalf("expected rejection for missing heading, got nil error")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %#v", err)
	}

	// Node must remain NOT completed.
	got, gerr := cn.Nodes.Get(ctx, nodeID)
	if gerr != nil {
		t.Fatalf("Get node: %v", gerr)
	}
	if got.Status == domain.NodeCompleted {
		t.Fatalf("node must not be completed after validator rejection")
	}
}

func TestCompleteNode_UnbalancedFence_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", unbalancedFenceMarkdown)
	nodeID := domain.ProgressNodeID("node-unbalanced-fence")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# 20. Graceful Pause and Auto Resume"))
	moveNodeToInProgress(t, db, clock, nodeID)

	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err == nil {
		t.Fatalf("expected rejection for unbalanced fence, got nil error")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %#v", err)
	}

	got, gerr := cn.Nodes.Get(ctx, nodeID)
	if gerr != nil {
		t.Fatalf("Get node: %v", gerr)
	}
	if got.Status == domain.NodeCompleted {
		t.Fatalf("node must not be completed after validator rejection")
	}
}

// --- Must-reject: "agent says complete" with no artifact -----------------

func TestCompleteNode_NoArtifacts_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	nodeID := domain.ProgressNodeID("node-no-artifacts")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      nil,
	})
	if err == nil {
		t.Fatalf("expected rejection for no artifacts")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %#v", err)
	}
}

// --- Must-reject: missing artifact file -----------------------------------

func TestCompleteNode_ArtifactFileDoesNotExist_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	nodeID := domain.ProgressNodeID("node-missing-file")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", "/nonexistent/path/does-not-exist.md")},
	})
	if err == nil {
		t.Fatalf("expected rejection for missing artifact file")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %#v", err)
	}
}

// --- Must-reject: changed artifact (claimed checksum does not match) -----

func TestCompleteNode_ChangedArtifact_ChecksumMismatch_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", validMarkdown)
	nodeID := domain.ProgressNodeID("node-changed-artifact")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# 20. Graceful Pause and Auto Resume"))
	moveNodeToInProgress(t, db, clock, nodeID)

	ref := fileArtifactRef("artifact-1", path)
	ref.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"[:64] // deliberately wrong

	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{ref},
	})
	if err == nil {
		t.Fatalf("expected rejection for checksum mismatch")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeIntegrity {
		t.Fatalf("expected ErrCodeIntegrity, got %#v", err)
	}
}

// --- Must-reject: completed child with violated dependency policy -------

func TestCompleteNode_ViolatedDependency_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	depID := domain.ProgressNodeID("node-dep")
	nodeID := domain.ProgressNodeID("node-depends-on-dep")
	insertNode(t, db, clock, newDocumentNode(taskID, depID, 1, domain.NodePending, "# Dep"))
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 2, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)
	// depID is left in NodePending (not completed/skipped) deliberately.

	edges := progress.NewEdgeStore(db)
	if err := edges.Insert(ctx, progress.Edge{TaskID: taskID, FromNodeID: nodeID, ToNodeID: depID, Kind: progress.EdgeDependsOn}); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	path := writeMarkdownFile(t, "section.md", validMarkdown)
	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err == nil {
		t.Fatalf("expected rejection for violated dependency policy")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %#v", err)
	}
}

func TestCompleteNode_SatisfiedDependency_Allowed(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	depID := domain.ProgressNodeID("node-dep-ok")
	nodeID := domain.ProgressNodeID("node-depends-on-dep-ok")
	insertNode(t, db, clock, newDocumentNode(taskID, depID, 1, domain.NodePending, "# Dep"))
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 2, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, depID)
	moveNodeToInProgress(t, db, clock, nodeID)

	depPath := writeMarkdownFile(t, "dep.md", "# Dep\n\nprose\n")

	if _, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         depID,
		IdempotencyKey: "dep-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("dep-artifact", depPath)},
	}); err != nil {
		t.Fatalf("complete dependency: %v", err)
	}

	edges := progress.NewEdgeStore(db)
	if err := edges.Insert(ctx, progress.Edge{TaskID: taskID, FromNodeID: nodeID, ToNodeID: depID, Kind: progress.EdgeDependsOn}); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	path := writeMarkdownFile(t, "section.md", "# X\n\n```yaml\nkey: value\n```\n")
	result, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err != nil {
		t.Fatalf("expected completion to succeed once dependency satisfied: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected completed, got %s", result.Node.Status)
	}
}

// --- Must-reject: invalid state transition (e.g. completing a pending node
// directly, never having entered in_progress) --------------------------

func TestCompleteNode_NodeNotInProgress_InvalidTransition_Rejected(t *testing.T) {
	clock := fixedClockAt(time.Now())
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	nodeID := domain.ProgressNodeID("node-still-pending")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	// Deliberately do NOT move to in_progress.

	path := writeMarkdownFile(t, "section.md", validMarkdown)
	_, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "key-1",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	})
	if err == nil {
		t.Fatalf("expected rejection for invalid state transition")
	}
	var derr *domain.Error
	ok := errors.As(err, &derr)
	if !ok || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation (invalid transition), got %#v", err)
	}
}

// --- Required test: 100 sequential nodes produce 100 verifiable checkpoints

func TestCompleteNode_100SequentialNodes_Produce100VerifiableCheckpoints(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	const n = 100
	for i := 0; i < n; i++ {
		nodeID := domain.ProgressNodeID("node-seq-" + itoaTest(i))
		insertNode(t, db, clock, newDocumentNode(taskID, nodeID, int64(i), domain.NodePending, "# X"))
		moveNodeToInProgress(t, db, clock, nodeID)

		path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
		result, err := cn.Run(ctx, progress.CompleteNodeInput{
			NodeID:         nodeID,
			IdempotencyKey: "key-seq-" + itoaTest(i),
			Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-seq-"+itoaTest(i), path)},
		})
		if err != nil {
			t.Fatalf("node %d: Run: %v", i, err)
		}
		if result.Node.Status != domain.NodeCompleted {
			t.Fatalf("node %d: expected completed, got %s", i, result.Node.Status)
		}
	}

	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("expected %d checkpoints, got %d", n, len(rows))
	}
	for _, row := range rows {
		manifest, err := statecheckpoint.Unmarshal([]byte(row.ManifestJSON))
		if err != nil {
			t.Fatalf("unmarshal manifest %s: %v", row.ID, err)
		}
		ok, err := statecheckpoint.Verify(manifest)
		if err != nil {
			t.Fatalf("verify manifest %s: %v", row.ID, err)
		}
		if !ok {
			t.Fatalf("checkpoint %s failed integrity verification", row.ID)
		}
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
