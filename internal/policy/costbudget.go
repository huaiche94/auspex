// costbudget.go implements ADR-043 increment 3: the user-declared
// per-turn COST budget as a policy-active resource, structurally a twin
// of context.go's D-08 utilization rule (same never-downgrade ladder,
// same annotation-vs-upgrade split, same Constitution-#2 posture).
//
// The activation model is the OPPOSITE of D-08's, by ADR-043's own
// decision text: context thresholds ship factory-ACTIVE (an objective
// ceiling every session has), while a cost budget is policy-active ONLY
// when the user declared one — "absence of a budget means the resource
// is simply not policy-active (explicit degradation, never a guess)".
// That opt-in is also why there is no cold-start confidence gate here,
// unlike contextProjectionMeetsBar: a user who declares a budget has
// asked to be warned against the best estimate available, and today's
// estimates are conservative cold-start ranges labeled as such — the
// decision's Calibrated/Confidence fields still disclose exactly that.
package policy

import (
	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pricing"
)

// ReasonTurnCostBudgetWarn / ReasonTurnCostBudgetCheckpoint are this
// package's decision-record spellings of the budget tiers (the
// domain.ReasonTurnCostBudget* codes are the cross-layer taxonomy
// entries), mirroring the ReasonContext* pair's dual-spelling scheme.
const (
	ReasonTurnCostBudgetWarn       = "turn_cost_budget_warn_exceeded"
	ReasonTurnCostBudgetCheckpoint = "turn_cost_budget_checkpoint_exceeded"
)

type costBudgetTier int

const (
	costBudgetTierNone costBudgetTier = iota
	costBudgetTierWarn
	costBudgetTierCheckpoint
)

// costBudgetTierOf evaluates the declared per-turn budget against the
// turn's estimated cost range. Tier semantics on the honest-range cost
// model (pricing.EstimateTurnCost's deliberately wide Low..High band):
//
//   - checkpoint: even the OPTIMISTIC estimate (LowUSD — every token
//     billed as input at the P50 count) exceeds the budget; the turn is
//     essentially certain to breach it.
//   - warn: the WORST-CASE estimate (HighUSD — every token billed as
//     output at the P90 count) exceeds the budget; the turn may breach it.
//   - none: budget unset (<= 0), no cost estimate, or the whole range
//     fits — each of which means exactly today's behavior.
func costBudgetTierOf(cost *pricing.CostRange, cfg Config) costBudgetTier {
	if cfg.TurnCostBudgetUSD <= 0 || cost == nil {
		return costBudgetTierNone
	}
	switch {
	case cost.LowUSD > cfg.TurnCostBudgetUSD:
		return costBudgetTierCheckpoint
	case cost.HighUSD > cfg.TurnCostBudgetUSD:
		return costBudgetTierWarn
	default:
		return costBudgetTierNone
	}
}

// applyTurnCostBudget overlays the budget rule onto the decision the
// preceding gates (including applyContextThresholds) produced. Identical
// discipline to applyContextThresholds:
//
//   - Tier none: base returned untouched.
//   - Tier fired, base at least as strong: annotation only — the tier's
//     reason codes are added, nothing else moves.
//   - Tier fired, base strictly weaker: action upgrades to the tier's
//     (WARN or CHECKPOINT_AND_RUN). Probability is nil unconditionally (a
//     budget comparison on an estimated range is never a hit probability
//     — Constitution principle #2); Calibrated ANDs in false because the
//     cost range is an uncalibrated estimate by construction (its
//     Source/label say so on every surface); Confidence keeps the base's
//     value (the cost estimate carries no Confidence of its own — the
//     token forecast's confidence already flowed into the base via the
//     risk path); RiskScore keeps the base's (no budget-specific risk
//     term exists in ADD §16.2 — inventing one here would be a made-up
//     coefficient, which D-10's grounding discipline forbids).
func applyTurnCostBudget(base Decision, req DecideRequest, cfg Config) Decision {
	tier := costBudgetTierOf(req.Cost, cfg)
	if tier == costBudgetTierNone {
		return base
	}

	var (
		tierAction   app.PolicyAction
		tierDomain   domain.ReasonCode
		tierPolicy   string
		tierSeverity string
	)
	switch tier {
	case costBudgetTierCheckpoint:
		tierAction = app.PolicyCheckpointAndRun
		tierDomain = domain.ReasonTurnCostBudgetCheckpointExceeded
		tierPolicy = ReasonTurnCostBudgetCheckpoint
		tierSeverity = "critical"
	default: // costBudgetTierWarn
		tierAction = app.PolicyWarn
		tierDomain = domain.ReasonTurnCostBudgetWarnExceeded
		tierPolicy = ReasonTurnCostBudgetWarn
		tierSeverity = "warning"
	}

	if strengthOf(base.Action) >= strengthOf(tierAction) {
		base.ReasonCodes = appendReasonCodeOnce(base.ReasonCodes, tierDomain)
		base.PolicyReasonCodes = append(base.PolicyReasonCodes, tierPolicy)
		return base
	}

	return Decision{
		Action:               tierAction,
		Calibrated:           false, // an estimated cost range is uncalibrated by construction this wave
		Confidence:           base.Confidence,
		RiskScore:            base.RiskScore,
		Probability:          nil, // a budget comparison is never a probability (Constitution principle #2)
		ReasonCodes:          appendReasonCodeOnce(base.ReasonCodes, tierDomain),
		PolicyReasonCodes:    append(base.PolicyReasonCodes, tierPolicy),
		RequiresConfirmation: false,
		Severity:             tierSeverity,
	}
}
