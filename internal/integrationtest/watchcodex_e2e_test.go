// watchcodex_e2e_test.go: issue #92's end-to-end acceptance over the REAL
// compiled binary and its production composition root (cmd/auspex/wire.go)
// — not an in-process command tree — with HOME and CODEX_HOME isolated to
// temp dirs: `auspex watch codex --once` over the synthetic watch
// fixtures (CLI + VS Code + subagent + message-text rollouts), then prove
//
//  1. the DB the binary created carries exactly the expected usage/
//     quota/context/turn rows, attributed (surface/originator/
//     parent_session_id) per rollout meta;
//  2. re-running the same scan is a row-count no-op (restart/rerun
//     dedupe via content-derived idempotency keys — the watcher persists
//     no offsets anywhere);
//  3. a REAL `auspex hook codex stop` for a watcher-captured turn is also
//     a row-count no-op (hook+watcher double-capture dedupes by
//     construction: the rollout's task_complete.turn_id IS the hook
//     payload's turn_id);
//  4. the PRIVACY GREP: the fixtures' needle-tagged conversation text
//     (user prose, assistant prose, base_instructions, last_agent_message)
//     appears NOWHERE in the raw SQLite artifact bytes (main DB + WAL
//     sidecars).
package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/huaiche94/auspex/internal/paths"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

const (
	watchE2ENeedle     = "WATCH-SECRET"
	watchE2EVSCodeSess = "019fa000-0000-7000-8000-000000000002"
	watchE2EVSCodeTurn = "019fa000-0000-7000-8000-00000000a201"
	watchE2ESubSess    = "019fa000-0000-7000-8000-000000000003"
	watchE2EParentSess = "019fa000-0000-7000-8000-00000000000f"
)

// watchE2EFixtures maps fixture name -> session id (the filename must
// embed it, mirroring Codex's rollout-<ts>-<uuid>.jsonl layout).
var watchE2EFixtures = map[string]string{
	"cli_two_turns.jsonl":      "019fa000-0000-7000-8000-000000000001",
	"vscode_single_turn.jsonl": watchE2EVSCodeSess,
	"subagent.jsonl":           watchE2ESubSess,
	"with_message_text.jsonl":  "019fa000-0000-7000-8000-000000000004",
}

// buildAuspexBinary compiles the real cmd/auspex into a temp dir (the same
// go-build technique buildFakeProviderBinary uses, applied to the product
// binary itself — the point of this E2E is the production wire.go path).
func buildAuspexBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "auspex")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/auspex")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build cmd/auspex: %v\n%s", err, out)
	}
	return bin
}

// watchE2EEnv builds the child process's scrubbed environment: only PATH
// and the Go/temp basics survive; HOME (and USERPROFILE, for Windows) and
// CODEX_HOME point at the isolated dirs, and no XDG override leaks in
// from the developer's machine, so the binary's paths.ResolveHost and
// this test's own paths.Resolve agree on where the DB lives.
func watchE2EEnv(home, codexHome string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
		"HOME=" + home,
		"USERPROFILE=" + home,
		"CODEX_HOME=" + codexHome,
	}
}

// fakeHomeEnv mirrors the child's scrubbed environment for
// paths.Resolve, so the test computes the same data dir the binary did.
type fakeHomeEnv struct{ home string }

func (e fakeHomeEnv) Getenv(string) string         { return "" }
func (e fakeHomeEnv) UserHomeDir() (string, error) { return e.home, nil }

type watchE2EStats struct {
	SchemaVersion string `json:"schema_version"`
	TurnsEmitted  int    `json:"turns_emitted"`
	EventsEmitted int    `json:"events_emitted"`
	Errors        int    `json:"errors"`
}

func runWatchOnce(t *testing.T, bin string, env []string) watchE2EStats {
	t.Helper()
	cmd := exec.Command(bin, "watch", "codex", "--once")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auspex watch codex --once: %v\n%s", err, out)
	}
	var stats watchE2EStats
	if err := json.Unmarshal(bytes.TrimSpace(out), &stats); err != nil {
		t.Fatalf("watch output is not the versioned JSON line: %v\n%s", err, out)
	}
	if stats.SchemaVersion != "auspex.watch-codex.v1" {
		t.Fatalf("schema_version = %q\n%s", stats.SchemaVersion, out)
	}
	return stats
}

