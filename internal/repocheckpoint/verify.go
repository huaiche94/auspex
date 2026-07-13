// verify.go: the Repository Checkpoint verify operation (agents/checkpoint.md
// Part B deliverable #4, "... and verify"; ADD §19.3 step 15; Part B
// security requirement "verify checksums before restore planning").
//
// Verify never trusts the repository_checkpoints DB row's own fields at
// face value — it reads the durable manifest.json and every artifact file
// from disk and recomputes their digests, so a row that was corrupted (or
// an artifact file that was tampered with or truncated after capture) is
// caught here rather than silently accepted by a later restore.
package repocheckpoint

import (
	"fmt"
	"os"
)

// VerifyResult is the outcome of verifying one Repository Checkpoint.
type VerifyResult struct {
	Valid    bool
	Manifest Manifest
	// Problems lists every integrity discrepancy found, empty when
	// Valid is true. Verify collects every problem it can find in one
	// pass rather than stopping at the first, so a caller surfacing this
	// to a user (or qa's leakage/integrity scanner) gets the complete
	// picture.
	Problems []string
}

// Verify loads row's manifest.json, confirms every artifact file listed in
// it exists on disk with a matching size and SHA-256 digest, and confirms
// the manifest's own checkpoint_id matches row.ID (catching a
// manifest-path/DB-row mismatch, e.g. from a corrupted artifact_root
// column). It does not touch Git or the working tree at all — verification
// is purely a check of the checkpoint's own captured evidence against
// itself, matching this package's create/verify scope (restore, which DOES
// need to reason about the live working tree, is out of this node's scope
// per the DAG: b08 owns restore dry-run).
func Verify(row Row) (VerifyResult, error) {
	manifestBytes, err := os.ReadFile(row.ManifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return VerifyResult{Valid: false, Problems: []string{
				fmt.Sprintf("manifest file missing: %s", row.ManifestPath),
			}}, nil
		}
		return VerifyResult{}, fmt.Errorf("repocheckpoint: verify: read manifest %s: %w", row.ManifestPath, err)
	}

	manifest, err := unmarshalManifest(manifestBytes)
	if err != nil {
		return VerifyResult{Valid: false, Problems: []string{
			fmt.Sprintf("manifest is not valid JSON: %v", err),
		}}, nil
	}

	var problems []string

	if manifest.SchemaVersion != ManifestSchemaVersion {
		problems = append(problems, fmt.Sprintf("manifest schema_version %q does not match expected %q", manifest.SchemaVersion, ManifestSchemaVersion))
	}
	if manifest.CheckpointID != string(row.ID) {
		problems = append(problems, fmt.Sprintf("manifest checkpoint_id %q does not match row ID %q", manifest.CheckpointID, row.ID))
	}
	if manifest.Repository.GitHead != row.GitHead {
		problems = append(problems, fmt.Sprintf("manifest git_head %q does not match row git_head %q", manifest.Repository.GitHead, row.GitHead))
	}

	for _, artifact := range manifest.Artifacts {
		path, safe := safeArtifactPath(row.ArtifactRoot, artifact.Path)
		if !safe {
			// checkpoint-b09 security gate: a manifest — read fresh from
			// disk on every Verify call, never assumed trustworthy just
			// because it lives under a checkpoint directory this package
			// itself once wrote — listing an artifact path that escapes
			// ArtifactRoot (a literal "../" traversal, an absolute path, or
			// a symlink hop) must never be joined onto ArtifactRoot and
			// read: doing so would let a tampered or maliciously
			// constructed manifest.json make Verify (and therefore
			// RestoreDryRun's checksum step, which calls Verify first) read
			// and hash arbitrary files on disk outside the checkpoint's own
			// evidence directory. Reported as a normal integrity problem
			// (fail-closed Valid:false), not a panic or a silently-skipped
			// artifact — mirrors validateUntrackedPath's identical
			// traversal/symlink posture in security.go, applied here to
			// manifest-declared paths instead of git-reported ones.
			problems = append(problems, fmt.Sprintf("artifact %s: path escapes checkpoint artifact root %s (rejected, not read)", artifact.Path, row.ArtifactRoot))
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			problems = append(problems, fmt.Sprintf("artifact %s missing on disk: %v", artifact.Path, statErr))
			continue
		}
		if info.Size() != artifact.Bytes {
			problems = append(problems, fmt.Sprintf("artifact %s size mismatch: manifest says %d bytes, disk has %d", artifact.Path, artifact.Bytes, info.Size()))
			continue
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			problems = append(problems, fmt.Sprintf("artifact %s unreadable: %v", artifact.Path, readErr))
			continue
		}
		actual := sha256Hex(content)
		if actual != artifact.SHA256 {
			problems = append(problems, fmt.Sprintf("artifact %s sha256 mismatch: manifest says %s, disk has %s", artifact.Path, artifact.SHA256, actual))
		}
	}

	return VerifyResult{
		Valid:    len(problems) == 0,
		Manifest: manifest,
		Problems: problems,
	}, nil
}
