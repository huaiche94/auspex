package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/scheduler"
)

// TestRestart_RecoversWakeJob is the required test (verbatim): "restart
// recovers wake job." Simulates an unclean shutdown — a job claimed
// (leased) by a worker that then vanishes without completing, failing, or
// renewing — and proves a FRESH Store (standing in for a fresh process
// startup, sharing the same underlying DB) correctly recovers it via
// Restart and can re-claim it, with no duplicate execution.
func TestRestart_RecoversWakeJob(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	ctx := context.Background()

	// "Old process": schedules and claims a job, then crashes (never
	// calls Complete/Fail/Renew). Critically, the lease has NOT yet
	// expired (60s lease, no time advanced) — proving Restart recovers it
	// even though ReclaimExpired alone would not (see restart.go's doc
	// comment on why Restart is unconditional).
	oldProcess := scheduler.NewStore(db.Conn(), clock, ids)
	job, err := oldProcess.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	claimed, err := oldProcess.Claim(ctx, "old-worker", 60*time.Second)
	if err != nil || !claimed.Found {
		t.Fatalf("Claim: found=%v err=%v", claimed.Found, err)
	}
	if claimed.Job.Status != scheduler.StatusLeased {
		t.Fatalf("job.Status after Claim = %q, want leased", claimed.Job.Status)
	}

	// "Fresh process startup": a brand new Store against the SAME
	// underlying DB (mirrors a daemon restart re-opening its existing
	// SQLite file), lease not expired yet.
	freshProcess := scheduler.NewStore(db.Conn(), clock, ids)
	report, err := freshProcess.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased != 1 {
		t.Fatalf("report.RecoveredLeased = %d, want 1", report.RecoveredLeased)
	}

	reloaded, err := freshProcess.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get after Restart: %v", err)
	}
	if reloaded.Status != scheduler.StatusScheduled {
		t.Fatalf("job.Status after Restart = %q, want scheduled", reloaded.Status)
	}
	if reloaded.LeaseOwner != nil {
		t.Fatalf("job.LeaseOwner after Restart = %v, want nil", reloaded.LeaseOwner)
	}

	// The recovered job must be claimable again by a new worker in the
	// new process.
	reclaimed, err := freshProcess.Claim(ctx, "new-worker", 60*time.Second)
	if err != nil {
		t.Fatalf("Claim after Restart: %v", err)
	}
	if !reclaimed.Found {
		t.Fatal("Claim after Restart: Found = false, want true (job should be claimable again)")
	}
	if reclaimed.Job.ID != job.ID {
		t.Fatalf("Claim after Restart returned job %q, want %q", reclaimed.Job.ID, job.ID)
	}
	if reclaimed.Job.LeaseOwner == nil || *reclaimed.Job.LeaseOwner != "new-worker" {
		t.Fatalf("reclaimed.Job.LeaseOwner = %v, want new-worker", reclaimed.Job.LeaseOwner)
	}

	// No duplicate execution: the old worker's stale Complete call must
	// still fail (it no longer holds the lease that matters).
	if _, err := freshProcess.Complete(ctx, job.ID, "old-worker"); err == nil {
		t.Fatal("old-worker's stale Complete succeeded after Restart recovery, want conflict")
	}
	// And the job must not have been silently duplicated into a second
	// row: exactly one row for this (pause_id, job_kind) pair exists,
	// enforced at the schema level (UNIQUE(pause_id, job_kind)) and
	// confirmed here by re-fetching the same ID and observing a single,
	// consistent Attempts count (one increment per Claim call: old
	// process's claim, then the new worker's reclaim-and-claim).
	final, err := freshProcess.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Attempts != 2 {
		t.Fatalf("final.Attempts = %d, want 2 (one per Claim call, no duplicate execution)", final.Attempts)
	}
}

