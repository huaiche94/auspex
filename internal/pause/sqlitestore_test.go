// sqlitestore_test.go proves pause.SQLiteStore satisfies the same
// PauseStore behavioral contract MemStore's own existing tests already
// establish (requestpause_test.go, wake_test.go), but against a REAL,
// migrated, on-disk SQLite database — this is the direct unit-level half of
// runtime-b10's restart-safety proof; restart_test.go (this same package)
// proves the end-to-end "discard the App, build a new one against the same
// file" scenario built on top of this store.
package pause_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/pause"
)

func newSeededSQLiteStore(t *testing.T) (*pause.SQLiteStore, pause.PauseKey) {
	t.Helper()
	db := openMigratedDB(t)
	seedChain(t, db, "wt1", "task1")
	return pause.NewSQLiteStore(db), pause.PauseKey{TaskID: "task1", SessionID: "sess1"}
}

func TestSQLiteStore_ImplementsPauseStore(t *testing.T) {
	var _ pause.PauseStore = (*pause.SQLiteStore)(nil)
}

func TestSQLiteStore_InsertThenGetByID_RoundTrips(t *testing.T) {
	store, key := newSeededSQLiteStore(t)
	ctx := context.Background()

	rec := pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PausePredicted, Reason: pause.TriggerReasonCalibrated}
	if err := store.Insert(ctx, rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, found, err := store.GetByID(ctx, "pause-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if got.ID != rec.ID || got.Key != rec.Key || got.Status != rec.Status || got.Reason != rec.Reason {
		t.Fatalf("GetByID = %+v, want %+v", got, rec)
	}
}

