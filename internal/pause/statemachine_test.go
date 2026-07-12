package pause

import (
	"errors"
	"testing"

	"github.com/huaiche94/preflight/internal/domain"
)

// TestStateTransition_HappyPath walks the full nominal path named in
// agents/runtime.md's "Required state path" (mapped onto the frozen
// domain.PauseStatus enum per doc.go): predicted -> requested ->
// quiescing -> checkpointing -> interrupting -> sleeping -> wake_pending
// -> validating -> resuming -> resumed.
func TestStateTransition_HappyPath(t *testing.T) {
	steps := []struct {
		event Event
		want  domain.PauseStatus
	}{
		{EventDebouncePassed, domain.PauseRequested},
		{EventThresholdCrossed, domain.PauseQuiescing},
		{EventSafePointReached, domain.PauseCheckpointing},
		{EventCheckpointVerified, domain.PauseInterrupting},
		{EventProviderStopped, domain.PauseSleeping},
		{EventWakeDue, domain.PauseWakePending},
		{EventResumeValid, domain.PauseValidating},
		{EventResumeValid, domain.PauseResuming},
		{EventResumeStarted, domain.PauseResumed},
	}

	state := domain.PausePredicted
	for i, step := range steps {
		next, err := Apply(state, step.event)
		if err != nil {
			t.Fatalf("step %d: Apply(%q, %q): unexpected error: %v", i, state, step.event, err)
		}
		if next != step.want {
			t.Fatalf("step %d: Apply(%q, %q) = %q, want %q", i, state, step.event, next, step.want)
		}
		state = next
	}

	if !IsTerminal(state) {
		t.Fatalf("final state %q should be terminal", state)
	}
}

// TestStateTransition_EmergencySkipsQuiesceWait proves ADD §17.6's
// emergency path: EventEmergency short-circuits Requested straight to
// Checkpointing (skipping the double-sample/safe-point wait), and is also
// valid directly from Quiescing (a pause already waiting for a safe point
// that then escalates to emergency).
func TestStateTransition_EmergencySkipsQuiesceWait(t *testing.T) {
	next, err := Apply(domain.PauseRequested, EventEmergency)
	if err != nil {
		t.Fatalf("Requested + emergency: unexpected error: %v", err)
	}
	if next != domain.PauseQuiescing {
		t.Fatalf("Requested + emergency = %q, want quiescing", next)
	}

	next, err = Apply(domain.PauseQuiescing, EventEmergency)
	if err != nil {
		t.Fatalf("Quiescing + emergency: unexpected error: %v", err)
	}
	if next != domain.PauseCheckpointing {
		t.Fatalf("Quiescing + emergency = %q, want checkpointing", next)
	}
}

