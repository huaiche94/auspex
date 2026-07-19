# ADR-0054 — Act on CHECKPOINT_AND_RUN: automatic pre-turn checkpoint, config-gated, fail-open (issue #116)

> 🌐 English | [繁體中文](0054-auto-checkpoint-and-run.zh-TW.md)

Status: Accepted
Date: 2026-07-19
Owner: owner-decided (auto vs. advisory was the open `adr-needed` question on issue #116); lead-executed
Tracking: issue #116 (M4 Progress Tree / State Checkpointing); README accuracy audit follow-up

## Context

`CHECKPOINT_AND_RUN` has been a real, persisted policy **decision** since
ADR-0043: the decider emits it for the critical risk band, high blast
radius, the context ceiling, and the cost-budget ceiling
(`internal/policy/decide.go`, `context.go`, `costbudget.go`), it is
persisted to `policy_decisions` (migration 0043) and rendered on the
forecast card. But nothing **acted** on it: the pre-turn hook branched
only on `BLOCK`, the managed runner switched only on `BLOCK`, and
`CheckpointCreate` was reachable solely through the operator-driven
`auspex checkpoint create` CLI. ADR-0043 and DECISION_LOG D-08 framed the
action as a *recommendation* ("CHECKPOINT_AND_RUN 建議"), and
`internal/orchestrator/decision.go`'s doc comment codified the advisory
stance: the operator "is expected to have already run `checkpoint
create` upstream."

That left a real gap between the product claim ("solidifies state before
the turn") and the wired behavior — the exact README-vs-shipped gap the
audit that filed #116 documented. The design question underneath:
should the decision **auto-invoke** the checkpoint, or stay advisory
with the automatic checkpoint living only in the PreCompact path (#114)?

## Decision

**Auto-checkpoint, gated by config, fail-open.** (Owner decision on
issue #116; this ADR supersedes the recommendation-only framing in
ADR-0043 §2 and DECISION_LOG D-08 for this action — the other seven
policy actions' semantics are untouched.)

1. **Automatic pre-turn checkpoint at both decision surfaces.** When the
   policy decision is `CHECKPOINT_AND_RUN` and the config gate is
   enabled, Auspex creates the checkpoint pair — state first, then
   repository, reusing `CheckpointCreate`'s frozen ordering — BEFORE the
   turn proceeds:
   - `HandleUserPromptSubmit` (native hook): checkpoint before the
     `allow` response is written; the outcome is surfaced as one
     `additionalContext` line so the coding agent sees it.
   - `internal/managed.Runner.Run` (managed one-shot): checkpoint before
     the provider process is spawned; the outcome is a HumanLog line.
   The orchestration lives in one new component
   (`internal/orchestrator/autocheckpoint.go`, `AutoCheckpointer`),
   shared by both call sites through the existing `HookDeps` bundle.

2. **The checkpoint ID is threaded through the existing decision-allow
   machinery, not a new channel.** After a successful create, the
   auto-checkpointer drives `DecisionAllowCmd`'s two documented flows —
   issue (binding `DecisionAllowRequest.RepositoryCheckpointID`) then
   immediate consume — i.e. exactly the calls an operator would have
   made by hand. The consumed-at-issuance authorization row (migration
   0044) is the persisted audit record that this turn proceeded exactly
   once under this decision with this repository checkpoint bound. It is
   consumed in the same invocation because the turn proceeds in the same
   invocation — leaving it live would fabricate a pending resubmission
   that will never come.

3. **Fail-open, loudly.** Any failure — unresolvable task/worktree
   target, state- or repository-checkpoint error, authorization
   recording error — allows the turn and records a warning (result
   field, additionalContext/HumanLog line). The safety net failing is
   never a reason to block the session it was meant to protect. This is
   the deliberate inverse of ADD §17.5/§20.15's fail-closed posture for
   **operator-requested** checkpoints (`auspex checkpoint create`,
   `CompleteNode`'s mandatory checkpoint), where the user explicitly
   asked and silently skipping would lie. Here nobody asked; Auspex adds
   protection opportunistically, so the honest degrade is a warning.
   Cold start is the common skip: a session with no task yet has no
   state checkpoint to create, and fabricating one would violate
   "unknown is not zero".

4. **Config gate: `state_checkpointing.on_checkpoint_and_run`, default
   `true`.** Named alongside the section's existing `on_<trigger>` keys
   (`on_node_completion`, `on_architecture_decision`, `on_pre_compact`,
   ADD §26.4). `false` restores the explicit advisory behavior: the
   decision renders on the card/statusline and the operator checkpoints
   by hand. This is the FIRST production consumer of the M1 layered
   config chain (`internal/config`): defaults < global user
   `config.yaml` < `.auspex/config.yaml` < `.auspex/local.yaml`, loaded
   fail-open at composition time (a malformed file degrades to the
   defaults; `doctor`, not every hook, is where config problems get
   reported). The §26.1 environment/CLI layers have no field mapping for
   this section yet, matching the config package's recorded status.

5. **Latency budget: the hot path never pays.** The pre-turn hook runs
   synchronously before every prompt, so the auto-checkpoint is invoked
   ONLY inside the `CHECKPOINT_AND_RUN` branch — ordinary `RUN`/`WARN`
   prompts execute zero additional statements. On the rare risky prompt,
   the added latency is the checkpoint pair itself (a git snapshot plus
   two SQLite writes) — accepted deliberately: `CHECKPOINT_AND_RUN`
   fires precisely when the projected blast radius or resource ceiling
   makes pre-turn solidification worth seconds, and the alternative
   (advisory-only) was an unwired product claim.

6. **Complementary to PreCompact (#114), not a substitute.** Two
   different triggers with two different semantics: pre-compact
   checkpoints **unconditionally at a context boundary** (about to lose
   conversational state); this path checkpoints **on a per-turn risk
   decision** (about to run something risky). Neither replaces the
   other; #114's scope and files are untouched by this change.

## Consequences

- The README's "solidifies state before the turn (`CHECKPOINT_AND_RUN`)"
  claim becomes true as shipped-and-wired (the final README reconcile is
  a separate docs PR, per the repo's reconcile discipline).
- `internal/orchestrator/decision.go`'s advisory doc comment is updated:
  the upstream checkpoint step is normally automatic now, manual only
  when the gate is off.
- The authorizations table gains consumed-at-issuance rows for
  auto-checkpointed turns — the intended audit trail. Their TTL/replay
  semantics are unchanged (consumed immediately, so nothing is left to
  replay).
- A disabled gate (`on_checkpoint_and_run: false`) or a minimal
  composition (nil `AutoCheckpointer`) is byte-identical to the
  pre-#116 surfaces — proven by tests.
- The managed runner passes its own explicit `WorktreeID`/`TaskID`
  target; the hook path resolves task (same heuristic as the evaluation
  pipeline, via the frozen Resolve port's narrow view) and worktree
  (`provider_sessions.worktree_id`) from the session.

## Alternatives considered

- **Stay advisory; automatic checkpoint only at PreCompact (#114).**
  Rejected by the owner: the decision's name promises an action, the
  README promises pre-turn solidification, and the two triggers protect
  against different losses (risky turn vs. context loss) — PreCompact
  alone leaves the risky-turn window open.
- **Default off (opt-in).** Rejected: the decision already fires only on
  the rare high-risk band, the cost is seconds on precisely those turns,
  and fail-open removes the availability risk; default-off would keep
  the product claim unwired for every user who never edits config.
- **Fail closed on checkpoint failure.** Rejected for this trigger: it
  would let an Auspex-side storage/git fault block every risky-band
  prompt — the exact "safety net takes down the session" failure mode
  ADD §17.5 exists to prevent. Operator-requested checkpoints keep
  their fail-closed contract.
- **A new persistence channel for the checkpoint binding.** Rejected:
  `DecisionAllowRequest.RepositoryCheckpointID` → `Authorization` was
  built for exactly this binding; adding a parallel column/table would
  duplicate a frozen contract's job.
