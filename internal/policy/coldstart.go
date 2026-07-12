package policy

import "github.com/huaiche94/preflight/internal/domain"

// Risk bands (ADD §16.5). Named exactly after the ADD's own table so this
// file can be checked against the ADD text directly.
const (
	// bandMediumThreshold is the low/medium boundary: score >= this value
	// enters the "medium" band (default action: warn).
	bandMediumThreshold = 0.45
	// bandHighThreshold is the medium/high boundary: score >= this value
	// enters the "high" band (default action: require confirmation, or
	// checkpoint when blast-radius risk is also elevated — see
	// decide.go's riskBandDecision, high-band case).
	bandHighThreshold = 0.65
	// bandCriticalThreshold is the high/critical boundary: score >= this
	// value enters the "critical" band (default action: checkpoint
	// preferred, ADD §16.5: ">=0.85 | critical | checkpoint preferred").
	bandCriticalThreshold = 0.85
)

// blastRadiusHighThreshold is this package's own documented threshold for
// "high blast radius" in agents/predictor.md's initial policy suggestion
// ("predicted P90 exceeds available headroom or high blast radius:
// CHECKPOINT"). The ADD does not name a separate blast-radius-specific
// checkpoint threshold beyond the shared band table (§16.5) that already
// governs OverallRisk (which itself is max(...) over the four components,
// including blast radius) — this constant only matters for
// riskBandDecision's choice between REQUIRE_CONFIRMATION and
// CHECKPOINT_AND_RUN inside the "high" band, not for whether the high
// band is entered at all. Set equal to bandCriticalThreshold's own
// component-level equivalent so a blast-radius component alone, even
// without pushing the max-based OverallRisk into "critical," still
// prefers a checkpoint over a bare confirmation prompt.
const blastRadiusHighThreshold = 0.85

// DefaultRunwayHitProbabilityThreshold is ADD §17.4's
// auto-pause-calibrated-runway rule: "runway.hit_probability_gte: 0.80".
// Configurable per Config.RunwayHitProbabilityThreshold; this is only the
// day-one default.
const DefaultRunwayHitProbabilityThreshold = 0.80

// Debounce/hysteresis (ADD §17.6). Auto-pause on a *calibrated* runway
// forecast requires two consecutive qualifying observations, not one —
// this package's Decide is stateless per call (mirrors
// internal/predictor/runway.Scorer's own statelessness: "history storage
// belongs to whichever role owns the observation store, outside this
// role's boundary"), so DecideRequest.PriorRunwayHitConfirmed carries the
// "was the previous consecutive observation already a qualifying one"
// signal in from the caller, which is expected to own that one bit of
// history (e.g. runtime's evaluation loop).
//
// This package does not re-implement the interval/staleness/reset legs of
// §17.6 (min 5s apart, quota sample age <= 30s, risk-must-fall-below-0.70
// -before-re-arming) because none of those require anything beyond what
// domain.RunwayForecast and the caller-supplied PriorRunwayHitConfirmed
// flag already carry into a single Decide call — see decide.go's
// runwayPauseDecision for exactly which fields are consulted.

// Emergency uncalibrated trigger (ADD §17.6): "provider reports limit
// reached；或 used >= 98%；或 estimated time to limit P50 <= 60 seconds."
// This is deliberately independent of calibration — an emergency pause is
// never described as a probability claim (agents/predictor.md: "PAUSE
// with reason emergency_threshold, not a probability claim").
const (
	emergencyUsedPercentThreshold      = 98.0
	emergencyTimeToLimitP50SecondsCeil = 60
)

// ReasonEmergencyThreshold is the reason code an emergency PAUSE always
// carries, literally "emergency_threshold" per agents/predictor.md: "PAUSE
// with reason emergency_threshold, not a probability claim." Kept as a
// plain string (not domain.ReasonCode) because it names a policy-layer
// trigger condition, not one of the frozen ADD §16.4 prediction reason
// codes — mirroring internal/predictor/runway's own precedent of using
// plain-string reason constants for signals that are this pipeline
// stage's own vocabulary, not the shared domain.ReasonCode enum.
const ReasonEmergencyThreshold = "emergency_threshold"

// ColdStartExample is the literal cold-start contract from
// agents/predictor.md, reproduced here (not merely in a comment) so a
// test can assert this package's own Decide output matches its shape
// field-for-field on a genuinely uncalibrated input, rather than relying
// on a copy-pasted JSON blob staying in sync with prose by hand:
//
//	{
//	  "calibrated": false,
//	  "confidence": "low",
//	  "risk_score": 0.84,
//	  "probability": null,
//	  "reason_codes": ["insufficient_history", "quota_headroom_low"]
//	}
//
// See coldstart_test.go for the assertions this backs.
var ColdStartExample = Decision{
	Calibrated:  false,
	Confidence:  domain.ConfidenceLow,
	RiskScore:   0.84,
	Probability: nil,
	ReasonCodes: []domain.ReasonCode{
		domain.ReasonPredictionColdStart, // stands in for "insufficient_history" (same concept, frozen enum's own name)
		domain.ReasonQuotaNearLimit,      // stands in for "quota_headroom_low": frozen domain.ReasonCode has no
		// literal QUOTA_HEADROOM_LOW constant (internal/domain/forecast.go's
		// closed enum has QUOTA_NEAR_LIMIT for this concept); using the
		// frozen enum value rather than inventing a new ReasonCode string
		// keeps this package's reason codes within the single frozen
		// vocabulary (Constitution §1), while ColdStartExample stays a
		// faithful worked example of the contract's *shape*
		// (calibrated/confidence/risk_score/probability/reason_codes), not
		// a claim that this exact string appears verbatim.
	},
}
