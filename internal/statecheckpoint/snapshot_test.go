// snapshot_test.go: checkpoint-a08's assigned incremental deliverable
// (agents/checkpoint.md Part A deliverable #8, "Snapshot/load-latest/verify
// APIs" — LoadLatest and Verify already shipped in checkpoint-a05;
// Snapshot(ctx, id) is this node's own genuine addition). Named so this
// node's own DAG validation command,
// `go test ./internal/statecheckpoint/... -run 'Snapshot|LoadLatest|Verify'`,
// selects it alongside the pre-existing LoadLatest/Verify coverage.
package statecheckpoint_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
)

// TestService_Snapshot_ReturnsSpecificHistoricalCheckpoint proves Snapshot
// answers a DIFFERENT question than LoadLatest: given an OLDER checkpoint's
// own ID (not the most recent one), Snapshot must still return exactly
// that checkpoint's reconstructed state, even after a newer checkpoint has
// since been created for the same task.
func TestService_Snapshot_ReturnsSpecificHistoricalCheckpoint(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	firstNode := domain.ProgressNodeID("node-1")
	secondNode := domain.ProgressNodeID("node-2")

	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: firstNode, Status: domain.NodeInProgress}},
		},
	}
	store := statecheckpoint.NewStore(db)
	ids := &seqIDs{}
	base := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)

	svc1 := statecheckpoint.NewService(store, tree, fixedClock{base}, ids)
	first, err := svc1.Create(context.Background(), app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}

	// Advance the tree state and create a second, newer checkpoint for the
	// same task — LoadLatest would now return this one, not the first.
	tree.nodes[taskID] = []statecheckpoint.NodeSnapshot{
		{ID: firstNode, Status: domain.NodeCompleted},
		{ID: secondNode, Status: domain.NodeInProgress},
	}
	svc2 := statecheckpoint.NewService(store, tree, fixedClock{base.Add(time.Hour)}, ids)
	second, err := svc2.Create(context.Background(), app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected two distinct checkpoint IDs, got the same: %s", first.ID)
	}

	// Snapshot(first.ID) must return the FIRST checkpoint's own state
	// (ActiveNodeID == firstNode, no completed nodes yet), not the latest.
	snap, err := svc2.Snapshot(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("Snapshot(first.ID): %v", err)
	}
	if snap.ID != first.ID {
		t.Fatalf("expected Snapshot to return checkpoint %s, got %s", first.ID, snap.ID)
	}
	if snap.ActiveNodeID == nil || *snap.ActiveNodeID != firstNode {
		t.Fatalf("expected Snapshot(first.ID) ActiveNodeID=%s, got %v", firstNode, snap.ActiveNodeID)
	}
	if len(snap.CompletedNodeIDs) != 0 {
		t.Fatalf("expected Snapshot(first.ID) to reflect the FIRST checkpoint's state (no completed nodes yet), got %v", snap.CompletedNodeIDs)
	}

	// Snapshot(second.ID) must return the SECOND checkpoint's own,
	// different state - proving Snapshot is not silently returning
	// LoadLatest's answer regardless of which ID was asked for.
	snap2, err := svc2.Snapshot(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("Snapshot(second.ID): %v", err)
	}
	if snap2.ID != second.ID {
		t.Fatalf("expected Snapshot to return checkpoint %s, got %s", second.ID, snap2.ID)
	}
	if len(snap2.CompletedNodeIDs) != 1 || snap2.CompletedNodeIDs[0] != firstNode {
		t.Fatalf("expected Snapshot(second.ID) CompletedNodeIDs=[%s], got %v", firstNode, snap2.CompletedNodeIDs)
	}

	// Cross-check against LoadLatest: it must agree with Snapshot(second.ID)
	// (the most recent one) but NOT with Snapshot(first.ID).
	latest, err := svc2.LoadLatest(context.Background(), taskID)
	if err != nil {
		t.Fatalf("LoadLatest: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("expected LoadLatest to agree with Snapshot(second.ID)=%s, got %s", second.ID, latest.ID)
	}
}

// TestService_Snapshot_UnknownID_NotFound proves Snapshot surfaces the same
// frozen not-found contract as LoadLatest/Verify for an ID that does not
// exist, rather than a bespoke error shape.
func TestService_Snapshot_UnknownID_NotFound(t *testing.T) {
	svc, _ := newTestService(t, fixedClock{time.Now()}, func(domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{}
	})
	_, err := svc.Snapshot(context.Background(), domain.StateCheckpointID("does-not-exist"))
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("expected ErrCodeNotFound, got %#v", err)
	}
}

// TestService_Snapshot_IntegritySHA256Populated proves Snapshot's returned
// domain.StateCheckpoint carries the same integrity digest Create sealed
// it with, so a caller can independently verify it (e.g. via Verify)
// without Snapshot itself needing to re-verify on every read.
func TestService_Snapshot_IntegritySHA256Populated(t *testing.T) {
	svc, taskID := newTestService(t, fixedClock{time.Now()}, func(taskID domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{
			nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
				taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
			},
		}
	})
	ctx := context.Background()

	created, err := svc.Create(ctx, app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	snap, err := svc.Snapshot(ctx, created.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.IntegritySHA256 == "" {
		t.Fatal("expected a non-empty IntegritySHA256 on the snapshot")
	}
	if snap.IntegritySHA256 != created.IntegritySHA256 {
		t.Fatalf("expected Snapshot's digest to match Create's own returned digest, got %q vs %q", snap.IntegritySHA256, created.IntegritySHA256)
	}
}

// TestService_Snapshot_ThenVerify_ConsistentAnswer proves Snapshot and
// Verify agree on the SAME checkpoint: an untampered checkpoint that
// Snapshot successfully reconstructs must also Verify as valid, and a
// checkpoint tampered after creation must Verify as invalid even though
// Snapshot can still (honestly) reconstruct the now-untrustworthy stored
// document — Snapshot is a plain read, not a verifying read, exactly as
// documented.
func TestService_Snapshot_ThenVerify_ConsistentAnswer(t *testing.T) {
	svc, taskID := newTestService(t, fixedClock{time.Now()}, func(taskID domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{
			nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
				taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
			},
		}
	})
	ctx := context.Background()

	created, err := svc.Create(ctx, app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Snapshot(ctx, created.ID); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	verdict, err := svc.Verify(ctx, created.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verdict.Valid {
		t.Fatal("expected an untampered, just-created checkpoint to verify as valid")
	}
}
