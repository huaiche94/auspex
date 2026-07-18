package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// --- migration from empty database (agents/foundation.md "Required tests") -

func TestMigrate_EmptyMigrationSet_CreatesTrackingTableOnly(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	// This node ships zero actual migration files (foundation-06 owns
	// those); Migrate against nil must still succeed and be a real,
	// tested no-op rather than an error.
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatalf("Migrate(nil): %v", err)
	}

	version, err := db.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 0 {
		t.Errorf("CurrentVersion = %d, want 0", version)
	}
}

func TestMigrate_FromEmptyDatabase_AppliesAllInOrder(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations := []sqlite.Migration{
		{Version: 2, Name: "second", SQL: `CREATE TABLE second (id INTEGER PRIMARY KEY)`},
		{Version: 1, Name: "first", SQL: `CREATE TABLE first (id INTEGER PRIMARY KEY)`},
	}

	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	version, err := db.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 2 {
		t.Errorf("CurrentVersion = %d, want 2", version)
	}

	for _, table := range []string{"first", "second"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Conn().QueryRowContext(ctx, q, table).Scan(&name); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}
}

func TestMigrate_OrderIndependentOfInputOrder(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	// A migration that would fail if applied before its dependency
	// (references a column added by version 1) proves ordering is by
	// Version, not by slice position.
	migrations := []sqlite.Migration{
		{Version: 2, Name: "add_column", SQL: `ALTER TABLE base ADD COLUMN extra TEXT`},
		{Version: 1, Name: "create_base", SQL: `CREATE TABLE base (id INTEGER PRIMARY KEY)`},
	}

	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

// --- reopen and idempotent migration (agents/foundation.md "Required
// tests") --------------------------------------------------------------

func TestMigrate_Reopen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	migrations := []sqlite.Migration{
		{Version: 1, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
	}

	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}

	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer func() { _ = db2.Close() }()

	// Reapplying the same migration set against the already-migrated
	// database must succeed without re-running migration 1's CREATE
	// TABLE (which would itself error on a second run if not properly
	// skipped).
	if err := db2.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (second, idempotent): %v", err)
	}

	version, err := db2.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("CurrentVersion = %d, want 1", version)
	}
}

