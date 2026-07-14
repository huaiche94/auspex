// openturn_test.go: issue #11 turn correlation — OpenTurnStore's lookup
// semantics against a real migrated DB, and the end-to-end proof that a
// Stop hook's terminal event lands in the events table carrying the
// turn_id a prior turn.started stamped (the join-activation this whole
// increment exists for).
package orchestrator_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

func openTurnTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "auspex.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func insertStartedEvent(t *testing.T, db *sqlite.DB, eventID, sessionID, turnID, occurredAt string) {
	t.Helper()
	var turn any
	if turnID != "" {
		turn = turnID
	}
	if _, err := db.Conn().ExecContext(context.Background(), `
		INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, payload_json)
		VALUES (?, 'auspex.event.v1', 'provider.turn.started', ?, ?, 'hook', 'claude', ?, ?, '{}')`,
		eventID, occurredAt, occurredAt, sessionID, turn,
	); err != nil {
		t.Fatalf("insert started event: %v", err)
	}
}

func TestOpenTurnStore_LatestStartedWins(t *testing.T) {
	db := openTurnTestDB(t)
	store := &orchestrator.OpenTurnStore{DB: db}
	ctx := context.Background()

	// Two started turns, second one newer; an unlabeled started (no
	// turn_id — pre-#14 shape) newest of all must NOT win: it carries
	// nothing to correlate against.
	insertStartedEvent(t, db, "ev-1", "sess-1", "turn-1", "2026-07-14T10:00:00Z")
	insertStartedEvent(t, db, "ev-2", "sess-1", "turn-2", "2026-07-14T10:05:00Z")
	insertStartedEvent(t, db, "ev-3", "sess-1", "", "2026-07-14T10:10:00Z")
	insertStartedEvent(t, db, "ev-other", "sess-2", "turn-other", "2026-07-14T10:20:00Z")

	turnID, ok := store.LatestStartedTurn(ctx, "sess-1")
	if !ok || turnID != "turn-2" {
		t.Fatalf("LatestStartedTurn = (%q, %v), want (turn-2, true)", turnID, ok)
	}

	if _, ok := store.LatestStartedTurn(ctx, "sess-unknown"); ok {
		t.Fatal("unknown session must resolve to ok=false")
	}

	var nilStore *orchestrator.OpenTurnStore
	if _, ok := nilStore.LatestStartedTurn(ctx, "sess-1"); ok {
		t.Fatal("nil store must be fail-open ok=false")
	}
}

func TestHandleStop_EndToEnd_TerminalEventCarriesStartedTurnID(t *testing.T) {
	// The activation proof: a persisted turn.started (as the
	// UserPromptSubmit hook writes it) followed by a real HandleStop
	// through the real persister yields a provider.turn.completed ROW
	// whose turn_id matches — exactly what retention's
	// lookupTurnOutcomes joins on (actual_known flips to 1 from here).
	db := openTurnTestDB(t)
	ctx := context.Background()
	insertStartedEvent(t, db, "ev-start", "sess_e2e", "turn-e2e-1", "2026-07-14T10:00:00Z")

	deps := orchestrator.HookDeps{
		Clock:     fixedClock{t: time.Date(2026, 7, 14, 10, 6, 0, 0, time.UTC)},
		IDs:       &sequentialHookIDs{},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
		OpenTurns: &orchestrator.OpenTurnStore{DB: db},
	}

	result, err := orchestrator.HandleStop(ctx, deps, []byte(`{"session_id":"sess_e2e","stop_hook_active":false}`))
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if !result.Persisted {
		t.Fatal("expected the stop event to persist")
	}

	var turnID sql.NullString
	err = db.Conn().QueryRowContext(ctx, `
		SELECT turn_id FROM events
		WHERE session_id = 'sess_e2e' AND event_type = 'provider.turn.completed'
		ORDER BY rowid DESC LIMIT 1`).Scan(&turnID)
	if err != nil {
		t.Fatalf("read persisted completed event: %v", err)
	}
	if !turnID.Valid || turnID.String != "turn-e2e-1" {
		t.Fatalf("persisted turn.completed turn_id = %v, want turn-e2e-1", turnID)
	}
}
