package pause

import (
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
)

func ptrFloat64(f float64) *float64  { return &f }
func ptrInt64(i int64) *int64        { return &i }
func ptrTime(t time.Time) *time.Time { return &t }

const testSession domain.SessionID = "sess-1"

func calibratedForecast(hitProb float64, quotaAt time.Time) domain.RunwayForecast {
	return domain.RunwayForecast{
		LimitID:         "5h",
		HorizonSeconds:  600,
		HitProbability:  ptrFloat64(hitProb),
		RiskScore:       hitProb,
		Calibrated:      true,
		Confidence:      domain.ConfidenceHigh,
		QuotaObservedAt: ptrTime(quotaAt),
	}
}

// TestObserve_TwoQualifyingObservationsTriggerRequest is the required test
// (verbatim, agents/runtime.md): "two qualifying observations trigger
// request." Two calibrated samples >= threshold, >= 5s apart, fresh quota
// data each time, fire on the second.
func TestObserve_TwoQualifyingObservationsTriggerRequest(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := o.Observe(testSession, calibratedForecast(0.85, base), base)
	if first.Fire {
		t.Fatalf("first qualifying sample should only arm the debounce, got Fire=true")
	}

	secondAt := base.Add(5 * time.Second)
	second := o.Observe(testSession, calibratedForecast(0.90, secondAt), secondAt)
	if !second.Fire {
		t.Fatalf("second qualifying sample (>= 5s later) should fire, got Fire=false")
	}
	if second.Event != EventDebouncePassed {
		t.Fatalf("second.Event = %q, want %q", second.Event, EventDebouncePassed)
	}
	if second.Reason != TriggerReasonCalibrated {
		t.Fatalf("second.Reason = %q, want %q", second.Reason, TriggerReasonCalibrated)
	}
}

// TestObserve_OneSpikeDoesNotTrigger is the required test (verbatim): "one
// spike does not [trigger]." A single qualifying sample, followed by
// nothing else, must never have fired.
func TestObserve_OneSpikeDoesNotTrigger(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	decision := o.Observe(testSession, calibratedForecast(0.95, base), base)
	if decision.Fire {
		t.Fatalf("a single qualifying sample must not fire a pause trigger")
	}

	// A later NON-qualifying sample (risk has fallen below the reset
	// band) must not retroactively fire either, and should clear the arm.
	after := base.Add(1 * time.Second)
	low := domain.RunwayForecast{
		RiskScore:       0.10,
		Calibrated:      true,
		HitProbability:  ptrFloat64(0.10),
		QuotaObservedAt: ptrTime(after),
	}
	decision2 := o.Observe(testSession, low, after)
	if decision2.Fire {
		t.Fatalf("a non-qualifying, low-risk sample must not fire")
	}
}

// TestObserve_TooSoonDoesNotFireButStaysArmed proves the 5s spacing
// requirement: a second qualifying sample arriving before
// MinDebounceInterval does not fire, but does not throw away the original
// arm either — a subsequent, sufficiently-spaced sample still completes
// the debounce.
func TestObserve_TooSoonDoesNotFireButStaysArmed(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := o.Observe(testSession, calibratedForecast(0.85, base), base)
	if first.Fire {
		t.Fatalf("first sample should not fire")
	}

	tooSoon := base.Add(2 * time.Second)
	second := o.Observe(testSession, calibratedForecast(0.90, tooSoon), tooSoon)
	if second.Fire {
		t.Fatalf("sample arriving < 5s after the first must not fire")
	}

	farEnough := base.Add(6 * time.Second)
	third := o.Observe(testSession, calibratedForecast(0.90, farEnough), farEnough)
	if !third.Fire {
		t.Fatalf("a subsequent sample >= 5s after the ORIGINAL arm should still fire")
	}
}

