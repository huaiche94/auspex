package pause

import (
	"fmt"

	"github.com/huaiche94/preflight/internal/domain"
)

// Event names the reason a transition is being attempted. Events are this
// package's own vocabulary (not persisted, not part of any frozen
// contract) — they exist so the transition table can distinguish, e.g.,
// two different edges that both leave `checkpointing` (one on success, one
// on failure) without the caller having to know the table's internal
// shape.
type Event string

const (
	// EventThresholdCrossed: a calibrated or emergency trigger fired
	// (ADD §20.2/§17.6) — the pause enters existence at Predicted.
	EventThresholdCrossed Event = "threshold_crossed"
	// EventDebouncePassed: two qualifying observations at least 5s apart
	// (ADD §17.6) turned a Predicted pause into a Requested one.
	EventDebouncePassed Event = "debounce_passed"
	// EventSafePointReached: the safe-point coordinator observed a valid
	// boundary (ADD §20.4) while quiescing.
	EventSafePointReached Event = "safe_point_reached"
	// EventEmergency: ADD §20.14/§17.6 emergency trigger — skips the
	// double-sample debounce and the safe-point wait, going straight to
	// checkpointing with a minimal/partial checkpoint allowed.
	EventEmergency Event = "emergency"
	// EventCheckpointVerified: Phase-3 persist (Progress Tree snapshot,
	// State Checkpoint, repository checkpoint, pause record, wake job)
	// completed and verified.
	EventCheckpointVerified Event = "checkpoint_verified"
	// EventCheckpointFailed: Phase-3 persist failed. Per ADD §20.15,
	// "state checkpoint fails -> do not interrupt unless emergency;
	// alert" — a non-emergency checkpoint failure is fail-closed.
	EventCheckpointFailed Event = "checkpoint_failed"
	// EventProviderStopped: the provider confirmed interrupted/stopped
	// (ADD §20.6 Phase 4).
	EventProviderStopped Event = "provider_stopped"
	// EventInterruptFailed: provider interrupt timed out. ADD §20.15:
	// "kill managed process, mark uncertain" — modeled as Failed here;
	// the process-kill/uncertainty bookkeeping is the caller's (Part A
	// deliverable 11's fake contract / a later real implementation), not
	// this state machine's concern.
	EventInterruptFailed Event = "interrupt_failed"
	// EventWakeDue: the durable scheduler's run_after has been reached
	// for this pause's wake job (folds into Sleeping -> WakePending; see
	// doc.go).
	EventWakeDue Event = "wake_due"
	// EventResumeValid: ADD §20.8's full resume-validation checklist
	// passed (quota safe, repo fingerprint compatible, session/provider
	// capability valid, authorization/consent valid).
	EventResumeValid Event = "resume_valid"
	// EventQuotaUnsafe: resume validation found quota still unsafe (ADD
	// §20.5: "Validating -> Sleeping: quota still unsafe") — reschedules
	// rather than failing.
	EventQuotaUnsafe Event = "quota_unsafe"
	// EventConflict: resume validation found a repository/session
	// conflict (ADD §20.5/§20.9) — blocks, does not reschedule.
	EventConflict Event = "conflict"
	// EventResumeStarted: the provider session resume/fork/bootstrap
	// began (ADD §20.5: "Resuming -> Active: resume started").
	EventResumeStarted Event = "resume_started"
	// EventResumeFailed: resume itself failed after validation passed
	// (ADD §20.5: "Resuming -> Failed: resume failed").
	EventResumeFailed Event = "resume_failed"
	// EventCancel: user cancellation (ADD §20.5: "Sleeping -> Cancelled:
	// user cancel"). Also valid from any pre-sleep state that has not yet
	// reached a terminal outcome — see the transition table for the exact
	// set; ADD §20.11 "cancel wins race with wake" requires cancel to be
	// acceptable up to the point resume has actually started.
	EventCancel Event = "cancel"
)

// transitionKey is a (current state, event) pair — the transition table's
// lookup key.
type transitionKey struct {
	From  domain.PauseStatus
	Event Event
}

