package orchestrator_test

// stopreconcile_test.go: issue #115 (M4) — the post-turn Progress Tree/
// Git/artifact reconciliation + evidence-gate outcome labeling the Stop
// hook runs. Covers the TurnReconcileService resolution/degrade rules
// directly, and the HandleStop-side emission (event shape, flag surface,
// per-turn idempotency, fail-open) through the public handler.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/progress"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// fakeProgressTreeReconciler is a controllable
// orchestrator.ProgressTreeReconciler.
type fakeProgressTreeReconciler struct {
	report progress.ReconcileReport
	err    error
	asked  []domain.TaskID
}

func (f *fakeProgressTreeReconciler) Reconcile(_ context.Context, taskID domain.TaskID) (progress.ReconcileReport, error) {
	f.asked = append(f.asked, taskID)
	return f.report, f.err
}

// fakeGitFingerprinter is a controllable orchestrator.GitFingerprinter.
type fakeGitFingerprinter struct {
	fp    gitx.Fingerprint
	err   error
	asked []string
}

func (f *fakeGitFingerprinter) Fingerprint(_ context.Context, path string) (gitx.Fingerprint, error) {
	f.asked = append(f.asked, path)
	return f.fp, f.err
}

func strptr(s string) *string { return &s }

func TestTurnReconcileService_UnresolvedSessionIsNotReconciled(t *testing.T) {
	// A session with no task yet (cold start / resolver error) yields
	// ok=false, never a fabricated empty report (unknown is not zero).
	svc := &orchestrator.TurnReconcileService{
		Sessions: &fakeSessionResolver{fn: func(context.Context, domain.SessionID) (app.ResolvedSession, error) {
			return app.ResolvedSession{}, errors.New("not registered")
		}},
		Progress: snapshotWithStatuses(),
	}
	if _, ok := svc.ReconcileTurn(context.Background(), "sess-1", nil); ok {
		t.Fatal("ReconcileTurn: expected ok=false for an unresolvable session")
	}
}

func TestTurnReconcileService_ReportsTreeCrashWindowAndGitState(t *testing.T) {
	reconciler := &fakeProgressTreeReconciler{report: progress.ReconcileReport{
		OrphanedStagedArtifacts: []string{"/evidence/sha256/aa/bb"},
	}}
	git := &fakeGitFingerprinter{fp: gitx.Fingerprint{HeadOID: "abc123"}}
	svc := &orchestrator.TurnReconcileService{
		Sessions:   resolverForTask("task-9"),
		Progress:   snapshotWithStatuses(domain.NodeCompleted, domain.NodeInProgress, domain.NodeCheckpointing, domain.NodePending),
		Reconciler: reconciler,
		Git:        git,
	}

	report, ok := svc.ReconcileTurn(context.Background(), "sess-1", strptr("/repo"))
	if !ok {
		t.Fatal("ReconcileTurn: expected ok=true")
	}
	if report.TaskID != "task-9" {
		t.Fatalf("TaskID = %q, want task-9", report.TaskID)
	}
	if report.NodesTotal != 4 || report.NodesCompleted != 1 {
		t.Fatalf("nodes total/completed = %d/%d, want 4/1", report.NodesTotal, report.NodesCompleted)
	}
	if len(report.OpenNodeIDs) != 1 || report.OpenNodeIDs[0] != "node-2" {
		t.Fatalf("OpenNodeIDs = %v, want [node-2]", report.OpenNodeIDs)
	}
	if len(report.CheckpointingNodeIDs) != 1 || report.CheckpointingNodeIDs[0] != "node-3" {
		t.Fatalf("CheckpointingNodeIDs = %v, want [node-3]", report.CheckpointingNodeIDs)
	}
	if report.OrphanedStagedArtifacts == nil || *report.OrphanedStagedArtifacts != 1 {
		t.Fatalf("OrphanedStagedArtifacts = %v, want 1", report.OrphanedStagedArtifacts)
	}
	if report.CheckpointIntegrityViolations == nil || *report.CheckpointIntegrityViolations != 0 {
		t.Fatalf("CheckpointIntegrityViolations = %v, want 0", report.CheckpointIntegrityViolations)
	}
	if report.GitHeadOID != "abc123" || report.GitChangedPaths == nil || *report.GitChangedPaths != 0 {
		t.Fatalf("git fields = %q/%v, want abc123/0", report.GitHeadOID, report.GitChangedPaths)
	}
	if len(reconciler.asked) != 1 || reconciler.asked[0] != "task-9" {
		t.Fatalf("reconciler asked = %v, want [task-9]", reconciler.asked)
	}
	if len(git.asked) != 1 || git.asked[0] != "/repo" {
		t.Fatalf("git asked = %v, want [/repo]", git.asked)
	}

	flags := report.Flags()
	wantFlags := map[string]bool{
		orchestrator.StopReconcileFlagOpenNodes:          true,
		orchestrator.StopReconcileFlagStuckCheckpointing: true,
		orchestrator.StopReconcileFlagOrphanedArtifacts:  true,
	}
	if len(flags) != len(wantFlags) {
		t.Fatalf("Flags() = %v, want exactly %v", flags, wantFlags)
	}
	for _, f := range flags {
		if !wantFlags[f] {
			t.Fatalf("Flags() = %v carries unexpected flag %q", flags, f)
		}
	}
}

