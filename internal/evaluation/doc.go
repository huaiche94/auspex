// Package evaluation implements app.EvaluationService (internal/app/ports.go,
// ADD §9.9): the frozen evaluate/decide/authorize contract every provider
// hook handler and runtime orchestrator calls through, never a concrete
// predictor/policy type directly.
//
// This is predictor-09 (docs/implementation/day1/EXECUTION_DAG.md,
// agents/predictor.md deliverable #11, "Evaluation persistence"), deps
// predictor-01 (this package's own migrations/0040-0044_*.sql) and
// predictor-08 (internal/policy.Decider). Per agents/predictor.md's own
// path note ("If internal/evaluation is absent from the frozen layout, use
// the exact path assigned by the contract-integrator; do not create a
// competing package") and CONTRACT_FREEZE.md, which names no alternate
// path for this concern anywhere in its "Import paths" table: this
// package's own predictor-01-authored migration file comments
// (0040_feature_vectors.sql through 0044_authorizations.sql) already
// reference "predictor-09" and "predictor's evaluation-persistence layer"
// by name against this exact path, confirming it is correct before this
// node started building — no separate contract-integrator sign-off exists
// beyond that, since Bootstrap explicitly deferred "predictor internals"
// (CONTRACT_FREEZE.md "What Bootstrap did NOT freeze").
//
// # Pipeline wiring (ADR-041)
//
//	Scope Estimator -> Token Forecaster -> Quota Forecaster -> Risk Combiner -> Policy
//
// EvaluateTurn runs this chain end-to-end for one turn using the four
// narrow app.* pipeline ports (ScopeEstimator/TokenForecaster/
// QuotaForecaster/RiskCombiner — each swappable, ADD §1.4) plus
// internal/policy.Decider, then persists a feature_vectors row (the
// FeatureSource-derived inputs, migration 0040), a predictions row (the
// Stage 1-4 forecast/risk output, migration 0041), and a policy_decisions
// row (Decider's output, migration 0043) inside one app.TxRunner.WithTx
// call — matching CONTRACT_FREEZE.md's transaction-boundary discipline
// even though ConsumeAuthorization is the only method that section names
// explicitly (the same atomicity reasoning applies: a prediction row with
// no corresponding policy_decisions row, or vice versa, is a partial
// evaluation, not a valid state).
//
// The independent Runway Predictor (internal/predictor/runway.Scorer) is
// NOT re-run by EvaluateTurn — ADR-041 states it plugs into
// GracefulPauseService.Observe, a different frozen port owned by a
// different role (runtime), and is not a RiskCombiner input. This
// package's DataSource supplies the *most recent already-computed*
// domain.RunwayForecast (if any) purely so Policy's runway-driven PAUSE
// gate can be evaluated during Decide — it never computes a new one.
//
// # Decide: read-back, not recompute
//
// app.DecideRequest carries only an EvaluationID, no risk/runway payload
// (internal/app/ports.go is frozen; this package does not widen it). Since
// internal/orchestrator's own Evaluate helper already calls
// EvaluateTurn immediately followed by Decide(ctx, DecideRequest{EvaluationID:
// evaluation.ID}) with nothing else available to supply, Decide cannot
// plausibly recompute a decision from inputs it does not have — it reads
// back the policy_decisions row EvaluateTurn already computed and stored
// for that evaluation. This is a deliberate, documented choice (agents/
// predictor.md's task explicitly allows either, "use your judgment ...
// but be explicit"): Decide never re-runs internal/policy.Decider itself.
//
// # ConsumeAuthorization scope note (predictor-09 vs predictor-10)
//
// docs/implementation/day1/EXECUTION_DAG.md defines a separate downstream
// node, predictor-10 ("One-time authorization", deps: predictor-09,
// validation `go test ./internal/evaluation/... -run Authorization`), and
// migration 0044_authorizations.sql's own comment (written by this role in
// an earlier wave) says exactly-once consumption is "enforced by
// predictor-10's service logic." This node's assignment nonetheless
// requires ConsumeAuthorization to exist, compile against the frozen
// interface, and behave correctly (agents/predictor.md deliverable #12 is
// listed as in-scope reading material for this wave, and app.
// EvaluationService cannot be partially implemented — Go interfaces are
// all-or-nothing). Rather than leaving a stub that would violate
// Constitution §7 rule 11 ("does not declare a task complete without
// durable evidence") or silently guessing which instruction should win,
// this node builds ConsumeAuthorization for real now, atomically
// (CONTRACT_FREEZE.md: "ConsumeAuthorization MUST be atomic with whatever
// action it authorizes ... no window where the authorization is marked
// consumed but the allowed action didn't happen, or vice versa"), with
// exactly-once consumption, expiry, and prompt/session binding all
// enforced at the storage layer inside a single WithTx call — but treats
// predictor-10's own dedicated `-run Authorization` validation gate and
// replay-focused hardening pass as still the authoritative place a future
// wave re-verifies and extends this behavior (e.g. against
// runtime-a08/runtime-b06's real integration needs), per Constitution §4's
// "if a role needs a change to a file it doesn't own ... it works around
// the gap with a documented assumption." This wave's own required-tests
// list (deterministic output, consume-exactly-once, stale/wrong-prompt/
// wrong-session rejection, clock-bound expiry) is implemented and passing
// here; predictor-10 is not started, stubbed, or scaffolded beyond what
// already had to exist for this interface to compile.
//
// Addendum (predictor-10, later wave): the dedicated hardening/re-verification
// pass named above has now run. It found and fixed one real gap — the
// prompt-hash binding check used to key off whether the REQUEST supplied a
// PromptHash rather than whether the authorization ROW was actually issued
// with one, letting a caller omit PromptHash to bypass prompt binding
// entirely (see service.go's inline comment on that check, and
// authorization_test.go's "Section 2: prompt/session binding hardening" for
// the adversarial test that caught it). Every other adversarial scenario
// exercised this wave — higher-contention concurrent replay, tight
// sequential replay loops, replay racing the expiry boundary,
// nanosecond-adjacent expiry boundaries, whitespace/case/unicode-
// normalization variants on the binding fields — passed against predictor-09's
// existing logic unchanged. See docs/implementation/day1/predictor.md's
// predictor-10 entry for the full account.
//
// # Boundary (agents/predictor.md)
//
// No provider JSON parsing, Git commands, checkpoint creation, or process
// interruption. This package returns decisions through the frozen
// app.EvaluationService port only.
package evaluation
