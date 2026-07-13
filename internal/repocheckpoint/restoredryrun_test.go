// restoredryrun_test.go: checkpoint-b08's required test coverage for the
// Restore dry-run deliverable (agents/checkpoint.md Part B deliverable #9;
// Auspex_ADD.md §19.6). Named so this node's own DAG validation
// command, `go test ./internal/repocheckpoint/... -run RestoreDryRun`,
// selects exactly this file.
package repocheckpoint_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
)

// captureForDryRun runs a real Capture (the same way capture_test.go does)
// and returns the resulting Row, ready to feed into RestoreDryRun/Restore.
func captureForDryRun(t *testing.T, rb *repoBuilder, artifactsRoot, id string) repocheckpoint.Row {
	t.Helper()
	client := gitx.NewClient(gitx.ExecRunner{})
	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, id), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	return result.Row
}

// TestRestoreDryRun_CleanCheckpoint_CurrentStateUnchanged_WouldSucceed is
// the baseline positive case: a checkpoint captured from a repo, with the
// repo still in EXACTLY the state it was captured from (clean, same HEAD),
// dry-run restores as fully successful with no problems at all.
func TestRestoreDryRun_CleanCheckpoint_CurrentStateUnchanged_WouldSucceed(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	artifactsRoot := t.TempDir()
	row := captureForDryRun(t, rb, artifactsRoot, "cp-1")

	client := gitx.NewClient(gitx.ExecRunner{})
	report, err := repocheckpoint.RestoreDryRun(context.Background(), client, row, rb.dir, "repo-1")
	if err != nil {
		t.Fatalf("RestoreDryRun: %v", err)
	}
	if !report.ChecksumValid {
		t.Fatalf("expected ChecksumValid=true, problems: %v", report.ChecksumProblems)
	}
	if !report.RepositoryIdentityMatch {
		t.Fatalf("expected RepositoryIdentityMatch=true, note: %s", report.RepositoryIdentityNote)
	}
	if report.WorktreeDirty {
		t.Fatalf("expected a clean worktree immediately after capture, got dirty paths: %v", report.WorktreeDirtyPaths)
	}
	if !report.StagedApplyCheck.WouldApply {
		t.Fatalf("expected staged patch to apply cleanly, detail: %s", report.StagedApplyCheck.Detail)
	}
	if !report.UnstagedApplyCheck.WouldApply {
		t.Fatalf("expected unstaged patch to apply cleanly, detail: %s", report.UnstagedApplyCheck.Detail)
	}
	if !report.WouldSucceed {
		t.Fatalf("expected overall WouldSucceed=true, problems: %v", report.Problems)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("expected zero problems, got: %v", report.Problems)
	}
}

