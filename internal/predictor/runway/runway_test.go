package runway

import (
	"math"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
)

func pctPtr(v float64) *float64 { return &v }

func obsAt(t time.Time, used float64) domain.QuotaObservation {
	return domain.QuotaObservation{
		ID:          "q-" + t.String(),
		LimitID:     "primary",
		UsedPercent: pctPtr(used),
		ObservedAt:  t,
	}
}

// assertRunwaySane checks the invariants every RunwayForecast produced by
// Score must satisfy regardless of input, mirroring the discipline used
// for predictor-04's quantile monotonicity tests: no NaN/Inf, RiskScore in
// [0,1], never Calibrated, never a populated HitProbability.
func assertRunwaySane(t *testing.T, label string, f domain.RunwayForecast) {
	t.Helper()
	if math.IsNaN(f.RiskScore) || math.IsInf(f.RiskScore, 0) {
		t.Fatalf("%s: RiskScore is NaN/Inf: %v", label, f.RiskScore)
	}
	if f.RiskScore < 0 || f.RiskScore > 1 {
		t.Fatalf("%s: RiskScore out of [0,1]: %v", label, f.RiskScore)
	}
	if f.Calibrated {
		t.Fatalf("%s: Calibrated must be false this wave (no durable calibrated history exists)", label)
	}
	if f.HitProbability != nil {
		t.Fatalf("%s: HitProbability must be nil while Calibrated is false (ADD principle 2 / Constitution §7 rule 7): got %v", label, *f.HitProbability)
	}
	if f.BurnRateP50 != nil && (math.IsNaN(*f.BurnRateP50) || math.IsInf(*f.BurnRateP50, 0)) {
		t.Fatalf("%s: BurnRateP50 is NaN/Inf", label)
	}
	if f.BurnRateP90 != nil && (math.IsNaN(*f.BurnRateP90) || math.IsInf(*f.BurnRateP90, 0)) {
		t.Fatalf("%s: BurnRateP90 is NaN/Inf", label)
	}
}

// TestScoreNeverCalibratedNeverPanics is a broad property-style sweep: for
// every combination of a handful of representative inputs, Score must
// never panic, never divide by zero, never produce NaN/Inf, and must
// always report an uncalibrated result. High-risk node (DAG: "a bad score
// risks false pause triggers") — breadth over depth here.
func TestScoreNeverCalibratedNeverPanics(t *testing.T) {
	base := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	usedValues := []float64{0, 1, 50, 94.9, 95, 99, 100, 150, -5}
	deltas := []float64{-10, -0.001, 0, 0.001, 5, 60, 1000}
	intervals := []time.Duration{0, time.Second, 2 * time.Second, time.Minute, 10 * time.Minute}

	for _, used := range usedValues {
		for _, delta := range deltas {
			for _, interval := range intervals {
				prevTime := base.Add(-interval)
				prevUsed := used - delta
				prev := obsAt(prevTime, prevUsed)
				cur := obsAt(base, used)
				cur.Reached = used >= 100

				req := ScoreRequest{
					Current:  cur,
					Previous: &prev,
					Now:      base,
				}
				got := (&Scorer{}).Score(req)
				assertRunwaySane(t, "sweep", got)
			}
		}
	}
}

func TestScoreColdStartNoPreviousSample(t *testing.T) {
	now := time.Now()
	req := ScoreRequest{
		Current: obsAt(now, 40),
		Now:     now,
	}
	got := NewScorer().Score(req)
	assertRunwaySane(t, "cold start", got)
	if got.SampleCount != 0 {
		t.Errorf("expected SampleCount=0 with no Previous sample, got %d", got.SampleCount)
	}
	if got.BurnRateP50 != nil || got.BurnRateP90 != nil {
		t.Errorf("expected nil burn rate with no Previous sample, got p50=%v p90=%v", got.BurnRateP50, got.BurnRateP90)
	}
	if !containsString(got.ReasonCodes, ReasonColdStart) {
		t.Errorf("expected %s in reason codes, got %v", ReasonColdStart, got.ReasonCodes)
	}
}

