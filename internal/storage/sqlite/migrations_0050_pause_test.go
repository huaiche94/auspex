package sqlite_test

// runtime-a01: tests for runtime Part A's migration range 0050-0059
// (pause_records, wake_jobs, resume_attempts — Auspex_ADD.md §12.2,
// CONTRACT_FREEZE.md "Migration ranges"). Kept in a separate file from
// foundation's migrate_test.go so each role's tests stay recognizably
// theirs inside this shared test package; test names deliberately contain
// "Migration0050" so `go test ./internal/storage/sqlite/... -run
// Migration0050` (EXECUTION_DAG.md runtime-a01's validation command)
// selects exactly these.
//
// See 0050_pause_records.sql's header for why turn_id /
// runway_forecast_id / state_checkpoint_id / repository_checkpoint_id are
// plain TEXT pointers (no FK) until the 0010-0049 ranges land — these
// tests exercise the constraints that ARE declared (FKs into foundation's
// tasks/provider_sessions, this range's own cross-table FKs, NOT NULL, and
// UNIQUE(pause_id, job_kind)) against the real embedded migration set.

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// --- embedded-file loading and application ---------------------------------

func TestMigration0050_AllMigrationsIncludesPauseRange(t *testing.T) {
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	want := map[int]string{
		50: "pause_records",
		51: "wake_jobs",
		52: "resume_attempts",
	}
	got := make(map[int]string, len(want))
	for _, m := range migrations {
		if m.Version >= 50 && m.Version <= 59 {
			got[m.Version] = m.Name
		}
	}
	if len(got) != len(want) {
		t.Fatalf("runtime range 0050-0059 has %d migrations %v, want %d %v", len(got), got, len(want), want)
	}
	for version, name := range want {
		if got[version] != name {
			t.Errorf("migration %04d = %q, want %q", version, got[version], name)
		}
	}
}

