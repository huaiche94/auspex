// rollup.go: ADR-046 tier 2 — the aggregates distilled from expired raw
// rows before they are archived and deleted. Two rollups exist:
//
//   - usage_rollups_daily, from expired `events` rows;
//   - calibration_samples, from expired `predictions` rows joined (by
//     turn_id, against the still-present events table — the engine
//     orders the predictions class before the events class for exactly
//     this reason) to the turn's actual outcome event where one exists.
//
// Both are computed in Go from the very rows being archived — never from
// a second query that could see different data — and written inside the
// same transaction as the deletes (engine.go), per ADR-046's rollup
// discipline. Every aggregate is derived only from payload fields
// internal/telemetry/claude/normalizer.go actually writes; see
// migrations/0060_retention.sql's per-column derivation notes.
package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// --- usage_rollups_daily ------------------------------------------------

// usageKey is usage_rollups_daily's composite primary key. Provider and
// session use "" for events that carried none — a documented key
// encoding (0060_retention.sql), not an observed value.
type usageKey struct {
	day       string
	provider  string
	sessionID string
	eventType string
}

// usageAgg accumulates one usage_rollups_daily row. Max* fields are
// pointers: nil means no event in the group carried the field (unknown is
// not zero — CONTRACT_FREEZE.md).
type usageAgg struct {
	count              int64
	firstAt, lastAt    string    // original stored timestamp strings
	firstT, lastT      time.Time // their parsed forms, for correct comparison
	maxUsedPercent     *float64
	maxUsedTokens      *int64
	maxTotalCostUSD    *float64
	maxTotalDurationMS *int64
}

// buildUsageRollups aggregates expired event rows into per-key
// usageAggs. Rows already passed filterExpired, so occurred_at is known
// to parse; a row that somehow does not is skipped defensively (it was
// not counted expired either).
func buildUsageRollups(eventRows []map[string]any) map[usageKey]*usageAgg {
	out := make(map[usageKey]*usageAgg)
	for _, row := range eventRows {
		occurredAt, _ := row["occurred_at"].(string)
		t, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			continue
		}
		key := usageKey{
			day:       t.UTC().Format("2006-01-02"),
			provider:  stringOrEmpty(row["provider"]),
			sessionID: stringOrEmpty(row["session_id"]),
			eventType: stringOrEmpty(row["event_type"]),
		}
		agg := out[key]
		if agg == nil {
			agg = &usageAgg{firstAt: occurredAt, firstT: t, lastAt: occurredAt, lastT: t}
			out[key] = agg
		}
		agg.count++
		if t.Before(agg.firstT) {
			agg.firstAt, agg.firstT = occurredAt, t
		}
		if t.After(agg.lastT) {
			agg.lastAt, agg.lastT = occurredAt, t
		}

		var payload map[string]any
		if s, ok := row["payload_json"].(string); ok {
			// A payload that fails to decode contributes to the count but
			// to no aggregate — the count is a property of the row, the
			// aggregates are properties of fields we could actually read.
			_ = json.Unmarshal([]byte(s), &payload)
		}
		maxFloat(&agg.maxUsedPercent, payload, "used_percent")
		maxInt(&agg.maxUsedTokens, payload, "used_tokens")
		maxFloat(&agg.maxTotalCostUSD, payload, "total_cost_usd")
		maxInt(&agg.maxTotalDurationMS, payload, "total_duration_ms")
	}
	return out
}

