// watch_test.go: command-level coverage for `auspex watch codex` (issue
// #92) — the stub-then-swap contract on the bare tree, and the real
// handler's --once pass over a staged CODEX_HOME writing real rows
// through the real store.
package cli_test

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

	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/rolloutwatch"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

type watchClock struct{ t time.Time }

func (c watchClock) Now() time.Time { return c.t }

type watchIDs struct{ n int }

func (s *watchIDs) NewID() string {
	s.n++
	return "watch-cli-" + strconv.Itoa(s.n)
}

func TestWatchCodex_StubOnBareTree(t *testing.T) {
	root := cli.NewRootCmd()
	cmd, remaining, err := root.Find([]string{"watch", "codex"})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("Find(watch codex) = (%v, %v, %v), want the stub leaf", cmd, remaining, err)
	}

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watch", "codex"})
	execErr := root.Execute()
	var derr *domain.Error
	if !errors.As(execErr, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("bare-tree watch codex = %v, want the honest not-implemented stub", execErr)
	}
}

func TestWatchCodex_OncePassOverStagedCodexHome(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "auspex.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Stage a CODEX_HOME with one rollout fixture.
	codexHome := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "codex", "watch", "vscode_single_turn.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	day := filepath.Join(codexHome, "sessions", "2026", "07", "14")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(day, "rollout-2026-07-14T10-00-00-019fa000-0000-7000-8000-000000000002.jsonl")
	if err := os.WriteFile(rollout, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	deps := rolloutwatch.Deps{
		Clock:     watchClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       &watchIDs{},
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	}
	cmd := cli.NewWatchCmd(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"codex", "--once", "--codex-home", codexHome, "--interval", "250ms"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch codex --once: %v", err)
	}

	var decoded struct {
		SchemaVersion string `json:"schema_version"`
		SessionsDir   string `json:"sessions_dir"`
		Once          bool   `json:"once"`
		TurnsEmitted  int    `json:"turns_emitted"`
		EventsEmitted int    `json:"events_emitted"`
		Errors        int    `json:"errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if decoded.SchemaVersion != "auspex.watch-codex.v1" || !decoded.Once {
		t.Errorf("output = %+v", decoded)
	}
	if decoded.SessionsDir != filepath.Join(codexHome, "sessions") {
		t.Errorf("sessions_dir = %q", decoded.SessionsDir)
	}
	if decoded.TurnsEmitted != 1 || decoded.EventsEmitted != 4 || decoded.Errors != 0 {
		t.Errorf("stats = %+v, want 1 turn / 4 events / 0 errors", decoded)
	}

	var rows int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM events WHERE provider = 'codex'`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 4 {
		t.Errorf("events rows = %d, want 4", rows)
	}
}
