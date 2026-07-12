// reconcile_test.go: checkpoint-a06's required crash-injection harness for
// Service.Create's own crash windows (see reconcile.go's package doc
// comment for the full analysis of what those windows actually are). Named
// so this node's own DAG validation command,
// `go test ./internal/statecheckpoint/... -run Reconcile`, selects exactly
// this file.
package statecheckpoint_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// tamperManifestTaskID directly rewrites a checkpoint's stored
// manifest_json to reference a different task_id than the row's own
// task_id column, keeping the (now-stale) integrity_sha256 unchanged —
// simulating on-disk tampering or bit rot the same way
// TestService_Verify_TamperedManifest_Invalid (service_test.go) does,
// reused here to prove Reconcile's own digest-mismatch detection.
func tamperManifestTaskID(t *testing.T, db *sqlite.DB, id domain.StateCheckpointID, keptIntegritySHA256 string) {
	t.Helper()
	tamperedManifestJSON := `{"schema_version":"preflight.state-checkpoint.v1","task_id":"tampered","integrity_sha256":"` + keptIntegritySHA256 + `"}`
	q := sqlite.QuerierFromContext(context.Background(), db)
	if _, err := q.ExecContext(context.Background(), `UPDATE state_checkpoints SET manifest_json = ? WHERE id = ?`, tamperedManifestJSON, string(id)); err != nil {
		t.Fatalf("tamperManifestTaskID: %v", err)
	}
}

// runCreateToHalt calls svc.Create and asserts it stopped via a
// *statecheckpoint.HaltError at exactly the expected phase — mirroring
// internal/progress's identical runToHalt helper for CompleteNode's own
// crash-injection tests.
func runCreateToHalt(t *testing.T, svc *statecheckpoint.Service, phase statecheckpoint.Phase, req app.CreateStateCheckpointRequest) {
	t.Helper()
	_, err := svc.Create(context.Background(), req)
	if err == nil {
		t.Fatalf("expected Create to halt at phase %q, but it completed normally", phase)
	}
	var halt *statecheckpoint.HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected a *HaltError at phase %q, got: %v", phase, err)
	}
	if halt.Phase != phase {
		t.Fatalf("expected halt at phase %q, got halt at %q", phase, halt.Phase)
	}
}

// TestReconcile_CrashAtPhaseReadTree_NoDurableStateNoViolations proves the
// first crash window: a halt immediately after ListNodes/ListArtifacts
// leaves NOTHING durable (Create has not written anything yet), so
// Reconcile finds zero checkpoints and zero violations for the task.
func TestReconcile_CrashAtPhaseReadTree_NoDurableStateNoViolations(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeInProgress}},
		},
	}
	store := statecheckpoint.NewStore(db)
	svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})
	svc.HaltAfter = statecheckpoint.PhaseReadTree

	runCreateToHalt(t, svc, statecheckpoint.PhaseReadTree, app.CreateStateCheckpointRequest{TaskID: taskID})

	reconciler := statecheckpoint.NewReconciler(store)
	report, err := reconciler.Reconcile(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.CheckpointsScanned != 0 {
		t.Fatalf("expected 0 checkpoints scanned (nothing durable was written), got %d", report.CheckpointsScanned)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("expected 0 violations, got %v", report.Violations)
	}
}

// TestReconcile_CrashAtPhaseSeal_NoDurableStateNoViolations proves the
// second crash window: a halt after the manifest is fully built and sealed
// IN MEMORY, but before Store.Insert runs, still leaves nothing durable.
func TestReconcile_CrashAtPhaseSeal_NoDurableStateNoViolations(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
		},
	}
	store := statecheckpoint.NewStore(db)
	svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})
	svc.HaltAfter = statecheckpoint.PhaseSeal

	runCreateToHalt(t, svc, statecheckpoint.PhaseSeal, app.CreateStateCheckpointRequest{TaskID: taskID})

	reconciler := statecheckpoint.NewReconciler(store)
	report, err := reconciler.Reconcile(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.CheckpointsScanned != 0 {
		t.Fatalf("expected 0 checkpoints scanned (manifest was only ever in memory), got %d", report.CheckpointsScanned)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("expected 0 violations, got %v", report.Violations)
	}
}

