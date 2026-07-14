// export_test.go: FR-170/171 calibration export (issue #11) — the JSONL
// stream carries the same honest prediction-vs-actual pairing the rollup
// archives, live and archived rows both, with the #20 identity labels
// surviving end-to-end (and archival itself, per migration 0061).
package retention

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// seedLabeledPrediction mirrors seedPrediction but stamps the #20 Phase 0
// identity columns (migration 0046) the way EvaluateTurn does.
func seedLabeledPrediction(t *testing.T, db *sqlite.DB, id, turnID string, createdAt time.Time, provider, modelID, modelFamily, effort string) {
	t.Helper()
	exec(t, db, `INSERT INTO predictions (
			id, turn_id, predictor_id, predictor_version, feature_set_version,
			token_p50, token_p80, token_p90,
			quota_risk_score, context_risk_score, completion_risk_score,
			blast_radius_risk_score, overall_risk_score,
			confidence, calibrated, reason_codes_json, created_at,
			provider, model_id, model_family, effort
		) VALUES (?, ?, 'rule', 'v1', 'fs1', 1000, 2000, 3000, 0.1, 0.2, 0.3, 0.4, 0.42, 'low', 0, '["PREDICTION_COLD_START"]', ?, ?, ?, ?, ?)`,
		id, turnID, ts(createdAt), provider, modelID, modelFamily, effort)
}

func decodeExportLines(t *testing.T, out []byte) []ExportRecord {
	t.Helper()
	var records []ExportRecord
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var rec ExportRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode export line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestExportCalibration_EmptyDatabase_IsValidEmptyDataset(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var buf bytes.Buffer

	summary, err := e.ExportCalibration(context.Background(), &buf)
	if err != nil {
		t.Fatalf("ExportCalibration: %v", err)
	}
	if summary.LiveRows != 0 || summary.ArchivedRows != 0 || summary.ActualKnownRows != 0 || summary.LabeledRows != 0 {
		t.Errorf("summary = %+v, want all zeros", summary)
	}
	if buf.Len() != 0 {
		t.Errorf("expected zero output bytes for an empty dataset, got %q", buf.String())
	}
}

func TestExportCalibration_LiveRow_JoinLabelsAndDetail(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	seedLabeledPrediction(t, db, "pred-1", "turn-1", newTime, "claude", "claude-fable-5", "fable", "high")
	seedEvent(t, db, "ev-start", "provider.turn.started", newTime, "sessX", "turn-1", `{"prompt_sha256":"x"}`)
	seedEvent(t, db, "ev-done", "provider.turn.completed", newTime.Add(time.Minute), "sessX", "turn-1", `{}`)

	var buf bytes.Buffer
	summary, err := e.ExportCalibration(ctx, &buf)
	if err != nil {
		t.Fatalf("ExportCalibration: %v", err)
	}
	if summary.LiveRows != 1 || summary.ArchivedRows != 0 || summary.ActualKnownRows != 1 || summary.LabeledRows != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	records := decodeExportLines(t, buf.Bytes())
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]
	if rec.SchemaVersion != ExportSchemaVersion || rec.Source != "live" {
		t.Errorf("envelope: %+v", rec)
	}
	if rec.PredictionID != "pred-1" || rec.TurnID != "turn-1" {
		t.Errorf("identity: %+v", rec)
	}
	if rec.ModelFamily == nil || *rec.ModelFamily != "fable" || rec.Effort == nil || *rec.Effort != "high" {
		t.Errorf("labels missing: family=%v effort=%v", rec.ModelFamily, rec.Effort)
	}
	if !rec.ActualKnown || rec.ActualOutcome == nil || *rec.ActualOutcome != "completed" {
		t.Errorf("actual side: known=%v outcome=%v", rec.ActualKnown, rec.ActualOutcome)
	}
	if rec.TokenP50 == nil || *rec.TokenP50 != 1000 || rec.OverallRiskScore != 0.42 {
		t.Errorf("predicted side: %+v", rec)
	}
	// Live-only detail present.
	if rec.QuotaRiskScore == nil || *rec.QuotaRiskScore != 0.1 {
		t.Errorf("live detail missing: quota=%v", rec.QuotaRiskScore)
	}
	if len(rec.ReasonCodes) != 1 || rec.ReasonCodes[0] != "PREDICTION_COLD_START" {
		t.Errorf("reason codes: %v", rec.ReasonCodes)
	}
}

func TestExportCalibration_ArchivedRow_IdentitySurvivesRetention(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	// A labeled prediction OLD enough for a retention pass to archive it
	// into calibration_samples and delete the live row.
	seedLabeledPrediction(t, db, "pred-old", "turn-old", oldTime, "claude", "claude-haiku-4-5", "haiku", "low")
	if _, err := e.Run(ctx, RunRequest{}); err != nil {
		t.Fatalf("retention Run: %v", err)
	}
	var liveLeft int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM predictions WHERE id = 'pred-old'`).Scan(&liveLeft); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if liveLeft != 0 {
		t.Fatal("precondition: retention pass should have archived+deleted the old prediction")
	}

	// Migration 0061's whole point: the archived sample kept its labels.
	var family, effort sql.NullString
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT model_family, effort FROM calibration_samples WHERE prediction_id = 'pred-old'`,
	).Scan(&family, &effort); err != nil {
		t.Fatalf("read archived sample: %v", err)
	}
	if family.String != "haiku" || effort.String != "low" {
		t.Fatalf("archived labels lost: family=%v effort=%v", family, effort)
	}

	// And the export surfaces the archived row, labels intact.
	var buf bytes.Buffer
	summary, err := e.ExportCalibration(ctx, &buf)
	if err != nil {
		t.Fatalf("ExportCalibration: %v", err)
	}
	if summary.ArchivedRows != 1 || summary.LabeledRows != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	records := decodeExportLines(t, buf.Bytes())
	if len(records) != 1 || records[0].Source != "archived" {
		t.Fatalf("records: %+v", records)
	}
	if records[0].ModelFamily == nil || *records[0].ModelFamily != "haiku" {
		t.Fatalf("archived export lost labels: %+v", records[0])
	}
}

func TestExportCalibration_NoPathsPromptsOrRemotesInOutput(t *testing.T) {
	// FR-171 pin: the export of a realistically-seeded database must not
	// contain the worktree's filesystem path (the only path-shaped value
	// the seeded fixture rows carry anywhere near the exported tables).
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	seedCore(t, db)
	seedLabeledPrediction(t, db, "pred-1", "turn-1", newTime, "claude", "claude-fable-5", "fable", "max")
	seedEvent(t, db, "ev-start", "provider.turn.started", newTime, "sess1", "turn-1", `{"prompt_sha256":"deadbeef"}`)

	var buf bytes.Buffer
	if _, err := e.ExportCalibration(ctx, &buf); err != nil {
		t.Fatalf("ExportCalibration: %v", err)
	}
	out := buf.String()
	for _, forbidden := range []string{"/tmp/repo1", "prompt_sha256", "canonical_root"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("export leaked %q:\n%s", forbidden, out)
		}
	}
}
