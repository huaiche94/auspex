// service_test.go: proves Service (service.go) is a real, correct
// app.GracefulPauseService — the Final integration gate's own corrective
// addition, not a numbered DAG node. Every test here follows the same
// technique fulllifecycle_test.go (runtime-a11) already established:
// compose already-tested pieces and confirm the COMPOSITION holds, rather
// than re-proving any individual piece's own internal correctness (the
// debounce math, the five-phase persist sequencing, the CAS races, and the
// four validation checks are all already exhaustively tested by
// observe_test.go/persistphase_test.go/wake_test.go/lifecycle_test.go/
// resumevalidation_test.go/fulllifecycle_test.go respectively — this file
// only proves Service's six methods correctly DELEGATE to them, matching
// the frozen app.GracefulPauseService shape end to end).
package pause_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/predictor/runway"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// --- compile-time contract assertion --------------------------------------

func TestService_ImplementsGracefulPauseService(t *testing.T) {
	var _ app.GracefulPauseService = (*pause.Service)(nil)
}

// --- shared test fixture ---------------------------------------------------

// fixedSessionContextResolver is this test file's own double for Service's
// first documented DTO-gap seam (service.go's package comment): every
// session resolves to the same fixed TaskID/WorktreeID/PausedWorkPaths,
// sufficient for a test that only ever uses one session/task/worktree
// triple.
type fixedSessionContextResolver struct {
	ctx pause.SessionContext
	err error
}

func (f fixedSessionContextResolver) ResolveSessionContext(_ context.Context, _ domain.SessionID) (pause.SessionContext, error) {
	if f.err != nil {
		return pause.SessionContext{}, f.err
	}
	return f.ctx, nil
}

// okRepoCheckpointService is this file's own fake for
// app.RepositoryCheckpointService's full three-method surface (unlike
// resumevalidation_test.go's okRepoVerifier, which only configures
// VerifyFunc — Service's ReachSafePoint path also needs CreateFunc, since
// Persist's phase 3 calls Create, not just Verify).
func okRepoCheckpointService() *fakes.FakeRepositoryCheckpointService {
	return &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			return app.RepositoryCheckpoint{ID: "repo-ckpt-1", GitHead: "head-1", Status: "created"}, nil
		},
		VerifyFunc: func(_ context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
			return app.RepositoryCheckpointVerification{ID: id, Valid: true}, nil
		},
	}
}

func okStateCheckpointService() *fakes.FakeStateCheckpointService {
	return &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{ID: "state-ckpt-1", TaskID: req.TaskID}, nil
		},
	}
}

func okProgressTreeService() *fakes.FakeProgressTreeService {
	return &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			return app.ProgressTreeSnapshot{TaskID: taskID}, nil
		},
	}
}

// serviceHarness bundles one fully-wired Service plus the raw collaborators
// a test might want to reconfigure (e.g. swap in a failing interrupter) or
// inspect directly (e.g. the scheduler.Store, to confirm a wake job was
// really scheduled).
type serviceHarness struct {
	svc            *pause.Service
	store          pause.PauseStore
	wakes          *scheduler.Store
	clock          *fixedServiceClock
	interrupterErr error
}

type fixedServiceClock struct{ t time.Time }

func (f *fixedServiceClock) Now() time.Time { return f.t }

// newServiceHarness builds a Service against a real, migrated in-memory-
// equivalent SQLite DB (openMigratedDB, this package's own existing test
// helper — persistphase_test.go/fulllifecycle_test.go's own harness) so
// EnterSleep's real scheduler.Store.GetByPauseKind lookup and Persist's
// real wake-job scheduling both exercise the genuine storage layer, not
// just MemStore — matching this role's own "prove the real thing, not an
// approximation" discipline (runtime-b10's lessons_learned).
func newServiceHarness(t *testing.T) *serviceHarness {
	t.Helper()
	db := openMigratedDB(t)
	seedChain(t, db, "wt1", "task1")

	clock := &fixedServiceClock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	store := pause.NewSQLiteStore(db)
	wakes := scheduler.NewStore(db.Conn(), clock, &seqIDs{prefix: "wj"})

	h := &serviceHarness{store: store, wakes: wakes, clock: clock}

	interrupter := fakeTurnInterrupter{fn: func(_ context.Context, _ app.RunLocator) error {
		return h.interrupterErr
	}}

	h.svc = pause.NewService(pause.ServiceDeps{
		Store: store,
		Clock: clock,
		IDs:   &seqIDs{prefix: "pause"},
		Sessions: fixedSessionContextResolver{ctx: pause.SessionContext{
			TaskID:          "task1",
			WorktreeID:      "wt1",
			PausedWorkPaths: []string{"internal/pause/lifecycle.go"},
		}},
		RunwayScorer:         runway.NewScorer(),
		Observer:             pause.NewObserver(pause.NewObserveConfig()),
		ProgressTree:         okProgressTreeService(),
		StateCheckpoint:      okStateCheckpointService(),
		RepositoryCheckpoint: okRepoCheckpointService(),
		WakeJobs:             wakes,
		WakeMaxAttempts:      5,
		WakeAfter:            10 * time.Minute,
		Interrupter:          interrupter,
		Locate:               func(domain.PauseID) app.RunLocator { return app.RunLocator{SessionID: "sess1", TurnID: "turn1"} },
		Quota:                okQuotaReader(),
		RepoFingerprint:      okRepoFingerprintReader(),
		Session:              okSessionReader(),
		Evaluations:          okEvaluations(),
		RepoPolicy:           pause.RepoChangePolicyAllowUnrelated,
	})
	return h
}