func TestScoreCurrentUsageUnknownFailsOpen(t *testing.T) {
	now := time.Now()
	req := ScoreRequest{
		Current: domain.QuotaObservation{LimitID: "primary", ObservedAt: now}, // UsedPercent nil
		Now:     now,
	}
	got := NewScorer().Score(req)
	if got.RiskScore != 0 {
		t.Errorf("expected RiskScore=0 (fail open, not a guess) when current usage is unknown, got %v", got.RiskScore)
	}
	if got.Confidence != domain.ConfidenceUnavailable {
		t.Errorf("expected ConfidenceUnavailable, got %v", got.Confidence)
	}
	if got.CurrentUsedPercent != nil {
		t.Errorf("expected CurrentUsedPercent to remain nil (unknown is not zero), got %v", *got.CurrentUsedPercent)
	}
}

func TestScoreReachedIsCriticalRegardlessOfBurnRate(t *testing.T) {
	now := time.Now()
	prev := obsAt(now.Add(-time.Minute), 10) // implies a huge burn rate if used
	cur := obsAt(now, 100)
	cur.Reached = true

	got := NewScorer().Score(ScoreRequest{Current: cur, Previous: &prev, Now: now})
	if got.RiskScore != 1.0 {
		t.Errorf("expected RiskScore=1.0 when Reached=true, got %v", got.RiskScore)
	}
	if got.EstimatedTimeToLimitP50Seconds == nil || *got.EstimatedTimeToLimitP50Seconds != 0 {
		t.Errorf("expected EstimatedTimeToLimitP50Seconds=0 when already reached, got %v", got.EstimatedTimeToLimitP50Seconds)
	}
}

func TestScoreNegativeDeltaTreatedAsResetNotNegativeBurnRate(t *testing.T) {
	now := time.Now()
	prev := obsAt(now.Add(-time.Minute), 80)
	cur := obsAt(now, 10) // usage dropped: a reset, not negative consumption
	got := NewScorer().Score(ScoreRequest{Current: cur, Previous: &prev, Now: now})

	if got.BurnRateP50 != nil {
		t.Errorf("expected nil burn rate after a negative-delta (reset) sample, got %v", *got.BurnRateP50)
	}
	if !containsString(got.ReasonCodes, ReasonNegativeDeltaOutlier) {
		t.Errorf("expected %s in reason codes, got %v", ReasonNegativeDeltaOutlier, got.ReasonCodes)
	}
	// Low current usage post-reset should score low risk, not high.
	if got.RiskScore > 0.3 {
		t.Errorf("expected low risk score after reset to 10%% usage, got %v", got.RiskScore)
	}
}

func TestScoreIntervalTooShortNotCountedTowardRate(t *testing.T) {
	now := time.Now()
	prev := obsAt(now.Add(-1*time.Second), 10) // 1s < minInterval (2s)
	cur := obsAt(now, 90)                      // would imply an enormous rate if counted
	got := NewScorer().Score(ScoreRequest{Current: cur, Previous: &prev, Now: now})

	if got.BurnRateP50 != nil {
		t.Errorf("expected nil burn rate for sub-2s interval, got %v", *got.BurnRateP50)
	}
	if !containsString(got.ReasonCodes, ReasonIntervalTooShort) {
		t.Errorf("expected %s in reason codes, got %v", ReasonIntervalTooShort, got.ReasonCodes)
	}
}

func TestScoreSanityCapMarksAnomalyAndDropsRate(t *testing.T) {
	now := time.Now()
	prev := obsAt(now.Add(-time.Minute), 0)
	cur := obsAt(now, 90) // 90 points/minute >> sanityCapPercentPerMinute (50)
	got := NewScorer().Score(ScoreRequest{Current: cur, Previous: &prev, Now: now})

	if got.BurnRateP50 != nil {
		t.Errorf("expected nil burn rate when sanity cap is exceeded, got %v", *got.BurnRateP50)
	}
	if !containsString(got.ReasonCodes, ReasonBurnRateAnomaly) {
		t.Errorf("expected %s in reason codes, got %v", ReasonBurnRateAnomaly, got.ReasonCodes)
	}
}

func TestScoreResetWithinHorizonLowersRisk(t *testing.T) {
	now := time.Now()
	prev := obsAt(now.Add(-time.Minute), 90)
	cur := obsAt(now, 98) // high usage, would otherwise be high risk
	resetAt := now.Add(5 * time.Minute)
	cur.ResetsAt = &resetAt

	got := NewScorer().Score(ScoreRequest{Current: cur, Previous: &prev, Now: now, Horizon: DefaultHorizon})
	if got.RiskScore >= 0.5 {
		t.Errorf("expected reduced risk when reset lands within horizon, got %v", got.RiskScore)
	}
	if !containsString(got.ReasonCodes, ReasonHeadroomAvailable) {
		t.Errorf("expected %s in reason codes, got %v", ReasonHeadroomAvailable, got.ReasonCodes)
	}
}

