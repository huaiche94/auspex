package progress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
)

func newArtifact(taskID domain.TaskID, nodeID domain.ProgressNodeID, id, uri, sha string) progress.ArtifactRow {
	return progress.ArtifactRow{
		ID:               id,
		TaskID:           taskID,
		NodeID:           &nodeID,
		Kind:             "file",
		URI:              uri,
		Bytes:            1024,
		SHA256:           sha,
		ValidationStatus: progress.ValidationPending,
		CreatedAt:        time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

func TestArtifactStore_InsertAndGet(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := nodeStore.Insert(ctx, n); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	a := newArtifact(taskID, "node-1", "artifact-1", "file:section.md", "deadbeef")
	if err := artifactStore.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := artifactStore.Get(ctx, "artifact-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URI != a.URI || got.SHA256 != a.SHA256 || got.ValidationStatus != progress.ValidationPending {
		t.Fatalf("unexpected artifact: %+v", got)
	}
}

func TestArtifactStore_Get_NotFound(t *testing.T) {
	db := openTestDB(t)
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	_, err := artifactStore.Get(ctx, "missing")
	if !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestArtifactStore_DuplicateEvidence_Conflict(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := nodeStore.Insert(ctx, n); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	a1 := newArtifact(taskID, "node-1", "artifact-1", "file:section.md", "deadbeef")
	if err := artifactStore.Insert(ctx, a1); err != nil {
		t.Fatalf("first Insert: %v", err)
	}

	// Same node + uri + sha256, different artifact ID: exact duplicate
	// evidence, must be rejected as a conflict per 0022's UNIQUE constraint.
	a2 := newArtifact(taskID, "node-1", "artifact-2", "file:section.md", "deadbeef")
	err := artifactStore.Insert(ctx, a2)
	if err == nil {
		t.Fatal("expected duplicate (node, uri, sha256) insert to fail")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict, got %s", domErr.Code)
	}
}

func TestArtifactStore_DifferentSHA256_NotBlockedByStore(t *testing.T) {
	// A different sha256 for the same (node, uri) is NOT rejected by the
	// store's own constraint (see artifact_store.go's Insert doc comment) —
	// surfacing that as a completion conflict is checkpoint-a04's job. This
	// test documents/locks that boundary so a04 doesn't have to guess it.
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := nodeStore.Insert(ctx, n); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	a1 := newArtifact(taskID, "node-1", "artifact-1", "file:section.md", "deadbeef")
	if err := artifactStore.Insert(ctx, a1); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	a2 := newArtifact(taskID, "node-1", "artifact-2", "file:section.md", "cafef00d")
	if err := artifactStore.Insert(ctx, a2); err != nil {
		t.Fatalf("expected different-sha256 insert to succeed at the store layer, got: %v", err)
	}
}

func TestArtifactStore_ListByNode(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := nodeStore.Insert(ctx, n); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	for i, sha := range []string{"aaa", "bbb"} {
		a := newArtifact(taskID, "node-1", "artifact-"+string(rune('a'+i)), "file:section.md", sha)
		if err := artifactStore.Insert(ctx, a); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	rows, err := artifactStore.ListByNode(ctx, "node-1")
	if err != nil {
		t.Fatalf("ListByNode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(rows))
	}
}

func TestArtifactStore_SetValidationStatus(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	nodeStore := progress.NewNodeStore(db, fixedClock{time.Now()})
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	n := newNode(taskID, "node-1", 1, domain.NodePending)
	if err := nodeStore.Insert(ctx, n); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	a := newArtifact(taskID, "node-1", "artifact-1", "file:section.md", "deadbeef")
	if err := artifactStore.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := artifactStore.SetValidationStatus(ctx, "artifact-1", progress.ValidationPassed); err != nil {
		t.Fatalf("SetValidationStatus: %v", err)
	}
	got, err := artifactStore.Get(ctx, "artifact-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ValidationStatus != progress.ValidationPassed {
		t.Fatalf("expected validation_status passed, got %s", got.ValidationStatus)
	}
}

func TestArtifactStore_SetValidationStatus_NotFound(t *testing.T) {
	db := openTestDB(t)
	artifactStore := progress.NewArtifactStore(db)
	ctx := context.Background()

	err := artifactStore.SetValidationStatus(ctx, "missing", progress.ValidationPassed)
	if !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
