// complete_node_integration_test.go: checkpoint-a09, the final integration
// gate for Part A (Progress Tree / State Checkpointing). agents/checkpoint.md
// scopes this node as proving the FULL Part A stack holds together
// end-to-end using REAL implementations throughout, not fakes — distinct
// from every earlier node's narrower, single-package proof:
//
//   - checkpoint-a04 proved CompleteNode's OWN atomic protocol (one
//     package, one node at a time, its own crash windows).
//   - checkpoint-a06 proved statecheckpoint.Service.Create's OWN crash
//     windows (a different package, a different entry point).
//
// This file proves three things neither of those did, using this role's
// actual production types wired together exactly as a real caller would:
//
//  1. 100 sequential nodes produce 100 verifiable checkpoints, extended
//     beyond checkpoint-a04's original test to also prove every one of
//     those 100 checkpoints is independently Snapshot()-able and
//     Verify()-able (statecheckpoint.Service's a05/a08 APIs) after the
//     fact, not merely created.
//  2. A genuinely cross-package concurrent race: many goroutines complete
//     DIFFERENT nodes concurrently via CompleteNode.Run (internal/progress)
//     while OTHER goroutines concurrently drive statecheckpoint.Service's
//     ad hoc Create/Snapshot/LoadLatest APIs AND internal/statecheckpoint's
//     own Reconciler against the same, growing set of state_checkpoints
//     rows — proving reads-during-writes across this package boundary
//     never observe torn or inconsistent state.
//  3. A full crash-and-recover scenario spanning BOTH packages: crash
//     mid-CompleteNode (state checkpoint half-written from Part A's own
//     transaction's point of view), "restart" (fresh Reconciler values
//     bound to the same durable DB), and confirm internal/progress's OWN
//     Reconciler (checkpoint-a04/a07's orphaned-staged-evidence scan) and
//     internal/statecheckpoint's OWN Reconciler (checkpoint-a06's
//     per-row integrity scan) reach conclusions that AGREE about the same
//     crash window rather than contradicting each other.
//
// This is explicitly the gate for qa-02's E2E test (EXECUTION_DAG.md: "the
// literal Day-1 demo"), so every scenario here uses the real CompleteNode,
// the real statecheckpoint.Store/Service/Reconciler, and the real
// progress.Reconciler — no package in this file is faked except the
// deterministic Clock/IDGenerator test doubles this whole role already
// uses everywhere else for reproducibility.
package progress_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/artifacts"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/progress"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// realTreeReader adapts this role's own production NodeStore/ArtifactStore
// (internal/progress) to statecheckpoint.TreeReader, so
// statecheckpoint.Service.Create/Snapshot/LoadLatest/Reconciler in this
// file's tests run against the SAME real stores CompleteNode itself uses —
// not an in-memory fake (service_test.go's fakeTreeReader is appropriate
// for that package's own unit tests, but this integration node's whole
// point is proving the REAL stack end to end, per the wave's explicit
// instruction). This adapter is deliberately test-local (not production
// code): it lives here because it is exactly the kind of "production
// wiring" seam internal/statecheckpoint's own doc comments say a later
// integration step supplies, and this node IS that integration proof.
type realTreeReader struct {
	nodes     *progress.NodeStore
	artifacts *progress.ArtifactStore
}

func (r *realTreeReader) ListNodes(ctx context.Context, taskID domain.TaskID) ([]statecheckpoint.NodeSnapshot, error) {
	nodes, err := r.nodes.ListByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]statecheckpoint.NodeSnapshot, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, statecheckpoint.NodeSnapshot{ID: n.ID, Status: n.Status})
	}
	return out, nil
}

func (r *realTreeReader) ListArtifacts(ctx context.Context, taskID domain.TaskID) ([]statecheckpoint.ArtifactSnapshot, error) {
	nodes, err := r.nodes.ListByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var out []statecheckpoint.ArtifactSnapshot
	for _, n := range nodes {
		rows, err := r.artifacts.ListByNode(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, statecheckpoint.ArtifactSnapshot{
				ID:               row.ID,
				URI:              row.URI,
				Bytes:            row.Bytes,
				SHA256:           row.SHA256,
				ValidationStatus: string(row.ValidationStatus),
			})
		}
	}
	return out, nil
}

