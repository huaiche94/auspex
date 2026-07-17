// run_test.go: unit coverage for Runner.Run against a compiled fake
// provider binary (testdata/fakeprovider — a Go helper compiled on demand
// with `go build`, never a shell script, so the identical test runs on
// windows-latest CI; the repo already accepts real subprocess spawning in
// tests, see internal/gitx's tests exec'ing real git). The gate side uses
// the same in-memory HookDeps doubles internal/orchestrator's own hook
// tests use (per-file duplicates, this repo's established convention for
// small cross-package test helpers).
package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- deterministic Clock/IDGenerator + persister doubles ------------------

type runTestClock struct{ t time.Time }

func (c runTestClock) Now() time.Time { return c.t }

type runTestIDs struct{ n int }

func (g *runTestIDs) NewID() string {
	g.n++
	return "rid-" + string(rune('0'+g.n))
}

type runTestPersister struct {
	calls [][]v1.Event
	err   error
}

func (p *runTestPersister) PersistAll(_ context.Context, _ app.TxRunner, evs []v1.Event) error {
	if p.err != nil {
		return p.err
	}
	p.calls = append(p.calls, evs)
	return nil
}

type runTestTxRunner struct{}

func (runTestTxRunner) WithTx(ctx context.Context, fn app.TxFunc) error { return fn(ctx) }

// allowingEvaluation builds a FakeEvaluationService whose Decide renders
// the given action for a fixed evaluation ID "eval-run-1".
func allowingEvaluation(action app.PolicyAction) *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-run-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-run-1", Action: action}, nil
		},
	}
}

func newTestRunner(persister *runTestPersister, eval app.EvaluationService, providerBin string) *Runner {
	return &Runner{
		Hooks: orchestrator.HookDeps{
			Clock:      runTestClock{t: time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)},
			IDs:        &runTestIDs{},
			Persister:  persister,
			TxRunner:   runTestTxRunner{},
			Evaluation: eval,
		},
		ProviderBin: providerBin,
	}
}

func baseRunRequest() RunRequest {
	return RunRequest{
		Provider:   ProviderClaude,
		SessionID:  "sess-run-test",
		WorktreeID: "wt-run-test",
		Prompt:     "add a small test",
	}
}

// buildFakeProvider compiles testdata/fakeprovider into a temp dir and
// returns the binary path. `go build` is available wherever these tests
// run (they run under the go tool) and its build cache makes repeat
// compilations cheap.
func buildFakeProvider(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-claude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakeprovider")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build testdata/fakeprovider: %v\n%s", err, out)
	}
	return bin
}

func fixtureAbs(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	return abs
}

func eventOfType(t *testing.T, evs []v1.Event, want v1.EventType) v1.Event {
	t.Helper()
	for _, ev := range evs {
		if ev.EventType == want {
			return ev
		}
	}
	t.Fatalf("no %s event in %+v", want, evs)
	return v1.Event{}
}

// --- the tests --------------------------------------------------------------

