// gc_test.go: `auspex gc`'s command surface — flag plumbing into
// orchestrator.GCRequest, the auspex.gc.v1 success envelope, and the
// frozen typed error contract on the not-wired and validation paths.
// The retention protocol itself is proven by internal/retention's own
// suite against real SQLite; this file tests the CLI layer with a fake
// runner, mirroring how diagnostics tests fake DBPinger.
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/retention"
)

// fakeRetentionRunner records the request and returns a canned result.
type fakeRetentionRunner struct {
	got    retention.RunRequest
	result retention.RunResult
	err    error
}

func (f *fakeRetentionRunner) Run(_ context.Context, req retention.RunRequest) (retention.RunResult, error) {
	f.got = req
	return f.result, f.err
}

func runGC(t *testing.T, deps orchestrator.GCDeps, args ...string) (string, error) {
	t.Helper()
	cmd := cli.NewGCCmd(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestGCCmd_SuccessOutputIsSchemaVersionedJSON(t *testing.T) {
	archive := &retention.ArchiveFile{Path: "/data/archive/events/2026-07/events-x.jsonl.gz", Rows: 3, SHA256: "abc", Bytes: 128}
	runner := &fakeRetentionRunner{result: retention.RunResult{
		RunID:                        "run-1",
		RanAt:                        time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
		RetentionDays:                90,
		Cutoff:                       time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
		Status:                       "ok",
		Tables:                       []retention.TableResult{{Table: "events", Selected: 3, Deleted: 3, Archive: archive}},
		UsageRollupRows:              2,
		CalibrationSamples:           1,
		CalibrationSamplesWithActual: 0,
		ReclaimableBytesEstimate:     4096,
		AutoVacuumMode:               "none",
	}}

	out, err := runGC(t, orchestrator.GCDeps{Runner: runner})
	if err != nil {
		t.Fatalf("gc: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	for key, want := range map[string]any{
		"schema_version":             "auspex.gc.v1",
		"run_id":                     "run-1",
		"status":                     "ok",
		"dry_run":                    false,
		"retention_days":             float64(90),
		"usage_rollup_rows":          float64(2),
		"calibration_samples":        float64(1),
		"reclaimable_bytes_estimate": float64(4096),
		"auto_vacuum_mode":           "none",
		"vacuum_ran":                 false,
	} {
		if got[key] != want {
			t.Errorf("output[%q] = %v, want %v", key, got[key], want)
		}
	}
	tables, ok := got["tables"].([]any)
	if !ok || len(tables) != 1 {
		t.Fatalf("output tables = %v", got["tables"])
	}
	table := tables[0].(map[string]any)
	if table["table"] != "events" || table["deleted"] != float64(3) || table["archive_path"] != archive.Path || table["archive_sha256"] != "abc" {
		t.Errorf("table entry = %v", table)
	}
}

func TestGCCmd_FlagsReachTheEngine(t *testing.T) {
	runner := &fakeRetentionRunner{result: retention.RunResult{Status: "ok"}}

	// Defaults: 90-day window, no dry run, no vacuum (ADR-046).
	if _, err := runGC(t, orchestrator.GCDeps{Runner: runner}); err != nil {
		t.Fatalf("gc: %v", err)
	}
	if runner.got.Policy.Days() != retention.DefaultRetentionDays || runner.got.DryRun || runner.got.Vacuum {
		t.Fatalf("default request = %+v", runner.got)
	}

	if _, err := runGC(t, orchestrator.GCDeps{Runner: runner}, "--dry-run", "--retention-days", "7", "--vacuum"); err != nil {
		t.Fatalf("gc with flags: %v", err)
	}
	if !runner.got.DryRun || runner.got.Policy.RetentionDays != 7 || !runner.got.Vacuum {
		t.Fatalf("flagged request = %+v", runner.got)
	}
}

func TestGCCmd_ErrorContract(t *testing.T) {
	// Not wired: the frozen unavailable error, not a panic or empty pass.
	_, err := runGC(t, orchestrator.GCDeps{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("unwired gc: err = %v, want unavailable domain.Error", err)
	}

	// Invalid flag value: validation, and the runner is never invoked.
	runner := &fakeRetentionRunner{result: retention.RunResult{Status: "ok"}}
	_, err = runGC(t, orchestrator.GCDeps{Runner: runner}, "--retention-days", "-3")
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("negative retention-days: err = %v, want validation domain.Error", err)
	}
	if runner.got.Policy.RetentionDays != 0 || runner.got.DryRun {
		t.Fatalf("runner was invoked despite validation failure: %+v", runner.got)
	}

	// Engine failures propagate as-is (fail-closed pass already recorded
	// its own retention_runs row; the CLI adds nothing).
	failing := &fakeRetentionRunner{err: &domain.Error{Code: domain.ErrCodeIntegrity, Message: "retention: archive verification failed", Retryable: false}}
	_, err = runGC(t, orchestrator.GCDeps{Runner: failing})
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeIntegrity {
		t.Fatalf("engine failure: err = %v, want integrity domain.Error", err)
	}
}
