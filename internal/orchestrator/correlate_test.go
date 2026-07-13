// correlate_test.go: unit tests for correlate.go's EventCorrelator (issue
// #1's event-correlation component). The ambiguity matrix is the point of
// this file — correlation must populate TaskID/ProgressNodeID exactly when
// they resolve unambiguously and leave them empty everywhere else
// ("unknown is not zero"; correlation must never guess):
//
//	session resolves?  task?   in_progress nodes   ->  TaskID      ProgressNodeID
//	no (error)         -       -                       empty       empty
//	yes                nil     -                       empty       empty
//	yes                t1      0                       t1          empty
//	yes                t1      1 (n1)                  t1          n1
//	yes                t1      2+                      t1          empty
//	yes                t1      snapshot error          t1          empty
//
// Test doubles follow this package's established conventions
// (hooks_test.go): a package-local narrow fake for the resolver (no
// FakeFeatureDataSource exists in internal/testutil/fakes, and the
// correlator only consumes the narrow SessionResolver view anyway), and
// internal/testutil/fakes.FakeProgressTreeService for the progress reader
// (its SnapshotFunc field is exactly the one method the narrow
// ProgressSnapshotReader view consumes).
package orchestrator_test

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// fakeSessionResolver is a package-local narrow double for
// orchestrator.SessionResolver, mirroring errorcontract_test.go's
// fakeAuthIssuer precedent for small package-specific interfaces.
type fakeSessionResolver struct {
	fn    func(ctx context.Context, sessionID domain.SessionID) (app.ResolvedSession, error)
	calls int
}

func (f *fakeSessionResolver) Resolve(ctx context.Context, sessionID domain.SessionID) (app.ResolvedSession, error) {
	f.calls++
	return f.fn(ctx, sessionID)
}

// resolverForTask returns a resolver that always resolves to taskID.
func resolverForTask(taskID domain.TaskID) *fakeSessionResolver {
	return &fakeSessionResolver{fn: func(context.Context, domain.SessionID) (app.ResolvedSession, error) {
		return app.ResolvedSession{RepositoryID: "repo-1", TaskID: &taskID}, nil
	}}
}

// snapshotWithStatuses returns a progress reader whose Snapshot yields one
// node per given status, with IDs node-1, node-2, ...
func snapshotWithStatuses(statuses ...domain.ProgressNodeStatus) *fakes.FakeProgressTreeService {
	return &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			snap := app.ProgressTreeSnapshot{TaskID: taskID}
			for i, st := range statuses {
				snap.Nodes = append(snap.Nodes, app.ProgressNode{
					ID:     domain.ProgressNodeID("node-" + string(rune('1'+i))),
					TaskID: taskID,
					Status: st,
				})
			}
			return snap, nil
		},
	}
}

func stopEvent(sessionID string) v1.Event {
	return v1.Event{
		SchemaVersion: v1.SchemaVersionEvent,
		EventID:       "ev-1",
		EventType:     v1.EventProviderTurnCompleted,
		SessionID:     sessionID,
	}
}

func TestCorrelate_ExactlyOneInProgressNode_PopulatesTaskAndNode(t *testing.T) {
	c := &orchestrator.EventCorrelator{
		Sessions: resolverForTask("task-1"),
		Progress: snapshotWithStatuses(domain.NodeCompleted, domain.NodeInProgress, domain.NodePending),
	}
	evs := []v1.Event{stopEvent("sess-1")}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", evs[0].TaskID, "task-1")
	}
	if evs[0].ProgressNodeID != "node-2" {
		t.Errorf("ProgressNodeID = %q, want %q (the single in_progress node)", evs[0].ProgressNodeID, "node-2")
	}
}

func TestCorrelate_NoTaskResolved_LeavesBothEmpty(t *testing.T) {
	c := &orchestrator.EventCorrelator{
		Sessions: &fakeSessionResolver{fn: func(context.Context, domain.SessionID) (app.ResolvedSession, error) {
			// Cold-start per app.FeatureDataSource's contract: session
			// known, no task yet — nil TaskID with no error.
			return app.ResolvedSession{RepositoryID: "repo-1", TaskID: nil}, nil
		}},
		Progress: &fakes.FakeProgressTreeService{
			SnapshotFunc: func(context.Context, domain.TaskID) (app.ProgressTreeSnapshot, error) {
				t.Error("Snapshot must not be called when no task resolved")
				return app.ProgressTreeSnapshot{}, nil
			},
		},
	}
	evs := []v1.Event{stopEvent("sess-1")}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "" || evs[0].ProgressNodeID != "" {
		t.Errorf("TaskID/ProgressNodeID = %q/%q, want both empty for a session with no task", evs[0].TaskID, evs[0].ProgressNodeID)
	}
}

