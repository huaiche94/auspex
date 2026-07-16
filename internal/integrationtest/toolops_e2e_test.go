// toolops_e2e_test.go: issue #67 slice 3a's end-to-end acceptance
// (ADR-052) over the REAL command tree and production stack, HOME
// isolated to a temp dir (the same posture managedrun_test.go
// establishes): user-prompt-submit -> three post-tool-use invocations
// (mixed Read/Edit on overlapping fixture paths) -> stop, then prove
//
//  1. the persisted provider.turn.completed payload carries the five
//     aggregates with the exact worked-example numbers (3 ops on 2
//     distinct files, 1 repeat, repeat_rate 1/3, max 2);
//  2. the observations export whitelists them through;
//  3. the toolop_scratch table is empty (turn-close cleanup);
//  4. a duplicate/re-entrant stop never yields a second aggregate-bearing
//     event;
//  5. the PRIVACY GREP: the fixture file paths — and their SHA-256 hexes
//     — appear NOWHERE in the raw SQLite artifact bytes (main DB + WAL
//     sidecars). Raw paths are never persisted in any form, hashes
//     included (ADR-052's binding invariant).
package integrationtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/retention"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// The two operative fixture paths (posttooluse/normal.json and
// posttooluse/unknown_fields.json carry them verbatim — the stale-table
// self-check below enforces that) and the session every claude
// normal.json fixture shares.
const (
	toolOpsE2ESession   = "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W"
	toolOpsFixturePathA = "/Users/dev/projects/auspex/internal/rotation/keyring.go"
	toolOpsFixturePathB = "/Users/dev/projects/auspex/research/calibration/experiments.ipynb"
)

// buildToolOpsHookRoot assembles the real `auspex hook claude ...`
// subtree over the real production collaborators (EventStore,
// OpenTurnStore, ToolOpScratchStore) against db, at a fixed instant.
func buildToolOpsHookRoot(db *sqlite.DB, at time.Time, ids *seqIDs) *cobra.Command {
	deps := orchestrator.HookDeps{
		Clock:     fixedClock{t: at},
		IDs:       ids,
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
		OpenTurns: &orchestrator.OpenTurnStore{DB: db},
		ToolOps:   &orchestrator.ToolOpScratchStore{DB: db},
	}
	hook := &cobra.Command{Use: "hook", SilenceUsage: true, SilenceErrors: true}
	hook.AddCommand(cli.NewHookClaudeCmd(deps))
	root := &cobra.Command{Use: "auspex", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(hook)
	return root
}

// runHook drives one hook leaf with stdin exactly as Claude Code's hook
// runner would, returning its stdout.
func runHook(t *testing.T, root *cobra.Command, stdin []byte, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(bytes.NewReader(stdin))
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("auspex %s: %v\nstderr: %s", strings.Join(args, " "), err, errOut.String())
	}
	return out.String()
}

// postToolUseE2EStdin derives a PostToolUse payload from the normal.json
// fixture's shape, varying tool_name and the target path — the mixed
// Read/Edit sequence over overlapping fixture paths the acceptance
// describes. transcriptPath points at the isolated-HOME synthetic
// transcript so the Stop-time replay can resolve path identity.
func postToolUseE2EStdin(t *testing.T, tool, path, transcriptPath string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id":      toolOpsE2ESession,
		"transcript_path": transcriptPath,
		"cwd":             "/Users/dev/projects/auspex",
		"hook_event_name": "PostToolUse",
		"tool_name":       tool,
		"tool_input":      map[string]any{"file_path": path},
		"tool_response":   map[string]any{"filePath": path, "success": true},
	})
	if err != nil {
		t.Fatalf("marshal post-tool-use stdin: %v", err)
	}
	return b
}

