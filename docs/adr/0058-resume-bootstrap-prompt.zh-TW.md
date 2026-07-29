# ADR-0058 — Resume bootstrap：由耐久狀態建構的 prompt，單一 builder 兩種 profile（§21.6 step 8；#9 收尾切片）

> 🌐 [English](0058-resume-bootstrap-prompt.md) | 繁體中文

Status: Accepted
Date: 2026-07-29
Owner: lead-executed（PROPOSAL — prompt 契約承諾，owner 於 merge 時簽核）
Tracking: issue #9（M7 Phase 2，step-8 resume）；ADD §20.10/§21.6 step 8/§21.8/§22.7、FR-151；建立於 ADR-013（App Server 為 managed 主路徑）與 ADR-0056（App Server 資料來源 + `managed_app_server` 語義）之上；向前餵給 #8（M11 managed shell/one-shot）與 #11（M13 managed-run 校準資料）

## 脈絡

Slice C 落地了 §21.6 的步驟 1–7：providerStopper 接縫讓 M10 暫停觸發器
接上 App Server 路徑（`turn/interrupt`、觀測 interrupted 終態、
checkpoint + wake job 持久化）。但今天 wake 觸發的只有狀態機：daemon
worker 認領 wake job、跑 `ValidateResume`、pause 紀錄 CAS 推進到
`Resumed` — **沒有任何東西重新啟動 provider**。`app.SessionResumer`
是一個沒有 production 實作的凍結 port；`ResumeThread` 零呼叫者。Step 8
——「reset 後 `thread/resume` and `turn/start` with bootstrap」——在 ADD
只有一行，bootstrap prompt 的內容就是開放的設計題。

基礎設施加諸的限制（皆已對 main 驗證）：

1. **原始 user prompt 從不落盤**（Constitution §7 隱私預設；prompt 一律
   redact）。resume 後的 agent 收到的東西只能*建構*，因為沒有東西可以
   重播。
2. **Progress Tree 是 canonical 耐久任務狀態**（Constitution §6.1），
   且每個節點帶第一方文字：`title`、`description`、`acceptance_json`、
   `next_action_json`（`0020_progress_nodes.sql`）。任務框架可以從它
   重建，不必碰 provider transcript。
3. **Checkpoint manifest 有為 resume 設計的欄位，但暫停路徑從不填。**
   `ProviderInfo{Name, SessionID, TurnID, InvocationMode}`、
   `NextActionInfo`、`ResumeInfo{StrategyOrder, PermissionMode}` 都在
   `statecheckpoint/manifest.go`，但暫停時的 `Create` 只填 progress
   摘要與 artifacts。Codex `thread_id` 唯一的耐久副本在
   `provider.session.started` 事件 payload 裡——那是 attribution 資料，
   不是第一級 locator。
4. **Wake job 依設計不帶 payload**（`0051_wake_jobs.sql`：
   `UNIQUE(pause_id, job_kind)` 是 exactly-once 錨點；一筆 job 只是
   pause 指標加 lease/重試簿記）。
5. **術語撞名：** D-07 的 `SessionBootstrapper` 是 hook 端從零資料建立
   session *紀錄*。本 ADR 的產物是 **resume bootstrap**——一段送出去的
   prompt。名稱分開、程式碼分開。

## 決定

1. **Bootstrap prompt 於 wake 時建構，從不重播、從不持久化。**
   唯一輸入全是第一方耐久狀態：pause 紀錄 + 其 `pauseContext`
   （`GitHeadBaseline`、`QuotaBaseline`、`PausedWorkPaths`）、最新
   State Checkpoint manifest、Progress Tree 資料列（title/description/
   acceptance/next action）、repository checkpoint 指紋、以及 wake 時
   重新驗證的 quota 觀測。任何輸入裡都不存在 provider transcript 文字，
   所以也不可能洩進 prompt。建構對輸入是決定性的——重試的 wake job
   重建出同一份 prompt，而非讀取儲存副本；這也意味 quota/git 數字是
   *wake 當下的新值*，不是暫停時的舊值。遙測只記 prompt 的位元組長度
   （ADR-051/0056 的 numbers-only 前例）。

2. **單一 builder、兩種 profile，由當下的 §20.10 策略選擇。**
   §20.10 的字面模板是共同骨架；兩種 profile 都以其權威條款收尾——
   *Git、測試、artifact 雜湊與 Progress Tree 的權威性高於對話記憶*。
   - **`re_entry`**（策略 1–2：同 session/thread——Codex
     `thread/resume` + `turn/start`、claude `--resume`）：provider
     保有自己的對話脈絡，所以 prompt 是差異簡報，刻意**不**重述任務：
     中斷事實 + 原因（runway 門檻）、暫停→喚醒的時間差、對照 checkpoint
     驗證 git 狀態與 artifact 雜湊的指示、active node + 記錄的 next
     action、以及剩餘 runway/預算數字。權威條款是對脈絡內過時信念的
     防線。
   - **`cold_bootstrap`**（策略 3：新 session——§21.8 exec 降級、
     FR-151 `new_session_progress_bootstrap`）：`re_entry` 的全部內容，
     加上從 Progress Tree 本身重建的任務框架——root/已完成/active 節點
     的 title、description 與 acceptance criteria。這是對空白脈絡唯一
     誠實的簡報方式，因為原始 prompt 在任何地方都不存在。

   兩種 profile 都是 provider 中立的文字。Provider adapter 只選擇
   *遞送方式*：`turn/start` 訊息（codex app-server）、帶 `--resume` 的
   `-p` 參數（claude，§22.7）、新 managed run 的初始 prompt（exec
   降級、#8 one-shot/shell）。builder 是共用基礎設施，不是 Codex 專屬
   程式碼——#8 重用的就是它。

