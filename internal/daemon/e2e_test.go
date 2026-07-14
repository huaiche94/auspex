// e2e_test.go: issue #7's acceptance criterion, verbatim — "一個排程的
// wake job 能在無人操作下於到期時被 daemon 執行並通過 resume validation."
//
// The test stages the real cross-process shape: "process A" (one
// pause.Service instance — the short-lived CLI/hook invocation) observes
// quota, requests the pause, and reaches the safe point (persist phase +
// wake-job scheduling + interrupt → Sleeping); "process B" (a SECOND
// pause.Service instance with its own EMPTY in-memory context map — the
// daemon) runs the Worker loop against the SAME migrated SQLite file.
// Nothing touches the pause after ReachSafePoint except the worker: the
// wake fires because the clock passed run_after, the REAL ValidateResume
// checklist runs inside Service.Resume (hydrated from the durable pause
// context, contextstore.go — the exact cross-process gap D-16 closed),
// and the job completes. Two Service instances is not a simulation
// shortcut: the in-memory context map is per-instance, so if the durable
// context path broke, process B's Resume would fail not_found and this
// test would fail — that assertion IS the point.
package daemon_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/daemon"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/predictor/runway"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	protocol "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- minimal doubles (mirroring internal/pause's own test fakes, which are
// package-private there) --------------------------------------------------

type e2eClock struct{ t time.Time }

func (c *e2eClock) Now() time.Time { return c.t }

type e2eIDs struct {
	prefix string
	n      int
}

func (s *e2eIDs) NewID() string {
	s.n++
	return s.prefix + "-" + string(rune('0'+s.n))
}

type e2eSessionResolver struct{ ctx pause.SessionContext }

func (r e2eSessionResolver) ResolveSessionContext(context.Context, domain.SessionID) (pause.SessionContext, error) {
	return r.ctx, nil
}

type e2eQuotaReader struct{}

func (e2eQuotaReader) ReadCurrentQuota(context.Context, domain.SessionID, string) (domain.QuotaObservation, error) {
	used := 40.0 // recovered well below the 90% baseline the pause recorded
	return domain.QuotaObservation{LimitID: "seven_day", UsedPercent: &used}, nil
}

type e2eFingerprintReader struct{}

func (e2eFingerprintReader) ReadCurrentFingerprint(context.Context, domain.WorktreeID) (pause.RepoFingerprint, error) {
	return pause.RepoFingerprint{HeadOID: "head-1"}, nil
}

type e2eSessionReader struct{}

func (e2eSessionReader) ReadSessionCapability(context.Context, domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
	return pause.SessionCapabilitySnapshot{Resumable: true, Capabilities: domain.ProviderCapabilities{SessionResume: true}}, nil
}

type e2eInterrupter struct{}

func (e2eInterrupter) Interrupt(context.Context, app.RunLocator) error { return nil }

func e2eEvaluations() *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
		},
	}
}

func e2eService(t *testing.T, db *sqlite.DB, store pause.PauseStore, wakes *scheduler.Store, clk *e2eClock, idPrefix string) *pause.Service {
	t.Helper()
	return pause.NewService(pause.ServiceDeps{
		Store: store,
		Clock: clk,
		IDs:   &e2eIDs{prefix: idPrefix},
		Sessions: e2eSessionResolver{ctx: pause.SessionContext{
			TaskID:          "task1",
			WorktreeID:      "wt1",
			PausedWorkPaths: []string{"internal/pause/lifecycle.go"},
		}},
		RunwayScorer: runway.NewScorer(),
		Observer:     pause.NewObserver(pause.NewObserveConfig()),
		ProgressTree: &fakes.FakeProgressTreeService{
			SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
				return app.ProgressTreeSnapshot{TaskID: taskID}, nil
			},
		},
		StateCheckpoint: &fakes.FakeStateCheckpointService{
			CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				return domain.StateCheckpoint{ID: "state-ckpt-1", TaskID: req.TaskID}, nil
			},
		},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{
			CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
				return app.RepositoryCheckpoint{ID: "repo-ckpt-1", GitHead: "head-1", Status: "created"}, nil
			},
			VerifyFunc: func(_ context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
				return app.RepositoryCheckpointVerification{ID: id, Valid: true}, nil
			},
		},
		WakeJobs:        wakes,
		WakeMaxAttempts: 5,
		WakeAfter:       10 * time.Minute,
		Interrupter:     e2eInterrupter{},
		Locate:          func(domain.PauseID) app.RunLocator { return app.RunLocator{SessionID: "sess1", TurnID: "turn1"} },
		Quota:           e2eQuotaReader{},
		RepoFingerprint: e2eFingerprintReader{},
		Session:         e2eSessionReader{},
		Evaluations:     e2eEvaluations(),
		RepoPolicy:      pause.RepoChangePolicyAllowUnrelated,
	})
}

