// patch_test.go: checkpoint-b05's assigned hardening of binary-safe patch
// generation (agents/checkpoint.md Part B deliverable #5). checkpoint-b04
// proved only that a binary-patch directive APPEARS in the output
// (TestCapture_BinaryFile); its own progress-artifact note flagged the
// remaining gap explicitly: "b05's own deeper edge cases (e.g. very large
// binary diffs, mixed binary+text in one patch, apply-round-trip
// verification) are NOT built here." This file closes that gap.
//
// Tests live in package repocheckpoint_test (not internal/gitx) because
// the DAG's frozen validation command for this node is
// `go test ./internal/repocheckpoint/... -run Patch` — every test name
// below carries "Patch" so that command selects exactly this file, mirroring
// the same per-node test-selection convention checkpoint-a01/a04 already
// established for their own DAG-selected test files.
//
// The apply-round-trip test is the one genuinely new proof this node adds:
// generate a patch via Capture's own gitx.Client.DiffPatch path, then
// apply it (`git apply`, argv-only) to a SEPARATE, freshly-checked-out
// clone of the same base commit, and assert the resulting file content is
// byte-identical to the original repo's working tree — proving the patch
// is not just "contains an expected substring" but is actually
// reconstructable evidence, the whole point of a Repository Checkpoint
// (ADD §19.1).
package repocheckpoint_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
)

// applyPatchToFreshCheckout clones the repo at rb.dir into a new temp
// directory checked out at HEAD (the base commit the patch was diffed
// against), then applies patchBytes there via `git apply` (argv-only, per
// Constitution §7 rule 5 — this test never builds a shell command string).
// Returns the path to the fresh checkout for the caller to inspect.
func applyPatchToFreshCheckout(t *testing.T, rb *repoBuilder, patchBytes []byte) string {
	t.Helper()
	if len(bytes.TrimSpace(patchBytes)) == 0 {
		t.Fatalf("applyPatchToFreshCheckout: empty patch")
	}

	freshDir := t.TempDir()
	// Clone rather than copy: a clone gives a real, independent .git with
	// the same commit graph, so `git apply` resolves the patch's
	// pre-image blobs (via --full-index) against a working tree that
	// genuinely matches the patch's base, not a filesystem copy that
	// happens to look similar.
	// -c core.autocrlf=false lands in the clone's own config BEFORE its
	// initial checkout runs, so the fresh worktree materializes the same
	// LF bytes the fixture repo holds — repo-level config does not follow
	// a clone, and windows-latest's system git defaults autocrlf=true,
	// which would CRLF-rewrite the checkout and every later `git apply`
	// write, breaking the byte-exact round-trip assertions (issue #24).
	cloneCmd := exec.Command("git", "clone", "-q", "-c", "core.autocrlf=false", rb.dir, freshDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, out)
	}

	patchPath := filepath.Join(t.TempDir(), "round-trip.patch")
	if err := os.WriteFile(patchPath, patchBytes, 0o644); err != nil {
		t.Fatalf("write patch file: %v", err)
	}

	checkCmd := exec.Command("git", "apply", "--check", patchPath)
	checkCmd.Dir = freshDir
	if out, err := checkCmd.CombinedOutput(); err != nil {
		t.Fatalf("git apply --check failed (patch does not cleanly apply to a fresh checkout of the same base commit): %v: %s", err, out)
	}

	applyCmd := exec.Command("git", "apply", patchPath)
	applyCmd.Dir = freshDir
	if out, err := applyCmd.CombinedOutput(); err != nil {
		t.Fatalf("git apply failed: %v: %s", err, out)
	}
	return freshDir
}

