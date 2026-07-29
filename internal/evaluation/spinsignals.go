// spinsignals.go: issue #143's capture-before-model slice (Cost Guard
// spin gate, ADR-0057 §5). The spin gate's three signal families —
// repeat-rate, no-progress window, verifier/evidence trend — cannot ship
// thresholds until M13 fits them from Auspex's OWN telemetry (the
// analysis's hard rule: no borrowed coefficients). This file therefore
// captures the SIGNALS ONLY, persisting them into every evaluation's
// feature_vectors.features_json snapshot, so the M13 calibration corpus
// accrues labeled per-turn spin inputs from today; no gate, no
// threshold, no action lives here.
//
// The source seam is optional and package-local (spinSignalSource, the
// AuthorizationIssuer/QuotaSource narrow-seam precedent): the frozen
// app.FeatureDataSource port is untouched, *SQLDataSource satisfies the
// seam, and any DataSource that does not is an honest absent-signal
// skip.
package evaluation

import (
	"context"

	"github.com/huaiche94/auspex/internal/domain"
)

// spinSignalTurns is how many recent aggregate-reporting turns the
// repeat-rate mean is computed over. Small on purpose: spin is a
// short-horizon condition (the loop is happening NOW), and the last-turn
// value rides alongside for the single-turn view.
const spinSignalTurns = 5

// SpinSignals is one turn's spin-gate signal snapshot (#143), persisted
// verbatim inside features_json. Every field is nil/zero-count honest:
// nil = not measurable this turn (no aggregates captured, no task
// resolved, no completed node yet), never a fabricated zero.
type SpinSignals struct {
	// LastTurnRepeatRate is repeated_ops/total_file_ops from the
	// session's most recent completed turn that carried the ADR-052
	// file-operation aggregates.
	LastTurnRepeatRate *float64 `json:"last_turn_repeat_rate,omitempty"`
	// MeanRepeatRate is the mean repeat-rate over the last
	// spinSignalTurns aggregate-reporting turns.
	MeanRepeatRate *float64 `json:"mean_repeat_rate,omitempty"`
	// ReportingTurns is how many of those recent turns actually carried
	// aggregates (the denominators above; 0 disclosure included).
	ReportingTurns int `json:"reporting_turns"`
	// TurnsSinceNodeAdvance counts the session's completed turns since
	// the task's most recent node completion — the no-progress window's
	// raw material. nil when the task has no completed node yet (a
	// session that has not completed anything is not necessarily
	// spinning — it may be starting).
	TurnsSinceNodeAdvance *int64 `json:"turns_since_node_advance,omitempty"`
	// EvidenceCount is the task's durable artifact count (the verifier
	// trend's level; M13 differences it across turns). nil when no task
	// resolved.
	EvidenceCount *int64 `json:"evidence_count,omitempty"`
}

// spinSignalSource is the optional, narrow, package-local seam through
// which the pipeline reads spin signals. ok=false means nothing was
// measurable for this session/task (cold start) — an honest skip that
// leaves the snapshot's spin field absent.
type spinSignalSource interface {
	SpinSignals(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (SpinSignals, bool, error)
}
