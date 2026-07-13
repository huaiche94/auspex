package risk

import (
	"context"
	"math"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// RuleRiskCombiner is the Wave 5 (Version 1, rule-based/deterministic)
// Stage-4 implementation of app.RiskCombiner (ADR-041, predictor-07). It
// implements ADD §16.2's "Initial explainable formula" verbatim (see
// doc.go), combining the upstream Stage 1-3 outputs
// (domain.ScopeEstimate, domain.TokenForecast, domain.QuotaForecast) into
// the four named risk components (quota, context, completion,
// blast-radius) plus an overall score.
//
// Stateless: like internal/predictor/quota.RuleQuotaForecaster,
// CombineRiskRequest already carries everything this stage needs directly
// (Scope, TokenForecast, QuotaForecast) — no session/repository/progress
// feature-lookup gap to bridge, so no package-local FeatureSource
// abstraction is needed here (unlike internal/predictor/scope and
// internal/predictor/token, which both need one).
type RuleRiskCombiner struct{}

// NewRuleRiskCombiner constructs a RuleRiskCombiner. It holds no state or
// configuration; all inputs arrive per-call via app.CombineRiskRequest.
func NewRuleRiskCombiner() *RuleRiskCombiner {
	return &RuleRiskCombiner{}
}

var _ app.RiskCombiner = (*RuleRiskCombiner)(nil)

// Combine implements app.RiskCombiner.
func (c *RuleRiskCombiner) Combine(_ context.Context, req app.CombineRiskRequest) (app.CombineRiskResult, error) {
	quotaRisk := quotaRiskComponent(req.QuotaForecast)
	contextRisk := contextRiskComponent(req.QuotaForecast)
	completionRisk := completionRiskComponent(req.Scope)
	blastRadiusRisk := blastRadiusRiskComponent(req.Scope)

	overallRisk := overallRiskComponent(quotaRisk, contextRisk, completionRisk, blastRadiusRisk)

	return app.CombineRiskResult{
		QuotaRisk:       quotaRisk,
		ContextRisk:     contextRisk,
		CompletionRisk:  completionRisk,
		BlastRadiusRisk: blastRadiusRisk,
		OverallRisk:     overallRisk,
	}, nil
}

// quotaRiskComponent implements ADD §16.2's
// quota_risk = sigmoid((projected_quota_p90 - 85) / 7), sourced from
// QuotaForecast.ProjectedQuotaUsedP90 (ADR-041: quota_risk and context_risk
// both come from the same Stage-3 QuotaForecast, different fields). A nil
// projection (§16.3: "quota unknown：不設 0；加入 QUOTA_UNKNOWN +
// uncertainty penalty") never becomes a fabricated 0% — it returns a
// conservative mid-range score instead (see unknownProjectionScore) plus
// domain.ReasonQuotaUnknown, and Calibrated/Confidence are propagated
// honestly from the upstream forecast, never manufactured.
func quotaRiskComponent(qf domain.QuotaForecast) domain.RiskComponent {
	var reasons []domain.ReasonCode
	var score float64

	if qf.ProjectedQuotaUsedP90 == nil {
		reasons = append(reasons, domain.ReasonQuotaUnknown)
		score = unknownProjectionScore
	} else {
		score = sigmoid((*qf.ProjectedQuotaUsedP90 - sigmoidMidpoint) / sigmoidScale)
	}

	reasons = append(reasons, qf.ReasonCodes...)
	return newRiskComponent(score, qf.Calibrated, qf.Confidence, reasons)
}

// contextRiskComponent implements ADD §16.2's
// context_risk = sigmoid((projected_context_p90 - 85) / 7), sourced from
// QuotaForecast.ProjectedContextUsedP90. Mirrors quotaRiskComponent's
// unknown-handling exactly (§16.3: "context unknown：同理").
func contextRiskComponent(qf domain.QuotaForecast) domain.RiskComponent {
	var reasons []domain.ReasonCode
	var score float64

	if qf.ProjectedContextUsedP90 == nil {
		reasons = append(reasons, domain.ReasonContextUnknown)
		score = unknownProjectionScore
	} else {
		score = sigmoid((*qf.ProjectedContextUsedP90 - sigmoidMidpoint) / sigmoidScale)
	}

	reasons = append(reasons, qf.ReasonCodes...)
	return newRiskComponent(score, qf.Calibrated, qf.Confidence, reasons)
}

// completionRiskComponent implements ADD §16.2's completion_risk formula
// (see doc.go's "Terminology note" for why this is named completion_risk,
// not execution_risk, per ADR-041). Every term is sourced from the frozen
// domain.ScopeEstimate — the only Stage-1 signal CombineRiskRequest
// carries. ScopeEstimate has no direct boolean/scalar field for
// open_ended_scope, recent_retry_rate, recent_test_failure_rate, or
// unresolved_progress_blockers (those live one layer down, in
// internal/features' SessionFeatures/ProgressFeatures/PromptFeatures,
// which the frozen CombineRiskRequest does not carry — see
// completionRiskTermsFromReasonCodes' own doc comment for the documented
// bridge this package uses instead).
func completionRiskComponent(scope domain.ScopeEstimate) domain.RiskComponent {
	filesChangedP90 := ptrToFloat(scope.FilesChangedP90)
	linesChangedP90 := ptrToFloat(scope.LinesChangedP90)

	integrationTests := boolToFloat(scope.RequiresIntegration)
	migration := boolToFloat(scope.MigrationLikely)
	crossLayer := boolToFloat(scope.CrossProject)

	openEndedScope, recentRetryRate, recentTestFailureRate, unresolvedProgressBlockers :=
		completionRiskTermsFromReasonCodes(scope.ReasonCodes)

	score := completionBase +
		completionFilesChangedP90Coefficient*filesChangedP90 +
		completionLinesChangedP90Coefficient*linesChangedP90 +
		completionIntegrationTestsCoefficient*integrationTests +
		completionMigrationCoefficient*migration +
		completionCrossLayerCoefficient*crossLayer +
		completionOpenEndedScopeCoefficient*openEndedScope +
		completionRecentRetryRateCoefficient*recentRetryRate +
		completionTestFailureRateCoefficient*recentTestFailureRate +
		completionProgressBlockersCoefficient*unresolvedProgressBlockers
	score = clamp01(score)

	reasons := scopeUnknownReasons(scope)
	reasons = append(reasons, scope.ReasonCodes...)

	return newRiskComponent(score, scope.Calibrated, scope.Confidence, reasons)
}

// blastRadiusRiskComponent implements ADD §16.2's blast_radius_risk
// formula, sourced from domain.ScopeEstimate. public_api_change has no
// direct ScopeEstimate boolean (the same documented gap noted throughout
// this pipeline — e.g. internal/predictor/token.complexityMultiplier's
// repository_wide term); it is read from
// domain.ReasonPublicAPIChange in scope.ReasonCodes via
// completionRiskTermsFromReasonCodes' sibling helper, hasReason, since that
// is the only channel through which a public-API-change signal reaches the
// frozen CombineRiskRequest shape this wave.
func blastRadiusRiskComponent(scope domain.ScopeEstimate) domain.RiskComponent {
	filesChangedP90 := ptrToFloat(scope.FilesChangedP90)

	crossProject := boolToFloat(scope.CrossProject)
	migration := boolToFloat(scope.MigrationLikely)
	securitySensitive := boolToFloat(scope.SecuritySensitive)
	publicAPIChange := boolToFloat(hasReason(scope.ReasonCodes, domain.ReasonPublicAPIChange))

	score := blastRadiusBase +
		blastRadiusFilesChangedP90Coeff*filesChangedP90 +
		blastRadiusCrossProjectCoefficient*crossProject +
		blastRadiusMigrationCoefficient*migration +
		blastRadiusSecuritySensitiveCoeff*securitySensitive +
		blastRadiusPublicAPIChangeCoeff*publicAPIChange
	score = clamp01(score)

	reasons := scopeUnknownReasons(scope)
	reasons = append(reasons, scope.ReasonCodes...)

	return newRiskComponent(score, scope.Calibrated, scope.Confidence, reasons)
}

// overallRiskComponent implements ADD §16.2's
// overall = max(quota, context, completion, blast_radius). Calibrated is
// true only when every contributing component is itself calibrated (an
// overall claim can never be more certain than its least-certain input);
// Confidence takes the lowest (most conservative) of the four, and
// ReasonCodes is the deduplicated union — this is the single place a
// caller can read to get the full explanation for the worst-case
// component that drove the overall score.
func overallRiskComponent(quota, context, completion, blastRadius domain.RiskComponent) domain.RiskComponent {
	score := math.Max(math.Max(quota.Score, context.Score), math.Max(completion.Score, blastRadius.Score))

	calibrated := quota.Calibrated && context.Calibrated && completion.Calibrated && blastRadius.Calibrated
	confidence := lowestConfidence(quota.Confidence, context.Confidence, completion.Confidence, blastRadius.Confidence)

	var reasons []domain.ReasonCode
	reasons = append(reasons, quota.ReasonCodes...)
	reasons = append(reasons, context.ReasonCodes...)
	reasons = append(reasons, completion.ReasonCodes...)
	reasons = append(reasons, blastRadius.ReasonCodes...)

	return newRiskComponent(score, calibrated, confidence, reasons)
}

// newRiskComponent constructs a domain.RiskComponent with score clamped to
// [0,1] (never NaN/Inf — see clamp01) and deduplicated reason codes. Per
// ADD principle 2 ("Score is not probability") and Constitution §7 rule 7,
// callers of this package must treat Score as a 0-1 risk score, never a
// probability, unless Calibrated is true.
func newRiskComponent(score float64, calibrated bool, confidence domain.Confidence, reasons []domain.ReasonCode) domain.RiskComponent {
	if confidence == "" {
		confidence = domain.ConfidenceLow
	}
	return domain.RiskComponent{
		Score:       clamp01(score),
		Calibrated:  calibrated,
		Confidence:  confidence,
		ReasonCodes: dedupeReasons(reasons),
	}
}

// scopeUnknownReasons adds domain.ReasonPredictionColdStart when scope
// itself is uncalibrated, so completion_risk/blast_radius_risk's own
// ReasonCodes honestly disclose that their ScopeEstimate input was a
// cold-start estimate, not a measured one — mirrors §16.3's "insufficient
// history：PREDICTION_COLD_START" unknown-handling rule, applied at the
// combiner layer rather than re-deriving it (RuleScopeEstimator already
// adds this reason to scope.ReasonCodes itself when appropriate; this is
// an explicit belt-and-suspenders re-assertion so completion/blast-radius
// risk never silently drops the signal if a future ScopeEstimator
// implementation forgets to).
func scopeUnknownReasons(scope domain.ScopeEstimate) []domain.ReasonCode {
	if !scope.Calibrated {
		return []domain.ReasonCode{domain.ReasonPredictionColdStart}
	}
	return nil
}

// completionRiskTermsFromReasonCodes bridges ADD §16.2's
// open_ended_scope/recent_retry_rate/recent_test_failure_rate/
// unresolved_progress_blockers formula terms to the frozen
// domain.ScopeEstimate shape this package actually receives.
//
// None of these four terms has a direct field on domain.ScopeEstimate
// (unlike files_changed_p90/lines_changed_p90/integration_tests/migration/
// cross_layer, which map onto existing ScopeEstimate fields one-to-one).
// The underlying signals do exist one layer down, in internal/features'
// PromptFeatures.OpenEndedIndicator, SessionFeatures.RetryRate/
// TestFailureRate, and ProgressFeatures.UnresolvedBlockers — but the
// frozen app.CombineRiskRequest (internal/app/ports.go, ADR-041) carries
// only Scope/TokenForecast/QuotaForecast, not those feature DTOs directly,
// and this role's boundary (agents/predictor.md: "Return decisions through
// frozen ports") means CombineRiskRequest is not a file this node may
// widen.
//
// internal/predictor/scope.RuleScopeEstimator already surfaces each of
// these upstream signals as a domain.ReasonCode on ScopeEstimate.ReasonCodes
// when the underlying condition holds (ReasonOpenEndedScope from
// PromptFeatures.OpenEndedIndicator; ReasonHighRecentRetryRate from
// SessionFeatures.RetryRate>0.3; ReasonHighRecentTestFailureRate is the
// same class of signal from SessionFeatures.TestFailureRate;
// ReasonProgressBlocked from ProgressFeatures.UnresolvedBlockers), so
// scope.ReasonCodes is the one channel through which these four formula
// terms actually reach this node. This documented bridge treats each
// term's presence in scope.ReasonCodes as a boolean indicator (1.0/0.0),
// not the underlying continuous rate — deliberately conservative (a
// present reason code contributes its full formula coefficient, not a
// partial one), consistent with this package's overall
// never-manufacture-false-precision discipline: a boolean "this condition
// was flagged" is honest about what CombineRiskRequest actually carries,
// whereas guessing a continuous rate from a boolean reason code would not
// be.
func completionRiskTermsFromReasonCodes(reasons []domain.ReasonCode) (openEndedScope, recentRetryRate, recentTestFailureRate, unresolvedProgressBlockers float64) {
	openEndedScope = boolToFloat(hasReason(reasons, domain.ReasonOpenEndedScope))
	recentRetryRate = boolToFloat(hasReason(reasons, domain.ReasonHighRecentRetryRate))
	recentTestFailureRate = boolToFloat(hasReason(reasons, domain.ReasonHighRecentTestFailureRate))
	unresolvedProgressBlockers = boolToFloat(hasReason(reasons, domain.ReasonProgressBlocked))
	return openEndedScope, recentRetryRate, recentTestFailureRate, unresolvedProgressBlockers
}

// unknownProjectionScore is the conservative score quota_risk/context_risk
// falls back to when their upstream projection is nil (genuinely unknown,
// never a fabricated 0% per ADD principle 1 / §16.3's "不設 0" rule). ADD
// §16.2's sigmoid formula has no defined output for a missing input, so
// this package picks sigmoid(0) = 0.5 — the sigmoid's own midpoint,
// i.e. "neither confidently low nor confidently high risk" — as the most
// defensible score-shaped placeholder for "unknown", paired with an
// explicit QUOTA_UNKNOWN/CONTEXT_UNKNOWN reason code and (via the
// component's own Confidence, propagated from the unknown upstream
// forecast) never presented with unwarranted confidence.
const unknownProjectionScore = 0.5

// sigmoid is the standard logistic function, used by quota_risk/
// context_risk exactly as ADD §16.2 names it. Bounded output in (0,1) for
// every finite input; for extreme inputs (large |x|) math.Exp may
// overflow/underflow to +Inf/0, which still yields a mathematically
// correct 0 or 1 output (never NaN) because Go's IEEE-754 float64
// arithmetic defines 1/(1+Inf)=0 and 1/(1+0)=1.
func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

// clamp01 bounds v to [0,1], and defends against NaN/Inf ever escaping
// this package regardless of how an upstream value or formula term
// misbehaves (agents/predictor.md's required "no divide-by-zero/NaN/Inf"
// test). NaN compares false against every ordered comparison, so a NaN
// input falls through both bounds checks below; it is treated as the most
// conservative (highest-risk) case, 1.0, since a score computation that
// produced NaN represents a data problem in an upstream signal — the same
// direction quota_risk's "unknown is not zero" discipline uses (favor
// disclosing elevated risk over silently understating it) —  not a
// license to report a placid, unearned low score.
func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 1.0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ptrToFloat reads a *int64 quantile field as float64, treating nil
// (genuinely unknown, ADD principle 1) as 0 for formula-input purposes
// only — not as a claim that the underlying quantity is measured to be
// zero. This mirrors internal/predictor/token.scopeMultiplier's identical
// nil-contributes-0 discipline for the same reason: an *unpopulated*
// scope signal must not inflate a downstream formula, but the resulting
// risk score's own Confidence/ReasonCodes (propagated from
// scope.Calibrated/scope.Confidence/scope.ReasonCodes by this package's
// callers) still honestly disclose that the input was incomplete.
func ptrToFloat(p *int64) float64 {
	if p == nil {
		return 0
	}
	return float64(*p)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

func hasReason(reasons []domain.ReasonCode, want domain.ReasonCode) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// confidenceRank orders domain.Confidence from least to most trustworthy,
// so lowestConfidence can pick the most conservative of several inputs.
// domain.ConfidenceUnavailable ranks below ConfidenceLow: "unavailable" is
// a stronger disclosure of missing signal than "low but present".
var confidenceRank = map[domain.Confidence]int{
	domain.ConfidenceUnavailable: 0,
	domain.ConfidenceLow:         1,
	domain.ConfidenceMedium:      2,
	domain.ConfidenceHigh:        3,
	domain.ConfidenceExact:       4,
}

// lowestConfidence returns the least-trustworthy (most conservative) of
// its arguments, so a combined result never claims to be more certain
// than its least-certain contributing input. An empty/unrecognized
// Confidence value ranks alongside domain.ConfidenceUnavailable (rank 0,
// the most conservative treatment) rather than panicking or silently
// sorting as "high".
func lowestConfidence(confidences ...domain.Confidence) domain.Confidence {
	lowest := domain.ConfidenceHigh
	lowestRankValue := confidenceRank[lowest]
	found := false

	for _, c := range confidences {
		rank, ok := confidenceRank[c]
		if !ok {
			rank = confidenceRank[domain.ConfidenceUnavailable]
		}
		if !found || rank < lowestRankValue {
			lowest = c
			lowestRankValue = rank
			found = true
		}
	}
	if !found {
		return domain.ConfidenceLow
	}
	if _, ok := confidenceRank[lowest]; !ok {
		return domain.ConfidenceUnavailable
	}
	return lowest
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
