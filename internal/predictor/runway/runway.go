package runway

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// DefaultHorizon is the ADD §15.5 default runway horizon: "H = 600 seconds
// by default".
const DefaultHorizon = 600 * time.Second

// Runway-specific reason codes. RunwayForecast.ReasonCodes is frozen as
// []string (internal/domain/usage.go), not []domain.ReasonCode — unlike
// ScopeEstimate, RunwayForecast predates ADR-041's typed ReasonCode
// introduction and ADR-041 did not change this field's type, so this
// package uses its own plain-string constants rather than domain.ReasonCode
// values, to match the frozen shape exactly.
const (
	ReasonInsufficientSamples                = "insufficient_runway_samples"
	ReasonColdStart                          = "prediction_cold_start"
	ReasonCurrentUsageUnknown                = "quota_current_usage_unknown"
	ReasonNegativeDeltaOutlier               = "negative_delta_outlier"
	ReasonIntervalTooShort                   = "interval_too_short"
	ReasonStaleSample                        = "stale_quota_sample"
	ReasonBurnRateAnomaly                    = "burn_rate_anomaly"
	ReasonCriticalUsage                      = "quota_current_usage_critical"
	ReasonProjectedExceedsLimitWithinHorizon = "quota_projected_exceeds_limit_within_horizon"
	ReasonProjectedNearLimit                 = "quota_projected_near_limit"
	ReasonHeadroomAvailable                  = "quota_headroom_available"
)

// minInterval is the ADD §15.4 outlier rule: "interval < 2 sec => 不計入
// rate" (an interval shorter than this is not counted toward the burn
// rate).
const minInterval = 2 * time.Second

// staleAfter is the age past which a quota sample's confidence is lowered
// (ADD §15.4: "stale sample > configured age => lower confidence"). No
// exact duration is specified in the ADD; 5 minutes is a conservative
// day-one default relative to the 10-minute horizon this package scores
// against (a sample stale relative to half the horizon is not trustworthy
// for a within-horizon exhaustion call).
const staleAfter = 5 * time.Minute

// sanityCapPercentPerMinute is the ADD §15.4 "rate > provider-specific
// sanity cap => mark anomaly" rule. No provider-specific cap is available
// yet (no live telemetry wired up this wave), so this package uses a
// single conservative default: more than 50 percentage-points of quota
// burned in one minute is treated as an anomaly rather than a real
// sustained burn rate.
const sanityCapPercentPerMinute = 50.0

// Sample is one burn-rate input observation: a QuotaObservation plus the
// wall-clock time it covers relative to the previous sample. Scorer
// consumes the two most recent samples for a given LimitID to compute an
// instantaneous burn rate (ADD §15.4).
type Sample = domain.QuotaObservation

// ScoreRequest bundles the current observation, optionally the previous
// observation for the same limit window (for burn-rate delta), and the
// horizon to score against.
type ScoreRequest struct {
	// Current is the latest QuotaObservation for one limit window. Required.
	Current domain.QuotaObservation
	// Previous is the prior observation for the same LimitID, if any. Nil
	// means no burn-rate history is available yet (cold start).
	Previous *domain.QuotaObservation
	// Now is the evaluation time. Defaults to Current.ObservedAt when zero.
	Now time.Time
	// Horizon is the runway horizon to score against. Defaults to
	// DefaultHorizon (600s) when zero.
	Horizon time.Duration
}

// Scorer computes ten-minute (or caller-configured horizon) runway
// forecasts per ADD §15.4-15.5, using the uncalibrated deterministic
// fallback (§15.7) since no durable calibrated burn-rate history exists
// yet this wave (§15.6's calibration gate is never met by construction).
type Scorer struct{}

// NewScorer constructs a Scorer. It is stateless — all history must be
// passed in via ScoreRequest.Previous by the caller (which owns durable
// storage of recent observations; predictor's boundary excludes storage).
func NewScorer() *Scorer {
	return &Scorer{}
}

