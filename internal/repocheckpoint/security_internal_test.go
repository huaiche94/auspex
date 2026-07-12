package repocheckpoint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateUntrackedPath_TraversalString_Rejected is a white-box unit
// test of the path-traversal guard directly: git itself will not normally
// report a literal "../" path from ls-files, so this exercises
// validateUntrackedPath's own defense-in-depth check with a crafted
// relative path string, independent of whether Git could ever produce one
// in practice.
func TestValidateUntrackedPath_TraversalString_Rejected(t *testing.T) {
	root := t.TempDir()
	_, reason, ok := validateUntrackedPath(root, "../outside.txt")
	if ok {
		t.Fatal("expected ../ path to be rejected")
	}
	if reason != SkipPathTraversal {
		t.Fatalf("expected SkipPathTraversal, got %s", reason)
	}
}

func TestValidateUntrackedPath_NestedTraversalString_Rejected(t *testing.T) {
	root := t.TempDir()
	_, reason, ok := validateUntrackedPath(root, "subdir/../../outside.txt")
	if ok {
		t.Fatal("expected nested ../ path to be rejected")
	}
	if reason != SkipPathTraversal {
		t.Fatalf("expected SkipPathTraversal, got %s", reason)
	}
}

func TestValidateUntrackedPath_GitInternal_Rejected(t *testing.T) {
	root := t.TempDir()
	_, reason, ok := validateUntrackedPath(root, ".git/config")
	if ok {
		t.Fatal("expected .git path to be rejected")
	}
	if reason != SkipGitInternal {
		t.Fatalf("expected SkipGitInternal, got %s", reason)
	}
}

func TestValidateUntrackedPath_NestedGitInternal_Rejected(t *testing.T) {
	root := t.TempDir()
	_, reason, ok := validateUntrackedPath(root, "sub/.git/HEAD")
	if ok {
		t.Fatal("expected nested .git path to be rejected")
	}
	if reason != SkipGitInternal {
		t.Fatalf("expected SkipGitInternal, got %s", reason)
	}
}

func TestValidateUntrackedPath_OrdinaryFile_Accepted(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "sub", "file.txt")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(abs, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resolved, _, ok := validateUntrackedPath(root, "sub/file.txt")
	if !ok {
		t.Fatal("expected ordinary nested file to be accepted")
	}
	if resolved != abs {
		t.Fatalf("expected resolved path %s, got %s", abs, resolved)
	}
}

func TestValidateUntrackedPath_SymlinkFile_Rejected(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	_, reason, ok := validateUntrackedPath(root, "link.txt")
	if ok {
		t.Fatal("expected symlink to be rejected")
	}
	if reason != SkipSymlink {
		t.Fatalf("expected SkipSymlink, got %s", reason)
	}
}

func TestValidateUntrackedPath_SymlinkedParentDir_Rejected(t *testing.T) {
	root := t.TempDir()
	realParent := t.TempDir()
	target := filepath.Join(realParent, "file.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	_, reason, ok := validateUntrackedPath(root, "linked-parent/file.txt")
	if ok {
		t.Fatal("expected path through a symlinked parent directory to be rejected")
	}
	if reason != SkipSymlink {
		t.Fatalf("expected SkipSymlink, got %s", reason)
	}
}

func TestValidateUntrackedPath_EmptyPath_Rejected(t *testing.T) {
	root := t.TempDir()
	_, reason, ok := validateUntrackedPath(root, "")
	if ok {
		t.Fatal("expected empty path to be rejected")
	}
	if reason != SkipUnreadable {
		t.Fatalf("expected SkipUnreadable, got %s", reason)
	}
}
