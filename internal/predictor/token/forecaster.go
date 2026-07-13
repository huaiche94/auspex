package token

import (
	"context"
	"math"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/features"
	"github.com/huaiche94/preflight/internal/predictor"
)

// FeatureSource resolves the feature inputs a token forecast needs beyond
// what app.ForecastTokensRequest itself carries (SessionID and the
// upstream Stage-1 domain.ScopeEstimate). Mirrors
// internal/predictor/scope.FeatureSource's rationale exactly: a narrow,
// consumer-side view of the frozen feature-lookup port
// app.FeatureDataSource (ADR-044). Note this view's Classification
// deliberately omits the taskID parameter the frozen port carries — the
// token stage never needs it; adapters supply nil.
type FeatureSource interface {
	// Classification returns the task classifier's output for the current
	// turn, and the prompt features it was derived from (ambiguity signal
	// for ambiguity_multiplier comes from the prompt directly).
	Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error)

	// Session returns session-derived features (recent-turn token
	// quantiles, retry rate, etc.) for sessionID. ok=false means "not
	// available yet" (cold-start), not an error.
	Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error)

	// Progress returns Progress-Tree-derived features for the current
	// task/node, when available. ok=false means "not available yet".
	Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error)

	// RecentSimilarTurnTokens returns raw total-token observations for
	// recent turns matching provider + model family + task class +
	// repository cohort (ADD §15.2's "similar" set). Used only when its
	// length is >= MinSimilarSamples; below that this package always uses
	// the ADD §14.6 cold-start default instead (ADD §15.2's explicit gate).
	RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) ([]float64, error)
}

// RuleTokenForecaster is the Wave 3 (Version 1, rule-based/heuristic)
// Stage-2 implementation of app.TokenForecaster (ADR-041, predictor-05b).
// It follows ADD §15.1's token decomposition and §15.2's initial token
// predictor formula: empirical P50/P90 from local history once >=8 similar
// samples exist, else the ADD §14.6 cold-start table, combined with the
// scope/verification/complexity/retry/progress/ambiguity multiplier model
// via geometric mean with caps (§15.2: "使用 geometric mean 避免乘數爆炸，
// 並做 caps").
//
// No durable historical telemetry store exists yet this wave — the same
// gap already noted for predictor-05/predictor-06 (agents/predictor.md's
// cold-start contract; CONTRACT_FREEZE.md's cold-start discipline for
// QuotaForecaster applies here by the same reasoning). Every result this
// implementation produces this wave is therefore Calibrated=false,
// Confidence<=ConfidenceLow: the >=8-sample empirical branch is
// implemented (so a future FeatureSource backed by durable storage
// activates it for free), but no caller wired up this wave supplies >=8
// samples, so it is cold-start-only in practice.
type RuleTokenForecaster struct {
	Source FeatureSource

	// MinSimilarSamples is the ADD §15.2 "count(similar) >= 8" gate.
	MinSimilarSamples int
}

// NewRuleTokenForecaster constructs a RuleTokenForecaster with the ADD
// §15.2 default minimum sample gate (8).
func NewRuleTokenForecaster(source FeatureSource) *RuleTokenForecaster {
	return &RuleTokenForecaster{Source: source, MinSimilarSamples: 8}
}

var _ app.TokenForecaster = (*RuleTokenForecaster)(nil)

// multiplierCap is the per-multiplier ceiling applied before combination,
// per ADD §15.2's explicit "avoid multiplier explosion" instruction. No
// exact cap value is specified in the ADD, so this package uses a
// conservative shared ceiling for every named multiplier — high enough
// that a legitimately large/complex turn is still distinguished from a
// small one, low enough that no single signal can dominate the geometric
// mean and blow up the forecast unboundedly.
const multiplierCap = 3.0

// combinedCap bounds the geometric-mean-combined multiplier itself, as a
// second line of defense beyond the per-multiplier caps (six capped
// multipliers combined could still compound past any single one).
const combinedCap = 6.0

