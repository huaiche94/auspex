// engine_test.go: the ADR-046 retention protocol's acceptance tests, all
// against real on-disk SQLite (helpers_test.go):
//
//   - round-trip fidelity: archived JSONL reconstructs every deleted row,
//     every column (issue #19's explicit acceptance criterion);
//   - fail-closed: an archive-write failure deletes nothing and records a
//     failed run;
//   - dry-run: zero mutations of any kind;
//   - boundary: rows exactly at (and fractionally after) the cutoff are
//     retained;
//   - checkpoint safeguards: keep-latest-per-task, referenced-anchor
//     keeps, terminality/undatable-task skips, artifact-dir containment;
//   - authorizations: deleted only when both consumed and expired.
package retention

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// readArchive decodes a .jsonl.gz archive back into row maps.
func readArchive(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip open %s: %v", path, err)
	}
	var out []map[string]any
	r := bufio.NewReader(gzr)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 1 {
			var row map[string]any
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatalf("decode archive line %q: %v", line, err)
			}
			out = append(out, row)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
	}
	return out
}

// jsonValueEqual compares a directly-scanned SQLite value against its
// JSON round-tripped form (JSON numbers decode as float64).
func jsonValueEqual(orig, decoded any) bool {
	switch o := orig.(type) {
	case nil:
		return decoded == nil
	case string:
		d, ok := decoded.(string)
		return ok && d == o
	case int64:
		d, ok := decoded.(float64)
		return ok && d == float64(o)
	case float64:
		d, ok := decoded.(float64)
		return ok && d == o
	default:
		return false
	}
}

// assertArchiveReconstructs proves the fidelity criterion for one table:
// the archive at path contains exactly the rows named by wantIDs, and
// each archived row carries every column of the pre-delete snapshot with
// an equal value.
func assertArchiveReconstructs(t *testing.T, path, keyColumn string, snapshot map[string]map[string]any, wantIDs []string) {
	t.Helper()
	rows := readArchive(t, path)
	if len(rows) != len(wantIDs) {
		t.Fatalf("archive %s: %d rows, want %d", path, len(rows), len(wantIDs))
	}
	wanted := make(map[string]bool, len(wantIDs))
	for _, id := range wantIDs {
		wanted[id] = true
	}
	for _, row := range rows {
		id, _ := row[keyColumn].(string)
		if !wanted[id] {
			t.Fatalf("archive %s: unexpected row %q", path, id)
		}
		orig := snapshot[id]
		if orig == nil {
			t.Fatalf("archive %s: no snapshot for row %q", path, id)
		}
		if len(row) != len(orig) {
			t.Fatalf("archive %s row %q: %d columns, want %d (%v vs %v)", path, id, len(row), len(orig), row, orig)
		}
		for col, want := range orig {
			got, present := row[col]
			if !present {
				t.Fatalf("archive %s row %q: column %q missing", path, id, col)
			}
			if !jsonValueEqual(want, got) {
				t.Fatalf("archive %s row %q column %q: archived %v (%T), want %v (%T)", path, id, col, got, got, want, want)
			}
		}
	}
}

// archivePathFor pulls table's archive path out of a RunResult.
func archivePathFor(t *testing.T, res RunResult, table string) string {
	t.Helper()
	for _, tr := range res.Tables {
		if tr.Table == table {
			if tr.Archive == nil {
				t.Fatalf("table %s: no archive recorded (result: %+v)", table, tr)
			}
			return tr.Archive.Path
		}
	}
	t.Fatalf("table %s: not in result", table)
	return ""
}

// tableResultFor pulls table's counters out of a RunResult.
func tableResultFor(t *testing.T, res RunResult, table string) TableResult {
	t.Helper()
	for _, tr := range res.Tables {
		if tr.Table == table {
			return tr
		}
	}
	t.Fatalf("table %s: not in result", table)
	return TableResult{}
}