func TestScoreCriticalUsageThreshold(t *testing.T) {
	now := time.Now()
	req := ScoreRequest{Current: obsAt(now, 96), Now: now}
	got := NewScorer().Score(req)
	if got.RiskScore != 1.0 {
		t.Errorf("expected RiskScore=1.0 for current_used>=95%%, got %v", got.RiskScore)
	}
	if !containsString(got.ReasonCodes, ReasonCriticalUsage) {
		t.Errorf("expected %s in reason codes, got %v", ReasonCriticalUsage, got.ReasonCodes)
	}
}

func TestScoreProjectedExceedsLimitWithinHorizon(t *testing.T) {
	now := time.Now()
	prev := obsAt(now.Add(-time.Minute), 40)
	cur := obsAt(now, 50) // 10 points/minute burn rate
	got := NewScorer().Score(ScoreRequest{Current: cur, Previous: &prev, Now: now, Horizon: DefaultHorizon})

	// projected_used_p90 = 50 + 10*10min = 150 -> clamped/considered >=100
	if got.RiskScore < 0.8 {
		t.Errorf("expected high risk score when projection exceeds 100%% within horizon, got %v", got.RiskScore)
	}
	if !containsString(got.ReasonCodes, ReasonProjectedExceedsLimitWithinHorizon) {
		t.Errorf("expected %s in reason codes, got %v", ReasonProjectedExceedsLimitWithinHorizon, got.ReasonCodes)
	}
}

func TestScoreDeterministicForSameInput(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	prev := obsAt(now.Add(-time.Minute), 20)
	cur := obsAt(now, 35)

	scorer := NewScorer()
	first := scorer.Score(ScoreRequest{Current: cur, Previous: &prev, Now: now})
	second := scorer.Score(ScoreRequest{Current: cur, Previous: &prev, Now: now})

	if first.RiskScore != second.RiskScore {
		t.Fatalf("Score is not deterministic: first=%v second=%v", first.RiskScore, second.RiskScore)
	}
	if (first.BurnRateP50 == nil) != (second.BurnRateP50 == nil) {
		t.Fatalf("Score burn-rate nilness is not deterministic")
	}
	if first.BurnRateP50 != nil && *first.BurnRateP50 != *second.BurnRateP50 {
		t.Fatalf("Score burn rate is not deterministic: first=%v second=%v", *first.BurnRateP50, *second.BurnRateP50)
	}
}

func TestCombineWindowsTakesMax(t *testing.T) {
	low := domain.RunwayForecast{LimitID: "a", RiskScore: 0.2}
	high := domain.RunwayForecast{LimitID: "b", RiskScore: 0.9}
	mid := domain.RunwayForecast{LimitID: "c", RiskScore: 0.5}

	got := CombineWindows([]domain.RunwayForecast{low, high, mid})
	if got.LimitID != "b" || got.RiskScore != 0.9 {
		t.Fatalf("expected max(RiskScore) window (b, 0.9), got %+v", got)
	}
}

func TestCombineWindowsEmptyInput(t *testing.T) {
	got := CombineWindows(nil)
	if got.Confidence != domain.ConfidenceUnavailable {
		t.Fatalf("expected ConfidenceUnavailable for empty input, got %v", got.Confidence)
	}
	if got.Calibrated {
		t.Fatalf("expected Calibrated=false for empty input")
	}
}

func TestScoreHorizonSecondsReflectsRequestedHorizon(t *testing.T) {
	now := time.Now()
	got := NewScorer().Score(ScoreRequest{Current: obsAt(now, 10), Now: now, Horizon: 15 * time.Minute})
	if got.HorizonSeconds != 900 {
		t.Errorf("expected HorizonSeconds=900 for a 15-minute horizon, got %d", got.HorizonSeconds)
	}
}

func TestScoreDefaultHorizonIsTenMinutes(t *testing.T) {
	now := time.Now()
	got := NewScorer().Score(ScoreRequest{Current: obsAt(now, 10), Now: now})
	if got.HorizonSeconds != 600 {
		t.Errorf("expected default HorizonSeconds=600 (ADD §15.5), got %d", got.HorizonSeconds)
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