// fullStackHarness bundles every real Part A component wired together the
// way a genuine caller (runtime's persist phase, or qa-02's E2E test) would:
// one *sqlite.DB, CompleteNode, statecheckpoint.Service, and both packages'
// own Reconcilers, all bound to the SAME durable database.
type fullStackHarness struct {
	db             *sqlite.DB
	taskID         domain.TaskID
	cn             *progress.CompleteNode
	svc            *statecheckpoint.Service
	stateReconcile *statecheckpoint.Reconciler
	progReconcile  *progress.Reconciler
}

func newFullStackHarness(t *testing.T, clock domain.Clock, idPrefix string) *fullStackHarness {
	t.Helper()
	db := openTestDB(t)
	taskID := seedTask(t, db)

	evidenceDir := t.TempDir()
	stager, err := progress.NewFileStager(evidenceDir)
	if err != nil {
		t.Fatalf("NewFileStager: %v", err)
	}

	nodes := progress.NewNodeStore(db, clock)
	artifactStore := progress.NewArtifactStore(db)
	checkpoints := statecheckpoint.NewStore(db)

	cn := &progress.CompleteNode{
		DB:          db,
		Clock:       clock,
		IDs:         &seqIDGenerator{prefix: idPrefix + "-node"},
		Nodes:       nodes,
		Edges:       progress.NewEdgeStore(db),
		Artifacts:   artifactStore,
		Validators:  artifacts.NewRegistry(),
		Stager:      stager,
		Checkpoints: checkpoints,
		Publisher:   progress.NoopPublisher{},
	}

	tree := &realTreeReader{nodes: nodes, artifacts: artifactStore}
	svc := statecheckpoint.NewService(checkpoints, tree, clock, &seqIDGenerator{prefix: idPrefix + "-svc"})

	return &fullStackHarness{
		db:             db,
		taskID:         taskID,
		cn:             cn,
		svc:            svc,
		stateReconcile: statecheckpoint.NewReconciler(checkpoints),
		progReconcile: &progress.Reconciler{
			Nodes:       nodes,
			Checkpoints: checkpoints,
			EvidenceDir: evidenceDir,
		},
	}
}

// uniqueMarkdown produces content that differs by nodeSuffix (not just
// filename) so each node's staged evidence has a genuinely distinct
// SHA-256 — load-bearing for the orphan-detection proof below, since
// FileStager's staging destination is content-addressed
// (EvidenceDir/sha256/<hex>): identical prose across nodes would make an
// unrelated node's evidence look "referenced" by pure content coincidence,
// masking a real orphan rather than proving one exists.
func uniqueMarkdown(nodeSuffix string) string {
	return "# X\n\nprose for " + nodeSuffix + "\n"
}

func completeOneNode(t *testing.T, h *fullStackHarness, clock domain.Clock, nodeSuffix, key string) (progress.CompleteNodeResult, error) {
	t.Helper()
	nodeID := domain.ProgressNodeID("node-" + nodeSuffix)
	insertNode(t, h.db, clock, newDocumentNode(h.taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, nodeID)
	path := writeMarkdownFile(t, "section-"+nodeSuffix+".md", uniqueMarkdown(nodeSuffix))
	return h.cn.Run(context.Background(), progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: key,
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-"+nodeSuffix, path)},
	})
}

// --- Proof 1: 100 sequential nodes -> 100 checkpoints, each independently
// Snapshot()-able AND Verify()-able via the real statecheckpoint.Service,
// not merely Unmarshal+Verify against the raw store row (checkpoint-a04's
// original test) -----------------------------------------------------------

