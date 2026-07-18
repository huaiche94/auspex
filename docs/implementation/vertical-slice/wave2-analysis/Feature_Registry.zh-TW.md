# 特徵登錄冊

> 🌐 [English](Feature_Registry.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 值 |
|---|---|
| 階段 | 3.7 — Phase 2 後分析 |
| 狀態 | **建立當下為權威版本（Canonical，Phase 3.7，Phase 2 分析）——現為某一時點的快照。** 撰寫當時，本文件被宣告為 Auspex 預測器管線所使用或將使用之每一項特徵的唯一真實來源，該階段的預測器工作也透過它參照特徵（此規則類比於憲章條款，由儲存庫負責人於當時所述）。由於它位於已凍結的 `wave2-analysis/` 目錄，**在此不再持續維護**：日後的真實來源為程式碼（`internal/features/**`、`internal/domain/**`）與 ADD 的 §14／§15／§16 特徵清單——也就是下方每一筆項目原本即可追溯到的那兩個來源。請將它當作 Phase 2 當時的參照來閱讀，並以那些活的來源加以核對。 |
| 方法 | 以下每一項特徵均以下列兩者之一為根據：(a) 真實、已驗證的程式碼（`internal/features/**`、`internal/domain/**`），或 (b) ADD 本身已明確指定的 §14/§15/§16 特徵清單。本登錄冊不會憑空杜撰任何無法追溯至上述兩個來源的特徵。 |

## 如何閱讀本登錄冊

每一項特徵都有兩張表：**身分與來源（Identity & Provenance）**（說明它是什麼、來自何處、以及目前有多少可信度）以及**適用性與維運（Suitability & Operations）**（哪個層級的預測器可以使用它、它餵入哪個階段、以及其維運成本）。分成兩張表是為了讓表格寬度易於閱讀——兩者合起來涵蓋了儲存庫負責人所指定的每一個欄位。

`Current Availability` 的數值：**Available（可用）**（目前確實有真實程式碼路徑產生此資料，即使只是來自測試固定資料〔fixtures〕，尚非即時遙測資料）、**Derived（衍生）**（由其他 Available 特徵計算而得，不需獨立蒐集）、**Estimated（估計）**（目前以冷啟動預設值或啟發式法則替代）、**Unknown（未知）**（目前完全沒有任何程式碼路徑產生此資料）。

---

## 1. 提示詞特徵

來源：`internal/features/prompt.go`（`PromptFeatures`，真實存在、於 Phase 1 建置、已驗證）。這些欄位皆無真實資料（ground truth）可言——每個欄位本身都是關於提示詞的衍生訊號，而非世界能獨立確認或否認的事實。

### 1a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `ByteLength` | 提示詞原始位元組數 | int / 位元組 | `ExtractPromptFeatures` | 已觀測 | 1.00 | 否 | 可用 | 低 |
| `RuneCount` | 提示詞原始字元數 | int / 字元 | 同上 | 已觀測 | 1.00 | 否 | 可用 | 低 |
| `LineCount` | 提示詞原始行數 | int / 行 | 同上 | 已觀測 | 1.00 | 否 | 可用 | 低 |
| `ApproxTokens` | ADD §14.7 節啟發式 token 概估值 | int / token | 同上 | 估計 | 0.30（§14.7 節規定 `confidence=low`） | 否 | 可用 | 中 |
| `TokenConfidence` | `ApproxTokens` 的信賴度標籤 | `domain.Confidence` enum | 同上 | 衍生 | N/A（信賴度數值本身不會再被評分信賴度） | 否 | 可用 | 低 |
| `ExplicitPathCount` | 以空白分隔、形似路徑之詞元（token）數量 | int | 同上 | 已觀測（啟發式樣式比對） | 0.60 | 否 | 可用 | 中 |
| `ListItemCount` | 項目符號／編號清單行數（作為交付項目數量的替代指標） | int | 同上 | 已觀測 | 0.70 | 否 | 可用 | 低 |
| `AcceptanceCriteriaCount` | checkbox 樣式的行數 | int | 同上 | 已觀測 | 0.80 | 否 | 可用 | 中 |
| `HasFixVerb` / `HasImplementVerb` / `HasRefactorVerb` / `HasInvestigateVerb` / `HasMigrateVerb` | 動詞出現與否旗標 | bool ×5 | 同上 | 已觀測（關鍵字比對） | 0.60（自陳：廣召回率詞表，依 `Wave2_Lessons.md` §1 issue #5 所述，有一定的偽陽性風險） | 否 | 可用 | 高 |
| `MentionsTests` / `MentionsSchemaOrAPI` / `MentionsSecurity` / `MentionsPerformance` / `MentionsDocumentation` | 關鍵字指標旗標 | bool ×5 | 同上 | 已觀測（關鍵字比對） | 0.60 | 否 | 可用 | 高 |
| `LongDocumentIndicator` | 偵測到章節／段落／報告用語 | bool | 同上 | 已觀測 | 0.60 | 否 | 可用 | 中 |
| `QuestionIndicator` | 偵測到僅含問題的提示詞 | bool | 同上 | 已觀測 | 0.60 | 否 | 可用 | 中 |

### 1b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| `ByteLength`/`RuneCount`/`LineCount` | 是 | 是 | 是 | 範疇估計 | 每回合 | 低（純粹針對已在記憶體中的提示詞進行運算） | 不會獨立保存——依 ADD §14.7 節的隱私規則，於需要時從提示詞雜湊沿革（hash lineage）中即時衍生 | N/A——原始資料從不儲存 |
| `ApproxTokens` | 是 | 是 | 低權重（一旦有真實用量資料即取代此欄位） | 範疇估計、Token 預測 | 每回合 | 低 | 同上 | 同上 |
| `ExplicitPathCount`/`ListItemCount`/`AcceptanceCriteriaCount` | 是 | 是 | 是 | 範疇估計 | 每回合 | 低 | 同上 | 同上 |
| 動詞／關鍵字旗標（10 個欄位） | 是 | 是 | 是 | 範疇估計、任務分類 | 每回合 | 低 | 同上 | 同上 |

**隱私附註（非獨立欄位，但至關重要）：** 上述欄位均不會保留提示詞的原始文字本身——`predictor-02` 的隱私測試（憲章 §7）透過 reflection walk（反射走訪）＋ JSON marshal ＋ `%+v` 格式檢查，針對預先埋入的 canary 字串進行驗證，以確認此點。本登錄冊之所以能收錄這個項目，正是*因為*有這項保證，而不是儘管有這項保證。

---

## 2. 儲存庫特徵

來源：`internal/features/dto.go`（`RepositoryFeatures`，型別已凍結，並已在 `predictor-05` 中透過假物件（fake）測試過，但**目前尚無真正的儲存庫內省（repository-introspection）產生器存在**——見下方 §0 但書。）

### 2a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `TrackedFileCount` | Git 已追蹤的檔案數量 | int | （已宣告，尚未串接） | 未知 | 0.00 | 否 | 未知 | 中 |
| `LanguageCount` | 相異程式語言數量 | int | （已宣告，尚未串接） | 未知 | 0.00 | 否 | 未知 | 低 |
| `GoModuleCount` / `GoPackageCount` | Go module／package 相依圖的大小 | int | ADD §14.4 節之 `go list -deps -json`（已指定，尚未實作） | 未知 | 0.00 | 否 | 未知 | 中 |
| `DotNetProjectRefs` | .NET 專案參照數量 | int | ADD §14.4 節之 `dotnet list <project> reference`（已指定，尚未實作） | 未知 | 0.00 | 否 | 未知 | 低 |
| `DirtyFileCount` / `DirtyLineCount` | 尚未提交（commit）變更的規模 | int | `internal/gitx`（儲存庫檢查點〔Repository Checkpoint〕角色，已有真實程式碼——`ParsePorcelainV2`、`DiffNumstat`）**但尚未串接至 `RepositoryFeatures`** | 可用（底層 gitx 資料而言），未知（作為 `RepositoryFeatures` 欄位而言） | 以已串接的狀態而言為 0.00；底層 `gitx` 基本操作（primitives）則為已觀測、信賴度 1.00 | 否 | 未知（屬串接缺口，而非資料缺口——見 §0） | 高 |
| `TargetDirFanOut` | 可能目標目錄的扇出（fan-out）數 | int | （已宣告，尚未串接） | 未知 | 0.00 | 否 | 未知 | 中 |
| `TestProjectCount` | 測試專案／package 數量 | int | （已宣告，尚未串接） | 未知 | 0.00 | 否 | 未知 | 低 |
| `IsMonorepo` / `IsWorktree` | 儲存庫結構旗標 | bool | `internal/gitx` resolver（已有真實程式碼）**但尚未串接** | 可用（就底層而言），未知（就已串接特徵而言） | 以已串接的狀態而言為 0.00 | 否 | 未知（串接缺口） | 低 |
| `RecentChangedPathCount` | 近期異動路徑數量 | int | （已宣告，尚未串接） | 未知 | 0.00 | 否 | 未知 | 中 |

**§0 但書，重要：** 這是整份登錄冊中**串接缺口而非資料缺口**最清楚的案例。`internal/gitx`（Phase 1/2，`checkpoint-b02`／`b03`）已經能計算未提交檔案／行數、worktree 對比 main 的偵測，以及完整的 status／numstat 解析——這些都是真實、已測試、屬於已觀測品質的資料。但目前沒有任何程式碼路徑會把這些資料餵入已填值的 `RepositoryFeatures`；根據本階段自身的驗證（見上方程式碼閱讀），`predictor-05` 的 `FeatureSource.Repository()` 方法目前只是一個介面，背後只有一個測試用假物件（fake）。補上這個缺口屬於串接工作，而非新資料蒐集工作——這一點已在 `Feature_Gap_Report.md` 與 `ADR_Recommendations.md` 中明確標記。

### 2b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| 所有 `RepositoryFeatures` 欄位 | 是（一旦串接） | 是（一旦串接） | 是（一旦串接） | 範疇估計 | 每回合，可快取（cache-eligible）（ADD §14.4 節：針對高成本呼叫採用「background-refresh」〔背景重新整理〕） | 中（Git／language-server 呼叫；ADD 明確警告不要讀取整個儲存庫） | 尚未定義——目前不存在任何用於儲存庫快照的持久化層 | 尚未定義 |

---

## 3. 工作階段特徵

來源：`internal/features/dto.go`（`SessionFeatures`，狀態與儲存庫特徵相同：型別已凍結，尚無實際運作中的產生器）。

### 3a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `RecentTurnUsageP50/P80/P90` | 近期回合 token 使用量的實證分位數 | `*float64` ×3 | （已宣告，尚未串接——需要 A1／B1 遙測資料，見 `Missing_Telemetry_Report.md`） | 未知 | 0.00 | 否 | 未知 | 極高 |
| `ChangedFilesRecentP50/P90` / `ChangedLinesRecentP50/P90` | 近期回合範疇的實證分位數 | `*float64` ×4 | 同上 | 未知 | 0.00 | 否 | 未知 | 高 |
| `RetryRate` / `TestFailureRate` | 近期實證失敗率 | `*float64` ×2 | 同上 | 未知 | 0.00 | 否 | 未知 | 高 |
| `ToolOutputBytesP50` | 工具輸出的實證大小 | `*int64` | 同上 | 未知 | 0.00 | 否 | 未知 | 低 |
| `ContextGrowthRateP50` | 上下文成長率的實證值 | `*float64` | 同上 | 未知 | 0.00 | 否 | 未知 | 高 |
| `CompactionCount` | 工作階段壓縮（compaction）次數 | int | 同上 | 未知 | 0.00 | 否 | 未知 | 中 |
| `CheckpointAge` | 距離上次檢查點的時間 | `*time.Duration` | 可部分從 `domain.StateCheckpoint.CreatedAt` 推導（已凍結型別，尚無實際運作中的產生器——進度樹／狀態檢查點〔Progress Tree/State Checkpointing〕，`checkpoint-a01` 及後續，尚未建置） | 未知 | 0.00 | 否 | 未知 | 中 |

所有 `SessionFeatures` 欄位都採用指標語意（pointer semantics）——依據該型別自身的文件註解（已於原始碼中驗證），`nil` 正確地代表「未知」，而非「零」。即使每個欄位目前的可用性都是 `Unknown`，這仍是值得一提的設計優點：一旦這組特徵日後真的串接上真實資料，「未知 vs. 零」的紀律早已內建其中，不需要事後補救。

### 3b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| 所有 `SessionFeatures` 欄位 | 是（一旦串接） | 是（一旦串接；這正是此資料主要服務的層級——依設計補充文件〔Design Supplement〕所述，即第 2 版「統計型預測器」） | 是（一旦串接） | 範疇估計、Token 預測 | 每回合，需要歷史資料累積（ADD §15.2 節：退出冷啟動前需要 `count(similar) >= 8`） | 中（需要查詢已保存的回合歷史紀錄） | 尚未定義——`foundation-06` 的 `turns`／`turn_usage` 資料表（ADD §12.2 節），尚未建置 | 尚未定義 |

---

## 4. 供應商特徵

來源：`internal/domain/capability.go`（`ProviderCapabilities`，真實存在、已於 Bootstrap 階段凍結）、`internal/domain/usage.go`（`UsageObservation`、`QuotaObservation`、`ContextObservation`，真實存在、已於 Bootstrap 階段凍結）、`claude-provider-01`／`-04`（真實的剖析器／正規化器，已針對固定資料〔fixtures〕測試過——但從未在真實 session 中測試過）。

### 4a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `ProviderCapabilities.*`（19 個布林欄位） | 各供應商的能力旗標 | bool ×19 | ADD §8.6 節，已凍結型別；依設計意圖而填值，尚未有實際運作中的 `ProviderCapabilityReader` 實作 | 未知（尚未建置真正的讀取器） | 0.00 | 否 | 未知 | 高 |
| `UsageObservation.{Input,CachedInput,CacheCreation,CacheRead,Output,Reasoning}Tokens` | 每回合的 token 明細 | `*int64` ×6 | `claude-provider` 狀態列（status-line）剖析器（真實程式碼） | 可用**僅針對固定資料而言**；針對即時資料而言則為未知 | 針對固定資料為 1.00，針對真實 session 為 0.00 | 否（源自固定資料，並非來自真實回合） | 可用（僅限固定資料範圍） | 極高 |
| `QuotaObservation.UsedPercent` | 五小時／七天配額百分比 | `*float64` | 同上 | 可用（僅限固定資料範圍） | 與上述相同的但書 | 否 | 可用（僅限固定資料範圍） | 極高 |
| `QuotaObservation.ResetsAt` | 配額視窗重設時間戳記 | `*time.Time` | 同上 | 可用（僅限固定資料範圍） | 與上述相同的但書 | 否 | 可用（僅限固定資料範圍） | 高 |
| `ContextObservation.{UsedTokens,WindowTokens,UsedPercent}` | 上下文視窗使用量 | `*int64`/`*int64`/`*float64` | 同上 | 可用（僅限固定資料範圍） | 與上述相同的但書 | 否 | 可用（僅限固定資料範圍） | 極高 |

**「Available（僅限固定資料範圍）」這個型態貫穿本節全部內容，值得在此明確且不含糊地說明一次：** `claude-provider` 的剖析器與正規化器是真實、已測試、可運作的程式碼——已由負責人（lead）在 Phase 1／2 審查期間獨立驗證過，包括隱私與冪等性（idempotency）測試。但它們通過的每一項測試，都是針對手工建構的 JSON 固定資料，而不是真實 Claude Code session 所發出的任何一個位元組。本登錄冊刻意不將這些資料稱為未加限定的「Available」——即 §2 節 `internal/gitx` 儲存庫資料所使用的那種無條件的「可用」，因為固定資料只能證明*剖析器*可以運作，並不能證明該*特徵*曾在真實環境中被觀測到。

### 4b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| `ProviderCapabilities.*` | 是 | 是 | 是 | 所有階段（能力旗標會決定每一個下游特徵是否可用） | 每個工作階段一次（能力在工作階段進行中不會改變） | 一旦實作完成即為低（只需一次偵測呼叫） | 尚未定義 | 尚未定義 |
| `UsageObservation.*` | 是 | 是 | 是 | Token 預測、配額預測 | 每回合（或依狀態列更新頻率，頻率更高） | 低（供應商本就會發出的資料，只需剖析——已完成）到中（持久化保存尚未完成） | `foundation-06` 的 `turn_usage` 資料表（ADD §12.2 節），尚未建置 | 依 ADD §27 節，原始數值會被保留；提示詞文字則永不保留（此為不相關的考量，正確地分開處理） |
| `QuotaObservation.*` | 是 | 是 | 是 | 配額預測、續航預測 | 每次狀態列更新 | 低（剖析已完成）／中（持久化尚未完成） | `foundation-06` 的 `quota_observations` 資料表，尚未建置 | 尚未定義 |
| `ContextObservation.*` | 是 | 是 | 是 | 配額預測（上下文推估）、風險合併 | 每回合 | 低／中（拆分方式同上） | `foundation-06` 的 `context_observations` 資料表，尚未建置 | 尚未定義 |

---

## 5. 執行特徵

來源：`internal/domain/forecast.go`（`ScopeEstimate`、`TokenForecast`、`QuotaForecast`，真實存在、已依 ADR-041 凍結）、`internal/predictor/scope`（`RuleScopeEstimator`，真實存在，`predictor-05`）、`internal/predictor/runway`（真實存在，`predictor-06`）。

### 5a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `ScopeEstimate.FilesReadP50/P80/P90` | 預測的已讀取檔案數分位數 | `*int64` ×3 | `RuleScopeEstimator`（真實存在，Phase 2） | 估計 | 冷啟動預設值或工作階段混合值，從未有真實資料驗證過 | 否——這是一項*預測*，而非觀測結果 | 估計 | 高 |
| `ScopeEstimate.FilesChangedP50/P80/P90` | 預測的已變更檔案數分位數 | `*int64` ×3 | 同上 | 估計 | 同上 | 否 | 估計 | 極高 |
| `ScopeEstimate.LinesChangedP50/P80/P90` | 預測的已變更行數分位數 | `*int64` ×3 | 同上 | 估計 | 同上 | 否 | 估計 | 極高 |
| `ScopeEstimate.ToolCallsP50/P90`、`VerificationP50/P90`、`RetryLoopsP50/P90`、`DurationP50/P90` | 預測的工具呼叫／驗證／重試／耗時 | `*int64` ×8 | 同上 | 未知 | N/A | 否 | 未知（本階段刻意保持 `nil`——已由 `TestEstimateScopeUnknownFieldsStayNil` 驗證） | 中 |
| `ScopeEstimate.RequiresUnitTests/RequiresIntegration/CrossProject/MigrationLikely/SecuritySensitive` | 預測的布林訊號 | bool ×5 | 同上 | 估計（源自關鍵字／啟發式推導） | 0.50-0.60 | 否 | 可用（已填值，信賴度低） | 高 |
| `TokenForecast.TokensP50/P80/P90` | 預測的 token 成本 | int64 ×3 | （尚未建置——`predictor-05b`，已延後至 Phase 2 之後） | 未知 | 0.00 | 否 | 未知 | 極高 |
| `QuotaForecast.ProjectedQuotaUsedP90` / `ProjectedContextUsedP90` | 推估的配額／上下文使用位置 | `*float64` ×2 | （尚未建置——`predictor-05c`，已延後至 Phase 2 之後） | 未知 | 0.00 | 否 | 未知 | 極高 |
| `RunwayForecast.RiskScore` | 10 分鐘配額危害分數 | float64 | `internal/predictor/runway`（真實存在，`predictor-06`） | 估計（依 ADD §15.7 節採用未校準備援值） | 本階段一律未校準（已由 `TestScoreNeverCalibratedNeverPanics` 驗證） | 否 | 可用（僅有分數，非機率值） | 極高 |
| `RunwayForecast.HitProbability` | 已校準的 10 分鐘命中機率 | `*float64` | 同上 | 未知 | 0.00（ADR-026／ADD §15.6 節：在通過校準閘門之前，正確地一律保持 `nil`） | 否 | 未知（依設計如此） | 極高 |

### 5b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| `ScopeEstimate.*` | 是（這正是規則型預測器的輸出） | 是（作為第 2 版的輸入特徵） | 是 | 範疇估計 | 每回合 | 低（純粹運算，一旦輸入齊備即不需 I/O） | `predictor-09` 評估持久化保存，尚未建置 | 尚未定義 |
| `TokenForecast.*` | 尚未建置 | 尚未建置 | 尚未建置 | Token 預測 | 每回合（一旦建置） | 低（運算，依 ADD §15.2 節公式） | 同上 | 尚未定義 |
| `QuotaForecast.*` | 尚未建置 | 尚未建置 | 尚未建置 | 配額預測、上下文預測 | 每回合（一旦建置） | 低至中（依 ADD §15.3／15.9 節） | 同上 | 尚未定義 |
| `RunwayForecast.RiskScore` | 是 | 是 | 低權重（在通過 ADD §15.6 節閘門之前，僅為分數，非已校準機率） | 續航預測 | 在受管理回合進行期間持續更新（ADD §15.4 節） | 低（針對即時燃燒率〔burn-rate〕樣本的算術運算） | 尚未定義 | 尚未定義 |
| `RunwayForecast.HitProbability` | 否（校準前從不填值） | 否（同上） | 否（同上） | 續航預測 | 校準前為 N/A | N/A | N/A | N/A |

---

## 6. 檢查點特徵

來源：`internal/domain/artifact.go`（`ArtifactRef`、`EvidenceRef`）、`internal/domain/checkpoint.go`（`StateCheckpoint`）、`internal/gitx`（`Fingerprint`，真實存在，`checkpoint-b02`／`b03`）。

### 6a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `Fingerprint.ComputeDigest()` | 權威的儲存庫狀態摘要值 | string（SHA-256 hex） | `internal/gitx`（真實存在，`checkpoint-b03`，已針對決定性〔determinism〕／順序無關性〔order-independence〕獨立驗證過） | 已觀測 | 1.00 | 是——真實 Git 狀態的加密摘要值 | 可用 | 高 |
| `Fingerprint.HeadOID` / `Branch` | 儲存庫識別資訊 | string ×2 | 同上 | 已觀測 | 1.00 | 是 | 可用 | 高 |
| `Fingerprint.Entries`（狀態項目） | 工作目錄／索引狀態 | `[]Entry` | 同上 | 已觀測 | 1.00 | 是 | 可用 | 中 |
| `ArtifactRef.SHA256` / `Bytes` | 產出物（artifact）識別資訊／大小 | string / int64 | `internal/domain/artifact.go`（已凍結型別；尚未建置產生器——進度樹／狀態檢查點角色，`checkpoint-a01` 及後續，尚未建置） | 未知 | 0.00 | 是（一旦產生即是如此——檢查碼〔checksum〕本質上就是真實資料） | 未知 | 極高 |
| `StateCheckpoint.ProgressTreeVersion` / `CompletedNodeIDs` | 進度狀態快照 | int64 / `[]ProgressNodeID` | 同上（已凍結型別，尚無產生器） | 未知 | 0.00 | 是（一旦產生） | 未知 | 極高 |
| `StateCheckpoint.IntegritySHA256` | 檢查點完整性摘要值 | string | 同上 | 未知 | 0.00 | 是（一旦產生） | 未知 | 極高 |

### 6b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| `Fingerprint.*` | N/A——用於恢復安全性（resume-safety）驗證，而非預測用途 | N/A | N/A | 並非預測階段特徵；由優雅暫停／恢復（Graceful Pause/Resume）驗證所使用（ADD §15.8 節、FR-149） | 每次檢查點事件 | 低（Git plumbing 呼叫，依已驗證的實作，argv 已具安全性） | 尚未定義——`repository_checkpoints` 資料表（ADD §12.2 節），尚未建置 | 尚未定義 |
| `ArtifactRef.*` / `StateCheckpoint.*` | N/A（原因相同） | N/A | N/A | 並非預測階段特徵 | 每個節點完成時 | 尚無法衡量（尚未建置） | `state_checkpoints`／`artifacts` 資料表（ADD §12.2 節），尚未建置 | 尚未定義 |

**附註：** 檢查點特徵之所以納入本登錄冊，是為了求其完整性（儲存庫負責人的指示將其列為 8 個必要群組之一），但它們正確地*不是*預測器的輸入——它們是完整性／恢復安全性的基本操作（primitives）。將它們收錄於此，是為了讓這份權威特徵字典保持完整，並不代表它們會餵入範疇／Token／配額／風險預測。

---

## 7. 校準特徵

來源：`internal/domain/measurement.go`（`Confidence`、`Calibrated` 這組模式貫穿每一種預測／觀測型別），ADD §15.6 節。

### 7a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `Confidence` enum（`Exact`/`High`/`Medium`/`Low`/`Unavailable`） | 每筆觀測資料的信賴標籤 | enum，5 個值 | `internal/domain/measurement.go`（真實存在，已於 Bootstrap 階段凍結） | 已觀測（enum 本身是真實程式碼）；衍生（任一實例的數值，皆由其產生者做出的判斷） | N/A（信賴度數值本身不會再被評分信賴度——這是刻意設計，而非缺口） | 否 | 可用 | 極高 |
| `Calibrated` bool（存在於 `TokenForecast`、`QuotaForecast`、`RiskComponent`、`RunwayForecast`） | 分數是否已通過校準閘門 | bool | 同樣的模式，真實存在 | 已觀測 | N/A | 否 | 可用（本階段每個產生器正確地一律回傳 `false`） | 極高 |
| ECE（Expected Calibration Error，預期校準誤差） | 校準品質指標 | float64，0-1 | ADD §15.6 節（已指定，尚未實作——需要至少 20 筆有效樣本） | 未知 | 0.00 | 是（一旦計算出來，即是針對真實結果的真實統計量） | 未知 | 極高 |
| Brier score（Brier 分數） | 校準品質指標 | float64 | 同上 | 未知 | 0.00 | 是（一旦計算出來） | 未知 | 高 |

### 7b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| `Confidence` / `Calibrated` | 是——每項輸出都必須具備 | 是 | 是 | 所有階段 | 每次預測 | 低（屬於中繼資料，不需另外蒐集） | 跟隨其所標註的紀錄一起儲存 | 與被標註的紀錄相同 |
| ECE / Brier score | 否（規則型預測器層級從不宣稱具備校準） | 是（這是將預測器從未校準晉升為已校準的閘門，ADD §15.6 節） | 是 | 後設層級（Meta）——決定任一層級的輸出是否可以以機率形式呈現 | 每次留出集評估週期 | 高（需要累積已標記的結果資料，即 `Missing_Telemetry_Report.md` 中的 A5／A6） | 尚未定義 | 尚未定義 |

---

## 8. 遙測特徵

來源：`pkg/protocol/v1/event.go`（`Event`，真實存在，已於 Bootstrap 階段凍結）、`internal/telemetry/claude/normalizer.go`（真實存在，`claude-provider-04`）。

### 8a. 身分與來源

| 特徵 | 說明 | 資料型別／單位 | 來源 | 出處 | 信賴度 | 真實資料 | 目前可用性 | 重要性 |
|---|---|---|---|---|---|---|---|---|
| `Event.EventType` | 封閉分類標籤（52 個值） | enum | `pkg/protocol/v1`（真實存在） | 已觀測 | 1.00 | 否（是一種分類，而非事實） | 可用 | 高 |
| `Event.IdempotencyKey` | 去重複用的鍵值 | string | `claude-provider-04` normalizer（真實存在，已測試） | 已觀測 | 1.00 | 是（決定性摘要值，已驗證） | 可用 | 高 |
| `Event.SchemaVersion` | 傳輸格式版本標籤 | string，例如 `auspex.event.v1` | 已於 Bootstrap 階段凍結的常數 | 已觀測 | 1.00 | 是 | 可用 | 中 |
| `Event.Payload` | 已正規化、已遮蔽（redacted）的事件內容 | `map[string]any` | `claude-provider-04` normalizer | 已觀測（僅針對固定資料而言——與 §4 節相同的但書） | 針對固定資料為 1.00 | 否 | 可用（僅限固定資料範圍） | 極高 |
| `Event.ObservedAt` / `OccurredAt` | 時間戳記 | `time.Time` ×2 | 同上 | 已觀測（針對固定資料／`domain.Clock` 注入而言） | 1.00 | 是 | 可用（僅限固定資料範圍） | 中 |

### 8b. 適用性與維運

| 特徵 | 規則型預測器 | 統計型預測器 | 機器學習型預測器 | 預測階段 | 更新頻率 | 蒐集成本 | 儲存位置 | 保留原則 |
|---|---|---|---|---|---|---|---|---|
| `Event.*`（所有欄位） | 間接——餵入 §4 節的觀測型別，並非直接被使用 | 間接 | 間接 | 餵入供應商特徵（§4 節），再由其餵入所有預測階段 | 每次供應商事件（在回合進行中頻率可能非常高） | 低（剖析／正規化已完成；持久化保存尚未完成——見 `Missing_Telemetry_Report.md` 中的 B1） | `events` 資料表為 ADD §11 節所隱含，但未列在 §12.2 節明確的資料表清單中——已在 `Feature_Gap_Report.md` 中標記為缺口 | 尚未定義；ADD §27 節規範原始酬載（payload）遮蔽要求，且已由 `claude-provider` 的隱私測試強制執行 |

---

## 9. 登錄冊完整性聲明

本登錄冊涵蓋此程式碼庫目前已凍結型別與真實實作所定義的每一項特徵，加上 ADD §14–§16 節文字所指定、但目前尚無程式碼產生的每一項特徵。除了這兩個來源之外，本登錄冊**不會**憑空杜撰任何特徵。特徵總數：**8 個群組、約 95 個個別欄位**（確切數量取決於像 `FilesReadP50/P80/P90` 這類指標陣列欄位的計算方式——究竟算作 1 個特徵家族，還是 3 個各自獨立的欄位；為求精確，本登錄冊在上文將其列為各自獨立的欄位）。

**整份登錄冊的可用性總覽：**

| 目前可用性 | 概估欄位數 | 佔比 |
|---|---|---|
| 可用 | ~35 | ~37% |
| 可用（僅限固定資料範圍，尚未上線） | ~15 | ~16% |
| 估計 | ~15 | ~16% |
| 未知 | ~30 | ~31% |

關於補上「未知／僅限固定資料範圍」缺口所需的工作，請參閱 `Feature_Gap_Report.md`；關於針對同一份資料的衍生信賴度檢視，請參閱 `Prediction_Confidence_Report.md`。
