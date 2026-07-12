package pause

import (
	"context"
	"fmt"
	"sync"

	"github.com/huaiche94/preflight/internal/domain"
)

// PauseKey is the natural idempotency key for RequestPause (agents/
// runtime.md Part A deliverable 3): one task, on one session, may have at
// most one live (non-terminal) pause in flight at a time. This mirrors
// wake_jobs' own UNIQUE(pause_id, job_kind) exactly-once anchor
// (internal/scheduler's migration 0051) — pause_records has no separate
// caller-supplied idempotency key column (Preflight_ADD.md §12.2), so the
// natural key IS the idempotency key here, exactly as CONTRACT_FREEZE.md's
// "ID and idempotency rules" section describes for
// CompleteNodeRequest.IdempotencyKey: "same completion request replayed
// ... MUST return the same result; a different payload under the same key
// is a conflict, not a silent overwrite."
type PauseKey struct {
	TaskID    domain.TaskID
	SessionID domain.SessionID
}

// PauseRecord is this package's own durable-shape view of a pause_records
// row — deliberately narrower than internal/app.PauseRecord (which is the
// frozen cross-component DTO GracefulPauseService returns): this type adds
// the Reason/TriggerReason/Key fields RequestPause's idempotency check and
// the safe-point coordinator both need internally, and a later node
// (runtime-a05) is responsible for mapping between the two at the
// GracefulPauseService boundary, not this one.
type PauseRecord struct {
	ID     domain.PauseID
	Key    PauseKey
	Status domain.PauseStatus
	Reason TriggerReason
	// Persist is the durable phase-progress bookkeeping runtime-a05's
	// PersistPhase orchestrator (persistphase.go) reads and updates via
	// PersistPauseStore.GetProgress/SaveProgress. Zero value (no field set)
	// means the persist phase has not been attempted yet for this record.
	// Embedding it directly on PauseRecord — rather than a separate
	// parallel map keyed by PauseID — keeps this package's one durable
	// record type as the single source of truth for a pause's full
	// lifecycle state, matching Constitution Sec 6's "state writes are
	// atomic, idempotent, and crash-recoverable" applied to this package's
	// own store, not just the cross-component stores it orchestrates.
	Persist PersistProgress
}

// PauseStore is the narrow persistence port RequestPause needs. It is
// declared HERE (internal/pause), not in internal/app/ports.go, because
// CONTRACT_FREEZE.md's frozen GracefulPauseService already covers the
// cross-component boundary (RequestPause/ReachSafePoint/etc.) — this
// interface is an internal implementation seam behind that boundary,
// letting runtime-a04 land against an in-memory fake today (per the DAG's
// own note: "no concrete store required to begin") and runtime-a05 supply
// a real SQLite-backed implementation later without this package's public
// API changing.
type PauseStore interface {
	// FindActiveByKey returns the current, non-terminal pause record for
	// key, if one exists. found is false if no such record exists (not an
	// error) — this is what makes RequestPause idempotent: a second call
	// with the same key finds the first call's record instead of
	// creating a new one.
	FindActiveByKey(ctx context.Context, key PauseKey) (rec PauseRecord, found bool, err error)
	// Insert durably creates a brand new pause record (always at
	// domain.PausePredicted, this package's entry state — see doc.go).
	// Callers must have already confirmed via FindActiveByKey that no
	// active record exists for rec.Key; Insert itself does not
	// re-check (single-writer-per-call discipline; concurrent-safety
	// across processes is a real SQLite-backed store's concern, deferred
	// to runtime-a05 per the DAG note).
	Insert(ctx context.Context, rec PauseRecord) error
	// GetByID returns the pause record identified by id, regardless of
	// whether it is terminal (unlike FindActiveByKey, which deliberately
	// hides terminal records so a fresh RequestPause can create a new one
	// for the same key). Callers that already have a concrete PauseID
	// (e.g. runtime-b07's `pause cancel`/`resume` CLI commands, which take
	// a --pause-id flag rather than re-deriving a PauseKey) need the
	// record regardless of its current lifecycle state. found is false if
	// no record with this ID exists at all.
	GetByID(ctx context.Context, id domain.PauseID) (rec PauseRecord, found bool, err error)
	// UpdateStatus durably transitions an existing record's Status field.
	// Callers are expected to have already validated the transition via
	// pause.Apply before calling this — UpdateStatus itself performs no
	// state-machine validation, it only persists whatever status the
	// caller supplies (mirrors SetStatus's existing test-only convenience,
	// promoted here to the frozen interface so a real caller — not just a
	// test — can drive a lifecycle transition through the same store
	// RequestPause/Persist already use).
	UpdateStatus(ctx context.Context, id domain.PauseID, status domain.PauseStatus) error
}

// RequestPauseRequest is RequestPause's input.
type RequestPauseRequest struct {
	Key    PauseKey
	Reason TriggerReason
}

// RequestPauseResult reports both the resulting record and whether this
// call actually created it (Created: false means an existing in-flight
// pause was returned unchanged — the idempotent-replay case).
type RequestPauseResult struct {
	Record  PauseRecord
	Created bool
}

