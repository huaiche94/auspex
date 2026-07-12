// Package sqlite is Preflight's SQLite runtime: connection setup, the
// journal/durability pragmas every later role's storage code depends on,
// and a transaction-callback boundary implementing internal/app.TxRunner.
//
// Driver: modernc.org/sqlite (pure Go, no CGO), per Preflight_ADD.md §1.4's
// tech-stack decision.
//
// This file (db.go) is the connection/pragma/transaction engine only. It
// deliberately creates no schema and applies no migrations — that is
// migrate.go's job, and the actual migration .sql files are a later node
// (foundation-06, out of scope here). Opening a DB with this package on an
// empty file yields a valid, correctly-configured, but completely empty
// SQLite database.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/huaiche94/preflight/internal/app"
)

// Compile-time assertion that *DB satisfies the frozen app.TxRunner port
// (internal/app/ports.go, owned by contract-integrator). If ports.go's
// TxRunner shape ever changes, this line fails to compile instead of the
// mismatch surfacing only when some other role tries to use DB as a
// TxRunner.
var _ app.TxRunner = (*DB)(nil)

// Pragmas are Preflight's fixed SQLite connection settings, per
// Preflight_ADD.md §12.1 / docs/implementation/day1/CONTRACT_FREEZE.md.
// These are load-bearing for every later role's storage code: WAL mode and
// a busy timeout are what make concurrent daemon+CLI access to the same
// database file safe instead of returning SQLITE_BUSY immediately.
const (
	pragmaJournalMode = "PRAGMA journal_mode = WAL"
	pragmaSynchronous = "PRAGMA synchronous = NORMAL"
	pragmaForeignKeys = "PRAGMA foreign_keys = ON"
	pragmaBusyTimeout = "PRAGMA busy_timeout = 5000"
	pragmaTempStore   = "PRAGMA temp_store = MEMORY"
)

// pragmaStatements is applied, in order, to every new connection this
// package opens.
var pragmaStatements = []string{
	pragmaJournalMode,
	pragmaSynchronous,
	pragmaForeignKeys,
	pragmaBusyTimeout,
	pragmaTempStore,
}

// DB wraps a *sql.DB configured with Preflight's pragmas and implements
// app.TxRunner (internal/app/ports.go) for the frozen WithTx transaction
// boundary every storage-touching service uses.
type DB struct {
	sqlDB *sql.DB
	// path is the filesystem path this DB was opened against; "" for an
	// in-memory database. Retained for diagnostics and for Migrate's
	// error messages.
	path string
}

// Open opens (creating if necessary) a SQLite database at path and applies
// Preflight's fixed pragmas (§12.1) to it. path may be ":memory:" for an
// in-memory database (tests only — an in-memory DB is not shared across
// connections in modernc.org/sqlite's default mode, so callers needing a
// realistic multi-connection test should use a temp file instead; see
// db_test.go).
//
// Open does not create or check schema; call a migrator (migrate.go)
// separately once migrations exist (foundation-06).
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := dataSourceName(path)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}

	// A single shared *sql.DB pool is used for a given path; the pragmas
	// below are per-connection state in SQLite, so they must be applied
	// via a ConnHook-style approach: since modernc.org/sqlite (and
	// database/sql generally) does not expose a portable per-connection
	// init hook through the standard sql.Open path, we instead force
	// pragma application on the pool's first connection immediately and
	// rely on WAL + busy_timeout being encoded in the DSN as well, so
	// every subsequent connection the pool opens also gets them (see
	// dataSourceName). Applying them explicitly here in addition to the
	// DSN guards against any driver version that ignores a given DSN
	// query parameter.
	if err := applyPragmas(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sqlite: applying pragmas to %s: %w", path, err)
	}

	// WAL mode plus a concurrent writer is the primary local-daemon
	// access pattern (ADD §1.4); a single writer at a time is still
	// SQLite's model, so cap concurrent connections modestly. This is
	// deliberately conservative rather than tuned, since no later role's
	// real concurrency profile exists yet to tune against.
	sqlDB.SetMaxOpenConns(8)

	return &DB{sqlDB: sqlDB, path: path}, nil
}

// dataSourceName builds the modernc.org/sqlite DSN for path, encoding the
// same durability pragmas as query parameters so every connection the pool
// opens (not just the first) gets them, per modernc.org/sqlite's supported
// "_pragma" DSN convention.
func dataSourceName(path string) string {
	if path == ":memory:" {
		return "file::memory:?cache=shared&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	}
	v := url.Values{}
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "synchronous(NORMAL)")
	v.Add("_pragma", "foreign_keys(ON)")
	v.Add("_pragma", "busy_timeout(5000)")
	v.Add("_pragma", "temp_store(MEMORY)")
	return "file:" + path + "?" + v.Encode()
}

// applyPragmas executes every pragma statement against sqlDB directly, in
// addition to the DSN-encoded pragmas dataSourceName sets. Belt-and-braces:
// PRAGMA statements are connection-scoped in SQLite, and this makes the
// pragma set explicit and independently testable (db_test.go asserts each
// one's effective value), rather than relying solely on DSN parsing
// behavior that could silently change between driver versions.
func applyPragmas(ctx context.Context, sqlDB *sql.DB) error {
	for _, stmt := range pragmaStatements {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing %q: %w", stmt, err)
		}
	}
	return nil
}

// Close closes the underlying connection pool.
func (d *DB) Close() error {
	return d.sqlDB.Close()
}

// Path returns the filesystem path (or ":memory:") this DB was opened
// against.
func (d *DB) Path() string {
	return d.path
}

// Conn exposes the underlying *sql.DB for callers that need to run plain
// queries outside a transaction (e.g. read-only diagnostics, migration
// version checks). Storage code performing writes should prefer WithTx.
func (d *DB) Conn() *sql.DB {
	return d.sqlDB
}

// --- Transaction boundary (internal/app.TxRunner) ---------------------------

// txKey is the context key WithTx stores the active *sql.Tx under, so a
// TxFunc closure (internal/app.TxFunc = func(ctx context.Context) error)
// can retrieve the transaction it is meant to run inside via TxFromContext,
// without app.TxFunc's frozen signature needing to pass a *sql.Tx
// parameter directly (internal/app/ports.go is frozen by contract-integrator
// and out of foundation's control to change).
type txKey struct{}

// WithTx implements app.TxRunner (internal/app/ports.go). It begins a
// transaction, stores it in ctx for the duration of fn, and commits on nil
// error or rolls back otherwise. fn retrieves the active transaction via
// TxFromContext(ctx) to issue queries against it.
func (d *DB) WithTx(ctx context.Context, fn app.TxFunc) error {
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin transaction: %w", err)
	}

	txCtx := context.WithValue(ctx, txKey{}, tx)

	if err := fn(txCtx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("sqlite: rollback after error %w: %w", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

// Querier is satisfied by both *sql.DB and *sql.Tx; storage code written
// against Querier works whether or not it is running inside a WithTx call.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// QuerierFromContext returns the active transaction if ctx was produced by
// WithTx, or db's plain connection pool otherwise. Storage code in later
// roles calls this once per operation to get a Querier without needing to
// know whether it is being invoked inside a transaction:
//
//	func (s *someStore) Insert(ctx context.Context, db *sqlite.DB, row Row) error {
//	    q := sqlite.QuerierFromContext(ctx, db)
//	    _, err := q.ExecContext(ctx, `INSERT INTO ...`, ...)
//	    return err
//	}
func QuerierFromContext(ctx context.Context, db *DB) Querier {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return db.sqlDB
}
