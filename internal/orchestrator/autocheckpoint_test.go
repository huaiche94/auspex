// autocheckpoint_test.go: ADR-0054's automatic pre-turn checkpoint
// (issue #116) — AutoCheckpointer.Run's fail-open contract and the
// existing-machinery authorization threading, SessionWorktreeStore
// against a real migrated DB, and the UserPromptSubmit hook integration
// (auto-checkpoint on CHECKPOINT_AND_RUN, byte-identical behavior when
// the gate is off, and hot-path discipline for ordinary decisions).
package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// --- narrow-seam fakes ------------------------------------------------------

type acpSessionResolver struct {
	resolved app.ResolvedSession
	err      error
}

func (f acpSessionResolver) Resolve(_ context.Context, _ domain.SessionID) (app.ResolvedSession, error) {
	return f.resolved, f.err
}

type acpWorktreeResolver struct {
	id domain.WorktreeID
	ok bool
}

func (f acpWorktreeResolver) WorktreeForSession(_ context.Context, _ domain.SessionID) (domain.WorktreeID, bool) {
	return f.id, f.ok
}

// acpIssuer implements orchestrator.AuthorizationIssuer, recording the
// binding DecisionAllowCmd's issue flow threads through.
type acpIssuer struct {
	err error

	calls       int
	gotTurnID   domain.TurnID
	gotHash     string
	gotDecision string
	gotRepoID   *domain.RepositoryCheckpointID
}

func (f *acpIssuer) IssueAuthorization(_ context.Context, turnID domain.TurnID, promptHash, _, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
	f.calls++
	f.gotTurnID = turnID
	f.gotHash = promptHash
	f.gotDecision = decision
	f.gotRepoID = repoCheckpointID
	if f.err != nil {
		return app.Authorization{}, f.err
	}
	return app.Authorization{ID: "auth-116", TurnID: turnID, PromptHash: promptHash, Decision: decision, RepositoryCheckpointID: repoCheckpointID}, nil
}

// autoCheckpointFixture bundles a fully-working AutoCheckpointer and the
// recording doubles behind it; individual tests then break one seam.
type autoCheckpointFixture struct {
	checkpointer *orchestrator.AutoCheckpointer
	issuer       *acpIssuer

	stateCalls   int
	stateGotTask domain.TaskID
	repoCalls    int
	repoGotWT    domain.WorktreeID
	consumeCalls int
	consumedID   string
}

func newAutoCheckpointFixture(taskID domain.TaskID, worktreeID domain.WorktreeID) *autoCheckpointFixture {
	fx := &autoCheckpointFixture{issuer: &acpIssuer{}}
	state := &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			fx.stateCalls++
			fx.stateGotTask = req.TaskID
			return domain.StateCheckpoint{ID: "sc-116", TaskID: req.TaskID}, nil
		},
	}
	repo := &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			fx.repoCalls++
			fx.repoGotWT = req.WorktreeID
			return app.RepositoryCheckpoint{ID: "rc-116", GitHead: "abc123", Status: "created"}, nil
		},
	}
	eval := &fakes.FakeEvaluationService{
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-116", Action: app.PolicyCheckpointAndRun}, nil
		},
		ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			fx.consumeCalls++
			fx.consumedID = req.AuthorizationID
			return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
		},
	}
	var sessions orchestrator.SessionResolver
	if taskID != "" {
		tid := taskID
		sessions = acpSessionResolver{resolved: app.ResolvedSession{RepositoryID: "repo-1", TaskID: &tid}}
	}
	fx.checkpointer = &orchestrator.AutoCheckpointer{
		Checkpoints: orchestrator.CheckpointCreateDeps{StateCheckpoint: state, RepositoryCheckpoint: repo},
		Decision:    orchestrator.DecisionDeps{Evaluation: eval, Issuer: fx.issuer},
		Sessions:    sessions,
		Worktrees:   acpWorktreeResolver{id: worktreeID, ok: worktreeID != ""},
	}
	return fx
}

func baseAutoCheckpointRequest() orchestrator.AutoCheckpointRequest {
	return orchestrator.AutoCheckpointRequest{
		SessionID:    "sess-116",
		TurnID:       "turn-116",
		EvaluationID: "eval-116",
		PromptHash:   "hash-116",
	}
}

// --- AutoCheckpointer.Run ---------------------------------------------------