// Score computes a domain.RunwayForecast for req. It never returns an
// error: every input, including a nil Previous, missing UsedPercent, or a
// degenerate/negative interval, degrades to a lower-confidence,
// higher-uncertainty forecast rather than failing (fail-open discipline
// for an operational observation, per CONTRACT_FREEZE.md's error
// contract — a runway score is exactly this kind of observation, not a
// state-integrity operation).
func (s *Scorer) Score(req ScoreRequest) domain.RunwayForecast {
	horizon := req.Horizon
	if horizon <= 0 {
		horizon = DefaultHorizon
	}
	now := req.Now
	if now.IsZero() {
		now = req.Current.ObservedAt
	}

	forecast := domain.RunwayForecast{
		LimitID:         req.Current.LimitID,
		HorizonSeconds:  int64(horizon.Seconds()),
		Calibrated:      false, // never true this wave — see doc.go cold-start contract
		HitProbability:  nil,   // only meaningful once Calibrated; never populated this wave
		QuotaObservedAt: observedAtPtr(req.Current),
	}

	var reasons []string

	currentUsed := req.Current.UsedPercent
	if currentUsed == nil {
		forecast.RiskScore = 0
		forecast.Confidence = domain.ConfidenceUnavailable
		forecast.ReasonCodes = append(forecast.ReasonCodes, ReasonCurrentUsageUnknown, ReasonColdStart)
		return forecast
	}
	forecast.CurrentUsedPercent = currentUsed

	// Reached is an explicit provider signal that the limit is already at
	// 100%, independent of any percentage math below.
	if req.Current.Reached {
		forecast.RiskScore = 1.0
		forecast.Confidence = domain.ConfidenceHigh
		forecast.ReasonCodes = []string{ReasonCriticalUsage}
		zero := int64(0)
		forecast.EstimatedTimeToLimitP50Seconds = &zero
		forecast.EstimatedTimeToLimitP90Seconds = &zero
		return forecast
	}

	burnP50, burnP90, sampleCount, burnReasons := estimateBurnRate(req, now)
	reasons = append(reasons, burnReasons...)
	forecast.SampleCount = int64(sampleCount)
	if burnP50 != nil {
		forecast.BurnRateP50 = burnP50
	}
	if burnP90 != nil {
		forecast.BurnRateP90 = burnP90
	}

	remaining := 100.0 - *currentUsed
	if remaining < 0 {
		remaining = 0
	}

	var projectedUsedP90 *float64
	if burnP90 != nil && *burnP90 > 0 {
		etaP90Seconds := (remaining / *burnP90) * 60.0
		if etaP90Seconds < 0 {
			etaP90Seconds = 0
		}
		v := int64(etaP90Seconds)
		forecast.EstimatedTimeToLimitP90Seconds = &v

		projected := *currentUsed + (*burnP90)*(horizon.Minutes())
		if projected > 100 {
			projected = 100
		}
		projectedUsedP90 = &projected
	}
	if burnP50 != nil && *burnP50 > 0 {
		etaP50Seconds := (remaining / *burnP50) * 60.0
		if etaP50Seconds < 0 {
			etaP50Seconds = 0
		}
		v := int64(etaP50Seconds)
		forecast.EstimatedTimeToLimitP50Seconds = &v
	}

	// Reset-awareness (ADD §15.8): a reset landing inside the horizon
	// means the window will not actually run out, regardless of burn
	// rate — treat as headroom-available rather than scoring a
	// now-irrelevant exhaustion time.
	resetsWithinHorizon := req.Current.ResetsAt != nil && !req.Current.ResetsAt.After(now.Add(horizon)) && req.Current.ResetsAt.After(now)

	riskScore, riskReasons := uncalibratedFallbackScore(*currentUsed, projectedUsedP90, resetsWithinHorizon)
	forecast.RiskScore = riskScore
	reasons = append(reasons, riskReasons...)

	forecast.Confidence = confidenceFor(sampleCount, req.Current, now)
	forecast.ReasonCodes = dedupe(reasons)

	return forecast
}

// estimateBurnRate implements the ADD §15.4 instantaneous-rate model
// (Δused_percent / Δminutes) between req.Previous and req.Current, with
// the ADD-specified outlier rules: negative delta => treat as a
// reset/correction (drop the sample, do not report a negative rate);
// interval < 2s => not counted; sample staler than staleAfter => lower
// confidence (reflected in the caller's Confidence, not here); rate above
// the sanity cap => marked anomalous and dropped rather than propagated.
//
// Returns (p50, p90, sampleCount, reasonCodes). p50/p90 are nil when no
// usable rate could be computed (cold start: no Previous, or the only
// candidate sample was rejected as an outlier).
func estimateBurnRate(req ScoreRequest, now time.Time) (*float64, *float64, int, []string) {
	if req.Previous == nil {
		return nil, nil, 0, []string{ReasonInsufficientSamples, ReasonColdStart}
	}
	prev := *req.Previous
	if prev.UsedPercent == nil || req.Current.UsedPercent == nil {
		return nil, nil, 0, []string{ReasonInsufficientSamples, ReasonColdStart}
	}

	interval := req.Current.ObservedAt.Sub(prev.ObservedAt)
	if interval < minInterval {
		return nil, nil, 0, []string{ReasonIntervalTooShort, ReasonInsufficientSamples}
	}

	delta := *req.Current.UsedPercent - *prev.UsedPercent
	if delta < 0 {
		// Reset or correction, not a real burn rate (ADD §15.4).
		return nil, nil, 0, []string{ReasonNegativeDeltaOutlier, ReasonInsufficientSamples}
	}

	ratePerMinute := delta / interval.Minutes()
	if ratePerMinute > sanityCapPercentPerMinute {
		return nil, nil, 0, []string{ReasonBurnRateAnomaly, ReasonInsufficientSamples}
	}

	// With exactly one interval available (no durable multi-sample
	// history this wave — that requires the storage layer another role
	// owns), P50 and P90 both collapse to the single observed rate: a
	// single-sample "distribution" has no spread to report, so reporting
	// the same value for both is more honest than fabricating a spread.
	// This is a single-sample calibration limitation, not a bug: with
	// only one interval, ADD §15.5's bootstrap-resampling machinery has
	// nothing to resample from.
	p50 := ratePerMinute
	p90 := ratePerMinute

	var reasons []string
	if now.Sub(req.Current.ObservedAt) > staleAfter {
		reasons = append(reasons, ReasonStaleSample)
	}

	return &p50, &p90, 1, reasons
}

