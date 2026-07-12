package pause

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/huaiche94/preflight/internal/domain"
)

type sequentialPauseIDs struct {
	counter atomic.Int64
}

func (g *sequentialPauseIDs) NewID() string {
	return "pause-" + strconv.FormatInt(g.counter.Add(1), 10)
}

const (
	testTaskID    domain.TaskID    = "task-1"
	testSessionID domain.SessionID = "sess-1"
)

func testKey() PauseKey {
	return PauseKey{TaskID: testTaskID, SessionID: testSessionID}
}

// TestRequestPause_FirstCallCreatesRecord proves the base case: no
// existing active pause for the key means RequestPause actually inserts a
// new record at the entry state.
func TestRequestPause_FirstCallCreatesRecord(t *testing.T) {
	store := NewMemStore()
	ids := &sequentialPauseIDs{}
	ctx := context.Background()

	result, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if !result.Created {
		t.Fatalf("first call: Created = false, want true")
	}
	if result.Record.Status != domain.PausePredicted {
		t.Fatalf("first call: Status = %q, want %q", result.Record.Status, domain.PausePredicted)
	}
	if result.Record.ID == "" {
		t.Fatalf("first call: expected a non-empty PauseID")
	}
}

// TestRequestPause_IdempotentReplayReturnsSameRecordNoDuplicate is the
// required test (verbatim intent): "RequestPause idempotency" — calling
// RequestPause a second time for the SAME key, while the first pause is
// still active (non-terminal), must return the exact same record (same
// ID) and must NOT create a second record.
func TestRequestPause_IdempotentReplayReturnsSameRecordNoDuplicate(t *testing.T) {
	store := NewMemStore()
	ids := &sequentialPauseIDs{}
	ctx := context.Background()

	first, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("first RequestPause: %v", err)
	}

	second, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("second RequestPause: %v", err)
	}
	if second.Created {
		t.Fatalf("second call: Created = true, want false (idempotent replay)")
	}
	if second.Record.ID != first.Record.ID {
		t.Fatalf("second call returned a different PauseID (%q) than the first (%q) — duplicate record created", second.Record.ID, first.Record.ID)
	}

	// Calling it many more times must keep converging on the same single
	// record — never accumulating duplicates.
	for i := 0; i < 5; i++ {
		again, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if again.Created || again.Record.ID != first.Record.ID {
			t.Fatalf("replay %d: got %+v, want the original record unchanged", i, again)
		}
	}
}

// TestRequestPause_ReplayWithDifferentReasonStillIdempotent proves that an
// emergency sample arriving while a calibrated pause is already in flight
// for the same key does not fork into a second pause record — it is still
// treated as a replay against the existing in-flight pause (escalation
// policy, if any, belongs to a later node).
func TestRequestPause_ReplayWithDifferentReasonStillIdempotent(t *testing.T) {
	store := NewMemStore()
	ids := &sequentialPauseIDs{}
	ctx := context.Background()

	first, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("first RequestPause: %v", err)
	}

	escalated, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonEmergency})
	if err != nil {
		t.Fatalf("escalated RequestPause: %v", err)
	}
	if escalated.Created {
		t.Fatalf("escalated call: Created = true, want false")
	}
	if escalated.Record.ID != first.Record.ID {
		t.Fatalf("escalated call created a second record: got %q, want %q", escalated.Record.ID, first.Record.ID)
	}
}

// TestRequestPause_NewRecordAllowedAfterPriorPauseTerminal proves
// idempotency is scoped to an ACTIVE (non-terminal) pause, not "ever, for
// this key": once a prior pause for the same task/session has reached a
// terminal state (e.g. resumed, cancelled), a fresh RequestPause call
// legitimately starts a brand new pause cycle rather than being wrongly
// treated as a replay of the old one forever.
func TestRequestPause_NewRecordAllowedAfterPriorPauseTerminal(t *testing.T) {
	store := NewMemStore()
	ids := &sequentialPauseIDs{}
	ctx := context.Background()

	first, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("first RequestPause: %v", err)
	}
	store.SetStatus(testKey(), domain.PauseResumed)

	second, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("second RequestPause: %v", err)
	}
	if !second.Created {
		t.Fatalf("second call after prior pause went terminal: Created = false, want true")
	}
	if second.Record.ID == first.Record.ID {
		t.Fatalf("second call reused the terminal record's ID; want a fresh PauseID for the new cycle")
	}
}

