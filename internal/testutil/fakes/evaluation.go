package fakes

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// FakeEvaluationService is a configurable test double for the frozen
// app.EvaluationService contract (evaluate/decide/authorize, ADD §9.9).
// See the package doc for the Func-field pattern and nil-Func behavior.
type FakeEvaluationService struct {
	EvaluateTurnFunc         func(ctx context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error)
	GetEvaluationFunc        func(ctx context.Context, id domain.EvaluationID) (app.Evaluation, error)
	DecideFunc               func(ctx context.Context, req app.DecideRequest) (app.DecisionResult, error)
	ConsumeAuthorizationFunc func(ctx context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error)
}

var _ app.EvaluationService = (*FakeEvaluationService)(nil)

func (f *FakeEvaluationService) EvaluateTurn(ctx context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
	if f.EvaluateTurnFunc == nil {
		return app.Evaluation{}, errUnconfigured("FakeEvaluationService", "EvaluateTurn")
	}
	return f.EvaluateTurnFunc(ctx, req)
}

func (f *FakeEvaluationService) GetEvaluation(ctx context.Context, id domain.EvaluationID) (app.Evaluation, error) {
	if f.GetEvaluationFunc == nil {
		return app.Evaluation{}, errUnconfigured("FakeEvaluationService", "GetEvaluation")
	}
	return f.GetEvaluationFunc(ctx, id)
}

func (f *FakeEvaluationService) Decide(ctx context.Context, req app.DecideRequest) (app.DecisionResult, error) {
	if f.DecideFunc == nil {
		return app.DecisionResult{}, errUnconfigured("FakeEvaluationService", "Decide")
	}
	return f.DecideFunc(ctx, req)
}

func (f *FakeEvaluationService) ConsumeAuthorization(ctx context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
	if f.ConsumeAuthorizationFunc == nil {
		return app.Authorization{}, errUnconfigured("FakeEvaluationService", "ConsumeAuthorization")
	}
	return f.ConsumeAuthorizationFunc(ctx, req)
}
