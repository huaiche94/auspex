package quota

import (
	"context"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// RuleQuotaForecaster is the Phase 4 (Version 1, deterministic/rule-based)
// Stage-3 implementation of app.QuotaForecaster (ADR-041, predictor-05c).
// It follows ADD §15.3's quota delta model
// (projected_used_p90 = current_used_percent + predicted_delta_p90) and
// §15.9's context projection
// (projected_context_used_p90 = current_context_used + predicted_net_context_growth_p90),
// using this package's documented cold-start default deltas (coldstart.go)
// since no durable historical telemetry store exists yet this phase —
// exactly the estimate CONTRACT_FREEZE.md's "Predictor pipeline ports
// (ADR-041)" section anticipates and licenses ("QuotaForecaster
// implementations MAY produce a deterministic current-observation-plus-
// default-delta estimate ... This is not a stub to be later thrown away").
//
// Stateless: unlike internal/predictor/scope and internal/predictor/token,
// this forecaster needs no FeatureSource abstraction, because
// app.ForecastQuotaRequest already carries everything Stage 3 needs
// directly — current Quota/Context observations plus the upstream
// TokenForecast — with no session/repository/progress feature lookup gap
// to bridge.
type RuleQuotaForecaster struct{}

// NewRuleQuotaForecaster constructs a RuleQuotaForecaster. It holds no
// state or configuration; all inputs arrive per-call via
// app.ForecastQuotaRequest.
func NewRuleQuotaForecaster() *RuleQuotaForecaster {
	return &RuleQuotaForecaster{}
}

var _ app.QuotaForecaster = (*RuleQuotaForecaster)(nil)

// ForecastQuota implements app.QuotaForecaster.
func (f *RuleQuotaForecaster) ForecastQuota(_ context.Context, req app.ForecastQuotaRequest) (domain.QuotaForecast, error) {
	var reasons []domain.ReasonCode

	quotaProjection, quotaReasons := projectQuota(req.Quota, req.TokenForecast)
	contextProjection, contextReasons := projectContext(req.Context, req.TokenForecast)

	reasons = append(reasons, quotaReasons...)
	reasons = append(reasons, contextReasons...)

	// Cold-start this phase, unconditionally (see doc.go): no empirical
	// per-provider/model/task-class delta distribution exists to satisfy
	// ADD §15.3 step 5's calibration gate, so this is always the
	// current-observation-plus-default-delta estimate, never a calibrated
	// probability (Constitution §7 rule 7).
	reasons = append(reasons, domain.ReasonPredictionColdStart)

	return domain.QuotaForecast{
		ProjectedQuotaUsedP90:   quotaProjection,
		ProjectedContextUsedP90: contextProjection,
		Calibrated:              false,
		Confidence:              domain.ConfidenceLow,
		ReasonCodes:             dedupeReasons(reasons),
	}, nil
}

// projectQuota implements ADD §15.3's quota delta model across every
// limit window in observations, combining them via the conservative
// max-across-windows rule already established by
// internal/predictor/runway.CombineWindows for the same "windows 高度相關"
// reasoning (ADD §15.5) — QuotaForecast carries a single scalar
// projection, not per-window detail, so the worst (highest projected
// usage) window drives it. Returns (nil, reasons) when no usable
// observation exists (unknown, never a fabricated zero — ADD principle 1).
func projectQuota(observations []domain.QuotaObservation, tokenForecast domain.TokenForecast) (*float64, []domain.ReasonCode) {
	var (
		worst        *float64
		worstReasons []domain.ReasonCode
		sawAny       bool
	)

	for _, obs := range observations {
		sawAny = true
		projected, reasons := projectOneQuotaWindow(obs, tokenForecast)
		if projected == nil {
			continue
		}
		if worst == nil || *projected > *worst {
			worst = projected
			worstReasons = reasons
		}
	}

	if !sawAny {
		return nil, []domain.ReasonCode{domain.ReasonQuotaUnknown}
	}
	if worst == nil {
		// Every window had an unusable (nil UsedPercent) observation:
		// still unknown, not zero.
		return nil, []domain.ReasonCode{domain.ReasonQuotaUnknown}
	}
	return worst, worstReasons
}

// projectOneQuotaWindow projects a single QuotaObservation forward by one
// turn using the cold-start default delta (P90), scaled by the token
// forecast when available (tokenAdjustedDelta), per ADD §15.3's
// projection formula. reset/decrease handling follows §15.3 step 4 ("reset
// /decrease 樣本標 censored") and §15.8 ("resets_at 是 schedule hint"):
// a window whose reset is imminent is not assumed to keep accumulating
// past that reset, so the delta is not applied past a known ResetsAt.
func projectOneQuotaWindow(obs domain.QuotaObservation, tokenForecast domain.TokenForecast) (*float64, []domain.ReasonCode) {
	if obs.UsedPercent == nil {
		return nil, []domain.ReasonCode{domain.ReasonQuotaUnknown}
	}

	current := *obs.UsedPercent
	delta := tokenAdjustedDelta(defaultQuotaDeltaP90, tokenForecast)

	var reasons []domain.ReasonCode

	// §15.8: resets_at is a schedule hint. If the window is expected to
	// reset before this turn plausibly completes, do not project the
	// delta past that reset — the projection stays at (a floor of) the
	// current usage rather than compounding across a reset boundary that
	// would zero it out in reality. No turn-duration estimate is wired up
	// this phase, so "imminent" uses a conservative fixed look-ahead
	// (turnHorizon) rather than a real duration forecast — documented
	// assumption, mirrors runway's own DefaultHorizon precedent of a
	// fixed default when no better signal exists.
	if obs.ResetsAt != nil {
		if until := time.Until(*obs.ResetsAt); until > 0 && until <= turnHorizon {
			reasons = append(reasons, domain.ReasonQuotaResetSoon)
			delta = 0
		}
	}

	projected := current + delta
	if projected > 100 {
		projected = 100
	}
	if projected < 0 {
		projected = 0
	}

	if obs.Reached || current >= quotaNearLimitThreshold || projected >= quotaNearLimitThreshold {
		reasons = append(reasons, domain.ReasonQuotaNearLimit)
	}

	return &projected, reasons
}

// projectContext implements ADD §15.9's context projection:
// projected_context_used_p90 = current_context_used + predicted_net_context_growth_p90.
// Growth is expressed in TOKENS (coldstart.go's
// defaultContextGrowthP90Tokens, D-14), scaled by the token forecast when
// available, then divided by the observed WindowTokens to get a
// percentage-point delta consistent with ContextObservation.UsedPercent's
// units — so the same "typical turn" costs 10 points of a 200k window
// but only 2 points of a 1M window. Only when WindowTokens is unknown
// does the pre-D-14 window-fraction default apply (a fraction is the only
// unit that survives without a window size).
func projectContext(obs domain.ContextObservation, tokenForecast domain.TokenForecast) (*float64, []domain.ReasonCode) {
	current, ok := currentContextUsedPercent(obs)
	if !ok {
		return nil, []domain.ReasonCode{domain.ReasonContextUnknown}
	}

	var deltaPercent float64
	if obs.WindowTokens != nil && *obs.WindowTokens > 0 {
		growthTokens := tokenAdjustedDelta(defaultContextGrowthP90Tokens, tokenForecast)
		deltaPercent = growthTokens / float64(*obs.WindowTokens) * 100
	} else {
		deltaPercent = tokenAdjustedDelta(fallbackContextGrowthP90Fraction, tokenForecast) * 100
	}

	projected := current + deltaPercent
	if projected > 100 {
		projected = 100
	}
	if projected < 0 {
		projected = 0
	}

	var reasons []domain.ReasonCode
	if current >= contextNearLimitThreshold || projected >= contextNearLimitThreshold {
		reasons = append(reasons, domain.ReasonContextNearLimit)
	}
	return &projected, reasons
}

// currentContextUsedPercent resolves the current context-window usage
// percentage from obs, preferring the exact UsedTokens/WindowTokens ratio
// and falling back to the provider-reported UsedPercent. D-14 flipped
// that preference: Claude Code rounds used_percentage to whole percents
// (1% of a 1M window is a 10k-token blur) while the token counts in the
// same payload are exact — prefer the sharp measurement, keep the coarse
// one as fallback. ok=false means genuinely unknown (ADD principle 1:
// unknown is not zero) — never a fabricated 0%.
func currentContextUsedPercent(obs domain.ContextObservation) (float64, bool) {
	if obs.UsedTokens != nil && obs.WindowTokens != nil && *obs.WindowTokens > 0 {
		return (float64(*obs.UsedTokens) / float64(*obs.WindowTokens)) * 100, true
	}
	if obs.UsedPercent != nil {
		return *obs.UsedPercent, true
	}
	return 0, false
}

// tokenAdjustedDelta scales base (a P90 default delta/growth value) by the
// upstream TokenForecast when it carries a usable, positive TokensP90,
// relative to a nominal "typical turn" token baseline (nominalTurnTokens).
// A larger-than-typical forecasted turn scales the delta up; a
// smaller-than-typical one scales it down — bounded by
// tokenScaledDeltaFloor/Ceiling (coldstart.go) so a single extreme
// TokenForecast cannot blow up or erase the conservative default. When
// TokenForecast carries no usable signal (TokensP90 <= 0, the zero-value
// case for a caller that supplies no upstream forecast), the unscaled
// base is returned unchanged — this keeps ForecastQuota usable even when
// TokenForecast is the interface's documented-optional fallback input
// (app.ForecastQuotaRequest's own doc comment: "MAY use TokenForecast ...
// MUST NOT require it").
func tokenAdjustedDelta(base float64, tokenForecast domain.TokenForecast) float64 {
	if tokenForecast.TokensP90 <= 0 {
		return base
	}
	scale := float64(tokenForecast.TokensP90) / nominalTurnTokens
	if scale < tokenScaledDeltaFloor {
		scale = tokenScaledDeltaFloor
	}
	if scale > tokenScaledDeltaCeiling {
		scale = tokenScaledDeltaCeiling
	}
	return base * scale
}

// nominalTurnTokens is the token count tokenAdjustedDelta treats as
// "typical" (scale factor 1.0) when relating an upstream TokenForecast to
// the default quota/context deltas. Matches
// internal/predictor/token.baseTurnTokens exactly (not imported across
// packages — the two constants measure the same underlying concept from
// different packages' independent vantage points, mirroring
// internal/predictor/token/coldstart.go's own documented rationale for
// keeping its cold-start table independent from scope's), so a
// TokenForecast produced by RuleTokenForecaster's own cold-start default
// scales this package's deltas to exactly 1.0x when nothing else is known.
const nominalTurnTokens = 6000.0

// turnHorizon bounds how far ahead a quota window's ResetsAt is treated as
// "imminent" for §15.8 reset-awareness (projectOneQuotaWindow). No
// turn-duration forecast is wired up this phase; 10 minutes matches
// internal/predictor/runway.DefaultHorizon, the same default horizon this
// codebase already uses elsewhere for "is something about to happen
// within a typical turn's timeframe" questions.
const turnHorizon = 10 * time.Minute

// quotaNearLimitThreshold/contextNearLimitThreshold flag ReasonQuotaNearLimit/
// ReasonContextNearLimit once current or projected usage crosses this
// percentage. No exact threshold is named in ADD §15.3/§15.9 (unlike
// §15.7's runway thresholds, which are explicit); 90% is chosen to mirror
// the "P90" framing already used throughout this pipeline stage (a
// projection at or above the same percentile mark used to compute it is
// treated as near-limit) — a documented, conservative default.
const (
	quotaNearLimitThreshold   = 90.0
	contextNearLimitThreshold = 90.0
)

func dedupeReasons(reasons []domain.ReasonCode) []domain.ReasonCode {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[domain.ReasonCode]struct{}, len(reasons))
	out := make([]domain.ReasonCode, 0, len(reasons))
	for _, r := range reasons {
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}
