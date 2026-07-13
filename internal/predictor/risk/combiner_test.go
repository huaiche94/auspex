package risk

import (
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int64) *int64     { return &v }

func containsReason(reasons []domain.ReasonCode, want domain.ReasonCode) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

func assertFiniteUnitScore(t *testing.T, label string, c domain.RiskComponent) {
	t.Helper()
	if math.IsNaN(c.Score) || math.IsInf(c.Score, 0) {
		t.Fatalf("%s: NaN/Inf score: %v", label, c.Score)
	}
	if c.Score < 0 || c.Score > 1 {
		t.Fatalf("%s: score out of [0,1]: %v", label, c.Score)
	}
}

// --- TestRiskComponentsQuotaAndContext ---------------------------------

func TestRiskComponentsQuotaAndContext(t *testing.T) {
	c := NewRuleRiskCombiner()

	t.Run("high projected quota yields high quota risk", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   ptrF(95),
				ProjectedContextUsedP90: ptrF(10),
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		assertFiniteUnitScore(t, "quota", got.QuotaRisk)
		if got.QuotaRisk.Score <= 0.5 {
			t.Errorf("QuotaRisk.Score = %v, want > 0.5 for projected 95%%", got.QuotaRisk.Score)
		}
		if got.ContextRisk.Score >= 0.5 {
			t.Errorf("ContextRisk.Score = %v, want < 0.5 for projected 10%%", got.ContextRisk.Score)
		}
	})

	t.Run("projected exactly at midpoint yields 0.5", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   ptrF(85),
				ProjectedContextUsedP90: ptrF(85),
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if math.Abs(got.QuotaRisk.Score-0.5) > 1e-9 {
			t.Errorf("QuotaRisk.Score = %v, want 0.5 at midpoint", got.QuotaRisk.Score)
		}
		if math.Abs(got.ContextRisk.Score-0.5) > 1e-9 {
			t.Errorf("ContextRisk.Score = %v, want 0.5 at midpoint", got.ContextRisk.Score)
		}
	})

	t.Run("nil quota projection is unknown, not zero", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   nil,
				ProjectedContextUsedP90: ptrF(20),
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if !containsReason(got.QuotaRisk.ReasonCodes, domain.ReasonQuotaUnknown) {
			t.Errorf("QuotaRisk.ReasonCodes = %v, want to contain QUOTA_UNKNOWN", got.QuotaRisk.ReasonCodes)
		}
		// Unknown must not silently become 0 (which would read as "no risk").
		if got.QuotaRisk.Score == 0 {
			t.Errorf("QuotaRisk.Score = 0 for unknown projection, want a non-zero conservative placeholder")
		}
	})

	t.Run("nil context projection is unknown, not zero", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   ptrF(20),
				ProjectedContextUsedP90: nil,
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if !containsReason(got.ContextRisk.ReasonCodes, domain.ReasonContextUnknown) {
			t.Errorf("ContextRisk.ReasonCodes = %v, want to contain CONTEXT_UNKNOWN", got.ContextRisk.ReasonCodes)
		}
		if got.ContextRisk.Score == 0 {
			t.Errorf("ContextRisk.Score = 0 for unknown projection, want a non-zero conservative placeholder")
		}
	})

	t.Run("quota and context risk both come from the same QuotaForecast", func(t *testing.T) {
		qf := domain.QuotaForecast{
			ProjectedQuotaUsedP90:   ptrF(92),
			ProjectedContextUsedP90: ptrF(60),
			Calibrated:              false,
			Confidence:              domain.ConfidenceLow,
		}
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{QuotaForecast: qf})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		wantQuota := sigmoid((92 - sigmoidMidpoint) / sigmoidScale)
		wantContext := sigmoid((60 - sigmoidMidpoint) / sigmoidScale)
		if math.Abs(got.QuotaRisk.Score-wantQuota) > 1e-9 {
			t.Errorf("QuotaRisk.Score = %v, want %v", got.QuotaRisk.Score, wantQuota)
		}
		if math.Abs(got.ContextRisk.Score-wantContext) > 1e-9 {
			t.Errorf("ContextRisk.Score = %v, want %v", got.ContextRisk.Score, wantContext)
		}
	})
}

// --- TestRiskComponentsCompletion ---------------------------------------

