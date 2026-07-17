// watcher_test.go: the issue-#92 watcher's behavioral suite over the
// synthetic watch fixtures (testdata/provider-events/codex/watch/*,
// numbers-only) and a real migrated SQLite store — offset tracking across
// ticks, token_count -> usage/quota/context projection, vscode/subagent
// attribution, hook+watcher dedupe in both orders, legacy no-turn-id
// determinism, torn-tail resume, persist-failure retry, restart re-scan
// idempotency, and the raw-DB privacy grep.
package rolloutwatch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/rolloutwatch"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- fixture ids (must match testdata/provider-events/codex/watch/*) ------

const (
	cliSession   = "019fa000-0000-7000-8000-000000000001"
	cliTurnA     = "019fa000-0000-7000-8000-00000000a101"
	cliTurnB     = "019fa000-0000-7000-8000-00000000a102"
	vscodeSess   = "019fa000-0000-7000-8000-000000000002"
	vscodeTurn   = "019fa000-0000-7000-8000-00000000a201"
	subagentSess = "019fa000-0000-7000-8000-000000000003"
	subagentTurn = "019fa000-0000-7000-8000-00000000a301"
	parentSess   = "019fa000-0000-7000-8000-00000000000f"
	msgTextSess  = "019fa000-0000-7000-8000-000000000004"
	watchNeedle  = "WATCH-SECRET"
)

// --- doubles ----------------------------------------------------------------

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type seqIDs struct {
	prefix string
	n      int
}

func (s *seqIDs) NewID() string {
	s.n++
	return s.prefix + "-" + strconv.Itoa(s.n)
}

// failOncePersister fails its first PersistAll call, then delegates —
// proving a failed persist advances nothing and the next tick re-emits
// losslessly.
type failOncePersister struct {
	inner  rolloutwatch.Persister
	failed bool
}

func (p *failOncePersister) PersistAll(ctx context.Context, runner app.TxRunner, evs []v1.Event) error {
	if !p.failed {
		p.failed = true
		return errors.New("injected persist failure")
	}
	return p.inner.PersistAll(ctx, runner, evs)
}

// --- helpers ----------------------------------------------------------------

func openTestDB(t *testing.T) *sqlite.DB {
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

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "codex", "watch", name))
	if err != nil {
		t.Fatalf("reading watch fixture %s: %v", name, err)
	}
	return b
}

