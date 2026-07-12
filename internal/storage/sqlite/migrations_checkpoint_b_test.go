// migrations_checkpoint_b_test.go — checkpoint-b01 (checkpoint role,
// Part B): tests for the Repository Checkpoint migration file
// 0030_repository_checkpoints.sql (checkpoint's 0030-0039 range per
// CONTRACT_FREEZE.md).
//
// Owned by the checkpoint role, not foundation — same rationale as
// migrations_checkpoint_a_test.go's header: the DAG's frozen validation
// command (`go test ./internal/storage/sqlite/... -run Migration0030`)
// requires the tests to live in this package. Kept as a separate file from
// Part A's tests per agents/checkpoint.md ("keep Part A and Part B
// implementations, migrations, and tests separate within this role's
// paths"). Shares the checkpoint-owned helpers defined in the Part A file.
package sqlite_test

import (
	"context"
	"testing"

	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// insertRepoCheckpoint inserts a minimal repository_checkpoints row. turnID
// may be nil (turn_id is a plain nullable TEXT pointer until
// claude-provider's turns table exists — see the migration header).
func insertRepoCheckpoint(t *testing.T, db *sqlite.DB, id, worktreeID string, taskID, turnID *string) error {
	t.Helper()
	return tryExec(t, db,
		`INSERT INTO repository_checkpoints
		   (id, worktree_id, task_id, turn_id, status, artifact_root, manifest_path,
		    git_head, index_diff_hash, worktree_diff_hash, recoverability, created_at)
		 VALUES (?, ?, ?, ?, 'created', '/tmp/preflight/checkpoints/rc1', '/tmp/preflight/checkpoints/rc1/manifest.json',
		         'deadbeef', 'idxhash', 'wthash', 'full', '2026-07-12T00:00:00Z')`,
		id, worktreeID, taskID, turnID)
}

func TestMigration0030_AllMigrations_IncludesRepositoryCheckpoints(t *testing.T) {
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	// Presence-only assertion (version + name), deliberately not an exact
	// total count — other roles' ranges must be able to land in the shared
	// migrations/ directory without breaking this test.
	for _, m := range migrations {
		if m.Version == 30 {
			if m.Name != "repository_checkpoints" {
				t.Fatalf("migration 0030 = %q, want %q", m.Name, "repository_checkpoints")
			}
			return
		}
	}
	t.Fatal("migration 0030_repository_checkpoints.sql not present in AllMigrations()")
}

func TestMigration0030_FromEmptyDatabase_CreatesRepositoryCheckpoints(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()

	var name string
	q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'repository_checkpoints'`
	if err := db.Conn().QueryRowContext(ctx, q).Scan(&name); err != nil {
		t.Fatalf("table repository_checkpoints not created: %v", err)
	}
}

func TestMigration0030_RejectsUnknownWorktree(t *testing.T) {
	db := openMigrated(t)

	if err := insertRepoCheckpoint(t, db, "rc1", "no-such-worktree", nil, nil); err == nil {
		t.Fatal("expected foreign key violation inserting a checkpoint with an unknown worktree_id")
	}
}

func TestMigration0030_WorktreeDeleteCascades_TaskDeleteDetaches(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1") // seeds repo1/wt1/task1

	if err := insertRepoCheckpoint(t, db, "rc1", "wt1", &taskID, nil); err != nil {
		t.Fatalf("insert rc1: %v", err)
	}

	// Deleting the task must detach (SET NULL), not destroy: the checkpoint
	// is repository evidence in its own right and outlives the task.
	mustExec(t, db, `DELETE FROM tasks WHERE id = ?`, taskID)

	var gotTask *string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT task_id FROM repository_checkpoints WHERE id = 'rc1'`).Scan(&gotTask); err != nil {
		t.Fatalf("select rc1: %v", err)
	}
	if gotTask != nil {
		t.Errorf("repository_checkpoints.task_id = %v, want nil after task delete (ON DELETE SET NULL)", *gotTask)
	}

	// Deleting the worktree cascades the checkpoint row away.
	mustExec(t, db, `DELETE FROM worktrees WHERE id = 'wt1'`)

	var count int
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM repository_checkpoints`).Scan(&count); err != nil {
		t.Fatalf("count repository_checkpoints: %v", err)
	}
	if count != 0 {
		t.Errorf("repository_checkpoints count after worktree delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}

func TestMigration0030_TurnIDIsPlainNullablePointer(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	seedTask(t, db, "task1")

	// turns (claude-provider's 0010-0019 range) does not exist yet, so
	// turn_id deliberately carries no FK (see the migration header): a row
	// with a turn_id value must be writable today without the turns table.
	turnID := "turn-not-yet-a-table"
	if err := insertRepoCheckpoint(t, db, "rc1", "wt1", nil, &turnID); err != nil {
		t.Fatalf("insert with turn_id set (no turns table yet): %v", err)
	}

	var got *string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT turn_id FROM repository_checkpoints WHERE id = 'rc1'`).Scan(&got); err != nil {
		t.Fatalf("select rc1: %v", err)
	}
	if got == nil || *got != turnID {
		t.Errorf("turn_id = %v, want %q", got, turnID)
	}
}

func TestMigration0030_TotalBytesNullMeansUnknown(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	seedTask(t, db, "task1")

	// unknown-is-not-zero (CONTRACT_FREEZE.md): a checkpoint without a
	// measured size stores NULL, and reads back NULL — never 0.
	if err := insertRepoCheckpoint(t, db, "rc1", "wt1", nil, nil); err != nil {
		t.Fatalf("insert rc1: %v", err)
	}

	var totalBytes *int64
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT total_bytes FROM repository_checkpoints WHERE id = 'rc1'`).Scan(&totalBytes); err != nil {
		t.Fatalf("select rc1: %v", err)
	}
	if totalBytes != nil {
		t.Errorf("total_bytes = %v, want NULL (unknown is not zero)", *totalBytes)
	}
}