func TestRiskComponentsCompletion(t *testing.T) {
	c := NewRuleRiskCombiner()

	t.Run("baseline scope yields base completion risk", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if math.Abs(got.CompletionRisk.Score-completionBase) > 1e-9 {
			t.Errorf("CompletionRisk.Score = %v, want base %v for empty scope", got.CompletionRisk.Score, completionBase)
		}
	})

	t.Run("large scope raises completion risk", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{
				FilesChangedP90:     ptrI(20),
				LinesChangedP90:     ptrI(2000),
				RequiresIntegration: true,
				MigrationLikely:     true,
				CrossProject:        true,
				ReasonCodes: []domain.ReasonCode{
					domain.ReasonOpenEndedScope,
					domain.ReasonHighRecentRetryRate,
					domain.ReasonHighRecentTestFailureRate,
					domain.ReasonProgressBlocked,
				},
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		// Every term maxed out should clamp to 1.0.
		if got.CompletionRisk.Score != 1.0 {
			t.Errorf("CompletionRisk.Score = %v, want 1.0 (clamped) for maxed-out scope", got.CompletionRisk.Score)
		}
	})

	t.Run("completion risk reads formula terms from reason codes", func(t *testing.T) {
		base, err := c.Combine(context.Background(), app.CombineRiskRequest{Scope: domain.ScopeEstimate{}})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		withOpenEnded, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{ReasonCodes: []domain.ReasonCode{domain.ReasonOpenEndedScope}},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		wantDelta := completionOpenEndedScopeCoefficient
		gotDelta := withOpenEnded.CompletionRisk.Score - base.CompletionRisk.Score
		if math.Abs(gotDelta-wantDelta) > 1e-9 {
			t.Errorf("open-ended-scope delta = %v, want %v", gotDelta, wantDelta)
		}
	})

	t.Run("cold-start scope propagates PREDICTION_COLD_START, never fabricates calibration", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{Calibrated: false, Confidence: domain.ConfidenceLow},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if got.CompletionRisk.Calibrated {
			t.Errorf("CompletionRisk.Calibrated = true, want false for uncalibrated scope input")
		}
		if !containsReason(got.CompletionRisk.ReasonCodes, domain.ReasonPredictionColdStart) {
			t.Errorf("CompletionRisk.ReasonCodes = %v, want to contain PREDICTION_COLD_START", got.CompletionRisk.ReasonCodes)
		}
	})
}

// --- TestRiskComponentsBlastRadius ---------------------------------------

func TestRiskComponentsBlastRadius(t *testing.T) {
	c := NewRuleRiskCombiner()

	t.Run("baseline scope yields base blast radius risk", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if math.Abs(got.BlastRadiusRisk.Score-blastRadiusBase) > 1e-9 {
			t.Errorf("BlastRadiusRisk.Score = %v, want base %v for empty scope", got.BlastRadiusRisk.Score, blastRadiusBase)
		}
	})

	t.Run("security-sensitive migration raises blast radius risk", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{
				MigrationLikely:   true,
				SecuritySensitive: true,
				// ReasonCodes set explicitly, mirroring how
				// scope.RuleScopeEstimator itself populates both the
				// boolean field and the corresponding reason code
				// together (estimator.go: "if migrationLikely { reasons =
				// append(reasons, domain.ReasonMigrationLikely) }") — the
				// combiner only echoes ReasonCodes it is given, it does
				// not re-derive reason codes from ScopeEstimate's boolean
				// fields itself.
				ReasonCodes: []domain.ReasonCode{
					domain.ReasonMigrationLikely,
					domain.ReasonSecuritySensitive,
				},
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		want := clamp01(blastRadiusBase + blastRadiusMigrationCoefficient + blastRadiusSecuritySensitiveCoeff)
		if math.Abs(got.BlastRadiusRisk.Score-want) > 1e-9 {
			t.Errorf("BlastRadiusRisk.Score = %v, want %v", got.BlastRadiusRisk.Score, want)
		}
		if !containsReason(got.BlastRadiusRisk.ReasonCodes, domain.ReasonSecuritySensitive) {
			t.Errorf("BlastRadiusRisk.ReasonCodes = %v, want to contain SECURITY_SENSITIVE", got.BlastRadiusRisk.ReasonCodes)
		}
		if !containsReason(got.BlastRadiusRisk.ReasonCodes, domain.ReasonMigrationLikely) {
			t.Errorf("BlastRadiusRisk.ReasonCodes = %v, want to contain MIGRATION_LIKELY", got.BlastRadiusRisk.ReasonCodes)
		}
	})

	t.Run("public API change reason code contributes its coefficient", func(t *testing.T) {
		base, err := c.Combine(context.Background(), app.CombineRiskRequest{Scope: domain.ScopeEstimate{}})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		withPublicAPI, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{ReasonCodes: []domain.ReasonCode{domain.ReasonPublicAPIChange}},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		gotDelta := withPublicAPI.BlastRadiusRisk.Score - base.BlastRadiusRisk.Score
		if math.Abs(gotDelta-blastRadiusPublicAPIChangeCoeff) > 1e-9 {
			t.Errorf("public-API-change delta = %v, want %v", gotDelta, blastRadiusPublicAPIChangeCoeff)
		}
	})

	t.Run("large file scope raises blast radius risk monotonically", func(t *testing.T) {
		small, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{FilesChangedP90: ptrI(2)},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		large, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{FilesChangedP90: ptrI(50)},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if large.BlastRadiusRisk.Score <= small.BlastRadiusRisk.Score {
			t.Errorf("BlastRadiusRisk.Score not monotonic in files changed: small=%v large=%v",
				small.BlastRadiusRisk.Score, large.BlastRadiusRisk.Score)
		}
	})
}

