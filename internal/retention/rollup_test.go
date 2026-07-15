// rollup_test.go: ADR-046 tier 2 correctness — usage_rollups_daily
// aggregates match the seeded events exactly (including cross-run
// accumulation), and calibration_samples' actual_known flag is honest for
// turns with and without outcome events.
package retention

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRun_UsageRollups_MatchSeededEvents(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	day1 := oldTime.Truncate(24 * time.Hour)
	day2 := day1.Add(24 * time.Hour)

	// Day 1, quota observations: three snapshots, max used_percent 81.5.
	seedEvent(t, db, "q1", "provider.quota.observed", day1.Add(1*time.Hour), "sessA", "", `{"limit_id":"five_hour","used_percent":10.0}`)
	seedEvent(t, db, "q2", "provider.quota.observed", day1.Add(2*time.Hour), "sessA", "", `{"limit_id":"five_hour","used_percent":81.5}`)
	seedEvent(t, db, "q3", "provider.quota.observed", day1.Add(3*time.Hour), "sessA", "", `{"limit_id":"seven_day","used_percent":33.0}`)
	// Day 1, usage observation: cumulative gauges.
	seedEvent(t, db, "u1", "provider.usage.observed", day1.Add(4*time.Hour), "sessA", "", `{"total_cost_usd":1.25,"total_duration_ms":4000}`)
	// Day 2, context observations: max used_tokens 200.
	seedEvent(t, db, "c1", "provider.context.observed", day2.Add(1*time.Hour), "sessA", "", `{"used_tokens":100,"window_tokens":200000}`)
	seedEvent(t, db, "c2", "provider.context.observed", day2.Add(2*time.Hour), "sessA", "", `{"used_tokens":200,"used_percent":0.1}`)

	res, err := e.Run(ctx, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.UsageRollupRows != 3 {
		t.Fatalf("UsageRollupRows = %d, want 3 (quota day1, usage day1, context day2)", res.UsageRollupRows)
	}

	type rollup struct {
		eventCount                int64
		firstAt, lastAt           string
		maxUsedPercent            sql.NullFloat64
		maxUsedTokens, maxDurMS   sql.NullInt64
		maxTotalCostUSD           sql.NullFloat64
		day, provider, sessionID_ string
	}
	read := func(day, eventType string) rollup {
		t.Helper()
		var r rollup
		err := db.Conn().QueryRowContext(ctx, `
			SELECT day, provider, session_id, event_count, first_event_at, last_event_at,
			       max_used_percent, max_used_tokens, max_total_cost_usd, max_total_duration_ms
			FROM usage_rollups_daily WHERE day = ? AND event_type = ?`, day, eventType).
			Scan(&r.day, &r.provider, &r.sessionID_, &r.eventCount, &r.firstAt, &r.lastAt,
				&r.maxUsedPercent, &r.maxUsedTokens, &r.maxTotalCostUSD, &r.maxDurMS)
		if err != nil {
			t.Fatalf("read rollup %s/%s: %v", day, eventType, err)
		}
		return r
	}

	day1s := day1.Format("2006-01-02")
	day2s := day2.Format("2006-01-02")

	quota := read(day1s, "provider.quota.observed")
	if quota.eventCount != 3 || quota.provider != "claude" || quota.sessionID_ != "sessA" {
		t.Errorf("quota rollup identity: %+v", quota)
	}
	if quota.firstAt != ts(day1.Add(1*time.Hour)) || quota.lastAt != ts(day1.Add(3*time.Hour)) {
		t.Errorf("quota first/last = %s/%s", quota.firstAt, quota.lastAt)
	}
	// Max across BOTH windows that day — documented conflation (0060).
	if !quota.maxUsedPercent.Valid || quota.maxUsedPercent.Float64 != 81.5 {
		t.Errorf("quota max_used_percent = %+v, want 81.5", quota.maxUsedPercent)
	}
	// Fields no quota payload carries stay NULL — unknown is not zero.
	if quota.maxUsedTokens.Valid || quota.maxTotalCostUSD.Valid || quota.maxDurMS.Valid {
		t.Errorf("quota rollup fabricated aggregates: %+v", quota)
	}

	usage := read(day1s, "provider.usage.observed")
	if usage.eventCount != 1 || !usage.maxTotalCostUSD.Valid || usage.maxTotalCostUSD.Float64 != 1.25 ||
		!usage.maxDurMS.Valid || usage.maxDurMS.Int64 != 4000 {
		t.Errorf("usage rollup: %+v", usage)
	}

	contextRollup := read(day2s, "provider.context.observed")
	if contextRollup.eventCount != 2 || !contextRollup.maxUsedTokens.Valid || contextRollup.maxUsedTokens.Int64 != 200 {
		t.Errorf("context rollup: %+v", contextRollup)
	}

	// Cross-run accumulation: a later pass over the same key ADDS counts
	// and MERGES maxima instead of clobbering.
	seedEvent(t, db, "q4", "provider.quota.observed", day1.Add(5*time.Hour), "sessA", "", `{"limit_id":"five_hour","used_percent":95.0}`)
	if _, err := e.Run(ctx, RunRequest{}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	quota = read(day1s, "provider.quota.observed")
	if quota.eventCount != 4 || quota.maxUsedPercent.Float64 != 95.0 || quota.lastAt != ts(day1.Add(5*time.Hour)) {
		t.Errorf("accumulated quota rollup: %+v", quota)
	}
	if quota.firstAt != ts(day1.Add(1*time.Hour)) {
		t.Errorf("accumulated quota first_event_at = %s, want unchanged", quota.firstAt)
	}
}

func TestRun_CalibrationSamples_ActualKnownIsHonest(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	// pred-with-actual: its turn has a started event AND a failed outcome
	// event carrying the same turn_id (the correlation only synthetic /
	// future events have today — hooks.go stamps turn_id on
	// provider.turn.started only; see ADR-046 "Calibration honesty").
	seedPrediction(t, db, "pred-with-actual", "turn-known", oldTime)
	seedEvent(t, db, "ev-k-start", "provider.turn.started", oldTime, "sessK", "turn-known", `{"prompt_sha256":"x"}`)
	seedEvent(t, db, "ev-k-fail", "provider.turn.failed", oldTime.Add(time.Minute), "sessK", "turn-known",
		`{"failure_class":"provider_rate_limit","error_message_len":9}`)

	// pred-started-only: turn.started exists (so the session resolves)
	// but no outcome event — the REAL shape of today's Claude sessions.
	seedPrediction(t, db, "pred-started-only", "turn-startonly", oldTime)
	seedEvent(t, db, "ev-s-start", "provider.turn.started", oldTime, "sessK", "turn-startonly", `{"prompt_sha256":"y"}`)

	// pred-no-events: nothing persisted for the turn at all.
	seedPrediction(t, db, "pred-no-events", "turn-silent", oldTime)

	if _, err := e.Run(ctx, RunRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	type sample struct {
		actualKnown                                 int64
		outcome, failureClass, outcomeAt, sessionID sql.NullString
		tokenP50                                    sql.NullInt64
		overallRisk                                 float64
	}
	read := func(predictionID string) sample {
		t.Helper()
		var s sample
		err := db.Conn().QueryRowContext(ctx, `
			SELECT actual_known, actual_outcome, actual_failure_class, actual_outcome_at,
			       session_id, token_p50, overall_risk_score
			FROM calibration_samples WHERE prediction_id = ?`, predictionID).
			Scan(&s.actualKnown, &s.outcome, &s.failureClass, &s.outcomeAt, &s.sessionID, &s.tokenP50, &s.overallRisk)
		if err != nil {
			t.Fatalf("read sample %s: %v", predictionID, err)
		}
		return s
	}

	known := read("pred-with-actual")
	if known.actualKnown != 1 || known.outcome.String != "failed" ||
		known.failureClass.String != "provider_rate_limit" ||
		known.outcomeAt.String != ts(oldTime.Add(time.Minute)) ||
		known.sessionID.String != "sessK" {
		t.Errorf("pred-with-actual sample: %+v", known)
	}
	// Predicted side copied verbatim from the predictions row.
	if !known.tokenP50.Valid || known.tokenP50.Int64 != 1000 || known.overallRisk != 0.42 {
		t.Errorf("pred-with-actual predicted side: %+v", known)
	}

	// Honest cold start: no outcome => actual_known=0 and NULL actuals —
	// never a fabricated "completed" or a zero.
	for _, tc := range []struct {
		id          string
		wantSession bool
	}{
		{"pred-started-only", true},
		{"pred-no-events", false},
	} {
		s := read(tc.id)
		if s.actualKnown != 0 || s.outcome.Valid || s.failureClass.Valid || s.outcomeAt.Valid {
			t.Errorf("%s: actuals not honestly NULL: %+v", tc.id, s)
		}
		if s.sessionID.Valid != tc.wantSession {
			t.Errorf("%s: session_id valid=%v, want %v", tc.id, s.sessionID.Valid, tc.wantSession)
		}
	}
}

// TestRun_CalibrationSamples_DurationPair proves the #62 Phase-1 duration
// calibration rail (migration 0062): the PREDICTED duration survives
// archival copied verbatim from the prediction row, and the ACTUAL
// per-turn duration joins from a turn-attributable provider.usage.observed
// event's total_duration_ms — while a turn with no such usage event keeps
// actual_duration_ms honestly NULL (the documented join gap) yet still
// carries its predicted forecast.
func TestRun_CalibrationSamples_DurationPair(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	// pred-dur-paired: a managed-run shaped turn — the usage event carries
	// the SAME turn_id as the prediction (managedrun.go's stampManagedScope),
	// so the actual per-turn duration is joinable.
	seedPrediction(t, db, "pred-dur-paired", "turn-dur-p", oldTime)
	exec(t, db, `UPDATE predictions SET duration_p50 = ?, duration_p90 = ? WHERE id = 'pred-dur-paired'`,
		int64(45_000_000_000), int64(120_000_000_000)) // 45s / 120s in ns
	seedEvent(t, db, "ev-dp-start", "provider.turn.started", oldTime, "sessD", "turn-dur-p", `{"prompt_sha256":"z"}`)
	seedEvent(t, db, "ev-dp-usage", "provider.usage.observed", oldTime.Add(time.Minute), "sessD", "turn-dur-p",
		`{"total_cost_usd":0.4,"total_duration_ms":87000,"total_api_duration_ms":41000}`)

	// pred-dur-nogap-actual: has a PREDICTED duration but its turn has no
	// turn-attributable usage event — the honest gap. Predicted survives;
	// actual_duration_ms stays NULL (never a fabricated zero).
	seedPrediction(t, db, "pred-dur-noactual", "turn-dur-n", oldTime)
	exec(t, db, `UPDATE predictions SET duration_p50 = ?, duration_p90 = ? WHERE id = 'pred-dur-noactual'`,
		int64(15_000_000_000), int64(30_000_000_000)) // 15s / 30s in ns
	seedEvent(t, db, "ev-dn-start", "provider.turn.started", oldTime, "sessD", "turn-dur-n", `{"prompt_sha256":"w"}`)
	// A session-cumulative usage snapshot with NO turn_id must NOT join to
	// this (or any) prediction — it is turn-unattributable by construction.
	seedEvent(t, db, "ev-dn-orphan-usage", "provider.usage.observed", oldTime.Add(time.Minute), "sessD", "",
		`{"total_duration_ms":999999}`)

	if _, err := e.Run(ctx, RunRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	type durSample struct {
		p50, p90, actualMs sql.NullInt64
	}
	read := func(predictionID string) durSample {
		t.Helper()
		var s durSample
		err := db.Conn().QueryRowContext(ctx, `
			SELECT duration_p50, duration_p90, actual_duration_ms
			FROM calibration_samples WHERE prediction_id = ?`, predictionID).
			Scan(&s.p50, &s.p90, &s.actualMs)
		if err != nil {
			t.Fatalf("read duration sample %s: %v", predictionID, err)
		}
		return s
	}

	paired := read("pred-dur-paired")
	if !paired.p50.Valid || paired.p50.Int64 != 45_000_000_000 ||
		!paired.p90.Valid || paired.p90.Int64 != 120_000_000_000 {
		t.Errorf("paired predicted duration not carried verbatim: %+v", paired)
	}
	if !paired.actualMs.Valid || paired.actualMs.Int64 != 87000 {
		t.Errorf("paired actual_duration_ms = %+v, want 87000 (from turn-joined usage event)", paired.actualMs)
	}

	noActual := read("pred-dur-noactual")
	if !noActual.p50.Valid || noActual.p50.Int64 != 15_000_000_000 ||
		!noActual.p90.Valid || noActual.p90.Int64 != 30_000_000_000 {
		t.Errorf("no-actual predicted duration not carried verbatim: %+v", noActual)
	}
	if noActual.actualMs.Valid {
		t.Errorf("no-actual actual_duration_ms = %v, want NULL (honest join gap, not a session-cumulative snapshot)", noActual.actualMs.Int64)
	}
}
