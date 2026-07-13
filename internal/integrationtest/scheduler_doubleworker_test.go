// scheduler_doubleworker_test.go implements qa-07 (docs/implementation/day1/
// EXECUTION_DAG.md's qa-07 row; agents/qa.md deliverable #7: "Scheduler
// double-worker/lease race test").
//
// # Why this test lives here, not in internal/scheduler
//
// The DAG's own row for this node names a validation command
// (`go test ./internal/scheduler/... -run DoubleWorkerRace -race -count=20`)
// that targets internal/scheduler/... directly — but that is runtime's
// EXCLUSIVE path (agents/runtime.md), not one of qa's own exclusive paths
// (.github/**, internal/integrationtest/**, testdata/e2e/**,
// testdata/security/**, docs/security/**, docs/implementation/day1/qa.md,
// SECURITY.md, CONTRIBUTING.md, CODE_OF_CONDUCT.md, GOVERNANCE.md — see
// agents/qa.md). qa may not touch internal/scheduler/** or internal/pause/**
// themselves per Constitution Sec4.2/Sec4.3 ("a role may only modify files
// inside its own declared paths") — runtime-a09 already built and proved
// this exact race at the package level
// (internal/scheduler/lease_test.go's own TestLease_ConcurrentWorkersYieldOneClaim,
// internal/pause/wake_test.go's own TestDuplicateWake_WorkersYieldOneResume).
//
// Per this node's own task instructions, this file instead builds an
// INDEPENDENT integration-scope test in internal/integrationtest,
// exercising the SAME double-worker race scenario from its own vantage
// point — calling ONLY runtime's real, already-exported scheduler/pause
// APIs (scheduler.NewStore, scheduler.Store.Schedule/Claim/Get,
// pause.NewSQLiteStore, pause.Wake) — without modifying anything under
// internal/scheduler/** or internal/pause/** themselves.
//
// # What is genuinely different here, not a re-run
//
//   - Real, on-disk (temp-file, never :memory:) SQLite for BOTH the
//     scheduler's wake_jobs table AND the pause subsystem's pause_records
//     table, in the SAME shared database file — runtime-a09's own
//     wake_test.go races exclusively against pause.NewMemStore() (an
//     in-memory reference double, by that file's own explicit design,
//     since a09's job was proving the STATE-MACHINE race property in
//     isolation); lease_test.go's own race IS already against a real
//     on-disk DB, but only for the scheduler half alone, never composed
//     with the pause half in the same race.
//   - The race this file drives is the COMPOSED, realistic one: N
//     concurrent "workers" each attempt the full real sequence a
//     production scheduler loop actually performs for one due job —
//     Claim (scheduler.Store) THEN, only if Claim won, Wake (pause,
//     against the SAME real PauseID the claimed job names) — rather than
//     either layer's race in isolation. This is a genuinely different
//     question from either upstream test: does composing the two
//     independently-proven exactly-once guarantees introduce a NEW race
//     window at their seam (e.g. between "I won the Claim" and "I call
//     Wake")? No such seam-level race is possible by construction if each
//     layer's own guarantee holds (this file's own result confirms that,
//     rather than assuming it) — but nothing upstream actually drove
//     both together concurrently until now.
//   - Repeated internally (an explicit attempts-loop), together with this
//     node's own required `-count=20` from the outer `go test` invocation
//     — i.e. this file is stress-tested both from within (many repeated
//     race trials per test process) and from without (go test -count=20
//     repeating the whole process many times), per the DAG's own risk
//     note: "flaky-by-nature; needs repeated runs."
package integrationtest

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/pause"
	"github.com/huaiche94/preflight/internal/scheduler"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// qa07Clock is a fixed, deterministic domain.Clock — this race's outcome
// must not depend on wall-clock timing at all (RunAfter is always already
// due), so a fixed clock is sufficient and keeps the race itself the only
// source of nondeterminism under test.
type qa07Clock struct{ t time.Time }

func (c qa07Clock) Now() time.Time { return c.t }

// qa07IDs is a concurrency-safe sequential domain.IDGenerator — this race
// deliberately calls NewID from many goroutines only indirectly (via
// scheduler.Store's own internal ID generation during Schedule, which
// itself is called only once, single-threaded, before the race begins);
// still made safe with a mutex to avoid this test's OWN plumbing being a
// second, accidental source of race-detector findings unrelated to the
// property under test.
type qa07IDs struct {
	mu     sync.Mutex
	n      int
	prefix string
}

func (g *qa07IDs) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return fmt.Sprintf("%s-%d", g.prefix, g.n)
}

