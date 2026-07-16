// sessionrisk.go: LatestRisk, the read-only "most recent risk scores for a
// session" query the daemon's FR-162 status surface (issue #10) consumes.
//
// This complements forecastcard.go's LatestForecastCard, which deliberately
// surfaces only the single OverallRiskScore (its presenter DTO is a per-turn
// forecast card, not a risk breakdown). FR-162 asks the VS Code companion to
// show risk WITH its component sub-scores, so this returns the four persisted
// components (quota/context/completion/blast-radius) alongside the overall
// score — the exact columns migration 0041 stores on every predictions row —
// by reusing this package's own getPrediction reader rather than duplicating
// its SQL. Session linkage is identical to LatestForecastCard's: predictions
// are turn-scoped (no session column, by design), so this joins through the
// events table's provider.turn.started rows whose turn_id the hook stamps at
// evaluation time. A session with no linkable prediction yet returns ok=false
// — cold start, not an error, matching this package's DataSource discipline.
package evaluation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// RiskSnapshot is the read-only risk view for a session's most recent
// persisted prediction: the overall 0-1 risk score plus the four component
// sub-scores, with the calibration/confidence labeling Constitution
// principle #2 requires (an uncalibrated score is never a probability). Every
// number is an estimate read straight back from predictions (migration
// 0041); nothing here is recomputed.
type RiskSnapshot struct {
	EvaluationID         domain.EvaluationID
	TurnID               domain.TurnID
	OverallRiskScore     float64
	QuotaRiskScore       float64
	ContextRiskScore     float64
	CompletionRiskScore  float64
	BlastRadiusRiskScore float64
	Calibrated           bool
	Confidence           domain.Confidence
	ReasonCodes          []domain.ReasonCode
	CreatedAt            time.Time
}

// LatestRisk returns the most recent prediction's risk scores for a session,
// or ok=false when the session has no linkable prediction yet (cold start,
// not an error). It reuses getPrediction (the same row reader ForecastCard
// uses) so the sub-scores come from exactly one SELECT projection.
func (s *SQLDataSource) LatestRisk(ctx context.Context, sessionID domain.SessionID) (RiskSnapshot, bool, error) {
	if sessionID == "" {
		return RiskSnapshot{}, false, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: LatestRisk requires a non-empty SessionID",
			Retryable: false,
		}
	}

	q := sqlite.QuerierFromContext(ctx, s.DB)
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT p.id FROM predictions p
		JOIN events e ON e.turn_id = p.turn_id
		WHERE e.session_id = ? AND e.event_type = 'provider.turn.started'
		ORDER BY p.created_at DESC, p.rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return RiskSnapshot{}, false, nil
	}
	if err != nil {
		return RiskSnapshot{}, false, fmt.Errorf("evaluation: LatestRisk: query predictions for session %s: %w", sessionID, err)
	}

	row, err := getPrediction(ctx, s.DB, domain.EvaluationID(id))
	if err != nil {
		// A row the join just found vanishing between the two queries is a
		// benign race, not an error the caller can act on — report cold start.
		if errors.Is(err, ErrNotFound) {
			return RiskSnapshot{}, false, nil
		}
		return RiskSnapshot{}, false, err
	}
	reasons, err := unmarshalReasonCodes(row.ReasonCodesJSON)
	if err != nil {
		return RiskSnapshot{}, false, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return RiskSnapshot{}, false, &domain.Error{
			Code:      domain.ErrCodeIntegrity,
			Message:   "evaluation: predictions.created_at is not a valid timestamp",
			Retryable: false,
			Details:   map[string]string{"evaluation_id": id},
		}
	}

	return RiskSnapshot{
		EvaluationID:         row.ID,
		TurnID:               row.TurnID,
		OverallRiskScore:     row.OverallRiskScore,
		QuotaRiskScore:       row.QuotaRiskScore,
		ContextRiskScore:     row.ContextRiskScore,
		CompletionRiskScore:  row.CompletionRiskScore,
		BlastRadiusRiskScore: row.BlastRadiusRiskScore,
		Calibrated:           row.Calibrated,
		Confidence:           row.Confidence,
		ReasonCodes:          reasons,
		CreatedAt:            createdAt,
	}, true, nil
}