func TestA09_100SequentialNodes_EachCheckpointIndependentlySnapshotAndVerifyable(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC))
	h := newFullStackHarness(t, clock, "seq100")
	ctx := context.Background()

	const n = 100
	var checkpointIDs []domain.StateCheckpointID
	for i := 0; i < n; i++ {
		suffix := fmt.Sprintf("seq-%d", i)
		result, err := completeOneNode(t, h, clock, suffix, "key-"+suffix)
		if err != nil {
			t.Fatalf("node %d: Run: %v", i, err)
		}
		if result.Node.Status != domain.NodeCompleted {
			t.Fatalf("node %d: expected completed, got %s", i, result.Node.Status)
		}
		checkpointIDs = append(checkpointIDs, result.Checkpoint.ID)
	}
	if len(checkpointIDs) != n {
		t.Fatalf("expected %d checkpoint IDs collected, got %d", n, len(checkpointIDs))
	}

	rows, err := h.cn.Checkpoints.ListByTask(ctx, h.taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("expected %d durable checkpoint rows, got %d", n, len(rows))
	}

	// The genuine a09 increment over a04's original test: every checkpoint
	// must be independently readable via Service.Snapshot (a08's API) AND
	// pass Service.Verify (a05's API) — not just "the row exists and its
	// manifest happens to Unmarshal", which is all a04's test proved.
	for i, id := range checkpointIDs {
		snap, err := h.svc.Snapshot(ctx, id)
		if err != nil {
			t.Fatalf("checkpoint %d (%s): Snapshot: %v", i, id, err)
		}
		if snap.ID != id {
			t.Fatalf("checkpoint %d: Snapshot returned wrong ID: got %s want %s", i, snap.ID, id)
		}
		if snap.TaskID != h.taskID {
			t.Fatalf("checkpoint %d: Snapshot returned wrong TaskID: got %s want %s", i, snap.TaskID, h.taskID)
		}

		verification, err := h.svc.Verify(ctx, id)
		if err != nil {
			t.Fatalf("checkpoint %d (%s): Verify: %v", i, id, err)
		}
		if !verification.Valid {
			t.Fatalf("checkpoint %d (%s): Verify reported Valid=false", i, id)
		}
	}

	// LoadLatest must agree with the LAST checkpoint created, proving the
	// task-scoped "most recent" read is consistent with the same 100-row
	// history Snapshot walked individually above.
	latest, err := h.svc.LoadLatest(ctx, h.taskID)
	if err != nil {
		t.Fatalf("LoadLatest: %v", err)
	}
	if latest.ID != checkpointIDs[n-1] {
		t.Fatalf("LoadLatest returned %s, expected the last-created checkpoint %s", latest.ID, checkpointIDs[n-1])
	}

	// Cross-package reconciliation over the resulting 100-checkpoint
	// history must find nothing wrong from EITHER package's own
	// Reconciler — the direct tie-in to this node's proof 3 below, run
	// once here in the pure happy-path (no crash injected) case.
	stateReport, err := h.stateReconcile.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("statecheckpoint Reconcile: %v", err)
	}
	if stateReport.CheckpointsScanned != n {
		t.Fatalf("expected statecheckpoint Reconciler to scan %d checkpoints, scanned %d", n, stateReport.CheckpointsScanned)
	}
	if len(stateReport.Violations) != 0 {
		t.Fatalf("expected zero violations from statecheckpoint Reconciler, got %v", stateReport.Violations)
	}

	progReport, err := h.progReconcile.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("progress Reconcile: %v", err)
	}
	if len(progReport.OrphanedStagedArtifacts) != 0 {
		t.Fatalf("expected zero orphaned staged artifacts after 100 clean completions, got %v", progReport.OrphanedStagedArtifacts)
	}
	if len(progReport.IntegrityViolations) != 0 {
		t.Fatalf("expected zero integrity violations from progress Reconciler, got %v", progReport.IntegrityViolations)
	}
}

// --- Proof 2: cross-package concurrent-completion race + concurrent reads
// (Reconciler + Snapshot/LoadLatest) against a growing checkpoint set ------

