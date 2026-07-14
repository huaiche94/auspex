// Package pricing is ADR-043 increment 1 (cost forecast first): a small,
// local, per-model price table that turns a token forecast into an
// estimated USD cost RANGE — never a point value, and never a measured
// cost. ADR-043's Consequences section names this artifact explicitly:
// "A pricing table becomes a maintained artifact (per provider/model,
// config-overridable, never fetched at runtime by default — local-first)."
// Issue #14 is the surface this feeds: the per-prompt forecast card shown
// on every UserPromptSubmit, the statusline --emit-line output, and
// `auspex evaluate`.
//
// # Every number here is an ESTIMATE input, clearly labeled
//
// The default prices below are conservative placeholder defaults,
// hand-maintained from Anthropic's published per-model list prices at the
// time of writing — they are NOT fetched from any endpoint at runtime
// (local-first, ADR-043), they do not know about prompt caching, batch
// discounts, or subscription plans (a Claude Code subscription user's
// marginal cost is $0; the estimate is still useful as a consumption
// signal), and they will drift as providers reprice. Consumers MUST
// present the resulting CostRange as an estimate (the forecast-card
// presenter in internal/evaluation labels it "uncalibrated estimate" per
// Constitution principle #2's spirit: an uncalibrated number is never
// presented as more certain than it is).
//
// # Config override: documented follow-up, not built this wave
//
// ADR-043 calls the table "config-overridable". internal/config today
// exposes only a merged Raw map with no production loader wired into
// cmd/auspex's composition root (nothing in wire.go loads YAML config
// at all yet), so wiring a YAML override path here would mean building the
// config-loading plumbing as a side effect of a pricing package — out of
// this increment's honest scope (Constitution §7 rule 10: no abstractions
// a later milestone needs but this one doesn't). The programmatic seam
// already exists (NewTable merges caller overrides over the defaults);
// binding it to a `pricing:` YAML section is the documented follow-up for
// whichever wave wires config loading into the binary.
package pricing

import (
	"sort"
	"strings"
)

// ModelPrice is a per-model price in USD per million tokens (MTok), split
// by direction the way providers price it (input/prompt tokens vs.
// output/completion tokens).
type ModelPrice struct {
	InputUSDPerMTok  float64
	OutputUSDPerMTok float64
}

// CostRange is an estimated cost band for a forecast turn. Per ADR-043
// ("Cost = token forecast quantiles × price → a range, never a point"),
// a CostRange always carries distinct Low/High bounds — even a degenerate
// token forecast (P50 == P90) produces a genuine range, because the
// input/output price spread is itself real uncertainty (the token
// forecast is a TOTAL with no input/output split; see EstimateTurnCost).
type CostRange struct {
	LowUSD  float64
	HighUSD float64

	// ModelFamily is the price-table key the estimate resolved to
	// ("opus", "sonnet", "haiku") or DefaultFamily when the model was
	// unknown/unresolvable — surfaced so presenters can say WHICH price
	// assumption produced the number instead of implying precision.
	ModelFamily string

	// Source labels the provenance of the prices used, so a future
	// config-override layer can distinguish "shipped defaults" from "the
	// user's own declared prices" in every rendered card.
	Source string
}

// SourceDefaultTable is the CostRange.Source value for estimates produced
// from this package's shipped placeholder defaults (as opposed to a
// future config-declared override, which would stamp its own source).
const SourceDefaultTable = "default-price-table"

// DefaultFamily is the price-table key used when a model ID does not
// resolve to any known family — the evaluation pipeline's persisted
// prediction rows carry no model column at all (migration 0041), so the
// forecast-card presenter always resolves here today. Sonnet-class is the
// deliberate fallback: it is Claude Code's default model class, and
// assuming Opus pricing for every unknown model would systematically
// overstate cost by 5x while Haiku pricing would understate it.
const DefaultFamily = "default"

// defaultFamilyPrices are the shipped placeholder defaults (see the
// package comment: conservative, hand-maintained list-price defaults,
// never runtime-fetched, expected to drift). Keys are lowercase family
// substrings matched against the provider's model ID/display name.
var defaultFamilyPrices = map[string]ModelPrice{
	// Claude Fable/Mythos class (e.g. claude-fable-5): $10 in / $50 out.
	// #20 Phase 0: added when turn-level model stamping made family
	// resolution real — the owner's own sessions run this class.
	"fable": {InputUSDPerMTok: 10, OutputUSDPerMTok: 50},
	// Same model class behind Project Glasswing's claude-mythos-5 id.
	"mythos": {InputUSDPerMTok: 10, OutputUSDPerMTok: 50},
	// Claude Opus class, current generation (claude-opus-4-6/4-7/4-8):
	// $5 in / $25 out. (The retiring claude-opus-4-1 was $15/$75; this
	// table prices the family a live model id actually resolves to.)
	"opus": {InputUSDPerMTok: 5, OutputUSDPerMTok: 25},
	// Claude Sonnet class (e.g. claude-sonnet-5): $3 in / $15 out.
	"sonnet": {InputUSDPerMTok: 3, OutputUSDPerMTok: 15},
	// Claude Haiku class (e.g. claude-haiku-4-5): $1 in / $5 out.
	"haiku": {InputUSDPerMTok: 1, OutputUSDPerMTok: 5},
}

