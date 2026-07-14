// forecast_hooks_test.go: issue #14's hook-surface tests — the
// UserPromptSubmit response gains the forecast card as additionalContext
// when everything succeeds, degrades to EXACTLY the pre-issue-#14
// response on any presenter failure (fail-open, never a new hook failure
// mode), the minted TurnID now links the persisted turn.started event to
// the evaluation, the statusline --emit-line composition renders in every
// degradation state, and EvaluatePrompt (the `auspex evaluate` core)
// shares the same path with a fail-closed posture.
package orchestrator_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// fakeForecastSource is a minimal orchestrator.ForecastCardSource double
// (the interface is deliberately satisfied in production only by the
// real *evaluation.Service — see hooks.go — so tests fake it locally,
// mirroring errorcontract_test.go's fakeAuthIssuer precedent for
// decision.go's AuthorizationIssuer).
type fakeForecastSource struct {
	card      evaluation.ForecastCard
	err       error
	latestOK  bool
	latestErr error

	gotEvaluationID domain.EvaluationID
	gotSessionID    domain.SessionID
}

func (f *fakeForecastSource) ForecastCard(_ context.Context, id domain.EvaluationID) (evaluation.ForecastCard, error) {
	f.gotEvaluationID = id
	if f.err != nil {
		return evaluation.ForecastCard{}, f.err
	}
	return f.card, nil
}

func (f *fakeForecastSource) LatestForecastCard(_ context.Context, sessionID domain.SessionID) (evaluation.ForecastCard, bool, error) {
	f.gotSessionID = sessionID
	if f.latestErr != nil {
		return evaluation.ForecastCard{}, false, f.latestErr
	}
	return f.card, f.latestOK, nil
}

func testForecastCard() evaluation.ForecastCard {
	p50, p80, p90 := int64(8000), int64(20000), int64(45000)
	return evaluation.ForecastCard{
		EvaluationID: "eval-1",
		TurnID:       "turn-1",
		TokensP50:    &p50, TokensP80: &p80, TokensP90: &p90,
		Cost:             &pricing.CostRange{LowUSD: 0.02, HighUSD: 0.68, ModelFamily: pricing.DefaultFamily, Source: pricing.SourceDefaultTable},
		OverallRiskScore: 0.42,
		Confidence:       domain.ConfidenceLow,
		PolicyAction:     app.PolicyWarn,
	}
}

func evaluationServiceFake(action app.PolicyAction) *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: action}, nil
		},
	}
}

// --- UserPromptSubmit: additionalContext -------------------------------

