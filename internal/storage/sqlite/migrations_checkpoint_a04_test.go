// migrations_checkpoint_a04_test.go — checkpoint-a04 (checkpoint role,
// Part A): tests for the two migration files this node adds to checkpoint's
// 0020-0029 range: 0023_state_checkpoints.sql and
// 0024_node_completions.sql. Same rationale as
// migrations_checkpoint_a_test.go for living in foundation's
// internal/storage/sqlite directory (package sqlite_test) despite being
// checkpoint-owned: schema-level constraint tests belong next to the
// migration engine, and internal/progress/internal/statecheckpoint's own
// test suites already cover the Go-level store/protocol behavior over
// these same tables (complete_node_test.go and friends) — this file is
// deliberately narrow: DDL-level constraints only, not CompleteNode
// protocol logic.
package sqlite_test

import (
	"context"
	"testing"

	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

func TestMigration0023_AllMigrations_IncludesStateCheckpointsAndCompletions(t *testing.T) {
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	want := map[int]string{
		23: "state_checkpoints",
		24: "node_completions",
	}
	found := map[int]string{}
	for _, m := range migrations {
		if _, ok := want[m.Version]; ok {
			found[m.Version] = m.Name
		}
	}
	for version, name := range want {
		if found[version] != name {
			t.Errorf("migration %04d = %q, want %q", version, found[version], name)
		}
	}
}

func TestMigration0023_FromEmptyDatabase_CreatesStateCheckpointsSchema(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()

	for _, table := range []string{"state_checkpoints", "node_completions"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Conn().QueryRowContext(ctx, q, table).Scan(&name); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	var idx string
	q := `SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_state_checkpoints_task_created'`
	if err := db.Conn().QueryRowContext(ctx, q).Scan(&idx); err != nil {
		t.Errorf("index idx_state_checkpoints_task_created not created: %v", err)
	}
	q = `SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_node_completions_idempotency_key'`
	if err := db.Conn().QueryRowContext(ctx, q).Scan(&idx); err != nil {
		t.Errorf("index idx_node_completions_idempotency_key not created: %v", err)
	}
}

func TestMigration0023_StateCheckpoints_RejectsUnknownTask(t *testing.T) {
	db := openMigrated(t)
	err := tryExec(t, db,
		`INSERT INTO state_checkpoints (id, task_id, progress_tree_version, manifest_json, integrity_sha256, created_at)
		 VALUES ('c1', 'no-such-task', 1, '{}', 'digest', '2026-07-12T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected foreign key violation inserting a checkpoint with an unknown task_id")
	}
}

func TestMigration0023_StateCheckpoints_TaskDeleteCascades(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1")

	mustExec(t, db,
		`INSERT INTO state_checkpoints (id, task_id, progress_tree_version, manifest_json, integrity_sha256, created_at)
		 VALUES ('c1', ?, 1, '{}', 'digest', '2026-07-12T00:00:00Z')`, taskID)

	mustExec(t, db, `DELETE FROM tasks WHERE id = ?`, taskID)

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM state_checkpoints`).Scan(&count); err != nil {
		t.Fatalf("count state_checkpoints: %v", err)
	}
	if count != 0 {
		t.Errorf("state_checkpoints count after task delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}

func TestMigration0023_StateCheckpoints_MultipleCheckpointsSameVersionAllowed(t *testing.T) {
	db := openMigrated(t)
	taskID := seedTask(t, db, "task1")

	mustExec(t, db,
		`INSERT INTO state_checkpoints (id, task_id, progress_tree_version, manifest_json, integrity_sha256, created_at)
		 VALUES ('c1', ?, 5, '{}', 'digest-1', '2026-07-12T00:00:00Z')`, taskID)
	// Same progress_tree_version, no UNIQUE constraint blocks a second
	// checkpoint at the same version (re-verify-triggered re-checkpoint;
	// see 0023's header comment for why this is deliberate).
	if err := tryExec(t, db,
		`INSERT INTO state_checkpoints (id, task_id, progress_tree_version, manifest_json, integrity_sha256, created_at)
		 VALUES ('c2', ?, 5, '{}', 'digest-2', '2026-07-12T00:01:00Z')`, taskID); err != nil {
		t.Fatalf("expected a second checkpoint at the same progress_tree_version to be allowed: %v", err)
	}
}

func TestMigration0024_NodeCompletions_RejectsUnknownNode(t *testing.T) {
	db := openMigrated(t)
	taskID := seedTask(t, db, "task1")

	err := tryExec(t, db,
		`INSERT INTO node_completions (node_id, task_id, idempotency_key, payload_digest, state_checkpoint_id, completed_node_json, created_at)
		 VALUES ('no-such-node', ?, 'key1', 'digest1', 'c1', '{}', '2026-07-12T00:00:00Z')`, taskID)
	if err == nil {
		t.Fatal("expected foreign key violation inserting a completion ledger row for an unknown node_id")
	}
}

func TestMigration0024_NodeCompletions_OneRowPerNode(t *testing.T) {
	db := openMigrated(t)
	taskID := seedTask(t, db, "task1")
	insertNode(t, db, "n-a", taskID, nil, 0)

	mustExec(t, db,
		`INSERT INTO node_completions (node_id, task_id, idempotency_key, payload_digest, state_checkpoint_id, completed_node_json, created_at)
		 VALUES ('n-a', ?, 'key1', 'digest1', 'c1', '{}', '2026-07-12T00:00:00Z')`, taskID)

	// A node can only ever complete once — a second ledger row for the
	// same node_id is a PRIMARY KEY violation, matching the node state
	// machine's own "completed is terminal" invariant.
	err := tryExec(t, db,
		`INSERT INTO node_completions (node_id, task_id, idempotency_key, payload_digest, state_checkpoint_id, completed_node_json, created_at)
		 VALUES ('n-a', ?, 'key2', 'digest2', 'c2', '{}', '2026-07-12T00:01:00Z')`, taskID)
	if err == nil {
		t.Fatal("expected primary key violation on a second completion ledger row for the same node_id")
	}
}

func TestMigration0024_NodeCompletions_NodeDeleteCascades(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1")
	insertNode(t, db, "n-a", taskID, nil, 0)

	mustExec(t, db,
		`INSERT INTO node_completions (node_id, task_id, idempotency_key, payload_digest, state_checkpoint_id, completed_node_json, created_at)
		 VALUES ('n-a', ?, 'key1', 'digest1', 'c1', '{}', '2026-07-12T00:00:00Z')`, taskID)

	mustExec(t, db, `DELETE FROM progress_nodes WHERE id = 'n-a'`)

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM node_completions`).Scan(&count); err != nil {
		t.Fatalf("count node_completions: %v", err)
	}
	if count != 0 {
		t.Errorf("node_completions count after node delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}
