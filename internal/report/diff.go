// diff.go: the #144 cost-regression diff (Cost Guard, ADR-0057) — two
// auspex.report.v1 documents compared metric by metric under a
// documented noise floor, with a QUALITY-AWARE overall verdict: spend
// moving up is only called a regression when the outcome economics
// (#140's cost per completed node) did not improve alongside it. The
// baseline is deliberately NOT a new artifact format: any saved
// `auspex report --json` output is a baseline (versioned, self-dating),
// which is what keeps the CI-gate slice (M15) a pure consumer.
package report

import (
	"fmt"
	"math"
	"strings"
)

// DiffSchemaVersion stamps the `auspex report diff --json` wire shape.
const DiffSchemaVersion = "auspex.report-diff.v1"

// Noise floor — documented operational defaults (the CacheChurn*
// constants convention; M13's benchmark work owns fitting real ones).
// A metric's move is "within noise" unless BOTH the relative and the
// absolute floor are exceeded — small windows produce large percentages
// on tiny absolutes, and vice versa.
const (
	DiffNoiseRelPercent = 10.0 // relative floor, percent of baseline
	DiffNoiseAbsCostUSD = 0.50 // absolute floor for dollar metrics
	DiffNoiseAbsTokens  = 50_000
	DiffNoiseAbsPerNode = 0.25 // dollars, cost-per-completed-node
)

// DiffVerdict is one metric's (or the overall) comparison outcome.
type DiffVerdict string

const (
	VerdictImproved     DiffVerdict = "improved"
	VerdictRegressed    DiffVerdict = "regressed"
	VerdictWithinNoise  DiffVerdict = "within_noise"
	VerdictIncomparable DiffVerdict = "incomparable" // a side is missing/gated
)

// MetricDelta is one compared metric. Baseline/Current are nil when
// that side's report did not carry the metric (below its own honesty
// gate, or predating the field) — which makes the verdict incomparable,
// never a fabricated zero-delta.
type MetricDelta struct {
	Metric   string      `json:"metric"`
	Baseline *float64    `json:"baseline,omitempty"`
	Current  *float64    `json:"current,omitempty"`
	Delta    *float64    `json:"delta,omitempty"`
	Verdict  DiffVerdict `json:"verdict"`
	// Note explains the verdict (which floor gated it, or which side is
	// missing) — the diff always carries its WHY.
	Note string `json:"note,omitempty"`
}

// Diff is the full report-vs-baseline comparison.
type Diff struct {
	SchemaVersion       string `json:"schema_version"`
	BaselineGeneratedAt string `json:"baseline_generated_at"`
	CurrentGeneratedAt  string `json:"current_generated_at"`
	BaselineWindow      string `json:"baseline_window"`
	CurrentWindow       string `json:"current_window"`

	Metrics []MetricDelta `json:"metrics"`

	// Overall is the quality-aware verdict: regressed only when total
	// spend regressed AND the cost per completed node did not improve —
	// spending more to complete verified work more cheaply per node is
	// not a regression (ADR-0057 / the Wattage lesson: quality evidence
	// gates the alarm).
	Overall     DiffVerdict `json:"overall"`
	OverallNote string      `json:"overall_note"`
}

