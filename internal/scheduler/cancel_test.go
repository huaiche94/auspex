// cancel_test.go: Store.Cancel (FR-163's storage half, issue #10) — legal
// only from `scheduled`, terminal representation `dead` +
// last_error=CancelledByOperator, and exactly-once resolution of the
// concurrent claim-vs-cancel race. Uses lease_test.go's helpers (same
// package): real file-backed SQLite, full migration set, seeded FK chain.
package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/scheduler"
)

func TestCancel_ScheduledJobBecomesDeadWithOperatorReason(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	cancelled, err := store.Cancel(ctx, job.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != scheduler.StatusDead {
		t.Errorf("cancelled.Status = %q, want %q", cancelled.Status, scheduler.StatusDead)
	}
	if cancelled.LastError == nil || *cancelled.LastError != scheduler.CancelledByOperator {
		t.Errorf("cancelled.LastError = %v, want %q", cancelled.LastError, scheduler.CancelledByOperator)
	}

	// A cancelled job is terminal: never claimable again.
	result, err := store.Claim(ctx, "worker-1", scheduler.DefaultLeaseDuration)
	if err != nil {
		t.Fatalf("Claim after cancel: %v", err)
	}
	if result.Found {
		t.Errorf("Claim after cancel found job %q — cancelled jobs must never be claimable", result.Job.ID)
	}
}

func TestCancel_LeasedJobConflicts(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if _, err := store.Claim(ctx, "worker-1", scheduler.DefaultLeaseDuration); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err = store.Cancel(ctx, job.ID)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("Cancel of leased job = %v, want *domain.Error with %q", err, domain.ErrCodeConflict)
	}
	if derr.Details["status"] != scheduler.StatusLeased {
		t.Errorf("conflict Details status = %q, want %q", derr.Details["status"], scheduler.StatusLeased)
	}

	// The leased job's execution path is untouched: the worker can still
	// complete it normally.
	if _, err := store.Complete(ctx, job.ID, "worker-1"); err != nil {
		t.Errorf("Complete after failed cancel: %v", err)
	}
}

func TestCancel_TerminalStatesConflict(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if _, err := store.Claim(ctx, "worker-1", scheduler.DefaultLeaseDuration); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := store.Complete(ctx, job.ID, "worker-1"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	_, err = store.Cancel(ctx, job.ID)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("Cancel of done job = %v, want *domain.Error with %q", err, domain.ErrCodeConflict)
	}

	// The job stays done — a failed cancel must not rewrite history.
	got, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != scheduler.StatusDone {
		t.Errorf("job.Status after rejected cancel = %q, want %q", got.Status, scheduler.StatusDone)
	}
}

func TestCancel_UnknownJobNotFound(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)

	_, err := store.Cancel(context.Background(), "does-not-exist")
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("Cancel of unknown job = %v, want *domain.Error with %q", err, domain.ErrCodeNotFound)
	}
}

func TestCancel_EmptyIDValidates(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)

	_, err := store.Cancel(context.Background(), "")
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("Cancel(\"\") = %v, want *domain.Error with %q", err, domain.ErrCodeValidation)
	}
}

// TestCancel_ConcurrentClaimResolvesExactlyOnce is the FR-163 race test:
// Cancel and Claim fired concurrently at the same due `scheduled` job must
// resolve to exactly one winner — either the job is leased (cancel got
// Conflict) or the job is dead-by-operator (claim found nothing). Never
// both, never neither.
func TestCancel_ConcurrentClaimResolvesExactlyOnce(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store, _ := newStore(t, clock)
	ctx := context.Background()

	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause1", Kind: "resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	var (
		wg        sync.WaitGroup
		claimRes  scheduler.ClaimResult
		claimErr  error
		cancelErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		claimRes, claimErr = store.Claim(ctx, "worker-1", scheduler.DefaultLeaseDuration)
	}()
	go func() {
		defer wg.Done()
		_, cancelErr = store.Cancel(ctx, job.ID)
	}()
	wg.Wait()

	if claimErr != nil {
		t.Fatalf("Claim: %v", claimErr)
	}

	claimWon := claimRes.Found
	cancelWon := cancelErr == nil
	if claimWon == cancelWon {
		t.Fatalf("claim/cancel race not exactly-once: claimWon=%v cancelWon=%v (cancelErr=%v)", claimWon, cancelWon, cancelErr)
	}

	final, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	switch {
	case claimWon:
		var derr *domain.Error
		if !errors.As(cancelErr, &derr) || derr.Code != domain.ErrCodeConflict {
			t.Errorf("losing Cancel = %v, want Conflict", cancelErr)
		}
		if final.Status != scheduler.StatusLeased {
			t.Errorf("final status = %q, want %q (claim won)", final.Status, scheduler.StatusLeased)
		}
	case cancelWon:
		if final.Status != scheduler.StatusDead {
			t.Errorf("final status = %q, want %q (cancel won)", final.Status, scheduler.StatusDead)
		}
		if final.LastError == nil || *final.LastError != scheduler.CancelledByOperator {
			t.Errorf("final LastError = %v, want %q", final.LastError, scheduler.CancelledByOperator)
		}
	}
}