func TestHookHandlers_UserPromptSubmit_AdditionalContextPresentOnSuccess(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = evaluationServiceFake(app.PolicyRun)
	forecast := &fakeForecastSource{card: testForecastCard()}
	deps.Forecast = forecast

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
	ac := result.Response.AdditionalContext
	if ac == "" {
		t.Fatal("AdditionalContext is empty, want the forecast card block (issue #14 deliverable 3)")
	}
	for _, want := range []string{"uncalibrated estimate", "P50 8000", "~$0.02", "policy: WARN"} {
		if !strings.Contains(ac, want) {
			t.Errorf("AdditionalContext missing %q:\n%s", want, ac)
		}
	}
	if forecast.gotEvaluationID != "eval-1" {
		t.Errorf("ForecastCard called with evaluation %q, want eval-1", forecast.gotEvaluationID)
	}

	// The wire encoding must carry it via hookSpecificOutput.
	body, err := claudehooks.EncodeUserPromptSubmitResponse(result.Response)
	if err != nil {
		t.Fatalf("EncodeUserPromptSubmitResponse: %v", err)
	}
	for _, want := range []string{`"hookSpecificOutput"`, `"hookEventName":"UserPromptSubmit"`, `"additionalContext"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("encoded response missing %s: %s", want, body)
		}
	}
}

// TestHookHandlers_UserPromptSubmit_ForecastErrorDegradesToTodayResponse:
// a presenter failure must degrade to exactly the pre-issue-#14 response
// (plain allow, no additionalContext, nil error) — the card is never a
// new way for the hook to fail.
func TestHookHandlers_UserPromptSubmit_ForecastErrorDegradesToTodayResponse(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = evaluationServiceFake(app.PolicyRun)
	deps.Forecast = &fakeForecastSource{err: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "card store down"}}

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit should fail open on a forecast error, got: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
	if result.Response.AdditionalContext != "" {
		t.Errorf("AdditionalContext = %q, want empty (degraded to today's response)", result.Response.AdditionalContext)
	}
	if !result.Evaluated {
		t.Error("Evaluated = false, want true — the evaluation itself succeeded; only the card degraded")
	}
}

// TestHookHandlers_UserPromptSubmit_NoForecastSource_IsTodayResponse: nil
// Forecast is the documented degrade (same convention as nil Persister/
// Correlator) — byte-identical to the pre-issue-#14 allow response.
func TestHookHandlers_UserPromptSubmit_NoForecastSource_IsTodayResponse(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = evaluationServiceFake(app.PolicyRun)

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response != (claudehooks.UserPromptSubmitResponse{Decision: claudehooks.HookDecisionAllow}) {
		t.Errorf("Response = %+v, want the plain allow response", result.Response)
	}
}

// TestHookHandlers_UserPromptSubmit_BlockKeepsAdditionalContext: the
// block/allow semantics are unchanged, and a block response still carries
// the card so the agent sees WHY alongside the block reason.
func TestHookHandlers_UserPromptSubmit_BlockKeepsAdditionalContext(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = evaluationServiceFake(app.PolicyBlock)
	card := testForecastCard()
	card.PolicyAction = app.PolicyBlock
	deps.Forecast = &fakeForecastSource{card: card}

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionBlock {
		t.Errorf("Decision = %q, want block", result.Response.Decision)
	}
	if result.Response.Reason == "" {
		t.Error("Reason is empty on a block decision")
	}
	if !strings.Contains(result.Response.AdditionalContext, "policy: BLOCK") {
		t.Errorf("block response should still carry the card:\n%q", result.Response.AdditionalContext)
	}
}

// TestHookHandlers_UserPromptSubmit_EventCarriesEvaluationTurnID: the
// linkage LatestForecastCard's statusline query joins on — one minted
// TurnID stamped on BOTH the persisted provider.turn.started event and
// the EvaluateTurn request.
func TestHookHandlers_UserPromptSubmit_EventCarriesEvaluationTurnID(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	var evaluatedTurnID domain.TurnID
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			evaluatedTurnID = req.TurnID
			return app.Evaluation{ID: "eval-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyRun}, nil
		},
	}

	_, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if len(persister.calls) != 1 || len(persister.calls[0]) != 1 {
		t.Fatalf("persister.calls = %v, want one call with one event", persister.calls)
	}
	ev := persister.calls[0][0]
	if ev.TurnID == "" {
		t.Fatal("persisted turn.started event has no TurnID — the session -> prediction linkage (LatestForecastCard) depends on it")
	}
	if domain.TurnID(ev.TurnID) != evaluatedTurnID {
		t.Errorf("event TurnID %q != EvaluateTurn TurnID %q — must be the same minted ID", ev.TurnID, evaluatedTurnID)
	}
}

// TestHookHandlers_HighContextCardRendersThresholdState (ADR-043
// increment 2, D-08): a card carrying a persisted context projection with
// a recorded warn-threshold state renders it on both hook surfaces — the
// UserPromptSubmit additionalContext block and the statusline emit-line —
// so the agent and the status bar both see WHY the context resource is
// policy-active.
func TestHookHandlers_HighContextCardRendersThresholdState(t *testing.T) {
	card := testForecastCard()
	proj := 91.0
	card.ContextProjectedP90 = &proj
	card.ContextWarnThresholdExceeded = true

	deps := baseHookDeps()
	deps.Evaluation = evaluationServiceFake(app.PolicyWarn)
	deps.Forecast = &fakeForecastSource{card: card, latestOK: true}

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if ac := result.Response.AdditionalContext; !strings.Contains(ac, "context: P90 ~91% of window (projected) — WARN threshold exceeded") {
		t.Errorf("AdditionalContext missing the context threshold line:\n%s", ac)
	}

	_, line, err := orchestrator.HandleStatusLineEmitLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStatusLineEmitLine: %v", err)
	}
	if !strings.Contains(line, "ctx P90 ~91% (warn)") {
		t.Errorf("emit-line = %q, want the ctx segment with the warn marker", line)
	}
}

// --- statusline --emit-line ---------------------------------------------

func TestHookHandlers_StatusLineEmitLine_ModelOnlyWhenNoForecast(t *testing.T) {
	deps := baseHookDeps() // no Forecast wired
	result, line, err := orchestrator.HandleStatusLineEmitLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStatusLineEmitLine: %v", err)
	}
	if result.EventsNormalized != 4 {
		t.Errorf("EventsNormalized = %d, want 4 (ingest identical to HandleStatusLine)", result.EventsNormalized)
	}
	// normal.json's model.display_name is "Opus 4.1".
	if line != statusBrand+" Opus 4.1" {
		t.Errorf("line = %q, want %q", line, statusBrand+" Opus 4.1")
	}
}

// Byte-exact ANSI pins for the statusline surface (issue #29), written
// out explicitly rather than imported from the renderer so a regression
// cannot rewrite its own expectations.
const (
	statusReset = "\x1b[0m"
	statusBrand = "\x1b[36max✈" + statusReset
	statusSep   = "\x1b[2m │ " + statusReset
)

func TestHookHandlers_StatusLineEmitLine_WithLatestForecast(t *testing.T) {
	deps := baseHookDeps()
	forecast := &fakeForecastSource{card: testForecastCard(), latestOK: true}
	deps.Forecast = forecast

	_, line, err := orchestrator.HandleStatusLineEmitLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStatusLineEmitLine: %v", err)
	}
	if want := statusBrand + " Opus 4.1" + statusSep + "🔮 est P50 8000tok ~$0.02–0.68" + statusSep + "\x1b[33m⚠ WARN" + statusReset; line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
	if forecast.gotSessionID == "" {
		t.Error("LatestForecastCard was not consulted with the snapshot's session ID")
	}
}

func TestHookHandlers_StatusLineEmitLine_ColdStartAndErrorDegradeToModelOnly(t *testing.T) {
	for name, forecast := range map[string]*fakeForecastSource{
		"cold start": {latestOK: false},
		"card error": {latestErr: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "down"}},
	} {
		deps := baseHookDeps()
		deps.Forecast = forecast
		_, line, err := orchestrator.HandleStatusLineEmitLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
		if err != nil {
			t.Fatalf("%s: HandleStatusLineEmitLine: %v", name, err)
		}
		if line != statusBrand+" Opus 4.1" {
			t.Errorf("%s: line = %q, want model-only fallback", name, line)
		}
	}
}

func TestHookHandlers_StatusLineEmitLine_MalformedInputStillEmitsLine(t *testing.T) {
	deps := baseHookDeps()
	result, line, err := orchestrator.HandleStatusLineEmitLine(context.Background(), deps, readFixture(t, "statusline", "malformed.json"))
	if err != nil {
		t.Fatalf("HandleStatusLineEmitLine on malformed input should fail open, got: %v", err)
	}
	if result.EventsNormalized != 0 {
		t.Errorf("EventsNormalized = %d, want 0", result.EventsNormalized)
	}
	if line != statusBrand {
		t.Errorf("line = %q, want the bare fallback %q — a status line must keep rendering", line, statusBrand)
	}
}

// --- EvaluatePrompt (the `auspex evaluate` core) ----------------------

func TestEvaluatePrompt_SharesHookPathAndReturnsCard(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	var gotHash string
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			gotHash = req.PromptHash
			return app.Evaluation{ID: "eval-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyWarn}, nil
		},
	}
	deps.Forecast = &fakeForecastSource{card: testForecastCard()}

	const rawPrompt = "Refactor the checkpoint manifest writer to use atomic rename."
	result, err := orchestrator.EvaluatePrompt(context.Background(), deps, orchestrator.EvaluatePromptRequest{
		SessionID: "sess-1",
		Prompt:    rawPrompt,
	})
	if err != nil {
		t.Fatalf("EvaluatePrompt: %v", err)
	}

	// The hash must be the SAME derivation the hook applies to the same
	// prompt text (claudehooks.NewUserPromptSubmitEvent is shared by
	// ParseUserPromptSubmit) — offline and hook evaluations of one prompt
	// are indistinguishable downstream.
	want := claudehooks.NewUserPromptSubmitEvent("sess-1", rawPrompt).PromptSHA256
	if gotHash != want {
		t.Errorf("PromptHash = %q, want the hook-path derivation %q", gotHash, want)
	}
	if strings.Contains(gotHash, "Refactor") {
		t.Fatal("raw prompt text leaked into the PromptHash field")
	}
	if result.Card == nil {
		t.Fatal("Card = nil, want the forecast card")
	}
	if result.Decision.Action != app.PolicyWarn {
		t.Errorf("Decision.Action = %q, want WARN", result.Decision.Action)
	}
	if !result.Persisted {
		t.Error("Persisted = false, want true (Persister+TxRunner wired)")
	}
	// The persisted event must carry the hash-only payload and the minted
	// turn ID — never raw text.
	ev := persister.calls[0][0]
	if domain.TurnID(ev.TurnID) != result.Evaluation.TurnID {
		t.Errorf("event TurnID %q != evaluation TurnID %q", ev.TurnID, result.Evaluation.TurnID)
	}
	for k, v := range ev.Payload {
		if s, ok := v.(string); ok && strings.Contains(s, "Refactor") {
			t.Errorf("raw prompt text leaked into persisted event payload field %q", k)
		}
	}
}

func TestEvaluatePrompt_FailsClosed(t *testing.T) {
	t.Run("missing session id", func(t *testing.T) {
		deps := baseHookDeps()
		deps.Evaluation = evaluationServiceFake(app.PolicyRun)
		_, err := orchestrator.EvaluatePrompt(context.Background(), deps, orchestrator.EvaluatePromptRequest{Prompt: "x"})
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
			t.Fatalf("err = %v, want ErrCodeValidation", err)
		}
	})
	t.Run("no evaluation service", func(t *testing.T) {
		deps := baseHookDeps()
		_, err := orchestrator.EvaluatePrompt(context.Background(), deps, orchestrator.EvaluatePromptRequest{SessionID: "sess-1"})
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
			t.Fatalf("err = %v, want ErrCodeUnavailable — a CLI evaluation fails closed, unlike the hook", err)
		}
	})
	t.Run("evaluation error propagates", func(t *testing.T) {
		deps := baseHookDeps()
		deps.Evaluation = &fakes.FakeEvaluationService{
			EvaluateTurnFunc: func(_ context.Context, _ app.EvaluateTurnRequest) (app.Evaluation, error) {
				return app.Evaluation{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "predictor down"}
			},
		}
		if _, err := orchestrator.EvaluatePrompt(context.Background(), deps, orchestrator.EvaluatePromptRequest{SessionID: "sess-1"}); err == nil {
			t.Fatal("expected the EvaluateTurn error to propagate (fail-closed)")
		}
	})
}

// TestEvaluatePrompt_CardErrorDegradesToNilCard: the presenter degrade is
// the ONLY soft failure — the evaluation result still returns.
func TestEvaluatePrompt_CardErrorDegradesToNilCard(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = evaluationServiceFake(app.PolicyRun)
	deps.Forecast = &fakeForecastSource{err: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "down"}}

	result, err := orchestrator.EvaluatePrompt(context.Background(), deps, orchestrator.EvaluatePromptRequest{SessionID: "sess-1", Prompt: "x"})
	if err != nil {
		t.Fatalf("EvaluatePrompt: %v", err)
	}
	if result.Card != nil {
		t.Errorf("Card = %+v, want nil after a card-read failure", result.Card)
	}
	if result.Evaluation.ID != "eval-1" {
		t.Errorf("Evaluation.ID = %q, want eval-1 (the evaluation itself succeeded)", result.Evaluation.ID)
	}
}