// BuildDiff compares current against baseline.
func BuildDiff(baseline, current Report) Diff {
	d := Diff{
		SchemaVersion:       DiffSchemaVersion,
		BaselineGeneratedAt: baseline.GeneratedAt,
		CurrentGeneratedAt:  current.GeneratedAt,
		BaselineWindow:      baseline.WindowLabel,
		CurrentWindow:       current.WindowLabel,
	}

	totalCost := compareMetric("total_cost_usd",
		baseline.Totals.CostUSD, current.Totals.CostUSD,
		DiffNoiseAbsCostUSD, false /* up is bad */)
	perNode := compareMetric("cost_per_completed_node_median_usd",
		baseline.OutcomeEconomics.CostPerCompletedNodeMedianUSD,
		current.OutcomeEconomics.CostPerCompletedNodeMedianUSD,
		DiffNoiseAbsPerNode, false)
	cacheRatio := compareMetric("cache_read_per_fresh_input",
		baseline.CacheHygiene.CacheReadPerFreshInput,
		current.CacheHygiene.CacheReadPerFreshInput,
		0.5, true /* up is good */)
	tokens := compareMetric("output_tokens",
		int64PtrToFloat(baseline.Totals.Tokens.Output),
		int64PtrToFloat(current.Totals.Tokens.Output),
		DiffNoiseAbsTokens, false)

	d.Metrics = []MetricDelta{totalCost, perNode, cacheRatio, tokens}

	// Quality-aware overall (the load-bearing rule): total spend
	// regressing alone is not the alarm — it must NOT be accompanied by
	// an improving cost per completed node.
	switch {
	case totalCost.Verdict == VerdictRegressed && perNode.Verdict == VerdictImproved:
		d.Overall = VerdictWithinNoise
		d.OverallNote = "total spend rose beyond noise, but cost per completed node IMPROVED — more verified work per dollar is not a regression"
	case totalCost.Verdict == VerdictRegressed:
		d.Overall = VerdictRegressed
		d.OverallNote = "total spend rose beyond noise with no outcome-economics improvement (cost per completed node: " + string(perNode.Verdict) + ")"
	case totalCost.Verdict == VerdictImproved:
		d.Overall = VerdictImproved
		d.OverallNote = "total spend fell beyond noise"
	case totalCost.Verdict == VerdictIncomparable:
		d.Overall = VerdictIncomparable
		d.OverallNote = "total spend is not comparable across the two reports: " + totalCost.Note
	default:
		d.Overall = VerdictWithinNoise
		d.OverallNote = "total spend moved within the documented noise floor"
	}
	return d
}

// compareMetric produces one MetricDelta under the shared relative
// floor plus the metric's absolute floor. upIsGood flips the
// improved/regressed mapping for metrics where growth is healthy.
func compareMetric(name string, baseline, current *float64, absFloor float64, upIsGood bool) MetricDelta {
	m := MetricDelta{Metric: name, Baseline: baseline, Current: current}
	if baseline == nil || current == nil {
		m.Verdict = VerdictIncomparable
		side := "baseline"
		if baseline != nil {
			side = "current"
		}
		m.Note = side + " report does not carry this metric (below its honesty gate or predating it)"
		return m
	}
	delta := *current - *baseline
	m.Delta = &delta

	abs := math.Abs(delta)
	rel := math.Inf(1)
	if *baseline != 0 {
		rel = 100 * abs / math.Abs(*baseline)
	}
	if abs < absFloor || rel < DiffNoiseRelPercent {
		m.Verdict = VerdictWithinNoise
		m.Note = fmt.Sprintf("within noise (floors: %.4g absolute, %.4g%% relative)", absFloor, DiffNoiseRelPercent)
		return m
	}
	worse := delta > 0
	if upIsGood {
		worse = delta < 0
	}
	if worse {
		m.Verdict = VerdictRegressed
	} else {
		m.Verdict = VerdictImproved
	}
	return m
}

func int64PtrToFloat(v *int64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

// RenderDiffText renders the diff for humans.
func RenderDiffText(d Diff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Auspex report diff  baseline %s (%s) -> current %s (%s)\n",
		d.BaselineGeneratedAt, d.BaselineWindow, d.CurrentGeneratedAt, d.CurrentWindow)
	for _, m := range d.Metrics {
		val := func(v *float64) string {
			if v == nil {
				return "-"
			}
			return fmt.Sprintf("%.4g", *v)
		}
		fmt.Fprintf(&b, "  %-36s %s -> %s  [%s]", m.Metric, val(m.Baseline), val(m.Current), m.Verdict)
		if m.Note != "" {
			fmt.Fprintf(&b, "  %s", m.Note)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "overall: %s — %s\n", d.Overall, d.OverallNote)
	return b.String()
}