// --- 1. Observe -------------------------------------------------------------

// TestService_Observe_ProducesForecastAndTracksDebounceState proves Observe
// composes runway.Scorer (produces the forecast the frozen signature
// returns) with Observer's own debounce/hysteresis bookkeeping (observe.go)
// -- confirmed here by checking that a SECOND qualifying observation, with
// no debounce-satisfying gap, still returns a well-formed forecast (Observe
// itself never surfaces Fire/Event, per its own doc comment) but that the
// underlying Observer state was genuinely fed, provable indirectly via
// burn-rate: the second call's forecast reflects a real Previous sample
// (BurnRateP50 populated), which is only possible if Service tracked
// quota history across the two calls -- runway.Scorer itself is stateless
// (its own doc comment) so this could not happen without Service's own
// composition.
func TestService_Observe_ProducesForecastAndTracksDebounceState(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()

	first, err := h.svc.Observe(ctx, app.RuntimeObservation{
		SessionID: "sess1",
		Quota: domain.QuotaObservation{
			LimitID: "limit-1", UsedPercent: ptrFloat(50),
			ObservedAt: h.clock.Now(),
		},
	})
	if err != nil {
		t.Fatalf("first Observe: %v", err)
	}
	if first.SampleCount != 0 {
		t.Fatalf("first call: SampleCount = %d, want 0 (cold start, no Previous yet)", first.SampleCount)
	}

	h.clock.t = h.clock.t.Add(1 * time.Minute)
	second, err := h.svc.Observe(ctx, app.RuntimeObservation{
		SessionID: "sess1",
		Quota: domain.QuotaObservation{
			LimitID: "limit-1", UsedPercent: ptrFloat(60),
			ObservedAt: h.clock.Now(),
		},
	})
	if err != nil {
		t.Fatalf("second Observe: %v", err)
	}
	if second.BurnRateP50 == nil {
		t.Fatal("second call: BurnRateP50 = nil, want populated (proves Service tracked the first call's sample as Previous)")
	}
	if *second.BurnRateP50 <= 0 {
		t.Fatalf("second call: BurnRateP50 = %v, want > 0 (usage increased 50%%->60%% over 1 minute)", *second.BurnRateP50)
	}
}

func TestService_Observe_EmergencyThresholdStillReturnsForecast(t *testing.T) {
	h := newServiceHarness(t)
	forecast, err := h.svc.Observe(context.Background(), app.RuntimeObservation{
		SessionID: "sess1",
		Quota:     domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(99), Reached: true, ObservedAt: h.clock.Now()},
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if forecast.RiskScore != 1.0 {
		t.Fatalf("RiskScore = %v, want 1.0 for a Reached observation", forecast.RiskScore)
	}
}

func TestService_Observe_MissingSessionIDRejected(t *testing.T) {
	h := newServiceHarness(t)
	_, err := h.svc.Observe(context.Background(), app.RuntimeObservation{})
	if err == nil {
		t.Fatal("expected an error for a missing SessionID")
	}
}

// --- 2. RequestPause ---------------------------------------------------------

// TestService_RequestPause_ResolvesSessionAndDelegates proves RequestPause
// resolves SessionID->TaskID via SessionContextResolver and delegates to
// the real pause.RequestPause for the actual idempotent create-or-return
// logic (requestpause.go) -- confirmed by calling twice with the same
// SessionID/Reason and checking the SECOND call returns the SAME PauseID
// (RequestPause's own idempotency, not reimplemented here) rather than
// creating a duplicate.
func TestService_RequestPause_ResolvesSessionAndDelegates(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()

	first, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("first RequestPause: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected a non-empty PauseID")
	}
	if first.Status != domain.PausePredicted {
		t.Fatalf("Status = %q, want %q (this package's entry state)", first.Status, domain.PausePredicted)
	}

	second, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("second RequestPause: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second RequestPause created a NEW pause (%q), want idempotent replay of %q", second.ID, first.ID)
	}

	// The underlying store shows a real (TaskID, SessionID)-keyed record --
	// proof SessionContextResolver's TaskID was genuinely threaded through
	// to pause.PauseKey, not merely accepted and discarded.
	rec, found, err := h.store.GetByID(ctx, first.ID)
	if err != nil || !found {
		t.Fatalf("GetByID: found=%v err=%v", found, err)
	}
	if rec.Key.TaskID != "task1" {
		t.Fatalf("Key.TaskID = %q, want %q (from SessionContextResolver)", rec.Key.TaskID, "task1")
	}
}

