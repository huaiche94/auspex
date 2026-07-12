package fakes

import (
	"context"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
)

// FakeStateCheckpointService is a configurable test double for the frozen
// app.StateCheckpointService contract (ADD §9.9). See the package doc for
// the Func-field pattern and nil-Func behavior.
type FakeStateCheckpointService struct {
	CreateFunc     func(ctx context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error)
	LoadLatestFunc func(ctx context.Context, taskID domain.TaskID) (domain.StateCheckpoint, error)
	VerifyFunc     func(ctx context.Context, id domain.StateCheckpointID) (app.StateCheckpointVerification, error)
}

var _ app.StateCheckpointService = (*FakeStateCheckpointService)(nil)

func (f *FakeStateCheckpointService) Create(ctx context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
	if f.CreateFunc == nil {
		return domain.StateCheckpoint{}, errUnconfigured("FakeStateCheckpointService", "Create")
	}
	return f.CreateFunc(ctx, req)
}

func (f *FakeStateCheckpointService) LoadLatest(ctx context.Context, taskID domain.TaskID) (domain.StateCheckpoint, error) {
	if f.LoadLatestFunc == nil {
		return domain.StateCheckpoint{}, errUnconfigured("FakeStateCheckpointService", "LoadLatest")
	}
	return f.LoadLatestFunc(ctx, taskID)
}

func (f *FakeStateCheckpointService) Verify(ctx context.Context, id domain.StateCheckpointID) (app.StateCheckpointVerification, error) {
	if f.VerifyFunc == nil {
		return app.StateCheckpointVerification{}, errUnconfigured("FakeStateCheckpointService", "Verify")
	}
	return f.VerifyFunc(ctx, id)
}
