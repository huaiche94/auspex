package repocheckpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
)

func captureForVerify(t *testing.T) (repocheckpoint.Row, string) {
	t.Helper()
	rb := newRepoBuilder(t)
	rb.write("a.txt", "content\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")
	rb.write("untracked.txt", "untracked content\n")

	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()

	result, err := repocheckpoint.Capture(context.Background(), client, testClock(),
		captureReq(rb, artifactsRoot, "cp-verify"), repocheckpoint.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	return result.Row, artifactsRoot
}

func TestVerify_FreshCapture_Valid(t *testing.T) {
	row, _ := captureForVerify(t)

	result, err := repocheckpoint.Verify(row)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid, got problems: %v", result.Problems)
	}
}

func TestVerify_TamperedArtifact_Invalid(t *testing.T) {
	row, _ := captureForVerify(t)

	patchPath := filepath.Join(row.ArtifactRoot, "staged.patch.gz")
	if err := os.WriteFile(patchPath, []byte("tampered content, wrong digest"), 0o644); err != nil {
		t.Fatalf("tamper with artifact: %v", err)
	}

	result, err := repocheckpoint.Verify(row)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid after tampering with an artifact file")
	}
	if len(result.Problems) == 0 {
		t.Fatal("expected at least one problem reported")
	}
}

func TestVerify_MissingArtifact_Invalid(t *testing.T) {
	row, _ := captureForVerify(t)

	patchPath := filepath.Join(row.ArtifactRoot, "unstaged.patch.gz")
	if err := os.Remove(patchPath); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}

	result, err := repocheckpoint.Verify(row)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid after removing an artifact file")
	}
}

func TestVerify_MissingManifest_Invalid(t *testing.T) {
	row, _ := captureForVerify(t)

	if err := os.Remove(row.ManifestPath); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	result, err := repocheckpoint.Verify(row)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid when manifest.json is missing")
	}
}

func TestVerify_ManifestCheckpointIDMismatch_Invalid(t *testing.T) {
	row, _ := captureForVerify(t)

	// Corrupt just the row's ID (simulating a corrupted DB row rather than
	// a tampered file) so manifest.checkpoint_id no longer matches.
	row.ID = "different-id"

	result, err := repocheckpoint.Verify(row)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid when row ID does not match manifest checkpoint_id")
	}
}
