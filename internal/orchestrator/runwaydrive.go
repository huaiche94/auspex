// runwaydrive.go: the hook-side driver that fills the runway_forecasts
// table (migration 0042) in native-hook mode. Lives in this package for
// OpenTurnStore/CodexStatusStore's reason (openturn.go, codexstatus.go):
// it is hook-path infrastructure with its own behavior worth testing
// against a real migrated DB in-package, not a pure DTO translation.
//
// # Why this exists (the M10 gap this closes)
//
// The independent Runway Predictor (internal/predictor/runway.Scorer) and
// the read path (internal/evaluation.SQLDataSource.RunwayForecast, which
// the policy's runway gate consumes per ADR-041) both already exist, but
// runway_forecasts was EMPTY in real use: the only producer,
// internal/pause.Service.Observe, is the managed-mode GracefulPauseService,
// which is never on the native-hook Stop path and does not itself persist
// to this table (it computes the forecast and returns it to its caller).
// Now that per-turn quota lands at Stop as provider.quota.observed events
// for both Claude (ADR-051 transcript) and Codex (rollout JSONL), this
// driver recomputes the forecast from that persisted telemetry and writes
// it, so the next turn's UserPromptSubmit evaluation reads a live runway
// signal instead of the cold-start zero forecast.
//
// It reuses the SAME store (the *sqlite.DB every other hook seam writes
// through) and the SAME stateless runway.Scorer the managed service uses —
// it does NOT duplicate the scorer, only the thin persistence the managed
// service happens not to perform. A full pause.Service is deliberately not
// wired onto the fail-open hook path: that service needs a large managed-
// mode dependency graph (checkpoint/interrupt/wake collaborators) a hook
// must never require, and native-hook mode cannot act on a pause anyway
// (an interactive turn cannot be interrupted — §8.8). This driver is
// numbers-only and fail-open by construction: a compute or write failure
// degrades to no forecast, never a hook failure.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/predictor/runway"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// RunwayDriver is HookDeps' narrow view of the runway driver: recompute
// and persist a forecast from a session's quota telemetry (DriveRunway),
// and read the latest persisted forecast back as a display hint
// (LatestRunwayHint, for the statusline surface). Both methods are
// fail-open — a nil receiver, missing telemetry, or a store error is a
// silent no-op / ok=false, never a hook failure. nil HookDeps.Runway
// disables both, exactly the pre-M10 behavior (runway_forecasts stays
// empty and the policy gate sees the cold-start zero forecast).
type RunwayDriver interface {
	DriveRunway(ctx context.Context, sessionID domain.SessionID)
	LatestRunwayHint(ctx context.Context, sessionID domain.SessionID) (RunwayHint, bool)
}

// RunwayHint is the read-back display state LatestRunwayHint resolves from
// the newest runway_forecasts row for a session — the numbers-only subset
// the statusline needs. Every field is honest about absence: a nil
// TimeToLimitP50Seconds means no burn rate was computable (cold start), not
// "zero seconds left".
type RunwayHint struct {
	// TimeToLimitP50Seconds is the uncalibrated estimated seconds until the
	// binding quota window is exhausted at the observed burn rate. nil when
	// no burn rate was computable (single sample / cold start / no burn).
	TimeToLimitP50Seconds *int64
	// RiskScore is the forecast's 0-1 risk score (never a probability while
	// Calibrated is false — Constitution §7).
	RiskScore float64
	// ProjectedExceedsWithinHorizon is true when the forecast carried the
	// runway.ReasonProjectedExceedsLimitWithinHorizon reason code: projected
	// P90 usage reaches the limit inside the horizon.
	ProjectedExceedsWithinHorizon bool
	// Calibrated mirrors the forecast's calibration bit — always false in
	// native-hook mode this phase (no durable calibrated burn-rate history),
	// carried so presenters can label the hint uncalibrated per §7.
	Calibrated bool
}

