package policy

import (
	"math"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int64) *int64     { return &v }

func calibratedComponent(score float64, confidence domain.Confidence) domain.RiskComponent {
	return domain.RiskComponent{Score: score, Calibrated: true, Confidence: confidence}
}

func uncalibratedComponent(score float64, confidence domain.Confidence, reasons ...domain.ReasonCode) domain.RiskComponent {
	return domain.RiskComponent{Score: score, Calibrated: false, Confidence: confidence, ReasonCodes: reasons}
}

// lowRiskAllInputs builds a CombineRiskResult where every component is
// calibrated and comfortably in the "low" band, for tests that need a
// clean ALLOW baseline.
func lowRiskAllInputs(score float64) app.CombineRiskResult {
	c := calibratedComponent(score, domain.ConfidenceHigh)
	return app.CombineRiskResult{
		QuotaRisk:       c,
		ContextRisk:     c,
		CompletionRisk:  c,
		BlastRadiusRisk: c,
		OverallRisk:     c,
	}
}

func uncalibratedRunway(riskScore float64) domain.RunwayForecast {
	return domain.RunwayForecast{
		RiskScore:  riskScore,
		Calibrated: false,
		Confidence: domain.ConfidenceLow,
	}
}

// --- TestPolicyPriorityOrder ------------------------------------------------

func TestPolicyPriorityOrder(t *testing.T) {
	d := NewDecider()

	t.Run("explicit deny wins over everything else", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			ExplicitDeny:                true,
			IntegrityFailure:            true,
			MandatoryCheckpointBoundary: true,
			Risk:                        lowRiskAllInputs(0.01),
			Runway:                      uncalibratedRunway(0.01),
		})
		if got.Action != app.PolicyBlock {
			t.Errorf("Action = %v, want PolicyBlock", got.Action)
		}
	})

	t.Run("integrity failure wins over pause/checkpoint/risk", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			IntegrityFailure:            true,
			MandatoryCheckpointBoundary: true,
			Risk:                        lowRiskAllInputs(0.99),
			Runway: domain.RunwayForecast{
				RiskScore:      1.0,
				Calibrated:     true,
				Confidence:     domain.ConfidenceHigh,
				HitProbability: ptrF(0.99),
			},
		})
		if got.Action != app.PolicyBlock {
			t.Errorf("Action = %v, want PolicyBlock", got.Action)
		}
	})

	t.Run("active pause trigger wins over mandatory checkpoint and risk band", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			MandatoryCheckpointBoundary: true,
			Risk:                        lowRiskAllInputs(0.10),
			Runway: domain.RunwayForecast{
				CurrentUsedPercent: ptrF(99),
				RiskScore:          0.95,
				Calibrated:         false,
				Confidence:         domain.ConfidenceLow,
			},
		})
		if got.Action != app.PolicyPause {
			t.Errorf("Action = %v, want PolicyPause (emergency)", got.Action)
		}
	})

	t.Run("mandatory checkpoint boundary wins over risk band when no pause trigger", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			MandatoryCheckpointBoundary: true,
			Risk:                        lowRiskAllInputs(0.01),
			Runway:                      uncalibratedRunway(0.01),
		})
		if got.Action != app.PolicyCheckpointAndRun {
			t.Errorf("Action = %v, want PolicyCheckpointAndRun", got.Action)
		}
	})

	t.Run("falls through to risk band when nothing else fires", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk:   lowRiskAllInputs(0.50),
			Runway: uncalibratedRunway(0.01),
		})
		if got.Action != app.PolicyWarn {
			t.Errorf("Action = %v, want PolicyWarn for medium band", got.Action)
		}
	})
}

// --- TestPolicyRiskBands -----------------------------------------------------

func TestPolicyRiskBands(t *testing.T) {
	d := NewDecider()

	cases := []struct {
		name  string
		score float64
		want  app.PolicyAction
	}{
		{"low band", 0.10, app.PolicyRun},
		{"just under medium", 0.4499, app.PolicyRun},
		{"medium band start", 0.45, app.PolicyWarn},
		{"medium band", 0.60, app.PolicyWarn},
		{"just under high", 0.6499, app.PolicyWarn},
		{"high band start", 0.65, app.PolicyRequireConfirmation},
		{"high band", 0.80, app.PolicyRequireConfirmation},
		{"just under critical", 0.8499, app.PolicyRequireConfirmation},
		{"critical band start", 0.85, app.PolicyCheckpointAndRun},
		{"critical band", 1.0, app.PolicyCheckpointAndRun},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Decide(DecideRequest{
				Risk:   lowRiskAllInputs(tc.score),
				Runway: uncalibratedRunway(0.01),
			})
			if got.Action != tc.want {
				t.Errorf("score=%v: Action = %v, want %v", tc.score, got.Action, tc.want)
			}
		})
	}

	t.Run("high band with high blast radius prefers checkpoint over confirmation", func(t *testing.T) {
		overall := calibratedComponent(0.70, domain.ConfidenceHigh)
		blast := calibratedComponent(0.90, domain.ConfidenceHigh)
		got := d.Decide(DecideRequest{
			Risk: app.CombineRiskResult{
				OverallRisk:     overall,
				BlastRadiusRisk: blast,
			},
			Runway: uncalibratedRunway(0.01),
		})
		if got.Action != app.PolicyCheckpointAndRun {
			t.Errorf("Action = %v, want PolicyCheckpointAndRun for high blast radius", got.Action)
		}
	})
}

