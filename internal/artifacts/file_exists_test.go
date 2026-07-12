package artifacts_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/preflight/internal/artifacts"
)

func TestFileExists_RealFile_Passes(t *testing.T) {
	v := artifacts.FileExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path: fixturePath(t, "add-section-18-valid.md"),
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got: %v", res.Reasons)
	}
}

func TestFileExists_MissingFile_Rejected(t *testing.T) {
	v := artifacts.FileExistsValidator{}
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

func TestFileExists_Directory_Rejected(t *testing.T) {
	dir := t.TempDir()
	v := artifacts.FileExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: dir})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure: path is a directory, not a file")
	}
}

func TestFileExists_EmptyPath_Rejected(t *testing.T) {
	v := artifacts.FileExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: ""})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for empty path")
	}
}

func TestFileExists_EmptyButPresentFile_Passes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	v := artifacts.FileExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{Path: path})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass for empty-but-present file, got: %v", res.Reasons)
	}
}

func TestFileExists_Kind(t *testing.T) {
	if got := (artifacts.FileExistsValidator{}).Kind(); got != "file_exists" {
		t.Fatalf("expected Kind() = file_exists, got %s", got)
	}
}
