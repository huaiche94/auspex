// autocheckpoint_test.go: the managed runner's half of ADR-0054 (issue
// #116) — a CHECKPOINT_AND_RUN gate decision auto-creates the checkpoint
// pair BEFORE the provider spawns, using the run's own explicit
// WorktreeID/TaskID target, and a checkpoint failure stays fail-open
// (the provider still runs; only BLOCK refuses to spawn).
package managed

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// runAutoCheckpointDoubles wires an AutoCheckpointer over recording fakes
// with NO session resolvers — the managed runner must supply its own
// explicit target, so resolver-free composition is itself part of the
// assertion.
type runAutoCheckpointDoubles struct {
	checkpointer *orchestrator.AutoCheckpointer

	stateCalls   int
	stateGotTask domain.TaskID
	repoCalls    int
	repoGotWT    domain.WorktreeID
	issuedRepoID *domain.RepositoryCheckpointID
	consumedID   string

	stateErr error
}

func newRunAutoCheckpointDoubles(eval app.EvaluationService) *runAutoCheckpointDoubles {
	d := &runAutoCheckpointDoubles{}
	d.checkpointer = &orchestrator.AutoCheckpointer{
		Checkpoints: orchestrator.CheckpointCreateDeps{
			StateCheckpoint: &fakes.FakeStateCheckpointService{
				CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
					if d.stateErr != nil {
						return domain.StateCheckpoint{}, d.stateErr
					}
					d.stateCalls++
					d.stateGotTask = req.TaskID
					return domain.StateCheckpoint{ID: "sc-run", TaskID: req.TaskID}, nil
				},
			},
			RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{
				CreateFunc: func(_ context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
					d.repoCalls++
					d.repoGotWT = req.WorktreeID
					return app.RepositoryCheckpoint{ID: "rc-run", GitHead: "deadbee", Status: "created"}, nil
				},
			},
		},
		Decision: orchestrator.DecisionDeps{
			Evaluation: eval,
			Issuer:     runIssuerFunc(func(repoID *domain.RepositoryCheckpointID) { d.issuedRepoID = repoID }),
		},
	}
	return d
}

// runIssuerFunc adapts a capture func into orchestrator.AuthorizationIssuer.
type runIssuerFunc func(repoID *domain.RepositoryCheckpointID)

func (f runIssuerFunc) IssueAuthorization(_ context.Context, turnID domain.TurnID, promptHash, _, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
	f(repoCheckpointID)
	return app.Authorization{ID: "auth-run", TurnID: turnID, PromptHash: promptHash, Decision: decision, RepositoryCheckpointID: repoCheckpointID}, nil
}

// checkpointAndRunEvaluationForRun mirrors allowingEvaluation but adds the
// ConsumeAuthorization leg the auto-checkpointer's immediate consume uses.
func checkpointAndRunEvaluationForRun(d *runAutoCheckpointDoubles) *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-run-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-run-1", Action: app.PolicyCheckpointAndRun}, nil
		},
		ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			d.consumedID = req.AuthorizationID
			return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
		},
	}
}

func TestRunner_Run_CheckpointAndRun_AutoCheckpointsBeforeSpawn(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", filepath.Join(t.TempDir(), "argv.json"))

	d := newRunAutoCheckpointDoubles(nil)
	eval := checkpointAndRunEvaluationForRun(d)
	d.checkpointer.Decision.Evaluation = eval

	persister := &runTestPersister{}
	runner := newTestRunner(persister, eval, bin)
	runner.Hooks.AutoCheckpoint = d.checkpointer

	var human bytes.Buffer
	req := baseRunRequest()
	taskID := domain.TaskID("task-run-test")
	req.TaskID = &taskID
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.Decision != app.PolicyCheckpointAndRun {
		t.Errorf("Decision = %q, want CHECKPOINT_AND_RUN", outcome.Decision)
	}
	if outcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (provider must still spawn and finish)", outcome.ExitCode)
	}
	// The explicit managed target, not a session lookup (no resolvers are
	// even wired in this fixture).
	if d.stateCalls != 1 || d.stateGotTask != "task-run-test" {
		t.Errorf("state Create calls=%d task=%q, want 1/task-run-test", d.stateCalls, d.stateGotTask)
	}
	if d.repoCalls != 1 || d.repoGotWT != "wt-run-test" {
		t.Errorf("repo Create calls=%d worktree=%q, want 1/wt-run-test (RunRequest's own WorktreeID)", d.repoCalls, d.repoGotWT)
	}
	if d.issuedRepoID == nil || *d.issuedRepoID != "rc-run" {
		t.Errorf("issued repo checkpoint binding = %v, want rc-run", d.issuedRepoID)
	}
	if d.consumedID != "auth-run" {
		t.Errorf("consumed authorization = %q, want auth-run (immediate consume)", d.consumedID)
	}
	if !strings.Contains(human.String(), "Auspex auto-checkpoint") || !strings.Contains(human.String(), "rc-run") {
		t.Errorf("HumanLog = %q, want the auto-checkpoint line naming the repository checkpoint", human.String())
	}
}

func TestRunner_Run_CheckpointAndRun_CheckpointFailure_StillSpawns(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", filepath.Join(t.TempDir(), "argv.json"))

	d := newRunAutoCheckpointDoubles(nil)
	eval := checkpointAndRunEvaluationForRun(d)
	d.checkpointer.Decision.Evaluation = eval
	d.stateErr = &domain.Error{Code: domain.ErrCodeUnavailable, Message: "state store down"}

	runner := newTestRunner(&runTestPersister{}, eval, bin)
	runner.Hooks.AutoCheckpoint = d.checkpointer

	var human bytes.Buffer
	req := baseRunRequest()
	taskID := domain.TaskID("task-run-test")
	req.TaskID = &taskID
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run must fail open on a checkpoint failure (only BLOCK refuses to spawn), got: %v", err)
	}
	if outcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 — the provider must still have run", outcome.ExitCode)
	}
	if d.repoCalls != 0 {
		t.Errorf("repo Create called %d times after a state failure", d.repoCalls)
	}
	if !strings.Contains(human.String(), "fail-open") {
		t.Errorf("HumanLog = %q, want the loud fail-open degrade line", human.String())
	}
}

func TestRunner_Run_CheckpointAndRun_NilAutoCheckpointer_StaysAdvisory(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", filepath.Join(t.TempDir(), "argv.json"))

	runner := newTestRunner(&runTestPersister{}, allowingEvaluation(app.PolicyCheckpointAndRun), bin)
	// runner.Hooks.AutoCheckpoint deliberately nil: the gate's off position.

	var human bytes.Buffer
	req := baseRunRequest()
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.Decision != app.PolicyCheckpointAndRun || outcome.ExitCode != 0 {
		t.Errorf("outcome = decision %q exit %d, want CHECKPOINT_AND_RUN/0 (advisory pre-#116 behavior)", outcome.Decision, outcome.ExitCode)
	}
	if strings.Contains(human.String(), "auto-checkpoint") {
		t.Errorf("HumanLog = %q, want no auto-checkpoint line when the gate is off", human.String())
	}
}