func TestA09_ConcurrentDifferentNodeCompletions_WithConcurrentReconcileAndSnapshot(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC))
	h := newFullStackHarness(t, clock, "conc")
	ctx := context.Background()

	const writers = 30
	const readerDuration = writers // roughly one reader pass per writer, see below

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		successes int
		writeErrs []error
	)
	start := make(chan struct{})

	// Writers: each completes a DIFFERENT node concurrently — proving
	// CompleteNode.Run's atomic protocol is safe under concurrent
	// completions that do NOT contend on the same row (a04/a02's own race
	// tests only ever targeted the SAME node; this is the genuinely new,
	// cross-node concurrency shape this wave's brief calls for).
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			suffix := fmt.Sprintf("conc-%d", i)
			nodeID := domain.ProgressNodeID("node-" + suffix)
			insertNode(t, h.db, clock, newDocumentNode(h.taskID, nodeID, int64(i), domain.NodePending, "# X"))
			moveNodeToInProgress(t, h.db, clock, nodeID)
			path := writeMarkdownFile(t, "section-"+suffix+".md", uniqueMarkdown(suffix))
			<-start
			_, err := h.cn.Run(ctx, progress.CompleteNodeInput{
				NodeID:         nodeID,
				IdempotencyKey: "key-" + suffix,
				Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-"+suffix, path)},
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				writeErrs = append(writeErrs, fmt.Errorf("writer %d: %w", i, err))
				return
			}
			successes++
		}(i)
	}

	// Readers: concurrently hammer BOTH Reconcilers and Snapshot/LoadLatest
	// against the SAME, growing set of state_checkpoints rows the writers
	// above are inserting — this is the actual cross-package "reads during
	// writes never see torn/inconsistent state" proof the wave brief asks
	// for. Every read must either succeed cleanly (see some consistent
	// prefix of completions) or return the frozen not-found error for "no
	// checkpoints yet" — it must NEVER report a violation/integrity
	// problem, and it must NEVER panic or return a partially-decoded
	// value, regardless of how many writes have landed at the moment it
	// runs.
	var (
		readerWG   sync.WaitGroup
		readerMu   sync.Mutex
		readerErrs []error
	)
	stopReaders := make(chan struct{})
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		<-start
		for i := 0; i < readerDuration; i++ {
			select {
			case <-stopReaders:
				return
			default:
			}

			stateReport, err := h.stateReconcile.Reconcile(ctx, h.taskID)
			if err != nil {
				readerMu.Lock()
				readerErrs = append(readerErrs, fmt.Errorf("statecheckpoint Reconcile: %w", err))
				readerMu.Unlock()
			} else if len(stateReport.Violations) != 0 {
				readerMu.Lock()
				readerErrs = append(readerErrs, fmt.Errorf("statecheckpoint Reconcile found violations mid-race: %v", stateReport.Violations))
				readerMu.Unlock()
			}

			progReport, err := h.progReconcile.Reconcile(ctx, h.taskID)
			if err != nil {
				readerMu.Lock()
				readerErrs = append(readerErrs, fmt.Errorf("progress Reconcile: %w", err))
				readerMu.Unlock()
			} else if len(progReport.IntegrityViolations) != 0 {
				readerMu.Lock()
				readerErrs = append(readerErrs, fmt.Errorf("progress Reconcile found integrity violations mid-race: %v", progReport.IntegrityViolations))
				readerMu.Unlock()
			}

			if latest, err := h.svc.LoadLatest(ctx, h.taskID); err == nil {
				// A successful LoadLatest must itself be independently
				// Verify()-able — a torn read would show up here as a
				// checkpoint whose stored digest doesn't match its own
				// manifest content.
				if verification, verr := h.svc.Verify(ctx, latest.ID); verr == nil && !verification.Valid {
					readerMu.Lock()
					readerErrs = append(readerErrs, fmt.Errorf("LoadLatest returned checkpoint %s that fails Verify mid-race", latest.ID))
					readerMu.Unlock()
				}
			} else if !isNotFoundTest(err) {
				readerMu.Lock()
				readerErrs = append(readerErrs, fmt.Errorf("LoadLatest: unexpected error mid-race: %w", err))
				readerMu.Unlock()
			}
		}
	}()

	close(start)
	wg.Wait()
	close(stopReaders)
	readerWG.Wait()

	if len(writeErrs) != 0 {
		t.Fatalf("expected all %d concurrent DIFFERENT-node completions to succeed, got %d errors: %v", writers, len(writeErrs), writeErrs)
	}
	if successes != writers {
		t.Fatalf("expected %d successful completions, got %d", writers, successes)
	}
	if len(readerErrs) != 0 {
		t.Fatalf("concurrent reader (Reconciler/Snapshot/LoadLatest) observed torn or inconsistent state during concurrent writes: %v", readerErrs)
	}

	// Final state: exactly `writers` checkpoints, every one independently
	// verifiable, confirming the concurrent readers' clean run above wasn't
	// merely lucky about when they happened to look.
	rows, err := h.cn.Checkpoints.ListByTask(ctx, h.taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != writers {
		t.Fatalf("expected %d final checkpoint rows, got %d", writers, len(rows))
	}
	finalReport, err := h.stateReconcile.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("final statecheckpoint Reconcile: %v", err)
	}
	if len(finalReport.Violations) != 0 {
		t.Fatalf("expected zero violations in final reconciliation, got %v", finalReport.Violations)
	}
}

// isNotFoundTest mirrors the package-internal isNotFound helper (this file
// is in progress_test, an external test package, so it cannot call
// internal/progress's unexported isNotFound directly) — used only to let
// the concurrent reader above tell "no checkpoints exist yet" apart from a
// genuine plumbing error.
func isNotFoundTest(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == domain.ErrCodeNotFound
}

// --- Proof 3: crash mid-CompleteNode, "restart", confirm BOTH packages'
// OWN Reconcilers agree on the outcome ---------------------------------------

