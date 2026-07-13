// Package progress implements the Progress Tree domain service (checkpoint
// role, Part A): the node/edge/artifact stores and the node state machine
// that make the Progress Tree the canonical durable task state
// (Constitution §6, Auspex_ADD.md §18).
//
// This file (statemachine.go) is the node lifecycle: the fixed set of valid
// domain.ProgressNodeStatus transitions and the single entry point
// (ValidateTransition) every status-changing store operation in this
// package must call before persisting a new status. It has no storage
// dependency — it is pure, deterministic logic over the frozen enum
// (internal/domain/status.go), so it is trivially unit-testable in
// isolation from SQLite and is the seam checkpoint-a04's CompleteNode
// protocol will call into rather than re-deriving transition rules itself.
package progress

import (
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// transitions is the frozen adjacency list of valid
// domain.ProgressNodeStatus moves, transcribed from
// CONTRACT_FREEZE.md's "Frozen state transitions" section:
//
//	pending -> ready -> in_progress -> checkpointing -> {completed | failed}
//
// with paused, skipped, blocked as side states reachable from
// in_progress/ready.
//
// Every additional edge below is a documented, narrow extension of that
// frozen backbone, not an invention of new states:
//
//   - pending -> blocked / pending -> skipped: a node can be found
//     unreachable (dependency policy) or explicitly skipped before any
//     work starts, without first passing through ready.
//   - ready -> blocked: a dependency can become violated after a node was
//     marked ready but before work started.
//   - in_progress -> failed: a node can fail outright without ever
//     reaching the checkpointing phase (e.g. the agent errors before
//     staging evidence).
//   - checkpointing -> in_progress: CompleteNode's own documented recovery
//     path (ADD §18.4 "validation fails" branch feeds back to
//     in_progress, not just blocked).
//   - paused -> in_progress / paused -> blocked / paused -> skipped: a
//     paused node resumes, is found blocked on resume, or is skipped
//     instead of resumed.
//   - blocked -> ready / blocked -> in_progress / blocked -> skipped:
//     a blocked node's dependency is resolved, or it is skipped instead.
//   - failed -> in_progress: a retried node re-enters progress; failed
//     itself stays terminal for the attempt that produced it (a new
//     attempt is a new transition into in_progress, not a mutation of the
//     failed record).
//
// completed, skipped are terminal: nothing transitions out of them. failed
// is terminal for retries handled elsewhere (a retry is a fresh
// in_progress transition, requested explicitly, not an implicit outbound
// edge a caller can reach by mistake) — see AllowRetryFromFailed below for
// the one narrow, explicit exception.
var transitions = map[domain.ProgressNodeStatus]map[domain.ProgressNodeStatus]bool{
	domain.NodePending: {
		domain.NodeReady:   true,
		domain.NodeBlocked: true,
		domain.NodeSkipped: true,
	},
	domain.NodeReady: {
		domain.NodeInProgress: true,
		domain.NodeBlocked:    true,
		domain.NodeSkipped:    true,
	},
	domain.NodeInProgress: {
		domain.NodeCheckpointing: true,
		domain.NodePaused:        true,
		domain.NodeFailed:        true,
		domain.NodeBlocked:       true,
	},
	domain.NodeCheckpointing: {
		domain.NodeCompleted:  true,
		domain.NodeFailed:     true,
		domain.NodeInProgress: true, // ADD §18.4: validation fails -> back to in_progress
	},
	domain.NodePaused: {
		domain.NodeInProgress: true,
		domain.NodeBlocked:    true,
		domain.NodeSkipped:    true,
	},
	domain.NodeBlocked: {
		domain.NodeReady:      true,
		domain.NodeInProgress: true,
		domain.NodeSkipped:    true,
	},
	domain.NodeFailed: {
		domain.NodeInProgress: true, // explicit retry only, see AllowRetryFromFailed
	},
	domain.NodeCompleted: {}, // terminal
	domain.NodeSkipped:   {}, // terminal
}

// validStatuses is the frozen enum membership set, used to reject unknown
// status values outright rather than silently treating them as "no
// transitions defined."
var validStatuses = map[domain.ProgressNodeStatus]bool{
	domain.NodePending:       true,
	domain.NodeReady:         true,
	domain.NodeInProgress:    true,
	domain.NodeCheckpointing: true,
	domain.NodePaused:        true,
	domain.NodeCompleted:     true,
	domain.NodeFailed:        true,
	domain.NodeSkipped:       true,
	domain.NodeBlocked:       true,
}

// TransitionError is returned by ValidateTransition for a rejected move. It
// wraps the frozen domain.Error shape (ErrCodeValidation, not retryable) so
// callers can treat it uniformly with every other validation failure in
// this package (via errors.As(err, *domain.Error) through Unwrap), while
// From/To stay programmatically inspectable without parsing the message
// string.
type TransitionError struct {
	From domain.ProgressNodeStatus
	To   domain.ProgressNodeStatus
	Err  *domain.Error
}

// Error implements the error interface by delegating to the wrapped
// *domain.Error's message.
func (e *TransitionError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped *domain.Error for errors.As/errors.Is.
func (e *TransitionError) Unwrap() error { return e.Err }

// newTransitionError builds a *TransitionError describing an invalid
// transition attempt.
func newTransitionError(from, to domain.ProgressNodeStatus, reason string) *TransitionError {
	return &TransitionError{
		From: from,
		To:   to,
		Err: &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("progress: invalid node transition %s -> %s: %s", from, to, reason),
			Retryable: false,
			Details: map[string]string{
				"from": string(from),
				"to":   string(to),
			},
		},
	}
}

// ValidateTransition reports whether moving a node from `from` to `to` is a
// permitted state-machine edge, per the frozen adjacency list above. It
// returns nil on a valid move (including the identity checks below) and a
// *TransitionError (ErrCodeValidation, non-retryable) otherwise. It never
// mutates anything — callers are responsible for persisting the new status
// only after this returns nil, inside their own transaction boundary.
func ValidateTransition(from, to domain.ProgressNodeStatus) error {
	if !validStatuses[from] {
		return newTransitionError(from, to, fmt.Sprintf("unknown source status %q", from))
	}
	if !validStatuses[to] {
		return newTransitionError(from, to, fmt.Sprintf("unknown target status %q", to))
	}
	if from == to {
		return newTransitionError(from, to, "no-op self-transition is not a valid state change")
	}
	if transitions[from][to] {
		return nil
	}
	return newTransitionError(from, to, "not a permitted edge in the node state machine")
}

// IsTerminal reports whether status has no outbound transitions at all
// (completed, skipped). failed is deliberately NOT terminal here — it has
// exactly one outbound edge (retry into in_progress) — so callers that need
// "can nothing else ever happen to this node" must check both IsTerminal
// and the retry policy explicitly rather than relying on this alone.
func IsTerminal(status domain.ProgressNodeStatus) bool {
	edges, ok := transitions[status]
	return ok && len(edges) == 0
}

// AllowedTransitions returns the set of statuses reachable from `from` in
// one step, for callers (e.g. CLI introspection, error messages) that want
// to present valid next moves rather than trial-and-error against
// ValidateTransition.
func AllowedTransitions(from domain.ProgressNodeStatus) []domain.ProgressNodeStatus {
	edges := transitions[from]
	out := make([]domain.ProgressNodeStatus, 0, len(edges))
	for to, ok := range edges {
		if ok {
			out = append(out, to)
		}
	}
	return out
}