// --- TestPolicyRunwayPause ---------------------------------------------------

func TestPolicyRunwayPause(t *testing.T) {
	d := NewDecider()

	t.Run("calibrated hit-probability above threshold arms on first observation, does not pause yet", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				Calibrated:     true,
				Confidence:     domain.ConfidenceHigh,
				HitProbability: ptrF(0.85),
				RiskScore:      0.85,
			},
			PriorRunwayHitConfirmed: false,
		})
		if got.Action != app.PolicyWarn {
			t.Errorf("Action = %v, want PolicyWarn (armed, not yet confirmed)", got.Action)
		}
		if got.Probability == nil || math.Abs(*got.Probability-0.85) > 1e-9 {
			t.Errorf("Probability = %v, want 0.85 (calibrated input)", got.Probability)
		}
	})

	t.Run("calibrated hit-probability above threshold twice in a row pauses", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				Calibrated:     true,
				Confidence:     domain.ConfidenceHigh,
				HitProbability: ptrF(0.85),
				RiskScore:      0.85,
			},
			PriorRunwayHitConfirmed: true,
		})
		if got.Action != app.PolicyPause {
			t.Errorf("Action = %v, want PolicyPause (confirmed twice)", got.Action)
		}
		if got.Probability == nil || math.Abs(*got.Probability-0.85) > 1e-9 {
			t.Errorf("Probability = %v, want 0.85 (calibrated input)", got.Probability)
		}
	})

	t.Run("calibrated but below threshold does not trigger pause path", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				Calibrated:     true,
				Confidence:     domain.ConfidenceHigh,
				HitProbability: ptrF(0.50),
				RiskScore:      0.50,
			},
			PriorRunwayHitConfirmed: true,
		})
		if got.Action == app.PolicyPause {
			t.Errorf("Action = %v, should not pause below threshold", got.Action)
		}
	})

	t.Run("configurable threshold is respected", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				Calibrated:     true,
				Confidence:     domain.ConfidenceHigh,
				HitProbability: ptrF(0.55),
				RiskScore:      0.55,
			},
			PriorRunwayHitConfirmed: true,
			Config:                  Config{RunwayHitProbabilityThreshold: 0.50},
		})
		if got.Action != app.PolicyPause {
			t.Errorf("Action = %v, want PolicyPause with lowered threshold", got.Action)
		}
	})

	t.Run("emergency used-percent skips debounce even with no prior confirmation", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				CurrentUsedPercent: ptrF(99),
				RiskScore:          0.95,
				Calibrated:         false,
				Confidence:         domain.ConfidenceLow,
			},
			PriorRunwayHitConfirmed: false,
		})
		if got.Action != app.PolicyPause {
			t.Errorf("Action = %v, want PolicyPause (emergency, no debounce needed)", got.Action)
		}
	})

	t.Run("emergency time-to-limit triggers pause", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				EstimatedTimeToLimitP50Seconds: ptrI(30),
				RiskScore:                      0.9,
				Calibrated:                     false,
				Confidence:                     domain.ConfidenceLow,
			},
		})
		if got.Action != app.PolicyPause {
			t.Errorf("Action = %v, want PolicyPause (emergency time-to-limit)", got.Action)
		}
	})
}

// --- TestPolicyDeterminism ---------------------------------------------------

func TestPolicyDeterminism(t *testing.T) {
	d := NewDecider()
	req := DecideRequest{
		Risk: lowRiskAllInputs(0.70),
		Runway: domain.RunwayForecast{
			Calibrated:     true,
			Confidence:     domain.ConfidenceHigh,
			HitProbability: ptrF(0.30),
			RiskScore:      0.30,
		},
		PriorRunwayHitConfirmed: false,
	}

	first := d.Decide(req)
	for i := 0; i < 100; i++ {
		got := d.Decide(req)
		if got.Action != first.Action || got.RiskScore != first.RiskScore || got.Calibrated != first.Calibrated {
			t.Fatalf("iteration %d: Decide is not deterministic for identical input: got %+v, want %+v", i, got, first)
		}
		if (got.Probability == nil) != (first.Probability == nil) {
			t.Fatalf("iteration %d: Probability nilness differs: got %v, want %v", i, got.Probability, first.Probability)
		}
	}
}

