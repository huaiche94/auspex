package repocheckpoint_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/gitx"
	"github.com/huaiche94/preflight/internal/repocheckpoint"
)

func newTestService(t *testing.T, worktreeID domain.WorktreeID, rb *repoBuilder) (*repocheckpoint.Service, *repocheckpoint.Store) {
	t.Helper()
	db := openTestDB(t)
	store := repocheckpoint.NewStore(db)
	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: "repo-1", Path: rb.dir}, nil
	}

	svc := repocheckpoint.NewService(client, store, testClock(), &seqIDs{}, artifactsRoot, resolve, repocheckpoint.CaptureOptions{})
	return svc, store
}

func TestService_CreateThenVerify_RoundTrips(t *testing.T) {
	db := openTestDB(t)
	worktreeID := seedWorktree(t, db)
	store := repocheckpoint.NewStore(db)

	rb := newRepoBuilder(t)
	rb.write("a.txt", "content\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		return repocheckpoint.WorktreeLocation{RepositoryID: "repo-1", Path: rb.dir}, nil
	}
	svc := repocheckpoint.NewService(client, store, testClock(), &seqIDs{}, artifactsRoot, resolve, repocheckpoint.CaptureOptions{})

	var _ app.RepositoryCheckpointService = svc

	ctx := context.Background()
	created, err := svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != string(repocheckpoint.StatusComplete) {
		t.Fatalf("expected status complete, got %s", created.Status)
	}

	verification, err := svc.Verify(ctx, created.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.Valid {
		t.Fatal("expected verification to be valid immediately after create")
	}

	row, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.VerifiedAt == nil {
		t.Fatal("expected VerifiedAt to be set after a successful Verify")
	}
}

func TestService_Create_UnknownWorktree_Errors(t *testing.T) {
	rb := newRepoBuilder(t)
	svc, _ := newTestService(t, "known-worktree", rb)

	_, err := svc.Create(context.Background(), app.CreateRepositoryCheckpointRequest{WorktreeID: "unknown-worktree"})
	if err == nil {
		t.Fatal("expected error for unknown worktree")
	}
}

func TestService_Restore_NotImplemented(t *testing.T) {
	rb := newRepoBuilder(t)
	svc, _ := newTestService(t, "worktree-1", rb)

	_, err := svc.Restore(context.Background(), app.RestoreRepositoryCheckpointRequest{ID: "cp-1"})
	if err == nil {
		t.Fatal("expected Restore to return an explicit not-implemented error")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T", err)
	}
	if domErr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("expected ErrCodeUnavailable, got %s", domErr.Code)
	}
}

func TestService_Verify_UnknownCheckpoint_Errors(t *testing.T) {
	rb := newRepoBuilder(t)
	svc, _ := newTestService(t, "worktree-1", rb)

	_, err := svc.Verify(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown checkpoint ID")
	}
}
