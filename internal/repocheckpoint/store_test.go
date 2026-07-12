package repocheckpoint_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/repocheckpoint"
)

func newRow(worktreeID domain.WorktreeID, id string) repocheckpoint.Row {
	total := int64(1024)
	return repocheckpoint.Row{
		ID:               domain.RepositoryCheckpointID(id),
		WorktreeID:       worktreeID,
		Status:           repocheckpoint.StatusComplete,
		ArtifactRoot:     "/tmp/checkpoints/" + id,
		ManifestPath:     "/tmp/checkpoints/" + id + "/manifest.json",
		GitHead:          "abc123",
		IndexDiffHash:    "deadbeef",
		WorktreeDiffHash: "cafef00d",
		Recoverability:   repocheckpoint.RecoverabilityComplete,
		TotalBytes:       &total,
		CreatedAt:        time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

func TestStore_InsertAndGet(t *testing.T) {
	db := openTestDB(t)
	worktreeID := seedWorktree(t, db)
	store := repocheckpoint.NewStore(db)
	ctx := context.Background()

	row := newRow(worktreeID, "cp-1")
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.Get(ctx, "cp-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GitHead != row.GitHead || got.WorktreeID != worktreeID {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.VerifiedAt != nil {
		t.Fatalf("expected VerifiedAt nil before verification, got %v", *got.VerifiedAt)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := repocheckpoint.NewStore(db)
	ctx := context.Background()

	_, err := store.Get(ctx, "missing")
	if !errors.Is(err, repocheckpoint.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_SetVerified(t *testing.T) {
	db := openTestDB(t)
	worktreeID := seedWorktree(t, db)
	store := repocheckpoint.NewStore(db)
	ctx := context.Background()

	row := newRow(worktreeID, "cp-1")
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	verifiedAt := time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := store.SetVerified(ctx, "cp-1", verifiedAt); err != nil {
		t.Fatalf("SetVerified: %v", err)
	}

	got, err := store.Get(ctx, "cp-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.VerifiedAt == nil || *got.VerifiedAt != verifiedAt {
		t.Fatalf("expected VerifiedAt %s, got %v", verifiedAt, got.VerifiedAt)
	}
}

func TestStore_SetVerified_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := repocheckpoint.NewStore(db)
	ctx := context.Background()

	err := store.SetVerified(ctx, "missing", time.Now().Format(time.RFC3339))
	if !errors.Is(err, repocheckpoint.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_OptionalFields_NilRoundTrip(t *testing.T) {
	db := openTestDB(t)
	worktreeID := seedWorktree(t, db)
	store := repocheckpoint.NewStore(db)
	ctx := context.Background()

	row := newRow(worktreeID, "cp-1")
	row.TaskID = nil
	row.TurnID = nil
	row.TotalBytes = nil

	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := store.Get(ctx, "cp-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TaskID != nil || got.TurnID != nil || got.TotalBytes != nil {
		t.Fatalf("expected nil optional fields to round-trip as nil, got: %+v", got)
	}
}
