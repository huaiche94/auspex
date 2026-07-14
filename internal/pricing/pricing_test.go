package pricing_test

import (
	"testing"

	"github.com/huaiche94/auspex/internal/pricing"
)

// TestDefaultTable_FamilyResolution proves the case-insensitive
// family-substring match resolves both real provider model IDs and
// human-facing display names (the two shapes
// internal/providers/claude.StatusLineSnapshot actually carries —
// ModelID "claude-opus-4-1-20250805" and ModelDisplayName "Opus 4.1").
func TestDefaultTable_FamilyResolution(t *testing.T) {
	table := pricing.DefaultTable()

	cases := []struct {
		modelID    string
		wantFamily string
	}{
		{"claude-opus-4-1-20250805", "opus"},
		{"Opus 4.1", "opus"},
		{"claude-sonnet-4-20250514", "sonnet"},
		{"Sonnet 4.5", "sonnet"},
		{"claude-haiku-4-5", "haiku"},
		{"Haiku 3.5", "haiku"},
		{"claude-fable-5", "fable"},
		{"Fable 5", "fable"},
		{"claude-fable-5[1m]", "fable"},
		{"claude-mythos-5", "mythos"},
	}
	for _, tc := range cases {
		price, family := table.Price(tc.modelID)
		if family != tc.wantFamily {
			t.Errorf("Price(%q) family = %q, want %q", tc.modelID, family, tc.wantFamily)
		}
		if price.InputUSDPerMTok <= 0 || price.OutputUSDPerMTok <= 0 {
			t.Errorf("Price(%q) = %+v, want positive input/output prices", tc.modelID, price)
		}
		if price.InputUSDPerMTok >= price.OutputUSDPerMTok {
			t.Errorf("Price(%q): input price %v >= output price %v — every shipped Claude default prices output above input", tc.modelID, price.InputUSDPerMTok, price.OutputUSDPerMTok)
		}
	}
}

// TestDefaultTable_UnknownModelFallsBackExplicitly proves an unknown (or
// entirely absent) model identity resolves to the labeled DefaultFamily
// fallback rather than erroring or returning a zero price — the explicit
// degradation the forecast surfaces rely on, since prediction rows
// (migration 0041) persist no model column at all.
func TestDefaultTable_UnknownModelFallsBackExplicitly(t *testing.T) {
	table := pricing.DefaultTable()
	for _, modelID := range []string{"", "gpt-5", "some-future-model"} {
		price, family := table.Price(modelID)
		if family != pricing.DefaultFamily {
			t.Errorf("Price(%q) family = %q, want %q", modelID, family, pricing.DefaultFamily)
		}
		if price.InputUSDPerMTok <= 0 || price.OutputUSDPerMTok <= 0 {
			t.Errorf("Price(%q) fallback = %+v, want positive prices", modelID, price)
		}
	}
}

// TestEstimateTurnCost_RangeNeverPoint is ADR-043's core presentation
// rule ("a range, never a point"): even a degenerate token band (P50 ==
// P90) must yield LowUSD strictly below HighUSD, because the
// input/output price spread is itself genuine uncertainty when the token
// forecast carries no direction split.
func TestEstimateTurnCost_RangeNeverPoint(t *testing.T) {
	table := pricing.DefaultTable()

	cr, ok := table.EstimateTurnCost("claude-sonnet-4-20250514", 10_000, 10_000)
	if !ok {
		t.Fatal("EstimateTurnCost returned ok=false for a valid band")
	}
	if !(cr.LowUSD < cr.HighUSD) {
		t.Errorf("degenerate band: LowUSD %v not strictly below HighUSD %v — a cost estimate must be a range, never a point (ADR-043)", cr.LowUSD, cr.HighUSD)
	}
	if cr.Source != pricing.SourceDefaultTable {
		t.Errorf("Source = %q, want %q", cr.Source, pricing.SourceDefaultTable)
	}
	if cr.ModelFamily != "sonnet" {
		t.Errorf("ModelFamily = %q, want sonnet", cr.ModelFamily)
	}

	// Sanity-check the arithmetic on the shipped sonnet defaults
	// ($3/$15 per MTok): 10k tokens -> $0.03 low, $0.15 high.
	if got, want := cr.LowUSD, 0.03; got != want {
		t.Errorf("LowUSD = %v, want %v", got, want)
	}
	if got, want := cr.HighUSD, 0.15; got != want {
		t.Errorf("HighUSD = %v, want %v", got, want)
	}
}

// TestEstimateTurnCost_NoForecastMeansNoEstimate: "unknown is not zero"
// (ADD principle 1) — an absent/invalid token band produces ok=false,
// never a fabricated $0 range.
func TestEstimateTurnCost_NoForecastMeansNoEstimate(t *testing.T) {
	table := pricing.DefaultTable()
	cases := []struct {
		name      string
		low, high int64
	}{
		{"both zero", 0, 0},
		{"negative high", 0, -1},
		{"negative low", -5, 10},
		{"inverted band", 20, 10},
	}
	for _, tc := range cases {
		if _, ok := table.EstimateTurnCost("opus", tc.low, tc.high); ok {
			t.Errorf("%s: EstimateTurnCost(%d, %d) ok=true, want false", tc.name, tc.low, tc.high)
		}
	}
}

// TestNewTable_OverridesMergeOverDefaults exercises the programmatic
// override seam (the config-file binding on top of it is a documented
// follow-up — see the package comment): overriding one family leaves the
// others at their defaults, a new family key becomes matchable, and a
// DefaultFamily key replaces the unknown-model fallback.
func TestNewTable_OverridesMergeOverDefaults(t *testing.T) {
	table := pricing.NewTable(map[string]pricing.ModelPrice{
		"opus":                {InputUSDPerMTok: 10, OutputUSDPerMTok: 50},
		"NewModel":            {InputUSDPerMTok: 2, OutputUSDPerMTok: 4},
		pricing.DefaultFamily: {InputUSDPerMTok: 7, OutputUSDPerMTok: 9},
	})

	if price, family := table.Price("claude-opus-4"); family != "opus" || price.InputUSDPerMTok != 10 || price.OutputUSDPerMTok != 50 {
		t.Errorf("opus override not applied: family=%q price=%+v", family, price)
	}
	if price, family := table.Price("claude-sonnet-4"); family != "sonnet" || price.InputUSDPerMTok != 3 {
		t.Errorf("sonnet default disturbed by unrelated override: family=%q price=%+v", family, price)
	}
	if _, family := table.Price("provider-newmodel-1"); family != "newmodel" {
		t.Errorf("new family key not matchable: family=%q, want newmodel", family)
	}
	if price, family := table.Price("unknown"); family != pricing.DefaultFamily || price.InputUSDPerMTok != 7 || price.OutputUSDPerMTok != 9 {
		t.Errorf("fallback override not applied: family=%q price=%+v", family, price)
	}
}
