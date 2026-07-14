// restore_test.go: issue #6 / ADR-048's real-restore acceptance tests.
// Every test drives the FULL Service.Restore path (gate sequence + apply)
// against a real temporary Git repository and a real migrated SQLite
// store — proving the restore actually reconstructs captured state, and
// that the safety invariants (no ref mutation, no deletes, no clobber,
// dirty-target safety checkpoint) hold in fact, not just in doc comments.
package repocheckpoint_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
)

// restoreHarness bundles the full real-service setup every restore test
// needs: a seeded DB, a real repo with a committed base state, and a
// Service resolving to that repo.
type restoreHarness struct {
	svc   *repocheckpoint.Service
	store *repocheckpoint.Store
	rb    *repoBuilder
	wtID  domain.WorktreeID
}

func newRestoreHarness(t *testing.T) *restoreHarness {
	t.Helper()
	db := openTestDB(t)
	worktreeID := seedWorktree(t, db)
	store := repocheckpoint.NewStore(db)

	rb := newRepoBuilder(t)
	rb.write("a.txt", "base-a\n")
	rb.write("b.txt", "base-b\n")
	rb.write("c.txt", "base-c\n")
	binBase := make([]byte, 512)
	for i := range binBase {
		binBase[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(rb.dir, "bin.dat"), binBase, 0o644); err != nil {
		t.Fatalf("write bin.dat: %v", err)
	}
	rb.git("add", ".")
	rb.git("commit", "-q", "-m", "base")

	client := gitx.NewClient(gitx.ExecRunner{})
	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: "repo-1", Path: rb.dir}, nil
	}
	svc := repocheckpoint.NewService(client, store, testClock(), &seqIDs{}, t.TempDir(), resolve, repocheckpoint.CaptureOptions{})
	return &restoreHarness{svc: svc, store: store, rb: rb, wtID: worktreeID}
}

// makeCapturedChanges writes the canonical staged/unstaged/binary/untracked
// change set the round-trip tests capture and later expect back.
func (h *restoreHarness) makeCapturedChanges(t *testing.T) (binStaged []byte) {
	t.Helper()
	h.rb.write("a.txt", "staged-a\n")
	binStaged = make([]byte, 512)
	for i := range binStaged {
		binStaged[i] = byte((i * 7) % 253)
	}
	if err := os.WriteFile(filepath.Join(h.rb.dir, "bin.dat"), binStaged, 0o644); err != nil {
		t.Fatalf("write staged bin.dat: %v", err)
	}
	h.rb.git("add", "a.txt", "bin.dat")
	h.rb.write("b.txt", "unstaged-b\n")
	h.rb.write("new.txt", "untracked-content\n")
	return binStaged
}

// resetToCleanBase discards every uncommitted change and untracked file,
// returning the worktree to the committed base state the checkpoint's
// patches were diffed against.
func (h *restoreHarness) resetToCleanBase(t *testing.T) {
	t.Helper()
	h.rb.git("reset", "--hard", "HEAD")
	h.rb.git("clean", "-fdq")
}

func (h *restoreHarness) headSHA(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(string(h.rb.git("rev-parse", "HEAD").Stdout))
}

func (h *restoreHarness) currentBranch(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(string(h.rb.git("rev-parse", "--abbrev-ref", "HEAD").Stdout))
}

func (h *restoreHarness) readFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.rb.dir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func TestRestore_Apply_RoundTrip_CleanTarget(t *testing.T) {
	h := newRestoreHarness(t)
	ctx := context.Background()

	binStaged := h.makeCapturedChanges(t)
	created, err := h.svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: h.wtID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	h.resetToCleanBase(t)
	headBefore := h.headSHA(t)
	branchBefore := h.currentBranch(t)

	result, err := h.svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, Apply: true})
	if err != nil {
		t.Fatalf("Restore --apply: %v", err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}
	if result.SafetyCheckpointID != nil {
		t.Fatalf("clean target must not take a safety checkpoint, got %s", *result.SafetyCheckpointID)
	}

	// Content round-trips byte-exactly: text and binary, staged and
	// unstaged scopes, plus the untracked file.
	if got := h.readFile(t, "a.txt"); string(got) != "staged-a\n" {
		t.Errorf("a.txt = %q, want staged content back", got)
	}
	if got := h.readFile(t, "bin.dat"); !bytes.Equal(got, binStaged) {
		t.Error("bin.dat: binary staged change did not round-trip byte-exactly")
	}
	if got := h.readFile(t, "b.txt"); string(got) != "unstaged-b\n" {
		t.Errorf("b.txt = %q, want unstaged content back", got)
	}
	if got := h.readFile(t, "new.txt"); string(got) != "untracked-content\n" {
		t.Errorf("new.txt = %q, want untracked content back", got)
	}

	// Staged/unstaged CLASSIFICATION is restored too, not just bytes:
	// the staged files are back in the index, the unstaged one is not.
	stagedNow := string(h.rb.git("diff", "--cached", "--name-only").Stdout)
	if !strings.Contains(stagedNow, "a.txt") || !strings.Contains(stagedNow, "bin.dat") {
		t.Errorf("staged set after restore = %q, want a.txt and bin.dat staged", stagedNow)
	}
	if strings.Contains(stagedNow, "b.txt") {
		t.Errorf("b.txt must NOT be staged after restore, staged set = %q", stagedNow)
	}

	// ADR-048 / Constitution #9: no ref mutation of any kind.
	if got := h.headSHA(t); got != headBefore {
		t.Fatalf("HEAD moved during restore: %s -> %s", headBefore, got)
	}
	if got := h.currentBranch(t); got != branchBefore {
		t.Fatalf("branch changed during restore: %s -> %s", branchBefore, got)
	}
}

