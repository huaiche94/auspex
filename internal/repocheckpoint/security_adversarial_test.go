// security_adversarial_test.go: checkpoint-b09, the Part B final security
// gate (agents/checkpoint.md Part B; EXECUTION_DAG.md's own risk callout:
// "path traversal/symlink escape tests are a security gate"). Earlier
// nodes (b04-b08) already built and unit-tested the individual guards
// (validateUntrackedPath, atomic writes, restore dry-run); this node's job
// is to attack the FULL pipeline (Capture -> archive -> manifest -> Verify
// -> RestoreDryRun) with genuinely malicious-shaped fixtures end to end,
// closing any remaining gap rather than re-proving what b02-b08 already
// covered, so qa-06's own dedicated malicious-fixture test
// (EXECUTION_DAG.md: "Feeds qa-06") can build on strong footing instead of
// starting from scratch.
//
// Every malicious fixture constructed in this file:
//
//  1. a symlink inside the worktree pointing OUTSIDE the repo root
//     (top-level and nested-directory-via-symlink variants — extends
//     security_test.go's existing single top-level-symlink case);
//  2. a `../` path-traversal component, exercised at TWO different
//     pipeline entry points: the untracked-archive path (already covered
//     by security_internal_test.go's unit tests, re-proven here end to
//     end via a real Capture) AND, the genuine NEW gap this node's audit
//     found, a manifest.json `artifacts[].path` field read back by Verify
//     (see the "Genuine finding" note below);
//  3. a caller-supplied CheckpointID containing `../` (Capture's own
//     ArtifactsRoot/<CheckpointID> join);
//  4. an embedded-newline filename (a real, platform-permitted "special
//     character" shape distinct from bare spaces, which b04 already
//     covers) — proven to survive the NUL-terminated `-z` parsing this
//     package already uses rather than corrupting it;
//  5. an oversized untracked file (re-run here as part of the same
//     end-to-end adversarial sweep, not a new mechanism).
//
// Genuine finding fixed by this node: Verify (verify.go) previously joined
// manifest.Artifacts[].Path directly onto row.ArtifactRoot with NO
// traversal/symlink check at all (unlike validateUntrackedPath's identical
// treatment of git-reported paths) — a hand-edited or maliciously restored
// manifest.json with an artifact path like "../../../etc/passwd" made
// Verify read and hash an arbitrary file outside the checkpoint's own
// directory. Confirmed exploitable with a standalone reproduction before
// being fixed via the new safeArtifactPath guard (security.go) that
// verify.go now calls; TestVerify_ManifestArtifactPathTraversal_Rejected
// below is the permanent regression test for that fix. writeArtifactDir
// was also hardened with an analogous safeRelativeName check
// (atomicwrite.go) as defense in depth, even though no production caller
// currently supplies an unsafe files-map key — see
// security_adversarial_internal_test.go for that white-box coverage and
// capture.go's CheckpointID guard.
//
// Every scenario here uses ONLY argv-based git invocations (via gitx.Client,
// this whole role's established discipline — Constitution §7 rule 5) —
// never a shell string — and every destructive assertion checks that the
// malicious path never appears INSIDE the produced archive/manifest/temp
// directory tree, not merely that Capture "didn't error."
package repocheckpoint_test

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/preflight/internal/gitx"
	"github.com/huaiche94/preflight/internal/repocheckpoint"
)

// --- 1. Symlink escape: nested-directory-via-symlink variant, extending
// security_test.go's existing top-level-symlink case with a symlinked
// DIRECTORY several levels deep, and a symlink whose target does not even
// exist (a dangling symlink, which os.Stat cannot resolve at all) --------

func TestAdversarial_SymlinkedDirectory_NestedDeep_NeverArchived(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("normal/keep.txt", "ordinary tracked-adjacent content\n")
	rb.git("add", "normal/keep.txt")
	rb.git("commit", "-q", "-m", "add normal file")

	outsideDir := t.TempDir()
	secretFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("EXFILTRATED"), 0o644); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}

	// Nested: worktree/a/b/escape -> outsideDir (a symlinked directory two
	// levels deep, not a top-level symlink file).
	if err := os.MkdirAll(filepath.Join(rb.dir, "a", "b"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDir := filepath.Join(rb.dir, "a", "b", "escape")
	if err := os.Symlink(outsideDir, linkDir); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-adv-1"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-adv-1", "untracked.zip")
	assertZipNeverContains(t, zipPath, "secret.txt", "EXFILTRATED")

	if result.Manifest.Recoverability.SkippedFileCount == 0 {
		t.Fatal("expected the file reached only through a nested symlinked directory to be recorded as skipped")
	}
}