// seedFidelityFixture populates every covered class with expired and hot
// rows, returning the expected per-table delete sets.
func seedFidelityFixture(t *testing.T, e *Engine, dataDir string) map[string][]string {
	t.Helper()
	db := e.DB
	seedCore(t, db)

	// events: an expired quota observation, the expired turn-1 lifecycle
	// (started + failed outcome — the calibration join input), and a hot
	// event that must survive.
	seedEvent(t, db, "ev-old-quota", "provider.quota.observed", oldTime, "sess1", "",
		`{"limit_id":"five_hour","used_percent":72.5}`)
	seedEvent(t, db, "ev-old-start", "provider.turn.started", oldTime, "sess1", "turn-1",
		`{"prompt_sha256":"abc","prompt_byte_length":11,"prompt_approx_tokens":3}`)
	seedEvent(t, db, "ev-old-outcome", "provider.turn.failed", oldTime.Add(time.Minute), "sess1", "turn-1",
		`{"failure_class":"provider_rate_limit","error_message_len":42}`)
	seedEvent(t, db, "ev-new", "provider.usage.observed", newTime, "sess1", "",
		`{"total_cost_usd":1.5}`)

	// feature_vectors.
	exec(t, db, `INSERT INTO feature_vectors (turn_id, feature_set_version, features_json, created_at)
		VALUES ('turn-1', 'fs1', '{"f":1}', ?)`, ts(oldTime))
	exec(t, db, `INSERT INTO feature_vectors (turn_id, feature_set_version, features_json, created_at)
		VALUES ('turn-new', 'fs1', '{"f":2}', ?)`, ts(newTime))

	// predictions.
	seedPrediction(t, db, "pred-1", "turn-1", oldTime)
	seedPrediction(t, db, "pred-new", "turn-new", newTime)

	// runway_forecasts: rf-old expires unreferenced; rf-ref is expired
	// but referenced by the SURVIVING dec-new, so it must be kept.
	for _, rf := range []struct {
		id string
		at time.Time
	}{{"rf-old", oldTime}, {"rf-ref", oldTime}, {"rf-new", newTime}} {
		exec(t, db, `INSERT INTO runway_forecasts (id, session_id, limit_id, horizon_seconds, risk_score, calibrated, confidence, reason_codes_json, created_at)
			VALUES (?, 'sess1', 'five_hour', 3600, 0.5, 0, 'low', '[]', ?)`, rf.id, ts(rf.at))
	}

	// policy_decisions: dec-1 is RECENT but tied to the expired pred-1
	// (goes with it — the cascade-equivalence rule); dec-orphan is an
	// expired standalone; dec-new survives and pins rf-ref.
	exec(t, db, `INSERT INTO policy_decisions (id, prediction_id, policy_version, action, severity, requires_confirmation, reason_codes_json, decided_at)
		VALUES ('dec-1', 'pred-1', 'v1', 'WARN', 'low', 0, '[]', ?)`, ts(newTime))
	exec(t, db, `INSERT INTO policy_decisions (id, prediction_id, policy_version, action, severity, requires_confirmation, reason_codes_json, decided_at)
		VALUES ('dec-orphan', NULL, 'v1', 'ALLOW', 'low', 0, '[]', ?)`, ts(oldTime))
	exec(t, db, `INSERT INTO policy_decisions (id, prediction_id, runway_forecast_id, policy_version, action, severity, requires_confirmation, reason_codes_json, decided_at)
		VALUES ('dec-new', 'pred-new', 'rf-ref', 'v1', 'ALLOW', 'low', 0, '[]', ?)`, ts(newTime))

	// authorizations: deleted only when BOTH consumed AND expired.
	exec(t, db, `INSERT INTO authorizations (id, turn_id, prompt_hash, snapshot_fingerprint, decision, issued_at, expires_at, consumed_at)
		VALUES ('auth-del', 'turn-a1', 'ph', 'fp', 'allow', ?, ?, ?)`, ts(oldTime), ts(oldTime.Add(time.Hour)), ts(oldTime.Add(time.Minute)))
	exec(t, db, `INSERT INTO authorizations (id, turn_id, prompt_hash, snapshot_fingerprint, decision, issued_at, expires_at, consumed_at)
		VALUES ('auth-unconsumed', 'turn-a2', 'ph', 'fp', 'allow', ?, ?, NULL)`, ts(oldTime), ts(oldTime.Add(time.Hour)))
	exec(t, db, `INSERT INTO authorizations (id, turn_id, prompt_hash, snapshot_fingerprint, decision, issued_at, expires_at, consumed_at)
		VALUES ('auth-fresh', 'turn-a3', 'ph', 'fp', 'allow', ?, ?, ?)`, ts(oldTime), ts(newTime), ts(oldTime.Add(time.Minute)))

	// Checkpoints for task-old-done (terminal, completion expired):
	// repo checkpoints rc-old < rc-ref < rc-latest by age; the latest
	// STATE checkpoint references rc-ref, so the keep set is
	// {rc-latest (newest), rc-ref (anchor reference)} and only rc-old
	// (plus rc-outside, whose artifact root lives outside the data dir)
	// is deleted. sc-old is superseded by sc-latest.
	for _, rc := range []struct {
		id string
		at time.Time
	}{{"rc-old", oldTime}, {"rc-ref", oldTime.Add(time.Hour)}, {"rc-latest", oldTime.Add(48 * time.Hour)}} {
		root := filepath.Join(dataDir, "checkpoints", rc.id)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir artifact root: %v", err)
		}
		seedRepoCheckpoint(t, db, rc.id, "task-old-done", root, rc.at)
	}
	outsideRoot := filepath.Join(t.TempDir(), "outside-artifacts")
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("mkdir outside artifact root: %v", err)
	}
	seedRepoCheckpoint(t, db, "rc-outside", "task-old-done", outsideRoot, oldTime.Add(-time.Hour))

	seedStateCheckpoint(t, db, "sc-old", "task-old-done", nil, oldTime)
	seedStateCheckpoint(t, db, "sc-latest", "task-old-done", "rc-ref", oldTime.Add(24*time.Hour))

	// node_completions for task-old-done needs its progress node.
	exec(t, db, `INSERT INTO progress_nodes (id, task_id, ordinal, kind, title, status, version, updated_at)
		VALUES ('node-1', 'task-old-done', 0, 'step', 'n', 'completed', 1, ?)`, ts(oldTime))
	exec(t, db, `INSERT INTO node_completions (node_id, task_id, idempotency_key, payload_digest, state_checkpoint_id, completed_node_json, created_at)
		VALUES ('node-1', 'task-old-done', 'ik', 'pd', 'sc-latest', '{}', ?)`, ts(oldTime))

	// Checkpoints that must all be KEPT: non-terminal task, terminal
	// task with NULL completed_at, terminal task completed recently.
	seedStateCheckpoint(t, db, "sc-open-task", "task-old-open", nil, oldTime)
	seedStateCheckpoint(t, db, "sc-null-task", "task-null-done", nil, oldTime)
	seedStateCheckpoint(t, db, "sc-new-task", "task-new-done", nil, oldTime)

	return map[string][]string{
		"policy_decisions":       {"dec-1", "dec-orphan"},
		"predictions":            {"pred-1"},
		"events":                 {"ev-old-quota", "ev-old-start", "ev-old-outcome"},
		"feature_vectors":        {"turn-1"},
		"runway_forecasts":       {"rf-old"},
		"authorizations":         {"auth-del"},
		"node_completions":       {"node-1"},
		"state_checkpoints":      {"sc-old"},
		"repository_checkpoints": {"rc-old", "rc-outside"},
	}
}

