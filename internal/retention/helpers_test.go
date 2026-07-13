// helpers_test.go: shared fixtures for this package's engine tests. All
// tests run against a REAL on-disk SQLite database (never :memory:) with
// the full embedded migration set applied — the same seeding-by-SQL
// convention internal/storage/sqlite's migration tests and
// internal/pause/persistphase_test.go established — plus deterministic
// clock/ID fakes per the codebase's fixedClock/seqIDs convention.
package retention

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// testNow is every test's fixed wall-clock instant; with the default
// 90-day policy the cutoff is testCutoff. Rows older than testCutoff are
// expired; rows at or after it are hot.
var (
	testNow    = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	testCutoff = testNow.AddDate(0, 0, -DefaultRetentionDays)
	oldTime    = testCutoff.AddDate(0, 0, -30) // comfortably expired
	newTime    = testCutoff.AddDate(0, 0, +30) // comfortably hot
)

type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

type seqIDs struct {
	prefix string
	n      int
}

func (g *seqIDs) NewID() string {
	g.n++
	return fmt.Sprintf("%s-%04d", g.prefix, g.n)
}

// newTestEngine opens a fresh migrated on-disk database under its own
// temp dir and returns an Engine over it plus the data dir archives land
// in. The DB is closed via t.Cleanup.
func newTestEngine(t *testing.T) (*Engine, *sqlite.DB, string) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()

	db, err := sqlite.Open(ctx, filepath.Join(dataDir, "auspex.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	engine := &Engine{
		DB:      db,
		Clock:   fixedClock{t: testNow},
		IDs:     &seqIDs{prefix: "run"},
		DataDir: dataDir,
	}
	return engine, db, dataDir
}

// exec runs one seeding statement, failing the test on error.
func exec(t *testing.T, db *sqlite.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Conn().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// count returns SELECT COUNT(*) with an optional WHERE clause.
func count(t *testing.T, db *sqlite.DB, table, where string, args ...any) int {
	t.Helper()
	query := "SELECT COUNT(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	var n int
	if err := db.Conn().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// ts renders a timestamp exactly as the production stores do
// (RFC3339Nano UTC with trailing zeros trimmed).
func ts(t time.Time) string { return formatTime(t) }

// snapshotTable reads every row of table into pk -> full row map, using
// the same generic scanner the engine archives with — so fidelity
// assertions compare against an independent read of the pre-delete state.
func snapshotTable(t *testing.T, db *sqlite.DB, table, keyColumn string) map[string]map[string]any {
	t.Helper()
	rows, err := queryRowMaps(context.Background(), db, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("snapshot %s: %v", table, err)
	}
	out := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		out[stringOrEmpty(row[keyColumn])] = row
	}
	return out
}

// seedCore inserts the FK backbone every class hangs off: one
// repository, one worktree, one provider session, and the four task
// archetypes the checkpoint tests need.
func seedCore(t *testing.T, db *sqlite.DB) {
	t.Helper()
	exec(t, db, `INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		VALUES ('repo1', '/tmp/repo1', '/tmp/repo1/.git', ?, ?)`, ts(oldTime), ts(newTime))
	exec(t, db, `INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		VALUES ('wt1', 'repo1', '/tmp/repo1', '/tmp/repo1/.git', ?, ?)`, ts(oldTime), ts(newTime))
	exec(t, db, `INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at)
		VALUES ('sess1', 'wt1', 'claude', 'native_hooks', ?)`, ts(oldTime))

	for _, task := range []struct {
		id, status  string
		completedAt any
	}{
		{"task-old-done", "completed", ts(oldTime)}, // terminal, completion expired
		{"task-old-open", "open", nil},              // NOT terminal, however old
		{"task-null-done", "failed", nil},           // terminal but undatable -> skipped
		{"task-new-done", "completed", ts(newTime)}, // terminal but completion still hot
	} {
		exec(t, db, `INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at, completed_at)
			VALUES (?, 'sess1', 'wt1', 'hash', ?, ?, ?, ?)`,
			task.id, task.status, ts(oldTime), ts(oldTime), task.completedAt)
	}
}

// seedEvent inserts one events row with the given identity/payload.
func seedEvent(t *testing.T, db *sqlite.DB, eventID, eventType string, occurredAt time.Time, sessionID, turnID, payloadJSON string) {
	t.Helper()
	var turn any
	if turnID != "" {
		turn = turnID
	}
	var session any
	if sessionID != "" {
		session = sessionID
	}
	exec(t, db, `INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, payload_json)
		VALUES (?, 'auspex.event.v1', ?, ?, ?, 'hook', 'claude', ?, ?, ?)`,
		eventID, eventType, ts(occurredAt), ts(occurredAt), session, turn, payloadJSON)
}

// seedPrediction inserts one predictions row with representative values
// in every column class (nullable quantiles set, REAL scores, JSON text).
func seedPrediction(t *testing.T, db *sqlite.DB, id, turnID string, createdAt time.Time) {
	t.Helper()
	exec(t, db, `INSERT INTO predictions (
			id, turn_id, predictor_id, predictor_version, feature_set_version,
			token_p50, token_p80, token_p90,
			quota_risk_score, context_risk_score, completion_risk_score,
			blast_radius_risk_score, overall_risk_score,
			confidence, calibrated, reason_codes_json, created_at
		) VALUES (?, ?, 'rule', 'v1', 'fs1', 1000, 2000, 3000, 0.1, 0.2, 0.3, 0.4, 0.42, 'low', 0, '["cold_start"]', ?)`,
		id, turnID, ts(createdAt))
}

// seedStateCheckpoint inserts one state_checkpoints row.
func seedStateCheckpoint(t *testing.T, db *sqlite.DB, id, taskID string, repoCheckpointID any, createdAt time.Time) {
	t.Helper()
	exec(t, db, `INSERT INTO state_checkpoints (id, task_id, progress_tree_version, repository_checkpoint_id, manifest_json, integrity_sha256, created_at)
		VALUES (?, ?, 1, ?, '{}', 'sha', ?)`, id, taskID, repoCheckpointID, ts(createdAt))
}

// seedRepoCheckpoint inserts one repository_checkpoints row whose
// artifact_root points at artifactRoot (created on disk by the caller
// when the test needs it to exist).
func seedRepoCheckpoint(t *testing.T, db *sqlite.DB, id, taskID, artifactRoot string, createdAt time.Time) {
	t.Helper()
	exec(t, db, `INSERT INTO repository_checkpoints (id, worktree_id, task_id, status, artifact_root, manifest_path, git_head, index_diff_hash, worktree_diff_hash, recoverability, created_at, metadata_json)
		VALUES (?, 'wt1', ?, 'verified', ?, ?, 'head', 'idx', 'wtd', 'full', ?, '{}')`,
		id, taskID, artifactRoot, filepath.Join(artifactRoot, "manifest.json"), ts(createdAt))
}
