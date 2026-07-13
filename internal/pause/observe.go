package pause

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// TriggerReason names why an observation qualified for a pause trigger —
// this package's own closed vocabulary (mirrors Event: not persisted
// verbatim by this package, not part of any frozen contract), distinct
// from predictor's domain.ReasonCode list, which explains a RISK SCORE's
// composition, not a PAUSE TRIGGER's decision. agents/runtime.md's
// "Day-one realism" section requires the calibrated and emergency paths
// be distinguishable "with a different reason code" — these two values are
// that distinction.
type TriggerReason string

const (
	// TriggerReasonCalibrated: ADD §20.2's primary trigger — two
	// consecutive calibrated samples with HitProbability >= threshold, at
	// least MinDebounceInterval apart, each with fresh-enough quota data.
	TriggerReasonCalibrated TriggerReason = "calibrated_hit_probability"
	// TriggerReasonEmergency: ADD §17.6's emergency trigger — provider
	// reports limit reached, used percent >= 98%, or estimated time to
	// limit P50 <= 60s. Uncalibrated (does not require HitProbability or
	// the double-sample debounce at all) and always overrides.
	TriggerReasonEmergency TriggerReason = "emergency_uncalibrated"
)

// Default debounce/hysteresis parameters, ADD §17.6/§20.2.
const (
	// DefaultHitProbabilityThreshold: ADD §20.2's calibrated trigger,
	// "P(hit any 5-hour quota limit within next 10 minutes) >= 0.80".
	DefaultHitProbabilityThreshold = 0.80
	// DefaultMinDebounceInterval: ADD §17.6, "at least 5 seconds apart".
	DefaultMinDebounceInterval = 5 * time.Second
	// DefaultMaxQuotaSampleAge: ADD §17.6/§20.2, "quota sample age <= 30
	// seconds" (freshness requirement on each qualifying sample).
	DefaultMaxQuotaSampleAge = 30 * time.Second
	// DefaultResetRiskScore: ADD §17.6, "risk fall below 0.70 before
	// trigger resets" — hysteresis band between the 0.80 trigger and the
	// 0.70 reset prevents flapping right at the threshold.
	DefaultResetRiskScore = 0.70
	// DefaultEmergencyUsedPercent: ADD §17.6, "used >= 98%".
	DefaultEmergencyUsedPercent = 98.0
	// DefaultEmergencyTimeToLimitP50: ADD §17.6, "estimated time to limit
	// P50 <= 60 seconds".
	DefaultEmergencyTimeToLimitP50 = 60 * time.Second
)

// ObserveConfig bundles the debounce/hysteresis thresholds so a caller can
// override them (e.g. for tests, or a future user/session policy knob)
// without this package hardcoding magic numbers into the decision logic
// itself. Zero-value ObserveConfig{} is invalid; NewObserveConfig fills in
// every ADD-specified default.
type ObserveConfig struct {
	HitProbabilityThreshold float64
	MinDebounceInterval     time.Duration
	MaxQuotaSampleAge       time.Duration
	ResetRiskScore          float64
	EmergencyUsedPercent    float64
	EmergencyTimeToLimitP50 time.Duration
}

// NewObserveConfig returns an ObserveConfig populated with every ADD
// §17.6/§20.2 default.
func NewObserveConfig() ObserveConfig {
	return ObserveConfig{
		HitProbabilityThreshold: DefaultHitProbabilityThreshold,
		MinDebounceInterval:     DefaultMinDebounceInterval,
		MaxQuotaSampleAge:       DefaultMaxQuotaSampleAge,
		ResetRiskScore:          DefaultResetRiskScore,
		EmergencyUsedPercent:    DefaultEmergencyUsedPercent,
		EmergencyTimeToLimitP50: DefaultEmergencyTimeToLimitP50,
	}
}

// ObserveDecision is Observer.Observe's result: either a trigger fired
// (Fire true, Event/Reason set to the qualifying edge) or the observation
// was recorded but did not (yet) qualify — the latter is the normal case
// for most observations, not an error.
type ObserveDecision struct {
	Fire   bool
	Event  Event
	Reason TriggerReason
}

// observeState is one session's private debounce/hysteresis bookkeeping.
// Not exported: callers only ever see it through Observer's map, keyed by
// domain.SessionID, so concurrent sessions never share state.
type observeState struct {
	// armed is true from the first qualifying calibrated sample until
	// either a second qualifying sample fires the trigger, or a
	// non-qualifying sample resets it (see resetsArm below) — this is the
	// debounce half of the required test "two qualifying observations
	// trigger request; one spike does not".
	armed         bool
	firstSampleAt time.Time
}

// resetsArm reports whether obs should clear an in-progress debounce arm
// rather than let it accumulate toward a second qualifying sample. Per ADD
// §17.6 "risk fall below 0.70 before trigger resets": the arm is only
// cleared once risk actually falls back under the reset band — a
// non-qualifying sample that is still elevated (between 0.70 and the
// trigger threshold) does NOT reset an armed state, so two qualifying
// samples separated by an in-between reading still correctly trigger.
// This is what makes "one spike does not [trigger]" true without also
// making "two qualifying samples with noise between them" incorrectly
// fail to trigger.
func resetsArm(cfg ObserveConfig, forecast domain.RunwayForecast) bool {
	return forecast.RiskScore < cfg.ResetRiskScore
}

