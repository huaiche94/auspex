// pausedrive_test.go: end-to-end coverage for the M10 Graceful Pause
// auto-trigger (issue #122, pausedrive.go) against the SAME compiled fake
// provider binary run_test.go uses and a REAL pause.Service composed the
// way internal/pause/service_test.go composes one (real SQLite-backed
// PauseStore + real scheduler.Store over a migrated temp DB, fakes for the
// checkpoint services — per-file duplicates of small cross-package test
// helpers, this repo's established convention).
//
// The three ADD M10 acceptance rows this file proves end to end in a
// managed run:
//
//	P_hit >= .80 twice -> pause requested   (TestAutoPause_CalibratedPHitTwice_...)
//	spike only -> no pause                  (TestAutoPause_SpikeOnly_NoPause)
//	safe point -> checkpoints -> interrupt -> sleeping
//	                                        (TestAutoPause_CalibratedPHitTwice_...'s
//	                                         sleeping/checkpoint/wake-job asserts)
//
// plus the ADD §17.6 emergency path (uncalibrated, no debounce), the
// calibration gate (a high P_hit on an UNCALIBRATED forecast must never
// fire), and the fail-toward-continuing posture (a failed pause request
// leaves the provider running).
package managed

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/clock"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/providers/codex/appserver"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- real migrated DB + seeded session chain (pause package's own test
//     harness conventions, duplicated per-file per repo convention) --------

func openMigratedPauseDB(t *testing.T) *sqlite.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auspex.db")
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

// seedPauseChain inserts the minimal repositories -> worktrees ->
// provider_sessions -> tasks chain pause_records' real FKs require, with
// the provider_sessions row keyed to the managed run's own SessionID.
func seedPauseChain(t *testing.T, db *sqlite.DB, sessionID domain.SessionID) {
	t.Helper()
	now := "2026-07-18T10:00:00Z"
	for _, stmt := range []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'repo1', '/tmp/repo1', '/tmp/repo1/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('` + string(sessionID) + `', 'wt1', 'claude', 'managed_stream_json', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('task1', '` + string(sessionID) + `', 'wt1', 'hash1', 'pending', '` + now + `', '` + now + `')`,
	} {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// --- pause.Service collaborator doubles (service_test.go's own idiom) ----

type fixedSessionResolver struct{}

func (fixedSessionResolver) ResolveSessionContext(_ context.Context, _ domain.SessionID) (pause.SessionContext, error) {
	return pause.SessionContext{TaskID: "task1", WorktreeID: "wt1"}, nil
}

func autoPauseCheckpointFakes() (*fakes.FakeProgressTreeService, *fakes.FakeStateCheckpointService, *fakes.FakeRepositoryCheckpointService) {
	progress := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			return app.ProgressTreeSnapshot{TaskID: taskID}, nil
		},
	}
	state := &fakes.FakeStateCheckpointService{
		CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
			return domain.StateCheckpoint{ID: "state-ckpt-1", TaskID: req.TaskID}, nil
		},
	}
	repo := &fakes.FakeRepositoryCheckpointService{
		CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
			return app.RepositoryCheckpoint{ID: "repo-ckpt-1", GitHead: "head-1", Status: "created"}, nil
		},
	}
	return progress, state, repo
}

// --- scripted forecast source --------------------------------------------

// scriptedForecasts is a deterministic RunwayObservationSource: call n is
// answered by fn(n). It stands in for the production
// GracefulPauseObservationSource so the e2e tests can hand the trigger
// exactly the forecast shapes each acceptance row needs — including the
// calibrated forecasts no production scorer can emit yet (the M13
// calibration gap; the production source's own behavior is covered by
// TestGracefulPauseObservationSource_* below).
type scriptedForecasts struct {
	mu sync.Mutex
	n  int
	fn func(call int) (domain.RunwayForecast, bool)
}

func (s *scriptedForecasts) ObserveRunway(_ context.Context, _ domain.SessionID) (domain.RunwayForecast, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.fn(s.n)
}

func calibratedHotForecast() domain.RunwayForecast {
	pHit := 0.9
	now := time.Now()
	return domain.RunwayForecast{
		LimitID:         "five_hour",
		Calibrated:      true,
		HitProbability:  &pHit,
		RiskScore:       0.9,
		Confidence:      domain.ConfidenceHigh,
		QuotaObservedAt: &now,
	}
}

