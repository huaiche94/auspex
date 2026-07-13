package repocheckpoint

import (
	"encoding/json"
	"fmt"
	"strings"
)

// marshalManifest renders a Manifest as indented JSON (manifest.json, ADD
// §19.2), matching the human-inspectable style already established for
// other Auspex manifests referenced in the ADD's appendices.
func marshalManifest(m Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("repocheckpoint: marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// unmarshalManifest parses manifest.json bytes back into a Manifest, the
// inverse of marshalManifest, used by Verify to load the durable record it
// checks the live filesystem/DB row against.
func unmarshalManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("repocheckpoint: unmarshal manifest: %w", err)
	}
	return m, nil
}

// marshalSkipped renders the skip ledger as indented JSON
// (skipped-files.json, ADD §19.2).
func marshalSkipped(skipped []SkippedFile) ([]byte, error) {
	b, err := json.MarshalIndent(skipped, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("repocheckpoint: marshal skipped-files: %w", err)
	}
	return append(b, '\n'), nil
}

// renderSummary produces summary.md (ADD §19.2): a short, human-readable
// description of what this checkpoint captured, for a person inspecting
// the artifact directory directly without parsing JSON.
func renderSummary(m Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Repository Checkpoint %s\n\n", m.CheckpointID)
	fmt.Fprintf(&b, "- Created: %s\n", m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&b, "- Status: %s\n", m.Status)
	fmt.Fprintf(&b, "- Repository: %s (worktree %s)\n", m.Repository.RepositoryID, m.Repository.WorktreeID)
	fmt.Fprintf(&b, "- HEAD: %s (%s)\n", m.Repository.GitHead, m.Repository.Branch)
	fmt.Fprintf(&b, "- Staged files: %d, Unstaged files: %d, Untracked files: %d\n",
		m.Snapshot.StagedFiles, m.Snapshot.UnstagedFiles, m.Snapshot.UntrackedFiles)
	fmt.Fprintf(&b, "- Lines: +%d/-%d\n", m.Snapshot.LinesAdded, m.Snapshot.LinesDeleted)
	fmt.Fprintf(&b, "- Recoverability: %s (%d file(s) skipped)\n", m.Recoverability.Level, m.Recoverability.SkippedFileCount)
	if len(m.Recoverability.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, w := range m.Recoverability.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}
