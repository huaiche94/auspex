package gitx

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestDiffPatch_StagedChange_ProducesApplicablePatch(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("a.txt", "line1\nline2\n")
	rb.git("add", "a.txt")

	c := NewClient(rb.runner)
	patch, err := c.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if !bytes.Contains(patch, []byte("a.txt")) {
		t.Fatalf("expected patch to mention a.txt, got: %s", patch)
	}
	if !bytes.Contains(patch, []byte("+line2")) {
		t.Fatalf("expected patch to show added line2, got: %s", patch)
	}
}

func TestDiffPatch_Unstaged_ProducesPatch(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("a.txt", "line1\nunstaged change\n")

	c := NewClient(rb.runner)
	patch, err := c.DiffPatch(context.Background(), rb.dir, false)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if !bytes.Contains(patch, []byte("unstaged change")) {
		t.Fatalf("expected patch to show unstaged change, got: %s", patch)
	}
}

func TestDiffPatch_NoChanges_EmptyPatchNoError(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	c := NewClient(rb.runner)
	patch, err := c.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if len(patch) != 0 {
		t.Fatalf("expected empty patch for no staged changes, got: %s", patch)
	}
}

func TestDiffPatch_BinaryFile_UsesBinaryDirective(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	binPath := rb.dir + "/bin.dat"
	if err := writeBinaryFixture(binPath); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	rb.git("add", "bin.dat")

	c := NewClient(rb.runner)
	patch, err := c.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if !bytes.Contains(patch, []byte("GIT binary patch")) && !bytes.Contains(patch, []byte("Binary files")) {
		t.Fatalf("expected a binary-patch directive in output, got: %s", patch)
	}
}

func TestListUntracked_ReturnsNonIgnoredFiles(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write(".gitignore", "ignored.txt\n")
	rb.write("ignored.txt", "should not appear\n")
	rb.write("untracked.txt", "should appear\n")

	c := NewClient(rb.runner)
	paths, err := c.ListUntracked(context.Background(), rb.dir)
	if err != nil {
		t.Fatalf("ListUntracked: %v", err)
	}

	joined := strings.Join(paths, ",")
	if !strings.Contains(joined, "untracked.txt") {
		t.Fatalf("expected untracked.txt in result, got: %v", paths)
	}
	if strings.Contains(joined, "ignored.txt") {
		t.Fatalf("expected ignored.txt to be excluded, got: %v", paths)
	}
}

func TestListUntracked_NoUntrackedFiles_EmptyResult(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "content\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	c := NewClient(rb.runner)
	paths, err := c.ListUntracked(context.Background(), rb.dir)
	if err != nil {
		t.Fatalf("ListUntracked: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no untracked files, got: %v", paths)
	}
}

func writeBinaryFixture(path string) error {
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	return os.WriteFile(path, data, 0o644)
}
