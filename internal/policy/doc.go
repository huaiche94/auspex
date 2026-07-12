// Package policy implements the Predictor pipeline's terminal stage
// (ADR-041 / predictor-08, agents/predictor.md deliverable 10): turning
// the combined risk score (internal/predictor/risk.RuleRiskCombiner's
// app.CombineRiskResult) and the independent ten-minute runway forecast
// (internal/predictor/runway.Scorer's domain.RunwayForecast) into one of
// the frozen app.PolicyAction values (ADD §17.2's PolicyResult.Action).
//
// # Pipeline position (ADR-041)
//
//	Scope Estimator -> Token Forecaster -> Quota Forecaster -> Risk Combiner -> Policy
//	                                                        Runway Predictor ---^
//
// RiskCombiner and the Runway Predictor are Policy's two direct inputs.
// Policy does NOT consume Runway through RiskCombiner — ADR-041 corrected
// the Day-1 execution DAG specifically because the original
// predictor-07->predictor-06 edge conflated two independent questions
// ("how risky is this turn" vs "is a quota limit imminent within 10
// minutes"). CONTRACT_FREEZE.md's "Predictor pipeline ports (ADR-041)"
// section states this explicitly: "GracefulPauseService.Observe (Runway
// Forecaster) is independent of this chain — it is not a RiskCombiner
// input and RiskCombiner is not one of its inputs." Policy is the one
// place both signals legitimately meet.
//
// # Priority order (ADD §17.3)
//
// Decide evaluates gates in this fixed order, first match wins:
//
//  1. explicit deny/security;
//  2. integrity failure (fail-closed, per CONTRACT_FREEZE.md's error
//     contract: a state-integrity failure never proceeds as if it
//     succeeded);
//  3. active graceful-pause trigger (calibrated runway hit-probability
//     debounced per §17.6, or an uncalibrated emergency condition);
//  4. mandatory state checkpoint boundary;
//  5. critical pre-turn risk (overall risk band >= 0.85, ADD §16.5);
//  6. high risk (overall risk band 0.65-0.85);
//  7. medium warning (overall risk band 0.45-0.65);
//  8. run (overall risk band < 0.45).
//
// # Bands (ADD §16.5)
//
//	score      band      default action
//	<0.45      low       ALLOW  (app.PolicyRun)
//	0.45-0.65  medium    WARN   (app.PolicyWarn)
//	0.65-0.85  high      CHECKPOINT preferred when blast-radius risk is
//	                     also high, otherwise REQUIRE_CONFIRMATION
//	>=0.85     critical  CHECKPOINT (app.PolicyCheckpointAndRun)
//
// Runway probability policy is separate from the risk bands above: a
// *calibrated* P_hit_10m >= a configured threshold (default 0.80, ADD
// §17.4's auto-pause-calibrated-runway rule) observed twice with the
// §17.6 debounce discipline can trigger PAUSE. An *uncalibrated* emergency
// condition (provider-reported limit reached, used% >= 98, or estimated
// time-to-limit P50 <= 60s — ADD §17.6) can also trigger PAUSE, skipping
// the double-sample debounce (an emergency does not wait for a second
// confirming sample) but is never described as a probability claim — its
// reason code is always emergency_threshold, never a hit-probability
// percentage.
//
// # The single load-bearing invariant (Constitution §6/§7)
//
// Per the DAG's own risk note on this node ("High — must never label an
// uncalibrated score a probability") and agents/predictor.md's cold-start
// contract: whenever any upstream input this package reads is
// Calibrated == false (CombineRiskResult.OverallRisk or
// domain.RunwayForecast), the returned Decision.Probability is nil,
// unconditionally, regardless of which PolicyAction is chosen. There is
// exactly one code path that ever sets Probability to a non-nil value
// (decide.go's runwayProbability helper), and it requires
// domain.RunwayForecast.Calibrated == true as a precondition it checks
// directly, not one it infers from anything else. A risk_score/
// RiskScore value is never copied into Probability under any
// circumstance — the two fields are never assignment-compatible in this
// package's code, by construction, not by convention.
//
// # Fail-open / fail-closed (ADD §17.5)
//
// Decide never returns an error: every caller-supplied gap (a zero-value
// CombineRiskResult, a zero-value RunwayForecast, an empty/unrecognized
// domain.Confidence) degrades to the most conservative applicable action
// rather than panicking or blocking construction of a Decision. This
// package draws the fail-open/fail-closed line the same way
// CONTRACT_FREEZE.md's error contract does: an *operational* gap (missing
// telemetry, an uncalibrated or low-confidence input) fails open in the
// sense of still producing a usable, explicit decision (never silently
// treated as "everything is fine" — see riskBandDecision's default-band
// ALLOW/WARN outputs, which still carry the propagated Confidence/
// ReasonCodes disclosing the gap), while integrity-
// shaped concerns (the DAG's own "must never label an uncalibrated score
// a probability" risk note) fail closed in the sense that Probability is
// suppressed rather than guessed. See decide.go's DecideRequest.IntegrityFailure
// field for the explicit-deny/integrity-failure priority gates (§17.3
// rules 1-2), which this package exposes as caller-supplied booleans
// since detecting an integrity failure or an explicit security deny is
// outside this package's boundary (no checkpoint creation, no Git
// commands — agents/predictor.md's Boundary section).
//
// # Boundary
//
// This package returns decisions through the frozen app.PolicyAction enum
// and app.DecisionResult shape (internal/app/ports.go) plus this
// package's own richer Decision type (which a future evaluation-
// persistence node, predictor-09, can flatten into app.Evaluation /
// app.DecisionResult). It does not parse provider JSON, run Git commands,
// create checkpoints, or interrupt a process — those are other roles'
// jobs (agents/predictor.md's Boundary section, Constitution §4).
package policy
