// report_swap_test.go: issue #91's wiring proof that App.RootCmd() swaps
// root.go's `report` stub for cli.NewReportCmd's real handler exactly
// when a report engine is wired — and keeps the honest stub when it is
// not (the same conditional-swap assertion style gc_swap_test.go uses,
// since the report engine likewise needs a real *sqlite.DB with no
// fake-able frozen interface standing in for it).
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
	"github.com/huaiche94/auspex/internal/report"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

type reportFixedClock struct{ t time.Time }

func (c reportFixedClock) Now() time.Time { return c.t }

func TestApp_RootCmd_ReportIsRealWhenEngineWired(t *testing.T) {
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
	services.Report = &report.Engine{
		DB:       db,
		Clock:    reportFixedClock{t: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)},
		Location: time.UTC,
	}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// The stub declares no flags, so --json parsing succeeding is itself
	// part of the real-not-stub proof.
	root.SetArgs([]string{"report", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("report on the wired tree: %v (want the real handler, not the stub)", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("wired report --json output is not JSON: %v\n%s", err, out.String())
	}
	if decoded["schema_version"] != "auspex.report.v1" {
		t.Errorf("schema_version = %v, want auspex.report.v1", decoded["schema_version"])
	}
}

func TestApp_RootCmd_ReportStaysStubWithoutEngine(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"report"})

	execErr := root.Execute()
	var derr *domain.Error
	if !errors.As(execErr, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("unwired report: err = %v, want the stub's unavailable domain.Error", execErr)
	}
}
