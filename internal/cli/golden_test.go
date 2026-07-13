// golden_test.go closes agents/runtime.md Part B's "Tests" list item "CLI
// golden tests" — a genuine, real gap confirmed by direct audit before
// writing this file (runtime-b10's own research pass): every existing
// success-path test across internal/cli/*_test.go and
// internal/app/wiring/*_test.go decodes stdout into a map[string]any (or an
// anonymous struct naming only one or two fields of interest) and checks
// individual keys — e.g. errorcontract_test.go's
// TestErrorContract_RealCheckpointCreate_SuccessPathIsSchemaVersionedJSON
// asserts only SchemaVersion, ignoring every sibling field entirely. That
// means an accidental added, removed, renamed, or reordered field in a real
// command's JSON success output would pass every existing test in this
// package silently. This file is the byte-for-byte (structurally, via
// json.Unmarshal + reflect.DeepEqual — not literal byte comparison, so
// insignificant whitespace/key-ordering differences from encoding/json's
// own map serialization never cause spurious failures) counterpart to
// claude-provider's own established golden-fixture convention
// (internal/hooks/claude/testdata/*.golden.json,
// userpromptsubmit_test.go's assertJSONEqual) — same technique, applied
// here to this role's own P0 command surface instead.
//
// Fixtures live under internal/cli/testdata/golden/ (this role's own
// exclusive path, internal/cli/**) — one *.golden.json file per covered
// command's full success-path output shape. Three representative REAL
// (non-stub) commands were chosen to cover this package's three distinct
// output shapes: checkpoint create (nested two-service result),
// decision allow issue flow (conditional-field result — Action is
// omit-empty), and doctor (a slice of sub-results, the shape most likely
// to silently gain/lose an element unnoticed by a map-key-only check).
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/cli"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/orchestrator"
	"github.com/huaiche94/preflight/internal/testutil/fakes"
)

// goldenPath returns the checked-in fixture path for name.
func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name)
}

// assertGolden compares got (raw JSON bytes from a command's real stdout)
// against the fixture at goldenPath(fixtureName), structurally (unmarshal
// both sides, reflect.DeepEqual) — so a field added, removed, renamed, or
// value-changed fails loudly, while JSON's own insignificant formatting
// differences never do. Set PREFLIGHT_UPDATE_GOLDEN=1 to rewrite the
// fixture from got instead of comparing (this role's own established
// convention for updating a deliberate output-shape change — mirrors the
// same escape hatch every golden-file testing setup needs so a real,
// intentional change isn't a hand-edited JSON diff).
func assertGolden(t *testing.T, fixtureName string, got []byte) {
	t.Helper()
	path := goldenPath(fixtureName)

	if os.Getenv("PREFLIGHT_UPDATE_GOLDEN") == "1" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, bytes.TrimSpace(got), "", "  "); err != nil {
			t.Fatalf("indenting golden output for %s: %v", fixtureName, err)
		}
		pretty.WriteByte('\n')
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatalf("writing golden fixture %s: %v", path, err)
		}
		t.Logf("updated golden fixture %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden fixture %s: %v (run with PREFLIGHT_UPDATE_GOLDEN=1 to create it)", path, err)
	}

	var gv, wv any
	if err := json.Unmarshal(bytes.TrimSpace(got), &gv); err != nil {
		t.Fatalf("command output is not valid JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatalf("golden fixture %s is not valid JSON: %v\n%s", path, err, want)
	}
	if !reflect.DeepEqual(gv, wv) {
		t.Fatalf("golden mismatch against %s:\n got:  %s\nwant:  %s", path, bytes.TrimSpace(got), bytes.TrimSpace(want))
	}
}

// TestGolden_CheckpointCreate_SuccessOutput proves `checkpoint create`'s
// full success JSON shape (all three fields, not just one) matches its
// checked-in fixture exactly.
func TestGolden_CheckpointCreate_SuccessOutput(t *testing.T) {
	deps := orchestrator.CheckpointCreateDeps{
		StateCheckpoint: &fakes.FakeStateCheckpointService{
			CreateFunc: func(_ context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
				return domain.StateCheckpoint{ID: "sc-golden-1", TaskID: req.TaskID}, nil
			},
		},
		RepositoryCheckpoint: &fakes.FakeRepositoryCheckpointService{
			CreateFunc: func(_ context.Context, _ app.CreateRepositoryCheckpointRequest) (app.RepositoryCheckpoint, error) {
				return app.RepositoryCheckpoint{ID: "rc-golden-1", GitHead: "cafef00dcafef00dcafef00dcafef00dcafef00d"}, nil
			},
		},
	}
	cmd := cli.NewCheckpointCmd(deps)
	cmd.SetArgs([]string{"create", "--task-id", "task-golden", "--worktree-id", "wt-golden"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("checkpoint create: %v", err)
	}
	assertGolden(t, "checkpoint_create_success.golden.json", out.Bytes())
}

// TestGolden_DecisionAllow_IssueFlow_SuccessOutput proves `decision allow`'s
// issue-flow full success JSON shape (Issued/Consumed/AuthorizationID/
// Action all present, per their actual conditional semantics — Action is
// only populated on the issue flow) matches its checked-in fixture exactly.
func TestGolden_DecisionAllow_IssueFlow_SuccessOutput(t *testing.T) {
	deps := orchestrator.DecisionDeps{
		Evaluation: &fakes.FakeEvaluationService{
			DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
				return app.DecisionResult{ID: "dec-golden-1", Action: app.PolicyRequireConfirmation}, nil
			},
		},
		Issuer: &fakeGoldenAuthorizationIssuer{
			issueFunc: func(_ context.Context, turnID domain.TurnID, _ string, _ string, decision string, _ *domain.RepositoryCheckpointID) (app.Authorization, error) {
				return app.Authorization{ID: "auth-golden-1", TurnID: turnID, Decision: decision}, nil
			},
		},
	}
	cmd := cli.NewDecisionCmd(deps)
	cmd.SetArgs([]string{"allow", "--evaluation-id", "eval-golden-1", "--turn-id", "turn-golden-1", "--prompt-hash", "hash-golden-1"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("decision allow: %v", err)
	}
	assertGolden(t, "decision_allow_issue_success.golden.json", out.Bytes())
}

// TestGolden_Doctor_AllSkipped_SuccessOutput proves `doctor`'s
// all-checks-skipped success JSON shape (a slice of per-check objects, the
// shape most likely for a map-key-only test to silently miss a
// gained/lost/reordered element) matches its checked-in fixture exactly.
func TestGolden_Doctor_AllSkipped_SuccessOutput(t *testing.T) {
	cmd := cli.NewDoctorCmd(orchestrator.DoctorDeps{})
	cmd.SetArgs(nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	assertGolden(t, "doctor_all_skipped_success.golden.json", out.Bytes())
}

// fakeGoldenAuthorizationIssuer is this file's own minimal local double for
// orchestrator.AuthorizationIssuer, mirroring wiring_test.go's own
// fakeAuthorizationIssuer / decision_test.go's own copy — kept package-local
// per this codebase's established narrow-interface-double convention
// (documented in those files) rather than shared across packages.
type fakeGoldenAuthorizationIssuer struct {
	issueFunc func(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error)
}

func (f *fakeGoldenAuthorizationIssuer) IssueAuthorization(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error) {
	return f.issueFunc(ctx, turnID, promptHash, snapshotFingerprint, decision, repoCheckpointID)
}
