// persistphase.go: the Phase-3 persist orchestrator (agents/runtime.md
// Part A deliverable 5 / EXECUTION_DAG.md runtime-a05 — "Second highest-risk
// task in the whole DAG": "orchestrates five durable writes across
// `checkpoint` Part A and Part B stores inside one logical operation."
//
// CONTRACT_FREEZE.md's "Transaction boundaries" section is the frozen
// authority this file implements against, verbatim:
//
//	"GracefulPauseService's persist phase (Progress Tree snapshot -> State
//	Checkpoint -> Repository Checkpoint -> Pause Record -> Wake Job) is a
//	sequence of dependent writes, not one flat transaction (it spans the
//	checkpoint role's two parts) -- each step's own transaction boundary is
//	defined by that step's owning service; runtime is responsible for
//	sequencing them and handling partial-sequence failure as a resumable
//	state, not a silent gap."
//
// That sentence is this file's entire design brief. There is deliberately
// NO internal/app.TxRunner.WithTx wrapping all five steps — Progress Tree,
// State Checkpoint, and Repository Checkpoint are each a different owning
// service with its own atomic boundary already (checkpoint-a04's
// CompleteNode transaction, checkpoint-a05's manifest write,
// checkpoint-b04's capture+insert); wrapping a SECOND, wider transaction
// around three independent services' own commits is not just unnecessary,
// it is impossible without a distributed-transaction protocol this project
// does not have. So PersistPhase's job is exactly what CONTRACT_FREEZE.md
// says: sequence five independently-durable steps in a fixed order, and
// make a crash after ANY one of them resumable without loss or
// duplication.
//
// # Why this mirrors runtime-a04's safepoint.go and runtime-b05's
// # orchestrator.CheckpointCreate exactly
//
// Both existing precedents establish the same shape this file scales up to
// five steps: validate all dependencies up front (fail closed, never a nil
// pointer panic later), call step N, return immediately on step N's error
// without attempting step N+1 (so a partial sequence is always "the first
// K steps genuinely succeeded, then stopped" — never "some later step ran
// despite an earlier one failing"). PersistPhase adds exactly one new
// concept beyond that: since there are five dependent steps instead of two,
// and a real process crash (not just a returned error) can occur BETWEEN
// two steps that each individually already committed durably, this file
// also needs a durable, restart-safe record of "how far did the last
// attempt get" -- that is PhaseProgress (recorded on PauseRecord, this
// package's own store) plus per-step idempotent-skip logic (Resume,
// below). HaltAfter/HaltError below is the literal crash-injection
// technique named in the task brief, transplanted from
// internal/progress/complete_node.go's own Phase/HaltError pattern
// (checkpoint-a04's precedent) rather than reinvented.
//
// # Idempotent-skip, not a second transaction
//
// Each of the five steps is independently idempotent-safe to retry (this is
// what makes "crash after every phase resumes/reconciles correctly"
// provable without a distributed transaction):
//
//  1. Progress Tree snapshot: a pure read (app.ProgressTreeService.Snapshot)
//     -- re-running it after a crash is always safe, it has no side effect
//     to duplicate. Recorded on the PauseRecord only so callers/diagnostics
//     can see a snapshot was taken; PersistPhase does not gate later steps
//     on this value's content, only on whether the call itself succeeded.
//  2. State Checkpoint: PersistPhase checks
//     PauseRecord.StateCheckpointID first -- if already set (a prior
//     attempt's Create already committed before the crash), it is NOT
//     called again; the existing ID is reused. This is the same "idempotent
//     by skip" discipline CONTRACT_FREEZE.md's CompleteNodeRequest section
//     describes at the checkpoint layer, applied here at the orchestration
//     layer since app.StateCheckpointService.Create itself has no
//     caller-supplied idempotency key in its frozen request shape
//     (CreateStateCheckpointRequest has only a TaskID).
//  3. Repository Checkpoint: identical skip-if-already-recorded logic,
//     keyed on PauseRecord.RepositoryCheckpointID.
//  4. Pause Record: this package's own store -- Upsert is naturally
//     idempotent, since it is a single durable UPDATE of one row's phase
//     markers (see PauseStore.UpdatePersistProgress below), not an insert
//     that could duplicate.
//  5. Wake Job: internal/scheduler.Store.Schedule already enforces
//     UNIQUE(pause_id, job_kind) -- a retried Schedule call after a crash
//     that already committed the row hits that constraint and returns
//     ErrCodeConflict, which PersistPhase treats as "already scheduled,
//     fetch the existing job" rather than a real failure (this is the
//     schema-level exactly-once anchor runtime-a06's own doc comment
//     names, reused here rather than reimplemented).
package pause

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/scheduler"
)