3. **Locator 耐久化：於暫停時填入 manifest 既有的設計欄位。**
   暫停路徑的 checkpoint 增填 `ProviderInfo`（provider 名稱；claude
   session id / codex thread+turn id；invocation mode）、
   `NextActionInfo`（取自 active node 的 `next_action_json`）與
   `ResumeInfo`（依宣告 capability 推導的策略順序；permission mode）。
   無 migration——`manifest_json` 無 schema——也無新資料表。事件 payload
   裡的 `thread_id` 保留作 attribution，但不再是唯一耐久副本。

4. **執行器是第一個 production `SessionResumer`。** Codex App Server
   實作：`thread/resume(threadID)` → 帶上建構好的 prompt 的
   `turn/start`。Resume 後的 run 重新武裝 providerStopper——resume 後
   的 run 再次可中斷，暫停→恢復循環因此在構造上冪等。審批請求維持
   上呈、絕不自動核准（ADR-0056 §4）。`ValidateResume` 的 BLOCK 意味
   不啟動 thread（既有前例）。Wake job 維持不帶 payload：worker 只交
   `pause_id`；其餘一切從耐久狀態重新推導。

5. **失敗階梯 = §20.10 的階梯，落成可操作。** 同 thread resume 失敗
   （thread 被逐出、協定錯誤）→ capability 存在才 fork（codex 今日：
   無）→ `cold_bootstrap` 新 session → 手動。策略降級沿用既有 pause
   生命週期事件與 wake job 的 lease/backoff 機制；耗盡時 job 轉
   `dead`，pause 維持可手動恢復。實際採用的策略以 enum 記在 pause
   紀錄的 metadata——只有數字與 enum。

## 誠實範圍

- 本 ADR 定案的是 **prompt 契約**（輸入、profile、權威條款、隱私姿態）
  與 **locator 耐久化**。實作切片：(i) 暫停時 manifest 增填、(ii)
  builder + golden prompt fixtures、(iii) App Server `SessionResumer` +
  fake server E2E、(iv) #8 重用 builder。每片都引用本 ADR。
- claude `--resume` 執行器屬 #8/M11 範圍；本 ADR 只要求 builder 維持
  provider 中立。
- 跨 provider resume（codex 下寫的 checkpoint 在 claude 上恢復）明確
  排除——capability 抽象日後或許允許；此處不封死也不建造。
- builder 需要瘦版 `app.ProgressNode`（ID/Status/Kind）沒有暴露的節點
  文字；實作切片以加法讀取路徑（新方法或 read-model，依
  CONTRACT_FREEZE 加法規則）處理，不放寬凍結 struct。

## 後果

- `SessionResumer` 取得第一個實作且**簽章零更動**；port 本身不需修訂
  CONTRACT_FREEZE.md。
- Golden fixtures 釘住兩種 profile 的渲染輸出；決定性測試斷言相同輸入
  重建相等；fake App Server E2E 延伸到完整的中斷→喚醒→恢復迴圈。
- Managed resumed run 開始產出 M13（#11）duration/runway 校準所需的
  暫停→恢復遙測——這正是校準資料牆一直在等的資料。
- 「Resume bootstrap」與 D-07 的 `SessionBootstrapper` 記錄為不相關的
  產物，防止命名漂移。

## 曾考慮的替代方案

- **重播原始 prompt。** 不可能且駁回：它從不落盤，且重播會重跑已完成
  的工作——耐久任務真相是 Progress Tree，不是 prompt。
- **於暫停時持久化建構好的 bootstrap。** 駁回：quota 與 git 狀態在
  暫停與喚醒之間會變，儲存的 prompt 依定義過時；wake 時重建同樣具
  決定性，也免去再落一個文字 blob。
- **兩種 profile 共用一份 prompt。** 駁回：對活著的 thread 重述任務
  浪費脈絡並誘發重新規劃；空白 session 沒有敘述則無法行動。這個分割
  是 §8.8 降級階梯在 prompt 層的顯式化。
- **Wake job 帶 payload。** 駁回：把耐久狀態複製進佇列、會過時、又讓
  exactly-once 錨點複雜化，零資訊增益。
- **專用 thread-locator 欄位/資料表。** 目前駁回：manifest 既有設計
  欄位已足夠且無需 migration；只有當「以 thread 查詢」成為真實查詢
  模式時再議。
