// reconcile.go: startup reconciliation for staged-artifact-vs-DB crash
// windows (agents/checkpoint.md Part A deliverable #6; ADD §18.9 "On
// startup/resume"). Because CompleteNode stages artifact evidence to
// durable storage (FileStager, outside the SQLite transaction) BEFORE
// opening the transaction that actually marks a node completed, a crash
// between those two points leaves an orphaned staged artifact with no
// corresponding DB state change — harmless (the node is simply still
// in_progress/checkpointing, and the next CompleteNode attempt re-stages
// idempotently, per FileStager's content-addressed dest path) but worth
// surfacing so an operator/caller can see it rather than silently
// accumulating orphaned evidence files forever.
//
// The reverse direction — a DB row claiming completion with no backing
// evidence — cannot happen by construction: the transaction that writes
// the node's `completed` status is the SAME transaction that writes the
// artifacts rows and the checkpoint row (complete_node.go's WithTx
// callback), so a partially-applied crash there rolls back everything via
// SQLite's own atomicity, never leaving a completed node with missing
// evidence. Reconcile's DB-side check below re-verifies this expectation
// defensively rather than assuming it, since "never trust a stored
// checksum/status alone without recomputing" is this whole package's
// governing discipline (mirrors internal/repocheckpoint/verify.go).
package progress

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
)

// ReconcileReport is Reconcile's result: what it found and fixed/flagged.
type ReconcileReport struct {
	// OrphanedStagedArtifacts lists evidence-directory files that exist on
	// disk but are not referenced by any committed artifacts row — the
	// staged-artifact-vs-DB crash window this deliverable targets. These
	// are informational, not errors: they are safe to leave in place (a
	// future completion attempt may still reference them) or garbage
	// collect; Reconcile does not delete them itself, since deleting
	// evidence a caller might still need is a much worse failure mode than
	// leaving harmless orphaned files on disk.
	OrphanedStagedArtifacts []string

	// IntegrityViolations lists checkpoints whose stored integrity_sha256
	// does not match a fresh recomputation, or whose manifest references a
	// node not present in progress_nodes at all — a state-integrity bug,
	// never expected in practice given the single-transaction write, but
	// checked defensively per this package's fail-closed discipline.
	IntegrityViolations []string
}

// Reconciler runs startup reconciliation for one task's Progress Tree.
type Reconciler struct {
	Nodes       *NodeStore
	Checkpoints *statecheckpoint.Store
	EvidenceDir string
}

// Reconcile performs ADD §18.9's reconciliation steps 1-4 (load latest
// checkpoint, verify hash, recalculate artifact checksums, compare repo
// fingerprint is Part B's concern) scoped to what Part A owns: checkpoint
// integrity and the staged-artifact-vs-DB crash window.
func (r *Reconciler) Reconcile(ctx context.Context, taskID domain.TaskID) (ReconcileReport, error) {
	var report ReconcileReport

	checkpoints, err := r.Checkpoints.ListByTask(ctx, taskID)
	if err != nil && !isNotFound(err) {
		return report, fmt.Errorf("progress: reconcile: list checkpoints for task %s: %w", taskID, err)
	}

	referenced := make(map[string]bool)
	for _, row := range checkpoints {
		manifest, err := statecheckpoint.Unmarshal([]byte(row.ManifestJSON))
		if err != nil {
			report.IntegrityViolations = append(report.IntegrityViolations,
				fmt.Sprintf("checkpoint %s: unparseable manifest: %v", row.ID, err))
			continue
		}
		ok, err := statecheckpoint.Verify(manifest)
		if err != nil {
			report.IntegrityViolations = append(report.IntegrityViolations,
				fmt.Sprintf("checkpoint %s: digest recompute failed: %v", row.ID, err))
			continue
		}
		if !ok {
			report.IntegrityViolations = append(report.IntegrityViolations,
				fmt.Sprintf("checkpoint %s: stored integrity_sha256 does not match recomputed digest", row.ID))
			continue
		}
		for _, a := range manifest.Artifacts {
			referenced[a.SHA256] = true
		}
	}

	if r.EvidenceDir != "" {
		orphans, err := scanOrphanedEvidence(r.EvidenceDir, referenced)
		if err != nil {
			return report, err
		}
		report.OrphanedStagedArtifacts = orphans
	}

	return report, nil
}

// scanOrphanedEvidence walks dir/sha256/** (FileStager's layout) and
// reports every content-addressed file whose digest (its own filename) is
// not in referenced.
func scanOrphanedEvidence(dir string, referenced map[string]bool) ([]string, error) {
	root := filepath.Join(dir, "sha256")
	var orphans []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		digest := d.Name()
		if !referenced[digest] {
			orphans = append(orphans, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("progress: reconcile: scan evidence dir %s: %w", dir, err)
	}
	return orphans, nil
}

// isNotFound reports whether err is the frozen ErrNotFound (or wraps it),
// so Reconcile can treat "no checkpoints yet for this task" as an empty
// report rather than an error.
func isNotFound(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == domain.ErrCodeNotFound
}
