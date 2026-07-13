// lifecycle.go: Cancel and Resume — the two remaining manual, caller-driven
// pause lifecycle actions runtime-b07's CLI layer wires up
// (`auspex pause cancel`, `auspex resume`). Both are thin orchestration
// over already-existing pieces this package built in earlier nodes: the
// transition table (runtime-a02, statemachine.go) proves the requested
// transition is legal, and PauseStore (runtime-a04, requestpause.go)
// durably records the result — this file's only new contribution is
// sequencing "validate via Apply, then persist via UpdateStatus" behind a
// caller-friendly PauseID-keyed API, mirroring RequestPause's own shape.
//
// # runtime-a09 update: compare-and-swap, not GetByID-then-UpdateStatus
//
// Both Cancel and Resume were originally written as a single GetByID
// followed by one or more unconditional UpdateStatus calls. That shape
// cannot prove either of agents/runtime.md's two required tests this node
// (runtime-a09) targets — "duplicate workers yield one resume" and "cancel
// wins race with wake" — because a second concurrent caller acting on the
// SAME PauseID could read the same starting status before the first
// caller's UpdateStatus call ever lands, then silently overwrite it. Both
// functions now go through PauseStore.CompareAndSwapStatus (see
// requestpause.go) instead, so every transition this file applies is an
// atomic, retryable read-Apply-swap unit — see Cancel's own doc comment
// below for the full argument, and wake.go for the scheduler-driven
// counterpart (Wake) that a09 also adds.
//
// # Why Resume does not (yet) call the full ADD Sec20.8 checklist
//
// agents/runtime.md Part A deliverable 8 ("Resume validation: quota safe;
// repository fingerprint compatible; session/provider capability valid;
// authorization/consent valid") is runtime-a08's own DAG node, not part of
// runtime-a05/b07's scope this wave (EXECUTION_DAG.md: runtime-a08 depends
// on runtime-a05, i.e. it comes AFTER this wave's two nodes). Resume here
// therefore implements only the STATE MACHINE half of a manual resume
// (WakePending -> Validating -> Resuming -> Resumed, or the
// Validating -> Sleeping/BlockedConflict rejection edges if the caller
// reports validation failed) — it does not itself perform any quota/
// repository/session/authorization check. ResumeRequest.Valid (or
// .QuotaUnsafe/.Conflict) is the caller's own pre-computed verdict; wiring
// a real check in is explicitly a08's job. This is documented here, not
// silently implied, per Constitution Sec7 rule 3 ("provider capability
// gaps are surfaced explicitly, never silently assumed away") applied to
// an internal capability gap, not just a provider one.
package pause

import (
	"context"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// CancelRequest is Cancel's input.
type CancelRequest struct {
	PauseID domain.PauseID
}

// CancelResult reports the record after cancellation.
type CancelResult struct {
	Record PauseRecord
}

// Cancel implements agents/runtime.md Part A deliverable 10: "Cancel
// prevents future resume." It applies EventCancel from the record's
// current status (failing if no edge exists — e.g. the record is already
// terminal, or Interrupting's deliberately-narrowed no-cancel-edge case
// documented in statemachine.go) and durably persists the resulting
// domain.PauseCancelled status. Once cancelled, IsTerminal(PauseCancelled)
// is true, so no further transition (including a race against a wake job
// concurrently trying to advance the same record — ADD's "cancel wins race
// with wake" required test, proven end-to-end in lifecycle_test.go /
// wake_test.go, and at the state-machine level in statemachine_test.go)
// can move it anywhere else.
//
// # Why this is a compare-and-swap loop, not GetByID-then-UpdateStatus
//
// A plain "read status, Apply, UpdateStatus" sequence has a real
// time-of-check-to-time-of-use gap: if a concurrent caller (most
// importantly, a scheduler worker driving Wake for the same PauseID) writes
// a new status in between this call's GetByID and its UpdateStatus, that
// concurrent write is silently clobbered — UpdateStatus is unconditional.
// Cancel instead loops on store.CompareAndSwapStatus: each iteration reads
// the record fresh, computes Apply(currentStatus, EventCancel), and only
// commits if the record's status is STILL currentStatus at write time. If
// CompareAndSwapStatus reports ok=false (someone else moved the record
// first), Cancel re-reads and retries against the new current status,
// rather than either clobbering the other writer's result or silently
// giving up. This is what makes "cancel wins race with wake" a real,
// provable guarantee rather than a best-effort one: Cancel never stops
// retrying until it either (a) durably lands EventCancel, or (b) discovers
// the record has reached a terminal state — including one a racing Wake/
// Resume got to first, in which case Cancel correctly reports the
// now-terminal TransitionError instead of pretending to have cancelled
// something that already finished.
func Cancel(ctx context.Context, store PauseStore, req CancelRequest) (CancelResult, error) {
	if store == nil {
		return CancelResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: Cancel requires a non-nil PauseStore", Retryable: false,
		}
	}
	if req.PauseID == "" {
		return CancelResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Cancel requires a PauseID", Retryable: false,
		}
	}

	status, err := applyCASVerb(ctx, store, req.PauseID, EventCancel, "Cancel")
	if err != nil {
		// Includes the terminal case — e.g. a racing Wake/Resume already
		// drove this record to Resumed before Cancel's CAS attempt caught
		// up; that is a legitimate lost race (wake/resume got there first),
		// reported as-is, not retried.
		return CancelResult{}, err
	}

	rec, found, err := store.GetByID(ctx, req.PauseID)
	if err != nil {
		return CancelResult{}, err
	}
	if !found {
		return CancelResult{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: Cancel: pause record %q not found", req.PauseID),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(req.PauseID)},
		}
	}
	rec.Status = status
	return CancelResult{Record: rec}, nil
}

