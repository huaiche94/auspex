package progress_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
)

func newNode(taskID domain.TaskID, id domain.ProgressNodeID, ordinal int64, status domain.ProgressNodeStatus) progress.Node {
	return progress.Node{
		ID:         id,
		TaskID:     taskID,
		Ordinal:    ordinal,
		Kind:       domain.NodeDocumentSection,
		Title:      "Test node " + string(id),
		Status:     status,
		Acceptance: []progress.AcceptanceCriterion{{Kind: "heading_exists", Value: "# Test"}},
		Version:    1,
		UpdatedAt:  time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

func TestNodeStore_InsertAndGet(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC)})
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.Get(ctx, "node-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "node-1" || got.TaskID != taskID || got.Status != domain.NodePending {
		t.Fatalf("unexpected node: %+v", got)
	}
	if len(got.Acceptance) != 1 || got.Acceptance[0].Kind != "heading_exists" {
		t.Fatalf("acceptance not round-tripped: %+v", got.Acceptance)
	}
}

func TestNodeStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := progress.NewNodeStore(db, fixedClock{time.Now()})
	ctx := context.Background()

	_, err := store.Get(ctx, "does-not-exist")
	if !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNodeStore_ListByTask_OrderedByParentThenOrdinal(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Now()})
	ctx := context.Background()

	for _, ord := range []int64{3, 1, 2} {
		n := newNode(taskID, domain.ProgressNodeID("node-"+string(rune('a'+ord))), ord, domain.NodePending)
		if err := store.Insert(ctx, n); err != nil {
			t.Fatalf("Insert ordinal %d: %v", ord, err)
		}
	}

	nodes, err := store.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	for i, want := range []int64{1, 2, 3} {
		if nodes[i].Ordinal != want {
			t.Errorf("position %d: expected ordinal %d, got %d", i, want, nodes[i].Ordinal)
		}
	}
}

// TestNodeStore_TransitionStatus_Valid exercises the store-level
// integration of ValidateTransition + optimistic concurrency: a legal
// transition succeeds, persists, and bumps version.
func TestNodeStore_TransitionStatus_Valid(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)})
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := store.TransitionStatus(ctx, "node-1", domain.NodePending, domain.NodeReady, 1); err != nil {
		t.Fatalf("TransitionStatus pending->ready: %v", err)
	}

	got, err := store.Get(ctx, "node-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.NodeReady {
		t.Fatalf("expected status ready, got %s", got.Status)
	}
	if got.Version != 2 {
		t.Fatalf("expected version 2 after transition, got %d", got.Version)
	}
}

// TestNodeStore_TransitionStatus_InvalidRejected is the DAG's required
// "invalid state transition rejected" test, exercised through the actual
// store (not just the pure state machine) so the store's own enforcement
// path is covered, not only statemachine_test.go's direct calls.
func TestNodeStore_TransitionStatus_InvalidRejected(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Now()})
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// pending -> completed skips the entire lifecycle; must be rejected,
	// and rejected WITHOUT mutating the row.
	err := store.TransitionStatus(ctx, "node-1", domain.NodePending, domain.NodeCompleted, 1)
	if err == nil {
		t.Fatal("expected pending->completed to be rejected")
	}

	got, getErr := store.Get(ctx, "node-1")
	if getErr != nil {
		t.Fatalf("Get after rejected transition: %v", getErr)
	}
	if got.Status != domain.NodePending {
		t.Fatalf("row must be unchanged after rejected transition, got status %s", got.Status)
	}
	if got.Version != 1 {
		t.Fatalf("version must be unchanged after rejected transition, got %d", got.Version)
	}
}

// TestNodeStore_TransitionStatus_StaleVersionConflict verifies the
// optimistic-concurrency guard: a transition using an out-of-date `from`
// status or version does not silently succeed.
func TestNodeStore_TransitionStatus_StaleVersionConflict(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Now()})
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.TransitionStatus(ctx, "node-1", domain.NodePending, domain.NodeReady, 1); err != nil {
		t.Fatalf("first transition: %v", err)
	}

	// Retry the same pending->ready transition with the now-stale version 1.
	err := store.TransitionStatus(ctx, "node-1", domain.NodePending, domain.NodeReady, 1)
	if err == nil {
		t.Fatal("expected stale version transition to fail")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict, got %s", domErr.Code)
	}
}

// TestNodeStore_ConcurrentTransition_OnlyOneWins is the DAG's required
// "concurrent completion race" test, scoped to the state-machine/store
// layer per this wave's brief (the full CompleteNode atomic protocol with
// artifact evidence is checkpoint-a04's job): many goroutines race to move
// the SAME node out of in_progress via different valid target states;
// exactly one transition must succeed and the rest must fail with a
// conflict, never a torn or duplicated write.
func TestNodeStore_ConcurrentTransition_OnlyOneWins(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Now()})
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodeInProgress)
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	const attempts = 20
	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := store.TransitionStatus(ctx, "node-1", domain.NodeInProgress, domain.NodeCheckpointing, 1)
			successes[idx] = err == nil
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful transition under concurrency, got %d", successCount)
	}

	got, err := store.Get(ctx, "node-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.NodeCheckpointing {
		t.Fatalf("expected final status checkpointing, got %s", got.Status)
	}
	if got.Version != 2 {
		t.Fatalf("expected exactly one version bump (version=2), got %d", got.Version)
	}
}

func TestNodeStore_SetTimestamps(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := progress.NewNodeStore(db, fixedClock{time.Now()})
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodeInProgress)
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	started := "2026-07-12T03:00:00Z"
	if err := store.SetTimestamps(ctx, "node-1", &started, nil); err != nil {
		t.Fatalf("SetTimestamps: %v", err)
	}

	got, err := store.Get(ctx, "node-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.StartedAt == nil || *got.StartedAt != started {
		t.Fatalf("expected started_at %s, got %v", started, got.StartedAt)
	}
	if got.CompletedAt != nil {
		t.Fatalf("expected completed_at to remain nil, got %v", *got.CompletedAt)
	}
}