// uncalibratedFallbackScore implements the ADD §15.7 uncalibrated
// fallback thresholds verbatim:
//
//	if current_used >= 95%: critical
//	else if projected_used_p90 >= 100% and horizon_p90 <= 10m: high
//	else if projected_used_p90 >= 95%: medium/high
//	else: scaled by remaining headroom
//
// resetsWithinHorizon overrides all of the above to a low, headroom-
// available score (ADD §15.8: a reset inside the horizon means the window
// will not actually be exhausted).
func uncalibratedFallbackScore(currentUsed float64, projectedUsedP90 *float64, resetsWithinHorizon bool) (float64, []string) {
	if resetsWithinHorizon {
		return 0.1, []string{ReasonHeadroomAvailable}
	}
	if currentUsed >= 95 {
		return 1.0, []string{ReasonCriticalUsage}
	}
	if projectedUsedP90 != nil {
		if *projectedUsedP90 >= 100 {
			return 0.85, []string{ReasonProjectedExceedsLimitWithinHorizon}
		}
		if *projectedUsedP90 >= 95 {
			return 0.65, []string{ReasonProjectedNearLimit}
		}
	}
	// Scale smoothly by remaining headroom below 95% current usage, so
	// the score is a continuous, explainable function of current_used
	// rather than a step function with only the two thresholds above.
	// At current_used=0 this is ~0; at current_used=95 this approaches
	// the 0.65 "medium/high" band from below, never crossing it.
	scaled := (currentUsed / 95.0) * 0.6
	if scaled < 0 {
		scaled = 0
	}
	return scaled, []string{ReasonHeadroomAvailable}
}

// confidenceFor derives an overall Confidence label from sample
// availability and sample freshness. Never ConfidenceExact (this package
// never has ground truth, only an estimate); never higher than
// ConfidenceMedium without a calibrated model, which this wave does not
// have (mirrors the ScoreExact-is-never-claimed discipline already used
// elsewhere in this role's Wave 1 code, e.g. features.ClassifyTask).
func confidenceFor(sampleCount int, current domain.QuotaObservation, now time.Time) domain.Confidence {
	if sampleCount == 0 {
		return domain.ConfidenceLow
	}
	if now.Sub(current.ObservedAt) > staleAfter {
		return domain.ConfidenceLow
	}
	return domain.ConfidenceMedium
}

func observedAtPtr(obs domain.QuotaObservation) *time.Time {
	t := obs.ObservedAt
	return &t
}

func dedupe(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(reasons))
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

// CombineWindows combines per-limit-window forecasts into a single overall
// risk signal via max(RiskScore) across windows — ADD §15.5's explicit v1
// default ("若 windows 高度相關，policy 可用保守 max(P_i)；v1 預設取
// max，避免錯誤獨立假設"), avoiding the independence-assuming
// 1-Π(1-P_i) formula that the calibrated path would use. Returns the
// single forecast with the highest RiskScore unchanged; callers needing
// per-window detail should retain the full slice separately. An empty
// input returns a zero-value, uncalibrated, ConfidenceUnavailable
// forecast rather than panicking.
func CombineWindows(forecasts []domain.RunwayForecast) domain.RunwayForecast {
	if len(forecasts) == 0 {
		return domain.RunwayForecast{
			Confidence:  domain.ConfidenceUnavailable,
			ReasonCodes: []string{ReasonInsufficientSamples, ReasonColdStart},
		}
	}
	worst := forecasts[0]
	for _, f := range forecasts[1:] {
		if f.RiskScore > worst.RiskScore {
			worst = f
		}
	}
	return worst
}
