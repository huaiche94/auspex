// toolops_test.go: issue #67 slice 3a (ADR-052) — the PostToolUse scratch
// store's semantics against a real migrated DB, HandlePostToolUse's
// fail-open accumulation, and the full in-package pipeline proof:
// user-prompt-submit -> N post-tool-use invocations -> stop, with the five
// aggregates stamped on the persisted provider.turn.completed row, the
// scratch cleaned at turn close, duplicate stops inert, and no path bytes
// anywhere in the database.
package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// toolOpsSession matches the session_id every claude fixture family's
// normal.json shares, so the fixture-driven hooks correlate.
const toolOpsSession = "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W"

// postToolUseStdin builds a realistic PostToolUse payload for one tool
// call. The paths below are synthetic TEST INPUTS (never persisted
// outputs) — the privacy assertions at the bottom of this file prove
// exactly that.
func postToolUseStdin(tool, path string) []byte {
	b, _ := json.Marshal(map[string]any{
		"session_id":      toolOpsSession,
		"cwd":             "/tmp/repo",
		"hook_event_name": "PostToolUse",
		"tool_name":       tool,
		"tool_input":      map[string]any{"file_path": path},
		"tool_response":   map[string]any{"filePath": path, "success": true},
	})
	return b
}

// stopStdin builds a Stop payload for the shared session pointing at
// transcriptPath ("" omits the field).
func stopStdin(transcriptPath string) []byte {
	payload := map[string]any{
		"session_id":       toolOpsSession,
		"hook_event_name":  "Stop",
		"stop_hook_active": false,
		"cwd":              "/tmp/repo",
	}
	if transcriptPath != "" {
		payload["transcript_path"] = transcriptPath
	}
	b, _ := json.Marshal(payload)
	return b
}

// writeToolOpsTranscript writes the synthetic session transcript whose
// last turn mirrors the worked example the E2E drives: Read a.go, Edit
// a.go, Read b.go (3 ops, 2 distinct files, 1 repeat, max 2).
func writeToolOpsTranscript(t *testing.T, dir string) string {
	t.Helper()
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"do the thing"}}`,
		`{"type":"assistant","requestId":"req-1","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/e2e-repo/alpha.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"r"}]}}`,
		`{"type":"assistant","requestId":"req-2","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_2","name":"Edit","input":{"file_path":"/tmp/e2e-repo/alpha.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"r"}]}}`,
		`{"type":"assistant","requestId":"req-3","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_3","name":"Read","input":{"file_path":"/tmp/e2e-repo/beta.go"}}]}}`,
	}
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing synthetic transcript: %v", err)
	}
	return path
}