func TestMigration0050_ApplyFromEmptyDatabase(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	version, err := db.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version < 52 {
		t.Errorf("CurrentVersion = %d, want >= 52 (resume_attempts, the highest runtime-a01 migration)", version)
	}

	for _, table := range []string{"pause_records", "wake_jobs", "resume_attempts"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Conn().QueryRowContext(ctx, q, table).Scan(&name); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	// ADD §12.3 required indexes owned by this range.
	for _, index := range []string{"idx_pause_status", "idx_wake_jobs_due"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`
		if err := db.Conn().QueryRowContext(ctx, q, index).Scan(&name); err != nil {
			t.Errorf("index %s not created: %v", index, err)
		}
	}
}

func TestMigration0050_Reapply_Idempotent(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (second, idempotent): %v", err)
	}
}

// --- behavioral FK/constraint tests ------------------------------------------

// migrateAndSeedPause applies the full embedded migration set to a fresh
// temp database, seeds foundation's repository → worktree →
// provider_session → task chain, and inserts pause row 'pause1'.
func migrateAndSeedPause(t *testing.T) *sqlite.DB {
	t.Helper()
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
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
		 VALUES ('pause1', 'task1', 'sess1', 'turn1', 'rf1', 'requested', '2026-01-01T00:00:00Z', 0)`,
	}
	for _, stmt := range seed {
		if _, err := db.Conn().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	return db
}

func TestMigration0050_PauseRecords_Constraints(t *testing.T) {
	db := migrateAndSeedPause(t)
	ctx := context.Background()

	exec := func(query string, args ...any) error {
		t.Helper()
		_, err := db.Conn().ExecContext(ctx, query, args...)
		return err
	}

	// An unknown task_id must be rejected — a pause belongs to a real task.
	if err := exec(`INSERT INTO pause_records (id, task_id, session_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
	                 VALUES ('pauseX', 'task-missing', 'sess1', 'rf1', 'requested', '2026-01-01T00:00:00Z', 0)`); err == nil {
		t.Error("expected foreign key violation inserting a pause with an unknown task_id")
	}

	// An unknown session_id must be rejected.
	if err := exec(`INSERT INTO pause_records (id, task_id, session_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
	                 VALUES ('pauseX', 'task1', 'sess-missing', 'rf1', 'requested', '2026-01-01T00:00:00Z', 0)`); err == nil {
		t.Error("expected foreign key violation inserting a pause with an unknown session_id")
	}

	// runway_forecast_id is NOT NULL: the forecast that justified the pause
	// is a required audit link (ADD §20) even while its FK cannot be
	// declared yet (see 0050_pause_records.sql header).
	if err := exec(`INSERT INTO pause_records (id, task_id, session_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
	                 VALUES ('pauseX', 'task1', 'sess1', NULL, 'requested', '2026-01-01T00:00:00Z', 0)`); err == nil {
		t.Error("expected NOT NULL violation inserting a pause with a NULL runway_forecast_id")
	}

	// A pause cannot outlive its task (ON DELETE CASCADE), and the cascade
	// chain repository → worktree → task → pause must resolve cleanly.
	if err := exec(`DELETE FROM repositories WHERE id = 'repo1'`); err != nil {
		t.Fatalf("delete repository (cascade through to pause_records): %v", err)
	}
	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM pause_records`).Scan(&count); err != nil {
		t.Fatalf("count pause_records: %v", err)
	}
	if count != 0 {
		t.Errorf("pause_records count after repository delete = %d, want 0 (ON DELETE CASCADE via tasks)", count)
	}
}

func TestMigration0050_WakeJobs_CascadeAndUniqueKind(t *testing.T) {
	db := migrateAndSeedPause(t)
	ctx := context.Background()

	exec := func(query string, args ...any) error {
		t.Helper()
		_, err := db.Conn().ExecContext(ctx, query, args...)
		return err
	}

	if err := exec(`INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, max_attempts, created_at, updated_at)
	                 VALUES ('wj1', 'pause1', 'resume', 'scheduled', '2026-01-01T01:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert wake job: %v", err)
	}

	// UNIQUE(pause_id, job_kind): a second job of the same kind for the
	// same pause is the schema-level anchor for exactly-once wake behavior
	// (agents/runtime.md P0 deliverable 9).
	if err := exec(`INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, max_attempts, created_at, updated_at)
	                 VALUES ('wj2', 'pause1', 'resume', 'scheduled', '2026-01-01T02:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Error("expected unique constraint violation on duplicate (pause_id, job_kind)")
	}

	// A different kind for the same pause is allowed.
	if err := exec(`INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, max_attempts, created_at, updated_at)
	                 VALUES ('wj3', 'pause1', 'notify', 'scheduled', '2026-01-01T02:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert wake job of a second kind: %v", err)
	}

	// An unknown pause_id must be rejected.
	if err := exec(`INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, max_attempts, created_at, updated_at)
	                 VALUES ('wj4', 'pause-missing', 'resume', 'scheduled', '2026-01-01T02:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Error("expected foreign key violation inserting a wake job with an unknown pause_id")
	}

	// A wake job cannot outlive its pause.
	if err := exec(`DELETE FROM pause_records WHERE id = 'pause1'`); err != nil {
		t.Fatalf("delete pause: %v", err)
	}
	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM wake_jobs`).Scan(&count); err != nil {
		t.Fatalf("count wake_jobs: %v", err)
	}
	if count != 0 {
		t.Errorf("wake_jobs count after pause delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}

func TestMigration0050_ResumeAttempts_AuditSurvivesWakeJob(t *testing.T) {
	db := migrateAndSeedPause(t)
	ctx := context.Background()

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Conn().ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}

	exec(`INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, max_attempts, created_at, updated_at)
	      VALUES ('wj1', 'pause1', 'resume', 'leased', '2026-01-01T01:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO resume_attempts (id, pause_id, wake_job_id, status, started_at)
	      VALUES ('ra1', 'pause1', 'wj1', 'validating', '2026-01-01T01:00:01Z')`)

	// The attempt's audit row must survive its wake job (ON DELETE SET
	// NULL) — auto-resume is audited (Constitution §7 rule 9).
	exec(`DELETE FROM wake_jobs WHERE id = 'wj1'`)

	var wakeJobID *string
	if err := db.Conn().QueryRowContext(ctx, `SELECT wake_job_id FROM resume_attempts WHERE id = 'ra1'`).Scan(&wakeJobID); err != nil {
		t.Fatalf("select resume attempt: %v", err)
	}
	if wakeJobID != nil {
		t.Errorf("resume_attempts.wake_job_id = %v, want nil after wake job delete (ON DELETE SET NULL)", *wakeJobID)
	}

	// But it does not survive its pause.
	exec(`DELETE FROM pause_records WHERE id = 'pause1'`)
	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM resume_attempts`).Scan(&count); err != nil {
		t.Fatalf("count resume_attempts: %v", err)
	}
	if count != 0 {
		t.Errorf("resume_attempts count after pause delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}
