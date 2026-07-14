// tx_immediate_test.go: issue #39's regression test. WithTx transactions
// must be IMMEDIATE (write lock at BEGIN): a DEFERRED read-then-write
// transaction in WAL mode dies with SQLITE_BUSY_SNAPSHOT ("database is
// locked (517)") whenever another writer commits between its first read
// and first write — busy_timeout cannot save it, because its read
// snapshot is already stale. Under _txlock=immediate, contending writers
// queue at BEGIN under busy_timeout and serialize cleanly.
//
// This test is the distilled shape of the production failure
// (evaluation's ConsumeAuthorization: SELECT the row, then conditionally
// UPDATE it, under 64-way contention on a slow windows-latest runner):
// N goroutines each read a counter and write back read+1 inside one
// WithTx. Deferred mode fails it two ways — spurious busy errors, and,
// if those were retried naively, lost updates; immediate mode must
// produce zero errors AND a final count of exactly N.
package sqlite_test

import (
	"context"
	"sync"
	"testing"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

func TestWithTx_ConcurrentReadThenWrite_SerializesWithoutBusySnapshot(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.Conn().ExecContext(ctx, `CREATE TABLE tx_counter (id INTEGER PRIMARY KEY, n INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Conn().ExecContext(ctx, `INSERT INTO tx_counter (id, n) VALUES (1, 0)`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	const workers = 32
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.WithTx(ctx, func(txCtx context.Context) error {
				q := sqlite.QuerierFromContext(txCtx, db)
				var cur int
				if err := q.QueryRowContext(txCtx, `SELECT n FROM tx_counter WHERE id = 1`).Scan(&cur); err != nil {
					return err
				}
				_, err := q.ExecContext(txCtx, `UPDATE tx_counter SET n = ? WHERE id = 1`, cur+1)
				return err
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: WithTx failed under contention (deferred-BEGIN busy-snapshot regression): %v", i, err)
		}
	}

	var final int
	if err := db.Conn().QueryRowContext(ctx, `SELECT n FROM tx_counter WHERE id = 1`).Scan(&final); err != nil {
		t.Fatalf("read final count: %v", err)
	}
	// Exactly N proves the transactions truly serialized (each read saw
	// the previous writer's commit) — not merely that nobody errored.
	if final != workers {
		t.Fatalf("final counter = %d, want %d (lost update: transactions overlapped instead of serializing)", final, workers)
	}
}
