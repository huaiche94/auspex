// hooksprecompact_test.go: coverage for the issue-#114 compaction hook
// handlers (hooksprecompact.go) — the pre-compaction checkpoint capture's
// ordering and fail-open discipline, the persisted event's checkpoint
// outcome payload, and the codex twins. Follows hooks_test.go's
// fakes-based conventions (recordingPersister, fixedClock, deterministic
// IDs) plus internal/testutil/fakes' checkpoint service doubles.
package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// precompactSessionResolver is a package-local narrow double for
// orchestrator.SessionResolver (correlate_test.go has its own; test files
// in one package share a namespace, hence the distinct name).
type precompactSessionResolver struct {
	resolved app.ResolvedSession
	err      error
	calls    int
}

func (r *precompactSessionResolver) Resolve(_ context.Context, _ domain.SessionID) (app.ResolvedSession, error) {
	r.calls++
	return r.resolved, r.err
}

type fakeWorktreeResolver struct {
	worktreeID domain.WorktreeID
	ok         bool
}

func (r *fakeWorktreeResolver) WorktreeForSession(_ context.Context, _ domain.SessionID) (domain.WorktreeID, bool) {
	return r.worktreeID, r.ok
}

func taskIDPtr(id string) *domain.TaskID {
	t := domain.TaskID(id)
	return &t
}

// newCaptureCheckpointer builds a fully-wired CompactCheckpointer whose
// checkpoint fakes record call order into the given slice.
func newCaptureCheckpointer(order *[]string) *orchestrator.CompactCheckpointer {
	return &orchestrator.CompactCheckpointer{
		Sessions:  &precompactSessionResolver{resolved: app.ResolvedSession{RepositoryID: "repo-1", TaskID: taskIDPtr("task-1")}},
		Worktrees: &fakeWorktreeResolver{worktreeID: "worktree-1", ok: true},
		State: &fakes.FakeStateCheckpointService{
			CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				*order = append(*order, "state:"+string(req.TaskID))
				return domain.StateCheckpoint{ID: "sc-1", TaskID: req.TaskID}, nil
			},
		},
		Repository: &fakes.FakeRepositoryCheckpointService{
			CreateFunc: func(_ context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
				*order = append(*order, "repo:"+string(req.WorktreeID))
				return app.RepositoryCheckpoint{ID: "rc-1", Status: "captured"}, nil
			},
		},
	}
}

const preCompactPayload = `{
	"session_id": "sess-1",
	"hook_event_name": "PreCompact",
	"cwd": "/repo",
	"trigger": "auto",
	"custom_instructions": ""
}`

func TestHandlePreCompact_CapturesCheckpointPairBeforePersist(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	var order []string
	deps.CompactCheckpoint = newCaptureCheckpointer(&order)

	result, err := orchestrator.HandlePreCompact(context.Background(), deps, []byte(preCompactPayload))
	if err != nil {
		t.Fatalf("HandlePreCompact: %v", err)
	}
	if !result.CheckpointCaptured {
		t.Fatalf("CheckpointCaptured = false (skip reason %q), want true", result.CheckpointSkipReason)
	}
	if result.EventsNormalized != 1 || !result.Persisted {
		t.Errorf("result = %+v, want 1 event persisted", result)
	}

	// The frozen CheckpointCreate ordering: state FIRST, then repository.
	if len(order) != 2 || order[0] != "state:task-1" || order[1] != "repo:worktree-1" {
		t.Fatalf("checkpoint call order = %v, want [state:task-1 repo:worktree-1]", order)
	}

	if len(persister.calls) != 1 || len(persister.calls[0]) != 1 {
		t.Fatalf("persister.calls = %v, want one call with one event", persister.calls)
	}
	ev := persister.calls[0][0]
	if ev.EventType != v1.EventProviderSessionCompacted {
		t.Errorf("EventType = %q, want provider.session.compacted", ev.EventType)
	}
	if got := ev.Payload["phase"]; got != "pre" {
		t.Errorf("payload phase = %v, want pre", got)
	}
	if got := ev.Payload["checkpoint_captured"]; got != true {
		t.Errorf("payload checkpoint_captured = %v, want true", got)
	}
	if got := ev.Payload["state_checkpoint_id"]; got != "sc-1" {
		t.Errorf("payload state_checkpoint_id = %v, want sc-1", got)
	}
	if got := ev.Payload["repository_checkpoint_id"]; got != "rc-1" {
		t.Errorf("payload repository_checkpoint_id = %v, want rc-1", got)
	}
	if got := ev.Payload["trigger"]; got != "auto" {
		t.Errorf("payload trigger = %v, want auto", got)
	}
}