// stageRollout writes content as a rollout file for sessionID under the
// standard YYYY/MM/DD layout and returns its path.
func stageRollout(t *testing.T, sessionsDir, sessionID string, content []byte) string {
	t.Helper()
	dir := filepath.Join(sessionsDir, "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "rollout-2026-07-14T09-00-00-"+sessionID+".jsonl")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func appendToFile(t *testing.T, path string, content []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile append: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(content); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func newWatcher(t *testing.T, db *sqlite.DB, sessionsDir string) *rolloutwatch.Watcher {
	t.Helper()
	w, err := rolloutwatch.New(rolloutwatch.Config{SessionsDir: sessionsDir}, rolloutwatch.Deps{
		Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{prefix: "watch"},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	})
	if err != nil {
		t.Fatalf("rolloutwatch.New: %v", err)
	}
	return w
}

type storedEvent struct {
	EventType      string
	Source         string
	SessionID      string
	TurnID         string
	IdempotencyKey string
	Payload        map[string]any
}

func loadEvents(t *testing.T, db *sqlite.DB) []storedEvent {
	t.Helper()
	rows, err := db.Conn().Query(`
		SELECT event_type, source, COALESCE(session_id,''), COALESCE(turn_id,''),
		       COALESCE(idempotency_key,''), payload_json
		FROM events ORDER BY rowid`)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []storedEvent
	for rows.Next() {
		var ev storedEvent
		var payloadJSON string
		if err := rows.Scan(&ev.EventType, &ev.Source, &ev.SessionID, &ev.TurnID, &ev.IdempotencyKey, &payloadJSON); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if err := json.Unmarshal([]byte(payloadJSON), &ev.Payload); err != nil {
			t.Fatalf("payload decode: %v", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func eventsOf(evs []storedEvent, eventType, turnID string) []storedEvent {
	var out []storedEvent
	for _, ev := range evs {
		if ev.EventType == eventType && (turnID == "" || ev.TurnID == turnID) {
			out = append(out, ev)
		}
	}
	return out
}

func wantNum(t *testing.T, payload map[string]any, key string, want float64) {
	t.Helper()
	got, ok := payload[key].(float64)
	if !ok || got != want {
		t.Errorf("payload[%q] = %v, want %v", key, payload[key], want)
	}
}

// --- tests -------------------------------------------------------------------

func TestNew_Validation(t *testing.T) {
	if _, err := rolloutwatch.New(rolloutwatch.Config{}, rolloutwatch.Deps{}); err == nil {
		t.Fatal("New with no SessionsDir/deps must fail closed")
	}
}

// TestScanOnce_TokenCountsBecomeUsageQuotaContext is the core projection
// test: two turns' rollout lines become two turn.completed (with the
// per-turn actuals under the shared token-key vocabulary), two
// context.observed, and four quota.observed events, all attributed.
func TestScanOnce_TokenCountsBecomeUsageQuotaContext(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	stageRollout(t, sessions, cliSession, fixtureBytes(t, "cli_two_turns.jsonl"))

	w := newWatcher(t, db, sessions)
	stats := w.ScanOnce(context.Background())
	if stats.TurnsEmitted != 2 || stats.EventsEmitted != 8 || stats.Errors != 0 {
		t.Fatalf("stats = %+v, want 2 turns / 8 events / 0 errors", stats)
	}

	evs := loadEvents(t, db)
	if len(evs) != 8 {
		t.Fatalf("stored %d events, want 8", len(evs))
	}

	// Turn A's terminal accounting comes from the LAST token_count inside
	// the turn (input 12000 incl. 8000 cached, output 500 incl. 100
	// reasoning) under the shared vocabulary: fresh input only.
	completedA := eventsOf(evs, string(v1.EventProviderTurnCompleted), cliTurnA)
	if len(completedA) != 1 {
		t.Fatalf("turn A completed rows = %d, want 1", len(completedA))
	}
	p := completedA[0].Payload
	wantNum(t, p, "input_tokens", 4000)
	wantNum(t, p, "cache_read_input_tokens", 8000)
	wantNum(t, p, "output_tokens", 500)
	wantNum(t, p, "reasoning_output_tokens", 100)
	wantNum(t, p, "total_tokens", 4500)
	if p["model_id"] != "gpt-5.2-codex" {
		t.Errorf("model_id = %v (from the turn_context line)", p["model_id"])
	}
	if p["originator"] != "codex-tui" || p["surface"] != "cli" {
		t.Errorf("attribution = %v/%v, want codex-tui/cli", p["originator"], p["surface"])
	}
	if completedA[0].SessionID != cliSession {
		t.Errorf("session_id = %q", completedA[0].SessionID)
	}

	ctxA := eventsOf(evs, string(v1.EventProviderContextObserved), cliTurnA)
	if len(ctxA) != 1 {
		t.Fatalf("turn A context rows = %d, want 1", len(ctxA))
	}
	wantNum(t, ctxA[0].Payload, "used_tokens", 12500)
	wantNum(t, ctxA[0].Payload, "window_tokens", 353400)

	quotaA := eventsOf(evs, string(v1.EventProviderQuotaObserved), cliTurnA)
	if len(quotaA) != 2 {
		t.Fatalf("turn A quota rows = %d, want primary+secondary", len(quotaA))
	}
	wantNum(t, quotaA[0].Payload, "used_percent", 10.5)
	wantNum(t, quotaA[0].Payload, "window_minutes", 300)
	if quotaA[0].Payload["limit_id"] != "primary" || quotaA[0].Payload["plan_type"] != "pro" {
		t.Errorf("quota[0] = %+v", quotaA[0].Payload)
	}
	wantNum(t, quotaA[1].Payload, "used_percent", 20.25)

	// Turn B: its own token_count, not turn A's.
	completedB := eventsOf(evs, string(v1.EventProviderTurnCompleted), cliTurnB)
	if len(completedB) != 1 {
		t.Fatalf("turn B completed rows = %d, want 1", len(completedB))
	}
	wantNum(t, completedB[0].Payload, "input_tokens", 5000)
	wantNum(t, completedB[0].Payload, "total_tokens", 6000)

	// Every watcher event says where it came from.
	for _, ev := range evs {
		if ev.Source != "provider_event" {
			t.Errorf("%s: source = %q, want provider_event", ev.EventType, ev.Source)
		}
	}
}

func TestScanOnce_OffsetTracking_AppendAcrossTicks(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()

	full := fixtureBytes(t, "cli_two_turns.jsonl")
	lines := bytes.SplitAfter(full, []byte("\n"))
	turnA := bytes.Join(lines[:6], nil) // meta..task_complete(A)
	turnB := bytes.Join(lines[6:], nil)

	path := stageRollout(t, sessions, cliSession, turnA)
	w := newWatcher(t, db, sessions)

	stats1 := w.ScanOnce(context.Background())
	if stats1.TurnsEmitted != 1 || stats1.BytesRead != int64(len(turnA)) {
		t.Fatalf("tick 1 stats = %+v, want 1 turn / %d bytes", stats1, len(turnA))
	}

	// Idle tick: nothing appended, nothing read.
	statsIdle := w.ScanOnce(context.Background())
	if statsIdle.BytesRead != 0 || statsIdle.TurnsEmitted != 0 {
		t.Fatalf("idle tick stats = %+v, want zero reads", statsIdle)
	}

	appendToFile(t, path, turnB)
	stats2 := w.ScanOnce(context.Background())
	if stats2.TurnsEmitted != 1 {
		t.Fatalf("tick 2 stats = %+v, want 1 turn", stats2)
	}
	if stats2.BytesRead != int64(len(turnB)) {
		t.Errorf("tick 2 read %d bytes, want only the appended %d (offset tracking)", stats2.BytesRead, len(turnB))
	}
	if evs := loadEvents(t, db); len(evs) != 8 {
		t.Fatalf("stored %d events, want 8 with no duplicates", len(evs))
	}
}

func TestScanOnce_RestartRescansAndDedupes(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	stageRollout(t, sessions, cliSession, fixtureBytes(t, "cli_two_turns.jsonl"))

	w1 := newWatcher(t, db, sessions)
	w1.ScanOnce(context.Background())
	before := loadEvents(t, db)

	// "Restart": a fresh Watcher has no offsets and re-scans from byte 0;
	// content-derived idempotency keys make the re-emission a store no-op.
	w2 := newWatcher(t, db, sessions)
	stats := w2.ScanOnce(context.Background())
	if stats.EventsEmitted != 8 {
		t.Fatalf("restart scan stats = %+v, want the full 8 events re-emitted", stats)
	}
	after := loadEvents(t, db)
	if len(after) != len(before) {
		t.Fatalf("restart re-scan grew the store: %d -> %d rows", len(before), len(after))
	}
}

// TestScanOnce_HookAndWatcherDedupeBothOrders is the store-level half of
// the issue-#92 dedupe proof, in both delivery orders, through the REAL
// hook handler (orchestrator.HandleCodexStop) and the REAL store.
func TestScanOnce_HookAndWatcherDedupeBothOrders(t *testing.T) {
	runHookStop := func(t *testing.T, db *sqlite.DB, rolloutPath string) {
		t.Helper()
		deps := orchestrator.HookDeps{
			Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 30, 0, 0, time.UTC)},
			IDs:       &seqIDs{prefix: "hook"},
			Persister: claudetelemetry.NewEventStore(db),
			TxRunner:  db,
		}
		stdin, err := json.Marshal(map[string]any{
			"session_id":      vscodeSess,
			"hook_event_name": "Stop",
			"turn_id":         vscodeTurn,
			"transcript_path": rolloutPath,
			"model":           "gpt-5.2-codex",
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := orchestrator.HandleCodexStop(context.Background(), deps, stdin)
		if err != nil {
			t.Fatalf("HandleCodexStop: %v", err)
		}
		if !result.Persisted || !result.UsageExtracted {
			t.Fatalf("hook result = %+v, want persisted with usage", result)
		}
	}

	assertSingleCapture := func(t *testing.T, db *sqlite.DB) {
		t.Helper()
		evs := loadEvents(t, db)
		if got := len(eventsOf(evs, string(v1.EventProviderTurnCompleted), vscodeTurn)); got != 1 {
			t.Errorf("turn.completed rows = %d, want 1 (dedupe by construction)", got)
		}
		if got := len(eventsOf(evs, string(v1.EventProviderContextObserved), vscodeTurn)); got != 1 {
			t.Errorf("context.observed rows = %d, want 1", got)
		}
		if got := len(eventsOf(evs, string(v1.EventProviderQuotaObserved), vscodeTurn)); got != 2 {
			t.Errorf("quota.observed rows = %d, want 2", got)
		}
		if len(evs) != 4 {
			t.Errorf("total rows = %d, want 4", len(evs))
		}
	}

	t.Run("hook_first_then_watcher", func(t *testing.T) {
		db := openTestDB(t)
		sessions := t.TempDir()
		path := stageRollout(t, sessions, vscodeSess, fixtureBytes(t, "vscode_single_turn.jsonl"))
		runHookStop(t, db, path)
		w := newWatcher(t, db, sessions)
		if stats := w.ScanOnce(context.Background()); stats.EventsEmitted != 4 {
			t.Fatalf("watcher stats = %+v, want 4 events emitted (then deduped)", stats)
		}
		assertSingleCapture(t, db)
	})

	t.Run("watcher_first_then_hook", func(t *testing.T) {
		db := openTestDB(t)
		sessions := t.TempDir()
		path := stageRollout(t, sessions, vscodeSess, fixtureBytes(t, "vscode_single_turn.jsonl"))
		w := newWatcher(t, db, sessions)
		w.ScanOnce(context.Background())
		runHookStop(t, db, path)
		assertSingleCapture(t, db)

		// Watcher won the race here, so the surviving rows carry the
		// vscode attribution labels.
		evs := loadEvents(t, db)
		completed := eventsOf(evs, string(v1.EventProviderTurnCompleted), vscodeTurn)
		if completed[0].Payload["surface"] != "vscode" || completed[0].Payload["originator"] != "codex_vscode" {
			t.Errorf("attribution = %+v", completed[0].Payload)
		}
	})
}

func TestScanOnce_SubagentAttributionAndParentLinkage(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	stageRollout(t, sessions, subagentSess, fixtureBytes(t, "subagent.jsonl"))

	w := newWatcher(t, db, sessions)
	w.ScanOnce(context.Background())

	evs := loadEvents(t, db)
	completed := eventsOf(evs, string(v1.EventProviderTurnCompleted), subagentTurn)
	if len(completed) != 1 {
		t.Fatalf("turn.completed rows = %d, want 1", len(completed))
	}
	p := completed[0].Payload
	if p["surface"] != "subagent" || p["originator"] != "codex_vscode" {
		t.Errorf("surface/originator = %v/%v", p["surface"], p["originator"])
	}
	if p["parent_session_id"] != parentSess {
		t.Errorf("parent_session_id = %v, want %q (subagent -> parent linkage)", p["parent_session_id"], parentSess)
	}
	if completed[0].SessionID != subagentSess {
		t.Errorf("session_id = %q, want the subagent's OWN session id", completed[0].SessionID)
	}
	// The rollout wire carries agent nickname/path; neither may persist.
	for _, ev := range evs {
		raw, _ := json.Marshal(ev.Payload)
		if bytes.Contains(raw, []byte("Fixture")) || bytes.Contains(raw, []byte("fixture_agent")) {
			t.Errorf("%s: agent nickname/path leaked into payload: %s", ev.EventType, raw)
		}
	}
}

// TestScanOnce_LegacyRolloutWithoutTurnIDs pins the fallback for rollouts
// whose task events predate turn_id (the PR-#83 fixture shape): events
// still emit, keyed off the task_complete line's own timestamp, and a
// restart's re-scan does not duplicate them.
func TestScanOnce_LegacyRolloutWithoutTurnIDs(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	legacy, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "codex", "rollout", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	stageRollout(t, sessions, "019f0000-1111-7aaa-8bbb-ccccdddd0001", legacy)

	w := newWatcher(t, db, sessions)
	if stats := w.ScanOnce(context.Background()); stats.TurnsEmitted != 1 || stats.EventsEmitted != 4 {
		t.Fatalf("stats = %+v, want 1 turn / 4 events", stats)
	}

	w2 := newWatcher(t, db, sessions)
	w2.ScanOnce(context.Background())
	if evs := loadEvents(t, db); len(evs) != 4 {
		t.Fatalf("restart duplicated legacy rows: %d, want 4", len(evs))
	}
}

// TestScanOnce_MalformedAndTornTailLines: garbage lines are skipped
// fail-open, and an unterminated tail line is left unconsumed so the next
// tick — after the writer finishes it — captures the turn.
func TestScanOnce_MalformedAndTornTailLines(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()

	full := fixtureBytes(t, "vscode_single_turn.jsonl")
	lines := bytes.SplitAfter(full, []byte("\n"))
	head := bytes.Join(lines[:4], nil) // meta, turn_context, task_started, token_count
	tail := lines[4]                   // the task_complete line
	torn := tail[:len(tail)/2]         // ...written halfway, no newline

	staged := append([]byte{}, head...)
	staged = append(staged, []byte("{this is not json}\n")...)
	staged = append(staged, torn...)
	path := stageRollout(t, sessions, vscodeSess, staged)

	w := newWatcher(t, db, sessions)
	stats1 := w.ScanOnce(context.Background())
	if stats1.Errors != 0 {
		t.Fatalf("stats = %+v: malformed/torn lines must be fail-open, not errors", stats1)
	}
	if stats1.TurnsEmitted != 0 {
		t.Fatalf("stats = %+v: the torn task_complete must not have been parsed", stats1)
	}
	wantConsumed := int64(len(head) + len("{this is not json}\n"))
	if stats1.BytesRead != wantConsumed {
		t.Errorf("consumed %d bytes, want %d (torn tail left unconsumed)", stats1.BytesRead, wantConsumed)
	}

	appendToFile(t, path, tail[len(tail)/2:]) // writer finishes the line
	stats2 := w.ScanOnce(context.Background())
	if stats2.TurnsEmitted != 1 || stats2.EventsEmitted != 4 {
		t.Fatalf("resume tick stats = %+v, want the completed turn", stats2)
	}
	if evs := loadEvents(t, db); len(evs) != 4 {
		t.Fatalf("stored %d events, want 4", len(evs))
	}
}

// TestScanOnce_BacklogOlderThanLookbackSkippedThenTailed: a file whose
// mtime predates the lookback window is never re-parsed, but appends to it
// ARE captured, with attribution lazily recovered from its leading
// session_meta line.
func TestScanOnce_BacklogOlderThanLookbackSkippedThenTailed(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()

	full := fixtureBytes(t, "cli_two_turns.jsonl")
	lines := bytes.SplitAfter(full, []byte("\n"))
	turnA := bytes.Join(lines[:6], nil)
	turnB := bytes.Join(lines[6:], nil)

	path := stageRollout(t, sessions, cliSession, turnA)
	old := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) // long before the fixed clock's lookback cutoff
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	w := newWatcher(t, db, sessions)
	stats1 := w.ScanOnce(context.Background())
	if stats1.TurnsEmitted != 0 || stats1.BytesRead != 0 {
		t.Fatalf("stats = %+v: backlog older than lookback must be skipped, not parsed", stats1)
	}

	appendToFile(t, path, turnB) // fresh mtime; only the appended bytes get parsed
	stats2 := w.ScanOnce(context.Background())
	if stats2.TurnsEmitted != 1 || stats2.BytesRead != int64(len(turnB)) {
		t.Fatalf("growth tick stats = %+v, want just turn B's bytes", stats2)
	}
	evs := loadEvents(t, db)
	completed := eventsOf(evs, string(v1.EventProviderTurnCompleted), cliTurnB)
	if len(completed) != 1 {
		t.Fatalf("turn B completed rows = %d, want 1", len(completed))
	}
	if completed[0].Payload["originator"] != "codex-tui" {
		t.Errorf("originator = %v, want attribution recovered from the leading meta line", completed[0].Payload["originator"])
	}
	if completed[0].SessionID != cliSession {
		t.Errorf("session_id = %q", completed[0].SessionID)
	}
}

func TestScanOnce_PersistFailureLeavesOffsetForRetry(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	stageRollout(t, sessions, vscodeSess, fixtureBytes(t, "vscode_single_turn.jsonl"))

	inner := claudetelemetry.NewEventStore(db)
	p := &failOncePersister{inner: inner}
	w, err := rolloutwatch.New(rolloutwatch.Config{SessionsDir: sessions}, rolloutwatch.Deps{
		Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{prefix: "watch"},
		Persister: p,
		TxRunner:  db,
	})
	if err != nil {
		t.Fatal(err)
	}

	stats1 := w.ScanOnce(context.Background())
	if stats1.Errors != 1 || stats1.EventsEmitted != 0 {
		t.Fatalf("failing tick stats = %+v, want 1 error / 0 events", stats1)
	}
	if len(loadEvents(t, db)) != 0 {
		t.Fatal("failed persist must not leave rows")
	}

	stats2 := w.ScanOnce(context.Background())
	if stats2.EventsEmitted != 4 || stats2.Errors != 0 {
		t.Fatalf("retry tick stats = %+v, want the full turn", stats2)
	}
	if evs := loadEvents(t, db); len(evs) != 4 {
		t.Fatalf("stored %d events, want 4", len(evs))
	}
}

func TestScanOnce_ByteBudgetDefersWorkAcrossTicks(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	stageRollout(t, sessions, cliSession, fixtureBytes(t, "cli_two_turns.jsonl"))

	w, err := rolloutwatch.New(rolloutwatch.Config{
		SessionsDir:     sessions,
		MaxBytesPerTick: 600, // roughly one fixture line
	}, rolloutwatch.Deps{
		Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{prefix: "watch"},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	})
	if err != nil {
		t.Fatal(err)
	}

	fileSize := int64(len(fixtureBytes(t, "cli_two_turns.jsonl")))
	first := w.ScanOnce(context.Background())
	if first.BytesRead >= fileSize {
		t.Fatalf("first tick read %d of %d bytes: budget not applied", first.BytesRead, fileSize)
	}
	turns := first.TurnsEmitted
	for i := 0; i < 50 && turns < 2; i++ {
		turns += w.ScanOnce(context.Background()).TurnsEmitted
	}
	if turns != 2 {
		t.Fatalf("turns emitted across budgeted ticks = %d, want 2", turns)
	}
	if evs := loadEvents(t, db); len(evs) != 8 {
		t.Fatalf("stored %d events, want 8", len(evs))
	}
}

// TestDrain_CatchesUpUnderTightBudget: Drain (the --once engine) loops
// budget-bounded passes inside one process until nothing is deferred —
// the cold-start/cron path where a single pass would stop short.
func TestDrain_CatchesUpUnderTightBudget(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	stageRollout(t, sessions, cliSession, fixtureBytes(t, "cli_two_turns.jsonl"))
	stageRollout(t, sessions, vscodeSess, fixtureBytes(t, "vscode_single_turn.jsonl"))

	w, err := rolloutwatch.New(rolloutwatch.Config{
		SessionsDir:     sessions,
		MaxBytesPerTick: 600, // roughly one fixture line per pass
	}, rolloutwatch.Deps{
		Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{prefix: "watch"},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := w.Drain(context.Background())
	if stats.TurnsEmitted != 3 || stats.EventsEmitted != 12 || stats.Deferred != 0 || stats.Errors != 0 {
		t.Fatalf("Drain stats = %+v, want all 3 turns / 12 events with nothing deferred", stats)
	}
	if evs := loadEvents(t, db); len(evs) != 12 {
		t.Fatalf("stored %d events, want 12", len(evs))
	}
}

// TestScanOnce_PrivacyGrep_NoContentReachesTheStore drives the
// message-text fixture (user prose, assistant prose, base_instructions,
// last_agent_message — all needle-tagged) through a full scan and greps
// the RAW database bytes, WAL sidecars included: the needle must be
// nowhere. The fixture self-check keeps the gate honest.
func TestScanOnce_PrivacyGrep_NoContentReachesTheStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}

	fixtureContent := fixtureBytes(t, "with_message_text.jsonl")
	if !bytes.Contains(fixtureContent, []byte(watchNeedle)) {
		t.Fatalf("privacy fixture is stale: no %q needle", watchNeedle)
	}
	sessions := t.TempDir()
	stageRollout(t, sessions, msgTextSess, fixtureContent)

	w, err := rolloutwatch.New(rolloutwatch.Config{SessionsDir: sessions}, rolloutwatch.Deps{
		Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{prefix: "watch"},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	})
	if err != nil {
		t.Fatal(err)
	}
	stats := w.ScanOnce(context.Background())
	if stats.TurnsEmitted != 1 || stats.EventsEmitted != 4 {
		t.Fatalf("stats = %+v, want the fixture's turn captured", stats)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, sidecar := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		raw, err := os.ReadFile(sidecar)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("reading %s: %v", sidecar, err)
		}
		if bytes.Contains(raw, []byte(watchNeedle)) {
			t.Errorf("raw database artifact %s contains conversation text (needle %q)", sidecar, watchNeedle)
		}
	}
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	db := openTestDB(t)
	sessions := t.TempDir()
	w, err := rolloutwatch.New(rolloutwatch.Config{
		SessionsDir: sessions,
		Interval:    5 * time.Millisecond,
	}, rolloutwatch.Deps{
		Clock:     fixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{prefix: "watch"},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	ticks := 0
	if err := w.Run(ctx, func(rolloutwatch.ScanStats) { ticks++ }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v, want the context's own error", err)
	}
	if ticks == 0 {
		t.Fatal("Run never scanned")
	}
}
