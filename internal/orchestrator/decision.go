// decision.go implements `auspex decision allow` / `auspex decision
// deny` (agents/runtime.md Part B P0 command list; EXECUTION_DAG.md
// runtime-b06) — the two remaining decision-flow commands after
// runtime-b03's Evaluate pipeline. Per the task brief, this node's explicit
// job is to wire the REAL internal/evaluation.Service (predictor-09/10,
// both integrated on main as of this wave) into the decision-allow path,
// replacing runtime-b03's FAKE app.EvaluationService — this is now a HARD
// dependency, not fake-able, per the DAG's own note: "second-authorization-
// replay-rejected is a required test," and a fake can only ever simulate
// that guarantee, never actually prove it holds against real, storage-
// backed exactly-once consumption.
//
// # The two-call flow this file implements
//
// agents/runtime.md Part B pipeline step 10 ("`decision allow` issues
// one-time authorization") and step 11 ("Resubmitted prompt consumes
// authorization exactly once before allowing") describe two DIFFERENT
// moments of the same flow, not one call:
//
//  1. First call (no AuthorizationID supplied): the evaluation already
//     computed a decision (via runtime-b03's Evaluate, upstream of this
//     command) that required confirmation/checkpoint before proceeding.
//     DecisionAllowCmd issues a fresh one-time app.Authorization bound to
//     this turn/prompt, and returns it WITHOUT yet allowing anything to
//     proceed — the caller must resubmit.
//  2. Second call (AuthorizationID supplied — the resubmission):
//     DecisionAllowCmd instead calls the real
//     app.EvaluationService.ConsumeAuthorization with that ID. Success
//     means this resubmission is allowed to proceed exactly once; a THIRD
//     call reusing the same AuthorizationID (the replay case) is rejected
//     by the same real, storage-backed exactly-once check predictor-10
//     hardened — proven end-to-end through this orchestrator layer, not
//     merely simulated by a fake.
//
// This mirrors pauselifecycle.go's Resume/Cancel split (two related but
// distinct actions behind one small file) and evaluate.go's own
// fail-open/fail-closed discipline: EvaluateTurn/Decide/ConsumeAuthorization/
// IssueAuthorization failures are this command's actual purpose, so they
// propagate as-is (fail-closed) — there is no operational-observation gap
// to fail open over here, unlike Evaluate's optional Progress
// Tree/observation/Git-snapshot steps.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// AuthorizationIssuer is the narrow local seam for
// *internal/evaluation.Service.IssueAuthorization — NOT part of the frozen
// app.EvaluationService interface (internal/app/ports.go only names
// EvaluateTurn/GetEvaluation/Decide/ConsumeAuthorization), so this package
// declares its own minimal seam for the one extra method it needs, exactly
// as internal/evaluation/service.go's own doc comment anticipates ("A
// future EvaluateTurn/Decide caller (e.g. internal/orchestrator, once it
// wires the real Service) is expected to call this"). Declared as an
// interface (not a direct *evaluation.Service field) so this package still
// depends only on a narrow, test-fakeable shape — mirrors
// UsageObservationLoader/GitSnapshotter's own precedent in evaluate.go.
type AuthorizationIssuer interface {
	IssueAuthorization(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error)
}

// DecisionDeps bundles DecisionAllowCmd/DecisionDenyCmd's collaborators.
// Evaluation is the frozen app.EvaluationService port (Decide/
// ConsumeAuthorization) — REAL this wave (internal/evaluation.Service),
// per the DAG's hard-dependency note. Issuer is the same concrete Service
// value, satisfying AuthorizationIssuer — a caller wires both fields from
// the one real *evaluation.Service instance (it satisfies both
// interfaces), but this package accepts them as two separate narrow seams
// rather than one wider one, so a test can still supply distinct fakes for
// each concern independently.
type DecisionDeps struct {
	Evaluation app.EvaluationService
	Issuer     AuthorizationIssuer
}

// DecisionAllowRequest is `auspex decision allow`'s input. Exactly one
// of the two flows this file's package comment describes applies,
// selected by whether AuthorizationID is empty:
//
//   - Issue flow (AuthorizationID == ""): EvaluationID/TurnID/PromptHash
//     identify which evaluation's decision is being allowed;
//     SnapshotFingerprint/RepositoryCheckpointID are threaded straight into
//     the new Authorization exactly as app.Authorization's own fields
//     expect (checkpoint's own frozen shape, not reinvented here).
//   - Consume flow (AuthorizationID != ""): AuthorizationID/TurnID/
//     PromptHash are passed straight into ConsumeAuthorization's frozen
//     request shape (app.ConsumeAuthorizationRequest) — the other three
//     issue-only fields are ignored (a resubmission does not re-derive a
//     new snapshot/checkpoint binding, the ALREADY-ISSUED authorization's
//     own bound values are what matters, and ConsumeAuthorization itself
//     checks them).
type DecisionAllowRequest struct {
	EvaluationID domain.EvaluationID
	TurnID       domain.TurnID
	PromptHash   string

	// AuthorizationID selects the consume flow when non-empty (a
	// resubmitted prompt presenting the authorization issued by a prior
	// allow call).
	AuthorizationID string

	// SnapshotFingerprint/RepositoryCheckpointID are issue-flow-only:
	// threaded verbatim into app.Authorization on issuance. Per
	// CONTRACT_FREEZE.md's transaction-boundary note on
	// GracefulPauseService's persist phase (an analogous "sequence, not
	// one flat transaction" discipline) — a decision that resulted in
	// PolicyCheckpointAndRun is expected to have already run `checkpoint
	// create` (this package's own CheckpointCreate) upstream of this call,
	// and the caller threads that checkpoint's own ID through here; this
	// command does not itself create a checkpoint (that would blur two
	// separately-owned steps into one, the same anti-pattern
	// CheckpointCreate's own doc comment warns against for its two
	// sub-steps).
	SnapshotFingerprint    string
	RepositoryCheckpointID *domain.RepositoryCheckpointID
}

