// autocheckpoint.go implements the ADR-0054 automatic pre-turn checkpoint
// for CHECKPOINT_AND_RUN policy decisions (issue #116, M4): when the
// decider renders app.PolicyCheckpointAndRun and the config gate
// (`state_checkpointing.on_checkpoint_and_run`, default enabled) is on,
// Auspex creates the checkpoint pair (state, then repository — the exact
// CheckpointCreate ordering) BEFORE the turn proceeds, instead of merely
// recommending that the operator run `auspex checkpoint create` by hand.
//
// # Where this runs (and where it deliberately does not)
//
// Exactly two call sites, both on the rare CHECKPOINT_AND_RUN branch and
// never on the ordinary-prompt hot path (hook latency budget, ADR-0054):
//
//   - HandleUserPromptSubmit (hooks.go): the native pre-turn hook, after
//     the decision is known and before the allow response is written.
//   - internal/managed.Runner.Run's policy-decision switch: the managed
//     one-shot gate, before the provider process is spawned.
//
// `auspex evaluate` (evaluateprompt.go) deliberately does NOT auto-
// checkpoint: an offline evaluation observes, it does not precede a real
// turn. The PreCompact auto-checkpoint (issue #114) is the complementary
// path — pre-compact solidifies unconditionally at a context boundary,
// this file solidifies on a per-turn risk decision; the two never
// substitute for each other (ADR-0054).
//
// # Fail-open contract (ADD §17.5, owner decision on #116)
//
// Run never returns an error and never blocks the turn: a target that
// cannot be resolved, a checkpoint-service failure, or an authorization-
// recording failure all degrade into AutoCheckpointOutcome.Warning while
// the turn proceeds. The safety net failing is never a reason to stop the
// work it was meant to protect — the deliberate inverse of ADD §20.15's
// fail-closed posture for OPERATOR-REQUESTED checkpoints, where the user
// explicitly asked for the checkpoint and silently skipping it would lie.
// Here nobody asked; Auspex is adding protection opportunistically, so
// the honest degrade is a loud warning, not a blocked prompt.
//
// # How the checkpoint ID is threaded (existing machinery, not new)
//
// After a successful create, Run records the binding through
// DecisionAllowCmd's two documented flows — issue (with
// DecisionAllowRequest.RepositoryCheckpointID) then consume — i.e. the
// SAME two calls an operator would have made as `auspex decision allow
// ... --repository-checkpoint-id` followed by the resubmission's consume.
// The resulting consumed-at-issuance authorization row (migration 0044)
// is the persisted audit record that this turn proceeded exactly once
// under this decision with this repository checkpoint bound; it is
// consumed immediately because the turn proceeds in the same invocation —
// leaving it live would fabricate a pending resubmission that will never
// come.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/domain"
)

// SessionWorktreeResolver and its SQL-backed SessionWorktreeStore live in
// hooksprecompact.go (issue #114 landed first; #116's identical copy was
// deduplicated at merge time — one declaration per package). This file's
// AutoCheckpointer consumes them unchanged.

// AutoCheckpointer executes the ADR-0054 automatic pre-turn checkpoint.
// A nil *AutoCheckpointer is the documented "gate off" state (config
// `state_checkpointing.on_checkpoint_and_run: false`, or a composition
// that never wired one): Run becomes a no-op and CHECKPOINT_AND_RUN stays
// exactly the advisory decision it was before issue #116 — the same
// nil-is-a-documented-degrade convention HookDeps' optional fields use.
type AutoCheckpointer struct {
	// Checkpoints are the two frozen checkpoint services CheckpointCreate
	// sequences (state first, repository second — that function's whole
	// documented point; this file reuses it rather than re-ordering).
	Checkpoints CheckpointCreateDeps
	// Decision is the DecisionAllowCmd dependency pair used to record the
	// checkpoint binding through the existing issue/consume machinery (see
	// the file doc comment). Optional: a composition without the real
	// AuthorizationIssuer still checkpoints, and Run reports the skipped
	// recording as a warning rather than failing.
	Decision DecisionDeps
	// Sessions resolves the session's task (the same narrow view of the
	// frozen app.FeatureDataSource port the event correlator uses,
	// correlate.go). Consulted only when the caller did not already know
	// its TaskID (the hook path; the managed runner passes its own).
	Sessions SessionResolver
	// Worktrees resolves the session's worktree binding. Consulted only
	// when the caller did not already know its WorktreeID.
	Worktrees SessionWorktreeResolver
}

// AutoCheckpointRequest identifies the turn being checkpointed. TaskID/
// WorktreeID are optional explicit targets for callers that already know
// them (the managed runner's RunRequest carries both); empty values are
// resolved from the session via the Sessions/Worktrees seams. PromptHash
// is the already-derived SHA-256 (never raw text — Constitution §7 rule
// 2), threaded into the authorization binding exactly as `decision allow`
// would.
type AutoCheckpointRequest struct {
	SessionID    domain.SessionID
	TurnID       domain.TurnID
	EvaluationID domain.EvaluationID
	PromptHash   string

	TaskID     domain.TaskID
	WorktreeID domain.WorktreeID
}

