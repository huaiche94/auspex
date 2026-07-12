// complete_node_crash_test.go: crash-injection tests for the CompleteNode
// atomic protocol (agents/checkpoint.md Part A required test "crash
// injection at each completion phase and reconciliation").
//
// Each test below drives CompleteNode.Run with HaltAfter set to a specific
// Phase, simulating a process crash at exactly that point (Run returns a
// *progress.HaltError instead of completing normally, and — critically —
// for the phases inside the DB transaction, the transaction itself never
// commits, so SQLite's own atomicity guarantees nothing from that attempt
// is durable). The test then asserts:
//
//  1. the system's observable state after the "crash" is exactly what ADD
//     §18.7's staged protocol promises for that phase (never a corrupted
//     halfway state, never a node that looks completed but isn't backed by
//     verified evidence — Constitution §6.5);
//  2. reconciliation (or, for the phases before the transaction, a bare
//     retry) recovers to a fully consistent state, without losing or
//     double-applying work.
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

// runToHalt runs cn with HaltAfter set to phase and asserts the result is
// exactly a *progress.HaltError for that phase (never a different error,
// never a nil error — a silent "crash" that actually succeeded would
// defeat the point of the test).
func runToHalt(t *testing.T, cn *progress.CompleteNode, phase progress.Phase, in progress.CompleteNodeInput) {
	t.Helper()
	cn.HaltAfter = phase
	defer func() { cn.HaltAfter = "" }()

	_, err := cn.Run(context.Background(), in)
	if err == nil {
		t.Fatalf("expected a halt at phase %q, got nil error (protocol completed instead of crashing)", phase)
	}
	var halt *progress.HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected *progress.HaltError at phase %q, got %#v", phase, err)
	}
	if halt.Phase != phase {
		t.Fatalf("expected halt at phase %q, got halt at %q", phase, halt.Phase)
	}
}

// --- Crash at PhaseStageArtifacts: after staging, before verification ----

func TestCompleteNode_CrashInjection_AfterStageArtifacts(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-crash-stage")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "crash-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}
	runToHalt(t, cn, progress.PhaseStageArtifacts, req)

	// Node must be observably untouched: still in_progress, never
	// checkpointing/completed.
	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node after crash: %v", err)
	}
	if node.Status != domain.NodeInProgress {
		t.Fatalf("expected node to remain in_progress after crash at stage phase, got %s", node.Status)
	}
	// No checkpoint must exist.
	if _, err := cn.Checkpoints.LoadLatest(ctx, taskID); !errors.Is(err, statecheckpoint.ErrNotFound) {
		t.Fatalf("expected no checkpoint to exist yet, got err=%v", err)
	}

	// Retry (no crash this time) must succeed cleanly - staging is
	// idempotent (content-addressed dest path), so the orphaned staged
	// copy from the halted attempt does not block or corrupt the retry.
	result, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("retry after crash: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected retry to complete the node, got %s", result.Node.Status)
	}

	// Reconciliation must find the orphaned staged artifact (from the
	// halted attempt) and NOT report it as an integrity violation - it is
	// a harmless orphan, referenced by the SAME content the successful
	// retry ultimately used and committed.
	rec := &progress.Reconciler{Nodes: cn.Nodes, Checkpoints: cn.Checkpoints, EvidenceDir: cn.Stager.(*progress.FileStager).EvidenceDir}
	report, err := rec.Reconcile(ctx, taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(report.IntegrityViolations) != 0 {
		t.Fatalf("expected no integrity violations, got %v", report.IntegrityViolations)
	}
}

// --- Crash at PhaseVerifyArtifacts: after verification, before the tx ----

func TestCompleteNode_CrashInjection_AfterVerifyArtifacts(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-crash-verify")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "crash-key-verify",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}
	runToHalt(t, cn, progress.PhaseVerifyArtifacts, req)

	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node after crash: %v", err)
	}
	if node.Status != domain.NodeInProgress {
		t.Fatalf("expected node to remain in_progress after crash at verify phase, got %s", node.Status)
	}

	result, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("retry after crash: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected retry to complete the node, got %s", result.Node.Status)
	}
}

// --- Crash at PhaseUpdateNode: mid-transaction, after node->checkpointing
// and the node->completed transition and timestamp write, but the whole
// transaction still rolls back since the callback returned an error -----

func TestCompleteNode_CrashInjection_AfterUpdateNode_RollsBackWholeTransaction(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-crash-update")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "crash-key-update",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}
	runToHalt(t, cn, progress.PhaseUpdateNode, req)

	// The whole transaction (node status change AND the timestamp write)
	// must have rolled back - the node must be observed exactly as it was
	// before this attempt (in_progress), never left "completed" without a
	// checkpoint, and never left stuck in the intermediate "checkpointing"
	// status either.
	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node after crash: %v", err)
	}
	if node.Status != domain.NodeInProgress {
		t.Fatalf("expected transaction rollback to leave node in_progress (pre-transaction state), got %s", node.Status)
	}
	if _, err := cn.Checkpoints.LoadLatest(ctx, taskID); !errors.Is(err, statecheckpoint.ErrNotFound) {
		t.Fatalf("expected no checkpoint to have been committed, got err=%v", err)
	}

	result, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("retry after crash: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected retry to complete the node, got %s", result.Node.Status)
	}
}