// TestRestoreDryRun_TamperedArtifact_ChecksumInvalid_WouldNotSucceed proves
// ADD §19.6's first-listed check ("verify checksum") actually gates the
// verdict: a checkpoint whose staged.patch.gz was tampered with after
// capture must fail the dry-run's checksum check.
func TestRestoreDryRun_TamperedArtifact_ChecksumInvalid_WouldNotSucceed(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")
	rb.write("a.txt", "line1\nline2\n")
	rb.git("add", "a.txt")

	artifactsRoot := t.TempDir()
	row := captureForDryRun(t, rb, artifactsRoot, "cp-1")

	// Tamper with the staged patch artifact on disk after capture,
	// simulating corruption or interference — the same tamper style
	// verify_test.go's own TestVerify_TamperedArtifact_Invalid uses.
	patchPath := filepath.Join(row.ArtifactRoot, "staged.patch.gz")
	if err := os.WriteFile(patchPath, []byte("corrupted"), 0o644); err != nil {
		t.Fatalf("tamper staged.patch.gz: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	report, err := repocheckpoint.RestoreDryRun(context.Background(), client, row, rb.dir, "repo-1")
	if err != nil {
		t.Fatalf("RestoreDryRun: %v", err)
	}
	if report.ChecksumValid {
		t.Fatal("expected ChecksumValid=false for a tampered artifact")
	}
	if report.WouldSucceed {
		t.Fatal("expected WouldSucceed=false when the checksum check fails")
	}
	if len(report.Problems) == 0 {
		t.Fatal("expected at least one problem recorded for the tampered artifact")
	}
}

// TestRestoreDryRun_RepositoryIdentityMismatch_WouldNotSucceed proves the
// "verify repo identity" check: a dry-run evaluated with an
// expectedRepositoryID that does NOT match what the checkpoint's manifest
// recorded at capture time must fail, even though the worktree itself is
// perfectly clean and the patches would apply fine.
func TestRestoreDryRun_RepositoryIdentityMismatch_WouldNotSucceed(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	artifactsRoot := t.TempDir()
	row := captureForDryRun(t, rb, artifactsRoot, "cp-1") // captured with repository_id "repo-1" (captureReq's fixture)

	client := gitx.NewClient(gitx.ExecRunner{})
	report, err := repocheckpoint.RestoreDryRun(context.Background(), client, row, rb.dir, "a-totally-different-repo-id")
	if err != nil {
		t.Fatalf("RestoreDryRun: %v", err)
	}
	if report.RepositoryIdentityMatch {
		t.Fatal("expected RepositoryIdentityMatch=false for a mismatched repository_id")
	}
	if report.WouldSucceed {
		t.Fatal("expected WouldSucceed=false when repository identity does not match")
	}
}

// TestRestoreDryRun_DirtyWorktree_ReportedButNotVetoedByThisFunction proves
// RestoreDryRun itself only REPORTS dirtiness (it has no AllowDirty policy
// input to decide whether that should block the verdict) — WouldSucceed
// reflects checksum/identity/apply-check only, per this function's own
// documented scope. Service.Restore is where AllowDirty actually turns
// dirtiness into a veto (see the Service-level tests below).
func TestRestoreDryRun_DirtyWorktree_ReportedButNotVetoedByThisFunction(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	artifactsRoot := t.TempDir()
	row := captureForDryRun(t, rb, artifactsRoot, "cp-1")

	// Dirty the worktree AFTER capture (a new untracked file).
	rb.write("untracked.txt", "some new content\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	report, err := repocheckpoint.RestoreDryRun(context.Background(), client, row, rb.dir, "repo-1")
	if err != nil {
		t.Fatalf("RestoreDryRun: %v", err)
	}
	if !report.WorktreeDirty {
		t.Fatal("expected WorktreeDirty=true after adding an untracked file")
	}
	if len(report.WorktreeDirtyPaths) == 0 {
		t.Fatal("expected at least one dirty path recorded")
	}
	// Checksum/identity/apply-check are all still fine, so this function's
	// OWN verdict (dirty-agnostic) must still be true.
	if !report.WouldSucceed {
		t.Fatalf("expected RestoreDryRun's own WouldSucceed=true (dirty-target veto is Service.Restore's job), problems: %v", report.Problems)
	}
}

// TestRestoreDryRun_DivergedPatch_ApplyCheckFails_WouldNotSucceed proves
// the "git apply --check, staged/unstaged separately" requirement: when
// the worktree has diverged from the checkpoint's own captured base in a
// way that conflicts with the staged patch, the dry-run correctly reports
// that patch would not apply.
func TestRestoreDryRun_DivergedPatch_ApplyCheckFails_WouldNotSucceed(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")
	rb.write("a.txt", "line1\nline2\n")
	rb.git("add", "a.txt")

	artifactsRoot := t.TempDir()
	row := captureForDryRun(t, rb, artifactsRoot, "cp-1")

	// Diverge the index from the patch's own base: reset and stage
	// completely different content, so the staged patch (which expects
	// "line1" as its pre-image) no longer applies.
	rb.git("reset", "-q", "a.txt")
	rb.write("a.txt", "something else entirely\nwith different lines\n")
	rb.git("add", "a.txt")

	client := gitx.NewClient(gitx.ExecRunner{})
	report, err := repocheckpoint.RestoreDryRun(context.Background(), client, row, rb.dir, "repo-1")
	if err != nil {
		t.Fatalf("RestoreDryRun: %v", err)
	}
	if report.StagedApplyCheck.WouldApply {
		t.Fatal("expected the staged patch to no longer apply against the diverged index")
	}
	if report.WouldSucceed {
		t.Fatal("expected WouldSucceed=false when the staged apply-check fails")
	}
	if len(report.Problems) == 0 {
		t.Fatal("expected at least one problem recorded for the failed apply-check")
	}
}

// --- Service.Restore-level tests: AllowDirty policy, frozen port shape ---

func TestServiceRestoreDryRun_CleanCheckpoint_DryRunSucceeds_NotApplied(t *testing.T) {
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

	ctx := context.Background()
	created, err := svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if result.Applied {
		t.Fatal("expected Applied=false: real restore is out of vertical-slice scope, dry-run never mutates")
	}
	if result.ID != created.ID {
		t.Fatalf("expected result.ID=%s, got %s", created.ID, result.ID)
	}
}

func TestServiceRestoreDryRun_DirtyWorktree_WithoutAllowDirty_Rejected(t *testing.T) {
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

	ctx := context.Background()
	created, err := svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Dirty the worktree after capture.
	rb.write("untracked.txt", "new stuff\n")

	_, err = svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, AllowDirty: false})
	if err == nil {
		t.Fatal("expected Restore to reject a dirty target when AllowDirty is false")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict, got %#v", err)
	}
}

func TestServiceRestoreDryRun_DirtyWorktree_WithAllowDirty_Permitted(t *testing.T) {
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

	ctx := context.Background()
	created, err := svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Dirty the worktree after capture (untracked file only — does not
	// affect staged/unstaged apply-check for a.txt at all).
	rb.write("untracked.txt", "new stuff\n")

	result, err := svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, AllowDirty: true})
	if err != nil {
		t.Fatalf("expected Restore to succeed with AllowDirty=true despite a dirty target, got: %v", err)
	}
	if result.Applied {
		t.Fatal("expected Applied=false even with AllowDirty=true: dry-run never mutates")
	}
}