func TestRunner_Run_HappyPath_GatesSpawnsParsesPersists(t *testing.T) {
	bin := buildFakeProvider(t)
	argvFile := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", argvFile)

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyRun), bin)

	var relay, human bytes.Buffer
	req := baseRunRequest()
	req.StreamRelay = &relay
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if outcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", outcome.ExitCode)
	}
	if outcome.Decision != app.PolicyRun || outcome.EvaluationID != "eval-run-1" {
		t.Errorf("gate outcome = decision %q evaluation %q, want RUN/eval-run-1", outcome.Decision, outcome.EvaluationID)
	}
	if outcome.GateDegraded {
		t.Error("GateDegraded = true, want false")
	}
	if outcome.TurnID == "" {
		t.Fatal("TurnID is empty")
	}
	// 1 turn.started (gate) + 2 terminal (turn.completed + usage).
	if outcome.EventsPersisted != 3 {
		t.Errorf("EventsPersisted = %d, want 3", outcome.EventsPersisted)
	}
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2 (gate, terminal)", len(persister.calls))
	}

	started := eventOfType(t, persister.calls[0], v1.EventProviderTurnStarted)
	if started.TurnID != string(outcome.TurnID) {
		t.Errorf("turn.started TurnID = %q, want %q — every event of one run must join on one TurnID", started.TurnID, outcome.TurnID)
	}

	completed := eventOfType(t, persister.calls[1], v1.EventProviderTurnCompleted)
	if completed.TurnID != string(outcome.TurnID) || completed.WorktreeID != "wt-run-test" {
		t.Errorf("turn.completed scope = turn %q worktree %q, want %q/wt-run-test", completed.TurnID, completed.WorktreeID, outcome.TurnID)
	}
	if completed.Payload["exit_code"] != 0 || completed.Payload["result_seen"] != true {
		t.Errorf("turn.completed payload = %+v, want exit_code 0 / result_seen true", completed.Payload)
	}

	usage := eventOfType(t, persister.calls[1], v1.EventProviderUsageObserved)
	if usage.Payload["total_cost_usd"] != 0.0417 {
		t.Errorf("usage payload = %+v, want total_cost_usd 0.0417", usage.Payload)
	}
	// Issue #11 end-to-end through the runner: the fixture result line's
	// token block arrives as raw components + the input+output total, and
	// the init line's model rides along as the cohort label.
	if usage.Payload["input_tokens"] != int64(2100) || usage.Payload["output_tokens"] != int64(350) ||
		usage.Payload["total_tokens"] != int64(2450) {
		t.Errorf("usage payload = %+v, want input/output/total tokens 2100/350/2450", usage.Payload)
	}
	if usage.Payload["model_id"] != "claude-sonnet-4-5" {
		t.Errorf("usage payload model_id = %v, want claude-sonnet-4-5 (from the stream's init line)", usage.Payload["model_id"])
	}
	if usage.TurnID != string(outcome.TurnID) {
		t.Errorf("usage TurnID = %q, want %q (exact turn attribution)", usage.TurnID, outcome.TurnID)
	}

	// Argv proof: exactly the promised argv-only invocation, prompt as a
	// single element (Constitution §7 rule 5 — no shell string).
	argvJSON, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading argv file: %v", err)
	}
	var argv []string
	if err := json.Unmarshal(argvJSON, &argv); err != nil {
		t.Fatalf("decoding argv file: %v", err)
	}
	wantArgv := []string{"-p", "add a small test", "--output-format", "stream-json", "--verbose"}
	if len(argv) != len(wantArgv) {
		t.Fatalf("argv = %v, want %v", argv, wantArgv)
	}
	for i := range argv {
		if argv[i] != wantArgv[i] {
			t.Fatalf("argv[%d] = %q, want %q (full argv %v)", i, argv[i], wantArgv[i], argv)
		}
	}

	if relay.Len() == 0 {
		t.Error("StreamRelay received nothing, want the raw stream lines")
	}
	if human.Len() == 0 {
		t.Error("HumanLog received nothing, want the decision line")
	}
}

func TestRunner_Run_BlockDecision_RefusesToSpawn(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", argvFile)

	persister := &runTestPersister{}
	// ProviderBin points at a path that cannot exist: if the runner ever
	// tried to spawn on a BLOCK, the error would be ErrCodeUnavailable
	// (spawn failure), not the unauthorized block below.
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyBlock), filepath.Join(t.TempDir(), "never-built-binary"))

	outcome, err := runner.Run(context.Background(), baseRunRequest())
	if err == nil {
		t.Fatal("Run returned nil error, want the typed BLOCK error")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %v (%T), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeUnauthorized {
		t.Errorf("err.Code = %q, want %q", derr.Code, domain.ErrCodeUnauthorized)
	}
	if derr.Details["evaluation_id"] != "eval-run-1" || derr.Details["policy_action"] != string(app.PolicyBlock) {
		t.Errorf("err.Details = %v, want evaluation_id/policy_action populated", derr.Details)
	}
	if outcome.Decision != app.PolicyBlock {
		t.Errorf("outcome.Decision = %q, want BLOCK", outcome.Decision)
	}

	// Only the gate's turn.started batch — no terminal events, because no
	// provider process ever existed.
	if len(persister.calls) != 1 {
		t.Fatalf("persister.calls = %d batches, want 1 (turn.started only)", len(persister.calls))
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Errorf("argv file exists (stat err %v) — the provider was spawned despite BLOCK", statErr)
	}
}