// TestDiffPatch_ApplyRoundTrip_TextChange_MatchesOriginal is the required
// "apply round-trip" test for the plain-text case: generate a patch,
// apply it to a fresh checkout, verify the result matches.
func TestDiffPatch_ApplyRoundTrip_TextChange_MatchesOriginal(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "line1\nline2\nline3\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("a.txt", "line1\nline2 CHANGED\nline3\nline4\n")
	rb.git("add", "a.txt")

	client := gitx.NewClient(gitx.ExecRunner{})
	patch, err := client.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}

	freshDir := applyPatchToFreshCheckout(t, rb, patch)

	want, err := os.ReadFile(filepath.Join(rb.dir, "a.txt"))
	if err != nil {
		t.Fatalf("read original a.txt: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(freshDir, "a.txt"))
	if err != nil {
		t.Fatalf("read applied a.txt: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("round-trip mismatch:\nwant: %q\ngot:  %q", want, got)
	}
}

// TestDiffPatch_ApplyRoundTrip_BinaryFile_MatchesOriginalByteForByte proves
// a binary artifact survives the patch round-trip byte-for-byte, not just
// that a binary directive appears in the patch text (checkpoint-b04's
// existing, shallower assertion).
func TestDiffPatch_ApplyRoundTrip_BinaryFile_MatchesOriginalByteForByte(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.git("commit", "--allow-empty", "-q", "-m", "initial")

	binData := make([]byte, 4096)
	for i := range binData {
		binData[i] = byte((i * 37) % 256)
	}
	if err := os.WriteFile(filepath.Join(rb.dir, "asset.bin"), binData, 0o644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	rb.git("add", "asset.bin")

	client := gitx.NewClient(gitx.ExecRunner{})
	patch, err := client.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if !bytes.Contains(patch, []byte("GIT binary patch")) {
		t.Fatalf("expected a GIT binary patch directive, got: %s", patch)
	}

	freshDir := applyPatchToFreshCheckout(t, rb, patch)

	got, err := os.ReadFile(filepath.Join(freshDir, "asset.bin"))
	if err != nil {
		t.Fatalf("read applied asset.bin: %v", err)
	}
	if !bytes.Equal(binData, got) {
		t.Fatalf("binary round-trip mismatch: original %d bytes, got %d bytes, equal=%v", len(binData), len(got), bytes.Equal(binData, got))
	}
}

// TestDiffPatch_MixedBinaryAndTextChangeset_BothSurviveRoundTrip is the
// required "mixed binary+text" edge case: a single changeset touching both
// a text file and a binary file in the SAME patch, applied together.
func TestDiffPatch_MixedBinaryAndTextChangeset_BothSurviveRoundTrip(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("readme.md", "# Title\n\nOriginal body.\n")
	rb.git("add", "readme.md")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("readme.md", "# Title\n\nOriginal body.\n\nAdded a new section.\n")
	binData := make([]byte, 1024)
	for i := range binData {
		binData[i] = byte(i % 251) // 251 is prime, avoids a trivial repeating byte pattern
	}
	if err := os.WriteFile(filepath.Join(rb.dir, "image.png"), binData, 0o644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	rb.git("add", "readme.md", "image.png")

	client := gitx.NewClient(gitx.ExecRunner{})
	patch, err := client.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if !strings.Contains(string(patch), "readme.md") || !strings.Contains(string(patch), "image.png") {
		t.Fatalf("expected a single patch mentioning both changed files, got: %s", patch)
	}
	if !bytes.Contains(patch, []byte("GIT binary patch")) {
		t.Fatalf("expected the mixed patch to still carry a binary directive for image.png, got: %s", patch)
	}

	freshDir := applyPatchToFreshCheckout(t, rb, patch)

	wantText, err := os.ReadFile(filepath.Join(rb.dir, "readme.md"))
	if err != nil {
		t.Fatalf("read original readme.md: %v", err)
	}
	gotText, err := os.ReadFile(filepath.Join(freshDir, "readme.md"))
	if err != nil {
		t.Fatalf("read applied readme.md: %v", err)
	}
	if !bytes.Equal(wantText, gotText) {
		t.Fatalf("text part of mixed changeset mismatch: want %q got %q", wantText, gotText)
	}

	gotBin, err := os.ReadFile(filepath.Join(freshDir, "image.png"))
	if err != nil {
		t.Fatalf("read applied image.png: %v", err)
	}
	if !bytes.Equal(binData, gotBin) {
		t.Fatalf("binary part of mixed changeset mismatch: %d vs %d bytes", len(binData), len(gotBin))
	}
}

// TestDiffPatch_LargeDiff_ManyFilesAndLines_AppliesCleanly is the required
// "large diffs" edge case: a changeset spanning many files and many
// changed lines in one patch, still generated and applied correctly (no
// truncation, no ordering corruption).
func TestDiffPatch_LargeDiff_ManyFilesAndLines_AppliesCleanly(t *testing.T) {
	rb := newRepoBuilder(t)

	const fileCount = 50
	const linesPerFile = 200

	for i := 0; i < fileCount; i++ {
		name := "file-" + itoa(i) + ".txt"
		rb.write(name, repeatedLines("original", linesPerFile))
		rb.git("add", name)
	}
	rb.git("commit", "-q", "-m", "initial large tree")

	// Modify every single file substantially: this produces a patch with
	// fileCount hunks and fileCount*linesPerFile changed lines - large by
	// this test's own standard, not by an arbitrary external byte count.
	for i := 0; i < fileCount; i++ {
		name := "file-" + itoa(i) + ".txt"
		rb.write(name, repeatedLines("modified-"+itoa(i), linesPerFile))
	}
	rb.git("add", ".")

	client := gitx.NewClient(gitx.ExecRunner{})
	patch, err := client.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}

	gotFileHeaders := strings.Count(string(patch), "diff --git")
	if gotFileHeaders != fileCount {
		t.Fatalf("expected %d file headers in the large patch, got %d", fileCount, gotFileHeaders)
	}

	freshDir := applyPatchToFreshCheckout(t, rb, patch)

	// Spot-check every file's content round-trips exactly, not just the
	// first/last (an ordering or truncation bug would likely show up in
	// the middle of a 50-file patch, not only at the edges).
	for i := 0; i < fileCount; i++ {
		name := "file-" + itoa(i) + ".txt"
		want, err := os.ReadFile(filepath.Join(rb.dir, name))
		if err != nil {
			t.Fatalf("read original %s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(freshDir, name))
		if err != nil {
			t.Fatalf("read applied %s: %v", name, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("large-diff round-trip mismatch for %s", name)
		}
	}
}

// TestDiffPatch_LargeSingleFileDiff_ManyChangedLines_AppliesCleanly covers
// the other "large diff" shape: one file with a very large number of
// changed lines (rather than many files), since Capture's own per-file
// caps (security.go) apply to the UNTRACKED archive, not to DiffPatch's
// tracked-file diff scope — a patch has no such cap and must handle a
// large single-file diff correctly.
func TestDiffPatch_LargeSingleFileDiff_ManyChangedLines_AppliesCleanly(t *testing.T) {
	rb := newRepoBuilder(t)
	const lineCount = 5000

	rb.write("big.txt", repeatedLines("original", lineCount))
	rb.git("add", "big.txt")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("big.txt", repeatedLines("modified", lineCount))
	rb.git("add", "big.txt")

	client := gitx.NewClient(gitx.ExecRunner{})
	patch, err := client.DiffPatch(context.Background(), rb.dir, true)
	if err != nil {
		t.Fatalf("DiffPatch: %v", err)
	}
	if len(patch) == 0 {
		t.Fatalf("expected a non-empty patch for a %d-line change", lineCount)
	}

	freshDir := applyPatchToFreshCheckout(t, rb, patch)

	want, err := os.ReadFile(filepath.Join(rb.dir, "big.txt"))
	if err != nil {
		t.Fatalf("read original big.txt: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(freshDir, "big.txt"))
	if err != nil {
		t.Fatalf("read applied big.txt: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("large single-file diff round-trip mismatch: %d vs %d bytes", len(want), len(got))
	}
}

// TestPatch_CaptureApplyRoundTrip_ViaGzippedManifestArtifacts proves the
// FULL Capture path (gzip'd staged.patch.gz, exactly what a real
// checkpoint persists to disk) round-trips too, not just the raw
// gitx.DiffPatch output the tests above exercise directly - closing the
// gap between "the patch bytes are correct" and "what Capture actually
// writes to the checkpoint artifact directory is correct." Named with the
// Patch prefix (rather than Capture) so this node's own DAG validation
// command, `go test ./internal/repocheckpoint/... -run Patch`, selects it
// along with every other test in this file.
func TestPatch_CaptureApplyRoundTrip_ViaGzippedManifestArtifacts(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("notes.md", "first line\n")
	rb.git("add", "notes.md")
	rb.git("commit", "-q", "-m", "initial")

	rb.write("notes.md", "first line\nsecond line\n")
	rb.git("add", "notes.md")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	if _, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-roundtrip"), repocheckpoint.CaptureOptions{}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	gzPath := filepath.Join(artifactsRoot, "cp-roundtrip", "staged.patch.gz")
	gzBytes, err := os.ReadFile(gzPath)
	if err != nil {
		t.Fatalf("read %s: %v", gzPath, err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(gr); err != nil {
		t.Fatalf("decompress staged.patch.gz: %v", err)
	}

	freshDir := applyPatchToFreshCheckout(t, rb, buf.Bytes())

	want, err := os.ReadFile(filepath.Join(rb.dir, "notes.md"))
	if err != nil {
		t.Fatalf("read original notes.md: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(freshDir, "notes.md"))
	if err != nil {
		t.Fatalf("read applied notes.md: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("manifest-artifact round-trip mismatch: want %q got %q", want, got)
	}
}

// repeatedLines builds n lines of "<prefix>-<i>\n", giving each line
// distinguishable content (rather than n identical lines) so a patch's
// hunks are meaningful and a truncation/reordering bug would produce a
// detectable content mismatch rather than an accidental pass.
func repeatedLines(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(prefix)
		b.WriteString("-")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	return b.String()
}
