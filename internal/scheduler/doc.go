// Package scheduler implements Auspex's durable wake scheduler (ADD
// §12.4, §20.7; agents/runtime.md Part A P0 deliverable 6: "Durable
// scheduler lease with claim/renew/complete/fail/retry"). It operates on
// the wake_jobs table runtime-a01 shipped
// (internal/storage/sqlite/migrations/0051_wake_jobs.sql) and implements
// exactly the lease-claim transaction shape ADD §12.4 specifies:
//
//	BEGIN IMMEDIATE;
//	SELECT id FROM wake_jobs
//	  WHERE status = 'scheduled' AND run_after <= :now
//	    AND (lease_expires_at IS NULL OR lease_expires_at < :now)
//	  ORDER BY run_after LIMIT 1;
//	UPDATE wake_jobs SET status='leased', lease_owner=:owner,
//	  lease_expires_at=:lease_until, attempts=attempts+1, updated_at=:now
//	  WHERE id = :id;
//	COMMIT;
//
// This node (runtime-a06) is a pure storage/concurrency-correctness layer:
// it does not know what a wake job's payload means (that is Part A's
// persist-phase orchestration, runtime-a05/a07+) — it only knows how to
// hand exactly one worker a due, unleased job at a time, durably, and
// safely under concurrent claimers (the DAG's stated risk: "lease
// correctness under concurrent workers is the whole point").
//
// # Lease lifecycle
//
// A wake_jobs row moves through the following statuses under this
// package's control: `scheduled` (not yet claimed, or reclaimed after
// lease expiry/failure) -> `leased` (claimed, owned, with an expiry) ->
// terminal `done` (Complete) or back to `scheduled` (Fail, if attempts
// remain) or terminal `dead` (Fail, once attempts are exhausted). These
// are this package's own status vocabulary for the wake_jobs.status
// column — distinct from, and not to be confused with, domain.PauseStatus
// (internal/pause's concern). No migration change is needed: 0051's
// `status TEXT NOT NULL` column was deliberately left un-enumerated at the
// schema level so the owning role (this one) defines the value set.
package scheduler