// RequestPause implements agents/runtime.md Part A deliverable 3:
// "RequestPause idempotency." Calling RequestPause more than once for the
// same PauseKey while a pause is already in flight (any non-terminal
// domain.PauseStatus) must not create a duplicate pause_records row and
// must not double-transition the state machine — the second (and every
// subsequent) call simply returns the existing record with Created:
// false.
//
// A different Reason on a replay is NOT treated as a conflict to reject
// (unlike CONTRACT_FREEZE.md's CompleteNodeRequest.IdempotencyKey rule,
// where a differing payload under the same key IS a conflict): an
// emergency sample arriving while a calibrated pause is already
// in-flight for the same task/session is a real, expected upgrade signal
// (ADD §17.6's emergency path "can skip the double-sample" precisely so
// it can escalate an already-observing situation faster), not a
// programming error. RequestPause therefore returns the existing record
// as-is on replay regardless of Reason; escalating an in-flight pause's
// urgency is a later node's policy decision (e.g. runtime-a05's persist
// orchestration shortening the quiesce timeout), not this function's.
func RequestPause(ctx context.Context, store PauseStore, ids domain.IDGenerator, req RequestPauseRequest) (RequestPauseResult, error) {
	if req.Key.TaskID == "" {
		return RequestPauseResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: RequestPause requires a TaskID", Retryable: false,
		}
	}
	if req.Key.SessionID == "" {
		return RequestPauseResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: RequestPause requires a SessionID", Retryable: false,
		}
	}
	if req.Reason == "" {
		return RequestPauseResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: RequestPause requires a Reason", Retryable: false,
		}
	}

	existing, found, err := store.FindActiveByKey(ctx, req.Key)
	if err != nil {
		return RequestPauseResult{}, err
	}
	if found {
		// Idempotent replay: return the record that already exists,
		// unchanged. No Insert call, no state-machine Apply call — this
		// is the core of the required behavior, not merely "don't error
		// twice."
		return RequestPauseResult{Record: existing, Created: false}, nil
	}

	// No active pause for this key: this call actually creates one.
	// Apply is invoked to prove the transition is legal from this
	// package's one true entry point (domain.PausePredicted has no
	// incoming edge in transitionTable by design — see doc.go — so a
	// freshly-created record simply STARTS there; Apply is not called
	// for the create step itself, only used by later phases). The state
	// value stored is exactly the entry state.
	rec := PauseRecord{
		ID:     domain.PauseID(ids.NewID()),
		Key:    req.Key,
		Status: domain.PausePredicted,
		Reason: req.Reason,
	}
	if err := store.Insert(ctx, rec); err != nil {
		return RequestPauseResult{}, err
	}
	return RequestPauseResult{Record: rec, Created: true}, nil
}

// --- In-memory PauseStore (reference implementation + test double) --------

// MemStore is a simple in-memory PauseStore, safe for concurrent use. It
// is both a usable default for callers that don't need cross-process
// durability yet and this package's own test double for RequestPause's
// required tests — runtime-a05 is expected to add a SQLite-backed
// PauseStore against the same interface without changing RequestPause's
// signature.
type MemStore struct {
	mu      sync.Mutex
	records map[PauseKey]PauseRecord
}

// NewMemStore constructs an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{records: make(map[PauseKey]PauseRecord)}
}

var _ PauseStore = (*MemStore)(nil)

func (m *MemStore) FindActiveByKey(_ context.Context, key PauseKey) (PauseRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[key]
	if !ok || IsTerminal(rec.Status) {
		return PauseRecord{}, false, nil
	}
	return rec, true, nil
}

func (m *MemStore) Insert(_ context.Context, rec PauseRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[rec.Key] = rec
	return nil
}

var _ PersistPauseStore = (*MemStore)(nil)

// findByID is MemStore's own linear scan from PauseID back to the record's
// PauseKey (the map's actual index). MemStore is a reference/test double,
// not a performance-sensitive production store (a real SQLite-backed
// PauseStore would simply index by PauseID as its primary key instead — see
// this package's persistphase_test.go harness, which layers exactly that
// indexing on top of a real sqlite.DB-backed store for the crash-injection
// tests), so a scan here is an acceptable, deliberately simple tradeoff.
func (m *MemStore) findByID(id domain.PauseID) (PauseKey, PauseRecord, bool) {
	for key, rec := range m.records {
		if rec.ID == id {
			return key, rec, true
		}
	}
	return PauseKey{}, PauseRecord{}, false
}

// GetProgress implements PersistPauseStore.
func (m *MemStore) GetProgress(_ context.Context, id domain.PauseID) (PersistProgress, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, rec, ok := m.findByID(id)
	if !ok {
		return PersistProgress{}, false, nil
	}
	return rec.Persist, true, nil
}

// SaveProgress implements PersistPauseStore.
func (m *MemStore) SaveProgress(_ context.Context, id domain.PauseID, progress PersistProgress) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, rec, ok := m.findByID(id)
	if !ok {
		return &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: SaveProgress: pause record %q not found", id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}
	rec.Persist = progress
	m.records[key] = rec
	return nil
}

// SetStatus is a test/caller convenience for advancing a stored record's
// status directly (e.g. to simulate a later phase's Apply result being
// persisted, or to mark a record terminal so a subsequent RequestPause
// call for the same key is free to create a new one). Not part of the
// PauseStore interface — MemStore-specific, since a real store would
// expose this through its own update path (a later node's concern).
func (m *MemStore) SetStatus(key PauseKey, status domain.PauseStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[key]; ok {
		rec.Status = status
		m.records[key] = rec
	}
}

// GetByID implements PauseStore.GetByID.
func (m *MemStore) GetByID(_ context.Context, id domain.PauseID) (PauseRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, rec, ok := m.findByID(id)
	return rec, ok, nil
}

// UpdateStatus implements PauseStore.UpdateStatus.
func (m *MemStore) UpdateStatus(_ context.Context, id domain.PauseID, status domain.PauseStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, rec, ok := m.findByID(id)
	if !ok {
		return &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: UpdateStatus: pause record %q not found", id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}
	rec.Status = status
	m.records[key] = rec
	return nil
}