// TestHandlePreCompact_CheckpointFailureFailsOpen pins issue #114's core
// design constraint: a checkpoint failure must never fail the hook (which
// would block/disturb the provider's compaction) — it is recorded on the
// persisted event instead.
func TestHandlePreCompact_CheckpointFailureFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	deps.CompactCheckpoint = &orchestrator.CompactCheckpointer{
		Sessions:  &precompactSessionResolver{resolved: app.ResolvedSession{TaskID: taskIDPtr("task-1")}},
		Worktrees: &fakeWorktreeResolver{worktreeID: "worktree-1", ok: true},
		State: &fakes.FakeStateCheckpointService{
			CreateFunc: func(_ context.Context, _ app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				return domain.StateCheckpoint{}, errors.New("disk full")
			},
		},
		Repository: &fakes.FakeRepositoryCheckpointService{
			CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
				t.Fatal("RepositoryCheckpoint.Create must not run after a state-checkpoint failure")
				return app.RepositoryCheckpoint{}, nil
			},
		},
	}

	result, err := orchestrator.HandlePreCompact(context.Background(), deps, []byte(preCompactPayload))
	if err != nil {
		t.Fatalf("a checkpoint failure must not fail the hook: %v", err)
	}
	if result.CheckpointCaptured {
		t.Error("CheckpointCaptured = true, want false")
	}
	if result.CheckpointSkipReason != orchestrator.CompactSkipCheckpointFailed {
		t.Errorf("CheckpointSkipReason = %q, want %q", result.CheckpointSkipReason, orchestrator.CompactSkipCheckpointFailed)
	}
	if !result.Persisted {
		t.Error("the observation event must still persist after a failed capture")
	}
	ev := persister.calls[0][0]
	if got := ev.Payload["checkpoint_captured"]; got != false {
		t.Errorf("payload checkpoint_captured = %v, want false", got)
	}
	if got := ev.Payload["checkpoint_skip_reason"]; got != orchestrator.CompactSkipCheckpointFailed {
		t.Errorf("payload checkpoint_skip_reason = %v, want %q", got, orchestrator.CompactSkipCheckpointFailed)
	}
}