// qa07OpenDB opens a REAL on-disk (temp-file, never :memory:) SQLite
// database holding both the scheduler's wake_jobs table and the pause
// subsystem's pause_records table — the shared file this composed race
// exercises.
func qa07OpenDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "qa07.db"))
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

// qa07SeedChain inserts a minimal repositories -> worktrees ->
// provider_sessions -> tasks chain so pause_records' task_id/session_id
// FKs are satisfiable (migration 0050's own FK shape). repoID/rootPath are
// caller-supplied and must be unique per call within a shared DB (repeated
// calls against distinct worktrees in the same database, as this file's
// many-jobs scenario does, would otherwise collide on repositories'
// UNIQUE git_common_dir column).
func qa07SeedChain(t *testing.T, db *sqlite.DB, repoID string, worktreeID domain.WorktreeID, taskID domain.TaskID, sessionID domain.SessionID) {
	t.Helper()
	now := "2026-07-12T07:00:00Z"
	rootPath := "/tmp/" + repoID
	stmts := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('` + repoID + `', '` + rootPath + `', '` + rootPath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('` + string(worktreeID) + `', '` + repoID + `', '` + rootPath + `', '` + rootPath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('` + string(sessionID) + `', '` + string(worktreeID) + `', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('` + string(taskID) + `', '` + string(sessionID) + `', '` + string(worktreeID) + `', 'hash-qa07', 'in_progress', '` + now + `', '` + now + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// TestDoubleWorkerRace_ScheduleClaimThenWake_ExactlyOneFullSequenceSucceeds
// is this node's headline scenario: schedule one real, due wake job for one
// real, Sleeping pause record (both against the SAME shared, real, on-disk
// SQLite database), then race N concurrent "workers," each attempting the
// full realistic sequence a scheduler worker loop performs — Claim, then
// (only if Claim won) Wake against the claimed job's own PauseID — and
// confirm exactly one worker's FULL sequence (Claim AND Wake) succeeds, not
// merely that Claim alone is exactly-once (already proven by
// lease_test.go) or that Wake alone is exactly-once (already proven by
// wake_test.go) in isolation.
func TestDoubleWorkerRace_ScheduleClaimThenWake_ExactlyOneFullSequenceSucceeds(t *testing.T) {
	const workers = 20
	const attempts = 20 // internal repeated-race loop, mirroring wake_test.go's own documented "qa-07's own -count=20 repeated-race discipline"

	for attempt := 0; attempt < attempts; attempt++ {
		db := qa07OpenDB(t)
		clock := qa07Clock{t: time.Date(2026, 7, 12, 7, 0, 0, 0, time.UTC)}

		repoID := fmt.Sprintf("repo-qa07-%d", attempt)
		worktreeID := domain.WorktreeID(fmt.Sprintf("wt-qa07-%d", attempt))
		taskID := domain.TaskID(fmt.Sprintf("task-qa07-%d", attempt))
		sessionID := domain.SessionID(fmt.Sprintf("sess-qa07-%d", attempt))
		pauseID := domain.PauseID(fmt.Sprintf("pause-qa07-%d", attempt))
		qa07SeedChain(t, db, repoID, worktreeID, taskID, sessionID)

		pauseStore := pause.NewSQLiteStore(db)
		if err := pauseStore.Insert(context.Background(), pause.PauseRecord{
			ID:     pauseID,
			Key:    pause.PauseKey{TaskID: taskID, SessionID: sessionID},
			Status: domain.PauseSleeping,
			Reason: pause.TriggerReasonCalibrated,
		}); err != nil {
			t.Fatalf("attempt %d: pauseStore.Insert: %v", attempt, err)
		}

		wakeStore := scheduler.NewStore(db.Conn(), clock, &qa07IDs{prefix: fmt.Sprintf("wj-%d", attempt)})
		job, err := wakeStore.Schedule(context.Background(), scheduler.ScheduleRequest{
			PauseID: pauseID, Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 100,
		})
		if err != nil {
			t.Fatalf("attempt %d: Schedule: %v", attempt, err)
		}

		var (
			wg              sync.WaitGroup
			claimSuccesses  atomic.Int64
			wakeSuccesses   atomic.Int64
			fullSequenceOK  atomic.Int64
			mu              sync.Mutex
			claimWinners    []string
			wakeErrs        []error
			unexpectedPanic atomic.Bool
		)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			owner := fmt.Sprintf("worker-%d-%d", attempt, i)
			wg.Add(1)
			go func(owner string) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						unexpectedPanic.Store(true)
						t.Errorf("attempt %d worker %s: panic: %v", attempt, owner, r)
					}
				}()
				<-start

				claimResult, err := wakeStore.Claim(context.Background(), owner, 5*time.Minute)
				if err != nil || !claimResult.Found {
					return // lost the claim race — an acceptable, expected outcome under contention
				}
				claimSuccesses.Add(1)
				mu.Lock()
				claimWinners = append(claimWinners, owner)
				mu.Unlock()

				if claimResult.Job.ID != job.ID {
					t.Errorf("attempt %d worker %s: claimed an unexpected job id %v, want %v", attempt, owner, claimResult.Job.ID, job.ID)
					return
				}

				// Only the Claim winner proceeds to Wake — exactly what a
				// real scheduler worker loop does (claim, then act on the
				// claimed job). This is the seam this test exists to
				// exercise: does a second, INDEPENDENT exactly-once
				// mechanism (pause.Wake's own CompareAndSwapStatus) compose
				// safely immediately after the first (Claim) without
				// introducing a new race window.
				wakeResult, wakeErr := pause.Wake(context.Background(), pauseStore, pause.WakeRequest{PauseID: pauseID})
				mu.Lock()
				wakeErrs = append(wakeErrs, wakeErr)
				mu.Unlock()
				if wakeErr != nil {
					t.Errorf("attempt %d worker %s: won Claim but Wake failed: %v (Wake should never fail for the SOLE Claim winner acting on its own newly-claimed job)", attempt, owner, wakeErr)
					return
				}
				if wakeResult.Record.Status != domain.PauseWakePending {
					t.Errorf("attempt %d worker %s: Wake succeeded but Status = %q, want %q", attempt, owner, wakeResult.Record.Status, domain.PauseWakePending)
					return
				}
				wakeSuccesses.Add(1)
				fullSequenceOK.Add(1)
			}(owner)
		}
		close(start)
		wg.Wait()

		if unexpectedPanic.Load() {
			t.Fatalf("attempt %d: at least one worker goroutine panicked (see errors above)", attempt)
		}
		if got := claimSuccesses.Load(); got != 1 {
			t.Fatalf("attempt %d: successful Claims = %d, want exactly 1; winners=%v", attempt, got, claimWinners)
		}
		if got := wakeSuccesses.Load(); got != 1 {
			t.Fatalf("attempt %d: successful full Claim->Wake sequences = %d, want exactly 1; wakeErrs=%v", attempt, got, wakeErrs)
		}
		if got := fullSequenceOK.Load(); got != 1 {
			t.Fatalf("attempt %d: full-sequence successes = %d, want exactly 1", attempt, got)
		}

		// Durable, post-race state must agree with the in-flight counters:
		// exactly one leased-then-still-leased job (Wake does not itself
		// touch wake_jobs — that is a later scheduler-worker-loop concern
		// this node's own scope note does not claim to cover, per
		// pauselifecycle.go's own documented SchedulerRunOnceCmd scope), and
		// the pause record durably at WakePending, never re-entered or
		// corrupted by the losing workers' no-op attempts.
		finalJob, err := wakeStore.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("attempt %d: Get(job): %v", attempt, err)
		}
		if finalJob.Status != scheduler.StatusLeased {
			t.Fatalf("attempt %d: final job.Status = %q, want %q", attempt, finalJob.Status, scheduler.StatusLeased)
		}
		if finalJob.Attempts != 1 {
			t.Fatalf("attempt %d: final job.Attempts = %d, want exactly 1 (only the winner incremented it)", attempt, finalJob.Attempts)
		}

		finalPause, found, err := pauseStore.GetByID(context.Background(), pauseID)
		if err != nil || !found {
			t.Fatalf("attempt %d: GetByID after race: found=%v err=%v", attempt, found, err)
		}
		if finalPause.Status != domain.PauseWakePending {
			t.Fatalf("attempt %d: final pause Status = %q, want %q", attempt, finalPause.Status, domain.PauseWakePending)
		}

		if err := db.Close(); err != nil {
			t.Fatalf("attempt %d: db.Close: %v", attempt, err)
		}
	}
}

