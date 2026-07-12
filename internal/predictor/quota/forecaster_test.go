package quota

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int64) *int64     { return &v }
func ptrT(v time.Time) *time.Time {
	return &v
}

func containsReason(reasons []domain.ReasonCode, want domain.ReasonCode) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// assertBoundedPercent checks the invariants every projected percentage
// must satisfy regardless of input: no NaN/Inf, within [0, 100].
func assertBoundedPercent(t *testing.T, label string, p *float64) {
	t.Helper()
	if p == nil {
		return
	}
	if math.IsNaN(*p) || math.IsInf(*p, 0) {
		t.Fatalf("%s: NaN/Inf projected percent: %v", label, *p)
	}
	if *p < 0 || *p > 100 {
		t.Fatalf("%s: projected percent out of [0,100]: %v", label, *p)
	}
}

// --- TestQuotaForecastNeverCalibratedThisWave -------------------------------

func TestQuotaForecastNeverCalibratedThisWave(t *testing.T) {
	f := NewRuleQuotaForecaster()
	cases := []struct {
		name string
		req  app.ForecastQuotaRequest
	}{
		{"empty request", app.ForecastQuotaRequest{}},
		{"with quota observation", app.ForecastQuotaRequest{
			Quota: []domain.QuotaObservation{{UsedPercent: ptrF(40)}},
		}},
		{"with context observation", app.ForecastQuotaRequest{
			Context: domain.ContextObservation{UsedPercent: ptrF(30)},
		}},
		{"with token forecast fallback", app.ForecastQuotaRequest{
			TokenForecast: domain.TokenForecast{TokensP90: 12000},
			Quota:         []domain.QuotaObservation{{UsedPercent: ptrF(50)}},
			Context:       domain.ContextObservation{UsedPercent: ptrF(20)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.ForecastQuota(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("ForecastQuota: %v", err)
			}
			if got.Calibrated {
				t.Errorf("Calibrated = true, want false (cold-start-only this wave)")
			}
			if got.Confidence != domain.ConfidenceLow {
				t.Errorf("Confidence = %q, want %q", got.Confidence, domain.ConfidenceLow)
			}
			if !containsReason(got.ReasonCodes, domain.ReasonPredictionColdStart) {
				t.Errorf("ReasonCodes = %v, want to contain %q", got.ReasonCodes, domain.ReasonPredictionColdStart)
			}
		})
	}
}

// --- TestQuotaForecastUnknownWhenNoObservation -------------------------------

func TestQuotaForecastUnknownWhenNoObservation(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedQuotaUsedP90 != nil {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want nil (no quota observations supplied)", *got.ProjectedQuotaUsedP90)
	}
	if got.ProjectedContextUsedP90 != nil {
		t.Errorf("ProjectedContextUsedP90 = %v, want nil (no context observation supplied)", *got.ProjectedContextUsedP90)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonQuotaUnknown) {
		t.Errorf("ReasonCodes = %v, want to contain %q", got.ReasonCodes, domain.ReasonQuotaUnknown)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonContextUnknown) {
		t.Errorf("ReasonCodes = %v, want to contain %q", got.ReasonCodes, domain.ReasonContextUnknown)
	}
}

// TestQuotaForecastNilUsedPercentIsUnknown checks that a QuotaObservation
// with a nil UsedPercent (measurement present but value unknown) is never
// silently treated as 0% used (ADD principle 1, "unknown is not zero").
func TestQuotaForecastNilUsedPercentIsUnknown(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{{LimitID: "five_hour", UsedPercent: nil}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedQuotaUsedP90 != nil {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want nil for an observation with unknown UsedPercent", *got.ProjectedQuotaUsedP90)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonQuotaUnknown) {
		t.Errorf("ReasonCodes = %v, want to contain %q", got.ReasonCodes, domain.ReasonQuotaUnknown)
	}
}

// --- TestQuotaForecastProjectsForward ---------------------------------------

func TestQuotaForecastProjectsForwardFromCurrentUsage(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{{LimitID: "five_hour", UsedPercent: ptrF(40)}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedQuotaUsedP90 == nil {
		t.Fatal("ProjectedQuotaUsedP90 = nil, want a projected value")
	}
	// Per ADD §15.3: projected_used_p90 = current_used_percent + predicted_delta_p90.
	// The default delta is strictly positive (coldstart.go), so the
	// projection must never be below the current usage.
	if *got.ProjectedQuotaUsedP90 < 40 {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want >= current usage (40)", *got.ProjectedQuotaUsedP90)
	}
	assertBoundedPercent(t, "quota", got.ProjectedQuotaUsedP90)
}

func TestContextForecastProjectsForwardFromCurrentUsage(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Context: domain.ContextObservation{UsedPercent: ptrF(25)},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedContextUsedP90 == nil {
		t.Fatal("ProjectedContextUsedP90 = nil, want a projected value")
	}
	if *got.ProjectedContextUsedP90 < 25 {
		t.Errorf("ProjectedContextUsedP90 = %v, want >= current usage (25)", *got.ProjectedContextUsedP90)
	}
	assertBoundedPercent(t, "context", got.ProjectedContextUsedP90)
}

// TestContextForecastFallsBackToTokenRatio checks ContextObservation's
// UsedTokens/WindowTokens fallback path when UsedPercent itself is nil.
func TestContextForecastFallsBackToTokenRatio(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Context: domain.ContextObservation{
			UsedTokens:   ptrI(50000),
			WindowTokens: ptrI(200000),
		},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedContextUsedP90 == nil {
		t.Fatal("ProjectedContextUsedP90 = nil, want a projected value derived from UsedTokens/WindowTokens")
	}
	// current = 50000/200000 = 25%
	if *got.ProjectedContextUsedP90 < 25 {
		t.Errorf("ProjectedContextUsedP90 = %v, want >= 25 (current ratio)", *got.ProjectedContextUsedP90)
	}
}

// TestContextForecastZeroWindowTokensIsUnknown guards the WindowTokens==0
// division-by-zero case explicitly.
func TestContextForecastZeroWindowTokensIsUnknown(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Context: domain.ContextObservation{
			UsedTokens:   ptrI(1000),
			WindowTokens: ptrI(0),
		},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedContextUsedP90 != nil {
		t.Errorf("ProjectedContextUsedP90 = %v, want nil when WindowTokens is 0", *got.ProjectedContextUsedP90)
	}
	assertBoundedPercent(t, "context", got.ProjectedContextUsedP90)
}

// --- TestQuotaForecastNearLimitReasonCode -----------------------------------

func TestQuotaForecastNearLimitReasonCode(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{{LimitID: "five_hour", UsedPercent: ptrF(95)}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonQuotaNearLimit) {
		t.Errorf("ReasonCodes = %v, want to contain %q at 95%% usage", got.ReasonCodes, domain.ReasonQuotaNearLimit)
	}
	// 95% is already at/above 100 minus a small default delta; must clamp, never exceed 100.
	if *got.ProjectedQuotaUsedP90 > 100 {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want <= 100", *got.ProjectedQuotaUsedP90)
	}
}

func TestQuotaForecastReachedFlagAlwaysNearLimit(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		// Low usage but the provider has flagged the limit as reached
		// (e.g. a burst limit distinct from the rolling percentage).
		Quota: []domain.QuotaObservation{{LimitID: "burst", UsedPercent: ptrF(10), Reached: true}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonQuotaNearLimit) {
		t.Errorf("ReasonCodes = %v, want to contain %q when Reached=true regardless of UsedPercent", got.ReasonCodes, domain.ReasonQuotaNearLimit)
	}
}

func TestContextForecastNearLimitReasonCode(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Context: domain.ContextObservation{UsedPercent: ptrF(92)},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonContextNearLimit) {
		t.Errorf("ReasonCodes = %v, want to contain %q at 92%% usage", got.ReasonCodes, domain.ReasonContextNearLimit)
	}
}

// --- TestQuotaForecastMultiWindowTakesWorst ---------------------------------

func TestQuotaForecastMultiWindowTakesWorst(t *testing.T) {
	f := NewRuleQuotaForecaster()
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{
			{LimitID: "five_hour", UsedPercent: ptrF(10)},
			{LimitID: "weekly", UsedPercent: ptrF(70)},
		},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedQuotaUsedP90 == nil {
		t.Fatal("ProjectedQuotaUsedP90 = nil, want a projected value")
	}
	// The 70%-usage window must drive the combined projection (max across
	// windows, ADD §15.5's v1 default reused here), not the 10% one.
	if *got.ProjectedQuotaUsedP90 < 70 {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want >= 70 (worst window must dominate)", *got.ProjectedQuotaUsedP90)
	}
}

// --- TestQuotaForecastResetSoonSuppressesDelta ------------------------------

func TestQuotaForecastResetSoonSuppressesDelta(t *testing.T) {
	f := NewRuleQuotaForecaster()
	resetsAt := time.Now().Add(1 * time.Minute)
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{{
			LimitID:     "five_hour",
			UsedPercent: ptrF(60),
			ResetsAt:    ptrT(resetsAt),
		}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedQuotaUsedP90 == nil {
		t.Fatal("ProjectedQuotaUsedP90 = nil, want a projected value")
	}
	// Reset imminent (within turnHorizon): delta must be suppressed, so
	// projection stays at current usage rather than compounding forward.
	if *got.ProjectedQuotaUsedP90 != 60 {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want == 60 (delta suppressed, reset imminent)", *got.ProjectedQuotaUsedP90)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonQuotaResetSoon) {
		t.Errorf("ReasonCodes = %v, want to contain %q", got.ReasonCodes, domain.ReasonQuotaResetSoon)
	}
}

func TestQuotaForecastResetFarAwayAppliesDelta(t *testing.T) {
	f := NewRuleQuotaForecaster()
	resetsAt := time.Now().Add(4 * time.Hour)
	got, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{{
			LimitID:     "five_hour",
			UsedPercent: ptrF(60),
			ResetsAt:    ptrT(resetsAt),
		}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if got.ProjectedQuotaUsedP90 == nil {
		t.Fatal("ProjectedQuotaUsedP90 = nil, want a projected value")
	}
	if *got.ProjectedQuotaUsedP90 <= 60 {
		t.Errorf("ProjectedQuotaUsedP90 = %v, want > 60 (reset far away, delta should apply)", *got.ProjectedQuotaUsedP90)
	}
	if containsReason(got.ReasonCodes, domain.ReasonQuotaResetSoon) {
		t.Errorf("ReasonCodes = %v, want NOT to contain %q (reset is 4h away)", got.ReasonCodes, domain.ReasonQuotaResetSoon)
	}
}

// --- TestQuotaForecastTokenForecastScalesDelta ------------------------------

func TestQuotaForecastTokenForecastScalesDelta(t *testing.T) {
	f := NewRuleQuotaForecaster()

	small, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		TokenForecast: domain.TokenForecast{TokensP90: 100},
		Quota:         []domain.QuotaObservation{{UsedPercent: ptrF(40)}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota (small): %v", err)
	}
	large, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		TokenForecast: domain.TokenForecast{TokensP90: 100_000},
		Quota:         []domain.QuotaObservation{{UsedPercent: ptrF(40)}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota (large): %v", err)
	}
	if !(*small.ProjectedQuotaUsedP90 < *large.ProjectedQuotaUsedP90) {
		t.Errorf("small-forecast projection (%v) should be < large-forecast projection (%v)",
			*small.ProjectedQuotaUsedP90, *large.ProjectedQuotaUsedP90)
	}
	assertBoundedPercent(t, "quota-small", small.ProjectedQuotaUsedP90)
	assertBoundedPercent(t, "quota-large", large.ProjectedQuotaUsedP90)
}

func TestQuotaForecastZeroTokenForecastUsesUnscaledDefault(t *testing.T) {
	f := NewRuleQuotaForecaster()
	withZero, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		TokenForecast: domain.TokenForecast{}, // zero-value, no forecast supplied
		Quota:         []domain.QuotaObservation{{UsedPercent: ptrF(40)}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	without, err := f.ForecastQuota(context.Background(), app.ForecastQuotaRequest{
		Quota: []domain.QuotaObservation{{UsedPercent: ptrF(40)}},
	})
	if err != nil {
		t.Fatalf("ForecastQuota: %v", err)
	}
	if *withZero.ProjectedQuotaUsedP90 != *without.ProjectedQuotaUsedP90 {
		t.Errorf("zero-value TokenForecast should behave identically to an absent one: %v vs %v",
			*withZero.ProjectedQuotaUsedP90, *without.ProjectedQuotaUsedP90)
	}
}

// --- TestQuotaForecastDeterministic -----------------------------------------

func TestQuotaForecastDeterministic(t *testing.T) {
	f := NewRuleQuotaForecaster()
	req := app.ForecastQuotaRequest{
		TokenForecast: domain.TokenForecast{TokensP90: 9000},
		Quota: []domain.QuotaObservation{
			{LimitID: "five_hour", UsedPercent: ptrF(35)},
			{LimitID: "weekly", UsedPercent: ptrF(55)},
		},
		Context: domain.ContextObservation{UsedPercent: ptrF(45)},
	}

	first, err := f.ForecastQuota(context.Background(), req)
	if err != nil {
		t.Fatalf("ForecastQuota (first): %v", err)
	}
	second, err := f.ForecastQuota(context.Background(), req)
	if err != nil {
		t.Fatalf("ForecastQuota (second): %v", err)
	}

	if *first.ProjectedQuotaUsedP90 != *second.ProjectedQuotaUsedP90 {
		t.Errorf("ProjectedQuotaUsedP90 not deterministic: %v vs %v", *first.ProjectedQuotaUsedP90, *second.ProjectedQuotaUsedP90)
	}
	if *first.ProjectedContextUsedP90 != *second.ProjectedContextUsedP90 {
		t.Errorf("ProjectedContextUsedP90 not deterministic: %v vs %v", *first.ProjectedContextUsedP90, *second.ProjectedContextUsedP90)
	}
}

// --- TestQuotaForecastNeverPanicsOnDegenerateInputs -------------------------

func TestQuotaForecastNeverPanicsOnDegenerateInputs(t *testing.T) {
	f := NewRuleQuotaForecaster()
	cases := []app.ForecastQuotaRequest{
		{},
		{Quota: []domain.QuotaObservation{{}}},
		{Quota: []domain.QuotaObservation{{UsedPercent: ptrF(-50)}}},
		{Quota: []domain.QuotaObservation{{UsedPercent: ptrF(1e9)}}},
		{Quota: []domain.QuotaObservation{{UsedPercent: ptrF(0), ResetsAt: ptrT(time.Now().Add(-1 * time.Hour))}}},
		{Context: domain.ContextObservation{UsedTokens: ptrI(-100), WindowTokens: ptrI(-1)}},
		{TokenForecast: domain.TokenForecast{TokensP90: -1}},
		{TokenForecast: domain.TokenForecast{TokensP90: math.MaxInt64}},
		{
			Quota: []domain.QuotaObservation{
				{UsedPercent: ptrF(50)},
				{UsedPercent: nil},
				{UsedPercent: ptrF(200)},
			},
			Context: domain.ContextObservation{UsedPercent: ptrF(-10)},
		},
	}
	for i, req := range cases {
		got, err := f.ForecastQuota(context.Background(), req)
		if err != nil {
			t.Fatalf("case %d: ForecastQuota returned error: %v", i, err)
		}
		assertBoundedPercent(t, "quota", got.ProjectedQuotaUsedP90)
		assertBoundedPercent(t, "context", got.ProjectedContextUsedP90)
	}
}
