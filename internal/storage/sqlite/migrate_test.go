package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/huaiche94/preflight/internal/storage/sqlite"
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
	path := filepath.Join(dir, "preflight.db")
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
	path := filepath.Join(dir, "preflight.db")
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

// --- newer schema rejected safely (agents/foundation.md "Required tests") --

func TestMigrate_DatabaseNewerThanBinary_RejectsSafely(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preflight.db")
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