func calmForecast() domain.RunwayForecast {
	return domain.RunwayForecast{LimitID: "five_hour", RiskScore: 0.2, Confidence: domain.ConfidenceMedium}
}

func emergencyForecast() domain.RunwayForecast {
	used := 99.0
	return domain.RunwayForecast{
		LimitID:            "five_hour",
		Calibrated:         false,
		CurrentUsedPercent: &used,
		RiskScore:          1,
		Confidence:         domain.ConfidenceMedium,
	}
}

// --- the e2e harness ------------------------------------------------------

type autoPauseHarness struct {
	db      *sqlite.DB
	store   *pause.SQLiteStore
	wakes   *scheduler.Store
	runs    *LiveRunInterrupter
	trigger *PauseTrigger
}

// fastObserveConfig is the ADD-default trigger config with only the
// debounce interval shrunk so "two consecutive samples, at least
// MinDebounceInterval apart" completes in tens of milliseconds instead of
// ten seconds — every threshold that gates WHAT qualifies (0.80, 30s
// freshness, 0.70 reset, 98%/60s emergency) stays at the ADD default.
func fastObserveConfig() pause.ObserveConfig {
	cfg := pause.NewObserveConfig()
	cfg.MinDebounceInterval = 10 * time.Millisecond
	return cfg
}

func newAutoPauseHarness(t *testing.T, sessionID domain.SessionID) *autoPauseHarness {
	t.Helper()
	db := openMigratedPauseDB(t)
	seedPauseChain(t, db, sessionID)

	clk := clock.New()
	store := pause.NewSQLiteStore(db)
	wakes := scheduler.NewStore(db.Conn(), clk, &runTestIDs{})
	runs := NewLiveRunInterrupter()
	progress, state, repo := autoPauseCheckpointFakes()

	svc := pause.NewService(pause.ServiceDeps{
		Store:                store,
		Clock:                clk,
		IDs:                  &runTestIDs{},
		Sessions:             fixedSessionResolver{},
		ProgressTree:         progress,
		StateCheckpoint:      state,
		RepositoryCheckpoint: repo,
		WakeJobs:             wakes,
		Interrupter:          runs,
		Locate: func(pauseID domain.PauseID) app.RunLocator {
			rec, found, err := store.GetByID(context.Background(), pauseID)
			if err != nil || !found {
				return app.RunLocator{}
			}
			return app.RunLocator{SessionID: rec.Key.SessionID}
		},
	})

	cfg := fastObserveConfig()
	return &autoPauseHarness{
		db:    db,
		store: store,
		wakes: wakes,
		runs:  runs,
		trigger: &PauseTrigger{
			Service:          svc,
			Runs:             runs,
			Source:           nil, // each test scripts its own
			Observe:          &cfg,
			Interval:         20 * time.Millisecond,
			InterruptGrace:   2 * time.Second,
			LifecycleTimeout: 30 * time.Second,
		},
	}
}

// pauseRecordRow is the durable pause_records state the acceptance asserts
// read back (checkpoint links land on the row via the persist phase).
type pauseRecordRow struct {
	ID           string
	Status       string
	StateCkpt    sql.NullString
	RepoCkpt     sql.NullString
	MetadataJSON string
}

