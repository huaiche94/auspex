// worker.go: the resident scheduler worker loop — ADD §23.6, and the exact
// gap issue #7 names: durable wake jobs existed (scheduler.Store) and every
// pipeline stage existed (pause.Wake → Service.Resume's internal
// ValidateResume → Complete/Fail), but nothing long-lived ever drove them,
// so "auto-resume after the limit resets" was a promise with no process
// behind it. SchedulerRunOnceCmd (orchestrator/pauselifecycle.go)
// deliberately stopped at Claim, leaving the claimed job "for a later
// node's worker loop to actually process" — this file is that worker loop.
//
// §23.6's seven steps map onto Run as: (1) reconcile leased expired jobs —
// scheduler.Store.Restart once at startup plus ReclaimExpired each sweep;
// (2) find due jobs + (3) lease — scheduler.Store.Claim (one BEGIN
// IMMEDIATE transaction for both); (4) execute — pause.Wake then
// GracefulPauseService.Resume, which runs the REAL ValidateResume checklist
// internally (Constitution §7 rule 9: unattended resume is re-verified
// before it runs, never bypassed here); (5) heartbeat lease — Renew between
// the wake and resume stages; (6) complete / reschedule / dead-letter —
// Complete on success and on terminal outcomes that must not retry,
// Fail (backoff, then dead after MaxAttempts) on retryable ones;
// (7) emit event — Broker.Publish of the frozen pkg/protocol/v1 types.
package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
	protocol "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// DefaultPollInterval is how long the worker sleeps between sweeps when no
// job was due. wake_jobs.run_after has second-level intent (quota resets,
// §20.7 backoff steps of 15s+), so a 5s poll bounds wake latency well below
// the shortest backoff step without hammering SQLite.
const DefaultPollInterval = 5 * time.Second

// DefaultWorkerOwner identifies this daemon's lease claims — a durable
// worker identity, unlike run-once's per-invocation
// "cli-scheduler-run-once" (its doc comment draws exactly this contrast).
const DefaultWorkerOwner = "daemon-worker"

// WorkerDeps bundles the worker loop's collaborators. Jobs, Pause,
// PauseStore, and Clock are required; Run fails closed on a nil one
// (missing-dependency-is-a-composition-bug, the discipline every service
// in this repo follows). Events and IDs are optional — a worker without a
// broker still resumes pauses, it just tells nobody live.
type WorkerDeps struct {
	Jobs       *scheduler.Store
	Pause      app.GracefulPauseService
	PauseStore pause.PauseStore
	Clock      domain.Clock
	IDs        domain.IDGenerator
	Events     *Broker

	// Owner is the lease-owner identity (DefaultWorkerOwner when empty).
	Owner string
	// PollInterval is the idle sweep interval (DefaultPollInterval when 0).
	PollInterval time.Duration
	// LeaseDuration is each claim's lease (scheduler.DefaultLeaseDuration
	// when 0).
	LeaseDuration time.Duration
}

// Worker is the resident loop. Construct with NewWorker, drive with Run.
type Worker struct {
	deps WorkerDeps
}

// NewWorker validates nothing (per-call fail-closed happens in Run, like
// pause.NewService) and applies the documented defaults.
func NewWorker(deps WorkerDeps) *Worker {
	if deps.Owner == "" {
		deps.Owner = DefaultWorkerOwner
	}
	if deps.PollInterval == 0 {
		deps.PollInterval = DefaultPollInterval
	}
	if deps.LeaseDuration == 0 {
		deps.LeaseDuration = scheduler.DefaultLeaseDuration
	}
	return &Worker{deps: deps}
}