// qualifiesCalibrated reports whether forecast is, on its own, a
// qualifying calibrated sample: calibrated, HitProbability present and >=
// threshold, and quota data fresh enough (ADD §17.6/§20.2). observedAt is
// the wall-clock time this sample was received (Observer's clock), used
// against forecast.QuotaObservedAt for the freshness check — a nil
// QuotaObservedAt fails closed (never assumed fresh), per Constitution
// §1.6's "unknown is not zero".
func qualifiesCalibrated(cfg ObserveConfig, forecast domain.RunwayForecast, observedAt time.Time) bool {
	if !forecast.Calibrated {
		return false
	}
	if forecast.HitProbability == nil || *forecast.HitProbability < cfg.HitProbabilityThreshold {
		return false
	}
	if forecast.QuotaObservedAt == nil {
		return false
	}
	age := observedAt.Sub(*forecast.QuotaObservedAt)
	if age < 0 {
		age = -age
	}
	return age <= cfg.MaxQuotaSampleAge
}

// qualifiesEmergency reports whether forecast independently qualifies for
// ADD §17.6's uncalibrated emergency trigger: provider-reported limit
// reached (modeled as CurrentUsedPercent >= 100, the only way this
// package can observe "limit reached" from a RunwayForecast alone), used
// percent >= EmergencyUsedPercent, or estimated P50 time-to-limit <=
// EmergencyTimeToLimitP50. Any one of the three is sufficient — this is
// deliberately independent of Calibrated/HitProbability: an uncalibrated
// forecast (Calibrated: false) can still trigger emergency, per the task
// brief's "explicit uncalibrated emergency policy with a different reason
// code".
func qualifiesEmergency(cfg ObserveConfig, forecast domain.RunwayForecast) bool {
	if forecast.CurrentUsedPercent != nil && *forecast.CurrentUsedPercent >= cfg.EmergencyUsedPercent {
		return true
	}
	if forecast.EstimatedTimeToLimitP50Seconds != nil {
		limit := int64(cfg.EmergencyTimeToLimitP50 / time.Second)
		if *forecast.EstimatedTimeToLimitP50Seconds <= limit {
			return true
		}
	}
	return false
}

// Observer holds per-session debounce/hysteresis state across repeated
// Observe calls (agents/runtime.md Part A deliverable 2). It is the
// runway-forecast-driven half of GracefulPauseService.Observe
// (internal/app/ports.go) — the caller is expected to construct one
// Observer per long-lived process (or per test) and call Observe once per
// incoming domain.RunwayForecast sample, in time order, for a given
// session.
//
// Observer is NOT safe for concurrent use on the SAME domain.SessionID
// from multiple goroutines without external serialization (matching this
// package's other pre-persistence pure-decision types); it has no
// internal locking because ordering, not concurrency, is the correctness
// property Observe needs (two samples "at least 5 seconds apart" is
// meaningless without a strict arrival order).
type Observer struct {
	cfg    ObserveConfig
	states map[domain.SessionID]*observeState
}

// NewObserver constructs an Observer with cfg. Pass NewObserveConfig() for
// ADD-default thresholds.
func NewObserver(cfg ObserveConfig) *Observer {
	return &Observer{cfg: cfg, states: make(map[domain.SessionID]*observeState)}
}

// Observe records forecast (observed at observedAt) for sessionID and
// returns whether it fires a pause trigger.
//
// Emergency (ADD §17.6) is checked FIRST and unconditionally: it "can skip
// the double-sample" by design, so a qualifying emergency sample fires
// immediately regardless of any in-progress calibrated debounce state (and
// does not consume/clear that state either — a caller that later cancels
// the emergency-triggered pause still has its calibrated arm exactly as
// it was).
//
// Otherwise, the calibrated path applies ADD §17.6's exact debounce/
// hysteresis rule:
//   - a lone qualifying sample only arms the debounce (Fire: false);
//   - a second qualifying sample, arriving >= MinDebounceInterval after the
//     first armed sample, fires (Fire: true, TriggerReasonCalibrated);
//   - a non-qualifying sample whose RiskScore has fallen below
//     ResetRiskScore clears the arm (hysteresis: hovering just under the
//     trigger threshold does not endlessly re-arm on noise);
//   - a second qualifying sample arriving TOO SOON (< MinDebounceInterval
//     after the first) does not fire — the arm's firstSampleAt is left
//     unchanged so a later, sufficiently-spaced qualifying sample can
//     still complete the debounce.
func (o *Observer) Observe(sessionID domain.SessionID, forecast domain.RunwayForecast, observedAt time.Time) ObserveDecision {
	if qualifiesEmergency(o.cfg, forecast) {
		return ObserveDecision{Fire: true, Event: EventEmergency, Reason: TriggerReasonEmergency}
	}

	state, ok := o.states[sessionID]
	if !ok {
		state = &observeState{}
		o.states[sessionID] = state
	}

	if !qualifiesCalibrated(o.cfg, forecast, observedAt) {
		if resetsArm(o.cfg, forecast) {
			state.armed = false
		}
		return ObserveDecision{}
	}

	if !state.armed {
		state.armed = true
		state.firstSampleAt = observedAt
		return ObserveDecision{}
	}

	if observedAt.Sub(state.firstSampleAt) < o.cfg.MinDebounceInterval {
		// Second qualifying sample arrived too soon; keep waiting on the
		// original arm rather than restarting the clock — a fast-firing
		// noisy predictor should not be able to indefinitely postpone a
		// real trigger by resetting firstSampleAt on every sample.
		return ObserveDecision{}
	}

	// Debounce satisfied: two qualifying samples, sufficiently apart.
	// Clear the arm so a subsequent independent pause cycle starts fresh.
	state.armed = false
	return ObserveDecision{Fire: true, Event: EventDebouncePassed, Reason: TriggerReasonCalibrated}
}

// Reset clears sessionID's debounce state entirely (e.g. after a pause
// this session's arm led to has been cancelled/resumed, so a fresh
// calibrated debounce cycle should not inherit a stale armed sample from
// before the previous pause).
func (o *Observer) Reset(sessionID domain.SessionID) {
	delete(o.states, sessionID)
}
