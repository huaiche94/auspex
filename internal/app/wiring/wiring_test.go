package wiring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/app/wiring"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// fullFakeServices returns a Services struct with every field populated by
// a fresh fake — the runtime-b02 baseline composition (no real service
// implementation exists yet; EXECUTION_DAG.md: "can start against
// claude-provider/checkpoint/predictor fakes").
func fullFakeServices() wiring.Services {
	return wiring.Services{
		Evaluation:           &fakes.FakeEvaluationService{},
		ProgressTree:         &fakes.FakeProgressTreeService{},
		StateCheckpoint:      &fakes.FakeStateCheckpointService{},
		GracefulPause:        &fakes.FakeGracefulPauseService{},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{},
	}
}

func TestNew_AllServicesPresent_Succeeds(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Fatal("New returned nil *App with nil error")
	}
}

func TestNew_MissingService_FailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*wiring.Services)
	}{
		{"Evaluation", func(s *wiring.Services) { s.Evaluation = nil }},
		{"ProgressTree", func(s *wiring.Services) { s.ProgressTree = nil }},
		{"StateCheckpoint", func(s *wiring.Services) { s.StateCheckpoint = nil }},
		{"GracefulPause", func(s *wiring.Services) { s.GracefulPause = nil }},
		{"RepositoryCheckpoint", func(s *wiring.Services) { s.RepositoryCheckpoint = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			services := fullFakeServices()
			tc.mutate(&services)

			a, err := wiring.New(services)
			if err == nil {
				t.Fatal("New succeeded with a missing service; want fail-closed validation error")
			}
			if a != nil {
				t.Error("New returned a non-nil *App alongside an error")
			}

			var domainErr *domain.Error
			if !errors.As(err, &domainErr) {
				t.Fatalf("err = %T (%v), want *domain.Error", err, err)
			}
			if domainErr.Code != domain.ErrCodeValidation {
				t.Errorf("Code = %q, want %q", domainErr.Code, domain.ErrCodeValidation)
			}
			if domainErr.Retryable {
				t.Error("Retryable = true, want false (composition holes are not transient)")
			}
			if !strings.Contains(domainErr.Details["missing_services"], tc.name) {
				t.Errorf("Details[missing_services] = %q, want it to contain %q", domainErr.Details["missing_services"], tc.name)
			}
		})
	}
}

func TestNew_AllMissing_ListsEveryService(t *testing.T) {
	_, err := wiring.New(wiring.Services{})
	if err == nil {
		t.Fatal("New succeeded on an empty Services struct")
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("err = %T (%v), want *domain.Error", err, err)
	}
	for _, name := range []string{"Evaluation", "ProgressTree", "StateCheckpoint", "GracefulPause", "RepositoryCheckpoint"} {
		if !strings.Contains(domainErr.Details["missing_services"], name) {
			t.Errorf("Details[missing_services] = %q, missing %q", domainErr.Details["missing_services"], name)
		}
	}
}

func TestApp_AccessorsReturnInjectedInstances(t *testing.T) {
	services := fullFakeServices()
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if a.Evaluation() != services.Evaluation {
		t.Error("Evaluation() did not return the injected instance")
	}
	if a.ProgressTree() != services.ProgressTree {
		t.Error("ProgressTree() did not return the injected instance")
	}
	if a.StateCheckpoint() != services.StateCheckpoint {
		t.Error("StateCheckpoint() did not return the injected instance")
	}
	if a.GracefulPause() != services.GracefulPause {
		t.Error("GracefulPause() did not return the injected instance")
	}
	if a.RepositoryCheckpoint() != services.RepositoryCheckpoint {
		t.Error("RepositoryCheckpoint() did not return the injected instance")
	}
}

