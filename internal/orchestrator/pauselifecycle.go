// pauselifecycle.go implements the orchestrator-layer wiring for
// `auspex pause request`, `auspex pause cancel`, `auspex resume`,
// and `auspex scheduler run-once` (agents/runtime.md Part B P0 command
// list; EXECUTION_DAG.md runtime-b07). Unlike runtime-b05's
// CheckpointCreate (which sequences TWO different cross-role services),
// every collaborator here is this SAME role's own Part A work
// (internal/pause, internal/scheduler) — per the DAG's own note, this
// dependency is "now a hard dependency... same branch, no fake needed": no
// internal/testutil/fakes double is used anywhere in this file, only the
// real internal/pause.RequestPause/Cancel/Resume and the real
// internal/scheduler.Store.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
)

// --- auspex pause request ------------------------------------------

// PauseLifecycleDeps bundles every collaborator this file's three pause
// commands need. Store is required by all three; WakeJobs is required only
// by SchedulerRunOnce (see that function's own nil check) — bundled into
// one Deps struct anyway, mirroring StatusDeps/DoctorDeps's existing
// per-command-family grouping convention, since all three pause commands
// and the scheduler command are one cohesive "operate the pause/scheduler
// subsystem from the CLI" surface.
type PauseLifecycleDeps struct {
	Store    pause.PauseStore
	WakeJobs *scheduler.Store
}

// PauseRequestRequest is `auspex pause request`'s input.
type PauseRequestRequest struct {
	TaskID    domain.TaskID
	SessionID domain.SessionID
	Reason    pause.TriggerReason
}

// PauseRequestResult reports the resulting record and whether this call
// created it or found an already-in-flight one (pass-through of
// pause.RequestPauseResult's own Created flag).
type PauseRequestResult struct {
	Record  pause.PauseRecord
	Created bool
}

// PauseRequestCmd implements `auspex pause request`: idempotent pause
// creation via this role's own runtime-a04 RequestPause. IDs come from a
// real domain.IDGenerator, matching every other orchestrator command's
// convention of never fabricating an ID inline.
func PauseRequestCmd(ctx context.Context, deps PauseLifecycleDeps, ids domain.IDGenerator, req PauseRequestRequest) (PauseRequestResult, error) {
	if deps.Store == nil {
		return PauseRequestResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: PauseRequestCmd requires a non-nil PauseStore", Retryable: false,
		}
	}
	if ids == nil {
		return PauseRequestResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: PauseRequestCmd requires a non-nil IDGenerator", Retryable: false,
		}
	}

	result, err := pause.RequestPause(ctx, deps.Store, ids, pause.RequestPauseRequest{
		Key:    pause.PauseKey{TaskID: req.TaskID, SessionID: req.SessionID},
		Reason: req.Reason,
	})
	if err != nil {
		return PauseRequestResult{}, err
	}
	return PauseRequestResult{Record: result.Record, Created: result.Created}, nil
}

// --- auspex pause cancel ----------------------------------------------

// PauseCancelRequest is `auspex pause cancel`'s input.
type PauseCancelRequest struct {
	PauseID domain.PauseID
}

// PauseCancelResult reports the record after cancellation.
type PauseCancelResult struct {
	Record pause.PauseRecord
}

// PauseCancelCmd implements `auspex pause cancel` via this role's own
// runtime-b07 pause.Cancel (lifecycle.go).
func PauseCancelCmd(ctx context.Context, deps PauseLifecycleDeps, req PauseCancelRequest) (PauseCancelResult, error) {
	if deps.Store == nil {
		return PauseCancelResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: PauseCancelCmd requires a non-nil PauseStore", Retryable: false,
		}
	}
	result, err := pause.Cancel(ctx, deps.Store, pause.CancelRequest{PauseID: req.PauseID})
	if err != nil {
		return PauseCancelResult{}, err
	}
	return PauseCancelResult{Record: result.Record}, nil
}

// --- auspex resume ----------------------------------------------------