// transitionTable is the explicit, exhaustive valid-transition map (P0
// deliverable 1). Every entry is a real, ADD-derived edge; anything not
// listed here is rejected by Validate/Apply. Built as a package-level var
// (not a function) so its shape is inspectable by tests without invoking
// any logic beyond a map lookup.
var transitionTable = map[transitionKey]domain.PauseStatus{
	// Predicted: a pause record now exists (ADD §20.5 Active -> Predicted).
	// This package's state machine starts here; there is no frozen
	// PauseStatus for the pre-pause "observing/Active" state (see doc.go).
	{From: domain.PausePredicted, Event: EventDebouncePassed}: domain.PauseRequested,
	{From: domain.PausePredicted, Event: EventEmergency}:      domain.PauseRequested,
	{From: domain.PausePredicted, Event: EventCancel}:         domain.PauseCancelled,

	// Requested -> Quiescing: stop new dispatch (ADD §20.6 Phase 1 -> 2).
	{From: domain.PauseRequested, Event: EventThresholdCrossed}: domain.PauseQuiescing,
	{From: domain.PauseRequested, Event: EventSafePointReached}: domain.PauseQuiescing,
	{From: domain.PauseRequested, Event: EventEmergency}:        domain.PauseQuiescing,
	{From: domain.PauseRequested, Event: EventCancel}:           domain.PauseCancelled,

	// Quiescing: waiting for a safe point (ADD §20.4/§20.6 Phase 2),
	// max 30s, or an emergency short-circuit (ADD §17.6: "Emergency 可跳過
	// double-sample, but still runs a minimal state checkpoint first" ->
	// enters Checkpointing directly, same as a normal safe point).
	{From: domain.PauseQuiescing, Event: EventSafePointReached}: domain.PauseCheckpointing,
	{From: domain.PauseQuiescing, Event: EventEmergency}:        domain.PauseCheckpointing,
	{From: domain.PauseQuiescing, Event: EventCancel}:           domain.PauseCancelled,

	// Checkpointing: Phase 3 persist (Progress Tree snapshot, State
	// Checkpoint, repository checkpoint, pause record, wake strategy).
	// Verified -> Interrupting; failed -> Failed (ADD §20.15: "state
	// checkpoint fails -> do not interrupt unless emergency; alert" — a
	// non-emergency failure does not proceed to Interrupting).
	{From: domain.PauseCheckpointing, Event: EventCheckpointVerified}: domain.PauseInterrupting,
	{From: domain.PauseCheckpointing, Event: EventCheckpointFailed}:   domain.PauseFailed,
	{From: domain.PauseCheckpointing, Event: EventCancel}:             domain.PauseCancelled,

	// Interrupting: provider stop signal in flight (ADD §20.6 Phase 4).
	{From: domain.PauseInterrupting, Event: EventProviderStopped}: domain.PauseSleeping,
	{From: domain.PauseInterrupting, Event: EventInterruptFailed}: domain.PauseFailed,

	// Sleeping: wake job scheduled (ADD §20.6 Phase 5). Cancel wins a race
	// with wake per ADD §20.11 / Part A required test "cancel wins race
	// with wake"; wake_due only ever moves Sleeping forward, never past a
	// cancel that already landed (enforced by the caller checking status
	// before applying EventWakeDue, and by Cancelled having no outbound
	// transitions at all — see "terminal states" below).
	{From: domain.PauseSleeping, Event: EventWakeDue}: domain.PauseWakePending,
	{From: domain.PauseSleeping, Event: EventCancel}:  domain.PauseCancelled,

	// WakePending -> Validating (ADD §20.5/§20.8).
	{From: domain.PauseWakePending, Event: EventResumeValid}: domain.PauseValidating,
	{From: domain.PauseWakePending, Event: EventCancel}:      domain.PauseCancelled,

	// Validating: ADD §20.8's checklist. Valid -> Resuming; quota still
	// unsafe -> back to Sleeping (reschedule, required test "unsafe quota
	// reschedules"); conflict -> BlockedConflict (required test "repo
	// overlap blocks").
	{From: domain.PauseValidating, Event: EventResumeValid}: domain.PauseResuming,
	{From: domain.PauseValidating, Event: EventQuotaUnsafe}: domain.PauseSleeping,
	{From: domain.PauseValidating, Event: EventConflict}:    domain.PauseBlockedConflict,
	{From: domain.PauseValidating, Event: EventCancel}:      domain.PauseCancelled,

	// Resuming -> Resumed (ADD §20.5: "Resuming -> Active: resume
	// started" — domain has no separate "Active" pause status; a resumed
	// pause's terminal record status is `resumed`, and the task/turn
	// resumes normal, non-pause-tracked execution from there) or Failed.
	{From: domain.PauseResuming, Event: EventResumeStarted}: domain.PauseResumed,
	{From: domain.PauseResuming, Event: EventResumeFailed}:  domain.PauseFailed,

	// BlockedConflict is reachable via manual paths (Inspect Diff / Create
	// New Plan / Resume Manually / Cancel per ADD §20.9's UI) that are
	// outside this state machine's scope (they re-enter via a fresh
	// RequestPause/manual resume flow, not a transition edge); cancel from
	// a conflict is still a same-package edge worth keeping explicit.
	{From: domain.PauseBlockedConflict, Event: EventCancel}: domain.PauseCancelled,
}

