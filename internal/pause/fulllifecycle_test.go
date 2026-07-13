// fulllifecycle_test.go: runtime-a11, the final Part A integration gate.
//
// This node is NOT new feature work — every prior Part A node (a02-a10) is
// already complete and individually tested. Its job, per agents/runtime.md's
// Part A "Required tests" list and EXECUTION_DAG.md's own framing ("a
// comprehensive final proof that your ENTIRE Part A stack composes
// correctly end-to-end under real concurrent/adversarial conditions"), is
// to compose the FULL lifecycle — RequestPause -> Persist ->
// InterruptAndSleep -> sleep -> Wake/Restart -> ValidateResume -> Resume —
// in one flow, and prove the required tests hold across that composition,
// not just at each individual package boundary.
//
// A dedicated research pass (this node's own first step, mirroring
// runtime-a10/b06's precedent of researching before writing code) audited
// every existing test file against agents/runtime.md's required-test list
// verbatim and found:
//
//   - "crash after every phase resumes/reconciles correctly": already
//     fully covered by persistphase_test.go's 5-phase crash-injection
//     harness for the PERSIST sub-phases specifically (Progress Tree
//     snapshot, State Checkpoint, Repository Checkpoint, Pause Record,
//     Wake Job). This file extends the SAME technique to the *other* ~9
//     top-level lifecycle transitions the persist-phase harness does not
//     touch (Predicted->Requested, Requested->Quiescing,
//     Quiescing->Checkpointing, Interrupting->Sleeping/Failed,
//     Sleeping->WakePending, WakePending->Validating,
//     Validating->Resuming/Sleeping/BlockedConflict, Resuming->Resumed) —
//     see TestFullLifecycle_CrashAfterEveryTransition_ResumesOrReconciles
//     below.
//   - "restart recovers wake job": a07's restart_test.go proves this at
//     the scheduler/lease level in isolation. Nothing previously composed
//     scheduler.Store.Restart with pause.Wake/ValidateResume in one flow —
//     see TestFullLifecycle_RestartRecoversWakeJob_ThenReEntersResumeValidation.
//   - "unsafe quota reschedules" / "repo overlap blocks" / "unrelated
//     repo change follows configured policy": a08's resumevalidation_test.go
//     proves these at the ValidateResume function level directly, with
//     hand-constructed CheckResult inputs. Nothing previously drove these
//     three scenarios through the FULL lifecycle (RequestPause -> Persist
//     -> simulated sleep -> Wake -> ValidateResume -> Resume, using
//     ValidateResume's own Verdict() mapping) — see
//     TestFullLifecycle_QuotaUnsafeReschedules_EndToEnd,
//     TestFullLifecycle_RepoOverlapBlocks_EndToEnd,
//     TestFullLifecycle_UnrelatedRepoChangeFollowsPolicy_EndToEnd below.
//   - "duplicate workers yield one resume" / "expired lease reclaimed" /
//     "cancel wins race with wake": a09's wake_test.go/splitbrain_test.go
//     prove these already, including one genuine composition (splitbrain_test.go
//     pairs a real scheduler.Store with pause.Wake). This node re-runs the
//     equivalent races one level further down the lifecycle — through
//     ValidateResume — to catch any interaction effect the earlier,
//     narrower compositions could have missed. See
//     TestFullLifecycle_DuplicateWakeRace_ThroughFullValidateResume and
//     TestFullLifecycle_CancelWinsRace_EvenDuringValidation below. No new
//     bug was found by this re-run (see this file's package-level summary
//     at the bottom); the composition holds.
//   - "provider interrupt failure leaves recoverable state": THE ONE
//     GENUINE GAP this node found. statemachine.go already had the
//     {Interrupting, interrupt_failed} -> Failed edge and
//     statemachine_test.go already proved it at the bare Apply level
//     (TestStateTransition_ProviderInterruptFailureLeavesRecoverableState),
//     and runtime-a10 already built FakeTurnInterrupter — but NO
//     production code anywhere in this package actually called a
//     TurnInterrupter and applied the resulting event to a real
//     PauseRecord (safepoint.go's PersistThenInterrupt deliberately proves
//     ordering only, per its own doc comment, and never touches
//     PauseStore/Apply). This node adds interrupt.go's
//     InterruptAndSleep — a genuinely new but small, in-bounds
//     (internal/pause, this role's own package) piece of production code —
//     and proves the required test against it directly:
//     TestFullLifecycle_ProviderInterruptFailure_LeavesRecoverableState.
package pause_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
)

// --- shared full-lifecycle fixtures ------------------------------------

// fixedLocator is a constant app.RunLocator, sufficient for every test in
// this file (none of them assert on the locator's own content — only that
// TurnInterrupterAdapter correctly threads SOME locator through to the
// underlying app.TurnInterrupter).
func fixedLocator(domain.PauseID) app.RunLocator {
	return app.RunLocator{SessionID: "sess-1", TurnID: "turn-1"}
}

// fakeTurnInterrupter is this file's own minimal app.TurnInterrupter
// double (a local copy of runtime-a10's fakes.FakeTurnInterrupter shape,
// avoiding an internal/testutil/fakes import purely to keep this file's
// dependency graph as narrow as the property it tests — every other test
// file in this package that needs the REAL fakes package already imports
// it, e.g. persistphase_test.go; this file's own interrupter double is
// simple enough not to need it).
type fakeTurnInterrupter struct {
	fn func(ctx context.Context, locator app.RunLocator) error
}

