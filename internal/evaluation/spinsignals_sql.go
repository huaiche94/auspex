// spinsignals_sql.go: *SQLDataSource's implementation of the
// spinSignalSource seam (#143). Reads only what already exists — the
// ADR-052 file-operation aggregates on provider.turn.completed payloads,
// progress_nodes' completion timestamps, and the artifacts ledger — and
// decodes numbers only (the payload's aggregate keys are counts by
// construction; no content field is touched).
package evaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

var _ spinSignalSource = (*SQLDataSource)(nil)

// SpinSignals implements the spinSignalSource seam.
func (s *SQLDataSource) SpinSignals(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (SpinSignals, bool, error) {
	var out SpinSignals

	// --- repeat-rate over the last spinSignalTurns aggregate-reporting
	//     completed turns -------------------------------------------------
	rows, err := s.DB.Conn().QueryContext(ctx, `
		SELECT payload_json FROM events
		WHERE session_id = ? AND event_type = 'provider.turn.completed'
		ORDER BY occurred_at DESC, rowid DESC LIMIT ?`,
		string(sessionID), spinSignalTurns)
	if err != nil {
		return SpinSignals{}, false, fmt.Errorf("evaluation: spin signals: select recent turns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var rates []float64
	for rows.Next() {
		var body sql.NullString
		if err := rows.Scan(&body); err != nil {
			return SpinSignals{}, false, fmt.Errorf("evaluation: spin signals: scan turn payload: %w", err)
		}
		var payload struct {
			TotalFileOps *int64 `json:"total_file_ops"`
			RepeatedOps  *int64 `json:"repeated_ops"`
		}
		if body.String == "" || json.Unmarshal([]byte(body.String), &payload) != nil {
			continue // undecodable payload degrades coverage, never correctness
		}
		if payload.TotalFileOps == nil || payload.RepeatedOps == nil || *payload.TotalFileOps <= 0 {
			continue
		}
		rates = append(rates, float64(*payload.RepeatedOps)/float64(*payload.TotalFileOps))
	}
	if err := rows.Err(); err != nil {
		return SpinSignals{}, false, fmt.Errorf("evaluation: spin signals: iterating turns: %w", err)
	}
	out.ReportingTurns = len(rates)
	if len(rates) > 0 {
		last := rates[0] // DESC order: rates[0] is the most recent turn
		out.LastTurnRepeatRate = &last
		sum := 0.0
		for _, r := range rates {
			sum += r
		}
		mean := sum / float64(len(rates))
		out.MeanRepeatRate = &mean
	}

	// --- no-progress window + evidence level (task-scoped) --------------
	if taskID != nil {
		var lastAdvance sql.NullString
		err := s.DB.Conn().QueryRowContext(ctx, `
			SELECT MAX(updated_at) FROM progress_nodes
			WHERE task_id = ? AND status = 'completed'`, string(*taskID)).Scan(&lastAdvance)
		if err != nil {
			return SpinSignals{}, false, fmt.Errorf("evaluation: spin signals: last node advance: %w", err)
		}
		if lastAdvance.Valid && lastAdvance.String != "" {
			if _, err := time.Parse(time.RFC3339Nano, lastAdvance.String); err == nil {
				var since int64
				err := s.DB.Conn().QueryRowContext(ctx, `
					SELECT COUNT(*) FROM events
					WHERE session_id = ? AND event_type = 'provider.turn.completed'
					  AND occurred_at > ?`, string(sessionID), lastAdvance.String).Scan(&since)
				if err != nil {
					return SpinSignals{}, false, fmt.Errorf("evaluation: spin signals: turns since advance: %w", err)
				}
				out.TurnsSinceNodeAdvance = &since
			}
		}

		var evidence int64
		if err := s.DB.Conn().QueryRowContext(ctx, `
			SELECT COUNT(*) FROM artifacts WHERE task_id = ?`, string(*taskID)).Scan(&evidence); err != nil {
			return SpinSignals{}, false, fmt.Errorf("evaluation: spin signals: evidence count: %w", err)
		}
		out.EvidenceCount = &evidence
	}

	measurable := out.ReportingTurns > 0 || out.TurnsSinceNodeAdvance != nil || out.EvidenceCount != nil
	return out, measurable, nil
}