func TestRunner_Run_EvaluationError_FailsOpenAndStillRuns(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))

	persister := &runTestPersister{}
	broken := &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, _ app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "pipeline down", Retryable: true}
		},
	}
	runner := newTestRunner(persister, broken, bin)

	var human bytes.Buffer
	req := baseRunRequest()
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v — an evaluation error must fail OPEN (ADD §17.5), not abort the run", err)
	}
	if !outcome.GateDegraded {
		t.Error("GateDegraded = false, want true")
	}
	if outcome.EvaluationID != "" || outcome.Decision != "" {
		t.Errorf("degraded outcome carries evaluation %q decision %q, want empty — no decision may be fabricated", outcome.EvaluationID, outcome.Decision)
	}
	if outcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", outcome.ExitCode)
	}
	if human.Len() == 0 {
		t.Error("HumanLog is empty — the degrade must be loud, never silent")
	}
	// The terminal events still land on the SAME TurnID the gate minted
	// for the (persisted) turn.started event.
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2", len(persister.calls))
	}
	started := eventOfType(t, persister.calls[0], v1.EventProviderTurnStarted)
	completed := eventOfType(t, persister.calls[1], v1.EventProviderTurnCompleted)
	if started.TurnID == "" || started.TurnID != completed.TurnID {
		t.Errorf("turn IDs split across degrade path: started %q vs completed %q", started.TurnID, completed.TurnID)
	}
}

func TestRunner_Run_ProviderFailure_PersistsTurnFailed(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_error.jsonl"))
	t.Setenv("AUSPEX_FAKE_EXIT_CODE", "1")

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyWarn), bin)

	outcome, err := runner.Run(context.Background(), baseRunRequest())
	if err != nil {
		t.Fatalf("Run: %v — a provider that ran and exited non-zero is attribution data, not a Run error", err)
	}
	if outcome.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", outcome.ExitCode)
	}
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2", len(persister.calls))
	}
	failed := eventOfType(t, persister.calls[1], v1.EventProviderTurnFailed)
	if failed.Payload["exit_code"] != 1 || failed.Payload["is_error"] != true {
		t.Errorf("turn.failed payload = %+v, want exit_code 1 / is_error true", failed.Payload)
	}
	// The error result line still carried usage — exact attribution holds
	// for failed turns too (that is when cost forensics matter most).
	usage := eventOfType(t, persister.calls[1], v1.EventProviderUsageObserved)
	if usage.Payload["total_cost_usd"] != 0.0031 {
		t.Errorf("usage payload = %+v, want total_cost_usd 0.0031", usage.Payload)
	}
}

func TestRunner_Run_SpawnFailure_TypedErrorAndTerminalEvent(t *testing.T) {
	persister := &runTestPersister{}
	missing := filepath.Join(t.TempDir(), "no-such-provider-binary")
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyRun), missing)

	outcome, err := runner.Run(context.Background(), baseRunRequest())
	if err == nil {
		t.Fatal("Run returned nil error, want the typed spawn failure")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want *domain.Error with code %q", err, domain.ErrCodeUnavailable)
	}
	if derr.Details["provider_bin"] != missing {
		t.Errorf("err.Details = %v, want provider_bin named", derr.Details)
	}
	if outcome.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (no exit code was ever observed)", outcome.ExitCode)
	}
	// turn.started must not dangle: a terminal turn.failed with the
	// spawn_failed marker is persisted before the error returns.
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2", len(persister.calls))
	}
	failed := eventOfType(t, persister.calls[1], v1.EventProviderTurnFailed)
	if failed.Payload["spawn_failed"] != true || failed.Payload["result_seen"] != false {
		t.Errorf("turn.failed payload = %+v, want spawn_failed true / result_seen false", failed.Payload)
	}
}

// --- the codex managed exec runner (issue #9 M7 Phase 1) -------------------

func codexExecFixtureAbs(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "provider-events", "codex", "exec", name))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	return abs
}

func baseCodexRunRequest() RunRequest {
	req := baseRunRequest()
	req.Provider = ProviderCodex
	return req
}