func TestTurnReconcileService_ScanAndGitFailuresDegradeToUnknown(t *testing.T) {
	// A reconciler error leaves the scan counts nil (unknown, not zero) and
	// the report flagged as scan-unavailable; a fingerprint error omits the
	// Git fields. Neither is an error — fail-open end to end.
	svc := &orchestrator.TurnReconcileService{
		Sessions:   resolverForTask("task-9"),
		Progress:   snapshotWithStatuses(domain.NodeCompleted),
		Reconciler: &fakeProgressTreeReconciler{err: errors.New("scan blew up")},
		Git:        &fakeGitFingerprinter{err: errors.New("not a repo")},
	}

	report, ok := svc.ReconcileTurn(context.Background(), "sess-1", strptr("/repo"))
	if !ok {
		t.Fatal("ReconcileTurn: expected ok=true despite scan/git failures")
	}
	if report.OrphanedStagedArtifacts != nil || report.CheckpointIntegrityViolations != nil {
		t.Fatalf("scan counts = %v/%v, want nil/nil (unknown is not zero)",
			report.OrphanedStagedArtifacts, report.CheckpointIntegrityViolations)
	}
	if report.GitHeadOID != "" || report.GitChangedPaths != nil {
		t.Fatalf("git fields = %q/%v, want empty/nil", report.GitHeadOID, report.GitChangedPaths)
	}
	flags := report.Flags()
	if len(flags) != 1 || flags[0] != orchestrator.StopReconcileFlagScanUnavailable {
		t.Fatalf("Flags() = %v, want exactly [%s]", flags, orchestrator.StopReconcileFlagScanUnavailable)
	}
}

// fakeStopReconciler is a controllable orchestrator.StopReconciler for
// driving HandleStop's emission path without the full resolution chain.
type fakeStopReconciler struct {
	report orchestrator.StopReconcileReport
	ok     bool
	asked  []domain.SessionID
	dirs   []*string
}

func (f *fakeStopReconciler) ReconcileTurn(_ context.Context, sessionID domain.SessionID, dir *string) (orchestrator.StopReconcileReport, bool) {
	f.asked = append(f.asked, sessionID)
	f.dirs = append(f.dirs, dir)
	return f.report, f.ok
}

// stopFixtureSession is the session_id carried by
// testdata/provider-events/claude/stop/normal.json.
const stopFixtureSession = "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W"

