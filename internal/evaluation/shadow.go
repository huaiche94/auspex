// shadow.go: ADR-0057 §5's shadow-first enforcement discipline at the
// evaluation-decision layer (issue #142, Cost Guard). When shadow
// enforcement is enabled, a STATISTICAL enforcement-grade decision —
// one that would block the prompt or pause the run based on estimates
// (risk scores, uncalibrated runway conditions, cost bands) — is
// recorded with its original action in policy_decisions.would_action
// and served DOWNGRADED to WARN, so enforcement can be evaluated
// against real traffic (false-positive review, estimated avoided
// spend) before it is turned on.
//
// The two fail-closed FACT gates are exempt BY DESIGN and
// unconditionally: an explicit deny (security) and a state-integrity
// failure are definite facts, not estimates — CONTRACT_FREEZE.md
// requires the integrity path to fail closed, and shadow-downgrading
// either would mean proceeding on denied or corrupted state. Shadow
// mode exists to measure statistical false positives; those two gates
// have none to measure.
package evaluation

import (
	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/policy"
)

// shadowExemptReasons are the fail-closed fact gates shadow never
// touches (decide.go priorities 1 and 2 — their exact policy-spelling
// codes).
var shadowExemptReasons = map[string]bool{
	"explicit_deny":     true,
	"integrity_failure": true,
}

// enforcementGrade reports whether an action interrupts or refuses work
// (vs. annotating it): the action set ADR-0057 §5 puts behind
// shadow-first evaluation.
func enforcementGrade(a app.PolicyAction) bool {
	switch a {
	case app.PolicyBlock, app.PolicyPause, app.PolicyPauseAndAutoResume:
		return true
	default:
		return false
	}
}

// applyShadowEnforcement returns the decision to serve and persist,
// plus the original action when a downgrade happened (nil otherwise —
// NULL in policy_decisions.would_action means "not shadowed", never
// "unknown"). When enabled and the decision is a statistical
// enforcement-grade action, the served action becomes WARN and
// RequiresConfirmation drops (WARN never requires one); every other
// field (risk score, confidence, reason codes, severity) is kept
// verbatim — shadow changes what Auspex DOES, never what it measured.
// The persisted would_action column IS the disclosure.
func applyShadowEnforcement(d policy.Decision, enabled bool) (policy.Decision, *string) {
	if !enabled || !enforcementGrade(d.Action) {
		return d, nil
	}
	for _, code := range d.PolicyReasonCodes {
		if shadowExemptReasons[code] {
			return d, nil
		}
	}
	wouldAction := string(d.Action)
	d.Action = app.PolicyWarn
	d.RequiresConfirmation = false
	return d, &wouldAction
}