// RunwayForecastStore is the concrete RunwayDriver over a *sqlite.DB. It
// reads the session's recent provider.quota.observed events straight from
// the events table (the single source of truth every hook already writes),
// scores each limit window with the stateless runway.Scorer, combines them
// via runway.CombineWindows (§15.5's conservative max(P_i)), and writes the
// worst-window forecast to runway_forecasts.
type RunwayForecastStore struct {
	DB *sqlite.DB
	// Scorer is the shared, stateless forecaster. nil defaults to
	// runway.NewScorer() (Scorer holds no state, so a zero-value default is
	// always safe).
	Scorer *runway.Scorer
	// Horizon overrides runway.DefaultHorizon (600s, §15.5) when non-zero.
	Horizon time.Duration
	// Clock supplies the forecast's compute time (created_at) and the
	// staleness reference the scorer scores samples against. nil falls back
	// to time.Now — but production always injects the same domain.Clock
	// every other hook seam uses, so telemetry never reads the wall clock
	// directly.
	Clock domain.Clock
}

var _ RunwayDriver = (*RunwayForecastStore)(nil)

// recentQuotaScanLimit bounds the events read: a handful of limit windows
// (five_hour/seven_day for Claude, primary/secondary for Codex) across the
// last few renders is plenty to recover, per window, the current sample
// plus the most recent PRIOR sample whose usage actually differs (see
// previousSample) — the burn-rate delta the scorer needs.
const recentQuotaScanLimit = 64

// perLimitSampleCap bounds how many recent samples per window are retained
// for previousSample's scan-back: enough to see through a run of identical-
// percent statusline renders (Claude re-emits the current quota snapshot on
// every ~300ms status render, so consecutive samples are usually equal) to
// the last real change, without unbounded retention.
const perLimitSampleCap = 12

// DriveRunway recomputes and persists the session's runway forecast from
// its persisted quota telemetry. Call it AFTER the Stop hook's events are
// committed (deps.persist) so the just-observed quota sample is visible to
// the read below. Fail-open throughout: no quota telemetry, a query error,
// or a write error all degrade to no forecast written — never a hook
// failure.
func (s *RunwayForecastStore) DriveRunway(ctx context.Context, sessionID domain.SessionID) {
	if s == nil || s.DB == nil || sessionID == "" {
		return
	}
	byLimit, order := s.recentQuotaByLimit(ctx, sessionID)
	if len(order) == 0 {
		return // no quota telemetry yet — honest cold start, no row.
	}

	scorer := s.Scorer
	if scorer == nil {
		scorer = runway.NewScorer()
	}
	now := s.now()

	forecasts := make([]domain.RunwayForecast, 0, len(order))
	for _, limitID := range order {
		samples := byLimit[limitID]
		req := runway.ScoreRequest{Current: samples[0], Now: now, Horizon: s.Horizon}
		if prev := previousSample(samples); prev != nil {
			req.Previous = prev
		}
		forecasts = append(forecasts, scorer.Score(req))
	}

	combined := runway.CombineWindows(forecasts)
	s.persist(ctx, sessionID, combined, now)
}

// recentQuotaByLimit reads the session's most recent provider.quota.observed
// events and groups them by limit_id, newest first, keeping up to
// perLimitSampleCap samples per window so previousSample can see past a run
// of identical-percent renders to the last real change. It decodes the same
// payload fields the datasource's Quota reader does (limit_id/used_percent/
// resets_at, per normalizer.go's quotaEvent). order preserves first-seen
// limit order for a deterministic, stable scoring pass. Any query/scan
// trouble yields empty results (fail-open) rather than an error.
func (s *RunwayForecastStore) recentQuotaByLimit(ctx context.Context, sessionID domain.SessionID) (map[string][]domain.QuotaObservation, []string) {
	rows, err := s.DB.Conn().QueryContext(ctx, `
		SELECT event_id, occurred_at, payload_json FROM events
		WHERE session_id = ? AND event_type = ?
		ORDER BY occurred_at DESC, rowid DESC LIMIT ?`,
		string(sessionID), string(v1.EventProviderQuotaObserved), recentQuotaScanLimit,
	)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = rows.Close() }()

	byLimit := make(map[string][]domain.QuotaObservation)
	order := make([]string, 0, 4)
	for rows.Next() {
		var eventID, occurredAt, payloadJSON string
		if err := rows.Scan(&eventID, &occurredAt, &payloadJSON); err != nil {
			return nil, nil
		}
		var payload map[string]any
		if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
			continue // undecodable payloads are skipped, not fatal.
		}
		limitID, _ := payload["limit_id"].(string)
		if limitID == "" {
			continue
		}
		if len(byLimit[limitID]) >= perLimitSampleCap {
			continue // enough history to find the last real change.
		}
		observedAt, perr := time.Parse(time.RFC3339Nano, occurredAt)
		if perr != nil {
			continue
		}
		if _, seen := byLimit[limitID]; !seen {
			order = append(order, limitID)
		}
		byLimit[limitID] = append(byLimit[limitID], domain.QuotaObservation{
			ID:          eventID,
			SessionID:   sessionID,
			LimitID:     limitID,
			UsedPercent: runwayPayloadFloatPtr(payload, "used_percent"),
			ResetsAt:    runwayPayloadTimePtr(payload, "resets_at"),
			Source:      domain.SourceStatusLine,
			Confidence:  domain.ConfidenceMedium,
			ObservedAt:  observedAt,
		})
	}
	if rows.Err() != nil {
		return nil, nil
	}
	return byLimit, order
}

