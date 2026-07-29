// sessionbudget.go implements the policy half of ADR-0057 / issue
// #141's session budget envelope: the user-declared SESSION budget as a
// policy-active resource, structurally a sibling of costbudget.go's
// ADR-043 per-turn rule (same opt-in activation, same overlay
// discipline, same never-downgrade ladder) — extended from one turn's
// band to the session's running ledger.
//
// Tier semantics (ADR-0057 §4's managed+end-only guarantee, expressed
// pre-turn: "no new turn that cannot be reserved"):
//
//   - exhausted: the session's KNOWN spend alone already meets or
//     exceeds the envelope. Action PAUSE — the statistical
//     enforcement-grade action, which means the #142 shadow machinery
//     (would_action, WARN downgrade) applies to it end to end; a real
//     enforcement consumer (the managed runner honoring a pre-turn
//     PAUSE with checkpoint-then-refuse) is the M11 follow-up slice.
//   - reservation: spend plus the next turn's worst-case band (HighUSD)
//     would breach the envelope: the turn cannot be reserved. Action
//     WARN — the operator sees it before the turn runs.
//   - none: no envelope declared, spend unknown (nil — unknown is not
//     zero), or everything fits.
//
// Spend is an attribution-model sum and the band is uncalibrated; both
// facts keep Calibrated false and Probability nil on any upgraded
// decision (Constitution principle #2), exactly as costbudget.go does.
package policy

import (
	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pricing"
)

// ReasonSessionBudgetReservation / ReasonSessionBudgetExhausted are this
// package's decision-record spellings of the envelope tiers (the
// domain.ReasonSessionBudget* codes are the cross-layer taxonomy
// entries), mirroring costbudget.go's dual-spelling scheme.
const (
	ReasonSessionBudgetReservation = "session_budget_reservation_exceeded"
	ReasonSessionBudgetExhausted   = "session_budget_exhausted"
)

type sessionBudgetTier int

const (
	sessionBudgetTierNone sessionBudgetTier = iota
	sessionBudgetTierReservation
	sessionBudgetTierExhausted
)

// sessionBudgetTierOf evaluates the declared envelope against the
// session's known spend and the next turn's estimated band.
func sessionBudgetTierOf(spent *float64, cost *pricing.CostRange, cfg Config) sessionBudgetTier {
	if cfg.SessionBudgetUSD <= 0 || spent == nil {
		return sessionBudgetTierNone
	}
	if *spent >= cfg.SessionBudgetUSD {
		return sessionBudgetTierExhausted
	}
	if cost != nil && *spent+cost.HighUSD > cfg.SessionBudgetUSD {
		return sessionBudgetTierReservation
	}
	return sessionBudgetTierNone
}

// applySessionBudget overlays the envelope rule onto the decision every
// preceding gate produced — applyTurnCostBudget's exact overlay
// discipline: annotation when the base is already at least as strong,
// upgrade otherwise, bit-for-bit untouched when the tier is none.
func applySessionBudget(base Decision, req DecideRequest, cfg Config) Decision {
	tier := sessionBudgetTierOf(req.SessionSpentUSD, req.Cost, cfg)
	if tier == sessionBudgetTierNone {
		return base
	}

	var (
		tierAction   app.PolicyAction
		tierDomain   domain.ReasonCode
		tierPolicy   string
		tierSeverity string
	)
	switch tier {
	case sessionBudgetTierExhausted:
		tierAction = app.PolicyPause
		tierDomain = domain.ReasonSessionBudgetExhausted
		tierPolicy = ReasonSessionBudgetExhausted
		tierSeverity = "critical"
	default: // sessionBudgetTierReservation
		tierAction = app.PolicyWarn
		tierDomain = domain.ReasonSessionBudgetReservationExceeded
		tierPolicy = ReasonSessionBudgetReservation
		tierSeverity = "warning"
	}

	if strengthOf(base.Action) >= strengthOf(tierAction) {
		base.ReasonCodes = appendReasonCodeOnce(base.ReasonCodes, tierDomain)
		base.PolicyReasonCodes = append(base.PolicyReasonCodes, tierPolicy)
		return base
	}

	return Decision{
		Action:               tierAction,
		Calibrated:           false, // spend sum + estimated band: uncalibrated by construction
		Confidence:           base.Confidence,
		RiskScore:            base.RiskScore,
		Probability:          nil, // a budget comparison is never a probability (Constitution principle #2)
		ReasonCodes:          appendReasonCodeOnce(base.ReasonCodes, tierDomain),
		PolicyReasonCodes:    append(base.PolicyReasonCodes, tierPolicy),
		RequiresConfirmation: base.RequiresConfirmation,
		Severity:             tierSeverity,
	}
}