func TestAutoCheckpointer_NilReceiver_IsAdvisoryNoOp(t *testing.T) {
	var a *orchestrator.AutoCheckpointer
	out := a.Run(context.Background(), baseAutoCheckpointRequest())
	if out.Attempted || out.Created || out.Warning != "" {
		t.Errorf("nil AutoCheckpointer outcome = %+v, want the zero (advisory) outcome", out)
	}
	if line := out.ContextLine(); line != "" {
		t.Errorf("ContextLine() = %q, want empty for the disabled gate", line)
	}
}

func TestAutoCheckpointer_HappyPath_CreatesBothAndRecordsBinding(t *testing.T) {
	fx := newAutoCheckpointFixture("task-1", "wt-1")

	out := fx.checkpointer.Run(context.Background(), baseAutoCheckpointRequest())

	if !out.Attempted || !out.Created {
		t.Fatalf("outcome = %+v, want Attempted+Created", out)
	}
	if out.Warning != "" {
		t.Errorf("Warning = %q, want empty on the happy path", out.Warning)
	}
	if out.StateCheckpointID != "sc-116" || out.RepositoryCheckpointID != "rc-116" {
		t.Errorf("checkpoint IDs = %q/%q, want sc-116/rc-116", out.StateCheckpointID, out.RepositoryCheckpointID)
	}
	if fx.stateCalls != 1 || fx.stateGotTask != "task-1" {
		t.Errorf("state Create calls=%d task=%q, want 1/task-1", fx.stateCalls, fx.stateGotTask)
	}
	if fx.repoCalls != 1 || fx.repoGotWT != "wt-1" {
		t.Errorf("repo Create calls=%d worktree=%q, want 1/wt-1", fx.repoCalls, fx.repoGotWT)
	}
	// The existing decision-allow machinery: issue with the repository
	// checkpoint ID bound, then immediate consume of that authorization.
	if fx.issuer.calls != 1 || fx.issuer.gotRepoID == nil || *fx.issuer.gotRepoID != "rc-116" {
		t.Fatalf("issuer calls=%d repoID=%v, want 1 call binding rc-116", fx.issuer.calls, fx.issuer.gotRepoID)
	}
	if fx.issuer.gotTurnID != "turn-116" || fx.issuer.gotHash != "hash-116" {
		t.Errorf("issuer binding = turn %q hash %q, want turn-116/hash-116", fx.issuer.gotTurnID, fx.issuer.gotHash)
	}
	if fx.issuer.gotDecision != string(app.PolicyCheckpointAndRun) {
		t.Errorf("issuer decision = %q, want %q", fx.issuer.gotDecision, app.PolicyCheckpointAndRun)
	}
	if out.AuthorizationID != "auth-116" || fx.consumeCalls != 1 || fx.consumedID != "auth-116" {
		t.Errorf("authorization = %q, consume calls=%d id=%q — want auth-116 issued and immediately consumed", out.AuthorizationID, fx.consumeCalls, fx.consumedID)
	}
	if line := out.ContextLine(); !strings.Contains(line, "sc-116") || !strings.Contains(line, "rc-116") {
		t.Errorf("ContextLine() = %q, want both checkpoint IDs named", line)
	}
}

func TestAutoCheckpointer_ExplicitTarget_SkipsSessionResolution(t *testing.T) {
	fx := newAutoCheckpointFixture("", "") // no resolvers configured at all
	req := baseAutoCheckpointRequest()
	req.TaskID = "task-explicit"
	req.WorktreeID = "wt-explicit"

	out := fx.checkpointer.Run(context.Background(), req)

	if !out.Created {
		t.Fatalf("outcome = %+v, want Created via the explicit managed-runner target", out)
	}
	if fx.stateGotTask != "task-explicit" || fx.repoGotWT != "wt-explicit" {
		t.Errorf("checkpoint target = %q/%q, want the explicit task-explicit/wt-explicit", fx.stateGotTask, fx.repoGotWT)
	}
}

