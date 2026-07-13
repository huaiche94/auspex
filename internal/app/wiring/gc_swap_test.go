// gc_swap_test.go: ADR-046's wiring proof that App.RootCmd() swaps
// root.go's `gc` stub for cli.NewGCCmd's real handler exactly when a
// retention engine is wired — and keeps the honest stub when it is not
// (the same conditional-swap assertion style the pause/decision subtrees
// use, since gc's engine likewise has no fake-able frozen interface).
package wiring_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app/wiring"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/retention"
)

// fakeGCRunner satisfies orchestrator.RetentionRunner without a database.
type fakeGCRunner struct{}

func (fakeGCRunner) Run(_ context.Context, _ retention.RunRequest) (retention.RunResult, error) {
	return retention.RunResult{RunID: "run-wired", Status: "ok"}, nil
}

func TestApp_RootCmd_GCIsRealWhenRunnerWired(t *testing.T) {
	services := fullFakeServices()
	services.GC = orchestrator.GCDeps{Runner: fakeGCRunner{}}
	a, err := wiring.New(services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// The stub declares no flags, so --dry-run parsing succeeding is
	// itself part of the real-not-stub proof.
	root.SetArgs([]string{"gc", "--dry-run"})

	if err := root.Execute(); err != nil {
		t.Fatalf("gc on the wired tree: %v (want the real handler, not the stub)", err)
	}
	if !strings.Contains(out.String(), `"run_id":"run-wired"`) || !strings.Contains(out.String(), `"schema_version":"auspex.gc.v1"`) {
		t.Errorf("output missing the real handler's auspex.gc.v1 envelope:\n%s", out.String())
	}
}

func TestApp_RootCmd_GCStaysStubWithoutRunner(t *testing.T) {
	a, err := wiring.New(fullFakeServices())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := a.RootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"gc"})

	execErr := root.Execute()
	var derr *domain.Error
	if !errors.As(execErr, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("unwired gc: err = %v, want the stub's unavailable domain.Error", execErr)
	}
}
