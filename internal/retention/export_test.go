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

// TestExportCalibration_DurationPair_LiveRoundTrip pins the #62 duration
// pair through the JSONL export: the predicted forecast (nanoseconds) and
// the actual per-turn duration (milliseconds from the turn-joined usage
// event) both reach the wire record under their distinct-unit JSON keys,
// and a decode round-trips them intact.
func TestExportCalibration_DurationPair_LiveRoundTrip(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	seedLabeledPrediction(t, db, "pred-dur", "turn-dur", newTime, "claude", "claude-fable-5", "fable", "high")
	exec(t, db, `UPDATE predictions SET duration_p50 = ?, duration_p90 = ? WHERE id = 'pred-dur'`,
		int64(45_000_000_000), int64(120_000_000_000)) // 45s / 120s in ns
	seedEvent(t, db, "ev-dur-start", "provider.turn.started", newTime, "sessX", "turn-dur", `{"prompt_sha256":"x"}`)
	seedEvent(t, db, "ev-dur-usage", "provider.usage.observed", newTime.Add(time.Minute), "sessX", "turn-dur",
		`{"total_duration_ms":87000,"total_api_duration_ms":41000}`)

	var buf bytes.Buffer
	if _, err := e.ExportCalibration(ctx, &buf); err != nil {
		t.Fatalf("ExportCalibration: %v", err)
	}
	records := decodeExportLines(t, buf.Bytes())
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]
	if rec.DurationP50 == nil || *rec.DurationP50 != 45_000_000_000 ||
		rec.DurationP90 == nil || *rec.DurationP90 != 120_000_000_000 {
		t.Errorf("predicted duration (ns) not exported: p50=%v p90=%v", rec.DurationP50, rec.DurationP90)
	}
	if rec.ActualDurationMs == nil || *rec.ActualDurationMs != 87000 {
		t.Errorf("actual_duration_ms not exported: %v, want 87000", rec.ActualDurationMs)
	}
	// The wire keys must carry their units so a consumer never confuses the
	// nanosecond prediction with the millisecond actual.
	if !bytes.Contains(buf.Bytes(), []byte(`"duration_p50_ns":45000000000`)) ||
		!bytes.Contains(buf.Bytes(), []byte(`"actual_duration_ms":87000`)) {
		t.Errorf("expected unit-tagged duration keys in JSON:\n%s", buf.String())
	}
}

// TestExportCalibration_CostBand_FromShippedPricing pins the #72 predicted
// cost band: the export prices each row's token quantiles through the same
// internal/pricing table the forecast card renders (LowUSD = P50 × input
// price, HighUSD = P90 × output price), so the calibration measures the
// exact cost the user was shown. A row with no token forecast carries no
// cost band (unknown is not zero — never a fabricated $0).
func TestExportCalibration_CostBand_FromShippedPricing(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	// Opus-priced row: seed defaults token_p50=1000, token_p90=3000; opus
	// is $5 in / $25 out → low = 1000×5/1e6, high = 3000×25/1e6. The model
	// FAMILY resolves from the model_id substring "opus", exactly as the
	// forecast card resolves it.
	seedLabeledPrediction(t, db, "pred-cost", "turn-cost", newTime, "claude", "claude-opus-4-8", "opus", "xhigh")

	// A row with NO token forecast must export NO cost band.
	exec(t, db, `INSERT INTO predictions (
			id, turn_id, predictor_id, predictor_version, feature_set_version,
			quota_risk_score, context_risk_score, completion_risk_score,
			blast_radius_risk_score, overall_risk_score,
			confidence, calibrated, reason_codes_json, created_at
		) VALUES ('pred-notok', 'turn-notok', 'rule', 'v1', 'fs1', 0.1, 0.2, 0.3, 0.4, 0.5, 'low', 0, '[]', ?)`,
		ts(newTime.Add(time.Second)))

	var buf bytes.Buffer
	if _, err := e.ExportCalibration(ctx, &buf); err != nil {
		t.Fatalf("ExportCalibration: %v", err)
	}
	records := decodeExportLines(t, buf.Bytes())
	byTurn := map[string]ExportRecord{}
	for _, r := range records {
		byTurn[r.TurnID] = r
	}

	// Replicate EstimateTurnCost's exact arithmetic so the comparison is a
	// true byte-for-byte float match, not an approximation.
	wantLow := float64(1000) * 5 / 1_000_000
	wantHigh := float64(3000) * 25 / 1_000_000

	cost := byTurn["turn-cost"]
	if cost.CostModelFamily == nil || *cost.CostModelFamily != "opus" {
		t.Errorf("cost_model_family = %v, want opus", cost.CostModelFamily)
	}
	if cost.CostLowUSD == nil || *cost.CostLowUSD != wantLow {
		t.Errorf("cost_low_usd = %v, want %v", cost.CostLowUSD, wantLow)
	}
	if cost.CostHighUSD == nil || *cost.CostHighUSD != wantHigh {
		t.Errorf("cost_high_usd = %v, want %v", cost.CostHighUSD, wantHigh)
	}

	notok := byTurn["turn-notok"]
	if notok.CostLowUSD != nil || notok.CostHighUSD != nil || notok.CostModelFamily != nil {
		t.Errorf("row without a token forecast must carry no cost band: %+v", notok)
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
