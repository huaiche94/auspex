// export.go: the FR-170/171 de-identified calibration dataset export
// (issue #11, M13). Lives in this package because ADR-046 already made
// retention the owner of the prediction-vs-actual pairing semantics
// (calibration_samples, buildCalibrationSamples' outcome join) — export
// reuses exactly that join for live rows rather than growing a second,
// subtly-different definition of "actual outcome".
//
// De-identification posture (FR-171: "預設移除 prompt、absolute path、
// remote、identity、content"): satisfied by CONSTRUCTION, not by
// scrubbing — the export selects only columns that are opaque random
// identifiers (prediction/turn/session IDs), closed enums (confidence,
// outcome, reason codes, effort), model identifiers, numbers, and
// timestamps. No exported table column carries prompt text (never
// persisted anywhere, Constitution §7 rule 2), filesystem paths, git
// remotes, file content, or user identity, so there is nothing to
// scrub and no scrubber to get wrong. Opaque row IDs are retained
// deliberately: they are the join/stratification keys the research
// pipeline needs, and they identify database rows, not people.
package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// ExportSchemaVersion stamps every exported line.
const ExportSchemaVersion = "auspex.calibration-export.v1"

// ExportRecord is one JSONL line of the calibration export: the union of
// what a LIVE predictions row (full pipeline detail) and an ARCHIVED
// calibration_samples row (ADR-046's compact pair) can supply, with nil
// for whatever a source honestly lacks. Source says which shape this
// line is — a consumer must not read an archived line's absent scope
// quantiles as measured zeros (unknown is not zero).
type ExportRecord struct {
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"` // "live" | "archived"

	PredictionID     string  `json:"prediction_id"`
	TurnID           string  `json:"turn_id"`
	SessionID        *string `json:"session_id,omitempty"`
	PredictorID      string  `json:"predictor_id"`
	PredictorVersion string  `json:"predictor_version"`
	PredictedAt      string  `json:"predicted_at"`

	TokenP50 *int64 `json:"token_p50,omitempty"`
	TokenP80 *int64 `json:"token_p80,omitempty"`
	TokenP90 *int64 `json:"token_p90,omitempty"`

	// Scope quantiles + component risk scores + projections: live rows
	// only (calibration_samples deliberately archives the compact pair).
	FilesReadP50            *int64   `json:"files_read_p50,omitempty"`
	FilesReadP90            *int64   `json:"files_read_p90,omitempty"`
	FilesChangedP50         *int64   `json:"files_changed_p50,omitempty"`
	FilesChangedP90         *int64   `json:"files_changed_p90,omitempty"`
	LinesChangedP50         *int64   `json:"lines_changed_p50,omitempty"`
	LinesChangedP90         *int64   `json:"lines_changed_p90,omitempty"`
	QuotaRiskScore          *float64 `json:"quota_risk_score,omitempty"`
	ContextRiskScore        *float64 `json:"context_risk_score,omitempty"`
	CompletionRiskScore     *float64 `json:"completion_risk_score,omitempty"`
	BlastRadiusRiskScore    *float64 `json:"blast_radius_risk_score,omitempty"`
	ProjectedContextUsedP90 *float64 `json:"projected_context_used_p90,omitempty"`
	ReasonCodes             []string `json:"reason_codes,omitempty"`

	OverallRiskScore float64 `json:"overall_risk_score"`
	Confidence       string  `json:"confidence"`
	Calibrated       bool    `json:"calibrated"`

	// Identity labels (#20 Phase 0 / migration 0046+0061). Nil = never
	// observed, honestly unlabeled.
	Provider    *string `json:"provider,omitempty"`
	ModelID     *string `json:"model_id,omitempty"`
	ModelFamily *string `json:"model_family,omitempty"`
	Effort      *string `json:"effort,omitempty"`

	// Actual side, per ADR-046's honesty rules: ActualKnown=false means
	// no terminal turn event was correlatable — every Actual* field nil.
	ActualKnown        bool    `json:"actual_known"`
	ActualOutcome      *string `json:"actual_outcome,omitempty"`
	ActualFailureClass *string `json:"actual_failure_class,omitempty"`
	ActualOutcomeAt    *string `json:"actual_outcome_at,omitempty"`
}

