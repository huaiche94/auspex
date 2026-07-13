package pause

import (
	"context"

	"github.com/huaiche94/auspex/internal/domain"
)

// Boundary names one observation this package's caller can report while a
// pause is Quiescing (ADD §20.6 Phase 2) — the provider-/orchestrator-
// visible event stream the safe-point coordinator watches to decide
// whether "now" is a safe moment to interrupt. This is this package's own
// closed vocabulary (mirrors Event/TriggerReason: not persisted, not part
// of any frozen contract) — a later node maps real provider/orchestrator
// signals onto these values.
type Boundary string

const (
	// BoundaryItemCompleted: ADD §20.4 "after item/completed".
	BoundaryItemCompleted Boundary = "item_completed"
	// BoundaryPostToolUse: ADD §20.4 "after PostToolUse".
	BoundaryPostToolUse Boundary = "post_tool_use"
	// BoundaryFilePatchFlushed: ADD §20.4 "after file patch applied and
	// flushed".
	BoundaryFilePatchFlushed Boundary = "file_patch_flushed"
	// BoundaryTestCommandExit: ADD §20.4 "after test command exit".
	BoundaryTestCommandExit Boundary = "test_command_exit"
	// BoundaryNodeArtifactPersisted: ADD §20.4 "after progress node
	// artifact persisted".
	BoundaryNodeArtifactPersisted Boundary = "node_artifact_persisted"
	// BoundaryBeforeNextToolDispatch: ADD §20.4 "before dispatching next
	// tool" — the coordinator is asked whether it's safe to interrupt
	// BEFORE the next tool call is sent, not mid-flight.
	BoundaryBeforeNextToolDispatch Boundary = "before_next_tool_dispatch"
	// BoundaryProviderWaitingForInput: ADD §20.4 "provider turn waiting
	// for input".
	BoundaryProviderWaitingForInput Boundary = "provider_waiting_for_input"

	// BoundaryFilesystemWriteActive: ADD §20.4 unsafe — "active filesystem
	// write".
	BoundaryFilesystemWriteActive Boundary = "filesystem_write_active"
	// BoundaryGitApplyOrCheckout: ADD §20.4 unsafe — "Git apply/checkout".
	BoundaryGitApplyOrCheckout Boundary = "git_apply_or_checkout"
	// BoundaryDBMigrationTransaction: ADD §20.4 unsafe — "DB migration
	// transaction".
	BoundaryDBMigrationTransaction Boundary = "db_migration_transaction"
	// BoundaryUncommittedCheckpointTempWrite: ADD §20.4 unsafe —
	// "uncommitted checkpoint temp write".
	BoundaryUncommittedCheckpointTempWrite Boundary = "uncommitted_checkpoint_temp_write"
	// BoundaryPackageManagerLockOperation: ADD §20.4 unsafe — "package
	// manager lock operation".
	BoundaryPackageManagerLockOperation Boundary = "package_manager_lock_operation"
	// BoundaryDestructiveCommandUnknownCompletion: ADD §20.4 unsafe —
	// "destructive command with unknown completion".
	BoundaryDestructiveCommandUnknownCompletion Boundary = "destructive_command_unknown_completion"
)

// safeBoundaries is ADD §20.4's exact "Safe point MAY be" list. Anything
// not in this set (including every explicitly-named unsafe boundary
// above, and any Boundary value this package doesn't recognize at all) is
// NOT a safe point — SafePointCoordinator fails closed on the unsafe side
// per this package's general discipline (Constitution §6: ambiguous state
// is never treated as success).
var safeBoundaries = map[Boundary]bool{
	BoundaryItemCompleted:           true,
	BoundaryPostToolUse:             true,
	BoundaryFilePatchFlushed:        true,
	BoundaryTestCommandExit:         true,
	BoundaryNodeArtifactPersisted:   true,
	BoundaryBeforeNextToolDispatch:  true,
	BoundaryProviderWaitingForInput: true,
}

// IsSafeBoundary reports whether b is one of ADD §20.4's named safe
// boundaries. Exported so callers building the Boundary stream (e.g. an
// orchestrator translating real provider events) can self-check without
// duplicating this package's list.
func IsSafeBoundary(b Boundary) bool {
	return safeBoundaries[b]
}

// SafePointObservation is one reported boundary, from a specific pause's
// quiescing wait.
type SafePointObservation struct {
	PauseID  domain.PauseID
	Boundary Boundary
}

// SafePointCoordinator decides WHEN it is actually safe to interrupt a
// turn (agents/runtime.md Part A deliverable 4) — built as an interface so
// different providers/turn shapes can implement their own notion of "safe
// point" (e.g. a future provider with a different natural boundary set)
// without this package's RequestPause/state-machine logic depending on
// any one provider's concrete event shape.
type SafePointCoordinator interface {
	// IsSafe reports whether obs represents a safe point to interrupt at.
	// It is a pure predicate — no side effect, no persistence — so
	// callers can probe candidate boundaries without committing to
	// anything.
	IsSafe(ctx context.Context, obs SafePointObservation) bool
}

