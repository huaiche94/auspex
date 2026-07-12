package orchestrator_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/idgen"
	"github.com/huaiche94/preflight/internal/orchestrator"
	"github.com/huaiche94/preflight/internal/pause"
	"github.com/huaiche94/preflight/internal/scheduler"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// --- preflight pause request ---------------------------------------------

func TestPauseRequestCmd_FirstCallCreatesRecord(t *testing.T) {
	store := pause.NewMemStore()
	deps := orchestrator.PauseLifecycleDeps{Store: store}

	result, err := orchestrator.PauseRequestCmd(context.Background(), deps, idgen.New(), orchestrator.PauseRequestRequest{
		TaskID: "task-1", SessionID: "sess-1", Reason: pause.TriggerReasonCalibrated,
	})
	if err != nil {
		t.Fatalf("PauseRequestCmd: %v", err)
	}
	if !result.Created {
		t.Fatal("expected Created = true on first call")
	}
	if result.Record.Status != domain.PausePredicted {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PausePredicted)
	}
}

func TestPauseRequestCmd_ReplayIsIdempotent(t *testing.T) {
	store := pause.NewMemStore()
	deps := orchestrator.PauseLifecycleDeps{Store: store}
	ids := idgen.New()
	req := orchestrator.PauseRequestRequest{TaskID: "task-1", SessionID: "sess-1", Reason: pause.TriggerReasonCalibrated}

	first, err := orchestrator.PauseRequestCmd(context.Background(), deps, ids, req)
	if err != nil {
		t.Fatalf("first PauseRequestCmd: %v", err)
	}
	second, err := orchestrator.PauseRequestCmd(context.Background(), deps, ids, req)
	if err != nil {
		t.Fatalf("second PauseRequestCmd: %v", err)
	}
	if second.Created {
		t.Fatal("expected Created = false on replay")
	}
	if second.Record.ID != first.Record.ID {
		t.Fatalf("replay produced a different record: %q vs %q", second.Record.ID, first.Record.ID)
	}
}

func TestPauseRequestCmd_NilDepsFailClosed(t *testing.T) {
	_, err := orchestrator.PauseRequestCmd(context.Background(), orchestrator.PauseLifecycleDeps{}, idgen.New(), orchestrator.PauseRequestRequest{
		TaskID: "task-1", SessionID: "sess-1", Reason: pause.TriggerReasonCalibrated,
	})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}

	store := pause.NewMemStore()
	_, err = orchestrator.PauseRequestCmd(context.Background(), orchestrator.PauseLifecycleDeps{Store: store}, nil, orchestrator.PauseRequestRequest{
		TaskID: "task-1", SessionID: "sess-1", Reason: pause.TriggerReasonCalibrated,
	})
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("nil IDGenerator: err = %v, want ErrCodeUnavailable", err)
	}
}

// --- preflight pause cancel ------------------------------------------------

func TestPauseCancelCmd_CancelsInFlightPause(t *testing.T) {
	store := pause.NewMemStore()
	deps := orchestrator.PauseLifecycleDeps{Store: store}
	created, err := orchestrator.PauseRequestCmd(context.Background(), deps, idgen.New(), orchestrator.PauseRequestRequest{
		TaskID: "task-1", SessionID: "sess-1", Reason: pause.TriggerReasonCalibrated,
	})
	if err != nil {
		t.Fatalf("PauseRequestCmd: %v", err)
	}

	result, err := orchestrator.PauseCancelCmd(context.Background(), deps, orchestrator.PauseCancelRequest{PauseID: created.Record.ID})
	if err != nil {
		t.Fatalf("PauseCancelCmd: %v", err)
	}
	if result.Record.Status != domain.PauseCancelled {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseCancelled)
	}
}

