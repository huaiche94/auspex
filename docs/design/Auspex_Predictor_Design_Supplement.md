# Auspex Predictor Design Supplement

> 🌐 English | [繁體中文](Auspex_Predictor_Design_Supplement.zh-TW.md)

> Status: Accepted — the Forecast-layer gap identified in this document
> (Scope Estimation feeding Risk Estimation with no explicit Token/Quota
> forecast stage between them) is formalized as
> `docs/adr/0041-predictor-forecast-layer.md`. The four pipeline
> interfaces below are frozen in `internal/app/ports.go`; no
> implementation exists yet.\
> Purpose: Companion document to `Auspex_ADD.md`\
> Scope: Scope Estimation, Token Prediction, Risk Estimation, and
> Checkpoint Decision

# Overview

This document defines the long-term design of the Auspex prediction
system.

The prediction engine is responsible for answering one question **before
an AI coding agent executes a turn**:

> Given the current repository, prompt, session history, and provider
> state, how likely is this next turn to complete successfully?

The prediction engine is provider-neutral and is composed of three
evolutionary stages.

------------------------------------------------------------------------

# External Evidence — Why Ranges, Not Probabilities

An independent study of eight frontier models on SWE-bench (Bai et al.,
*How Do AI Agents Spend Your Money? Analyzing and Predicting Token
Consumption in Agentic Coding Tasks*, arXiv:2604.22750, 2026) measured
exactly the task this document designs for — predicting an agent's token
cost before it runs — and its findings are the external grounding for the
humility baked into every design choice below:

-   **Token usage is high-variance and partly irreducible.** Runs of the
    same task differed by up to **30×** in total tokens, worst on the most
    expensive tasks. Precise per-turn prediction is not a solved problem
    waiting on effort; it is bounded by the phenomenon itself.
-   **Self-prediction is weak.** Frontier models predicted their own token
    usage only weakly-to-moderately (Pearson ≤ **0.39**, best case output
    tokens; input worse) and **systematically underestimated** real usage.
    Any self-prediction path must widen the input axis and apply an upward
    bias correction.
-   **Perceived difficulty is not cost.** Expert difficulty ratings
    correlated only weakly with real consumption (Kendall **τ_b = 0.32**).
    A scope estimate must not read surface prompt complexity as a cost
    proxy — consistent with "Why Line Count Alone Is Wrong" below.
-   **Cost is input-dominated, and cache-read dominates the dollars.**
    Input/output ratios averaged ~153; under explicit-cache pricing the
    re-read of accumulated context — not output — is the largest share of
    the bill. Estimating dollars means estimating context growth and
    re-reads, per token class.

These numbers are measured on other models and are **not** imported as
Auspex coefficients; they justify the *shape* of the design — ranges over
point estimates, uncalibrated scores never labelled probabilities
(Constitution §7 rule 7), wider input intervals, and a preference for
**observed** failure signals (e.g. repeated same-file operations) over
predicted token counts. The roadmap that operationalises these findings —
the cache-aware cost decomposition and the repeated-file-operation risk
factor — is `docs/backlog/token-cost-prediction-research.md`.

------------------------------------------------------------------------

# Evolution Roadmap

## Version 1 --- Rule Predictor

Characteristics:

-   Deterministic
-   No ML
-   Explainable
-   Fast
-   No training required

Uses:

-   heuristic scoring
-   repository statistics
-   session telemetry
-   manually tuned multipliers

Output:

-   P50
-   P80
-   P90
-   Risk score
-   Confidence
-   Explanation

------------------------------------------------------------------------

## Version 2 --- Statistical Predictor

Characteristics:

-   Uses historical telemetry
-   Learns repository-specific distributions
-   Quantile estimation
-   Confidence calibration

Possible algorithms:

-   Quantile Regression
-   Bayesian estimation
-   Distribution fitting
-   Empirical probability tables

------------------------------------------------------------------------

## Version 3 --- ML Predictor

Characteristics:

-   Learns from thousands of completed turns
-   Repository-aware
-   User-aware
-   Provider-aware

Possible algorithms:

-   Gradient Boosted Trees
-   Quantile Regression Forest
-   XGBoost
-   LightGBM
-   Binary Classification
-   Survival Analysis

------------------------------------------------------------------------

# Pipeline Interfaces

Each pipeline stage below is frozen as a narrow, single-method Go
interface (`internal/app/ports.go`, `docs/adr/0041-predictor-forecast-layer.md`).
This is what makes the Version 1/2/3 roadmap above an actual migration
path instead of a rewrite: any one stage can be swapped for a better
implementation without touching the others, because callers depend only
on the interface, never on a concrete type.

