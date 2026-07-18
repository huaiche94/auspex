package token

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
)

// fakeSource is a minimal, fully-controllable FeatureSource for tests.
type fakeSource struct {
	class    features.Classification
	prompt   features.PromptFeatures
	classErr error

	sess    features.SessionFeatures
	sessOK  bool
	sessErr error

	prog    features.ProgressFeatures
	progOK  bool
	progErr error

	similar     []float64
	similarRung features.SimilarTurnCohortRung
	similarErr  error
}

func (f fakeSource) Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	return f.class, f.prompt, f.classErr
}

func (f fakeSource) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return f.sess, f.sessOK, f.sessErr
}

func (f fakeSource) Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error) {
	return f.prog, f.progOK, f.progErr
}

func (f fakeSource) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) (features.SimilarTurnTokens, error) {
	return features.SimilarTurnTokens{Samples: f.similar, Rung: f.similarRung}, f.similarErr
}

var _ FeatureSource = fakeSource{}

func baseReq(scope domain.ScopeEstimate) app.ForecastTokensRequest {
	return app.ForecastTokensRequest{
		SessionID: domain.SessionID("sess-1"),
		Scope:     scope,
	}
}

func ptr(v int64) *int64 { return &v }

func minimalScope() domain.ScopeEstimate {
	return domain.ScopeEstimate{
		FilesChangedP50: ptr(2),
		FilesChangedP90: ptr(6),
		LinesChangedP50: ptr(70),
		LinesChangedP90: ptr(280),
	}
}

// assertForecastSane checks the invariants that must hold for every
// result, regardless of input: no NaN/Inf, non-negative, and monotonic
// P50 <= P80 <= P90 (mirrors predictor.Quantiles' own guarantee).
func assertForecastSane(t *testing.T, label string, tf domain.TokenForecast) {
	t.Helper()
	for _, v := range []struct {
		name string
		val  int64
	}{{"P50", tf.TokensP50}, {"P80", tf.TokensP80}, {"P90", tf.TokensP90}} {
		if v.val < 0 {
			t.Fatalf("%s: %s is negative: %d", label, v.name, v.val)
		}
	}
	if tf.TokensP50 > tf.TokensP80 {
		t.Fatalf("%s: monotonicity violated: P50=%d > P80=%d", label, tf.TokensP50, tf.TokensP80)
	}
	if tf.TokensP80 > tf.TokensP90 {
		t.Fatalf("%s: monotonicity violated: P80=%d > P90=%d", label, tf.TokensP80, tf.TokensP90)
	}
	assertSplitSane(t, label, tf)
}

// assertSplitSane checks the #65 input/output decomposition invariants that
// must hold whenever the split is populated: all four bounds present and
// non-negative, each axis monotonic (P50 <= P90), the two P50s partition the
// total P50 (no central mass lost or invented), and the INPUT interval
// structurally WIDER than the output interval (ADR-0053).
func assertSplitSane(t *testing.T, label string, tf domain.TokenForecast) {
	t.Helper()
	present := []*int64{tf.InputTokensP50, tf.InputTokensP90, tf.OutputTokensP50, tf.OutputTokensP90}
	anyNil := false
	for _, p := range present {
		if p == nil {
			anyNil = true
		}
	}
	if anyNil {
		// A forecaster that does not split leaves ALL four nil; a partial
		// split would be a bug (a fabricated half-axis). Enforce all-or-none.
		for _, p := range present {
			if p != nil {
				t.Fatalf("%s: partial input/output split (some axes nil, some set)", label)
			}
		}
		return
	}
	inP50, inP90 := *tf.InputTokensP50, *tf.InputTokensP90
	outP50, outP90 := *tf.OutputTokensP50, *tf.OutputTokensP90
	for name, v := range map[string]int64{"inputP50": inP50, "inputP90": inP90, "outputP50": outP50, "outputP90": outP90} {
		if v < 0 {
			t.Fatalf("%s: split %s is negative: %d", label, name, v)
		}
	}
	if inP50 > inP90 {
		t.Fatalf("%s: input monotonicity violated: P50=%d > P90=%d", label, inP50, inP90)
	}
	if outP50 > outP90 {
		t.Fatalf("%s: output monotonicity violated: P50=%d > P90=%d", label, outP50, outP90)
	}
	// Central mass partitions the total P50 (allow +-1 for independent
	// rounding of each half).
	if sum := inP50 + outP50; sum < tf.TokensP50-1 || sum > tf.TokensP50+1 {
		t.Fatalf("%s: input P50 + output P50 = %d does not partition total P50 = %d", label, sum, tf.TokensP50)
	}
	// The whole point of the split: the input interval must be wider than
	// the output interval. Compare absolute widths (equal centers this wave)
	// so the check holds even if a future share makes the centers differ.
	inWidth, outWidth := inP90-inP50, outP90-outP50
	if tf.TokensP50 > 0 && inWidth <= outWidth {
		t.Fatalf("%s: input interval (width %d) is not wider than output interval (width %d)", label, inWidth, outWidth)
	}
}