// previousSample picks the burn-rate "previous" sample for a window from
// its newest-first history: the most recent PRIOR sample whose used_percent
// actually differs from the current one. This is the §15.4 delta made
// robust to the statusline's identical-percent spam — Claude re-emits the
// same quota snapshot on every render, so the literally-second-newest sample
// is usually equal to the newest (a zero delta that would report a
// meaningless burn rate of 0). Measuring against the last render where the
// percentage genuinely changed recovers the real burn over the real
// interval. A window that has genuinely not moved (all samples equal, or
// only one sample) returns nil — an honest cold start / no-burn, not a
// fabricated rate. A decrease (reset/correction) is returned like any other
// change and the scorer classifies it as a negative-delta outlier.
func previousSample(samples []domain.QuotaObservation) *domain.QuotaObservation {
	if len(samples) < 2 {
		return nil
	}
	current := samples[0]
	if current.UsedPercent == nil {
		// No current percentage to delta against — hand back the immediate
		// prior sample and let the scorer degrade it to cold start.
		prev := samples[1]
		return &prev
	}
	for i := 1; i < len(samples); i++ {
		p := samples[i]
		if p.UsedPercent != nil && *p.UsedPercent != *current.UsedPercent {
			return &p
		}
	}
	return nil
}

// persist writes one runway_forecasts row for the combined forecast.
// Idempotent on the provider session id + the binding window's own current
// sample time: the row id is a deterministic digest of (sessionID, limitID,
// quotaObservedAt), so a re-entrant Stop recomputing the SAME sample writes
// the SAME id and INSERT OR IGNORE leaves the first (identical) row intact —
// no duplicate, no delete/cascade. A genuinely newer sample (a new turn, or
// a statusline snapshot in between) has a distinct sample time, hence a new
// id and a new time-series row the read's created_at ordering surfaces as
// the latest. Fail-open: a missing provider_sessions FK, a marshal error,
// or any write error degrades to no row written.
func (s *RunwayForecastStore) persist(ctx context.Context, sessionID domain.SessionID, f domain.RunwayForecast, now time.Time) {
	reasons := f.ReasonCodes
	if reasons == nil {
		reasons = []string{}
	}
	reasonJSON, err := json.Marshal(reasons)
	if err != nil {
		return
	}
	quotaObservedAt := ""
	if f.QuotaObservedAt != nil {
		quotaObservedAt = f.QuotaObservedAt.UTC().Format(time.RFC3339Nano)
	}
	id := runwayForecastID(string(sessionID), f.LimitID, quotaObservedAt)
	createdAt := now.UTC().Format(time.RFC3339Nano)

	q := sqlite.QuerierFromContext(ctx, s.DB)
	_, _ = q.ExecContext(ctx, `
		INSERT OR IGNORE INTO runway_forecasts (
			id, session_id, turn_id, task_id, limit_id, horizon_seconds,
			hit_probability, risk_score, calibrated, confidence,
			current_used_percent, burn_rate_p50, burn_rate_p90,
			estimated_time_to_limit_p50_seconds, estimated_time_to_limit_p90_seconds,
			reason_codes_json, created_at
		) VALUES (?, ?, NULL, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(sessionID), f.LimitID, f.HorizonSeconds,
		runwayNullFloat(f.HitProbability), f.RiskScore, runwayBoolToInt(f.Calibrated), string(f.Confidence),
		runwayNullFloat(f.CurrentUsedPercent), runwayNullFloat(f.BurnRateP50), runwayNullFloat(f.BurnRateP90),
		runwayNullInt64(f.EstimatedTimeToLimitP50Seconds), runwayNullInt64(f.EstimatedTimeToLimitP90Seconds),
		string(reasonJSON), createdAt,
	)
	// Any error is intentionally swallowed: a runway row is an operational
	// observation, not a state-integrity write (CONTRACT_FREEZE.md error
	// contract), and this runs on the fail-open hook path.
}

// LatestRunwayHint reads the newest runway_forecasts row for a session into
// the display subset the statusline needs. ok=false on cold start (no row),
// a query error, or an empty session id — the caller renders the line
// without a runway segment, never an error.
func (s *RunwayForecastStore) LatestRunwayHint(ctx context.Context, sessionID domain.SessionID) (RunwayHint, bool) {
	if s == nil || s.DB == nil || sessionID == "" {
		return RunwayHint{}, false
	}
	var (
		riskScore     float64
		calibratedInt int64
		estP50        sql.NullInt64
		reasonJSON    string
	)
	err := s.DB.Conn().QueryRowContext(ctx, `
		SELECT risk_score, calibrated, estimated_time_to_limit_p50_seconds, reason_codes_json
		FROM runway_forecasts
		WHERE session_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&riskScore, &calibratedInt, &estP50, &reasonJSON)
	if err != nil {
		return RunwayHint{}, false
	}

	hint := RunwayHint{RiskScore: riskScore, Calibrated: calibratedInt != 0}
	if estP50.Valid {
		v := estP50.Int64
		hint.TimeToLimitP50Seconds = &v
	}
	if reasonJSON != "" {
		var reasons []string
		if json.Unmarshal([]byte(reasonJSON), &reasons) == nil {
			for _, r := range reasons {
				if r == runway.ReasonProjectedExceedsLimitWithinHorizon {
					hint.ProjectedExceedsWithinHorizon = true
					break
				}
			}
		}
	}
	return hint, true
}