// --- TestRiskComponentsOverall --------------------------------------------

func TestRiskComponentsOverall(t *testing.T) {
	c := NewRuleRiskCombiner()

	t.Run("overall is the max of the four components", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{
				MigrationLikely:   true,
				SecuritySensitive: true,
			},
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   ptrF(50),
				ProjectedContextUsedP90: ptrF(20),
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		want := math.Max(math.Max(got.QuotaRisk.Score, got.ContextRisk.Score),
			math.Max(got.CompletionRisk.Score, got.BlastRadiusRisk.Score))
		if math.Abs(got.OverallRisk.Score-want) > 1e-9 {
			t.Errorf("OverallRisk.Score = %v, want max() = %v", got.OverallRisk.Score, want)
		}
	})

	t.Run("overall calibrated only when every component is calibrated", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope:         domain.ScopeEstimate{Calibrated: true, Confidence: domain.ConfidenceHigh},
			QuotaForecast: domain.QuotaForecast{Calibrated: false, Confidence: domain.ConfidenceLow, ProjectedQuotaUsedP90: ptrF(10), ProjectedContextUsedP90: ptrF(10)},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if got.OverallRisk.Calibrated {
			t.Errorf("OverallRisk.Calibrated = true, want false (quota forecast input was uncalibrated)")
		}
	})

	t.Run("overall reason codes are the union of all four components", func(t *testing.T) {
		got, err := c.Combine(context.Background(), app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{
				MigrationLikely: true,
				ReasonCodes:     []domain.ReasonCode{domain.ReasonMigrationLikely},
			},
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   nil,
				ProjectedContextUsedP90: ptrF(10),
			},
		})
		if err != nil {
			t.Fatalf("Combine: %v", err)
		}
		if !containsReason(got.OverallRisk.ReasonCodes, domain.ReasonQuotaUnknown) {
			t.Errorf("OverallRisk.ReasonCodes = %v, want to contain QUOTA_UNKNOWN (from QuotaRisk)", got.OverallRisk.ReasonCodes)
		}
		if !containsReason(got.OverallRisk.ReasonCodes, domain.ReasonMigrationLikely) {
			t.Errorf("OverallRisk.ReasonCodes = %v, want to contain MIGRATION_LIKELY (from CompletionRisk/BlastRadiusRisk)", got.OverallRisk.ReasonCodes)
		}
	})
}

// --- TestRiskComponentsNeverNaNOrInf --------------------------------------

