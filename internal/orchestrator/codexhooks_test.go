// codexhooks_test.go: issue #9 Phase 1 — the codex hook handlers'
// orchestration behavior (fail-open, persistence, gate decisions, rollout
// enrichment), mirroring hooks_test.go's conventions and reusing its
// shared fakes (fixedClock, sequentialHookIDs, recordingPersister,
// noopTxRunner live in that file; this package is one test binary).
package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func readCodexFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "codex", dir, name))
	if err != nil {
		t.Fatalf("reading codex fixture %s/%s: %v", dir, name, err)
	}
	return b
}

// --- HandleCodexSessionStart -------------------------------------------------

func TestCodexSessionStart_NormalizesAndPersists(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleCodexSessionStart(context.Background(), deps, readCodexFixture(t, "sessionstart", "normal.json"))
	if err != nil {
		t.Fatalf("HandleCodexSessionStart: %v", err)
	}
	if result.EventsNormalized != 1 || !result.Persisted {
		t.Errorf("result = %+v, want 1 normalized + persisted", result)
	}
	if len(persister.calls) != 1 || len(persister.calls[0]) != 1 {
		t.Fatalf("persister.calls = %v", persister.calls)
	}
	ev := persister.calls[0][0]
	if ev.EventType != v1.EventProviderSessionStarted {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if ev.Provider != "codex" {
		t.Errorf("Provider = %q", ev.Provider)
	}
}

func TestCodexSessionStart_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleCodexSessionStart(context.Background(), deps, readCodexFixture(t, "sessionstart", "malformed.json"))
	if err != nil {
		t.Fatalf("malformed input must fail open, got: %v", err)
	}
	if result.EventsNormalized != 0 || result.Persisted {
		t.Errorf("result = %+v, want zero on malformed input", result)
	}
}

// --- HandleCodexUserPromptSubmit ----------------------------------------------

func TestCodexUserPromptSubmit_NoEvaluationService_AllowsWithoutOpinion(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleCodexUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != codexhooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
	if result.Evaluated {
		t.Error("Evaluated = true with no EvaluationService wired")
	}
	if !result.Persisted {
		t.Error("Persisted = false; telemetry must land even without evaluation")
	}
	// The persisted turn.started must carry codex's own turn_id.
	ev := persister.calls[0][0]
	if ev.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
		t.Errorf("event TurnID = %q, want the provider's turn_id", ev.TurnID)
	}
}

func TestCodexUserPromptSubmit_BlockDecisionRendered(t *testing.T) {
	deps := baseHookDeps()
	var gotReq app.EvaluateTurnRequest
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			gotReq = req
			return app.Evaluation{ID: "eval-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyBlock}, nil
		},
	}

	result, err := orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleCodexUserPromptSubmit: %v", err)
	}
	if !result.Evaluated {
		t.Error("Evaluated = false, want true")
	}
	if result.Response.Decision != codexhooks.HookDecisionBlock {
		t.Fatalf("Decision = %q, want block", result.Response.Decision)
	}
	if !strings.Contains(result.Response.Reason, "eval-1") {
		t.Errorf("Reason = %q, want the evaluation id", result.Response.Reason)
	}
	// The evaluation ran under the codex provider and the provider's turn id.
	if gotReq.Provider != "codex" {
		t.Errorf("EvaluateTurn Provider = %q, want codex", gotReq.Provider)
	}
	if gotReq.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
		t.Errorf("EvaluateTurn TurnID = %q, want the provider's turn_id", gotReq.TurnID)
	}
	if gotReq.PromptHash == "" {
		t.Error("EvaluateTurn PromptHash empty")
	}

	// The rendered block body is codex-wire-valid and matches the pinned
	// golden shape (modulo the fixture's fixed additionalContext, absent
	// here because no Forecast source is wired).
	body, encErr := codexhooks.EncodeUserPromptSubmitResponse(result.Response)
	if encErr != nil {
		t.Fatalf("encode: %v", encErr)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("block body not valid JSON: %v", err)
	}
	if decoded["decision"] != "block" || decoded["reason"] == "" {
		t.Errorf("block body = %s, want decision=block with a reason (codex requires reason on block)", body)
	}
}

func TestCodexUserPromptSubmit_EvaluationFailureFailsOpenToAllow(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, _ app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "predictor down"}
		},
	}
	result, err := orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("evaluation failure must fail open, got: %v", err)
	}
	if result.Response.Decision != codexhooks.HookDecisionAllow || result.Evaluated {
		t.Errorf("result = %+v, want un-evaluated allow", result)
	}
}

func TestCodexUserPromptSubmit_MalformedInputFailsOpenToAllow(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "malformed.json"))
	if err != nil {
		t.Fatalf("malformed input must fail open, got: %v", err)
	}
	if result.Response.Decision != codexhooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
}

func TestCodexUserPromptSubmit_MissingTurnID_MintsOne(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	var gotTurn domain.TurnID
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			gotTurn = req.TurnID
			return app.Evaluation{ID: "eval-2", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyRun}, nil
		},
	}

	// missing_fields.json carries no turn_id.
	result, err := orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "missing_fields.json"))
	if err != nil {
		t.Fatalf("HandleCodexUserPromptSubmit: %v", err)
	}
	if !result.Evaluated {
		t.Fatal("Evaluated = false")
	}
	if gotTurn == "" {
		t.Error("EvaluateTurn received an empty TurnID; a minted one is required")
	}
	if got := persister.calls[0][0].TurnID; got != string(gotTurn) {
		t.Errorf("persisted event TurnID = %q, want the minted %q (event/prediction linkage)", got, gotTurn)
	}
}

