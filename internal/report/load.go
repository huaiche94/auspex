// load.go: the report's read-only SELECT layer. Own, minimal SQL over
// tables other roles own (events, predictions, feature_vectors) — the
// same query-into-plain-values convention internal/retention/select.go
// established (queryRowMaps), mirrored here rather than imported because
// those helpers are deliberately package-private to retention and this
// package needs only a narrow slice of them.
package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// seriesEventTypes is the closed set the turn-attribution pass consumes:
// cumulative usage snapshots (and their managed per-turn variant) plus
// the turn boundary events that bracket them. Quota/rate-limit events are
// loaded separately (loadQuotaSection) — they need no bracketing.
var seriesEventTypes = []string{
	string(v1.EventProviderUsageObserved),
	string(v1.EventProviderTurnStarted),
	string(v1.EventProviderTurnCompleted),
	string(v1.EventProviderTurnFailed),
	string(v1.EventProviderTurnInterrupted),
}

// seriesEvent is one loaded events row, payload decoded. Pointer fields
// follow the repository-wide rule: nil = the payload did not carry that
// key (unknown is not zero).
type seriesEvent struct {
	eventType  string
	occurredAt time.Time
	provider   string
	sessionID  string
	turnID     string

	// provider.usage.observed measurements. Statusline snapshots carry
	// these session-cumulative; managed-run usage events (turnID != "")
	// carry them per-turn (internal/telemetry/claude/managedrun.go).
	totalCostUSD       *float64
	totalDurationMs    *int64
	totalAPIDurationMs *int64

	// Per-turn token accounting (provider.turn.completed transcript
	// enrichment per ADR-051/#87, or managed usage events).
	inputTokens              *int64
	outputTokens             *int64
	cacheReadInputTokens     *int64
	cacheCreationInputTokens *int64
	reasoningOutputTokens    *int64

	// Identity labels riding the events themselves (#20 Phase 0).
	modelID string
	effort  string
}