// TurnBoundaryCoordinator is the concrete SafePointCoordinator
// implementation for the turn/section-boundary case ADD §20.4 describes:
// a safe point is any of the named safe boundaries, full stop — this
// implementation has no additional provider-specific state (e.g. no
// "only after N tool calls" heuristic); it exists as the direct, ADD-
// literal mapping from Boundary to "safe or not," and is what
// runtime-a04's required test exercises.
type TurnBoundaryCoordinator struct{}

// NewTurnBoundaryCoordinator constructs a TurnBoundaryCoordinator. It is a
// stateless value type; the constructor exists for consistency with this
// package's other New* constructors and so future fields (if ever needed)
// don't force every call site to change from a bare struct literal.
func NewTurnBoundaryCoordinator() *TurnBoundaryCoordinator {
	return &TurnBoundaryCoordinator{}
}

var _ SafePointCoordinator = (*TurnBoundaryCoordinator)(nil)

func (c *TurnBoundaryCoordinator) IsSafe(_ context.Context, obs SafePointObservation) bool {
	return IsSafeBoundary(obs.Boundary)
}

// --- Safe-point-triggered persist-then-interrupt sequencing ----------------

// CheckpointPersister is the narrow seam PersistThenInterrupt uses for
// "persist state at the safe point" (ADD §20.6 Phase 3). It is
// intentionally as narrow as this node needs — just "persist something
// and report whether it succeeded" — because the DAG explicitly scopes
// runtime-a04 to "no concrete store required to begin" and directs using
// fakes for checkpoint's services (mirroring runtime-b05's
// CheckpointCreateDeps pattern in internal/orchestrator/checkpoint.go,
// which fakes app.StateCheckpointService/app.RepositoryCheckpointService
// the same way). The full multi-step Phase-3 persist orchestration
// (Progress Tree snapshot, State Checkpoint, Repository Checkpoint, Pause
// Record, Wake Job — CONTRACT_FREEZE.md's "GracefulPauseService's persist
// phase") is runtime-a05's node, not this one; this seam is deliberately
// just enough to prove the ordering guarantee this node's required test
// asks for.
type CheckpointPersister interface {
	Persist(ctx context.Context, pauseID domain.PauseID) error
}

// Interrupter is the narrow seam PersistThenInterrupt uses for "signal the
// provider to stop" (ADD §20.6 Phase 4) — deliberately narrower than the
// frozen app.TurnInterrupter (which needs a full RunLocator); this node
// only needs to prove ordering, and a later node wires the real
// app.TurnInterrupter behind an adapter satisfying this interface.
type Interrupter interface {
	Interrupt(ctx context.Context, pauseID domain.PauseID) error
}

// PersistThenInterrupt implements the ordering half of "safe point
// persists checkpoints before interrupt" (agents/runtime.md required
// test): given a reported safe-point observation, checkpoint persistence
// via persister.Persist MUST be called and MUST succeed before
// interrupter.Interrupt is ever invoked — never the reverse, and never
// both unconditionally. This mirrors runtime-b05's
// internal/orchestrator.CheckpointCreate ordering pattern exactly (state
// before repository, early-return on the first error), applied one layer
// up at the safe-point boundary instead of the checkpoint-role boundary.
//
// If obs is not a safe boundary at all, PersistThenInterrupt returns a
// validation error and calls neither collaborator — this function is not
// itself responsible for WAITING for a safe point (that is the quiescing
// phase's timeout/poll loop, a later concern), only for enforcing the
// ordering once a boundary is reported.
func PersistThenInterrupt(ctx context.Context, coordinator SafePointCoordinator, persister CheckpointPersister, interrupter Interrupter, obs SafePointObservation) error {
	if obs.PauseID == "" {
		return &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: PersistThenInterrupt requires a PauseID", Retryable: false,
		}
	}
	if coordinator == nil || persister == nil || interrupter == nil {
		return &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: PersistThenInterrupt requires a non-nil coordinator, persister, and interrupter", Retryable: false,
		}
	}
	if !coordinator.IsSafe(ctx, obs) {
		return &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   "pause: PersistThenInterrupt called at a non-safe boundary",
			Retryable: false,
			Details:   map[string]string{"boundary": string(obs.Boundary)},
		}
	}

	// Persist FIRST. Per ADD §20.15 ("state checkpoint fails -> do not
	// interrupt unless emergency; alert"), a persist failure returns
	// immediately and Interrupt is never called — the exact same
	// fail-closed shape runtime-b05's CheckpointCreate uses for its own
	// two-step ordering.
	if err := persister.Persist(ctx, obs.PauseID); err != nil {
		return err
	}

	// Interrupt SECOND, only reached because Persist already succeeded.
	return interrupter.Interrupt(ctx, obs.PauseID)
}
