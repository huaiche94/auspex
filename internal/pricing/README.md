# internal/pricing/ — local per-model price table: token forecast → estimated USD cost range

> 🌐 English | [繁體中文](README.zh-TW.md)

ADR-043 increment 1 ("cost forecast first"): a small, local, hand-maintained price table that
turns a token forecast into an estimated cost RANGE — never a point value, and never a measured
cost. Prices are placeholder defaults from Anthropic's published list prices at time of writing,
never fetched at runtime (local-first), and are expected to drift; they know nothing about
batch discounts or subscription plans. The forecast RANGE stays cache-blind (2-class); the
four-class `FourClassCost` (below, #66) is the cache-aware cost primitive for turns whose four
token classes are KNOWN.

Key entry points (`pricing.go`):

- `Table` / `DefaultTable()` / `NewTable(overrides)`: immutable family→price table
  (fable/mythos, opus, sonnet, haiku), matched by lowercase substring of the model ID in
  deterministic sorted order.
- `Table.Price(modelID)` resolves a `ModelPrice` (USD per MTok, input/output split) plus the
  family it matched; unknown models fall back to `DefaultFamily` (sonnet-class — Claude Code's
  default class; opus would systematically overstate by 5x, haiku understate).
- `Table.EstimateTurnCost(modelID, tokensLow, tokensHigh)` produces a `CostRange`: the token
  forecast is a total with no input/output split, so the low bound prices every token as input
  at the low quantile and the high bound as output at the high quantile — the spread is real
  uncertainty, so even P50 == P90 yields a genuine range. `ModelFamily` and `Source` disclose
  which price assumption produced the number.
- `Table.FourClassCost(modelID, nonCachedInput, cacheCreation, cacheRead, output)` (#66,
  arXiv:2604.22750, the ADR-043 cost axis) is the cache-aware model: a POINT `CostBreakdown` over
  KNOWN per-turn token counts, pricing each class separately under Anthropic explicit-cache rates
  (cache read = 10% of input, cache write = 125% of input — `CacheReadInputMultiplier` /
  `CacheCreationInputMultiplier`, derived from the base input rate so the one published
  relationship has one home). Its point: `CacheReadUSD` is usually the largest share of the bill
  even though a cache-read token is the cheapest class, because accumulated context is re-read
  across a turn's many round-trips — the mechanism behind #72 Phase 2's ~7–8× cost under-forecast.
  It never touches the forecast card (which stays 2-class until a four-class token FORECAST
  exists); the four classes are captured per managed turn today, so this half is gated on
  managed-run data volume, not on capture. `ok=false` on a negative (unmeasured) class; all-zero
  is a valid `$0`.

Consumers: [`internal/evaluation`](../evaluation/README.md)'s forecast card (which labels the
range "uncalibrated estimate", Constitution principle #2) and
[`internal/policy`](../policy/README.md)'s cost-budget rule (`costbudget.go`), fed from the
Stage-2 forecast of [`internal/predictor/token`](../predictor/token/README.md).

A YAML config override is a documented follow-up, not built yet — no production config loader is
wired into `cmd/auspex` today; `NewTable`'s overrides parameter is the existing programmatic
seam. This package has no `doc.go`; the package contract is the package comment at the top of
`pricing.go`.
