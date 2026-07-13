// collect.go: ADR-046 step (a) for every covered table class — builds
// the full read-only plan of a retention pass (rows to archive+delete,
// rollups to write, artifact directories to remove, skip notes) without
// mutating anything. The engine (engine.go) then archives, verifies, and
// finally deletes exactly what this plan named.
package retention

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// plan is one pass's complete, immutable selection.
type plan struct {
	// batches, in DELETION order. FK-aware ordering (ADR-046):
	// policy_decisions before predictions (so the explicit delete set
	// equals what the ON DELETE CASCADE would have touched), and every
	// checkpoint table after the row-only classes.
	batches []*tableBatch

	usage   map[usageKey]*usageAgg
	samples []calibrationSample

	// artifactDirs are repository_checkpoints.artifact_root values of
	// deleted rows, removed only after the delete transaction commits.
	artifactDirs []string

	notes []string
}

// deletionTables is the fixed table set a pass covers, in deletion order;
// engine.go reports one TableResult per entry even when zero rows
// expired, so the auspex.gc.v1 output shape is stable.
var deletionTables = []string{
	"policy_decisions",
	"predictions",
	"events",
	"feature_vectors",
	"runway_forecasts",
	"authorizations",
	"node_completions",
	"state_checkpoints",
	"repository_checkpoints",
}

// terminalTaskStatuses mirrors internal/progress.TaskCompleted/TaskFailed
// (task_store.go's tasks.status vocabulary). Kept as wire strings rather
// than an import: retention must not depend on internal/progress just for
// two constants, and a drift here fails the boundary tests loudly.
var terminalTaskStatuses = []string{"completed", "failed"}

// collect builds the plan for cutoff. Class order matters only in one
// place: predictions (calibration rollup) must be collected while the
// events it joins are still present — trivially true here since collect
// never deletes, but the batches list also keeps that ordering for the
// delete phase.
func (e *Engine) collect(ctx context.Context, cutoff time.Time) (*plan, error) {
	p := &plan{usage: map[usageKey]*usageAgg{}}

	// --- predictions + policy_decisions (ADR-046 class 3) -------------
	predictions, notes, err := selectExpired(ctx, e.DB, "predictions", "id", "created_at", cutoff, "")
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, notes...)

	decisions, err := e.collectPolicyDecisions(ctx, cutoff, predictions)
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, decisions.notes...)

	p.samples, err = buildCalibrationSamples(ctx, e.DB, predictions.rows)
	if err != nil {
		return nil, err
	}

	// --- events (usage rollup source) ----------------------------------
	events, notes, err := selectExpired(ctx, e.DB, "events", "event_id", "occurred_at", cutoff, "")
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, notes...)
	p.usage = buildUsageRollups(events.rows)

	// --- feature_vectors ------------------------------------------------
	featureVectors, notes, err := selectExpired(ctx, e.DB, "feature_vectors", "turn_id", "created_at", cutoff, "")
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, notes...)

	// --- runway_forecasts ------------------------------------------------
	runway, err := e.collectRunwayForecasts(ctx, cutoff, decisions.batch)
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, runway.notes...)

	// --- authorizations: BOTH consumed AND expired (ADR-046) -------------
	authorizations, notes, err := selectExpired(ctx, e.DB, "authorizations", "id", "expires_at", cutoff, "consumed_at IS NOT NULL")
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, notes...)

	// --- checkpoints ------------------------------------------------------
	cps, err := e.collectCheckpoints(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	p.notes = append(p.notes, cps.notes...)
	p.artifactDirs = cps.artifactDirs

	p.batches = []*tableBatch{
		decisions.batch,
		predictions,
		events,
		featureVectors,
		runway.batch,
		authorizations,
		cps.nodeCompletions,
		cps.state,
		cps.repository,
	}
	return p, nil
}

// batchAndNotes pairs a selection with its skip/exclusion notes.
type batchAndNotes struct {
	batch *tableBatch
	notes []string
}