func TestApp_CallsRouteToInjectedFake(t *testing.T) {
	// Configure one method on two different services and prove a call
	// through the container reaches exactly the configured closure with
	// its arguments intact — the container must be pass-through plumbing,
	// not a wrapper that re-interprets calls.
	wantPause := app.PauseRecord{ID: domain.PauseID("pause-7"), Status: domain.PauseRequested}
	var gotReason string

	services := fullFakeServices()
	services.GracefulPause = &fakes.FakeGracefulPauseService{
		RequestPauseFunc: func(_ context.Context, req app.PauseRequest) (app.PauseRecord, error) {
			gotReason = req.Reason
			return wantPause, nil
		},
	}
	services.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: domain.EvaluationID("eval-1"), TurnID: req.TurnID}, nil
		},
	}

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := a.GracefulPause().RequestPause(context.Background(), app.PauseRequest{
		SessionID: domain.SessionID("sess-1"),
		Reason:    "quota_runway_low",
	})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if got != wantPause {
		t.Errorf("RequestPause = %+v, want %+v", got, wantPause)
	}
	if gotReason != "quota_runway_low" {
		t.Errorf("fake saw Reason = %q, want %q", gotReason, "quota_runway_low")
	}

	eval, err := a.Evaluation().EvaluateTurn(context.Background(), app.EvaluateTurnRequest{TurnID: domain.TurnID("turn-9")})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	if eval.TurnID != domain.TurnID("turn-9") {
		t.Errorf("EvaluateTurn.TurnID = %q, want %q", eval.TurnID, "turn-9")
	}
}

func TestApp_UnconfiguredFakeMethod_FailsLoud(t *testing.T) {
	// The fakes' nil-Func contract, exercised through the container: an
	// unconfigured method must return the frozen ErrCodeUnavailable shape
	// naming the fake and method — never a silent zero value.
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = a.GracefulPause().Observe(context.Background(), app.RuntimeObservation{})
	if err == nil {
		t.Fatal("Observe on an unconfigured fake succeeded; want loud unconfigured error")
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("err = %T (%v), want *domain.Error", err, err)
	}
	if domainErr.Code != domain.ErrCodeUnavailable {
		t.Errorf("Code = %q, want %q", domainErr.Code, domain.ErrCodeUnavailable)
	}
	if domainErr.Retryable {
		t.Error("Retryable = true, want false (retrying an unconfigured fake never succeeds)")
	}
	if domainErr.Details["fake"] != "FakeGracefulPauseService" || domainErr.Details["method"] != "Observe" {
		t.Errorf("Details = %v, want fake=FakeGracefulPauseService method=Observe", domainErr.Details)
	}
}

func TestApp_RootCmd_BuildsP0CommandTree(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	if root == nil {
		t.Fatal("RootCmd returned nil")
	}
	if root.Use != "auspex" {
		t.Errorf("root.Use = %q, want %q", root.Use, "auspex")
	}

	want := []string{
		"version", "init", "hook", "evaluate", "decision", "checkpoint",
		"progress", "state", "pause", "resume", "scheduler", "status", "doctor",
	}
	got := make(map[string]bool)
	for _, sub := range root.Commands() {
		got[sub.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("RootCmd tree is missing top-level command %q", name)
		}
	}
}

// TestApp_RootCmd_HookClaudeIsRealNotStub proves runtime-b04's wiring:
// `auspex hook claude user-prompt-submit` on the App-built tree is
// internal/cli.NewHookClaudeCmd's real handler (which renders a
// provider-compatible JSON response and returns nil), not
// internal/cli.NewRootCmd()'s standalone ErrCodeUnavailable stub.
func TestApp_RootCmd_HookClaudeIsRealNotStub(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"hook", "claude", "user-prompt-submit"})
	root.SetIn(strings.NewReader(`{"session_id":"sess-1","prompt":"do a thing"}`))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("hook claude user-prompt-submit: %v (want the real handler to succeed, not the stub's ErrCodeUnavailable)", err)
	}
	if out.Len() == 0 {
		t.Fatal("hook claude user-prompt-submit produced no stdout output")
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
}

// TestApp_RootCmd_HookClaudeFallsBackToRealClockWhenHooksUnset proves the
// zero-value HookSupport fallback: a Services value with Hooks left unset
// still produces a working hook command tree (real domain.Clock/
// domain.IDGenerator, no persistence) rather than panicking on a nil
// Clock inside the orchestrator's Normalizer construction.
func TestApp_RootCmd_HookClaudeFallsBackToRealClockWhenHooksUnset(t *testing.T) {
	services := fullFakeServices() // Hooks left at zero value
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"hook", "claude", "stop"})
	root.SetIn(strings.NewReader(`{"session_id":"sess-1","hook_event_name":"Stop","stop_hook_active":false}`))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("hook claude stop with zero-value HookSupport: %v", err)
	}
}

