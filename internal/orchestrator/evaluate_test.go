package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/gitx"
	"github.com/huaiche94/preflight/internal/orchestrator"
	"github.com/huaiche94/preflight/internal/testutil/fakes"
)

// fakeObservationLoader is a minimal local test double for
// orchestrator.UsageObservationLoader — this package's own narrow
// interface, not one of internal/testutil/fakes' frozen-port doubles.
type fakeObservationLoader struct {
	obs []domain.UsageObservation
	err error
}

func (f *fakeObservationLoader) LoadRecentObservations(ctx context.Context, sessionID domain.SessionID) ([]domain.UsageObservation, error) {
	return f.obs, f.err
}

// fakeGitSnapshotter is a local test double for orchestrator.GitSnapshotter
// so these tests never shell out to a real git binary.
type fakeGitSnapshotter struct {
	fp  gitx.Fingerprint
	err error
}

func (f *fakeGitSnapshotter) Fingerprint(ctx context.Context, path string) (gitx.Fingerprint, error) {
	return f.fp, f.err
}

func baseRequest() orchestrator.EvaluateRequest {
	return orchestrator.EvaluateRequest{
		SessionID:  domain.SessionID("sess-1"),
		TurnID:     domain.TurnID("turn-1"),
		Provider:   "claude",
		PromptHash: "deadbeef",
	}
}

func evaluatingFake(t *testing.T) *fakes.FakeEvaluationService {
	t.Helper()
	return &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{
				ID:         domain.EvaluationID("eval-1"),
				TurnID:     req.TurnID,
				Calibrated: false,
				Confidence: domain.ConfidenceLow,
			}, nil
		},
		DecideFunc: func(_ context.Context, req app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: domain.DecisionID("dec-1"), Action: app.PolicyRun}, nil
		},
	}
}

// --- Happy path ---------------------------------------------------------

func TestEvaluate_HappyPath_CallsEvaluateTurnThenDecide(t *testing.T) {
	var sawEvaluateTurn, sawDecide bool
	evalSvc := &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			sawEvaluateTurn = true
			if req.SessionID != "sess-1" || req.TurnID != "turn-1" || req.Provider != "claude" || req.PromptHash != "deadbeef" {
				t.Errorf("EvaluateTurn request mismatch: %+v", req)
			}
			return app.Evaluation{ID: domain.EvaluationID("eval-1"), TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, req app.DecideRequest) (app.DecisionResult, error) {
			sawDecide = true
			if req.EvaluationID != domain.EvaluationID("eval-1") {
				t.Errorf("Decide request EvaluationID = %q, want eval-1", req.EvaluationID)
			}
			return app.DecisionResult{ID: domain.DecisionID("dec-1"), Action: app.PolicyRun}, nil
		},
	}

	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{Evaluation: evalSvc}, baseRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !sawEvaluateTurn {
		t.Error("EvaluateTurn was never called")
	}
	if !sawDecide {
		t.Error("Decide was never called (policy application step, pipeline step 6)")
	}
	if result.Evaluation.ID != domain.EvaluationID("eval-1") {
		t.Errorf("result.Evaluation.ID = %q, want eval-1", result.Evaluation.ID)
	}
	if result.Decision.ID != domain.DecisionID("dec-1") {
		t.Errorf("result.Decision.ID = %q, want dec-1", result.Decision.ID)
	}
}

// --- Validation ----------------------------------------------------------

func TestEvaluate_ValidatesRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		req  orchestrator.EvaluateRequest
	}{
		{"missing session", orchestrator.EvaluateRequest{TurnID: "t1", Provider: "claude", PromptHash: "h"}},
		{"missing turn", orchestrator.EvaluateRequest{SessionID: "s1", Provider: "claude", PromptHash: "h"}},
		{"missing provider", orchestrator.EvaluateRequest{SessionID: "s1", TurnID: "t1", PromptHash: "h"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{Evaluation: evaluatingFake(t)}, tc.req)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			var derr *domain.Error
			if !errors.As(err, &derr) {
				t.Fatalf("err = %T, want *domain.Error", err)
			}
			if derr.Code != domain.ErrCodeValidation {
				t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeValidation)
			}
		})
	}
}