// Run executes the §23.6 loop until ctx is cancelled. Returns ctx.Err() on
// cancellation (the caller decides whether that is an error) or the first
// non-recoverable startup error (a failed Restart means the store itself is
// broken — running a worker against it would burn attempts on every job).
// Per-job errors never stop the loop: they are recorded on the job row
// itself (Fail → last_error, backoff, dead-letter), which is the §20.7
// audit surface, and the sweep moves on.
func (w *Worker) Run(ctx context.Context) error {
	if w.deps.Jobs == nil || w.deps.Pause == nil || w.deps.PauseStore == nil || w.deps.Clock == nil {
		return &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "daemon: Worker requires Jobs, Pause, PauseStore, and Clock", Retryable: false,
		}
	}
	if _, err := w.deps.Jobs.Restart(ctx); err != nil {
		if ctx.Err() != nil {
			// Cancelled mid-startup: shutdown, not a broken store.
			return ctx.Err()
		}
		return err
	}

	ticker := time.NewTicker(w.deps.PollInterval)
	defer ticker.Stop()
	for {
		w.sweep(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// sweep drains every currently-due job, then returns. Claim errors end the
// sweep early (the next tick retries); per-job execution errors are
// absorbed into that job's own row and never end the sweep.
func (w *Worker) sweep(ctx context.Context) {
	// Step 1 (recurring half): a lease left behind by a crashed PEER worker
	// (ours are recovered by Restart at startup) becomes claimable again.
	_, _ = w.deps.Jobs.ReclaimExpired(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		claim, err := w.deps.Jobs.Claim(ctx, w.deps.Owner, w.deps.LeaseDuration)
		if err != nil || !claim.Found {
			return
		}
		w.execute(ctx, claim.Job)
	}
}

// execute drives one claimed job through wake → validated resume →
// complete/reschedule. Outcome mapping (§23.6 step 6):
//
//   - Resume succeeds, record Resumed        → Complete (job done)
//   - verdict QuotaUnsafe, record → Sleeping → Fail (backoff reschedule —
//     quota recovering is expected; ADD §20.7)
//   - verdict Conflict, record → BlockedConflict → Complete (ADD §20.9:
//     conflicts resolve manually, an automatic retry can never clear one)
//   - pause moved on first (cancel won the race, duplicate wake) →
//     Complete (exactly-once: the job's purpose no longer exists)
//   - infrastructure error (store down, context missing…) → Fail (retry
//     with backoff; dead-letter after MaxAttempts is the honest terminal
//     state for a job that can never execute)
func (w *Worker) execute(ctx context.Context, job scheduler.Job) {
	w.publish(protocol.EventPauseWakeTriggered, job, nil)

	if _, err := pause.Wake(ctx, w.deps.PauseStore, pause.WakeRequest{PauseID: job.PauseID}); err != nil {
		var transition *pause.TransitionError
		if errors.As(err, &transition) {
			// The pause is no longer Sleeping — cancelled, or a duplicate
			// wake already advanced it. This job has nothing left to do.
			_, _ = w.deps.Jobs.Complete(ctx, job.ID, w.deps.Owner)
			return
		}
		_, _ = w.deps.Jobs.Fail(ctx, job.ID, w.deps.Owner, "wake: "+err.Error())
		return
	}

	// Step 5: heartbeat before the potentially-slow validation stage (its
	// checks call out to quota/repo/session/evaluation readers).
	_, _ = w.deps.Jobs.Renew(ctx, job.ID, w.deps.Owner, w.deps.LeaseDuration)

	w.publish(protocol.EventPauseResumeValidationStarted, job, nil)
	result, err := w.deps.Pause.Resume(ctx, app.ResumeRequest{PauseID: job.PauseID})
	if err != nil {
		var transition *pause.TransitionError
		if errors.As(err, &transition) {
			_, _ = w.deps.Jobs.Complete(ctx, job.ID, w.deps.Owner)
			return
		}
		_, _ = w.deps.Jobs.Fail(ctx, job.ID, w.deps.Owner, "resume: "+err.Error())
		return
	}

	switch result.Status {
	case domain.PauseResumed:
		_, _ = w.deps.Jobs.Complete(ctx, job.ID, w.deps.Owner)
		w.publish(protocol.EventPauseResumeCompleted, job, map[string]any{"status": string(result.Status)})
	case domain.PauseSleeping:
		// Quota-unsafe verdict: the record went back to Sleeping;
		// reschedule THIS job with the scheduler's own backoff so the
		// sleep actually ends again later. (Service.Resume's internal
		// reschedule attempt uses its own lease identity and yields to
		// ours — we hold the lease, so the Fail here is the one that
		// lands.)
		_, _ = w.deps.Jobs.Fail(ctx, job.ID, w.deps.Owner, "resume validation: quota still unsafe")
		w.publish(protocol.EventPauseResumeBlocked, job, map[string]any{"status": string(result.Status), "reason": "quota_unsafe"})
	case domain.PauseBlockedConflict:
		_, _ = w.deps.Jobs.Complete(ctx, job.ID, w.deps.Owner)
		w.publish(protocol.EventPauseResumeBlocked, job, map[string]any{"status": string(result.Status), "reason": "conflict"})
	default:
		// A status this worker does not recognize: retry-with-backoff is
		// the safe default (never silently complete a job whose outcome
		// is unclear).
		_, _ = w.deps.Jobs.Fail(ctx, job.ID, w.deps.Owner, "resume: unexpected status "+string(result.Status))
	}
}

// publish emits a protocol v1 event to the broker, if one is wired.
func (w *Worker) publish(eventType protocol.EventType, job scheduler.Job, payload map[string]any) {
	if w.deps.Events == nil {
		return
	}
	now := w.deps.Clock.Now()
	id := ""
	if w.deps.IDs != nil {
		id = w.deps.IDs.NewID()
	}
	w.deps.Events.Publish(protocol.Event{
		SchemaVersion: protocol.SchemaVersionEvent,
		EventID:       id,
		EventType:     eventType,
		OccurredAt:    now,
		ObservedAt:    now,
		Source:        "daemon",
		Payload: mergePayload(map[string]any{
			"pause_id":    string(job.PauseID),
			"wake_job_id": string(job.ID),
			"attempts":    job.Attempts,
		}, payload),
	})
}

func mergePayload(base, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}