// PersistPhase names the five durable writes, in the fixed order
// CONTRACT_FREEZE.md freezes. Exported (unlike pause's internal Event/
// Boundary vocabularies) because a caller building a restart-time
// reconciliation report or a diagnostics surface needs to name which phase
// a resumed pause stopped at, not just this package's private bookkeeping.
type PersistPhase string

const (
	PhaseProgressSnapshot     PersistPhase = "progress_snapshot"
	PhaseStateCheckpoint      PersistPhase = "state_checkpoint"
	PhaseRepositoryCheckpoint PersistPhase = "repository_checkpoint"
	PhasePauseRecord          PersistPhase = "pause_record"
	PhaseWakeJob              PersistPhase = "wake_job"
)

// persistPhaseOrder is the fixed sequence, used both to drive Persist's
// linear execution and by tests asserting the full order is exactly this.
var persistPhaseOrder = []PersistPhase{
	PhaseProgressSnapshot,
	PhaseStateCheckpoint,
	PhaseRepositoryCheckpoint,
	PhasePauseRecord,
	PhaseWakeJob,
}

// HaltError is returned by Persist when HaltAfter caused an intentional
// mid-protocol stop, simulating a process crash immediately after the named
// phase's write committed (or, for a phase that itself failed, immediately
// after that failure was observed -- see Persist's doc comment for exactly
// which case each required crash test exercises). Mirrors
// internal/progress.HaltError's exact shape and purpose (checkpoint-a04's
// precedent), so a crash-injection test here reads the same way that
// package's own tests do.
type HaltError struct {
	Phase PersistPhase
}

func (e *HaltError) Error() string {
	return fmt.Sprintf("pause: PersistPhase halted after phase %q (fault injection)", e.Phase)
}

// PersistPauseStore is the narrow slice of durable pause-record state
// Persist needs: reading the current phase-progress markers and durably
// recording each step's result as it completes. Declared here (not
// internal/app/ports.go) for the same reason PauseStore is
// (requestpause.go's doc comment): an internal seam behind the already-
// frozen GracefulPauseService boundary, not a new cross-component contract.
type PersistPauseStore interface {
	// GetProgress returns the pause record's current phase-progress
	// markers. found is false if id does not exist at all (a caller error
	// -- Persist requires an already-Requested pause record to exist
	// before persisting against it).
	GetProgress(ctx context.Context, id domain.PauseID) (PersistProgress, bool, error)
	// SaveProgress durably records progress after a step succeeds. Called
	// once per successful step (never batched), so a crash immediately
	// after this call still leaves exactly one step's worth of newly
	// durable state, not a partial write of several steps at once.
	SaveProgress(ctx context.Context, id domain.PauseID, progress PersistProgress) error
}

// PersistProgress is the durable, resumable record of how far a given
// pause's persist phase has gotten. Every field is set exactly once, the
// first time its corresponding step succeeds, and never cleared -- a
// resumed attempt reads this back to decide which steps to skip (already
// durable, per the idempotent-skip discipline in this file's package
// comment) versus which to actually run.
type PersistProgress struct {
	ProgressSnapshotTaken  bool
	StateCheckpointID      *domain.StateCheckpointID
	RepositoryCheckpointID *domain.RepositoryCheckpointID
	PauseRecordSaved       bool
	WakeJobID              *domain.WakeJobID
}