// keyColumns mirrors the engine's per-table primary keys for snapshotting.
var keyColumns = map[string]string{
	"policy_decisions":       "id",
	"predictions":            "id",
	"events":                 "event_id",
	"feature_vectors":        "turn_id",
	"runway_forecasts":       "id",
	"authorizations":         "id",
	"node_completions":       "node_id",
	"state_checkpoints":      "id",
	"repository_checkpoints": "id",
}

func TestRun_RoundTripFidelity_AllClasses(t *testing.T) {
	e, db, dataDir := newTestEngine(t)
	expected := seedFidelityFixture(t, e, dataDir)

	// Independent pre-delete snapshot of every covered table.
	snapshots := map[string]map[string]map[string]any{}
	totals := map[string]int{}
	for table, keyCol := range keyColumns {
		snapshots[table] = snapshotTable(t, db, table, keyCol)
		totals[table] = len(snapshots[table])
	}

	res, err := e.Run(context.Background(), RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("Status = %q, want ok", res.Status)
	}
	if res.RetentionDays != DefaultRetentionDays {
		t.Fatalf("RetentionDays = %d, want %d", res.RetentionDays, DefaultRetentionDays)
	}

	for table, wantIDs := range expected {
		tr := tableResultFor(t, res, table)
		if tr.Selected != len(wantIDs) || tr.Deleted != len(wantIDs) {
			t.Errorf("%s: selected=%d deleted=%d, want both %d", table, tr.Selected, tr.Deleted, len(wantIDs))
		}
		// Deleted rows are gone; everything else survived.
		if got, want := count(t, db, table, ""), totals[table]-len(wantIDs); got != want {
			t.Errorf("%s: %d rows remain, want %d", table, got, want)
		}
		for _, id := range wantIDs {
			if n := count(t, db, table, keyColumns[table]+" = ?", id); n != 0 {
				t.Errorf("%s: row %q still present after gc", table, id)
			}
		}
		// The acceptance criterion: the archive reconstructs the deleted
		// rows, every column.
		assertArchiveReconstructs(t, archivePathFor(t, res, table), keyColumns[table], snapshots[table], wantIDs)
	}

	// Kept sentinels, by class rule.
	for table, id := range map[string]string{
		"events":                 "ev-new",
		"feature_vectors":        "turn-new",
		"predictions":            "pred-new",
		"policy_decisions":       "dec-new",
		"runway_forecasts":       "rf-ref", // expired but referenced by surviving dec-new
		"authorizations":         "auth-unconsumed",
		"state_checkpoints":      "sc-latest", // keep-latest safeguard
		"repository_checkpoints": "rc-ref",    // anchor-referenced keep
	} {
		if n := count(t, db, table, keyColumns[table]+" = ?", id); n != 1 {
			t.Errorf("%s: expected kept row %q missing", table, id)
		}
	}
	for _, id := range []string{"sc-open-task", "sc-null-task", "sc-new-task"} {
		if n := count(t, db, "state_checkpoints", "id = ?", id); n != 1 {
			t.Errorf("state_checkpoints: task-safeguard row %q missing", id)
		}
	}

	// Artifact directories: deleted rc-old's dir removed; kept rows' dirs
	// intact; the outside-data-dir root left in place with a note.
	if _, err := os.Stat(filepath.Join(dataDir, "checkpoints", "rc-old")); !os.IsNotExist(err) {
		t.Errorf("rc-old artifact dir still exists (stat err: %v)", err)
	}
	for _, id := range []string{"rc-ref", "rc-latest"} {
		if _, err := os.Stat(filepath.Join(dataDir, "checkpoints", id)); err != nil {
			t.Errorf("kept artifact dir %s: %v", id, err)
		}
	}
	wantNotes := []string{
		"skipped terminal task task-null-done",
		"left artifact dir outside data dir in place",
	}
	joined := strings.Join(res.Notes, "\n")
	for _, want := range wantNotes {
		if !strings.Contains(joined, want) {
			t.Errorf("notes missing %q; notes: %v", want, res.Notes)
		}
	}

	// The run is durably audited.
	if n := count(t, db, "retention_runs", "status = 'ok' AND dry_run = 0 AND error IS NULL"); n != 1 {
		t.Errorf("retention_runs ok rows = %d, want 1", n)
	}

	// Rollups landed in the same pass (details proven in rollup_test.go).
	if res.UsageRollupRows == 0 || count(t, db, "usage_rollups_daily", "") == 0 {
		t.Errorf("usage rollups missing: result=%d, table=%d", res.UsageRollupRows, count(t, db, "usage_rollups_daily", ""))
	}
	if res.CalibrationSamples != 1 || count(t, db, "calibration_samples", "") != 1 {
		t.Errorf("calibration samples: result=%d, table=%d, want 1", res.CalibrationSamples, count(t, db, "calibration_samples", ""))
	}
}