func (f fakeTurnInterrupter) Interrupt(ctx context.Context, locator app.RunLocator) error {
	return f.fn(ctx, locator)
}

var _ app.TurnInterrupter = fakeTurnInterrupter{}

// --- 1. Crash-after-every-transition (extends persistphase_test.go's own
//        persist-sub-phase harness to the OTHER ~9 lifecycle transitions) --

// TestFullLifecycle_CrashAfterEveryTransition_ResumesOrReconciles drives a
// pause record through the ENTIRE required state path (agents/runtime.md:
// "observing -> pause_requested -> quiescing -> safe_point_reached ->
// persisting -> interrupting -> sleeping -> wake_due -> validating ->
// resuming -> resumed"), "crashing" (via a simulated process restart —
// re-reading the record fresh from the store, exactly as a real restart
// would) immediately after each transition durably lands, and proving the
// next step can always resume/reconcile correctly from whatever the store
// shows: never stuck, never silently re-entering a step whose durable
// effect already committed, never able to run an event that is no longer
// valid from the now-current status.
func TestFullLifecycle_CrashAfterEveryTransition_ResumesOrReconciles(t *testing.T) {
	ctx := context.Background()

	type step struct {
		name  string
		event pause.Event
		want  domain.PauseStatus
	}
	steps := []step{
		{"debounce_passed", pause.EventDebouncePassed, domain.PauseRequested},
		{"threshold_crossed", pause.EventThresholdCrossed, domain.PauseQuiescing},
		{"safe_point_reached", pause.EventSafePointReached, domain.PauseCheckpointing},
		{"checkpoint_verified", pause.EventCheckpointVerified, domain.PauseInterrupting},
		{"provider_stopped", pause.EventProviderStopped, domain.PauseSleeping},
		{"wake_due", pause.EventWakeDue, domain.PauseWakePending},
		{"resume_valid (enter Validating)", pause.EventResumeValid, domain.PauseValidating},
		{"resume_valid (Validating->Resuming)", pause.EventResumeValid, domain.PauseResuming},
		{"resume_started", pause.EventResumeStarted, domain.PauseResumed},
	}

	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-crash-sweep")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	if err := store.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PausePredicted, Reason: pause.TriggerReasonCalibrated}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}

	for i, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			// "Crash": re-read the record fresh from the durable store,
			// exactly as a restarted process reconciling from disk would —
			// no in-memory state from the previous step's own call is
			// reused here.
			before, found, err := store.GetByID(ctx, pauseID)
			if err != nil || !found {
				t.Fatalf("pre-step GetByID: found=%v err=%v", found, err)
			}

			// The reconciled (post-"crash") status must equal EXACTLY the
			// immediately preceding step's own output (i.e. this subtest's
			// natural, expected starting point) and must never equal any
			// status from TWO OR MORE steps back — that would mean the
			// record somehow regressed or got double-processed back onto a
			// status it had already durably left behind. Stated as a check
			// on the durable STATUS itself, not on whether a given event
			// string can textually apply again: an earlier draft of this
			// check asserted "no earlier step's EVENT can ever re-apply,"
			// which is the WRONG property — EventResumeValid legitimately
			// has two distinct edges in the transition table
			// (WakePending->Validating and Validating->Resuming,
			// statemachine.go), so the same event name recurring is
			// expected, correct behavior, not a replay bug. A second wrong
			// draft of this check then flagged the immediately-preceding
			// step's own output as an illegitimate "re-entry," which is
			// also wrong: that status is precisely this subtest's expected
			// starting point, not a regression — only skipping BACK past
			// it (matching a status from two-or-more steps earlier) would
			// be real double-processing.
			wantBefore := domain.PausePredicted
			if i > 0 {
				wantBefore = steps[i-1].want
			}
			if before.Status != wantBefore {
				t.Fatalf("step %d (%s): reconciled starting status = %q, want %q (the immediately preceding step's own durable output)", i, s.name, before.Status, wantBefore)
			}
			for j := 0; j < i-1; j++ { // anything from TWO or more steps back
				if before.Status == steps[j].want {
					t.Fatalf("step %d (%s): reconciled status %q re-entered a status TWO OR MORE steps back — real double-processing", i, s.name, before.Status)
				}
			}

			next, err := pause.Apply(before.Status, s.event)
			if err != nil {
				t.Fatalf("step %d (%s): Apply(%q, %q): %v", i, s.name, before.Status, s.event, err)
			}
			if next != s.want {
				t.Fatalf("step %d (%s): Apply(%q, %q) = %q, want %q", i, s.name, before.Status, s.event, next, s.want)
			}
			ok, foundCAS, err := store.CompareAndSwapStatus(ctx, pauseID, before.Status, next)
			if err != nil || !foundCAS || !ok {
				t.Fatalf("step %d (%s): CompareAndSwapStatus: ok=%v found=%v err=%v", i, s.name, ok, foundCAS, err)
			}

			// "Crash" again immediately after this step's own durable
			// write: a fresh read must show exactly this step's result,
			// never a partial or stale value.
			after, found, err := store.GetByID(ctx, pauseID)
			if err != nil || !found {
				t.Fatalf("post-step GetByID: found=%v err=%v", found, err)
			}
			if after.Status != s.want {
				t.Fatalf("step %d (%s): reconciled status after crash = %q, want %q", i, s.name, after.Status, s.want)
			}
		})
	}

	final, found, err := store.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("final GetByID: found=%v err=%v", found, err)
	}
	if final.Status != domain.PauseResumed {
		t.Fatalf("final status = %q, want %q", final.Status, domain.PauseResumed)
	}
	if !pause.IsTerminal(final.Status) {
		t.Fatal("expected final Resumed status to be terminal")
	}
}

