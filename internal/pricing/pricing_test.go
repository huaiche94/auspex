package pricing_test

import (
	"math"
	"testing"

	"github.com/huaiche94/auspex/internal/pricing"
)

// approxEqual compares two USD amounts with a tolerance that swamps float64
// rounding but is far tighter than any cost the estimates carry.
func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

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

// TestFourClassCost_ExactArithmetic pins the explicit-cache formula for opus
// ($5 in / $25 out): cache-read is 10% of input ($0.50/MTok) and cache-write
// is a 25% surcharge ($6.25/MTok). Each class priced independently, then
// summed.
func TestFourClassCost_ExactArithmetic(t *testing.T) {
	table := pricing.DefaultTable()
	// 1000 tokens of each class keeps the expected values easy to read.
	b, ok := table.FourClassCost("claude-opus-4-8", 1000, 1000, 1000, 1000)
	if !ok {
		t.Fatal("FourClassCost ok=false for valid counts")
	}
	if b.ModelFamily != "opus" {
		t.Errorf("family = %q, want opus", b.ModelFamily)
	}
	const mtok = 1_000_000
	wantIn := float64(1000) * 5 / mtok            // 0.005
	wantCreate := float64(1000) * 5 * 1.25 / mtok // 0.00625
	wantRead := float64(1000) * 5 * 0.10 / mtok   // 0.0005
	wantOut := float64(1000) * 25 / mtok          // 0.025
	if !approxEqual(b.NonCachedInputUSD, wantIn) ||
		!approxEqual(b.CacheCreationUSD, wantCreate) ||
		!approxEqual(b.CacheReadUSD, wantRead) ||
		!approxEqual(b.OutputUSD, wantOut) {
		t.Errorf("per-class = %+v; want in=%v create=%v read=%v out=%v",
			b, wantIn, wantCreate, wantRead, wantOut)
	}
	if !approxEqual(b.TotalUSD, wantIn+wantCreate+wantRead+wantOut) {
		t.Errorf("total = %v, want %v", b.TotalUSD, wantIn+wantCreate+wantRead+wantOut)
	}
}

// TestFourClassCost_CacheReadDominatesRealisticTurn is the #66 thesis made
// executable: for a realistic multi-round-trip Claude Code opus turn — a
// growing ~120K-token context re-read across ~20 tool round-trips (2.4M
// cache-read tokens), 40K newly cached, 30K output, 5K fresh input — the
// CHEAPEST token class (cache-read, $0.50/MTok) is nonetheless the LARGEST
// dollar share, because the context is re-read so many times within one turn.
// The total (~$2.23) lands right on the #72 Phase-2 opus median actual
// (~$1.90), i.e. cache-read traffic is what the 2-class forecast misses.
func TestFourClassCost_CacheReadDominatesRealisticTurn(t *testing.T) {
	table := pricing.DefaultTable()
	b, ok := table.FourClassCost("claude-opus-4-8",
		5_000,     // non-cached (fresh) input
		40_000,    // cache creation (newly cached context)
		2_400_000, // cache reads (context re-read across the turn's round-trips)
		30_000,    // output
	)
	if !ok {
		t.Fatal("FourClassCost ok=false for valid counts")
	}
	if b.CacheReadUSD <= b.OutputUSD ||
		b.CacheReadUSD <= b.CacheCreationUSD ||
		b.CacheReadUSD <= b.NonCachedInputUSD {
		t.Errorf("cache-read is not the largest class: %+v", b)
	}
	// Sanity: the total is in the single-dollars range the Phase-2 opus
	// median actual sits in, not the sub-cent range a 2-class token forecast
	// of the same fresh work (5K in + 30K out ≈ $0.78) would produce.
	if b.TotalUSD < 1.5 || b.TotalUSD > 3.0 {
		t.Errorf("total = %.4f, want ≈ $2.2 (single-dollars)", b.TotalUSD)
	}
}

// TestFourClassCost_HonestyGuards: a negative (unmeasured) class is rejected
// rather than treated as a measured 0, and an all-zero turn is a valid $0.
func TestFourClassCost_HonestyGuards(t *testing.T) {
	table := pricing.DefaultTable()
	if _, ok := table.FourClassCost("claude-opus-4-8", -1, 0, 0, 0); ok {
		t.Error("negative token count must yield ok=false (unknown is not zero)")
	}
	b, ok := table.FourClassCost("claude-opus-4-8", 0, 0, 0, 0)
	if !ok || b.TotalUSD != 0 {
		t.Errorf("all-zero turn = (%+v, ok=%v), want valid $0", b, ok)
	}
}