func TestMigrate_Reopen_AppliesOnlyNewMigrations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer func() { _ = db2.Close() }()

	// The binary now knows about a newer migration 2 in addition to the
	// already-applied migration 1.
	if err := db2.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
		{Version: 2, Name: "create_u", SQL: `CREATE TABLE u (id INTEGER PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("Migrate (second, +1 new): %v", err)
	}

	version, err := db2.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 2 {
		t.Errorf("CurrentVersion = %d, want 2", version)
	}
}

// --- backfilled gap migrations applied (issue #22) -------------------------

func TestMigrate_Reopen_AppliesBackfilledGapMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	// First run: versions 10 and 50 applied — the database's max is 50.
	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Migrate(ctx, []sqlite.Migration{
		{Version: 10, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
		{Version: 50, Name: "create_u", SQL: `CREATE TABLE u (id INTEGER PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second run: the binary now also ships version 45 — a backfill into
	// a range BELOW the database's applied maximum (the issue-#22 shape:
	// 0045 landing after 0050-0052 shipped). Max-version semantics
	// skipped it forever; set-difference semantics must apply it.
	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer func() { _ = db2.Close() }()

	backfilled := []sqlite.Migration{
		{Version: 10, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
		{Version: 45, Name: "add_column", SQL: `ALTER TABLE t ADD COLUMN extra REAL`},
		{Version: 50, Name: "create_u", SQL: `CREATE TABLE u (id INTEGER PRIMARY KEY)`},
	}
	if err := db2.Migrate(ctx, backfilled); err != nil {
		t.Fatalf("Migrate (second, +backfilled 45): %v", err)
	}

	// The backfill's schema change is live...
	var extra *float64
	q := `SELECT extra FROM t LIMIT 1`
	if _, err := db2.Conn().ExecContext(ctx, `INSERT INTO t (id, extra) VALUES (1, 0.5)`); err != nil {
		t.Fatalf("insert into backfilled column: %v", err)
	}
	if err := db2.Conn().QueryRowContext(ctx, q).Scan(&extra); err != nil {
		t.Fatalf("read backfilled column: %v", err)
	}

	// ...its audit row is recorded...
	var name string
	row := db2.Conn().QueryRowContext(ctx, `SELECT name FROM schema_migrations WHERE version = 45`)
	if err := row.Scan(&name); err != nil {
		t.Fatalf("schema_migrations row for backfilled version 45: %v", err)
	}
	if name != "add_column" {
		t.Errorf("schema_migrations name for 45 = %q, want %q", name, "add_column")
	}

	// ...and a third run with the same set is an idempotent no-op (the
	// ALTER would error if re-executed).
	if err := db2.Migrate(ctx, backfilled); err != nil {
		t.Fatalf("Migrate (third, idempotent after backfill): %v", err)
	}
}

func TestMigrate_AppliedVersionUnknownToBinary_BelowMax_Ignored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	// A newer binary applied a backfilled version 45 alongside 10 and 50.
	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Migrate(ctx, []sqlite.Migration{
		{Version: 10, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
		{Version: 45, Name: "add_column", SQL: `ALTER TABLE t ADD COLUMN extra REAL`},
		{Version: 50, Name: "create_u", SQL: `CREATE TABLE u (id INTEGER PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// An older binary that predates the backfill (knows 10 and 50 only,
	// max 50) reopens the database. Version 45 is applied-but-unknown,
	// yet sits below the binary's own max — that is a backfill this
	// binary predates, not a database from the future, so Migrate must
	// no-op cleanly rather than fail or (worse) try to reconcile it. The
	// fail-closed ErrSchemaNewerThanBinary check stays keyed on the
	// MAXIMUM applied version only.
	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer func() { _ = db2.Close() }()

	if err := db2.Migrate(ctx, []sqlite.Migration{
		{Version: 10, Name: "create_t", SQL: `CREATE TABLE t (id INTEGER PRIMARY KEY)`},
		{Version: 50, Name: "create_u", SQL: `CREATE TABLE u (id INTEGER PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("Migrate (older binary, applied-unknown 45 below max): %v", err)
	}
}

// --- newer schema rejected safely (agents/foundation.md "Required tests") --

func TestMigrate_DatabaseNewerThanBinary_RejectsSafely(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	// Simulate a database previously migrated by a newer binary that
	// knew about migration version 5.
	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Migrate(ctx, []sqlite.Migration{
		{Version: 5, Name: "future", SQL: `CREATE TABLE future_table (id INTEGER PRIMARY KEY)`},
	}); err != nil {
		t.Fatalf("Migrate (simulate future binary): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen with an "older" binary that only knows migrations up to
	// version 2.
	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer func() { _ = db2.Close() }()

	err = db2.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "one", SQL: `CREATE TABLE one (id INTEGER PRIMARY KEY)`},
		{Version: 2, Name: "two", SQL: `CREATE TABLE two (id INTEGER PRIMARY KEY)`},
	})
	if !errors.Is(err, sqlite.ErrSchemaNewerThanBinary) {
		t.Fatalf("err = %v, want ErrSchemaNewerThanBinary", err)
	}

	// Fail-closed: nothing from the "older" binary's migration set
	// should have been applied.
	var name string
	q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'one'`
	err = db2.Conn().QueryRowContext(ctx, q).Scan(&name)
	if err == nil {
		t.Error("migration 'one' should not have been applied when schema is newer than binary")
	}
}

// --- Migration application failure leaves state consistent -----------------

func TestMigrate_FailingMigration_DoesNotRecordVersion(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	err := db.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "bad", SQL: `THIS IS NOT VALID SQL`},
	})
	if err == nil {
		t.Fatal("expected error applying invalid migration SQL")
	}

	version, verErr := db.CurrentVersion(ctx)
	if verErr != nil {
		t.Fatalf("CurrentVersion: %v", verErr)
	}
	if version != 0 {
		t.Errorf("CurrentVersion = %d, want 0 (failed migration must not be recorded)", version)
	}
}

// TestMigration_PartialBatchFailure_RollsBackEntireBatch is a foundation-07
// regression test for a deliberate behavior change made alongside the
// concurrency fix above: Migrate used to apply each migration in its own
// independent transaction (foundation-05/06), so a batch of [migration 1
// (valid), migration 2 (invalid)] would leave migration 1's effects
// PERMANENTLY committed even though Migrate returned an error for
// migration 2 — a partially-applied migration run, which
// CONTRACT_FREEZE.md's error contract calls out as exactly the kind of
// state-integrity failure that MUST fail closed, not partially succeed.
// Migrate now runs the entire read-then-apply-all-pending-migrations
// sequence as one transaction (BEGIN IMMEDIATE ... COMMIT), so a failure
// on ANY migration in the batch rolls back every migration in that same
// Migrate call, not just the failing one.
func TestMigration_PartialBatchFailure_RollsBackEntireBatch(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	err := db.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "good", SQL: `CREATE TABLE good (id INTEGER PRIMARY KEY)`},
		{Version: 2, Name: "bad", SQL: `THIS IS NOT VALID SQL`},
	})
	if err == nil {
		t.Fatal("expected error applying a batch with an invalid migration")
	}

	version, verErr := db.CurrentVersion(ctx)
	if verErr != nil {
		t.Fatalf("CurrentVersion: %v", verErr)
	}
	if version != 0 {
		t.Errorf("CurrentVersion = %d, want 0 (entire batch must roll back, not just the failing migration)", version)
	}

	var name string
	q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'good'`
	if scanErr := db.Conn().QueryRowContext(ctx, q).Scan(&name); scanErr == nil {
		t.Error("table 'good' from migration 1 must not exist: its transaction should have rolled back alongside migration 2's failure")
	}
}

// --- LoadMigrationsFS --------------------------------------------------

func TestLoadMigrationsFS_ParsesAndSorts(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0002_second.sql": &fstest.MapFile{Data: []byte("CREATE TABLE second (id INTEGER);")},
		"migrations/0000_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE first (id INTEGER);")},
		"migrations/README.md":       &fstest.MapFile{Data: []byte("not a migration")},
	}

	migrations, err := sqlite.LoadMigrationsFS(fsys, "migrations")
	if err != nil {
		t.Fatalf("LoadMigrationsFS: %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("len(migrations) = %d, want 2 (README.md must be skipped)", len(migrations))
	}
	if migrations[0].Version != 0 || migrations[0].Name != "first" {
		t.Errorf("migrations[0] = %+v, want version 0 name first", migrations[0])
	}
	if migrations[1].Version != 2 || migrations[1].Name != "second" {
		t.Errorf("migrations[1] = %+v, want version 2 name second", migrations[1])
	}
}

func TestLoadMigrationsFS_InvalidFilename_Errors(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/not-numbered.sql": &fstest.MapFile{Data: []byte("CREATE TABLE x (id INTEGER);")},
	}

	_, err := sqlite.LoadMigrationsFS(fsys, "migrations")
	if !errors.Is(err, sqlite.ErrInvalidMigrationFilename) {
		t.Errorf("err = %v, want ErrInvalidMigrationFilename", err)
	}
}

func TestLoadMigrationsFS_DuplicateVersion_Errors(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0001_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE a (id INTEGER);")},
		"migrations/0001_second.sql": &fstest.MapFile{Data: []byte("CREATE TABLE b (id INTEGER);")},
	}

	_, err := sqlite.LoadMigrationsFS(fsys, "migrations")
	if !errors.Is(err, sqlite.ErrDuplicateMigrationVersion) {
		t.Errorf("err = %v, want ErrDuplicateMigrationVersion", err)
	}
}