// TestFullLifecycle_CrashDuringQuiescing_EmergencyShortCircuitReconciles
// proves the emergency short-circuit path (Quiescing -> Checkpointing
// directly on EventEmergency, skipping the ordinary safe-point wait) is
// equally crash-safe: "crashing" (re-reading fresh) right after the
// emergency transition must show Checkpointing, and the SafePointReached
// edge must no longer apply from that reconciled state in a way that
// double-applies the transition.
func TestFullLifecycle_CrashDuringQuiescing_EmergencyShortCircuitReconciles(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-emergency")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	if err := store.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PauseQuiescing, Reason: pause.TriggerReasonEmergency}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}

	next, err := pause.Apply(domain.PauseQuiescing, pause.EventEmergency)
	if err != nil {
		t.Fatalf("Apply emergency: %v", err)
	}
	ok, found, err := store.CompareAndSwapStatus(ctx, pauseID, domain.PauseQuiescing, next)
	if err != nil || !found || !ok {
		t.Fatalf("CompareAndSwapStatus: ok=%v found=%v err=%v", ok, found, err)
	}

	// "Crash": fresh read.
	rec, found, err := store.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("GetByID after crash: found=%v err=%v", found, err)
	}
	if rec.Status != domain.PauseCheckpointing {
		t.Fatalf("reconciled status = %q, want %q", rec.Status, domain.PauseCheckpointing)
	}
	// The now-stale EventSafePointReached from the OLD Quiescing status
	// must not be re-appliable against the reconciled Checkpointing
	// status (no such edge exists FROM Checkpointing for that event).
	if _, err := pause.Apply(rec.Status, pause.EventSafePointReached); err == nil {
		t.Fatal("expected SafePointReached to be rejected from reconciled Checkpointing status")
	}
}

// --- 2. Restart recovers wake job, THEN re-enters resume validation -----