// DecisionAllowResult reports the outcome of either flow. Exactly one of
// Issued/Consumed is true (never both, never neither) — Authorization is
// populated in both cases (the newly issued one, or the one that was just
// consumed), so a caller can render either outcome without a type switch.
type DecisionAllowResult struct {
	Decision      app.DecisionResult
	Authorization app.Authorization
	// Issued is true for the first-call (issue) flow.
	Issued bool
	// Consumed is true for the second-call (resubmission/consume) flow.
	Consumed bool
}

// DecisionAllowCmd implements `auspex decision allow`. See this file's
// package comment for the full two-flow rationale.
func DecisionAllowCmd(ctx context.Context, deps DecisionDeps, req DecisionAllowRequest) (DecisionAllowResult, error) {
	if deps.Evaluation == nil {
		return DecisionAllowResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DecisionAllowCmd requires a non-nil EvaluationService", Retryable: false,
		}
	}
	if req.TurnID == "" {
		return DecisionAllowResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: DecisionAllowCmd requires a TurnID", Retryable: false,
		}
	}

	// --- Consume flow: a resubmitted prompt presenting an already-issued
	// authorization. Checked first (before requiring EvaluationID) since
	// this flow does not need one — see DecisionAllowRequest's doc
	// comment on why the issue-only fields are ignored here.
	if req.AuthorizationID != "" {
		auth, err := deps.Evaluation.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{
			AuthorizationID: req.AuthorizationID,
			TurnID:          req.TurnID,
			PromptHash:      req.PromptHash,
		})
		if err != nil {
			// Fail-closed, as-is: a replay, expiry, or binding mismatch is
			// this command's actual purpose to detect, not an operational
			// gap to paper over (mirrors Evaluate's own EvaluateTurn/Decide
			// fail-closed steps, not its optional-dependency fail-open
			// ones).
			return DecisionAllowResult{}, err
		}
		return DecisionAllowResult{Authorization: auth, Consumed: true}, nil
	}

	// --- Issue flow: first call, no AuthorizationID yet.
	if deps.Issuer == nil {
		return DecisionAllowResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DecisionAllowCmd requires a non-nil AuthorizationIssuer for the issue flow", Retryable: false,
		}
	}
	if req.EvaluationID == "" {
		return DecisionAllowResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: DecisionAllowCmd requires an EvaluationID for the issue flow", Retryable: false,
		}
	}

	decision, err := deps.Evaluation.Decide(ctx, app.DecideRequest{EvaluationID: req.EvaluationID})
	if err != nil {
		return DecisionAllowResult{}, err
	}

	auth, err := deps.Issuer.IssueAuthorization(ctx, req.TurnID, req.PromptHash, req.SnapshotFingerprint, string(decision.Action), req.RepositoryCheckpointID)
	if err != nil {
		return DecisionAllowResult{}, err
	}

	return DecisionAllowResult{Decision: decision, Authorization: auth, Issued: true}, nil
}

// DecisionDenyRequest is `auspex decision deny`'s input.
type DecisionDenyRequest struct {
	EvaluationID domain.EvaluationID
}

// DecisionDenyResult reports the evaluation's decision that was denied —
// deny does not itself mutate any state (there is no "un-authorization" to
// revoke; simply never issuing/consuming one already achieves "denied"),
// it only confirms and reports which decision the caller is choosing not
// to proceed with, for the CLI layer to render.
type DecisionDenyResult struct {
	Decision app.DecisionResult
}

// DecisionDenyCmd implements `auspex decision deny`: reads back the
// evaluation's already-computed decision via the real
// app.EvaluationService.Decide (read-back, not recompute — see
// internal/evaluation/doc.go's "Decide: read-back, not recompute" section)
// and reports it. No Authorization is issued or consumed — a denied turn
// has nothing further to allow.
func DecisionDenyCmd(ctx context.Context, deps DecisionDeps, req DecisionDenyRequest) (DecisionDenyResult, error) {
	if deps.Evaluation == nil {
		return DecisionDenyResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DecisionDenyCmd requires a non-nil EvaluationService", Retryable: false,
		}
	}
	if req.EvaluationID == "" {
		return DecisionDenyResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: DecisionDenyCmd requires an EvaluationID", Retryable: false,
		}
	}
	decision, err := deps.Evaluation.Decide(ctx, app.DecideRequest{EvaluationID: req.EvaluationID})
	if err != nil {
		return DecisionDenyResult{}, err
	}
	return DecisionDenyResult{Decision: decision}, nil
}
