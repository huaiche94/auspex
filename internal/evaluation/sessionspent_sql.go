// sessionspent_sql.go: the session's known attributed spend (issue
// #141) — the budget envelope rule's ledger input — via an optional
// package-local seam (the spinSignalSource/AuthorizationIssuer
// precedent; the frozen app.FeatureDataSource port is untouched).
//
// Spend model, matching the report engine's two attribution paths
// (internal/report/derive.go): managed runs persist EXACT per-turn
// costs on turn-stamped usage events; native statusline sessions
// persist a session-CUMULATIVE total on turnless usage events. Within
// one session the larger of the two is the closest-to-true figure (a
// session generally produces one kind; when both exist the cumulative
// series already contains the managed turns' spend or vice versa —
// taking max never double-counts). No usage event at all = nil spend:
// unknown is not zero, and the envelope rule stays silent on unknown.
package evaluation

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// sessionSpentSource is the optional seam the pipeline reads the
// session ledger through.
type sessionSpentSource interface {
	SessionSpentUSD(ctx context.Context, sessionID domain.SessionID) (*float64, error)
}

var _ sessionSpentSource = (*SQLDataSource)(nil)

// SessionSpentUSD implements sessionSpentSource.
func (s *SQLDataSource) SessionSpentUSD(ctx context.Context, sessionID domain.SessionID) (*float64, error) {
	var perTurn, cumulative sql.NullFloat64
	err := s.DB.Conn().QueryRowContext(ctx, `
		SELECT
			SUM(CASE WHEN turn_id IS NOT NULL AND turn_id != ''
				THEN json_extract(payload_json, '$.total_cost_usd') END),
			MAX(CASE WHEN turn_id IS NULL OR turn_id = ''
				THEN json_extract(payload_json, '$.total_cost_usd') END)
		FROM events
		WHERE session_id = ? AND event_type = 'provider.usage.observed'`,
		string(sessionID)).Scan(&perTurn, &cumulative)
	if err != nil {
		return nil, fmt.Errorf("evaluation: session spent: %w", err)
	}
	if !perTurn.Valid && !cumulative.Valid {
		return nil, nil // no usage telemetry at all — unknown, not zero
	}
	spent := perTurn.Float64
	if cumulative.Valid && cumulative.Float64 > spent {
		spent = cumulative.Float64
	}
	return &spent, nil
}
