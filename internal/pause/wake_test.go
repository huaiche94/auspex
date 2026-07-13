package pause_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/pause"
)

// --- Wake: basic behavior --------------------------------------------------

func TestWake_SleepingTransitionsToWakePending(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)
	result, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "pause-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if result.Record.Status != domain.PauseWakePending {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseWakePending)
	}
	rec, _, _ := store.GetByID(context.Background(), "pause-1")
	if rec.Status != domain.PauseWakePending {
		t.Fatalf("durable Status = %q, want %q", rec.Status, domain.PauseWakePending)
	}
}

func TestWake_UnknownPauseIDFailsNotFound(t *testing.T) {
	store := pause.NewMemStore()
	_, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "does-not-exist"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want ErrCodeNotFound", err)
	}
}

func TestWake_ValidatesRequest(t *testing.T) {
	store := pause.NewMemStore()
	_, err := pause.Wake(context.Background(), store, pause.WakeRequest{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

func TestWake_NilStoreFailsClosed(t *testing.T) {
	_, err := pause.Wake(context.Background(), nil, pause.WakeRequest{PauseID: "pause-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

func TestWake_NonSleepingStateRejected(t *testing.T) {
	// EventWakeDue has exactly one edge in the transition table, from
	// Sleeping (statemachine.go). Any other non-terminal state must reject
	// it, not silently no-op or clamp.
	store := newSeededMemStore(t, "pause-1", domain.PauseQuiescing)
	_, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "pause-1"})
	var terr *pause.TransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TransitionError", err)
	}
}

func TestWake_AlreadyWakePendingRejectedNotDuplicated(t *testing.T) {
	// A record already moved past Sleeping (e.g. a prior Wake call already
	// won) must reject a second EventWakeDue attempt outright — this is
	// the sequential (non-concurrent) half of "duplicate workers yield one
	// resume": even called twice in strict sequence, the second call must
	// not pretend to succeed.
	store := newSeededMemStore(t, "pause-1", domain.PauseWakePending)
	_, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "pause-1"})
	var terr *pause.TransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TransitionError (no edge for EventWakeDue from WakePending)", err)
	}
}

// --- Required test: "duplicate workers yield one resume" -------------------
//
// This is the PAUSE-level proof the task brief asks for: independent of
// scheduler.Store's own lease-claim exclusivity (proven in
// internal/scheduler/lease_test.go's TestLease_ConcurrentWorkersYieldOneClaim),
// this test simulates the split-brain scenario where TWO workers both
// believe they legitimately hold a claim on the SAME wake job/PauseID (e.g.
// because a lease was reclaimed after appearing expired, but the original
// holder was not actually dead) and both concurrently call Wake for that
// PauseID. Exactly one must observe success (ok=true, transitioned to
// WakePending); the other must observe a definitive rejection — never two
// concurrent "successful" wakes on the same pause.
func TestDuplicateWake_WorkersYieldOneResume(t *testing.T) {
	const attempts = 50 // repeat the race many times to make a flaky
	// implementation (e.g. a lock-free read-modify-write) fail reliably,
	// mirroring qa-07's own "-count=20" repeated-race discipline.
	for attempt := 0; attempt < attempts; attempt++ {
		store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)

		const workers = 20
		var (
			wg        sync.WaitGroup
			successes atomic.Int64
			mu        sync.Mutex
			results   []error
		)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "pause-1"})
				mu.Lock()
				results = append(results, err)
				mu.Unlock()
				if err == nil {
					successes.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()

		if got := successes.Load(); got != 1 {
			t.Fatalf("attempt %d: successful Wake calls = %d, want exactly 1; errors=%v", attempt, got, results)
		}

		// Every failing attempt must be a real TransitionError (the
		// record already moved off Sleeping), never a silent success
		// disguised as something else, and never a different error class
		// (e.g. a panic-recovered generic error) that would mask the
		// actual outcome.
		var transitionErrs int
		for _, err := range results {
			if err == nil {
				continue
			}
			var terr *pause.TransitionError
			if !errors.As(err, &terr) {
				t.Fatalf("attempt %d: a losing Wake call returned %v (%T), want *pause.TransitionError", attempt, err, err)
			}
			transitionErrs++
		}
		if transitionErrs != workers-1 {
			t.Fatalf("attempt %d: got %d TransitionErrors, want %d (workers-1)", attempt, transitionErrs, workers-1)
		}

		final, found, err := store.GetByID(context.Background(), "pause-1")
		if err != nil || !found {
			t.Fatalf("attempt %d: GetByID after race: found=%v err=%v", attempt, found, err)
		}
		if final.Status != domain.PauseWakePending {
			t.Fatalf("attempt %d: final Status = %q, want %q", attempt, final.Status, domain.PauseWakePending)
		}
	}
}

