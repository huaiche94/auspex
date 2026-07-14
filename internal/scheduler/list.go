// list.go: read-only wake-job listing for the M6 daemon's status surfaces
// (#7): GET /v1/scheduler/jobs and /v1/status's per-status counts. Listing
// is deliberately separate from lease.go — it takes no lease, mutates
// nothing, and exists only so an operator/extension can SEE the queue
// (ADD §23.4), never to influence claim order.
package scheduler

import (
	"context"
	"fmt"
)

// ListLimit caps List's result set — the status surface is a live glance,
// not a pager; a queue anywhere near this size is itself the finding.
const ListLimit = 200

// List returns up to ListLimit wake jobs ordered soonest-due first, then
// by id for a stable order between equal run_after values.
func (s *Store) List(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pause_id, job_kind, status, run_after, lease_owner, lease_expires_at,
		       attempts, max_attempts, last_error, created_at, updated_at
		FROM wake_jobs
		ORDER BY run_after ASC, id ASC
		LIMIT ?
	`, ListLimit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: List: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scheduler: List: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: List: %w", err)
	}
	return jobs, nil
}
