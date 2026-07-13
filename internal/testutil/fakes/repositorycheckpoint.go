package fakes

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// FakeRepositoryCheckpointService is a configurable test double for the
// frozen app.RepositoryCheckpointService contract (ADD §9.9; checkpoint
// role Part B owns the real implementation). See the package doc for the
// Func-field pattern and nil-Func behavior.
type FakeRepositoryCheckpointService struct {
	CreateFunc  func(ctx context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error)
	VerifyFunc  func(ctx context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error)
	RestoreFunc func(ctx context.Context, req app.RestoreRepositoryCheckpointRequest) (app.RestoreResult, error)
}

var _ app.RepositoryCheckpointService = (*FakeRepositoryCheckpointService)(nil)

func (f *FakeRepositoryCheckpointService) Create(ctx context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
	if f.CreateFunc == nil {
		return app.RepositoryCheckpoint{}, errUnconfigured("FakeRepositoryCheckpointService", "Create")
	}
	return f.CreateFunc(ctx, req)
}

func (f *FakeRepositoryCheckpointService) Verify(ctx context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
	if f.VerifyFunc == nil {
		return app.RepositoryCheckpointVerification{}, errUnconfigured("FakeRepositoryCheckpointService", "Verify")
	}
	return f.VerifyFunc(ctx, id)
}

func (f *FakeRepositoryCheckpointService) Restore(ctx context.Context, req app.RestoreRepositoryCheckpointRequest) (app.RestoreResult, error) {
	if f.RestoreFunc == nil {
		return app.RestoreResult{}, errUnconfigured("FakeRepositoryCheckpointService", "Restore")
	}
	return f.RestoreFunc(ctx, req)
}