// ResumeRequest is Resume's input. Verdict fields are the caller's own
// pre-computed resume-validation outcome (see this file's package comment
// for why: the real checks are runtime-a08's scope, not built yet). Exactly
// one of Valid/QuotaUnsafe/Conflict must be true; Resume rejects an
// ambiguous or all-false request rather than guessing.
type ResumeRequest struct {
	PauseID domain.PauseID
	// Valid reports the caller determined every resume-validation check
	// passed (ADD Sec20.8) — advances WakePending->Validating->Resuming
	// and then Resuming->Resumed in one call (there is no externally
	// observable reason to stop at an intermediate state for a CLI-driven
	// manual resume, unlike the scheduler-driven wake path a09 will build,
	// which needs to pause at each step to interleave with lease/duplicate-
	// wake handling).
	Valid bool
	// QuotaUnsafe reports the caller determined quota is still unsafe —
	// the required "unsafe quota reschedules" edge (Validating->Sleeping).
	// This is a08's scope to actually CALL correctly; Resume here only
	// applies the edge once told to.
	QuotaUnsafe bool
	// Conflict reports the caller determined a repository/session/
	// authorization conflict exists — the required "repo overlap blocks"
	// edge (Validating->BlockedConflict).
	Conflict bool
}

// ResumeResult reports the record after Resume's transition(s).
type ResumeResult struct {
	Record PauseRecord
}

// Resume implements the state-machine half of agents/runtime.md Part A
// deliverable 8 (see package comment for the explicit real-validation gap).
// The record must currently be domain.PauseWakePending (the state a wake
// job's EventWakeDue transition leaves it in, or wherever a caller's own
// bookkeeping has already advanced it to Validating from a prior partial
// call — see below); Resume drives it forward according to exactly one of
// req's three verdict fields.
func Resume(ctx context.Context, store PauseStore, req ResumeRequest) (ResumeResult, error) {
	if store == nil {
		return ResumeResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: Resume requires a non-nil PauseStore", Retryable: false,
		}
	}
	if req.PauseID == "" {
		return ResumeResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: Resume requires a PauseID", Retryable: false,
		}
	}
	verdictCount := boolToInt(req.Valid) + boolToInt(req.QuotaUnsafe) + boolToInt(req.Conflict)
	if verdictCount != 1 {
		return ResumeResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "pause: Resume requires exactly one of Valid/QuotaUnsafe/Conflict to be true",
			Retryable: false,
			Details:   map[string]string{"valid": boolStr(req.Valid), "quota_unsafe": boolStr(req.QuotaUnsafe), "conflict": boolStr(req.Conflict)},
		}
	}

	// Step into Validating first if still WakePending — EventResumeValid
	// is the one edge the transition table defines out of WakePending
	// (statemachine.go), regardless of which of the three verdicts the
	// caller ultimately reports; the verdict itself only matters once
	// Validating is reached. Each step below is its own atomic
	// read-Apply-CompareAndSwap unit (applyCASVerb/applyCASFrom, defined
	// below) rather than one GetByID up front followed by several
	// UpdateStatus calls — the latter would let a concurrent Cancel (or a
	// second duplicate wake worker driving the SAME PauseID) observe a
	// stale status in between this function's own steps and silently lose
	// that race instead of being correctly reported back to its own
	// caller. See applyCASVerb's doc comment and Cancel's doc comment
	// above for the full rationale.
	status, err := currentStatus(ctx, store, req.PauseID, "Resume")
	if err != nil {
		return ResumeResult{}, err
	}
	if status == domain.PauseWakePending {
		status, err = applyCASVerb(ctx, store, req.PauseID, EventResumeValid, "Resume")
		if err != nil {
			return ResumeResult{}, err
		}
	}

	var event Event
	switch {
	case req.Valid:
		event = EventResumeValid
	case req.QuotaUnsafe:
		event = EventQuotaUnsafe
	case req.Conflict:
		event = EventConflict
	}
	status, err = applyCASFrom(ctx, store, req.PauseID, status, event, "Resume")
	if err != nil {
		return ResumeResult{}, err
	}

	// A fully-valid resume advances one step further, Resuming->Resumed —
	// per this function's doc comment, a CLI-driven manual resume has no
	// reason to stop at the intermediate Resuming state.
	if req.Valid {
		status, err = applyCASFrom(ctx, store, req.PauseID, status, EventResumeStarted, "Resume")
		if err != nil {
			return ResumeResult{}, err
		}
	}

	rec, found, err := store.GetByID(ctx, req.PauseID)
	if err != nil {
		return ResumeResult{}, err
	}
	if !found {
		return ResumeResult{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: Resume: pause record %q not found", req.PauseID),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(req.PauseID)},
		}
	}
	rec.Status = status
	return ResumeResult{Record: rec}, nil
}