func TestEvaluate_NilEvaluationServiceFailsUnavailable(t *testing.T) {
	_, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{}, baseRequest())
	if err == nil {
		t.Fatal("expected an error with a nil EvaluationService")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T, want *domain.Error", err)
	}
	if derr.Code != domain.ErrCodeUnavailable {
		t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeUnavailable)
	}
}

// --- Fail-closed on the actual decision steps -----------------------------

func TestEvaluate_EvaluateTurnErrorPropagatesFailClosed(t *testing.T) {
	wantErr := &domain.Error{Code: domain.ErrCodeIntegrity, Message: "boom", Retryable: false}
	evalSvc := &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, _ app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{}, wantErr
		},
	}
	_, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{Evaluation: evalSvc}, baseRequest())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the exact EvaluateTurn error propagated", err)
	}
}

func TestEvaluate_DecideErrorPropagatesFailClosed(t *testing.T) {
	wantErr := &domain.Error{Code: domain.ErrCodeUnavailable, Message: "policy engine down", Retryable: false}
	evalSvc := &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: domain.EvaluationID("eval-1"), TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{}, wantErr
		},
	}
	_, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{Evaluation: evalSvc}, baseRequest())
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the exact Decide error propagated", err)
	}
}

// --- Fail-open on operational observation steps ---------------------------

func TestEvaluate_ProgressTreeLoadFailureDegradesNotAborts(t *testing.T) {
	taskID := domain.TaskID("task-1")
	req := baseRequest()
	req.TaskID = &taskID

	progressSvc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, _ domain.TaskID) (app.ProgressTreeSnapshot, error) {
			return app.ProgressTreeSnapshot{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "db hiccup"}
		},
	}

	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:   evaluatingFake(t),
		ProgressTree: progressSvc,
	}, req)
	if err != nil {
		t.Fatalf("Evaluate should fail open on a Progress Tree load error, got: %v", err)
	}
	if result.HasProgressTree {
		t.Error("HasProgressTree = true, want false (load failed)")
	}
	if result.Evaluation.ID == "" {
		t.Error("Evaluate did not still produce an Evaluation despite the Progress Tree gap")
	}
}

func TestEvaluate_ProgressTreeLoadedWhenTaskIDPresent(t *testing.T) {
	taskID := domain.TaskID("task-1")
	req := baseRequest()
	req.TaskID = &taskID

	wantSnap := app.ProgressTreeSnapshot{TaskID: taskID, Nodes: []app.ProgressNode{{ID: "node-1"}}}
	progressSvc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, gotTaskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			if gotTaskID != taskID {
				t.Errorf("Snapshot called with TaskID %q, want %q", gotTaskID, taskID)
			}
			return wantSnap, nil
		},
	}

	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:   evaluatingFake(t),
		ProgressTree: progressSvc,
	}, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasProgressTree {
		t.Fatal("HasProgressTree = false, want true")
	}
	if len(result.ProgressTree.Nodes) != 1 {
		t.Fatalf("ProgressTree.Nodes = %v, want 1 entry", result.ProgressTree.Nodes)
	}
}

func TestEvaluate_NoTaskIDSkipsProgressTreeLoad(t *testing.T) {
	called := false
	progressSvc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, _ domain.TaskID) (app.ProgressTreeSnapshot, error) {
			called = true
			return app.ProgressTreeSnapshot{}, nil
		},
	}
	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:   evaluatingFake(t),
		ProgressTree: progressSvc,
	}, baseRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if called {
		t.Error("Snapshot was called despite no TaskID on the request")
	}
	if result.HasProgressTree {
		t.Error("HasProgressTree = true, want false (no TaskID supplied)")
	}
}