// TestDuplicateWake_WorkersAcrossManyPausesEachWokenOnce extends the
// single-pause race proof to N independently-sleeping pauses raced by M
// workers each — proving the exactly-once guarantee holds per-PauseID, not
// merely as an artifact of there being only one record to contend over in
// the test.
func TestDuplicateWake_WorkersAcrossManyPausesEachWokenOnce(t *testing.T) {
	store := pause.NewMemStore()
	const pauseCount = 8
	for i := 0; i < pauseCount; i++ {
		id := domain.PauseID(pauseIDFor(i))
		if err := store.Insert(context.Background(), pause.PauseRecord{
			ID:     id,
			Key:    pause.PauseKey{TaskID: domain.TaskID(pauseIDFor(i)), SessionID: "sess-1"},
			Status: domain.PauseSleeping,
			Reason: pause.TriggerReasonCalibrated,
		}); err != nil {
			t.Fatalf("seed Insert %d: %v", i, err)
		}
	}

	const workersPerPause = 6
	var wg sync.WaitGroup
	successes := make([]atomic.Int64, pauseCount)
	start := make(chan struct{})
	for i := 0; i < pauseCount; i++ {
		id := domain.PauseID(pauseIDFor(i))
		idx := i
		for w := 0; w < workersPerPause; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: id})
				if err == nil {
					successes[idx].Add(1)
				}
			}()
		}
	}
	close(start)
	wg.Wait()

	for i := 0; i < pauseCount; i++ {
		if got := successes[i].Load(); got != 1 {
			t.Errorf("pause %d: successful Wake calls = %d, want exactly 1", i, got)
		}
	}
}

func pauseIDFor(i int) string {
	return "pause-multi-" + string(rune('a'+i))
}