func TestLoadMigrationsFS_EmptyDir_ReturnsEmptySlice(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/.gitkeep": &fstest.MapFile{Data: []byte("")},
	}

	migrations, err := sqlite.LoadMigrationsFS(fsys, "migrations")
	if err != nil {
		t.Fatalf("LoadMigrationsFS: %v", err)
	}
	if len(migrations) != 0 {
		t.Errorf("len(migrations) = %d, want 0", len(migrations))
	}
}

// --- foundation-06: real embedded core-schema migrations --------------------
//
// The tests above cover the migration engine itself (foundation-05) against
// synthetic in-memory Migration values. These exercise AllMigrations()'s
// embed.FS wiring against the actual migrations/0000-0003_*.sql files
// (repositories, worktrees, provider_sessions, tasks) — the highest-risk
// deliverable of foundation-06 per EXECUTION_DAG.md ("every feature role's
// migrations FK into these tables"). Names deliberately contain "Migration"
// so `go test ./internal/storage/sqlite/... -run Migration` (the DAG's
// foundation-06 validation command) selects them.

func TestAllMigrations_LoadsCoreSchemaFiles(t *testing.T) {
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	// AllMigrations() loads the real embedded migrations/ directory, which
	// by design also picks up every other role's migration files as they
	// land there (claude-provider 0010-0019, checkpoint 0020-0039,
	// predictor 0040-0049, runtime 0050-0059 — see CONTRACT_FREEZE.md and
	// migrate.go's AllMigrations doc comment). This test only owns
	// asserting that foundation's own four migrations are present, sorted
	// first, and correctly named — not that they are the ONLY migrations
	// that exist, since that stops being true the moment any sibling
	// role's migration lands in the same tree.
	want := []struct {
		version int
		name    string
	}{
		{1, "repositories"},
		{2, "worktrees"},
		{3, "provider_sessions"},
		{4, "tasks"},
	}
	if len(migrations) < len(want) {
		t.Fatalf("len(migrations) = %d, want at least %d (%+v)", len(migrations), len(want), migrations)
	}
	for i, w := range want {
		if migrations[i].Version != w.version || migrations[i].Name != w.name {
			t.Errorf("migrations[%d] = {Version:%d Name:%s}, want {Version:%d Name:%s}",
				i, migrations[i].Version, migrations[i].Name, w.version, w.name)
		}
	}
}