func TestWatchCodexE2E_RealBinary_CaptureDedupeAndPrivacy(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real binary")
	}
	bin := buildAuspexBinary(t)

	home := t.TempDir()
	codexHome := t.TempDir()
	env := watchE2EEnv(home, codexHome)

	// Stage the four fixtures as a real CODEX_HOME sessions tree.
	day := filepath.Join(codexHome, "sessions", "2026", "07", "14")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutPaths := map[string]string{}
	for name, sessionID := range watchE2EFixtures {
		content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "codex", "watch", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		path := filepath.Join(day, "rollout-2026-07-14T09-00-00-"+sessionID+".jsonl")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		rolloutPaths[name] = path
	}
	// Fixture self-check: the privacy needle must actually be on disk.
	if raw, _ := os.ReadFile(rolloutPaths["with_message_text.jsonl"]); !bytes.Contains(raw, []byte(watchE2ENeedle)) {
		t.Fatalf("privacy fixture is stale: no %q needle", watchE2ENeedle)
	}

	// Pass 1: capture. 5 turns (2 cli + 1 vscode + 1 subagent + 1
	// message-text), 4 events each (completed + context + 2 quota).
	stats := runWatchOnce(t, bin, env)
	if stats.TurnsEmitted != 5 || stats.EventsEmitted != 20 || stats.Errors != 0 {
		t.Fatalf("pass 1 stats = %+v, want 5 turns / 20 events / 0 errors", stats)
	}

	// Pass 2: a fresh process has no offsets — it re-scans everything and
	// every event dedupes in the store.
	stats2 := runWatchOnce(t, bin, env)
	if stats2.EventsEmitted != 20 || stats2.Errors != 0 {
		t.Fatalf("pass 2 stats = %+v, want the same 20 events re-emitted cleanly", stats2)
	}

	// The REAL Stop hook for a watcher-captured turn: exit 0 and no new
	// rows (double capture collapses on identical idempotency keys).
	hookStdin, err := json.Marshal(map[string]any{
		"session_id":      watchE2EVSCodeSess,
		"hook_event_name": "Stop",
		"turn_id":         watchE2EVSCodeTurn,
		"transcript_path": rolloutPaths["vscode_single_turn.jsonl"],
		"model":           "gpt-5.2-codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	hookCmd := exec.Command(bin, "hook", "codex", "stop")
	hookCmd.Env = env
	hookCmd.Stdin = bytes.NewReader(hookStdin)
	if out, err := hookCmd.CombinedOutput(); err != nil {
		t.Fatalf("auspex hook codex stop: %v\n%s", err, out)
	}

	// Locate the DB exactly where the binary's composition root put it.
	dirs, err := paths.Resolve(runtime.GOOS, fakeHomeEnv{home: home})
	if err != nil {
		t.Fatalf("paths.Resolve: %v", err)
	}
	dbPath := filepath.Join(dirs.Data, "auspex.db")

	// PRIVACY GREP over the raw artifact bytes, WAL sidecars included,
	// BEFORE this test opens the DB (so nothing here can checkpoint or
	// rewrite what the binary left behind).
	for _, sidecar := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		raw, err := os.ReadFile(sidecar)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("reading %s: %v", sidecar, err)
		}
		if bytes.Contains(raw, []byte(watchE2ENeedle)) {
			t.Errorf("raw database artifact %s contains conversation text (needle %q)", sidecar, watchE2ENeedle)
		}
	}

	// Row-level verification.
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open binary-created db: %v", err)
	}
	defer func() { _ = db.Close() }()

	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Conn().QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}

	if got := count(`SELECT COUNT(*) FROM events WHERE provider = 'codex'`); got != 20 {
		t.Errorf("codex event rows = %d, want 20 (rerun + hook must both dedupe)", got)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.turn.completed'`); got != 5 {
		t.Errorf("turn.completed rows = %d, want 5", got)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.turn.completed' AND turn_id = ?`, watchE2EVSCodeTurn); got != 1 {
		t.Errorf("vscode turn rows = %d, want exactly 1 despite watcher rerun + hook", got)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.quota.observed'`); got != 10 {
		t.Errorf("quota.observed rows = %d, want 10", got)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.context.observed'`); got != 5 {
		t.Errorf("context.observed rows = %d, want 5", got)
	}

	// Attribution splits: the subagent rollout's rows carry its OWN
	// session id, the subagent surface label, and the parent linkage.
	var payloadJSON string
	if err := db.Conn().QueryRow(`
		SELECT payload_json FROM events
		WHERE event_type = 'provider.turn.completed' AND session_id = ?`, watchE2ESubSess).Scan(&payloadJSON); err != nil {
		t.Fatalf("subagent turn row: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["surface"] != "subagent" || payload["originator"] != "codex_vscode" || payload["parent_session_id"] != watchE2EParentSess {
		t.Errorf("subagent attribution = %+v", payload)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE json_extract(payload_json, '$.surface') = 'vscode'`); got != 4 {
		t.Errorf("vscode-surface rows = %d, want 4", got)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE json_extract(payload_json, '$.surface') = 'cli'`); got != 12 {
		t.Errorf("cli-surface rows = %d, want 12 (two cli turns + the message-text turn)", got)
	}
}