// completedPhase reports the last phase in persistPhaseOrder that p already
// has durable evidence for, or "" if none. Used only for diagnostics/tests
// (Persist itself checks each field independently rather than jumping to a
// single resume point, since two steps could in principle be recorded out
// of the check order a future refactor might introduce -- checking each
// field explicitly is more defensive than trusting a derived cursor).
func (p PersistProgress) completedPhase() PersistPhase {
	last := PersistPhase("")
	for _, phase := range persistPhaseOrder {
		switch phase {
		case PhaseProgressSnapshot:
			if p.ProgressSnapshotTaken {
				last = phase
			}
		case PhaseStateCheckpoint:
			if p.StateCheckpointID != nil {
				last = phase
			}
		case PhaseRepositoryCheckpoint:
			if p.RepositoryCheckpointID != nil {
				last = phase
			}
		case PhasePauseRecord:
			if p.PauseRecordSaved {
				last = phase
			}
		case PhaseWakeJob:
			if p.WakeJobID != nil {
				last = phase
			}
		}
	}
	return last
}

// PersistDeps bundles Persist's five collaborators. Every field is
// required; Persist fails closed (ErrCodeUnavailable) if any is nil,
// mirroring orchestrator.CheckpointCreateDeps' own up-front nil validation
// exactly -- a missing dependency is a composition bug, never silently
// skipped.
type PersistDeps struct {
	// ProgressTree is checkpoint's frozen Progress Tree service
	// (internal/app/ports.go). No dedicated "snapshot only" port exists at
	// this layer -- ProgressTreeService.Snapshot is the one already-frozen
	// method that returns exactly what a Phase-3 snapshot needs
	// (app.ProgressTreeSnapshot), so this deliberately reuses it rather
	// than inventing a narrower one (Constitution Sec 7 rule 10: no
	// speculative new abstractions). checkpoint-a04's real Progress Tree
	// side is integrated this wave, but Snapshot is used here as a FAKE
	// this wave per the task brief's explicit instruction -- see this
	// package's persistphase_test.go and docs/implementation/day1/
	// runtime.md's Wave 7 section for why (no dedicated frozen service
	// port beyond the general ProgressTreeService exists specifically for
	// this call site, and the task brief names it fake-able regardless).
	ProgressTree app.ProgressTreeService
	// StateCheckpoint is checkpoint-a05's frozen port. FAKE this wave --
	// checkpoint-a05's real implementation is a sibling teammate's
	// concurrent, not-yet-mergeable work this same wave (task brief,
	// verbatim). internal/testutil/fakes.FakeStateCheckpointService is the
	// intended double.
	StateCheckpoint app.StateCheckpointService
	// RepositoryCheckpoint is checkpoint-b04's frozen port. REAL this wave
	// -- checkpoint-b04 landed on main in Wave 5 and is mergeable; the
	// task brief explicitly instructs calling the real
	// internal/repocheckpoint.Service here, not a fake.
	RepositoryCheckpoint app.RepositoryCheckpointService
	// Pauses is this package's own store (real, this role owns it).
	Pauses PersistPauseStore
	// WakeJobs is runtime-a06's own scheduler store (real, this role owns
	// it).
	WakeJobs *scheduler.Store

	// HaltAfter, if non-empty, causes Persist to return a *HaltError
	// immediately after the named phase's step durably succeeds, without
	// attempting any later phase -- the crash-injection hook required by
	// agents/runtime.md's "crash after every phase resumes/reconciles
	// correctly" test, mirroring internal/progress.CompleteNode.HaltAfter's
	// exact convention. Production callers leave this empty.
	HaltAfter PersistPhase
}

// PersistRequest is Persist's input.
type PersistRequest struct {
	PauseID    domain.PauseID
	TaskID     domain.TaskID
	WorktreeID domain.WorktreeID
	// WakeRunAfter is when the scheduled wake job becomes claimable
	// (scheduler.ScheduleRequest.RunAfter / app.WakeJob.RunAfter).
	WakeRunAfter time.Time
	// WakeMaxAttempts is the wake job's retry budget
	// (scheduler.ScheduleRequest.MaxAttempts). Required (> 0); Persist does
	// not silently default it, since ADD §20.7's retry schedule is only
	// meaningful against a caller-chosen budget.
	WakeMaxAttempts int
}