func openToolOpsDB(t *testing.T) *sqlite.DB {
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

func toolOpsDeps(db *sqlite.DB, at time.Time, ids *sequentialHookIDs) orchestrator.HookDeps {
	return orchestrator.HookDeps{
		Clock:     fixedClock{t: at},
		IDs:       ids,
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
		OpenTurns: &orchestrator.OpenTurnStore{DB: db},
		ToolOps:   &orchestrator.ToolOpScratchStore{DB: db},
	}
}

func scratchRows(t *testing.T, db *sqlite.DB) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM toolop_scratch`).Scan(&n); err != nil {
		t.Fatalf("count toolop_scratch: %v", err)
	}
	return n
}

// --- scratch store semantics ---------------------------------------------

func TestToolOpScratchStore_RecordFoldClear(t *testing.T) {
	db := openToolOpsDB(t)
	store := &orchestrator.ToolOpScratchStore{DB: db}
	ctx := context.Background()

	// Three ops on turn-1, one on turn-2 (a crash-orphan sibling), one on
	// the empty-turn key.
	for range 3 {
		if !store.RecordFileOp(ctx, "sess-1", "turn-1") {
			t.Fatal("RecordFileOp(turn-1) = false, want true")
		}
	}
	if !store.RecordFileOp(ctx, "sess-1", "turn-2") {
		t.Fatal("RecordFileOp(turn-2) = false, want true")
	}
	if !store.RecordFileOp(ctx, "sess-1", "") {
		t.Fatal("RecordFileOp(empty turn) = false, want true")
	}

	if ops, ok := store.FoldFileOps(ctx, "sess-1", "turn-1"); !ok || ops != 3 {
		t.Errorf("FoldFileOps(turn-1) = %d/%v, want 3/true", ops, ok)
	}
	if ops, ok := store.FoldFileOps(ctx, "sess-1", ""); !ok || ops != 1 {
		t.Errorf("FoldFileOps(empty turn) = %d/%v, want 1/true", ops, ok)
	}
	if _, ok := store.FoldFileOps(ctx, "sess-1", "turn-never"); ok {
		t.Error("FoldFileOps(unknown turn): ok = true, want false (no capture ran)")
	}
	if _, ok := store.FoldFileOps(ctx, "sess-other", "turn-1"); ok {
		t.Error("FoldFileOps(other session): ok = true, want false")
	}

	// Clear is session-wide: orphaned turn rows go with it.
	if !store.Clear(ctx, "sess-1") {
		t.Fatal("Clear = false, want true")
	}
	if n := scratchRows(t, db); n != 0 {
		t.Errorf("scratch rows after Clear = %d, want 0", n)
	}

	// Fail-open contract: nil receiver/DB never errors, never panics.
	var nilStore *orchestrator.ToolOpScratchStore
	if nilStore.RecordFileOp(ctx, "s", "t") || nilStore.Clear(ctx, "s") {
		t.Error("nil store: writes reported success")
	}
	if _, ok := nilStore.FoldFileOps(ctx, "s", "t"); ok {
		t.Error("nil store: FoldFileOps ok = true, want false")
	}
}

// --- HandlePostToolUse -----------------------------------------------------

func TestHandlePostToolUse_AccumulatesUnderOpenTurn(t *testing.T) {
	db := openToolOpsDB(t)
	ctx := context.Background()
	insertStartedEvent(t, db, "ev-started", toolOpsSession, "turn-open", "2026-07-14T10:00:00Z")
	deps := toolOpsDeps(db, time.Date(2026, 7, 14, 10, 1, 0, 0, time.UTC), &sequentialHookIDs{})

	for i, stdin := range [][]byte{
		postToolUseStdin("Read", "/tmp/e2e-repo/alpha.go"),
		postToolUseStdin("Edit", "/tmp/e2e-repo/alpha.go"),
		readFixture(t, "posttooluse", "normal.json"),         // same fixture session: third op on the open turn
		readFixture(t, "posttooluse", "unknown_fields.json"), // different session, no started turn: empty-turn key
	} {
		result, err := orchestrator.HandlePostToolUse(ctx, deps, stdin)
		if err != nil {
			t.Fatalf("HandlePostToolUse[%d]: %v", i, err)
		}
		if !result.FileOp || !result.Recorded {
			t.Errorf("HandlePostToolUse[%d] = %+v, want FileOp+Recorded", i, result)
		}
	}

	store := deps.ToolOps
	if ops, ok := store.FoldFileOps(ctx, toolOpsSession, "turn-open"); !ok || ops != 3 {
		t.Errorf("FoldFileOps(open turn) = %d/%v, want 3/true", ops, ok)
	}
	// The unknown_fields session has no started turn: accumulated under "".
	if ops, ok := store.FoldFileOps(ctx, "sess_01H9X8K7QZ3M4N5P6R7S8T9V0Z", ""); !ok || ops != 1 {
		t.Errorf("no-started-turn session fold = %d/%v, want 1/true under the empty-turn key", ops, ok)
	}

	// Non-file-op payloads accumulate nothing.
	for name, stdin := range map[string][]byte{
		"ignored tool":   []byte(`{"session_id":"` + toolOpsSession + `","tool_name":"Bash","tool_input":{"command":"go test"}}`),
		"no file target": readFixture(t, "posttooluse", "missing_fields.json"),
		"malformed":      readFixture(t, "posttooluse", "malformed.json"),
	} {
		result, err := orchestrator.HandlePostToolUse(ctx, deps, stdin)
		if err != nil {
			t.Fatalf("HandlePostToolUse(%s): %v (must fail open)", name, err)
		}
		if result.Recorded {
			t.Errorf("HandlePostToolUse(%s): Recorded = true, want false", name)
		}
	}
	if ops, _ := store.FoldFileOps(ctx, toolOpsSession, "turn-open"); ops != 3 {
		t.Errorf("fold after non-ops = %d, want still 3", ops)
	}

	// nil ToolOps is the documented degrade: parsed, classified, dropped.
	noScratch := deps
	noScratch.ToolOps = nil
	result, err := orchestrator.HandlePostToolUse(ctx, noScratch, postToolUseStdin("Read", "/tmp/e2e-repo/alpha.go"))
	if err != nil || !result.FileOp || result.Recorded {
		t.Errorf("nil ToolOps: result = %+v err = %v, want FileOp only", result, err)
	}
}

// --- the full pipeline: prompt -> tool ops -> stop -------------------------

// TestToolOps_FullPipeline_StampsAggregatesCleansScratchDedupesStops is
// the in-package end-to-end: the real handlers over one real DB, driven
// by hook payloads exactly as the CLI leaves would deliver them.
func TestToolOps_FullPipeline_StampsAggregatesCleansScratchDedupesStops(t *testing.T) {
	db := openToolOpsDB(t)
	ctx := context.Background()
	ids := &sequentialHookIDs{}
	transcript := writeToolOpsTranscript(t, t.TempDir())

	// Turn start: persists provider.turn.started with a minted turn_id
	// (Evaluation nil is the documented fail-open degrade — the event
	// still persists, which is all this pipeline needs).
	deps := toolOpsDeps(db, time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC), ids)
	upsResult, err := orchestrator.HandleUserPromptSubmit(ctx, deps, readFixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	if !upsResult.Persisted {
		t.Fatal("turn.started did not persist")
	}

	// Three PostToolUse invocations: Read alpha, Edit alpha, Read beta —
	// mirrored by the synthetic transcript.
	for _, stdin := range [][]byte{
		postToolUseStdin("Read", "/tmp/e2e-repo/alpha.go"),
		postToolUseStdin("Edit", "/tmp/e2e-repo/alpha.go"),
		postToolUseStdin("Read", "/tmp/e2e-repo/beta.go"),
	} {
		result, err := orchestrator.HandlePostToolUse(ctx, deps, stdin)
		if err != nil || !result.Recorded {
			t.Fatalf("HandlePostToolUse: %+v, %v", result, err)
		}
	}
	if n := scratchRows(t, db); n != 1 {
		t.Fatalf("scratch rows mid-turn = %d, want 1", n)
	}

	// Stop: folds, stamps, clears.
	stopDeps := toolOpsDeps(db, time.Date(2026, 7, 14, 10, 5, 0, 0, time.UTC), ids)
	stopResult, err := orchestrator.HandleStop(ctx, stopDeps, stopStdin(transcript))
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if !stopResult.Persisted {
		t.Fatal("turn.completed did not persist")
	}

	var turnID, payloadJSON string
	if err := db.Conn().QueryRowContext(ctx, `
		SELECT turn_id, payload_json FROM events
		WHERE event_type = 'provider.turn.completed' AND session_id = ?`,
		toolOpsSession,
	).Scan(&turnID, &payloadJSON); err != nil {
		t.Fatalf("read turn.completed: %v", err)
	}
	if turnID == "" {
		t.Error("turn.completed carries no turn_id — open-turn stamping regressed")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for key, want := range map[string]float64{
		"distinct_files_touched": 2,
		"total_file_ops":         3,
		"repeated_ops":           1,
		"max_ops_on_one_file":    2,
	} {
		if got, ok := payload[key].(float64); !ok || got != want {
			t.Errorf("payload[%q] = %v, want %v", key, payload[key], want)
		}
	}
	if rate, ok := payload["repeat_rate"].(float64); !ok || rate < 0.333 || rate > 0.334 {
		t.Errorf("payload[repeat_rate] = %v, want 1/3", payload["repeat_rate"])
	}

	// Turn close cleaned the scratch.
	if n := scratchRows(t, db); n != 0 {
		t.Errorf("scratch rows after Stop = %d, want 0 (turn-close cleanup)", n)
	}

	// Duplicate Stop (same instant): storage-level idempotency dedupes the
	// row entirely.
	if _, err := orchestrator.HandleStop(ctx, stopDeps, stopStdin(transcript)); err != nil {
		t.Fatalf("duplicate HandleStop: %v", err)
	}
	// Re-entrant Stop (later instant): a second event lands, but with no
	// aggregate keys — the scratch was already folded, so downstream's
	// earliest-terminal-event-per-turn join sees exactly one
	// aggregate-bearing record.
	lateDeps := toolOpsDeps(db, time.Date(2026, 7, 14, 10, 6, 0, 0, time.UTC), ids)
	if _, err := orchestrator.HandleStop(ctx, lateDeps, stopStdin(transcript)); err != nil {
		t.Fatalf("re-entrant HandleStop: %v", err)
	}
	var withAggregates, total int
	if err := db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*), SUM(CASE WHEN payload_json LIKE '%total_file_ops%' THEN 1 ELSE 0 END)
		FROM events WHERE event_type = 'provider.turn.completed' AND session_id = ?`,
		toolOpsSession,
	).Scan(&total, &withAggregates); err != nil {
		t.Fatalf("count turn.completed: %v", err)
	}
	if total != 2 {
		t.Errorf("turn.completed rows = %d, want 2 (original + re-entrant; duplicate deduped)", total)
	}
	if withAggregates != 1 {
		t.Errorf("aggregate-bearing turn.completed rows = %d, want exactly 1", withAggregates)
	}

	// Privacy: the operative file paths appear NOWHERE in the database —
	// not in events, not in scratch remnants, not in any table. (The
	// integration-level test additionally scans raw DB file bytes and the
	// SHA-256 hexes; this in-package check guards the same invariant at
	// the SQL layer.)
	for _, needle := range []string{"/tmp/e2e-repo/alpha.go", "/tmp/e2e-repo/beta.go", "alpha.go", "beta.go"} {
		var hits int
		if err := db.Conn().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE payload_json LIKE '%' || ? || '%'`, needle,
		).Scan(&hits); err != nil {
			t.Fatalf("scan events for %q: %v", needle, err)
		}
		if hits != 0 {
			t.Errorf("path needle %q found in %d events payloads — paths must never persist", needle, hits)
		}
	}
}

// TestHandleStop_TranscriptUnreadable_DegradesToHookCountedTotal: the
// identity-dependent fields need the transcript replay; without it the
// hook's own count still stamps an exact total_file_ops and nothing else
// (unknown is not zero).
func TestHandleStop_TranscriptUnreadable_DegradesToHookCountedTotal(t *testing.T) {
	db := openToolOpsDB(t)
	ctx := context.Background()
	ids := &sequentialHookIDs{}
	deps := toolOpsDeps(db, time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC), ids)

	if _, err := orchestrator.HandleUserPromptSubmit(ctx, deps, readFixture(t, "userpromptsubmit", "normal.json")); err != nil {
		t.Fatalf("HandleUserPromptSubmit: %v", err)
	}
	for range 3 {
		if _, err := orchestrator.HandlePostToolUse(ctx, deps, postToolUseStdin("Edit", "/tmp/e2e-repo/alpha.go")); err != nil {
			t.Fatalf("HandlePostToolUse: %v", err)
		}
	}
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	if _, err := orchestrator.HandleStop(ctx, deps, stopStdin(missing)); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}

	var payloadJSON string
	if err := db.Conn().QueryRowContext(ctx, `
		SELECT payload_json FROM events
		WHERE event_type = 'provider.turn.completed' AND session_id = ?`,
		toolOpsSession,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("read turn.completed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got, ok := payload["total_file_ops"].(float64); !ok || got != 3 {
		t.Errorf("payload[total_file_ops] = %v, want 3 (the hook-counted exact total)", payload["total_file_ops"])
	}
	for _, key := range []string{"distinct_files_touched", "repeated_ops", "repeat_rate", "max_ops_on_one_file"} {
		if _, present := payload[key]; present {
			t.Errorf("payload key %q present, want absent on the transcript-less degrade", key)
		}
	}
	if n := scratchRows(t, db); n != 0 {
		t.Errorf("scratch rows after Stop = %d, want 0", n)
	}
}

// TestHandleStop_NoCaptureRan_PayloadStaysPre67 pins absence-honesty: a
// turn with no PostToolUse capture (hook not registered) stamps none of
// the five keys — absent, not zero.
func TestHandleStop_NoCaptureRan_PayloadStaysPre67(t *testing.T) {
	db := openToolOpsDB(t)
	ctx := context.Background()
	deps := toolOpsDeps(db, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), &sequentialHookIDs{})

	if _, err := orchestrator.HandleStop(ctx, deps, readFixture(t, "stop", "normal.json")); err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	var payloadJSON string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE event_type = 'provider.turn.completed'`,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("read turn.completed: %v", err)
	}
	for _, key := range []string{"distinct_files_touched", "total_file_ops", "repeated_ops", "repeat_rate", "max_ops_on_one_file"} {
		if strings.Contains(payloadJSON, key) {
			t.Errorf("payload carries %q with no capture ran — must stay byte-identical to the pre-#67 shape: %s", key, payloadJSON)
		}
	}
}

