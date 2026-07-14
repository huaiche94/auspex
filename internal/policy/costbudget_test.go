package policy

import (
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pricing"
)

// budgetRequest builds a DecideRequest whose base gates land on RUN (no
// risk, no runway signal), with the given cost estimate and budget — so
// every action change observed in these tests is the budget rule's own.
func budgetRequest(cost *pricing.CostRange, budgetUSD float64) DecideRequest {
	return DecideRequest{
		Risk: app.CombineRiskResult{
			OverallRisk: domain.RiskComponent{Score: 0.1, Confidence: domain.ConfidenceLow},
		},
		Cost:   cost,
		Config: Config{TurnCostBudgetUSD: budgetUSD},
	}
}

func costRange(low, high float64) *pricing.CostRange {
	return &pricing.CostRange{LowUSD: low, HighUSD: high, ModelFamily: "fable", Source: pricing.SourceDefaultTable}
}

// TestCostBudget_InactiveWithoutBudget pins ADR-043's activation model:
// no declared budget (the zero value) means the rule is entirely
// inactive, whatever the estimate says — "absence of a budget means the
// resource is simply not policy-active".
func TestCostBudget_InactiveWithoutBudget(t *testing.T) {
	d := NewDecider()
	got := d.Decide(budgetRequest(costRange(5, 50), 0))
	if got.Action != app.PolicyRun {
		t.Errorf("Action = %v, want RUN — an undeclared budget must never fire", got.Action)
	}
	for _, rc := range got.ReasonCodes {
		if rc == domain.ReasonTurnCostBudgetWarnExceeded || rc == domain.ReasonTurnCostBudgetCheckpointExceeded {
			t.Errorf("budget reason code %q present without a declared budget", rc)
		}
	}
}

// TestCostBudget_InactiveWithoutEstimate: a declared budget with no cost
// estimate (nil — cold session with no token band) stays silent; unknown
// is not zero, and it is certainly not "over budget".
func TestCostBudget_InactiveWithoutEstimate(t *testing.T) {
	d := NewDecider()
	if got := d.Decide(budgetRequest(nil, 0.10)); got.Action != app.PolicyRun {
		t.Errorf("Action = %v, want RUN — no estimate must never fire the budget rule", got.Action)
	}
}

// TestCostBudget_WarnTier: worst case exceeds the budget, optimistic
// case fits — the turn MAY breach: WARN, with both reason-code spellings.
func TestCostBudget_WarnTier(t *testing.T) {
	d := NewDecider()
	got := d.Decide(budgetRequest(costRange(0.05, 0.40), 0.25))
	if got.Action != app.PolicyWarn {
		t.Fatalf("Action = %v, want WARN", got.Action)
	}
	if got.Probability != nil {
		t.Errorf("Probability = %v, want nil — a budget comparison is never a probability", *got.Probability)
	}
	if got.Calibrated {
		t.Error("Calibrated = true, want false — the cost range is an uncalibrated estimate")
	}
	assertHasReason(t, got, domain.ReasonTurnCostBudgetWarnExceeded, ReasonTurnCostBudgetWarn)
}

// TestCostBudget_CheckpointTier: even the optimistic estimate exceeds
// the budget — the breach is essentially certain: CHECKPOINT_AND_RUN.
func TestCostBudget_CheckpointTier(t *testing.T) {
	d := NewDecider()
	got := d.Decide(budgetRequest(costRange(0.30, 0.90), 0.25))
	if got.Action != app.PolicyCheckpointAndRun {
		t.Fatalf("Action = %v, want CHECKPOINT_AND_RUN", got.Action)
	}
	assertHasReason(t, got, domain.ReasonTurnCostBudgetCheckpointExceeded, ReasonTurnCostBudgetCheckpoint)
}

// TestCostBudget_ExactBudgetDoesNotFire: thresholds are strict
// inequalities, same convention as the D-08 context thresholds — a range
// that exactly meets the budget fits it.
func TestCostBudget_ExactBudgetDoesNotFire(t *testing.T) {
	d := NewDecider()
	if got := d.Decide(budgetRequest(costRange(0.10, 0.25), 0.25)); got.Action != app.PolicyRun {
		t.Errorf("Action = %v, want RUN — HighUSD == budget is within budget", got.Action)
	}
}

// TestCostBudget_NeverDowngrades: a critical-risk-band
// CHECKPOINT_AND_RUN stays exactly as chosen when the budget tier is the
// weaker WARN; the budget rule only annotates with its reason codes.
// (Priority-1/2 gates like ExplicitDeny return before the resource
// overlays entirely — same as the D-08 context rule — so the
// never-downgrade case is exercised on the risk-band path the overlays
// actually see.)
func TestCostBudget_NeverDowngrades(t *testing.T) {
	d := NewDecider()
	req := budgetRequest(costRange(0.05, 0.40), 0.25) // budget tier: WARN
	req.Risk.OverallRisk = domain.RiskComponent{Score: 0.95, Confidence: domain.ConfidenceMedium}
	got := d.Decide(req)
	if got.Action != app.PolicyCheckpointAndRun {
		t.Fatalf("Action = %v, want the critical band's CHECKPOINT_AND_RUN preserved — never downgrade", got.Action)
	}
	assertHasReason(t, got, domain.ReasonTurnCostBudgetWarnExceeded, ReasonTurnCostBudgetWarn)
}

// TestCostBudget_ComposesWithContextRule: both resource overlays fire;
// the strongest action wins and BOTH tiers' reason codes survive
// (ADR-043: one forecast entry per resource, same shape — the decision
// discloses every threshold that was crossed).
func TestCostBudget_ComposesWithContextRule(t *testing.T) {
	d := NewDecider()
	proj := 97.0                                      // above the default 95 checkpoint threshold
	req := budgetRequest(costRange(0.05, 0.40), 0.25) // budget tier: WARN
	req.Quota = domain.QuotaForecast{
		ProjectedContextUsedP90: &proj,
		Confidence:              domain.ConfidenceMedium, // meets the D-08 bar
	}
	got := d.Decide(req)
	if got.Action != app.PolicyCheckpointAndRun {
		t.Fatalf("Action = %v, want CHECKPOINT_AND_RUN — the stronger of the two tiers", got.Action)
	}
	assertHasReason(t, got, domain.ReasonContextCheckpointThresholdExceeded, ReasonContextCheckpointThreshold)
	assertHasReason(t, got, domain.ReasonTurnCostBudgetWarnExceeded, ReasonTurnCostBudgetWarn)
}

func assertHasReason(t *testing.T, d Decision, wantDomain domain.ReasonCode, wantPolicy string) {
	t.Helper()
	foundDomain := false
	for _, rc := range d.ReasonCodes {
		if rc == wantDomain {
			foundDomain = true
		}
	}
	if !foundDomain {
		t.Errorf("ReasonCodes = %v, missing %q", d.ReasonCodes, wantDomain)
	}
	foundPolicy := false
	for _, rc := range d.PolicyReasonCodes {
		if rc == wantPolicy {
			foundPolicy = true
		}
	}
	if !foundPolicy {
		t.Errorf("PolicyReasonCodes = %v, missing %q", d.PolicyReasonCodes, wantPolicy)
	}
}