// TestApp_RootCmd_HookClaudeMalformedInputStillProducesValidJSON proves
// "hook fallback remains syntactically valid when Auspex fails"
// end-to-end through the wired CLI tree, not just at the orchestrator
// unit level (internal/orchestrator/hooks_test.go already covers the
// orchestrator function directly) — malformed stdin on
// user-prompt-submit must still yield a valid JSON allow response, never
// a raw error dumped to stdout.
func TestApp_RootCmd_HookClaudeMalformedInputStillProducesValidJSON(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"hook", "claude", "user-prompt-submit"})
	root.SetIn(strings.NewReader(`{ not valid json`))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("hook claude user-prompt-submit with malformed input: %v, want fail-open success", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON on malformed input: %v (output: %q)", jsonErr, out.String())
	}
}

// TestApp_RootCmd_CheckpointCreateIsRealNotStub proves runtime-b05's
// wiring: `auspex checkpoint create` on the App-built tree calls
// through to the injected StateCheckpoint/RepositoryCheckpoint fakes (in
// state-then-repository order) and renders a real JSON result, not
// internal/cli.NewRootCmd()'s standalone ErrCodeUnavailable stub.
func TestApp_RootCmd_CheckpointCreateIsRealNotStub(t *testing.T) {
	var callOrder []string
	services := fullFakeServices()
	services.StateCheckpoint = &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			callOrder = append(callOrder, "state")
			return domain.StateCheckpoint{ID: "sc-1", TaskID: req.TaskID}, nil
		},
	}
	services.RepositoryCheckpoint = &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			callOrder = append(callOrder, "repository")
			return app.RepositoryCheckpoint{ID: "rc-1", GitHead: "cafef00d"}, nil
		},
	}

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"checkpoint", "create", "--task-id", "task-1", "--worktree-id", "wt-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("checkpoint create: %v (want the real handler to succeed, not the stub's ErrCodeUnavailable)", err)
	}
	if len(callOrder) != 2 || callOrder[0] != "state" || callOrder[1] != "repository" {
		t.Fatalf("call order = %v, want [state, repository] end-to-end through the wired CLI command", callOrder)
	}

	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["state_checkpoint_id"] != "sc-1" {
		t.Errorf("state_checkpoint_id = %v, want sc-1", decoded["state_checkpoint_id"])
	}
	if decoded["repository_checkpoint_id"] != "rc-1" {
		t.Errorf("repository_checkpoint_id = %v, want rc-1", decoded["repository_checkpoint_id"])
	}
}

// TestApp_RootCmd_StatusIsRealNotStub proves runtime-b08's wiring:
// `auspex status` on the App-built tree calls through to the injected
// ProgressTree fake and renders real JSON, not the standalone stub.
func TestApp_RootCmd_StatusIsRealNotStub(t *testing.T) {
	services := fullFakeServices()
	services.ProgressTree = &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			return app.ProgressTreeSnapshot{TaskID: taskID, Nodes: []app.ProgressNode{{ID: "n1"}}}, nil
		},
	}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"status", "--session-id", "sess-1", "--task-id", "task-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v (want the real handler to succeed, not the stub's ErrCodeUnavailable)", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["has_progress_tree"] != true {
		t.Errorf("has_progress_tree = %v, want true", decoded["has_progress_tree"])
	}
}

