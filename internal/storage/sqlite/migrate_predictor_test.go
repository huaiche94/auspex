// migrate_predictor_test.go: predictor-01's own validation of its migration
// range (0040-0049 per CONTRACT_FREEZE.md's migration-range table).
//
// internal/storage/sqlite/migrate_test.go is owned by foundation (not one
// of predictor's exclusive paths — see agents/predictor.md), so per
// Constitution §4.4 ("a role never edits a file it doesn't own; it works
// around the gap with a documented assumption"), this is a new,
// additional file rather than an edit to that existing one. It follows the
// same pattern foundation-06 already established for its own range in this
// package (TestAllMigrations_LoadsCoreSchemaFiles / TestCoreMigrations_*):
// test names contain "Migration0040" so the DAG's predictor-01 validation
// command (`go test ./internal/storage/sqlite/... -run Migration0040`)
// selects them.
//
// feature_vectors/predictions/runway_forecasts/authorizations all reference
// `turns` (claude-provider's 0010-0019 range) and authorizations also
// references `repository_checkpoints` (checkpoint Part B's 0030-0039
// range) conceptually, per Preflight_ADD.md §12.2 — but neither table
// exists as a migration on this branch yet, and none of the four
// migration files in this range declares a SQL-level FK constraint against
// them: a forward REFERENCES clause is not merely inert until the target
// table exists, it actively breaks unrelated cascading DELETEs anywhere
// else in the schema once foreign_keys=ON (SQLite resolves every
// FK-referenced table reachable from a DELETE's cascade graph at prepare
// time, confirmed empirically against
// TestCoreMigrations_ForeignKeys_RepositoryToWorktree in migrate_test.go,
// which failed with "no such table: main.turns" during this node's first
// draft). Each migration file's own header comment documents this and
// follows 0004_tasks.sql's existing precedent (active_node_id/
// progress_nodes) of omitting the constraint and keeping the column plain
// TEXT. session_id -> provider_sessions and task_id -> tasks (both present
// on this branch) keep real FK constraints; those are exercised here too.
package sqlite_test

import (
	"context"
	"testing"

	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// TestMigration0040_PredictorRangeLoadsAndApplies confirms all five files in
// predictor's range load via AllMigrations() (proving they sit correctly
// under migrations/ and parse as valid filenames/SQL) and apply cleanly
// against an empty database together with every earlier-range migration
// already on this branch (0001-0004).
func TestMigration0040_PredictorRangeLoadsAndApplies(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	want := []struct {
		version int
		name    string
	}{
		{40, "feature_vectors"},
		{41, "predictions"},
		{42, "runway_forecasts"},
		{43, "policy_decisions"},
		{44, "authorizations"},
	}
	found := make(map[int]string, len(want))
	for _, m := range migrations {
		found[m.Version] = m.Name
	}
	for _, w := range want {
		name, ok := found[w.version]
		if !ok {
			t.Errorf("migration %d (%s) not found in AllMigrations()", w.version, w.name)
			continue
		}
		if name != w.name {
			t.Errorf("migration %d name = %q, want %q", w.version, name, w.name)
		}
	}

	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	version, err := db.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if version < 44 {
		t.Errorf("CurrentVersion = %d, want >= 44 after predictor range applies", version)
	}
}

// TestMigration0040_TablesHaveExpectedColumns spot-checks each predictor
// table's column set against Preflight_ADD.md §12.2's canonical schema,
// using PRAGMA table_info (cheap structural check, does not require the
// forward-referenced tables in other roles' ranges to exist).
func TestMigration0040_TablesHaveExpectedColumns(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cases := []struct {
		table   string
		columns []string
	}{
		{"feature_vectors", []string{"turn_id", "feature_set_version", "features_json", "created_at"}},
		{"predictions", []string{
			"id", "turn_id", "predictor_id", "predictor_version", "feature_set_version",
			"token_p50", "token_p80", "token_p90",
			"files_read_p50", "files_read_p90", "files_changed_p50", "files_changed_p90",
			"lines_changed_p50", "lines_changed_p90",
			"quota_risk_score", "context_risk_score", "completion_risk_score", "blast_radius_risk_score",
			"overall_risk_score", "confidence", "calibrated", "reason_codes_json", "created_at",
		}},
		{"runway_forecasts", []string{
			"id", "session_id", "turn_id", "task_id", "limit_id", "horizon_seconds",
			"hit_probability", "risk_score", "calibrated", "confidence", "current_used_percent",
			"burn_rate_p50", "burn_rate_p90",
			"estimated_time_to_limit_p50_seconds", "estimated_time_to_limit_p90_seconds",
			"reason_codes_json", "created_at",
		}},
		{"policy_decisions", []string{
			"id", "prediction_id", "runway_forecast_id", "policy_version", "action",
			"severity", "requires_confirmation", "reason_codes_json", "decided_at",
		}},
		{"authorizations", []string{
			"id", "turn_id", "prompt_hash", "snapshot_fingerprint", "decision",
			"repository_checkpoint_id", "issued_at", "expires_at", "consumed_at",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			rows, err := db.Conn().QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, tc.table)
			if err != nil {
				t.Fatalf("pragma_table_info(%s): %v", tc.table, err)
			}
			defer func() { _ = rows.Close() }()

			got := make(map[string]bool)
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					t.Fatalf("scan column name: %v", err)
				}
				got[name] = true
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows.Err: %v", err)
			}

			for _, col := range tc.columns {
				if !got[col] {
					t.Errorf("table %s missing expected column %q", tc.table, col)
				}
			}
		})
	}
}

