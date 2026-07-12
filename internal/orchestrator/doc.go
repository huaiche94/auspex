// Package orchestrator wires the frozen internal/app ports into
// Preflight's day-one pipeline (Preflight_ADD.md §13; agents/runtime.md
// Part B "Pipeline behavior"). It is the layer between provider-facing
// input (CLI flags, a normalized hook event) and the frozen cross-role
// services (internal/app/ports.go) — it does not implement prediction,
// policy, checkpointing, or pause logic itself; it sequences calls into
// the services that do.
//
// # Scope of this node (runtime-b03: the Evaluate pipeline)
//
// agents/runtime.md Part B "Pipeline behavior" lists twelve steps overall;
// this node covers steps 1-6 (receive input, resolve repository/worktree/
// session, load Progress Tree + usage observations, snapshot Git state,
// evaluate through the predictor role, apply policy) ending at a returned
// app.Evaluation plus an app.DecisionResult. Steps 7-8 (produce a
// provider-compatible response for allow / persist+return a decision ID
// for block-checkpoint) are runtime-b04/b06's concern (hook handlers,
// decision allow/deny) — this package exposes the Evaluate/Decide result
// as a plain Go value; turning it into a provider-shaped HTTP/CLI response
// is the caller's job, not this pipeline's.
//
// # What "resolve repository/worktree/session" means at this node
//
// No frozen internal/app port exists yet for repository/worktree/session
// *resolution* (as opposed to the entities themselves, which
// internal/domain already types). Preflight_ADD.md §13.2 stage 1 groups
// "provider, repo, worktree, session, task" resolution as one step, but
// building a new persistence-backed resolver service here would both
// duplicate whatever foundation/checkpoint eventually own for that (a
// `repositories`/`worktrees`/`provider_sessions` row already exists per
// the schema those roles shipped) and violate Constitution §7 rule 10 (no
// speculative abstractions this milestone doesn't need). This node
// therefore takes the resolved IDs as direct input (EvaluateRequest's
// SessionID/RepositoryID/WorktreeID/TaskID fields) — the realistic shape
// for a hook handler that already has a provider session ID from the
// normalized event, or a CLI command run inside an already-`init`ed
// repository — and treats "resolve" as validating those IDs are present
// and well-formed, not as a new database lookup layer. If a later wave
// freezes a real ResolverService port, swapping this node's resolve step
// to call it is a localized, additive change.
//
// # Predictor pipeline: fake this wave
//
// Stage 5 ("evaluate through the predictor role") calls
// app.EvaluationService.EvaluateTurn — the frozen port — but no real
// implementation exists yet (predictor-08 Policy/predictor-09 Evaluation
// persistence are not built this wave; EXECUTION_DAG.md marks runtime-b03
// "Soft/fake-able on predictor-08/predictor-09; needs the real thing by
// merge time"). Evaluate is wired against whatever app.EvaluationService
// the caller injects via wiring.App — this wave, that is
// internal/testutil/fakes.FakeEvaluationService (see
// docs/implementation/day1/runtime.md's Wave 5 section for the exact call
// site). No code in this package hardcodes a fake; swapping to the real
// predictor.EvaluationService, once it lands, is a wiring-layer change
// only (internal/app/wiring), not an orchestrator change — this package
// depends solely on the app.EvaluationService interface type.
package orchestrator