func TestServiceRestoreDryRun_UnknownWorktreeResolution_Errors(t *testing.T) {
	db := openTestDB(t)
	worktreeID := seedWorktree(t, db) // real worktrees(id) row, so the row insert below satisfies the FK
	store := repocheckpoint.NewStore(db)
	rb := newRepoBuilder(t)

	resolveErr := &domain.Error{Code: domain.ErrCodeNotFound, Message: "worktree gone"}
	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		return repocheckpoint.WorktreeLocation{}, resolveErr
	}
	svc := repocheckpoint.NewService(client, store, testClock(), &seqIDs{}, artifactsRoot, resolve, repocheckpoint.CaptureOptions{})

	// Insert a row directly (bypassing Create, which would itself fail on
	// this resolver) so Restore has something to load before resolution
	// fails.
	row := repocheckpoint.Row{
		ID:               "cp-orphaned",
		WorktreeID:       worktreeID,
		Status:           repocheckpoint.StatusComplete,
		ArtifactRoot:     rb.dir,
		ManifestPath:     filepath.Join(rb.dir, "manifest.json"),
		GitHead:          "deadbeef",
		IndexDiffHash:    "x",
		WorktreeDiffHash: "y",
		Recoverability:   repocheckpoint.RecoverabilityComplete,
		CreatedAt:        "2026-07-12T00:00:00Z",
	}
	if err := store.Insert(context.Background(), row); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	_, err := svc.Restore(context.Background(), app.RestoreRepositoryCheckpointRequest{ID: row.ID})
	if err == nil {
		t.Fatal("expected Restore to propagate a worktree-resolution error")
	}
}
