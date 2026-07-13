package policy

import (
	"math"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// Decision is this package's own richer decision shape. It carries
// everything the frozen app.DecisionResult (internal/app/ports.go) does
// not have room for yet (RiskScore, Probability, Confidence, reason
// codes) so a future evaluation-persistence node (predictor-09) can
// flatten it into app.Evaluation/app.DecisionResult without this package
// guessing at that shape itself (agents/predictor.md's Boundary: "Return
// decisions through frozen ports" — Decision.Action is always one of the
// frozen app.PolicyAction values; Decision itself is this node's
// documented bridge type, not a competing frozen contract).
type Decision struct {
	// Action is the frozen policy action (app.PolicyAction).
	Action app.PolicyAction

	// Calibrated is true only when every input this decision was based on
	// is itself calibrated. Mirrors domain.RiskComponent/RunwayForecast's
	// own Calibrated field one level up, at the whole-decision level.
	Calibrated bool

	// Confidence is the most conservative (lowest) Confidence among the
	// inputs actually consulted to reach Action.
	Confidence domain.Confidence

	// RiskScore is the 0-1 (never probability) score that most directly
	// drove Action — normally CombineRiskResult.OverallRisk.Score, or the
	// RunwayForecast.RiskScore when a runway-driven gate fired instead.
	// Always populated (never NaN/Inf — see clamp01 in the risk package
	// this mirrors); a 0 value is a real "no risk signal", never a
	// stand-in for "unknown" (unknown is carried by Confidence/
	// ReasonCodes instead, per ADD principle 1).
	RiskScore float64

	// Probability is nil unless every contributing input was Calibrated
	// == true, in which case it carries the calibrated ten-minute runway
	// hit-probability that justified the decision. THIS IS THE
	// LOAD-BEARING FIELD for Constitution §6/§7: runwayPauseDecision is
	// the only function in this entire package that ever assigns this
	// field a non-nil value, and it does so in exactly two places, both
	// guarded by an explicit `rf.Calibrated &&` check immediately before
	// the assignment (grep this package for `Probability:` — every other
	// call site sets it to literal nil). See coldstart_test.go's
	// TestColdStart* suite for exhaustive proof this holds for every
	// PolicyAction.
	Probability *float64

	// ReasonCodes are the frozen ADD §16.4 enum reason codes propagated
	// from the upstream RiskCombiner components (CombineRiskResult's own
	// ReasonCodes fields) that are relevant to Action.
	ReasonCodes []domain.ReasonCode

	// PolicyReasonCodes are this package's own plain-string reason codes
	// (mirroring internal/predictor/runway's precedent of a
	// package-local plain-string vocabulary distinct from the shared
	// domain.ReasonCode closed enum) plus any runway-sourced reason
	// strings (domain.RunwayForecast.ReasonCodes is frozen as []string,
	// not []domain.ReasonCode — see runway/runway.go's own doc comment
	// on why). Includes ReasonEmergencyThreshold when Action was driven
	// by the uncalibrated emergency gate.
	PolicyReasonCodes []string

	// RequiresConfirmation mirrors ADD §17.2's PolicyResult.RequiresConfirmation.
	RequiresConfirmation bool

	// Severity mirrors ADD §17.2's PolicyResult.Severity — one of "none",
	// "warning", "high", "critical", "emergency" — this package's own
	// human/log-facing label, not a frozen enum.
	Severity string
}

// DecideRequest bundles Policy's two direct pipeline inputs (ADR-041:
// CombineRiskResult from RiskCombiner, RunwayForecast from the
// independent Runway Predictor) plus the small set of caller-supplied
// signals this package's boundary excludes it from detecting itself
// (explicit deny/security and integrity failure — ADD §17.3 priority
// rules 1-2 — detecting either requires capabilities this role's Boundary
// explicitly excludes: no Git commands, no checkpoint creation, no
// process interruption).
type DecideRequest struct {
	// Risk is RiskCombiner's output (predictor-07). A zero value has
	// OverallRisk.Confidence == "" (unset), which riskBandDecision
	// treats as the most conservative Confidence via Decision.Confidence
	// simply propagating whatever upstream reported — it is the caller's
	// responsibility to populate a real domain.ConfidenceUnavailable
	// value on a genuinely-missing input (mirrors risk.RuleRiskCombiner's
	// own newRiskComponent, which defaults an empty Confidence to
	// domain.ConfidenceLow — see risk/combiner.go), never a false "zero
	// risk" claim: OverallRisk.Score stays 0 only if every component
	// score is honestly 0, not because this package fabricates it.
	Risk app.CombineRiskResult

	// Runway is the independent ten-minute runway forecast
	// (predictor-06, internal/predictor/runway.Scorer.Score's output),
	// consumed directly per ADR-041 — never derived from Risk.
	Runway domain.RunwayForecast

	// ExplicitDeny signals an external explicit deny/security decision
	// (ADD §17.3 priority 1) that this package did not itself compute
	// (out of this package's boundary — detecting a security policy deny
	// requires capabilities this role does not own). When true, Decide
	// returns PolicyBlock unconditionally, before any risk/runway
	// evaluation.
	ExplicitDeny bool

	// IntegrityFailure signals a caller-detected state-integrity failure
	// (ADD §17.3 priority 2 — e.g. a checkpoint checksum mismatch
	// reported by the checkpoint role). Per CONTRACT_FREEZE.md's error
	// contract, an integrity failure always fails closed. When true,
	// Decide returns PolicyBlock unconditionally (after ExplicitDeny,
	// before any risk/runway evaluation).
	IntegrityFailure bool

	// MandatoryCheckpointBoundary signals an external mandatory state
	// checkpoint boundary (ADD §17.3 priority 4, e.g. ADD §17.4's
	// document-section-persist rule: a progress-node kind/transition
	// combination that always requires a checkpoint regardless of risk
	// score). Detecting a Progress Tree node kind/transition is outside
	// this package's boundary (internal/progress owns that), so the
	// caller supplies the already-evaluated boolean.
	MandatoryCheckpointBoundary bool

	// PriorRunwayHitConfirmed is the ADD §17.6 debounce bit: whether the
	// immediately preceding evaluation for this session already saw a
	// qualifying calibrated hit-probability (>= threshold) or a
	// qualifying emergency condition. Auto-pause on a calibrated
	// forecast requires two consecutive qualifying observations; the
	// emergency path does not require this (ADD §17.6: "Emergency 可跳過
	// double-sample"). This package is stateless per call (like
	// runway.Scorer) — the caller (expected to be the same evaluation
	// loop that already tracks turn-over-turn history) owns this one bit
	// of debounce state.
	PriorRunwayHitConfirmed bool

	// Config carries this decision's thresholds. A zero value uses
	// DefaultConfig().
	Config Config
}

// Config carries Decide's configurable thresholds so a deployment can
// tune sensitivity without recompiling. All fields default to the ADD's
// own documented day-one values via DefaultConfig.
type Config struct {
	// RunwayHitProbabilityThreshold is ADD §17.4's calibrated
	// hit-probability auto-pause gate (default 0.80).
	RunwayHitProbabilityThreshold float64
}

// DefaultConfig returns the ADD-literal day-one threshold set.
func DefaultConfig() Config {
	return Config{
		RunwayHitProbabilityThreshold: DefaultRunwayHitProbabilityThreshold,
	}
}

func (c Config) normalized() Config {
	if c.RunwayHitProbabilityThreshold <= 0 {
		c.RunwayHitProbabilityThreshold = DefaultRunwayHitProbabilityThreshold
	}
	return c
}

// Decider is the Policy stage's stateless decision engine. Like
// risk.RuleRiskCombiner and runway.Scorer, it holds no state or
// configuration of its own beyond what arrives per-call — all history
// (e.g. PriorRunwayHitConfirmed) is the caller's responsibility to track
// and pass in.
type Decider struct{}

// NewDecider constructs a Decider.
func NewDecider() *Decider {
	return &Decider{}
}

// Decide implements ADD §17.3's priority order, evaluating gates in
// order and returning on the first match. It never returns an error —
// every input gap degrades to the most conservative applicable decision
// (ADD §17.5's fail-open/fail-closed table; see doc.go's "Fail-open /
// fail-closed" section for how this package draws that line).
func (d *Decider) Decide(req DecideRequest) Decision {
	cfg := req.Config.normalized()

	// Priority 1: explicit deny/security.
	if req.ExplicitDeny {
		return Decision{
			Action:            app.PolicyBlock,
			Calibrated:        true, // an explicit deny is a definite fact, not an estimate
			Confidence:        domain.ConfidenceExact,
			RiskScore:         1.0,
			Probability:       nil,
			PolicyReasonCodes: []string{"explicit_deny"},
			Severity:          "critical",
		}
	}

	// Priority 2: integrity failure. CONTRACT_FREEZE.md: "A state-integrity
	// failure ... MUST fail closed ... the caller must not proceed as if
	// it succeeded."
	if req.IntegrityFailure {
		return Decision{
			Action:            app.PolicyBlock,
			Calibrated:        true,
			Confidence:        domain.ConfidenceExact,
			RiskScore:         1.0,
			Probability:       nil,
			PolicyReasonCodes: []string{"integrity_failure"},
			Severity:          "critical",
		}
	}

	// Priority 3: active graceful-pause trigger (calibrated debounced
	// runway hit-probability, or an uncalibrated emergency condition).
	if pause, ok := runwayPauseDecision(req.Runway, cfg, req.PriorRunwayHitConfirmed); ok {
		return pause
	}

	// Priority 4: mandatory state checkpoint boundary, independent of
	// risk score.
	if req.MandatoryCheckpointBoundary {
		overall := req.Risk.OverallRisk
		return Decision{
			Action:            app.PolicyCheckpointAndRun,
			Calibrated:        overall.Calibrated,
			Confidence:        overall.Confidence,
			RiskScore:         clamp01Risk(overall.Score),
			Probability:       nil, // mandatory boundary is structural, never a probability claim
			ReasonCodes:       overall.ReasonCodes,
			PolicyReasonCodes: []string{"mandatory_checkpoint_boundary"},
			Severity:          "high",
		}
	}

	// Priorities 5-8: risk-band decision (ADD §16.5).
	return riskBandDecision(req.Risk)
}

// runwayPauseDecision implements ADD §17.3 priority 3 and §17.4's
// auto-pause-calibrated-runway rule plus §17.6's emergency trigger.
// Returns ok=false when neither condition fires, so the caller falls
// through to the next priority.
func runwayPauseDecision(rf domain.RunwayForecast, cfg Config, priorConfirmed bool) (Decision, bool) {
	// Uncalibrated emergency condition (ADD §17.6): provider reports
	// limit reached, or used% >= 98, or estimated time-to-limit P50 <=
	// 60s. Never described as a probability — agents/predictor.md: "PAUSE
	// with reason emergency_threshold, not a probability claim."
	if emergency, reason := isRunwayEmergency(rf); emergency {
		return Decision{
			Action:            app.PolicyPause,
			Calibrated:        false,
			Confidence:        rf.Confidence,
			RiskScore:         clamp01Risk(rf.RiskScore),
			Probability:       nil, // emergency path is uncalibrated by definition; never a probability
			PolicyReasonCodes: append([]string{ReasonEmergencyThreshold}, reason),
			Severity:          "emergency",
		}, true
	}

	// Calibrated ten-minute hit-probability gate (ADD §17.4), debounced
	// per §17.6: requires two consecutive qualifying observations.
	if rf.Calibrated && rf.HitProbability != nil && *rf.HitProbability >= cfg.RunwayHitProbabilityThreshold {
		if !priorConfirmed {
			// First qualifying observation: arm, but do not pause yet
			// (ADD §17.6's double-sample requirement). Report a WARN so
			// the elevated-but-not-yet-actionable signal is still
			// visible, never silently dropped.
			p := *rf.HitProbability
			return Decision{
				Action:            app.PolicyWarn,
				Calibrated:        true,
				Confidence:        rf.Confidence,
				RiskScore:         clamp01Risk(rf.RiskScore),
				Probability:       &p, // calibrated: true was just checked above — safe to report
				PolicyReasonCodes: []string{"runway_hit_probability_armed_pending_confirmation"},
				Severity:          "high",
			}, true
		}
		p := *rf.HitProbability
		return Decision{
			Action:            app.PolicyPause,
			Calibrated:        true,
			Confidence:        rf.Confidence,
			RiskScore:         clamp01Risk(rf.RiskScore),
			Probability:       &p, // calibrated: true was just checked above — safe to report
			PolicyReasonCodes: []string{"runway_hit_probability_confirmed_twice"},
			Severity:          "critical",
		}, true
	}

	return Decision{}, false
}

// isRunwayEmergency implements ADD §17.6's emergency trigger conditions.
// RunwayForecast has no direct "provider reports limit reached" boolean
// of its own (that signal is domain.QuotaObservation.Reached, one layer
// up, which runway.Scorer already folds into RiskScore==1.0 with
// Confidence high per its own Score implementation) — this function
// checks the two conditions RunwayForecast does expose directly
// (CurrentUsedPercent, EstimatedTimeToLimitP50Seconds), plus treats a
// RiskScore of exactly 1.0 with high confidence as the
// already-folded-in "limit reached" signal from upstream.
func isRunwayEmergency(rf domain.RunwayForecast) (bool, string) {
	if rf.RiskScore >= 1.0 && rf.Confidence == domain.ConfidenceHigh {
		return true, "runway_limit_reached"
	}
	if rf.CurrentUsedPercent != nil && *rf.CurrentUsedPercent >= emergencyUsedPercentThreshold {
		return true, "runway_used_percent_critical"
	}
	if rf.EstimatedTimeToLimitP50Seconds != nil && *rf.EstimatedTimeToLimitP50Seconds <= emergencyTimeToLimitP50SecondsCeil {
		return true, "runway_time_to_limit_critical"
	}
	return false, ""
}

// riskBandDecision implements ADD §16.5's band table plus
// agents/predictor.md's "predicted P90 exceeds available headroom or
// high blast radius: CHECKPOINT" refinement of the "high" band. Never
// sets Probability — OverallRisk.Score is a risk score, and per
// Constitution §7 rule 7/agents/predictor.md's cold-start contract, this
// function has no calibrated-probability input to report even when
// OverallRisk.Calibrated happens to be true (RiskCombiner's Score is
// defined as a 0-1 risk score, never a probability, regardless of
// Calibrated — see risk/combiner.go's own doc comment: "Score is not
// probability").
func riskBandDecision(risk app.CombineRiskResult) Decision {
	overall := risk.OverallRisk
	score := clamp01Risk(overall.Score)
	blastScore := clamp01Risk(risk.BlastRadiusRisk.Score)

	switch {
	case score >= bandCriticalThreshold:
		return Decision{
			Action:            app.PolicyCheckpointAndRun,
			Calibrated:        overall.Calibrated,
			Confidence:        overall.Confidence,
			RiskScore:         score,
			Probability:       nil,
			ReasonCodes:       overall.ReasonCodes,
			PolicyReasonCodes: []string{"critical_risk_band"},
			Severity:          "critical",
		}
	case score >= bandHighThreshold:
		action := app.PolicyRequireConfirmation
		policyReason := "high_risk_band"
		if blastScore >= blastRadiusHighThreshold {
			action = app.PolicyCheckpointAndRun
			policyReason = "high_blast_radius"
		}
		return Decision{
			Action:               action,
			Calibrated:           overall.Calibrated,
			Confidence:           overall.Confidence,
			RiskScore:            score,
			Probability:          nil,
			ReasonCodes:          overall.ReasonCodes,
			PolicyReasonCodes:    []string{policyReason},
			RequiresConfirmation: action == app.PolicyRequireConfirmation,
			Severity:             "high",
		}
	case score >= bandMediumThreshold:
		return Decision{
			Action:            app.PolicyWarn,
			Calibrated:        overall.Calibrated,
			Confidence:        overall.Confidence,
			RiskScore:         score,
			Probability:       nil,
			ReasonCodes:       overall.ReasonCodes,
			PolicyReasonCodes: []string{"medium_risk_band"},
			Severity:          "warning",
		}
	default:
		return Decision{
			Action:            app.PolicyRun,
			Calibrated:        overall.Calibrated,
			Confidence:        overall.Confidence,
			RiskScore:         score,
			Probability:       nil,
			ReasonCodes:       overall.ReasonCodes,
			PolicyReasonCodes: []string{"low_risk_band"},
			Severity:          "none",
		}
	}
}

// clamp01Risk bounds v to [0,1], defending against NaN/Inf ever escaping
// this package regardless of how an upstream RiskCombiner/RunwayForecast
// value misbehaves (agents/predictor.md's required "no
// divide-by-zero/NaN/Inf" test) — mirrors
// internal/predictor/risk.clamp01 exactly, including its documented
// choice to treat NaN as the most conservative (highest-risk) case, 1.0,
// rather than a placid, unearned low score (a NaN score represents a
// data problem in an upstream signal, not license to under-report risk).
func clamp01Risk(v float64) float64 {
	if math.IsNaN(v) {
		return 1.0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
