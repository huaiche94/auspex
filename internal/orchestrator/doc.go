// Package orchestrator wires the frozen internal/app ports into
// Auspex's day-one pipeline (Auspex_ADD.md §13; agents/runtime.md
// Part B "Pipeline behavior"). It is the layer between provider-facing
// input (CLI flags, a normalized hook event) and the frozen cross-role
// services (internal/app/ports.go) — it does not implement prediction,
// policy, checkpointing, or pause logic itself; it sequences calls into
// the services that do.
//
// # What this package sequences
//
// agents/runtime.md Part B "Pipeline behavior" lists twelve steps overall.
// This package began (runtime-b03) as just the Evaluate pipeline — steps
// 1-6: receive input, resolve repository/worktree/session, load Progress
// Tree + usage observations, snapshot Git state, evaluate through the
// predictor role, apply policy — and has since grown to own the rest of
// the hook lifecycle around it: the hook handlers (hooks.go), decision
// allow/deny (decision.go), the daemon loop (daemon.go), pause lifecycle
// (pauselifecycle.go), progress completion (progresscomplete.go), and GC
// (gc.go). Its job stays the same throughout — it sequences calls into the
// frozen internal/app services and returns plain Go values; turning a
// result into a provider-shaped HTTP/CLI response is still the caller's
// job, not this pipeline's.
//
// # What "resolve repository/worktree/session" means at this node
//
// No frozen internal/app port exists yet for repository/worktree/session
// *resolution* (as opposed to the entities themselves, which
// internal/domain already types). Auspex_ADD.md §13.2 stage 1 groups
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
// and well-formed, not as a new database lookup layer. If a later phase
// freezes a real ResolverService port, swapping this node's resolve step
// to call it is a localized, additive change.
//
// # Predictor pipeline: real
//
// Stage 5 ("evaluate through the predictor role") calls
// app.EvaluationService.EvaluateTurn — the frozen port. The real
// implementation (predictor-08 Policy / predictor-09 Evaluation
// persistence) has since landed, and cmd/auspex/wire.go injects it via
// wiring.App; decision.go treats a non-nil EvaluationService as a HARD
// dependency now, not the fake runtime-b03 started against. This vindicated
// the original design: no code in this package ever hardcoded a fake, so
// the swap to the real predictor.EvaluationService was a wiring-layer
// change only (internal/app/wiring), not an orchestrator change — this
// package depends solely on the app.EvaluationService interface type. Tests
// still inject internal/testutil/fakes.FakeEvaluationService where they
// want to drive the pipeline without the real predictor.
package orchestrator
