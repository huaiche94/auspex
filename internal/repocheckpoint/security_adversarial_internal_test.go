// security_adversarial_internal_test.go: checkpoint-b09, white-box unit
// coverage of the two path-safety primitives this node's own security
// audit added (safeArtifactPath, safeRelativeName — security.go) as
// defense in depth beyond validateUntrackedPath's existing, already
// well-tested guard (security_internal_test.go). See
// security_adversarial_test.go for the end-to-end fixture proofs (real
// symlinks escaping a real repo, a tampered manifest actually read through
// Verify) that these unit tests' assumptions hold at the package's real
// entry points too.
package repocheckpoint

import (
	"os"
	"path/filepath"
	"testing"
)

// --- safeArtifactPath (manifest.Artifacts[].Path validation, added by this
// node after finding Verify previously joined a manifest-declared artifact
// path onto ArtifactRoot with NO traversal check at all — see verify.go's
// call site and this node's own lessons-learned entry) -------------------

func TestSafeArtifactPath_TraversalString_Rejected(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeArtifactPath(root, "../outside.txt"); ok {
		t.Fatal("expected ../ artifact path to be rejected")
	}
}

func TestSafeArtifactPath_DeeplyNestedTraversal_Rejected(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeArtifactPath(root, "a/b/c/../../../../../../etc/passwd"); ok {
		t.Fatal("expected deeply nested ../ escape to be rejected")
	}
}

func TestSafeArtifactPath_AbsolutePath_Rejected(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeArtifactPath(root, "/etc/passwd"); ok {
		t.Fatal("expected an absolute artifact path to be rejected")
	}
}

func TestSafeArtifactPath_AbsoluteWindowsStylePath_RejectedOnThisPlatform(t *testing.T) {
	// filepath.IsAbs is platform-dependent (a "C:\..." string is not
	// absolute per Go's own path/filepath on non-Windows); the leading-"/"
	// check below is what actually catches a slash-rooted path regardless
	// of GOOS, and is asserted directly here rather than relying on
	// filepath.IsAbs's platform-specific behavior for this exact string.
	root := t.TempDir()
	if _, ok := safeArtifactPath(root, "/windows/style/absolute"); ok {
		t.Fatal("expected a leading-slash path to be rejected regardless of platform")
	}
}

func TestSafeArtifactPath_OrdinaryNestedFile_Accepted(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "sub", "file.txt")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(abs, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resolved, ok := safeArtifactPath(root, "sub/file.txt")
	if !ok {
		t.Fatal("expected an ordinary nested artifact path to be accepted")
	}
	if resolved != abs {
		t.Fatalf("expected resolved path %s, got %s", abs, resolved)
	}
}

func TestSafeArtifactPath_TopLevelFile_Accepted(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeArtifactPath(root, "manifest.json"); !ok {
		t.Fatal("expected a plain top-level artifact name to be accepted")
	}
}

func TestSafeArtifactPath_SymlinkArtifact_Rejected(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "real.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	if _, ok := safeArtifactPath(root, "link.txt"); ok {
		t.Fatal("expected a symlinked artifact path to be rejected")
	}
}

func TestSafeArtifactPath_SymlinkedAncestorDir_Rejected(t *testing.T) {
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
	if _, ok := safeArtifactPath(root, "linked-parent/file.txt"); ok {
		t.Fatal("expected an artifact path through a symlinked ancestor directory to be rejected")
	}
}

func TestSafeArtifactPath_EmptyPath_Rejected(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeArtifactPath(root, ""); ok {
		t.Fatal("expected an empty artifact path to be rejected")
	}
}

func TestSafeArtifactPath_MissingFile_StillResolvesForCallerToReportMissing(t *testing.T) {
	// A path component that legitimately does not exist YET (e.g. Verify
	// checking an artifact that was never written) must not itself be
	// treated as a traversal — the caller's own os.Stat immediately after
	// this call is what reports "missing", exactly as it already did
	// before this node's fix.
	root := t.TempDir()
	resolved, ok := safeArtifactPath(root, "never-written.txt")
	if !ok {
		t.Fatal("expected a nonexistent-but-safe artifact path to still resolve")
	}
	if filepath.Dir(resolved) != filepath.Clean(root) {
		t.Fatalf("expected resolved path under root, got %s", resolved)
	}
}

// --- safeRelativeName (writeArtifactDir's files-map-key guard) ----------

func TestSafeRelativeName_TraversalSegment_Rejected(t *testing.T) {
	if safeRelativeName("../escape.txt") {
		t.Fatal("expected a ../ name to be rejected")
	}
}

func TestSafeRelativeName_NestedTraversalSegment_Rejected(t *testing.T) {
	if safeRelativeName("a/b/../../../escape.txt") {
		t.Fatal("expected a nested ../ name to be rejected")
	}
}

func TestSafeRelativeName_AbsolutePath_Rejected(t *testing.T) {
	if safeRelativeName("/etc/passwd") {
		t.Fatal("expected an absolute name to be rejected")
	}
}

func TestSafeRelativeName_Empty_Rejected(t *testing.T) {
	if safeRelativeName("") {
		t.Fatal("expected an empty name to be rejected")
	}
}

func TestSafeRelativeName_OrdinaryName_Accepted(t *testing.T) {
	for _, name := range []string{"manifest.json", "untracked.zip", "sub/dir/file.txt", "staged.patch.gz"} {
		if !safeRelativeName(name) {
			t.Fatalf("expected ordinary name %q to be accepted", name)
		}
	}
}

// --- writeArtifactDir defense in depth (rejects a malicious files map key
// even though no production caller currently supplies one) ---------------

func TestWriteArtifactDir_MaliciousFilesMapKey_TraversalRejected(t *testing.T) {
	root := t.TempDir()
	finalDir := filepath.Join(root, "checkpoints", "cp-evil")
	outsideMarker := filepath.Join(root, "escaped.txt")

	err := writeArtifactDir(finalDir, map[string][]byte{
		"../../escaped.txt": []byte("should never land outside finalDir's parent chain"),
	})
	if err == nil {
		t.Fatal("expected writeArtifactDir to reject a traversal-shaped files map key")
	}
	if _, statErr := os.Stat(outsideMarker); statErr == nil {
		t.Fatal("SECURITY: writeArtifactDir wrote a file outside its intended directory tree")
	}
	if _, statErr := os.Stat(finalDir); statErr == nil {
		t.Fatal("expected finalDir to never be committed when any file in the batch is rejected")
	}
}

func TestWriteArtifactDir_MaliciousFilesMapKey_AbsolutePathRejected(t *testing.T) {
	root := t.TempDir()
	finalDir := filepath.Join(root, "checkpoints", "cp-evil-abs")
	outsideAbs := filepath.Join(t.TempDir(), "absolute-escape.txt")

	err := writeArtifactDir(finalDir, map[string][]byte{
		outsideAbs: []byte("must never be written to an absolute, caller-controlled path"),
	})
	if err == nil {
		t.Fatal("expected writeArtifactDir to reject an absolute-path files map key")
	}
	if _, statErr := os.Stat(outsideAbs); statErr == nil {
		t.Fatal("SECURITY: writeArtifactDir wrote a file at an absolute attacker-chosen path")
	}
}
