package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
)

// Job statuses — this package's own vocabulary for wake_jobs.status (see
// doc.go). Not part of any frozen domain enum.
const (
	StatusScheduled = "scheduled"
	StatusLeased    = "leased"
	StatusDone      = "done"
	StatusDead      = "dead"
)

// DefaultLeaseDuration is ADD §20.7's default lease duration.
const DefaultLeaseDuration = 60 * time.Second

// RetryBackoff is ADD §20.7's fixed exponential retry schedule:
// 15s, 30s, 60s, 5m. Fail() uses backoff[min(attempts, len(backoff)-1)] so
// attempts beyond the table's length keep reusing the longest interval
// rather than indexing out of range or growing unbounded.
var RetryBackoff = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
	5 * time.Minute,
}

// Job is the caller-facing view of a claimed wake_jobs row.
type Job struct {
	ID           domain.WakeJobID
	PauseID      domain.PauseID
	Kind         string
	Status       string
	RunAfter     time.Time
	LeaseOwner   *string
	LeaseExpires *time.Time
	Attempts     int
	MaxAttempts  int
	LastError    *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Querier is satisfied by both *sql.DB and *sql.Tx, mirroring
// internal/storage/sqlite.Querier — duplicated here (rather than importing
// the sqlite package's interface type) so this package's exported API
// depends only on database/sql, not on a specific storage package's
// internal type identity. Any *sql.DB/*sql.Tx satisfies both.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ConnBeginner is satisfied by *sql.DB: it can reserve a single physical
// connection. Claim needs BEGIN IMMEDIATE specifically (ADD §12.4) — a
// plain *sql.Tx via BeginTx uses SQLite's default deferred BEGIN
// (internal/storage/sqlite's DSN sets no _txlock override), which would
// let two concurrent claimers both pass the SELECT before either UPDATEs,
// re-introducing the double-claim race this node exists to prevent.
// database/sql's *sql.Tx also does not expose a portable way to request
// SQLite's non-standard "BEGIN IMMEDIATE" syntax through TxOptions. So
// Claim instead reserves a single *sql.Conn (a pinned physical connection,
// guaranteed not shared with any other caller for its lifetime) and issues
// "BEGIN IMMEDIATE"/"COMMIT"/"ROLLBACK" as plain statements on it directly
// — the same approach ADD §12.4's literal SQL block describes, and
// independent of internal/app.TxRunner's generic WithTx (which is built
// for ordinary deferred transactions, not this lease-specific locking
// mode).
type ConnBeginner interface {
	Conn(ctx context.Context) (*sql.Conn, error)
}

// DB is the minimal handle Store needs: something that can both run plain
// queries (Schedule, Get) and reserve a connection for BEGIN IMMEDIATE
// (Claim).
type DB interface {
	Querier
	ConnBeginner
}

// Clock abstracts "now" so tests can control lease expiry and run_after
// comparisons deterministically, per internal/domain.Clock's existing
// convention.
type Clock = domain.Clock

// IDGenerator abstracts ID generation, per internal/domain.IDGenerator's
// existing convention.
type IDGenerator = domain.IDGenerator

// Store is the durable scheduler lease store, backed by the wake_jobs
// table (runtime-a01's migration 0051). All methods are safe for
// concurrent use across goroutines and OS processes sharing the same
// SQLite file (WAL mode + busy_timeout, per internal/storage/sqlite).
type Store struct {
	db    DB
	clock Clock
	ids   IDGenerator
}

// NewStore constructs a Store. db is typically a *sqlite.DB's Conn()
// (*sql.DB) — Store deliberately depends on the database/sql-shaped DB
// interface above, not on internal/storage/sqlite.DB directly, so it has
// no import-time dependency on that package's concrete type (only on
// whatever satisfies Querier+TxBeginner, which *sql.DB already does).
func NewStore(db DB, clock Clock, ids IDGenerator) *Store {
	return &Store{db: db, clock: clock, ids: ids}
}

// ScheduleRequest describes a new wake job to durably persist.
type ScheduleRequest struct {
	PauseID     domain.PauseID
	Kind        string
	RunAfter    time.Time
	MaxAttempts int
}

// Schedule inserts a new wake_jobs row in `scheduled` status. Per the
// schema's UNIQUE(pause_id, job_kind) constraint (0051_wake_jobs.sql), a
// duplicate (PauseID, Kind) pair is rejected as *domain.Error with
// ErrCodeConflict — this is the durable, storage-level half of "duplicate
// wake exactly-once behavior" (agents/runtime.md P0 deliverable 9): a
// second scheduling attempt for a pause/kind that already has one active
// row cannot silently create a second job.
func (s *Store) Schedule(ctx context.Context, req ScheduleRequest) (Job, error) {
	if req.PauseID == "" {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "scheduler: Schedule requires a PauseID", Retryable: false,
		}
	}
	if req.Kind == "" {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "scheduler: Schedule requires a Kind", Retryable: false,
		}
	}
	if req.MaxAttempts <= 0 {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "scheduler: Schedule requires MaxAttempts > 0", Retryable: false,
		}
	}

	now := s.clock.Now().UTC()
	id := domain.WakeJobID(s.ids.NewID())

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, attempts, max_attempts, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)
	`, string(id), string(req.PauseID), req.Kind, StatusScheduled, formatTime(req.RunAfter), req.MaxAttempts, formatTime(now), formatTime(now))
	if err != nil {
		if isUniqueConstraintError(err) {
			return Job{}, &domain.Error{
				Code:      domain.ErrCodeConflict,
				Message:   fmt.Sprintf("scheduler: a wake job of kind %q already exists for pause %q", req.Kind, req.PauseID),
				Retryable: false,
				Details:   map[string]string{"pause_id": string(req.PauseID), "job_kind": req.Kind},
			}
		}
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: insert wake job: %v", err), Retryable: false,
		}
	}

	return s.Get(ctx, id)
}

// Get loads a single wake job by ID.
func (s *Store) Get(ctx context.Context, id domain.WakeJobID) (Job, error) {
	return getJob(ctx, s.db, id)
}

// getJob is Get's implementation, parameterized over the Querier so Claim
// can reuse it against its own reserved *sql.Conn instead of going back to
// the pooled s.db — reusing s.db here would ask database/sql's pool for a
// SECOND connection while Claim is still holding its first one for the
// transaction's duration, and under full pool saturation (many concurrent
// Claim callers, internal/storage/sqlite.DB caps at 8) that second
// acquisition can never be satisfied by any of the (all busy) existing
// connections — a real, reproducible self-deadlock this node's own
// concurrency test caught (TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce
// hanging every goroutine in database/sql's connection-wait queue).
func getJob(ctx context.Context, q Querier, id domain.WakeJobID) (Job, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, pause_id, job_kind, status, run_after, lease_owner, lease_expires_at,
		       attempts, max_attempts, last_error, created_at, updated_at
		FROM wake_jobs WHERE id = ?
	`, string(id))
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeNotFound, Message: fmt.Sprintf("scheduler: wake job %q not found", id), Retryable: false,
		}
	}
	if err != nil {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: scan wake job: %v", err), Retryable: false,
		}
	}
	return job, nil
}