func TestRun_FailClosed_UnwritableArchiveDir(t *testing.T) {
	e, db, dataDir := newTestEngine(t)
	seedFidelityFixture(t, e, dataDir)

	// A regular FILE where the archive tree must go makes every archive
	// MkdirAll fail — the injected write failure.
	if err := os.WriteFile(filepath.Join(dataDir, "archive"), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("plant blocking file: %v", err)
	}

	totals := map[string]int{}
	for table := range keyColumns {
		totals[table] = count(t, db, table, "")
	}

	res, err := e.Run(context.Background(), RunRequest{})
	if err == nil {
		t.Fatal("Run succeeded, want archive-write failure")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("error is %T, want *domain.Error", err)
	}
	if res.Status != "failed" {
		t.Errorf("Status = %q, want failed", res.Status)
	}

	// Fail-closed: ZERO rows deleted anywhere, no rollups written.
	for table, want := range totals {
		if got := count(t, db, table, ""); got != want {
			t.Errorf("%s: %d rows after failed run, want %d (nothing may be deleted)", table, got, want)
		}
	}
	if n := count(t, db, "usage_rollups_daily", ""); n != 0 {
		t.Errorf("usage_rollups_daily = %d rows after failed run, want 0", n)
	}
	if n := count(t, db, "calibration_samples", ""); n != 0 {
		t.Errorf("calibration_samples = %d rows after failed run, want 0", n)
	}
	// ...and the failure itself is durably recorded.
	if n := count(t, db, "retention_runs", "status = 'failed' AND error IS NOT NULL"); n != 1 {
		t.Errorf("retention_runs failed rows = %d, want 1", n)
	}
}

