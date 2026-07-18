# internal/predictor/token/ — Stage 2: rule-based token-cost forecast for the upcoming turn

> 🌐 English | [繁體中文](README.zh-TW.md)

`RuleTokenForecaster` (`forecaster.go`) implements the frozen `app.TokenForecaster` port
(ADR-041, predictor-05b): it turns the Stage-1 `domain.ScopeEstimate` from
[`scope/`](../scope/README.md) plus prompt/session/progress features into a
`domain.TokenForecast` (TokensP50/P80/P90), per ADD §15.1 (token decomposition) and §15.2
(initial token predictor).

The base P50/P90 is empirical only when `RecentSimilarTurnTokens` supplies >= 8 similar-turn
samples (selected by the ADR-047 / #20 provider/model/effort fallback ladder, with the answering
rung disclosed as a reason code). Below that gate the base is a cold-start constant:
`baseTurnTokens` (6000) × the ADD §14.6 relative task-class multiplier (`coldstart.go`), with
P90 fixed at 2× P50. Six ADD §15.2 multipliers (scope, verification, complexity, retry,
progress, ambiguity) are combined by geometric mean, each capped at 3.0 and the combination at
6.0. P80 is a documented assumption: a log-space interpolation between P50 and P90, since ADD
§15.2 names no base P80.

Input/output split (#65 Phase 1, ADR-0053): alongside the authoritative total, every forecast
carries distinct input and output P50/P90 intervals (`Input/OutputTokensP50/P90`), the **input
interval structurally wider** than the output interval — Bai et al. 2026's finding that models
predict input tokens worse than output. The widening (`inputIntervalWideningFactor`, 1.5) and the
neutral central split (`defaultInputTokenShare`, 0.5) are uncalibrated structural defaults gated
on #11, never fitted coefficients; the paper's ~153:1 input:output magnitude is never imported.
The split never flips `Calibrated` to true.

Cold-start honesty note (issue #42, still open): the cold-start numbers are bootstrap constants,
not measurements. Before the #42 fix path the forecast was effectively prompt-blind — persisted
turn payloads carried only hash/length/approx-tokens, read-back collapsed every class to
`unknown`, and P50 came out ~3210 for essentially every prompt. The classifier-vocabulary and
payload fixes landed (acceptance proof:
`internal/integrationtest/forecast_prompt_conditioned_test.go`, which asserts P50 now differs by
task class in the direction the §14.6 multipliers imply), but until a deployment accumulates
>= 8 similar samples the forecast still responds to the prompt only through the class multiplier
and the §15.2 multipliers over these constants. Every result this wave is `Calibrated=false`
with Confidence at most medium — never a probability (Constitution §7 rule 7).

Output feeds [`quota/`](../quota/README.md) (delta scaling) and
[`internal/pricing`](../../pricing/README.md) (cost range). ADD sections cited above live in
[Auspex_ADD.md](../../../docs/design/Auspex_ADD.md). See `doc.go` for the package contract.