func readPauseRecords(t *testing.T, db *sqlite.DB) []pauseRecordRow {
	t.Helper()
	rows, err := db.Conn().QueryContext(context.Background(), `
		SELECT id, status, state_checkpoint_id, repository_checkpoint_id, metadata_json
		FROM pause_records ORDER BY id`)
	if err != nil {
		t.Fatalf("query pause_records: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []pauseRecordRow
	for rows.Next() {
		var r pauseRecordRow
		if err := rows.Scan(&r.ID, &r.Status, &r.StateCkpt, &r.RepoCkpt, &r.MetadataJSON); err != nil {
			t.Fatalf("scan pause_records: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pause_records rows: %v", err)
	}
	return out
}

// --- acceptance: P_hit >= .80 twice -> pause requested, and
//     safe point -> checkpoints -> interrupt -> sleeping -------------------

func TestAutoPause_CalibratedPHitTwice_PausesCheckpointsInterruptsSleeps(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	// The fake provider lingers long after writing its stream: without a
	// real interrupt the run would take ~20s. The trigger must end it far
	// sooner.
	t.Setenv("AUSPEX_FAKE_SLEEP_MS", "20000")

	req := baseRunRequest()
	harness := newAutoPauseHarness(t, req.SessionID)
	// Every heartbeat sees a fresh CALIBRATED P_hit=0.9 sample: sample one
	// arms the debounce, sample two (>= MinDebounceInterval later) fires —
	// ADD §17.6/§20.2's exact double-sample rule, thresholds at their ADD
	// defaults.
	harness.trigger.Source = &scriptedForecasts{fn: func(int) (domain.RunwayForecast, bool) {
		return calibratedHotForecast(), true
	}}

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyRun), bin)
	runner.Pause = harness.trigger

	humanLog := &bytes.Buffer{}
	req.HumanLog = humanLog

	start := time.Now()
	outcome, err := runner.Run(context.Background(), req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Interrupt actually stopped the provider: honest -1 (no exit code
	// observed), and the run ended in a fraction of the fake's 20s linger.
	if outcome.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1 (provider interrupted by auto-pause)", outcome.ExitCode)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("run took %v — the auto-pause interrupt did not stop the provider", elapsed)
	}

	// Pause requested (acceptance row 1), with the CALIBRATED reason code.
	records := readPauseRecords(t, harness.db)
	if len(records) != 1 {
		t.Fatalf("pause_records rows = %d, want exactly 1", len(records))
	}
	rec := records[0]
	if !strings.Contains(rec.MetadataJSON, string(pause.TriggerReasonCalibrated)) {
		t.Fatalf("pause record metadata %q does not carry the calibrated trigger reason %q", rec.MetadataJSON, pause.TriggerReasonCalibrated)
	}

	// Safe point -> checkpoints -> interrupt -> sleeping (acceptance row 3):
	// the record is durably Sleeping with both checkpoint links populated by
	// the persist phase, and the durable wake job exists.
	if rec.Status != string(domain.PauseSleeping) {
		t.Fatalf("pause record status = %q, want %q", rec.Status, domain.PauseSleeping)
	}
	if !rec.StateCkpt.Valid || rec.StateCkpt.String == "" {
		t.Fatal("pause record has no state_checkpoint_id — the persist phase's state checkpoint did not land")
	}
	if !rec.RepoCkpt.Valid || rec.RepoCkpt.String == "" {
		t.Fatal("pause record has no repository_checkpoint_id — the persist phase's repository checkpoint did not land")
	}
	job, found, err := harness.wakes.GetByPauseKind(context.Background(), domain.PauseID(rec.ID), "pause_resume")
	if err != nil || !found {
		t.Fatalf("wake job lookup: found=%v err=%v — Sleeping without a durable wake job", found, err)
	}
	if job.PauseID != domain.PauseID(rec.ID) {
		t.Fatalf("wake job PauseID = %q, want %q", job.PauseID, rec.ID)
	}

	log := humanLog.String()
	for _, want := range []string{"auto-pause", "requested", "sleeping"} {
		if !strings.Contains(log, want) {
			t.Errorf("human log %q missing %q", log, want)
		}
	}
}

// --- acceptance: spike only -> no pause -----------------------------------

func TestAutoPause_SpikeOnly_NoPause(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	// Long enough for many heartbeats (20ms interval), short enough to keep
	// the test fast — the provider must run to completion untouched.
	t.Setenv("AUSPEX_FAKE_SLEEP_MS", "500")

	req := baseRunRequest()
	harness := newAutoPauseHarness(t, req.SessionID)
	// One qualifying spike, then risk collapses below the 0.70 hysteresis
	// reset: the lone arm is cleared and nothing may fire.
	harness.trigger.Source = &scriptedForecasts{fn: func(call int) (domain.RunwayForecast, bool) {
		if call == 1 {
			return calibratedHotForecast(), true
		}
		return calmForecast(), true
	}}

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyRun), bin)
	runner.Pause = harness.trigger

	humanLog := &bytes.Buffer{}
	req.HumanLog = humanLog

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (provider must run to completion)", outcome.ExitCode)
	}
	if records := readPauseRecords(t, harness.db); len(records) != 0 {
		t.Fatalf("pause_records rows = %d, want 0 (a lone spike must not pause)", len(records))
	}
	if strings.Contains(humanLog.String(), "auto-pause") {
		t.Fatalf("human log %q mentions auto-pause for a lone spike", humanLog.String())
	}
}