// PersistResult reports every durable identifier Persist produced (or
// reused, on a resumed attempt), so a caller can thread them into whatever
// PauseRecord it maintains (e.g. runtime-b07's CLI wiring) or diagnostics.
type PersistResult struct {
	Progress PersistProgress
	// Resumed reports whether this call found and reused ANY prior
	// progress at all (true even if only phase 1 had already completed) --
	// distinct from a fully-fresh attempt where every step ran for the
	// first time.
	Resumed bool
	// LastCompletedPhase names the furthest phase in persistPhaseOrder that
	// Progress has durable evidence for at the moment Persist returns --
	// diagnostics-only (e.g. a `preflight status`-style surface reporting
	// "this pause's persist phase reached repository_checkpoint"), never
	// used by Persist itself to decide what to skip (each step checks its
	// own field independently -- see PersistProgress.completedPhase's doc
	// comment for why).
	LastCompletedPhase PersistPhase
}

// wakeJobKind is this package's chosen scheduler.Store job_kind value for
// the pause-resume wake job Persist schedules. A pause has exactly one wake
// job per CONTRACT_FREEZE.md's frozen PauseRecord shape (a single
// wake_pending state, not several), so a single fixed kind string is
// sufficient -- scheduler's own UNIQUE(pause_id, job_kind) constraint is
// keyed on this value.
const wakeJobKind = "pause_resume"

