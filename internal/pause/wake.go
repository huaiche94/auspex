// wake.go: Wake — the scheduler-driven counterpart to lifecycle.go's
// manual Resume/Cancel, implementing agents/runtime.md Part A deliverable 9
// ("Duplicate wake exactly-once behavior") end to end, at the PAUSE level,
// not just the lease level.
//
// # Why a dedicated Wake function, not just "call Resume after Claim"
//
// internal/scheduler.Store.Claim (runtime-a06/a07) already proves LEASE
// exclusivity: two workers racing Claim on the same wake_jobs row can never
// both win the claim, because the claim itself is a single BEGIN IMMEDIATE
// transaction. That is necessary but not sufficient for this node's
// required test. The DAG brief is explicit about the gap this file closes:
// "a lease was reclaimed after appearing expired, but the original holder
// wasn't actually dead — a 'split brain' scenario" — in that scenario TWO
// workers each hold what they believe is a valid, currently-leased claim on
// the SAME wake job (the original holder never learned its lease was
// reclaimed; it is still running), and each is about to drive the SAME
// PauseID's state machine forward via EventWakeDue and onward. Lease
// exclusivity alone does not prevent this, because by construction it is
// the lease layer itself that (wrongly, from the original holder's point of
// view) decided a second claim was legitimate. The only remaining backstop
// is the PAUSE record's own state machine: Wake must ensure that even if
// two callers both believe they hold a valid claim on the same PauseID,
// only ONE of them actually succeeds in transitioning that pause record,
// and the other observes a definitive, non-silent rejection rather than
// also "succeeding" — i.e., never two concurrent resume attempts on the
// same pause. That guarantee lives here, at the PauseStore.
// CompareAndSwapStatus layer (requestpause.go, lifecycle.go), independent
// of whatever the lease layer decided.
//
// # Why cancel wins even mid-wake
//
// Wake's every step re-reads the record's actual current status and swaps
// conditionally (via applyCASVerb/applyCASFrom, lifecycle.go) exactly like
// Resume does — so a Cancel landing between any two of Wake's steps is
// picked up immediately: Wake's next step reads the now-Cancelled status,
// Apply(Cancelled, EventWakeDue-or-whatever-comes-next) rejects it (Cancelled
// is terminal, statemachine.go), and Wake reports that TransitionError
// as-is rather than clobbering the cancellation. This is the literal
// "cancel wins race with wake" required test, proven here at the
// integration level (wake_test.go) on top of the state-machine-level proof
// statemachine_test.go already established for the bare Apply/terminal-state
// semantics.
package pause

import (
	"context"

	"github.com/huaiche94/preflight/internal/domain"
)

// WakeRequest is Wake's input: the PauseID a claimed wake job names.
type WakeRequest struct {
	PauseID domain.PauseID
}

// WakeResult reports the record after Wake's EventWakeDue transition.
type WakeResult struct {
	Record PauseRecord
}

// Wake implements the PauseID-level half of "duplicate wake exactly-once
// behavior": it applies EventWakeDue (Sleeping -> WakePending) via the same
// compare-and-swap discipline Cancel/Resume use, so that if two callers
// both attempt Wake for the same PauseID — the split-brain scenario this
// node's doc comment describes — at most one of them observes ok=true and
// the other observes either a TransitionError (if it reads AFTER the
// winner's swap already landed and moved status off Sleeping) or, in the
// narrowest possible interleaving, retries against the now-changed status
// and then correctly fails the same way. Either way, EventWakeDue is
// durably applied to a given PauseID's record at most once per Sleeping
// period — never twice, never by two callers "simultaneously."
//
// Wake does not itself claim or complete a scheduler lease — that remains
// internal/scheduler.Store's job (Claim/Complete/Fail); Wake is deliberately
// scoped to only the pause-record half of processing a claimed wake job, so
// a caller (e.g. a future scheduler-worker loop) composes
// scheduler.Store.Claim -> pause.Wake -> pause.Resume/ValidateResume ->
// scheduler.Store.Complete, with this function's compare-and-swap guarantee
// covering the middle, pause-record-mutating steps regardless of what the
// lease layer decided.
func Wake(ctx context.Context, store PauseStore, req WakeRequest) (WakeResult, error) {
	if store == nil {
		return WakeResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: Wake requires a non-nil PauseStore", Retryable: false,
		}
	}
	if req.PauseID == "" {
		return WakeResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Wake requires a PauseID", Retryable: false,
		}
	}

	status, err := applyCASVerb(ctx, store, req.PauseID, EventWakeDue, "Wake")
	if err != nil {
		// Includes both required-test failure shapes: a second duplicate
		// wake attempt on an already-WakePending/Validating/etc. record
		// (no edge for EventWakeDue from anywhere but Sleeping —
		// statemachine.go), and a cancel that already landed first
		// (Cancelled is terminal — same TransitionError shape, reported
		// as-is, never silently swallowed or retried past).
		return WakeResult{}, err
	}

	rec, found, err := store.GetByID(ctx, req.PauseID)
	if err != nil {
		return WakeResult{}, err
	}
	if !found {
		return WakeResult{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   "pause: Wake: pause record " + string(req.PauseID) + " not found",
			Retryable: false,
			Details:   map[string]string{"pause_id": string(req.PauseID)},
		}
	}
	rec.Status = status
	return WakeResult{Record: rec}, nil
}