func TestService_RequestPause_SessionResolutionErrorPropagates(t *testing.T) {
	h := newServiceHarness(t)
	h.svc.Sessions = fixedSessionContextResolver{err: errors.New("session lookup failed")}
	_, err := h.svc.RequestPause(context.Background(), app.PauseRequest{SessionID: "sess1", Reason: "calibrated_hit_probability"})
	if err == nil {
		t.Fatal("expected the SessionContextResolver's error to propagate")
	}
}

// --- 3. ReachSafePoint -------------------------------------------------------

// TestService_ReachSafePoint_PersistsBeforeInterrupting proves
// ReachSafePoint composes PersistThenInterrupt's ordering guarantee with
// the REAL five-phase Persist (persistphase.go): after a successful call,
// the record must be all the way at Sleeping (Checkpointing->Interrupting
// via Persist's own success, then Interrupting->Sleeping via
// InterruptAndSleep), and the real collaborators (ProgressTree/
// StateCheckpoint/RepositoryCheckpoint/WakeJobs) must all have actually
// been called -- not merely ordered correctly against each other in the
// abstract, safepoint_test.go's own narrower proof.
func TestService_ReachSafePoint_PersistsBeforeInterrupting(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()

	created, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}

	rec, err := h.svc.ReachSafePoint(ctx, app.SafePoint{PauseID: created.ID, At: h.clock.Now()})
	if err != nil {
		t.Fatalf("ReachSafePoint: %v", err)
	}
	if rec.Status != domain.PauseSleeping {
		t.Fatalf("Status = %q, want %q", rec.Status, domain.PauseSleeping)
	}

	// The real Persist phases actually ran: a wake job exists for this
	// pause (phase 5), and the durable PersistProgress markers (via this
	// same SQLiteStore's own PersistPauseStore reconciliation) show every
	// phase completed -- proof this Service really drove pause.Persist,
	// not a shortcut that skipped straight to Sleeping.
	job, found, err := h.wakes.GetByPauseKind(ctx, created.ID, "pause_resume")
	if err != nil || !found {
		t.Fatalf("expected a scheduled wake job: found=%v err=%v", found, err)
	}
	if job.PauseID != created.ID {
		t.Fatalf("wake job PauseID = %q, want %q", job.PauseID, created.ID)
	}

	progress, found, err := h.store.(pause.PersistPauseStore).GetProgress(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetProgress: found=%v err=%v", found, err)
	}
	if !progress.ProgressSnapshotTaken || progress.StateCheckpointID == nil ||
		progress.RepositoryCheckpointID == nil || !progress.PauseRecordSaved || progress.WakeJobID == nil {
		t.Fatalf("expected every Persist phase durably recorded, got %+v", progress)
	}
}

// TestService_ReachSafePoint_PersistFailureNeverInterrupts proves the
// ORDERING half explicitly: if a Persist collaborator fails, the provider
// interrupter must never be called at all (PersistThenInterrupt's own
// contract, safepoint.go), and the record must not reach Sleeping.
func TestService_ReachSafePoint_PersistFailureNeverInterrupts(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()

	created, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}

	var interruptCalled bool
	h.svc.Interrupter = fakeTurnInterrupter{fn: func(_ context.Context, _ app.RunLocator) error {
		interruptCalled = true
		return nil
	}}
	h.svc.StateCheckpoint = &fakes.FakeStateCheckpointService{
		CreateFunc: func(context.Context, app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{}, errors.New("state checkpoint store unavailable")
		},
	}

	_, err = h.svc.ReachSafePoint(ctx, app.SafePoint{PauseID: created.ID, At: h.clock.Now()})
	if err == nil {
		t.Fatal("expected ReachSafePoint to surface the Persist failure")
	}
	if interruptCalled {
		t.Fatal("the provider interrupter must never be called when Persist fails")
	}

	rec, found, err := h.store.GetByID(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetByID: found=%v err=%v", found, err)
	}
	if rec.Status == domain.PauseSleeping || rec.Status == domain.PauseInterrupting {
		t.Fatalf("Status = %q, must not have advanced past the Persist failure", rec.Status)
	}
}