// ClaimResult is Claim's return value: either a job was claimed (Found
// true) or no due, unleased job existed (Found false) — the latter is not
// an error, just an empty sweep.
type ClaimResult struct {
	Job   Job
	Found bool
}

// Claim atomically finds and leases the single oldest due, unleased wake
// job (ADD §12.4's exact query shape), or reclaims one whose lease has
// expired. owner identifies the claiming worker (opaque string — a
// process/goroutine identity for diagnostics and lease debugging, not
// interpreted by this package). leaseDuration is how long the claim is
// held before it is eligible for reclaim by another worker if this one
// never calls Complete/Fail/Renew.
//
// Concurrency correctness (the DAG's stated risk): the SELECT and UPDATE
// run inside one BEGIN IMMEDIATE transaction. BEGIN IMMEDIATE acquires
// SQLite's write lock up front (rather than the default deferred/optimistic
// upgrade), so two goroutines/processes racing Claim serialize on that
// lock — the loser's transaction blocks until the winner commits, then
// re-reads a wake_jobs table that no longer has the row in a
// claimable state, so it correctly finds nothing (or the next job in
// run_after order). This is proven directly by
// TestLease_ConcurrentWorkersYieldOneClaim (-race, many goroutines, one
// job).
func (s *Store) Claim(ctx context.Context, owner string, leaseDuration time.Duration) (ClaimResult, error) {
	if owner == "" {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "scheduler: Claim requires a non-empty owner", Retryable: false,
		}
	}
	if leaseDuration <= 0 {
		leaseDuration = DefaultLeaseDuration
	}

	// Reserve a single physical connection for the duration of this
	// claim attempt. This is required (not just a nicety): "BEGIN
	// IMMEDIATE"/"COMMIT" issued as plain statements only serialize
	// correctly with each other if they run on the SAME underlying
	// SQLite connection — database/sql's *sql.DB is a pool, and issuing
	// unpaired BEGIN/COMMIT statements through it directly could let the
	// pool hand each statement to a different physical connection,
	// silently losing the transaction boundary entirely. *sql.Conn pins
	// one connection to this goroutine until Close.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: fmt.Sprintf("scheduler: reserve connection: %v", err), Retryable: true,
		}
	}
	defer func() { _ = conn.Close() }()

	// ADD §12.4's literal transaction shape: BEGIN IMMEDIATE acquires
	// SQLite's write lock up front (rather than the default deferred/
	// optimistic upgrade a plain BEGIN would use), so a second
	// concurrent Claim on another connection blocks here — inside
	// SQLite's own lock wait, honoring internal/storage/sqlite's
	// busy_timeout pragma — until this one commits or rolls back, then
	// re-reads a wake_jobs table that no longer has the row in a
	// claimable state. This is what makes concurrent Claim callers
	// serialize correctly instead of racing; proven directly by
	// TestLease_ConcurrentWorkersYieldOneClaim (-race, many goroutines,
	// one job).
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: fmt.Sprintf("scheduler: BEGIN IMMEDIATE: %v", err), Retryable: true,
		}
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	now := s.clock.Now().UTC()
	nowStr := formatTime(now)

	// Matches ADD §12.4's query concept (status='scheduled' AND due AND
	// unleased) but is deliberately widened to ALSO match a `leased` row
	// whose lease has expired: ADD §12.4 labels its SQL block a concept
	// ("概念"), and a lease that is claimable only after a separate
	// ReclaimExpired sweep first flips it back to `scheduled` would leave
	// a window where an expired lease is inert until something else
	// happens to run that sweep — Part A required test "expired lease
	// reclaimed" (agents/runtime.md) is proven directly against Claim
	// here (TestLease_ExpiredLeaseReclaimedByAnotherWorker), independent
	// of whether ReclaimExpired has run. ReclaimExpired (below) remains
	// the explicit, no-Claim-required restart-recovery sweep ADD §28.3
	// step 2 describes, for observability/diagnostics ahead of any
	// worker actually claiming.
	row := conn.QueryRowContext(ctx, `
		SELECT id, pause_id, job_kind, status, run_after, lease_owner, lease_expires_at,
		       attempts, max_attempts, last_error, created_at, updated_at
		FROM wake_jobs
		WHERE run_after <= ?
		  AND (
		    (status = ? AND lease_expires_at IS NULL)
		    OR (status IN (?, ?) AND lease_expires_at IS NOT NULL AND lease_expires_at < ?)
		  )
		ORDER BY run_after
		LIMIT 1
	`, nowStr, StatusScheduled, StatusScheduled, StatusLeased, nowStr)

	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, cerr := conn.ExecContext(ctx, `COMMIT`); cerr != nil {
			return ClaimResult{}, &domain.Error{
				Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: commit empty claim sweep: %v", cerr), Retryable: false,
			}
		}
		committed = true
		return ClaimResult{Found: false}, nil
	}
	if err != nil {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: scan claimable job: %v", err), Retryable: false,
		}
	}

	leaseUntil := now.Add(leaseDuration)
	leaseUntilStr := formatTime(leaseUntil)

	// The WHERE clause re-checks the same claimability condition (not
	// just "id matches") so a concurrent winner that already claimed this
	// row between our SELECT and UPDATE — impossible under BEGIN
	// IMMEDIATE's serialization within this package, but cheap
	// insurance against a future caller bypassing Claim's transaction —
	// cannot be silently overwritten.
	res, err := conn.ExecContext(ctx, `
		UPDATE wake_jobs
		SET status = ?, lease_owner = ?, lease_expires_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE id = ?
		  AND (
		    (status = ? AND lease_expires_at IS NULL)
		    OR (status IN (?, ?) AND lease_expires_at IS NOT NULL AND lease_expires_at < ?)
		  )
	`, StatusLeased, owner, leaseUntilStr, nowStr, string(job.ID),
		StatusScheduled, StatusScheduled, StatusLeased, nowStr)
	if err != nil {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: lease claim update: %v", err), Retryable: false,
		}
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: lease claim rows affected: %v", err), Retryable: false,
		}
	}
	if affected == 0 {
		// Lost the race between SELECT and UPDATE despite BEGIN
		// IMMEDIATE — should be unreachable under correct SQLite
		// serialization, but fail closed rather than report a false
		// claim if it ever happens (e.g. a future driver change).
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeConflict, Message: "scheduler: lost claim race despite BEGIN IMMEDIATE", Retryable: true,
		}
	}

	// Re-fetch the claimed row's full state through the SAME reserved
	// connection, still inside the transaction (before COMMIT) — using
	// s.db (the pooled *sql.DB) here instead would request a second
	// connection from the pool while this one is still held, which is
	// exactly the self-deadlock getJob's doc comment describes.
	claimed, err := getJob(ctx, conn, job.ID)
	if err != nil {
		return ClaimResult{}, err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return ClaimResult{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: commit claim: %v", err), Retryable: false,
		}
	}
	committed = true

	return ClaimResult{Job: claimed, Found: true}, nil
}