func TestRestore_DryRunDefault_MutatesNothing(t *testing.T) {
	h := newRestoreHarness(t)
	ctx := context.Background()

	h.makeCapturedChanges(t)
	created, err := h.svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: h.wtID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.resetToCleanBase(t)

	result, err := h.svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("Restore (dry-run): %v", err)
	}
	if result.Applied {
		t.Fatal("dry-run must never report Applied=true")
	}

	if got := h.readFile(t, "a.txt"); string(got) != "base-a\n" {
		t.Errorf("a.txt = %q: dry-run mutated the worktree", got)
	}
	if _, err := os.Lstat(filepath.Join(h.rb.dir, "new.txt")); !os.IsNotExist(err) {
		t.Error("dry-run recreated an untracked file")
	}
	if status := string(h.rb.git("status", "--porcelain").Stdout); strings.TrimSpace(status) != "" {
		t.Errorf("dry-run left the tree dirty: %q", status)
	}
}

func TestRestore_Apply_DirtyTarget_RejectedWithoutAllowDirty(t *testing.T) {
	h := newRestoreHarness(t)
	ctx := context.Background()

	h.makeCapturedChanges(t)
	created, err := h.svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: h.wtID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.resetToCleanBase(t)
	h.rb.write("c.txt", "someone's uncommitted work\n")

	_, err = h.svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, Apply: true})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict for dirty target without AllowDirty, got %v", err)
	}

	// Fail closed means NOTHING was applied.
	if got := h.readFile(t, "a.txt"); string(got) != "base-a\n" {
		t.Errorf("a.txt = %q: rejected restore still mutated the tree", got)
	}
	if got := h.readFile(t, "c.txt"); string(got) != "someone's uncommitted work\n" {
		t.Errorf("c.txt = %q: rejected restore touched the dirty file", got)
	}
}

func TestRestore_Apply_DirtyTarget_TakesSafetyCheckpointFirst(t *testing.T) {
	h := newRestoreHarness(t)
	ctx := context.Background()

	h.makeCapturedChanges(t)
	created, err := h.svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: h.wtID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.resetToCleanBase(t)
	h.rb.write("c.txt", "dirty-c\n")

	result, err := h.svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, Apply: true, AllowDirty: true})
	if err != nil {
		t.Fatalf("Restore --apply --allow-dirty: %v", err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}
	if result.SafetyCheckpointID == nil {
		t.Fatal("dirty-target apply must capture a safety checkpoint first")
	}

	// The safety checkpoint is a real, durable, verifiable checkpoint of
	// the PRE-restore dirty state.
	safetyRow, err := h.store.Get(ctx, *result.SafetyCheckpointID)
	if err != nil {
		t.Fatalf("safety checkpoint row not persisted: %v", err)
	}
	verifyResult, err := repocheckpoint.Verify(safetyRow)
	if err != nil || !verifyResult.Valid {
		t.Fatalf("safety checkpoint does not verify: valid=%v err=%v", verifyResult.Valid, err)
	}

	// The restore itself landed, and never deleted the dirty file (ADD
	// §19.6 "never delete extra files").
	if got := h.readFile(t, "a.txt"); string(got) != "staged-a\n" {
		t.Errorf("a.txt = %q, want restored staged content", got)
	}
	if got := h.readFile(t, "c.txt"); string(got) != "dirty-c\n" {
		t.Errorf("c.txt = %q: restore deleted/overwrote the dirty file", got)
	}
}

func TestRestore_Apply_NoClobber_ExistingUntrackedPreserved(t *testing.T) {
	h := newRestoreHarness(t)
	ctx := context.Background()

	h.makeCapturedChanges(t)
	created, err := h.svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: h.wtID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.resetToCleanBase(t)
	// The checkpoint's untracked path already exists with DIFFERENT
	// content — restore must skip it (disclosed), never overwrite.
	h.rb.write("new.txt", "mine, hands off\n")

	result, err := h.svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, Apply: true, AllowDirty: true})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !result.Applied {
		t.Fatal("expected Applied=true")
	}

	if got := h.readFile(t, "new.txt"); string(got) != "mine, hands off\n" {
		t.Fatalf("new.txt = %q: restore clobbered an existing file", got)
	}
	found := false
	for _, s := range result.UntrackedSkipped {
		if strings.Contains(s, "new.txt") && strings.Contains(s, string(repocheckpoint.SkipExistsNotOverwritten)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("UntrackedSkipped = %v, want a disclosed exists_not_overwritten entry for new.txt", result.UntrackedSkipped)
	}
}

func TestRestore_Apply_NoNewCommitsEver(t *testing.T) {
	h := newRestoreHarness(t)
	ctx := context.Background()

	h.makeCapturedChanges(t)
	created, err := h.svc.Create(ctx, app.CreateRepositoryCheckpointRequest{WorktreeID: h.wtID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.resetToCleanBase(t)
	countBefore := strings.TrimSpace(string(h.rb.git("rev-list", "--count", "HEAD").Stdout))

	if _, err := h.svc.Restore(ctx, app.RestoreRepositoryCheckpointRequest{ID: created.ID, Apply: true}); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	countAfter := strings.TrimSpace(string(h.rb.git("rev-list", "--count", "HEAD").Stdout))
	if countBefore != countAfter {
		t.Fatalf("commit count changed during restore: %s -> %s (Constitution #9 violated)", countBefore, countAfter)
	}
}
