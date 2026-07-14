package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- fixtures ----------------------------------------------------------

func readFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "claude", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

// --- deterministic Clock/IDGenerator, mirroring internal/scheduler's fakes -

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type sequentialHookIDs struct{ n int }

func (g *sequentialHookIDs) NewID() string {
	g.n++
	return "id-" + string(rune('0'+g.n))
}

// --- EventPersister test double -----------------------------------------

type recordingPersister struct {
	calls [][]v1.Event
	err   error
}

func (p *recordingPersister) PersistAll(_ context.Context, _ app.TxRunner, evs []v1.Event) error {
	if p.err != nil {
		return p.err
	}
	p.calls = append(p.calls, evs)
	return nil
}

type noopTxRunner struct{}

func (noopTxRunner) WithTx(ctx context.Context, fn app.TxFunc) error { return fn(ctx) }

func baseHookDeps() orchestrator.HookDeps {
	return orchestrator.HookDeps{
		Clock: fixedClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		IDs:   &sequentialHookIDs{},
	}
}

// --- HandleStatusLine ------------------------------------------------------

func TestHookHandlers_StatusLine_NormalizesAllFourEventKinds(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleStatusLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStatusLine: %v", err)
	}
	if result.EventsNormalized != 4 {
		t.Errorf("EventsNormalized = %d, want 4 (context, usage, five_hour, seven_day)", result.EventsNormalized)
	}
	if !result.Persisted {
		t.Error("Persisted = false, want true (Persister+TxRunner both configured)")
	}
	if len(persister.calls) != 1 || len(persister.calls[0]) != 4 {
		t.Fatalf("persister.calls = %v, want one call with 4 events", persister.calls)
	}
}

func TestHookHandlers_StatusLine_MissingFieldsOmitsThoseEvents(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleStatusLine(context.Background(), deps, readFixture(t, "statusline", "missing_fields.json"))
	if err != nil {
		t.Fatalf("HandleStatusLine: %v", err)
	}
	// missing_fields.json is expected to omit at least one of the four
	// normalizable groups (unknown is not zero: absent fields must not
	// synthesize an event that claims to observe them).
	if result.EventsNormalized >= 4 {
		t.Errorf("EventsNormalized = %d, want < 4 for a payload with missing fields", result.EventsNormalized)
	}
}

func TestHookHandlers_StatusLine_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleStatusLine(context.Background(), deps, readFixture(t, "statusline", "malformed.json"))
	if err != nil {
		t.Fatalf("HandleStatusLine on malformed input should fail open (nil error), got: %v", err)
	}
	if result.EventsNormalized != 0 {
		t.Errorf("EventsNormalized = %d, want 0 on malformed input", result.EventsNormalized)
	}
	if result.Persisted {
		t.Error("Persisted = true, want false on malformed input")
	}
}

func TestHookHandlers_StatusLine_NoPersisterConfigured_StillNormalizes(t *testing.T) {
	deps := baseHookDeps() // Persister/TxRunner both nil
	result, err := orchestrator.HandleStatusLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStatusLine: %v", err)
	}
	if result.EventsNormalized != 4 {
		t.Errorf("EventsNormalized = %d, want 4", result.EventsNormalized)
	}
	if result.Persisted {
		t.Error("Persisted = true, want false (no Persister configured)")
	}
}

func TestHookHandlers_StatusLine_PersistFailureDegradesNotAborts(t *testing.T) {
	deps := baseHookDeps()
	deps.Persister = &recordingPersister{err: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "db down"}}
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleStatusLine(context.Background(), deps, readFixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStatusLine should fail open on a persist error, got: %v", err)
	}
	if result.Persisted {
		t.Error("Persisted = true, want false after a persist failure")
	}
	if result.EventsNormalized != 4 {
		t.Errorf("EventsNormalized = %d, want 4 (normalization still happened despite the persist failure)", result.EventsNormalized)
	}
}

// --- HandleUserPromptSubmit -------------------------------------------------

func TestHookHandlers_UserPromptSubmit_NoEvaluationService_AllowsByDefault(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
	if result.Evaluated {
		t.Error("Evaluated = true, want false (no EvaluationService configured)")
	}
	if result.EventsNormalized != 1 {
		t.Errorf("EventsNormalized = %d, want 1", result.EventsNormalized)
	}
}

func TestHookHandlers_UserPromptSubmit_NeverSeesRawPromptText(t *testing.T) {
	deps := baseHookDeps()
	var capturedHash string
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			capturedHash = req.PromptHash
			// The prompt hash must look like a hash (hex), never the raw
			// fixture prompt text ("Refactor the checkpoint manifest...").
			return app.Evaluation{ID: domain.EvaluationID("eval-1"), TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyRun}, nil
		},
	}

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if !result.Evaluated {
		t.Fatal("Evaluated = false, want true")
	}
	if len(capturedHash) != 64 { // SHA-256 hex digest length
		t.Errorf("PromptHash = %q (len %d), want a 64-char hex digest", capturedHash, len(capturedHash))
	}
	for _, r := range capturedHash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("PromptHash %q contains a non-hex character %q — looks like raw text leaked", capturedHash, r)
		}
	}
}

func TestHookHandlers_UserPromptSubmit_PolicyBlockRendersBlockResponse(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: domain.EvaluationID("eval-42"), TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyBlock}, nil
		},
	}

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
}

func TestHookHandlers_UserPromptSubmit_PolicyRunAllowsThrough(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: domain.EvaluationID("eval-1"), TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyRun}, nil
		},
	}
	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
}

func TestHookHandlers_UserPromptSubmit_EvaluationErrorFailsOpenToAllow(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, _ app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "predictor down"}
		},
	}
	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit should fail open on an evaluation error, got: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow (fail-open on evaluation error)", result.Response.Decision)
	}
}