```go
type ScopeEstimator interface {
    EstimateScope(context.Context, EstimateScopeRequest) (domain.ScopeEstimate, error)
}

type TokenForecaster interface {
    ForecastTokens(context.Context, ForecastTokensRequest) (domain.TokenForecast, error)
}

type QuotaForecaster interface {
    ForecastQuota(context.Context, ForecastQuotaRequest) (domain.QuotaForecast, error)
}

type RiskCombiner interface {
    Combine(context.Context, CombineRiskRequest) (CombineRiskResult, error)
}
```

Expected implementation lineage, matching the Evolution Roadmap above:

```text
TokenForecaster
├── RuleTokenForecaster          (Version 1 — heuristic, §15.2 MVP formula)
├── StatisticalTokenForecaster   (Version 2 — empirical quantiles, calibrated)
└── MLTokenForecaster            (Version 3 — learned, repository/user/provider-aware)

QuotaForecaster
├── RuleQuotaForecaster          (Version 1 — deterministic delta model, §15.3)
├── StatisticalQuotaForecaster   (Version 2 — cohort-calibrated empirical delta)
└── MLQuotaForecaster            (Version 3 — full statistical model, §15.3-15.9)
```

`ScopeEstimator` and `RiskCombiner` follow the same pattern
(`RuleScopeEstimator`/`RuleRiskCombiner` first, swapped out later) —
omitted here only because their Version 2/3 lineage isn't yet as clearly
differentiated as the token/quota forecasters' is.

Any implementation may be entirely replaced — including a full swap from
a rule-based to a statistical or ML approach — as long as it satisfies the
frozen interface. That substitution is an implementation change, not an
architecture change, and does not require a new ADR unless it also
changes the interface signature itself.

------------------------------------------------------------------------

# Why Line Count Alone Is Wrong

Never estimate tokens by:

    changed_lines × token_per_line

Because token usage is dominated by reasoning and exploration.

Examples:

-   Editing five authentication lines may require reading twenty files
    and running integration tests.
-   Adding 300 DTO lines may require almost no reasoning.
-   A debugging session may modify zero lines while consuming tens of
    thousands of tokens.
-   A failing integration test may cause multiple retry loops.

Therefore prediction must model work, not output size.

------------------------------------------------------------------------

# Two-Stage Prediction Model

## Stage 1 --- Scope Estimation

Predict the expected work before execution.

Outputs:

-   files_read
-   files_changed
-   lines_added
-   lines_deleted
-   tool_calls
-   test_commands
-   expected_retry_count

This stage predicts **what the agent is likely to do**, not token usage.

------------------------------------------------------------------------

## Stage 1 Features

### Prompt Features

Collect:

-   prompt token count
-   prompt length
-   task verb (fix, refactor, implement, investigate...)
-   requires tests
-   requires integration tests
-   cross-layer change
-   mentions migration
-   mentions schema
-   mentions API contract
-   explicit file paths
-   explicit acceptance criteria

------------------------------------------------------------------------

### Repository Features

Collect:

-   repository size
-   language
-   project count
-   dependency graph fan-out
-   target module size
-   test suite size
-   dirty file count
-   current diff size

------------------------------------------------------------------------

### Session Features

Collect:

-   recent N turns token usage
-   changed files
-   changed lines
-   tool call count
-   retry count
-   failed tests
-   context growth
-   compaction count

------------------------------------------------------------------------

### Task Similarity

Prefer matching historical tasks by category:

Examples:

-   ASP.NET Core controller
-   Redis Lua
-   EF migration
-   Go refactor
-   SQLite migration
-   Authentication
-   Build fixes

Never compare against the global average when similar tasks exist.

------------------------------------------------------------------------

# Stage 2 --- Token Prediction

Estimate:

    EstimatedTokens =
        BaseSessionCost
      + PromptCost
      + ExplorationCost
      + ReadCost
      + EditCost
      + VerificationCost
      + RetryCost
      + FinalResponseCost

Example components:

ReadCost

    files_read
    ×
    average_tokens_per_file_read

EditCost

    files_changed × edit_overhead

    +

    changed_lines × token_per_changed_line

VerificationCost

    test_commands

    ×

    average_test_output_tokens

RetryCost

    expected_retry_count

    ×

    average_retry_cost

------------------------------------------------------------------------