func TestCoreMigrations_FromEmptyDatabase(t *testing.T) {
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
	// >= 4, not == 4: AllMigrations() loads the real embedded migrations/
	// directory, which also picks up every other role's migration files as
	// they land (0010-0019 claude-provider, 0020-0039 checkpoint, 0040-0049
	// predictor, 0050-0059 runtime, per CONTRACT_FREEZE.md). Those always
	// sort after foundation's own 0001-0004 range and only raise
	// CurrentVersion, never lower it, so this test's actual intent — "the
	// core foundation tables were created correctly" (verified below) —
	// only requires that foundation's migrations applied, not that they
	// were the only ones present.
	if version < 4 {
		t.Errorf("CurrentVersion = %d, want at least 4 (tasks, the highest foundation-06 migration)", version)
	}

	for _, table := range []string{"repositories", "worktrees", "provider_sessions", "tasks"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Conn().QueryRowContext(ctx, q, table).Scan(&name); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}
}

func TestCoreMigrations_Reopen_Idempotent(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	// Re-running against an already-migrated database must be a no-op, not
	// a "table already exists" error.
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (second, idempotent): %v", err)
	}
}

func TestCoreMigrations_ForeignKeys_RepositoryToWorktree(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// A worktree referencing a nonexistent repository must be rejected:
	// foreign_keys = ON (db.go's pragma set) plus this migration's FK
	// constraint is what every later role's migration range depends on
	// holding.
	_, err = db.Conn().ExecContext(ctx,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'does-not-exist', '/tmp/x', '/tmp/x/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	)
	if err == nil {
		t.Fatal("expected a foreign key violation inserting a worktree with an unknown repository_id")
	}

	// The happy path: insert a repository, then a worktree referencing it,
	// then confirm ON DELETE CASCADE removes the worktree when the
	// repository is deleted.
	if _, err := db.Conn().ExecContext(ctx,
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo', '/tmp/repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if _, err := db.Conn().ExecContext(ctx,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'repo1', '/tmp/repo', '/tmp/repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert worktree: %v", err)
	}
	if _, err := db.Conn().ExecContext(ctx, `DELETE FROM repositories WHERE id = 'repo1'`); err != nil {
		t.Fatalf("delete repository: %v", err)
	}

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM worktrees WHERE id = 'wt1'`).Scan(&count); err != nil {
		t.Fatalf("count worktrees: %v", err)
	}
	if count != 0 {
		t.Errorf("worktree count after repository delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}

func TestCoreMigrations_ForeignKeys_TaskSessionSetNull(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Conn().ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}

	exec(`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
	      VALUES ('repo1', '/tmp/repo', '/tmp/repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
	      VALUES ('wt1', 'repo1', '/tmp/repo', '/tmp/repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
	      VALUES ('sess1', 'wt1', 'claude-code', 'interactive', '2026-01-01T00:00:00Z', '{}')`)
	// tasks.session_id is nullable and ON DELETE SET NULL (ADD §12.2): a
	// task must outlive the provider_sessions row that started it.
	exec(`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
	      VALUES ('task1', 'sess1', 'wt1', 'hash1', 'pending', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)

	exec(`DELETE FROM provider_sessions WHERE id = 'sess1'`)

	var sessionID *string
	if err := db.Conn().QueryRowContext(ctx, `SELECT session_id FROM tasks WHERE id = 'task1'`).Scan(&sessionID); err != nil {
		t.Fatalf("select task: %v", err)
	}
	if sessionID != nil {
		t.Errorf("tasks.session_id = %v, want nil after provider_sessions delete (ON DELETE SET NULL)", *sessionID)
	}

	var taskCount int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE id = 'task1'`).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("task count after session delete = %d, want 1 (task must survive)", taskCount)
	}
}

func TestCoreMigrations_UniqueConstraints(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	exec := func(query string, args ...any) error {
		t.Helper()
		_, err := db.Conn().ExecContext(ctx, query, args...)
		return err
	}

	// repositories.git_common_dir is UNIQUE.
	if err := exec(`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
	                 VALUES ('repo1', '/tmp/a', '/tmp/shared/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert repo1: %v", err)
	}
	if err := exec(`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
	                 VALUES ('repo2', '/tmp/b', '/tmp/shared/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Error("expected unique constraint violation on duplicate git_common_dir")
	}

	// worktrees UNIQUE(repository_id, root_path).
	if err := exec(`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
	                 VALUES ('wt1', 'repo1', '/tmp/a', '/tmp/a/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert wt1: %v", err)
	}
	if err := exec(`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
	                 VALUES ('wt2', 'repo1', '/tmp/a', '/tmp/a/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Error("expected unique constraint violation on duplicate (repository_id, root_path)")
	}
}

func TestCoreMigrations_ReopenFromFile_AppliesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := db1.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}

	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (second): %v", err)
	}
	defer func() { _ = db2.Close() }()

	if err := db2.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (second, reopen): %v", err)
	}

	version, err := db2.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	// >= 4, not == 4: see TestCoreMigrations_FromEmptyDatabase above — this
	// test's intent is "reopening a migrated DB and re-running Migrate is
	// idempotent," which holds regardless of how many additional sibling-
	// role migrations AllMigrations() also loaded.
	if version < 4 {
		t.Errorf("CurrentVersion = %d, want at least 4", version)
	}
}

// --- foundation-07: migration test harness hardening ------------------------
//
// The tests above (foundation-05/06) cover the migration engine's
// single-caller correctness: apply-from-empty, reopen-and-idempotent
// (sequentially, one *DB at a time), newer-schema-rejected, and
// invalid-permissions/corrupt-DB classification during Open (db_test.go).
// None of them exercise the engine under genuine concurrent access, or
// distinguish a failure surfacing during Migrate itself (as opposed to
// during Open, before Migrate is ever called) — both explicitly named in
// agents/foundation.md's "Required tests" list. These tests close that
// gap. Names are prefixed TestMigration_ (matching this phase's DAG
// validation command `-run TestMigration`, distinct from the existing
// TestMigrate_/TestCoreMigrations_ prefixes used by foundation-05/06).

// TestMigration_ConcurrentReopen_SerializesAndConverges opens the SAME
// on-disk database file from several goroutines simultaneously (as two
// independent *sql.DB connections, i.e. the same shape a CLI invocation
// racing a daemon startup would produce) and has every one of them call
// Migrate with the real embedded migration set at once.
//
// This is a REAL BUG this node found in the prior (foundation-05/06)
// Migrate implementation: it read the database's current version and then
// applied migrations as separate, unsynchronized operations, so two
// concurrent callers could both observe current=0 before either committed
// and both attempt to CREATE TABLE, failing with "table already exists".
// Fixed in this same node (migrate.go) by having Migrate reserve a single
// connection and issue BEGIN IMMEDIATE before reading the current version,
// holding SQLite's write lock for the whole read-then-apply sequence so a
// second concurrent Migrate call blocks (per busy_timeout) rather than
// racing. This test is the regression guard for that fix — run with -race
// per this phase's validation command to also catch any Go-level data race
// in the harness itself, though the interesting bug here was a SQLite
// transaction race, not a Go memory race.
func TestMigration_ConcurrentReopen_SerializesAndConverges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db, openErr := sqlite.Open(ctx, path)
			if openErr != nil {
				errs[i] = openErr
				return
			}
			defer func() { _ = db.Close() }()
			errs[i] = db.Migrate(ctx, migrations)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: Migrate error: %v", i, err)
		}
	}

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	version, err := db.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	// >= 4, not == 4: see TestCoreMigrations_FromEmptyDatabase above — this
	// test's intent is "concurrent Migrate callers converge on the same
	// applied version instead of racing," which holds regardless of how
	// many additional sibling-role migrations AllMigrations() also loaded.
	if version < 4 {
		t.Errorf("CurrentVersion = %d, want at least 4 (converged despite concurrent callers)", version)
	}

	// Re-running Migrate once more against the now-converged database must
	// still be a clean idempotent no-op, proving concurrent access didn't
	// leave schema_migrations in a state a subsequent normal reopen chokes
	// on.
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate (post-convergence, idempotent): %v", err)
	}
}

// TestMigration_ConcurrentReopen_NoDuplicateSchemaMigrationsRows confirms
// the concurrent scenario above doesn't just "not error" but produces
// exactly one schema_migrations row per migration version — a duplicate
// row (e.g. two callers both inserting version 1) would be a
// state-integrity bug even if every individual Migrate call happened to
// return nil.
func TestMigration_ConcurrentReopen_NoDuplicateSchemaMigrationsRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, openErr := sqlite.Open(ctx, path)
			if openErr != nil {
				return
			}
			defer func() { _ = db.Close() }()
			_ = db.Migrate(ctx, migrations)
		}()
	}
	wg.Wait()

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var rowCount, distinctVersions int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations`).Scan(&rowCount, &distinctVersions); err != nil {
		t.Fatalf("querying schema_migrations: %v", err)
	}
	if rowCount != len(migrations) {
		t.Errorf("schema_migrations row count = %d, want %d", rowCount, len(migrations))
	}
	if rowCount != distinctVersions {
		t.Errorf("schema_migrations has %d rows but only %d distinct versions (duplicate rows)", rowCount, distinctVersions)
	}
}

// --- locked/busy behavior during migration specifically (agents/
// foundation.md "Required tests": "locked/busy behavior" — db_test.go's
// TestBusyTimeout_ConcurrentWriteWaitsInsteadOfFailingImmediately already
// covers this for a plain INSERT; this covers it for Migrate itself) -------

// TestMigration_BlocksBehindHolderTransaction_ThenSucceeds proves Migrate
// itself (not just an arbitrary write) waits behind another connection's
// uncommitted write lock rather than failing immediately with
// SQLITE_BUSY, and succeeds once that lock is released — well within
// db.go's busy_timeout pragma. This specifically exercises Migrate's own
// BEGIN IMMEDIATE acquisition path added by this node, distinct from a
// plain ExecContext call.
func TestMigration_BlocksBehindHolderTransaction_ThenSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	dbHolder, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (holder): %v", err)
	}
	defer func() { _ = dbHolder.Close() }()
	dbMigrator, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (migrator): %v", err)
	}
	defer func() { _ = dbMigrator.Close() }()

	// Establish schema_migrations up front so the holder's write lock
	// below is the ONLY thing standing between the migrator and applying
	// migration 1.
	if err := dbHolder.Migrate(ctx, nil); err != nil {
		t.Fatalf("prep Migrate: %v", err)
	}

	tx, err := dbHolder.Conn().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE holder (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("holder create: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO holder (id) VALUES (1)`); err != nil {
		t.Fatalf("holder insert: %v", err)
	}

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- dbMigrator.Migrate(ctx, []sqlite.Migration{
			{Version: 1, Name: "x", SQL: `CREATE TABLE x (id INTEGER PRIMARY KEY)`},
		})
	}()

	// Give the migrator goroutine time to actually reach and block on
	// BEGIN IMMEDIATE before releasing the holder's lock, so a "returned
	// immediately without waiting" bug would show up as an elapsed time
	// far below this sleep, not just a coincidentally-fast success.
	const holdFor = 300 * time.Millisecond
	time.Sleep(holdFor)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit holder tx: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Migrate while blocked behind holder: %v", err)
		}
		if elapsed := time.Since(start); elapsed < holdFor-50*time.Millisecond {
			t.Errorf("Migrate returned after %v, want >= ~%v (did it actually wait for the write lock?)", elapsed, holdFor)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Migrate did not complete within busy_timeout + margin: it must wait, not deadlock")
	}

	version, err := dbMigrator.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("CurrentVersion = %d, want 1 (migration applied after lock released)", version)
	}
}

