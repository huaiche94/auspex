// migrate.go: the forward-only migration engine (ADD §12.5), plus
// (foundation-06) the embedded loader for foundation's own migration range
// (0000-0009 per CONTRACT_FREEZE.md's migration-range table). foundation-05
// shipped the engine with no .sql files present, which exercised
// LoadMigrationsFS/Migrate's empty-set no-op path only; AllMigrations below
// is the first real caller that loads and applies actual .sql files.
package sqlite

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// migrationsFS embeds every "*.sql" file under migrations/ into the binary,
// so a deployed auspex binary carries its own schema history without
// depending on the source tree being present at runtime. Only files
// directly under this directory are embedded (no subdirectories), matching
// LoadMigrationsFS's own non-recursive contract.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// AllMigrations returns every migration Auspex ships, sorted ascending
// by version, by loading migrationsFS through LoadMigrationsFS. This is the
// single source every caller (the CLI's db-open path, this package's own
// tests, and any later role's integration tests) should use to get "the
// real migration set" rather than hand-rolling an fs.FS each time.
//
// Only foundation's own range (0000-0009) exists as files today; later
// roles' migrations (claude-provider 0010-0019, checkpoint 0020-0039,
// predictor 0040-0049, runtime 0050-0059 — see CONTRACT_FREEZE.md) land as
// additional files under migrations/ in their own commits and are picked
// up automatically once present, with no change needed here.
func AllMigrations() ([]Migration, error) {
	return LoadMigrationsFS(migrationsFS, "migrations")
}

