# Auspex Vertical-Slice Contract Freeze

> 🌐 [English](CONTRACT_FREEZE.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：**已接受（ACCEPTED）** — 啟動階段（Bootstrap stage），由主導者直
接執行（參見 `CONSTITUTION.md` 關於「Bootstrap 僅限主導者前置條件」的
待批修訂案，已由專案負責人於 2026-07-12 核准）。
契約提交（Contract commit）：`4262b4b`
Go module：`github.com/huaiche94/auspex`
Schema 基準線：`auspex.event.v1` / `auspex.progress-tree.v1` / `auspex.state-checkpoint.v1` / `auspex.repository-checkpoint.v1` / `auspex.pause.v1` / `auspex.api.v1`

## 匯入路徑（Import paths）

| 關注點 | 套件 |
|---|---|
| Domain 實體 | `github.com/huaiche94/auspex/internal/domain` |
| 跨元件 ports | `github.com/huaiche94/auspex/internal/app` |
| Event 協定 | `github.com/huaiche94/auspex/pkg/protocol/v1` |
| SQLite runtime | `github.com/huaiche94/auspex/internal/storage/sqlite`（尚未建立 — `foundation` 角色負責） |

## Schema 版本字串（Schema-version strings）

```text
auspex.event.v1
auspex.progress-tree.v1
auspex.state-checkpoint.v1
auspex.repository-checkpoint.v1
auspex.pause.v1
auspex.api.v1
```

以常數形式定義於 `pkg/protocol/v1/event.go`（`SchemaVersionEvent` 等），
並由 `pkg/protocol/v1/event_test.go` 涵蓋測試。

## ID 與冪等性規則（ID and idempotency rules）

- 所有 Auspex 自有的實體 ID（`internal/domain/ids.go`）都是不透明
  （opaque）、以 `string` 為底的型別（`RepositoryID`、`WorktreeID`、
  `SessionID`、`TurnID`、`EvaluationID`、`PredictionID`、`DecisionID`、
  `TaskID`、`ProgressNodeID`、`StateCheckpointID`、
  `RepositoryCheckpointID`、`PauseID`、`WakeJobID`、`ResumeAttemptID`、
  `EventID`）——產生當下即為 UUIDv7（由 `foundation` 的 `internal/idgen`
  負責），永遠不會被解析出任何意義。
- 事件冪等性：`Event.IdempotencyKey`（`pkg/protocol/v1/event.go`）——當
  供應商（provider）提供穩定 ID 時，依供應商事件身分決定性地產生；否則
  採內容摘要（digest）。確切的摘要演算法由負責角色（例如
  `claude-provider`）定義；此欄位本身則在此凍結。
- `CompleteNodeRequest.IdempotencyKey`（`internal/app/ports.go`）——以相
  同金鑰重放（replay）相同的完成請求時，必須回傳相同的結果；同一金鑰下
  夾帶不同內容則視為衝突（conflict），而非靜默覆寫（Constitution §6）。
- `Authorization` — 一次性；其消費（consumption）為恰好一次
  （exactly-once），由 `predictor` 在儲存層強制執行，而非僅靠本契約本
  身。

## 未知／null 語意（Unknown/null semantics）

- 選填（optional）的數值／量測欄位（`internal/domain/usage.go` 中的
  `UsageObservation`、`QuotaObservation`、`ContextObservation`、
  `RunwayForecast`）使用 Go 指標型別（`*int64`、`*float64`、
  `*time.Time`）——`nil` 代表**未知**，絕不會以零值替代（ADD 原則 1：
  「未知不是零」（Unknown is not zero））。
- `RunwayForecast.HitProbability` 是 `*float64`，只有在
  `Calibrated == true` 時才有意義；未校準（uncalibrated）的預測仍會回
  報 `RiskScore`（永遠存在，範圍 0–1，絕不稱之為機率）——這與
  `agents/predictor.md` 中 ADD §5.1 FR-045／冷啟動契約的作法一致。
- `ProviderCapabilities`（`internal/domain/capability.go`）的欄位皆為單
  純的 `bool`——供應商轉接器（adapter）回報 `false` 必須代表「已確認不
  存在」，而不是「尚未檢查」。尚未檢查某項能力的轉接器，在能夠明確回答
  之前，不得呼叫 `Capabilities()`。

## 交易邊界（Transaction boundaries）

- `TxRunner.WithTx`（`internal/app/ports.go`）是唯一凍結的交易回呼
  （callback）形狀，所有涉及儲存體的操作都使用它。回傳非 nil 的錯誤即
  會回滾（rollback）。
- `ProgressTreeService.CompleteNode` 必須在同一次 `WithTx` 呼叫內（或等
  效的 outbox-pattern 邊界內）執行其產出物暫存並驗證
  （artifact-stage-and-verify）、節點狀態更新，以及 State Checkpoint 建
  立——部分完成（partial completion）不是有效狀態（Constitution §6）。
- `EvaluationService.ConsumeAuthorization` 必須與其所授權的動作（例如放
  行某個提示詞）保持原子性（atomic）——不得存在授權已標記為已消費、但
  被允許的動作卻沒有發生的時間窗，反之亦然。
- `GracefulPauseService` 的持久化階段（persist phase）（Progress Tree
  快照 → State Checkpoint → Repository Checkpoint → Pause Record →
  Wake Job）是一連串相依的寫入，而不是單一的扁平交易（它橫跨
  `checkpoint` 角色的兩個部分）——每個步驟自身的交易邊界由該步驟所屬的
  服務定義；`runtime` 負責將它們排序，並將「部分序列失敗」處理成可續行
  （resumable）的狀態，而不是靜默的落差。

## 錯誤契約（Error contract）

`internal/domain/errors.go` 定義了凍結的形狀：

```go
type ErrorCode string
const (
    ErrCodeValidation ErrorCode = "validation"
    ErrCodeNotFound ErrorCode = "not_found"
    ErrCodeConflict ErrorCode = "conflict"
    ErrCodeUnauthorized ErrorCode = "unauthorized"
    ErrCodeIntegrity ErrorCode = "integrity"
    ErrCodeUnavailable ErrorCode = "unavailable"
    ErrCodeInternal ErrorCode = "internal"
)
type Error struct { Code ErrorCode; Message string; Retryable bool; Details map[string]string }
```

失敗即開放（fail-open）與失敗即關閉（fail-closed）
（Constitution §immutable-day-one-rule-10，源自本垂直切片計畫）：**操作
性觀測（operational observation）**失敗（例如讀取配額逾時）**可以**失
敗即開放——以 `Confidence: ConfidenceUnavailable` 繼續進行，不阻擋使用
者。**狀態完整性（state-integrity）**失敗（例如檢查點的 SHA-256 不相
符，或交易部分套用）**必須**失敗即關閉——`ErrCodeIntegrity`、
`Retryable: false`，且呼叫端不得當作成功繼續進行。

## 隱私契約（Privacy contract）

- 原始提示詞文字（raw prompt text）絕不會是 `internal/domain` 或
  `pkg/protocol/v1` 中任何型別的欄位。`EvaluateTurnRequest.PromptHash`
  （`internal/app/ports.go`）與 `Authorization.PromptHash` 是僅有的兩個
  由提示詞衍生出的欄位，且兩者都是雜湊值（hash），不是文字。
- `Event.Payload`（`pkg/protocol/v1/event.go`）是一個正規化的
  `map[string]any`，由負責的供應商角色在遮蔽（redaction）後填入——凍結
  契約本身並不強制執行遮蔽；那是各供應商角色依其自身職責包（packet）
  （例如 `agents/claude-provider.md` §Privacy）所負的責任，並由 `qa` 的
  洩漏掃描器（`qa-05`）驗證。

## Migration 範圍（Migration ranges）

- 0000–0009 `foundation`
- 0010–0019 `claude-provider`
- 0020–0029 `checkpoint`（Part A — progress/state）
- 0030–0039 `checkpoint`（Part B — repository）
- 0040–0049 `predictor`
- 0050–0059 `runtime`（Part A — pause/scheduler）
- 0060–0069 retention/gc（跨切面（cross-cutting），不屬於任何垂直切片
  角色所有；由 ADR-046 指派，`docs/adr/0046-tiered-telemetry-retention.md`）

`runtime` Part B 沒有分配到範圍；除非 `contract-integrator` 明確指派一
個範圍，否則它不會新增 schema（`Auspex_Parallel_Execution_Plan.md`
§7）。

## Predictor pipeline ports（ADR-041）

凍結於 2026-07-12，修訂原始的 Bootstrap 契約。完整理由請見：
`docs/adr/0041-predictor-forecast-layer.md`。

`internal/app/ports.go` 中新增四個窄式（narrow）介面，各自對應一個獨立
的管線階段，目前皆尚未實作（僅有契約）：

```text
ScopeEstimator.EstimateScope   -> domain.ScopeEstimate    (ADD §14)
TokenForecaster.ForecastTokens -> domain.TokenForecast     (ADD §15.1-15.2)
QuotaForecaster.ForecastQuota  -> domain.QuotaForecast     (ADD §15.3, §15.9)
RiskCombiner.Combine           -> CombineRiskResult        (ADD §16.1-16.2)
```

管線順序：Scope Estimator → Token Forecaster → Quota Forecaster →
Risk Combiner → Policy。**`GracefulPauseService.Observe`（Runway
Forecaster）獨立於此鏈之外**——它不是 `RiskCombiner` 的輸入，
`RiskCombiner` 也不是它的輸入之一。這是原始 Bootstrap 時期 DAG 中一個
真實的錯誤（`predictor-07` 曾相依於 `predictor-06`）；ADR-041 已將其修
正。

`internal/domain/forecast.go` 中新增的凍結型別：`ScopeEstimate`（精確
對應 ADD §14.1 的欄位集合，依下方「未知不是零」規則採指標型數值欄
位）、`TokenForecast`（`TokensP50/P80/P90`）、`QuotaForecast`
（`ProjectedQuotaUsedP90`、`ProjectedContextUsedP90` — 兩個投影放在同
一型別中，因為它們共用同一套差值投影（delta-projection）技術，且都會
餵入 `RiskCombiner`）、`RiskComponent`（`Score`、`Calibrated`、
`Confidence`、`ReasonCodes`）、`DataQuality`。`ReasonCode` 現在是以 ADD
§16.4 常數清單為底的型別化 `string` 列舉——`Evaluation.ReasonCodes` 從
`[]string` 改為 `[]domain.ReasonCode`（安全：Wave 1 沒有任何程式碼建構
或消費過該欄位）。

術語說明：`Auspex_Predictor_Design_Supplement.md` 將第三個風險項稱為
「execution_risk」；凍結契約則沿用 ADD §16.1 既有的名稱
`completion_risk`——同一個概念、統一一個名稱，依 Constitution §1。

冷啟動（Cold-start）：在尚未存在持久性歷史遙測資料之前，
`QuotaForecaster` 的實作**可以**產生一個決定性的「目前觀測值加上預設
差值」估計（`Calibrated: false`、`Confidence: ConfidenceLow`）——這與
`predictor-04`／`predictor-08` 已建立的紀律相同。這不是一個之後會被丟
棄的樁（stub），而是在此凍結形狀下正確的第一版實作。

## 凍結的狀態轉換（Frozen state transitions）

列舉（Enum）來源（皆位於 `internal/domain/status.go`，其線上字串（wire
strings）由 `internal/domain/status_test.go` 驗證）：

- `TurnStatus`: `pending → authorized → running → {pause_pending → pausing → paused → resuming} → {completed | failed | interrupted | blocked | cancelled}`
- `ProgressNodeStatus`: `pending → ready → in_progress → checkpointing → {completed | failed} `，其中 `paused`、`skipped`、`blocked` 是可從 `in_progress`／`ready` 到達的旁支狀態（side states）。
- `PauseStatus`：`predicted → requested → quiescing → checkpointing → interrupting → sleeping → wake_pending → validating → resuming → resumed`，其中 `blocked_conflict`、`cancelled`、`failed` 是依 `agents/runtime.md` 所要求的狀態路徑可到達的終端／旁支狀態。

完整的各角色轉換驗證邏輯屬於對應的負責角色（節點轉換屬 `checkpoint`，
暫停轉換屬 `runtime`）——本檔案僅凍結列舉值及其線上字串，不凍結轉換表
的實作。

## Bootstrap 未凍結的部分（刻意留給負責角色決定）

依 `agents/contract-integrator.md` 的「範圍之外（Out of scope）」：目
前尚不存在 Claude 解析器、predictor 內部邏輯、checkpoint 儲存體內部邏
輯、暫停狀態機實作，或 CLI 處理器。`internal/app/ports.go` 中的請求／
回應 DTO 只具備足以編譯並表達介面形狀的最小欄位集——負責角色**可能**
會發現需要額外欄位；新增欄位的請求須依 Constitution §4 透過該角色的進
度成品提出，而不是靜默修改 `internal/app/ports.go`。

## 修訂紀錄（Amendments）

- **2026-07-17 — ADR-0053（#65 第一階段）：`TokenForecast` 新增
  input/output 拆分。** `domain.TokenForecast`（由 ADR-041 凍結）新增四個
  附加性（additive）pointer 欄位——`InputTokensP50/P90`、
  `OutputTokensP50/P90`——將即將發生的 turn 的 token 拆解為兩個相異的區間，
  其中 **input 區間在結構上比 output 區間更寬**（僅採 Bai et al. 2026 的
  方向：模型預測 input token 較差）。凍結的 total（`TokensP50/P80/P90`）維持
  不變且仍具權威性；`nil` 代表預測器未拆分（unknown 不等於 zero）。純附加式：
  所有建構點維持可編譯。相對加寬幅度（`inputIntervalWideningFactor`）與中心
  切分（`defaultInputTokenShare`）都是受 #11 把關的未校準結構性預設值——絕非
  擬合係數，且論文的 ~153:1 input:output 比值絕不引入（grounding 紀律）。此
  拆分絕不把 `Calibrated` 翻成 true（Constitution 原則 #2）。以附加方式持久化
  於 `predictions`（migration `0063`：`token_input_p50/p90`、
  `token_output_p50/p90`，皆可為 NULL），讓 forecast card 得以讀回；此 slice
  不傳播到 research export（該拆分是已 export 之 total 的確定性轉換，故無
  unlabeled-history 破口——延後到「校準後預測器獨立估計兩軸」的 wave）。完整
  設計見 ADR-0053。

- **2026-07-14 — ADR-048（#6）：真正的儲存庫檢查點還原（repository
  checkpoint restore）。** `app.RestoreRepositoryCheckpointRequest` 新
  增了附加性（additive）欄位 `Apply bool`（零值時完全保留
  checkpoint-b08「僅限 dry-run」的原有語意）；`app.RestoreResult` 新增
  了附加性欄位 `SafetyCheckpointID` 與 `UntrackedSkipped`。當設定
  Apply 且通過每一項 ADD §19.6 關卡時，`Service.Restore` 現在會真正執
  行還原：已暫存（staged）的修補檔透過 `git apply --index`、未暫存的
  透過 `git apply`、未追蹤檔案則進行擷取（no-clobber，具備
  capture-grade 路徑安全性）。任何參照（ref）都絕不會被異動
  （Constitution #9 — 結構性保證，`git apply` 無法移動 ref）；若目標
  處於 dirty 狀態，會先擷取一個安全檢查點（safety checkpoint）。完整
  的安全設計請見 ADR-048。

- **2026-07-14 — ADR-047（#20 第一階段）：`RecentSimilarTurnTokens` 回
  傳其世代層級（cohort rung）。** `app.FeatureDataSource.RecentSimilarTurnTokens`
  （以及 `internal/predictor/token.FeatureSource` 的窄式檢視）現在回傳
  `features.SimilarTurnTokens{Samples, Rung}`，而不是單純的
  `[]float64`，使得 ADD §15.2 世代（cohort）後備階梯所落在的層級，能在
  已持久化的預測資料列上以原因代碼（reason code）表示。此變更依
  ADR-044「變更需要 ADR」的規則核可；所有實作者與測試替身（fake）皆於
  同一變更中一併更新。四個附加性 `domain.ReasonCode` 值
  （`TOKEN_COHORT_MODEL_EFFORT` / `TOKEN_COHORT_MODEL_FAMILY` /
  `TOKEN_COHORT_PROVIDER_ONLY` / `TOKEN_COHORT_SESSION_ONLY`）以下方
  ADR-043 代碼相同的附加性核可方式加入分類體系。

- **2026-07-13 — ADR-044（REC-01）：feature-lookup port 凍結。**
  `app.FeatureDataSource` + `app.ResolvedSession` 新增至
  `internal/app/ports.go`，將 `internal/evaluation.DataSource` 的形狀
  逐字提升進凍結契約中（該套件現在改為別名指向凍結型別）。
  `internal/predictor/scope.FeatureSource` 與
  `internal/predictor/token.FeatureSource` 仍作為同一個 port 在消費端
  的窄式檢視（介面隔離，於各自定義處記載）。此舉結掉了上方一節中
  「repository/session feature lookup」的延遲事項；該節其餘內容仍然適
  用。

- **2026-07-13 — ADR-043 第二次增量（D-08）：兩個附加性
  `domain.ReasonCode` 值。** `CONTEXT_WARN_THRESHOLD_EXCEEDED` 與
  `CONTEXT_CHECKPOINT_THRESHOLD_EXCEEDED` 新增至
  `internal/domain/forecast.go` 中以 ADD §16.4 為底的列舉，由
  `internal/policy` 的情境使用率門檻規則（DECISION_LOG.md D-08）發出，
  使預測介面能解釋由情境（context）驅動的 WARN/CHECKPOINT_AND_RUN。純
  附加性：沒有任何既有值被改名、移除或重新賦予意義；針對原始清單做模
  式比對（pattern-matching）的消費端不受影響。此次增量的其餘部分皆維
  持在凍結範圍之外（套件內部的 `policy.DecideRequest`／`policy.Config`
  欄位、附加性 migration 0045、呈現層卡片欄位）。

- **2026-07-13 — ADR-045：產品更名 Preflight → Auspex。** 每個凍結的
  schema 版本字串都重新加上前綴（`preflight.error.v1` →
  `auspex.error.v1`、`preflight.event.v1` → `auspex.event.v1`、
  `preflight.evaluate.v1` → `auspex.evaluate.v1` 等），module 路徑改為
  `github.com/huaiche94/auspex`，使用者資料目錄改為 `auspex/`。之所以
  允許這麼做，純粹是因為專案尚未正式發布、也沒有任何外部消費者；首次
  公開發布之後，本文件自身的 schema 版本規則將禁止這類變更。
  `docs/archive/` 中的歷史文件依設計保留舊字串。

- **2026-07-13 — ADR-046：migration 範圍 0060–0069 指派給
  retention/gc。** 分層遙測保留（tiered telemetry retention，
  `internal/retention`、`auspex gc`、migration `0060_retention.sql`）
  是跨切面（cross-cutting）的——它會跨所有角色的資料表封存並刪除資料
  列——因此它獲得自己專屬的範圍，而不是借用某個垂直切片角色的範圍。沒
  有新增任何凍結 port：gc 是 CLI 背後的內部維運事務，不是跨元件服務
  （`internal/app/ports.go` 不變）。新的指令輸出 schema 版本字串：
  `auspex.gc.v1`。
