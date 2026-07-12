package artifacts_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/preflight/internal/artifacts"
)

func TestFenceBalance_RealADDSection_Passes(t *testing.T) {
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path: fixturePath(t, "add-section-18-valid.md"),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got failure reasons: %v", res.Reasons)
	}
}

// TestFenceBalance_UnbalancedFence_Rejected is the DAG's required
// "unbalanced fence rejected" test, run against the real fixture with one
// closing ``` fence deleted (add-section-18-unbalanced-fence.md).
func TestFenceBalance_UnbalancedFence_Rejected(t *testing.T) {
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path: fixturePath(t, "add-section-18-unbalanced-fence.md"),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure: fixture has an unclosed code fence")
	}
	if len(res.Reasons) == 0 {
		t.Fatal("expected at least one reason for the failure")
	}
}

func TestFenceBalance_FileDoesNotExist(t *testing.T) {
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path: fixturePath(t, "does-not-exist.md"),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for missing file")
	}
}

func TestFenceBalance_EmptyFile_Passes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass for empty file, got: %v", res.Reasons)
	}
}

func TestFenceBalance_NestedDifferentMarker_Balanced(t *testing.T) {
	// A ~~~ fence containing a ``` example inside it is valid CommonMark:
	// the inner backticks are ordinary content, and only the matching ~~~
	// closes the block.
	dir := t.TempDir()
	path := filepath.Join(dir, "nested.md")
	content := "# Heading\n\n~~~text\nExample:\n```\nnot a real fence here\n```\n~~~\n\nDone.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass for nested-marker fence, got: %v", res.Reasons)
	}
}

func TestFenceBalance_MismatchedMarkerNeverCloses_Rejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mismatched.md")
	content := "# Heading\n\n```text\nopened with backticks\n~~~\nclosed with tildes, does not match\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure: fence opened with backticks never closed by a backtick fence")
	}
}

func TestFenceBalance_ShorterClosingRun_DoesNotClose(t *testing.T) {
	// CommonMark: a closing fence needs a run length >= the opening run.
	// A 4-backtick open followed by a 3-backtick "close" does not close it.
	dir := t.TempDir()
	path := filepath.Join(dir, "short-close.md")
	content := "# Heading\n\n````text\ncode with a ``` triple inside\n````\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass: inner ``` is shorter than opening ```` so it doesn't close, but the final ```` does; got: %v", res.Reasons)
	}
}

func TestFenceBalance_IndentedCodeBlock_NotTreatedAsFence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "indented.md")
	// 4-space indented line starting with backticks is an indented code
	// block in CommonMark, not a fence delimiter.
	content := "# Heading\n\n    ```\n    this looks like a fence but is indented 4 spaces\n\nDone.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	v := artifacts.FenceBalanceValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass: indented lines are not fence delimiters, got: %v", res.Reasons)
	}
}

func TestFenceBalance_Kind(t *testing.T) {
	if got := (artifacts.FenceBalanceValidator{}).Kind(); got != "fence_balance" {
		t.Fatalf("expected Kind() = fence_balance, got %s", got)
	}
}
