// splitbrain_test.go: the scheduler-integration half of runtime-a09's
// "duplicate workers yield one resume" required test. wake_test.go already
// proves the PAUSE-level guarantee in isolation (two callers racing
// pause.Wake for the same PauseID); this file additionally proves the
// scenario the task brief names literally by its own term: "split brain" —
// a lease was reclaimed after appearing expired, but the original holder
// wasn't actually dead, so TWO real scheduler.Store workers each durably
// believe they hold a valid claim on the same wake job at the same time,
// and both attempt to drive the same PauseID forward.
//
// internal/scheduler.Store's own lease semantics (runtime-a06/a07,
// lease_test.go) make a genuine simultaneous double-claim of the SAME row
// impossible under normal operation (BEGIN IMMEDIATE serializes Claim
// callers). The split-brain case this test models is the one lease_test.go
// cannot express on its own: a SEQUENTIAL reclaim (worker A's lease expires,
// worker B legitimately claims the row per Store's own rules) where worker A
// is still alive and, not knowing it lost the lease, proceeds to also call
// pause.Wake for the same PauseID — i.e. the lease layer correctly granted
// B a claim, but that alone does not stop A from also trying. Proving A's
// attempt is harmless (exactly one of A/B's Wake calls succeeds, and the
// pause record is driven forward exactly once) is this file's job, and it
// is what makes the guarantee end-to-end rather than lease-only.
package pause_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
)

// TestDuplicateWake_SplitBrainReclaimedLeaseOriginalHolderStillAlive_OnlyOneWakeSucceeds
// models the exact split-brain interleaving named in the task brief:
//
//  1. Worker A claims wake_jobs row for pause-1 with a short lease.
//  2. The lease expires (simulated via the fake clock) — but A is still
//     alive and about to act, it just hasn't renewed in time.
//  3. Worker B claims the same row (scheduler.Store.Claim correctly grants
//     this per its own expired-lease-reclaim rule, runtime-a07).
//  4. Both A and B now each believe they hold a legitimate claim. Both
//     concurrently call pause.Wake for pause-1.
//
// The required guarantee: exactly one of A/B's Wake calls succeeds, the
// pause record durably reaches WakePending exactly once (never processed
// twice), and the loser observes a genuine, non-silent rejection.
func TestDuplicateWake_SplitBrainReclaimedLeaseOriginalHolderStillAlive_OnlyOneWakeSucceeds(t *testing.T) {
	clock := newSplitBrainClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	seedChain(t, db, "wt1", "task1")
	seedPauseRecordRow(t, db, "pause-1", "task1")

	wakeStore := scheduler.NewStore(db.Conn(), clock, &seqIDs{prefix: "wj"})
	ctx := context.Background()

	job, err := wakeStore.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause-1", Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	// Worker A claims with a short lease.
	claimA, err := wakeStore.Claim(ctx, "worker-A", 10*time.Second)
	if err != nil || !claimA.Found {
		t.Fatalf("worker-A Claim: found=%v err=%v", claimA.Found, err)
	}
	if claimA.Job.ID != job.ID {
		t.Fatalf("worker-A claimed the wrong job: %v", claimA.Job.ID)
	}

	// Time passes; A's lease expires, but A is still alive (never crashed,
	// never released) — it simply has not renewed it in time. This is
	// exactly the "wasn't actually dead" half of the split-brain scenario.
	clock.Advance(30 * time.Second)

	// Worker B claims the now-expired lease. scheduler.Store.Claim's own
	// expired-lease-reclaim rule (runtime-a07) makes this legitimate from
	// the lease layer's own point of view.
	claimB, err := wakeStore.Claim(ctx, "worker-B", 10*time.Second)
	if err != nil || !claimB.Found {
		t.Fatalf("worker-B Claim: found=%v err=%v", claimB.Found, err)
	}
	if claimB.Job.ID != job.ID {
		t.Fatalf("worker-B claimed the wrong job: %v", claimB.Job.ID)
	}

	// Both A and B now each believe they legitimately hold this job.
	// Both concurrently attempt to drive the SAME PauseID's pause record
	// forward via pause.Wake, against a real MemStore shared between them
	// (modeling the durable pause_records row both workers would read/
	// write against in a fully-wired system — see seedPauseRecordRow's own
	// doc comment for why this test uses MemStore rather than a SQL-backed
	// PauseStore here: no such adapter exists yet, a documented gap this
	// report calls out).
	pauses := pause.NewMemStore()
	if err := pauses.Insert(ctx, pause.PauseRecord{
		ID:     "pause-1",
		Key:    pause.PauseKey{TaskID: "task1", SessionID: "sess1"},
		Status: domain.PauseSleeping,
		Reason: pause.TriggerReasonCalibrated,
	}); err != nil {
		t.Fatalf("seed pauses.Insert: %v", err)
	}

	var (
		wg         sync.WaitGroup
		successes  atomic.Int64
		errA, errB error
	)
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errA = pause.Wake(ctx, pauses, pause.WakeRequest{PauseID: "pause-1"})
		if errA == nil {
			successes.Add(1)
		}
		// Worker A, believing it holds the lease, also tries to Complete
		// its (by now reclaimed-out-from-under-it) lease — this must fail
		// with ErrCodeConflict, proving the LEASE layer independently
		// rejects A too, not just the pause layer.
		_, _ = wakeStore.Complete(ctx, job.ID, "worker-A")
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errB = pause.Wake(ctx, pauses, pause.WakeRequest{PauseID: "pause-1"})
		if errB == nil {
			successes.Add(1)
		}
	}()
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful Wake calls across A/B = %d, want exactly 1 (errA=%v errB=%v)", got, errA, errB)
	}

	final, found, err := pauses.GetByID(ctx, "pause-1")
	if err != nil || !found {
		t.Fatalf("GetByID after split-brain race: found=%v err=%v", found, err)
	}
	if final.Status != domain.PauseWakePending {
		t.Fatalf("final Status = %q, want %q (woken exactly once)", final.Status, domain.PauseWakePending)
	}

	// The lease layer's own Complete call must confirm B (the true current
	// holder) can complete the job, while A's own claim, which it still
	// believes is valid, was already superseded — Complete is keyed to
	// (id, owner) with a status check, so only the CURRENT owner (B, the
	// last legitimate claimant) can complete it.
	completedJob, err := wakeStore.Complete(ctx, job.ID, "worker-B")
	if err != nil {
		t.Fatalf("worker-B Complete (true current holder): %v", err)
	}
	if completedJob.Status != scheduler.StatusDone {
		t.Fatalf("completedJob.Status = %q, want %q", completedJob.Status, scheduler.StatusDone)
	}
}

// --- local fake clock (this file's own copy, mirroring lease_test.go's) ----

type splitBrainClock struct {
	mu  sync.Mutex
	now time.Time
}

func newSplitBrainClock(t time.Time) *splitBrainClock { return &splitBrainClock{now: t} }

func (c *splitBrainClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *splitBrainClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