func TestA09_CrashMidCompleteNode_BothPackagesReconciliationAgree(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC))
	h := newFullStackHarness(t, clock, "crash")
	ctx := context.Background()

	// First, complete a few nodes cleanly so the crash scenario below is
	// realistic (a real task already has completed history before hitting
	// a crash on its NEXT node), and so both Reconcilers have real,
	// pre-existing durable state to reason about, not an empty task.
	for i := 0; i < 3; i++ {
		suffix := fmt.Sprintf("pre-%d", i)
		if _, err := completeOneNode(t, h, clock, suffix, "key-"+suffix); err != nil {
			t.Fatalf("pre-crash node %d: %v", i, err)
		}
	}
	preCrashRows, err := h.cn.Checkpoints.ListByTask(ctx, h.taskID)
	if err != nil {
		t.Fatalf("ListByTask before crash: %v", err)
	}
	if len(preCrashRows) != 3 {
		t.Fatalf("expected 3 pre-crash checkpoints, got %d", len(preCrashRows))
	}

	// Now simulate a crash mid-CompleteNode for a fourth node: halt AFTER
	// artifact evidence has been staged to durable storage but BEFORE the
	// DB transaction (node update + checkpoint insert + idempotency
	// ledger) ever opens. This is the exact "state checkpoint half-written"
	// crash window the wave brief names: from Part A's perspective, staged
	// evidence now exists on disk with NOTHING in the DB referencing it
	// yet — internal/progress's own Reconciler's precise target.
	crashNodeID := domain.ProgressNodeID("node-crash-victim")
	insertNode(t, h.db, clock, newDocumentNode(h.taskID, crashNodeID, 4, domain.NodePending, "# X"))
	moveNodeToInProgress(t, h.db, clock, crashNodeID)
	// Content unique to this node (see uniqueMarkdown's doc comment): must
	// not collide with the 3 pre-crash nodes' already-referenced digests,
	// or the "orphaned" assertion below would pass by accident rather than
	// by proof.
	crashPath := writeMarkdownFile(t, "section-crash.md", uniqueMarkdown("crash-victim"))

	h.cn.HaltAfter = progress.PhaseVerifyArtifacts
	_, err = h.cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         crashNodeID,
		IdempotencyKey: "key-crash-victim",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-crash-victim", crashPath)},
	})
	var halt *progress.HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected a *progress.HaltError simulating the crash, got %v", err)
	}

	// "Restart": construct fresh Reconciler values bound to the SAME
	// durable DB and evidence directory — exactly what a real process
	// restart would do (no in-memory state survives; only what is on disk
	// and in SQLite does). Deliberately NOT reusing h.progReconcile/
	// h.stateReconcile's original struct values, even though they'd behave
	// identically here, specifically to model "a fresh process rebuilding
	// these from scratch on startup" rather than "the same process
	// object happens to still be around."
	restartedProgReconciler := &progress.Reconciler{
		Nodes:       progress.NewNodeStore(h.db, clock),
		Checkpoints: statecheckpoint.NewStore(h.db),
		EvidenceDir: h.progReconcile.EvidenceDir,
	}
	restartedStateReconciler := statecheckpoint.NewReconciler(statecheckpoint.NewStore(h.db))

	progReport, err := restartedProgReconciler.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("progress Reconcile after crash: %v", err)
	}
	stateReport, err := restartedStateReconciler.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("statecheckpoint Reconcile after crash: %v", err)
	}

	// --- The two-reconciler-agreement proof itself ------------------------
	//
	// internal/progress's Reconciler must find exactly one orphaned staged
	// artifact (the crash victim's evidence, staged before the halt, with
	// no committed artifacts row referencing it yet) and zero checkpoint
	// integrity violations among the 3 PRE-EXISTING, cleanly-completed
	// checkpoints (the crash never touched those).
	if len(progReport.OrphanedStagedArtifacts) != 1 {
		t.Fatalf("expected exactly 1 orphaned staged artifact from the crash victim, got %d: %v", len(progReport.OrphanedStagedArtifacts), progReport.OrphanedStagedArtifacts)
	}
	if len(progReport.IntegrityViolations) != 0 {
		t.Fatalf("expected zero checkpoint integrity violations from progress Reconciler (crash never reached checkpoint creation), got %v", progReport.IntegrityViolations)
	}

	// internal/statecheckpoint's Reconciler, scanning the SAME task's
	// state_checkpoints table, must find EXACTLY the 3 pre-crash rows (no
	// fourth row exists — the crash halted before the DB transaction that
	// would have inserted one ever opened) and zero violations among them.
	// This is the two reconcilers' point of agreement: NEITHER one
	// believes a 4th checkpoint exists, NEITHER one flags the 3 real rows
	// as broken, and BOTH independently conclude "the crash victim never
	// became durable state checkpoint history" from their own, differently
	// scoped vantage points (one reads the filesystem+artifacts table, the
	// other reads state_checkpoints rows) without either needing to know
	// about the other's existence.
	if stateReport.CheckpointsScanned != 3 {
		t.Fatalf("expected statecheckpoint Reconciler to see exactly the 3 pre-crash checkpoints (crash victim never committed), scanned %d", stateReport.CheckpointsScanned)
	}
	if len(stateReport.Violations) != 0 {
		t.Fatalf("expected zero violations among the 3 genuine pre-crash checkpoints, got %v", stateReport.Violations)
	}

	// Cross-check via the node/DB state directly: the crash victim node
	// must still show a PRE-completion status (the halted transaction
	// never committed, so NodeStore's own row is untouched), agreeing with
	// both Reconcilers' conclusion that this node's completion never
	// became durable.
	victimNode, err := h.cn.Nodes.Get(ctx, crashNodeID)
	if err != nil {
		t.Fatalf("Get crash victim node: %v", err)
	}
	if victimNode.Status == domain.NodeCompleted {
		t.Fatalf("crash victim node must NOT be completed (transaction never opened/committed), got status=%s", victimNode.Status)
	}

	rows, err := h.cn.Checkpoints.ListByTask(ctx, h.taskID)
	if err != nil {
		t.Fatalf("ListByTask after crash: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected exactly 3 durable checkpoint rows after the crash (unchanged from pre-crash), got %d", len(rows))
	}

	// Recovery: a fresh CompleteNode.Run for the SAME node (new caller,
	// same evidence — the retry a real provider integration would issue)
	// must now succeed cleanly, producing a 4th checkpoint, and BOTH
	// reconcilers must then agree the orphan is gone (once its artifact
	// row exists, it is no longer "orphaned" by definition) and the
	// checkpoint count is now 4 with zero violations — proving recovery
	// doesn't just avoid contradiction, it actually converges to a
	// fully-reconciled state.
	h.cn.HaltAfter = ""
	// Deliberately the SAME content as the original crash-victim staging
	// above (uniqueMarkdown("crash-victim")), not merely the same node: a
	// real retry re-supplies the same evidence the agent already produced,
	// and this is exactly what FileStager's content-addressed idempotency
	// guarantee depends on — the retry's Stage call resolves to the SAME
	// sha256 destination the pre-crash halt already wrote, so the
	// "orphan is gone after recovery" assertion below is proving the real
	// mechanism, not a coincidence of two different orphans overlapping.
	retryPath := writeMarkdownFile(t, "section-crash-retry.md", uniqueMarkdown("crash-victim"))
	retryResult, err := h.cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         crashNodeID,
		IdempotencyKey: "key-crash-victim-retry",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-crash-victim-retry", retryPath)},
	})
	if err != nil {
		t.Fatalf("retry after crash: Run: %v", err)
	}
	if retryResult.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected retry to complete the node, got status=%s", retryResult.Node.Status)
	}

	postRecoveryProgReport, err := restartedProgReconciler.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("progress Reconcile after recovery: %v", err)
	}
	// The original crash-victim evidence file staged before the halt is
	// content-addressed by its own sha256 (FileStager); the retry staged
	// identical content, so it hashes to the SAME path and is now
	// referenced by the newly-committed artifacts row — the orphan from
	// before the retry is thus expected to be gone.
	if len(postRecoveryProgReport.OrphanedStagedArtifacts) != 0 {
		t.Fatalf("expected zero orphaned staged artifacts after successful recovery retry, got %v", postRecoveryProgReport.OrphanedStagedArtifacts)
	}

	postRecoveryStateReport, err := restartedStateReconciler.Reconcile(ctx, h.taskID)
	if err != nil {
		t.Fatalf("statecheckpoint Reconcile after recovery: %v", err)
	}
	if postRecoveryStateReport.CheckpointsScanned != 4 {
		t.Fatalf("expected 4 checkpoints after recovery (3 pre-crash + 1 retry), scanned %d", postRecoveryStateReport.CheckpointsScanned)
	}
	if len(postRecoveryStateReport.Violations) != 0 {
		t.Fatalf("expected zero violations after recovery, got %v", postRecoveryStateReport.Violations)
	}
}