// --- HandleCodexStop -----------------------------------------------------------

// codexStopPayload builds a Stop stdin whose transcript_path points at the
// given rollout file (the fixtures' baked-in path is a fictional host path,
// deliberately unreadable — production resolves whatever the payload says).
func codexStopPayload(t *testing.T, transcriptPath string) []byte {
	t.Helper()
	payload := map[string]any{
		"session_id":       "019f0000-1111-7aaa-8bbb-ccccdddd0001",
		"hook_event_name":  "Stop",
		"cwd":              "/home/dev/projects/sample",
		"model":            "gpt-5.2-codex",
		"permission_mode":  "default",
		"turn_id":          "019f0000-2222-7aaa-8bbb-ccccdddd0101",
		"stop_hook_active": false,
	}
	if transcriptPath != "" {
		payload["transcript_path"] = transcriptPath
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func codexRolloutFixture(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "provider-events", "codex", "rollout", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestCodexStop_RolloutViaTranscriptPath_FullEventSet(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	stdin := codexStopPayload(t, codexRolloutFixture(t, "normal.jsonl"))
	result, err := orchestrator.HandleCodexStop(context.Background(), deps, stdin)
	if err != nil {
		t.Fatalf("HandleCodexStop: %v", err)
	}
	if !result.UsageExtracted {
		t.Fatal("UsageExtracted = false, want the rollout snapshot")
	}
	if result.EventsNormalized != 4 {
		t.Fatalf("EventsNormalized = %d, want 4 (turn.completed + context + 2 quota)", result.EventsNormalized)
	}
	events := persister.calls[0]
	if events[0].EventType != v1.EventProviderTurnCompleted {
		t.Errorf("event[0] = %q", events[0].EventType)
	}
	if events[0].Payload["total_tokens"] != int64(42738-30976+1636) {
		t.Errorf("total_tokens = %v", events[0].Payload["total_tokens"])
	}
	for _, ev := range events {
		if ev.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
			t.Errorf("event %s TurnID = %q, want the provider's turn_id", ev.EventType, ev.TurnID)
		}
	}
}

func TestCodexStop_TranscriptPathNull_FallsBackToSessionsScan(t *testing.T) {
	// Isolated CODEX_HOME with the sessions-layout copy of the fixture.
	home := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "07", "14")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(codexRolloutFixture(t, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(day, "rollout-2026-07-14T09-00-00-019f0000-1111-7aaa-8bbb-ccccdddd0001.jsonl")
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)

	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleCodexStop(context.Background(), deps, codexStopPayload(t, ""))
	if err != nil {
		t.Fatalf("HandleCodexStop: %v", err)
	}
	if !result.UsageExtracted {
		t.Fatal("UsageExtracted = false; the sessions-dir scan fallback must find the rollout")
	}
	if result.EventsNormalized != 4 {
		t.Errorf("EventsNormalized = %d, want 4", result.EventsNormalized)
	}
}

func TestCodexStop_UnreadableRollout_DegradesToBareTurnCompleted(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // empty: the scan finds nothing either
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	stdin := codexStopPayload(t, filepath.Join(t.TempDir(), "missing.jsonl"))
	result, err := orchestrator.HandleCodexStop(context.Background(), deps, stdin)
	if err != nil {
		t.Fatalf("an unreadable rollout must never fail the Stop hook: %v", err)
	}
	if result.UsageExtracted {
		t.Error("UsageExtracted = true for a missing rollout")
	}
	if result.EventsNormalized != 1 {
		t.Errorf("EventsNormalized = %d, want 1", result.EventsNormalized)
	}
	ev := persister.calls[0][0]
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if _, present := ev.Payload[key]; present {
			t.Errorf("payload key %q fabricated without a rollout", key)
		}
	}
}

func TestCodexStop_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleCodexStop(context.Background(), deps, readCodexFixture(t, "stop", "malformed.json"))
	if err != nil {
		t.Fatalf("malformed input must fail open, got: %v", err)
	}
	if result.EventsNormalized != 0 || result.Persisted {
		t.Errorf("result = %+v, want zero on malformed input", result)
	}
}

// --- gate response golden pins across the orchestrator boundary -------------

// TestCodexGate_GoldenResponses drives the FULL handler (not just the
// encoder) and pins the exact bytes a Codex process reads on stdout for the
// allow and block outcomes, against the same golden fixtures the encoder
// tests use.
func TestCodexGate_GoldenResponses(t *testing.T) {
	// Allow (no evaluation service): {}.
	deps := baseHookDeps()
	result, err := orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := codexhooks.EncodeUserPromptSubmitResponse(result.Response)
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.TrimSpace(string(readCodexFixture(t, "userpromptsubmit", "response_allow.golden.json")))
	if string(body) != golden {
		t.Errorf("allow body = %s, want golden %s", body, golden)
	}

	// Block with the fixed forecast context: byte-equal to the block golden.
	deps = baseHookDeps()
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyBlock}, nil
		},
	}
	result, err = orchestrator.HandleCodexUserPromptSubmit(context.Background(), deps, readCodexFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The golden's additionalContext is the fixed sample string; the
	// handler leaves it empty without a Forecast source, so substitute it
	// exactly as a wired forecast card would have.
	result.Response.AdditionalContext = "forecast: sample"
	body, err = codexhooks.EncodeUserPromptSubmitResponse(result.Response)
	if err != nil {
		t.Fatal(err)
	}
	golden = strings.TrimSpace(string(readCodexFixture(t, "userpromptsubmit", "response_block.golden.json")))
	if string(body) != golden {
		t.Errorf("block body = %s, want golden %s", body, golden)
	}
}
