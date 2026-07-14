// managedgate_test.go: coverage for managedgate.go's EvaluateManagedPrompt
// (issue #8's `auspex run` gate), reusing hooks_test.go's own doubles
// (baseHookDeps, recordingPersister, noopTxRunner) since the whole point
// of the function is that it rides the same shared evaluateSubmittedPrompt
// core the hook handlers ride.
package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func TestEvaluateManagedPrompt_EvaluatesAndStampsOneTurnID(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	var evaluatedTurn domain.TurnID
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			evaluatedTurn = req.TurnID
			return app.Evaluation{ID: "eval-mg-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-mg-1", Action: app.PolicyWarn}, nil
		},
	}

	cwd := "/tmp/managed-worktree"
	result, err := orchestrator.EvaluateManagedPrompt(context.Background(), deps, orchestrator.ManagedPromptRequest{
		SessionID: "sess-mg",
		Prompt:    "refactor the widget",
		CWD:       &cwd,
	})
	if err != nil {
		t.Fatalf("EvaluateManagedPrompt: %v", err)
	}

	if result.TurnID == "" {
		t.Fatal("TurnID is empty")
	}
	if result.TurnID != evaluatedTurn {
		t.Errorf("TurnID %q differs from the TurnID EvaluateTurn received (%q)", result.TurnID, evaluatedTurn)
	}
	if result.Evaluation.ID != "eval-mg-1" || result.Decision.Action != app.PolicyWarn {
		t.Errorf("result = evaluation %q decision %q, want eval-mg-1/WARN", result.Evaluation.ID, result.Decision.Action)
	}
	if !result.Persisted {
		t.Error("Persisted = false, want true (Persister+TxRunner configured)")
	}

	// The persisted provider.turn.started event carries the SAME TurnID
	// (the linkage the managed runner's terminal events join on) and the
	// managed cwd (the issue-#17 bootstrap input) — and, per Constitution
	// §7 rule 2, a hash rather than the raw prompt.
	if len(persister.calls) != 1 || len(persister.calls[0]) != 1 {
		t.Fatalf("persister.calls = %+v, want one call with one event", persister.calls)
	}
	ev := persister.calls[0][0]
	if ev.EventType != v1.EventProviderTurnStarted {
		t.Fatalf("EventType = %q, want %q", ev.EventType, v1.EventProviderTurnStarted)
	}
	if ev.TurnID != string(result.TurnID) {
		t.Errorf("event TurnID = %q, want %q", ev.TurnID, result.TurnID)
	}
	if ev.Payload["cwd"] != cwd {
		t.Errorf("event payload cwd = %v, want %q", ev.Payload["cwd"], cwd)
	}
	if _, hasHash := ev.Payload["prompt_sha256"]; !hasHash {
		t.Error("event payload has no prompt_sha256")
	}
	for _, v := range ev.Payload {
		if s, ok := v.(string); ok && s == "refactor the widget" {
			t.Fatal("raw prompt text leaked into the persisted event payload")
		}
	}
}

func TestEvaluateManagedPrompt_PipelineError_StillReturnsTurnID(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, _ app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "pipeline down", Retryable: true}
		},
	}

	result, err := orchestrator.EvaluateManagedPrompt(context.Background(), deps, orchestrator.ManagedPromptRequest{
		SessionID: "sess-mg-err",
		Prompt:    "anything",
	})
	if err == nil {
		t.Fatal("err = nil, want the pipeline error passed through (the CALLER owns the fail-open/closed posture)")
	}
	// The degrade contract managedgate.go exists for: TurnID/Persisted
	// stay valid so the runner's terminal events join the already-
	// persisted turn.started event.
	if result.TurnID == "" {
		t.Fatal("TurnID is empty on the error path — the managed degrade path cannot keep one TurnID without it")
	}
	if !result.Persisted {
		t.Error("Persisted = false, want true (turn.started persists before the pipeline runs)")
	}
	if len(persister.calls) != 1 || persister.calls[0][0].TurnID != string(result.TurnID) {
		t.Fatalf("persisted turn.started TurnID mismatch: calls %+v vs result %q", persister.calls, result.TurnID)
	}
}

func TestEvaluateManagedPrompt_EmptySessionID_ValidationError(t *testing.T) {
	_, err := orchestrator.EvaluateManagedPrompt(context.Background(), baseHookDeps(), orchestrator.ManagedPromptRequest{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want *domain.Error with code %q", err, domain.ErrCodeValidation)
	}
}