func TestRun_DryRun_TrulySideEffectFree(t *testing.T) {
	e, db, dataDir := newTestEngine(t)
	expected := seedFidelityFixture(t, e, dataDir)

	totals := map[string]int{}
	for table := range keyColumns {
		totals[table] = count(t, db, table, "")
	}

	res, err := e.Run(context.Background(), RunRequest{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !res.DryRun || res.Status != "ok" {
		t.Fatalf("DryRun=%v Status=%q, want true/ok", res.DryRun, res.Status)
	}

	// It still REPORTS what a real pass would do...
	for table, wantIDs := range expected {
		tr := tableResultFor(t, res, table)
		if tr.Selected != len(wantIDs) {
			t.Errorf("%s: dry-run selected=%d, want %d", table, tr.Selected, len(wantIDs))
		}
		if tr.Deleted != 0 || tr.Archive != nil {
			t.Errorf("%s: dry-run reported deleted=%d archive=%v, want 0/nil", table, tr.Deleted, tr.Archive)
		}
	}

	// ...but performs none of it: identical row counts, no rollup rows,
	// no retention_runs row, no archive directory at all.
	for table, want := range totals {
		if got := count(t, db, table, ""); got != want {
			t.Errorf("%s: %d rows after dry run, want %d", table, got, want)
		}
	}
	for _, table := range []string{"usage_rollups_daily", "calibration_samples", "retention_runs"} {
		if n := count(t, db, table, ""); n != 0 {
			t.Errorf("%s: %d rows after dry run, want 0", table, n)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "archive")); !os.IsNotExist(err) {
		t.Errorf("archive directory exists after dry run (stat err: %v)", err)
	}
}

func TestRun_Boundary_ExactCutoffRetained(t *testing.T) {
	e, db, _ := newTestEngine(t)

	// Exactly at the cutoff: retained (strict <, policy.go).
	seedEvent(t, db, "ev-at-cutoff", "provider.usage.observed", testCutoff, "sess-b", "", `{}`)
	// 500ms AFTER the cutoff, inside the cutoff's own second: retained.
	// This is the case naive string comparison against a trimmed
	// RFC3339Nano cutoff gets wrong ("...00Z" vs "...00.5Z") — proving
	// the coarse-SQL + exact-Go filter (select.go) is doing its job.
	seedEvent(t, db, "ev-fraction", "provider.usage.observed", testCutoff.Add(500*time.Millisecond), "sess-b", "", `{}`)
	// One second BEFORE the cutoff: expired.
	seedEvent(t, db, "ev-expired", "provider.usage.observed", testCutoff.Add(-time.Second), "sess-b", "", `{}`)

	res, err := e.Run(context.Background(), RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tr := tableResultFor(t, res, "events"); tr.Deleted != 1 {
		t.Fatalf("events deleted = %d, want 1", tr.Deleted)
	}
	for id, want := range map[string]int{"ev-at-cutoff": 1, "ev-fraction": 1, "ev-expired": 0} {
		if n := count(t, db, "events", "event_id = ?", id); n != want {
			t.Errorf("event %s: count %d, want %d", id, n, want)
		}
	}
}

func TestRun_Validation(t *testing.T) {
	e, _, _ := newTestEngine(t)

	_, err := e.Run(context.Background(), RunRequest{Policy: Policy{RetentionDays: -1}})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("negative retention: err = %v, want validation domain.Error", err)
	}

	broken := &Engine{}
	if _, err := broken.Run(context.Background(), RunRequest{}); !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("zero-value engine: err = %v, want validation domain.Error", err)
	}
}

func TestRun_SpaceReclamation_HonestAboutAutoVacuum(t *testing.T) {
	e, db, _ := newTestEngine(t)
	seedEvent(t, db, "ev-old", "provider.usage.observed", oldTime, "sess-v", "", `{"total_cost_usd":2.5}`)

	// db.go sets no auto_vacuum pragma, so the real database is in NONE
	// mode — the engine must report that, not assume incremental works.
	res, err := e.Run(context.Background(), RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.AutoVacuumMode != "none" {
		t.Fatalf("AutoVacuumMode = %q, want none (db.go bootstraps no auto_vacuum)", res.AutoVacuumMode)
	}
	if res.VacuumRan {
		t.Fatal("VacuumRan = true without --vacuum")
	}

	// Opt-in full VACUUM on a later pass.
	seedEvent(t, db, "ev-old-2", "provider.usage.observed", oldTime, "sess-v", "", `{}`)
	res, err = e.Run(context.Background(), RunRequest{Vacuum: true})
	if err != nil {
		t.Fatalf("Run --vacuum: %v", err)
	}
	if !res.VacuumRan {
		t.Fatal("VacuumRan = false with Vacuum requested")
	}
}