// TestApp_RootCmd_ProgressCompleteIsRealNotStub proves issue #1's wiring:
// `auspex progress complete` on the App-built tree calls through to the
// injected ProgressTree fake's CompleteNode and renders real JSON, not
// internal/cli.NewRootCmd()'s standalone ErrCodeUnavailable stub — while
// `progress show` in the SAME swapped subtree deliberately remains a stub
// (cli.NewProgressCmd's documented split).
func TestApp_RootCmd_ProgressCompleteIsRealNotStub(t *testing.T) {
	var got app.CompleteNodeRequest
	services := fullFakeServices()
	services.ProgressTree = &fakes.FakeProgressTreeService{
		CompleteNodeFunc: func(_ context.Context, req app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error) {
			got = req
			return app.ProgressNode{ID: req.NodeID, TaskID: "task-1", Status: domain.NodeCompleted},
				domain.StateCheckpoint{ID: "sc-1", TaskID: "task-1"}, nil
		},
	}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"progress", "complete", "--node", "node-1", "--idempotency-key", "key-1", "--artifact", "file=/tmp/a.md", "--json"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("progress complete: %v (want the real handler to succeed, not the stub's ErrCodeUnavailable)", err)
	}
	if got.NodeID != "node-1" || got.IdempotencyKey != "key-1" || len(got.Artifacts) != 1 {
		t.Fatalf("CompleteNode saw %+v, want node-1/key-1/one artifact end-to-end through the wired CLI command", got)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["node_status"] != string(domain.NodeCompleted) {
		t.Errorf("node_status = %v, want %q", decoded["node_status"], domain.NodeCompleted)
	}
	if decoded["state_checkpoint_id"] != "sc-1" {
		t.Errorf("state_checkpoint_id = %v, want sc-1", decoded["state_checkpoint_id"])
	}

	// The swap must not have silently promoted `progress show` — it stays
	// the honest stub on this tree too.
	root.SetArgs([]string{"progress", "show"})
	out.Reset()
	showErr := root.Execute()
	var derr *domain.Error
	if !errors.As(showErr, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Errorf("progress show: err = %v, want the stub's ErrCodeUnavailable", showErr)
	}
}