// upsertUsageRollups writes aggs into usage_rollups_daily inside the
// caller's transaction (ctx must come from WithTx). The ON CONFLICT arm
// accumulates across retention runs: counts add; first/last and max
// aggregates merge with the coalesce-wrapped min/max idiom so a NULL on
// either side never poisons a known value (SQLite's scalar min/max return
// NULL if ANY argument is NULL).
func upsertUsageRollups(ctx context.Context, db *sqlite.DB, aggs map[usageKey]*usageAgg) error {
	if len(aggs) == 0 {
		return nil
	}
	keys := make([]usageKey, 0, len(aggs))
	for k := range aggs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.day != b.day {
			return a.day < b.day
		}
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		if a.sessionID != b.sessionID {
			return a.sessionID < b.sessionID
		}
		return a.eventType < b.eventType
	})

	q := sqlite.QuerierFromContext(ctx, db)
	for _, k := range keys {
		agg := aggs[k]
		_, err := q.ExecContext(ctx, `
			INSERT INTO usage_rollups_daily (
				day, provider, session_id, event_type, event_count,
				first_event_at, last_event_at,
				max_used_percent, max_used_tokens,
				max_total_cost_usd, max_total_duration_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(day, provider, session_id, event_type) DO UPDATE SET
				event_count           = event_count + excluded.event_count,
				first_event_at        = min(coalesce(first_event_at, excluded.first_event_at), coalesce(excluded.first_event_at, first_event_at)),
				last_event_at         = max(coalesce(last_event_at, excluded.last_event_at), coalesce(excluded.last_event_at, last_event_at)),
				max_used_percent      = max(coalesce(max_used_percent, excluded.max_used_percent), coalesce(excluded.max_used_percent, max_used_percent)),
				max_used_tokens       = max(coalesce(max_used_tokens, excluded.max_used_tokens), coalesce(excluded.max_used_tokens, max_used_tokens)),
				max_total_cost_usd    = max(coalesce(max_total_cost_usd, excluded.max_total_cost_usd), coalesce(excluded.max_total_cost_usd, max_total_cost_usd)),
				max_total_duration_ms = max(coalesce(max_total_duration_ms, excluded.max_total_duration_ms), coalesce(excluded.max_total_duration_ms, max_total_duration_ms))
		`,
			k.day, k.provider, k.sessionID, k.eventType, agg.count,
			agg.firstAt, agg.lastAt,
			nullableF64(agg.maxUsedPercent), nullableI64(agg.maxUsedTokens),
			nullableF64(agg.maxTotalCostUSD), nullableI64(agg.maxTotalDurationMS),
		)
		if err != nil {
			return fmt.Errorf("retention: upserting usage_rollups_daily for %s/%s: %w", k.day, k.eventType, err)
		}
	}
	return nil
}

// --- calibration_samples -------------------------------------------------

// outcomeEventTypes are the turn-outcome event types a calibration sample
// derives its actual side from — the provider.turn.* terminal set in
// pkg/protocol/v1's frozen taxonomy.
var outcomeEventTypes = []string{
	"provider.turn.completed",
	"provider.turn.failed",
	"provider.turn.interrupted",
}

// calibrationSample is one calibration_samples row awaiting insert.
// Pointer fields are NULL columns; actualKnown=false stores the honest
// cold start (0060_retention.sql's actual_known doc).
type calibrationSample struct {
	predictionID       string
	turnID             string
	sessionID          *string
	predictorID        string
	predictorVersion   string
	predictedAt        string
	tokenP50           *int64
	tokenP80           *int64
	tokenP90           *int64
	overallRiskScore   float64
	confidence         string
	calibrated         int64
	actualKnown        bool
	actualOutcome      *string
	actualFailureClass *string
	actualOutcomeAt    *string

	// Identity labels copied verbatim from the prediction row's 0046
	// columns (#11, migration 0061): the stratification keys calibration
	// needs must survive archival, or every retention pass re-opens the
	// unlabeled-history hole capture-before-model exists to prevent.
	provider    *string
	modelID     *string
	modelFamily *string
	effort      *string
}

// turnOutcome is the earliest terminal event observed for one turn.
type turnOutcome struct {
	occurredAt   time.Time
	occurredAtS  string
	eventID      string
	outcome      string // event type with the "provider.turn." prefix stripped
	failureClass *string
	sessionID    *string
}