// --- invalid permissions and corrupt DB error classification DURING
// migration specifically (agents/foundation.md "Required tests" —
// db_test.go's TestOpen_CorruptFile_FailsOnFirstQuery and
// TestOpen_UnwritableDirectory_Errors already cover this for Open itself;
// these cover the case where Open succeeds but the subsequent Migrate call
// is what fails) -------------------------------------------------------------

// TestMigration_CorruptDatabase_FailsDuringMigrateNotOpen builds a
// legitimate multi-page database, closes it, then corrupts only a LATE
// page of the file (leaving the header and first page — which is all
// db.go's pragma statements touch — intact). Open succeeds (pragmas only
// read/write header-adjacent state), but Migrate's own read of the current
// schema version must fail with a classified, non-nil error rather than
// silently reporting an incorrect version or panicking. This is the
// "corrupt DB error classification" required test, specifically for the
// migration path rather than Open's already-covered path.
func TestMigration_CorruptDatabase_FailsDuringMigrateNotOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (build legitimate file): %v", err)
	}
	if _, err := db.Conn().ExecContext(ctx, `CREATE TABLE filler (id INTEGER PRIMARY KEY, data TEXT)`); err != nil {
		t.Fatalf("create filler: %v", err)
	}
	// Enough rows to force the file across several pages, so a late-file
	// corruption lands past the schema/header pages Open's pragmas touch.
	for i := 0; i < 2000; i++ {
		if _, err := db.Conn().ExecContext(ctx, `INSERT INTO filler (data) VALUES (?)`,
			"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"); err != nil {
			t.Fatalf("insert filler: %v", err)
		}
	}
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatalf("prep Migrate: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file to corrupt: %v", err)
	}
	if len(data) < 20000 {
		t.Fatalf("file too small (%d bytes) to safely corrupt only late pages; filler loop needs adjusting", len(data))
	}
	// Corrupt the last 20% of the file; the first 80% (schema, page 1,
	// early filler pages) stays intact so Open's pragma statements still
	// succeed.
	start := len(data) * 8 / 10
	for i := start; i < len(data); i++ {
		data[i] = 0xFF
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing corrupted file: %v", err)
	}

	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		// If a future modernc.org/sqlite version starts detecting this
		// corruption during Open (e.g. a stricter pragma implementation),
		// that is a stronger guarantee, not a regression — but it would
		// mean this test is no longer exercising the Migrate-specific
		// path it's named for.
		t.Fatalf("Open unexpectedly failed on late-page corruption (expected Open to succeed and Migrate to fail): %v", err)
	}
	defer func() { _ = db2.Close() }()

	err = db2.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "x", SQL: `CREATE TABLE x (id INTEGER PRIMARY KEY)`},
	})
	if err == nil {
		t.Fatal("expected Migrate to fail against a corrupted database file")
	}
	t.Logf("Migrate correctly classified corruption as an error: %v", err)
}

