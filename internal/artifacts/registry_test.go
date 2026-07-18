package artifacts_test

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/artifacts"
)

// alwaysPassValidator is a minimal custom Validator implementation, used to
// prove the "optional custom validator interface" deliverable: any type
// satisfying artifacts.Validator can be registered and dispatched to
// without this package's built-ins knowing about it in advance.
type alwaysPassValidator struct{ kind string }

func (a alwaysPassValidator) Kind() string { return a.kind }
func (a alwaysPassValidator) Validate(context.Context, artifacts.Candidate) (artifacts.Result, error) {
	return artifacts.Passed, nil
}

func TestRegistry_BuiltinsPreregistered(t *testing.T) {
	r := artifacts.NewRegistry()
	for _, kind := range []string{"file_exists", "checksum_matches", "heading_exists", "fence_balance"} {
		if _, ok := r.Lookup(kind); !ok {
			t.Errorf("expected built-in validator %q to be pre-registered", kind)
		}
	}
}

func TestRegistry_RegisterCustomValidator_DispatchWorks(t *testing.T) {
	r := artifacts.NewRegistry()
	custom := alwaysPassValidator{kind: "no_placeholder_text"}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := r.Validate(context.Background(), "no_placeholder_text", artifacts.Candidate{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected custom validator to pass, got: %v", res.Reasons)
	}
}

func TestRegistry_RegisterDuplicateKind_Rejected(t *testing.T) {
	r := artifacts.NewRegistry()
	err := r.Register(alwaysPassValidator{kind: "file_exists"})
	if err == nil {
		t.Fatal("expected registering a duplicate kind (file_exists, a built-in) to fail")
	}
}

func TestRegistry_UnknownKind_ReturnsFailedResultNotError(t *testing.T) {
	r := artifacts.NewRegistry()
	res, err := r.Validate(context.Background(), "totally_unknown_validator", artifacts.Candidate{})
	if err != nil {
		t.Fatalf("expected no Go error for an unknown kind, got: %v", err)
	}
	if res.Passed {
		t.Fatal("expected Result.Passed=false for an unknown validator kind")
	}
	if len(res.Reasons) == 0 {
		t.Fatal("expected a reason explaining the unknown kind")
	}
}

// TestRegistry_ValidMarkdownSection_CompletesValidation exercises the DAG's
// required "valid Markdown section completes and checkpoints" test, scoped
// per this phase's brief to "validator returns success" (the full completion
// protocol that would create a State Checkpoint is checkpoint-a04's job):
// running every relevant built-in validator against the real, valid ADD
// fixture all report success together, as CompleteNode would require before
// proceeding.
func TestRegistry_ValidMarkdownSection_CompletesValidation(t *testing.T) {
	r := artifacts.NewRegistry()
	ctx := context.Background()
	path := fixturePath(t, "add-section-18-valid.md")

	checks := []struct {
		kind      string
		candidate artifacts.Candidate
	}{
		{"file_exists", artifacts.Candidate{Path: path}},
		{"heading_exists", artifacts.Candidate{Path: path, Heading: "# 18. Progress Tree 與 State Checkpointing"}},
		{"fence_balance", artifacts.Candidate{Path: path}},
	}
	for _, c := range checks {
		res, err := r.Validate(ctx, c.kind, c.candidate)
		if err != nil {
			t.Fatalf("Validate(%s): %v", c.kind, err)
		}
		if !res.Passed {
			t.Fatalf("Validate(%s) expected pass, got: %v", c.kind, res.Reasons)
		}
	}
}