// ResumeCmdRequest is `auspex resume`'s input. See pause.ResumeRequest's
// doc comment (lifecycle.go) for why the verdict is caller-supplied this
// wave rather than independently computed here: real resume validation
// (quota/repository/session/authorization checks) is runtime-a08's scope,
// not yet built. Defaulting Valid to true when no verdict flag is set at
// all keeps the common CLI case (`auspex resume` with no extra flags)
// usable today without requiring a caller to already know about a08's
// not-yet-existing checks — this default is documented, not silent, and is
// exactly the kind of honest, explicit stand-in Constitution Sec7 rule 3
// requires rather than a silently-assumed-safe gap.
type ResumeCmdRequest struct {
	PauseID     domain.PauseID
	QuotaUnsafe bool
	Conflict    bool
}

// ResumeCmdResult reports the record after Resume's transition(s).
type ResumeCmdResult struct {
	Record pause.PauseRecord
}

// ResumeCmd implements `auspex resume` via this role's own runtime-b07
// pause.Resume (lifecycle.go).
func ResumeCmd(ctx context.Context, deps PauseLifecycleDeps, req ResumeCmdRequest) (ResumeCmdResult, error) {
	if deps.Store == nil {
		return ResumeCmdResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: ResumeCmd requires a non-nil PauseStore", Retryable: false,
		}
	}
	valid := !req.QuotaUnsafe && !req.Conflict
	result, err := pause.Resume(ctx, deps.Store, pause.ResumeRequest{
		PauseID:     req.PauseID,
		Valid:       valid,
		QuotaUnsafe: req.QuotaUnsafe,
		Conflict:    req.Conflict,
	})
	if err != nil {
		return ResumeCmdResult{}, err
	}
	return ResumeCmdResult{Record: result.Record}, nil
}

// --- auspex scheduler run-once -----------------------------------------

// SchedulerRunOnceRequest is `auspex scheduler run-once`'s input. Owner
// identifies this CLI invocation as a lease claimant (scheduler.Store.Claim
// requires a non-empty owner) — a fixed, recognizable value rather than a
// caller-supplied flag, since a one-shot CLI sweep has no durable identity
// across invocations the way a long-running daemon worker would.
type SchedulerRunOnceRequest struct {
	// Owner overrides the default lease-owner identity
	// ("cli-scheduler-run-once"); left empty, the default applies. Exposed
	// so a caller that wants to distinguish multiple concurrent
	// `run-once` invocations (e.g. a test, or a scripted parallel sweep)
	// can do so, without requiring every ordinary invocation to supply one.
	Owner string
}

// DefaultSchedulerRunOnceOwner is the lease-owner identity `run-once` uses
// when the caller does not supply one.
const DefaultSchedulerRunOnceOwner = "cli-scheduler-run-once"

// SchedulerRunOnceResult reports what the sweep found.
type SchedulerRunOnceResult struct {
	// Claimed reports whether a due, unleased job existed and was claimed.
	Claimed bool
	Job     scheduler.Job
}

// SchedulerRunOnceCmd implements `auspex scheduler run-once`: a single
// Claim sweep via runtime-a06's scheduler.Store, using
// scheduler.DefaultLeaseDuration. This command claims and reports a job; it
// deliberately does NOT itself drive the claimed job's pause record forward
// (that would require wiring EventWakeDue and then the full Resume
// validation chain, runtime-a08/a09's scope, not this node's) — a claimed
// job is left leased for a later node's worker loop to actually process,
// consistent with "run a single scheduler sweep and exit" (agents/
// runtime.md P0 command description) naming a claim sweep, not a full
// wake-to-resume pipeline.
func SchedulerRunOnceCmd(ctx context.Context, deps PauseLifecycleDeps, req SchedulerRunOnceRequest) (SchedulerRunOnceResult, error) {
	if deps.WakeJobs == nil {
		return SchedulerRunOnceResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: SchedulerRunOnceCmd requires a non-nil scheduler.Store", Retryable: false,
		}
	}
	owner := req.Owner
	if owner == "" {
		owner = DefaultSchedulerRunOnceOwner
	}

	result, err := deps.WakeJobs.Claim(ctx, owner, scheduler.DefaultLeaseDuration)
	if err != nil {
		return SchedulerRunOnceResult{}, err
	}
	return SchedulerRunOnceResult{Claimed: result.Found, Job: result.Job}, nil
}