// schemaMigrationsTable tracks which migrations have been applied. Created
// automatically by Migrate on first run.
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version     INTEGER PRIMARY KEY,
	name        TEXT NOT NULL,
	applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`

// Migration is one forward-only schema change.
type Migration struct {
	// Version is the migration's numeric prefix (e.g. 3 for
	// "0003_add_repositories.sql"). Versions must be unique and are
	// applied in ascending order.
	Version int
	// Name is the migration filename's descriptive suffix (e.g.
	// "add_repositories" for "0003_add_repositories.sql"), used only for
	// the schema_migrations audit row and error messages.
	Name string
	// SQL is the migration's full statement text, executed as-is inside
	// a single transaction per migration.
	SQL string
}

// migrationFilePattern matches Auspex's fixed migration filename
// convention, per Auspex_ADD.md §12.5 ("Migration file: 0001_name.sql")
// and agents/foundation.md's exclusive-path glob
// ("migrations/0000-0009_*.sql"): a zero-padded numeric version, an
// underscore, a descriptive name, and a .sql extension.
var migrationFilePattern = regexp.MustCompile(`^(\d{4,})_([a-zA-Z0-9_]+)\.sql$`)

// ErrSchemaNewerThanBinary is returned by Migrate when the database's
// highest applied migration version is newer than any migration this
// binary knows about — i.e. the DB was migrated by a newer Auspex
// version. Per ADD §12.5 ("DB schema newer than binary => read-only
// diagnostics, refuse writes"), the caller MUST treat this as fail-closed:
// do not attempt any write against this database.
var ErrSchemaNewerThanBinary = errors.New("sqlite: database schema is newer than this binary's known migrations")

// ErrDuplicateMigrationVersion is returned by LoadMigrationsFS when two
// migration files declare the same version.
var ErrDuplicateMigrationVersion = errors.New("sqlite: duplicate migration version")

// ErrInvalidMigrationFilename is returned by LoadMigrationsFS for a file
// under the migrations root that does not match migrationFilePattern.
var ErrInvalidMigrationFilename = errors.New("sqlite: invalid migration filename")

// LoadMigrationsFS reads every "*.sql" file directly under root in an
// fs.FS (typically a go:embed of internal/storage/sqlite/migrations),
// parses each filename per Auspex's NNNN_name.sql convention, and
// returns them sorted ascending by version. Subdirectories are not
// traversed. A malformed filename or a duplicate version is an error —
// this is deliberately strict, since a silently-skipped or silently-
// reordered migration is a schema-integrity bug, not a degrade-gracefully
// situation (ADD's fail-closed rule for state-integrity failures, mirrored
// in CONTRACT_FREEZE.md's error contract).
func LoadMigrationsFS(fsys fs.FS, root string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("sqlite: reading migrations dir %s: %w", root, err)
	}

	seen := make(map[int]string, len(entries))
	migrations := make([]Migration, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		match := migrationFilePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("%w: %s (want NNNN_name.sql)", ErrInvalidMigrationFilename, name)
		}

		version, err := strconv.Atoi(match[1])
		if err != nil {
			// Unreachable given the regex's \d+ capture, but handled
			// explicitly rather than ignored.
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidMigrationFilename, name, err)
		}
		if existing, dup := seen[version]; dup {
			return nil, fmt.Errorf("%w: version %d in both %s and %s", ErrDuplicateMigrationVersion, version, existing, name)
		}
		seen[version] = name

		contents, err := fs.ReadFile(fsys, root+"/"+name)
		if err != nil {
			return nil, fmt.Errorf("sqlite: reading migration %s: %w", name, err)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    match[2],
			SQL:     string(contents),
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

// Migrate applies every migration in migrations whose Version is greater
// than the database's current highest applied version, in ascending order,
// each inside its own transaction. It is idempotent and safe to call on
// every process startup:
//
//   - empty database: schema_migrations is created, then every migration
//     is applied in order (a "migration from empty database" run);
//   - reopen with nothing new to apply: schema_migrations already reflects
//     every migration in `migrations`; Migrate is a fast no-op;
//   - reopen with new migrations added to the binary since last run: only
//     the new, higher-versioned migrations are applied;
//   - reopen where the database's highest applied version is HIGHER than
//     any version in `migrations` (this binary is older than whatever
//     last migrated this database): Migrate returns
//     ErrSchemaNewerThanBinary and applies nothing, per ADD §12.5's
//     fail-closed "refuse writes" rule. Callers MUST check for this error
//     specifically and switch to read-only diagnostics mode rather than
//     proceeding as if migration succeeded.
//
// With zero migrations passed (this node's actual state — no .sql files
// exist yet), Migrate creates schema_migrations and returns nil: a
// deliberate, tested no-op.
//
// Concurrency (foundation-07): Migrate reserves a single dedicated
// connection and opens it with "BEGIN IMMEDIATE" before reading the
// database's current version, holding SQLite's write lock for the entire
// read-current-version-then-apply-pending-migrations sequence. This closes
// a real TOCTOU race foundation-07's concurrent-reopen test caught in the
// prior (foundation-05/06) implementation: two connections could each read
// current=0 before either committed, then both attempt to apply the same
// migration ("table already exists"). BEGIN IMMEDIATE acquires the RESERVED
// lock up front, so a second concurrent Migrate call blocks behind the
// first (waiting up to db.go's busy_timeout pragma, per the "locked/busy
// behavior" required test) rather than racing it, and observes the first
// call's committed schema_migrations rows once it proceeds.
func (d *DB) Migrate(ctx context.Context, migrations []Migration) error {
	conn, err := d.sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: reserving connection for migration: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("sqlite: acquiring migration write lock: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, schemaMigrationsTable); err != nil {
		return fmt.Errorf("sqlite: creating schema_migrations: %w", err)
	}

	sorted := make([]Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

	current, err := currentVersionOn(ctx, conn)
	if err != nil {
		return err
	}

	maxKnown := 0
	for _, m := range sorted {
		if m.Version > maxKnown {
			maxKnown = m.Version
		}
	}
	if current > maxKnown {
		return fmt.Errorf("%w: database is at version %d, binary knows up to %d", ErrSchemaNewerThanBinary, current, maxKnown)
	}

	for _, m := range sorted {
		if m.Version <= current {
			continue
		}
		if _, err := conn.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("sqlite: applying migration %04d_%s.sql: executing migration body: %w", m.Version, m.Name, err)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
			m.Version, m.Name,
		); err != nil {
			return fmt.Errorf("sqlite: applying migration %04d_%s.sql: recording schema_migrations row: %w", m.Version, m.Name, err)
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("sqlite: committing migration transaction: %w", err)
	}
	committed = true
	return nil
}

// CurrentVersion returns the highest migration version recorded as applied
// in schema_migrations, or 0 if none have been applied (including the case
// where schema_migrations itself does not exist yet, e.g. on a completely
// fresh database that Migrate has never touched).
func (d *DB) CurrentVersion(ctx context.Context) (int, error) {
	if _, err := d.sqlDB.ExecContext(ctx, schemaMigrationsTable); err != nil {
		return 0, fmt.Errorf("sqlite: creating schema_migrations: %w", err)
	}
	return currentVersionOn(ctx, d.sqlDB)
}

// currentVersionOn reads the highest applied migration version through q,
// which is either d.sqlDB (CurrentVersion's plain-pool read) or a single
// reserved *sql.Conn already holding Migrate's BEGIN IMMEDIATE write lock
// (Migrate's read-then-apply sequence) — both satisfy Querier's
// QueryRowContext method.
func currentVersionOn(ctx context.Context, q Querier) (int, error) {
	var version *int
	row := q.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`)
	if err := row.Scan(&version); err != nil {
		return 0, fmt.Errorf("sqlite: reading current schema version: %w", err)
	}
	if version == nil {
		return 0, nil
	}
	return *version, nil
}
