// Package pause implements Auspex's Graceful Pause / Safe Points /
// Durable Scheduler state machine (Auspex_ADD.md §20; agents/runtime.md
// Part A). This node (runtime-a02) builds only the state transition
// validator — the pause/resume integrity boundary every later Part A node
// (Observe, RequestPause, the persist-phase orchestrator, resume
// validation) is built on top of. It depends on runtime-a01's migrations
// (internal/storage/sqlite/migrations/0050-0052_*.sql) for the durable
// shape of a pause record, but this node itself does no I/O — it is a pure
// function of (current state, event) -> (next state, error).
//
// # Frozen state enum vs. agents/runtime.md's "Required state path"
//
// docs/implementation/vertical-slice/CONTRACT_FREEZE.md freezes exactly twelve
// domain.PauseStatus wire strings (internal/domain/status.go):
//
//	predicted, requested, quiescing, checkpointing, interrupting, sleeping,
//	wake_pending, validating, resuming, resumed, blocked_conflict,
//	cancelled, failed
//
// and says explicitly: "Full per-role transition validation logic belongs
// to the owning role ... this file freezes only the enum values and their
// wire strings, not the transition table implementation." Constitution §6
// rule 4 is equally explicit that no role invents an ad hoc status value.
//
// agents/runtime.md's Part A "Required state path" prose
// (observing -> pause_requested -> quiescing -> safe_point_reached ->
// persisting -> interrupting -> sleeping -> wake_due -> validating ->
// resuming -> resumed) and Auspex_ADD.md §20.5's mermaid diagram
// (Active -> Predicted -> Requested -> Quiescing -> SafePointReached ->
// Checkpointing -> Interrupting -> Sleeping -> WakePending -> Validating ->
// Resuming -> Active, plus EmergencyInterrupt/MinimalCheckpoint/Failed/
// BlockedConflict/Cancelled) both use a handful of state names that are
// NOT among the twelve frozen wire strings: "observing"/"Active" (no pause
// exists yet — this package's state machine only starts once a
// PauseRecord exists, at Predicted or Requested), "safe_point_reached"/
// "SafePointReached" and "persisting" (both fold into the frozen
// `checkpointing` state — reaching a safe point and persisting the
// mandatory Phase-3 writes are sub-steps of one durable phase, not
// separately durable PauseStatus values), "wake_due" (fold into
// `wake_pending` — "the wake job's run_after has been reached" is a
// scheduler-observed condition on a wake_pending pause, not a fourth pause
// status), and "EmergencyInterrupt"/"MinimalCheckpoint" (ADD §20.14's
// emergency path reuses `checkpointing` with an internal "minimal" flag
// this package does not model as state, since a later node persists the
// distinction as an attribute of the checkpoint, not the pause's own
// status).
//
// This is a resolution, not a new decision: CONTRACT_FREEZE.md is tier-3
// (frozen contract) authority over agents/runtime.md's tier-4 operational
// prose per Constitution §2, and the Constitution itself forbids inventing
// states outside the frozen enum. Every one of agents/runtime.md's named
// path steps is reachable and tested here; several are just represented by
// one frozen status value covering more than one document's prose noun.
package pause
