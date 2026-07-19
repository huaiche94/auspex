# ADR-0054 — 對 CHECKPOINT_AND_RUN 採取行動：自動 pre-turn checkpoint，config 可關，fail-open（issue #116）

> 🌐 [English](0054-auto-checkpoint-and-run.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：已接受（Accepted）
日期：2026-07-19
負責人：owner 拍板（auto vs. advisory 即 issue #116 上懸置的 `adr-needed` 問題）；由 lead 執行
追蹤：issue #116（M4 Progress Tree / State Checkpointing）；README 準確性稽核後續

## 背景

`CHECKPOINT_AND_RUN` 自 ADR-0043 起就是真實、有持久化的 policy **決策**：
decider 會在 critical 風險帶、高 blast radius、context 天花板與成本預算
天花板時發出它（`internal/policy/decide.go`、`context.go`、
`costbudget.go`），持久化到 `policy_decisions`（migration 0043）並渲染在
預估卡上。但沒有任何程式碼**對它採取行動**：pre-turn hook 只分支
`BLOCK`、managed runner 也只 switch `BLOCK`，`CheckpointCreate` 只能透過
操作者手動的 `auspex checkpoint create` CLI 觸達。ADR-0043 與
DECISION_LOG D-08 把這個動作定調為*建議*（"CHECKPOINT_AND_RUN 建議"），
`internal/orchestrator/decision.go` 的doc comment 更把 advisory 立場寫成
契約：操作者「預期已在上游先跑過 `checkpoint create`」。

這在產品主張（「在該 turn 之前固化狀態」）與實際接線行為之間留下真實落差
——正是提報 #116 的 README 稽核所記錄的 README-vs-shipped gap。底下的設計
問題是：這個決策應該**自動觸發** checkpoint，還是維持 advisory、讓自動
checkpoint 只存在於 PreCompact 路徑（#114）？

## 決定

**自動 checkpoint，config 可關，fail-open。**（issue #116 上的 owner
決定；本 ADR 就此動作取代 ADR-0043 §2 與 DECISION_LOG D-08 的
「僅建議」定調——其餘七個 policy action 的語義不變。）

1. **兩個決策面都自動 pre-turn checkpoint。** 當 policy 決策是
   `CHECKPOINT_AND_RUN` 且 config gate 開啟時，Auspex 在該 turn 繼續之前
   建立 checkpoint 對——先 state、後 repository，重用 `CheckpointCreate`
   凍結的順序：
   - `HandleUserPromptSubmit`（native hook）：在寫出 `allow` 回應之前
     checkpoint；結果以一行 `additionalContext` 呈現給 coding agent。
   - `internal/managed.Runner.Run`（managed one-shot）：在 provider
     程序 spawn 之前 checkpoint；結果是一行 HumanLog。
   編排邏輯集中在一個新元件
   （`internal/orchestrator/autocheckpoint.go` 的 `AutoCheckpointer`），
   兩個呼叫點透過既有的 `HookDeps` bundle 共用。

2. **checkpoint ID 走既有的 decision-allow 機制，不另闢通道。** 建立成功
   後，auto-checkpointer 驅動 `DecisionAllowCmd` 的兩個既有流程——issue
   （綁定 `DecisionAllowRequest.RepositoryCheckpointID`）接著立即
   consume——即操作者手動時會下的那兩個呼叫。發出即消耗的 authorization
   row（migration 0044）就是持久化的稽核紀錄：這個 turn 在此決策下、綁著
   此 repository checkpoint，恰好進行了一次。之所以在同一次呼叫內就
   consume，是因為 turn 在同一次呼叫內就繼續了——留著未消耗會捏造一個
   永遠不會到來的 resubmission。

3. **Fail-open，且要喊出來。** 任何失敗——task/worktree 目標解析不到、
   state 或 repository checkpoint 出錯、authorization 紀錄失敗——都放行
   該 turn 並記下警告（result 欄位、additionalContext/HumanLog 行）。
   安全網自身故障，永遠不該成為封鎖它所要保護的 session 的理由。這是
   刻意與 ADD §17.5/§20.15 對**操作者主動要求**的 checkpoint（`auspex
   checkpoint create`、`CompleteNode` 的強制 checkpoint）採 fail-closed
   的立場相反：那裡使用者明確要求了，靜默跳過等於說謊；這裡沒有人要求，
   Auspex 是機會性地加保護，誠實的降級是警告。cold start 是最常見的
   跳過：還沒有 task 的 session 沒有 state checkpoint 可建，捏造一個
   違反「unknown is not zero」。

4. **Config gate：`state_checkpointing.on_checkpoint_and_run`，預設
   `true`。** 命名對齊該 section 既有的 `on_<trigger>` 鍵
   （`on_node_completion`、`on_architecture_decision`、`on_pre_compact`，
   ADD §26.4）。`false` 恢復明確的 advisory 行為：決策照樣渲染在卡片/
   statusline 上，由操作者手動 checkpoint。這是 M1 分層 config 鏈
   （`internal/config`）的**第一個** production 消費者：defaults < 全域
   使用者 `config.yaml` < `.auspex/config.yaml` < `.auspex/local.yaml`，
   於組裝時 fail-open 載入（格式壞掉降級為預設值；回報 config 問題是
   `doctor` 的職責，不是每次 hook）。§26.1 的 environment/CLI 層對此
   section 尚無欄位對應，與 config 套件自身記錄的現狀一致。

5. **延遲預算：熱路徑永遠不付費。** pre-turn hook 在每個 prompt 前同步
   執行，因此 auto-checkpoint 只在 `CHECKPOINT_AND_RUN` 分支內被呼叫——
   一般 `RUN`/`WARN` prompt 零額外執行。在罕見的高風險 prompt 上，多出的
   延遲就是 checkpoint 對本身（一次 git snapshot 加兩筆 SQLite 寫入）——
   刻意接受：`CHECKPOINT_AND_RUN` 正是在投影 blast radius 或資源天花板
   值得花幾秒 pre-turn 固化時才觸發，而另一個選項（僅 advisory）是一個
   沒接線的產品主張。

6. **與 PreCompact（#114）互補，不互相取代。** 兩個觸發器、兩種語義：
   pre-compact 在 context 邊界**無條件** checkpoint（即將遺失對話狀態）；
   本路徑在 per-turn 風險決策時 checkpoint（即將執行高風險動作）。彼此
   不可替代；本變更不動 #114 的範圍與檔案。

## 後果

- README 的「在該 turn 之前固化狀態（`CHECKPOINT_AND_RUN`）」主張成為
  已接線的事實（README 最終 reconcile 依 repo 慣例另開 docs PR）。
- `internal/orchestrator/decision.go` 的 advisory doc comment 已更新：
  上游 checkpoint 步驟現在通常是自動的，只有 gate 關閉時才是手動。
- authorizations 表會為 auto-checkpoint 的 turn 增加發出即消耗的 row——
  這正是要的稽核軌跡。TTL/replay 語義不變（立即消耗，沒有可 replay 的
  殘留）。
- gate 關閉（`on_checkpoint_and_run: false`）或最小組裝（nil
  `AutoCheckpointer`）與 #116 之前的表面逐位元相同——有測試證明。
- managed runner 傳入自己明確的 `WorktreeID`/`TaskID` 目標；hook 路徑則
  從 session 解析 task（與評估管線相同的啟發式，經凍結 Resolve port 的
  窄視圖）與 worktree（`provider_sessions.worktree_id`）。

## 曾考慮的替代方案

- **維持 advisory；自動 checkpoint 只放 PreCompact（#114）。** Owner
  否決：決策的名字承諾了動作、README 承諾了 pre-turn 固化，且兩個觸發器
  防護的是不同損失（高風險 turn vs. context 遺失）——只有 PreCompact 會
  讓高風險 turn 的窗口開著。
- **預設關閉（opt-in）。** 否決：決策本來就只在罕見的高風險帶觸發、成本
  是恰好在那些 turn 上的幾秒鐘、fail-open 已消除可用性風險；預設關閉
  等於對所有從不改 config 的使用者維持沒接線的產品主張。
- **checkpoint 失敗時 fail closed。** 就此觸發器否決：這會讓 Auspex 端
  的 storage/git 故障封鎖所有高風險帶 prompt——正是 ADD §17.5 要避免的
  「安全網拖垮 session」故障模式。操作者主動要求的 checkpoint 維持其
  fail-closed 契約。
- **為 checkpoint 綁定另建持久化通道。** 否決：
  `DecisionAllowRequest.RepositoryCheckpointID` → `Authorization` 正是
  為這個綁定而建；另加平行欄位/表格是重複一個凍結契約的職責。
