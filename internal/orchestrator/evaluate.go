package orchestrator

import (
	"context"
	"strings"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/gitx"
)

// UsageObservationLoader loads the most recent usage/quota/context
// observations for a session (agents/runtime.md Part B pipeline step 3:
// "Load current Progress Tree and usage observations"). This is a narrow
// interface local to this package, not a frozen internal/app port — no
// role has shipped a queryable observation store behind a stable contract
// yet (internal/telemetry/claude.EventStore persists raw provider events,
// not decoded UsageObservation/QuotaObservation/ContextObservation rows).
// Evaluate treats a nil Loader as "no observation history available" (an
// operational gap, not an error — see fail-open/fail-closed below), so
// callers that don't have a real one yet may omit it entirely.
type UsageObservationLoader interface {
	LoadRecentObservations(ctx context.Context, sessionID domain.SessionID) ([]domain.UsageObservation, error)
}

// GitSnapshotter captures a lightweight Git snapshot for a worktree
// (agents/runtime.md Part B pipeline step 4). Satisfied by *gitx.Client
// (internal/gitx, checkpoint role's Part B Git plumbing) — declared as an
// interface here, rather than importing *gitx.Client's concrete type
// directly into EvaluateRequest, so this package's dependency on gitx is
// limited to the one method it actually calls and tests can supply a
// fake instead of shelling out to a real git binary.
type GitSnapshotter interface {
	Fingerprint(ctx context.Context, path string) (gitx.Fingerprint, error)
}

// EvaluateRequest is this pipeline's entry point request. It carries
// already-resolved identity (see doc.go's "What resolve means" note)
// rather than raw provider payloads — normalizing a raw hook payload into
// these fields is runtime-b04's job (hook handlers), upstream of this
// package.
type EvaluateRequest struct {
	SessionID    domain.SessionID
	TurnID       domain.TurnID
	Provider     string
	PromptHash   string
	RepositoryID domain.RepositoryID
	WorktreeID   domain.WorktreeID
	TaskID       *domain.TaskID
	// WorktreePath is the filesystem path Capture snapshots (pipeline step
	// 4). Optional: when empty, Capture is skipped and
	// EvaluateResult.GitFingerprint is the zero value — a CLI command
	// invoked outside a resolved worktree (or a test that doesn't care
	// about Git state) is not forced to supply one.
	WorktreePath string
}

// EvaluateResult is Evaluate's return value: the predictor pipeline's
// Evaluation, the policy's DecisionResult for it, the Progress Tree
// snapshot and usage observations that were loaded going in, and the Git
// fingerprint captured during Capture (zero value if WorktreePath was
// empty). Bundling these together (rather than returning just the
// Evaluation) is what lets runtime-b04's hook handlers and runtime-b06's
// decision commands build a provider-compatible response without
// re-deriving inputs Evaluate already computed.
type EvaluateResult struct {
	Evaluation      app.Evaluation
	Decision        app.DecisionResult
	ProgressTree    app.ProgressTreeSnapshot
	Observations    []domain.UsageObservation
	GitFingerprint  gitx.Fingerprint
	HasGitSnapshot  bool
	HasProgressTree bool
}

// Deps bundles Evaluate's collaborators. Evaluation and ProgressTree are
// the two frozen app services this pipeline calls directly (Decide is
// invoked on the same EvaluationService after EvaluateTurn — see below).
// ObservationLoader and GitSnapshot are both optional (nil-safe) per their
// own doc comments — omitting either degrades what Evaluate can populate
// in EvaluateResult without making Evaluate itself fail, matching ADD
// §17.5's fail-open default for operational-observation gaps (as opposed
// to state-integrity failures, which stay fail-closed — see below).
type Deps struct {
	Evaluation        app.EvaluationService
	ProgressTree      app.ProgressTreeService
	ObservationLoader UsageObservationLoader
	GitSnapshot       GitSnapshotter
}