// loadSeriesEvents loads the full turn/usage series, ordered per session
// in capture order. Deliberately NOT window-filtered: cumulative-delta
// derivation needs the pre-window baseline sample and the pre-window
// turn.started of a window-straddling turn; filtering to the window
// happens after derivation (filterWindow). Volume is bounded by the
// retention engine's hot window (ADR-046, default 90 days).
func loadSeriesEvents(ctx context.Context, db *sqlite.DB) ([]seriesEvent, error) {
	args := make([]any, len(seriesEventTypes))
	for i, t := range seriesEventTypes {
		args[i] = t
	}
	rows, err := db.Conn().QueryContext(ctx,
		`SELECT event_type, occurred_at, provider, session_id, turn_id, payload_json
			FROM events
			WHERE event_type IN (`+placeholders(len(seriesEventTypes))+`)
			ORDER BY session_id, occurred_at, rowid`, args...)
	if err != nil {
		return nil, fmt.Errorf("report: select event series: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []seriesEvent
	for rows.Next() {
		var (
			eventType, occurredAt             string
			provider, sessionID, turnID, body sql.NullString
		)
		if err := rows.Scan(&eventType, &occurredAt, &provider, &sessionID, &turnID, &body); err != nil {
			return nil, fmt.Errorf("report: scan event row: %w", err)
		}
		ts, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			// A row that cannot be dated cannot be windowed or bracketed;
			// skipping it degrades coverage, never correctness.
			continue
		}
		ev := seriesEvent{
			eventType:  eventType,
			occurredAt: ts,
			provider:   provider.String,
			sessionID:  sessionID.String,
			turnID:     turnID.String,
		}
		decodeSeriesPayload(&ev, body.String)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("report: iterating event rows: %w", err)
	}
	return out, nil
}

// decodeSeriesPayload copies the whitelisted numeric/enum payload keys
// into ev. A payload that fails to decode leaves every measurement nil —
// the event's boundary/ordering role survives even when its measurements
// cannot be read (same posture as retention's observations export).
func decodeSeriesPayload(ev *seriesEvent, body string) {
	var payload map[string]any
	if body == "" || json.Unmarshal([]byte(body), &payload) != nil {
		return
	}
	ev.totalCostUSD = payloadFloat(payload, "total_cost_usd")
	ev.totalDurationMs = payloadInt(payload, "total_duration_ms")
	ev.totalAPIDurationMs = payloadInt(payload, "total_api_duration_ms")
	ev.inputTokens = payloadInt(payload, "input_tokens")
	ev.outputTokens = payloadInt(payload, "output_tokens")
	ev.cacheReadInputTokens = payloadInt(payload, "cache_read_input_tokens")
	ev.cacheCreationInputTokens = payloadInt(payload, "cache_creation_input_tokens")
	ev.reasoningOutputTokens = payloadInt(payload, "reasoning_output_tokens")
	if s := payloadString(payload, "model_id"); s != nil {
		ev.modelID = *s
	}
	if s := payloadString(payload, "effort"); s != nil {
		ev.effort = *s
	}
}

// turnLabels carries the per-turn identity/classification labels the
// model-mix and right-sizing sections join against.
type turnLabels struct {
	// predictions rows (#20 Phase 0 stamps): turn_id -> labels from the
	// latest prediction row for that turn.
	provider    map[string]string
	modelID     map[string]string
	modelFamily map[string]string
	effort      map[string]string
	// feature_vectors.features_json task_class (ADD §14.3 taxonomy;
	// "unknown" is the classifier's own honest answer and is preserved
	// as-is — a turn with NO row at all is different and stays absent).
	taskClass map[string]string
}

// loadTurnLabels reads predictions' model/effort stamps and
// feature_vectors' task classes. Both reads are label lookups: a missing
// row simply leaves the turn unlabeled.
func loadTurnLabels(ctx context.Context, db *sqlite.DB) (turnLabels, error) {
	labels := turnLabels{
		provider:    map[string]string{},
		modelID:     map[string]string{},
		modelFamily: map[string]string{},
		effort:      map[string]string{},
		taskClass:   map[string]string{},
	}

	// ORDER BY created_at so a re-evaluated turn's LATEST stamp wins
	// (later rows overwrite earlier map entries).
	rows, err := db.Conn().QueryContext(ctx,
		`SELECT turn_id, provider, model_id, model_family, effort
			FROM predictions ORDER BY created_at, id`)
	if err != nil {
		return labels, fmt.Errorf("report: select prediction labels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var turnID string
		var provider, modelID, modelFamily, effort sql.NullString
		if err := rows.Scan(&turnID, &provider, &modelID, &modelFamily, &effort); err != nil {
			return labels, fmt.Errorf("report: scan prediction labels: %w", err)
		}
		if provider.Valid && provider.String != "" {
			labels.provider[turnID] = provider.String
		}
		if modelID.Valid && modelID.String != "" {
			labels.modelID[turnID] = modelID.String
		}
		if modelFamily.Valid && modelFamily.String != "" {
			labels.modelFamily[turnID] = modelFamily.String
		}
		if effort.Valid && effort.String != "" {
			labels.effort[turnID] = effort.String
		}
	}
	if err := rows.Err(); err != nil {
		return labels, fmt.Errorf("report: iterating prediction labels: %w", err)
	}

	fvRows, err := db.Conn().QueryContext(ctx,
		`SELECT turn_id, features_json FROM feature_vectors`)
	if err != nil {
		return labels, fmt.Errorf("report: select feature vectors: %w", err)
	}
	defer func() { _ = fvRows.Close() }()
	for fvRows.Next() {
		var turnID, featuresJSON string
		if err := fvRows.Scan(&turnID, &featuresJSON); err != nil {
			return labels, fmt.Errorf("report: scan feature vector: %w", err)
		}
		var features struct {
			TaskClass string `json:"task_class"`
		}
		if json.Unmarshal([]byte(featuresJSON), &features) == nil && features.TaskClass != "" {
			labels.taskClass[turnID] = features.TaskClass
		}
	}
	if err := fvRows.Err(); err != nil {
		return labels, fmt.Errorf("report: iterating feature vectors: %w", err)
	}
	return labels, nil
}

// loadQuotaSection builds section 5 straight from SQL: rate_limit.hit
// count plus per-(provider, limit_id) closest approach over
// provider.quota.observed, window-filtered. The SQL bound uses a coarse
// whole-second superset boundary (stored RFC3339Nano strings with
// trimmed zeros are not totally ordered under string comparison at
// sub-second granularity — internal/retention/select.go's
// coarseCutoffString rationale) and the exact parsed-time rule is
// applied per row below.
func loadQuotaSection(ctx context.Context, db *sqlite.DB, from, to time.Time) (QuotaSection, error) {
	section := QuotaSection{}
	coarseFrom := from.UTC().Truncate(time.Second).Add(-2 * time.Second).Format("2006-01-02T15:04:05Z")

	rows, err := db.Conn().QueryContext(ctx,
		`SELECT event_type, occurred_at, provider, payload_json
			FROM events
			WHERE event_type IN (?, ?) AND occurred_at >= ?
			ORDER BY occurred_at, rowid`,
		string(v1.EventProviderQuotaObserved), string(v1.EventProviderRateLimitHit), coarseFrom)
	if err != nil {
		return section, fmt.Errorf("report: select quota events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type approachKey struct{ provider, limitID string }
	approaches := map[approachKey]*QuotaApproach{}
	var keys []approachKey

	for rows.Next() {
		var eventType, occurredAt string
		var provider, body sql.NullString
		if err := rows.Scan(&eventType, &occurredAt, &provider, &body); err != nil {
			return section, fmt.Errorf("report: scan quota event: %w", err)
		}
		ts, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil || ts.Before(from) || !ts.Before(to) {
			continue
		}
		if eventType == string(v1.EventProviderRateLimitHit) {
			section.RateLimitHits++
			continue
		}

		var payload map[string]any
		if body.String == "" || json.Unmarshal([]byte(body.String), &payload) != nil {
			continue
		}
		usedPercent := payloadFloat(payload, "used_percent")
		if usedPercent == nil {
			continue // a snapshot without the measurement cannot approach anything
		}
		limitID := "unknown"
		if s := payloadString(payload, "limit_id"); s != nil && *s != "" {
			limitID = *s
		}
		key := approachKey{provider: provider.String, limitID: limitID}
		app, ok := approaches[key]
		if !ok {
			app = &QuotaApproach{Provider: provider.String, LimitID: limitID}
			approaches[key] = app
			keys = append(keys, key)
		}
		app.Samples++
		if *usedPercent >= app.MaxUsedPercent {
			app.MaxUsedPercent = *usedPercent
			app.ObservedAt = occurredAt
		}
	}
	if err := rows.Err(); err != nil {
		return section, fmt.Errorf("report: iterating quota events: %w", err)
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].provider != keys[j].provider {
			return keys[i].provider < keys[j].provider
		}
		return keys[i].limitID < keys[j].limitID
	})
	for _, key := range keys {
		section.ClosestApproach = append(section.ClosestApproach, *approaches[key])
	}
	return section, nil
}

// placeholders renders "?, ?, ..." for n parameters (mirrors
// internal/retention/select.go).
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// payloadFloat reads one numeric payload key; a missing key or a
// non-number stays nil (unknown is not zero).
func payloadFloat(payload map[string]any, key string) *float64 {
	if v, ok := payload[key].(float64); ok {
		return &v
	}
	return nil
}

// payloadInt reads one integer payload key. JSON numbers decode as
// float64; the producers wrote these fields from Go ints.
func payloadInt(payload map[string]any, key string) *int64 {
	if v, ok := payload[key].(float64); ok {
		i := int64(v)
		return &i
	}
	return nil
}

// payloadString reads one enum/identifier payload key.
func payloadString(payload map[string]any, key string) *string {
	if v, ok := payload[key].(string); ok {
		return &v
	}
	return nil
}