// TestObserve_HysteresisResetRequiresFallingBelow070 proves ADD §17.6's
// exact hysteresis band: a non-qualifying sample that is still >= 0.70 (in
// between the 0.70 reset band and the 0.80 trigger threshold) does NOT
// reset an armed debounce — only a sample that actually falls below 0.70
// does. This is what makes two qualifying samples separated by in-between
// noise still correctly trigger.
func TestObserve_HysteresisResetRequiresFallingBelow070(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := o.Observe(testSession, calibratedForecast(0.85, base), base)
	if first.Fire {
		t.Fatalf("first sample should not fire")
	}

	// An in-between sample: RiskScore 0.75 is below the 0.80 trigger
	// threshold (does not itself qualify) but NOT below the 0.70 reset
	// band, so it must not clear the arm.
	mid := base.Add(2 * time.Second)
	midForecast := domain.RunwayForecast{
		RiskScore:       0.75,
		Calibrated:      true,
		HitProbability:  ptrFloat64(0.75),
		QuotaObservedAt: ptrTime(mid),
	}
	midDecision := o.Observe(testSession, midForecast, mid)
	if midDecision.Fire {
		t.Fatalf("in-between non-qualifying sample must not itself fire")
	}

	after := base.Add(6 * time.Second)
	final := o.Observe(testSession, calibratedForecast(0.90, after), after)
	if !final.Fire {
		t.Fatalf("arm must survive an in-between sample that stayed >= reset band (0.70); want fire on the later qualifying sample")
	}
}

// TestObserve_StaleQuotaSampleDoesNotQualify proves ADD §17.6's "quota
// sample age <= 30 seconds" freshness requirement: an otherwise-qualifying
// forecast (Calibrated, HitProbability above threshold) whose
// QuotaObservedAt is stale must not count as a qualifying sample at all.
func TestObserve_StaleQuotaSampleDoesNotQualify(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	stale := base.Add(-time.Minute) // 60s old, > 30s max

	forecast := calibratedForecast(0.95, stale)
	first := o.Observe(testSession, forecast, base)
	if first.Fire {
		t.Fatalf("stale-quota sample must not fire on its own")
	}

	// A second, otherwise-qualifying-and-fresh sample right after: since
	// the first never armed (it didn't qualify), this is only the FIRST
	// real arm, so it should not fire yet either.
	fresh := base.Add(6 * time.Second)
	second := o.Observe(testSession, calibratedForecast(0.95, fresh), fresh)
	if second.Fire {
		t.Fatalf("only one real qualifying sample has been seen so far (the stale one didn't count); must not fire yet")
	}
}

// TestObserve_MissingQuotaObservedAtFailsClosed proves the unknown-is-not-
// zero rule (Constitution §1.6 / CONTRACT_FREEZE.md): a nil
// QuotaObservedAt must never be treated as "fresh by default."
func TestObserve_MissingQuotaObservedAtFailsClosed(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	forecast := domain.RunwayForecast{
		RiskScore:      0.95,
		Calibrated:     true,
		HitProbability: ptrFloat64(0.95),
		// QuotaObservedAt intentionally nil.
	}
	decision := o.Observe(testSession, forecast, base)
	if decision.Fire {
		t.Fatalf("a forecast with nil QuotaObservedAt must never qualify, and must not fire")
	}
}

// TestObserve_UncalibratedForecastNeverQualifiesCalibratedPath proves the
// calibrated trigger requires Calibrated == true; an uncalibrated forecast
// (even with a high RiskScore) never satisfies the calibrated debounce —
// it can only ever fire via the independent emergency path.
func TestObserve_UncalibratedForecastNeverQualifiesCalibratedPath(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	forecast := domain.RunwayForecast{
		RiskScore:       0.99,
		Calibrated:      false,
		HitProbability:  nil,
		QuotaObservedAt: ptrTime(base),
	}
	first := o.Observe(testSession, forecast, base)
	if first.Fire {
		t.Fatalf("uncalibrated forecast must not fire the calibrated path")
	}
	second := o.Observe(testSession, forecast, base.Add(6*time.Second))
	if second.Fire {
		t.Fatalf("repeated uncalibrated forecasts must never fire the calibrated path")
	}
}

// --- Emergency (uncalibrated) trigger ---------------------------------------