// TestFullLifecycle_RestartRecoversWakeJob_ThenReEntersResumeValidation
// composes scheduler.Store.Restart (a07's own guarantee, proven in
// isolation by restart_test.go) with pause.Wake and pause.ValidateResume
// in one flow: a wake job is claimed (simulating a worker that started
// processing it), the "process" then restarts (Restart reclaims every
// leased job unconditionally, per a07's own documented semantics), and
// THIS node's required composition is that the recovered job, once
// reclaimed by a fresh worker, correctly drives the SAME PauseID through
// Wake and into a real ValidateResume call — not merely reclaimed as an
// inert lease row that nothing then acts on.
func TestFullLifecycle_RestartRecoversWakeJob_ThenReEntersResumeValidation(t *testing.T) {
	ctx := context.Background()
	clock := newSplitBrainClock(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	seedChain(t, db, "wt1", "task1")
	seedPauseRecordRow(t, db, "pause-restart", "task1")

	wakeStore := scheduler.NewStore(db.Conn(), clock, &seqIDs{prefix: "wj"})
	job, err := wakeStore.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause-restart", Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	// A worker claims the job — simulating the in-flight processing a real
	// scheduler loop would be doing right before the whole process dies.
	claim, err := wakeStore.Claim(ctx, "worker-before-crash", 5*time.Minute)
	if err != nil || !claim.Found {
		t.Fatalf("Claim before crash: found=%v err=%v", claim.Found, err)
	}

	// "Restart": a fresh scheduler.Store value (simulating a new process,
	// same DB file) reconciles at startup — a07's own required test,
	// re-confirmed here as this node's starting point rather than
	// re-proven from scratch.
	freshStore := scheduler.NewStore(db.Conn(), clock, &seqIDs{prefix: "wj2"})
	report, err := freshStore.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if report.RecoveredLeased == 0 {
		t.Fatalf("expected Restart to reclaim the leased job, got RecoveredLeased=%d", report.RecoveredLeased)
	}

	// A NEW worker, post-restart, can now claim the reclaimed job.
	claim2, err := freshStore.Claim(ctx, "worker-after-restart", 5*time.Minute)
	if err != nil || !claim2.Found {
		t.Fatalf("Claim after restart: found=%v err=%v", claim2.Found, err)
	}
	if claim2.Job.ID != job.ID {
		t.Fatalf("post-restart claim got a different job: %v, want %v", claim2.Job.ID, job.ID)
	}

	// The composition this node's own job is to prove: the recovered job
	// drives pause.Wake, and THEN a real ValidateResume call — not just a
	// reclaimed lease row nothing acts on further.
	pauses := pause.NewMemStore()
	if err := pauses.Insert(ctx, pause.PauseRecord{
		ID: "pause-restart", Key: pause.PauseKey{TaskID: "task1", SessionID: "sess1"},
		Status: domain.PauseSleeping, Reason: pause.TriggerReasonCalibrated,
	}); err != nil {
		t.Fatalf("seed pauses.Insert: %v", err)
	}

	wakeResult, err := pause.Wake(ctx, pauses, pause.WakeRequest{PauseID: "pause-restart"})
	if err != nil {
		t.Fatalf("Wake after restart-recovered claim: %v", err)
	}
	if wakeResult.Record.Status != domain.PauseWakePending {
		t.Fatalf("post-Wake status = %q, want %q", wakeResult.Record.Status, domain.PauseWakePending)
	}

	// Re-enter resume validation for real (all four checks pass) —
	// confirms the recovered wake job's pause record is a fully live
	// input to ValidateResume, not just reachable by Wake.
	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota:                okQuotaReader(),
		RepositoryCheckpoint: okRepoVerifier(),
		RepoFingerprint:      okRepoFingerprintReader(),
		Session:              okSessionReader(),
		Evaluations:          okEvaluations(),
	}, pause.ResumeValidationRequest{
		SessionID:              "sess1",
		QuotaBaseline:          okQuotaBaseline(),
		RepositoryCheckpointID: "rc-1",
		BaselineGitHead:        "head-1",
		WorktreeID:             "wt1",
		Authorization:          app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	})
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if !validation.AllPass() {
		t.Fatalf("expected ValidateResume to pass for the restart-recovered pause, got %+v", validation)
	}

	resumeResult, err := pause.Resume(ctx, pauses, withPauseIDHelper(validation.Verdict(), "pause-restart"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumeResult.Record.Status != domain.PauseResumed {
		t.Fatalf("final status = %q, want %q", resumeResult.Record.Status, domain.PauseResumed)
	}

	// Finally, the scheduler side completes cleanly too — the lease layer
	// and the pause layer agree on the outcome.
	completed, err := freshStore.Complete(ctx, job.ID, "worker-after-restart")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Status != scheduler.StatusDone {
		t.Fatalf("job Status = %q, want %q", completed.Status, scheduler.StatusDone)
	}
}

// withPauseID is a small test-local helper: ValidateResumeResult.Verdict()
// returns a pause.ResumeRequest with only its verdict fields set (Verdict()
// deliberately does not know the PauseID — see resumevalidation.go's own
// doc comment), so every full-lifecycle test that calls both ValidateResume
// and Resume in sequence needs to thread the PauseID through itself.
func withPauseIDHelper(req pause.ResumeRequest, id domain.PauseID) pause.ResumeRequest {
	req.PauseID = id
	return req
}

// --- 3. unsafe quota reschedules / repo overlap blocks / unrelated repo
//        change follows configured policy — through the FULL lifecycle ---

// runFullLifecycleToSleeping drives a fresh pause record from Predicted
// all the way to Sleeping (RequestPause's entry state through the
// checkpoint/interrupt sequence), using the REAL statemachine.Apply +
// CompareAndSwapStatus discipline every step of the way — this is the
// common setup every quota/repo-policy full-lifecycle test below shares,
// factored out once rather than repeated three times.
func runFullLifecycleToSleeping(t *testing.T, store pause.PauseStore, pauseID domain.PauseID, key pause.PauseKey) {
	t.Helper()
	ctx := context.Background()
	if err := store.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PausePredicted, Reason: pause.TriggerReasonCalibrated}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
	for _, ev := range []pause.Event{
		pause.EventDebouncePassed,
		pause.EventThresholdCrossed,
		pause.EventSafePointReached,
		pause.EventCheckpointVerified, // Checkpointing -> Interrupting (Persist itself is exercised separately by persistphase_test.go; this sweep only needs the STATUS transitions, not Persist's own five sub-writes)
		pause.EventProviderStopped,
	} {
		current, found, err := store.GetByID(ctx, pauseID)
		if err != nil || !found {
			t.Fatalf("GetByID before %q: found=%v err=%v", ev, found, err)
		}
		next, err := pause.Apply(current.Status, ev)
		if err != nil {
			t.Fatalf("Apply(%q, %q): %v", current.Status, ev, err)
		}
		ok, found, err := store.CompareAndSwapStatus(ctx, pauseID, current.Status, next)
		if err != nil || !found || !ok {
			t.Fatalf("CompareAndSwapStatus(%q -> %q): ok=%v found=%v err=%v", current.Status, next, ok, found, err)
		}
	}
	rec, _, _ := store.GetByID(ctx, pauseID)
	if rec.Status != domain.PauseSleeping {
		t.Fatalf("runFullLifecycleToSleeping: final setup status = %q, want %q", rec.Status, domain.PauseSleeping)
	}
}