// --- Required test: "cancel wins race with wake" ----------------------------
//
// Genuine concurrent proof (-race): Cancel and Wake are fired concurrently
// at the same Sleeping PauseID, many times over. The first version of this
// test asserted "exactly one of Cancel/Wake succeeds" — that assumption is
// WRONG and this test caught it failing immediately: per statemachine.go,
// WakePending still has an EventCancel edge to Cancelled, so it is entirely
// legitimate for Wake to land Sleeping->WakePending and THEN Cancel to land
// WakePending->Cancelled a moment later — both calls report success, and
// that is "cancel wins race with wake" working exactly as intended, not a
// violation. The property that actually matters, and the one this test
// enforces, is narrower and stronger:
//
//  1. Cancel must NEVER be spuriously rejected merely because Wake is
//     concurrently in flight. In this two-way race (no Resume involved),
//     every state Wake alone can reach from Sleeping (i.e. WakePending)
//     still has an outbound Cancel edge, so Cancel must always eventually
//     succeed here — its retry loop (applyCASVerb, lifecycle.go) keeps
//     re-reading and re-attempting until it lands EventCancel.
//  2. The final durable status is therefore always Cancelled.
//  3. If Wake also reported success, that is consistent (it validly landed
//     WakePending before Cancel's own CAS caught up) — not a bug. If Wake
//     reported failure, it must be a genuine *pause.TransitionError (lost
//     the initial Sleeping CAS to Cancel outright), never a different,
//     unexplained error shape.
//
// TestCancel_WinsAgainstAlreadyInFlightWake below removes scheduling
// non-determinism entirely and forces the "wake already landed, THEN
// cancel arrives" interleaving explicitly, as an exact, non-racy
// companion proof.
func TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState(t *testing.T) {
	const attempts = 50
	for attempt := 0; attempt < attempts; attempt++ {
		store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)

		var wg sync.WaitGroup
		var cancelErr, wakeErr error
		start := make(chan struct{})

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = pause.Cancel(context.Background(), store, pause.CancelRequest{PauseID: "pause-1"})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, wakeErr = pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "pause-1"})
		}()
		close(start)
		wg.Wait()

		// Property 1: Cancel must always win eventually in this race —
		// Sleeping and WakePending both have a Cancel edge, so there is
		// no legitimate way for Cancel to lose against a bare Wake (as
		// opposed to a full Resume, which DOES eventually close the race
		// window — see TestCancel_CannotWinAfterResumeStarted).
		if cancelErr != nil {
			t.Fatalf("attempt %d: Cancel() unexpectedly failed racing only Wake (not a full Resume): %v", attempt, cancelErr)
		}

		// Property 2: the final durable status is always Cancelled.
		final, found, err := store.GetByID(context.Background(), "pause-1")
		if err != nil || !found {
			t.Fatalf("attempt %d: GetByID after race: found=%v err=%v", attempt, found, err)
		}
		if final.Status != domain.PauseCancelled {
			t.Fatalf("attempt %d: final Status = %q, want %q (cancel must win this race)", attempt, final.Status, domain.PauseCancelled)
		}

		// Property 3: Wake's own error, if any, must be a genuine
		// TransitionError, not a mystery failure.
		if wakeErr != nil {
			var terr *pause.TransitionError
			if !errors.As(wakeErr, &terr) {
				t.Fatalf("attempt %d: Wake() failed with %v (%T), want *pause.TransitionError", attempt, wakeErr, wakeErr)
			}
		}
	}
}

// TestCancel_WinsAgainstAlreadyInFlightWake forces the specific
// interleaving the required test's name literally describes: a wake job is
// already "in flight" (Wake has already been called and durably landed
// WakePending) at the moment Cancel arrives. Per statemachine.go,
// WakePending still has an EventCancel edge to Cancelled — cancel must
// still be able to win even after wake fired, right up until resume
// actually starts (Resuming -> Resumed has no further Cancel edge, by
// design: once resume has genuinely started, ADD §20.11's race window is
// over). This proves "cancel wins race with wake" for the case where wake
// nominally "got there first" but resume has not yet actually started.
func TestCancel_WinsAgainstAlreadyInFlightWake(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)

	wakeResult, err := pause.Wake(context.Background(), store, pause.WakeRequest{PauseID: "pause-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if wakeResult.Record.Status != domain.PauseWakePending {
		t.Fatalf("Wake left status %q, want %q", wakeResult.Record.Status, domain.PauseWakePending)
	}

	cancelResult, err := pause.Cancel(context.Background(), store, pause.CancelRequest{PauseID: "pause-1"})
	if err != nil {
		t.Fatalf("Cancel after in-flight wake: %v", err)
	}
	if cancelResult.Record.Status != domain.PauseCancelled {
		t.Fatalf("Cancel Status = %q, want %q", cancelResult.Record.Status, domain.PauseCancelled)
	}

	final, _, _ := store.GetByID(context.Background(), "pause-1")
	if final.Status != domain.PauseCancelled {
		t.Fatalf("durable Status = %q, want %q — cancel must win even after wake already fired", final.Status, domain.PauseCancelled)
	}
}