func TestAdversarial_DanglingSymlink_NeverCrashesCapture(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	link := filepath.Join(rb.dir, "dangling.txt")
	if err := os.Symlink(filepath.Join(t.TempDir(), "does-not-exist.txt"), link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-adv-2"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture must not error on a dangling symlink, got: %v", err)
	}
	if result.Manifest.Recoverability.SkippedFileCount == 0 {
		t.Fatal("expected the dangling symlink to be recorded as skipped, not silently dropped or archived")
	}
	zipPath := filepath.Join(artifactsRoot, "cp-adv-2", "untracked.zip")
	if _, statErr := os.Stat(zipPath); statErr == nil {
		names := mustOpenZip(t, zipPath)
		for _, name := range names {
			if name == "dangling.txt" {
				t.Fatal("a dangling symlink must never appear as an archived entry")
			}
		}
	}
}

// --- 2a. Path traversal via the untracked-archive pipeline, proven end to
// end through a REAL Capture call (not just validateUntrackedPath's own
// unit test) using a symlink whose name itself looks like an attempted
// escape, combined with a sibling ordinary file, so a single Capture run
// exercises both the reject-path and the accept-path together ------------

func TestAdversarial_Capture_TraversalShapedSymlinkName_RejectedAlongsideOrdinaryFile(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("ordinary.txt", "this one must survive\n")

	outsideFile := filepath.Join(t.TempDir(), "outside-payload.txt")
	if err := os.WriteFile(outsideFile, []byte("must never be archived"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	// A symlink whose own on-disk name contains no ".." (git would never
	// report one that does — the traversal-string case is exercised
	// directly against validateUntrackedPath by security_internal_test.go
	// and against Verify's own manifest-path variant below) but whose
	// TARGET escapes the worktree — the realistic vector this package
	// actually has to defend Capture's live pipeline against.
	escapeLink := filepath.Join(rb.dir, "looks-like-evidence.txt")
	if err := os.Symlink(outsideFile, escapeLink); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-adv-3"), repocheckpoint.CaptureOptions{}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-adv-3", "untracked.zip")
	names := mustOpenZip(t, zipPath)
	foundOrdinary := false
	for _, n := range names {
		if n == "looks-like-evidence.txt" {
			t.Fatal("symlink-escape entry must never be archived")
		}
		if n == "ordinary.txt" {
			foundOrdinary = true
		}
	}
	if !foundOrdinary {
		t.Fatalf("expected the ordinary sibling file to still be archived; entries: %v", names)
	}
	assertZipNeverContains(t, zipPath, "outside-payload.txt", "must never be archived")
}

// --- 2b. Path traversal via a TAMPERED manifest.json read back by Verify
// -- the genuine new gap this node's adversarial audit found and fixed.
// This is an end-to-end proof (goes through the real Verify entry point on
// a real, disk-resident manifest, not a call to the internal
// safeArtifactPath helper directly — that unit-level coverage lives in
// security_adversarial_internal_test.go) --------------------------------

func TestVerify_ManifestArtifactPathTraversal_Rejected(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("real.txt", "genuine checkpoint content\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	captureResult, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-adv-4"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// A secret file OUTSIDE the checkpoint's own artifact directory, that
	// a real restore-time Verify call must never be able to read.
	secretsDir := t.TempDir()
	secretFile := filepath.Join(secretsDir, "totally-secret.txt")
	if err := os.WriteFile(secretFile, []byte("SHOULD NEVER BE READ BY VERIFY"), 0o644); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	checkpointDir := filepath.Join(artifactsRoot, "cp-adv-4")
	traversal, err := filepath.Rel(checkpointDir, secretFile)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if !filepathHasTraversal(traversal) {
		t.Fatalf("test setup bug: expected a ../-containing relative path, got %q", traversal)
	}

	// Hand-tamper manifest.json exactly the way a compromised or
	// maliciously restored checkpoint directory could: rewrite the
	// (legitimate) untracked.zip artifact entry's path to the traversal
	// string above, keeping every other field byte-identical so this is a
	// surgical, realistic tamper rather than a wholesale rewrite.
	manifestPath := filepath.Join(checkpointDir, "manifest.json")
	tamperManifestArtifactPath(t, manifestPath, "untracked.zip", traversal)

	row := captureResult.Row
	result, err := repocheckpoint.Verify(row)
	if err != nil {
		t.Fatalf("Verify returned a hard error instead of a fail-closed report: %v", err)
	}
	if result.Valid {
		t.Fatal("SECURITY: Verify accepted a manifest whose artifact path escapes the checkpoint's artifact root")
	}
	foundTraversalProblem := false
	for _, p := range result.Problems {
		if containsSubstring(p, "escapes checkpoint artifact root") {
			foundTraversalProblem = true
		}
	}
	if !foundTraversalProblem {
		t.Fatalf("expected a traversal-specific problem message, got: %v", result.Problems)
	}

	// The strongest possible assertion: the secret file's content must
	// never have been read at all. There is no direct hook to observe
	// "was this file opened," so this checks the next best thing — the
	// secret's exact content never appears anywhere in the problem report
	// (a naive implementation might, e.g., echo back read bytes in an
	// error message).
	for _, p := range result.Problems {
		if containsSubstring(p, "SHOULD NEVER BE READ BY VERIFY") {
			t.Fatal("SECURITY: the secret file's content leaked into Verify's problem report")
		}
	}
}

// --- 3. Malicious CheckpointID: Capture's own ArtifactsRoot/<CheckpointID>
// join must reject a traversal-shaped ID rather than escaping ArtifactsRoot

func TestAdversarial_Capture_MaliciousCheckpointID_TraversalRejected(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("a.txt", "content\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	_, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "../../escape-checkpoint-id"), repocheckpoint.CaptureOptions{})
	if err == nil {
		t.Fatal("expected Capture to reject a traversal-shaped CheckpointID")
	}

	// Nothing must have been written outside artifactsRoot as a result of
	// the attempted escape.
	escapedDir := filepath.Join(filepath.Dir(filepath.Dir(artifactsRoot)), "escape-checkpoint-id")
	if _, statErr := os.Stat(escapedDir); statErr == nil {
		t.Fatal("SECURITY: Capture created a directory outside ArtifactsRoot from a malicious CheckpointID")
	}
}

func TestAdversarial_Capture_MaliciousCheckpointID_AbsolutePathRejected(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")
	rb.write("a.txt", "content\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	absoluteEscape := filepath.Join(t.TempDir(), "abs-escape")

	_, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, absoluteEscape), repocheckpoint.CaptureOptions{})
	if err == nil {
		t.Fatal("expected Capture to reject an absolute-path-shaped CheckpointID")
	}
	if _, statErr := os.Stat(absoluteEscape); statErr == nil {
		t.Fatal("SECURITY: Capture created a directory at an absolute attacker-chosen path")
	}
}

// --- 4. Embedded newline in a filename: a real, platform-permitted
// "special character" shape (macOS/Linux both allow \n in a filename; only
// NUL and '/' are truly forbidden at the filesystem level) distinct from
// the existing spaces-in-path coverage (TestCapture_SpacesAndNewlinesInPath
// in capture_test.go already covers a name with a SPACE; this test adds
// the literal-embedded-NEWLINE-byte case specifically named in this node's
// own required-tests list: "spaces/newlines in path where platform
// permits") ---------------------------------------------------------------

func TestAdversarial_EmbeddedNewlineInFilename_SurvivesNULTerminatedParsing(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	nameWithNewline := "evidence\nwith-embedded-newline.txt"
	path := filepath.Join(rb.dir, nameWithNewline)
	if err := os.WriteFile(path, []byte("content after a newline-bearing name\n"), 0o644); err != nil {
		t.Skipf("filesystem does not support embedded newlines in filenames: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-adv-5"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture must handle an embedded-newline filename cleanly (NUL-terminated -z parsing), got: %v", err)
	}
	if result.Manifest.Snapshot.UntrackedFiles != 1 {
		t.Fatalf("expected exactly 1 untracked file (the newline-named one), got %d — a newline-unsafe parser would likely miscount", result.Manifest.Snapshot.UntrackedFiles)
	}

	zipPath := filepath.Join(artifactsRoot, "cp-adv-5", "untracked.zip")
	names := mustOpenZip(t, zipPath)
	found := false
	for _, n := range names {
		if n == nameWithNewline {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the exact newline-bearing name to appear in the archive, got entries: %v", names)
	}
}

// --- 5. Oversize file, re-run as part of this node's own end-to-end
// adversarial sweep (the underlying mechanism is checkpoint-b04's own
// TestCapture_OversizeFile_Skipped; this variant additionally combines it
// with a symlink escape attempt in the SAME capture, proving the two
// independent guards both fire correctly in one pass rather than one
// masking the other) ------------------------------------------------------

func TestAdversarial_OversizeFile_AndSymlinkEscape_BothRejectedInSameCapture(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	big := make([]byte, 2048)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(rb.dir, "big.bin"), big, 0o644); err != nil {
		t.Fatalf("write big file: %v", err)
	}

	outsideFile := filepath.Join(t.TempDir(), "secret2.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(rb.dir, "escape2.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-adv-6"), repocheckpoint.CaptureOptions{
			MaxUntrackedFileBytes: 100,
		})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if result.Manifest.Recoverability.SkippedFileCount != 2 {
		t.Fatalf("expected both the oversize file AND the symlink escape to be independently skipped, got %d skipped", result.Manifest.Recoverability.SkippedFileCount)
	}
	zipPath := filepath.Join(artifactsRoot, "cp-adv-6", "untracked.zip")
	if _, statErr := os.Stat(zipPath); statErr == nil {
		names := mustOpenZip(t, zipPath)
		for _, n := range names {
			if n == "big.bin" || n == "escape2.txt" {
				t.Fatalf("neither the oversize file nor the symlink escape may appear in the archive, found: %s", n)
			}
		}
	}
}

// --- helpers --------------------------------------------------------------

func assertZipNeverContains(t *testing.T, zipPath, forbiddenName, forbiddenContentSubstring string) {
	t.Helper()
	if _, statErr := os.Stat(zipPath); statErr != nil {
		return // no archive at all is trivially safe
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip %s: %v", zipPath, err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.Name == forbiddenName {
			t.Fatalf("SECURITY: forbidden entry %q present in archive", forbiddenName)
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		buf := make([]byte, 4096)
		n, _ := rc.Read(buf)
		_ = rc.Close()
		if n > 0 && containsSubstring(string(buf[:n]), forbiddenContentSubstring) {
			t.Fatalf("SECURITY: forbidden content substring %q found inside archive entry %q", forbiddenContentSubstring, f.Name)
		}
	}
}

func filepathHasTraversal(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// containsSubstring is defined once for this package's external test suite
// in untracked_test.go (a thin strings.Contains wrapper); reused here
// rather than redeclared.

// tamperManifestArtifactPath rewrites exactly one artifacts[].path field
// inside a real manifest.json on disk, keeping the rest of the document
// byte-for-byte as Capture produced it — a surgical, realistic tamper
// (this is exactly the shape a compromised checkpoint directory, or a hand
// edited manifest from an untrusted "restore this checkpoint" source,
// would take) rather than fabricating a synthetic manifest from scratch.
func tamperManifestArtifactPath(t *testing.T, manifestPath, artifactName, newPath string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	oldField := `"path":"` + artifactName + `"`
	newField := `"path":"` + jsonEscape(newPath) + `"`
	replaced := strings.Replace(string(raw), oldField, newField, 1)
	if replaced == string(raw) {
		// The manifest may have been marshaled with indentation/spacing
		// this exact literal doesn't match; fall back to a looser
		// find-and-replace on the artifact name alone inside a
		// "path": "..." shape (still surgical: only ONE field changes).
		oldFieldSpaced := `"path": "` + artifactName + `"`
		newFieldSpaced := `"path": "` + jsonEscape(newPath) + `"`
		replaced = strings.Replace(string(raw), oldFieldSpaced, newFieldSpaced, 1)
	}
	if replaced == string(raw) {
		t.Fatalf("tamperManifestArtifactPath: could not find artifact %q path field to tamper in manifest:\n%s", artifactName, raw)
	}
	if err := os.WriteFile(manifestPath, []byte(replaced), 0o644); err != nil {
		t.Fatalf("write tampered manifest: %v", err)
	}
}

func jsonEscape(s string) string {
	out := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			out = append(out, '\\', '\\')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}
