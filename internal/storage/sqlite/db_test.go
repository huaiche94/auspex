package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

func TestOpen_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")

	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected database file to exist: %v", err)
	}
	if db.Path() != path {
		t.Errorf("Path() = %q, want %q", db.Path(), path)
	}
}

// --- Pragma behavior (High risk per EXECUTION_DAG.md: "WAL/busy-timeout/FK
// pragmas are load-bearing for every later role") ---------------------------

func TestOpen_JournalModeIsWAL(t *testing.T) {
	db := openTemp(t)

	var mode string
	if err := db.Conn().QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("querying journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestOpen_ForeignKeysOn(t *testing.T) {
	db := openTemp(t)

	var fk int
	if err := db.Conn().QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("querying foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1 (ON)", fk)
	}
}

func TestOpen_BusyTimeoutSet(t *testing.T) {
	db := openTemp(t)

	var timeout int
	if err := db.Conn().QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("querying busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestOpen_SynchronousNormal(t *testing.T) {
	db := openTemp(t)

	// SQLite reports synchronous as an integer: 0=OFF, 1=NORMAL, 2=FULL.
	var sync int
	if err := db.Conn().QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatalf("querying synchronous: %v", err)
	}
	if sync != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}
}

func TestOpen_TempStoreMemory(t *testing.T) {
	db := openTemp(t)

	// SQLite reports temp_store as an integer: 0=DEFAULT, 1=FILE, 2=MEMORY.
	var tempStore int
	if err := db.Conn().QueryRowContext(context.Background(), "PRAGMA temp_store").Scan(&tempStore); err != nil {
		t.Fatalf("querying temp_store: %v", err)
	}
	if tempStore != 2 {
		t.Errorf("temp_store = %d, want 2 (MEMORY)", tempStore)
	}
}

// TestPragmas_ApplyAcrossMultipleConnections opens two independent *DB
// handles against the SAME file-backed database and confirms both observe
// WAL mode and the busy_timeout — i.e. the pragmas are not an artifact of
// a single lucky connection but genuinely apply to every connection any
// process opens against this database file, which is what every later
// role's storage code depends on for concurrent daemon+CLI access.
func TestPragmas_ApplyAcrossMultipleConnections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.db")
	ctx := context.Background()

	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open db1: %v", err)
	}
	defer func() { _ = db1.Close() }()

	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open db2: %v", err)
	}
	defer func() { _ = db2.Close() }()

	for i, db := range []*sqlite.DB{db1, db2} {
		var mode string
		if err := db.Conn().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatalf("db%d: querying journal_mode: %v", i+1, err)
		}
		if mode != "wal" {
			t.Errorf("db%d journal_mode = %q, want wal", i+1, mode)
		}

		var timeout int
		if err := db.Conn().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatalf("db%d: querying busy_timeout: %v", i+1, err)
		}
		if timeout != 5000 {
			t.Errorf("db%d busy_timeout = %d, want 5000", i+1, timeout)
		}
	}
}

// TestBusyTimeout_ConcurrentWriteWaitsInsteadOfFailingImmediately proves the
// busy_timeout pragma has real effect: a long-running write transaction
// from one connection must not cause a concurrent writer to fail instantly
// with SQLITE_BUSY; instead the second writer waits (up to busy_timeout)
// and succeeds once the first commits. This directly exercises the
// "locked/busy behavior" required test (agents/foundation.md).
func TestBusyTimeout_ConcurrentWriteWaitsInsteadOfFailingImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "busy.db")
	ctx := context.Background()

	db1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open db1: %v", err)
	}
	defer func() { _ = db1.Close() }()
	db2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open db2: %v", err)
	}
	defer func() { _ = db2.Close() }()

	if _, err := db1.Conn().ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx1, err := db1.Conn().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	if _, err := tx1.ExecContext(ctx, `INSERT INTO t (id) VALUES (1)`); err != nil {
		t.Fatalf("tx1 insert: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		// This write will block behind tx1's uncommitted write until
		// either tx1 commits/rolls back or busy_timeout elapses. We
		// commit tx1 shortly after starting this goroutine, well within
		// the 5000ms busy_timeout, so it should succeed rather than
		// error out immediately.
		_, execErr := db2.Conn().ExecContext(ctx, `INSERT INTO t (id) VALUES (2)`)
		done <- execErr
	}()

	time.Sleep(50 * time.Millisecond)
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("concurrent write under busy_timeout failed: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("concurrent write did not complete within busy_timeout + margin")
	}
}

// --- WithTx / transaction boundary ------------------------------------------

func TestWithTx_CommitsOnSuccess(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.Conn().ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	err := db.WithTx(ctx, func(txCtx context.Context) error {
		q := sqlite.QuerierFromContext(txCtx, db)
		_, err := q.ExecContext(txCtx, `INSERT INTO t (id) VALUES (1)`)
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestWithTx_RollsBackOnError(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.Conn().ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	sentinel := context.Canceled
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		q := sqlite.QuerierFromContext(txCtx, db)
		if _, err := q.ExecContext(txCtx, `INSERT INTO t (id) VALUES (1)`); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx error = %v, want sentinel", err)
	}

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 0 {
		t.Errorf("row count = %d, want 0 (rollback expected)", count)
	}
}

func TestQuerierFromContext_OutsideTx_UsesPool(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	q := sqlite.QuerierFromContext(ctx, db)
	if _, err := q.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("exec outside tx: %v", err)
	}
}

func TestForeignKeys_ViolationRejected(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	stmts := []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id))`,
	}
	for _, s := range stmts {
		if _, err := db.Conn().ExecContext(ctx, s); err != nil {
			t.Fatalf("schema setup: %v", err)
		}
	}

	_, err := db.Conn().ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES (1, 999)`)
	if err == nil {
		t.Fatal("expected foreign key violation error, got nil")
	}
}

// --- Invalid permissions / corrupt DB error classification (agents/
// foundation.md "Required tests") --------------------------------------------

func TestOpen_CorruptFile_FailsOnFirstQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database"), 0o644); err != nil {
		t.Fatalf("seeding corrupt file: %v", err)
	}

	ctx := context.Background()
	_, err := sqlite.Open(ctx, path)
	// modernc.org/sqlite's sql.Open never touches the file; the pragma
	// application inside Open is what actually opens the connection and
	// therefore is what must surface a corruption error.
	if err == nil {
		t.Fatal("expected an error opening a corrupt database file")
	}
}

func TestOpen_UnwritableDirectory_Errors(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.Chmod on a Windows directory maps mode bits onto the
		// read-only file attribute only, which does NOT prevent creating
		// files inside it — the test's "unwritable directory" premise
		// cannot be established without ACL manipulation (issue #24).
		t.Skip("windows: chmod cannot make a directory unwritable")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
	if err == nil {
		// Close before failing: leaving the unexpectedly-opened handle
		// alive keeps the db file locked, which on Windows also breaks
		// t.TempDir's RemoveAll cleanup and cascades a second failure.
		_ = db.Close()
		t.Fatal("expected an error opening a database under an unwritable directory")
	}
}

func openTemp(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