func openMigratedE2EDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "auspex.db"))
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
	now := "2026-07-14T10:00:00Z"
	for _, stmt := range []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('sess1', 'wt1', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('task1', 'sess1', 'wt1', 'hash1', 'pending', '` + now + `', '` + now + `')`,
	} {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	return db
}

func TestDaemonWorker_ExecutesScheduledWakeUnattended(t *testing.T) {
	db := openMigratedE2EDB(t)
	ctx := context.Background()
	clk := &e2eClock{t: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}

	// --- "process A": the invocation that pauses -------------------------
	storeA := pause.NewSQLiteStore(db)
	wakesA := scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wj"})
	svcA := e2eService(t, db, storeA, wakesA, clk, "pauseA")

	// Observe first so RequestPause records a real quota baseline (90%,
	// the state that justified pausing) — ValidateResume later compares
	// the daemon-side reading (40%) against exactly this.
	used := 90.0
	if _, err := svcA.Observe(ctx, app.RuntimeObservation{
		SessionID: "sess1",
		Quota:     domain.QuotaObservation{SessionID: "sess1", LimitID: "seven_day", UsedPercent: &used, ObservedAt: clk.Now()},
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	rec, err := svcA.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: "calibrated"})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if _, err := svcA.ReachSafePoint(ctx, app.SafePoint{PauseID: rec.ID, At: clk.Now()}); err != nil {
		t.Fatalf("ReachSafePoint: %v", err)
	}
	job, err := svcA.EnterSleep(ctx, rec.ID)
	if err != nil {
		t.Fatalf("EnterSleep: %v", err)
	}

	// --- the clock passes the wake time; "process A" is gone -------------
	clk.t = job.RunAfter.Add(time.Minute)

	// --- "process B": the daemon ------------------------------------------
	// Fresh Service instance == fresh (empty) in-memory context map. Its
	// Resume can only succeed via the durable pause context.
	storeB := pause.NewSQLiteStore(db)
	wakesB := scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wjB"})
	svcB := e2eService(t, db, storeB, wakesB, clk, "pauseB")
	broker := daemon.NewBroker()
	events, cancelSub := broker.Subscribe()
	defer cancelSub()

	worker := daemon.NewWorker(daemon.WorkerDeps{
		Jobs:         wakesB,
		Pause:        svcB,
		PauseStore:   storeB,
		Clock:        clk,
		IDs:          &e2eIDs{prefix: "ev"},
		Events:       broker,
		PollInterval: 10 * time.Millisecond,
	})
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()

	// --- unattended outcome ------------------------------------------------
	deadline := time.After(10 * time.Second)
	for {
		j, found, err := wakesB.GetByPauseKind(ctx, rec.ID, "pause_resume")
		if err != nil {
			t.Fatalf("GetByPauseKind: %v", err)
		}
		if found && j.Status == scheduler.StatusDone {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("wake job never completed; job = %+v", j)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancelWorker()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("worker.Run: %v", err)
	}

	final, found, err := storeB.GetByID(ctx, rec.ID)
	if err != nil || !found {
		t.Fatalf("GetByID = found %v, err %v", found, err)
	}
	if final.Status != domain.PauseResumed {
		t.Fatalf("pause status = %q, want %q (resume validation must have PASSED unattended)", final.Status, domain.PauseResumed)
	}

	// The broker saw the lifecycle: wake triggered → validation started →
	// resume completed (order-tolerant scan; the assertions above already
	// proved the state machine, this proves the SSE surface saw it too).
	seen := map[protocol.EventType]bool{}
	for {
		select {
		case ev := <-events:
			seen[ev.EventType] = true
		default:
			goto scanned
		}
	}
scanned:
	for _, want := range []protocol.EventType{
		protocol.EventPauseWakeTriggered,
		protocol.EventPauseResumeValidationStarted,
		protocol.EventPauseResumeCompleted,
	} {
		if !seen[want] {
			t.Errorf("event %q never published to the broker", want)
		}
	}
}

// TestDaemonWorker_QuotaStillUnsafeReschedules proves the §20.7 backoff
// path end-to-end: a daemon-side quota reading WORSE than the pause's
// baseline sends the record back to Sleeping and the wake job back to
// scheduled with a later run_after — never Resumed, never dead on the
// first attempt.
func TestDaemonWorker_QuotaStillUnsafeReschedules(t *testing.T) {
	db := openMigratedE2EDB(t)
	ctx := context.Background()
	clk := &e2eClock{t: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}

	storeA := pause.NewSQLiteStore(db)
	wakesA := scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wj"})
	svcA := e2eService(t, db, storeA, wakesA, clk, "pauseA")

	used := 90.0
	if _, err := svcA.Observe(ctx, app.RuntimeObservation{
		SessionID: "sess1",
		Quota:     domain.QuotaObservation{SessionID: "sess1", LimitID: "seven_day", UsedPercent: &used, ObservedAt: clk.Now()},
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	rec, err := svcA.RequestPause(ctx, app.PauseRequest{SessionID: "sess1", Reason: "calibrated"})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if _, err := svcA.ReachSafePoint(ctx, app.SafePoint{PauseID: rec.ID, At: clk.Now()}); err != nil {
		t.Fatalf("ReachSafePoint: %v", err)
	}
	job, err := svcA.EnterSleep(ctx, rec.ID)
	if err != nil {
		t.Fatalf("EnterSleep: %v", err)
	}
	clk.t = job.RunAfter.Add(time.Minute)

	storeB := pause.NewSQLiteStore(db)
	wakesB := scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wjB"})
	svcB := e2eServiceWithQuota(t, db, storeB, wakesB, clk, unsafeQuotaReader{})

	worker := daemon.NewWorker(daemon.WorkerDeps{
		Jobs: wakesB, Pause: svcB, PauseStore: storeB, Clock: clk,
		PollInterval: 10 * time.Millisecond,
	})
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()

	deadline := time.After(10 * time.Second)
	var j scheduler.Job
	for {
		var found bool
		var err error
		j, found, err = wakesB.GetByPauseKind(ctx, rec.ID, "pause_resume")
		if err != nil {
			t.Fatalf("GetByPauseKind: %v", err)
		}
		if found && j.Status == scheduler.StatusScheduled && j.Attempts > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("wake job never rescheduled; job = %+v", j)
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancelWorker()
	<-done

	if !j.RunAfter.After(clk.Now()) {
		t.Errorf("rescheduled run_after %v not in the future of %v", j.RunAfter, clk.Now())
	}
	final, _, err := storeB.GetByID(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Status != domain.PauseSleeping {
		t.Errorf("pause status = %q, want %q (quota-unsafe goes back to sleep)", final.Status, domain.PauseSleeping)
	}
}

// unsafeQuotaReader reports usage WORSE than any baseline — quota never
// recovered.
type unsafeQuotaReader struct{}

func (unsafeQuotaReader) ReadCurrentQuota(context.Context, domain.SessionID, string) (domain.QuotaObservation, error) {
	used := 99.0
	return domain.QuotaObservation{LimitID: "seven_day", UsedPercent: &used, Reached: true}, nil
}

// e2eServiceWithQuota is e2eService with a caller-chosen quota reader.
func e2eServiceWithQuota(t *testing.T, db *sqlite.DB, store pause.PauseStore, wakes *scheduler.Store, clk *e2eClock, quota pause.QuotaSnapshotReader) *pause.Service {
	t.Helper()
	svc := e2eService(t, db, store, wakes, clk, "pauseB")
	svc.Quota = quota
	return svc
}