// TestApp_RootCmd_DoctorIsRealNotStub proves runtime-b08's wiring:
// `auspex doctor` on the App-built tree runs real checks (here, all
// skipped since Diagnostics was left at its zero value) and renders real
// JSON, not the standalone stub's ErrCodeUnavailable.
func TestApp_RootCmd_DoctorIsRealNotStub(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetArgs([]string{"doctor"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("doctor: %v (want the real handler to succeed, not the stub's ErrCodeUnavailable)", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["healthy"] != true {
		t.Errorf("healthy = %v, want true (zero-value Diagnostics means all-skipped, which is healthy)", decoded["healthy"])
	}
}

// --- runtime-b07: pause/resume/scheduler wiring -----------------------------

// TestApp_RootCmd_PauseUnwired_StaysStub proves the documented fallback:
// leaving Services.PauseLifecycle at its zero value (no Store injected)
// keeps RootCmd's original `pause`/`resume`/`scheduler` stub tree in place,
// exactly like every other not-yet-wired P0 command family.
func TestApp_RootCmd_PauseUnwired_StaysStub(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()

	// No flags passed here: the standalone stub commands (internal/cli/
	// root.go) never defined any flags at all (their RunE ignores input
	// entirely and always returns notImplemented), so passing a real flag
	// name would fail at cobra's own flag-parsing stage with "unknown
	// flag" rather than reaching RunE — this test only needs to prove the
	// stub tree, not the real one's flags, is what's still mounted.
	for _, args := range [][]string{
		{"pause", "request"},
		{"resume"},
		{"scheduler", "run-once"},
	} {
		root.SetArgs(args)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		err := root.Execute()
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
			t.Errorf("args=%v: err = %v, want the stub's ErrCodeUnavailable", args, err)
		}
	}
}

// TestApp_RootCmd_PauseRequestCancelResume_RealEndToEnd proves runtime-b07's
// wiring end to end through the actual CLI tree: `pause request` creates a
// real record via internal/pause.RequestPause, `resume` (seeded to
// WakePending directly against the same store, simulating a wake job having
// already fired) advances it to Resumed, all through cobra command
// execution, not a direct orchestrator call.
func TestApp_RootCmd_PauseRequestThenCancel_RealEndToEnd(t *testing.T) {
	store := pause.NewMemStore()
	services := fullFakeServices()
	services.PauseLifecycle = orchestrator.PauseLifecycleDeps{Store: store}

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()

	root.SetArgs([]string{"pause", "request", "--task-id", "task-1", "--session-id", "sess-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("pause request: %v", err)
	}
	var requested map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &requested); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	pauseID, _ := requested["pause_id"].(string)
	if pauseID == "" {
		t.Fatal("expected a non-empty pause_id in pause request's output")
	}
	if requested["created"] != true {
		t.Errorf("created = %v, want true", requested["created"])
	}

	out.Reset()
	root.SetArgs([]string{"pause", "cancel", "--pause-id", pauseID})
	if err := root.Execute(); err != nil {
		t.Fatalf("pause cancel: %v", err)
	}
	var cancelled map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &cancelled); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if cancelled["status"] != string(domain.PauseCancelled) {
		t.Errorf("status = %v, want %q", cancelled["status"], domain.PauseCancelled)
	}
}

// TestApp_RootCmd_Resume_RealEndToEnd proves `auspex resume` reaches the
// real internal/pause.Resume through the wired CLI tree.
func TestApp_RootCmd_Resume_RealEndToEnd(t *testing.T) {
	store := pause.NewMemStore()
	if err := store.Insert(context.Background(), pause.PauseRecord{
		ID:     "pause-1",
		Key:    pause.PauseKey{TaskID: "task-1", SessionID: "sess-1"},
		Status: domain.PauseWakePending,
		Reason: pause.TriggerReasonCalibrated,
	}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
	services := fullFakeServices()
	services.PauseLifecycle = orchestrator.PauseLifecycleDeps{Store: store}

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()
	root.SetArgs([]string{"resume", "--pause-id", "pause-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["status"] != string(domain.PauseResumed) {
		t.Errorf("status = %v, want %q", decoded["status"], domain.PauseResumed)
	}
}

// TestApp_RootCmd_SchedulerRunOnce_RealEndToEnd proves `auspex scheduler
// run-once` reaches the real internal/scheduler.Store.Claim through the
// wired CLI tree, against a real migrated temp-file SQLite database.
func TestApp_RootCmd_SchedulerRunOnce_RealEndToEnd(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "auspex.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := "2026-07-12T09:00:00Z"
	seed := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at) VALUES ('repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at) VALUES ('wt1', 'repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json) VALUES ('sess1', 'wt1', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at) VALUES ('task1', 'sess1', 'wt1', 'hash1', 'pending', '` + now + `', '` + now + `')`,
		`INSERT INTO pause_records (id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled) VALUES ('pause1', 'task1', 'sess1', 'turn1', 'rf1', 'sleeping', '` + now + `', 1)`,
	}
	for _, stmt := range seed {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	wakeStore := scheduler.NewStore(db.Conn(), realClockAt(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)), &wiringSeqIDs{prefix: "wj"})
	if _, err := wakeStore.Schedule(context.Background(), scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "pause_resume", RunAfter: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC), MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	services := fullFakeServices()
	services.PauseLifecycle = orchestrator.PauseLifecycleDeps{WakeJobs: wakeStore}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()
	root.SetArgs([]string{"scheduler", "run-once"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("scheduler run-once: %v", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["claimed"] != true {
		t.Fatalf("claimed = %v, want true", decoded["claimed"])
	}
}

type realClockAt time.Time

func (c realClockAt) Now() time.Time { return time.Time(c) }

type wiringSeqIDs struct {
	n      int
	prefix string
}

func (g *wiringSeqIDs) NewID() string {
	g.n++
	return g.prefix + "-" + string(rune('0'+g.n))
}

// --- runtime-b06: decision allow/deny wiring --------------------------------

// fakeAuthorizationIssuer is this file's own minimal local double for
// orchestrator.AuthorizationIssuer, mirroring
// internal/orchestrator/decision_test.go's own copy — kept package-local
// (not shared) since it is a narrow, package-specific interface, exactly
// like that file's own precedent explains.
type fakeAuthorizationIssuer struct {
	issueFunc func(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error)
}

func (f *fakeAuthorizationIssuer) IssueAuthorization(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
	return f.issueFunc(ctx, turnID, promptHash, snapshotFingerprint, decision, repoCheckpointID)
}

// TestApp_RootCmd_DecisionUnwired_StaysStub proves the documented fallback:
// leaving Services.Decision.Issuer at its zero value (nil) keeps RootCmd's
// original `decision` stub tree in place — the mere presence of a
// (fake-satisfiable) EvaluationService is not enough to trigger the swap,
// per Services.Decision's own doc comment on why Issuer specifically gates
// it.
func TestApp_RootCmd_DecisionUnwired_StaysStub(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()
	root.SetArgs([]string{"decision", "allow"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err = root.Execute()
	if err == nil {
		t.Fatal("decision allow succeeded despite Decision.Issuer being unwired; want the stub's ErrCodeUnavailable")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want the stub's ErrCodeUnavailable", err)
	}
}

// TestApp_RootCmd_DecisionAllow_RealEndToEnd proves runtime-b06's wiring:
// once Services.Decision.Issuer is set, `decision allow` swaps to the real
// handler and calls through to Decide then IssueAuthorization, end to end
// through the CLI tree.
func TestApp_RootCmd_DecisionAllow_RealEndToEnd(t *testing.T) {
	services := fullFakeServices()
	services.Evaluation = &fakes.FakeEvaluationService{
		DecideFunc: func(_ context.Context, req app.DecideRequest) (app.DecisionResult, error) {
			if req.EvaluationID != "eval-1" {
				t.Errorf("Decide EvaluationID = %q, want eval-1", req.EvaluationID)
			}
			return app.DecisionResult{ID: "dec-1", Action: app.PolicyRequireConfirmation}, nil
		},
	}
	services.Decision = orchestrator.DecisionDeps{
		Issuer: &fakeAuthorizationIssuer{
			issueFunc: func(_ context.Context, turnID domain.TurnID, _ string, _ string, decision string, _ *domain.RepositoryCheckpointID) (app.Authorization, error) {
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

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()
	root.SetArgs([]string{"decision", "allow", "--evaluation-id", "eval-1", "--turn-id", "turn-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("decision allow: %v (want the real handler to succeed, not the stub's ErrCodeUnavailable)", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["issued"] != true {
		t.Errorf("issued = %v, want true", decoded["issued"])
	}
	if decoded["authorization_id"] != "auth-1" {
		t.Errorf("authorization_id = %v, want auth-1", decoded["authorization_id"])
	}
}

// TestApp_RootCmd_DecisionAllow_ConsumeFlow_RealEndToEnd proves the
// resubmission (consume) flow routes through the wired CLI tree to
// ConsumeAuthorization, not Decide/Issuer.
func TestApp_RootCmd_DecisionAllow_ConsumeFlow_RealEndToEnd(t *testing.T) {
	decideCalled := false
	services := fullFakeServices()
	services.Evaluation = &fakes.FakeEvaluationService{
		DecideFunc: func(context.Context, app.DecideRequest) (app.DecisionResult, error) {
			decideCalled = true
			return app.DecisionResult{}, nil
		},
		ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			if req.AuthorizationID != "auth-1" || req.TurnID != "turn-1" {
				t.Errorf("ConsumeAuthorization request mismatch: %+v", req)
			}
			return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
		},
	}
	services.Decision = orchestrator.DecisionDeps{
		Issuer: &fakeAuthorizationIssuer{
			issueFunc: func(context.Context, domain.TurnID, string, string, string, *domain.RepositoryCheckpointID) (app.Authorization, error) {
				t.Error("IssueAuthorization must not be called on the consume flow")
				return app.Authorization{}, nil
			},
		},
	}

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()
	root.SetArgs([]string{"decision", "allow", "--turn-id", "turn-1", "--authorization-id", "auth-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("decision allow (consume flow): %v", err)
	}
	if decideCalled {
		t.Error("Decide was called on the consume flow — it must not be")
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["consumed"] != true {
		t.Errorf("consumed = %v, want true", decoded["consumed"])
	}
}

// TestApp_RootCmd_DecisionDeny_RealEndToEnd proves `decision deny` swaps to
// the real handler too, once Decision.Issuer is wired.
func TestApp_RootCmd_DecisionDeny_RealEndToEnd(t *testing.T) {
	services := fullFakeServices()
	services.Evaluation = &fakes.FakeEvaluationService{
		DecideFunc: func(context.Context, app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-1", Action: app.PolicyBlock}, nil
		},
	}
	services.Decision = orchestrator.DecisionDeps{
		Issuer: &fakeAuthorizationIssuer{},
	}

	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := a.RootCmd()
	root.SetArgs([]string{"decision", "deny", "--evaluation-id", "eval-1"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("decision deny: %v", err)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v (output: %q)", jsonErr, out.String())
	}
	if decoded["action"] != string(app.PolicyBlock) {
		t.Errorf("action = %v, want %q", decoded["action"], app.PolicyBlock)
	}
}