// Evaluate runs agents/runtime.md Part B pipeline steps 1-6: receive
// input (the caller's EvaluateRequest), resolve repository/worktree/
// session (validated present, per doc.go), load the current Progress Tree
// and usage observations, snapshot lightweight Git state, evaluate
// through the predictor role (app.EvaluationService.EvaluateTurn), and
// apply policy (app.EvaluationService.Decide on the resulting evaluation).
//
// # Fail-open vs fail-closed (CONTRACT_FREEZE.md "Error contract")
//
// Loading usage observations and capturing the Git snapshot are
// operational observations: per ADD §17.5 ("quota unavailable ->
// continue with uncertainty", "telemetry unavailable -> fail open +
// warning"), a failure in either step does NOT abort Evaluate — it is
// recorded via EvaluateResult's Has* flags being false and the pipeline
// proceeds with whatever it has (mirroring the frozen
// domain.Confidence/ConfidenceUnavailable discipline the predictor role
// itself uses for the same reason). Loading the Progress Tree snapshot
// follows the same fail-open rule for the same reason: an evaluation must
// still be possible for a brand-new task with no tree yet.
//
// EvaluateTurn and Decide themselves are NOT operational observations —
// they are the actual prediction/policy decision this pipeline exists to
// produce, so an error from either is returned to the caller as-is
// (fail-closed: Evaluate does not fabricate a fallback Evaluation or
// silently default to an allow decision).
func Evaluate(ctx context.Context, deps Deps, req EvaluateRequest) (EvaluateResult, error) {
	if err := validate(req); err != nil {
		return EvaluateResult{}, err
	}

	var result EvaluateResult

	if deps.ProgressTree != nil && req.TaskID != nil {
		if snap, err := deps.ProgressTree.Snapshot(ctx, *req.TaskID); err == nil {
			result.ProgressTree = snap
			result.HasProgressTree = true
		}
		// A Snapshot error is an operational gap (fail-open per the
		// doc comment above): a brand-new task legitimately has no
		// tree yet, and a storage hiccup here must not block
		// evaluation — the predictor pipeline degrades to
		// ConfidenceLow/Unavailable on its own missing-history inputs,
		// it does not need Evaluate to pre-empt that judgment.
	}

	if deps.ObservationLoader != nil {
		if obs, err := deps.ObservationLoader.LoadRecentObservations(ctx, req.SessionID); err == nil {
			result.Observations = obs
		}
		// Same fail-open rule: an observation-load error degrades to
		// "no observations" rather than aborting the pipeline.
	}

	if deps.GitSnapshot != nil && req.WorktreePath != "" {
		if fp, err := deps.GitSnapshot.Fingerprint(ctx, req.WorktreePath); err == nil {
			result.GitFingerprint = fp
			result.HasGitSnapshot = true
		}
		// Same fail-open rule: Git being momentarily unavailable (e.g.
		// a lock file held by a concurrent operation) must not block
		// evaluation — the predictor's blast-radius risk component
		// degrades on a missing fingerprint the same way it degrades
		// on any other missing input.
	}

	if deps.Evaluation == nil {
		return EvaluateResult{}, &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "orchestrator: Evaluate requires a non-nil EvaluationService",
			Retryable: false,
		}
	}

	evaluation, err := deps.Evaluation.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID:  req.SessionID,
		TurnID:     req.TurnID,
		Provider:   req.Provider,
		PromptHash: req.PromptHash,
	})
	if err != nil {
		// Fail-closed: EvaluateTurn is the pipeline's actual purpose, not
		// an operational observation — its failure propagates as-is.
		return EvaluateResult{}, err
	}
	result.Evaluation = evaluation

	decision, err := deps.Evaluation.Decide(ctx, app.DecideRequest{EvaluationID: evaluation.ID})
	if err != nil {
		return EvaluateResult{}, err
	}
	result.Decision = decision

	return result, nil
}

func validate(req EvaluateRequest) error {
	var missing []string
	if req.SessionID == "" {
		missing = append(missing, "SessionID")
	}
	if req.TurnID == "" {
		missing = append(missing, "TurnID")
	}
	if req.Provider == "" {
		missing = append(missing, "Provider")
	}
	if len(missing) > 0 {
		return &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "orchestrator: Evaluate request is missing required fields",
			Retryable: false,
			Details:   map[string]string{"missing_fields": strings.Join(missing, ",")},
		}
	}
	return nil
}