// --- ADD §17.6 emergency path: uncalibrated, no double-sample -------------

func TestAutoPause_EmergencyUncalibrated_FiresImmediately(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_SLEEP_MS", "20000")

	req := baseRunRequest()
	harness := newAutoPauseHarness(t, req.SessionID)
	// used >= 98% on an UNCALIBRATED forecast: the emergency trigger needs
	// no calibration and no second sample (ADD §17.6) — this is the one
	// path that can fire in production today (see pausedrive.go's
	// calibration-gate doc).
	harness.trigger.Source = &scriptedForecasts{fn: func(int) (domain.RunwayForecast, bool) {
		return emergencyForecast(), true
	}}

	runner := newTestRunner(&runTestPersister{}, allowingEvaluation(app.PolicyRun), bin)
	runner.Pause = harness.trigger
	humanLog := &bytes.Buffer{}
	req.HumanLog = humanLog

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1 (provider interrupted)", outcome.ExitCode)
	}
	records := readPauseRecords(t, harness.db)
	if len(records) != 1 {
		t.Fatalf("pause_records rows = %d, want 1", len(records))
	}
	if !strings.Contains(records[0].MetadataJSON, string(pause.TriggerReasonEmergency)) {
		t.Fatalf("pause record metadata %q does not carry the emergency reason %q", records[0].MetadataJSON, pause.TriggerReasonEmergency)
	}
	if records[0].Status != string(domain.PauseSleeping) {
		t.Fatalf("pause record status = %q, want %q", records[0].Status, domain.PauseSleeping)
	}
}

// --- the calibration gate: uncalibrated P_hit must never fire -------------

func TestAutoPause_UncalibratedHighPHit_GatedNoPause(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_SLEEP_MS", "500")

	req := baseRunRequest()
	harness := newAutoPauseHarness(t, req.SessionID)
	// A P_hit far above 0.80 on a forecast whose Calibrated bit is FALSE —
	// exactly what a fabricated probability would look like pre-M13. The
	// calibration gate (pause.Observer's qualifiesCalibrated) must refuse
	// it, and the sub-98% usage keeps the emergency path out too.
	harness.trigger.Source = &scriptedForecasts{fn: func(int) (domain.RunwayForecast, bool) {
		pHit := 0.95
		now := time.Now()
		used := 60.0
		return domain.RunwayForecast{
			LimitID:            "five_hour",
			Calibrated:         false,
			HitProbability:     &pHit,
			RiskScore:          0.95,
			CurrentUsedPercent: &used,
			Confidence:         domain.ConfidenceMedium,
			QuotaObservedAt:    &now,
		}, true
	}}

	runner := newTestRunner(&runTestPersister{}, allowingEvaluation(app.PolicyRun), bin)
	runner.Pause = harness.trigger
	req.HumanLog = &bytes.Buffer{}

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (uncalibrated P_hit is gated; the provider must finish)", outcome.ExitCode)
	}
	if records := readPauseRecords(t, harness.db); len(records) != 0 {
		t.Fatalf("pause_records rows = %d, want 0 (calibration gate must hold)", len(records))
	}
}

// --- fail toward continuing work ------------------------------------------

func TestAutoPause_PauseRequestFails_RunContinues(t *testing.T) {
	bin := buildFakeProvider(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", fixtureAbs(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_SLEEP_MS", "500")

	req := baseRunRequest()
	// A service whose RequestPause always fails (e.g. no task row to pause
	// against): the trigger fires, the request fails, and the run MUST
	// continue to completion — fail toward continuing work, never toward
	// killing the session.
	svc := &fakes.FakeGracefulPauseService{
		RequestPauseFunc: func(_ context.Context, _ app.PauseRequest) (app.PauseRecord, error) {
			return app.PauseRecord{}, errors.New("no active task for session")
		},
	}
	cfg := fastObserveConfig()
	trigger := &PauseTrigger{
		Service:  svc,
		Runs:     NewLiveRunInterrupter(),
		Source:   &scriptedForecasts{fn: func(int) (domain.RunwayForecast, bool) { return emergencyForecast(), true }},
		Observe:  &cfg,
		Interval: 20 * time.Millisecond,
	}

	runner := newTestRunner(&runTestPersister{}, allowingEvaluation(app.PolicyRun), bin)
	runner.Pause = trigger
	humanLog := &bytes.Buffer{}
	req.HumanLog = humanLog

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (a failed pause request must not touch the provider)", outcome.ExitCode)
	}
	log := humanLog.String()
	if !strings.Contains(log, "pause request failed") || !strings.Contains(log, "run continues") {
		t.Fatalf("human log %q does not record the failed trigger", log)
	}
}