func TestAutoCheckpointer_NoTaskResolved_SkipsFailOpen(t *testing.T) {
	fx := newAutoCheckpointFixture("task-1", "wt-1")
	fx.checkpointer.Sessions = acpSessionResolver{resolved: app.ResolvedSession{RepositoryID: "repo-1"}} // cold start: nil TaskID

	out := fx.checkpointer.Run(context.Background(), baseAutoCheckpointRequest())

	if !out.Attempted || out.Created {
		t.Fatalf("outcome = %+v, want Attempted but not Created", out)
	}
	if !strings.Contains(out.Warning, "no task resolved") {
		t.Errorf("Warning = %q, want a no-task-resolved warning", out.Warning)
	}
	if fx.stateCalls != 0 || fx.repoCalls != 0 {
		t.Errorf("checkpoint services called (%d/%d) despite unresolvable target — must skip entirely", fx.stateCalls, fx.repoCalls)
	}
	if line := out.ContextLine(); !strings.Contains(line, "fail-open") {
		t.Errorf("ContextLine() = %q, want the fail-open skip line", line)
	}
}

func TestAutoCheckpointer_StateCheckpointError_FailsOpen(t *testing.T) {
	fx := newAutoCheckpointFixture("task-1", "wt-1")
	fx.checkpointer.Checkpoints.StateCheckpoint = &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "state store down"}
		},
	}

	out := fx.checkpointer.Run(context.Background(), baseAutoCheckpointRequest())

	if out.Created {
		t.Fatal("Created = true, want false after a state-checkpoint failure")
	}
	if !strings.Contains(out.Warning, "checkpoint create failed") {
		t.Errorf("Warning = %q, want a checkpoint-create failure warning", out.Warning)
	}
	if fx.repoCalls != 0 {
		t.Errorf("repo Create called %d times after state failure — CheckpointCreate's ordering must hold", fx.repoCalls)
	}
	if fx.issuer.calls != 0 {
		t.Errorf("issuer called %d times with no checkpoint to bind", fx.issuer.calls)
	}
}

func TestAutoCheckpointer_IssuanceError_CheckpointStillStands(t *testing.T) {
	fx := newAutoCheckpointFixture("task-1", "wt-1")
	fx.issuer.err = &domain.Error{Code: domain.ErrCodeUnavailable, Message: "authorizations table locked"}

	out := fx.checkpointer.Run(context.Background(), baseAutoCheckpointRequest())

	if !out.Created {
		t.Fatal("Created = false, want true — a recording failure must not un-create the checkpoint")
	}
	if out.AuthorizationID != "" {
		t.Errorf("AuthorizationID = %q, want empty after an issuance failure", out.AuthorizationID)
	}
	if !strings.Contains(out.Warning, "authorization issuance failed") {
		t.Errorf("Warning = %q, want an issuance-failure warning", out.Warning)
	}
	// The warning must ride the Created line, not replace it.
	if line := out.ContextLine(); !strings.Contains(line, "rc-116") || !strings.Contains(line, "Warning:") {
		t.Errorf("ContextLine() = %q, want the created line carrying the warning", line)
	}
}

// --- SessionWorktreeStore ---------------------------------------------------

func TestSessionWorktreeStore_ResolvesBinding_AndFailsOpen(t *testing.T) {
	db := openTurnTestDB(t)
	ctx := context.Background()
	seed := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Conn().ExecContext(ctx, stmt, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	seed(`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
	      VALUES ('repo-1', '/tmp/r', '/tmp/r/.git', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`)
	seed(`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
	      VALUES ('wt-1', 'repo-1', '/tmp/r', '/tmp/r/.git', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`)
	seed(`INSERT INTO provider_sessions (id, worktree_id, provider, provider_session_id, invocation_mode, started_at)
	      VALUES ('sess-1', 'wt-1', 'claude', 'sess-1', 'native-hook', '2026-07-18T00:00:00Z')`)

	store := &orchestrator.SessionWorktreeStore{DB: db}
	if wt, ok := store.WorktreeForSession(ctx, "sess-1"); !ok || wt != "wt-1" {
		t.Errorf("WorktreeForSession(sess-1) = %q/%v, want wt-1/true", wt, ok)
	}
	if _, ok := store.WorktreeForSession(ctx, "sess-unknown"); ok {
		t.Error("WorktreeForSession(sess-unknown) ok = true, want false")
	}
	if _, ok := store.WorktreeForSession(ctx, ""); ok {
		t.Error("WorktreeForSession(\"\") ok = true, want false")
	}
	var nilStore *orchestrator.SessionWorktreeStore
	if _, ok := nilStore.WorktreeForSession(ctx, "sess-1"); ok {
		t.Error("nil store ok = true, want false (fail-open)")
	}
}

// --- HandleUserPromptSubmit integration ------------------------------------

func checkpointAndRunEvaluation() *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-116", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-116", Action: app.PolicyCheckpointAndRun}, nil
		},
		ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
		},
	}
}

