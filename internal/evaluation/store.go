package evaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// ErrNotFound mirrors internal/statecheckpoint.ErrNotFound's shape
// (itself mirroring internal/progress.ErrNotFound) so callers of this
// package's stores get the same frozen domain.Error contract regardless
// of which package's store they queried.
var ErrNotFound = &domain.Error{
	Code:      domain.ErrCodeNotFound,
	Message:   "evaluation: no matching row",
	Retryable: false,
}

// featureSetVersion is the fixed feature-set version string persisted
// with every feature_vectors/predictions row this wave (migration
// 0040/0041's feature_set_version column). No versioning scheme beyond a
// literal constant exists yet — a later wave introducing a second feature
// set would bump this.
const featureSetVersion = "v1"

// predictorID and predictorVersion are the fixed identifiers persisted in
// predictions.predictor_id/predictor_version (migration 0041) — this
// package's own name for "which predictor implementation produced this
// row," since no separate predictor-registry concept exists yet.
const (
	predictorID      = "predictor.RulePipeline"
	predictorVersion = "v1"
)

// policyVersion is the fixed identifier persisted in
// policy_decisions.policy_version (migration 0043).
const policyVersion = "policy.Decider/v1"

// featureVectorRow mirrors migrations/0040_feature_vectors.sql exactly.
type featureVectorRow struct {
	TurnID            domain.TurnID
	FeatureSetVersion string
	FeaturesJSON      string
	CreatedAt         string
}