// TestRiskComponentsNeverNaNOrInf is a broad property-style sweep across
// extreme/degenerate inputs (mirrors internal/predictor/runway's own
// "sweep first" convention for High/Medium-risk nodes): every component's
// Score must stay in [0,1], never NaN/Inf, regardless of how extreme the
// upstream inputs are.
func TestRiskComponentsNeverNaNOrInf(t *testing.T) {
	c := NewRuleRiskCombiner()
	rng := rand.New(rand.NewSource(42))

	extremeFloats := []float64{
		0, 1, -1, 100, -100, 1e9, -1e9,
		math.MaxFloat64, -math.MaxFloat64,
		math.SmallestNonzeroFloat64,
	}
	extremeInts := []int64{0, 1, -1, 1000000, -1000000, math.MaxInt64, math.MinInt64}

	for i := 0; i < 500; i++ {
		req := app.CombineRiskRequest{
			Scope: domain.ScopeEstimate{
				FilesChangedP90:     randIntPtr(rng, extremeInts),
				LinesChangedP90:     randIntPtr(rng, extremeInts),
				RequiresIntegration: rng.Intn(2) == 0,
				MigrationLikely:     rng.Intn(2) == 0,
				CrossProject:        rng.Intn(2) == 0,
				SecuritySensitive:   rng.Intn(2) == 0,
				Calibrated:          rng.Intn(2) == 0,
			},
			QuotaForecast: domain.QuotaForecast{
				ProjectedQuotaUsedP90:   randFloatPtr(rng, extremeFloats),
				ProjectedContextUsedP90: randFloatPtr(rng, extremeFloats),
				Calibrated:              rng.Intn(2) == 0,
			},
		}
		got, err := c.Combine(context.Background(), req)
		if err != nil {
			t.Fatalf("trial %d: Combine returned error: %v", i, err)
		}
		assertFiniteUnitScore(t, "QuotaRisk", got.QuotaRisk)
		assertFiniteUnitScore(t, "ContextRisk", got.ContextRisk)
		assertFiniteUnitScore(t, "CompletionRisk", got.CompletionRisk)
		assertFiniteUnitScore(t, "BlastRadiusRisk", got.BlastRadiusRisk)
		assertFiniteUnitScore(t, "OverallRisk", got.OverallRisk)
	}
}

func randIntPtr(rng *rand.Rand, choices []int64) *int64 {
	if rng.Intn(4) == 0 {
		return nil // sometimes unknown
	}
	v := choices[rng.Intn(len(choices))]
	return &v
}

func randFloatPtr(rng *rand.Rand, choices []float64) *float64 {
	if rng.Intn(4) == 0 {
		return nil // sometimes unknown
	}
	v := choices[rng.Intn(len(choices))]
	return &v
}

// --- TestRiskComponentsDeterministic --------------------------------------

func TestRiskComponentsDeterministic(t *testing.T) {
	c := NewRuleRiskCombiner()
	req := app.CombineRiskRequest{
		Scope: domain.ScopeEstimate{
			FilesChangedP90:     ptrI(12),
			LinesChangedP90:     ptrI(800),
			RequiresIntegration: true,
			MigrationLikely:     true,
			CrossProject:        true,
			SecuritySensitive:   true,
			ReasonCodes: []domain.ReasonCode{
				domain.ReasonOpenEndedScope,
				domain.ReasonPublicAPIChange,
			},
			Calibrated: false,
			Confidence: domain.ConfidenceLow,
		},
		QuotaForecast: domain.QuotaForecast{
			ProjectedQuotaUsedP90:   ptrF(88),
			ProjectedContextUsedP90: ptrF(70),
			Calibrated:              false,
			Confidence:              domain.ConfidenceLow,
		},
	}

	first, err := c.Combine(context.Background(), req)
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := c.Combine(context.Background(), req)
		if err != nil {
			t.Fatalf("trial %d: Combine: %v", i, err)
		}
		if got.QuotaRisk.Score != first.QuotaRisk.Score ||
			got.ContextRisk.Score != first.ContextRisk.Score ||
			got.CompletionRisk.Score != first.CompletionRisk.Score ||
			got.BlastRadiusRisk.Score != first.BlastRadiusRisk.Score ||
			got.OverallRisk.Score != first.OverallRisk.Score {
			t.Fatalf("trial %d: non-deterministic output: first=%+v got=%+v", i, first, got)
		}
	}
}

// --- TestRiskComponentsColdStartNeverFabricatesCalibration ----------------

// TestRiskComponentsColdStartNeverFabricatesCalibration enforces
// agents/predictor.md's cold-start contract: when upstream Stage 1/3
// forecasts are themselves cold-start/uncalibrated, RiskCombiner's output
// must propagate that honestly (Calibrated=false, low/medium Confidence)
// rather than manufacturing false confidence at the combination step.
func TestRiskComponentsColdStartNeverFabricatesCalibration(t *testing.T) {
	c := NewRuleRiskCombiner()
	got, err := c.Combine(context.Background(), app.CombineRiskRequest{
		Scope: domain.ScopeEstimate{
			Calibrated:  false,
			Confidence:  domain.ConfidenceLow,
			ReasonCodes: []domain.ReasonCode{domain.ReasonPredictionColdStart},
		},
		QuotaForecast: domain.QuotaForecast{
			Calibrated:              false,
			Confidence:              domain.ConfidenceLow,
			ProjectedQuotaUsedP90:   ptrF(40),
			ProjectedContextUsedP90: ptrF(30),
			ReasonCodes:             []domain.ReasonCode{domain.ReasonPredictionColdStart},
		},
	})
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}

	for name, comp := range map[string]domain.RiskComponent{
		"QuotaRisk":       got.QuotaRisk,
		"ContextRisk":     got.ContextRisk,
		"CompletionRisk":  got.CompletionRisk,
		"BlastRadiusRisk": got.BlastRadiusRisk,
		"OverallRisk":     got.OverallRisk,
	} {
		if comp.Calibrated {
			t.Errorf("%s.Calibrated = true, want false when every input is cold-start/uncalibrated", name)
		}
		if comp.Confidence == domain.ConfidenceHigh || comp.Confidence == domain.ConfidenceExact {
			t.Errorf("%s.Confidence = %q, want <= medium when every input is cold-start/uncalibrated", name, comp.Confidence)
		}
	}
}