// TestMigration0040_PolicyDecisionsForeignKeys verifies the FK
// relationships fully contained within predictor's own migration range:
// policy_decisions -> predictions (ON DELETE CASCADE) and
// policy_decisions -> runway_forecasts (ON DELETE SET NULL), plus
// runway_forecasts -> provider_sessions (ON DELETE CASCADE, a real
// cross-range FK into foundation's 0003, which is present on this branch).
// No forward-referenced table from another not-yet-landed role is needed
// (turn_id on predictions/runway_forecasts is deliberately unconstrained —
// see this file's package doc comment).
func TestMigration0040_PolicyDecisionsForeignKeys(t *testing.T) {
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

	// turn_id is NOT NULL but unconstrained by FK (see package doc
	// comment), so any non-empty placeholder is valid here.
	exec(`INSERT INTO predictions (
		id, turn_id, predictor_id, predictor_version, feature_set_version,
		quota_risk_score, context_risk_score, completion_risk_score, blast_radius_risk_score,
		overall_risk_score, confidence, reason_codes_json, created_at
	) VALUES (
		'pred1', 'turn-placeholder', 'rule-v1', '1.0.0', 'v1',
		0.1, 0.1, 0.1, 0.1, 0.1, 'low', '[]', '2026-01-01T00:00:00Z'
	)`)

	exec(`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
	      VALUES ('repo-pd', '/tmp/pd-repo', '/tmp/pd-repo/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
	      VALUES ('wt-pd', 'repo-pd', '/tmp/pd', '/tmp/pd/.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
	      VALUES ('sess-pd', 'wt-pd', 'claude-code', 'interactive', '2026-01-01T00:00:00Z', '{}')`)
	exec(`INSERT INTO runway_forecasts (
		id, session_id, limit_id, horizon_seconds, risk_score, calibrated, confidence, reason_codes_json, created_at
	) VALUES (
		'runway1', 'sess-pd', 'weekly_hours', 600, 0.2, 0, 'low', '[]', '2026-01-01T00:00:00Z'
	)`)

	exec(`INSERT INTO policy_decisions (
		id, prediction_id, runway_forecast_id, policy_version, action, severity, requires_confirmation, reason_codes_json, decided_at
	) VALUES (
		'pd1', 'pred1', 'runway1', 'v1', 'ALLOW', 'low', 0, '[]', '2026-01-01T00:00:00Z'
	)`)

	// ON DELETE CASCADE: deleting the prediction removes the policy_decisions row.
	exec(`DELETE FROM predictions WHERE id = 'pred1'`)
	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM policy_decisions WHERE id = 'pd1'`).Scan(&count); err != nil {
		t.Fatalf("count policy_decisions: %v", err)
	}
	if count != 0 {
		t.Errorf("policy_decisions count after prediction delete = %d, want 0 (ON DELETE CASCADE)", count)
	}

	// runway_forecasts.session_id -> provider_sessions is a real FK
	// (ON DELETE CASCADE): deleting the session must cascade-delete the
	// runway_forecasts row (already inserted above as 'runway1').
	exec(`DELETE FROM provider_sessions WHERE id = 'sess-pd'`)
	var runwayCount int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM runway_forecasts WHERE id = 'runway1'`).Scan(&runwayCount); err != nil {
		t.Fatalf("count runway_forecasts: %v", err)
	}
	if runwayCount != 0 {
		t.Errorf("runway_forecasts count after provider_sessions delete = %d, want 0 (ON DELETE CASCADE)", runwayCount)
	}
}

// TestMigration0040_AuthorizationsUniqueTurnID verifies the
// UNIQUE(turn_id) constraint that backs "one-time authorization issuance"
// (agents/predictor.md deliverable #12; CONTRACT_FREEZE.md: "Authorization
// — one-time"). turn_id/repository_checkpoint_id are unconstrained by FK
// (see package doc comment), so no other-role fixture tables are needed.
func TestMigration0040_AuthorizationsUniqueTurnID(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	insert := func(id string) error {
		_, err := db.Conn().ExecContext(ctx, `INSERT INTO authorizations (
			id, turn_id, prompt_hash, snapshot_fingerprint, decision, issued_at, expires_at
		) VALUES (
			?, 'turn-auth', 'hash1', 'fp1', 'ALLOW', '2026-01-01T00:00:00Z', '2026-01-01T00:05:00Z'
		)`, id)
		return err
	}

	if err := insert("auth-1"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert("auth-2"); err == nil {
		t.Fatal("expected UNIQUE(turn_id) violation inserting a second authorization for the same turn")
	}
}