// --- TestPolicyFailOpenFailClosed -------------------------------------------
//
// Documented choice: an *operational* gap (a zero-value/missing risk or
// runway input) fails open in the sense that Decide always returns a
// usable, conservative Decision rather than panicking or blocking —
// mirroring ADD §17.5's "predictor error -> fallback heuristic" /
// "telemetry unavailable -> fail open + warning" rows. A caller-flagged
// *integrity* concern (ExplicitDeny, IntegrityFailure) fails closed
// (PolicyBlock, unconditionally, highest priority) per ADD §17.5's
// "State Checkpoint requested but failed -> fail closed" row and
// CONTRACT_FREEZE.md's error contract.
func TestPolicyFailOpenFailClosed(t *testing.T) {
	d := NewDecider()

	t.Run("zero-value Risk and Runway never panics, degrades to a safe low-risk decision", func(t *testing.T) {
		got := d.Decide(DecideRequest{})
		if got.Action != app.PolicyRun {
			t.Errorf("Action = %v, want PolicyRun for all-zero input (score 0 is the low band)", got.Action)
		}
		if got.Probability != nil {
			t.Errorf("Probability = %v, want nil for zero-value (uncalibrated) input", got.Probability)
		}
	})

	t.Run("ExplicitDeny fails closed regardless of how favorable every other input is", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			ExplicitDeny: true,
			Risk:         lowRiskAllInputs(0.0),
			Runway:       uncalibratedRunway(0.0),
		})
		if got.Action != app.PolicyBlock {
			t.Errorf("Action = %v, want PolicyBlock (fail closed on explicit deny)", got.Action)
		}
	})

	t.Run("IntegrityFailure fails closed regardless of how favorable every other input is", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			IntegrityFailure: true,
			Risk:             lowRiskAllInputs(0.0),
			Runway:           uncalibratedRunway(0.0),
		})
		if got.Action != app.PolicyBlock {
			t.Errorf("Action = %v, want PolicyBlock (fail closed on integrity failure)", got.Action)
		}
	})

	t.Run("negative threshold config falls back to the documented default rather than misbehaving", func(t *testing.T) {
		got := d.Decide(DecideRequest{
			Risk: lowRiskAllInputs(0.01),
			Runway: domain.RunwayForecast{
				Calibrated:     true,
				Confidence:     domain.ConfidenceHigh,
				HitProbability: ptrF(0.79), // below the real 0.80 default
				RiskScore:      0.79,
			},
			PriorRunwayHitConfirmed: true,
			Config:                  Config{RunwayHitProbabilityThreshold: -1},
		})
		if got.Action == app.PolicyPause {
			t.Errorf("Action = %v, want non-pause: 0.79 < DefaultRunwayHitProbabilityThreshold (0.80), and a negative configured threshold must fall back to the default, not silently accept everything", got.Action)
		}
	})

	t.Run("no divide-by-zero/NaN/Inf ever escapes for degenerate inputs", func(t *testing.T) {
		degenerate := []app.CombineRiskResult{
			{},
			{OverallRisk: domain.RiskComponent{Score: math.NaN()}},
			{OverallRisk: domain.RiskComponent{Score: math.Inf(1)}},
			{OverallRisk: domain.RiskComponent{Score: math.Inf(-1)}},
		}
		for i, risk := range degenerate {
			got := d.Decide(DecideRequest{Risk: risk, Runway: uncalibratedRunway(0.0)})
			if math.IsNaN(got.RiskScore) || math.IsInf(got.RiskScore, 0) {
				t.Errorf("case %d: RiskScore = %v, want finite", i, got.RiskScore)
			}
		}
	})
}

// --- TestPolicyBenchmarkFastPath ---------------------------------------------

func BenchmarkDecide(b *testing.B) {
	d := NewDecider()
	req := DecideRequest{
		Risk: lowRiskAllInputs(0.55),
		Runway: domain.RunwayForecast{
			Calibrated:     true,
			Confidence:     domain.ConfidenceHigh,
			HitProbability: ptrF(0.40),
			RiskScore:      0.40,
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Decide(req)
	}
}

// TestDecideFastPath is not a benchmark itself but asserts Decide
// completes comfortably within ADD §29.11's "policy <1ms" target on a
// representative input, as a cheap regression guard between full
// benchmark runs.
func TestDecideFastPath(t *testing.T) {
	d := NewDecider()
	req := DecideRequest{
		Risk: lowRiskAllInputs(0.55),
		Runway: domain.RunwayForecast{
			Calibrated:     true,
			Confidence:     domain.ConfidenceHigh,
			HitProbability: ptrF(0.40),
			RiskScore:      0.40,
		},
	}
	start := time.Now()
	for i := 0; i < 1000; i++ {
		_ = d.Decide(req)
	}
	elapsed := time.Since(start)
	if per := elapsed / 1000; per > time.Millisecond {
		t.Errorf("Decide averaged %v per call over 1000 calls, want < 1ms (ADD §29.11 policy target)", per)
	}
}
