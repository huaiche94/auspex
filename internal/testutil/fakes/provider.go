package fakes

import (
	"context"

	"github.com/huaiche94/preflight/internal/app"
)

// FakeTurnInterrupter is a configurable test double for the frozen
// app.TurnInterrupter contract (internal/app/ports.go; ADD §9.10/§20.6
// Phase 4 — the provider stop signal internal/pause's safe-point/
// persist-then-interrupt sequencing depends on). See the package doc for
// the Func-field pattern and nil-Func behavior.
//
// This is runtime-a10's fake half of "Provider interrupter/resumer fake
// contract tests" (agents/runtime.md Part A deliverable 11) — see
// providercontract.go for the reusable contract suite this fake (and any
// future real adapter, e.g. claude-provider's stretch-goal "signal
// interruption... adapter") is expected to pass.
type FakeTurnInterrupter struct {
	InterruptFunc func(ctx context.Context, locator app.RunLocator) error
}

var _ app.TurnInterrupter = (*FakeTurnInterrupter)(nil)

func (f *FakeTurnInterrupter) Interrupt(ctx context.Context, locator app.RunLocator) error {
	if f.InterruptFunc == nil {
		return errUnconfigured("FakeTurnInterrupter", "Interrupt")
	}
	return f.InterruptFunc(ctx, locator)
}

// FakeSessionResumer is a configurable test double for the frozen
// app.SessionResumer contract (internal/app/ports.go; ADD §9.10/§20.5
// "Resuming -> Active: resume started" — the provider session resume/fork/
// bootstrap step). See the package doc for the Func-field pattern and
// nil-Func behavior.
//
// This is runtime-a10's fake half of "Provider interrupter/resumer fake
// contract tests" — see providercontract.go.
type FakeSessionResumer struct {
	ResumeFunc func(ctx context.Context, req app.ResumeProviderRequest) (app.RunHandle, error)
}

var _ app.SessionResumer = (*FakeSessionResumer)(nil)

func (f *FakeSessionResumer) Resume(ctx context.Context, req app.ResumeProviderRequest) (app.RunHandle, error) {
	if f.ResumeFunc == nil {
		return app.RunHandle{}, errUnconfigured("FakeSessionResumer", "Resume")
	}
	return f.ResumeFunc(ctx, req)
}