func TestHandlePreCompact_SkipReasons(t *testing.T) {
	cases := []struct {
		name       string
		checkpoint *orchestrator.CompactCheckpointer
		want       string
	}{
		{
			name:       "nil capturer -> not_configured",
			checkpoint: nil,
			want:       orchestrator.CompactSkipNotConfigured,
		},
		{
			name:       "missing collaborators -> not_configured",
			checkpoint: &orchestrator.CompactCheckpointer{},
			want:       orchestrator.CompactSkipNotConfigured,
		},
		{
			name: "resolver error -> session_unresolved",
			checkpoint: &orchestrator.CompactCheckpointer{
				Sessions:   &precompactSessionResolver{err: errors.New("not found")},
				Worktrees:  &fakeWorktreeResolver{},
				State:      &fakes.FakeStateCheckpointService{},
				Repository: &fakes.FakeRepositoryCheckpointService{},
			},
			want: orchestrator.CompactSkipSessionUnresolved,
		},
		{
			name: "no task -> no_task",
			checkpoint: &orchestrator.CompactCheckpointer{
				Sessions:   &precompactSessionResolver{resolved: app.ResolvedSession{RepositoryID: "repo-1"}},
				Worktrees:  &fakeWorktreeResolver{},
				State:      &fakes.FakeStateCheckpointService{},
				Repository: &fakes.FakeRepositoryCheckpointService{},
			},
			want: orchestrator.CompactSkipNoTask,
		},
		{
			name: "no worktree -> no_worktree",
			checkpoint: &orchestrator.CompactCheckpointer{
				Sessions:   &precompactSessionResolver{resolved: app.ResolvedSession{TaskID: taskIDPtr("task-1")}},
				Worktrees:  &fakeWorktreeResolver{ok: false},
				State:      &fakes.FakeStateCheckpointService{},
				Repository: &fakes.FakeRepositoryCheckpointService{},
			},
			want: orchestrator.CompactSkipNoWorktree,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := baseHookDeps()
			persister := &recordingPersister{}
			deps.Persister = persister
			deps.TxRunner = noopTxRunner{}
			deps.CompactCheckpoint = tc.checkpoint

			result, err := orchestrator.HandlePreCompact(context.Background(), deps, []byte(preCompactPayload))
			if err != nil {
				t.Fatalf("HandlePreCompact: %v", err)
			}
			if result.CheckpointCaptured {
				t.Error("CheckpointCaptured = true, want false")
			}
			if result.CheckpointSkipReason != tc.want {
				t.Errorf("CheckpointSkipReason = %q, want %q", result.CheckpointSkipReason, tc.want)
			}
			if !result.Persisted {
				t.Error("the observation event must persist regardless of capture outcome")
			}
			if got := persister.calls[0][0].Payload["checkpoint_skip_reason"]; got != tc.want {
				t.Errorf("payload checkpoint_skip_reason = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestHandlePreCompact_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandlePreCompact(context.Background(), deps, []byte(`{not json`))
	if err != nil {
		t.Fatalf("HandlePreCompact on malformed input should fail open (nil error), got: %v", err)
	}
	if result.EventsNormalized != 0 || result.Persisted {
		t.Errorf("result = %+v, want zero result on malformed input", result)
	}
	if len(persister.calls) != 0 {
		t.Errorf("nothing must persist for malformed input, got %v", persister.calls)
	}
}

func TestHandlePostCompact_PersistsPostPhaseEvent(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandlePostCompact(context.Background(), deps, []byte(`{
		"session_id": "sess-1",
		"hook_event_name": "PostCompact",
		"cwd": "/repo",
		"trigger": "auto"
	}`))
	if err != nil {
		t.Fatalf("HandlePostCompact: %v", err)
	}
	if result.EventsNormalized != 1 || !result.Persisted {
		t.Errorf("result = %+v, want 1 event persisted", result)
	}
	ev := persister.calls[0][0]
	if ev.EventType != v1.EventProviderSessionCompacted {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if got := ev.Payload["phase"]; got != "post" {
		t.Errorf("payload phase = %v, want post", got)
	}
	if _, present := ev.Payload["checkpoint_captured"]; present {
		t.Error("a post-compaction event must not carry checkpoint keys")
	}
}

func TestHandlePostCompact_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandlePostCompact(context.Background(), deps, []byte(`]`))
	if err != nil {
		t.Fatalf("fail-open expected, got: %v", err)
	}
	if result.EventsNormalized != 0 {
		t.Errorf("EventsNormalized = %d, want 0", result.EventsNormalized)
	}
}

// --- codex twins ------------------------------------------------------------

const codexPreCompactPayload = `{
	"session_id": "019f-sess",
	"hook_event_name": "PreCompact",
	"cwd": "/repo",
	"model": "gpt-5.2-codex",
	"trigger": "auto"
}`

func TestHandleCodexPreCompact_CapturesAndPersists(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}
	var order []string
	deps.CompactCheckpoint = newCaptureCheckpointer(&order)

	result, err := orchestrator.HandleCodexPreCompact(context.Background(), deps, []byte(codexPreCompactPayload))
	if err != nil {
		t.Fatalf("HandleCodexPreCompact: %v", err)
	}
	if !result.CheckpointCaptured {
		t.Fatalf("CheckpointCaptured = false (skip reason %q), want true", result.CheckpointSkipReason)
	}
	if len(order) != 2 || order[0] != "state:task-1" || order[1] != "repo:worktree-1" {
		t.Fatalf("checkpoint call order = %v, want state first then repository", order)
	}
	ev := persister.calls[0][0]
	if ev.EventType != v1.EventProviderSessionCompacted {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if ev.Provider != "codex" {
		t.Errorf("Provider = %q, want codex", ev.Provider)
	}
	if got := ev.Payload["phase"]; got != "pre" {
		t.Errorf("payload phase = %v, want pre", got)
	}
	if got := ev.Payload["model_id"]; got != "gpt-5.2-codex" {
		t.Errorf("payload model_id = %v", got)
	}
	if got := ev.Payload["checkpoint_captured"]; got != true {
		t.Errorf("payload checkpoint_captured = %v, want true", got)
	}
}

func TestHandleCodexPreCompact_MalformedInputFailsOpen(t *testing.T) {
	deps := baseHookDeps()
	result, err := orchestrator.HandleCodexPreCompact(context.Background(), deps, []byte(`{`))
	if err != nil {
		t.Fatalf("fail-open expected, got: %v", err)
	}
	if result.EventsNormalized != 0 {
		t.Errorf("EventsNormalized = %d, want 0", result.EventsNormalized)
	}
}

func TestHandleCodexPostCompact_PersistsPostPhaseEvent(t *testing.T) {
	deps := baseHookDeps()
	persister := &recordingPersister{}
	deps.Persister = persister
	deps.TxRunner = noopTxRunner{}

	result, err := orchestrator.HandleCodexPostCompact(context.Background(), deps, []byte(`{
		"session_id": "019f-sess",
		"hook_event_name": "PostCompact",
		"model": "gpt-5.2-codex"
	}`))
	if err != nil {
		t.Fatalf("HandleCodexPostCompact: %v", err)
	}
	if result.EventsNormalized != 1 || !result.Persisted {
		t.Errorf("result = %+v, want 1 event persisted", result)
	}
	ev := persister.calls[0][0]
	if got := ev.Payload["phase"]; got != "post" {
		t.Errorf("payload phase = %v, want post", got)
	}
}

// TestCompactCheckpointer_CaptureNeverErrors is the direct unit pin on the
// capturer's fail-open contract (every failure mode is an outcome, never a
// panic/error) including the nil receiver.
func TestCompactCheckpointer_NilReceiverIsNotConfigured(t *testing.T) {
	var c *orchestrator.CompactCheckpointer
	outcome := c.Capture(context.Background(), "sess-1")
	if outcome.Captured || outcome.Attempted {
		t.Errorf("outcome = %+v, want unattempted", outcome)
	}
	if outcome.SkipReason != orchestrator.CompactSkipNotConfigured {
		t.Errorf("SkipReason = %q, want %q", outcome.SkipReason, orchestrator.CompactSkipNotConfigured)
	}
}