// terminalStates have no outbound transitions: applying ANY event from one
// of these is always rejected. Modeled as a set (not just "absent from
// transitionTable") so ValidTransitions/IsTerminal can report a state as
// deliberately terminal rather than merely "no matching edges yet found in
// this map" — Constitution §6 rule 4's fixed-enum discipline means the two
// cases must never be confused.
var terminalStates = map[domain.PauseStatus]bool{
	domain.PauseResumed:   true,
	domain.PauseCancelled: true,
	domain.PauseFailed:    true,
}

// allKnownStates is used for input validation only (rejecting garbage
// domain.PauseStatus values that aren't even part of the frozen enum).
var allKnownStates = map[domain.PauseStatus]bool{
	domain.PausePredicted:       true,
	domain.PauseRequested:       true,
	domain.PauseQuiescing:       true,
	domain.PauseCheckpointing:   true,
	domain.PauseInterrupting:    true,
	domain.PauseSleeping:        true,
	domain.PauseWakePending:     true,
	domain.PauseValidating:      true,
	domain.PauseResuming:        true,
	domain.PauseResumed:         true,
	domain.PauseBlockedConflict: true,
	domain.PauseCancelled:       true,
	domain.PauseFailed:          true,
}

// IsTerminal reports whether status has no valid outbound transition.
func IsTerminal(status domain.PauseStatus) bool {
	return terminalStates[status]
}

// IsKnownState reports whether status is one of the twelve frozen
// domain.PauseStatus wire values. Any other string (including "", a typo,
// or a value from a different enum) is not a state this package's
// transition table can ever have an edge for.
func IsKnownState(status domain.PauseStatus) bool {
	return allKnownStates[status]
}

// Validate reports whether the (from, event) pair has a defined edge in
// the transition table, without mutating anything. It is the pure
// predicate Apply is built on, exposed separately so callers (and tests)
// can ask "is this legal?" without needing a value to transition.
func Validate(from domain.PauseStatus, event Event) bool {
	_, ok := transitionTable[transitionKey{From: from, Event: event}]
	return ok
}

// TransitionError is returned by Apply when (from, event) has no valid
// edge. It distinguishes three rejection reasons so callers/tests can
// assert on the right one: an unknown `from` state, a terminal `from`
// state, or a `from` state that is known and non-terminal but simply has
// no edge for this event.
type TransitionError struct {
	From     domain.PauseStatus
	Event    Event
	Reason   string
	Unknown  bool
	Terminal bool
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("pause: invalid transition from %q on event %q: %s", e.From, e.Event, e.Reason)
}

// Apply validates and executes a state transition. On success it returns
// the next domain.PauseStatus and a nil error. On an invalid transition it
// returns the zero PauseStatus and a *TransitionError — Apply never
// silently clamps to the current state or guesses a "closest" valid
// transition (Constitution §6: state writes must be crash-recoverable and
// unambiguous, not best-effort).
func Apply(from domain.PauseStatus, event Event) (domain.PauseStatus, error) {
	if !IsKnownState(from) {
		return "", &TransitionError{
			From:    from,
			Event:   event,
			Reason:  "not a recognized PauseStatus value",
			Unknown: true,
		}
	}
	if IsTerminal(from) {
		return "", &TransitionError{
			From:     from,
			Event:    event,
			Reason:   "state is terminal; no outbound transitions",
			Terminal: true,
		}
	}
	next, ok := transitionTable[transitionKey{From: from, Event: event}]
	if !ok {
		return "", &TransitionError{
			From:   from,
			Event:  event,
			Reason: "no transition defined for this event from this state",
		}
	}
	return next, nil
}

// ValidEvents returns every event that has a defined transition from
// status, in a deterministic (map-iteration-independent) order. Useful for
// diagnostics (`preflight doctor`-style introspection) and tests that want
// to assert the full outbound edge set for a state rather than probing one
// event at a time.
func ValidEvents(status domain.PauseStatus) []Event {
	var events []Event
	// Iterate a fixed declaration order rather than transitionTable's
	// randomized map order, so callers get stable output.
	for _, ev := range allEventsInDeclarationOrder {
		if Validate(status, ev) {
			events = append(events, ev)
		}
	}
	return events
}

var allEventsInDeclarationOrder = []Event{
	EventThresholdCrossed,
	EventDebouncePassed,
	EventSafePointReached,
	EventEmergency,
	EventCheckpointVerified,
	EventCheckpointFailed,
	EventProviderStopped,
	EventInterruptFailed,
	EventWakeDue,
	EventResumeValid,
	EventQuotaUnsafe,
	EventConflict,
	EventResumeStarted,
	EventResumeFailed,
	EventCancel,
}