// --- LiveRunInterrupter unit coverage -------------------------------------

func TestLiveRunInterrupter_NoLiveRun_FailsClosed(t *testing.T) {
	registry := NewLiveRunInterrupter()
	err := registry.Interrupt(context.Background(), app.RunLocator{SessionID: "sess-none"})
	if err == nil {
		t.Fatal("Interrupt with no registered run must fail closed, got nil")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("want a typed ErrCodeUnavailable capability error, got %v", err)
	}
}

// --- GracefulPauseObservationSource unit coverage -------------------------

type scriptedQuota struct {
	observations []domain.QuotaObservation
	err          error
}

func (s *scriptedQuota) Quota(_ context.Context, _ domain.SessionID) ([]domain.QuotaObservation, error) {
	return s.observations, s.err
}

func TestGracefulPauseObservationSource_CombinesAndDedupes(t *testing.T) {
	usedA, usedB := 40.0, 95.0
	quota := &scriptedQuota{observations: []domain.QuotaObservation{
		{ID: "e1", LimitID: "five_hour", UsedPercent: &usedA, ObservedAt: time.Now()},
		{ID: "e2", LimitID: "seven_day", UsedPercent: &usedB, ObservedAt: time.Now()},
	}}

	var observeCalls int
	svc := &fakes.FakeGracefulPauseService{
		ObserveFunc: func(_ context.Context, obs app.RuntimeObservation) (domain.RunwayForecast, error) {
			observeCalls++
			risk := 0.3
			if obs.Quota.LimitID == "seven_day" {
				risk = 0.9
			}
			return domain.RunwayForecast{LimitID: obs.Quota.LimitID, RiskScore: risk}, nil
		},
	}
	source := &GracefulPauseObservationSource{Service: svc, Quota: quota}

	// First tick: both windows observed once, worst window wins (ADD §15.5
	// conservative max — runway.CombineWindows).
	forecast, ok := source.ObserveRunway(context.Background(), "sess1")
	if !ok {
		t.Fatal("ObserveRunway = ok=false, want a combined forecast")
	}
	if forecast.LimitID != "seven_day" || forecast.RiskScore != 0.9 {
		t.Fatalf("combined forecast = %+v, want the worst window (seven_day, 0.9)", forecast)
	}
	if observeCalls != 2 {
		t.Fatalf("Observe calls = %d, want 2 (one per window)", observeCalls)
	}

	// Second tick with the SAME event IDs: no re-observation (an identical
	// statusline re-render must not zero the burn-rate delta), but the
	// cached worst forecast still reports.
	forecast, ok = source.ObserveRunway(context.Background(), "sess1")
	if !ok || forecast.RiskScore != 0.9 {
		t.Fatalf("cached combined forecast = (%+v, %v), want the same worst window", forecast, ok)
	}
	if observeCalls != 2 {
		t.Fatalf("Observe calls after identical tick = %d, want still 2", observeCalls)
	}

	// A genuinely NEW sample for the calm window is re-observed and the
	// combination re-evaluated.
	usedA2 := 99.0
	quota.observations[0] = domain.QuotaObservation{ID: "e3", LimitID: "five_hour", UsedPercent: &usedA2, ObservedAt: time.Now()}
	svc.ObserveFunc = func(_ context.Context, obs app.RuntimeObservation) (domain.RunwayForecast, error) {
		observeCalls++
		return domain.RunwayForecast{LimitID: obs.Quota.LimitID, RiskScore: 1}, nil
	}
	forecast, ok = source.ObserveRunway(context.Background(), "sess1")
	if !ok || forecast.RiskScore != 1 {
		t.Fatalf("recombined forecast = (%+v, %v), want the fresh worst window", forecast, ok)
	}
	if observeCalls != 3 {
		t.Fatalf("Observe calls after new sample = %d, want 3", observeCalls)
	}
}

