package progress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
)

func seedTwoNodes(t *testing.T, ctx context.Context, nodeStore *progress.NodeStore, taskID domain.TaskID) (domain.ProgressNodeID, domain.ProgressNodeID) {
	t.Helper()
	a := newNode(taskID, "node-a", 1, domain.NodePending)
	b := newNode(taskID, "node-b", 2, domain.NodePending)
	if err := nodeStore.Insert(ctx, a); err != nil {
		t.Fatalf("insert node-a: %v", err)
	}
	if err := nodeStore.Insert(ctx, b); err != nil {
		t.Fatalf("insert node-b: %v", err)
	}
	return "node-a", "node-b"
}

func TestEdgeStore_InsertAndDependenciesOf(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	edgeStore := progress.NewEdgeStore(db)
	ctx := context.Background()

	from, to := seedTwoNodes(t, ctx, nodeStore, taskID)

	if err := edgeStore.Insert(ctx, progress.Edge{
		TaskID: taskID, FromNodeID: from, ToNodeID: to, Kind: progress.EdgeDependsOn,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	deps, err := edgeStore.DependenciesOf(ctx, taskID, from)
	if err != nil {
		t.Fatalf("DependenciesOf: %v", err)
	}
	if len(deps) != 1 || deps[0] != to {
		t.Fatalf("expected dependency [%s], got %v", to, deps)
	}
}

func TestEdgeStore_RelatesTo_NotADependency(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	edgeStore := progress.NewEdgeStore(db)
	ctx := context.Background()

	from, to := seedTwoNodes(t, ctx, nodeStore, taskID)

	if err := edgeStore.Insert(ctx, progress.Edge{
		TaskID: taskID, FromNodeID: from, ToNodeID: to, Kind: progress.EdgeRelatesTo,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	deps, err := edgeStore.DependenciesOf(ctx, taskID, from)
	if err != nil {
		t.Fatalf("DependenciesOf: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("relates_to edge must not be treated as a dependency, got %v", deps)
	}
}

func TestEdgeStore_DuplicateEdge_Conflict(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	edgeStore := progress.NewEdgeStore(db)
	ctx := context.Background()

	from, to := seedTwoNodes(t, ctx, nodeStore, taskID)
	edge := progress.Edge{TaskID: taskID, FromNodeID: from, ToNodeID: to, Kind: progress.EdgeDependsOn}

	if err := edgeStore.Insert(ctx, edge); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	err := edgeStore.Insert(ctx, edge)
	if err == nil {
		t.Fatal("expected duplicate edge insert to fail")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict, got %s", domErr.Code)
	}
}

func TestEdgeStore_UnknownKind_Rejected(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	edgeStore := progress.NewEdgeStore(db)
	ctx := context.Background()

	from, to := seedTwoNodes(t, ctx, nodeStore, taskID)

	err := edgeStore.Insert(ctx, progress.Edge{
		TaskID: taskID, FromNodeID: from, ToNodeID: to, Kind: progress.EdgeKind("bogus"),
	})
	if err == nil {
		t.Fatal("expected unknown edge kind to be rejected")
	}
}

func TestEdgeStore_SelfReferential_Rejected(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	edgeStore := progress.NewEdgeStore(db)
	ctx := context.Background()

	from, _ := seedTwoNodes(t, ctx, nodeStore, taskID)

	err := edgeStore.Insert(ctx, progress.Edge{
		TaskID: taskID, FromNodeID: from, ToNodeID: from, Kind: progress.EdgeDependsOn,
	})
	if err == nil {
		t.Fatal("expected self-referential edge to be rejected")
	}
}

func TestEdgeStore_ListByTask(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	edgeStore := progress.NewEdgeStore(db)
	ctx := context.Background()

	from, to := seedTwoNodes(t, ctx, nodeStore, taskID)
	if err := edgeStore.Insert(ctx, progress.Edge{TaskID: taskID, FromNodeID: from, ToNodeID: to, Kind: progress.EdgeDependsOn}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	edges, err := edgeStore.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
}