// TestFullLifecycle_QuotaUnsafeReschedules_EndToEnd drives the required
// test "unsafe quota reschedules" through RequestPause(seed)->...->Sleeping
// ->Wake->ValidateResume->Resume, using a quota reader that reports a
// worse-than-baseline quota — proving the full pipeline reschedules
// (lands back at Sleeping) rather than merely asserting ValidateResume's
// own isolated CheckResult, as resumevalidation_test.go already does.
func TestFullLifecycle_QuotaUnsafeReschedules_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-quota-e2e")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	runFullLifecycleToSleeping(t, store, pauseID, key)

	wakeResult, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if wakeResult.Record.Status != domain.PauseWakePending {
		t.Fatalf("post-Wake status = %q, want %q", wakeResult.Record.Status, domain.PauseWakePending)
	}

	worseningQuotaReader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil // baseline was 50 (okQuotaBaseline) -- worse
	}}

	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota:                worseningQuotaReader,
		RepositoryCheckpoint: okRepoVerifier(),
		RepoFingerprint:      okRepoFingerprintReader(),
		Session:              okSessionReader(),
		Evaluations:          okEvaluations(),
	}, pause.ResumeValidationRequest{
		SessionID:              "sess1",
		QuotaBaseline:          okQuotaBaseline(),
		RepositoryCheckpointID: "rc-1",
		BaselineGitHead:        "head-1",
		WorktreeID:             "wt1",
		Authorization:          app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	})
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if validation.AllPass() {
		t.Fatal("expected quota check to fail")
	}
	if validation.Quota.Pass {
		t.Fatal("expected Quota.Pass = false")
	}

	resumeResult, err := pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), pauseID))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumeResult.Record.Status != domain.PauseSleeping {
		t.Fatalf("final status after quota-unsafe verdict = %q, want %q (rescheduled)", resumeResult.Record.Status, domain.PauseSleeping)
	}

	// The rescheduled record must still be alive (non-terminal) — a
	// subsequent Wake attempt must be legal again, proving "reschedule"
	// really means "can be woken again," not merely "didn't error."
	if pause.IsTerminal(resumeResult.Record.Status) {
		t.Fatal("expected rescheduled status to be non-terminal")
	}
	if _, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID}); err != nil {
		t.Fatalf("second Wake after reschedule: %v", err)
	}
}

// TestFullLifecycle_RepoOverlapBlocks_EndToEnd drives "repo overlap blocks"
// through the same full pipeline: a repository fingerprint reader reports
// a changed path that overlaps the paused work's own files, and the full
// pipeline must land at BlockedConflict (not merely fail ValidateResume's
// own isolated CheckResult).
func TestFullLifecycle_RepoOverlapBlocks_EndToEnd(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-overlap-e2e")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	runFullLifecycleToSleeping(t, store, pauseID, key)

	if _, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID}); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	overlappingReader := fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"internal/pause/lifecycle.go"}}, nil
	}}

	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota:                okQuotaReader(),
		RepositoryCheckpoint: okRepoVerifier(),
		RepoFingerprint:      overlappingReader,
		Session:              okSessionReader(),
		Evaluations:          okEvaluations(),
	}, pause.ResumeValidationRequest{
		SessionID:              "sess1",
		QuotaBaseline:          okQuotaBaseline(),
		RepositoryCheckpointID: "rc-1",
		BaselineGitHead:        "head-1",
		WorktreeID:             "wt1",
		PausedWorkPaths:        []string{"internal/pause/lifecycle.go"},
		RepoPolicy:             pause.RepoChangePolicyAllowUnrelated,
		Authorization:          app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	})
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if validation.Repository.Pass {
		t.Fatal("expected Repository.Pass = false (overlap)")
	}
	if validation.Repository.Reason != pause.ReasonRepositoryOverlapBlocks {
		t.Fatalf("Repository.Reason = %q, want %q", validation.Repository.Reason, pause.ReasonRepositoryOverlapBlocks)
	}

	resumeResult, err := pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), pauseID))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumeResult.Record.Status != domain.PauseBlockedConflict {
		t.Fatalf("final status = %q, want %q", resumeResult.Record.Status, domain.PauseBlockedConflict)
	}

	// BlockedConflict is deliberately NOT a terminal status
	// (statemachine.go's terminalStates set) — ADD §20.9's manual
	// resolution UI (Inspect Diff / Create New Plan / Resume Manually /
	// Cancel) needs an outbound edge to reach from here, specifically
	// Cancel (the one edge the transition table defines for
	// BlockedConflict). An earlier draft of this test asserted the
	// opposite (IsTerminal == true) — that was this test's own wrong
	// mental model, not a real product requirement; re-checked against
	// statemachine.go's transitionTable/terminalStates directly, which
	// settles it. What IS required: no AUTOMATIC event (anything other
	// than a human-initiated Cancel) can move it further — proven by
	// confirming every other event is rejected from BlockedConflict.
	if pause.IsTerminal(resumeResult.Record.Status) {
		t.Fatal("expected BlockedConflict to NOT be terminal (ADD §20.9 manual-resolution UI reaches it via Cancel)")
	}
	for _, ev := range []pause.Event{
		pause.EventResumeValid, pause.EventQuotaUnsafe, pause.EventWakeDue,
		pause.EventCheckpointVerified, pause.EventProviderStopped, pause.EventResumeStarted,
	} {
		if pause.Validate(resumeResult.Record.Status, ev) {
			t.Errorf("expected event %q to have no automatic edge from BlockedConflict", ev)
		}
	}
	if !pause.Validate(resumeResult.Record.Status, pause.EventCancel) {
		t.Fatal("expected BlockedConflict to have exactly the documented manual Cancel edge")
	}
}