// buildCalibrationSamples derives one sample per expired prediction row,
// joining the events table (still un-deleted at this point — the engine's
// class ordering guarantees it) by turn_id for the actual side. It reads
// the DB (outcome + session lookups) but writes nothing; inserts happen
// in the delete transaction via insertCalibrationSamples.
func buildCalibrationSamples(ctx context.Context, db *sqlite.DB, predictionRows []map[string]any) ([]calibrationSample, error) {
	if len(predictionRows) == 0 {
		return nil, nil
	}
	turnIDs := make([]any, 0, len(predictionRows))
	seen := make(map[string]bool, len(predictionRows))
	for _, row := range predictionRows {
		if id := stringOrEmpty(row["turn_id"]); id != "" && !seen[id] {
			seen[id] = true
			turnIDs = append(turnIDs, id)
		}
	}

	outcomes, err := lookupTurnOutcomes(ctx, db, turnIDs)
	if err != nil {
		return nil, err
	}
	sessions, err := lookupTurnSessions(ctx, db, turnIDs)
	if err != nil {
		return nil, err
	}

	samples := make([]calibrationSample, 0, len(predictionRows))
	for _, row := range predictionRows {
		s := calibrationSample{
			predictionID:     stringOrEmpty(row["id"]),
			turnID:           stringOrEmpty(row["turn_id"]),
			predictorID:      stringOrEmpty(row["predictor_id"]),
			predictorVersion: stringOrEmpty(row["predictor_version"]),
			predictedAt:      stringOrEmpty(row["created_at"]),
			tokenP50:         int64Ptr(row["token_p50"]),
			tokenP80:         int64Ptr(row["token_p80"]),
			tokenP90:         int64Ptr(row["token_p90"]),
			confidence:       stringOrEmpty(row["confidence"]),
		}
		if v, ok := row["overall_risk_score"].(float64); ok {
			s.overallRiskScore = v
		}
		if v, ok := row["calibrated"].(int64); ok {
			s.calibrated = v
		}
		s.provider = nullableColumnStr(row["provider"])
		s.modelID = nullableColumnStr(row["model_id"])
		s.modelFamily = nullableColumnStr(row["model_family"])
		s.effort = nullableColumnStr(row["effort"])
		if o, ok := outcomes[s.turnID]; ok {
			// Actual side: derivable — the turn has a terminal event.
			s.actualKnown = true
			outcome := o.outcome
			s.actualOutcome = &outcome
			s.actualFailureClass = o.failureClass
			at := o.occurredAtS
			s.actualOutcomeAt = &at
			s.sessionID = o.sessionID
		}
		if s.sessionID == nil {
			if sid, ok := sessions[s.turnID]; ok {
				s.sessionID = &sid
			}
		}
		samples = append(samples, s)
	}
	return samples, nil
}

// lookupTurnOutcomes finds, per turn, the earliest terminal
// provider.turn.* event (ordered by occurred_at then event_id for
// determinism when two events share a timestamp).
func lookupTurnOutcomes(ctx context.Context, db *sqlite.DB, turnIDs []any) (map[string]turnOutcome, error) {
	out := make(map[string]turnOutcome)
	for _, chunk := range chunkKeys(turnIDs) {
		query := `SELECT turn_id, event_id, event_type, occurred_at, session_id, payload_json
			FROM events
			WHERE turn_id IN (` + placeholders(len(chunk)) + `)
			  AND event_type IN (?, ?, ?)`
		args := append(append([]any{}, chunk...), anySlice(outcomeEventTypes)...)
		rows, err := queryRowMaps(ctx, db, query, args...)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			occurredAtS := stringOrEmpty(row["occurred_at"])
			t, err := time.Parse(time.RFC3339Nano, occurredAtS)
			if err != nil {
				continue // an undatable event cannot be "the earliest outcome"
			}
			o := turnOutcome{
				occurredAt:  t,
				occurredAtS: occurredAtS,
				eventID:     stringOrEmpty(row["event_id"]),
				outcome:     outcomeFromEventType(stringOrEmpty(row["event_type"])),
			}
			if sid, ok := row["session_id"].(string); ok && sid != "" {
				o.sessionID = &sid
			}
			if o.outcome == "failed" {
				var payload map[string]any
				if s, ok := row["payload_json"].(string); ok {
					_ = json.Unmarshal([]byte(s), &payload)
				}
				if fc, ok := payload["failure_class"].(string); ok && fc != "" {
					o.failureClass = &fc
				}
			}
			turnID := stringOrEmpty(row["turn_id"])
			existing, ok := out[turnID]
			if !ok || o.occurredAt.Before(existing.occurredAt) ||
				(o.occurredAt.Equal(existing.occurredAt) && o.eventID < existing.eventID) {
				out[turnID] = o
			}
		}
	}
	return out, nil
}