// AutoCheckpointOutcome reports what the auto-checkpoint attempt did.
// There is deliberately no error: every failure mode is a Warning on a
// still-proceeding turn (the file doc comment's fail-open contract).
type AutoCheckpointOutcome struct {
	// Attempted is false only for the nil-AutoCheckpointer no-op (gate
	// off / not composed) — callers use it to keep the pre-#116 surfaces
	// byte-identical when the feature is disabled.
	Attempted bool
	// Created is true when BOTH checkpoints (state + repository) were
	// durably created. False with a non-empty Warning is the fail-open
	// degrade: the turn proceeded without a fresh checkpoint.
	Created                bool
	StateCheckpointID      domain.StateCheckpointID
	RepositoryCheckpointID domain.RepositoryCheckpointID
	// AuthorizationID is the consumed-at-issuance audit record binding
	// this turn to the repository checkpoint (empty when recording was
	// skipped or failed — see Warning).
	AuthorizationID string
	// Warning is non-empty whenever any step degraded. It may coexist
	// with Created=true (checkpoint succeeded, recording degraded).
	Warning string
}

// ContextLine renders the outcome as the single additionalContext line
// the UserPromptSubmit hook appends (and the managed runner logs), so the
// coding agent and the operator see the same sentence. Empty when nothing
// was attempted — a disabled gate must not add a line.
func (o AutoCheckpointOutcome) ContextLine() string {
	if !o.Attempted {
		return ""
	}
	if !o.Created {
		return "Auspex auto-checkpoint (CHECKPOINT_AND_RUN) skipped — turn proceeds without a fresh checkpoint (fail-open): " + o.Warning
	}
	line := "Auspex auto-checkpoint (CHECKPOINT_AND_RUN): created state checkpoint " +
		string(o.StateCheckpointID) + " and repository checkpoint " + string(o.RepositoryCheckpointID) + " before this turn."
	if o.Warning != "" {
		line += " Warning: " + o.Warning
	}
	return line
}

// Run executes the automatic pre-turn checkpoint for one
// CHECKPOINT_AND_RUN decision: resolve the (task, worktree) target,
// create the checkpoint pair via CheckpointCreate, and record the binding
// through DecisionAllowCmd's issue+consume flows. Nil-receiver-safe and
// fail-open on every path per the file doc comment — Run never returns an
// error and callers never branch on failure, they only surface Warning.
func (a *AutoCheckpointer) Run(ctx context.Context, req AutoCheckpointRequest) AutoCheckpointOutcome {
	if a == nil {
		return AutoCheckpointOutcome{}
	}
	out := AutoCheckpointOutcome{Attempted: true}

	taskID := req.TaskID
	if taskID == "" {
		if a.Sessions == nil {
			out.Warning = "no task resolver wired for session " + string(req.SessionID)
			return out
		}
		resolved, err := a.Sessions.Resolve(ctx, req.SessionID)
		if err != nil || resolved.TaskID == nil || *resolved.TaskID == "" {
			// Cold start (no task yet) and a resolver failure degrade the
			// same way: with no task there is no state checkpoint to
			// create, and fabricating one would violate "unknown is not
			// zero" (ADD principle 1).
			out.Warning = "no task resolved for session " + string(req.SessionID)
			return out
		}
		taskID = *resolved.TaskID
	}

	worktreeID := req.WorktreeID
	if worktreeID == "" {
		// A nil resolver is the same ok=false degrade as a failed lookup —
		// fail-open either way.
		wt, ok := domain.WorktreeID(""), false
		if a.Worktrees != nil {
			wt, ok = a.Worktrees.WorktreeForSession(ctx, req.SessionID)
		}
		if !ok || wt == "" {
			out.Warning = "no worktree resolved for session " + string(req.SessionID)
			return out
		}
		worktreeID = wt
	}

	created, err := CheckpointCreate(ctx, a.Checkpoints, CheckpointCreateRequest{
		TaskID:     taskID,
		WorktreeID: worktreeID,
	})
	if err != nil {
		out.Warning = "checkpoint create failed: " + err.Error()
		return out
	}
	out.Created = true
	out.StateCheckpointID = created.State.ID
	out.RepositoryCheckpointID = created.Repository.ID

	// Record the binding through the existing decision-allow machinery
	// (file doc comment): issue an authorization carrying the repository
	// checkpoint ID, then consume it immediately — the turn proceeds NOW,
	// exactly once, in this same invocation.
	if a.Decision.Evaluation == nil || a.Decision.Issuer == nil {
		out.Warning = "checkpoint created but authorization recording skipped: decision deps not wired"
		return out
	}
	repoCkptID := created.Repository.ID
	issued, err := DecisionAllowCmd(ctx, a.Decision, DecisionAllowRequest{
		EvaluationID:           req.EvaluationID,
		TurnID:                 req.TurnID,
		PromptHash:             req.PromptHash,
		RepositoryCheckpointID: &repoCkptID,
	})
	if err != nil {
		out.Warning = "checkpoint created but authorization issuance failed: " + err.Error()
		return out
	}
	out.AuthorizationID = issued.Authorization.ID
	if _, err := DecisionAllowCmd(ctx, a.Decision, DecisionAllowRequest{
		TurnID:          req.TurnID,
		PromptHash:      req.PromptHash,
		AuthorizationID: issued.Authorization.ID,
	}); err != nil {
		out.Warning = "checkpoint created and authorization issued but immediate consumption failed: " + err.Error()
	}
	return out
}