// TestStateTransition_CheckpointFailureFailsClosed proves ADD §20.15:
// "state checkpoint fails -> do not interrupt unless emergency; alert" —
// a checkpoint failure from Checkpointing goes to Failed, never onward to
// Interrupting.
func TestStateTransition_CheckpointFailureFailsClosed(t *testing.T) {
	next, err := Apply(domain.PauseCheckpointing, EventCheckpointFailed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != domain.PauseFailed {
		t.Fatalf("Checkpointing + checkpoint_failed = %q, want failed", next)
	}
	if !IsTerminal(next) {
		t.Fatal("failed must be terminal")
	}
}

// TestStateTransition_UnsafeQuotaReschedules proves the required test
// "unsafe quota reschedules": Validating + quota_unsafe returns to
// Sleeping (not Failed, not BlockedConflict) so the scheduler retries
// later.
func TestStateTransition_UnsafeQuotaReschedules(t *testing.T) {
	next, err := Apply(domain.PauseValidating, EventQuotaUnsafe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != domain.PauseSleeping {
		t.Fatalf("Validating + quota_unsafe = %q, want sleeping", next)
	}
}

// TestStateTransition_RepoConflictBlocks proves the required test "repo
// overlap blocks": Validating + conflict goes to BlockedConflict, a
// distinct outcome from a reschedule.
func TestStateTransition_RepoConflictBlocks(t *testing.T) {
	next, err := Apply(domain.PauseValidating, EventConflict)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != domain.PauseBlockedConflict {
		t.Fatalf("Validating + conflict = %q, want blocked_conflict", next)
	}
}

// TestStateTransition_CancelWinsRaceWithWake proves the required test
// "cancel wins race with wake": once Cancelled is reached from Sleeping,
// it is terminal — a subsequent wake_due (representing the race loser)
// has no valid transition out of Cancelled.
func TestStateTransition_CancelWinsRaceWithWake(t *testing.T) {
	cancelled, err := Apply(domain.PauseSleeping, EventCancel)
	if err != nil {
		t.Fatalf("Sleeping + cancel: unexpected error: %v", err)
	}
	if cancelled != domain.PauseCancelled {
		t.Fatalf("Sleeping + cancel = %q, want cancelled", cancelled)
	}

	// The wake side of the race arrives after cancel already won: applying
	// wake_due to the now-cancelled record must be rejected, not silently
	// accepted or resurrected into wake_pending.
	_, err = Apply(cancelled, EventWakeDue)
	var terr *TransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("cancelled + wake_due: got err %v, want *TransitionError", err)
	}
	if !terr.Terminal {
		t.Fatalf("cancelled + wake_due: TransitionError.Terminal = false, want true")
	}
}

// TestStateTransition_ProviderInterruptFailureLeavesRecoverableState
// proves the required test "provider interrupt failure leaves recoverable
// state": Interrupting + interrupt_failed goes to Failed (a terminal but
// still-inspectable/recoverable-via-restart-reconciliation state per ADD
// §28.3/§28.4), not back to an earlier phase and not to Cancelled.
func TestStateTransition_ProviderInterruptFailureLeavesRecoverableState(t *testing.T) {
	next, err := Apply(domain.PauseInterrupting, EventInterruptFailed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != domain.PauseFailed {
		t.Fatalf("Interrupting + interrupt_failed = %q, want failed", next)
	}
}

// TestStateTransition_ResumeFailurePath proves ADD §20.5's "Resuming ->
// Failed: resume failed" edge.
func TestStateTransition_ResumeFailurePath(t *testing.T) {
	next, err := Apply(domain.PauseResuming, EventResumeFailed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != domain.PauseFailed {
		t.Fatalf("Resuming + resume_failed = %q, want failed", next)
	}
}

// TestStateTransition_TerminalStatesRejectEveryEvent proves that once a
// pause reaches any terminal state (resumed, cancelled, failed), no event
// at all is accepted — terminal really means terminal, not "terminal for
// the events we happened to test."
func TestStateTransition_TerminalStatesRejectEveryEvent(t *testing.T) {
	for _, terminal := range []domain.PauseStatus{domain.PauseResumed, domain.PauseCancelled, domain.PauseFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			for _, ev := range allEventsInDeclarationOrder {
				if Validate(terminal, ev) {
					t.Errorf("Validate(%q, %q) = true, want false (terminal state)", terminal, ev)
				}
				_, err := Apply(terminal, ev)
				var terr *TransitionError
				if !errors.As(err, &terr) {
					t.Errorf("Apply(%q, %q): got err %v, want *TransitionError", terminal, ev, err)
					continue
				}
				if !terr.Terminal {
					t.Errorf("Apply(%q, %q): TransitionError.Terminal = false, want true", terminal, ev)
				}
			}
		})
	}
}

// TestStateTransition_UnknownStateRejected proves Apply fails closed (with
// Unknown: true, not a panic or a silent no-op) for any string that is not
// one of the twelve frozen domain.PauseStatus values — e.g. a typo, an
// empty string, or a value belonging to a different enum entirely
// (ProgressNodeStatus's "pending").
func TestStateTransition_UnknownStateRejected(t *testing.T) {
	for _, bogus := range []domain.PauseStatus{"", "typo_state", domain.PauseStatus(domain.NodePending)} {
		t.Run(string(bogus), func(t *testing.T) {
			if IsKnownState(bogus) {
				t.Fatalf("IsKnownState(%q) = true, want false", bogus)
			}
			_, err := Apply(bogus, EventDebouncePassed)
			var terr *TransitionError
			if !errors.As(err, &terr) {
				t.Fatalf("Apply(%q, ...): got err %v, want *TransitionError", bogus, err)
			}
			if !terr.Unknown {
				t.Fatalf("Apply(%q, ...): TransitionError.Unknown = false, want true", bogus)
			}
		})
	}
}

// TestStateTransition_InvalidEdgeRejected proves that a known, non-terminal
// state still rejects an event with no defined edge (e.g. jumping straight
// from Requested to Resumed) rather than guessing the "closest" valid
// transition.
func TestStateTransition_InvalidEdgeRejected(t *testing.T) {
	cases := []struct {
		from  domain.PauseStatus
		event Event
	}{
		{domain.PauseRequested, EventResumeStarted},
		{domain.PauseCheckpointing, EventWakeDue},
		{domain.PauseSleeping, EventResumeStarted},
		{domain.PauseValidating, EventProviderStopped},
	}
	for _, c := range cases {
		t.Run(string(c.from)+"/"+string(c.event), func(t *testing.T) {
			if Validate(c.from, c.event) {
				t.Fatalf("Validate(%q, %q) = true, want false", c.from, c.event)
			}
			_, err := Apply(c.from, c.event)
			var terr *TransitionError
			if !errors.As(err, &terr) {
				t.Fatalf("got err %v, want *TransitionError", err)
			}
			if terr.Unknown || terr.Terminal {
				t.Fatalf("TransitionError = %+v, want a plain no-edge rejection", terr)
			}
		})
	}
}

// TestStateTransition_EveryFrozenStateIsReachableAndHasAnEdgeOrIsTerminal
// is a table-completeness check: every one of the twelve frozen
// domain.PauseStatus values is either terminal or has at least one
// outbound edge — a state the table forgot to wire up would otherwise be
// a silent dead end discovered only much later by an integration test.
func TestStateTransition_EveryFrozenStateIsReachableAndHasAnEdgeOrIsTerminal(t *testing.T) {
	for status := range allKnownStates {
		if IsTerminal(status) {
			continue
		}
		if len(ValidEvents(status)) == 0 {
			t.Errorf("state %q is non-terminal but has no outbound transition", status)
		}
	}
}

// TestStateTransition_BlockedConflictCanStillBeCancelled proves
// BlockedConflict is not itself accidentally terminal — ADD §20.9's UI
// offers a [Cancel] action from the blocked state.
func TestStateTransition_BlockedConflictCanStillBeCancelled(t *testing.T) {
	if IsTerminal(domain.PauseBlockedConflict) {
		t.Fatal("blocked_conflict must not be terminal: ADD §20.9 UI offers Cancel from it")
	}
	next, err := Apply(domain.PauseBlockedConflict, EventCancel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != domain.PauseCancelled {
		t.Fatalf("BlockedConflict + cancel = %q, want cancelled", next)
	}
}

// TestStateTransition_CancelAcceptedFromEveryPreSleepPhase proves cancel
// is honored throughout the pre-sleep pipeline (ADD §20.6's phases are all
// user-visible and cancellable before the provider process is actually
// asleep), not just from Sleeping.
func TestStateTransition_CancelAcceptedFromEveryPreSleepPhase(t *testing.T) {
	for _, from := range []domain.PauseStatus{
		domain.PausePredicted,
		domain.PauseRequested,
		domain.PauseQuiescing,
		domain.PauseCheckpointing,
		domain.PauseSleeping,
		domain.PauseWakePending,
		domain.PauseValidating,
		domain.PauseBlockedConflict,
	} {
		t.Run(string(from), func(t *testing.T) {
			next, err := Apply(from, EventCancel)
			if err != nil {
				t.Fatalf("Apply(%q, cancel): unexpected error: %v", from, err)
			}
			if next != domain.PauseCancelled {
				t.Fatalf("Apply(%q, cancel) = %q, want cancelled", from, next)
			}
		})
	}
}

// TestStateTransition_InterruptingHasNoCancelEdge proves a deliberate
// narrowing: once a provider interrupt is actually in flight
// (Interrupting), Preflight cannot cancel out from under an in-progress
// process signal — the only valid outcomes are provider_stopped or
// interrupt_failed. This documents the boundary explicitly rather than
// leaving "why doesn't Interrupting accept cancel" to be reverse-engineered
// from the table.
func TestStateTransition_InterruptingHasNoCancelEdge(t *testing.T) {
	if Validate(domain.PauseInterrupting, EventCancel) {
		t.Fatal("Interrupting must not accept cancel while a provider interrupt is in flight")
	}
}

// TestStateTransition_TableHasNoDuplicateOrNilEntries is a structural
// sanity check on transitionTable itself.
func TestStateTransition_TableHasNoDuplicateOrNilEntries(t *testing.T) {
	seen := make(map[transitionKey]bool, len(transitionTable))
	for key, next := range transitionTable {
		if seen[key] {
			t.Errorf("duplicate transition table entry for %+v", key)
		}
		seen[key] = true
		if next == "" {
			t.Errorf("transition table entry for %+v maps to empty PauseStatus", key)
		}
		if !IsKnownState(next) {
			t.Errorf("transition table entry for %+v maps to unrecognized PauseStatus %q", key, next)
		}
	}
}