func TestGracefulPauseObservationSource_ColdStartIsHonest(t *testing.T) {
	source := &GracefulPauseObservationSource{
		Service: &fakes.FakeGracefulPauseService{},
		Quota:   &scriptedQuota{}, // no telemetry at all
	}
	if _, ok := source.ObserveRunway(context.Background(), "sess1"); ok {
		t.Fatal("cold start must report ok=false, never a zero forecast")
	}
}

// --- acceptance: ADD §21.6 — the full graceful sequence on the App
//     Server managed path (issue #9 slice C): runway trigger -> safe
//     point -> checkpoints -> turn/interrupt -> interrupted terminal ->
//     durable wake job -----------------------------------------------------

func TestAutoPause_AppServer_ProtocolInterrupt_PausesCheckpointsSleeps(t *testing.T) {
	bin := buildFakeAppServer(t)
	// No scripted events: the fake's turn stays inProgress until a
	// turn/interrupt arrives, which is exactly the long-running turn the
	// §21.6 sequence exists to stop.
	t.Setenv("AUSPEX_FAKE_AS_EVENTS", "")

	req := baseCodexRunRequest()
	harness := newAutoPauseHarness(t, req.SessionID)
	harness.trigger.Source = &scriptedForecasts{fn: func(int) (domain.RunwayForecast, bool) {
		return calibratedHotForecast(), true
	}}

	persister := &runTestPersister{}
	runner := newAppServerRunner(persister, bin)
	runner.Pause = harness.trigger

	humanLog := &bytes.Buffer{}
	req.HumanLog = humanLog

	// The safety net: if the trigger never fires, the context-cancel
	// bare-interrupt path ends the run and the asserts below fail loudly
	// instead of the test hanging on a forever-inProgress turn.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	outcome, err := runner.Run(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The turn ended by protocol interrupt: the ADR-013 primary path was
	// used, the terminal notification carried status interrupted (§21.6
	// step 6), and the run ended far sooner than the watchdog timeout.
	if !outcome.UsedAppServer {
		t.Fatal("UsedAppServer = false, want the ADR-013 primary path")
	}
	if outcome.AppServer.TurnStatus != string(appserver.TurnStatusInterrupted) {
		t.Fatalf("TurnStatus = %q, want %q", outcome.AppServer.TurnStatus, appserver.TurnStatusInterrupted)
	}
	if outcome.AppServer.ConnectionLost {
		t.Fatal("ConnectionLost = true — the graceful interrupt should keep the terminal observable")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("run took %v — the auto-pause interrupt did not stop the turn", elapsed)
	}

	// The frozen lifecycle ran end to end (§21.6 steps 3, 4, 7): durable
	// Sleeping record, both checkpoint links, durable wake job.
	records := readPauseRecords(t, harness.db)
	if len(records) != 1 {
		t.Fatalf("pause_records rows = %d, want exactly 1", len(records))
	}
	rec := records[0]
	if rec.Status != string(domain.PauseSleeping) {
		t.Fatalf("pause record status = %q, want %q", rec.Status, domain.PauseSleeping)
	}
	if !rec.StateCkpt.Valid || rec.StateCkpt.String == "" {
		t.Fatal("pause record has no state_checkpoint_id — §21.6 step 3 did not land")
	}
	if !rec.RepoCkpt.Valid || rec.RepoCkpt.String == "" {
		t.Fatal("pause record has no repository_checkpoint_id — §21.6 step 4 did not land")
	}
	if _, found, err := harness.wakes.GetByPauseKind(context.Background(), domain.PauseID(rec.ID), "pause_resume"); err != nil || !found {
		t.Fatalf("wake job lookup: found=%v err=%v — Sleeping without a durable wake job (§21.6 step 7)", found, err)
	}

	// The terminal batch persisted the honest interrupted event, with the
	// thread_id resume locator on the session.started event (what the
	// follow-up resume slice reads).
	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2 (gate, terminal)", len(persister.calls))
	}
	terminal := persister.calls[1]
	if ev := eventOfType(t, terminal, v1.EventProviderTurnInterrupted); ev.Payload["turn_status"] != string(appserver.TurnStatusInterrupted) {
		t.Errorf("turn.interrupted payload = %+v", ev.Payload)
	}
	if ev := eventOfType(t, terminal, v1.EventProviderSessionStarted); ev.Payload["thread_id"] != "th_fake" {
		t.Errorf("session.started payload = %+v, want the thread_id resume locator", ev.Payload)
	}
}