// collectPolicyDecisions selects the policy_decisions delete set: every
// decision tied to an expired prediction (they would ON DELETE CASCADE
// with it — selecting them explicitly keeps the archive equal to the
// delete set, ADR-046), plus orphan decisions (prediction_id IS NULL)
// expired by their own decided_at.
func (e *Engine) collectPolicyDecisions(ctx context.Context, cutoff time.Time, predictions *tableBatch) (batchAndNotes, error) {
	byPrediction, err := selectByKeyIn(ctx, e.DB, "policy_decisions", "prediction_id", predictions.keys, "id")
	if err != nil {
		return batchAndNotes{}, err
	}

	orphanCandidates, err := queryRowMaps(ctx, e.DB,
		`SELECT * FROM policy_decisions WHERE prediction_id IS NULL AND decided_at < ? ORDER BY id`,
		coarseCutoffString(cutoff))
	if err != nil {
		return batchAndNotes{}, err
	}
	orphans, notes := filterExpired(orphanCandidates, "policy_decisions", "decided_at", cutoff)

	rows := append(byPrediction, orphans...) //nolint:gocritic // deliberate combined slice; byPrediction is not reused
	return batchAndNotes{batch: newBatch("policy_decisions", "id", rows), notes: notes}, nil
}

// collectRunwayForecasts selects expired runway_forecasts, EXCLUDING rows
// still referenced by a policy_decisions row that survives this pass —
// deleting those would ON DELETE SET NULL-mutate a row being kept
// (ADR-046's per-class table).
func (e *Engine) collectRunwayForecasts(ctx context.Context, cutoff time.Time, deletedDecisions *tableBatch) (batchAndNotes, error) {
	candidates, notes, err := selectExpired(ctx, e.DB, "runway_forecasts", "id", "created_at", cutoff, "")
	if err != nil {
		return batchAndNotes{}, err
	}

	deletedDecisionIDs := make(map[string]bool, len(deletedDecisions.keys))
	for _, k := range deletedDecisions.keys {
		deletedDecisionIDs[stringOrEmpty(k)] = true
	}

	refs, err := queryRowMaps(ctx, e.DB,
		`SELECT id, runway_forecast_id FROM policy_decisions WHERE runway_forecast_id IS NOT NULL`)
	if err != nil {
		return batchAndNotes{}, err
	}
	stillReferenced := make(map[string]bool)
	for _, ref := range refs {
		if !deletedDecisionIDs[stringOrEmpty(ref["id"])] {
			stillReferenced[stringOrEmpty(ref["runway_forecast_id"])] = true
		}
	}

	var kept int
	var rows []map[string]any
	for _, row := range candidates.rows {
		if stillReferenced[stringOrEmpty(row["id"])] {
			kept++
			continue
		}
		rows = append(rows, row)
	}
	if kept > 0 {
		notes = append(notes, fmt.Sprintf("runway_forecasts: kept %d expired row(s) still referenced by surviving policy_decisions", kept))
	}
	return batchAndNotes{batch: newBatch("runway_forecasts", "id", rows), notes: notes}, nil
}

// checkpointPlan is collectCheckpoints' result: the three checkpoint-side
// batches plus the artifact directories of deleted repository
// checkpoints.
type checkpointPlan struct {
	state           *tableBatch
	nodeCompletions *tableBatch
	repository      *tableBatch
	artifactDirs    []string
	notes           []string
}

