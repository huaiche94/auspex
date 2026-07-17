// watch_swap_test.go: issue #92's wiring proof that App.RootCmd() swaps
// root.go's `watch` stub for cli.NewWatchCmd's real handlers exactly when
// the watcher deps are wired — and keeps the honest stub when they are
// not (the same conditional-swap assertion style gc/report use, since the
// watcher likewise persists through a real *sqlite.DB-backed event store
// with no fake-able frozen interface standing in for it).
package wiring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app/wiring"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/idgen"
	"github.com/huaiche94/auspex/internal/rolloutwatch"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

type watchFixedClock struct{ t time.Time }

func (c watchFixedClock) Now() time.Time { return c.t }

func TestApp_RootCmd_WatchCodexIsRealWhenDepsWired(t *testing.T) {
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

	services := fullFakeServices()
	services.Watch = &rolloutwatch.Deps{
		Clock:     watchFixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		IDs:       idgen.New(),
		Persister: claudetelemetry.NewEventStore(db),
		TxRunner:  db,
	}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// The stub declares no flags, so --once/--codex-home parsing
	// succeeding is itself part of the real-not-stub proof. The temp
	// codex home is empty: a scan over nothing must still emit the
	// versioned stats line (fail-open, never an error).
	root.SetArgs([]string{"watch", "codex", "--once", "--codex-home", t.TempDir()})

	if err := root.Execute(); err != nil {
		t.Fatalf("watch codex on the wired tree: %v (want the real handler, not the stub)", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("wired watch codex --once output is not JSON: %v\n%s", err, out.String())
	}
	if decoded["schema_version"] != "auspex.watch-codex.v1" {
		t.Errorf("schema_version = %v, want auspex.watch-codex.v1", decoded["schema_version"])
	}
	if decoded["once"] != true {
		t.Errorf("once = %v, want true", decoded["once"])
	}
}

func TestApp_RootCmd_WatchStaysStubWithoutDeps(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watch", "codex"})

	execErr := root.Execute()
	if execErr == nil {
		t.Fatal("watch codex without wired deps must stay the honest stub")
	}
	var derr *domain.Error
	if !errors.As(execErr, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("stub error = %v, want domain.Error{ErrCodeUnavailable}", execErr)
	}
}
