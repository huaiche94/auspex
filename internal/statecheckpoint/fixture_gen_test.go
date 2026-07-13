package statecheckpoint_test

import (
	"os"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
)

// TestGenerateSampleManifestFixture is not a real assertion test; it is a
// one-shot generator (skipped unless AUSPEX_GENERATE_FIXTURES=1) used to
// produce testdata/checkpoints/state/sample-manifest.json from an ACTUAL
// Build+Seal+Marshal call, the same "generated from a real run, not
// hand-typed" discipline checkpoint-b04 established for its own
// sample-manifest.json fixture. Run once via:
//
//	AUSPEX_GENERATE_FIXTURES=1 go test ./internal/statecheckpoint/... -run TestGenerateSampleManifestFixture
//
// then commit the resulting file. Left in the test suite (rather than a
// throwaway script) so the fixture can be regenerated deterministically if
// the Manifest shape ever changes.
func TestGenerateSampleManifestFixture(t *testing.T) {
	if os.Getenv("AUSPEX_GENERATE_FIXTURES") != "1" {
		t.Skip("set AUSPEX_GENERATE_FIXTURES=1 to (re)generate testdata/checkpoints/state/sample-manifest.json")
	}

	usedPct := 61.4
	resetsAt := time.Date(2026, 7, 12, 22, 14, 3, 0, time.UTC)
	activeNode := domain.ProgressNodeID("section-20")
	nextNode := domain.ProgressNodeID("section-20")

	m := statecheckpoint.Build(statecheckpoint.BuildInput{
		CheckpointID: "0198f5d8-9dd1-7f80-90f3-2f7a12345678",
		TaskID:       "0198f5d8-2e36-7b75-8af3-8f09fd0f7081",
		CreatedAt:    time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC),
		ProgressTree: statecheckpoint.ProgressTreeSummary{
			Version:          17,
			ActiveNodeID:     &activeNode,
			CompletedNodeIDs: []domain.ProgressNodeID{"section-01", "section-02", "section-03"},
			PausedNodeIDs:    []domain.ProgressNodeID{},
		},
		Artifacts: []statecheckpoint.ArtifactSummary{
			{
				ID:               "artifact-add",
				URI:              "file:Auspex_ADD.md",
				MediaType:        "text/markdown",
				Bytes:            128442,
				SHA256:           "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
				ValidationStatus: "passed",
			},
		},
		Repository: statecheckpoint.RepositoryInfo{
			RepositoryID:     "0198f5d8-0000-7000-8000-000000000001",
			WorktreeID:       "0198f5d8-0000-7000-8000-000000000002",
			GitHead:          "f1a83bc123",
			Branch:           "docs/auspex-add",
			IndexDiffHash:    "sha256:index-diff-hash",
			WorktreeDiffHash: "sha256:worktree-diff-hash",
		},
		Provider: statecheckpoint.ProviderInfo{
			Name:           "codex",
			SessionID:      "thr_123",
			TurnID:         "turn_456",
			InvocationMode: "managed_app_server",
		},
		Quota: []statecheckpoint.QuotaObservationRef{
			{LimitID: "codex", UsedPercent: 87.2, WindowSecs: 18000, ResetsAt: &resetsAt},
		},
		ContextUsedPct: &usedPct,
		NextAction: statecheckpoint.NextActionInfo{
			NodeID:      &nextNode,
			Description: "Continue after subsection 20.8 and run document validation.",
		},
		Resume: statecheckpoint.ResumeInfo{
			StrategyOrder:  []string{"same_provider_session", "new_session_progress_bootstrap"},
			PermissionMode: "default",
		},
	})

	sealed, err := statecheckpoint.Seal(m)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := statecheckpoint.Marshal(sealed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile("../../testdata/checkpoints/state/sample-manifest.json", append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