// defaultFallback is the DefaultFamily price (Sonnet-class; see
// DefaultFamily's doc comment for why).
var defaultFallback = ModelPrice{InputUSDPerMTok: 3, OutputUSDPerMTok: 15}

// Table resolves model identifiers to prices. Immutable after
// construction; safe for concurrent use.
type Table struct {
	families map[string]ModelPrice
	// familyOrder is the deterministic (sorted) match order for Price —
	// map iteration order is randomized in Go, and a model ID that
	// happened to contain two family substrings must resolve identically
	// on every call (forecast surfaces are compared byte-for-byte in
	// tests and shown to users repeatedly).
	familyOrder []string
	fallback    ModelPrice
}

// DefaultTable returns the shipped default price table.
func DefaultTable() *Table {
	return NewTable(nil)
}

// NewTable returns a table with overrides merged over the shipped
// defaults: an override keyed by an existing family name replaces that
// family's default price; a new key adds a family; an override keyed by
// DefaultFamily replaces the unknown-model fallback. This is the
// programmatic override seam the package comment describes — the YAML
// config binding on top of it is a documented follow-up.
func NewTable(overrides map[string]ModelPrice) *Table {
	families := make(map[string]ModelPrice, len(defaultFamilyPrices)+len(overrides))
	for k, v := range defaultFamilyPrices {
		families[k] = v
	}
	fallback := defaultFallback
	for k, v := range overrides {
		if k == DefaultFamily {
			fallback = v
			continue
		}
		families[strings.ToLower(k)] = v
	}
	order := make([]string, 0, len(families))
	for k := range families {
		order = append(order, k)
	}
	sort.Strings(order)
	return &Table{families: families, familyOrder: order, fallback: fallback}
}

// Price resolves modelID (a provider model ID or display name, e.g.
// "claude-opus-4-1-20250805" or "Opus 4.1") to a ModelPrice by
// case-insensitive family-substring match, returning the resolved family
// key alongside. An empty or unrecognized modelID resolves to the
// DefaultFamily fallback — an explicit, labeled degradation, never an
// error, because a missing model identity must not break a forecast
// surface (the same fail-open discipline the hook handlers use).
func (t *Table) Price(modelID string) (ModelPrice, string) {
	lowered := strings.ToLower(modelID)
	for _, family := range t.familyOrder {
		if family != "" && strings.Contains(lowered, family) {
			return t.families[family], family
		}
	}
	return t.fallback, DefaultFamily
}

// EstimateTurnCost converts a total-token forecast band [tokensLow,
// tokensHigh] (e.g. TokensP50..TokensP90 from domain.TokenForecast) into
// a CostRange for modelID. ok=false when the forecast carries no usable
// band (tokensHigh <= 0 or an inverted band) — per ADD principle 1,
// "unknown is not zero": no token forecast means no cost estimate, never
// a fabricated $0.
//
// Because domain.TokenForecast is a TOTAL token count with no
// input/output split (ADD §15.1's decomposition is not persisted per
// direction), the band brackets the true uncertainty honestly:
//
//	LowUSD  = tokensLow  × InputUSDPerMTok  (everything billed as input)
//	HighUSD = tokensHigh × OutputUSDPerMTok (everything billed as output)
//
// The result is deliberately wide — a range, never a point (ADR-043) —
// and consumers label it as an estimate.
func (t *Table) EstimateTurnCost(modelID string, tokensLow, tokensHigh int64) (CostRange, bool) {
	if tokensHigh <= 0 || tokensLow < 0 || tokensLow > tokensHigh {
		return CostRange{}, false
	}
	price, family := t.Price(modelID)
	const mtok = 1_000_000
	return CostRange{
		LowUSD:      float64(tokensLow) * price.InputUSDPerMTok / mtok,
		HighUSD:     float64(tokensHigh) * price.OutputUSDPerMTok / mtok,
		ModelFamily: family,
		Source:      SourceDefaultTable,
	}, true
}
