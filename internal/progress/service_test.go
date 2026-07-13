// service_test.go: proves Service (service.go) — the real, production
// app.ProgressTreeService adapter added as a Final-integration-gate
// corrective addition — correctly composes and delegates to this package's
// own already-tested pieces (NodeStore, EdgeStore, ArtifactStore,
// CompleteNode, Reconciler) without diverging from them. Per this addition's
// own brief, these tests are integration-style proofs that Service's thin
// translation layer produces the SAME result calling the underlying piece
// directly would, NOT a from-scratch re-test of CompleteNode/Reconciler's
// own correctness (already proven exhaustively across a04-a09).
package progress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/artifacts"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// serviceHarness bundles a fully-wired Service plus direct access to every
// underlying piece it composes, so tests can call a method through Service
// AND call the same underlying piece directly, then assert the two agree —
// exactly the "does Service diverge from the tested piece" proof this
// node's brief asks for.
type serviceHarness struct {
	db        *sqlite.DB
	tasks     *progress.TaskStore
	nodes     *progress.NodeStore
	edges     *progress.EdgeStore
	artifactS *progress.ArtifactStore
	cn        *progress.CompleteNode
	reconcile *progress.Reconciler
	svc       *progress.Service
}

func newServiceHarness(t *testing.T, clock domain.Clock, idPrefix string) *serviceHarness {
	t.Helper()
	db := openTestDB(t)

	evidenceDir := t.TempDir()
	stager, err := progress.NewFileStager(evidenceDir)
	if err != nil {
		t.Fatalf("NewFileStager: %v", err)
	}

	tasks := progress.NewTaskStore(db, clock)
	nodes := progress.NewNodeStore(db, clock)
	edges := progress.NewEdgeStore(db)
	artifactStore := progress.NewArtifactStore(db)
	checkpoints := statecheckpoint.NewStore(db)

	cn := &progress.CompleteNode{
		DB:          db,
		Clock:       clock,
		IDs:         &seqIDGenerator{prefix: idPrefix + "-node"},
		Nodes:       nodes,
		Edges:       edges,
		Artifacts:   artifactStore,
		Validators:  artifacts.NewRegistry(),
		Stager:      stager,
		Checkpoints: checkpoints,
		Publisher:   progress.NoopPublisher{},
	}

	reconciler := &progress.Reconciler{
		Nodes:       nodes,
		Checkpoints: checkpoints,
		EvidenceDir: evidenceDir,
	}

	svc := progress.NewService(tasks, nodes, cn, reconciler, clock, &seqIDGenerator{prefix: idPrefix + "-svc"})

	return &serviceHarness{
		db:        db,
		tasks:     tasks,
		nodes:     nodes,
		edges:     edges,
		artifactS: artifactStore,
		cn:        cn,
		reconcile: reconciler,
		svc:       svc,
	}
}