func TestCorrelate_ZeroInProgressNodes_TaskOnly(t *testing.T) {
	c := &orchestrator.EventCorrelator{
		Sessions: resolverForTask("task-1"),
		Progress: snapshotWithStatuses(domain.NodeCompleted, domain.NodePending),
	}
	evs := []v1.Event{stopEvent("sess-1")}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", evs[0].TaskID, "task-1")
	}
	if evs[0].ProgressNodeID != "" {
		t.Errorf("ProgressNodeID = %q, want empty (zero in_progress nodes: nothing to attribute to)", evs[0].ProgressNodeID)
	}
}

func TestCorrelate_MultipleInProgressNodes_TaskOnly_NeverGuesses(t *testing.T) {
	c := &orchestrator.EventCorrelator{
		Sessions: resolverForTask("task-1"),
		Progress: snapshotWithStatuses(domain.NodeInProgress, domain.NodeInProgress),
	}
	evs := []v1.Event{stopEvent("sess-1")}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", evs[0].TaskID, "task-1")
	}
	if evs[0].ProgressNodeID != "" {
		t.Errorf("ProgressNodeID = %q, want empty (two in_progress candidates: picking one would be a guess)", evs[0].ProgressNodeID)
	}
}

func TestCorrelate_ResolverError_LeavesEventUncorrelated(t *testing.T) {
	c := &orchestrator.EventCorrelator{
		Sessions: &fakeSessionResolver{fn: func(context.Context, domain.SessionID) (app.ResolvedSession, error) {
			return app.ResolvedSession{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "no provider_sessions row"}
		}},
		Progress: snapshotWithStatuses(domain.NodeInProgress),
	}
	evs := []v1.Event{stopEvent("sess-unregistered")}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "" || evs[0].ProgressNodeID != "" {
		t.Errorf("TaskID/ProgressNodeID = %q/%q, want both empty on a resolver error (fail-open, uncorrelated)", evs[0].TaskID, evs[0].ProgressNodeID)
	}
}

func TestCorrelate_SnapshotError_KeepsResolvedTask_NodeEmpty(t *testing.T) {
	c := &orchestrator.EventCorrelator{
		Sessions: resolverForTask("task-1"),
		Progress: &fakes.FakeProgressTreeService{
			SnapshotFunc: func(context.Context, domain.TaskID) (app.ProgressTreeSnapshot, error) {
				return app.ProgressTreeSnapshot{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "db down"}
			},
		},
	}
	evs := []v1.Event{stopEvent("sess-1")}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q (the task DID resolve unambiguously; only the node lookup failed)", evs[0].TaskID, "task-1")
	}
	if evs[0].ProgressNodeID != "" {
		t.Errorf("ProgressNodeID = %q, want empty after a snapshot error", evs[0].ProgressNodeID)
	}
}

func TestCorrelate_NilCorrelatorAndNilResolver_AreDocumentedNoOps(t *testing.T) {
	evs := []v1.Event{stopEvent("sess-1")}

	var nilC *orchestrator.EventCorrelator
	nilC.Correlate(context.Background(), evs) // must not panic

	(&orchestrator.EventCorrelator{}).Correlate(context.Background(), evs) // nil Sessions: no-op

	if evs[0].TaskID != "" || evs[0].ProgressNodeID != "" {
		t.Errorf("no-op correlators mutated the event: TaskID/ProgressNodeID = %q/%q", evs[0].TaskID, evs[0].ProgressNodeID)
	}
}

func TestCorrelate_EmptySessionID_Skipped(t *testing.T) {
	resolver := resolverForTask("task-1")
	c := &orchestrator.EventCorrelator{Sessions: resolver}
	evs := []v1.Event{stopEvent("")}
	c.Correlate(context.Background(), evs)

	if resolver.calls != 0 {
		t.Errorf("Resolve called %d times for an event with no SessionID, want 0", resolver.calls)
	}
	if evs[0].TaskID != "" {
		t.Errorf("TaskID = %q, want empty", evs[0].TaskID)
	}
}