// collectCheckpoints implements ADR-046's checkpoint class with all its
// safeguards:
//
//   - only tasks whose status is terminal (completed/failed) AND whose
//     completed_at is older than the window are eligible;
//   - a terminal task with NULL/unparseable completed_at, or with any
//     undatable checkpoint row, is skipped entirely with a note
//     (conservative: never guess about resumability evidence);
//   - the most recent state checkpoint AND most recent repository
//     checkpoint per task are always kept, plus the repository
//     checkpoint the kept state checkpoint references.
func (e *Engine) collectCheckpoints(ctx context.Context, cutoff time.Time) (checkpointPlan, error) {
	cp := checkpointPlan{
		state:           newBatch("state_checkpoints", "id", nil),
		nodeCompletions: newBatch("node_completions", "node_id", nil),
		repository:      newBatch("repository_checkpoints", "id", nil),
	}

	taskRows, err := queryRowMaps(ctx, e.DB,
		`SELECT id, status, completed_at FROM tasks WHERE status IN (`+placeholders(len(terminalTaskStatuses))+`) ORDER BY id`,
		anySlice(terminalTaskStatuses)...)
	if err != nil {
		return checkpointPlan{}, err
	}

	var eligible []any
	for _, row := range taskRows {
		taskID := stringOrEmpty(row["id"])
		completedAt, ok := row["completed_at"].(string)
		if !ok || completedAt == "" {
			cp.notes = append(cp.notes, fmt.Sprintf("checkpoints: skipped terminal task %s: completed_at is NULL, completion age not derivable", taskID))
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, completedAt)
		if err != nil {
			cp.notes = append(cp.notes, fmt.Sprintf("checkpoints: skipped terminal task %s: completed_at %q unparseable", taskID, completedAt))
			continue
		}
		if t.Before(cutoff) {
			eligible = append(eligible, taskID)
		}
	}
	if len(eligible) == 0 {
		return cp, nil
	}

	stateRows, err := selectByKeyIn(ctx, e.DB, "state_checkpoints", "task_id", eligible, "id")
	if err != nil {
		return checkpointPlan{}, err
	}
	repoRows, err := selectByKeyIn(ctx, e.DB, "repository_checkpoints", "task_id", eligible, "id")
	if err != nil {
		return checkpointPlan{}, err
	}
	completionRows, err := selectByKeyIn(ctx, e.DB, "node_completions", "task_id", eligible, "node_id")
	if err != nil {
		return checkpointPlan{}, err
	}

	// Latest state checkpoint per task (the resume anchor). Any undatable
	// row disqualifies its whole task from checkpoint GC.
	keepState, disqualified1 := latestPerTask(stateRows)
	keepRepo, disqualified2 := latestPerTask(repoRows)

	skippedTasks := map[string]bool{}
	for task := range disqualified1 {
		skippedTasks[task] = true
	}
	for task := range disqualified2 {
		skippedTasks[task] = true
	}
	for task := range skippedTasks {
		cp.notes = append(cp.notes, fmt.Sprintf("checkpoints: skipped task %s: checkpoint row with unparseable created_at, latest-checkpoint safeguard not computable", task))
	}

	// The kept state checkpoint's repository_checkpoint_id joins the keep
	// set — the anchor must never dangle (ADR-046 safeguard list).
	keepRepoIDs := make(map[string]bool)
	for _, row := range repoRows {
		if keepRepo[stringOrEmpty(row["id"])] {
			keepRepoIDs[stringOrEmpty(row["id"])] = true
		}
	}
	for _, row := range stateRows {
		if keepState[stringOrEmpty(row["id"])] {
			if rc, ok := row["repository_checkpoint_id"].(string); ok && rc != "" {
				keepRepoIDs[rc] = true
			}
		}
	}

	var stateDelete, repoDelete, completionDelete []map[string]any
	for _, row := range stateRows {
		if skippedTasks[stringOrEmpty(row["task_id"])] || keepState[stringOrEmpty(row["id"])] {
			continue
		}
		stateDelete = append(stateDelete, row)
	}
	for _, row := range repoRows {
		if skippedTasks[stringOrEmpty(row["task_id"])] || keepRepoIDs[stringOrEmpty(row["id"])] {
			continue
		}
		repoDelete = append(repoDelete, row)
		if root, ok := row["artifact_root"].(string); ok && root != "" {
			cp.artifactDirs = append(cp.artifactDirs, root)
		}
	}
	for _, row := range completionRows {
		if skippedTasks[stringOrEmpty(row["task_id"])] {
			continue
		}
		completionDelete = append(completionDelete, row)
	}

	cp.state = newBatch("state_checkpoints", "id", stateDelete)
	cp.repository = newBatch("repository_checkpoints", "id", repoDelete)
	cp.nodeCompletions = newBatch("node_completions", "node_id", completionDelete)
	return cp, nil
}

// latestPerTask returns the id set of each task's newest row by parsed
// created_at (ties broken by larger id, so the answer is total). A task
// with any unparseable created_at lands in disqualified instead — its
// rows must all be kept.
func latestPerTask(rows []map[string]any) (keepIDs map[string]bool, disqualified map[string]bool) {
	type latest struct {
		id string
		t  time.Time
	}
	best := map[string]latest{}
	disqualified = map[string]bool{}
	for _, row := range rows {
		task := stringOrEmpty(row["task_id"])
		id := stringOrEmpty(row["id"])
		t, err := time.Parse(time.RFC3339Nano, stringOrEmpty(row["created_at"]))
		if err != nil {
			disqualified[task] = true
			continue
		}
		cur, ok := best[task]
		if !ok || t.After(cur.t) || (t.Equal(cur.t) && id > cur.id) {
			best[task] = latest{id: id, t: t}
		}
	}
	keepIDs = make(map[string]bool, len(best))
	for task, b := range best {
		if !disqualified[task] {
			keepIDs[b.id] = true
		}
	}
	return keepIDs, disqualified
}

// artifactDirInsideDataDir reports whether dir resolves strictly inside
// dataDir — the containment guard ADR-046 requires before RemoveAll on a
// repository checkpoint's artifact_root (a root outside the data dir is
// noted, never removed).
func artifactDirInsideDataDir(dir, dataDir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absData, absDir)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
