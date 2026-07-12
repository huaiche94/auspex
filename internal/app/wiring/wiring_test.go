package wiring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/app/wiring"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/testutil/fakes"
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
	if root.Use != "preflight" {
		t.Errorf("root.Use = %q, want %q", root.Use, "preflight")
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
// `preflight hook claude user-prompt-submit` on the App-built tree is
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
// "hook fallback remains syntactically valid when Preflight fails"
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