func TestHookHandlers_UserPromptSubmit_MalformedInputFailsOpenToAllow(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "malformed.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit on malformed input should fail open (nil error), got: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
}

// --- HandleStop / HandleStopFailure -----------------------------------------

func TestHookHandlers_Stop_Normalizes(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if result.EventsNormalized != 1 {
		t.Errorf("EventsNormalized = %d, want 1", result.EventsNormalized)
	}
	if !result.Persisted {
		t.Error("Persisted = false, want true")
	}
}

// fakeOpenTurns is a controllable orchestrator.OpenTurnResolver.
type fakeOpenTurns struct {
	turnID domain.TurnID
	ok     bool
	asked  []domain.SessionID
}

func (f *fakeOpenTurns) LatestStartedTurn(_ context.Context, sessionID domain.SessionID) (domain.TurnID, bool) {
	f.asked = append(f.asked, sessionID)
	return f.turnID, f.ok
}

func TestHookHandlers_Stop_StampsLatestStartedTurn(t *testing.T) {
	// Issue #11 turn correlation: the terminal event must carry the
	// session's latest started turn's ID — this is what activates the
	// prediction↔actual join (ADR-046).
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	resolver := &fakeOpenTurns{turnID: "turn-open-1", ok: true}
	deps.OpenTurns = resolver

	if _, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json")); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if len(persister.calls) != 1 || len(persister.calls[0]) != 1 {
		t.Fatalf("persist calls = %+v, want one batch of one event", persister.calls)
	}
	if got := persister.calls[0][0].TurnID; got != "turn-open-1" {
		t.Fatalf("turn.completed TurnID = %q, want the resolved open turn", got)
	}
	if len(resolver.asked) != 1 {
		t.Fatalf("resolver asked %d times, want 1", len(resolver.asked))
	}
}

func TestHookHandlers_Stop_NoResolverOrNoOpenTurn_StampsNothing(t *testing.T) {
	// Unknown is not zero: absent correlation stays an absent TurnID —
	// never a fabricated one — on both the nil-resolver and the
	// ok=false paths (pre-#11 behavior preserved exactly).
	for name, resolver := range map[string]orchestrator.OpenTurnResolver{
		"nil resolver": nil,
		"no open turn": &fakeOpenTurns{ok: false},
	} {
		deps := baseHookDeps()
		persister := &recordingPersister{}
		deps.Persister = persister
		deps.TxRunner = noopTxRunner{}
		deps.OpenTurns = resolver

		if _, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "normal.json")); err != nil {
			t.Fatalf("%s: HandleStop: %v", name, err)
		}
		if got := persister.calls[0][0].TurnID; got != "" {
			t.Fatalf("%s: TurnID = %q, want empty", name, got)
		}
	}
}

func TestHookHandlers_StopFailure_StampsBothEmittedEvents(t *testing.T) {
	// The rate-limit fixture normalizes into TWO events (turn.failed +
	// rate_limit.hit); both belong to the same just-ended turn and both
	// must carry its ID.
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.OpenTurns = &fakeOpenTurns{turnID: "turn-open-2", ok: true}

	if _, err := orchestrator.HandleStopFailure(context.Background(), deps, readFixture(t, "stopfailure", "rate_limit.json")); err != nil {
		t.Fatalf("HandleStopFailure: %v", err)
	}
	events := persister.calls[0]
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (turn.failed + rate_limit.hit)", len(events))
	}
	for _, ev := range events {
		if ev.TurnID != "turn-open-2" {
			t.Fatalf("event %s TurnID = %q, want turn-open-2", ev.EventType, ev.TurnID)
		}
	}
}

func TestHookHandlers_Stop_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleStop(context.Background(), deps, readFixture(t, "stop", "malformed.json"))
	if err != nil {
		t.Fatalf("HandleStop on malformed input should fail open, got: %v", err)
	}
	if result.EventsNormalized != 0 {
		t.Errorf("EventsNormalized = %d, want 0", result.EventsNormalized)
	}
}

func TestHookHandlers_StopFailure_RateLimitEmitsTwoEvents(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleStopFailure(context.Background(), deps, readFixture(t, "stopfailure", "rate_limit.json"))
	if err != nil {
		t.Fatalf("HandleStopFailure: %v", err)
	}
	if result.FailureClass != domain.FailureProviderRateLimit {
		t.Errorf("FailureClass = %q, want %q", result.FailureClass, domain.FailureProviderRateLimit)
	}
	if result.EventsNormalized != 2 {
		t.Errorf("EventsNormalized = %d, want 2 (turn.failed + rate_limit.hit)", result.EventsNormalized)
	}
}

func TestHookHandlers_StopFailure_NetworkEmitsOneEvent(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleStopFailure(context.Background(), deps, readFixture(t, "stopfailure", "network.json"))
	if err != nil {
		t.Fatalf("HandleStopFailure: %v", err)
	}
	if result.FailureClass != domain.FailureNetwork {
		t.Errorf("FailureClass = %q, want %q", result.FailureClass, domain.FailureNetwork)
	}
	if result.EventsNormalized != 1 {
		t.Errorf("EventsNormalized = %d, want 1 (turn.failed only, not a rate limit)", result.EventsNormalized)
	}
}

func TestHookHandlers_StopFailure_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleStopFailure(context.Background(), deps, readFixture(t, "stopfailure", "malformed.json"))
	if err != nil {
		t.Fatalf("HandleStopFailure on malformed input should fail open, got: %v", err)
	}
	if result.EventsNormalized != 0 {
		t.Errorf("EventsNormalized = %d, want 0", result.EventsNormalized)
	}
}