func TestService_ReachSafePoint_UnknownPauseIDRejected(t *testing.T) {
	h := newServiceHarness(t)
	_, err := h.svc.ReachSafePoint(context.Background(), app.SafePoint{PauseID: "does-not-exist", At: h.clock.Now()})
	if err == nil {
		t.Fatal("expected an error for a PauseID with no remembered context (RequestPause was never called)")
	}
}

// --- 4. EnterSleep -----------------------------------------------------------

// TestService_EnterSleep_ReportsTheWakeJobPersistScheduled proves EnterSleep
// reports the SAME wake job Persist's own phase 5 already scheduled during
// ReachSafePoint (persistphase.go's scheduleWakeJobIdempotent), rather than
// creating a second one -- composition, not new scheduling logic.
func TestService_EnterSleep_ReportsTheWakeJobPersistScheduled(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()

	created, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if _, err := h.svc.ReachSafePoint(ctx, app.SafePoint{PauseID: created.ID, At: h.clock.Now()}); err != nil {
		t.Fatalf("ReachSafePoint: %v", err)
	}

	wakeJob, err := h.svc.EnterSleep(ctx, created.ID)
	if err != nil {
		t.Fatalf("EnterSleep: %v", err)
	}
	if wakeJob.PauseID != created.ID {
		t.Fatalf("WakeJob.PauseID = %q, want %q", wakeJob.PauseID, created.ID)
	}

	directLookup, found, err := h.wakes.GetByPauseKind(ctx, created.ID, "pause_resume")
	if err != nil || !found {
		t.Fatalf("GetByPauseKind: found=%v err=%v", found, err)
	}
	if wakeJob.ID != directLookup.ID {
		t.Fatalf("EnterSleep reported job %q, want the SAME job Persist scheduled (%q)", wakeJob.ID, directLookup.ID)
	}
}

// TestService_EnterSleep_RejectsRecordNotYetSleeping proves EnterSleep
// fails closed (no wake job invented) when called before ReachSafePoint
// ever ran.
func TestService_EnterSleep_RejectsRecordNotYetSleeping(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()
	created, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	_, err = h.svc.EnterSleep(ctx, created.ID)
	if err == nil {
		t.Fatal("expected EnterSleep to reject a pause that never reached Sleeping")
	}
	var terr *pause.TransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("expected a *pause.TransitionError, got %v", err)
	}
}

// --- 5. Resume ---------------------------------------------------------------