func TestHookHandlers_UserPromptSubmit_CheckpointAndRun_AutoCheckpointsBeforeAllow(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = checkpointAndRunEvaluation()
	fx := newAutoCheckpointFixture("task-1", "wt-1")
	// The checkpointer's own decision read-back must see the same fake.
	fx.checkpointer.Decision.Evaluation = deps.Evaluation
	deps.AutoCheckpoint = fx.checkpointer

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow (CHECKPOINT_AND_RUN proceeds after the checkpoint)", result.Response.Decision)
	}
	if result.AutoCheckpoint == nil || !result.AutoCheckpoint.Created {
		t.Fatalf("AutoCheckpoint = %+v, want a Created outcome", result.AutoCheckpoint)
	}
	if fx.stateCalls != 1 || fx.repoCalls != 1 {
		t.Errorf("checkpoint service calls = %d/%d, want 1/1", fx.stateCalls, fx.repoCalls)
	}
	if fx.issuer.gotHash == "" || fx.issuer.gotTurnID == "" {
		t.Errorf("issuer binding = turn %q hash %q, want the hook turn's own values threaded", fx.issuer.gotTurnID, fx.issuer.gotHash)
	}
	if !strings.Contains(result.Response.AdditionalContext, "Auspex auto-checkpoint") {
		t.Errorf("AdditionalContext = %q, want the auto-checkpoint line surfaced to the agent", result.Response.AdditionalContext)
	}
}

func TestHookHandlers_UserPromptSubmit_CheckpointAndRun_GateOff_StaysAdvisory(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = checkpointAndRunEvaluation()
	// deps.AutoCheckpoint deliberately nil: the config gate's off position.

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow", result.Response.Decision)
	}
	if result.AutoCheckpoint != nil {
		t.Errorf("AutoCheckpoint = %+v, want nil (advisory pre-#116 behavior)", result.AutoCheckpoint)
	}
	if strings.Contains(result.Response.AdditionalContext, "auto-checkpoint") {
		t.Errorf("AdditionalContext = %q, want no auto-checkpoint line when the gate is off", result.Response.AdditionalContext)
	}
}

func TestHookHandlers_UserPromptSubmit_CheckpointAndRun_FailureStillAllows(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = checkpointAndRunEvaluation()
	fx := newAutoCheckpointFixture("task-1", "wt-1")
	fx.checkpointer.Decision.Evaluation = deps.Evaluation
	fx.checkpointer.Checkpoints.StateCheckpoint = &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "state store down"}
		},
	}
	deps.AutoCheckpoint = fx.checkpointer

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit must fail open on a checkpoint failure, got: %v", err)
	}
	if result.Response.Decision != claudehooks.HookDecisionAllow {
		t.Errorf("Decision = %q, want allow — the safety net failing must never block the turn", result.Response.Decision)
	}
	if result.AutoCheckpoint == nil || result.AutoCheckpoint.Created || result.AutoCheckpoint.Warning == "" {
		t.Fatalf("AutoCheckpoint = %+v, want an Attempted, not-Created outcome with a warning", result.AutoCheckpoint)
	}
	if !strings.Contains(result.Response.AdditionalContext, "fail-open") {
		t.Errorf("AdditionalContext = %q, want the fail-open warning surfaced", result.Response.AdditionalContext)
	}
}

func TestHookHandlers_UserPromptSubmit_OrdinaryDecision_NeverTouchesCheckpointServices(t *testing.T) {
	deps := baseHookDeps()
	deps.Evaluation = &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-1", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{Action: app.PolicyRun}, nil
		},
	}
	fx := newAutoCheckpointFixture("task-1", "wt-1")
	deps.AutoCheckpoint = fx.checkpointer

	result, err := orchestrator.HandleUserPromptSubmit(context.Background(), deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if fx.stateCalls != 0 || fx.repoCalls != 0 || fx.issuer.calls != 0 {
		t.Errorf("checkpoint machinery touched (%d/%d/%d) on a RUN decision — the hot path must never pay checkpoint latency", fx.stateCalls, fx.repoCalls, fx.issuer.calls)
	}
	if result.AutoCheckpoint != nil {
		t.Errorf("AutoCheckpoint = %+v, want nil on a RUN decision", result.AutoCheckpoint)
	}
}
