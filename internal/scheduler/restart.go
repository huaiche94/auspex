package scheduler

import (
	"context"
	"fmt"

	"github.com/huaiche94/preflight/internal/domain"
)

// RestartReport summarizes what a single Restart call recovered, for the
// startup report ADD §28.3 step 10 names ("emit startup report") — this
// package does not itself emit or format that report (that is a Part
// B/daemon concern), it only returns the data needed to build one.
type RestartReport struct {
	// RecoveredLeased is how many `leased` wake_jobs rows were released
	// back to `scheduled` because this restart categorically cannot be
	// the process that leased them (see Restart's doc comment).
	RecoveredLeased int
	// OverdueClaimable is how many rows are now sitting `scheduled` with
	// run_after already in the past — i.e. ready for the next Claim sweep
	// to pick up immediately (ADD §20.7 "machine shutdown: on next daemon
	// start process overdue jobs"). This is informational only; Restart
	// does not itself claim/execute them (that remains Claim's job, so a
	// single worker still claims one at a time under BEGIN IMMEDIATE, not
	// this restart-recovery step).
	OverdueClaimable int
}

// Restart implements agents/runtime.md Part A deliverable 7: "Restart
// recovery of overdue/leased jobs." It is the scheduler's half of ADD
// §28.3's startup reconciliation ("release expired scheduler leases" /
// "process overdue wake jobs") and the crash-consistency-matrix row "wake
// job leased then daemon dies -> lease expiry reclaims" / ADD §29.6
// scenario 11 "daemon restart rebuilds job" — call it once, early, on
// every process startup, before any worker begins calling Claim.
//
// # Why every `leased` row, not just ones whose lease has already expired
//
// ReclaimExpired (runtime-a06) already releases a lease once
// lease_expires_at has passed — that handles the general, ANY-TIME case
// (a worker's lease can expire while the rest of the daemon keeps
// running, e.g. a single stuck goroutine). Restart is a narrower, stronger
// statement specific to process startup: by definition, every lease
// owner recorded in this SQLite file was created by a PREVIOUS process
// instance (this call happens before this process's Store has claimed
// anything), so a `leased` row cannot legitimately still be "in
// progress" from this process's point of view even if its
// lease_expires_at has not technically elapsed yet — the worker that
// held it no longer exists to renew, complete, or fail it, and waiting
// out the remainder of a stale lease's TTL would silently delay recovery
// (and, per ADD §20.5's overall design, the daemon holding the only
// durable record of that pause's wake job) for up to DefaultLeaseDuration
// with no benefit. Restart therefore reclaims unconditionally rather than
// re-checking expiry.
//
// # No duplicate execution
//
// Restart never calls Claim itself and never marks a job `done` — it only
// ever moves a `leased` row back to `scheduled` (releasing lease_owner/
// lease_expires_at, exactly like ReclaimExpired), the same state a job
// reaches after Fail() with attempts remaining. A job already `done` or
// `dead` is untouched (Restart's UPDATE only matches status = 'leased'),
// so a job that actually finished before the crash is never
// re-executed, and a job already exhausted (dead) is never resurrected.
// Once released, the existing Claim serialization (BEGIN IMMEDIATE, one
// worker wins per row) is exactly what already prevents two workers from
// executing the same recovered job twice — Restart's only job is to make
// the row claimable again, not to re-implement single-claim correctness a
// second time.
func (s *Store) Restart(ctx context.Context) (RestartReport, error) {
	recovered, err := s.reclaimAllLeased(ctx)
	if err != nil {
		return RestartReport{}, err
	}

	overdue, err := s.countOverdueScheduled(ctx)
	if err != nil {
		return RestartReport{}, err
	}

	return RestartReport{RecoveredLeased: recovered, OverdueClaimable: overdue}, nil
}

// reclaimAllLeased releases every wake_jobs row currently in `leased`
// status back to `scheduled`, regardless of lease_expires_at — see
// Restart's doc comment for why an unconditional release is correct only
// at process-startup time (never during normal operation, where
// ReclaimExpired's expiry check remains the right, narrower behavior).
func (s *Store) reclaimAllLeased(ctx context.Context) (int, error) {
	now := formatTime(s.clock.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
		UPDATE wake_jobs
		SET status = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
		WHERE status = ?
	`, StatusScheduled, now, StatusLeased)
	if err != nil {
		return 0, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: restart reclaim leased jobs: %v", err), Retryable: false,
		}
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: restart reclaim rows affected: %v", err), Retryable: false,
		}
	}
	return int(affected), nil
}

// countOverdueScheduled reports how many `scheduled` rows have run_after
// already in the past — informational for the startup report (ADD §28.3
// step 10), computed after reclaimAllLeased so a just-recovered job that
// is also overdue is correctly counted.
func (s *Store) countOverdueScheduled(ctx context.Context) (int, error) {
	now := formatTime(s.clock.Now().UTC())
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM wake_jobs WHERE status = ? AND run_after <= ?
	`, StatusScheduled, now)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: count overdue scheduled jobs: %v", err), Retryable: false,
		}
	}
	return count, nil
}
