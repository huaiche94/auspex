package fakes

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// FakeProgressTreeService is a configurable test double for the frozen
// app.ProgressTreeService contract (ADD §9.9; Constitution §6 — the
// Progress Tree is canonical task state). See the package doc for the
// Func-field pattern and nil-Func behavior.
type FakeProgressTreeService struct {
	CreateTaskFunc   func(ctx context.Context, req app.CreateTaskRequest) (app.Task, error)
	UpsertPlanFunc   func(ctx context.Context, req app.UpsertPlanRequest) (app.ProgressTree, error)
	StartNodeFunc    func(ctx context.Context, req app.StartNodeRequest) (app.ProgressNode, error)
	CompleteNodeFunc func(ctx context.Context, req app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error)
	FailNodeFunc     func(ctx context.Context, req app.FailNodeRequest) (app.ProgressNode, error)
	SnapshotFunc     func(ctx context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error)
	ReconcileFunc    func(ctx context.Context, req app.ReconcileProgressRequest) (app.ReconcileResult, error)
}

var _ app.ProgressTreeService = (*FakeProgressTreeService)(nil)

func (f *FakeProgressTreeService) CreateTask(ctx context.Context, req app.CreateTaskRequest) (app.Task, error) {
	if f.CreateTaskFunc == nil {
		return app.Task{}, errUnconfigured("FakeProgressTreeService", "CreateTask")
	}
	return f.CreateTaskFunc(ctx, req)
}

func (f *FakeProgressTreeService) UpsertPlan(ctx context.Context, req app.UpsertPlanRequest) (app.ProgressTree, error) {
	if f.UpsertPlanFunc == nil {
		return app.ProgressTree{}, errUnconfigured("FakeProgressTreeService", "UpsertPlan")
	}
	return f.UpsertPlanFunc(ctx, req)
}

func (f *FakeProgressTreeService) StartNode(ctx context.Context, req app.StartNodeRequest) (app.ProgressNode, error) {
	if f.StartNodeFunc == nil {
		return app.ProgressNode{}, errUnconfigured("FakeProgressTreeService", "StartNode")
	}
	return f.StartNodeFunc(ctx, req)
}

func (f *FakeProgressTreeService) CompleteNode(ctx context.Context, req app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error) {
	if f.CompleteNodeFunc == nil {
		return app.ProgressNode{}, domain.StateCheckpoint{}, errUnconfigured("FakeProgressTreeService", "CompleteNode")
	}
	return f.CompleteNodeFunc(ctx, req)
}

func (f *FakeProgressTreeService) FailNode(ctx context.Context, req app.FailNodeRequest) (app.ProgressNode, error) {
	if f.FailNodeFunc == nil {
		return app.ProgressNode{}, errUnconfigured("FakeProgressTreeService", "FailNode")
	}
	return f.FailNodeFunc(ctx, req)
}

func (f *FakeProgressTreeService) Snapshot(ctx context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
	if f.SnapshotFunc == nil {
		return app.ProgressTreeSnapshot{}, errUnconfigured("FakeProgressTreeService", "Snapshot")
	}
	return f.SnapshotFunc(ctx, taskID)
}

func (f *FakeProgressTreeService) Reconcile(ctx context.Context, req app.ReconcileProgressRequest) (app.ReconcileResult, error) {
	if f.ReconcileFunc == nil {
		return app.ReconcileResult{}, errUnconfigured("FakeProgressTreeService", "Reconcile")
	}
	return f.ReconcileFunc(ctx, req)
}
