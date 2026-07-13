package artifacts_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/artifacts"
)

// fixturePath resolves a path under testdata/checkpoints/state, the real
// ADD-section fixtures required by this node's DAG entry
// ("Needs real ADD-section fixtures").
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "checkpoints", "state", name)
}

func TestHeadingExists_RealADDSection_Passes(t *testing.T) {
	v := artifacts.HeadingExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:    fixturePath(t, "add-section-18-valid.md"),
		Heading: "# 18. Progress Tree 與 State Checkpointing",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got failure reasons: %v", res.Reasons)
	}
}

func TestHeadingExists_SubheadingAlsoMatches(t *testing.T) {
	v := artifacts.HeadingExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:    fixturePath(t, "add-section-18-valid.md"),
		Heading: "## 18.5 Document section contract",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got failure reasons: %v", res.Reasons)
	}
}

// TestHeadingExists_MissingHeading_Rejected is the DAG's required "missing
// heading ... rejected" test, run against the real fixture with the H1
// deleted (add-section-18-missing-heading.md).
func TestHeadingExists_MissingHeading_Rejected(t *testing.T) {
	v := artifacts.HeadingExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:    fixturePath(t, "add-section-18-missing-heading.md"),
		Heading: "# 18. Progress Tree 與 State Checkpointing",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure: heading was removed from fixture")
	}
	if len(res.Reasons) == 0 {
		t.Fatal("expected at least one reason for the failure")
	}
}

func TestHeadingExists_HeadingTextInsideFence_NotCountedAsHeading(t *testing.T) {
	// The valid fixture's §18.5 code fence contains a YAML line
	// "title: Graceful Pause" and its §18.11 fence contains prose that
	// mentions "Progress Tree node" — neither is a real ATX heading line,
	// so searching for a heading that only appears inside a fence must
	// fail, proving fence-awareness rather than a naive substring search.
	v := artifacts.HeadingExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:    fixturePath(t, "add-section-18-valid.md"),
		Heading: "Task: Build Auspex ADD",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure: candidate text only appears inside a code fence, not as a heading")
	}
}

func TestHeadingExists_FileDoesNotExist(t *testing.T) {
	v := artifacts.HeadingExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:    fixturePath(t, "does-not-exist.md"),
		Heading: "# anything",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for missing file")
	}
}

func TestHeadingExists_EmptyHeading_Rejected(t *testing.T) {
	v := artifacts.HeadingExistsValidator{}
	res, err := v.Validate(context.Background(), artifacts.Candidate{
		Path:    fixturePath(t, "add-section-18-valid.md"),
		Heading: "",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure for empty heading candidate")
	}
}

func TestHeadingExists_Kind(t *testing.T) {
	if got := (artifacts.HeadingExistsValidator{}).Kind(); got != "heading_exists" {
		t.Fatalf("expected Kind() = heading_exists, got %s", got)
	}
}