func TestTokenForecastMonotonicity(t *testing.T) {
	cases := []struct {
		name   string
		source fakeSource
		scope  domain.ScopeEstimate
	}{
		{
			name:   "cold start, unknown class, empty scope",
			source: fakeSource{class: features.Classification{Class: features.TaskClassUnknown, Confidence: domain.ConfidenceUnavailable}},
			scope:  domain.ScopeEstimate{},
		},
		{
			name:   "documentation-short, cold start",
			source: fakeSource{class: features.Classification{Class: features.TaskClassDocumentationShort, Confidence: domain.ConfidenceLow}},
			scope:  minimalScope(),
		},
		{
			name:   "repository-wide, large scope",
			source: fakeSource{class: features.Classification{Class: features.TaskClassRepositoryWide, Confidence: domain.ConfidenceLow}},
			scope: domain.ScopeEstimate{
				FilesChangedP50:     ptr(20),
				FilesChangedP90:     ptr(60),
				LinesChangedP50:     ptr(1000),
				LinesChangedP90:     ptr(6000),
				RequiresUnitTests:   true,
				RequiresIntegration: true,
				CrossProject:        true,
				MigrationLikely:     true,
				SecuritySensitive:   true,
			},
		},
		{
			name: "migration with retry and progress signal",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassMigration, Confidence: domain.ConfidenceLow},
				sess:   sessionWithRetry(0.5),
				sessOK: true,
				prog:   progressWith(0.1, 25),
				progOK: true,
			},
			scope: minimalScope(),
		},
		{
			name: "open-ended prompt",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassFeatureLocal, Confidence: domain.ConfidenceLow},
				prompt: features.PromptFeatures{OpenEndedIndicator: true},
			},
			scope: minimalScope(),
		},
		{
			name: "explicit files and acceptance criteria named",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassFeatureLocal, Confidence: domain.ConfidenceLow},
				prompt: features.PromptFeatures{ExplicitPathCount: 3, AcceptanceCriteriaCount: 2},
			},
			scope: minimalScope(),
		},
		{
			name: "empirical base from >= 8 similar samples",
			source: fakeSource{
				class:   features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
				similar: []float64{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000, 9000},
			},
			scope: minimalScope(),
		},
		{
			name:   "nil scope pointers (fully unknown scope)",
			source: fakeSource{class: features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow}},
			scope:  domain.ScopeEstimate{},
		},
		{
			name: "negative retry rate and out-of-range completed ratio guarded",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
				sess:   sessionWithRetry(-5),
				sessOK: true,
				prog:   progressWith(5, 3), // > 1, must clamp
				progOK: true,
			},
			scope: minimalScope(),
		},
	}

	f := NewRuleTokenForecaster(nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.Source = tc.source
			got, err := f.ForecastTokens(context.Background(), baseReq(tc.scope))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertForecastSane(t, tc.name, got)
		})
	}
}