// TestRequestPause_DifferentSessionsAreIndependent proves the natural key
// is scoped per (TaskID, SessionID) — a different session on the same
// task gets its own independent pause record.
func TestRequestPause_DifferentSessionsAreIndependent(t *testing.T) {
	store := NewMemStore()
	ids := &sequentialPauseIDs{}
	ctx := context.Background()

	keyA := PauseKey{TaskID: testTaskID, SessionID: "sess-a"}
	keyB := PauseKey{TaskID: testTaskID, SessionID: "sess-b"}

	a, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: keyA, Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("RequestPause A: %v", err)
	}
	b, err := RequestPause(ctx, store, ids, RequestPauseRequest{Key: keyB, Reason: TriggerReasonCalibrated})
	if err != nil {
		t.Fatalf("RequestPause B: %v", err)
	}
	if !a.Created || !b.Created {
		t.Fatalf("both distinct sessions should independently create: got a.Created=%v b.Created=%v", a.Created, b.Created)
	}
	if a.Record.ID == b.Record.ID {
		t.Fatalf("distinct sessions must not share a PauseID")
	}
}

// TestRequestPause_ValidatesRequest proves RequestPause fails closed on a
// malformed request rather than silently proceeding with a zero-value
// key/reason.
func TestRequestPause_ValidatesRequest(t *testing.T) {
	store := NewMemStore()
	ids := &sequentialPauseIDs{}
	ctx := context.Background()

	cases := []struct {
		name string
		req  RequestPauseRequest
	}{
		{"missing task id", RequestPauseRequest{Key: PauseKey{SessionID: testSessionID}, Reason: TriggerReasonCalibrated}},
		{"missing session id", RequestPauseRequest{Key: PauseKey{TaskID: testTaskID}, Reason: TriggerReasonCalibrated}},
		{"missing reason", RequestPauseRequest{Key: testKey()}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RequestPause(ctx, store, ids, c.req)
			var derr *domain.Error
			if !errors.As(err, &derr) {
				t.Fatalf("got err %v, want *domain.Error", err)
			}
			if derr.Code != domain.ErrCodeValidation {
				t.Fatalf("Code = %q, want %q", derr.Code, domain.ErrCodeValidation)
			}
		})
	}
}

// TestRequestPause_StoreErrorPropagates proves a FindActiveByKey/Insert
// failure surfaces as-is rather than being swallowed.
func TestRequestPause_StoreErrorPropagates(t *testing.T) {
	ctx := context.Background()
	ids := &sequentialPauseIDs{}
	wantErr := &domain.Error{Code: domain.ErrCodeUnavailable, Message: "boom", Retryable: true}

	failingStore := &fakePauseStore{
		findFunc: func(context.Context, PauseKey) (PauseRecord, bool, error) {
			return PauseRecord{}, false, wantErr
		},
	}
	_, err := RequestPause(ctx, failingStore, ids, RequestPauseRequest{Key: testKey(), Reason: TriggerReasonCalibrated})
	if !errors.Is(err, error(wantErr)) {
		t.Fatalf("got err %v, want the store's error propagated unchanged", err)
	}
}

// fakePauseStore is a minimal configurable PauseStore double for
// exercising error paths this package's own MemStore never returns.
type fakePauseStore struct {
	findFunc   func(context.Context, PauseKey) (PauseRecord, bool, error)
	insertFunc func(context.Context, PauseRecord) error
}

var _ PauseStore = (*fakePauseStore)(nil)

func (f *fakePauseStore) FindActiveByKey(ctx context.Context, key PauseKey) (PauseRecord, bool, error) {
	return f.findFunc(ctx, key)
}

func (f *fakePauseStore) Insert(ctx context.Context, rec PauseRecord) error {
	if f.insertFunc == nil {
		return nil
	}
	return f.insertFunc(ctx, rec)
}

// GetByID/UpdateStatus are not exercised by this package's own
// RequestPause-focused tests (see lifecycle_test.go for Cancel/Resume
// coverage against a real MemStore instead) — minimal stubs keep
// fakePauseStore satisfying the full PauseStore interface without pulling
// in unused configurability this file's tests don't need.
func (f *fakePauseStore) GetByID(context.Context, domain.PauseID) (PauseRecord, bool, error) {
	return PauseRecord{}, false, nil
}

func (f *fakePauseStore) UpdateStatus(context.Context, domain.PauseID, domain.PauseStatus) error {
	return nil
}
