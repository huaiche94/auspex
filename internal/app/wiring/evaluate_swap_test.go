// evaluate_swap_test.go: issue #14's wiring proof that App.RootCmd()
// swaps root.go's `evaluate` stub for cli.NewEvaluateCmd's real handler
// (the same stub-then-swap assertion style
// TestApp_RootCmd_HookClaudeIsRealNotStub established for the hook
// subtree).
package wiring_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/app/wiring"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// TestApp_RootCmd_EvaluateIsRealNotStub drives `auspex evaluate
// --session-id ...` on the App-built tree against a configured
// evaluation fake and expects the REAL handler's success output —
// not root.go's ErrCodeUnavailable "not yet implemented" stub (which
// would also reject the --session-id flag outright, since the stub
// declares no flags).
func TestApp_RootCmd_EvaluateIsRealNotStub(t *testing.T) {
	services := fullFakeServices()
	services.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-wired", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyRun}, nil
		},
	}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"evaluate", "--session-id", "sess-1"})

	if err := root.Execute(); err != nil {
		t.Fatalf("evaluate on the wired tree: %v (want the real handler, not the stub)", err)
	}
	if !strings.Contains(out.String(), "eval-wired") {
		t.Errorf("output missing the evaluation ID from the real handler:\n%s", out.String())
	}
	// No ForecastCardSource wired (Hooks.Forecast zero value) — the real
	// handler must degrade to the card-unavailable line, never error.
	if !strings.Contains(out.String(), "forecast card unavailable") {
		t.Errorf("output should name the degraded (no ForecastCardSource) card state:\n%s", out.String())
	}
}
