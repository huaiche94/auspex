package pause_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// --- deterministic Clock/IDGenerator test doubles ---------------------------

type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

type seqIDs struct {
	counter atomic.Int64
	prefix  string
}

func (g *seqIDs) NewID() string {
	n := g.counter.Add(1)
	return g.prefix + "-" + itoa(n)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// --- real SQLite DB + real Git repository test harness ----------------------
//
// runtime-a05's task brief requires the Repository Checkpoint step to use
// the REAL internal/repocheckpoint.Service (checkpoint-b04, integrated on
// main since Wave 5), not a fake — this harness builds one against a real,
// migrated temp-file SQLite database and a real temporary Git repository,
// mirroring internal/repocheckpoint's own service_test.go /
// internal/scheduler's lease_test.go seeding conventions (both public-API
// only; this package does not reach into either's test-internal helpers).

func openMigratedDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
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
	return db
}

// seedChain inserts a minimal repositories -> worktrees -> provider_sessions
// -> tasks chain so a Repository Checkpoint's own worktree FK and this
// test's PersistRequest.TaskID/WorktreeID are both satisfiable.
func seedChain(t *testing.T, db *sqlite.DB, worktreeID domain.WorktreeID, taskID domain.TaskID) {
	t.Helper()
	now := "2026-07-12T10:00:00Z"
	stmts := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('` + string(worktreeID) + `', 'repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('sess1', '` + string(worktreeID) + `', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('` + string(taskID) + `', 'sess1', '` + string(worktreeID) + `', 'hash1', 'pending', '` + now + `', '` + now + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// seedPauseRecordRow inserts a row into the REAL pause_records table
// (runtime-a01's migration 0050) for pauseID, so wake_jobs.pause_id's real
// FK constraint (0051_wake_jobs.sql) is satisfiable when Persist's phase 5
// calls scheduler.Store.Schedule against the same DB. This is deliberately
// separate from this package's own in-memory pause.MemStore (seeded by
// seedPauseRecord, below): PersistPauseStore (pause.MemStore in these
// tests) is the durable record THIS package's own persist-progress
// bookkeeping lives on, while pause_records the SQL TABLE is what
// runtime-a06's scheduler depends on via a real foreign key — the two are
// today two different backing stores for what is conceptually one pause
// record (a documented gap tracked in this node's report, not silently
// papered over: a future integration node reconciles PersistPauseStore
// onto a real SQLite-backed PauseStore against this same table).
func seedPauseRecordRow(t *testing.T, db *sqlite.DB, pauseID domain.PauseID, taskID domain.TaskID) {
	t.Helper()
	now := "2026-07-12T10:00:00Z"
	_, err := db.Conn().ExecContext(context.Background(), `
		INSERT INTO pause_records (id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
		VALUES (?, ?, 'sess1', 'turn1', 'rf1', 'checkpointing', ?, 1)
	`, string(pauseID), string(taskID), now)
	if err != nil {
		t.Fatalf("seedPauseRecordRow: %v", err)
	}
}

// newRepoBuilder creates a real, temporary Git repository (skips the test
// if git is unavailable), mirroring internal/repocheckpoint's own
// unexported repoBuilder helper (not reusable across package boundaries).
type repoBuilder struct {
	t   *testing.T
	dir string
}

func newRepoBuilder(t *testing.T) *repoBuilder {
	t.Helper()
	runner := gitx.ExecRunner{}
	dir, err := os.MkdirTemp("", "auspex-persistphase-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	rb := &repoBuilder{t: t, dir: resolved}
	if _, err := runner.Run(context.Background(), rb.dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	rb.git("init", "-q", "-b", "main")
	rb.git("config", "user.name", "Auspex Test")
	rb.git("config", "user.email", "test@auspex.invalid")
	rb.git("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(rb.dir, "a.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")
	return rb
}

func (rb *repoBuilder) git(args ...string) {
	rb.t.Helper()
	runner := gitx.ExecRunner{}
	res, err := runner.Run(context.Background(), rb.dir, "git", args...)
	if err != nil {
		rb.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		rb.t.Fatalf("git %s: exit %d: %s", strings.Join(args, " "), res.ExitCode, res.Stderr)
	}
}

// newRealRepositoryCheckpointService builds a REAL app.RepositoryCheckpointService
// (internal/repocheckpoint.Service, checkpoint-b04) against db and rb — the
// task brief's explicit instruction for this node's Repository Checkpoint
// step, in contrast to the State Checkpoint step (fake, see
// newFakeStateCheckpointService below).
func newRealRepositoryCheckpointService(t *testing.T, db *sqlite.DB, worktreeID domain.WorktreeID, rb *repoBuilder, clock domain.Clock) app.RepositoryCheckpointService {
	t.Helper()
	store := repocheckpoint.NewStore(db)
	client := gitx.NewClient(gitx.ExecRunner{})
	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: "repo1", Path: rb.dir}, nil
	}
	svc := repocheckpoint.NewService(client, store, clock, &seqIDs{prefix: "rc"}, t.TempDir(), resolve, repocheckpoint.CaptureOptions{})
	var _ app.RepositoryCheckpointService = svc
	return svc
}

// --- test harness: bundles everything a PersistPhase test needs ------------

type harness struct {
	deps    pause.PersistDeps
	pauses  *pause.MemStore
	wakeJob *scheduler.Store
	db      *sqlite.DB
	taskID  domain.TaskID
}

// newHarness builds a full PersistDeps: real RepositoryCheckpointService,
// fake StateCheckpointService and ProgressTreeService (per the task brief:
// checkpoint-a05's real implementation is a sibling teammate's concurrent,
// not-yet-mergeable work this same wave), real pause.MemStore, and a real
// scheduler.Store against the same migrated DB.
func newHarness(t *testing.T, clock domain.Clock) *harness {
	t.Helper()
	worktreeID := domain.WorktreeID("wt1")
	taskID := domain.TaskID("task1")

	db := openMigratedDB(t)
	seedChain(t, db, worktreeID, taskID)
	rb := newRepoBuilder(t)

	repoSvc := newRealRepositoryCheckpointService(t, db, worktreeID, rb, clock)

	var stateCounter atomic.Int64
	stateSvc := &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			n := stateCounter.Add(1)
			return domain.StateCheckpoint{ID: domain.StateCheckpointID("sc-" + itoa(n)), TaskID: req.TaskID}, nil
		},
	}

	progressCalls := 0
	progressSvc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, tID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			progressCalls++
			return app.ProgressTreeSnapshot{TaskID: tID}, nil
		},
	}

	pauses := pause.NewMemStore()
	wakeStore := scheduler.NewStore(db.Conn(), clock, &seqIDs{prefix: "wj"})

	return &harness{
		deps: pause.PersistDeps{
			ProgressTree:         progressSvc,
			StateCheckpoint:      stateSvc,
			RepositoryCheckpoint: repoSvc,
			Pauses:               pauses,
			WakeJobs:             wakeStore,
		},
		pauses:  pauses,
		wakeJob: wakeStore,
		db:      db,
		taskID:  taskID,
	}
}

func basePersistRequest(pauseID domain.PauseID, now time.Time) pause.PersistRequest {
	return pause.PersistRequest{
		PauseID:         pauseID,
		TaskID:          "task1",
		WorktreeID:      "wt1",
		WakeRunAfter:    now.Add(time.Hour),
		WakeMaxAttempts: 3,
	}
}

// seedPauseRecord inserts a bare PauseRecord directly into the harness's
// MemStore (simulating RequestPause having already created the record —
// runtime-a04's own scope — before Persist is ever called), AND a matching
// row in the real pause_records table so wake_jobs' real FK (0051's
// UNIQUE(pause_id, job_kind) table) is satisfiable once phase 5 runs
// against the same DB. See seedPauseRecordRow's doc comment for why two
// stores are seeded here.
func seedPauseRecord(t *testing.T, h *harness, pauseID domain.PauseID) {
	t.Helper()
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess-1"}
	if err := h.pauses.Insert(context.Background(), pause.PauseRecord{
		ID: pauseID, Key: key, Status: domain.PauseCheckpointing, Reason: pause.TriggerReasonCalibrated,
	}); err != nil {
		t.Fatalf("seedPauseRecord: Insert: %v", err)
	}
	seedPauseRecordRow(t, h.db, pauseID, h.taskID)
}

// --- Happy path --------------------------------------------------------

func TestPersistPhase_HappyPath_AllFiveStepsInOrder(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)

	result, err := pause.Persist(ctx, h.deps, basePersistRequest(pauseID, clock.Now()))
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if !result.Progress.ProgressSnapshotTaken {
		t.Error("expected ProgressSnapshotTaken = true")
	}
	if result.Progress.StateCheckpointID == nil {
		t.Error("expected StateCheckpointID to be set")
	}
	if result.Progress.RepositoryCheckpointID == nil {
		t.Error("expected RepositoryCheckpointID to be set")
	}
	if !result.Progress.PauseRecordSaved {
		t.Error("expected PauseRecordSaved = true")
	}
	if result.Progress.WakeJobID == nil {
		t.Fatal("expected WakeJobID to be set")
	}
	if result.Resumed {
		t.Error("expected Resumed = false on a fresh pause record")
	}
	if result.LastCompletedPhase != pause.PhaseWakeJob {
		t.Errorf("LastCompletedPhase = %q, want %q (the final phase) after a full successful run", result.LastCompletedPhase, pause.PhaseWakeJob)
	}

	// The wake job must be durably scheduled and claimable.
	job, err := h.wakeJob.Get(ctx, *result.Progress.WakeJobID)
	if err != nil {
		t.Fatalf("Get wake job: %v", err)
	}
	if job.PauseID != pauseID {
		t.Errorf("job.PauseID = %q, want %q", job.PauseID, pauseID)
	}
	if job.Status != scheduler.StatusScheduled {
		t.Errorf("job.Status = %q, want scheduled", job.Status)
	}

	// The persist progress must itself be durable in the pause store.
	progress, found, err := h.pauses.GetProgress(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("GetProgress after Persist: found=%v err=%v", found, err)
	}
	if progress.WakeJobID == nil || *progress.WakeJobID != *result.Progress.WakeJobID {
		t.Fatalf("durable progress.WakeJobID = %v, want %v", progress.WakeJobID, result.Progress.WakeJobID)
	}
}

// --- Validation / fail-closed dependency checks -----------------------------

func TestPersistPhase_ValidatesRequiredFields(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)

	base := basePersistRequest(pauseID, clock.Now())
	cases := []pause.PersistRequest{
		func() pause.PersistRequest { r := base; r.PauseID = ""; return r }(),
		func() pause.PersistRequest { r := base; r.TaskID = ""; return r }(),
		func() pause.PersistRequest { r := base; r.WorktreeID = ""; return r }(),
		func() pause.PersistRequest { r := base; r.WakeMaxAttempts = 0; return r }(),
	}
	for i, req := range cases {
		_, err := pause.Persist(ctx, h.deps, req)
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
			t.Errorf("case %d: err = %v, want ErrCodeValidation", i, err)
		}
	}
}

func TestPersistPhase_NilDependenciesFailClosed(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)
	req := basePersistRequest(pauseID, clock.Now())

	cases := []struct {
		name string
		deps pause.PersistDeps
	}{
		{"ProgressTree", func() pause.PersistDeps { d := h.deps; d.ProgressTree = nil; return d }()},
		{"StateCheckpoint", func() pause.PersistDeps { d := h.deps; d.StateCheckpoint = nil; return d }()},
		{"RepositoryCheckpoint", func() pause.PersistDeps { d := h.deps; d.RepositoryCheckpoint = nil; return d }()},
		{"Pauses", func() pause.PersistDeps { d := h.deps; d.Pauses = nil; return d }()},
		{"WakeJobs", func() pause.PersistDeps { d := h.deps; d.WakeJobs = nil; return d }()},
	}
	for _, c := range cases {
		_, err := pause.Persist(ctx, c.deps, req)
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
			t.Errorf("%s: err = %v, want ErrCodeUnavailable", c.name, err)
		}
	}
}

func TestPersistPhase_UnknownPauseRecordFailsClosed(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()

	_, err := pause.Persist(ctx, h.deps, basePersistRequest("does-not-exist", clock.Now()))
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want ErrCodeNotFound", err)
	}
}

// --- Crash injection: one test per phase boundary, per agents/runtime.md's
// required test "crash after every phase resumes/reconciles correctly" ------

func runToHalt(t *testing.T, h *harness, phase pause.PersistPhase, req pause.PersistRequest) pause.PersistResult {
	t.Helper()
	deps := h.deps
	deps.HaltAfter = phase
	result, err := pause.Persist(context.Background(), deps, req)
	var halt *pause.HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected a halt at phase %q, got err=%v", phase, err)
	}
	if halt.Phase != phase {
		t.Fatalf("expected halt at phase %q, got halt at %q", phase, halt.Phase)
	}
	return result
}

func TestPersistPhase_CrashInjection_AfterProgressSnapshot_ResumesCleanly(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)
	req := basePersistRequest(pauseID, clock.Now())

	halted := runToHalt(t, h, pause.PhaseProgressSnapshot, req)
	if !halted.Progress.ProgressSnapshotTaken {
		t.Fatal("expected ProgressSnapshotTaken durable after halt")
	}
	if halted.Progress.StateCheckpointID != nil {
		t.Fatal("expected no StateCheckpointID yet — later phase must not have run")
	}
	if halted.LastCompletedPhase != pause.PhaseProgressSnapshot {
		t.Errorf("LastCompletedPhase = %q, want %q", halted.LastCompletedPhase, pause.PhaseProgressSnapshot)
	}

	result, err := pause.Persist(ctx, h.deps, req)
	if err != nil {
		t.Fatalf("resume after crash: %v", err)
	}
	if result.Progress.WakeJobID == nil {
		t.Fatal("expected resume to complete all remaining phases")
	}
	if !result.Resumed {
		t.Error("expected Resumed = true on a resumed attempt")
	}

	// Exactly one wake job must exist for this pause — no duplicate from
	// the halted attempt (which never reached phase 5 anyway) plus the
	// resumed attempt.
	assertExactlyOneWakeJob(t, h, ctx, pauseID)
}

func TestPersistPhase_CrashInjection_AfterStateCheckpoint_ResumesWithoutDuplicateCheckpoint(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)
	req := basePersistRequest(pauseID, clock.Now())

	halted := runToHalt(t, h, pause.PhaseStateCheckpoint, req)
	if halted.Progress.StateCheckpointID == nil {
		t.Fatal("expected StateCheckpointID durable after halt")
	}
	firstStateID := *halted.Progress.StateCheckpointID
	if halted.Progress.RepositoryCheckpointID != nil {
		t.Fatal("expected no RepositoryCheckpointID yet")
	}

	result, err := pause.Persist(ctx, h.deps, req)
	if err != nil {
		t.Fatalf("resume after crash: %v", err)
	}
	// The State Checkpoint must NEVER be created a second time — the same
	// ID recorded before the crash must still be the one in the final
	// result (proves the fake's CreateFunc was not invoked again).
	if result.Progress.StateCheckpointID == nil || *result.Progress.StateCheckpointID != firstStateID {
		t.Fatalf("StateCheckpointID after resume = %v, want unchanged %v (must not re-create)", result.Progress.StateCheckpointID, firstStateID)
	}
	if result.Progress.RepositoryCheckpointID == nil {
		t.Fatal("expected RepositoryCheckpointID to be produced by the resumed attempt")
	}

	assertExactlyOneWakeJob(t, h, ctx, pauseID)
}

func TestPersistPhase_CrashInjection_AfterRepositoryCheckpoint_ResumesWithoutDuplicateCheckpoint(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)
	req := basePersistRequest(pauseID, clock.Now())

	halted := runToHalt(t, h, pause.PhaseRepositoryCheckpoint, req)
	if halted.Progress.RepositoryCheckpointID == nil {
		t.Fatal("expected RepositoryCheckpointID durable after halt")
	}
	firstRepoID := *halted.Progress.RepositoryCheckpointID
	if halted.Progress.PauseRecordSaved {
		t.Fatal("expected PauseRecordSaved still false")
	}

	result, err := pause.Persist(ctx, h.deps, req)
	if err != nil {
		t.Fatalf("resume after crash: %v", err)
	}
	if result.Progress.RepositoryCheckpointID == nil || *result.Progress.RepositoryCheckpointID != firstRepoID {
		t.Fatalf("RepositoryCheckpointID after resume = %v, want unchanged %v (must not re-capture)", result.Progress.RepositoryCheckpointID, firstRepoID)
	}
	// Confirm the repository checkpoint itself is verifiable and there is
	// really only one row for it (Verify would fail if capture had somehow
	// run twice and corrupted the manifest).
	verification, err := h.deps.RepositoryCheckpoint.Verify(ctx, firstRepoID)
	if err != nil {
		t.Fatalf("Verify repository checkpoint: %v", err)
	}
	if !verification.Valid {
		t.Fatal("expected the repository checkpoint to verify as valid")
	}

	assertExactlyOneWakeJob(t, h, ctx, pauseID)
}

func TestPersistPhase_CrashInjection_AfterPauseRecord_ResumesCleanly(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)
	req := basePersistRequest(pauseID, clock.Now())

	halted := runToHalt(t, h, pause.PhasePauseRecord, req)
	if !halted.Progress.PauseRecordSaved {
		t.Fatal("expected PauseRecordSaved durable after halt")
	}
	if halted.Progress.WakeJobID != nil {
		t.Fatal("expected no WakeJobID yet")
	}

	result, err := pause.Persist(ctx, h.deps, req)
	if err != nil {
		t.Fatalf("resume after crash: %v", err)
	}
	if result.Progress.WakeJobID == nil {
		t.Fatal("expected resume to schedule the wake job")
	}

	assertExactlyOneWakeJob(t, h, ctx, pauseID)
}

func TestPersistPhase_CrashInjection_AfterWakeJob_StateIsDurableAndReplaySafe(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()
	pauseID := domain.PauseID("pause-1")
	seedPauseRecord(t, h, pauseID)
	req := basePersistRequest(pauseID, clock.Now())

	halted := runToHalt(t, h, pause.PhaseWakeJob, req)
	if halted.Progress.WakeJobID == nil {
		t.Fatal("expected WakeJobID durable after halt (the crash happens AFTER phase 5's own durable write)")
	}

	// A subsequent call after the "crash" must be a pure no-op replay: every
	// step already durable, nothing re-created, and it must succeed (no
	// HaltAfter configured this time).
	result, err := pause.Persist(ctx, h.deps, req)
	if err != nil {
		t.Fatalf("replay after post-commit crash: %v", err)
	}
	if *result.Progress.WakeJobID != *halted.Progress.WakeJobID {
		t.Fatalf("WakeJobID changed on replay: got %v, want unchanged %v", result.Progress.WakeJobID, halted.Progress.WakeJobID)
	}
	if *result.Progress.StateCheckpointID != *halted.Progress.StateCheckpointID {
		t.Fatal("StateCheckpointID changed on replay — must not re-create")
	}
	if *result.Progress.RepositoryCheckpointID != *halted.Progress.RepositoryCheckpointID {
		t.Fatal("RepositoryCheckpointID changed on replay — must not re-create")
	}

	assertExactlyOneWakeJob(t, h, ctx, pauseID)
}

// TestPersist_ReconciliationAfterCrash_NeverLosesOrDoublesWork drives every
// phase boundary across independent pause records in one sweep, proving the
// crash-then-resume property holds uniformly, not just for one
// hand-picked phase (mirrors internal/progress's own
// TestCompleteNode_ReconciliationAfterCrash_NeverLosesOrDoublesWork sweep
// precedent).
func TestPersistPhase_ReconciliationAfterCrash_NeverLosesOrDoublesWork(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC)}
	h := newHarness(t, clock)
	ctx := context.Background()

	phases := []pause.PersistPhase{
		pause.PhaseProgressSnapshot,
		pause.PhaseStateCheckpoint,
		pause.PhaseRepositoryCheckpoint,
		pause.PhasePauseRecord,
		pause.PhaseWakeJob,
	}

	for i, phase := range phases {
		pauseID := domain.PauseID("pause-recon-" + itoa(int64(i)))
		key := pause.PauseKey{TaskID: "task1", SessionID: domain.SessionID("sess-recon-" + itoa(int64(i)))}
		if err := h.pauses.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PauseCheckpointing, Reason: pause.TriggerReasonCalibrated}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		seedPauseRecordRow(t, h.db, pauseID, h.taskID)
		req := basePersistRequest(pauseID, clock.Now())

		runToHalt(t, h, phase, req)

		result, err := pause.Persist(ctx, h.deps, req)
		if err != nil {
			t.Fatalf("phase %s: recovery run failed: %v", phase, err)
		}
		if result.Progress.WakeJobID == nil {
			t.Fatalf("phase %s: expected full completion after recovery", phase)
		}
		assertExactlyOneWakeJob(t, h, ctx, pauseID)
	}
}

func assertExactlyOneWakeJob(t *testing.T, h *harness, ctx context.Context, pauseID domain.PauseID) {
	t.Helper()
	job, found, err := h.wakeJob.GetByPauseKind(ctx, pauseID, "pause_resume")
	if err != nil {
		t.Fatalf("GetByPauseKind: %v", err)
	}
	if !found {
		t.Fatalf("expected exactly one wake job for pause %q, found none", pauseID)
	}
	if job.PauseID != pauseID {
		t.Fatalf("wake job PauseID = %q, want %q", job.PauseID, pauseID)
	}
}
