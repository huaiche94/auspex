package statecheckpoint_test

import (
	"os"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
)

func sampleManifest() statecheckpoint.Manifest {
	return statecheckpoint.Build(statecheckpoint.BuildInput{
		CheckpointID: "checkpoint-1",
		TaskID:       "task-1",
		CreatedAt:    time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC),
		ProgressTree: statecheckpoint.ProgressTreeSummary{
			Version:          17,
			CompletedNodeIDs: []domain.ProgressNodeID{"section-01", "section-02"},
		},
		Artifacts: []statecheckpoint.ArtifactSummary{
			{ID: "artifact-add", URI: "file:Auspex_ADD.md", Bytes: 128442, SHA256: "abc123", ValidationStatus: "passed"},
		},
		Repository: statecheckpoint.RepositoryInfo{GitHead: "f1a83bc"},
		Provider:   statecheckpoint.ProviderInfo{Name: "codex", SessionID: "thr_123"},
	})
}

func TestDigest_Deterministic(t *testing.T) {
	m := sampleManifest()
	d1, err := statecheckpoint.Digest(m)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	d2, err := statecheckpoint.Digest(m)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if d1 != d2 {
		t.Fatalf("expected deterministic digest, got %s vs %s", d1, d2)
	}
	if len(d1) != 64 {
		t.Fatalf("expected a 64-char hex SHA-256 digest, got %d chars: %s", len(d1), d1)
	}
}

func TestDigest_ExcludesIntegrityField(t *testing.T) {
	m := sampleManifest()
	d, err := statecheckpoint.Digest(m)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	m.IntegritySHA256 = "some-prior-value-that-should-be-ignored"
	d2, err := statecheckpoint.Digest(m)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if d != d2 {
		t.Fatalf("expected digest to be independent of the existing IntegritySHA256 field value")
	}
}

func TestDigest_SensitiveToContentChange(t *testing.T) {
	m1 := sampleManifest()
	m2 := sampleManifest()
	m2.ProgressTree.Version = 18

	d1, err := statecheckpoint.Digest(m1)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	d2, err := statecheckpoint.Digest(m2)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if d1 == d2 {
		t.Fatalf("expected different digests for different progress_tree.version content")
	}
}

func TestSeal_PopulatesIntegritySHA256(t *testing.T) {
	m := sampleManifest()
	if m.IntegritySHA256 != "" {
		t.Fatalf("precondition: unsealed manifest must start with empty IntegritySHA256")
	}
	sealed, err := statecheckpoint.Seal(m)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed.IntegritySHA256 == "" {
		t.Fatalf("expected Seal to populate IntegritySHA256")
	}
	// Seal must not mutate its argument.
	if m.IntegritySHA256 != "" {
		t.Fatalf("Seal must not mutate its input manifest")
	}
}

func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	sealed, err := statecheckpoint.Seal(sampleManifest())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := statecheckpoint.Marshal(sealed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, err := statecheckpoint.Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.IntegritySHA256 != sealed.IntegritySHA256 {
		t.Fatalf("round-trip lost IntegritySHA256: got %s want %s", parsed.IntegritySHA256, sealed.IntegritySHA256)
	}
	if parsed.CheckpointID != sealed.CheckpointID || parsed.TaskID != sealed.TaskID {
		t.Fatalf("round-trip lost identity fields: %+v", parsed)
	}
	if len(parsed.Artifacts) != len(sealed.Artifacts) {
		t.Fatalf("round-trip lost artifacts: got %d want %d", len(parsed.Artifacts), len(sealed.Artifacts))
	}
}

func TestVerify_SealedManifestPasses(t *testing.T) {
	sealed, err := statecheckpoint.Seal(sampleManifest())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ok, err := statecheckpoint.Verify(sealed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatalf("expected a freshly sealed manifest to verify")
	}
}

func TestVerify_TamperedManifestFails(t *testing.T) {
	sealed, err := statecheckpoint.Seal(sampleManifest())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	tampered := sealed
	tampered.ProgressTree.Version = 999 // mutate content without resealing

	ok, err := statecheckpoint.Verify(tampered)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatalf("expected a tampered manifest (content changed, checksum not recomputed) to fail verification")
	}
}

func TestSampleManifestFixture_VerifiesAgainstSchema(t *testing.T) {
	b, err := os.ReadFile("../../testdata/checkpoints/state/sample-manifest.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	m, err := statecheckpoint.Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal fixture: %v", err)
	}
	if m.SchemaVersion != statecheckpoint.SchemaVersion {
		t.Fatalf("fixture schema_version mismatch: got %s want %s", m.SchemaVersion, statecheckpoint.SchemaVersion)
	}
	ok, err := statecheckpoint.Verify(m)
	if err != nil {
		t.Fatalf("Verify fixture: %v", err)
	}
	if !ok {
		t.Fatalf("fixture's stored integrity_sha256 does not match its recomputed digest")
	}
	if len(m.IntegritySHA256) != 64 {
		t.Fatalf("expected a 64-hex-char digest, got %d chars", len(m.IntegritySHA256))
	}
}