// ForecastTokens implements app.TokenForecaster.
func (f *RuleTokenForecaster) ForecastTokens(ctx context.Context, req app.ForecastTokensRequest) (domain.TokenForecast, error) {
	class, promptFeat, err := f.Source.Classification(ctx, req.SessionID)
	if err != nil {
		return domain.TokenForecast{}, err
	}

	sessFeat, sessOK, err := f.Source.Session(ctx, req.SessionID)
	if err != nil {
		return domain.TokenForecast{}, err
	}

	progFeat, progOK, err := f.Source.Progress(ctx, req.SessionID)
	if err != nil {
		return domain.TokenForecast{}, err
	}

	var reasons []domain.ReasonCode
	confidence := domain.ConfidenceLow
	calibrated := false

	// --- Base P50/P90 (ADD §15.2: empirical once >=8 similar samples,
	// else cold-start default) ------------------------------------------
	basePfifty, baseP90, baseIsEmpirical, err := f.base(ctx, req.SessionID, class.Class)
	if err != nil {
		return domain.TokenForecast{}, err
	}
	if !baseIsEmpirical {
		reasons = append(reasons, domain.ReasonPredictionColdStart)
	} else {
		// Empirical base sharpens the estimate but this is still not a
		// calibrated probability (mirrors scope.RuleScopeEstimator's same
		// discipline for session-blended estimates).
		confidence = domain.ConfidenceMedium
		reasons = append(reasons, domain.ReasonTelemetrySparse)
	}
	if class.Class == features.TaskClassUnknown {
		reasons = append(reasons, domain.ReasonPredictionColdStart)
	}

	// --- Multipliers (ADD §15.2) ----------------------------------------
	scopeMult := scopeMultiplier(req.Scope)
	verifMult := verificationMultiplier(req.Scope)
	complexMult := complexityMultiplier(req.Scope)
	retryMult, retryReason := retryMultiplier(sessFeat, sessOK)
	progressMult, progressReason := progressMultiplier(progFeat, progOK)
	ambiguityMult, ambiguityReason := ambiguityMultiplier(promptFeat)

	if retryReason != "" {
		reasons = append(reasons, retryReason)
	}
	if progressReason != "" {
		reasons = append(reasons, progressReason)
	}
	if ambiguityReason != "" {
		reasons = append(reasons, ambiguityReason)
	}

	combined := combineMultipliers(
		scopeMult, verifMult, complexMult, retryMult, progressMult, ambiguityMult,
	)

	p50 := basePfifty * combined
	p90 := baseP90 * combined

	// p50 <= p90 must hold: the base pair already satisfies it
	// (EmpiricalQuantiles/cold-start construction both guarantee it) and
	// combined is a single positive scalar applied to both, which
	// preserves order. Guarded explicitly as the single choke point, in
	// case a future edit to the base computation breaks that locally.
	if p50 > p90 {
		p50, p90 = p90, p50
	}

	// P80 assumption: ADD §15.2's base-quantile description names only
	// P50/P90 ("base_p50 = weighted_quantile(tokens, 0.50)" / "base_p90 =
	// weighted_quantile(tokens, 0.90)") — no base_p80. Rather than
	// fabricating an unrelated third empirical quantile, P80 is
	// interpolated between the (already multiplier-adjusted) P50 and P90
	// using a log-space weighted blend (60% of the way from P50 to P90 in
	// log-space, matching typical right-skewed token-count distributions
	// where P80 sits closer to P90 than to P50 on a linear scale but the
	// gap should not grow unboundedly for very wide P50-P90 spreads).
	// Documented explicitly per this node's assigned scope.
	p80 := interpolateP80(p50, p90)

	if scopeMult >= multiplierCap || verifMult >= multiplierCap || complexMult >= multiplierCap ||
		retryMult >= multiplierCap || progressMult >= multiplierCap || ambiguityMult >= multiplierCap {
		reasons = append(reasons, domain.ReasonOpenEndedScope)
	}

	if req.Scope.SecuritySensitive {
		reasons = append(reasons, domain.ReasonSecuritySensitive)
	}
	if req.Scope.MigrationLikely {
		reasons = append(reasons, domain.ReasonMigrationLikely)
	}
	if req.Scope.CrossProject {
		reasons = append(reasons, domain.ReasonCrossLayerChange)
	}

	return domain.TokenForecast{
		TokensP50:   round(p50),
		TokensP80:   round(p80),
		TokensP90:   round(p90),
		Calibrated:  calibrated, // always false this wave — see doc.go / package comment
		Confidence:  confidence,
		ReasonCodes: dedupeReasons(reasons),
	}, nil
}