func insertFeatureVector(ctx context.Context, db *sqlite.DB, r featureVectorRow) error {
	q := sqlite.QuerierFromContext(ctx, db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO feature_vectors (turn_id, feature_set_version, features_json, created_at)
		VALUES (?, ?, ?, ?)`,
		string(r.TurnID), r.FeatureSetVersion, r.FeaturesJSON, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("evaluation: insert feature_vectors for turn %s: %w", r.TurnID, err)
	}
	return nil
}

// predictionRow mirrors migrations/0041_predictions.sql exactly, plus
// 0045's additive projected_context_used_p90 column (ADR-043 increment 2:
// the persisted context projection the forecast card reads back).
type predictionRow struct {
	ID                domain.EvaluationID
	TurnID            domain.TurnID
	PredictorID       string
	PredictorVersion  string
	FeatureSetVersion string
	TokenP50          *int64
	TokenP80          *int64
	TokenP90          *int64
	FilesReadP50      *int64
	FilesReadP90      *int64
	FilesChangedP50   *int64
	FilesChangedP90   *int64
	LinesChangedP50   *int64
	LinesChangedP90   *int64
	// DurationP50/P90 are the Phase-1 wall-clock duration forecast in
	// nanoseconds (#62, migration 0047), nil when the scope estimator left
	// duration unknown or the row predates #62 — unknown is not zero.
	DurationP50 *int64
	DurationP90 *int64
	// TokenInput/OutputP50/P90 are the #65 Phase-1 input/output token
	// decomposition (migration 0063): the split domain.TokenForecast now
	// carries alongside the authoritative total, the input interval
	// structurally wider than the output interval (ADR-0053). nil when the
	// forecaster did not split or the row predates #65 — unknown is not zero.
	TokenInputP50        *int64
	TokenInputP90        *int64
	TokenOutputP50       *int64
	TokenOutputP90       *int64
	QuotaRiskScore       float64
	ContextRiskScore     float64
	CompletionRiskScore  float64
	BlastRadiusRiskScore float64
	OverallRiskScore     float64
	// ProjectedContextUsedP90 is domain.QuotaForecast's context
	// projection (percent, 0-100), nil when the forecaster had no usable
	// context observation (unknown is not zero — migration 0045).
	ProjectedContextUsedP90 *float64
	// Provider/ModelID/ModelFamily/Effort are the turn's identity stamp
	// (#20 Phase 0, migration 0046): which model/effort this prediction
	// was made for, resolved at EvaluateTurn time from the session's
	// latest observed identity. All nil when never observed — unknown is
	// not zero; calibration (#11) stratifies by these labels.
	Provider        *string
	ModelID         *string
	ModelFamily     *string
	Effort          *string
	Confidence      domain.Confidence
	Calibrated      bool
	ReasonCodesJSON string
	CreatedAt       string
}

func insertPrediction(ctx context.Context, db *sqlite.DB, r predictionRow) error {
	q := sqlite.QuerierFromContext(ctx, db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO predictions (
			id, turn_id, predictor_id, predictor_version, feature_set_version,
			token_p50, token_p80, token_p90,
			files_read_p50, files_read_p90,
			files_changed_p50, files_changed_p90,
			lines_changed_p50, lines_changed_p90,
			quota_risk_score, context_risk_score, completion_risk_score,
			blast_radius_risk_score, overall_risk_score,
			projected_context_used_p90,
			provider, model_id, model_family, effort,
			duration_p50, duration_p90,
			token_input_p50, token_input_p90, token_output_p50, token_output_p90,
			confidence, calibrated, reason_codes_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.TurnID), r.PredictorID, r.PredictorVersion, r.FeatureSetVersion,
		nullableInt64(r.TokenP50), nullableInt64(r.TokenP80), nullableInt64(r.TokenP90),
		nullableInt64(r.FilesReadP50), nullableInt64(r.FilesReadP90),
		nullableInt64(r.FilesChangedP50), nullableInt64(r.FilesChangedP90),
		nullableInt64(r.LinesChangedP50), nullableInt64(r.LinesChangedP90),
		r.QuotaRiskScore, r.ContextRiskScore, r.CompletionRiskScore,
		r.BlastRadiusRiskScore, r.OverallRiskScore,
		nullableFloat64(r.ProjectedContextUsedP90),
		nullableString(r.Provider), nullableString(r.ModelID), nullableString(r.ModelFamily), nullableString(r.Effort),
		nullableInt64(r.DurationP50), nullableInt64(r.DurationP90),
		nullableInt64(r.TokenInputP50), nullableInt64(r.TokenInputP90),
		nullableInt64(r.TokenOutputP50), nullableInt64(r.TokenOutputP90),
		string(r.Confidence), boolToInt(r.Calibrated), r.ReasonCodesJSON, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("evaluation: insert predictions %s: %w", r.ID, err)
	}
	return nil
}

func getPrediction(ctx context.Context, db *sqlite.DB, id domain.EvaluationID) (predictionRow, error) {
	q := sqlite.QuerierFromContext(ctx, db)
	row := q.QueryRowContext(ctx, `
		SELECT id, turn_id, predictor_id, predictor_version, feature_set_version,
		       token_p50, token_p80, token_p90,
		       files_read_p50, files_read_p90,
		       files_changed_p50, files_changed_p90,
		       lines_changed_p50, lines_changed_p90,
		       quota_risk_score, context_risk_score, completion_risk_score,
		       blast_radius_risk_score, overall_risk_score,
		       projected_context_used_p90,
		       provider, model_id, model_family, effort,
		       duration_p50, duration_p90,
		       token_input_p50, token_input_p90, token_output_p50, token_output_p90,
		       confidence, calibrated, reason_codes_json, created_at
		FROM predictions WHERE id = ?`, string(id))
	r, err := scanPrediction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return predictionRow{}, ErrNotFound
	}
	if err != nil {
		return predictionRow{}, fmt.Errorf("evaluation: get predictions %s: %w", id, err)
	}
	return r, nil
}

func scanPrediction(row interface{ Scan(dest ...any) error }) (predictionRow, error) {
	var (
		r                                               predictionRow
		id, turnID, predID, predVersion, featureVersion string
		confidence, reasonCodesJSON, createdAt          string
		calibrated                                      int64
		tokenP50, tokenP80, tokenP90                    sql.NullInt64
		filesReadP50, filesReadP90                      sql.NullInt64
		filesChangedP50, filesChangedP90                sql.NullInt64
		linesChangedP50, linesChangedP90                sql.NullInt64
		projectedContextUsedP90                         sql.NullFloat64
		provider, modelID, modelFamily, effort          sql.NullString
		durationP50, durationP90                        sql.NullInt64
		tokenInputP50, tokenInputP90                    sql.NullInt64
		tokenOutputP50, tokenOutputP90                  sql.NullInt64
	)
	if err := row.Scan(
		&id, &turnID, &predID, &predVersion, &featureVersion,
		&tokenP50, &tokenP80, &tokenP90,
		&filesReadP50, &filesReadP90,
		&filesChangedP50, &filesChangedP90,
		&linesChangedP50, &linesChangedP90,
		&r.QuotaRiskScore, &r.ContextRiskScore, &r.CompletionRiskScore,
		&r.BlastRadiusRiskScore, &r.OverallRiskScore,
		&projectedContextUsedP90,
		&provider, &modelID, &modelFamily, &effort,
		&durationP50, &durationP90,
		&tokenInputP50, &tokenInputP90, &tokenOutputP50, &tokenOutputP90,
		&confidence, &calibrated, &reasonCodesJSON, &createdAt,
	); err != nil {
		return predictionRow{}, err
	}
	r.ID = domain.EvaluationID(id)
	r.TurnID = domain.TurnID(turnID)
	r.PredictorID = predID
	r.PredictorVersion = predVersion
	r.FeatureSetVersion = featureVersion
	r.TokenP50 = nullInt64Ptr(tokenP50)
	r.TokenP80 = nullInt64Ptr(tokenP80)
	r.TokenP90 = nullInt64Ptr(tokenP90)
	r.FilesReadP50 = nullInt64Ptr(filesReadP50)
	r.FilesReadP90 = nullInt64Ptr(filesReadP90)
	r.FilesChangedP50 = nullInt64Ptr(filesChangedP50)
	r.FilesChangedP90 = nullInt64Ptr(filesChangedP90)
	r.LinesChangedP50 = nullInt64Ptr(linesChangedP50)
	r.LinesChangedP90 = nullInt64Ptr(linesChangedP90)
	r.ProjectedContextUsedP90 = nullFloat64Ptr(projectedContextUsedP90)
	r.Provider = nullStringPtr(provider)
	r.ModelID = nullStringPtr(modelID)
	r.ModelFamily = nullStringPtr(modelFamily)
	r.Effort = nullStringPtr(effort)
	r.DurationP50 = nullInt64Ptr(durationP50)
	r.DurationP90 = nullInt64Ptr(durationP90)
	r.TokenInputP50 = nullInt64Ptr(tokenInputP50)
	r.TokenInputP90 = nullInt64Ptr(tokenInputP90)
	r.TokenOutputP50 = nullInt64Ptr(tokenOutputP50)
	r.TokenOutputP90 = nullInt64Ptr(tokenOutputP90)
	r.Confidence = domain.Confidence(confidence)
	r.Calibrated = calibrated != 0
	r.ReasonCodesJSON = reasonCodesJSON
	r.CreatedAt = createdAt
	return r, nil
}

// policyDecisionRow mirrors migrations/0043_policy_decisions.sql exactly.
type policyDecisionRow struct {
	ID                   domain.DecisionID
	PredictionID         domain.EvaluationID
	RunwayForecastID     *string
	PolicyVersion        string
	Action               string
	Severity             string
	RequiresConfirmation bool
	ReasonCodesJSON      string
	DecidedAt            string
}

func insertPolicyDecision(ctx context.Context, db *sqlite.DB, r policyDecisionRow) error {
	q := sqlite.QuerierFromContext(ctx, db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO policy_decisions (
			id, prediction_id, runway_forecast_id, policy_version, action,
			severity, requires_confirmation, reason_codes_json, decided_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.PredictionID), nullableString(r.RunwayForecastID),
		r.PolicyVersion, r.Action, r.Severity, boolToInt(r.RequiresConfirmation),
		r.ReasonCodesJSON, r.DecidedAt,
	)
	if err != nil {
		return fmt.Errorf("evaluation: insert policy_decisions %s: %w", r.ID, err)
	}
	return nil
}

func getPolicyDecisionByPredictionID(ctx context.Context, db *sqlite.DB, predictionID domain.EvaluationID) (policyDecisionRow, error) {
	q := sqlite.QuerierFromContext(ctx, db)
	row := q.QueryRowContext(ctx, `
		SELECT id, prediction_id, runway_forecast_id, policy_version, action,
		       severity, requires_confirmation, reason_codes_json, decided_at
		FROM policy_decisions WHERE prediction_id = ?
		ORDER BY decided_at DESC, rowid DESC LIMIT 1`, string(predictionID))
	r, err := scanPolicyDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return policyDecisionRow{}, ErrNotFound
	}
	if err != nil {
		return policyDecisionRow{}, fmt.Errorf("evaluation: get policy_decisions for prediction %s: %w", predictionID, err)
	}
	return r, nil
}

func scanPolicyDecision(row interface{ Scan(dest ...any) error }) (policyDecisionRow, error) {
	var (
		r                                             policyDecisionRow
		id, predictionID, policyVer, action, severity string
		reasonCodesJSON, decidedAt                    string
		requiresConfirmation                          int64
		runwayForecastID                              sql.NullString
	)
	if err := row.Scan(
		&id, &predictionID, &runwayForecastID, &policyVer, &action,
		&severity, &requiresConfirmation, &reasonCodesJSON, &decidedAt,
	); err != nil {
		return policyDecisionRow{}, err
	}
	r.ID = domain.DecisionID(id)
	r.PredictionID = domain.EvaluationID(predictionID)
	if runwayForecastID.Valid {
		v := runwayForecastID.String
		r.RunwayForecastID = &v
	}
	r.PolicyVersion = policyVer
	r.Action = action
	r.Severity = severity
	r.RequiresConfirmation = requiresConfirmation != 0
	r.ReasonCodesJSON = reasonCodesJSON
	r.DecidedAt = decidedAt
	return r, nil
}

// authorizationRow mirrors migrations/0044_authorizations.sql exactly.
type authorizationRow struct {
	ID                     string
	TurnID                 domain.TurnID
	PromptHash             string
	SnapshotFingerprint    string
	Decision               string
	RepositoryCheckpointID *domain.RepositoryCheckpointID
	IssuedAt               string
	ExpiresAt              string
	ConsumedAt             *string
}

func insertAuthorization(ctx context.Context, db *sqlite.DB, r authorizationRow) error {
	q := sqlite.QuerierFromContext(ctx, db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO authorizations (
			id, turn_id, prompt_hash, snapshot_fingerprint, decision,
			repository_checkpoint_id, issued_at, expires_at, consumed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, string(r.TurnID), r.PromptHash, r.SnapshotFingerprint, r.Decision,
		nullableRepoCheckpointID(r.RepositoryCheckpointID), r.IssuedAt, r.ExpiresAt,
		nullableString(r.ConsumedAt),
	)
	if err != nil {
		return fmt.Errorf("evaluation: insert authorizations %s: %w", r.ID, err)
	}
	return nil
}

func getAuthorizationForUpdate(ctx context.Context, db *sqlite.DB, id string) (authorizationRow, error) {
	q := sqlite.QuerierFromContext(ctx, db)
	row := q.QueryRowContext(ctx, `
		SELECT id, turn_id, prompt_hash, snapshot_fingerprint, decision,
		       repository_checkpoint_id, issued_at, expires_at, consumed_at
		FROM authorizations WHERE id = ?`, id)
	r, err := scanAuthorization(row)
	if errors.Is(err, sql.ErrNoRows) {
		return authorizationRow{}, ErrNotFound
	}
	if err != nil {
		return authorizationRow{}, fmt.Errorf("evaluation: get authorizations %s: %w", id, err)
	}
	return r, nil
}

// markAuthorizationConsumed atomically consumes an authorization: it only
// updates the row when consumed_at IS STILL NULL, so two concurrent
// consumers racing on the same authorization ID can never both succeed —
// exactly one UPDATE affects a row (rowsAffected == 1), the other affects
// zero. This is the storage-layer exactly-once enforcement point
// migration 0044's own comment names ("enforced by predictor's service
// logic checking consumed_at IS NULL before consuming, inside the same
// transaction").
func markAuthorizationConsumed(ctx context.Context, db *sqlite.DB, id string, consumedAt string) (bool, error) {
	q := sqlite.QuerierFromContext(ctx, db)
	res, err := q.ExecContext(ctx, `
		UPDATE authorizations SET consumed_at = ?
		WHERE id = ? AND consumed_at IS NULL`,
		consumedAt, id,
	)
	if err != nil {
		return false, fmt.Errorf("evaluation: consume authorization %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("evaluation: consume authorization %s: rows affected: %w", id, err)
	}
	return n == 1, nil
}

func scanAuthorization(row interface{ Scan(dest ...any) error }) (authorizationRow, error) {
	var (
		r                                            authorizationRow
		id, turnID, promptHash, snapshotFP, decision string
		issuedAt, expiresAt                          string
		repoCheckpointID, consumedAt                 sql.NullString
	)
	if err := row.Scan(
		&id, &turnID, &promptHash, &snapshotFP, &decision,
		&repoCheckpointID, &issuedAt, &expiresAt, &consumedAt,
	); err != nil {
		return authorizationRow{}, err
	}
	r.ID = id
	r.TurnID = domain.TurnID(turnID)
	r.PromptHash = promptHash
	r.SnapshotFingerprint = snapshotFP
	r.Decision = decision
	if repoCheckpointID.Valid {
		c := domain.RepositoryCheckpointID(repoCheckpointID.String)
		r.RepositoryCheckpointID = &c
	}
	r.IssuedAt = issuedAt
	r.ExpiresAt = expiresAt
	if consumedAt.Valid {
		v := consumedAt.String
		r.ConsumedAt = &v
	}
	return r, nil
}

// --- helpers -----------------------------------------------------------

func nullableInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func nullableFloat64(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func nullFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullableString(v *string) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

func nullableRepoCheckpointID(v *domain.RepositoryCheckpointID) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*v), Valid: true}
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func marshalReasonCodes(codes []domain.ReasonCode) (string, error) {
	if codes == nil {
		codes = []domain.ReasonCode{}
	}
	b, err := json.Marshal(codes)
	if err != nil {
		return "", fmt.Errorf("evaluation: marshal reason codes: %w", err)
	}
	return string(b), nil
}

func unmarshalReasonCodes(s string) ([]domain.ReasonCode, error) {
	if s == "" {
		return nil, nil
	}
	var codes []domain.ReasonCode
	if err := json.Unmarshal([]byte(s), &codes); err != nil {
		return nil, fmt.Errorf("evaluation: unmarshal reason codes: %w", err)
	}
	return codes, nil
}