func TestSQLiteStore_GetByID_UnknownIDNotFoundNotError(t *testing.T) {
	store, _ := newSeededSQLiteStore(t)
	_, found, err := store.GetByID(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if found {
		t.Fatal("found = true, want false for an unknown ID")
	}
}

func TestSQLiteStore_FindActiveByKey_FindsNonTerminalRecord(t *testing.T) {
	store, key := newSeededSQLiteStore(t)
	ctx := context.Background()
	if err := store.Insert(ctx, pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PausePredicted}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rec, found, err := store.FindActiveByKey(ctx, key)
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if !found || rec.ID != "pause-1" {
		t.Fatalf("FindActiveByKey = (%+v, %v), want pause-1 found", rec, found)
	}
}

func TestSQLiteStore_FindActiveByKey_HidesTerminalRecord(t *testing.T) {
	store, key := newSeededSQLiteStore(t)
	ctx := context.Background()
	if err := store.Insert(ctx, pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PauseResumed}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	_, found, err := store.FindActiveByKey(ctx, key)
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if found {
		t.Fatal("found = true, want false — a terminal (Resumed) record must not be returned as active")
	}
}

func TestSQLiteStore_FindActiveByKey_UnknownKeyNotFoundNotError(t *testing.T) {
	store, _ := newSeededSQLiteStore(t)
	_, found, err := store.FindActiveByKey(context.Background(), pause.PauseKey{TaskID: "no-such-task", SessionID: "sess1"})
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if found {
		t.Fatal("found = true, want false for a key with no rows at all")
	}
}

func TestSQLiteStore_UpdateStatus_PersistsNewStatus(t *testing.T) {
	store, key := newSeededSQLiteStore(t)
	ctx := context.Background()
	if err := store.Insert(ctx, pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PausePredicted}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.UpdateStatus(ctx, "pause-1", domain.PauseRequested); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	rec, _, err := store.GetByID(ctx, "pause-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if rec.Status != domain.PauseRequested {
		t.Fatalf("Status = %q, want %q", rec.Status, domain.PauseRequested)
	}
}

func TestSQLiteStore_UpdateStatus_UnknownIDReturnsNotFound(t *testing.T) {
	store, _ := newSeededSQLiteStore(t)
	err := store.UpdateStatus(context.Background(), "does-not-exist", domain.PauseRequested)
	if err == nil {
		t.Fatal("UpdateStatus on an unknown ID: want an error, got nil")
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.ErrCodeNotFound {
		t.Fatalf("err = %v, want a domain.Error with ErrCodeNotFound", err)
	}
}

func TestSQLiteStore_CompareAndSwapStatus_SucceedsWhenExpectedMatches(t *testing.T) {
	store, key := newSeededSQLiteStore(t)
	ctx := context.Background()
	if err := store.Insert(ctx, pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PauseSleeping}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	ok, found, err := store.CompareAndSwapStatus(ctx, "pause-1", domain.PauseSleeping, domain.PauseWakePending)
	if err != nil {
		t.Fatalf("CompareAndSwapStatus: %v", err)
	}
	if !found || !ok {
		t.Fatalf("found=%v ok=%v, want both true", found, ok)
	}
	rec, _, _ := store.GetByID(ctx, "pause-1")
	if rec.Status != domain.PauseWakePending {
		t.Fatalf("Status = %q, want %q", rec.Status, domain.PauseWakePending)
	}
}

func TestSQLiteStore_CompareAndSwapStatus_FailsWhenExpectedStale(t *testing.T) {
	store, key := newSeededSQLiteStore(t)
	ctx := context.Background()
	if err := store.Insert(ctx, pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PauseWakePending}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	ok, found, err := store.CompareAndSwapStatus(ctx, "pause-1", domain.PauseSleeping, domain.PauseWakePending)
	if err != nil {
		t.Fatalf("CompareAndSwapStatus: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true (record exists)")
	}
	if ok {
		t.Fatal("ok = true, want false (expected status was stale)")
	}
	rec, _, _ := store.GetByID(ctx, "pause-1")
	if rec.Status != domain.PauseWakePending {
		t.Fatalf("Status = %q, want unchanged %q", rec.Status, domain.PauseWakePending)
	}
}

func TestSQLiteStore_CompareAndSwapStatus_UnknownIDReportsNotFound(t *testing.T) {
	store, _ := newSeededSQLiteStore(t)
	ok, found, err := store.CompareAndSwapStatus(context.Background(), "does-not-exist", domain.PauseSleeping, domain.PauseWakePending)
	if err != nil {
		t.Fatalf("CompareAndSwapStatus: %v", err)
	}
	if found || ok {
		t.Fatalf("found=%v ok=%v, want both false for an unknown ID", found, ok)
	}
}

// TestSQLiteStore_CompareAndSwapStatus_ConcurrentCallersSerializeCorrectly
// mirrors wake_test.go's identical MemStore proof exactly, against the REAL
// SQLite conditional UPDATE this time: many goroutines racing the same CAS
// on the same row, against the same on-disk file (WAL mode + busy_timeout,
// internal/storage/sqlite/db.go), must still yield exactly one winner — the
// storage layer's own single-writer-per-commit semantics are what this test
// actually exercises, not any locking this package adds on top.
func TestSQLiteStore_CompareAndSwapStatus_ConcurrentCallersSerializeCorrectly(t *testing.T) {
	const attempts = 10
	for attempt := 0; attempt < attempts; attempt++ {
		store, key := newSeededSQLiteStore(t)
		ctx := context.Background()
		if err := store.Insert(ctx, pause.PauseRecord{ID: "pause-1", Key: key, Status: domain.PauseSleeping}); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		const workers = 20
		var wg sync.WaitGroup
		var wins atomic.Int64
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ok, found, err := store.CompareAndSwapStatus(ctx, "pause-1", domain.PauseSleeping, domain.PauseWakePending)
				if err != nil {
					t.Errorf("CompareAndSwapStatus: %v", err)
					return
				}
				if !found {
					t.Errorf("found = false, want true")
					return
				}
				if ok {
					wins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got := wins.Load(); got != 1 {
			t.Fatalf("attempt %d: wins = %d, want exactly 1", attempt, got)
		}
	}
}
