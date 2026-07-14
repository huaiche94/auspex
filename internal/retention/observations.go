// observations.go: the FR-170/171 de-identified raw observation-series
// export (issue #11) — the companion dataset to export.go's calibration
// pairs. Where the calibration export carries prediction-vs-actual rows,
// this export carries the underlying per-session time series those
// actuals will eventually be derived FROM: statusline usage/context/quota
// snapshots plus the turn boundary events that bracket them.
//
// Why raw series and not per-turn deltas: statusline usage totals are
// SESSION-CUMULATIVE (total_cost_usd only grows), so "this turn cost
// $0.12" is a subtraction across snapshots that lag the work they
// measure — an attribution MODEL, not an observation. The Go bridges
// refuse to model (capture-before-model discipline; the same reason
// internal/telemetry/claude/normalizer.go persists totals verbatim), and
// research/ is exactly where modeling is allowed — so this export ships
// the honest inputs (cumulative samples + turn boundaries, ordered per
// session) and research/calibration/observations.py owns the delta
// derivation, clearly labeled best-effort.
//
// De-identification posture (FR-171, same as export.go): satisfied by
// CONSTRUCTION, not by scrubbing — but here the construction is a
// WHITELIST PROJECTION rather than column selection, because
// events.payload_json is an open map that can carry prompt_sha256,
// prompt_byte_length, and cwd (normalizer.go's turn.started payload).
// ObservationRecord has exactly one struct field per whitelisted
// numeric/enum payload key and nothing else; a payload key without a
// struct field CANNOT appear in the output, no matter what a future
// producer adds to the payload. Never convert this to a blacklist scrub
// — a scrubber has to enumerate what to remove and silently leaks
// whatever it forgot.
package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// ObservationsSchemaVersion stamps every exported line.
const ObservationsSchemaVersion = "auspex.observations-export.v1"

// observationEventTypes is the closed set of event types this export
// covers: the three statusline observation series plus the four turn
// boundary events (pkg/protocol/v1's frozen taxonomy). Deliberately NOT
// "every event type": each type admitted here was reviewed against the
// payload whitelist below, and an unreviewed type must stay out until it
// is (provider.rate_limit.hit, for example, carries raw_status_code —
// not whitelisted, so its event type is not either).
var observationEventTypes = []string{
	"provider.usage.observed",
	"provider.context.observed",
	"provider.quota.observed",
	"provider.turn.started",
	"provider.turn.completed",
	"provider.turn.failed",
	"provider.turn.interrupted",
}

// ObservationRecord is one JSONL line of the observations export: event
// identity plus the whitelisted payload projection. Every payload field
// is a pointer with omitempty — a snapshot that did not carry a
// measurement exports without that key (unknown is not zero,
// CONTRACT_FREEZE.md), and the Python side must treat absence as
// honestly-unknown, never as a measured zero.
type ObservationRecord struct {
	SchemaVersion string  `json:"schema_version"`
	EventType     string  `json:"event_type"`
	OccurredAt    string  `json:"occurred_at"`
	SessionID     *string `json:"session_id,omitempty"`
	TurnID        *string `json:"turn_id,omitempty"`

	// provider.usage.observed (session-cumulative statusline totals,
	// plus the #20 Phase 1 identity labels riding the same snapshot).
	TotalCostUSD       *float64 `json:"total_cost_usd,omitempty"`
	TotalDurationMs    *int64   `json:"total_duration_ms,omitempty"`
	TotalAPIDurationMs *int64   `json:"total_api_duration_ms,omitempty"`
	TotalLinesAdded    *int64   `json:"total_lines_added,omitempty"`
	TotalLinesRemoved  *int64   `json:"total_lines_removed,omitempty"`
	ModelID            *string  `json:"model_id,omitempty"`

	// provider.usage.observed, managed-run variant (issue #11: the turn-
	// stamped usage event `auspex run` persists from the provider's own
	// result line). Unlike the cumulative statusline fields above, these
	// are PER-TURN samples — total_tokens (input + output; the sum choice
	// is documented on internal/telemetry/claude's managedUsageEvent) is
	// the per-turn actual research/calibration/report.py joins against
	// token predictions on turn_id. All plain token counts: numeric,
	// identity-free, safe to whitelist.
	InputTokens              *int64 `json:"input_tokens,omitempty"`
	OutputTokens             *int64 `json:"output_tokens,omitempty"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens,omitempty"`
	TotalTokens              *int64 `json:"total_tokens,omitempty"`

	// provider.context.observed.
	UsedTokens   *int64   `json:"used_tokens,omitempty"`
	WindowTokens *int64   `json:"window_tokens,omitempty"`
	UsedPercent  *float64 `json:"used_percent,omitempty"`

	// provider.quota.observed (used_percent is shared with context above
	// — same key, same meaning: a percentage of a provider limit).
	LimitID  *string `json:"limit_id,omitempty"`
	ResetsAt *string `json:"resets_at,omitempty"`

	// provider.turn.* boundaries. Effort also appears on usage snapshots
	// (#20 Phase 1) — one field serves both.
	Effort       *string `json:"effort,omitempty"`
	FailureClass *string `json:"failure_class,omitempty"`
}

// ObservationsSummary is what ExportObservations reports about a
// completed export (the CLI's auspex.observations-export-summary.v1
// payload).
type ObservationsSummary struct {
	Rows             int
	Sessions         int // distinct non-NULL session_id values
	TurnBoundaryRows int // provider.turn.* lines (delta-window anchors)
}

