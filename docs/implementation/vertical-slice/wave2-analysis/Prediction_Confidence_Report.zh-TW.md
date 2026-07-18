# 預測信心度報告

> 🌐 [English](Prediction_Confidence_Report.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 數值 |
|---|---|
| 階段 | 3.8 — Phase 2 後分析 |
| 衍生自 | `Feature_Registry.md`（正典來源）。本文件提供一份依信心度排序的檢視，以及訓練適用性的建議；不重述登錄表中已有的描述、來源或操作性中繼資料。 |
| 狀態 | 僅為分析 |

## 1. 高信心度特徵（信心度 ≥ 0.80）

這些是觀測值而非預測值，可直接安心信任。

| 指標 | 數值（本波） | 資料來源 | 信心度 | 證據來源 | 更新頻率 | Ground Truth | Rule | Statistical | ML |
|---|---|---|---|---|---|---|---|---|---|
| `ByteLength`/`RuneCount`/`LineCount` | 每提示詞計算 | 觀測值 | 1.00 | `internal/features/prompt.go`，已驗證 | 每回合 | No | Yes | Yes | Yes |
| `Fingerprint.ComputeDigest()` | 每儲存庫狀態計算 | 觀測值 | 1.00 | `internal/gitx`，已獨立驗證其確定性 | 每次檢查點事件 | Yes | N/A（非預測器輸入） | N/A | N/A |
| `Fingerprint.HeadOID`/`Branch` | 每儲存庫狀態 | 觀測值 | 1.00 | 同上 | 每次檢查點事件 | Yes | N/A | N/A | N/A |
| `Event.IdempotencyKey` | 每事件 | 觀測值 | 1.00 | `claude-provider-04`，已驗證其確定性 | 每事件 | Yes | N/A（管線基礎設施，非特徵） | N/A | N/A |
| `Event.SchemaVersion` | 常數 | 觀測值 | 1.00 | 於初始建置時凍結 | 靜態 | Yes | N/A | N/A | N/A |
| `UsageObservation.*` / `QuotaObservation.*` / `ContextObservation.*`（僅限測試夾具範疇） | 依測試夾具而定 | **僅對測試夾具**為觀測值 | 對測試夾具為 1.00／對即時現實為 0.00 | `claude-provider-01`/`04`，已驗證 | 每次 status-line 更新（即時後） | No | Yes | Yes | Yes |
| `AcceptanceCriteriaCount` | 每提示詞 | 觀測值（樣式比對） | 0.80 | `internal/features/prompt.go` | 每回合 | No | Yes | Yes | Yes |

**請仔細閱讀 `UsageObservation` 該列**：其信心度取決於情境，並非單一數字——
對其已測試過的測試夾具（fixtures）為 1.00，對其從未見過的即時 session 則為
0.00。本報告將其列於此處，是因為*解析器*本身屬於高信心度；未來的讀者切勿將
此誤解為「Auspex 擁有高信心度的真實使用量資料」——那是錯誤的（見 §2）。

## 2. 中信心度特徵（0.30 ≤ 信心度 < 0.80）

存在真實訊號，但屬於啟發式、未經即時現實驗證，或兩者皆是。

| 指標 | 數值（本波） | 資料來源 | 信心度 | 證據來源 | 更新頻率 | Ground Truth | Rule | Statistical | ML |
|---|---|---|---|---|---|---|---|---|---|
| `ExplicitPathCount` | 每提示詞 | 觀測值（啟發式） | 0.60 | `internal/features/prompt.go` | 每回合 | No | Yes | Yes | Yes |
| 動詞存在旗標（5 個） | 每提示詞 | 觀測值（關鍵字比對） | 0.60 | 同上；`Wave2_Lessons.md` §1 issue #5 已自我標註偽陽性風險 | 每回合 | No | Yes | Yes | Yes |
| 關鍵字指示旗標（5 個） | 每提示詞 | 觀測值（關鍵字比對） | 0.60 | 同上 | 每回合 | No | Yes | Yes | Yes |
| `ScopeEstimate.RequiresUnitTests/RequiresIntegration/CrossProject/MigrationLikely/SecuritySensitive` | 每回合預測 | 估計值 | 0.50-0.60 | `predictor-05`，已驗證結構正確性，未驗證準確度 | 每回合 | No | Yes | Yes | Yes |
| `ApproxTokens` | 每提示詞 | 估計值 | 0.30（ADD §14.7 強制規定 `confidence=low`） | `internal/features/prompt.go` | 每回合 | No | Yes | Yes | 低權重（一旦真實使用量存在即被取代） |
| `RunwayForecast.RiskScore` | 每觀測值 | 估計值（未校準的後備方案） | 變動，本波始終未校準 | `internal/predictor/runway`，已透過 300 種組合掃描驗證 | 進行中回合期間持續更新 | No | Yes | Yes | 低權重（校準前不是機率） |
| `Fingerprint.Entries`（狀態項目） | 每儲存庫狀態 | 觀測值 | 解析本身為 1.00，但未列入 §1，因其在檢查點識別中的*用途*並非預測器信心度的問題 | 觀測值 | 每次檢查點事件 | Yes | N/A | N/A | N/A |

## 3. 低信心度特徵（信心度 < 0.30，但非零——仍存在部分訊號）

| 指標 | 數值（本波） | 資料來源 | 信心度 | 證據來源 | 更新頻率 | Ground Truth | Rule | Statistical | ML |
|---|---|---|---|---|---|---|---|---|---|
| `ScopeEstimate.FilesReadP50/P80/P90` | 冷啟動預設值或 session 混合 | 估計值 | 低（從未經真實資料驗證，依 `Feature_Gap_Report.md` §1.1） | `predictor-05` | 每回合 | No | Yes | Yes | Yes |
| `ScopeEstimate.FilesChangedP50/P80/P90` | 冷啟動預設值或 session 混合 | 估計值 | 低，原因相同——`RepositoryFeatures` 尚未接線 | `predictor-05` | 每回合 | No | Yes | Yes | Yes |
| `ScopeEstimate.LinesChangedP50/P80/P90` | 冷啟動預設值或 session 混合 | 估計值 | 低 | `predictor-05` | 每回合 | No | Yes | Yes | Yes |

## 4. 零信心度／未知特徵（信心度 = 0.00，完全不存在任何資料）

這是最大的一群，依 `Feature_Registry.md` §9（約佔所有已登錄欄位的 ~31%）。
此處不逐一重列（那會與登錄表重複）——改以成因分組：

| 成因分組 | 代表性特徵 | 數量（約） | 資料來源 |
|---|---|---|---|
| 從未觀測到任何即時遙測 | `TokenForecast.*`、`SessionFeatures.*`、實際 token 使用量、實際耗時 | ~20 | 未知 |
| 既有資料與預測器輸入之間未接線 | `RepositoryFeatures.*`（Git 資料存在，但未連接） | ~9 | 未知（明確屬於接線缺口，依 `Feature_Gap_Report.md` §1.1） |
| 元件尚未建置 | `ArtifactRef.*`、`StateCheckpoint.*`、`QuotaForecast.*`、`ProviderCapabilities.*`（真實偵測） | ~10 | 未知 |
| 需要累積資料量，而非建置工作 | ECE、Brier 分數 | 2 | 未知 |

## 5. 分類與訓練建議

### 5.1 哪些特徵應成為未來的訓練標籤

訓練標籤是統計式／ML 預測器要學習預測的*結果*——不是輸入。從本登錄表來
看，候選項目為：

- **`UsageObservation.*`（即時後）** — Token 預測器的直接目標。目前在即時
  資料層面的信心度為 `Unknown`；建議在 `Missing_Telemetry_Report.md` A1 缺
  口關閉後，作為第一優先的訓練標籤候選。
- **`QuotaObservation.UsedPercent`（即時後）** — 配額預測器的直接目標。
- **歷史結果標籤**（`completed_normally`/`hit_usage_limit` 等，
  `Missing_Telemetry_Report.md` A5）— 任何分類式 Version 2/3 預測器的直接
  目標。

以上沒有任何一項*今天*就能作為訓練標籤使用——每一項在 §4 中都是
`Unknown`。這是一項前瞻性建議，並非宣告訓練現在即可開始。

### 5.2 哪些特徵應維持為輔助性質（僅作輸入，永不作標籤）

- 所有「Prompt Features」（登錄表 §1）— 這些描述的是輸入，不是結果；不存
  在任何「訓練模型去『預測』`ByteLength`」的合理意義。
- 所有「Repository Features」與「Session Features」— 同樣的理由；這些是脈
  絡（context），不是結果。
- `ProviderCapabilities.*` — 這是 session 層級的常數，不是逐回合變動、可
  供預測的訊號。

### 5.3 哪些特徵即使未來可得，也不應用於訓練

- **`ApproxTokens`** — 明確是真實 token 數的低信心度啟發式*替代品*
  （ADD §14.7）。一旦真實的 `UsageObservation` 資料存在，改用
  `ApproxTokens` 而非真實使用量來訓練，就是在對標籤的代理值、而非標籤本身
  進行訓練——在真實訊號可得後，這會造成實際的傷害。建議：僅將
  `ApproxTokens` 用作 Rule Predictor 的輸入，並在真實使用量資料以足夠數量
  存在的那一刻，將其自任何統計式／ML 特徵集中除役。
- **處於 `Calibrated: false` 狀態的 `RunwayForecast.RiskScore`** — 訓練模
  型去重現一個明確未經校準的啟發式方法的輸出，會將該啟發式方法的偏誤直接
  固化，而非從真實結果中學習。ADR-026/033 已禁止將其以機率形式呈現；本建
  議將同一邏輯延伸至訓練資料的衛生規範。
- **任何被當作標籤使用的關鍵字／動詞存在旗標** — 這些本身就是從提示詞推導
  出來的啟發式*輸入*；它們不代表任何結果。列於此處僅為預先阻止一種可能發
  生的錯誤（例如「訓練模型預測 `HasMigrateVerb`」是毫無意義的——它本來就
  可由提示詞文字確定性地推得）。

## 6. 彙總計數

| 分組 | 約略欄位數 | 佔登錄表比例 |
|---|---|---|
| 高信心度（≥0.80） | ~15（多為管線基礎設施／識別欄位，加上僅限測試夾具範疇的觀測值） | ~16% |
| 中信心度（0.30-0.79） | ~20 | ~21% |
| 低信心度（<0.30，非零） | ~9 | ~9% |
| 零信心度／未知 | ~41 | ~43%（對應登錄表 §9 的 ~31% Unknown 加上 ~16% 僅限測試夾具範疇者，此處改依信心度而非可得性狀態重新分類） |

這個信心度分布，就是 Auspex 預測器誠實的現況：一個扎實、經充分測試的 Rule
Predictor 基礎（上文 §1-2），坐落在一大批要嘛尚不存在、要嘛從未對照現實檢
驗過的特徵之上。這句話的兩半，任何一半都不應被解讀得比另一半更真實。