// TestMigration_ReadOnlyFile_FailsDuringMigrateNotOpen covers the
// "invalid permissions" half of the same required-test bullet:
// TestOpen_UnwritableDirectory_Errors (db_test.go) proves Open fails when
// the DIRECTORY can't be written to (file creation fails). This proves the
// complementary case: the directory is writable and the file already
// exists, so Open itself succeeds (it only needs read access plus a
// WAL-mode pragma set, which SQLite permits read-only-ish against an
// existing well-formed file up to a point), but Migrate's first write
// (CREATE TABLE IF NOT EXISTS schema_migrations) must fail with a
// classified permissions error once the file itself is read-only.
func TestMigration_ReadOnlyFile_FailsDuringMigrateNotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	ctx := context.Background()

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (create file): %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod file read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open unexpectedly failed on a read-only (but existing, well-formed) file: %v", err)
	}
	defer func() { _ = db2.Close() }()

	err = db2.Migrate(ctx, []sqlite.Migration{
		{Version: 1, Name: "x", SQL: `CREATE TABLE x (id INTEGER PRIMARY KEY)`},
	})
	if err == nil {
		t.Fatal("expected Migrate to fail against a read-only database file")
	}
	t.Logf("Migrate correctly classified read-only permissions as an error: %v", err)
}
