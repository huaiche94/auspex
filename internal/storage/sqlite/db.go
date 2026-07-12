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
	"errors"
	"fmt"
	"net/url"
	"time"

	modernc "modernc.org/sqlite" // registers the "sqlite" database/sql driver

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
//
// busy_timeout MUST be applied first (foundation-07): PRAGMA journal_mode =
// WAL itself briefly needs to acquire a lock to switch modes, so on a
// connection racing another process/connection's write lock (e.g. several
// callers opening the same database file concurrently, per this package's
// TestMigration_ConcurrentReopen_* tests), applying journal_mode before
// busy_timeout is confirmed active on THIS connection let SQLITE_BUSY
// surface immediately from applyPragmas during Open, instead of the
// connection waiting up to busy_timeout the way every other write on this
// package is documented to. This was a real, reproducible flake
// (~30% failure rate under `-race -count=20` on several concurrent Opens
// against one file) caught by foundation-07's concurrent-reopen test, not
// a theoretical concern.
var pragmaStatements = []string{
	pragmaBusyTimeout,
	pragmaJournalMode,
	pragmaSynchronous,
	pragmaForeignKeys,
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
	// busy_timeout first, matching pragmaStatements' ordering below and
	// for the same reason (foundation-07): journal_mode(WAL) itself briefly
	// needs a lock to switch modes, so it must not run on a connection
	// before that connection's own busy_timeout is active.
	v := url.Values{}
	v.Add("_pragma", "busy_timeout(5000)")
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "synchronous(NORMAL)")
	v.Add("_pragma", "foreign_keys(ON)")
	v.Add("_pragma", "temp_store(MEMORY)")
	return "file:" + path + "?" + v.Encode()
}

// applyPragmas executes every pragma statement against sqlDB directly, in
// addition to the DSN-encoded pragmas dataSourceName sets. Belt-and-braces:
// PRAGMA statements are connection-scoped in SQLite, and this makes the
// pragma set explicit and independently testable (db_test.go asserts each
// one's effective value), rather than relying solely on DSN parsing
// behavior that could silently change between driver versions.
//
// Retries transient SQLITE_BUSY on each statement (foundation-07): a brand
// new connection's very first statement — including PRAGMA busy_timeout
// itself — has no busy-wait protection yet, because busy_timeout only takes
// effect once IT successfully runs. Under concurrent Open() calls racing
// another connection's held write lock (e.g. this package's Migrate, which
// holds a BEGIN IMMEDIATE lock for its whole read-then-apply sequence),
// that bootstrap statement can itself be rejected immediately with
// SQLITE_BUSY rather than waiting — a real, reproducible failure this node
// found via TestMigration_ConcurrentReopen_SerializesAndConverges (~5-30%
// under `-race -count=20`, depending on machine load). A short bounded
// retry closes this bootstrap gap without weakening the "fail-closed on
// state-integrity failures" rule (CONTRACT_FREEZE.md): SQLITE_BUSY here is
// the definition of an operational, transiently-retryable condition, not a
// state-integrity failure, and retrying a handful of times over a fraction
// of a second is strictly less surprising to a caller than a bare Open()
// occasionally failing under ordinary concurrent access this package is
// documented to support.
func applyPragmas(ctx context.Context, sqlDB *sql.DB) error {
	for _, stmt := range pragmaStatements {
		if err := execWithBusyRetry(ctx, sqlDB, stmt); err != nil {
			return fmt.Errorf("executing %q: %w", stmt, err)
		}
	}
	return nil
}

// pragmaBusyRetryAttempts and pragmaBusyRetryDelay bound applyPragmas'
// SQLITE_BUSY retry: up to ~500ms total across 10 attempts, comfortably
// inside the 5000ms busy_timeout this same pragma set establishes once it
// succeeds, and short enough that Open() does not itself become a
// surprising source of multi-second latency under contention.
const (
	pragmaBusyRetryAttempts = 10
	pragmaBusyRetryDelay    = 50 * time.Millisecond
)

// execWithBusyRetry runs stmt against sqlDB, retrying a bounded number of
// times if the error is SQLITE_BUSY (matched by message substring, since
// modernc.org/sqlite's *sqlite.Error/Code() is an internal type this
// package does not import — see isBusyError). Any other error returns
// immediately, unretried.
func execWithBusyRetry(ctx context.Context, sqlDB *sql.DB, stmt string) error {
	var lastErr error
	for attempt := 0; attempt < pragmaBusyRetryAttempts; attempt++ {
		_, err := sqlDB.ExecContext(ctx, stmt)
		if err == nil {
			return nil
		}
		if !isBusyError(err) {
			return err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		case <-time.After(pragmaBusyRetryDelay):
		}
	}
	return lastErr
}

// sqliteBusyCode is SQLITE_BUSY's numeric result code, per SQLite's public,
// long-stable C API (https://www.sqlite.org/rescode.html#busy). Not
// re-exported by modernc.org/sqlite's top-level package (only by its
// internal modernc.org/sqlite/lib, which this package does not otherwise
// need), so it is named here as its own constant rather than importing
// that internal package for one value.
const sqliteBusyCode = 5

// isBusyError reports whether err is SQLite's SQLITE_BUSY ("database is
// locked"), via errors.As against the driver's exported *modernc.Error
// type and its Code() method — not a string match, so it is not sensitive
// to error-message wording changes across driver versions.
func isBusyError(err error) bool {
	var sqliteErr *modernc.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteBusyCode
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
