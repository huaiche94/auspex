package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// fakeAuthorizationIssuer is this file's own minimal local test double for
// orchestrator.AuthorizationIssuer — this package's own narrow interface
// (decision.go), not one of internal/testutil/fakes' frozen-port doubles,
// mirroring evaluate_test.go's fakeObservationLoader/fakeGitSnapshotter
// precedent for the same reason (a package-local interface gets a
// package-local fake, not a shared one).
type fakeAuthorizationIssuer struct {
	issueFunc func(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error)
}

func (f *fakeAuthorizationIssuer) IssueAuthorization(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
	return f.issueFunc(ctx, turnID, promptHash, snapshotFingerprint, decision, repoCheckpointID)
}

// --- DecisionAllowCmd: structural / validation coverage ---------------------
//
// These tests exercise DecisionAllowCmd's OWN orchestration logic (nil-dep
// guards, flow selection, request validation) against lightweight fakes —
// they do not touch authorization semantics (exactly-once consumption,
// replay rejection, expiry, binding), which is deliberately NOT fake-able
// per the DAG's hard-dependency note and is instead proven against the
// REAL internal/evaluation.Service in decision_realauth_test.go.

func TestDecisionAllowCmd_NilEvaluationServiceFailsClosed(t *testing.T) {
	_, err := orchestrator.DecisionAllowCmd(context.Background(), orchestrator.DecisionDeps{}, orchestrator.DecisionAllowRequest{
		EvaluationID: "eval-1", TurnID: "turn-1",
	})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

func TestDecisionAllowCmd_RequiresTurnID(t *testing.T) {
	deps := orchestrator.DecisionDeps{Evaluation: &fakes.FakeEvaluationService{}}
	_, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{EvaluationID: "eval-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

func TestDecisionAllowCmd_IssueFlow_RequiresEvaluationID(t *testing.T) {
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{},
		Issuer:     &fakeAuthorizationIssuer{},
	}
	_, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{TurnID: "turn-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

func TestDecisionAllowCmd_IssueFlow_NilIssuerFailsClosed(t *testing.T) {
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(context.Context, app.DecideRequest) (app.DecisionResult, error) {
				return app.DecisionResult{ID: "dec-1", Action: app.PolicyRequireConfirmation}, nil
			},
		},
	}
	_, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{
		EvaluationID: "eval-1", TurnID: "turn-1",
	})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable (issue flow requires a non-nil Issuer)", err)
	}
}

func TestDecisionAllowCmd_IssueFlow_CallsDecideThenIssuer_InOrder(t *testing.T) {
	var callOrder []string
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(_ context.Context, req app.DecideRequest) (app.DecisionResult, error) {
				callOrder = append(callOrder, "decide")
				if req.EvaluationID != "eval-1" {
					t.Errorf("Decide EvaluationID = %q, want eval-1", req.EvaluationID)
				}
				return app.DecisionResult{ID: "dec-1", Action: app.PolicyRequireConfirmation}, nil
			},
		},
		Issuer: &fakeAuthorizationIssuer{
			issueFunc: func(_ context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
				callOrder = append(callOrder, "issue")
				if turnID != "turn-1" {
					t.Errorf("IssueAuthorization turnID = %q, want turn-1", turnID)
				}
				if decision != string(app.PolicyRequireConfirmation) {
					t.Errorf("IssueAuthorization decision = %q, want %q", decision, app.PolicyRequireConfirmation)
				}
				return app.Authorization{ID: "auth-1", TurnID: turnID}, nil
			},
		},
	}

	result, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{
		EvaluationID: "eval-1", TurnID: "turn-1", PromptHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd: %v", err)
	}
	if len(callOrder) != 2 || callOrder[0] != "decide" || callOrder[1] != "issue" {
		t.Fatalf("call order = %v, want [decide, issue]", callOrder)
	}
	if !result.Issued || result.Consumed {
		t.Fatalf("result = %+v, want Issued=true Consumed=false", result)
	}
	if result.Authorization.ID != "auth-1" {
		t.Fatalf("Authorization.ID = %q, want auth-1", result.Authorization.ID)
	}
}

func TestDecisionAllowCmd_IssueFlow_DecideErrorPropagatesNeverCallsIssuer(t *testing.T) {
	wantErr := &domain.Error{Code: domain.ErrCodeIntegrity, Message: "boom", Retryable: false}
	issuerCalled := false
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(context.Context, app.DecideRequest) (app.DecisionResult, error) {
				return app.DecisionResult{}, wantErr
			},
		},
		Issuer: &fakeAuthorizationIssuer{
			issueFunc: func(context.Context, domain.TurnID, string, string, string, *domain.RepositoryCheckpointID) (app.Authorization, error) {
				issuerCalled = true
				return app.Authorization{}, nil
			},
		},
	}
	_, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{
		EvaluationID: "eval-1", TurnID: "turn-1",
	})
	if !errors.Is(err, error(wantErr)) {
		t.Fatalf("err = %v, want the exact Decide error propagated", err)
	}
	if issuerCalled {
		t.Fatal("IssueAuthorization was called despite Decide failing — checkpoint/decide failure must never issue an authorization")
	}
}