func TestPauseCancelCmd_UnknownPauseIDFailsNotFound(t *testing.T) {
	store := pause.NewMemStore()
	deps := orchestrator.PauseLifecycleDeps{Store: store}
	_, err := orchestrator.PauseCancelCmd(context.Background(), deps, orchestrator.PauseCancelRequest{PauseID: "does-not-exist"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want ErrCodeNotFound", err)
	}
}

func TestPauseCancelCmd_NilStoreFailsClosed(t *testing.T) {
	_, err := orchestrator.PauseCancelCmd(context.Background(), orchestrator.PauseLifecycleDeps{}, orchestrator.PauseCancelRequest{PauseID: "pause-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

// --- preflight resume ------------------------------------------------------

func seedWakePendingRecord(t *testing.T, store *pause.MemStore, id domain.PauseID) {
	t.Helper()
	if err := store.Insert(context.Background(), pause.PauseRecord{
		ID:     id,
		Key:    pause.PauseKey{TaskID: "task-1", SessionID: "sess-1"},
		Status: domain.PauseWakePending,
		Reason: pause.TriggerReasonCalibrated,
	}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
}

func TestResumeCmd_DefaultsToValidAndReachesResumed(t *testing.T) {
	store := pause.NewMemStore()
	seedWakePendingRecord(t, store, "pause-1")
	deps := orchestrator.PauseLifecycleDeps{Store: store}

	result, err := orchestrator.ResumeCmd(context.Background(), deps, orchestrator.ResumeCmdRequest{PauseID: "pause-1"})
	if err != nil {
		t.Fatalf("ResumeCmd: %v", err)
	}
	if result.Record.Status != domain.PauseResumed {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseResumed)
	}
}

func TestResumeCmd_QuotaUnsafeReschedules(t *testing.T) {
	store := pause.NewMemStore()
	seedWakePendingRecord(t, store, "pause-1")
	deps := orchestrator.PauseLifecycleDeps{Store: store}

	result, err := orchestrator.ResumeCmd(context.Background(), deps, orchestrator.ResumeCmdRequest{PauseID: "pause-1", QuotaUnsafe: true})
	if err != nil {
		t.Fatalf("ResumeCmd: %v", err)
	}
	if result.Record.Status != domain.PauseSleeping {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseSleeping)
	}
}

func TestResumeCmd_ConflictBlocks(t *testing.T) {
	store := pause.NewMemStore()
	seedWakePendingRecord(t, store, "pause-1")
	deps := orchestrator.PauseLifecycleDeps{Store: store}

	result, err := orchestrator.ResumeCmd(context.Background(), deps, orchestrator.ResumeCmdRequest{PauseID: "pause-1", Conflict: true})
	if err != nil {
		t.Fatalf("ResumeCmd: %v", err)
	}
	if result.Record.Status != domain.PauseBlockedConflict {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseBlockedConflict)
	}
}

func TestResumeCmd_NilStoreFailsClosed(t *testing.T) {
	_, err := orchestrator.ResumeCmd(context.Background(), orchestrator.PauseLifecycleDeps{}, orchestrator.ResumeCmdRequest{PauseID: "pause-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

// --- preflight scheduler run-once -----------------------------------------

func openMigratedSchedulerDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "preflight.db")
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

func seedSchedulerChain(t *testing.T, db *sqlite.DB) {
	t.Helper()
	now := "2026-07-12T09:00:00Z"
	stmts := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('sess1', 'wt1', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('task1', 'sess1', 'wt1', 'hash1', 'pending', '` + now + `', '` + now + `')`,
		`INSERT INTO pause_records (id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
		 VALUES ('pause1', 'task1', 'sess1', 'turn1', 'rf1', 'sleeping', '` + now + `', 1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

type fixedSchedClock struct{ t time.Time }

func (f fixedSchedClock) Now() time.Time { return f.t }

type seqSchedIDs struct{ n int }

func (g *seqSchedIDs) NewID() string {
	g.n++
	return "wj-" + string(rune('0'+g.n))
}

func TestSchedulerRunOnceCmd_ClaimsDueJob(t *testing.T) {
	clock := fixedSchedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	db := openMigratedSchedulerDB(t)
	seedSchedulerChain(t, db)
	store := scheduler.NewStore(db.Conn(), clock, &seqSchedIDs{})

	_, err := store.Schedule(context.Background(), scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	deps := orchestrator.PauseLifecycleDeps{WakeJobs: store}
	result, err := orchestrator.SchedulerRunOnceCmd(context.Background(), deps, orchestrator.SchedulerRunOnceRequest{})
	if err != nil {
		t.Fatalf("SchedulerRunOnceCmd: %v", err)
	}
	if !result.Claimed {
		t.Fatal("expected Claimed = true for a due job")
	}
	if result.Job.Status != scheduler.StatusLeased {
		t.Fatalf("Job.Status = %q, want %q", result.Job.Status, scheduler.StatusLeased)
	}
	if result.Job.LeaseOwner == nil || *result.Job.LeaseOwner != orchestrator.DefaultSchedulerRunOnceOwner {
		t.Fatalf("Job.LeaseOwner = %v, want %q", result.Job.LeaseOwner, orchestrator.DefaultSchedulerRunOnceOwner)
	}
}

func TestSchedulerRunOnceCmd_NoJobsIsNotAnError(t *testing.T) {
	clock := fixedSchedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	db := openMigratedSchedulerDB(t)
	seedSchedulerChain(t, db)
	store := scheduler.NewStore(db.Conn(), clock, &seqSchedIDs{})

	deps := orchestrator.PauseLifecycleDeps{WakeJobs: store}
	result, err := orchestrator.SchedulerRunOnceCmd(context.Background(), deps, orchestrator.SchedulerRunOnceRequest{})
	if err != nil {
		t.Fatalf("SchedulerRunOnceCmd: %v", err)
	}
	if result.Claimed {
		t.Fatal("expected Claimed = false when no job is due")
	}
}

func TestSchedulerRunOnceCmd_CustomOwner(t *testing.T) {
	clock := fixedSchedClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	db := openMigratedSchedulerDB(t)
	seedSchedulerChain(t, db)
	store := scheduler.NewStore(db.Conn(), clock, &seqSchedIDs{})
	if _, err := store.Schedule(context.Background(), scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	deps := orchestrator.PauseLifecycleDeps{WakeJobs: store}
	result, err := orchestrator.SchedulerRunOnceCmd(context.Background(), deps, orchestrator.SchedulerRunOnceRequest{Owner: "worker-custom"})
	if err != nil {
		t.Fatalf("SchedulerRunOnceCmd: %v", err)
	}
	if result.Job.LeaseOwner == nil || *result.Job.LeaseOwner != "worker-custom" {
		t.Fatalf("Job.LeaseOwner = %v, want worker-custom", result.Job.LeaseOwner)
	}
}

func TestSchedulerRunOnceCmd_NilWakeJobsFailsClosed(t *testing.T) {
	_, err := orchestrator.SchedulerRunOnceCmd(context.Background(), orchestrator.PauseLifecycleDeps{}, orchestrator.SchedulerRunOnceRequest{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}