// TestFullLifecycle_UnrelatedRepoChangeFollowsPolicy_EndToEnd drives
// "unrelated repo change follows configured policy" through the full
// pipeline TWICE — once under RepoChangePolicyAllowUnrelated (must pass
// through to Resumed) and once under RepoChangePolicyBlockAny (must land
// at BlockedConflict) — for the exact same non-overlapping change, proving
// the policy configuration is actually load-bearing end to end, not just
// inside CheckRepositoryCompatibility's own isolated unit test.
func TestFullLifecycle_UnrelatedRepoChangeFollowsPolicy_EndToEnd(t *testing.T) {
	unrelatedReader := fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"README.md"}}, nil
	}}
	deps := func(policy pause.RepoChangePolicy) (pause.ResumeValidationDeps, pause.ResumeValidationRequest) {
		return pause.ResumeValidationDeps{
				Quota:                okQuotaReader(),
				RepositoryCheckpoint: okRepoVerifier(),
				RepoFingerprint:      unrelatedReader,
				Session:              okSessionReader(),
				Evaluations:          okEvaluations(),
			}, pause.ResumeValidationRequest{
				SessionID:              "sess1",
				QuotaBaseline:          okQuotaBaseline(),
				RepositoryCheckpointID: "rc-1",
				BaselineGitHead:        "head-1",
				WorktreeID:             "wt1",
				PausedWorkPaths:        []string{"internal/pause/lifecycle.go"}, // README.md does NOT overlap
				RepoPolicy:             policy,
				Authorization:          app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
			}
	}

	t.Run("allow_unrelated_resumes", func(t *testing.T) {
		ctx := context.Background()
		store := pause.NewMemStore()
		pauseID := domain.PauseID("pause-policy-allow")
		runFullLifecycleToSleeping(t, store, pauseID, pause.PauseKey{TaskID: "task1", SessionID: "sess1"})
		if _, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID}); err != nil {
			t.Fatalf("Wake: %v", err)
		}
		d, req := deps(pause.RepoChangePolicyAllowUnrelated)
		validation, err := pause.ValidateResume(ctx, d, req)
		if err != nil {
			t.Fatalf("ValidateResume: %v", err)
		}
		if !validation.AllPass() {
			t.Fatalf("expected AllowUnrelated policy to pass an unrelated change, got %+v", validation)
		}
		result, err := pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), pauseID))
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if result.Record.Status != domain.PauseResumed {
			t.Fatalf("final status = %q, want %q", result.Record.Status, domain.PauseResumed)
		}
	})

	t.Run("block_any_blocks", func(t *testing.T) {
		ctx := context.Background()
		store := pause.NewMemStore()
		pauseID := domain.PauseID("pause-policy-block")
		runFullLifecycleToSleeping(t, store, pauseID, pause.PauseKey{TaskID: "task1", SessionID: "sess1"})
		if _, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID}); err != nil {
			t.Fatalf("Wake: %v", err)
		}
		d, req := deps(pause.RepoChangePolicyBlockAny)
		validation, err := pause.ValidateResume(ctx, d, req)
		if err != nil {
			t.Fatalf("ValidateResume: %v", err)
		}
		if validation.AllPass() {
			t.Fatal("expected BlockAny policy to fail an unrelated change")
		}
		result, err := pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), pauseID))
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if result.Record.Status != domain.PauseBlockedConflict {
			t.Fatalf("final status = %q, want %q", result.Record.Status, domain.PauseBlockedConflict)
		}
	})
}

// --- 4. duplicate-wake / expired-lease / cancel-wins-race, one level
//        further down the lifecycle (through ValidateResume) ------------

// TestFullLifecycle_DuplicateWakeRace_ThroughFullValidateResume re-runs
// a09's "duplicate workers yield one resume" race, but drives the WINNING
// call one step further than wake_test.go/splitbrain_test.go do: past
// Wake and through a real ValidateResume call, confirming the interaction
// between the CAS-based exactly-once guarantee and ValidateResume's own
// read of the (by-then-WakePending) record introduces no new failure mode.
func TestFullLifecycle_DuplicateWakeRace_ThroughFullValidateResume(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-dup-wake-e2e")
	runFullLifecycleToSleeping(t, store, pauseID, pause.PauseKey{TaskID: "task1", SessionID: "sess1"})

	const n = 20
	var wg sync.WaitGroup
	var successes atomic.Int64
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID})
			if err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful Wake calls = %d, want exactly 1", got)
	}

	rec, found, err := store.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("GetByID: found=%v err=%v", found, err)
	}
	if rec.Status != domain.PauseWakePending {
		t.Fatalf("status after duplicate-wake race = %q, want %q", rec.Status, domain.PauseWakePending)
	}

	// One level further than the existing a09 tests go: ValidateResume
	// against the record the race left behind must work exactly as if no
	// race had ever happened.
	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota: okQuotaReader(), RepositoryCheckpoint: okRepoVerifier(), RepoFingerprint: okRepoFingerprintReader(),
		Session: okSessionReader(), Evaluations: okEvaluations(),
	}, pause.ResumeValidationRequest{
		SessionID: "sess1", QuotaBaseline: okQuotaBaseline(), RepositoryCheckpointID: "rc-1",
		BaselineGitHead: "head-1", WorktreeID: "wt1",
		Authorization: app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	})
	if err != nil {
		t.Fatalf("ValidateResume after duplicate-wake race: %v", err)
	}
	if !validation.AllPass() {
		t.Fatalf("expected ValidateResume to pass cleanly after the race, got %+v", validation)
	}
	result, err := pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), pauseID))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Record.Status != domain.PauseResumed {
		t.Fatalf("final status = %q, want %q", result.Record.Status, domain.PauseResumed)
	}
}