// Persist implements agents/runtime.md Part A deliverable 5 end to end:
// Progress Tree snapshot -> State Checkpoint -> Repository Checkpoint ->
// Pause Record -> Wake Job, in that fixed order, each step durably recorded
// before the next begins, and each step skipped (not re-run) if a prior
// attempt's SaveProgress already recorded it as done -- see this file's
// package comment for the full idempotent-skip rationale per step.
//
// # Fail-closed dependency validation
//
// Every PersistDeps field is checked up front, before any step runs, and
// before req itself is validated -- mirrors orchestrator.CheckpointCreate's
// and safepoint.PersistThenInterrupt's own precedent exactly: a missing
// collaborator is a composition bug, surfaced the same way regardless of
// which request would have triggered it.
//
// # What "resumes/reconciles correctly" means here, precisely
//
// A crash (simulated via HaltAfter, or a genuine process death mid-Persist)
// after phase N's SaveProgress call has committed means phase N's own
// durable side effect (a State Checkpoint row, a Repository Checkpoint
// capture + row, the pause record's own updated columns, or a wake_jobs
// row) is now permanently durable and MUST NOT be produced a second time.
// A crash BEFORE phase N's SaveProgress call (e.g. the underlying service
// call itself failed, or the process died between the service call
// returning and SaveProgress being invoked) means phase N's own service
// determines what "durable" means for its own boundary (e.g.
// checkpoint-b04's Create already committed its own row internally before
// returning -- see the Repository Checkpoint step's own doc comment below
// for how that specific case is handled without creating a duplicate
// checkpoint on retry).
func Persist(ctx context.Context, deps PersistDeps, req PersistRequest) (PersistResult, error) {
	if deps.ProgressTree == nil {
		return PersistResult{}, missingDepError("ProgressTree")
	}
	if deps.StateCheckpoint == nil {
		return PersistResult{}, missingDepError("StateCheckpoint")
	}
	if deps.RepositoryCheckpoint == nil {
		return PersistResult{}, missingDepError("RepositoryCheckpoint")
	}
	if deps.Pauses == nil {
		return PersistResult{}, missingDepError("Pauses")
	}
	if deps.WakeJobs == nil {
		return PersistResult{}, missingDepError("WakeJobs")
	}
	if req.PauseID == "" {
		return PersistResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Persist requires a PauseID", Retryable: false,
		}
	}
	if req.TaskID == "" {
		return PersistResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Persist requires a TaskID", Retryable: false,
		}
	}
	if req.WorktreeID == "" {
		return PersistResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Persist requires a WorktreeID", Retryable: false,
		}
	}
	if req.WakeMaxAttempts <= 0 {
		return PersistResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Persist requires WakeMaxAttempts > 0", Retryable: false,
		}
	}

	progress, found, err := deps.Pauses.GetProgress(ctx, req.PauseID)
	if err != nil {
		return PersistResult{}, err
	}
	if !found {
		return PersistResult{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: Persist requires an existing pause record %q", req.PauseID),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(req.PauseID)},
		}
	}
	resumed := progress != (PersistProgress{})

	// --- Phase 1: Progress Tree snapshot ------------------------------------
	if !progress.ProgressSnapshotTaken {
		if _, err := deps.ProgressTree.Snapshot(ctx, req.TaskID); err != nil {
			return PersistResult{}, err
		}
		progress.ProgressSnapshotTaken = true
		if err := deps.Pauses.SaveProgress(ctx, req.PauseID, progress); err != nil {
			return PersistResult{}, err
		}
	}
	if halt := haltIfRequested(deps.HaltAfter, PhaseProgressSnapshot); halt != nil {
		return newPersistResult(progress, resumed), halt
	}

	// --- Phase 2: State Checkpoint (FAKE this wave -- see PersistDeps doc) --
	if progress.StateCheckpointID == nil {
		ckpt, err := deps.StateCheckpoint.Create(ctx, app.CreateStateCheckpointRequest{TaskID: req.TaskID})
		if err != nil {
			return PersistResult{}, err
		}
		progress.StateCheckpointID = &ckpt.ID
		if err := deps.Pauses.SaveProgress(ctx, req.PauseID, progress); err != nil {
			return PersistResult{}, err
		}
	}
	if halt := haltIfRequested(deps.HaltAfter, PhaseStateCheckpoint); halt != nil {
		return newPersistResult(progress, resumed), halt
	}

	// --- Phase 3: Repository Checkpoint (REAL this wave -- checkpoint-b04) --
	if progress.RepositoryCheckpointID == nil {
		repoCkpt, err := deps.RepositoryCheckpoint.Create(ctx, app.CreateRepositoryCheckpointRequest{
			WorktreeID: req.WorktreeID,
			TaskID:     &req.TaskID,
		})
		if err != nil {
			return PersistResult{}, err
		}
		progress.RepositoryCheckpointID = &repoCkpt.ID
		if err := deps.Pauses.SaveProgress(ctx, req.PauseID, progress); err != nil {
			return PersistResult{}, err
		}
	}
	if halt := haltIfRequested(deps.HaltAfter, PhaseRepositoryCheckpoint); halt != nil {
		return newPersistResult(progress, resumed), halt
	}

	// --- Phase 4: Pause Record --------------------------------------------
	// By this point, the two checkpoint IDs above are already durable
	// (each was recorded by its own SaveProgress call as soon as it was
	// produced) -- this phase's OWN job is different: it is the one step
	// whose entire durable side effect IS a PersistProgress write, so its
	// "step succeeded" marker and its "step's durable effect" are the same
	// SaveProgress call. PauseRecordSaved is set true here specifically so
	// a caller/reconciler can observe that the record-level bookkeeping
	// step itself (as opposed to either checkpoint sub-step) completed,
	// per agents/runtime.md's persist-phase list naming "Pause Record" as
	// its own distinct step, not merely a side effect of the checkpoint
	// steps.
	if !progress.PauseRecordSaved {
		progress.PauseRecordSaved = true
		if err := deps.Pauses.SaveProgress(ctx, req.PauseID, progress); err != nil {
			return PersistResult{}, err
		}
	}
	if halt := haltIfRequested(deps.HaltAfter, PhasePauseRecord); halt != nil {
		return newPersistResult(progress, resumed), halt
	}

	// --- Phase 5: Wake Job ---------------------------------------------------
	if progress.WakeJobID == nil {
		job, err := scheduleWakeJobIdempotent(ctx, deps.WakeJobs, req)
		if err != nil {
			return PersistResult{}, err
		}
		progress.WakeJobID = &job.ID
		if err := deps.Pauses.SaveProgress(ctx, req.PauseID, progress); err != nil {
			return PersistResult{}, err
		}
	}
	if halt := haltIfRequested(deps.HaltAfter, PhaseWakeJob); halt != nil {
		return newPersistResult(progress, resumed), halt
	}

	return newPersistResult(progress, resumed), nil
}