// TestCancel_CannotWinAfterResumeStarted proves the OTHER edge of ADD
// §20.11: cancel's race window closes once resume has actually started
// (Resuming has no EventCancel edge — statemachine.go) — a pause that has
// progressed to Resuming (the point after which the provider session
// resume/fork/bootstrap has actually begun, per EventResumeStarted's own
// doc comment) can no longer be cancelled at all. This is not a bug in
// "cancel wins" — ADD's race is specifically about cancel racing a WAKE,
// not about cancel being able to unwind an already-started resume, which
// would be a much stronger (and un-asked-for) guarantee.
func TestCancel_CannotWinAfterResumeStarted(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseResuming)
	_, err := pause.Cancel(context.Background(), store, pause.CancelRequest{PauseID: "pause-1"})
	var terr *pause.TransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TransitionError (no Cancel edge from Resuming)", err)
	}
}

// --- CompareAndSwapStatus: direct unit coverage -----------------------------

func TestMemStore_CompareAndSwapStatus_SucceedsWhenExpectedMatches(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)
	ok, found, err := store.CompareAndSwapStatus(context.Background(), "pause-1", domain.PauseSleeping, domain.PauseWakePending)
	if err != nil {
		t.Fatalf("CompareAndSwapStatus: %v", err)
	}
	if !found || !ok {
		t.Fatalf("found=%v ok=%v, want both true", found, ok)
	}
	rec, _, _ := store.GetByID(context.Background(), "pause-1")
	if rec.Status != domain.PauseWakePending {
		t.Fatalf("Status = %q, want %q", rec.Status, domain.PauseWakePending)
	}
}

func TestMemStore_CompareAndSwapStatus_FailsWhenExpectedStale(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseWakePending)
	ok, found, err := store.CompareAndSwapStatus(context.Background(), "pause-1", domain.PauseSleeping, domain.PauseWakePending)
	if err != nil {
		t.Fatalf("CompareAndSwapStatus: %v", err)
	}
	if !found {
		t.Fatalf("found = false, want true (record exists)")
	}
	if ok {
		t.Fatalf("ok = true, want false (expected status was stale)")
	}
	// Record must be untouched.
	rec, _, _ := store.GetByID(context.Background(), "pause-1")
	if rec.Status != domain.PauseWakePending {
		t.Fatalf("Status = %q, want unchanged %q", rec.Status, domain.PauseWakePending)
	}
}

func TestMemStore_CompareAndSwapStatus_UnknownIDReportsNotFound(t *testing.T) {
	store := pause.NewMemStore()
	ok, found, err := store.CompareAndSwapStatus(context.Background(), "does-not-exist", domain.PauseSleeping, domain.PauseWakePending)
	if err != nil {
		t.Fatalf("CompareAndSwapStatus: %v", err)
	}
	if found || ok {
		t.Fatalf("found=%v ok=%v, want both false for an unknown ID", found, ok)
	}
}

func TestMemStore_CompareAndSwapStatus_ConcurrentCallersSerializeCorrectly(t *testing.T) {
	// Direct proof at the store layer (independent of pause.Wake/Cancel's
	// own Apply-based semantics): many goroutines racing the exact same
	// CAS (same expected, same next) must yield exactly one winner.
	const attempts = 30
	for attempt := 0; attempt < attempts; attempt++ {
		store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)
		const workers = 25
		var wg sync.WaitGroup
		var wins atomic.Int64
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ok, found, err := store.CompareAndSwapStatus(context.Background(), "pause-1", domain.PauseSleeping, domain.PauseWakePending)
				if err != nil {
					t.Errorf("CompareAndSwapStatus: %v", err)
					return
				}
				if !found {
					t.Errorf("found = false, want true")
					return
				}
				if ok {
					wins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got := wins.Load(); got != 1 {
			t.Fatalf("attempt %d: wins = %d, want exactly 1", attempt, got)
		}
	}
}