// TestDoubleWorkerRace_AcrossManyIndependentJobs_EachClaimedAndWokenExactlyOnce
// extends the single-job race to N independently-due wake jobs (each
// bound to its own Sleeping pause record) raced by M workers each,
// composing lease_test.go's own "many jobs" pattern
// (TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce) with the
// pause-side Wake step this node adds — proving the exactly-once guarantee
// holds per-job/per-pause under CONCURRENT cross-contention (workers racing
// for ANY of several due jobs at once, not just one), not merely as an
// artifact of there being only a single record to contend over.
func TestDoubleWorkerRace_AcrossManyIndependentJobs_EachClaimedAndWokenExactlyOnce(t *testing.T) {
	const jobCount = 6
	const workers = 12

	db := qa07OpenDB(t)
	clock := qa07Clock{t: time.Date(2026, 7, 12, 7, 30, 0, 0, time.UTC)}
	pauseStore := pause.NewSQLiteStore(db)
	wakeStore := scheduler.NewStore(db.Conn(), clock, &qa07IDs{prefix: "wj-many"})

	pauseIDs := make([]domain.PauseID, 0, jobCount)
	jobIDs := make(map[domain.WakeJobID]domain.PauseID, jobCount)
	for i := 0; i < jobCount; i++ {
		repoID := fmt.Sprintf("repo-qa07-many-%d", i)
		worktreeID := domain.WorktreeID(fmt.Sprintf("wt-qa07-many-%d", i))
		taskID := domain.TaskID(fmt.Sprintf("task-qa07-many-%d", i))
		sessionID := domain.SessionID(fmt.Sprintf("sess-qa07-many-%d", i))
		pauseID := domain.PauseID(fmt.Sprintf("pause-qa07-many-%d", i))
		qa07SeedChain(t, db, repoID, worktreeID, taskID, sessionID)
		if err := pauseStore.Insert(context.Background(), pause.PauseRecord{
			ID: pauseID, Key: pause.PauseKey{TaskID: taskID, SessionID: sessionID},
			Status: domain.PauseSleeping, Reason: pause.TriggerReasonCalibrated,
		}); err != nil {
			t.Fatalf("pauseStore.Insert %d: %v", i, err)
		}
		job, err := wakeStore.Schedule(context.Background(), scheduler.ScheduleRequest{
			PauseID: pauseID, Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 10,
		})
		if err != nil {
			t.Fatalf("Schedule %d: %v", i, err)
		}
		pauseIDs = append(pauseIDs, pauseID)
		jobIDs[job.ID] = pauseID
	}

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		claimedBy   = map[domain.WakeJobID]string{}
		wokenPauses = map[domain.PauseID]string{}
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		owner := fmt.Sprintf("worker-many-%d", i)
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			for {
				claimResult, err := wakeStore.Claim(context.Background(), owner, 5*time.Minute)
				if err != nil || !claimResult.Found {
					return
				}
				mu.Lock()
				if existing, dup := claimedBy[claimResult.Job.ID]; dup {
					t.Errorf("job %q claimed by both %q and %q", claimResult.Job.ID, existing, owner)
				}
				claimedBy[claimResult.Job.ID] = owner
				pauseID := jobIDs[claimResult.Job.ID]
				mu.Unlock()

				wakeResult, wakeErr := pause.Wake(context.Background(), pauseStore, pause.WakeRequest{PauseID: pauseID})
				if wakeErr != nil {
					t.Errorf("worker %s: won Claim for job %q but Wake(%q) failed: %v", owner, claimResult.Job.ID, pauseID, wakeErr)
					continue
				}
				if wakeResult.Record.Status != domain.PauseWakePending {
					t.Errorf("worker %s: Wake(%q) succeeded but Status = %q", owner, pauseID, wakeResult.Record.Status)
					continue
				}
				mu.Lock()
				if existing, dup := wokenPauses[pauseID]; dup {
					t.Errorf("pause %q woken by both %q and %q", pauseID, existing, owner)
				}
				wokenPauses[pauseID] = owner
				mu.Unlock()
			}
		}(owner)
	}
	close(start)
	wg.Wait()

	if len(claimedBy) != jobCount {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimedBy), jobCount)
	}
	if len(wokenPauses) != jobCount {
		t.Fatalf("woke %d distinct pauses, want %d", len(wokenPauses), jobCount)
	}
	for _, pauseID := range pauseIDs {
		if _, ok := wokenPauses[pauseID]; !ok {
			t.Errorf("pause %q was never woken", pauseID)
		}
		rec, found, err := pauseStore.GetByID(context.Background(), pauseID)
		if err != nil || !found {
			t.Fatalf("GetByID(%q): found=%v err=%v", pauseID, found, err)
		}
		if rec.Status != domain.PauseWakePending {
			t.Errorf("pause %q final Status = %q, want %q", pauseID, rec.Status, domain.PauseWakePending)
		}
	}
}
