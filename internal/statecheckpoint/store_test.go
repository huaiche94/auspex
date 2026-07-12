package statecheckpoint_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "preflight.db")
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

func TestStore_InsertGet(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := statecheckpoint.NewStore(db)
	ctx := context.Background()

	row := statecheckpoint.Row{
		ID:                  "checkpoint-1",
		TaskID:              taskID,
		ProgressTreeVersion: 1,
		ManifestJSON:        `{"schema_version":"preflight.state-checkpoint.v1"}`,
		IntegritySHA256:     "abc123",
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.Get(ctx, "checkpoint-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != row.ID || got.TaskID != row.TaskID || got.IntegritySHA256 != row.IntegritySHA256 {
		t.Fatalf("unexpected row: %+v", got)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := statecheckpoint.NewStore(db)
	_, err := store.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, statecheckpoint.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_LoadLatest(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := statecheckpoint.NewStore(db)
	ctx := context.Background()

	base := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	for i, ts := range []time.Time{base, base.Add(1 * time.Hour), base.Add(2 * time.Hour)} {
		row := statecheckpoint.Row{
			ID:                  domain.StateCheckpointID("checkpoint-" + string(rune('a'+i))),
			TaskID:              taskID,
			ProgressTreeVersion: int64(i + 1),
			ManifestJSON:        `{}`,
			IntegritySHA256:     "digest",
			CreatedAt:           ts.Format(time.RFC3339),
		}
		if err := store.Insert(ctx, row); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	latest, err := store.LoadLatest(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadLatest: %v", err)
	}
	if latest.ProgressTreeVersion != 3 {
		t.Fatalf("expected the latest (version 3) checkpoint, got version %d", latest.ProgressTreeVersion)
	}
}

func TestStore_LoadLatest_NotFound(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := statecheckpoint.NewStore(db)
	_, err := store.LoadLatest(context.Background(), taskID)
	if !errors.Is(err, statecheckpoint.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a task with no checkpoints, got %v", err)
	}
}

func TestStore_ListByTask_OrderedOldestFirst(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := statecheckpoint.NewStore(db)
	ctx := context.Background()

	base := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	ids := []domain.StateCheckpointID{"c-1", "c-2", "c-3"}
	for i, id := range ids {
		row := statecheckpoint.Row{
			ID:                  id,
			TaskID:              taskID,
			ProgressTreeVersion: int64(i + 1),
			ManifestJSON:        `{}`,
			IntegritySHA256:     "digest",
			CreatedAt:           base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}
		if err := store.Insert(ctx, row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	rows, err := store.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.ID != ids[i] {
			t.Fatalf("expected order %v, got row %d = %s", ids, i, r.ID)
		}
	}
}