// --- TestRiskComponentsReasonCodeGolden -----------------------------------

// TestRiskComponentsReasonCodeGolden pins the exact reason-code set each
// component produces for a fixed, representative input — a golden test so
// any future change to reason-code derivation shows up as an explicit
// diff, per agents/predictor.md's "reason-code golden tests" requirement.
func TestRiskComponentsReasonCodeGolden(t *testing.T) {
	c := NewRuleRiskCombiner()
	got, err := c.Combine(context.Background(), app.CombineRiskRequest{
		Scope: domain.ScopeEstimate{
			FilesChangedP90:   ptrI(20),
			MigrationLikely:   true,
			SecuritySensitive: true,
			CrossProject:      true,
			ReasonCodes: []domain.ReasonCode{
				domain.ReasonMigrationLikely,
				domain.ReasonSecuritySensitive,
				domain.ReasonCrossLayerChange,
				domain.ReasonOpenEndedScope,
			},
			Calibrated: false,
			Confidence: domain.ConfidenceLow,
		},
		QuotaForecast: domain.QuotaForecast{
			ProjectedQuotaUsedP90:   nil,
			ProjectedContextUsedP90: ptrF(92),
			ReasonCodes:             []domain.ReasonCode{domain.ReasonContextNearLimit},
			Calibrated:              false,
			Confidence:              domain.ConfidenceLow,
		},
	})
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}

	// QuotaForecast.ReasonCodes is a single shared field covering both the
	// quota and context sub-signals (domain.QuotaForecast's own doc
	// comment: "Both projections are produced together"; mirrors how
	// quota.RuleQuotaForecaster itself appends quotaReasons and
	// contextReasons into one combined slice) — there is no per-field
	// reason-code split in the frozen struct, so quotaRiskComponent and
	// contextRiskComponent both echo the same qf.ReasonCodes in full,
	// plus their own unknown-projection reason code when applicable.
	wantQuotaReasons := []domain.ReasonCode{domain.ReasonQuotaUnknown, domain.ReasonContextNearLimit}
	wantContextReasons := []domain.ReasonCode{domain.ReasonContextNearLimit}
	wantCompletionReasons := []domain.ReasonCode{
		domain.ReasonPredictionColdStart,
		domain.ReasonMigrationLikely,
		domain.ReasonSecuritySensitive,
		domain.ReasonCrossLayerChange,
		domain.ReasonOpenEndedScope,
	}
	wantBlastRadiusReasons := wantCompletionReasons // same scope input, same ReasonCodes source

	assertReasonSetEqual(t, "QuotaRisk", got.QuotaRisk.ReasonCodes, wantQuotaReasons)
	assertReasonSetEqual(t, "ContextRisk", got.ContextRisk.ReasonCodes, wantContextReasons)
	assertReasonSetEqual(t, "CompletionRisk", got.CompletionRisk.ReasonCodes, wantCompletionReasons)
	assertReasonSetEqual(t, "BlastRadiusRisk", got.BlastRadiusRisk.ReasonCodes, wantBlastRadiusReasons)
}

func assertReasonSetEqual(t *testing.T, label string, got, want []domain.ReasonCode) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s.ReasonCodes = %v, want %v (length mismatch)", label, got, want)
		return
	}
	gotSet := make(map[domain.ReasonCode]struct{}, len(got))
	for _, r := range got {
		gotSet[r] = struct{}{}
	}
	for _, w := range want {
		if _, ok := gotSet[w]; !ok {
			t.Errorf("%s.ReasonCodes = %v, want to contain %v", label, got, w)
		}
	}
}

// --- TestRiskComponentsSatisfiesFrozenInterface ---------------------------

func TestRiskComponentsSatisfiesFrozenInterface(t *testing.T) {
	var _ app.RiskCombiner = NewRuleRiskCombiner()
}
