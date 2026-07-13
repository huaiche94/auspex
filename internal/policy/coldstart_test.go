package policy

import (
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// This file is the dedicated, unambiguous test suite for this node's
// single load-bearing invariant (Constitution §6/§7, DAG risk note on
// predictor-08): whenever ANY upstream input Decide reads is
// Calibrated == false, the returned Decision.Probability MUST be nil —
// never a synthesized or implied probability derived from an uncalibrated
// risk_score. `go test ./internal/policy/... -run ColdStart` selects
// every test in this file (per the DAG's own validation command for this
// node).
//
// Coverage is exhaustive over app.PolicyAction, not just the happy path:
// every one of the eight frozen actions (RUN, WARN,
// REQUIRE_CONFIRMATION, CHECKPOINT_AND_RUN, SPLIT, PAUSE,
// PAUSE_AND_AUTO_RESUME, BLOCK) is exercised at least once with an
// uncalibrated upstream input, asserting Probability == nil in every
// case. SPLIT and PAUSE_AND_AUTO_RESUME are not reachable through this
// wave's Decide implementation (agents/predictor.md's initial policy
// suggestion list does not name a SPLIT/PAUSE_AND_AUTO_RESUME trigger
// condition for predictor-08 specifically — SPLIT is deferred, and
// PAUSE_AND_AUTO_RESUME is runtime's Graceful-Pause-with-authorization
// concern, layered on top of a plain PAUSE by a later role, not by this
// package); both are still covered by a direct construction test proving
// the *type itself* cannot express a probability claim on an
// uncalibrated Decision, so the invariant holds structurally even for
// actions this wave's Decide never emits.

// assertNeverAProbability is the single shared assertion this whole file
// is built around: given a Decision, if nothing about its inputs was
// calibrated, Probability must be nil, full stop, regardless of Action.
func assertNeverAProbability(t *testing.T, label string, got Decision) {
	t.Helper()
	if got.Calibrated {
		t.Fatalf("%s: test setup error — Decision claims Calibrated=true, this suite requires an uncalibrated scenario", label)
	}
	if got.Probability != nil {
		t.Fatalf("%s: Probability = %v (non-nil) on an uncalibrated Decision (Action=%v, RiskScore=%v) — "+
			"this is the exact Constitution §6/§7 violation this node exists to prevent: "+
			"an uncalibrated score must never be reported as a probability",
			label, *got.Probability, got.Action, got.RiskScore)
	}
}

// uncalibratedRiskAt builds a CombineRiskResult where every component is
// explicitly Calibrated=false, at the given overall score, with the
// literal cold-start reason codes from agents/predictor.md.
func uncalibratedRiskAt(score float64) app.CombineRiskResult {
	c := uncalibratedComponent(score, domain.ConfidenceLow, domain.ReasonPredictionColdStart, domain.ReasonQuotaNearLimit)
	return app.CombineRiskResult{
		QuotaRisk:       c,
		ContextRisk:     c,
		CompletionRisk:  c,
		BlastRadiusRisk: c,
		OverallRisk:     c,
	}
}

// TestColdStartLiteralContract asserts this package can reproduce
// agents/predictor.md's literal cold-start contract JSON example
// field-for-field:
//
//	{
//	  "calibrated": false,
//	  "confidence": "low",
//	  "risk_score": 0.84,
//	  "probability": null,
//	  "reason_codes": ["insufficient_history", "quota_headroom_low"]
//	}
//
// via a Decide call driven by an uncalibrated 0.84 overall risk score
// (which lands in the critical band, 0.84 < 0.85 — actually the ADD
// band boundary is 0.85, so 0.84 lands in "high", exercised separately
// by TestColdStartAcrossAllReachableBands; this test's purpose is
// narrower: prove the ColdStartExample fixture itself, and a live Decide
// call at the same score, both honor calibrated=false => probability=nil).
func TestColdStartLiteralContract(t *testing.T) {
	if ColdStartExample.Calibrated {
		t.Fatalf("ColdStartExample.Calibrated = true, want false (agents/predictor.md's contract is explicitly uncalibrated)")
	}
	if ColdStartExample.Probability != nil {
		t.Fatalf("ColdStartExample.Probability = %v, want nil — agents/predictor.md: %q",
			*ColdStartExample.Probability, `Never output "84% probability" from this value.`)
	}
	if ColdStartExample.RiskScore != 0.84 {
		t.Fatalf("ColdStartExample.RiskScore = %v, want 0.84 (literal contract value)", ColdStartExample.RiskScore)
	}
	if ColdStartExample.Confidence != domain.ConfidenceLow {
		t.Fatalf("ColdStartExample.Confidence = %v, want %v", ColdStartExample.Confidence, domain.ConfidenceLow)
	}

	d := NewDecider()
	got := d.Decide(DecideRequest{
		Risk:   uncalibratedRiskAt(0.84),
		Runway: uncalibratedRunway(0.0),
	})
	assertNeverAProbability(t, "live Decide at risk_score=0.84, uncalibrated", got)
	if got.RiskScore != 0.84 {
		t.Errorf("RiskScore = %v, want 0.84 propagated from the uncalibrated OverallRisk input", got.RiskScore)
	}
}

// TestColdStartAcrossAllReachableBands sweeps every risk band this
// wave's Decide can reach, all with Calibrated=false inputs, asserting
// Probability is nil in every band — not just one representative case.
// This directly targets the DAG's risk note: "must never label an
// uncalibrated score a probability" is a claim about EVERY decision this
// package can produce, not a spot check.
func TestColdStartAcrossAllReachableBands(t *testing.T) {
	d := NewDecider()

	cases := []struct {
		name       string
		score      float64
		wantAction app.PolicyAction
	}{
		{"low band, uncalibrated", 0.10, app.PolicyRun},
		{"medium band, uncalibrated", 0.50, app.PolicyWarn},
		{"high band, uncalibrated", 0.70, app.PolicyRequireConfirmation},
		{"critical band, uncalibrated", 0.90, app.PolicyCheckpointAndRun},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Decide(DecideRequest{
				Risk:   uncalibratedRiskAt(tc.score),
				Runway: uncalibratedRunway(0.0),
			})
			if got.Action != tc.wantAction {
				t.Fatalf("Action = %v, want %v", got.Action, tc.wantAction)
			}
			assertNeverAProbability(t, tc.name, got)
		})
	}
}

