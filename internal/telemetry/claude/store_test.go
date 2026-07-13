package claude_test

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claude "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// seqIDs mirrors normalizer_test.go's deterministic domain.IDGenerator
// fake; duplicated here (rather than exported from normalizer_test.go,
// which is package claude, not claude_test) since this file deliberately
// tests EventStore from an external, black-box package (claude_test) to
// exercise only its exported surface, matching the "Idempotent" validation
// command's expectation of a self-contained, fixture-independent test.
type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "id-" + strconv.Itoa(s.n)
}

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("sqlite.AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return db
}

func sampleEvent(ids *seqIDs, idempotencyKey string, occurredAt time.Time) v1.Event {
	return v1.Event{
		SchemaVersion:  v1.SchemaVersionEvent,
		EventID:        ids.NewID(),
		EventType:      v1.EventProviderUsageObserved,
		OccurredAt:     occurredAt,
		ObservedAt:     occurredAt,
		IdempotencyKey: idempotencyKey,
		Source:         string(domain.SourceStatusLine),
		Provider:       "claude",
		SessionID:      "session-1",
		Payload: map[string]any{
			"total_cost_usd": 1.5,
		},
	}
}

// --- Duplicate-write idempotency (DAG validation command: `-run Idempotent`) -

func TestIdempotent_DuplicateEventSameKey_NoDuplicateRow(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ids := &seqIDs{}
	ctx := context.Background()

	occurredAt := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	key := "fixed-idempotency-key"

	first := sampleEvent(ids, key, occurredAt)
	second := sampleEvent(ids, key, occurredAt) // distinct EventID, same key

	if err := store.PersistAll(ctx, db, []v1.Event{first}); err != nil {
		t.Fatalf("first PersistAll: %v", err)
	}
	if err := store.PersistAll(ctx, db, []v1.Event{second}); err != nil {
		t.Fatalf("second PersistAll (duplicate key): %v", err)
	}

	count, err := store.CountByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatalf("CountByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count for idempotency key = %d, want 1 (no duplicate)", count)
	}

	// The stored row must be the FIRST event's row, unmodified — a
	// duplicate write is a no-op, not a silent overwrite (Constitution
	// §6 rule 6: "Duplicate completion with conflicting evidence is
	// rejected, not silently merged or overwritten" — the same
	// discipline applies to event idempotency per CONTRACT_FREEZE.md).
	stored, err := store.GetByEventID(ctx, first.EventID)
	if err != nil {
		t.Fatalf("GetByEventID(first): %v", err)
	}
	if stored.EventID != first.EventID {
		t.Errorf("stored EventID = %q, want %q (original row must survive)", stored.EventID, first.EventID)
	}

	// The second event's own EventID must NOT have been written as a
	// separate row, since ON CONFLICT(idempotency_key) DO NOTHING
	// short-circuits the whole insert for that row.
	if _, err := store.GetByEventID(ctx, second.EventID); err == nil {
		t.Errorf("GetByEventID(second) succeeded, want ErrEventNotFound (duplicate must not create a second row)")
	}
}

func TestIdempotent_SamePersistCallTwice_ReturnsNilBothTimes(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ids := &seqIDs{}
	ctx := context.Background()

	ev := sampleEvent(ids, "replay-key", time.Now().UTC())

	// Persisting the exact same v1.Event value (same EventID, same
	// IdempotencyKey) twice must succeed both times with no error — the
	// caller (e.g. a hook re-fired after a crash mid-write) should never
	// have to distinguish "first time" from "replay" itself.
	if err := store.PersistAll(ctx, db, []v1.Event{ev}); err != nil {
		t.Fatalf("first Persist: %v", err)
	}
	if err := store.PersistAll(ctx, db, []v1.Event{ev}); err != nil {
		t.Fatalf("replayed Persist (identical event): %v", err)
	}

	count, err := store.CountByIdempotencyKey(ctx, "replay-key")
	if err != nil {
		t.Fatalf("CountByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
}

// --- Out-of-order delivery must not break idempotency ----------------------

func TestIdempotent_OutOfOrderDelivery_BothPersistIndependently(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ids := &seqIDs{}
	ctx := context.Background()

	earlier := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 7, 12, 12, 5, 0, 0, time.UTC)

	// Two logically distinct observations (different OccurredAt ->
	// different digestKey/IdempotencyKey per normalizer.go's own
	// algorithm), delivered to the store with the LATER one arriving
	// first — e.g. a retried hook invocation racing a fresh one.
	laterEvent := sampleEvent(ids, "quota.session-1.five_hour."+later.Format(time.RFC3339Nano), later)
	earlierEvent := sampleEvent(ids, "quota.session-1.five_hour."+earlier.Format(time.RFC3339Nano), earlier)

	if err := store.PersistAll(ctx, db, []v1.Event{laterEvent}); err != nil {
		t.Fatalf("persisting later event first: %v", err)
	}
	if err := store.PersistAll(ctx, db, []v1.Event{earlierEvent}); err != nil {
		t.Fatalf("persisting earlier event second: %v", err)
	}

	// Both distinct observations must be durably present — out-of-order
	// arrival must not cause the earlier one to be dropped, rejected, or
	// merged into the later one's row.
	if _, err := store.GetByEventID(ctx, laterEvent.EventID); err != nil {
		t.Errorf("GetByEventID(laterEvent): %v", err)
	}
	if _, err := store.GetByEventID(ctx, earlierEvent.EventID); err != nil {
		t.Errorf("GetByEventID(earlierEvent): %v", err)
	}
}

func TestIdempotent_OutOfOrderDuplicateRedelivery_StillDeduplicates(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ids := &seqIDs{}
	ctx := context.Background()

	t0 := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	key := "turn-started-key"

	// Simulate three deliveries of logically the same event (same
	// IdempotencyKey) arriving out of process order: A, then a replay of
	// A, then another replay of A. None should produce more than one row,
	// regardless of delivery order or how many times it is redelivered.
	a1 := sampleEvent(ids, key, t0)
	a2 := sampleEvent(ids, key, t0)
	a3 := sampleEvent(ids, key, t0)

	deliveries := [][]v1.Event{{a2}, {a1}, {a3}, {a1}} // shuffled + repeated
	for i, batch := range deliveries {
		if err := store.PersistAll(ctx, db, batch); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}

	count, err := store.CountByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatalf("CountByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after shuffled redelivery = %d, want 1", count)
	}
}

