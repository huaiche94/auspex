# ADR-041 — Predictor 管線新增明確的 Forecast 層

> 🌐 [English](0041-predictor-forecast-layer.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：已接受（Accepted）
日期：2026-07-12
負責人：contract-integrator（lead）
核准人：repository owner，2026-07-12

## 背景

`Auspex_ADD.md` §2.2 已將 `TokenForecast` 與 `QuotaForecast` 列為 canonical `AuspexDecision` struct 的欄位，而 §15.1–15.3/§15.9 也已完整定義其公式（token 分解＋倍率模型；quota/context 百分比差值推估）。但在 Bootstrap 期間，這兩個型別從未被加入凍結合約層：`internal/domain` 沒有 `TokenForecast`、`QuotaForecast`、`RiskComponent` 或 `DataQuality` 型別，而 `internal/app/ports.go` 的 `Evaluation` struct 是個薄殼形狀，並未承載其中任何一個。`ScopeEstimate`（§14.1）雖然在 ADD 中已完整定義，同樣從未被實作。

凍結的執行 DAG（`docs/implementation/vertical-slice/EXECUTION_DAG.md`）延續了相同的缺口：`predictor-05`（Scope Estimator）直接餵入 `predictor-07`（Risk Combiner），而 `predictor-06`（Runway Forecaster）也被列為 `predictor-07` 的相依項。但 `Auspex_ADD.md` §16.2 自身的風險公式需要 `projected_quota_p90` 與 `projected_context_p90`——分別是 quota 差值模型（§15.3）與 context 推估（§15.9）的輸出——而 DAG 中沒有任何節點產生這兩者。`predictor-06` 的 Runway Forecaster 回答的是另一個問題（迫在眉睫的 10 分鐘內 quota 耗盡風險，§15.5，直接供 Graceful Pause 使用），從來就不是 `quota_risk`/`context_risk` 的有效來源。DAG 中 `predictor-07` 對 `predictor-06` 的相依，本身就是同一個規格未完整的管線所產生的副產物，而非刻意的設計決策——`Auspex_ADD.md` §7.3 自己的 C4 Evaluation Components 圖也呈現了相同的混淆（`TOK --> RUNWAY --> RISK`），本 ADR 對此做出修正。

這個問題是透過 `Auspex_Predictor_Design_Supplement.md`（一份獨立指出相同缺口的輔助設計文件）在任何 Phase 2 predictor 實作開始之前發現的。依 Constitution §6/§7 以及本專案自身對架構的「no blind resume」紀律：在程式碼寫成之前發現的真實缺口，應在合約層修正，而不是在實作層打補丁繞過。

## 決策

在 Scope Estimation 與 Risk Combination 之間插入一個明確的 Forecast 層：

```text
Scope Estimator
      ↓
Token Forecast
      ↓
Quota Forecast (also produces the context-window projection)
      ↓
Risk Combiner
      ↓
Policy

Runway Predictor — independent, not part of this chain. Feeds Graceful
Pause directly (as it always correctly did — Auspex_ADD.md §7.4's
Continuity Components diagram already modeled this independence via
`Runway Hazard Monitor`; only §7.3's per-turn evaluation diagram and this
DAG's `predictor-07` edge incorrectly wired it into the risk path).
```

四個新的、範圍狹窄且可替換的介面已凍結於 `internal/app/ports.go`（並鏡射至 `Auspex_ADD.md` §9.9），使得任一階段的 Rule／Statistical／ML 實作都能被替換，而不影響其他階段——這與 `Auspex_ADD.md` §1.4 已述明、並在 `Auspex_Predictor_Design_Supplement.md` 的 Version 1/2/3 roadmap 中正式化的演進式路線圖意圖一致：

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

這些介面背後有四個新的凍結 domain 型別支撐（`internal/domain/forecast.go`）：`domain.ScopeEstimate`（鏡射 ADD §14.1 已定義的 struct，欄位集完全相同，但依 ADD principle 1「unknown 不等於 zero」的原則改用 pointer 型別的數值欄位——ADD 自身的虛擬碼使用純 `int`，本 ADR 為了與其他所有凍結量測型別保持一致而修正此點）、`domain.TokenForecast`（P50/P80/P90，新定義——ADD §15.1–15.2 有公式但沒有 struct）、`domain.QuotaForecast`（`ProjectedQuotaUsedP90`、`ProjectedContextUsedP90`——兩種推估合併於同一型別，因為 §15.3 與 §15.9 使用相同的差值推估技術，且兩者皆餵入 `RiskCombiner`）、`domain.RiskComponent`（單一具名風險項——`Score`、`Calibrated`、`Confidence`、`ReasonCodes`）、`domain.DataQuality`（整體信任訊號，獨立於任何單一元件自身的 confidence）。

同時新增一個 `domain.ReasonCode` 型別（以 `string` 為底的封閉列舉），由 ADD §16.4 已列出的約 28 個常數支撐。既有的（Phase 1 已凍結但尚未被任何已合併程式碼使用的）`Evaluation.ReasonCodes` 欄位型別由 `[]string` 改為 `[]domain.ReasonCode` 以使用它——這是安全的，因為目前沒有任何 Phase 1 程式碼會建構或讀取該欄位。

### 修正後的 DAG 相依邊

- 新節點 `predictor-05b`（Token Forecaster）：相依於 `predictor-05`。
- 新節點 `predictor-05c`（Quota Forecaster）：相依於 `predictor-05b`。對 Phase 2 而言是 cold-start 安全的——在 `claude-provider-05`（持久化 telemetry persistence）與 `foundation-06`（SQLite）於後續 phase 落地、進而完成完整的經驗校準之前，採用「目前觀測值＋預設差值」的確定性推估是可接受的，這與 `predictor-04`/`predictor-08` 已建立的既有 cold-start 合約一致。
- `predictor-07`（Risk Combiner）：相依項由 `predictor-05, predictor-06` 修正為 `predictor-05, predictor-05c`——移除 `predictor-06`（Runway）作為相依項；它從來就不是 risk combination 的有效輸入。
- `predictor-08`（Policy）：相依項由 `predictor-07` 修正為 `predictor-07, predictor-06`——Policy 同時直接消費綜合風險分數與獨立的 runway 命中機率（這與 `agents/predictor.md` 既有的「Initial policy suggestion」清單一致，該清單已將校準過的十分鐘命中機率列為獨立的 policy 輸入）。
- `predictor-11`（Required tests）：相依清單擴充，納入 `predictor-05b`、`predictor-05c`。

### 術語說明

`Auspex_Predictor_Design_Supplement.md` 的「Risk Estimation」小節將第三個風險項稱為 `execution_risk = P(task_requires_multiple_turns)`。`Auspex_ADD.md` §16.1 早已為同一概念命名並正式化為 `completion_risk`（「即使 quota/context 足夠，仍需要多輪或未滿足 acceptance criteria 的風險」），並在 §16.2 給出完整公式。本 ADR 保留 ADD 既有的名稱——`completion_risk`——作為凍結的用語，因為它已經以公式形式實作，若改名將使同一概念分裂成兩個名稱，而這正是 Constitution §1（single source of truth）要防止的情況。`blast_radius_risk`（ADD 的第四個元件，未列於 Supplement 較短的清單中）維持不變；本 ADR 並未移除它。

## 影響

- `internal/domain/forecast.go` 與 `internal/app/ports.go` 新增凍結的型別／介面（僅為合約，尚無實作，依明確指示——「先核准 ADR，不要求先做出 stub」）。
- `Auspex_ADD.md` §7.3 的 C4 圖、§9.9 的介面清單，以及 §33 的 ADR 清單均已更新以反映此決策。
- `CONTRACT_FREEZE.md` 新增一節，記載這四個介面、reason-code 分類法，以及 `Evaluation.ReasonCodes` 的型別變更。
- 執行 DAG 新增兩個 predictor 節點與三條修正後的相依邊；Phase 2+ 剩餘的 predictor 任務總數增加 2 個（從 Phase 1 結束後剩餘的 6 個增加到 8 個）。
- 本 ADR 之前提出的 Phase 2 predictor 分配方案已被取代——在讓本 ADR 落地的同一次變更中重新產生。
- 不影響任何 Phase 1 程式碼。沒有 migration、schema、checkpoint 格式、隱私預設值或公開協定相容性的變更。就其本質而言，本 ADR 並不落入 Constitution §3 任何強制要求 ADR 的觸發條件之中（它是在實作、而非變更一項已經拍板的 ADD 決策）——之所以仍然撰寫本文件，是因為 repository owner 的 Phase 2 指示已將 `Auspex_ADD.md`、`CONTRACT_FREEZE.md` 與 DAG 凍結，需經明確的 ADR 核准才能變動，而本文件正是提供這項核准。