// TestRestart_ExpiredLeaseAlsoRecovered proves Restart also correctly
// covers the case ReclaimExpired already handled (a lease that HAS
// expired by the time the process restarts) — Restart is a superset, not
// a replacement with narrower coverage.
func TestRestart_ExpiredLeaseAlsoRecovered(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	ctx := context.Background()

	store := scheduler.NewStore(db.Conn(), clock, ids)
	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if _, err := store.Claim(ctx, "old-worker", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	clock.Advance(2 * time.Minute)

	report, err := store.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased != 1 {
		t.Fatalf("report.RecoveredLeased = %d, want 1", report.RecoveredLeased)
	}

	reloaded, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.Status != scheduler.StatusScheduled {
		t.Fatalf("job.Status after Restart = %q, want scheduled", reloaded.Status)
	}
}

// TestRestart_DoneAndDeadJobsUntouched proves Restart never resurrects a
// job that already finished (done) or was already exhausted (dead) —
// "without creating duplicate execution" requires that a job which
// genuinely completed before the crash is never re-run.
func TestRestart_DoneAndDeadJobsUntouched(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	ctx := context.Background()

	store := scheduler.NewStore(db.Conn(), clock, ids)

	doneJob, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule (done): %v", err)
	}
	if _, err := store.Claim(ctx, "worker-done", 60*time.Second); err != nil {
		t.Fatalf("Claim (done): %v", err)
	}
	if _, err := store.Complete(ctx, doneJob.ID, "worker-done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	deadJob, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "expire_check", RunAfter: clock.Now(), MaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("Schedule (dead): %v", err)
	}
	if _, err := store.Claim(ctx, "worker-dead", 60*time.Second); err != nil {
		t.Fatalf("Claim (dead): %v", err)
	}
	if _, err := store.Fail(ctx, deadJob.ID, "worker-dead", "boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	deadReloaded, err := store.Get(ctx, deadJob.ID)
	if err != nil {
		t.Fatalf("Get (dead): %v", err)
	}
	if deadReloaded.Status != scheduler.StatusDead {
		t.Fatalf("deadJob.Status = %q, want dead (MaxAttempts=1 exhausted)", deadReloaded.Status)
	}

	report, err := store.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased != 0 {
		t.Fatalf("report.RecoveredLeased = %d, want 0 (no leased jobs at all)", report.RecoveredLeased)
	}

	doneReloaded, err := store.Get(ctx, doneJob.ID)
	if err != nil {
		t.Fatalf("Get (done): %v", err)
	}
	if doneReloaded.Status != scheduler.StatusDone {
		t.Fatalf("doneJob.Status after Restart = %q, want done (must not be resurrected)", doneReloaded.Status)
	}

	deadAfterRestart, err := store.Get(ctx, deadJob.ID)
	if err != nil {
		t.Fatalf("Get (dead) after Restart: %v", err)
	}
	if deadAfterRestart.Status != scheduler.StatusDead {
		t.Fatalf("deadJob.Status after Restart = %q, want dead (must not be resurrected)", deadAfterRestart.Status)
	}
}

// TestRestart_MultipleLeasedJobsAllRecovered proves Restart recovers every
// leased job in one sweep, not just the first one found.
func TestRestart_MultipleLeasedJobsAllRecovered(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	ctx := context.Background()

	store := scheduler.NewStore(db.Conn(), clock, ids)

	kinds := []string{"resume", "expire_check", "notify"}
	var jobIDs []domain.WakeJobID
	for _, kind := range kinds {
		job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
			PauseID: "pause1", Kind: kind, RunAfter: clock.Now(), MaxAttempts: 3,
		})
		if err != nil {
			t.Fatalf("Schedule(%q): %v", kind, err)
		}
		if _, err := store.Claim(ctx, "worker-"+kind, 60*time.Second); err != nil {
			t.Fatalf("Claim(%q): %v", kind, err)
		}
		jobIDs = append(jobIDs, job.ID)
	}

	report, err := store.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased != len(kinds) {
		t.Fatalf("report.RecoveredLeased = %d, want %d", report.RecoveredLeased, len(kinds))
	}

	for i, id := range jobIDs {
		reloaded, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%q): %v", kinds[i], err)
		}
		if reloaded.Status != scheduler.StatusScheduled {
			t.Fatalf("job %q (%s) status = %q, want scheduled", id, kinds[i], reloaded.Status)
		}
	}
}

// TestRestart_OverdueClaimableReportsCount proves the informational
// OverdueClaimable count (feeding the ADD §28.3 step 10 startup report)
// reflects jobs whose run_after is already due, including ones just
// recovered by this same Restart call.
func TestRestart_OverdueClaimableReportsCount(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	ctx := context.Background()

	store := scheduler.NewStore(db.Conn(), clock, ids)

	// One job overdue and leased (will be recovered, then counted as
	// overdue-claimable).
	if _, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if _, err := store.Claim(ctx, "worker-1", 60*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// A second job, still scheduled (never claimed), not yet due.
	future := clock.Now().Add(time.Hour)
	if _, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "notify", RunAfter: future, MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("Schedule (future): %v", err)
	}

	report, err := store.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased != 1 {
		t.Fatalf("report.RecoveredLeased = %d, want 1", report.RecoveredLeased)
	}
	if report.OverdueClaimable != 1 {
		t.Fatalf("report.OverdueClaimable = %d, want 1 (only the recovered job is due; the future one is not)", report.OverdueClaimable)
	}
}

// TestRestart_NoLeasedJobsIsNoOp proves Restart on a quiescent scheduler
// (nothing was leased at crash time) is a harmless no-op, not an error.
func TestRestart_NoLeasedJobsIsNoOp(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	ids := &sequentialIDs{prefix: "wj"}
	ctx := context.Background()

	store := scheduler.NewStore(db.Conn(), clock, ids)
	if _, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	report, err := store.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased != 0 {
		t.Fatalf("report.RecoveredLeased = %d, want 0", report.RecoveredLeased)
	}
	if report.OverdueClaimable != 1 {
		t.Fatalf("report.OverdueClaimable = %d, want 1 (the never-claimed job is already due)", report.OverdueClaimable)
	}
}