// seedWorktree inserts a minimal repositories -> worktrees chain (no task
// row — Service.CreateTask's whole job is creating that row) so
// tasks.worktree_id's FK (0004's schema) is satisfiable.
func seedWorktree(t *testing.T, db *sqlite.DB) domain.WorktreeID {
	t.Helper()
	ctx := context.Background()
	repoID := "repo-" + t.Name()
	worktreeID := "worktree-" + t.Name()
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	err := db.WithTx(ctx, func(ctx context.Context) error {
		q := sqlite.QuerierFromContext(ctx, db)
		if _, err := q.ExecContext(ctx, `
			INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)`, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?)`, worktreeID, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seedWorktree: %v", err)
	}
	return domain.WorktreeID(worktreeID)
}

// --- Compile-time contract assertion (this node's core deliverable) -------

func TestService_SatisfiesFrozenProgressTreeServiceInterface(t *testing.T) {
	var _ app.ProgressTreeService = (*progress.Service)(nil)
}

// --- CreateTask -------------------------------------------------------------

func TestService_CreateTask_PersistsRowReadableByTaskStore(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "createtask")
	ctx := context.Background()
	worktreeID := seedWorktree(t, h.db)

	task, err := h.svc.CreateTask(ctx, app.CreateTaskRequest{
		WorktreeID:    worktreeID,
		ObjectiveHash: "objective-hash-1",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == "" {
		t.Fatalf("expected a non-empty generated Task ID")
	}
	if task.Status != string(progress.TaskOpen) {
		t.Fatalf("expected status %q, got %q", progress.TaskOpen, task.Status)
	}

	// Prove Service didn't diverge from TaskStore: the row it wrote must be
	// readable directly via TaskStore.Get with the same fields.
	row, err := h.tasks.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("TaskStore.Get after Service.CreateTask: %v", err)
	}
	if row.WorktreeID != worktreeID {
		t.Fatalf("expected worktree_id %s, got %s", worktreeID, row.WorktreeID)
	}
	if row.ObjectiveHash != "objective-hash-1" {
		t.Fatalf("expected objective_hash %q, got %q", "objective-hash-1", row.ObjectiveHash)
	}
	if row.Status != progress.TaskOpen {
		t.Fatalf("expected TaskStore row status %q, got %q", progress.TaskOpen, row.Status)
	}
}

func TestService_CreateTask_RejectsEmptyWorktreeID(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "createtask-empty")
	ctx := context.Background()

	_, err := h.svc.CreateTask(ctx, app.CreateTaskRequest{ObjectiveHash: "x"})
	assertValidationError(t, err)
}

// --- UpsertPlan --------------------------------------------------------------

func TestService_UpsertPlan_ReportsVersionMatchingNodeStoreListByTask(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "upsertplan")
	ctx := context.Background()
	taskID := seedTask(t, h.db)

	for i := 0; i < 3; i++ {
		nodeID := domain.ProgressNodeID("node-plan-" + itoaTest(i))
		insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, int64(i), domain.NodePending, "# X"))
	}

	tree, err := h.svc.UpsertPlan(ctx, app.UpsertPlanRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("UpsertPlan: %v", err)
	}
	if tree.TaskID != taskID {
		t.Fatalf("expected TaskID %s, got %s", taskID, tree.TaskID)
	}

	// Prove Service didn't diverge from NodeStore: the version reported must
	// equal len(NodeStore.ListByTask) at the moment of the call.
	nodes, err := h.nodes.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("NodeStore.ListByTask: %v", err)
	}
	if tree.Version != int64(len(nodes)) {
		t.Fatalf("expected version %d (len of ListByTask), got %d", len(nodes), tree.Version)
	}

	// The task row's own active_node_id/progress_tree_version must have been
	// kept in sync (0004_tasks.sql's documented responsibility).
	row, err := h.tasks.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("TaskStore.Get: %v", err)
	}
	if row.ProgressTreeVersion != tree.Version {
		t.Fatalf("expected task row progress_tree_version %d, got %d", tree.Version, row.ProgressTreeVersion)
	}
}

func TestService_UpsertPlan_RejectsUnknownTask(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "upsertplan-unknown")
	ctx := context.Background()

	_, err := h.svc.UpsertPlan(ctx, app.UpsertPlanRequest{TaskID: "does-not-exist"})
	if !isNotFoundTest(err) {
		t.Fatalf("expected a not-found error for an unknown task, got %v", err)
	}
}

// --- StartNode ---------------------------------------------------------------

func TestService_StartNode_TransitionsToInProgressMatchingNodeStore(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "startnode")
	ctx := context.Background()
	taskID := seedTask(t, h.db)
	nodeID := domain.ProgressNodeID("node-start-1")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	if err := h.nodes.TransitionStatus(ctx, nodeID, domain.NodePending, domain.NodeReady, 1); err != nil {
		t.Fatalf("seed transition to ready: %v", err)
	}

	got, err := h.svc.StartNode(ctx, app.StartNodeRequest{NodeID: nodeID})
	if err != nil {
		t.Fatalf("StartNode: %v", err)
	}
	if got.Status != domain.NodeInProgress {
		t.Fatalf("expected status %s, got %s", domain.NodeInProgress, got.Status)
	}

	// Prove Service didn't diverge from NodeStore: a direct Get must agree.
	row, err := h.nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("NodeStore.Get: %v", err)
	}
	if row.Status != domain.NodeInProgress {
		t.Fatalf("expected NodeStore row status %s, got %s", domain.NodeInProgress, row.Status)
	}
	if row.StartedAt == nil {
		t.Fatalf("expected started_at to be set")
	}
	if got.ID != row.ID || got.TaskID != row.TaskID || got.Kind != row.Kind {
		t.Fatalf("Service.StartNode DTO does not match underlying NodeStore row: dto=%+v row=%+v", got, row)
	}
}

func TestService_StartNode_InvalidTransitionSurfacesStateMachineError(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "startnode-invalid")
	ctx := context.Background()
	taskID := seedTask(t, h.db)
	nodeID := domain.ProgressNodeID("node-start-invalid")
	// A completed node has no outbound transition to in_progress
	// (statemachine.go's transitions table): insert directly as completed.
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodeCompleted, "# X"))

	_, err := h.svc.StartNode(ctx, app.StartNodeRequest{NodeID: nodeID})
	if err == nil {
		t.Fatalf("expected an error transitioning a completed node to in_progress")
	}
	var transErr *progress.TransitionError
	if !errors.As(err, &transErr) {
		t.Fatalf("expected a *progress.TransitionError (surfaced from ValidateTransition via NodeStore), got %T: %v", err, err)
	}
}

// --- CompleteNode (the interface method) -------------------------------------

// TestService_CompleteNode_MatchesDirectCompleteNodeRun is this node's
// central proof: Service.CompleteNode, given the SAME inputs, must produce
// a result equivalent to calling CompleteNode.Run directly (same node
// status, same checkpoint ID/manifest content) — proving Service is a pure
// translation layer, not a divergent reimplementation.
func TestService_CompleteNode_MatchesDirectCompleteNodeRun(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "completenode")
	ctx := context.Background()
	taskID := seedTask(t, h.db)

	// Node A: completed directly via CompleteNode.Run (the reference path).
	nodeA := domain.ProgressNodeID("node-complete-direct")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeA, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeA)
	pathA := writeMarkdownFile(t, "section-a.md", uniqueMarkdownTest("direct"))
	directResult, err := h.cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeA,
		IdempotencyKey: "key-direct",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-direct", pathA)},
	})
	if err != nil {
		t.Fatalf("direct CompleteNode.Run: %v", err)
	}

	// Node B: completed via Service.CompleteNode (the adapter under test),
	// with equivalent inputs (same shape, different node/evidence so they
	// don't collide).
	nodeB := domain.ProgressNodeID("node-complete-via-service")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeB, 2, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeB)
	pathB := writeMarkdownFile(t, "section-b.md", uniqueMarkdownTest("via-service"))
	gotNode, gotCheckpoint, err := h.svc.CompleteNode(ctx, app.CompleteNodeRequest{
		NodeID:         nodeB,
		IdempotencyKey: "key-via-service",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-via-service", pathB)},
	})
	if err != nil {
		t.Fatalf("Service.CompleteNode: %v", err)
	}

	if gotNode.Status != directResult.Node.Status {
		t.Fatalf("Service.CompleteNode status %s does not match direct CompleteNode.Run status %s", gotNode.Status, directResult.Node.Status)
	}
	if gotNode.Status != domain.NodeCompleted {
		t.Fatalf("expected completed status, got %s", gotNode.Status)
	}
	if gotNode.ID != nodeB {
		t.Fatalf("expected returned node ID %s, got %s", nodeB, gotNode.ID)
	}

	// The direct path's own result must independently pass the same shape
	// checks below, establishing what "correct" looks like before comparing
	// Service's translation against it.
	if directResult.Checkpoint.TaskID != taskID || directResult.Checkpoint.IntegritySHA256 == "" {
		t.Fatalf("sanity check on direct CompleteNode.Run result failed: %+v", directResult.Checkpoint)
	}

	// The returned domain.StateCheckpoint must describe exactly the durable
	// checkpoint row Service.CompleteNode's own call to CompleteOp.Run
	// produced — proving stateCheckpointFromResult's translation doesn't
	// lose or alter fields relative to CompleteNodeResult.Checkpoint/Manifest.
	viaServiceRow, err := h.cn.Checkpoints.Get(ctx, gotCheckpoint.ID)
	if err != nil {
		t.Fatalf("load service-completion checkpoint row: %v", err)
	}
	if viaServiceRow.TaskID != taskID {
		t.Fatalf("expected checkpoint task_id %s, got %s", taskID, viaServiceRow.TaskID)
	}
	if gotCheckpoint.ID != viaServiceRow.ID {
		t.Fatalf("Service-returned checkpoint ID %s does not match the durable row it wrote %s", gotCheckpoint.ID, viaServiceRow.ID)
	}
	if gotCheckpoint.IntegritySHA256 != viaServiceRow.IntegritySHA256 {
		t.Fatalf("Service-returned checkpoint digest %s does not match durable row digest %s", gotCheckpoint.IntegritySHA256, viaServiceRow.IntegritySHA256)
	}
	if gotCheckpoint.TaskID != taskID {
		t.Fatalf("expected domain.StateCheckpoint.TaskID %s, got %s", taskID, gotCheckpoint.TaskID)
	}
	if len(gotCheckpoint.CompletedNodeIDs) != 1 || gotCheckpoint.CompletedNodeIDs[0] != nodeB {
		t.Fatalf("expected CompletedNodeIDs=[%s], got %v", nodeB, gotCheckpoint.CompletedNodeIDs)
	}

	// The durable row's own manifest must independently pass
	// statecheckpoint.Verify — proving Service.CompleteNode's translation
	// didn't merely echo back plausible-looking fields while leaving the
	// underlying manifest that CompleteOp.Run sealed uninspected.
	manifest, err := statecheckpoint.Unmarshal([]byte(viaServiceRow.ManifestJSON))
	if err != nil {
		t.Fatalf("unmarshal service-completion manifest: %v", err)
	}
	ok, err := statecheckpoint.Verify(manifest)
	if err != nil {
		t.Fatalf("statecheckpoint.Verify: %v", err)
	}
	if !ok {
		t.Fatalf("expected the Service-completed checkpoint's manifest to pass integrity verification")
	}
}

// TestService_CompleteNode_IdempotentReplayMatchesDirectRun proves the
// idempotency ledger's replay path (already exhaustively tested against
// CompleteNode.Run directly in complete_node_idempotency_test.go) still
// produces the same replayed result when reached through Service.
func TestService_CompleteNode_IdempotentReplayMatchesDirectRun(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "completenode-replay")
	ctx := context.Background()
	taskID := seedTask(t, h.db)
	nodeID := domain.ProgressNodeID("node-replay")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeID)
	path := writeMarkdownFile(t, "section-replay.md", uniqueMarkdownTest("replay"))

	req := app.CompleteNodeRequest{
		NodeID:         nodeID,
		IdempotencyKey: "key-replay",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-replay", path)},
	}

	first, firstCheckpoint, err := h.svc.CompleteNode(ctx, req)
	if err != nil {
		t.Fatalf("first Service.CompleteNode: %v", err)
	}
	second, secondCheckpoint, err := h.svc.CompleteNode(ctx, req)
	if err != nil {
		t.Fatalf("replayed Service.CompleteNode: %v", err)
	}

	if first.ID != second.ID || first.Status != second.Status {
		t.Fatalf("expected replay to return the same node result: first=%+v second=%+v", first, second)
	}
	if firstCheckpoint.ID != secondCheckpoint.ID {
		t.Fatalf("expected replay to return the SAME checkpoint ID: first=%s second=%s", firstCheckpoint.ID, secondCheckpoint.ID)
	}

	// Exactly one checkpoint row must exist for this node's completion — the
	// replay must not have produced a second one.
	rows, err := h.cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint row after a replayed completion, got %d", len(rows))
	}
}

func TestService_CompleteNode_ConflictingPayloadRejected(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 14, 30, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "completenode-conflict")
	ctx := context.Background()
	taskID := seedTask(t, h.db)
	nodeID := domain.ProgressNodeID("node-conflict")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeID)
	path1 := writeMarkdownFile(t, "section-conflict-1.md", uniqueMarkdownTest("conflict-1"))
	path2 := writeMarkdownFile(t, "section-conflict-2.md", uniqueMarkdownTest("conflict-2"))

	if _, _, err := h.svc.CompleteNode(ctx, app.CompleteNodeRequest{
		NodeID:         nodeID,
		IdempotencyKey: "key-conflict",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-conflict-1", path1)},
	}); err != nil {
		t.Fatalf("first completion: %v", err)
	}

	_, _, err := h.svc.CompleteNode(ctx, app.CompleteNodeRequest{
		NodeID:         nodeID,
		IdempotencyKey: "key-conflict", // same key
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-conflict-2", path2)},
	})
	if err == nil {
		t.Fatalf("expected a conflict error for the same idempotency key with different evidence")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected a domain.Error{Code: ErrCodeConflict}, got %v", err)
	}
}

// --- FailNode ------------------------------------------------------------

func TestService_FailNode_TransitionsToFailedMatchingNodeStore(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "failnode")
	ctx := context.Background()
	taskID := seedTask(t, h.db)
	nodeID := domain.ProgressNodeID("node-fail-1")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeID)

	got, err := h.svc.FailNode(ctx, app.FailNodeRequest{NodeID: nodeID, FailureClass: domain.FailureBuild})
	if err != nil {
		t.Fatalf("FailNode: %v", err)
	}
	if got.Status != domain.NodeFailed {
		t.Fatalf("expected status %s, got %s", domain.NodeFailed, got.Status)
	}

	row, err := h.nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("NodeStore.Get: %v", err)
	}
	if row.Status != domain.NodeFailed {
		t.Fatalf("expected NodeStore row status %s, got %s", domain.NodeFailed, row.Status)
	}
}

func TestService_FailNode_RejectsEmptyFailureClass(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "failnode-empty")
	ctx := context.Background()
	taskID := seedTask(t, h.db)
	nodeID := domain.ProgressNodeID("node-fail-empty")
	insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeID)

	_, err := h.svc.FailNode(ctx, app.FailNodeRequest{NodeID: nodeID})
	assertValidationError(t, err)
}

// --- Snapshot ------------------------------------------------------------

func TestService_Snapshot_MatchesNodeStoreListByTask(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "snapshot")
	ctx := context.Background()
	taskID := seedTask(t, h.db)

	var wantIDs []domain.ProgressNodeID
	for i := 0; i < 5; i++ {
		nodeID := domain.ProgressNodeID("node-snap-" + itoaTest(i))
		insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, int64(i), domain.NodePending, "# X"))
		wantIDs = append(wantIDs, nodeID)
	}

	snap, err := h.svc.Snapshot(ctx, taskID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.TaskID != taskID {
		t.Fatalf("expected TaskID %s, got %s", taskID, snap.TaskID)
	}

	directNodes, err := h.nodes.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("NodeStore.ListByTask: %v", err)
	}
	if len(snap.Nodes) != len(directNodes) {
		t.Fatalf("Snapshot returned %d nodes, NodeStore.ListByTask returned %d", len(snap.Nodes), len(directNodes))
	}
	for i, n := range snap.Nodes {
		if n.ID != directNodes[i].ID || n.Status != directNodes[i].Status || n.Kind != directNodes[i].Kind {
			t.Fatalf("Snapshot node %d does not match NodeStore row: dto=%+v row=%+v", i, n, directNodes[i])
		}
	}
	if len(snap.Nodes) != len(wantIDs) {
		t.Fatalf("expected %d nodes, got %d", len(wantIDs), len(snap.Nodes))
	}
}

func TestService_Snapshot_RejectsEmptyTaskID(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "snapshot-empty")
	ctx := context.Background()

	_, err := h.svc.Snapshot(ctx, "")
	assertValidationError(t, err)
}

// --- Reconcile -----------------------------------------------------------

// TestService_Reconcile_MatchesDirectReconcilerOutcome proves
// Service.Reconcile's translation agrees with calling progress.Reconciler
// directly: same orphan/violation-free outcome after a clean completion,
// and it must report every node in the task as reconciled.
func TestService_Reconcile_MatchesDirectReconcilerOutcome(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 17, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "reconcile")
	ctx := context.Background()
	taskID := seedTask(t, h.db)

	var nodeIDs []domain.ProgressNodeID
	for i := 0; i < 3; i++ {
		suffix := "reconcile-" + itoaTest(i)
		nodeID := domain.ProgressNodeID("node-" + suffix)
		insertNode(t, h.db, clock, newDocumentNode(taskID, nodeID, int64(i), domain.NodePending, "# X"))
		moveNodeToInProgress(t, h.db, clock, nodeID)
		path := writeMarkdownFile(t, "section-"+suffix+".md", uniqueMarkdownTest(suffix))
		if _, err := h.cn.Run(ctx, progress.CompleteNodeInput{
			NodeID:         nodeID,
			IdempotencyKey: "key-" + suffix,
			Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-"+suffix, path)},
		}); err != nil {
			t.Fatalf("complete node %d: %v", i, err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}

	result, err := h.svc.Reconcile(ctx, app.ReconcileProgressRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Service.Reconcile: %v", err)
	}
	if result.TaskID != taskID {
		t.Fatalf("expected TaskID %s, got %s", taskID, result.TaskID)
	}
	if len(result.ReconciledNodes) != len(nodeIDs) {
		t.Fatalf("expected %d reconciled nodes, got %d", len(nodeIDs), len(result.ReconciledNodes))
	}
	reconciledSet := make(map[domain.ProgressNodeID]bool, len(result.ReconciledNodes))
	for _, id := range result.ReconciledNodes {
		reconciledSet[id] = true
	}
	for _, id := range nodeIDs {
		if !reconciledSet[id] {
			t.Fatalf("expected node %s to be present in ReconciledNodes, got %v", id, result.ReconciledNodes)
		}
	}

	// Cross-check against calling the Reconciler directly — Service must not
	// have swallowed or altered its verdict (clean stack -> zero problems).
	directReport, err := h.reconcile.Reconcile(ctx, taskID)
	if err != nil {
		t.Fatalf("direct Reconciler.Reconcile: %v", err)
	}
	if len(directReport.OrphanedStagedArtifacts) != 0 {
		t.Fatalf("expected zero orphaned artifacts from the direct Reconciler, got %v", directReport.OrphanedStagedArtifacts)
	}
	if len(directReport.IntegrityViolations) != 0 {
		t.Fatalf("expected zero integrity violations from the direct Reconciler, got %v", directReport.IntegrityViolations)
	}
}

func TestService_Reconcile_RejectsEmptyTaskID(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 17, 0, 0, 0, time.UTC))
	h := newServiceHarness(t, clock, "reconcile-empty")
	ctx := context.Background()

	_, err := h.svc.Reconcile(ctx, app.ReconcileProgressRequest{})
	assertValidationError(t, err)
}

// --- shared small helpers (this file's own, distinct names from sibling
// test files' helpers to avoid redeclaration within package progress_test) -

func assertValidationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a validation error, got nil")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected a domain.Error{Code: ErrCodeValidation}, got %v", err)
	}
}

func uniqueMarkdownTest(suffix string) string {
	return "# X\n\nprose for " + suffix + "\n"
}

// itoaTest is already defined in complete_node_test.go (same package,
// progress_test) — reused here rather than redeclared.