// lookupTurnSessions maps turn_id -> session_id from the turn's own
// provider.turn.started events (the one event type the orchestrator
// stamps with a turn_id today — hooks.go's evaluateSubmittedPrompt), for
// samples whose outcome event carried no session (or did not exist).
func lookupTurnSessions(ctx context.Context, db *sqlite.DB, turnIDs []any) (map[string]string, error) {
	out := make(map[string]string)
	for _, chunk := range chunkKeys(turnIDs) {
		query := `SELECT turn_id, session_id, event_id FROM events
			WHERE turn_id IN (` + placeholders(len(chunk)) + `)
			  AND event_type = 'provider.turn.started'
			  AND session_id IS NOT NULL
			ORDER BY event_id`
		rows, err := queryRowMaps(ctx, db, query, chunk...)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			turnID := stringOrEmpty(row["turn_id"])
			if _, ok := out[turnID]; !ok {
				out[turnID] = stringOrEmpty(row["session_id"])
			}
		}
	}
	return out, nil
}

// insertCalibrationSamples writes samples inside the caller's delete
// transaction. ON CONFLICT DO NOTHING: a prediction is only ever rolled
// up once (it is deleted in the same transaction), so a conflict can only
// mean a previous run archived this prediction and then failed before
// recording — replaying is a no-op, not an error.
func insertCalibrationSamples(ctx context.Context, db *sqlite.DB, samples []calibrationSample, runID, createdAt string) error {
	q := sqlite.QuerierFromContext(ctx, db)
	for _, s := range samples {
		actualKnown := 0
		if s.actualKnown {
			actualKnown = 1
		}
		_, err := q.ExecContext(ctx, `
			INSERT INTO calibration_samples (
				prediction_id, turn_id, session_id,
				predictor_id, predictor_version, predicted_at,
				token_p50, token_p80, token_p90,
				overall_risk_score, confidence, calibrated,
				actual_known, actual_outcome, actual_failure_class, actual_outcome_at,
				provider, model_id, model_family, effort,
				retention_run_id, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(prediction_id) DO NOTHING
		`,
			s.predictionID, s.turnID, nullableStr(s.sessionID),
			s.predictorID, s.predictorVersion, s.predictedAt,
			nullableI64(s.tokenP50), nullableI64(s.tokenP80), nullableI64(s.tokenP90),
			s.overallRiskScore, s.confidence, s.calibrated,
			actualKnown, nullableStr(s.actualOutcome), nullableStr(s.actualFailureClass), nullableStr(s.actualOutcomeAt),
			nullableStr(s.provider), nullableStr(s.modelID), nullableStr(s.modelFamily), nullableStr(s.effort),
			runID, createdAt,
		)
		if err != nil {
			return fmt.Errorf("retention: inserting calibration sample for prediction %s: %w", s.predictionID, err)
		}
	}
	return nil
}

// outcomeFromEventType strips the frozen "provider.turn." prefix, so the
// stored outcome vocabulary is completed/failed/interrupted.
func outcomeFromEventType(eventType string) string {
	const prefix = "provider.turn."
	if len(eventType) > len(prefix) && eventType[:len(prefix)] == prefix {
		return eventType[len(prefix):]
	}
	return eventType
}

// --- small value helpers --------------------------------------------------

// stringOrEmpty reads a scanned column value as string ("" for NULL).
func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

// int64Ptr reads a scanned nullable INTEGER column.
func int64Ptr(v any) *int64 {
	if i, ok := v.(int64); ok {
		return &i
	}
	return nil
}

// nullableColumnStr reads a scanned nullable TEXT column: NULL (or a row
// map that predates the column entirely) stays nil, never "".
func nullableColumnStr(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// maxFloat folds payload[key] (a JSON number) into *dst if larger (or
// first).
func maxFloat(dst **float64, payload map[string]any, key string) {
	v, ok := payload[key].(float64)
	if !ok {
		return
	}
	if *dst == nil || v > **dst {
		*dst = &v
	}
}

// maxInt folds payload[key] into *dst as int64 (JSON numbers decode as
// float64; the persisted fields are integral by construction).
func maxInt(dst **int64, payload map[string]any, key string) {
	v, ok := payload[key].(float64)
	if !ok {
		return
	}
	i := int64(v)
	if *dst == nil || i > **dst {
		*dst = &i
	}
}

// nullableF64/nullableI64/nullableStr map nil pointers to SQL NULL.
func nullableF64(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableI64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableStr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// anySlice widens []string for variadic query args.
func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
