// interrupt.go: InterruptAndSleep — runtime-a11's closure of a genuine gap
// this comprehensive-integration node found, not a re-test of existing
// behavior. agents/runtime.md's required test "provider interrupt failure
// leaves recoverable state" already had a transition-table edge
// (statemachine.go: {Interrupting, interrupt_failed} -> Failed, proven at
// the bare Apply level by TestStateTransition_ProviderInterruptFailureLeavesRecoverableState)
// and a fake to drive it (runtime-a10's FakeTurnInterrupter), but no
// production code anywhere in this package actually CALLS a
// TurnInterrupter and applies EventProviderStopped/EventInterruptFailed to
// a real PauseRecord based on the outcome. safepoint.go's
// PersistThenInterrupt (runtime-a04) deliberately stops short of this: it
// proves persist-before-interrupt ORDERING only, via its own narrow
// Interrupter seam, and never touches PauseStore/Apply at all (see its own
// doc comment — that was runtime-a04's explicit, documented scope
// boundary, not an oversight). Nothing built since has closed the
// remaining gap: driving Interrupting -> {Sleeping | Failed} against a
// real, durable PauseRecord.
//
// InterruptAndSleep closes exactly that gap: given a record already at
// domain.PauseInterrupting (i.e. Persist has already completed — the real
// production sequencing this function assumes, not re-validates), it
// calls the real app.TurnInterrupter, then applies EventProviderStopped
// (-> Sleeping) on success or EventInterruptFailed (-> Failed) on error,
// via the same CompareAndSwapStatus discipline lifecycle.go/wake.go use —
// so a concurrent Cancel racing this call is handled identically (Cancel
// wins if it lands first; Interrupting has no Cancel edge per
// statemachine.go, so once this function's own CAS attempt is in flight
// there is nothing left to race, matching ADD §20.11's documented
// narrowing of the cancel-race window to "before Resuming actually
// starts", not "before every phase").
package pause

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// TurnInterrupterAdapter adapts the frozen app.TurnInterrupter (which
// needs a full app.RunLocator) onto this package's PauseID-keyed call
// shape — mirrors safepoint.go's own Interrupter seam's doc comment
// ("a later node wires the real app.TurnInterrupter behind an adapter
// satisfying this interface"); this is that later node, for the
// state-machine-integrated call path specifically.
type TurnInterrupterAdapter struct {
	Interrupter app.TurnInterrupter
	// Locate resolves a PauseID to the app.RunLocator the real interrupter
	// needs. A production caller supplies this from whatever record
	// carries the pause's SessionID/TurnID (e.g. its own PauseRecord
	// extension or a sibling lookup); tests supply a fixed locator.
	Locate func(pauseID domain.PauseID) app.RunLocator
}

// Interrupt implements this file's narrow Interrupter-like call shape by
// delegating to the real app.TurnInterrupter via a.Locate.
func (a TurnInterrupterAdapter) Interrupt(ctx context.Context, pauseID domain.PauseID) error {
	if a.Interrupter == nil || a.Locate == nil {
		return &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: TurnInterrupterAdapter requires a non-nil Interrupter and Locate", Retryable: false,
		}
	}
	return a.Interrupter.Interrupt(ctx, a.Locate(pauseID))
}

// InterruptAndSleep drives a PauseRecord already at domain.PauseInterrupting
// through the provider-stop-signal step (ADD §20.6 Phase 4): call
// interrupter.Interrupt(pauseID), then durably apply whichever of
// EventProviderStopped/EventInterruptFailed the outcome implies, via
// CompareAndSwapStatus (never a plain UpdateStatus — the same
// TOCTOU-safety runtime-a09 already established for Cancel/Resume/Wake
// applies here too: a concurrent Cancel or a caller retrying this same
// step after a partial failure must never silently clobber or duplicate
// the other's result).
//
// The required test this proves — "provider interrupt failure leaves
// recoverable state" — means precisely this: an Interrupt failure must NOT
// leave the record stuck at Interrupting (an intermediate state with no
// further outbound edge for wake/resume to ever find), and must NOT
// silently retry the interrupt call itself (this function attempts
// Interrupt exactly once per call; a caller wanting a retry-with-backoff
// policy composes that ABOVE this function, same as any other operational
// retry decision — this function's own job is only the state-machine
// integration, not a retry policy). Instead it durably lands the record at
// domain.PauseFailed — a terminal status, but one restart-time
// reconciliation (ADD §28.3/§28.4: "inspect provider, reconcile") can
// still read back, diagnose, and act on — "recoverable" in the sense of
// "durably observable and not corrupted," not "automatically retried."
func InterruptAndSleep(ctx context.Context, store PauseStore, interrupter Interrupter, pauseID domain.PauseID) (PauseRecord, error) {
	if store == nil {
		return PauseRecord{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: InterruptAndSleep requires a non-nil PauseStore", Retryable: false,
		}
	}
	if interrupter == nil {
		return PauseRecord{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: InterruptAndSleep requires a non-nil Interrupter", Retryable: false,
		}
	}
	if pauseID == "" {
		return PauseRecord{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: InterruptAndSleep requires a PauseID", Retryable: false,
		}
	}

	// Confirm the record is actually at Interrupting before calling the
	// provider — a caller invoking this out of sequence (e.g. Persist
	// never completed, or a concurrent caller already moved the record)
	// gets a normal TransitionError from the CAS attempt below rather than
	// this function silently calling Interrupt against a record it has no
	// business touching yet. Reading first (rather than only relying on
	// the CAS's own found/ok signal) lets the interrupt call itself be
	// skipped entirely when the precondition already doesn't hold.
	current, err := currentStatus(ctx, store, pauseID, "InterruptAndSleep")
	if err != nil {
		return PauseRecord{}, err
	}
	if current != domain.PauseInterrupting {
		return PauseRecord{}, &TransitionError{
			From:   current,
			Event:  EventProviderStopped,
			Reason: "InterruptAndSleep requires the record to be Interrupting",
		}
	}

	interruptErr := interrupter.Interrupt(ctx, pauseID)

	var status domain.PauseStatus
	if interruptErr == nil {
		status, err = applyCASFrom(ctx, store, pauseID, domain.PauseInterrupting, EventProviderStopped, "InterruptAndSleep")
	} else {
		status, err = applyCASFrom(ctx, store, pauseID, domain.PauseInterrupting, EventInterruptFailed, "InterruptAndSleep")
	}
	if err != nil {
		// The state transition itself failed (e.g. a racing Cancel already
		// moved the record — Interrupting has no Cancel edge per
		// statemachine.go, so in practice this means a concurrent caller
		// raced this same function and won; report as-is, never retried
		// silently). This is distinct from interruptErr, which is reported
		// below when the transition itself succeeds.
		return PauseRecord{}, err
	}

	rec, found, err := store.GetByID(ctx, pauseID)
	if err != nil {
		return PauseRecord{}, err
	}
	if !found {
		return PauseRecord{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   "pause: InterruptAndSleep: pause record " + string(pauseID) + " not found",
			Retryable: false,
			Details:   map[string]string{"pause_id": string(pauseID)},
		}
	}
	rec.Status = status

	if interruptErr != nil {
		// The state machine transition succeeded (durably landed at
		// Failed) even though the underlying provider call failed — that
		// is the whole point of "leaves recoverable state": the FAILURE is
		// what's being reported back to the caller, not swallowed, but the
		// STORE is left in a well-defined, readable state rather than
		// stuck at Interrupting or partially written.
		return rec, interruptErr
	}
	return rec, nil
}