func TestTokenForecastNeverCalibratedThisWave(t *testing.T) {
	// Cold-start-only contract for this wave: no durable historical
	// telemetry store exists yet (agents/predictor.md cold-start
	// contract), so Calibrated must always be false and Confidence must
	// never exceed ConfidenceMedium (reached only via the >=8-sample
	// empirical-base branch, still not a calibrated probability).
	cases := []struct {
		name    string
		similar []float64
	}{
		{"cold start (no samples)", nil},
		{"below sample gate (7 samples)", []float64{1, 2, 3, 4, 5, 6, 7}},
		{"at sample gate (8 samples)", []float64{1, 2, 3, 4, 5, 6, 7, 8}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fakeSource{
				class:   features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
				similar: tc.similar,
			}
			f := NewRuleTokenForecaster(source)
			got, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Calibrated {
				t.Errorf("expected Calibrated=false, got true")
			}
			if got.Confidence != domain.ConfidenceLow && got.Confidence != domain.ConfidenceMedium {
				t.Errorf("expected Confidence in {low, medium}, got %s", got.Confidence)
			}
		})
	}
}

func TestTokenForecastColdStartReasonCode(t *testing.T) {
	source := fakeSource{class: features.Classification{Class: features.TaskClassUnknown, Confidence: domain.ConfidenceUnavailable}}
	f := NewRuleTokenForecaster(source)
	got, err := f.ForecastTokens(context.Background(), baseReq(domain.ScopeEstimate{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsReason(got.ReasonCodes, domain.ReasonPredictionColdStart) {
		t.Errorf("expected %s in reason codes, got %v", domain.ReasonPredictionColdStart, got.ReasonCodes)
	}
}

func TestTokenForecastCohortRungReasonCodes(t *testing.T) {
	// #20 Phase 1: an empirical base must say WHICH ladder rung supplied
	// its samples, and exactly one cohort code may appear. An unknown
	// rung vocabulary maps to the session-only code (the most
	// conservative claim).
	eight := []float64{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000}
	cohortCodes := []domain.ReasonCode{
		domain.ReasonTokenCohortModelEffort,
		domain.ReasonTokenCohortModelFamily,
		domain.ReasonTokenCohortProviderOnly,
		domain.ReasonTokenCohortSessionOnly,
	}
	cases := []struct {
		rung features.SimilarTurnCohortRung
		want domain.ReasonCode
	}{
		{features.CohortRungModelEffort, domain.ReasonTokenCohortModelEffort},
		{features.CohortRungModelFamily, domain.ReasonTokenCohortModelFamily},
		{features.CohortRungProvider, domain.ReasonTokenCohortProviderOnly},
		{features.CohortRungSession, domain.ReasonTokenCohortSessionOnly},
		{features.SimilarTurnCohortRung("some-future-rung"), domain.ReasonTokenCohortSessionOnly},
	}
	for _, tc := range cases {
		t.Run(string(tc.rung), func(t *testing.T) {
			source := fakeSource{
				class:       features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
				similar:     eight,
				similarRung: tc.rung,
			}
			f := NewRuleTokenForecaster(source)
			got, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !containsReason(got.ReasonCodes, tc.want) {
				t.Errorf("expected %s in reason codes, got %v", tc.want, got.ReasonCodes)
			}
			for _, code := range cohortCodes {
				if code != tc.want && containsReason(got.ReasonCodes, code) {
					t.Errorf("unexpected extra cohort code %s in %v", code, got.ReasonCodes)
				}
			}
		})
	}

	t.Run("cold start carries no cohort code", func(t *testing.T) {
		source := fakeSource{
			class:       features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
			similar:     []float64{1, 2, 3}, // below the gate
			similarRung: features.CohortRungModelEffort,
		}
		f := NewRuleTokenForecaster(source)
		got, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, code := range cohortCodes {
			if containsReason(got.ReasonCodes, code) {
				t.Errorf("cohort code %s must not appear on a cold-start forecast, got %v", code, got.ReasonCodes)
			}
		}
	})
}

func TestTokenForecastDeterministic(t *testing.T) {
	source := fakeSource{
		class:   features.Classification{Class: features.TaskClassFeatureCrossLayer, Confidence: domain.ConfidenceLow},
		prompt:  features.PromptFeatures{ExplicitPathCount: 3, AcceptanceCriteriaCount: 1},
		sess:    sessionWithRetry(0.2),
		sessOK:  true,
		prog:    progressWith(0.4, 4),
		progOK:  true,
		similar: []float64{1000, 1200, 1400, 1600, 1800, 2000, 2200, 2400, 2600},
	}
	f := NewRuleTokenForecaster(source)
	req := baseReq(minimalScope())

	first, err := f.ForecastTokens(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := f.ForecastTokens(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.TokensP50 != second.TokensP50 ||
		first.TokensP80 != second.TokensP80 ||
		first.TokensP90 != second.TokensP90 ||
		first.Calibrated != second.Calibrated ||
		first.Confidence != second.Confidence ||
		!reasonsEqual(first.ReasonCodes, second.ReasonCodes) {
		t.Fatalf("ForecastTokens is not deterministic for identical input: first=%+v second=%+v", first, second)
	}
}

func TestTokenForecastPropagatesSourceErrors(t *testing.T) {
	wantErr := errors.New("boom")

	t.Run("classification error", func(t *testing.T) {
		f := NewRuleTokenForecaster(fakeSource{classErr: wantErr})
		_, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected error to propagate, got %v", err)
		}
	})

	t.Run("session error", func(t *testing.T) {
		f := NewRuleTokenForecaster(fakeSource{
			class:   features.Classification{Class: features.TaskClassBugfixLocal},
			sessErr: wantErr,
		})
		_, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected error to propagate, got %v", err)
		}
	})

	t.Run("progress error", func(t *testing.T) {
		f := NewRuleTokenForecaster(fakeSource{
			class:   features.Classification{Class: features.TaskClassBugfixLocal},
			progErr: wantErr,
		})
		_, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected error to propagate, got %v", err)
		}
	})

	t.Run("similar-samples error", func(t *testing.T) {
		f := NewRuleTokenForecaster(fakeSource{
			class:      features.Classification{Class: features.TaskClassBugfixLocal},
			similarErr: wantErr,
		})
		_, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected error to propagate, got %v", err)
		}
	})
}

// TestMultiplierCapsPreventExplosion feeds an intentionally extreme scope
// (every complexity/verification flag set, huge file/line counts, high
// retry rate, deep critical path, open-ended prompt) and asserts the
// combined multiplier never blows past ADD §15.2's "avoid multiplier
// explosion" instruction: the resulting forecast must stay within a sane
// bound relative to the cold-start base, not scale unboundedly.
func TestTokenForecastMultiplierCapsPreventExplosion(t *testing.T) {
	source := fakeSource{
		class:  features.Classification{Class: features.TaskClassRepositoryWide, Confidence: domain.ConfidenceLow},
		prompt: features.PromptFeatures{OpenEndedIndicator: true},
		sess:   sessionWithRetry(100), // absurd input, must be clamped
		sessOK: true,
		prog:   progressWith(-100, 999), // absurd input, must be clamped
		progOK: true,
	}
	extremeScope := domain.ScopeEstimate{
		FilesChangedP50:     ptr(1_000_000),
		FilesChangedP90:     ptr(10_000_000),
		LinesChangedP50:     ptr(100_000_000),
		LinesChangedP90:     ptr(1_000_000_000),
		RequiresUnitTests:   true,
		RequiresIntegration: true,
		CrossProject:        true,
		MigrationLikely:     true,
		SecuritySensitive:   true,
	}
	f := NewRuleTokenForecaster(source)
	got, err := f.ForecastTokens(context.Background(), baseReq(extremeScope))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertForecastSane(t, "extreme scope", got)

	// Cold-start base for repository-wide is baseTurnTokens * 3.5 = 21000,
	// P90 = 42000. Even with every multiplier maxed and combined via
	// geometric mean capped at combinedCap (6.0), P90 must not exceed
	// base_p90 * combinedCap.
	maxSanePvNinety := int64(baseTurnTokens * 3.5 * 2.0 * combinedCap)
	if got.TokensP90 > maxSanePvNinety {
		t.Errorf("multiplier explosion: TokensP90=%d exceeds cap-derived bound %d", got.TokensP90, maxSanePvNinety)
	}
}

// TestZeroAndNegativeSimilarSamplesNeverPanic exercises degenerate
// RecentSimilarTurnTokens inputs (empty, single value, all zeros,
// negative values from a corrupt source) to ensure no divide-by-zero,
// NaN, Inf, or panic — mirrors predictor-04's own quantile-utility
// degenerate-input discipline.
func TestTokenForecastZeroAndNegativeSimilarSamplesNeverPanic(t *testing.T) {
	cases := [][]float64{
		nil,
		{},
		{0, 0, 0, 0, 0, 0, 0, 0},
		{-100, -50, 0, 50, 100, 200, 300, 400},
		{5, 5, 5, 5, 5, 5, 5, 5},
	}
	for i, samples := range cases {
		source := fakeSource{
			class:   features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
			similar: samples,
		}
		f := NewRuleTokenForecaster(source)
		got, err := f.ForecastTokens(context.Background(), baseReq(minimalScope()))
		if err != nil {
			t.Fatalf("case %d: unexpected error: %v", i, err)
		}
		assertForecastSane(t, "degenerate samples", got)
	}
}

// TestTokenForecastInputOutputSplit pins the #65 Phase-1 decomposition
// (ADR-0053): every forecast carries a populated input/output split, the
// input interval is structurally wider than the output interval, and the
// split never flips the forecast to calibrated.
func TestTokenForecastInputOutputSplit(t *testing.T) {
	cases := []struct {
		name   string
		source fakeSource
		scope  domain.ScopeEstimate
	}{
		{
			name:   "cold start bugfix-local",
			source: fakeSource{class: features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow}},
			scope:  minimalScope(),
		},
		{
			name:   "empirical base (>=8 samples)",
			source: fakeSource{class: features.Classification{Class: features.TaskClassFeatureLocal, Confidence: domain.ConfidenceLow}, similar: []float64{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000}, similarRung: features.CohortRungModelEffort},
			scope:  minimalScope(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewRuleTokenForecaster(tc.source)
			got, err := f.ForecastTokens(context.Background(), baseReq(tc.scope))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// The split must be populated (all four bounds present) whenever
			// the total is meaningful.
			if got.InputTokensP50 == nil || got.InputTokensP90 == nil ||
				got.OutputTokensP50 == nil || got.OutputTokensP90 == nil {
				t.Fatalf("expected populated input/output split, got %+v", got)
			}
			// assertForecastSane already enforces the width/partition
			// invariants; assert the direction explicitly here too so the
			// intent (input strictly wider) is pinned by name.
			assertForecastSane(t, tc.name, got)
			inWidth := *got.InputTokensP90 - *got.InputTokensP50
			outWidth := *got.OutputTokensP90 - *got.OutputTokensP50
			if inWidth <= outWidth {
				t.Fatalf("input interval width %d must exceed output interval width %d", inWidth, outWidth)
			}
			// The split is uncalibrated: it must never claim calibration.
			if got.Calibrated {
				t.Fatalf("split forecast must not be calibrated this wave")
			}
		})
	}
}

func reasonsEqual(a, b []domain.ReasonCode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsReason(reasons []domain.ReasonCode, target domain.ReasonCode) bool {
	for _, r := range reasons {
		if r == target {
			return true
		}
	}
	return false
}

func sessionWithRetry(rate float64) features.SessionFeatures {
	return features.SessionFeatures{RetryRate: &rate}
}

func progressWith(completedRatio float64, criticalPathLength int) features.ProgressFeatures {
	return features.ProgressFeatures{CompletedRatio: &completedRatio, CriticalPathLength: criticalPathLength}
}