// TestReconcile_CrashAtPhaseInsert_RowFullyValid_NoViolations proves the
// one phase where durable state DOES exist (Store.Insert's single
// statement already committed before the halt fires): the resulting row
// must be a fully valid, self-consistent checkpoint — never a dangling or
// half-written one — so Reconcile finds it and reports zero violations.
func TestReconcile_CrashAtPhaseInsert_RowFullyValid_NoViolations(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
		},
		artifacts: map[domain.TaskID][]statecheckpoint.ArtifactSnapshot{
			taskID: {{ID: "a-1", URI: "file:///a.md", Bytes: 5, SHA256: "abc123", ValidationStatus: "passed"}},
		},
	}
	store := statecheckpoint.NewStore(db)
	svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})
	svc.HaltAfter = statecheckpoint.PhaseInsert

	runCreateToHalt(t, svc, statecheckpoint.PhaseInsert, app.CreateStateCheckpointRequest{TaskID: taskID})

	reconciler := statecheckpoint.NewReconciler(store)
	report, err := reconciler.Reconcile(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.CheckpointsScanned != 1 {
		t.Fatalf("expected exactly 1 checkpoint scanned (Insert committed before the halt), got %d", report.CheckpointsScanned)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("expected 0 violations for a row inserted atomically before the crash, got %v", report.Violations)
	}
}

// TestReconcile_AllThreePhases_NeverLeavesADanglingRow is the required
// "genuine crash injection harness proving no orphaned/dangling state
// survives startup" test in its most direct form: halt at every phase in
// turn and confirm Reconcile's verdict is always zero violations —
// exhaustively covering every reachable crash window in Create's sequence,
// not just one of them.
func TestReconcile_AllThreePhases_NeverLeavesADanglingRow(t *testing.T) {
	phases := []statecheckpoint.Phase{
		statecheckpoint.PhaseReadTree,
		statecheckpoint.PhaseSeal,
		statecheckpoint.PhaseInsert,
	}

	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			db := openTestDB(t)
			taskID := seedTask(t, db)
			tree := &fakeTreeReader{
				nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
					taskID: {{ID: "n-1", Status: domain.NodeInProgress}},
				},
			}
			store := statecheckpoint.NewStore(db)
			svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})
			svc.HaltAfter = phase

			runCreateToHalt(t, svc, phase, app.CreateStateCheckpointRequest{TaskID: taskID})

			reconciler := statecheckpoint.NewReconciler(store)
			report, err := reconciler.Reconcile(context.Background(), taskID)
			if err != nil {
				t.Fatalf("Reconcile after halt at %q: %v", phase, err)
			}
			if len(report.Violations) != 0 {
				t.Fatalf("phase %q left a dangling/inconsistent row: %v", phase, report.Violations)
			}
		})
	}
}

// TestReconcile_NoHalt_NormalCreate_StillFullyValid proves the baseline
// (no crash injected at all): an ordinary, uninterrupted Create still
// produces a checkpoint Reconcile reports as fully valid — so the
// crash-injection tests above are meaningfully proving something beyond
// "Reconcile always reports success no matter what."
func TestReconcile_NoHalt_NormalCreate_StillFullyValid(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
		},
	}
	store := statecheckpoint.NewStore(db)
	svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})

	if _, err := svc.Create(context.Background(), app.CreateStateCheckpointRequest{TaskID: taskID}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reconciler := statecheckpoint.NewReconciler(store)
	report, err := reconciler.Reconcile(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.CheckpointsScanned != 1 {
		t.Fatalf("expected exactly 1 checkpoint scanned, got %d", report.CheckpointsScanned)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("expected 0 violations for an uninterrupted Create, got %v", report.Violations)
	}
}

// TestReconcile_TamperedManifest_ReportsDigestViolation proves Reconcile
// actually detects a genuine problem when one exists (not merely a
// vacuous "always passes" check) — direct DB tampering after a normal
// Create must surface as a digest-mismatch violation.
func TestReconcile_TamperedManifest_ReportsDigestViolation(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
		},
	}
	store := statecheckpoint.NewStore(db)
	svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})

	cp, err := svc.Create(context.Background(), app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tamperManifestTaskID(t, db, cp.ID, cp.IntegritySHA256)

	reconciler := statecheckpoint.NewReconciler(store)
	report, err := reconciler.Reconcile(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(report.Violations) == 0 {
		t.Fatal("expected at least one violation for a tampered manifest, got none")
	}
}

// TestReconcile_UnknownTask_EmptyReport proves a task with no checkpoints
// at all (never created, or none yet) reconciles to an empty, zero-error
// report rather than surfacing a spurious not-found error.
func TestReconcile_UnknownTask_EmptyReport(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := statecheckpoint.NewStore(db)
	reconciler := statecheckpoint.NewReconciler(store)

	report, err := reconciler.Reconcile(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.CheckpointsScanned != 0 || len(report.Violations) != 0 {
		t.Fatalf("expected an empty report for a task with no checkpoints, got %+v", report)
	}
}