// ExportSummary is what ExportCalibration reports about a completed
// export (the CLI's auspex.calibration-export-summary.v1 payload).
type ExportSummary struct {
	LiveRows        int
	ArchivedRows    int
	ActualKnownRows int
	LabeledRows     int // rows carrying at least a model_family label
}

// ExportCalibration is Engine's method form of the free function below,
// binding the engine's own DB — the narrow seam cli.NewExportCmd consumes.
func (e *Engine) ExportCalibration(ctx context.Context, w io.Writer) (ExportSummary, error) {
	return ExportCalibration(ctx, e.DB, w)
}

// ExportCalibration streams every prediction-vs-actual pair — live
// predictions joined against turn-outcome events exactly as the
// retention rollup joins them, plus already-archived calibration_samples
// — as JSONL onto w. Read-only: it never writes to the database and is
// safe to run at any time, including mid-hot-window with zero rows
// anywhere (an empty export is a valid, honest dataset).
func ExportCalibration(ctx context.Context, db *sqlite.DB, w io.Writer) (ExportSummary, error) {
	summary := ExportSummary{}
	enc := json.NewEncoder(w)

	liveRows, err := queryRowMaps(ctx, db, `SELECT * FROM predictions ORDER BY created_at, id`)
	if err != nil {
		return summary, fmt.Errorf("retention: export: select predictions: %w", err)
	}
	samples, err := buildCalibrationSamples(ctx, db, liveRows)
	if err != nil {
		return summary, fmt.Errorf("retention: export: join live outcomes: %w", err)
	}
	for i, s := range samples {
		rec := recordFromSample(s, "live")
		enrichFromLiveRow(&rec, liveRows[i])
		if err := writeExportRecord(enc, rec, &summary); err != nil {
			return summary, err
		}
		summary.LiveRows++
	}

	archivedRows, err := queryRowMaps(ctx, db, `SELECT * FROM calibration_samples ORDER BY predicted_at, prediction_id`)
	if err != nil {
		return summary, fmt.Errorf("retention: export: select calibration_samples: %w", err)
	}
	for _, row := range archivedRows {
		rec := recordFromArchivedRow(row)
		if err := writeExportRecord(enc, rec, &summary); err != nil {
			return summary, err
		}
		summary.ArchivedRows++
	}

	return summary, nil
}

func writeExportRecord(enc *json.Encoder, rec ExportRecord, summary *ExportSummary) error {
	if rec.ActualKnown {
		summary.ActualKnownRows++
	}
	if rec.ModelFamily != nil {
		summary.LabeledRows++
	}
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("retention: export: encode record %s: %w", rec.PredictionID, err)
	}
	return nil
}

// recordFromSample maps the shared calibrationSample shape (the rollup's
// own join output) onto the wire record — used for live rows, whose
// pairing MUST be byte-identical in semantics to what a later retention
// pass would archive for the same prediction.
func recordFromSample(s calibrationSample, source string) ExportRecord {
	return ExportRecord{
		SchemaVersion:      ExportSchemaVersion,
		Source:             source,
		PredictionID:       s.predictionID,
		TurnID:             s.turnID,
		SessionID:          s.sessionID,
		PredictorID:        s.predictorID,
		PredictorVersion:   s.predictorVersion,
		PredictedAt:        s.predictedAt,
		TokenP50:           s.tokenP50,
		TokenP80:           s.tokenP80,
		TokenP90:           s.tokenP90,
		OverallRiskScore:   s.overallRiskScore,
		Confidence:         s.confidence,
		Calibrated:         s.calibrated == 1,
		Provider:           s.provider,
		ModelID:            s.modelID,
		ModelFamily:        s.modelFamily,
		Effort:             s.effort,
		ActualKnown:        s.actualKnown,
		ActualOutcome:      s.actualOutcome,
		ActualFailureClass: s.actualFailureClass,
		ActualOutcomeAt:    s.actualOutcomeAt,
	}
}