// base returns (p50, p90, isEmpirical, error): the empirical base from
// RecentSimilarTurnTokens when at least MinSimilarSamples observations are
// available (ADD §15.2's "count(similar) >= 8" gate), else the ADD §14.6
// cold-start default scaled by baseTurnTokens.
func (f *RuleTokenForecaster) base(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) (p50, p90 float64, isEmpirical bool, err error) {
	min := f.MinSimilarSamples
	if min <= 0 {
		min = 8
	}

	samples, err := f.Source.RecentSimilarTurnTokens(ctx, sessionID, class)
	if err != nil {
		return 0, 0, false, err
	}
	if len(samples) >= min {
		q := predictor.EmpiricalQuantiles(samples)
		return q.P50, q.P90, true, nil
	}

	mult := lookupColdStartMultiplier(class)
	base := baseTurnTokens * mult
	// Cold-start P90 is a fixed spread above P50 (no distribution to
	// measure yet): 2x mirrors the ADD's own "Never Return a Single
	// Number" P50/P80/P95 example ratios in
	// Preflight_Predictor_Design_Supplement.md (38000/61000/94000 ~=
	// 1 : 1.6 : 2.47), rounded to a conservative, explainable constant
	// rather than reverse-engineering a precise ratio from one example.
	return base, base * 2.0, false, nil
}

// scopeMultiplier implements ADD §15.2's scope_multiplier verbatim, using
// the Stage-1 ScopeEstimate's P50 files/lines changed. Nil fields
// (unknown, never populated) contribute 0, matching "unknown is not zero"
// only in the sense that an *unpopulated* scope signal must not inflate
// the multiplier — it does not silently claim a nonzero scope that was
// never estimated.
func scopeMultiplier(scope domain.ScopeEstimate) float64 {
	filesChanged := 0.0
	if scope.FilesChangedP50 != nil {
		filesChanged = float64(*scope.FilesChangedP50)
	}
	linesChanged := 0.0
	if scope.LinesChangedP50 != nil {
		linesChanged = float64(*scope.LinesChangedP50)
	}
	m := 1.0 + 0.06*filesChanged + 0.002*linesChanged
	return capMultiplier(m)
}

// verificationMultiplier implements ADD §15.2's verification_multiplier.
// build_required has no direct ScopeEstimate/PromptFeatures signal wired
// up this wave, so it is treated as implied by RequiresIntegration (an
// integration-test-requiring turn is assumed to also require a build) —
// documented assumption, not a silent omission.
func verificationMultiplier(scope domain.ScopeEstimate) float64 {
	unitTests := 0.0
	if scope.RequiresUnitTests {
		unitTests = 1.0
	}
	integrationTests := 0.0
	buildRequired := 0.0
	if scope.RequiresIntegration {
		integrationTests = 1.0
		buildRequired = 1.0
	}
	m := 1.0 + 0.20*unitTests + 0.45*integrationTests + 0.25*buildRequired
	return capMultiplier(m)
}

// complexityMultiplier implements ADD §15.2's complexity_multiplier.
// repository_wide has no direct boolean on ScopeEstimate; CrossProject is
// reused for both cross_layer and repository_wide terms is avoided —
// repository_wide is approximated by the largest cold-start scope shapes
// (files_changed_p90 >= 15, mirroring scope.RuleScopeEstimator's own
// ReasonLargeFileScope threshold) rather than left uncounted.
func complexityMultiplier(scope domain.ScopeEstimate) float64 {
	crossLayer := 0.0
	if scope.CrossProject {
		crossLayer = 1.0
	}
	migration := 0.0
	if scope.MigrationLikely {
		migration = 1.0
	}
	securitySensitive := 0.0
	if scope.SecuritySensitive {
		securitySensitive = 1.0
	}
	repositoryWide := 0.0
	if scope.FilesChangedP90 != nil && *scope.FilesChangedP90 >= 15 {
		repositoryWide = 1.0
	}
	m := 1.0 + 0.35*crossLayer + 0.45*migration + 0.30*securitySensitive + 0.25*repositoryWide
	return capMultiplier(m)
}

// retryMultiplier implements ADD §15.2's retry_multiplier
// (1 + min(0.75, recent_retry_rate * 0.8)) from SessionFeatures.RetryRate.
// No session data (sessOK=false) or a nil RetryRate both mean "unknown",
// which returns the neutral multiplier 1.0 (no retry signal to apply) with
// a cold-start reason code, not a fabricated zero retry rate presented as
// measured.
func retryMultiplier(sess features.SessionFeatures, sessOK bool) (float64, domain.ReasonCode) {
	if !sessOK || sess.RetryRate == nil {
		return 1.0, domain.ReasonPredictionColdStart
	}
	rate := *sess.RetryRate
	if rate < 0 {
		rate = 0
	}
	m := 1.0 + math.Min(0.75, rate*0.8)
	reason := domain.ReasonCode("")
	if rate > 0.3 {
		reason = domain.ReasonHighRecentRetryRate
	}
	return capMultiplier(m), reason
}

