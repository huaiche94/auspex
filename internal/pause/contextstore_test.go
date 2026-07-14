// contextstore_test.go: the durable pauseContext round-trip (contextstore.go,
// #7/D-16) — internal (package pause) because pauseContext itself is
// unexported by design; the cross-process behavior it enables is proven at
// the daemon level, this file proves the storage layer alone.
package pause

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

func openMigratedDBInternal(t *testing.T) *sqlite.DB {
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
	return db
}

// seedContextChain mirrors persistphase_test.go's seedChain (that helper is
// package pause_test; not importable here) plus one active pause record.
func seedContextChain(t *testing.T, db *sqlite.DB, store *SQLiteStore) domain.PauseID {
	t.Helper()
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
	rec := PauseRecord{
		ID:     "pause-ctx-1",
		Key:    PauseKey{TaskID: "task1", SessionID: "sess1"},
		Status: domain.PausePredicted,
		Reason: TriggerReasonCalibrated,
	}
	if err := store.Insert(context.Background(), rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return rec.ID
}

func testPauseContext() pauseContext {
	used := 87.5
	window := int64(604800)
	resets := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	return pauseContext{
		TaskID:          "task1",
		WorktreeID:      "wt1",
		PausedWorkPaths: []string{"internal/pause/service.go", "docs/plan.md"},
		GitHeadBaseline: "abc123def",
		QuotaBaseline: domain.QuotaObservation{
			ID: "obs1", SessionID: "sess1", Provider: "claude-code",
			LimitID: "seven_day", LimitName: "Weekly limit",
			UsedPercent: &used, WindowSeconds: &window, ResetsAt: &resets,
			Reached: false, Source: domain.MeasurementSource("provider"),
			Confidence: domain.ConfidenceExact,
			ObservedAt: time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC),
		},
	}
}

func TestSQLiteStore_ContextRoundTrips(t *testing.T) {
	db := openMigratedDBInternal(t)
	store := NewSQLiteStore(db)
	id := seedContextChain(t, db, store)
	ctx := context.Background()

	// Absent before SaveContext: found=false, not an error.
	if _, found, err := store.LoadContext(ctx, id); err != nil || found {
		t.Fatalf("LoadContext before save = found %v, err %v; want false, nil", found, err)
	}

	want := testPauseContext()
	if err := store.SaveContext(ctx, id, want); err != nil {
		t.Fatalf("SaveContext: %v", err)
	}
	got, found, err := store.LoadContext(ctx, id)
	if err != nil || !found {
		t.Fatalf("LoadContext = found %v, err %v; want true, nil", found, err)
	}
	assertContextEqual(t, got, want)

	// Unknown pause id: found=false / not-found error, never a fabricated context.
	if _, found, err := store.LoadContext(ctx, "no-such-pause"); err != nil || found {
		t.Fatalf("LoadContext unknown id = found %v, err %v; want false, nil", found, err)
	}
	if err := store.SaveContext(ctx, "no-such-pause", want); err == nil {
		t.Fatal("SaveContext unknown id: want not-found error, got nil")
	}
}

// TestSQLiteStore_ContextSurvivesSaveProgress is the regression this file
// exists for: SaveProgress rewrites metadata_json (it always did); with a
// third writer on the same column, each writer must preserve the others'
// keys — a wake-scheduled pause whose persist phase runs AFTER SaveContext
// must not lose the context its unattended resume depends on.
func TestSQLiteStore_ContextSurvivesSaveProgress(t *testing.T) {
	db := openMigratedDBInternal(t)
	store := NewSQLiteStore(db)
	id := seedContextChain(t, db, store)
	ctx := context.Background()

	want := testPauseContext()
	if err := store.SaveContext(ctx, id, want); err != nil {
		t.Fatalf("SaveContext: %v", err)
	}

	ckpt := domain.StateCheckpointID("sc1")
	wakeJob := domain.WakeJobID("wj1")
	if err := store.SaveProgress(ctx, id, PersistProgress{
		ProgressSnapshotTaken: true,
		StateCheckpointID:     &ckpt,
		PauseRecordSaved:      true,
		WakeJobID:             &wakeJob,
	}); err != nil {
		t.Fatalf("SaveProgress: %v", err)
	}

	// Context survived the progress rewrite…
	got, found, err := store.LoadContext(ctx, id)
	if err != nil || !found {
		t.Fatalf("LoadContext after SaveProgress = found %v, err %v; want true, nil", found, err)
	}
	assertContextEqual(t, got, want)

	// …and the progress + reason keys survived the context write.
	progress, found, err := store.GetProgress(ctx, id)
	if err != nil || !found {
		t.Fatalf("GetProgress = found %v, err %v; want true, nil", found, err)
	}
	if !progress.ProgressSnapshotTaken || !progress.PauseRecordSaved ||
		progress.WakeJobID == nil || *progress.WakeJobID != wakeJob ||
		progress.StateCheckpointID == nil || *progress.StateCheckpointID != ckpt {
		t.Errorf("GetProgress after context write = %+v, want all persisted fields intact", progress)
	}
	rec, found, err := store.GetByID(ctx, id)
	if err != nil || !found {
		t.Fatalf("GetByID = found %v, err %v; want true, nil", found, err)
	}
	if rec.Reason != TriggerReasonCalibrated {
		t.Errorf("Reason after both writers = %q, want %q", rec.Reason, TriggerReasonCalibrated)
	}
}

func assertContextEqual(t *testing.T, got, want pauseContext) {
	t.Helper()
	if got.TaskID != want.TaskID || got.WorktreeID != want.WorktreeID || got.GitHeadBaseline != want.GitHeadBaseline {
		t.Errorf("context scalars = %+v, want %+v", got, want)
	}
	if len(got.PausedWorkPaths) != len(want.PausedWorkPaths) {
		t.Fatalf("PausedWorkPaths = %v, want %v", got.PausedWorkPaths, want.PausedWorkPaths)
	}
	for i := range want.PausedWorkPaths {
		if got.PausedWorkPaths[i] != want.PausedWorkPaths[i] {
			t.Errorf("PausedWorkPaths[%d] = %q, want %q", i, got.PausedWorkPaths[i], want.PausedWorkPaths[i])
		}
	}
	g, w := got.QuotaBaseline, want.QuotaBaseline
	if g.ID != w.ID || g.SessionID != w.SessionID || g.Provider != w.Provider ||
		g.LimitID != w.LimitID || g.LimitName != w.LimitName ||
		g.Reached != w.Reached || g.Source != w.Source || g.Confidence != w.Confidence ||
		!g.ObservedAt.Equal(w.ObservedAt) {
		t.Errorf("QuotaBaseline scalars = %+v, want %+v", g, w)
	}
	if g.UsedPercent == nil || *g.UsedPercent != *w.UsedPercent {
		t.Errorf("UsedPercent = %v, want %v", g.UsedPercent, w.UsedPercent)
	}
	if g.WindowSeconds == nil || *g.WindowSeconds != *w.WindowSeconds {
		t.Errorf("WindowSeconds = %v, want %v", g.WindowSeconds, w.WindowSeconds)
	}
	if g.ResetsAt == nil || !g.ResetsAt.Equal(*w.ResetsAt) {
		t.Errorf("ResetsAt = %v, want %v", g.ResetsAt, w.ResetsAt)
	}
}
