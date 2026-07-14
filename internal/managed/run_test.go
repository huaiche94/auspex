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

func TestRunner_Run_Validation(t *testing.T) {
	runner := newTestRunner(&runTestPersister{}, allowingEvaluation(app.PolicyRun), "unused")

	cases := []struct {
		name   string
		mutate func(*RunRequest)
	}{
		{"unsupported provider", func(r *RunRequest) { r.Provider = "codex" }},
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