func TestHandleStop_EmitsProgressTreeReconciledEvent(t *testing.T) {
	orphans := 2
	violations := 0
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.OpenTurns = &fakeOpenTurns{turnID: "turn-7", ok: true}
	reconciler := &fakeStopReconciler{ok: true, report: orchestrator.StopReconcileReport{
		TaskID:                        "task-9",
		NodesTotal:                    3,
		NodesCompleted:                2,
		OpenNodeIDs:                   []domain.ProgressNodeID{"node-3"},
		OrphanedStagedArtifacts:       &orphans,
		CheckpointIntegrityViolations: &violations,
		GitHeadOID:                    "abc123",
	}}
	deps.StopReconcile = reconciler

	if _, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json")); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}

	// Two persist batches: the turn.completed event, then the reconciled
	// event (emitted after the turn's own events are committed).
	if len(persister.calls) != 2 {
		t.Fatalf("persist batches = %d, want 2", len(persister.calls))
	}
	if len(reconciler.asked) != 1 || reconciler.asked[0] != domain.SessionID(stopFixtureSession) {
		t.Fatalf("reconciler asked = %v, want the fixture session", reconciler.asked)
	}
	if len(reconciler.dirs) != 1 || reconciler.dirs[0] == nil || *reconciler.dirs[0] == "" {
		t.Fatal("reconciler must receive the Stop payload's reported cwd")
	}

	batch := persister.calls[1]
	if len(batch) != 1 {
		t.Fatalf("reconcile batch size = %d, want 1", len(batch))
	}
	ev := batch[0]
	if ev.EventType != v1.EventProgressTreeReconciled {
		t.Fatalf("EventType = %q, want %q", ev.EventType, v1.EventProgressTreeReconciled)
	}
	if ev.SchemaVersion != v1.SchemaVersionEvent {
		t.Fatalf("SchemaVersion = %q, want %q", ev.SchemaVersion, v1.SchemaVersionEvent)
	}
	if ev.SessionID != stopFixtureSession {
		t.Fatalf("SessionID = %q, want the fixture session", ev.SessionID)
	}
	if ev.TaskID != "task-9" {
		t.Fatalf("TaskID = %q, want task-9 (stamped from the report, no correlator round trip)", ev.TaskID)
	}
	if ev.TurnID != "turn-7" {
		t.Fatalf("TurnID = %q, want turn-7 (stamped from the open-turn resolver)", ev.TurnID)
	}
	if ev.Source != string(domain.SourceHook) {
		t.Fatalf("Source = %q, want hook", ev.Source)
	}
	if ev.IdempotencyKey == "" {
		t.Fatal("IdempotencyKey must be set")
	}

	if got := ev.Payload["evidence_gate"]; got != "flagged" {
		t.Fatalf(`payload evidence_gate = %v, want "flagged"`, got)
	}
	flags, _ := ev.Payload["flags"].([]string)
	if len(flags) != 2 {
		t.Fatalf("payload flags = %v, want open-nodes + orphaned-artifacts", ev.Payload["flags"])
	}
	openIDs, _ := ev.Payload["open_node_ids"].([]string)
	if len(openIDs) != 1 || openIDs[0] != "node-3" {
		t.Fatalf("payload open_node_ids = %v, want [node-3]", ev.Payload["open_node_ids"])
	}
	if got := ev.Payload["orphaned_staged_artifacts"]; got != 2 {
		t.Fatalf("payload orphaned_staged_artifacts = %v, want 2", got)
	}
	if got := ev.Payload["nodes_total"]; got != 3 {
		t.Fatalf("payload nodes_total = %v, want 3", got)
	}
	if got := ev.Payload["git_head_oid"]; got != "abc123" {
		t.Fatalf("payload git_head_oid = %v, want abc123", got)
	}
	if _, present := ev.Payload["git_changed_paths"]; present {
		t.Fatal("payload git_changed_paths must be omitted when unknown (unknown is not zero)")
	}
}

func TestHandleStop_ReconciledEventKeyIsPerTurnIdempotent(t *testing.T) {
	// A re-entrant Stop (stop_hook_active) re-reconciles the SAME turn:
	// the key must be identical across the two invocations even at
	// different observation times, so the UNIQUE idempotency index dedupes
	// the second row.
	keys := make([]string, 0, 2)
	for i, at := range []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
	} {
		deps := baseHookDeps()
		deps.Clock = fixedClock{t: at}
		persister := &recordingPersister{}
		deps.Persister = persister
		deps.TxRunner = noopTxRunner{}
		deps.OpenTurns = &fakeOpenTurns{turnID: "turn-7", ok: true}
		deps.StopReconcile = &fakeStopReconciler{ok: true, report: orchestrator.StopReconcileReport{TaskID: "task-9"}}

		if _, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json")); err != nil {
			t.Fatalf("HandleStop run %d: %v", i, err)
		}
		if len(persister.calls) != 2 {
			t.Fatalf("run %d persist batches = %d, want 2", i, len(persister.calls))
		}
		keys = append(keys, persister.calls[1][0].IdempotencyKey)
	}
	if keys[0] != keys[1] {
		t.Fatalf("per-turn keys differ across re-entrant Stops: %q vs %q", keys[0], keys[1])
	}
}

func TestHandleStop_NoReconcilerAndNoReportStayFailOpen(t *testing.T) {
	// nil StopReconcile: exactly the pre-#115 behavior (one persist batch).
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	if _, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json")); err != nil {
		t.Fatalf("HandleStop (nil reconciler): %v", err)
	}
	if len(persister.calls) != 1 {
		t.Fatalf("persist batches = %d, want 1 with no reconciler wired", len(persister.calls))
	}

	// ok=false (unresolvable session): the Stop hook still succeeds and no
	// reconciled event is fabricated.
	deps = baseHookDeps()
	persister = &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.StopReconcile = &fakeStopReconciler{ok: false}
	result, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStop (ok=false reconciler): %v", err)
	}
	if result.EventsNormalized != 1 {
		t.Fatalf("EventsNormalized = %d, want 1", result.EventsNormalized)
	}
	if len(persister.calls) != 1 {
		t.Fatalf("persist batches = %d, want 1 when nothing reconciled", len(persister.calls))
	}
}