// ExportObservations is Engine's method form of the free function below,
// binding the engine's own DB — the narrow seam cli.NewExportCmd consumes.
func (e *Engine) ExportObservations(ctx context.Context, w io.Writer) (ObservationsSummary, error) {
	return ExportObservations(ctx, e.DB, w)
}

// ExportObservations streams every covered events row as JSONL onto w,
// ordered by session_id then occurred_at then rowid — so each session's
// cumulative series arrives contiguous and in capture order, ready for
// research/calibration/observations.py's per-turn windowing. (occurred_at
// is compared as text: RFC3339Nano strings order correctly to the
// second, and rowid — insertion order — breaks ties; the Python side
// re-sorts by parsed timestamp anyway, see coarseCutoffString's note on
// trimmed-zero sub-second ordering.) Read-only and safe to run at any
// time; an empty export is a valid, honest dataset.
func ExportObservations(ctx context.Context, db *sqlite.DB, w io.Writer) (ObservationsSummary, error) {
	summary := ObservationsSummary{}
	enc := json.NewEncoder(w)

	args := make([]any, len(observationEventTypes))
	for i, t := range observationEventTypes {
		args[i] = t
	}
	rows, err := queryRowMaps(ctx, db,
		`SELECT event_type, occurred_at, session_id, turn_id, payload_json
			FROM events
			WHERE event_type IN (`+placeholders(len(observationEventTypes))+`)
			ORDER BY session_id, occurred_at, rowid`, args...)
	if err != nil {
		return summary, fmt.Errorf("retention: observations export: select events: %w", err)
	}

	sessions := make(map[string]bool)
	for _, row := range rows {
		rec := observationRecordFromRow(row)
		if err := enc.Encode(rec); err != nil {
			return summary, fmt.Errorf("retention: observations export: encode record: %w", err)
		}
		summary.Rows++
		if rec.SessionID != nil {
			sessions[*rec.SessionID] = true
		}
		if strings.HasPrefix(rec.EventType, "provider.turn.") {
			summary.TurnBoundaryRows++
		}
	}
	summary.Sessions = len(sessions)
	return summary, nil
}

// observationRecordFromRow builds the wire record from one events row:
// identity columns verbatim, then the payload whitelist projection. This
// function is the entire de-identification mechanism — it only ever
// COPIES named keys out of the payload, so prompt_sha256 /
// prompt_byte_length / prompt_approx_tokens / cwd / stop_hook_active /
// error_message_len / raw_* and anything a future producer invents are
// unexportable by construction.
func observationRecordFromRow(row map[string]any) ObservationRecord {
	rec := ObservationRecord{
		SchemaVersion: ObservationsSchemaVersion,
		EventType:     stringOrEmpty(row["event_type"]),
		OccurredAt:    stringOrEmpty(row["occurred_at"]),
		SessionID:     nullableColumnStr(row["session_id"]),
		TurnID:        nullableColumnStr(row["turn_id"]),
	}

	var payload map[string]any
	if s, ok := row["payload_json"].(string); ok {
		// A payload that fails to decode is disclosed as a line with no
		// measurements rather than aborting the export — the event's
		// identity/ordering role (a turn boundary, say) survives even
		// when its measurements cannot be read (same posture as
		// rollup.go's aggregate builder).
		_ = json.Unmarshal([]byte(s), &payload)
	}

	rec.TotalCostUSD = payloadFloat(payload, "total_cost_usd")
	rec.TotalDurationMs = payloadInt(payload, "total_duration_ms")
	rec.TotalAPIDurationMs = payloadInt(payload, "total_api_duration_ms")
	rec.TotalLinesAdded = payloadInt(payload, "total_lines_added")
	rec.TotalLinesRemoved = payloadInt(payload, "total_lines_removed")
	rec.ModelID = payloadString(payload, "model_id")
	rec.InputTokens = payloadInt(payload, "input_tokens")
	rec.OutputTokens = payloadInt(payload, "output_tokens")
	rec.CacheReadInputTokens = payloadInt(payload, "cache_read_input_tokens")
	rec.CacheCreationInputTokens = payloadInt(payload, "cache_creation_input_tokens")
	rec.TotalTokens = payloadInt(payload, "total_tokens")
	rec.UsedTokens = payloadInt(payload, "used_tokens")
	rec.WindowTokens = payloadInt(payload, "window_tokens")
	rec.UsedPercent = payloadFloat(payload, "used_percent")
	rec.LimitID = payloadString(payload, "limit_id")
	rec.ResetsAt = payloadString(payload, "resets_at")
	rec.Effort = payloadString(payload, "effort")
	rec.FailureClass = payloadString(payload, "failure_class")
	return rec
}

// payloadFloat reads one whitelisted numeric payload key; a missing key
// or a non-number stays nil (unknown is not zero).
func payloadFloat(payload map[string]any, key string) *float64 {
	if v, ok := payload[key].(float64); ok {
		return &v
	}
	return nil
}

// payloadInt reads one whitelisted integer payload key. JSON numbers
// decode as float64; the int64 conversion mirrors rollup.go's maxInt
// (the producers wrote these fields from Go ints, normalizer.go).
func payloadInt(payload map[string]any, key string) *int64 {
	if v, ok := payload[key].(float64); ok {
		i := int64(v)
		return &i
	}
	return nil
}

// payloadString reads one whitelisted enum/identifier payload key.
func payloadString(payload map[string]any, key string) *string {
	if v, ok := payload[key].(string); ok {
		return &v
	}
	return nil
}
