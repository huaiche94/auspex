package quota

// defaultQuotaDeltaP50/P90 are the cold-start "typical turn" quota
// percentage-point delta, used when no empirical per-provider/model/task-
// class delta distribution exists yet (ADD §15.3 step 5, "依
// provider/model/task class 計算 empirical P50/P90" — unreachable this
// wave with no durable telemetry store, per this package's doc comment).
//
// ADD §15.3 does not name an exact default delta value (unlike §14.6's
// token-multiplier table), so these are this package's own conservative,
// documented bootstrap constants: a "typical" turn is assumed to consume
// roughly 2 percentage points of a rolling quota window at P50, with a
// P90 tail of 6 points (3x) to reflect that some turns burn substantially
// more (long tool-call chains, retries, large context re-reads) without
// assuming a fixed token ceiling (ADD §15.3's explicit "不得假設固定
// token ceiling"). These are starting points, not measured values, and
// are expected to be replaced by StatisticalQuotaForecaster's empirical
// quantiles (Preflight_Predictor_Design_Supplement.md's Version 2) once
// durable per-window delta samples exist.
const (
	defaultQuotaDeltaP50 = 2.0
	defaultQuotaDeltaP90 = 6.0
)

// tokenScaledDeltaFloor/Ceiling bound the token-forecast-derived quota
// delta adjustment (see forecaster.go's tokenAdjustedDelta) so a single
// very large or very small TokenForecast can sharpen but never replace the
// conservative default outside a documented, bounded range — mirrors
// internal/predictor/token's per-multiplier capping discipline (ADD
// §15.2's "避免乘數爆炸，並做 caps" instruction, reused here since §15.3
// gives no equivalent explicit cap for the quota delta model).
const (
	tokenScaledDeltaFloor   = 0.5
	tokenScaledDeltaCeiling = 3.0
)

// defaultContextGrowthP50/P90 are the cold-start "typical turn" net
// context-window growth, as a fraction of the context window's total
// token capacity (0-1), used when no same-session delta history exists to
// calibrate against (ADD §15.9: "以 same-session deltas calibrate" —
// unreachable this wave for the same reason as the quota defaults above).
// Context growth is normally smaller as a fraction than quota burn since
// provider cache/compaction reduces net growth relative to total tokens
// processed (ADD §15.9: "Net growth 不等於 total tokens，因 provider
// cache/compaction"); these bootstrap values are deliberately more
// conservative (larger) than a "cache always helps" assumption would
// suggest, so a first cold-start estimate does not understate risk.
const (
	defaultContextGrowthP50Fraction = 0.03
	defaultContextGrowthP90Fraction = 0.10
)
