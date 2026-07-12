package scheduler_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/scheduler"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// --- fakes: deterministic Clock/IDGenerator, per internal/domain's ports ---

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type sequentialIDs struct {
	counter atomic.Int64
	prefix  string
}

func (g *sequentialIDs) NewID() string {
	return fmt.Sprintf("%s-%d", g.prefix, g.counter.Add(1))
}

// --- test DB setup: real file-backed SQLite, full embedded migration set ---

// openMigratedDB opens a fresh temp-file SQLite database, applies every
// embedded migration (through runtime-a01's 0050-0052 range), and seeds
// the repository -> worktree -> provider_session -> task -> pause_records
// chain foundation/runtime-a01 established, so wake_jobs rows can legally
// reference a real pause_id. Mirrors
// internal/storage/sqlite/migrations_0050_pause_test.go's
// migrateAndSeedPause helper (that file lives in package sqlite_test,
// unexported, so this package builds its own equivalent against the
// public sqlite.Open/AllMigrations/Migrate API).
func openMigratedDB(t *testing.T) *sqlite.DB {
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

	seed := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo', '/tmp/repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'repo1', '/tmp/repo', '/tmp/repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('sess1', 'wt1', 'claude-code', 'interactive', '2026-01-01T00:00:00Z', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('task1', 'sess1', 'wt1', 'hash1', 'pending', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO pause_records (id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
		 VALUES ('pause1', 'task1', 'sess1', 'turn1', 'rf1', 'sleeping', '2026-01-01T00:00:00Z', 1)`,
	}
	for _, stmt := range seed {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	return db
}

func newStore(t *testing.T, clock domain.Clock) (*scheduler.Store, *sqlite.DB) {
	t.Helper()
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	return scheduler.NewStore(db.Conn(), clock, ids), db
}

// --- Claim/Renew/Complete/Fail/Retry basic lifecycle ------------------------

func TestLease_ScheduleThenClaim(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if job.Status != scheduler.StatusScheduled {
		t.Fatalf("job.Status = %q, want scheduled", job.Status)
	}
	if job.Attempts != 0 {
		t.Fatalf("job.Attempts = %d, want 0", job.Attempts)
	}

	result, err := store.Claim(ctx, "worker-1", scheduler.DefaultLeaseDuration)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !result.Found {
		t.Fatal("Claim: Found = false, want true (job is due)")
	}
	if result.Job.ID != job.ID {
		t.Fatalf("Claim returned job %q, want %q", result.Job.ID, job.ID)
	}
	if result.Job.Status != scheduler.StatusLeased {
		t.Fatalf("claimed job.Status = %q, want leased", result.Job.Status)
	}
	if result.Job.LeaseOwner == nil || *result.Job.LeaseOwner != "worker-1" {
		t.Fatalf("claimed job.LeaseOwner = %v, want worker-1", result.Job.LeaseOwner)
	}
	if result.Job.Attempts != 1 {
		t.Fatalf("claimed job.Attempts = %d, want 1 (incremented on claim per ADD §12.4)", result.Job.Attempts)
	}
}

func TestLease_ClaimSkipsNotYetDueJob(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	_, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now().Add(1 * time.Hour), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	result, err := store.Claim(ctx, "worker-1", scheduler.DefaultLeaseDuration)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if result.Found {
		t.Fatal("Claim: Found = true, want false (job's run_after is in the future)")
	}
}

func TestLease_RenewExtendsLease(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	claimed, err := store.Claim(ctx, "worker-1", 60*time.Second)
	if err != nil || !claimed.Found {
		t.Fatalf("Claim: found=%v err=%v", claimed.Found, err)
	}
	firstExpiry := *claimed.Job.LeaseExpires

	clock.Advance(30 * time.Second)
	renewed, err := store.Renew(ctx, job.ID, "worker-1", 60*time.Second)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if !renewed.LeaseExpires.After(firstExpiry) {
		t.Fatalf("renewed lease expiry %v did not extend past original %v", renewed.LeaseExpires, firstExpiry)
	}
}

func TestLease_RenewByWrongOwnerFailsConflict(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	if _, err := store.Claim(ctx, "worker-1", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err := store.Renew(ctx, job.ID, "worker-2", 60*time.Second)
	assertConflict(t, err)
}

func TestLease_CompleteMarksDone(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	if _, err := store.Claim(ctx, "worker-1", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	done, err := store.Complete(ctx, job.ID, "worker-1")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if done.Status != scheduler.StatusDone {
		t.Fatalf("job.Status = %q, want done", done.Status)
	}

	// A completed job must not be claimable again.
	result, err := store.Claim(ctx, "worker-2", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim after complete: %v", err)
	}
	if result.Found {
		t.Fatal("Claim after complete: Found = true, want false (job is done)")
	}
}

func TestLease_CompleteByWrongOwnerFailsConflict(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	if _, err := store.Claim(ctx, "worker-1", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err := store.Complete(ctx, job.ID, "worker-2")
	assertConflict(t, err)
}

// TestLease_FailReschedulesWithBackoff proves ADD §20.7's retry schedule:
// a failed attempt with attempts remaining returns to `scheduled` with
// run_after advanced by RetryBackoff[attempts-1], and releases the lease.
func TestLease_FailReschedulesWithBackoff(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 5})
	claimed, err := store.Claim(ctx, "worker-1", 60*time.Second)
	if err != nil || !claimed.Found {
		t.Fatalf("Claim: found=%v err=%v", claimed.Found, err)
	}

	failed, err := store.Fail(ctx, job.ID, "worker-1", "provider unavailable")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if failed.Status != scheduler.StatusScheduled {
		t.Fatalf("job.Status after first failure = %q, want scheduled (retry)", failed.Status)
	}
	if failed.LeaseOwner != nil {
		t.Fatalf("job.LeaseOwner after failure = %v, want nil (lease released)", failed.LeaseOwner)
	}
	wantRunAfter := clock.Now().Add(scheduler.RetryBackoff[0]) // attempts was 1 at time of failure
	if !failed.RunAfter.Equal(wantRunAfter) {
		t.Fatalf("job.RunAfter = %v, want %v (15s backoff after 1st attempt)", failed.RunAfter, wantRunAfter)
	}
	if failed.LastError == nil || *failed.LastError != "provider unavailable" {
		t.Fatalf("job.LastError = %v, want %q", failed.LastError, "provider unavailable")
	}

	// Not claimable again until the backoff elapses.
	notYet, err := store.Claim(ctx, "worker-2", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim (too early): %v", err)
	}
	if notYet.Found {
		t.Fatal("Claim before backoff elapsed: Found = true, want false")
	}

	clock.Advance(scheduler.RetryBackoff[0] + time.Second)
	retried, err := store.Claim(ctx, "worker-2", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim after backoff: %v", err)
	}
	if !retried.Found {
		t.Fatal("Claim after backoff elapsed: Found = false, want true")
	}
	if retried.Job.Attempts != 2 {
		t.Fatalf("job.Attempts on 2nd claim = %d, want 2", retried.Job.Attempts)
	}
}

// TestLease_FailExhaustsMaxAttemptsGoesDead proves a job with no attempts
// remaining goes to the terminal `dead` status instead of being
// rescheduled forever.
func TestLease_FailExhaustsMaxAttemptsGoesDead(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 1})
	claimed, err := store.Claim(ctx, "worker-1", 60*time.Second)
	if err != nil || !claimed.Found {
		t.Fatalf("Claim: found=%v err=%v", claimed.Found, err)
	}
	if claimed.Job.Attempts != 1 {
		t.Fatalf("attempts after first claim = %d, want 1", claimed.Job.Attempts)
	}

	failed, err := store.Fail(ctx, job.ID, "worker-1", "permanent failure")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if failed.Status != scheduler.StatusDead {
		t.Fatalf("job.Status = %q, want dead (attempts %d >= max %d)", failed.Status, failed.Attempts, failed.MaxAttempts)
	}

	// A dead job is never claimable, no matter how far the clock advances.
	clock.Advance(24 * time.Hour)
	result, err := store.Claim(ctx, "worker-2", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if result.Found {
		t.Fatal("Claim found a dead job, want none")
	}
}

func TestLease_FailByWrongOwnerFailsConflict(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	if _, err := store.Claim(ctx, "worker-1", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err := store.Fail(ctx, job.ID, "worker-2", "not mine")
	assertConflict(t, err)
}

// --- Required test: "expired lease reclaimed" -------------------------------

func TestLease_ExpiredLeaseReclaimedByAnotherWorker(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})

	first, err := store.Claim(ctx, "worker-1", 60*time.Second)
	if err != nil || !first.Found {
		t.Fatalf("first Claim: found=%v err=%v", first.Found, err)
	}

	// worker-1 vanishes (crash) without completing/failing/renewing.
	// Advance the clock past the lease's expiry.
	clock.Advance(61 * time.Second)

	second, err := store.Claim(ctx, "worker-2", 60*time.Second)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if !second.Found {
		t.Fatal("second Claim: Found = false, want true (worker-1's lease expired)")
	}
	if second.Job.ID != job.ID {
		t.Fatalf("second Claim returned job %q, want %q", second.Job.ID, job.ID)
	}
	if second.Job.LeaseOwner == nil || *second.Job.LeaseOwner != "worker-2" {
		t.Fatalf("second Claim job.LeaseOwner = %v, want worker-2", second.Job.LeaseOwner)
	}
	if second.Job.Attempts != 2 {
		t.Fatalf("job.Attempts after reclaim = %d, want 2 (one per claim)", second.Job.Attempts)
	}

	// worker-1's now-stale Complete/Renew calls must fail: it no longer
	// holds the lease.
	if _, err := store.Complete(ctx, job.ID, "worker-1"); err == nil {
		t.Fatal("worker-1's stale Complete succeeded after its lease was reclaimed, want conflict")
	}
}

// TestLease_ReclaimExpired proves the explicit restart-recovery sweep
// (ADD §28.3 step 2): a leased-but-expired job is reset to `scheduled`
// with its lease cleared, independent of a subsequent Claim call.
func TestLease_ReclaimExpired(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, _ := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	if _, err := store.Claim(ctx, "worker-1", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	clock.Advance(61 * time.Second)

	n, err := store.ReclaimExpired(ctx)
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReclaimExpired returned %d, want 1", n)
	}

	reloaded, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.Status != scheduler.StatusScheduled {
		t.Fatalf("job.Status after ReclaimExpired = %q, want scheduled", reloaded.Status)
	}
	if reloaded.LeaseOwner != nil {
		t.Fatalf("job.LeaseOwner after ReclaimExpired = %v, want nil", reloaded.LeaseOwner)
	}

	// A not-yet-expired lease must not be touched.
	if _, err := store.Claim(ctx, "worker-2", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	n2, err := store.ReclaimExpired(ctx)
	if err != nil {
		t.Fatalf("ReclaimExpired (2nd): %v", err)
	}
	if n2 != 0 {
		t.Fatalf("ReclaimExpired (2nd) returned %d, want 0 (lease not yet expired)", n2)
	}
}

// --- Required test: "duplicate workers yield one resume" (concurrency) -----

// TestLease_ConcurrentWorkersYieldOneClaim is this node's core correctness
// proof, matching the DAG's stated risk ("lease correctness under
// concurrent workers is the whole point") and agents/runtime.md's required
// test "duplicate workers yield one resume": many goroutines race to claim
// the SAME single due job through the SAME shared *sql.DB connection pool
// (mirroring multiple daemon/CLI processes sharing one SQLite file); at
// most one may win. Run with -race per the task brief.
func TestLease_ConcurrentWorkersYieldOneClaim(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, db := newStore(t, clock)
	_ = db
	ctx := context.Background()

	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 100})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	const workers = 20
	var (
		wg        sync.WaitGroup
		successes atomic.Int64
		mu        sync.Mutex
		winners   []string
	)

	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		owner := fmt.Sprintf("worker-%d", i)
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			result, err := store.Claim(ctx, owner, 60*time.Second)
			if err != nil {
				// A lease conflict/busy retry is an acceptable outcome
				// under contention; anything else is a real bug.
				return
			}
			if result.Found {
				successes.Add(1)
				mu.Lock()
				winners = append(winners, owner)
				mu.Unlock()
			}
		}(owner)
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful claims = %d, want exactly 1; winners=%v", got, winners)
	}

	final, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != scheduler.StatusLeased {
		t.Fatalf("final job.Status = %q, want leased", final.Status)
	}
	if final.Attempts != 1 {
		t.Fatalf("final job.Attempts = %d, want exactly 1 (only the winner incremented it)", final.Attempts)
	}
	if final.LeaseOwner == nil || *final.LeaseOwner != winners[0] {
		t.Fatalf("final job.LeaseOwner = %v, want %q", final.LeaseOwner, winners[0])
	}
}

// TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce extends the
// single-job race proof to N jobs claimed by M workers concurrently: the
// total number of successful claims must equal exactly the number of due
// jobs (no job double-claimed, no job skipped).
func TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	const jobCount = 8
	jobIDs := make(map[domain.WakeJobID]bool, jobCount)
	for i := 0; i < jobCount; i++ {
		job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
			PauseID: "pause1", Kind: fmt.Sprintf("kind-%d", i), RunAfter: clock.Now(), MaxAttempts: 3,
		})
		if err != nil {
			t.Fatalf("Schedule %d: %v", i, err)
		}
		jobIDs[job.ID] = true
	}

	const workers = 12
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed = map[domain.WakeJobID]string{}
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		owner := fmt.Sprintf("worker-%d", i)
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			for {
				result, err := store.Claim(ctx, owner, 60*time.Second)
				if err != nil || !result.Found {
					return
				}
				mu.Lock()
				if existing, dup := claimed[result.Job.ID]; dup {
					t.Errorf("job %q claimed by both %q and %q", result.Job.ID, existing, owner)
				}
				claimed[result.Job.ID] = owner
				mu.Unlock()
			}
		}(owner)
	}
	close(start)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(claimed) != jobCount {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimed), jobCount)
	}
	for id := range jobIDs {
		if _, ok := claimed[id]; !ok {
			t.Errorf("job %q was never claimed", id)
		}
	}
}

// --- Validation / error-shape tests ------------------------------------------

func TestLease_ScheduleRejectsDuplicatePauseAndKind(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	if _, err := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3}); err != nil {
		t.Fatalf("first Schedule: %v", err)
	}
	_, err := store.Schedule(ctx, scheduler.ScheduleRequest{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3})
	if err == nil {
		t.Fatal("expected an error scheduling a duplicate (pause_id, job_kind)")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("got err %v (%T), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeConflict {
		t.Fatalf("Code = %q, want %q", derr.Code, domain.ErrCodeConflict)
	}
}

func TestLease_ScheduleValidatesRequest(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	cases := []scheduler.ScheduleRequest{
		{PauseID: "", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3},
		{PauseID: "pause1", Kind: "", RunAfter: clock.Now(), MaxAttempts: 3},
		{PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 0},
	}
	for i, req := range cases {
		if _, err := store.Schedule(ctx, req); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestLease_ClaimRejectsEmptyOwner(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	_, err := store.Claim(context.Background(), "", 60*time.Second)
	if err == nil {
		t.Fatal("expected an error for empty owner")
	}
}

func TestLease_GetUnknownJobFailsNotFound(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	_, err := store.Get(context.Background(), "does-not-exist")
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("got err %v, want *domain.Error", err)
	}
	if derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("Code = %q, want %q", derr.Code, domain.ErrCodeNotFound)
	}
}

// --- helpers ------------------------------------------------------------

func assertConflict(t *testing.T, err error) {
	t.Helper()
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("got err %v (%T), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeConflict {
		t.Fatalf("Code = %q, want %q", derr.Code, domain.ErrCodeConflict)
	}
}

// Compile-time sanity: *sql.DB (as returned by sqlite.DB.Conn()) satisfies
// scheduler.DB.
var _ scheduler.DB = (*sql.DB)(nil)
