package pause_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
)

func newSeededMemStore(t *testing.T, id domain.PauseID, status domain.PauseStatus) *pause.MemStore {
	t.Helper()
	store := pause.NewMemStore()
	if err := store.Insert(context.Background(), pause.PauseRecord{
		ID:     id,
		Key:    pause.PauseKey{TaskID: "task-1", SessionID: "sess-1"},
		Status: status,
		Reason: pause.TriggerReasonCalibrated,
	}); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
	return store
}

// --- Cancel ------------------------------------------------------------

func TestLifecycle_Cancel_TransitionsToCancelledAndPersists(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseSleeping)
	result, err := pause.Cancel(context.Background(), store, pause.CancelRequest{PauseID: "pause-1"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if result.Record.Status != domain.PauseCancelled {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseCancelled)
	}

	rec, found, err := store.GetByID(context.Background(), "pause-1")
	if err != nil || !found {
		t.Fatalf("GetByID after Cancel: found=%v err=%v", found, err)
	}
	if rec.Status != domain.PauseCancelled {
		t.Fatalf("durable Status = %q, want %q", rec.Status, domain.PauseCancelled)
	}
}

func TestLifecycle_Cancel_UnknownPauseIDFailsNotFound(t *testing.T) {
	store := pause.NewMemStore()
	_, err := pause.Cancel(context.Background(), store, pause.CancelRequest{PauseID: "does-not-exist"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want ErrCodeNotFound", err)
	}
}

func TestLifecycle_Cancel_TerminalStateRejected(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseResumed)
	_, err := pause.Cancel(context.Background(), store, pause.CancelRequest{PauseID: "pause-1"})
	var terr *pause.TransitionError
	if !errors.As(err, &terr) || !terr.Terminal {
		t.Fatalf("err = %v, want a terminal *TransitionError", err)
	}
}

func TestLifecycle_Cancel_ValidatesRequest(t *testing.T) {
	store := pause.NewMemStore()
	_, err := pause.Cancel(context.Background(), store, pause.CancelRequest{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

func TestLifecycle_Cancel_NilStoreFailsClosed(t *testing.T) {
	_, err := pause.Cancel(context.Background(), nil, pause.CancelRequest{PauseID: "pause-1"})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

// --- Resume --------------------------------------------------------------

func TestLifecycle_Resume_ValidVerdictReachesResumed(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseWakePending)
	result, err := pause.Resume(context.Background(), store, pause.ResumeRequest{PauseID: "pause-1", Valid: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Record.Status != domain.PauseResumed {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseResumed)
	}
	rec, _, _ := store.GetByID(context.Background(), "pause-1")
	if rec.Status != domain.PauseResumed {
		t.Fatalf("durable Status = %q, want %q", rec.Status, domain.PauseResumed)
	}
}

func TestLifecycle_Resume_QuotaUnsafeReschedulesToSleeping(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseWakePending)
	result, err := pause.Resume(context.Background(), store, pause.ResumeRequest{PauseID: "pause-1", QuotaUnsafe: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Record.Status != domain.PauseSleeping {
		t.Fatalf("Status = %q, want %q (unsafe quota reschedules)", result.Record.Status, domain.PauseSleeping)
	}
}

func TestLifecycle_Resume_ConflictBlocks(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseWakePending)
	result, err := pause.Resume(context.Background(), store, pause.ResumeRequest{PauseID: "pause-1", Conflict: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Record.Status != domain.PauseBlockedConflict {
		t.Fatalf("Status = %q, want %q (repo overlap blocks)", result.Record.Status, domain.PauseBlockedConflict)
	}
}

func TestLifecycle_Resume_AlreadyValidatingSkipsReentryStep(t *testing.T) {
	// A record already in Validating (e.g. a prior partial call already
	// advanced it past WakePending) must not attempt EventResumeValid a
	// second time to LEAVE WakePending — Resume must detect it is already
	// past that step and apply the verdict event directly.
	store := newSeededMemStore(t, "pause-1", domain.PauseValidating)
	result, err := pause.Resume(context.Background(), store, pause.ResumeRequest{PauseID: "pause-1", Valid: true})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Record.Status != domain.PauseResumed {
		t.Fatalf("Status = %q, want %q", result.Record.Status, domain.PauseResumed)
	}
}

func TestLifecycle_Resume_RequiresExactlyOneVerdict(t *testing.T) {
	store := newSeededMemStore(t, "pause-1", domain.PauseWakePending)
	cases := []pause.ResumeRequest{
		{PauseID: "pause-1"},
		{PauseID: "pause-1", Valid: true, Conflict: true},
	}
	for i, req := range cases {
		_, err := pause.Resume(context.Background(), store, req)
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
			t.Errorf("case %d: err = %v, want ErrCodeValidation", i, err)
		}
	}
}

func TestLifecycle_Resume_UnknownPauseIDFailsNotFound(t *testing.T) {
	store := pause.NewMemStore()
	_, err := pause.Resume(context.Background(), store, pause.ResumeRequest{PauseID: "does-not-exist", Valid: true})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want ErrCodeNotFound", err)
	}
}

func TestLifecycle_Resume_NilStoreFailsClosed(t *testing.T) {
	_, err := pause.Resume(context.Background(), nil, pause.ResumeRequest{PauseID: "pause-1", Valid: true})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

func TestLifecycle_Resume_InvalidFromStateRejected(t *testing.T) {
	// A record already Resumed (terminal) cannot Resume again.
	store := newSeededMemStore(t, "pause-1", domain.PauseResumed)
	_, err := pause.Resume(context.Background(), store, pause.ResumeRequest{PauseID: "pause-1", Valid: true})
	var terr *pause.TransitionError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TransitionError", err)
	}
}
