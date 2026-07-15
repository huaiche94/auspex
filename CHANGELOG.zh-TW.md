# Changelog（變更紀錄）

> 🌐 [English](CHANGELOG.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

Auspex 所有重大變更都記錄在此檔案中。格式遵循
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/)；一旦開始正式發布版本，版本號將遵循 [SemVer](https://semver.org/)。

## [Unreleased]（尚未發布）

### Fixed（修正）

- **Migration runner 會套用回填（backfilled，號碼有缺口／gap-numbered）的 migration**
  （[#22](https://github.com/huaiche94/auspex/issues/22)）：`Migrate` 現在會以「相對於
  `schema_migrations` 的集合差（set difference）」來計算待執行的工作，而不是「所有大於
  `MAX(version)` 的項目」。在每個領域（domain）各自獨立的 migration 範圍編號規則下
  （CONTRACT_FREEZE.md），一個 migration 的編號可能比早已套用的範圍更小——0045
  （predictions context projection）在 0050–0052 已經上線之後才落地，於是在任何已經到達版本
  52 的資料庫上被永久靜默跳過，並在 hook 「fail-open」的默默運作下，不易察覺地破壞了每一次
  `EvaluateTurn` 的寫入。`ErrSchemaNewerThanBinary` 這個 fail-closed 檢查沒有變動（仍然是以已套用的最大版本號為準）。受 0045 缺口影響的資料庫會在下次啟動時自我修復。

### Changed（變更）

- **產品更名 Preflight → Auspex**（ADR-045，取代 ADR-001）：Go module 名稱
  `github.com/huaiche94/auspex`、二進位檔 `auspex`、schema 版本前綴
  `auspex.*.v1`、使用者資料目錄 `auspex/`。這是發布前（pre-release）的更名，沒有 migration；舊的本機 `preflight/`
  資料目錄原地保留、不再使用。GitHub 會將舊 repository 網址重新導向。

### Documentation（文件）

- **Hook 子指令大小寫正式定為 kebab-case**（ADR-050，
  [#61](https://github.com/huaiche94/auspex/issues/61)）：解決 REC-03——ADD 附錄
  E.1/E.3 的 hook 安裝範本與 §24.3 範例原本寫成 `auspex hook claude UserPromptSubmit`
  （PascalCase），而已出貨的 CLI、`agents/runtime.md` 與 DAG 驗證指令用的是 kebab-case
  （`user-prompt-submit`）。ADR-050 正式採 kebab-case（符合 Cobra 慣例、變動最小），並把
  ADD 的 argv 一併更新對齊；provider 自身的 `hook_event_name` payload 欄位與 settings.json
  hook-matcher key 維持 PascalCase（不同命名空間）。無程式碼變更——CLI 早已以 kebab-case
  出貨，`internal/cli/doc.go` 的 REC-03 註記現在指向此解決用 ADR。不涉及任何凍結契約。
- **文件重新整理＋繁體中文翻譯**
  （ADR-049，D-17）：三份設計文件從 repository 根目錄搬到 `docs/design/`
  （仍在維護中的文件已更新引用路徑；歷史紀錄則刻意保持不變）、`README.md`
  針對首次瀏覽者重寫、每個資料夾都新增了一份 `README.md`
  介紹，並且每一份文件都新增了一份非規範性的
  `<name>.zh-TW.md` 繁體中文對照文件。英文仍是唯一的規範性文本。

### Added（新增）

- **成本預測校準——逐 cohort 殘差（Phase 2）**
  （[#72](https://github.com/huaiche94/auspex/issues/72)）：
  `research/calibration/report.py` 現在會把 Phase 1 的成本 join 依 #20 的
  cohort 三元組分層，並對每個達到 ADD §15.2 門檻（≥ 8 個**已 join** 的 turn）
  的 cohort，擬合出「forecast 的 high bound 相對於真實成本低估了幾倍」的經驗
  倍率（`actual/high` 的中位數與 P90）；門檻以下或有未標記軸的 cohort 只回報、
  絕不擬合（立基紀律）。在擁有者的實地資料上,兩個已標記 cohort 都過門檻:
  `claude/fable/xhigh` 中位低估約 7 倍（P90 約 57 倍）、`claude/opus/xhigh`
  約 8.5 倍（P90 約 39 倍）——尾端遠比中位嚴重,正是 #66 針對的 cache-read
  盲點。Go forecast 不受影響;這些倍率是未來階段（#66 的 cache-aware 成本模型）
  會取用的輸入。僅 research 層——不涉及契約,不需 migration。
- **成本預測校準軌道（Phase 1）**
  （[#72](https://github.com/huaiche94/auspex/issues/72)）：校準匯出現在
  每列都帶上預測成本區間（`cost_low_usd` / `cost_high_usd` /
  `cost_model_family`），由與 forecast card 相同的 `internal/pricing`
  價目表依 token 分位數計價——因此校準衡量的正是使用者看到的那個成本
  數字，不會有第二份會漂移的價目表（`internal/retention/export.go`，
  純新增欄位，無需 migration）。`research/calibration/report.py` 新增
  **成本區間涵蓋率**區段，把該預測區間與 `observations.py` 從 session
  累計的 `total_cost_usd` 序列推導出的每回合成本差值做 join。這正是
  #72 指出的 hook 模式突破口：不同於 `total_tokens` 實際值（僅
  managed run 有——statusline 不帶每回合 token），每回合**成本**差值單
  靠原生 hook 遙測即可推導，所以原生 hook 回合終於能在不使用
  `auspex run` 的情況下把預測與實際 join 起來（在擁有者第一份實地資料
  上為 156/157 筆）。報告會分開統計實際值落在區間之下（成本高估）與之上
  （成本低估）；第一次實際執行顯示 91% 落在區間之上——正是 token 冷啟動
  （#42）與未計入快取的計價（#66）所預測的系統性低估，如今以真實資料
  量化出來。每回合成本的**實際值**仍屬 research 層的歸因推導
  （`observations.py`），絕不由 capture-before-model 的 Go bridge 計算。
  純新增匯出欄位 → 向後相容 → 不需 ADR（Constitution §3）。Phase 2
  （依已標記 cohort 擬合成本殘差——`claude/opus/xhigh` 與
  `claude/fable/xhigh` 兩個 cohort 已達 §15.2 門檻）仍以 #11 為前置條件。
- **每個 turn 的預估時間（duration forecast，Phase 1）**（#62）：scope
  估計器現在會填入原本預留的 `ScopeEstimate.DurationP50/P90` 欄位——一個
  由分類後的 scope 推導出的 cold-start wall-clock 估計
  （`internal/predictor/scope/duration.go`），因此它會隨 prompt 變動，
  而非固定常數。每筆 prediction 持久化（migration 0047，
  `predictions.duration_p50/p90`，單位 nanosecond），並連同新增的
  `actual_duration_ms` 欄位（migration 0062，從該 turn 的
  `provider.usage.observed` 事件 `total_duration_ms` join 而來）一併寫入
  `calibration_samples`，讓「預測 vs 實際」時長配對得以累積以供校準（#11）
  並在封存後仍保留——目前於 managed-run（`auspex run`）路徑上可對應到
  turn，session 累計式的 statusline usage 則暫記為 NULL（誠實的缺口），
  待 turn 標記覆蓋擴大後自動補上（#1）。並在 forecast card／
  UserPromptSubmit 的 `additionalContext` 上以 `time:` 一行呈現，
  `auspex evaluate --json` 則多一個 `duration` 區塊，calibration export
  亦輸出（`duration_p50_ns`／`actual_duration_ms`）。標示為未校準
  （Constitution §7），且刻意
  **不**顯示於 statusline，直到它被校準（#11）或以其他方式變得會隨
  prompt 反應（#42）為止——這是 D-15／#42 的教訓：固定的 cold-start
  數字在 statusline 上沒有訊號價值。Phase 2（以 Claude Code 已回報的
  `total_duration_ms` 遙測進行校準）仍 gate 在 #11。
- **終端 hook 事件的 turn 關聯（turn correlation）**（PR
  [#54](https://github.com/huaiche94/auspex/pull/54)）：Stop／StopFailure
  事件現在會回頭關聯到該 turn 的評估紀錄，讓「預測 vs 實際」的結果配對
  得以累積，供 M13 校準管線使用
  （[#11](https://github.com/huaiche94/auspex/issues/11)）。
- **背景 daemon（常駐服務）＋具授權的 loopback HTTP API**（M6，D-16，
  [#7](https://github.com/huaiche94/auspex/issues/7)）：`auspex daemon
  run` 前景（foreground）程序，並可用 `auspex daemon install` 產生
  macOS LaunchAgent plist；bearer token 存於 `<data>/daemon.token`（權限 0600，
  每次啟動都會重新產生）；SSE 事件串流位於 `/v1/events/stream`。到期的 wake job 現在可以無人值守地執行。
- **VS Code companion 延伸模組 MVP**
  （[#10](https://github.com/huaiche94/auspex/issues/10)，PR
  [#53](https://github.com/huaiche94/auspex/pull/53)）：狀態列（status-bar）
  顯示 daemon 存活狀態、活動列（activity-bar）視圖（狀態／進度／checkpoint／
  暫停／wake job）、排程恢復（scheduled resume）的內嵌取消按鈕；在
  marketplace 發布者完成註冊之前
  （[#18](https://github.com/huaiche94/auspex/issues/18)），僅能從原始碼或本機 VSIX 使用。
- **逐一 prompt 的預測（forecast）呈現介面**
  （[#14](https://github.com/huaiche94/auspex/issues/14)），statusline 迭代至 v3
  （[#31](https://github.com/huaiche94/auspex/issues/31)，
  [#41](https://github.com/huaiche94/auspex/issues/41)）：以實測優先（measured-first）的
  context 區段、週視窗（weekly-window）區段、單一政策徽章（policy badge）；靜態的
  cold-start token 區段先行撤下，直到預測結果真正會隨 prompt 而變動為止
  （[#42](https://github.com/huaiche94/auspex/issues/42)）。
- **Native-hook session bootstrap**
  （[#17](https://github.com/huaiche94/auspex/issues/17)）：hook 會冪等地
  （idempotently）註冊 repository／worktree／session 資料列，讓評估
  pipeline 在真實 provider session 中零設定即可運作。
- **事件關聯（correlation）＋ `auspex progress complete`**（D-01，
  [#1](https://github.com/huaiche94/auspex/issues/1)）：provider 事件現在會與
  Progress Tree 節點建立關聯；完成狀態仍然維持明確、且需要證據把關（evidence-gated）。
- **真正的 repository-checkpoint 還原**
  （[#6](https://github.com/huaiche94/auspex/issues/6)），了結了
  checkpoint-b08 dry-run 的延後事項。
- **`auspex gc`——分層遙測資料保留**（ADR-046，
  [#19](https://github.com/huaiche94/auspex/issues/19)）：早於 90 天熱窗口
  （`--retention-days`）的原始事件／特徵／預測／決策／forecast／已消耗
  authorization，以及終態任務（terminal tasks）被取代的 checkpoint，都會被彙整進
  `usage_rollups_daily` ＋ `calibration_samples`（保留預測值與實際值的配對，供
  #11 校準使用），以完整欄位保真度歸檔為 gzip JSONL 存放於資料目錄下，經讀回驗證後才會刪除——採
  fail-closed 原則，絕不半途而廢。每個任務最新的 state／repository checkpoint 永遠會被保留。
  `--dry-run` 完全無副作用；`--vacuum` 可選擇性地執行完整 `VACUUM`（資料庫執行於
  `auto_vacuum=NONE`，因此單純刪除只會釋放頁面供重複使用）。migration 範圍 0060–0069
  現已指派給 retention/gc 使用。
- 完整的 vertical slice（垂直切片，85/85 個 DAG 節點，從 Bootstrap 到 Stage-5
  最終整合閘門）：凍結的 domain／port／event 合約、依角色劃分 migration 範圍的
  SQLite 儲存層、Claude Code provider 解析器＋hook 處理器＋冪等的遙測持久化、
  具證據把關（evidence-gated）原子性 CompleteNode 的 Progress Tree、具啟動時對帳
  （reconciliation）的 State Checkpointing、Repository Checkpoint（建立／驗證／
  patch／未追蹤檔案歸檔並含機密遮蔽、還原 dry-run）、predictor pipeline
  （prompt 特徵 → 任務分類器 → 範疇估算器 → token／配額預測器 → 風險合成器 →
  runway 分數）、涵蓋八種凍結動作的 cold-start 政策引擎、具重播拒絕
  （replay rejection）的一次性授權（one-time authorization）、優雅暫停狀態機
  ＋具租約（lease）復原的持久性排程器（scheduler）、完整串接的 `auspex` CLI
  （`evaluate`、`decision`、`checkpoint`、`pause`／`resume`／`scheduler`、
  `status`、`doctor`、`hook claude ...`）、跨平台 CI，以及 qa 安全性／整合測試套件
  （E2E demo、外洩掃描器、路徑穿越測試 fixture、race 測試）。

### Known gaps（已知缺口）

- 所有預測目前都是 cold-start 規則——未經校準的分數，不是機率。token
  預測目前幾乎不隨 prompt 而變動
  （[#42](https://github.com/huaiche94/auspex/issues/42)）；根據真實遙測資料進行校準是
  M13 里程碑
  （[#11](https://github.com/huaiche94/auspex/issues/11)）。
- 目前唯一的 provider 轉接器是 Claude Code；Codex（M7/M8）追蹤於
  [#9](https://github.com/huaiche94/auspex/issues/9)。受管的 one-shot／
  shell 模式（M11）追蹤於
  [#8](https://github.com/huaiche94/auspex/issues/8)。
- Prompt 特徵萃取在阻塞式（blocking）hook 路徑上會執行多次 O(n)
  掃描（[#51](https://github.com/huaiche94/auspex/issues/51)），且其 payload
  schema 缺少萃取版本標籤
  （[#50](https://github.com/huaiche94/auspex/issues/50)）。
