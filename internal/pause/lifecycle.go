// lifecycle.go: Cancel and Resume — the two remaining manual, caller-driven
// pause lifecycle actions runtime-b07's CLI layer wires up
// (`preflight pause cancel`, `preflight resume`). Both are thin orchestration
// over already-existing pieces this package built in earlier nodes: the
// transition table (runtime-a02, statemachine.go) proves the requested
// transition is legal, and PauseStore (runtime-a04, requestpause.go)
// durably records the result — this file's only new contribution is
// sequencing "validate via Apply, then persist via UpdateStatus" behind a
// caller-friendly PauseID-keyed API, mirroring RequestPause's own shape.
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

	"github.com/huaiche94/preflight/internal/domain"
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
// with wake" required test, proven at the state-machine level in
// statemachine_test.go) can move it anywhere else.
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

	next, err := Apply(rec.Status, EventCancel)
	if err != nil {
		return CancelResult{}, err
	}

	if err := store.UpdateStatus(ctx, req.PauseID, next); err != nil {
		return CancelResult{}, err
	}
	rec.Status = next
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

	// Step into Validating first if still WakePending — EventResumeValid
	// is the one edge the transition table defines out of WakePending
	// (statemachine.go), regardless of which of the three verdicts the
	// caller ultimately reports; the verdict itself only matters once
	// Validating is reached.
	status := rec.Status
	if status == domain.PauseWakePending {
		status, err = Apply(status, EventResumeValid)
		if err != nil {
			return ResumeResult{}, err
		}
		if err := store.UpdateStatus(ctx, req.PauseID, status); err != nil {
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
	status, err = Apply(status, event)
	if err != nil {
		return ResumeResult{}, err
	}
	if err := store.UpdateStatus(ctx, req.PauseID, status); err != nil {
		return ResumeResult{}, err
	}

	// A fully-valid resume advances one step further, Resuming->Resumed —
	// per this function's doc comment, a CLI-driven manual resume has no
	// reason to stop at the intermediate Resuming state.
	if req.Valid {
		status, err = Apply(status, EventResumeStarted)
		if err != nil {
			return ResumeResult{}, err
		}
		if err := store.UpdateStatus(ctx, req.PauseID, status); err != nil {
			return ResumeResult{}, err
		}
	}

	rec.Status = status
	return ResumeResult{Record: rec}, nil
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
