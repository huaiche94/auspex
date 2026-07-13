package fakes

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// FakeGracefulPauseService is a configurable test double for the frozen
// app.GracefulPauseService contract (ADD §20; agents/runtime.md Part A).
// The real implementation is this same role's own later work
// (internal/pause/**, runtime-a02+); until it exists, Part B's wiring and
// orchestration tests run against this double. See the package doc for
// the Func-field pattern and nil-Func behavior.
type FakeGracefulPauseService struct {
	ObserveFunc        func(ctx context.Context, obs app.RuntimeObservation) (domain.RunwayForecast, error)
	RequestPauseFunc   func(ctx context.Context, req app.PauseRequest) (app.PauseRecord, error)
	ReachSafePointFunc func(ctx context.Context, sp app.SafePoint) (app.PauseRecord, error)
	EnterSleepFunc     func(ctx context.Context, id domain.PauseID) (app.WakeJob, error)
	ResumeFunc         func(ctx context.Context, req app.ResumeRequest) (app.ResumeResult, error)
	CancelFunc         func(ctx context.Context, id domain.PauseID) error
}

var _ app.GracefulPauseService = (*FakeGracefulPauseService)(nil)

func (f *FakeGracefulPauseService) Observe(ctx context.Context, obs app.RuntimeObservation) (domain.RunwayForecast, error) {
	if f.ObserveFunc == nil {
		return domain.RunwayForecast{}, errUnconfigured("FakeGracefulPauseService", "Observe")
	}
	return f.ObserveFunc(ctx, obs)
}

func (f *FakeGracefulPauseService) RequestPause(ctx context.Context, req app.PauseRequest) (app.PauseRecord, error) {
	if f.RequestPauseFunc == nil {
		return app.PauseRecord{}, errUnconfigured("FakeGracefulPauseService", "RequestPause")
	}
	return f.RequestPauseFunc(ctx, req)
}

func (f *FakeGracefulPauseService) ReachSafePoint(ctx context.Context, sp app.SafePoint) (app.PauseRecord, error) {
	if f.ReachSafePointFunc == nil {
		return app.PauseRecord{}, errUnconfigured("FakeGracefulPauseService", "ReachSafePoint")
	}
	return f.ReachSafePointFunc(ctx, sp)
}

func (f *FakeGracefulPauseService) EnterSleep(ctx context.Context, id domain.PauseID) (app.WakeJob, error) {
	if f.EnterSleepFunc == nil {
		return app.WakeJob{}, errUnconfigured("FakeGracefulPauseService", "EnterSleep")
	}
	return f.EnterSleepFunc(ctx, id)
}

func (f *FakeGracefulPauseService) Resume(ctx context.Context, req app.ResumeRequest) (app.ResumeResult, error) {
	if f.ResumeFunc == nil {
		return app.ResumeResult{}, errUnconfigured("FakeGracefulPauseService", "Resume")
	}
	return f.ResumeFunc(ctx, req)
}

func (f *FakeGracefulPauseService) Cancel(ctx context.Context, id domain.PauseID) error {
	if f.CancelFunc == nil {
		return errUnconfigured("FakeGracefulPauseService", "Cancel")
	}
	return f.CancelFunc(ctx, id)
}
