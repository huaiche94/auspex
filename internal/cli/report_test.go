// report_test.go: `auspex report` command-layer tests — flag parsing,
// the text/JSON output split, and the auspex.report.v1 schema stamp —
// against a fake ReportGenerator (the CLI layer never constructs
// storage; Engine-level derivation is internal/report's own test suite).
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/report"
)

// fakeReportGenerator records the window it was asked for and returns a
// canned report.
type fakeReportGenerator struct {
	window time.Duration
	rep    report.Report
	err    error
}

func (f *fakeReportGenerator) GenerateReport(_ context.Context, window time.Duration) (report.Report, error) {
	f.window = window
	return f.rep, f.err
}

func cannedReport() report.Report {
	cost := 2.15
	return report.Report{
		SchemaVersion: report.ReportSchemaVersion,
		GeneratedAt:   "2026-07-16T12:00:00Z",
		WindowFrom:    "2026-07-09T12:00:00Z",
		WindowTo:      "2026-07-16T12:00:00Z",
		WindowLabel:   "7d",
		Totals: report.Totals{
			Turns: 3, TurnsCompleted: 2, TurnsUnclosed: 1,
			Sessions: 2, ActiveDays: 1,
			CostUSD: &cost, CostAttributedTurns: 2,
		},
		RightSizing: report.RightSizing{
			MinCohortTurns: report.MinCohortTurns,
			Note:           "not enough data yet (need >=8 cost-attributed turns per task-class x model/effort cohort)",
		},
		Takeaways: []report.Takeaway{{
			Case:     report.CaseExpensiveTurns,
			Title:    "Where the money went",
			Fired:    true,
			Analysis: "Costliest turn was $43.94.",
			Lesson:   "Plan first.",
			Action:   "Scope the task.",
		}},
	}
}

func runReport(t *testing.T, gen cli.ReportGenerator, args ...string) (string, error) {
	t.Helper()
	cmd := cli.NewReportCmd(gen)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestReportCmd_DefaultWindowAndTextOutput(t *testing.T) {
	gen := &fakeReportGenerator{rep: cannedReport()}
	out, err := runReport(t, gen)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if gen.window != 7*24*time.Hour {
		t.Errorf("window = %v, want the 7d default", gen.window)
	}
	for _, want := range []string{"Auspex usage report", "Totals", "not enough data yet", "$2.15", "Actionable takeaways", "Where the money went", "action:"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"schema_version"`) {
		t.Errorf("default output must be text, not JSON:\n%s", out)
	}
}

func TestReportCmd_WindowFlagForms(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"36h", 36 * time.Hour},
	} {
		gen := &fakeReportGenerator{rep: cannedReport()}
		if _, err := runReport(t, gen, "--window", tc.flag); err != nil {
			t.Fatalf("--window %s: %v", tc.flag, err)
		}
		if gen.window != tc.want {
			t.Errorf("--window %s parsed to %v, want %v", tc.flag, gen.window, tc.want)
		}
	}
}

func TestReportCmd_InvalidWindowIsTypedValidationError(t *testing.T) {
	for _, bad := range []string{"nope", "0d", "-3d", "-4h", "0s", ""} {
		gen := &fakeReportGenerator{rep: cannedReport()}
		_, err := runReport(t, gen, "--window", bad)
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
			t.Errorf("--window %q: err = %v, want a validation domain.Error", bad, err)
		}
		if gen.window != 0 {
			t.Errorf("--window %q: generator was called despite the invalid flag", bad)
		}
	}
}

func TestReportCmd_JSONOutputIsSchemaVersioned(t *testing.T) {
	gen := &fakeReportGenerator{rep: cannedReport()}
	out, err := runReport(t, gen, "--json")
	if err != nil {
		t.Fatalf("report --json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("--json output is not one JSON document: %v\n%s", err, out)
	}
	if decoded["schema_version"] != "auspex.report.v1" {
		t.Errorf("schema_version = %v, want auspex.report.v1", decoded["schema_version"])
	}
	totals, ok := decoded["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals missing from JSON output: %s", out)
	}
	if totals["cost_usd"] != 2.15 {
		t.Errorf("totals.cost_usd = %v, want 2.15", totals["cost_usd"])
	}
	takeaways, ok := decoded["takeaways"].([]any)
	if !ok || len(takeaways) == 0 {
		t.Fatalf("takeaways missing from JSON output: %s", out)
	}
	first, _ := takeaways[0].(map[string]any)
	if first["case"] != "expensive_turns" || first["action"] == "" {
		t.Errorf("takeaway[0] = %v, want the expensive_turns case with an action", first)
	}
}

func TestReportCmd_GeneratorErrorIsTypedInternalError(t *testing.T) {
	gen := &fakeReportGenerator{err: errors.New("boom")}
	_, err := runReport(t, gen)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeInternal {
		t.Fatalf("err = %v, want an internal domain.Error", err)
	}
	if !strings.Contains(derr.Message, "boom") {
		t.Errorf("Message = %q, want the generator error included", derr.Message)
	}
}