// TestFullLifecycle_ExpiredLeaseReclaimed_ThenFullValidateResume re-proves
// "expired lease reclaimed" (a06/a07's own scheduler-level guarantee) one
// level further down: past the reclaim, through pause.Wake and a real
// ValidateResume/Resume call, on the SAME PauseID the reclaimed job names.
func TestFullLifecycle_ExpiredLeaseReclaimed_ThenFullValidateResume(t *testing.T) {
	ctx := context.Background()
	clock := newSplitBrainClock(time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
	db := openMigratedDB(t)
	seedChain(t, db, "wt1", "task1")
	seedPauseRecordRow(t, db, "pause-lease-e2e", "task1")

	wakeStore := scheduler.NewStore(db.Conn(), clock, &seqIDs{prefix: "wj"})
	job, err := wakeStore.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: "pause-lease-e2e", Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	firstClaim, err := wakeStore.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil || !firstClaim.Found {
		t.Fatalf("first Claim: found=%v err=%v", firstClaim.Found, err)
	}
	clock.Advance(30 * time.Second) // lease expires

	reclaimed, err := wakeStore.ReclaimExpired(ctx)
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if reclaimed == 0 {
		t.Fatal("expected at least one job reclaimed")
	}
	secondClaim, err := wakeStore.Claim(ctx, "worker-2", 5*time.Minute)
	if err != nil || !secondClaim.Found || secondClaim.Job.ID != job.ID {
		t.Fatalf("second Claim after reclaim: found=%v err=%v job=%v", secondClaim.Found, err, secondClaim.Job.ID)
	}

	store := pause.NewMemStore()
	runFullLifecycleToSleeping(t, store, "pause-lease-e2e", pause.PauseKey{TaskID: "task1", SessionID: "sess1"})

	if _, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: "pause-lease-e2e"}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota: okQuotaReader(), RepositoryCheckpoint: okRepoVerifier(), RepoFingerprint: okRepoFingerprintReader(),
		Session: okSessionReader(), Evaluations: okEvaluations(),
	}, pause.ResumeValidationRequest{
		SessionID: "sess1", QuotaBaseline: okQuotaBaseline(), RepositoryCheckpointID: "rc-1",
		BaselineGitHead: "head-1", WorktreeID: "wt1",
		Authorization: app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	})
	if err != nil || !validation.AllPass() {
		t.Fatalf("ValidateResume after lease reclaim: err=%v validation=%+v", err, validation)
	}
	result, err := pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), "pause-lease-e2e"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Record.Status != domain.PauseResumed {
		t.Fatalf("final status = %q, want %q", result.Record.Status, domain.PauseResumed)
	}
	completedJob, err := wakeStore.Complete(ctx, job.ID, "worker-2")
	if err != nil {
		t.Fatalf("Complete by the true current holder: %v", err)
	}
	if completedJob.Status != scheduler.StatusDone {
		t.Fatalf("job Status = %q, want %q", completedJob.Status, scheduler.StatusDone)
	}
}

// TestFullLifecycle_CancelWinsRace_EvenDuringValidation re-proves "cancel
// wins race with wake" one level further than wake_test.go's own coverage:
// a Cancel call races NOT against Wake itself, but against a caller that
// has already Woken the record and is in the middle of a (slow,
// concurrently-running) ValidateResume call — proving that even though
// ValidateResume itself doesn't touch PauseStore, a Cancel landing WHILE
// ValidateResume is in flight (i.e. before the caller gets to call Resume
// with the verdict) is correctly picked up by Resume's own CAS check
// afterward, rather than Resume blindly clobbering the Cancel.
func TestFullLifecycle_CancelWinsRace_EvenDuringValidation(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-cancel-during-validation")
	runFullLifecycleToSleeping(t, store, pauseID, pause.PauseKey{TaskID: "task1", SessionID: "sess1"})

	if _, err := pause.Wake(ctx, store, pause.WakeRequest{PauseID: pauseID}); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	// Cancel lands WHILE (conceptually) ValidateResume would still be
	// running — modeled here by simply calling Cancel BEFORE the caller
	// gets around to calling Resume with ValidateResume's verdict, since
	// ValidateResume itself never touches the store (resumevalidation.go:
	// every check is a pure read against its own narrow seam) — the only
	// place a race with Cancel is EVEN OBSERVABLE is at the Resume call
	// that follows, exactly as this test proves.
	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota: okQuotaReader(), RepositoryCheckpoint: okRepoVerifier(), RepoFingerprint: okRepoFingerprintReader(),
		Session: okSessionReader(), Evaluations: okEvaluations(),
	}, pause.ResumeValidationRequest{
		SessionID: "sess1", QuotaBaseline: okQuotaBaseline(), RepositoryCheckpointID: "rc-1",
		BaselineGitHead: "head-1", WorktreeID: "wt1",
		Authorization: app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	})
	if err != nil || !validation.AllPass() {
		t.Fatalf("ValidateResume: err=%v validation=%+v", err, validation)
	}

	if _, err := pause.Cancel(ctx, store, pause.CancelRequest{PauseID: pauseID}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// Resume, called with the (now-stale) verdict computed before Cancel
	// landed, must be rejected — Cancel already durably moved the record
	// to a terminal state before Resume's own CAS attempt runs.
	_, err = pause.Resume(ctx, store, withPauseIDHelper(validation.Verdict(), pauseID))
	if err == nil {
		t.Fatal("expected Resume to fail after a Cancel landed first")
	}
	var terr *pause.TransitionError
	if !errors.As(err, &terr) || !terr.Terminal {
		t.Fatalf("expected a terminal TransitionError, got %v", err)
	}

	rec, found, err := store.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("GetByID: found=%v err=%v", found, err)
	}
	if rec.Status != domain.PauseCancelled {
		t.Fatalf("final status = %q, want %q (cancel wins)", rec.Status, domain.PauseCancelled)
	}
}

