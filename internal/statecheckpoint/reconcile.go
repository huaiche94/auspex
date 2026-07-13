// reconcile.go: startup reconciliation for Service.Create's own crash
// windows (agents/checkpoint.md Part A deliverable #6, checkpoint-a06;
// ADD §18.9 "On startup/resume"). This is deliberately narrower than, and
// separate from, internal/progress.Reconciler (checkpoint-a04's own
// startup reconciliation for CompleteNode's staged-artifact-vs-DB crash
// window) — that reconciler targets a DIFFERENT crash window entirely
// (FileStager's evidence directory vs. the artifacts table, scoped to
// node completion). This file's Reconciler targets Create's own, narrower
// sequence: read tree state -> build/seal manifest -> insert row.
//
// # What crash windows actually exist in Create
//
// Tracing Create's three phases (Phase/HaltAfter in service.go) against
// what is actually durable at each point:
//
//   - PhaseReadTree (after ListNodes/ListArtifacts): nothing written yet.
//     A crash here is a pure no-op from this package's perspective — the
//     next Create call (or the caller's own retry) starts fresh. Nothing
//     to reconcile.
//   - PhaseSeal (after Build/Seal/Marshal produce a fully self-checksummed
//     manifest in memory): still nothing written. Same as above.
//   - PhaseInsert (after Store.Insert's single INSERT statement commits):
//     this is the ONE phase where durable state exists. Store.Insert is a
//     single SQL statement, which SQLite commits atomically — there is no
//     reachable "half-inserted row" state for this package to crash into
//     (unlike internal/repocheckpoint's multi-file artifact writes, which
//     is exactly why that package needs a temp-dir-then-atomic-rename
//     protocol and this one does not). So the real question a startup
//     reconciler must answer is not "is this row half-written" (it
//     cannot be) but "is this row - now that it durably exists -
//     everything CompleteNode's Constitution §6.2/§6.5 requires backing
//     evidence to be": a well-formed schema version, a manifest whose
//     digest still matches what is stored (bit rot / an interrupted
//     future rewrite this package does not currently perform, but
//     "trust nothing, recompute" is this whole role's governing
//     discipline per internal/repocheckpoint/verify.go's identical
//     stance), and a manifest whose own task_id/checkpoint_id agree with
//     the row's queryable columns (a crash during some future migration
//     or manual DB surgery diverging the two, caught the same way
//     internal/repocheckpoint.Verify cross-checks manifest.CheckpointID
//     against row.ID).
//
// Reconcile therefore proves the negative case exhaustively (crash at any
// phase never leaves a row that looks complete but isn't backed by
// verified evidence, Constitution §6.5) rather than "fixing" anything —
// there is nothing for it to repair, by construction, since the only
// durable-state phase is a single atomic statement. Every check below is a
// read-only integrity scan; Reconcile never deletes, rewrites, or
// re-inserts a row itself (mirrors internal/repocheckpoint.Verify and
// internal/progress.Reconciler both being read-only diagnostic passes,
// never self-healing writers).
package statecheckpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// ReconcileReport is Reconcile's result for one task: every integrity
// problem found among that task's state_checkpoints rows, empty when
// every row is fully valid.
type ReconcileReport struct {
	// TaskID is the task these checkpoints belong to (echoed back for a
	// caller aggregating reports across many tasks).
	TaskID domain.TaskID
	// Violations lists every checkpoint row this pass found to be
	// internally inconsistent or unverifiable, one human-readable entry
	// per problem. Empty (nil) means every row for this task passed every
	// check — the expected, and so-far only reachable, outcome given
	// Store.Insert's single-statement atomicity (see this file's package
	// doc comment).
	Violations []string
	// CheckpointsScanned is how many rows this pass examined, so a caller
	// (or a test) can distinguish "reconciled zero problems because there
	// were zero checkpoints" from "reconciled zero problems among N
	// checkpoints" — the same evidence-of-work discipline Constitution §6
	// requires of completion evidence generally.
	CheckpointsScanned int
}

// Reconciler runs startup reconciliation over one task's State Checkpoints.
type Reconciler struct {
	Store *Store
}

// NewReconciler constructs a Reconciler bound to store.
func NewReconciler(store *Store) *Reconciler {
	return &Reconciler{Store: store}
}

// Reconcile loads every state_checkpoints row for taskID and verifies each
// one is fully self-consistent: parseable manifest JSON, the frozen
// SchemaVersion, a non-empty stored digest, a recomputed digest that
// matches the stored one, and manifest.TaskID/manifest.CheckpointID
// agreeing with the row's own queryable task_id/id columns. Every problem
// found is collected (not just the first), matching
// internal/repocheckpoint.VerifyResult's "collect every problem in one
// pass" discipline so a caller gets the complete picture in one call.
func (r *Reconciler) Reconcile(ctx context.Context, taskID domain.TaskID) (ReconcileReport, error) {
	report := ReconcileReport{TaskID: taskID}

	rows, err := r.Store.ListByTask(ctx, taskID)
	if err != nil {
		if isNotFoundErr(err) {
			return report, nil
		}
		return report, fmt.Errorf("statecheckpoint: reconcile: list checkpoints for task %s: %w", taskID, err)
	}
	report.CheckpointsScanned = len(rows)

	for _, row := range rows {
		for _, problem := range reconcileOneRow(row) {
			report.Violations = append(report.Violations, fmt.Sprintf("checkpoint %s: %s", row.ID, problem))
		}
	}

	return report, nil
}

// reconcileOneRow applies every per-row check to a single state_checkpoints
// row, returning every problem found (never stopping at the first).
func reconcileOneRow(row Row) []string {
	var problems []string

	manifest, err := Unmarshal([]byte(row.ManifestJSON))
	if err != nil {
		// An unparseable manifest makes every further check meaningless
		// (there is no document left to cross-check) — report this one
		// problem and stop for this row, mirroring
		// statecheckpoint.Service.Verify's identical fail-closed handling
		// of the same failure mode.
		return []string{fmt.Sprintf("manifest_json is not valid JSON: %v", err)}
	}

	if manifest.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("manifest schema_version %q does not match expected %q", manifest.SchemaVersion, SchemaVersion))
	}
	if row.IntegritySHA256 == "" {
		problems = append(problems, "stored integrity_sha256 is empty (row was never sealed before being persisted)")
	}
	if string(manifest.TaskID) != string(row.TaskID) {
		problems = append(problems, fmt.Sprintf("manifest task_id %q does not match row task_id %q", manifest.TaskID, row.TaskID))
	}
	if string(manifest.CheckpointID) != string(row.ID) {
		problems = append(problems, fmt.Sprintf("manifest checkpoint_id %q does not match row id %q", manifest.CheckpointID, row.ID))
	}

	ok, err := Verify(manifest)
	if err != nil {
		problems = append(problems, fmt.Sprintf("digest recompute failed: %v", err))
	} else if !ok {
		problems = append(problems, "stored integrity_sha256 does not match a fresh digest recomputation")
	}

	return problems
}

// isNotFoundErr reports whether err is the frozen ErrNotFound (or wraps
// it), so Reconcile can treat "no checkpoints yet for this task" as an
// empty, zero-violation report rather than an error — a task that has
// never been checkpointed is not itself a reconciliation problem.
// ListByTask does not currently return this error for an empty result (it
// returns a nil slice with a nil error instead), but this check is kept
// defensively: a future ListByTask revision that DOES distinguish
// "unknown task" from "known task, zero checkpoints" should not silently
// turn into a hard Reconcile failure for this package's own callers.
func isNotFoundErr(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Code == domain.ErrCodeNotFound
}