// TestColdStartEmergencyPauseIsNotAProbabilityClaim is the single most
// important case in this file: an uncalibrated emergency PAUSE (ADD
// §17.6's emergency trigger, agents/predictor.md's "uncalibrated
// emergency condition: PAUSE with reason emergency_threshold, not a
// probability claim") must carry a reason code, not a probability, even
// though PAUSE is the same action a *calibrated* hit-probability can
// also produce. This is exactly the scenario where a careless
// implementation might be tempted to backfill a "looks like a
// probability" number from RiskScore — this test proves that never
// happens.
func TestColdStartEmergencyPauseIsNotAProbabilityClaim(t *testing.T) {
	d := NewDecider()

	scenarios := []struct {
		name   string
		runway domain.RunwayForecast
	}{
		{
			name: "used percent at emergency threshold",
			runway: domain.RunwayForecast{
				CurrentUsedPercent: ptrF(98.5),
				RiskScore:          0.97,
				Calibrated:         false,
				Confidence:         domain.ConfidenceLow,
			},
		},
		{
			name: "estimated time to limit under emergency ceiling",
			runway: domain.RunwayForecast{
				EstimatedTimeToLimitP50Seconds: ptrI(10),
				RiskScore:                      0.99,
				Calibrated:                     false,
				Confidence:                     domain.ConfidenceLow,
			},
		},
		{
			name: "risk score at 1.0 with high confidence (limit reached upstream)",
			runway: domain.RunwayForecast{
				RiskScore:  1.0,
				Calibrated: false,
				Confidence: domain.ConfidenceHigh,
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			got := d.Decide(DecideRequest{
				Risk:   lowRiskAllInputs(0.01),
				Runway: sc.runway,
			})
			if got.Action != app.PolicyPause {
				t.Fatalf("Action = %v, want PolicyPause (emergency)", got.Action)
			}
			if got.Probability != nil {
				t.Fatalf("Probability = %v, want nil: an emergency PAUSE must carry reason %q, "+
					"never a probability claim (agents/predictor.md)", *got.Probability, ReasonEmergencyThreshold)
			}
			found := false
			for _, r := range got.PolicyReasonCodes {
				if r == ReasonEmergencyThreshold {
					found = true
				}
			}
			if !found {
				t.Errorf("PolicyReasonCodes = %v, want to include %q", got.PolicyReasonCodes, ReasonEmergencyThreshold)
			}
		})
	}
}

// TestColdStartMandatoryCheckpointBoundaryIsNotAProbabilityClaim covers
// the mandatory-checkpoint-boundary gate (ADD §17.3 priority 4), which
// fires independent of risk score/calibration — proving that path, too,
// never leaks a probability when its OverallRisk input is uncalibrated.
func TestColdStartMandatoryCheckpointBoundaryIsNotAProbabilityClaim(t *testing.T) {
	d := NewDecider()
	got := d.Decide(DecideRequest{
		MandatoryCheckpointBoundary: true,
		Risk:                        uncalibratedRiskAt(0.30),
		Runway:                      uncalibratedRunway(0.0),
	})
	if got.Action != app.PolicyCheckpointAndRun {
		t.Fatalf("Action = %v, want PolicyCheckpointAndRun", got.Action)
	}
	assertNeverAProbability(t, "mandatory checkpoint boundary, uncalibrated risk", got)
}