// --- Crash at PhaseCreateCheckpoint: mid-transaction, after the checkpoint
// row insert but before the ledger row - still rolls back entirely -----

func TestCompleteNode_CrashInjection_AfterCreateCheckpoint_RollsBackWholeTransaction(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-crash-checkpoint")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "crash-key-checkpoint",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}
	runToHalt(t, cn, progress.PhaseCreateCheckpoint, req)

	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node after crash: %v", err)
	}
	if node.Status != domain.NodeInProgress {
		t.Fatalf("expected transaction rollback to leave node in_progress, got %s", node.Status)
	}
	// The checkpoint row itself must NOT be durable either - this is the
	// exact "checkpoint manifest referencing uncommitted rows" hazard the
	// single-transaction design exists to prevent: if the checkpoint row
	// alone had committed while the node update rolled back, the manifest
	// would reference a "completed" node that the DB does not actually
	// show as completed. Asserting no checkpoint exists proves that
	// hazard cannot occur - the two either commit together or not at all.
	if _, err := cn.Checkpoints.LoadLatest(ctx, taskID); !errors.Is(err, statecheckpoint.ErrNotFound) {
		t.Fatalf("expected no checkpoint to have been committed (all-or-nothing tx), got err=%v", err)
	}

	result, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("retry after crash: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected retry to complete the node, got %s", result.Node.Status)
	}
	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint after the successful retry, got %d", len(rows))
	}
}

// --- Crash at PhaseCommit: the transaction ALREADY committed successfully;
// this simulates a crash between a successful commit and event publish --

func TestCompleteNode_CrashInjection_AfterCommit_StateIsDurable(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-crash-commit")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "crash-key-commit",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-1", path)},
	}
	runToHalt(t, cn, progress.PhaseCommit, req)

	// Unlike every earlier phase, the transaction DID commit here - the
	// node must be observed as fully completed, with a durable, verifiable
	// checkpoint, even though Run itself returned a *HaltError (simulating
	// the crash happening strictly AFTER durability was achieved, before
	// the process got around to publishing events).
	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node after crash: %v", err)
	}
	if node.Status != domain.NodeCompleted {
		t.Fatalf("expected node to be durably completed despite the post-commit crash, got %s", node.Status)
	}
	row, err := cn.Checkpoints.LoadLatest(ctx, taskID)
	if err != nil {
		t.Fatalf("expected a durable checkpoint after post-commit crash: %v", err)
	}
	manifest, err := statecheckpoint.Unmarshal([]byte(row.ManifestJSON))
	if err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	ok, err := statecheckpoint.Verify(manifest)
	if err != nil || !ok {
		t.Fatalf("expected the durable checkpoint to verify: ok=%v err=%v", ok, err)
	}

	// A subsequent call with the SAME key must now correctly replay
	// (proving the idempotency ledger row itself was part of the same
	// committed transaction, not lost by the post-commit crash).
	cn.HaltAfter = ""
	replay, err := cn.Run(ctx, req)
	if err != nil {
		t.Fatalf("replay after post-commit crash: %v", err)
	}
	if !replay.Replayed {
		t.Fatalf("expected replay to be recognized as such")
	}
}

// --- Reconciliation integration: startup after a crash must never lose or
// double-apply work across a whole sequence of nodes with random halts --

func TestCompleteNode_ReconciliationAfterCrash_NeverLosesOrDoublesWork(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	phases := []progress.Phase{
		progress.PhaseStageArtifacts,
		progress.PhaseVerifyArtifacts,
		progress.PhaseUpdateNode,
		progress.PhaseCreateCheckpoint,
	}

	for i, phase := range phases {
		nodeID := domain.ProgressNodeID("node-recon-" + itoaTest(i))
		insertNode(t, db, clock, newDocumentNode(taskID, nodeID, int64(i), domain.NodePending, "# X"))
		moveNodeToInProgress(t, db, clock, nodeID)

		path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
		req := progress.CompleteNodeInput{
			NodeID:         nodeID,
			IdempotencyKey: "recon-key-" + itoaTest(i),
			Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-recon-"+itoaTest(i), path)},
		}

		// Simulate the crash.
		runToHalt(t, cn, phase, req)

		// "Restart": run again with no halt configured.
		result, err := cn.Run(ctx, req)
		if err != nil {
			t.Fatalf("phase %s: recovery run failed: %v", phase, err)
		}
		if result.Node.Status != domain.NodeCompleted {
			t.Fatalf("phase %s: expected node completed after recovery, got %s", phase, result.Node.Status)
		}
	}

	// Exactly one checkpoint per node - no duplicate/double-applied work
	// from any of the simulated crashes.
	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != len(phases) {
		t.Fatalf("expected exactly %d checkpoints (one per node), got %d", len(phases), len(rows))
	}

	rec := &progress.Reconciler{Nodes: cn.Nodes, Checkpoints: cn.Checkpoints, EvidenceDir: cn.Stager.(*progress.FileStager).EvidenceDir}
	report, err := rec.Reconcile(ctx, taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(report.IntegrityViolations) != 0 {
		t.Fatalf("expected no integrity violations after full recovery, got %v", report.IntegrityViolations)
	}
}