func TestEvaluate_ObservationLoadFailureDegradesNotAborts(t *testing.T) {
	loader := &fakeObservationLoader{err: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "telemetry down"}}
	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:        evaluatingFake(t),
		ObservationLoader: loader,
	}, baseRequest())
	if err != nil {
		t.Fatalf("Evaluate should fail open on an observation-load error, got: %v", err)
	}
	if result.Observations != nil {
		t.Errorf("Observations = %v, want nil after a load failure", result.Observations)
	}
}

func TestEvaluate_ObservationsLoadedWhenLoaderPresent(t *testing.T) {
	in := []domain.UsageObservation{{Source: domain.SourceStatusLine, Confidence: domain.ConfidenceHigh}}
	loader := &fakeObservationLoader{obs: in}
	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:        evaluatingFake(t),
		ObservationLoader: loader,
	}, baseRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("Observations = %v, want 1 entry", result.Observations)
	}
}

func TestEvaluate_GitSnapshotFailureDegradesNotAborts(t *testing.T) {
	snap := &fakeGitSnapshotter{err: &domain.Error{Code: domain.ErrCodeUnavailable, Message: "git busy"}}
	req := baseRequest()
	req.WorktreePath = "/tmp/some/repo"

	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:  evaluatingFake(t),
		GitSnapshot: snap,
	}, req)
	if err != nil {
		t.Fatalf("Evaluate should fail open on a Git snapshot error, got: %v", err)
	}
	if result.HasGitSnapshot {
		t.Error("HasGitSnapshot = true, want false (snapshot failed)")
	}
}

func TestEvaluate_GitSnapshotCapturedWhenWorktreePathPresent(t *testing.T) {
	wantFP := gitx.Fingerprint{Digest: "abc123", HeadOID: "deadbeef"}
	snap := &fakeGitSnapshotter{fp: wantFP}
	req := baseRequest()
	req.WorktreePath = "/tmp/some/repo"

	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:  evaluatingFake(t),
		GitSnapshot: snap,
	}, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasGitSnapshot {
		t.Fatal("HasGitSnapshot = false, want true")
	}
	if result.GitFingerprint.Digest != "abc123" {
		t.Errorf("GitFingerprint.Digest = %q, want abc123", result.GitFingerprint.Digest)
	}
}

func TestEvaluate_NoWorktreePathSkipsGitSnapshot(t *testing.T) {
	called := false
	wrapped := &countingSnapshotter{inner: &fakeGitSnapshotter{}, called: &called}

	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation:  evaluatingFake(t),
		GitSnapshot: wrapped,
	}, baseRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if called {
		t.Error("Fingerprint was called despite no WorktreePath on the request")
	}
	if result.HasGitSnapshot {
		t.Error("HasGitSnapshot = true, want false (no WorktreePath supplied)")
	}
}

type countingSnapshotter struct {
	inner  orchestrator.GitSnapshotter
	called *bool
}

func (c *countingSnapshotter) Fingerprint(ctx context.Context, path string) (gitx.Fingerprint, error) {
	*c.called = true
	return c.inner.Fingerprint(ctx, path)
}

// --- Nil-safe optional deps ------------------------------------------------

func TestEvaluate_AllOptionalDepsNil_StillSucceeds(t *testing.T) {
	result, err := orchestrator.Evaluate(context.Background(), orchestrator.Deps{
		Evaluation: evaluatingFake(t),
		// ProgressTree, ObservationLoader, GitSnapshot all nil.
	}, baseRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.HasProgressTree || result.HasGitSnapshot || result.Observations != nil {
		t.Errorf("expected all-degraded result with nil optional deps, got %+v", result)
	}
	if result.Evaluation.ID == "" {
		t.Error("Evaluation was not populated despite the required EvaluationService being present")
	}
}
