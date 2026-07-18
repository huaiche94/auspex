# ADR-047 — Token forecaster 的相似 turn cohort 後備階梯（#20 Phase 1）

> 🌐 [English](0047-token-cohort-fallback-ladder.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：已接受（Accepted）
日期：2026-07-14
負責人：由 lead 執行
追蹤：issue #20（`docs/backlog/provider-model-effort-features.md` §4 的 Phase 1）；排序依 DECISION_LOG D-10

## 背景

ADD §15.2 將 token forecaster 的經驗基準定義為「符合下列條件的近期 turn：provider ＋ model family ＋ task class ＋ repository cohort」——但實作（`SQLDataSource.RecentSimilarTurnTokens`）僅以 session 做篩選，且凍結的 feature-lookup port（ADR-044）回傳的只是一個裸的 `[]float64`，完全沒有管道能說明究竟是哪個 cohort 給出了答案。Phase 0（#20，D-10）讓 turn 的身分變得可被擷取：statusline ingest 將 `provider_sessions.model/effort` 維護為一個「最新觀測值」解析快取，而 prediction 資料列會持久化 `(provider, model_id, model_family, effort)` 這組標記。

Cohort 機制仍留有兩個缺口：

1. **樣本端標記。** Usage observation（`provider.usage.observed`）不帶任何身分標記，因此即使日後出現 total-token 欄位，樣本仍無法以 turn 粒度被分配到 cohort——若 session 中途發生 `/model` 或 `/fast` 切換，session 層級的 join 會把歷史資料誤植（backlog §3 constraint 1）。
2. **Rung 可見性。** Backlog 的稀疏性限制（§3 constraint 4）要求一套明確的降級階梯，並且「Confidence/reason codes 需反映是哪一個 rung 回答的」——這在只有 `[]float64` 的情況下是不可能做到的。

## 決策

1. **為樣本介面加上標記（擷取，純增量式）。** Claude telemetry normalizer 會把來自 statusline snapshot 的 `model_id` 與 `effort`，標記到每一個 `provider.usage.observed` payload 上。這些標記屬於中繼資料：它們絕不會阻擋事件發出，身分標記缺席時也不會阻擋任何東西。
2. **修訂凍結 port（依 ADR-044「變更需要 ADR」的授權而進行）。** `FeatureDataSource.RecentSimilarTurnTokens` 現在回傳 `features.SimilarTurnTokens{Samples []float64, Rung SimilarTurnCohortRung}`。Consumer 端的窄視圖（`internal/predictor/token.FeatureSource`）做出完全相同的變更；interface segregation 維持不變。
3. **`SQLDataSource` 中的後備階梯**（backlog §3.4），由最具體到最不具體排列，由樣本數 ≥ 8（ADD §15.2 的門檻，對應 `minSimilarTurnSamples`）的第一個 rung 回答：
   - `provider + model family + effort`（`CohortRungModelEffort`）
   - `provider + model family`（`CohortRungModelFamily`）
   - `provider`（`CohortRungProvider`）
   - session-recent（`CohortRungSession`）——與階梯導入前的行為完全相同，作為最終的後備方案（當該 turn 的 provider 從未被觀測到時，也是這個答案）。

   若某個 rung 在 turn 端的標記未被觀測到，該 rung 會被跳過，絕不會被當作「空集合也算相符」（unknown 不等於 zero）。Turn 的身分是從 `provider_sessions` 解析而來（與 prediction 標記同一個來源）；樣本的 model ID 則透過與標記的 `model_family` 欄位相同的定價表規則解析為 family，因此兩者在結構上絕不可能互相矛盾。
4. **Reason codes（對 ADD §16.4 分類法的純增量新增，授權方式與 ADR-043 的 code 相同）。** 每個經驗基準都會伴隨恰好一個 `TOKEN_COHORT_MODEL_EFFORT`／`TOKEN_COHORT_MODEL_FAMILY`／`TOKEN_COHORT_PROVIDER_ONLY`／`TOKEN_COHORT_SESSION_ONLY`；cold-start forecast 則維持發出 `PREDICTION_COLD_START` 不變。未來若出現未知的 rung 值，會對應到 session-only 的 code——這是最保守的宣告。Confidence 的語意不變（經驗值 ⇒ 最高只到 ConfidenceMedium，本波次絕不校準）。

## 誠實範圍聲明

- **Task class 與 repository 不在此階梯之中。** 這兩者在樣本介面上都不存在（classification 是一個衍生、事後產生的訊號，從未被持久化到 usage 事件上；statusline ingest 也沒有填入 `events.repository_id`）。`class` 參數仍被接受但未被使用，並在查詢處註記——與階梯導入前的實作採取相同的誠實紀律。
- **此階梯目前是休眠中的機制。** 目前沒有任何 payload 帶有 `total_tokens`，因此每一個 rung 都會得到零筆樣本，行為與先前逐位元組相同（session rung、空集合、cold-start 預設值）。當未來某個 phase 新增此欄位時，此階梯將自動啟用——這與階梯導入前的查詢自身所記載的合約完全相同。
- **Effort 是以原始字串比對。** 目前只有 Claude 這個 provider 會發出 effort；跨 provider 正規化的 `effort_class` 對應表，依 backlog 自身的排序，屬於延後到 Phase 3（codex 接線）才處理的凍結合約議題。
- **沒有任何數值決策。** 門檻值（8）與近期樣本上限（50）都是既有的 ADD §15.2／實作常數；候選池的上限是推導出來的（4 × limit，每個身分 rung 各一份再加一份備用），而非經過調校。

## 影響

- 校準（#11）現在可以用同一組正規化身分，同時對 prediction 與 token 樣本進行分層；從此刻起，不再有「歷史資料未標記」的缺口。
- `CONTRACT_FREEZE.md` 為此次 port 變更新增一筆 Amendments 條目；每一個實作者／fake 都在同一個 commit 中一併更新（Go 的型別系統強制要求完整性）。
- Phase 2（逐 cohort 的經驗 quota 差值／分位數）將建立在相同的 rung 詞彙之上；Phase 3 會將 codex（model、reasoning、speed）對應進同一組三元組。

## 已考慮的替代方案

- **透過 `provider_sessions` join 而非 payload 標記來解析 cohort**——已否決：session 層級的標記會誤植 turn 層級的歷史資料（backlog §3 constraint 1 已明確警告此點）。
- **維持 port 不變、改在頻外（out-of-band）記錄 rung 的選擇結果**——已否決：reason codes 是本管線的說明管道（ADD §16.4）；如果一個 forecast 的 cohort 具體程度，在持久化的 prediction 資料列上完全不可見，就會違背驅動 #20 的校準目的。
- **透過 SQL 的 json_extract 在資料庫端篩選 cohort**——已否決：model-family 的解析邏輯存在於 `internal/pricing` 的 Go 規則中；若在 SQL 字串樣式中重複這套邏輯，會讓 cohort 的歸屬與 prediction 標記的 family 彼此漂移不一致。