// --- Concurrent duplicate writes (idempotency under real transaction
// contention, not just sequential calls) ------------------------------------

func TestIdempotent_ConcurrentDuplicateWrites_NoDuplicateRow(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ctx := context.Background()
	key := "concurrent-key"

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids := &seqIDs{n: i * 1000} // distinct EventIDs per goroutine
			ev := sampleEvent(ids, key, time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC))
			errs[i] = store.PersistAll(ctx, db, []v1.Event{ev})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d PersistAll: %v", i, err)
		}
	}

	count, err := store.CountByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatalf("CountByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after %d concurrent duplicate writes = %d, want 1", n, count)
	}
}

// --- Non-duplicate events with distinct keys persist as distinct rows ------

func TestIdempotent_DistinctKeys_BothPersist(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ids := &seqIDs{}
	ctx := context.Background()

	t0 := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	evA := sampleEvent(ids, "key-a", t0)
	evB := sampleEvent(ids, "key-b", t0)

	if err := store.PersistAll(ctx, db, []v1.Event{evA, evB}); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	countA, err := store.CountByIdempotencyKey(ctx, "key-a")
	if err != nil {
		t.Fatalf("CountByIdempotencyKey(key-a): %v", err)
	}
	countB, err := store.CountByIdempotencyKey(ctx, "key-b")
	if err != nil {
		t.Fatalf("CountByIdempotencyKey(key-b): %v", err)
	}
	if countA != 1 || countB != 1 {
		t.Fatalf("counts = (%d, %d), want (1, 1)", countA, countB)
	}
}

// --- Payload/field round-trip (guards against corruption on duplicate path) -

func TestIdempotent_PayloadNotCorruptedAfterDuplicateAttempt(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ids := &seqIDs{}
	ctx := context.Background()

	t0 := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	key := "payload-key"
	ev := sampleEvent(ids, key, t0)
	ev.Payload = map[string]any{
		"total_cost_usd":    2.75,
		"total_duration_ms": float64(4200),
	}

	dup := sampleEvent(ids, key, t0)
	dup.Payload = map[string]any{
		"total_cost_usd": 999.0, // must never win: original row is authoritative
	}

	if err := store.PersistAll(ctx, db, []v1.Event{ev}); err != nil {
		t.Fatalf("PersistAll(original): %v", err)
	}
	if err := store.PersistAll(ctx, db, []v1.Event{dup}); err != nil {
		t.Fatalf("PersistAll(duplicate): %v", err)
	}

	stored, err := store.GetByEventID(ctx, ev.EventID)
	if err != nil {
		t.Fatalf("GetByEventID: %v", err)
	}
	if got := stored.Payload["total_cost_usd"]; got != 2.75 {
		t.Errorf("stored payload total_cost_usd = %v, want 2.75 (must not be corrupted by duplicate attempt)", got)
	}
	if got := stored.Payload["total_duration_ms"]; got != float64(4200) {
		t.Errorf("stored payload total_duration_ms = %v, want 4200", got)
	}
}

// --- Not-found path ----------------------------------------------------------

func TestGetByEventID_NotFound(t *testing.T) {
	db := openTestDB(t)
	store := claude.NewEventStore(db)
	ctx := context.Background()

	if _, err := store.GetByEventID(ctx, "does-not-exist"); err == nil {
		t.Error("expected ErrEventNotFound, got nil")
	}
}
