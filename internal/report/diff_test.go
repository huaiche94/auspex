// diff_test.go: #144's quality-aware regression diff — pure in-memory
// report fixtures (the baseline IS the report wire shape, so no DB is
// involved at this layer).
package report

import (
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func reportWith(cost *float64, perNode *float64) Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		GeneratedAt:   "2026-07-29T12:00:00Z",
		WindowLabel:   "7d",
		Totals:        Totals{CostUSD: cost},
		OutcomeEconomics: OutcomeEconomics{
			CostPerCompletedNodeMedianUSD: perNode,
		},
	}
}

func TestBuildDiff_QualityAwareOverall(t *testing.T) {
	cases := []struct {
		name        string
		baseline    Report
		current     Report
		wantOverall DiffVerdict
	}{
		{
			// Spend +100% beyond both floors, per-node worse too.
			name:        "spend up, per-node up -> regressed",
			baseline:    reportWith(f(10), f(2.0)),
			current:     reportWith(f(20), f(4.0)),
			wantOverall: VerdictRegressed,
		},
		{
			// Spend up, but each completed node got MUCH cheaper: the
			// quality-aware rule refuses to call it a regression.
			name:        "spend up, per-node improved -> not a regression",
			baseline:    reportWith(f(10), f(4.0)),
			current:     reportWith(f(20), f(2.0)),
			wantOverall: VerdictWithinNoise,
		},
		{
			name:        "spend down -> improved",
			baseline:    reportWith(f(20), f(2.0)),
			current:     reportWith(f(10), f(2.0)),
			wantOverall: VerdictImproved,
		},
		{
			// +4% is inside the 10% relative floor.
			name:        "small move -> within noise",
			baseline:    reportWith(f(100), nil),
			current:     reportWith(f(104), nil),
			wantOverall: VerdictWithinNoise,
		},
		{
			// $0.30 move exceeds 10% relative but not the $0.50 absolute floor.
			name:        "tiny absolutes -> within noise",
			baseline:    reportWith(f(1.0), nil),
			current:     reportWith(f(1.3), nil),
			wantOverall: VerdictWithinNoise,
		},
		{
			name:        "no baseline cost -> incomparable",
			baseline:    reportWith(nil, nil),
			current:     reportWith(f(10), nil),
			wantOverall: VerdictIncomparable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := BuildDiff(tc.baseline, tc.current)
			if d.Overall != tc.wantOverall {
				t.Errorf("Overall = %q (%s), want %q", d.Overall, d.OverallNote, tc.wantOverall)
			}
			if d.SchemaVersion != DiffSchemaVersion {
				t.Errorf("SchemaVersion = %q", d.SchemaVersion)
			}
		})
	}
}

func TestBuildDiff_IncomparableMetricCarriesWhy(t *testing.T) {
	d := BuildDiff(reportWith(f(10), nil), reportWith(f(10), f(2)))
	var perNode *MetricDelta
	for i := range d.Metrics {
		if d.Metrics[i].Metric == "cost_per_completed_node_median_usd" {
			perNode = &d.Metrics[i]
		}
	}
	if perNode == nil || perNode.Verdict != VerdictIncomparable {
		t.Fatalf("per-node metric = %+v, want incomparable", perNode)
	}
	if !strings.Contains(perNode.Note, "baseline") {
		t.Errorf("incomparable note %q does not name the missing side", perNode.Note)
	}
}

func TestRenderDiffText_Smoke(t *testing.T) {
	d := BuildDiff(reportWith(f(10), f(4)), reportWith(f(20), f(2)))
	text := RenderDiffText(d)
	for _, want := range []string{"total_cost_usd", "overall:", "cost per completed node IMPROVED"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered diff missing %q:\n%s", want, text)
		}
	}
}