func TestRunner_Run_Codex_HappyPath_GatesSpawnsParsesPersists(t *testing.T) {
	bin := buildFakeProvider(t)
	argvFile := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", codexExecFixtureAbs(t, "normal.jsonl"))
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", argvFile)

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyRun), bin)

	var relay bytes.Buffer
	req := baseCodexRunRequest()
	req.StreamRelay = &relay

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", outcome.ExitCode)
	}
	// 1 turn.started (gate) + 3 terminal (session.started from
	// thread.started, turn.completed, usage.observed).
	if outcome.EventsPersisted != 4 {
		t.Errorf("EventsPersisted = %d, want 4", outcome.EventsPersisted)
	}
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2 (gate, terminal)", len(persister.calls))
	}

	// The gate's turn.started must carry the run's HONEST provider —
	// issue #9 M7's generalization of the issue-#8 claude-only gate.
	started := eventOfType(t, persister.calls[0], v1.EventProviderTurnStarted)
	if started.Provider != "codex" {
		t.Errorf("turn.started Provider = %q, want codex", started.Provider)
	}
	if started.TurnID != string(outcome.TurnID) {
		t.Errorf("turn.started TurnID = %q, want %q", started.TurnID, outcome.TurnID)
	}

	sessionStarted := eventOfType(t, persister.calls[1], v1.EventProviderSessionStarted)
	if sessionStarted.Payload["thread_id"] != "019f0000-3333-7aaa-8bbb-ccccdddd0201" {
		t.Errorf("session.started payload = %+v, want the stream's thread_id", sessionStarted.Payload)
	}

	completed := eventOfType(t, persister.calls[1], v1.EventProviderTurnCompleted)
	if completed.Provider != "codex" || completed.TurnID != string(outcome.TurnID) || completed.WorktreeID != "wt-run-test" {
		t.Errorf("turn.completed scope = provider %q turn %q worktree %q", completed.Provider, completed.TurnID, completed.WorktreeID)
	}
	if completed.Payload["exit_code"] != 0 || completed.Payload["turn_completed_seen"] != true {
		t.Errorf("turn.completed payload = %+v", completed.Payload)
	}

	// Exact usage attribution under the shared vocabulary: fresh input
	// 4200-3072=1128, cache read 3072, output 180, reasoning 64, total
	// 1128+180=1308 (never codex's own cached-inclusive 4380).
	usage := eventOfType(t, persister.calls[1], v1.EventProviderUsageObserved)
	if usage.Payload["input_tokens"] != int64(1128) || usage.Payload["cache_read_input_tokens"] != int64(3072) ||
		usage.Payload["output_tokens"] != int64(180) || usage.Payload["reasoning_output_tokens"] != int64(64) ||
		usage.Payload["total_tokens"] != int64(1308) {
		t.Errorf("usage payload = %+v, want 1128/3072/180/64/1308", usage.Payload)
	}
	if usage.TurnID != string(outcome.TurnID) {
		t.Errorf("usage TurnID = %q, want %q (exact turn attribution)", usage.TurnID, outcome.TurnID)
	}

	// Argv proof: exactly ADD §21.8's `codex exec --json <prompt>`,
	// prompt as one argv element (Constitution §7 rule 5).
	argvJSON, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading argv file: %v", err)
	}
	var argv []string
	if err := json.Unmarshal(argvJSON, &argv); err != nil {
		t.Fatalf("decoding argv file: %v", err)
	}
	wantArgv := []string{"exec", "--json", "add a small test"}
	if len(argv) != len(wantArgv) {
		t.Fatalf("argv = %v, want %v", argv, wantArgv)
	}
	for i := range argv {
		if argv[i] != wantArgv[i] {
			t.Fatalf("argv[%d] = %q, want %q (full argv %v)", i, argv[i], wantArgv[i], argv)
		}
	}

	if relay.Len() == 0 {
		t.Error("StreamRelay received nothing, want the raw JSONL lines")
	}
}