// scheduleWakeJobIdempotent calls scheduler.Store.Schedule and treats a
// UNIQUE(pause_id, job_kind) conflict as "already scheduled by a prior
// attempt that crashed after Schedule's own INSERT committed but before
// this package's SaveProgress call recorded it" -- fetching and returning
// the existing row instead of propagating the conflict as a real failure.
// This is what makes phase 5 safe to retry even in the one narrow window
// where the underlying store's own write already succeeded durably but
// this package had not yet recorded that fact.
func scheduleWakeJobIdempotent(ctx context.Context, store *scheduler.Store, req PersistRequest) (scheduler.Job, error) {
	job, err := store.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID:     req.PauseID,
		Kind:        wakeJobKind,
		RunAfter:    req.WakeRunAfter,
		MaxAttempts: req.WakeMaxAttempts,
	})
	if err == nil {
		return job, nil
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeConflict {
		return scheduler.Job{}, err
	}
	return findWakeJobByPauseID(ctx, store, req.PauseID)
}

// findWakeJobByPauseID recovers the already-scheduled job after a
// UNIQUE(pause_id, job_kind) conflict, via scheduler.Store.GetByPauseKind
// (added alongside this node, since internal/scheduler is this same role's
// own owned package -- Part A owns both internal/pause and
// internal/scheduler, so closing this gap directly rather than declaring it
// out of scope was in-bounds). Claim is deliberately NOT used for this: it
// mutates state (leases the job) as a side effect of finding it, which
// would corrupt what must stay a read-only recovery path.
func findWakeJobByPauseID(ctx context.Context, store *scheduler.Store, pauseID domain.PauseID) (scheduler.Job, error) {
	job, found, err := store.GetByPauseKind(ctx, pauseID, wakeJobKind)
	if err != nil {
		return scheduler.Job{}, err
	}
	if !found {
		// Should be unreachable: Schedule just reported a UNIQUE conflict
		// on this exact (pauseID, wakeJobKind) pair, so a matching row must
		// exist. Fail closed rather than silently returning a zero Job if
		// this ever happens (e.g. a concurrent delete between the two
		// calls, which nothing in this codebase does today).
		return scheduler.Job{}, &domain.Error{
			Code:      domain.ErrCodeIntegrity,
			Message:   fmt.Sprintf("pause: wake job scheduling conflict reported for pause %q but no matching row found on lookup", pauseID),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(pauseID), "job_kind": wakeJobKind},
		}
	}
	return job, nil
}

func haltIfRequested(configured, phase PersistPhase) error {
	if configured == phase {
		return &HaltError{Phase: phase}
	}
	return nil
}

// newPersistResult builds a PersistResult from progress, deriving
// LastCompletedPhase via PersistProgress.completedPhase and walking
// persistPhaseOrder — the single call site both fields' values come from,
// so every Persist return (halted or fully successful) reports a
// consistent, independently-verifiable view of how far the persist phase
// actually got.
func newPersistResult(progress PersistProgress, resumed bool) PersistResult {
	return PersistResult{
		Progress:           progress,
		Resumed:            resumed,
		LastCompletedPhase: progress.completedPhase(),
	}
}

func missingDepError(name string) error {
	return &domain.Error{
		Code:      domain.ErrCodeUnavailable,
		Message:   fmt.Sprintf("pause: Persist requires a non-nil %s dependency", name),
		Retryable: false,
		Details:   map[string]string{"missing_dependency": name},
	}
}