// TestDecisionAllowCmd_ConsumeFlow_SelectedWhenAuthorizationIDPresent proves
// flow SELECTION: supplying AuthorizationID routes to ConsumeAuthorization
// and never touches Decide/Issuer at all (the resubmission does not
// re-derive a new decision or a new authorization — see decision.go's
// package comment).
func TestDecisionAllowCmd_ConsumeFlow_SelectedWhenAuthorizationIDPresent(t *testing.T) {
	decideCalled := false
	issuerCalled := false
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(context.Context, app.DecideRequest) (app.DecisionResult, error) {
				decideCalled = true
				return app.DecisionResult{}, nil
			},
			ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
				if req.AuthorizationID != "auth-1" || req.TurnID != "turn-1" || req.PromptHash != "hash-1" {
					t.Errorf("ConsumeAuthorization request mismatch: %+v", req)
				}
				return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
			},
		},
		Issuer: &fakeAuthorizationIssuer{
			issueFunc: func(context.Context, domain.TurnID, string, string, string, *domain.RepositoryCheckpointID) (app.Authorization, error) {
				issuerCalled = true
				return app.Authorization{}, nil
			},
		},
	}

	result, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{
		TurnID: "turn-1", PromptHash: "hash-1", AuthorizationID: "auth-1",
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd: %v", err)
	}
	if decideCalled || issuerCalled {
		t.Fatalf("consume flow called Decide (%v) or Issuer (%v) — it must call neither", decideCalled, issuerCalled)
	}
	if !result.Consumed || result.Issued {
		t.Fatalf("result = %+v, want Consumed=true Issued=false", result)
	}
	if result.Authorization.ID != "auth-1" {
		t.Fatalf("Authorization.ID = %q, want auth-1", result.Authorization.ID)
	}
}

func TestDecisionAllowCmd_ConsumeFlow_ErrorPropagatesFailClosed(t *testing.T) {
	wantErr := &domain.Error{Code: domain.ErrCodeConflict, Message: "evaluation: authorization has already been consumed", Retryable: false}
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			ConsumeAuthorizationFunc: func(context.Context, app.ConsumeAuthorizationRequest) (app.Authorization, error) {
				return app.Authorization{}, wantErr
			},
		},
	}
	_, err := orchestrator.DecisionAllowCmd(context.Background(), deps, orchestrator.DecisionAllowRequest{
		TurnID: "turn-1", AuthorizationID: "auth-1",
	})
	if !errors.Is(err, error(wantErr)) {
		t.Fatalf("err = %v, want the exact ConsumeAuthorization error propagated", err)
	}
}

// --- DecisionDenyCmd ---------------------------------------------------------

func TestDecisionDenyCmd_NilEvaluationServiceFailsClosed(t *testing.T) {
	_, err := orchestrator.DecisionDenyCmd(context.Background(), orchestrator.DecisionDeps{}, orchestrator.DecisionDenyRequest{EvaluationID: "eval-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

func TestDecisionDenyCmd_RequiresEvaluationID(t *testing.T) {
	deps := orchestrator.DecisionDeps{Evaluation: &fakes.FakeEvaluationService{}}
	_, err := orchestrator.DecisionDenyCmd(context.Background(), deps, orchestrator.DecisionDenyRequest{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

func TestDecisionDenyCmd_ReadsBackDecisionViaDecide(t *testing.T) {
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(_ context.Context, req app.DecideRequest) (app.DecisionResult, error) {
				if req.EvaluationID != "eval-1" {
					t.Errorf("Decide EvaluationID = %q, want eval-1", req.EvaluationID)
				}
				return app.DecisionResult{ID: "dec-1", Action: app.PolicyBlock}, nil
			},
		},
	}
	result, err := orchestrator.DecisionDenyCmd(context.Background(), deps, orchestrator.DecisionDenyRequest{EvaluationID: "eval-1"})
	if err != nil {
		t.Fatalf("DecisionDenyCmd: %v", err)
	}
	if result.Decision.Action != app.PolicyBlock {
		t.Fatalf("Decision.Action = %q, want %q", result.Decision.Action, app.PolicyBlock)
	}
}

func TestDecisionDenyCmd_DecideErrorPropagates(t *testing.T) {
	wantErr := &domain.Error{Code: domain.ErrCodeNotFound, Message: "no such evaluation", Retryable: false}
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(context.Context, app.DecideRequest) (app.DecisionResult, error) {
				return app.DecisionResult{}, wantErr
			},
		},
	}
	_, err := orchestrator.DecisionDenyCmd(context.Background(), deps, orchestrator.DecisionDenyRequest{EvaluationID: "eval-1"})
	if !errors.Is(err, error(wantErr)) {
		t.Fatalf("err = %v, want the exact Decide error propagated", err)
	}
}