// currentStatus reads id's current status, translating a missing record
// into the frozen ErrCodeNotFound shape callers expect (Resume/Wake's own
// "unknown pause ID" error), so every read call site in this file reports
// the same error regardless of which step it occurs at.
func currentStatus(ctx context.Context, store PauseStore, id domain.PauseID, verb string) (domain.PauseStatus, error) {
	rec, found, err := store.GetByID(ctx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: %s: pause record %q not found", verb, id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}
	return rec.Status, nil
}

// applyCASVerb re-reads id's current status, computes Apply(current,
// event), and commits via CompareAndSwapStatus, retrying on a lost race
// (another writer moved the record between the read and the swap) until it
// either commits or Apply itself rejects the (possibly now-different)
// current status — see Cancel's doc comment above for the exact rationale
// this mirrors. Returns the new status on success. verb is only used to
// label a not-found error consistently with whichever public function
// (Cancel/Resume/Wake) is calling this.
func applyCASVerb(ctx context.Context, store PauseStore, id domain.PauseID, event Event, verb string) (domain.PauseStatus, error) {
	for {
		current, err := currentStatus(ctx, store, id, verb)
		if err != nil {
			return "", err
		}
		next, applied, err := tryApplyCAS(ctx, store, id, current, event)
		if err != nil {
			return "", err
		}
		if !applied {
			continue
		}
		return next, nil
	}
}

// applyCASFrom is applyCASVerb's variant for a caller that already knows
// the expected current status (e.g. Resume, which just derived it from its
// own previous step in the same call) — it still verifies that status via
// CompareAndSwapStatus rather than assuming it is still current, and falls
// back to a fresh applyCASVerb retry loop if it is not (the same "someone
// else moved it first" case applyCASVerb itself handles).
func applyCASFrom(ctx context.Context, store PauseStore, id domain.PauseID, expected domain.PauseStatus, event Event, verb string) (domain.PauseStatus, error) {
	next, applied, err := tryApplyCAS(ctx, store, id, expected, event)
	if err != nil {
		return "", err
	}
	if applied {
		return next, nil
	}
	return applyCASVerb(ctx, store, id, event, verb)
}

// tryApplyCAS is the one-shot (no retry) building block both applyCAS and
// applyCASFrom share: compute Apply(expected, event), then attempt exactly
// one CompareAndSwapStatus against expected. applied=false (nil error)
// means the record's status was no longer expected at swap time — the
// caller decides whether to retry.
func tryApplyCAS(ctx context.Context, store PauseStore, id domain.PauseID, expected domain.PauseStatus, event Event) (next domain.PauseStatus, applied bool, err error) {
	next, err = Apply(expected, event)
	if err != nil {
		return "", false, err
	}
	ok, found, err := store.CompareAndSwapStatus(ctx, id, expected, next)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: pause record %q not found", id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}
	return next, ok, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
