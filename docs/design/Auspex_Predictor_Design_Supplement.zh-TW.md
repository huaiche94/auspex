# Auspex Predictor 設計補充文件

> 🌐 [English](Auspex_Predictor_Design_Supplement.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

> 狀態：Accepted — 本文件所指出的 Forecast 層缺口
> （Scope Estimation 銜接 Risk Estimation，中間卻沒有明確的 Token/Quota
> 預測（forecast）階段）已正式化為
> `docs/adr/0041-predictor-forecast-layer.md`。下方四個管線
> 介面已凍結於 `internal/app/ports.go`；目前尚無
> 實作。\
> 目的：作為 `Auspex_ADD.md` 的補充文件\
> 範圍：Scope Estimation、Token Prediction、Risk Estimation，以及
> Checkpoint Decision

# 概觀

本文件定義 Auspex 預測系統的長期設計。

預測引擎的職責，是在**AI 編碼 agent 執行一個 turn 之前**回答一個問題：

> 根據目前的 repository、prompt、session 歷史與 provider 狀態，這一次的下個 turn 有多大機率會順利完成？

預測引擎與 provider 無關（provider-neutral），由三個演進階段組成。

------------------------------------------------------------------------

# External Evidence — 為何是區間，而非機率

一份對八個 frontier 模型在 SWE-bench 上的獨立研究（Bai 等，*How Do AI
Agents Spend Your Money? Analyzing and Predicting Token Consumption in
Agentic Coding Tasks*，arXiv:2604.22750，2026）量測的，正是本文件所設計的
任務——在 agent 執行前預測其 token 成本——其發現是下方每一項設計選擇所內建
之「謙遜」的外部依據：

-   **token 用量高度變異，且部分不可化約。** 同一任務的不同執行，總 token
    可差到 **30×**，在最昂貴的任務上最嚴重。精確的每輪預測並非一個只待努力
    就能解決的問題；它受限於現象本身。
-   **自我預測很弱。** frontier 模型對自身 token 用量的預測只有弱到中等
    （Pearson ≤ **0.39**，最佳為 output token；input 更差），且**系統性
    低估**真實用量。任何自我預測路線都必須加寬 input 軸並施加向上偏誤修正。
-   **感知難度不等於成本。** 專家難度評分與真實消耗只有弱相關（Kendall
    **τ_b = 0.32**）。scope 估計不可把表面的 prompt 複雜度當成成本代理——
    這與下方「為何單靠行數是錯的」一致。
-   **成本由 input 主導，而 cache-read 主導金額。** input/output 比平均約
    153；在顯式快取定價下，對累積 context 的重讀——而非 output——才是帳單
    佔比最大的部分。估「錢」等於按 token 類別估 context 的成長與重讀次數。

這些數字是對其他模型量測所得，**並未**被引入作為 Auspex 的係數；它們佐證的
是設計的*形狀*——以區間取代點估計、uncalibrated 分數絕不標為機率
（Constitution §7 第 7 條）、更寬的 input 區間，以及偏好**可觀測**的失敗訊號
（例如反覆操作同一檔案）而非預測的 token 數。將這些發現落地的路線圖——
cache-aware 成本拆解與重複檔案操作 risk factor——見
`docs/backlog/token-cost-prediction-research.md`。

------------------------------------------------------------------------

# 演進路線圖

## 版本一 --- 規則型 Predictor（Rule Predictor）

特性：

-   確定性（Deterministic）
-   不使用 ML
-   可解釋（Explainable）
-   快速
-   不需要訓練

用途：

-   啟發式評分（heuristic scoring）
-   repository 統計資料
-   session 遙測（telemetry）
-   人工調校的乘數（multiplier）

輸出：

-   P50
-   P80
-   P90
-   風險分數（Risk score）
-   信心值（Confidence）
-   說明（Explanation）

------------------------------------------------------------------------

## 版本二 --- 統計型 Predictor（Statistical Predictor）

特性：

-   使用歷史遙測資料
-   學習特定 repository 的分佈
-   分位數估計（Quantile estimation）
-   信心校準（Confidence calibration）

可能採用的演算法：

-   Quantile Regression
-   Bayesian estimation
-   Distribution fitting
-   Empirical probability tables

------------------------------------------------------------------------

## 版本三 --- ML Predictor

特性：

-   從數千個已完成的 turn 中學習
-   具備 repository 感知能力
-   具備使用者感知能力
-   具備 provider 感知能力

可能採用的演算法：

-   Gradient Boosted Trees
-   Quantile Regression Forest
-   XGBoost
-   LightGBM
-   Binary Classification
-   Survival Analysis

------------------------------------------------------------------------

# 管線介面（Pipeline Interfaces）

下方每一個管線階段，都被凍結為一個窄範圍、單一方法（single-method）的 Go
介面（`internal/app/ports.go`、`docs/adr/0041-predictor-forecast-layer.md`）。
正是這個設計，讓上述版本一／二／三的路線圖成為一條真正的遷移路徑，而不是
一次重寫：任何一個階段都可以替換為更好的實作，而不必動到其他階段，因為
呼叫端只依賴介面，從不依賴具體型別。

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

與上方演進路線圖相對應的預期實作脈絡（lineage）：

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

`ScopeEstimator` 與 `RiskCombiner` 遵循相同的模式（先有
`RuleScopeEstimator`／`RuleRiskCombiner`，之後再替換）——此處略而不列，僅僅是
因為它們的版本二／三脈絡目前還不像 token／quota forecaster 那樣有明確區分。

任何實作都可以被完全取代——包括從規則式（rule-based）整個換成統計式或 ML
方法——只要它滿足已凍結的介面即可。這樣的替換屬於實作層級的變更，而非架構
變更，除非它同時改變了介面本身的簽章（signature），否則不需要新的 ADR。

------------------------------------------------------------------------

# 為何單靠行數是錯的

絕不要用下面這種方式估計 token：

    changed_lines × token_per_line

因為 token 用量主要是由推理（reasoning）與探索（exploration）主導。

範例：

-   修改五行 authentication 程式碼，可能需要讀取二十個檔案
    並執行整合測試。
-   新增 300 行 DTO 程式碼，可能幾乎不需要推理。
-   一次除錯（debugging）過程可能一行都沒改，卻消耗掉數萬個
    token。
-   一個失敗的整合測試，可能引發多次重試迴圈（retry loop）。

因此預測必須為「工作量」建模，而不是為「輸出大小」建模。

------------------------------------------------------------------------

# 兩階段預測模型

## 階段一 --- 範圍估計（Scope Estimation）

在執行前預測預期的工作量。

輸出：

-   files_read
-   files_changed
-   lines_added
-   lines_deleted
-   tool_calls
-   test_commands
-   expected_retry_count

此階段預測的是**agent 可能會做什麼**，而不是 token 用量。

------------------------------------------------------------------------

## 階段一特徵

### Prompt 特徵

蒐集：

-   prompt 的 token 數
-   prompt 長度
-   任務動詞（fix、refactor、implement、investigate…）
-   是否需要測試
-   是否需要整合測試
-   跨層（cross-layer）變更
-   提到 migration
-   提到 schema
-   提到 API 合約
-   明確指定的檔案路徑
-   明確的驗收標準

------------------------------------------------------------------------

### Repository 特徵

蒐集：

-   repository 大小
-   程式語言
-   專案數量
-   dependency graph 的扇出度（fan-out）
-   目標模組大小
-   測試套件大小
-   dirty file 數量
-   目前的 diff 大小

------------------------------------------------------------------------

### Session 特徵

蒐集：

-   最近 N 個 turn 的 token 用量
-   變更的檔案數
-   變更的行數
-   工具呼叫（tool call）次數
-   重試次數
-   失敗的測試數
-   context 成長量
-   compaction 次數

------------------------------------------------------------------------

### 任務相似度（Task Similarity）

優先依類別（category）比對歷史任務：

範例：

-   ASP.NET Core controller
-   Redis Lua
-   EF migration
-   Go refactor
-   SQLite migration
-   Authentication
-   Build fixes

只要存在相似任務，就絕不要拿全域平均值來比較。

------------------------------------------------------------------------

# 階段二 --- Token 預測

估計：

    EstimatedTokens =
        BaseSessionCost
      + PromptCost
      + ExplorationCost
      + ReadCost
      + EditCost
      + VerificationCost
      + RetryCost
      + FinalResponseCost

範例組成項目：

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

# 絕不只回傳單一數字

不好的做法：

    Estimated:

    48231 tokens

好的做法：

    P50:

    38000

    P80:

    61000

    P95:

    94000

Checkpoint 決策應該使用 P80／P90，而不是平均值（mean）。

------------------------------------------------------------------------

# MVP 啟發式公式

    predicted_tokens =

    median(last_5_similar_turn_tokens)

    ×

    task_complexity_multiplier

    ×

    context_multiplier

    ×

    uncertainty_multiplier

範例：複雜度乘數（complexity multiplier）

    1.0
    + 0.10 × estimated_files
    + 0.002 × estimated_changed_lines
    + 0.25 × requires_tests
    + 0.35 × requires_integration_tests
    + 0.30 × cross_project_change
    + 0.40 × migration_or_schema_change
    + 0.25 × unclear_scope

Context 乘數

    1 +

    (current_context_tokens / context_window)

    ×

    0.5

不確定性乘數（uncertainty multiplier）

  情境                                      乘數
  ---------------------------------------- ------
  明確列出檔案與驗收標準                    1.0
  大致明確                                  1.2
  需要探索                                  1.5
  開放式的「修好整個系統」                  2.0

------------------------------------------------------------------------
</content>

# 風險估計（Risk Estimation）

絕不能只靠 token 預測。

計算：

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

整體：

    overall_risk =
    max(
        quota_risk,
        context_risk,
        execution_risk
    )

之所以取最大值，是因為任何單一失敗模式都可能導致執行終止。

------------------------------------------------------------------------

# 估計剩餘的五小時配額（Quota）

## 最佳情況

Provider 提供：

-   used_percent
-   reset_at
-   時間窗口（window）長度

接著直接計算剩餘的餘裕（headroom）。

------------------------------------------------------------------------

## 現實情況

Provider 未提供 quota 資訊。

維護一份本機帳本（ledger）：

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

Rolling usage（滾動用量）：

    rolling_usage

    =

    Σ tokens

    within last five hours

由於配額上限（quota ceiling）因 provider 而異，且可能隨時間改變，應從觀察到的
限制事件（limit event）估計出一個**effective_limit**，而不是假設一個固定的
token 上限。

------------------------------------------------------------------------

# 更好的統計模型

與其使用普通迴歸（ordinary regression）：

改用：

-   Survival Analysis
-   Binary Classification
-   Quantile Regression
-   Gradient Boosted Trees

可能的標籤：

-   completed_normally
-   hit_usage_limit
-   required_compaction
-   user_interrupted
-   tool_failure
-   required_followup_turn

避免把每一個未完成的 turn 都當成 quota 失敗來處理。

------------------------------------------------------------------------

# 最高價值特徵

優先蒐集：

1.  相似任務的 token P50／P90
2.  Rolling usage（滾動用量）
3.  目前的 context 大小
4.  估計的讀取檔案數
5.  估計的變更檔案數
6.  測試類型
7.  重試／失敗率
8.  Prompt 模糊程度
9.  Dependency 扇出度（fan-out）
10. 跨專案變更

變更的行數只是一個弱特徵（weak feature）。

推理、讀取、重試與工具輸出，通常才是主導 token 用量的因素。

------------------------------------------------------------------------

# 執行前的範圍估計

在執行編碼 agent 之前：

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

範圍估計器（scope estimator）應避免讀取整個 repository。

而是改用：

-   repository 樹狀結構
-   符號索引（symbol index）
-   dependency metadata
-   最近異動過的檔案
-   git grep
-   language server 參考（references）

輸出：

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

規劃（planning）本身也會消耗資源。

優先使用確定性的工具（deterministic tooling），只有在必要時才呼叫 LLM。

------------------------------------------------------------------------

# 設計原則

-   先預測工作量，再預測 token。
-   優先使用範圍區間（range），而非單點估計（point estimate）。
-   營運決策優先採用 P80／P90。
-   將 quota 風險、context 風險與 execution 風險分開處理。
-   隨時間學習特定 repository 的行為模式。
-   在導入 ML 之前先使用確定性的啟發式方法。
-   將預測視為一個持續校準（calibrated）的系統。