func TestRunner_Run_Codex_TurnFailed_PersistsFailureNoUsage(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", codexExecFixtureAbs(t, "turn_failed.jsonl"))
	t.Setenv("AUSPEX_FAKE_EXIT_CODE", "1")

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyWarn), bin)

	outcome, err := runner.Run(context.Background(), baseCodexRunRequest())
	if err != nil {
		t.Fatalf("Run: %v — a provider that ran and exited non-zero is attribution data, not a Run error", err)
	}
	if outcome.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", outcome.ExitCode)
	}
	if outcome.Codex.Failed == nil {
		t.Error("outcome.Codex.Failed is nil, want the parsed turn.failed")
	}
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2", len(persister.calls))
	}
	failed := eventOfType(t, persister.calls[1], v1.EventProviderTurnFailed)
	if failed.Payload["exit_code"] != 1 || failed.Payload["turn_failed_seen"] != true ||
		failed.Payload["error_events"] != 1 || failed.Payload["failure_message_len"] != 30 {
		t.Errorf("turn.failed payload = %+v", failed.Payload)
	}
	for _, ev := range persister.calls[1] {
		if ev.EventType == v1.EventProviderUsageObserved {
			t.Error("usage event fabricated although turn.completed never arrived (unknown is not zero)")
		}
	}
}

// killedExitCode is the exit code a context-killed provider child reports,
// which is platform-specific: on Unix exec.CommandContext SIGKILLs it and
// ProcessState.ExitCode() is -1 (terminated by signal, no wait status); on
// Windows the child is TerminateProcess'd and exits with code 1. The
// runtime contract this test pins ("the run failed because we killed it,
// no graceful exit code") is identical across platforms — only the
// OS-reported number differs, so the assertion tracks it per-OS rather
// than hardcoding the Unix value (which reddened windows-latest, #102).
func killedExitCode() int {
	if runtime.GOOS == "windows" {
		return 1
	}
	return -1
}

// TestRunner_Run_Codex_ContextCancelled_KillsProviderCleanly pins the
// process-hygiene contract for the codex spec: cancelling the run's
// context kills the provider (exec.CommandContext), Run returns instead
// of hanging, the unobserved exit code is an honest -1, and the run still
// gets its terminal turn.failed (the started event never dangles). The
// fake provider sleeps far longer than the test would ever wait, so a
// return at all proves the kill.
func TestRunner_Run_Codex_ContextCancelled_KillsProviderCleanly(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", codexExecFixtureAbs(t, "normal.jsonl"))
	t.Setenv("AUSPEX_FAKE_SLEEP_MS", "60000")

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyRun), bin)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	outcome, err := runner.Run(ctx, baseCodexRunRequest())
	if err != nil {
		t.Fatalf("Run: %v — a cancelled provider is a failed run result, never a Run error or a hang", err)
	}
	if outcome.ExitCode != killedExitCode() {
		t.Errorf("ExitCode = %d, want %d (killed; no exit code observed)", outcome.ExitCode, killedExitCode())
	}
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2 (gate, terminal)", len(persister.calls))
	}
	failed := eventOfType(t, persister.calls[1], v1.EventProviderTurnFailed)
	if failed.Payload["exit_code"] != killedExitCode() {
		t.Errorf("turn.failed payload = %+v, want exit_code %d", failed.Payload, killedExitCode())
	}
	// The stream written BEFORE the kill was already parsed — captured
	// attribution survives the interruption.
	if outcome.Codex.Completed == nil {
		t.Error("outcome.Codex.Completed is nil — pre-kill stream lines were lost")
	}
}

func TestRunner_Run_Validation(t *testing.T) {
	runner := newTestRunner(&runTestPersister{}, allowingEvaluation(app.PolicyRun), "unused")

	cases := []struct {
		name   string
		mutate func(*RunRequest)
	}{
		{"unsupported provider", func(r *RunRequest) { r.Provider = "gemini" }},
		{"missing session", func(r *RunRequest) { r.SessionID = "" }},
		{"missing worktree", func(r *RunRequest) { r.WorktreeID = "" }},
		{"empty prompt", func(r *RunRequest) { r.Prompt = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRunRequest()
			tc.mutate(&req)
			_, err := runner.Run(context.Background(), req)
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
				t.Fatalf("err = %v, want *domain.Error with code %q", err, domain.ErrCodeValidation)
			}
		})
	}
}