// runwayStatusETA returns the statusline's runway-ETA input from a hint:
// the uncalibrated P50 seconds-to-limit, but ONLY when the forecast
// projects exhaustion within the horizon (ProjectedExceedsWithinHorizon).
// Otherwise nil — a session with headroom shows no runway segment, keeping
// the bar quiet (the surface is a within-horizon warning, not a persistent
// countdown that would cry wolf on every idle session).
func runwayStatusETA(hint RunwayHint) *int64 {
	if !hint.ProjectedExceedsWithinHorizon {
		return nil
	}
	return hint.TimeToLimitP50Seconds
}

func (s *RunwayForecastStore) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now()
}

// runwayForecastID derives the deterministic, idempotent runway_forecasts
// primary key from its identity parts, joined with a unit-separator byte so
// distinct part boundaries never collide (mirrors telemetry's own digestKey
// discipline). The "runway-" prefix keeps ids self-describing in the table.
func runwayForecastID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte{0x1f})
		h.Write([]byte(p))
	}
	return "runway-" + hex.EncodeToString(h.Sum(nil))
}

// runwayPayloadFloatPtr / runwayPayloadTimePtr decode the nullable payload
// fields a quota event may carry. JSON round-trips numbers as float64;
// absence or a wrong type yields nil (unknown is not zero).
func runwayPayloadFloatPtr(payload map[string]any, key string) *float64 {
	if v, ok := payload[key].(float64); ok {
		return &v
	}
	return nil
}

func runwayPayloadTimePtr(payload map[string]any, key string) *time.Time {
	s, ok := payload[key].(string)
	if !ok || s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}

// runwayNullFloat / runwayNullInt64 map a nil pointer to a SQL NULL (an
// untyped nil the driver binds as NULL) and a set pointer to its value.
func runwayNullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func runwayNullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func runwayBoolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
