// cancel.go: operator-initiated cancellation of a scheduled wake job —
// FR-163's storage half (issue #10; ADD §8.4 "cancel scheduled resume").
// The VS Code companion (and any future CLI surface) POSTs
// /v1/scheduler/jobs/{id}/cancel, internal/httpapi maps that onto this
// method, and this method is the single place the state transition and its
// concurrency rules live.
//
// # Why `dead` + last_error, not a new `cancelled` status
//
// wake_jobs.status has no CHECK constraint (0051_wake_jobs.sql), so the
// schema would technically accept any string — but the status vocabulary is
// this package's lease.go constants (scheduled/leased/done/dead), and every
// consumer (Claim's claimability predicate, Restart's leased-only sweep,
// list.go's status counts, the worker's outcome mapping) was written
// against exactly those four. A fifth status would be a vocabulary change
// every one of those call sites — plus the frozen-ish jobView the HTTP API
// already serves — would need to re-audit, for zero behavioral gain: `dead`
// already means "terminal, will never execute, never claimable", which is
// precisely a cancelled job's semantics. So Cancel reuses the existing
// terminal status and records WHY in last_error (CancelledByOperator),
// the same field Fail() already uses for its terminal reason — least
// invasive representation consistent with the existing schema, per the
// issue-#10 decision note.
//
// # What cancelling a wake job does NOT do
//
// It does not touch the pause record. pause.Cancel (lifecycle.go) is the
// pause-level operation with its own state machine and cancel-wins-race
// semantics; cancelling only the wake job means "do not auto-resume this
// pause" (FR-154/FR-163) while the pause itself stays Sleeping, still
// eligible for a manual `auspex resume`. The two operations compose but
// neither implies the other.
package scheduler

import (
	"context"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// CancelledByOperator is the last_error value a cancelled job carries —
// exported so API/UI layers can distinguish "operator cancelled this"
// from a genuinely dead (retries-exhausted) job without string-matching
// a private literal.
const CancelledByOperator = "cancelled by operator"

// Cancel transitions a wake job from `scheduled` to `dead` with
// last_error = CancelledByOperator. Cancellation is legal ONLY from
// `scheduled`: a `leased` job is mid-execution on some worker (interrupting
// it here would race the worker's own Complete/Fail bookkeeping), and
// `done`/`dead` are terminal — cancelling them would rewrite history.
//
// Concurrency (the FR-163 cancel-vs-claim race): the UPDATE re-checks
// status = 'scheduled' in its WHERE clause, so Cancel and a concurrent
// Claim resolve exactly-once through SQLite's own write serialization —
// if Claim commits first the row is `leased` and this UPDATE matches zero
// rows (Conflict); if Cancel commits first the row is `dead` and Claim's
// claimability predicate no longer matches it. There is no interleaving in
// which both win, and no lost update in which either silently overwrites
// the other (mirrors Claim's own defensive WHERE re-check).
func (s *Store) Cancel(ctx context.Context, id domain.WakeJobID) (Job, error) {
	if id == "" {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "scheduler: Cancel requires a job ID", Retryable: false,
		}
	}

	now := formatTime(s.clock.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
		UPDATE wake_jobs
		SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, StatusDead, CancelledByOperator, now, string(id), StatusScheduled)
	if err != nil {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: cancel: %v", err), Retryable: false,
		}
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: cancel rows affected: %v", err), Retryable: false,
		}
	}
	if affected == 0 {
		// Nothing matched: either the job does not exist (NotFound, from
		// Get) or it exists in a non-cancellable status (Conflict, with the
		// actual status in Details so the caller can render WHY — "already
		// running" vs "already finished").
		current, err := s.Get(ctx, id)
		if err != nil {
			return Job{}, err
		}
		return Job{}, &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   fmt.Sprintf("scheduler: cancel: job %q is %q, not %q (a leased job is mid-execution; done/dead are terminal)", id, current.Status, StatusScheduled),
			Retryable: false,
			Details:   map[string]string{"wake_job_id": string(id), "status": current.Status},
		}
	}
	return s.Get(ctx, id)
}