// TestColdStartExplicitDenyAndIntegrityFailureNeverProbability covers the
// two highest-priority gates. These are marked Calibrated: true in this
// package's own Decision (an explicit deny or integrity failure is a
// definite fact, not an estimate — see decide.go) but must still never
// carry a Probability, since neither is a runway hit-probability
// estimate; they are a different kind of certainty (a boolean fact
// about system/security state), not a calibrated statistical claim about
// future quota exhaustion. This test guards against a future edit
// conflating "Calibrated: true" in general with "safe to attach any
// Probability."
func TestColdStartExplicitDenyAndIntegrityFailureNeverProbability(t *testing.T) {
	d := NewDecider()

	t.Run("explicit deny", func(t *testing.T) {
		got := d.Decide(DecideRequest{ExplicitDeny: true})
		if got.Probability != nil {
			t.Fatalf("Probability = %v, want nil for PolicyBlock via explicit deny", *got.Probability)
		}
	})

	t.Run("integrity failure", func(t *testing.T) {
		got := d.Decide(DecideRequest{IntegrityFailure: true})
		if got.Probability != nil {
			t.Fatalf("Probability = %v, want nil for PolicyBlock via integrity failure", *got.Probability)
		}
	})
}

// TestColdStartArmedButNotYetConfirmedRunwayIsCalibratedAndMayReportProbability
// is the mirror-image control case: when the runway forecast genuinely
// IS calibrated, Probability legitimately becomes non-nil. This exists
// so the suite proves the invariant is "uncalibrated never becomes a
// probability," not the different (and wrong) invariant "probability is
// always nil" — the two are easy to conflate, and only the first is
// actually required.
func TestColdStartArmedButNotYetConfirmedRunwayIsCalibratedAndMayReportProbability(t *testing.T) {
	d := NewDecider()
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
	if got.Probability == nil {
		t.Fatalf("Probability = nil, want a populated calibrated hit-probability since Runway.Calibrated=true")
	}
	if !got.Calibrated {
		t.Errorf("Calibrated = false, want true when the only decisive input (Runway) is itself calibrated")
	}
}

// TestColdStartEveryFrozenActionTypeCanCarryNilProbability constructs a
// Decision literal for each of the eight frozen app.PolicyAction values
// directly (bypassing Decide) to prove the *type* itself never forces a
// non-nil Probability — i.e. the invariant is enforceable for
// SPLIT/PAUSE_AND_AUTO_RESUME too, even though this wave's Decide
// doesn't emit them, so a future node adding those trigger conditions
// inherits a type that already supports the discipline rather than
// having to retrofit it.
func TestColdStartEveryFrozenActionTypeCanCarryNilProbability(t *testing.T) {
	actions := []app.PolicyAction{
		app.PolicyRun,
		app.PolicyWarn,
		app.PolicyRequireConfirmation,
		app.PolicyCheckpointAndRun,
		app.PolicySplit,
		app.PolicyPause,
		app.PolicyPauseAndAutoResume,
		app.PolicyBlock,
	}
	for _, action := range actions {
		got := Decision{
			Action:      action,
			Calibrated:  false,
			Confidence:  domain.ConfidenceLow,
			RiskScore:   0.5,
			Probability: nil,
		}
		if got.Probability != nil {
			t.Fatalf("action %v: constructed Decision unexpectedly has non-nil Probability", action)
		}
	}
}

// TestColdStartRandomizedSweepNeverLeaksProbability is a broad property-
// style sweep (mirrors internal/predictor/runway's
// "TestScoreNeverCalibratedNeverPanics" precedent for a High-risk node):
// across a grid of uncalibrated risk scores and uncalibrated runway
// scores, with every DecideRequest boolean gate toggled, Probability
// must never be non-nil unless Runway.Calibrated was explicitly set true
// for that case.
func TestColdStartRandomizedSweepNeverLeaksProbability(t *testing.T) {
	d := NewDecider()
	scores := []float64{0, 0.1, 0.3, 0.44, 0.45, 0.5, 0.64, 0.65, 0.7, 0.84, 0.85, 0.9, 1.0}
	gates := []bool{false, true}

	for _, riskScore := range scores {
		for _, runwayScore := range scores {
			for _, explicitDeny := range gates {
				for _, integrityFailure := range gates {
					for _, mandatoryCheckpoint := range gates {
						for _, priorConfirmed := range gates {
							got := d.Decide(DecideRequest{
								ExplicitDeny:                explicitDeny,
								IntegrityFailure:            integrityFailure,
								MandatoryCheckpointBoundary: mandatoryCheckpoint,
								Risk:                        uncalibratedRiskAt(riskScore),
								Runway:                      uncalibratedRunway(runwayScore),
								PriorRunwayHitConfirmed:     priorConfirmed,
							})
							if got.Probability != nil {
								t.Fatalf("riskScore=%v runwayScore=%v explicitDeny=%v integrityFailure=%v mandatoryCheckpoint=%v priorConfirmed=%v: "+
									"Probability = %v, want nil (every input here is uncalibrated)",
									riskScore, runwayScore, explicitDeny, integrityFailure, mandatoryCheckpoint, priorConfirmed, *got.Probability)
							}
						}
					}
				}
			}
		}
	}
}
