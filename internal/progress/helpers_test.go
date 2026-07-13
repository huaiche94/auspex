package progress_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// fixedClock is a deterministic domain.Clock test double.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("sqlite.AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return db
}

// seedTask inserts a minimal repositories -> worktrees -> tasks chain so
// progress_nodes' FK into tasks(id) (0020's schema) is satisfiable, and
// returns the new task's ID.
func seedTask(t *testing.T, db *sqlite.DB) domain.TaskID {
	t.Helper()
	ctx := context.Background()
	repoID := "repo-" + t.Name()
	worktreeID := "worktree-" + t.Name()
	taskID := "task-" + t.Name()
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	err := db.WithTx(ctx, func(ctx context.Context) error {
		q := sqlite.QuerierFromContext(ctx, db)
		if _, err := q.ExecContext(ctx, `
			INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)`, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?)`, worktreeID, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO tasks (id, worktree_id, objective_hash, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, taskID, worktreeID, "objective-hash", "in_progress", now, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seedTask: %v", err)
	}
	return domain.TaskID(taskID)
}