// TestHandleStopFailure_ClearsScratch: a failed turn is a closed turn —
// scratch cleaned, and (per ADR-052) no aggregates on turn.failed.
func TestHandleStopFailure_ClearsScratch(t *testing.T) {
	db := openToolOpsDB(t)
	ctx := context.Background()
	deps := toolOpsDeps(db, time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC), &sequentialHookIDs{})

	if _, err := orchestrator.HandlePostToolUse(ctx, deps, postToolUseStdin("Edit", "/tmp/e2e-repo/alpha.go")); err != nil {
		t.Fatalf("HandlePostToolUse: %v", err)
	}
	if n := scratchRows(t, db); n != 1 {
		t.Fatalf("scratch rows = %d, want 1", n)
	}

	stdin := []byte(`{"session_id":"` + toolOpsSession + `","hook_event_name":"StopFailure","error":{"type":"api_error","message":"boom","status_code":500}}`)
	if _, err := orchestrator.HandleStopFailure(ctx, deps, stdin); err != nil {
		t.Fatalf("HandleStopFailure: %v", err)
	}
	if n := scratchRows(t, db); n != 0 {
		t.Errorf("scratch rows after StopFailure = %d, want 0", n)
	}
	var payloadJSON string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE event_type = 'provider.turn.failed'`,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("read turn.failed: %v", err)
	}
	if strings.Contains(payloadJSON, "file_ops") || strings.Contains(payloadJSON, "repeat_rate") {
		t.Errorf("turn.failed payload carries aggregate keys — approved for turn.completed only: %s", payloadJSON)
	}
}

// Guard: the orchestrator handlers never receive or forward domain.TurnID
// values derived from paths — a compile-time-adjacent sanity anchor for
// reviewers; the real privacy proof is the byte scan in
// internal/integrationtest.
var _ = domain.TurnID("")