// TestObserve_EmergencyUsedPercentFiresImmediately proves ADD §17.6's
// emergency trigger ("used >= 98%") fires on a SINGLE sample — no
// double-sample debounce — with a distinct reason code from the
// calibrated path (agents/runtime.md: "explicit uncalibrated emergency
// policy with a different reason code").
func TestObserve_EmergencyUsedPercentFiresImmediately(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	forecast := domain.RunwayForecast{
		RiskScore:          0.99,
		Calibrated:         false,
		CurrentUsedPercent: ptrFloat64(98.5),
	}
	decision := o.Observe(testSession, forecast, base)
	if !decision.Fire {
		t.Fatalf("emergency used-percent sample must fire immediately (no debounce)")
	}
	if decision.Event != EventEmergency {
		t.Fatalf("decision.Event = %q, want %q", decision.Event, EventEmergency)
	}
	if decision.Reason != TriggerReasonEmergency {
		t.Fatalf("decision.Reason = %q, want %q (distinct from calibrated)", decision.Reason, TriggerReasonEmergency)
	}
	if decision.Reason == TriggerReasonCalibrated {
		t.Fatalf("emergency reason must never equal the calibrated reason code")
	}
}

// TestObserve_EmergencyTimeToLimitFiresImmediately proves the "estimated
// time to limit P50 <= 60 seconds" emergency branch.
func TestObserve_EmergencyTimeToLimitFiresImmediately(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	forecast := domain.RunwayForecast{
		RiskScore:                      0.99,
		Calibrated:                     false,
		EstimatedTimeToLimitP50Seconds: ptrInt64(45),
	}
	decision := o.Observe(testSession, forecast, base)
	if !decision.Fire || decision.Event != EventEmergency {
		t.Fatalf("time-to-limit emergency sample should fire immediately: got %+v", decision)
	}
}

// TestObserve_EmergencyDoesNotConsumeCalibratedArm proves an emergency
// firing leaves any in-progress calibrated debounce arm untouched: if the
// emergency-triggered pause is later cancelled, a still-armed calibrated
// debounce (from before the emergency sample) can still complete normally.
func TestObserve_EmergencyDoesNotConsumeCalibratedArm(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := o.Observe(testSession, calibratedForecast(0.85, base), base)
	if first.Fire {
		t.Fatalf("first calibrated sample should only arm")
	}

	emergencyAt := base.Add(1 * time.Second)
	emergency := o.Observe(testSession, domain.RunwayForecast{
		RiskScore:          1.0,
		CurrentUsedPercent: ptrFloat64(99),
	}, emergencyAt)
	if !emergency.Fire || emergency.Event != EventEmergency {
		t.Fatalf("emergency sample should fire: got %+v", emergency)
	}

	// The calibrated arm should still be intact: a sample >= 5s after the
	// ORIGINAL arm (not the emergency sample) should still complete it.
	after := base.Add(6 * time.Second)
	final := o.Observe(testSession, calibratedForecast(0.90, after), after)
	if !final.Fire || final.Event != EventDebouncePassed {
		t.Fatalf("calibrated arm should survive an intervening emergency sample: got %+v", final)
	}
}

// TestObserve_PerSessionIsolation proves two sessions' debounce states
// never interfere with each other.
func TestObserve_PerSessionIsolation(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	const sessionA domain.SessionID = "session-a"
	const sessionB domain.SessionID = "session-b"

	if d := o.Observe(sessionA, calibratedForecast(0.85, base), base); d.Fire {
		t.Fatalf("session A's first sample should not fire")
	}
	// Session B's first-ever sample must not benefit from session A's arm.
	if d := o.Observe(sessionB, calibratedForecast(0.90, base.Add(6*time.Second)), base.Add(6*time.Second)); d.Fire {
		t.Fatalf("session B must have its own independent debounce state, got Fire=true on its first sample")
	}
}

// TestObserve_ResetClearsArmedState proves Reset lets a fresh debounce
// cycle start without inheriting a stale arm from a previous pause cycle
// (e.g. after cancel/resume).
func TestObserve_ResetClearsArmedState(t *testing.T) {
	o := NewObserver(NewObserveConfig())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if d := o.Observe(testSession, calibratedForecast(0.85, base), base); d.Fire {
		t.Fatalf("first sample should not fire")
	}
	o.Reset(testSession)

	// Without the reset, a sample >= 5s later would fire (proven above).
	// With the reset, this now becomes a fresh first arm and must not
	// fire.
	after := base.Add(6 * time.Second)
	decision := o.Observe(testSession, calibratedForecast(0.90, after), after)
	if decision.Fire {
		t.Fatalf("after Reset, a single sample must not fire (fresh debounce cycle)")
	}
}