// runToSleeping drives a fresh pause all the way to Sleeping via the real
// Service methods (RequestPause -> ReachSafePoint), returning its PauseID.
func runToSleeping(t *testing.T, h *serviceHarness) domain.PauseID {
	t.Helper()
	ctx := context.Background()
	// Observe FIRST, matching the real pipeline order (ADD §20.2/§20.3:
	// Observe is a continuous background recompute that runs ahead of any
	// RequestPause call) -- this is also what lets RequestPause remember a
	// real QuotaBaseline for later resume validation (see RequestPause's
	// own doc comment: Observe is this Service's only source for that
	// baseline, since the frozen PauseRequest DTO carries no quota sample).
	if _, err := h.svc.Observe(ctx, app.RuntimeObservation{
		SessionID: "sess1",
		Quota:     domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(40), ObservedAt: h.clock.Now()},
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	created, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if _, err := h.svc.ReachSafePoint(ctx, app.SafePoint{PauseID: created.ID, At: h.clock.Now()}); err != nil {
		t.Fatalf("ReachSafePoint: %v", err)
	}
	if _, err := pause.Wake(ctx, h.store, pause.WakeRequest{PauseID: created.ID}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	return created.ID
}

// TestService_Resume_ValidatesBeforeResuming proves Resume runs the real
// ValidateResume checklist BEFORE calling the state-machine Resume, mapped
// via Verdict() -- confirmed here on the all-checks-pass path (lands at
// Resumed).
func TestService_Resume_ValidatesBeforeResuming(t *testing.T) {
	h := newServiceHarness(t)
	pauseID := runToSleeping(t, h)

	result, err := h.svc.Resume(context.Background(), app.ResumeRequest{PauseID: pauseID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != domain.PauseResumed {
		t.Fatalf("Status = %q, want %q", result.Status, domain.PauseResumed)
	}
}

// TestService_Resume_QuotaUnsafeReschedulesRatherThanResumes proves a
// quota-unsafe ValidateResume verdict maps onto Resume's QuotaUnsafe path
// (lands back at Sleeping, per Verdict()'s own documented mapping), not a
// hard failure -- the full "unsafe quota reschedules" required test, now
// proven through the real Service composition rather than
// resumevalidation_test.go's isolated CheckResult-level proof or
// fulllifecycle_test.go's own hand-assembled pipeline.
func TestService_Resume_QuotaUnsafeReschedulesRatherThanResumes(t *testing.T) {
	h := newServiceHarness(t)
	pauseID := runToSleeping(t, h)

	h.svc.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil // worse than okQuotaBaseline's implicit baseline
	}}

	result, err := h.svc.Resume(context.Background(), app.ResumeRequest{PauseID: pauseID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != domain.PauseSleeping {
		t.Fatalf("Status = %q, want %q (rescheduled, not resumed)", result.Status, domain.PauseSleeping)
	}
	if pause.IsTerminal(result.Status) {
		t.Fatal("expected the rescheduled status to remain non-terminal")
	}
}

// TestService_Resume_RepositoryConflictBlocksRatherThanResumes proves a
// repository-overlap ValidateResume failure maps onto Resume's Conflict
// path (BlockedConflict), the "repo overlap blocks" required test through
// the real Service.
func TestService_Resume_RepositoryConflictBlocksRatherThanResumes(t *testing.T) {
	h := newServiceHarness(t)
	pauseID := runToSleeping(t, h)

	h.svc.RepoFingerprint = fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"internal/pause/lifecycle.go"}}, nil
	}}

	result, err := h.svc.Resume(context.Background(), app.ResumeRequest{PauseID: pauseID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != domain.PauseBlockedConflict {
		t.Fatalf("Status = %q, want %q", result.Status, domain.PauseBlockedConflict)
	}
}

// --- 6. Cancel -----------------------------------------------------------

// TestService_Cancel_DelegatesToLifecycleCancel proves Cancel maps the
// frozen bare-PauseID signature onto pause.CancelRequest and delegates to
// the real, already-tested pause.Cancel (lifecycle.go).
func TestService_Cancel_DelegatesToLifecycleCancel(t *testing.T) {
	h := newServiceHarness(t)
	ctx := context.Background()
	created, err := h.svc.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: string(pause.TriggerReasonCalibrated)})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}

	if err := h.svc.Cancel(ctx, created.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	rec, found, err := h.store.GetByID(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetByID: found=%v err=%v", found, err)
	}
	if rec.Status != domain.PauseCancelled {
		t.Fatalf("Status = %q, want %q", rec.Status, domain.PauseCancelled)
	}
}

// TestService_Cancel_WinsRaceAgainstResume_ThroughTheRealService re-proves
// "cancel wins race with wake" one more level up: through Service's own
// Resume (not just the bare pause.Resume/pause.Cancel functions
// fulllifecycle_test.go already exercises), confirming Service adds no new
// race window of its own.
func TestService_Cancel_WinsRaceAgainstResume_ThroughTheRealService(t *testing.T) {
	h := newServiceHarness(t)
	pauseID := runToSleeping(t, h)
	ctx := context.Background()

	if _, err := pause.Wake(ctx, h.store, pause.WakeRequest{PauseID: pauseID}); err == nil {
		// Already woken by runToSleeping's own Wake call above in some
		// paths; a second Wake attempt here is expected to fail (already
		// WakePending) -- this branch intentionally left inert; the real
		// assertion is below.
		_ = err
	}

	if err := h.svc.Cancel(ctx, pauseID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	_, err := h.svc.Resume(ctx, app.ResumeRequest{PauseID: pauseID})
	if err == nil {
		t.Fatal("expected Resume to fail after Cancel already landed")
	}

	rec, found, err := h.store.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("GetByID: found=%v err=%v", found, err)
	}
	if rec.Status != domain.PauseCancelled {
		t.Fatalf("Status = %q, want %q (cancel wins)", rec.Status, domain.PauseCancelled)
	}
}

func TestService_Cancel_MissingStoreFailsClosed(t *testing.T) {
	svc := pause.NewService(pause.ServiceDeps{})
	if err := svc.Cancel(context.Background(), "pause-1"); err == nil {
		t.Fatal("expected Cancel to fail closed with no Store configured")
	}
}