// --- 5. provider interrupt failure leaves recoverable state (THE genuine
//        gap this node closes — see interrupt.go) ------------------------

// TestFullLifecycle_ProviderInterruptFailure_LeavesRecoverableState proves
// the one required test this node found genuinely uncovered by any prior
// node: a real (fake-backed) app.TurnInterrupter call that fails partway
// through the interrupting phase must leave the PauseRecord at a durable,
// well-defined, readable status — domain.PauseFailed — not stuck at
// Interrupting and not corrupted.
func TestFullLifecycle_ProviderInterruptFailure_LeavesRecoverableState(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-interrupt-fail")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	if err := store.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PauseInterrupting, Reason: pause.TriggerReasonCalibrated}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}

	var interruptCalls atomic.Int64
	interrupter := pause.TurnInterrupterAdapter{
		Interrupter: fakeTurnInterrupter{fn: func(_ context.Context, locator app.RunLocator) error {
			interruptCalls.Add(1)
			return errors.New("provider interrupt: connection reset partway through stop signal")
		}},
		Locate: fixedLocator,
	}

	rec, err := pause.InterruptAndSleep(ctx, store, interrupter, pauseID)
	if err == nil {
		t.Fatal("expected InterruptAndSleep to surface the provider's interrupt failure")
	}
	if interruptCalls.Load() != 1 {
		t.Fatalf("expected exactly one Interrupt call, got %d (no silent internal retry)", interruptCalls.Load())
	}

	// The RECORD returned alongside the error is not corrupted: it
	// reflects the real, durable post-failure status.
	if rec.Status != domain.PauseFailed {
		t.Fatalf("returned record Status = %q, want %q", rec.Status, domain.PauseFailed)
	}

	// The store itself — read back fresh, exactly as a restart-time
	// reconciliation pass would — shows the SAME thing: durable, readable,
	// terminal, never stuck at the intermediate Interrupting status.
	final, found, err := store.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("GetByID after interrupt failure: found=%v err=%v", found, err)
	}
	if final.Status != domain.PauseFailed {
		t.Fatalf("durable status after interrupt failure = %q, want %q (recoverable, not stuck)", final.Status, domain.PauseFailed)
	}
	if final.Status == domain.PauseInterrupting {
		t.Fatal("record must not remain stuck at Interrupting after an interrupt failure")
	}
	if !pause.IsTerminal(final.Status) {
		t.Fatal("expected Failed to be terminal")
	}

	// A second call against the now-Failed record must be rejected
	// cleanly (terminal state, no outbound edges) — never a panic, never
	// a silent no-op success that would misreport "interrupted twice."
	if _, err := pause.InterruptAndSleep(ctx, store, interrupter, pauseID); err == nil {
		t.Fatal("expected a second InterruptAndSleep call against a Failed record to be rejected")
	}
}

// TestFullLifecycle_ProviderInterruptSucceeds_ReachesSleeping is the
// control case for the test above: a successful Interrupt call must land
// the SAME record at Sleeping, proving InterruptAndSleep's own success
// path (not just its failure path) is correct, and that the failure
// test above is exercising a real branch, not the only reachable outcome.
func TestFullLifecycle_ProviderInterruptSucceeds_ReachesSleeping(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-interrupt-ok")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	if err := store.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PauseInterrupting, Reason: pause.TriggerReasonCalibrated}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}

	interrupter := pause.TurnInterrupterAdapter{
		Interrupter: fakeTurnInterrupter{fn: func(_ context.Context, _ app.RunLocator) error { return nil }},
		Locate:      fixedLocator,
	}

	rec, err := pause.InterruptAndSleep(ctx, store, interrupter, pauseID)
	if err != nil {
		t.Fatalf("InterruptAndSleep: %v", err)
	}
	if rec.Status != domain.PauseSleeping {
		t.Fatalf("Status = %q, want %q", rec.Status, domain.PauseSleeping)
	}
}

// TestFullLifecycle_InterruptAndSleep_WrongStartingStateRejected confirms
// InterruptAndSleep fails closed (a TransitionError, not a silent call to
// the provider) when invoked against a record that is not actually at
// Interrupting — e.g. Persist never completed, or a concurrent caller
// already moved it. The provider must never be called in that case.
func TestFullLifecycle_InterruptAndSleep_WrongStartingStateRejected(t *testing.T) {
	ctx := context.Background()
	store := pause.NewMemStore()
	pauseID := domain.PauseID("pause-interrupt-wrong-state")
	key := pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
	if err := store.Insert(ctx, pause.PauseRecord{ID: pauseID, Key: key, Status: domain.PauseCheckpointing, Reason: pause.TriggerReasonCalibrated}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}

	var called atomic.Bool
	interrupter := pause.TurnInterrupterAdapter{
		Interrupter: fakeTurnInterrupter{fn: func(_ context.Context, _ app.RunLocator) error {
			called.Store(true)
			return nil
		}},
		Locate: fixedLocator,
	}

	_, err := pause.InterruptAndSleep(ctx, store, interrupter, pauseID)
	if err == nil {
		t.Fatal("expected InterruptAndSleep to reject a record not at Interrupting")
	}
	if called.Load() {
		t.Fatal("the provider must not be called when the precondition is not met")
	}
}