func TestCorrelate_ExistingTaskID_NeverOverwritten(t *testing.T) {
	resolver := resolverForTask("task-from-lookup")
	c := &orchestrator.EventCorrelator{Sessions: resolver, Progress: snapshotWithStatuses(domain.NodeInProgress)}

	ev := stopEvent("sess-1")
	ev.TaskID = "task-from-producer"
	evs := []v1.Event{ev}
	c.Correlate(context.Background(), evs)

	if evs[0].TaskID != "task-from-producer" {
		t.Errorf("TaskID = %q, want the producer's own %q preserved", evs[0].TaskID, "task-from-producer")
	}
	if resolver.calls != 0 {
		t.Errorf("Resolve called %d times for an already-correlated event, want 0", resolver.calls)
	}
}

func TestCorrelate_BatchWithSharedSession_ResolvesOnce(t *testing.T) {
	resolver := resolverForTask("task-1")
	snapshotCalls := 0
	c := &orchestrator.EventCorrelator{
		Sessions: resolver,
		Progress: &fakes.FakeProgressTreeService{
			SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
				snapshotCalls++
				return app.ProgressTreeSnapshot{TaskID: taskID, Nodes: []app.ProgressNode{
					{ID: "node-1", TaskID: taskID, Status: domain.NodeInProgress},
				}}, nil
			},
		},
	}

	// A status-line batch: up to four events for the same session
	// (normalizer.go's NormalizeStatusLine).
	evs := []v1.Event{stopEvent("sess-1"), stopEvent("sess-1"), stopEvent("sess-1"), stopEvent("sess-1")}
	c.Correlate(context.Background(), evs)

	if resolver.calls != 1 || snapshotCalls != 1 {
		t.Errorf("Resolve/Snapshot called %d/%d times for one four-event batch, want 1/1 (memoized)", resolver.calls, snapshotCalls)
	}
	for i := range evs {
		if evs[i].TaskID != "task-1" || evs[i].ProgressNodeID != "node-1" {
			t.Errorf("event %d: TaskID/ProgressNodeID = %q/%q, want task-1/node-1", i, evs[i].TaskID, evs[i].ProgressNodeID)
		}
	}
}

// --- correlation through the production hook handler ----------------------

// TestHookHandlers_Stop_PersistsCorrelatedEvent proves the correlator is
// actually invoked on hooks.go's persist path (not just correct in
// isolation): HandleStop with a Correlator wired into HookDeps persists a
// provider.turn.completed event carrying the resolved TaskID and the
// single in_progress node's ID.
func TestHookHandlers_Stop_PersistsCorrelatedEvent(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.Correlator = &orchestrator.EventCorrelator{
		Sessions: resolverForTask("task-1"),
		Progress: snapshotWithStatuses(domain.NodePending, domain.NodeInProgress),
	}

	result, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if !result.Persisted {
		t.Fatal("Persisted = false, want true")
	}
	if len(persister.calls) != 1 || len(persister.calls[0]) != 1 {
		t.Fatalf("persister.calls = %v, want one call with one event", persister.calls)
	}
	got := persister.calls[0][0]
	if got.TaskID != "task-1" {
		t.Errorf("persisted event TaskID = %q, want %q", got.TaskID, "task-1")
	}
	if got.ProgressNodeID != "node-2" {
		t.Errorf("persisted event ProgressNodeID = %q, want %q", got.ProgressNodeID, "node-2")
	}
}

// TestHookHandlers_Stop_CorrelatorFailureNeverFailsTheHook proves the
// fail-open contract at the handler level: a resolver that errors on every
// call still yields a successfully persisted (uncorrelated) event and a
// nil handler error — correlation failure is invisible to the provider.
func TestHookHandlers_Stop_CorrelatorFailureNeverFailsTheHook(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.Correlator = &orchestrator.EventCorrelator{
		Sessions: &fakeSessionResolver{fn: func(context.Context, domain.SessionID) (app.ResolvedSession, error) {
			return app.ResolvedSession{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "resolver down", Retryable: true}
		}},
	}

	result, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStop must not fail on a correlation error, got: %v", err)
	}
	if !result.Persisted {
		t.Fatal("Persisted = false, want true (the event persists uncorrelated)")
	}
	got := persister.calls[0][0]
	if got.TaskID != "" || got.ProgressNodeID != "" {
		t.Errorf("TaskID/ProgressNodeID = %q/%q, want both empty when the resolver errored", got.TaskID, got.ProgressNodeID)
	}
	if got.SessionID == "" {
		t.Error("SessionID lost during failed correlation — the event itself must persist unchanged")
	}
}