// progressMultiplier implements ADD §15.2's progress_multiplier
// (max(0.25, remaining_critical_path_cost / original_task_cost)) using
// ProgressFeatures.CompletedRatio as the source for the ratio: remaining
// fraction = 1 - CompletedRatio approximates
// remaining_critical_path_cost/original_task_cost when no separate cost
// model exists yet (documented assumption — ADD §15.2 does not specify how
// "critical path cost" is measured, and ProgressFeatures.CompletedRatio is
// the closest available signal this wave). No progress data (progOK=false)
// or a nil CompletedRatio means "unknown", which returns the neutral
// multiplier 1.0 with a cold-start reason code.
func progressMultiplier(prog features.ProgressFeatures, progOK bool) (float64, domain.ReasonCode) {
	if !progOK || prog.CompletedRatio == nil {
		return 1.0, domain.ReasonPredictionColdStart
	}
	ratio := *prog.CompletedRatio
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	remaining := 1.0 - ratio
	m := math.Max(0.25, remaining)
	reason := domain.ReasonCode("")
	if prog.CriticalPathLength > 10 {
		reason = domain.ReasonLongRemainingCriticalPath
	}
	return capMultiplier(m), reason
}

// ambiguityMultiplier implements ADD §15.2's ambiguity_multiplier
// (1.0 / 1.2 / 1.5 / 2.0 four-band table) from PromptFeatures signals:
//   - explicit files + acceptance criteria named -> 1.0
//   - mostly clear (has explicit paths, no acceptance criteria) -> 1.2
//   - requires exploration (no explicit paths, not open-ended) -> 1.5
//   - open-ended ("fix the system") -> 2.0
//
// This mapping from PromptFeatures to the four ADD-named bands is this
// package's own documented interpretation — the ADD names the bands and
// their multipliers but not the exact feature-to-band rule, so this
// mirrors the ordering/severity already established by
// PromptFeatures.OpenEndedIndicator's own doc comment.
func ambiguityMultiplier(pf features.PromptFeatures) (float64, domain.ReasonCode) {
	switch {
	case pf.OpenEndedIndicator:
		return capMultiplier(2.0), domain.ReasonOpenEndedScope
	case pf.ExplicitPathCount == 0:
		return capMultiplier(1.5), domain.ReasonCode("")
	case pf.AcceptanceCriteriaCount > 0:
		return capMultiplier(1.0), domain.ReasonCode("")
	default:
		return capMultiplier(1.2), domain.ReasonCode("")
	}
}

// capMultiplier enforces ADD §15.2's "做 caps" instruction per-multiplier.
func capMultiplier(m float64) float64 {
	if m < 0 {
		return 0
	}
	if m > multiplierCap {
		return multiplierCap
	}
	return m
}

// combineMultipliers combines the six named multipliers via geometric mean
// (ADD §15.2: "使用 geometric mean 避免乘數爆炸"), then applies a second,
// combined-level cap as a further explosion guard.
func combineMultipliers(ms ...float64) float64 {
	if len(ms) == 0 {
		return 1.0
	}
	product := 1.0
	for _, m := range ms {
		if m <= 0 {
			m = 1.0 // a zero/negative multiplier is a bug elsewhere, not a valid "erase the forecast" signal
		}
		product *= m
	}
	geoMean := math.Pow(product, 1.0/float64(len(ms)))
	if geoMean > combinedCap {
		return combinedCap
	}
	if geoMean < 0 {
		return 0
	}
	return geoMean
}

// interpolateP80 blends p50 and p90 in log-space (see ForecastTokens'
// P80-assumption comment). Falls back to the linear midpoint when either
// bound is non-positive (log undefined) — token counts should never be
// negative, but this guards degenerate/zero inputs rather than producing
// NaN.
func interpolateP80(p50, p90 float64) float64 {
	if p50 <= 0 || p90 <= 0 {
		return (p50 + p90) / 2
	}
	const weight = 0.6 // 60% of the way from P50 to P90 in log-space
	logP50 := math.Log(p50)
	logP90 := math.Log(p90)
	logP80 := logP50 + weight*(logP90-logP50)
	return math.Exp(logP80)
}

func round(f float64) int64 {
	if f < 0 {
		return 0
	}
	return int64(f + 0.5)
}

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