// enrichFromLiveRow adds the live-only detail (scope quantiles, component
// risk scores, context projection, reason codes) a predictions row still
// carries but the compact archived pair does not.
func enrichFromLiveRow(rec *ExportRecord, row map[string]any) {
	rec.FilesReadP50 = int64Ptr(row["files_read_p50"])
	rec.FilesReadP90 = int64Ptr(row["files_read_p90"])
	rec.FilesChangedP50 = int64Ptr(row["files_changed_p50"])
	rec.FilesChangedP90 = int64Ptr(row["files_changed_p90"])
	rec.LinesChangedP50 = int64Ptr(row["lines_changed_p50"])
	rec.LinesChangedP90 = int64Ptr(row["lines_changed_p90"])
	rec.QuotaRiskScore = float64Ptr(row["quota_risk_score"])
	rec.ContextRiskScore = float64Ptr(row["context_risk_score"])
	rec.CompletionRiskScore = float64Ptr(row["completion_risk_score"])
	rec.BlastRadiusRiskScore = float64Ptr(row["blast_radius_risk_score"])
	rec.ProjectedContextUsedP90 = float64Ptr(row["projected_context_used_p90"])

	// reason_codes_json is []domain.ReasonCode serialized at persist time
	// (predictor-09 owns the shape). A decode failure is disclosed as an
	// absent field rather than aborting the export — the numeric pair is
	// still a valid calibration sample without its explanations.
	if raw := stringOrEmpty(row["reason_codes_json"]); raw != "" {
		var codes []string
		if err := json.Unmarshal([]byte(raw), &codes); err == nil {
			rec.ReasonCodes = codes
		}
	}
}

// recordFromArchivedRow maps a calibration_samples row (SELECT * map)
// onto the wire record.
func recordFromArchivedRow(row map[string]any) ExportRecord {
	rec := ExportRecord{
		SchemaVersion:      ExportSchemaVersion,
		Source:             "archived",
		PredictionID:       stringOrEmpty(row["prediction_id"]),
		TurnID:             stringOrEmpty(row["turn_id"]),
		SessionID:          nullableColumnStr(row["session_id"]),
		PredictorID:        stringOrEmpty(row["predictor_id"]),
		PredictorVersion:   stringOrEmpty(row["predictor_version"]),
		PredictedAt:        stringOrEmpty(row["predicted_at"]),
		TokenP50:           int64Ptr(row["token_p50"]),
		TokenP80:           int64Ptr(row["token_p80"]),
		TokenP90:           int64Ptr(row["token_p90"]),
		Confidence:         stringOrEmpty(row["confidence"]),
		Provider:           nullableColumnStr(row["provider"]),
		ModelID:            nullableColumnStr(row["model_id"]),
		ModelFamily:        nullableColumnStr(row["model_family"]),
		Effort:             nullableColumnStr(row["effort"]),
		ActualOutcome:      nullableColumnStr(row["actual_outcome"]),
		ActualFailureClass: nullableColumnStr(row["actual_failure_class"]),
		ActualOutcomeAt:    nullableColumnStr(row["actual_outcome_at"]),
	}
	if v, ok := row["overall_risk_score"].(float64); ok {
		rec.OverallRiskScore = v
	}
	if v, ok := row["calibrated"].(int64); ok {
		rec.Calibrated = v == 1
	}
	if v, ok := row["actual_known"].(int64); ok {
		rec.ActualKnown = v == 1
	}
	return rec
}

// float64Ptr reads a scanned nullable REAL column.
func float64Ptr(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}