// Renew extends an already-held lease. Fails with ErrCodeConflict if owner
// no longer holds the lease (already reclaimed, completed, or failed by
// someone else) — a worker must not be able to renew a lease it lost.
func (s *Store) Renew(ctx context.Context, id domain.WakeJobID, owner string, leaseDuration time.Duration) (Job, error) {
	if leaseDuration <= 0 {
		leaseDuration = DefaultLeaseDuration
	}
	now := s.clock.Now().UTC()
	leaseUntil := formatTime(now.Add(leaseDuration))

	res, err := s.db.ExecContext(ctx, `
		UPDATE wake_jobs
		SET lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_owner = ?
	`, leaseUntil, formatTime(now), string(id), StatusLeased, owner)
	if err != nil {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: renew lease: %v", err), Retryable: false,
		}
	}
	if err := requireOneRowAffected(res, id, owner, "renew"); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

// Complete marks a leased job done. Fails with ErrCodeConflict if owner no
// longer holds the lease.
func (s *Store) Complete(ctx context.Context, id domain.WakeJobID, owner string) (Job, error) {
	now := formatTime(s.clock.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
		UPDATE wake_jobs
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_owner = ?
	`, StatusDone, now, string(id), StatusLeased, owner)
	if err != nil {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: complete: %v", err), Retryable: false,
		}
	}
	if err := requireOneRowAffected(res, id, owner, "complete"); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

// Fail marks a leased job's attempt as failed. If the job's attempts count
// has reached MaxAttempts, it transitions to the terminal `dead` status;
// otherwise it is rescheduled back to `scheduled` with run_after advanced
// by RetryBackoff[attempts-1] (ADD §20.7's 15s/30s/60s/5m schedule) and its
// lease released, so a future Claim (by this worker or another) can pick
// it up again. Fails with ErrCodeConflict if owner no longer holds the
// lease.
func (s *Store) Fail(ctx context.Context, id domain.WakeJobID, owner string, failureReason string) (Job, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if current.Status != StatusLeased || current.LeaseOwner == nil || *current.LeaseOwner != owner {
		return Job{}, leaseConflictError(id, owner, "fail")
	}

	now := s.clock.Now().UTC()

	if current.Attempts >= current.MaxAttempts {
		res, err := s.db.ExecContext(ctx, `
			UPDATE wake_jobs
			SET status = ?, last_error = ?, updated_at = ?
			WHERE id = ? AND status = ? AND lease_owner = ?
		`, StatusDead, failureReason, formatTime(now), string(id), StatusLeased, owner)
		if err != nil {
			return Job{}, &domain.Error{
				Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: fail (dead): %v", err), Retryable: false,
			}
		}
		if err := requireOneRowAffected(res, id, owner, "fail"); err != nil {
			return Job{}, err
		}
		return s.Get(ctx, id)
	}

	backoff := RetryBackoff[len(RetryBackoff)-1]
	if idx := current.Attempts - 1; idx >= 0 && idx < len(RetryBackoff) {
		backoff = RetryBackoff[idx]
	}
	nextRunAfter := now.Add(backoff)

	res, err := s.db.ExecContext(ctx, `
		UPDATE wake_jobs
		SET status = ?, run_after = ?, lease_owner = NULL, lease_expires_at = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_owner = ?
	`, StatusScheduled, formatTime(nextRunAfter), failureReason, formatTime(now), string(id), StatusLeased, owner)
	if err != nil {
		return Job{}, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: fail (retry): %v", err), Retryable: false,
		}
	}
	if err := requireOneRowAffected(res, id, owner, "fail"); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

// ReclaimExpired resets every job whose lease has expired (lease_owner
// still set, lease_expires_at in the past, status still `leased`) back to
// `scheduled`, releasing the stale lease so a Claim can pick it up again.
// This is the restart-recovery half of "expired lease reclaimed" — daemon
// startup (ADD §28.3 step 2: "release expired scheduler leases") calls
// this once before resuming normal Claim sweeps. Returns the number of
// jobs reclaimed.
func (s *Store) ReclaimExpired(ctx context.Context) (int, error) {
	now := formatTime(s.clock.Now().UTC())
	res, err := s.db.ExecContext(ctx, `
		UPDATE wake_jobs
		SET status = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
		WHERE status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?
	`, StatusScheduled, now, StatusLeased, now)
	if err != nil {
		return 0, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: reclaim expired leases: %v", err), Retryable: false,
		}
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: reclaim rows affected: %v", err), Retryable: false,
		}
	}
	return int(affected), nil
}

func requireOneRowAffected(res sql.Result, id domain.WakeJobID, owner, verb string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return &domain.Error{
			Code: domain.ErrCodeIntegrity, Message: fmt.Sprintf("scheduler: %s rows affected: %v", verb, err), Retryable: false,
		}
	}
	if affected == 0 {
		return leaseConflictError(id, owner, verb)
	}
	return nil
}

func leaseConflictError(id domain.WakeJobID, owner, verb string) error {
	return &domain.Error{
		Code:      domain.ErrCodeConflict,
		Message:   fmt.Sprintf("scheduler: %s: job %q is not leased by owner %q (already reclaimed, completed, or failed by another worker)", verb, id, owner),
		Retryable: false,
		Details:   map[string]string{"wake_job_id": string(id), "owner": owner},
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var (
		id, pauseID, kind, status, runAfter, createdAt, updatedAt string
		leaseOwner, leaseExpiresAt, lastError                     sql.NullString
		attempts, maxAttempts                                     int
	)
	if err := row.Scan(&id, &pauseID, &kind, &status, &runAfter, &leaseOwner, &leaseExpiresAt,
		&attempts, &maxAttempts, &lastError, &createdAt, &updatedAt); err != nil {
		return Job{}, err
	}

	job := Job{
		ID:          domain.WakeJobID(id),
		PauseID:     domain.PauseID(pauseID),
		Kind:        kind,
		Status:      status,
		Attempts:    attempts,
		MaxAttempts: maxAttempts,
	}
	if t, err := parseTime(runAfter); err == nil {
		job.RunAfter = t
	}
	if t, err := parseTime(createdAt); err == nil {
		job.CreatedAt = t
	}
	if t, err := parseTime(updatedAt); err == nil {
		job.UpdatedAt = t
	}
	if leaseOwner.Valid {
		v := leaseOwner.String
		job.LeaseOwner = &v
	}
	if leaseExpiresAt.Valid {
		if t, err := parseTime(leaseExpiresAt.String); err == nil {
			job.LeaseExpires = &t
		}
	}
	if lastError.Valid {
		v := lastError.String
		job.LastError = &v
	}
	return job, nil
}

const timeLayout = time.RFC3339Nano

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}

// isUniqueConstraintError reports whether err is a SQLite UNIQUE
// constraint violation, matched by message substring since
// modernc.org/sqlite's internal error-code type is not exported at the
// package this file depends on (mirrors internal/storage/sqlite.isBusyError's
// same documented tradeoff for SQLITE_BUSY).
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: UNIQUE")
}