func TestToolOpsE2E_CaptureStampExportCleanupPrivacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "toolops-e2e.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Stale-fixture self-check: the operative paths must still be the
	// fixtures' literal content, or the privacy grep below stops proving
	// what it claims to.
	normalFixture := qa02Fixture(t, "posttooluse", "normal.json")
	unknownFixture := qa02Fixture(t, "posttooluse", "unknown_fields.json")
	if !strings.Contains(string(normalFixture), toolOpsFixturePathA) {
		t.Fatalf("posttooluse/normal.json no longer contains %q — update this test's needles", toolOpsFixturePathA)
	}
	if !strings.Contains(string(unknownFixture), toolOpsFixturePathB) {
		t.Fatalf("posttooluse/unknown_fields.json no longer contains %q — update this test's needles", toolOpsFixturePathB)
	}

	// The synthetic session transcript in the isolated HOME: the same
	// three ops the hook invocations deliver, in the provider's own JSONL
	// shape (a test INPUT — Claude Code owns this file in production;
	// Auspex only ever reads it).
	transcriptPath := filepath.Join(home, "transcript.jsonl")
	transcriptLines := []string{
		`{"type":"user","message":{"role":"user","content":"tighten the keyring rotation"}}`,
		fmt.Sprintf(`{"type":"assistant","requestId":"req-1","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"%s"}}]}}`, toolOpsFixturePathA),
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"r"}]}}`,
		fmt.Sprintf(`{"type":"assistant","requestId":"req-2","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_2","name":"Edit","input":{"file_path":"%s"}}]}}`, toolOpsFixturePathA),
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"r"}]}}`,
		fmt.Sprintf(`{"type":"assistant","requestId":"req-3","message":{"role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_3","name":"NotebookEdit","input":{"notebook_path":"%s"}}]}}`, toolOpsFixturePathB),
	}
	if err := os.WriteFile(transcriptPath, []byte(strings.Join(transcriptLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write synthetic transcript: %v", err)
	}

	ids := &seqIDs{}
	base := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)

	// --- turn start ------------------------------------------------------
	root := buildToolOpsHookRoot(db, base, ids)
	runHook(t, root, qa02Fixture(t, "userpromptsubmit", "normal.json"), "hook", "claude", "user-prompt-submit")

	// --- N post-tool-use invocations: Read A, Edit A, NotebookEdit B ----
	for i, stdin := range [][]byte{
		postToolUseE2EStdin(t, "Read", toolOpsFixturePathA, transcriptPath),
		postToolUseE2EStdin(t, "Edit", toolOpsFixturePathA, transcriptPath),
		func() []byte {
			// The third op is the unknown_fields fixture verbatim except
			// for its session (rewritten onto the turn's session): a
			// NotebookEdit on fixture path B with unknown fields riding
			// along — proving tolerance end to end.
			var payload map[string]any
			if err := json.Unmarshal(unknownFixture, &payload); err != nil {
				t.Fatalf("decode unknown_fields fixture: %v", err)
			}
			payload["session_id"] = toolOpsE2ESession
			payload["transcript_path"] = transcriptPath
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("re-encode fixture: %v", err)
			}
			return b
		}(),
	} {
		out := runHook(t, buildToolOpsHookRoot(db, base.Add(time.Duration(i+1)*time.Second), ids), stdin, "hook", "claude", "post-tool-use")
		if strings.TrimSpace(out) != "{}" {
			t.Fatalf("post-tool-use stdout = %q, want the {} no-op response", out)
		}
	}

	// Mid-turn: the scratch carries exactly one counter row for the turn.
	var scratchOps int64
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT file_ops FROM toolop_scratch WHERE session_id = ?`, toolOpsE2ESession,
	).Scan(&scratchOps); err != nil {
		t.Fatalf("read mid-turn scratch: %v", err)
	}
	if scratchOps != 3 {
		t.Fatalf("mid-turn scratch file_ops = %d, want 3", scratchOps)
	}

	// --- stop -------------------------------------------------------------
	stopStdin := []byte(fmt.Sprintf(
		`{"session_id":%q,"transcript_path":%q,"cwd":"/Users/dev/projects/auspex","hook_event_name":"Stop","stop_hook_active":false}`,
		toolOpsE2ESession, transcriptPath))
	runHook(t, buildToolOpsHookRoot(db, base.Add(time.Minute), ids), stopStdin, "hook", "claude", "stop")

	// 1. The five aggregates, exact worked-example numbers, on the
	//    turn-correlated provider.turn.completed row.
	var turnID, payloadJSON string
	if err := db.Conn().QueryRowContext(ctx, `
		SELECT turn_id, payload_json FROM events
		WHERE event_type = 'provider.turn.completed' AND session_id = ?`,
		toolOpsE2ESession,
	).Scan(&turnID, &payloadJSON); err != nil {
		t.Fatalf("read turn.completed: %v", err)
	}
	if turnID == "" {
		t.Error("turn.completed carries no turn_id")
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
		t.Errorf("payload[repeat_rate] = %v, want 1/3 (~0.33)", payload["repeat_rate"])
	}

	// 2. The observations export carries them through the whitelist.
	var export bytes.Buffer
	if _, err := retention.ExportObservations(ctx, db, &export); err != nil {
		t.Fatalf("ExportObservations: %v", err)
	}
	var exported map[string]any
	for _, line := range strings.Split(strings.TrimSpace(export.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode export line %q: %v", line, err)
		}
		if rec["event_type"] == "provider.turn.completed" && rec["total_file_ops"] != nil {
			exported = rec
		}
	}
	if exported == nil {
		t.Fatal("observations export carries no aggregate-bearing turn.completed line")
	}
	for key, want := range map[string]float64{
		"distinct_files_touched": 2,
		"total_file_ops":         3,
		"repeated_ops":           1,
		"max_ops_on_one_file":    2,
	} {
		if got, ok := exported[key].(float64); !ok || got != want {
			t.Errorf("export[%q] = %v, want %v", key, exported[key], want)
		}
	}
	if rate, ok := exported["repeat_rate"].(float64); !ok || rate < 0.333 || rate > 0.334 {
		t.Errorf("export[repeat_rate] = %v, want 1/3", exported["repeat_rate"])
	}

	// 3. Scratch cleaned at turn close.
	var scratchRows int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM toolop_scratch`).Scan(&scratchRows); err != nil {
		t.Fatalf("count scratch: %v", err)
	}
	if scratchRows != 0 {
		t.Errorf("toolop_scratch rows after stop = %d, want 0", scratchRows)
	}

	// 4. Duplicate stop (same instant: storage-level dedupe) and
	//    re-entrant stop (later instant: event without aggregates) — one
	//    aggregate-bearing record either way.
	runHook(t, buildToolOpsHookRoot(db, base.Add(time.Minute), ids), stopStdin, "hook", "claude", "stop")
	runHook(t, buildToolOpsHookRoot(db, base.Add(2*time.Minute), ids), stopStdin, "hook", "claude", "stop")
	var total, withAggregates int
	if err := db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*), SUM(CASE WHEN payload_json LIKE '%total_file_ops%' THEN 1 ELSE 0 END)
		FROM events WHERE event_type = 'provider.turn.completed' AND session_id = ?`,
		toolOpsE2ESession,
	).Scan(&total, &withAggregates); err != nil {
		t.Fatalf("count turn.completed: %v", err)
	}
	if total != 2 || withAggregates != 1 {
		t.Errorf("turn.completed rows = %d (aggregate-bearing %d), want 2 rows with exactly 1 aggregate-bearing", total, withAggregates)
	}

	// 5. THE PRIVACY GREP (ADR-052's binding invariant): flush the WAL,
	//    close, and scan the raw SQLite artifact bytes for the fixture
	//    paths, their basenames, and their SHA-256 hexes — zero hits.
	if _, err := db.Conn().ExecContext(ctx, "PRAGMA wal_checkpoint(FULL)"); err != nil {
		t.Fatalf("wal_checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	forbidden := []string{
		toolOpsFixturePathA,
		toolOpsFixturePathB,
		"keyring.go",
		"experiments.ipynb",
		"secret canary: keyring rotation cadence", // tool_input file content
		"notebook canary: calibration sweep",
	}
	for _, path := range []string{toolOpsFixturePathA, toolOpsFixturePathB} {
		sum := sha256.Sum256([]byte(path))
		forbidden = append(forbidden,
			hex.EncodeToString(sum[:]),
			strings.ToUpper(hex.EncodeToString(sum[:])))
	}

	sawAggregateKey, sawSession := false, false
	for _, artifact := range sqliteArtifactPaths(t, dbPath) {
		raw, err := os.ReadFile(artifact)
		if err != nil {
			if os.IsNotExist(err) && artifact != dbPath {
				continue
			}
			t.Fatalf("reading %s: %v", artifact, err)
		}
		for _, needle := range forbidden {
			if bytes.Contains(raw, []byte(needle)) {
				t.Errorf("privacy grep HIT: %q found in raw bytes of %s — paths must never persist, raw or hashed", needle, artifact)
			}
		}
		if bytes.Contains(raw, []byte("total_file_ops")) {
			sawAggregateKey = true
		}
		if bytes.Contains(raw, []byte(toolOpsE2ESession)) {
			sawSession = true
		}
	}
	// Falsifiability: the scan must be reading the bytes the pipeline
	// actually wrote — the aggregate key and the session id ARE there.
	if !sawAggregateKey || !sawSession {
		t.Fatalf("falsifiability check failed (aggregate key present=%v, session present=%v) — the privacy grep may not be scanning the written data", sawAggregateKey, sawSession)
	}
}