# Never Return a Single Number

Bad:

    Estimated:

    48231 tokens

Good:

    P50:

    38000

    P80:

    61000

    P95:

    94000

Checkpoint decisions should use P80/P90 instead of the mean.

------------------------------------------------------------------------

# MVP Heuristic Formula

    predicted_tokens =

    median(last_5_similar_turn_tokens)

    ×

    task_complexity_multiplier

    ×

    context_multiplier

    ×

    uncertainty_multiplier

Example complexity multiplier

    1.0
    + 0.10 × estimated_files
    + 0.002 × estimated_changed_lines
    + 0.25 × requires_tests
    + 0.35 × requires_integration_tests
    + 0.30 × cross_project_change
    + 0.40 × migration_or_schema_change
    + 0.25 × unclear_scope

Context multiplier

    1 +

    (current_context_tokens / context_window)

    ×

    0.5

Uncertainty multiplier

  Situation                                Multiplier
  -------------------------------------- ------------
  Explicit files + acceptance criteria            1.0
  Mostly clear                                    1.2
  Requires exploration                            1.5
  Open-ended "fix the system"                     2.0

------------------------------------------------------------------------

# Risk Estimation

Never use token prediction alone.

Compute:

    quota_risk =
    predicted_next_turn_p90
    /
    estimated_remaining_rolling_quota

    context_risk =
    predicted_context_growth_p90
    /
    available_context_headroom

    execution_risk =
    P(task_requires_multiple_turns)

Overall:

    overall_risk =
    max(
        quota_risk,
        context_risk,
        execution_risk
    )

The maximum is used because any single failure mode can terminate
execution.

------------------------------------------------------------------------

# Estimating Remaining Five-Hour Quota

## Best Case

Provider exposes:

-   used_percent
-   reset_at
-   window duration

Then compute remaining headroom directly.

------------------------------------------------------------------------

## Realistic Case

Provider does not expose quota.

Maintain a local ledger:

``` json
[
  {
    "timestamp": "...",
    "tokens": 18230
  },
  {
    "timestamp": "...",
    "tokens": 34110
  }
]
```

Rolling usage:

    rolling_usage

    =

    Σ tokens

    within last five hours

Because quota ceilings are provider-dependent and may change over time,
estimate an **effective_limit** from observed limit events rather than
assuming a fixed token ceiling.

------------------------------------------------------------------------

# Better Statistical Models

Instead of ordinary regression:

Use:

-   Survival Analysis
-   Binary Classification
-   Quantile Regression
-   Gradient Boosted Trees

Possible labels:

-   completed_normally
-   hit_usage_limit
-   required_compaction
-   user_interrupted
-   tool_failure
-   required_followup_turn

Avoid treating every incomplete turn as a quota failure.

------------------------------------------------------------------------

# Highest-Value Features

Collect first:

1.  Similar-task token P50/P90
2.  Rolling usage
3.  Current context size
4.  Estimated files read
5.  Estimated files changed
6.  Test type
7.  Retry/failure rate
8.  Prompt ambiguity
9.  Dependency fan-out
10. Cross-project changes

Changed line count is only a weak feature.

Reasoning, reading, retries, and tool output usually dominate token
usage.

------------------------------------------------------------------------

# Scope Estimation Before Execution

Before running the coding agent:

    User Prompt

    ↓

    Scope Estimator

    ↓

    Candidate Files

    ↓

    Risk Estimator

    ↓

    Checkpoint Decision

    ↓

    Main Execution

The scope estimator should avoid reading the entire repository.

Instead use:

-   repository tree
-   symbol index
-   dependency metadata
-   recent touched files
-   git grep
-   language server references

Output:

``` json
{
  "estimatedFilesRead": {
    "p50": 8,
    "p90": 19
  },
  "estimatedFilesChanged": {
    "p50": 4,
    "p90": 9
  },
  "estimatedChangedLines": {
    "p50": 120,
    "p90": 410
  },
  "requiresTests": true,
  "testScope": "integration",
  "uncertainty": 0.42
}
```

Planning itself consumes resources.

Prefer deterministic tooling first and invoke an LLM only when
necessary.

------------------------------------------------------------------------

# Design Principles

-   Predict work before predicting tokens.
-   Prefer ranges over point estimates.
-   Prefer P80/P90 for operational decisions.
-   Separate quota risk, context risk, and execution risk.
-   Learn repository-specific behavior over time.
-   Use deterministic heuristics before ML.
-   Treat prediction as a continuously calibrated system.
