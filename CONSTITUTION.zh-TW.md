# Auspex Repository 憲章（Constitution）

> 🌐 [English](CONSTITUTION.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 值 |
|---|---|
| 狀態（Status） | **規範性（Normative）。本 repository 至高的流程權威（supreme process authority）。** |
| 範疇（Scope） | Auspex 這個「專案」如何被建置與治理——而不是 Auspex 這個「產品」在執行期（runtime）做了什麼（那是 `docs/design/Auspex_ADD.md` 的範疇）。 |
| 位階（Precedence） | 本文件的權威性高於 `README.md` 與 `AGENTS.md`。若兩者中任一份與本文件矛盾，以本 Constitution 為準，另一份文件視為錯誤，必須修正。 |
| 修訂（Amendment） | 只有 `contract-integrator` 這個 role／架構負責人（architecture lead）可以修訂本檔案，且必須依循下方 §3 定義的相同 ADR 紀律。 |

這不是一份 README，也不是給貢獻者速查的小抄。它是一套規則，讓在這個 repository 上多代理（multi-agent）、多 session、跨多日進行的工作能夠收斂（converge）而非發散（diverge）。無論你是人類還是 agent，在動任何一個檔案之前，請先讀完本文件。

---

## 1. 單一事實來源（Single Source of Truth）

Auspex 針對每一個主題都只有一份權威文件。永遠不要把任何其他文件、先前的草稿、PR 描述、留言，或對話內容，當作比下方指名文件更具權威性的來源。

| 主題 | 唯一事實來源 |
|---|---|
| 產品架構、領域模型（domain model）、需求、路線圖 | `docs/design/Auspex_ADD.md` |
| 修訂 ADD 的架構決策 | `docs/adr/` 下已被接受的 ADR |
| 流程、治理、不變量（invariants）、誰能修改什麼 | **本檔案** |
| 目前執行波次（execution phase）的機制（拓樸、合併順序、所有權地圖） | `docs/design/Auspex_Parallel_Execution_Plan.md`（或後續波次的接替文件） |
| 特定 role 的任務、專屬路徑、交付項目、測試 | `agents/` 下對應的檔案 |
| 貢獻者／agent 速查表 | `AGENTS.md` |
| 專案進入點／導覽 | `README.md` |
| Repository markdown 稽核軌跡 | `docs/repository_inventory.md` |

若兩份文件互相矛盾，以此列表中位階較高者為準，位階較低者視為需要修正的錯誤——而不是每次遇到時臨場判斷的裁量問題。

## 2. 文件優先順序（衝突解決）

當程式碼、issue、PR、prompt，或 agent 自身的推理與治理文件（governing document）衝突時，依下列順序解決：

```text
1. This Constitution                        (process/governance/invariants)
2. docs/design/Auspex_ADD.md + accepted ADRs          (architecture)
3. Current execution plan                    (this phase's mechanics)
4. agents/*.md                               (role-scoped operational detail)
5. AGENTS.md / README.md                     (summaries — must not contradict 1-4)
6. Everything else (comments, chat, memory)  (never authoritative)
```

任何 agent——無論人類或 AI——都不得因為某個較低層級的任務用另一種方式實作比較容易，就更動較高層級的決策。如果某份較低層級的文件發現自己需要一些較高層級所禁止的東西，這是提出 ADR（§3）的信號，而不是悄悄地產生分歧。

## 3. ADR 規則

在變更以下項目之前，**必須**先有一份 Architecture Decision Record（架構決策紀錄）：

- 正式環境（production）的執行期語言；
- daemon 的傳輸方式（transport）；
- 以不向後相容（backward-incompatible）的方式變更 SQLite schema；
- provider 整合合約（integration contract）；
- checkpoint 格式或還原（restore）安全模型；
- State Checkpointing 的不變量；
- Graceful Pause／Auto-Resume 的語意；
- 隱私預設值；
- 公開的 CLI／API／協定相容性；
- OSS 授權條款；
- 預測輸出從分數（score）變為機率（probability）；
- 本 Constitution 本身。

規則：

1. ADR 存放於 `docs/adr/NNNN-title.md`，依序編號。
2. 只有 `contract-integrator` role（架構負責人）可以接受（accept）一份 ADR。任何 role 都可以提出（propose）一份 ADR。
3. 已被接受的 ADR 是不可變的歷史紀錄。要變更某項決策，須撰寫一份新的 ADR 來取代（supersede）舊的——絕不可就地修改已接受 ADR 的決策內容。
4. 一份 ADR 必須說明：脈絡（context）、決策內容，以及它對 `docs/design/Auspex_ADD.md` 或本 Constitution 帶來了什麼變更（如果有的話）。如果兩份文件都不需要變更，那這個決策本來就不需要 ADR。
5. `docs/design/Auspex_ADD.md` 只能由 `contract-integrator` 編輯，且僅限於出現真正的矛盾、必須修改時——並且對應的 ADR 必須在同一次變更中一併落地。

## 4. 誰能修改什麼

1. 每個 role 都擁有一組互不重疊（disjoint）的路徑，宣告於其 `agents/*.md` 檔案中，並彙整於目前執行計畫的共享檔案政策（shared-file policy）章節。
2. 一個 role 只能修改其自身宣告路徑之內的檔案。
3. 共享、跨領域（cross-cutting）的檔案——`docs/design/Auspex_ADD.md`、本 `CONSTITUTION.md`、`AGENTS.md`、`internal/domain/**`、`internal/app/ports.go`、`pkg/protocol/v1/**`、`docs/adr/**`——專屬於 `contract-integrator` 所有。任何其他 role 都不得編輯它們，即使只是「修個錯字」也不行。
4. 如果某個 role 需要變更一個它不擁有的檔案，它會透過其進度產物（`docs/implementation/vertical-slice/<role>.md`，或更晚波次的等效文件）提出變更請求——它不會自行編輯，也不會閒置等待；而是先以有文件紀錄的假設（documented assumption）繞過這個缺口，直到該檔案的擁有者回應為止。
5. 任何 role 都不得擴張自己的路徑所有權。只有 `contract-integrator` 可以重新指派路徑所有權，且必須在同一次變更中同時更新執行計畫與受影響的 `agents/*.md` 檔案。
6. `go.mod` 與 `go.sum` 僅由 `foundation` 擁有。
7. Migration 檔案的編號範圍依 role 固定分配（見目前執行計畫 §7）；一個 role 絕不會在其指派範圍之外撰寫 migration。

## 5. 何時可以新增一個 Provider

Auspex 透過 `docs/design/Auspex_ADD.md` §6.7 與 §8 中以能力（capability）為基礎的模型來整合 provider（Codex、Claude Code，以及未來其他 provider）。只有在**以下全部條件**都成立時，才可以新增一個新的 provider 整合：

1. 它實作了 ADD §9.10 中定義的狹窄 provider 介面（`ProviderDetector`、`HookNormalizer`、`ManagedRunner` 等）——不需要把既有介面擴張成一個「上帝介面（God interface）」，也不需要新增一個只有這一個 provider 會實作、且沒有文件說明理由的介面。
2. 它的 `ProviderCapabilities`（ADD §8.6）在執行期會被明確偵測並宣告——絕不假設，絕不硬編碼「這個 provider 永遠支援 X」。
3. 缺少的能力會明確降級（degrade explicitly）（ADD 原則：「Capabilities are explicit」，§1.6 第 8 條），而不是靜默地假裝可以運作。
4. 在新 provider 的轉接器（adapter）合併之前，必須存在以 fixture 為基礎的合約測試（contract test）——沒有任何 adapter 可以僅靠一個真實、未錄製的帳號作為唯一測試就合併。
5. 它不需要變更 `internal/domain/**`——如果需要，這本身就是需要先提出 ADR 的信號（§3）。
6. 它不會 fork 或爬取（scrape）某個 provider 原生的互動式 TUI（ADD 中列為非目標）。
7. 如果新增它會改變 provider 整合合約本身的形貌（而不只是在既有合約之下新增一個 adapter），就必須在開始實作**之前**先有 ADR（§3），而不是事後補上。

## 6. Progress Tree 不變量

以下是產品層級的不變量（`docs/design/Auspex_ADD.md` §1.3、§1.6、§6.4、FR-100–FR-110），在實作上不可協商，不是風格上的偏好：

1. **Progress Tree 是具規範性、持久性的任務狀態（canonical durable task state）。** 對話上下文、聊天記憶，以及 agent 自己聲稱「已完成」，永遠都不是事實來源。
2. **一個節點在沒有持久性、經驗證器檢核的產物證據之前，不得標記為 `completed`**——必須是真實的檔案、資料庫紀錄、checksum，或 Git 快照。單靠文字說明是不夠的（ADD 原則 5：「Completed means evidenced」）。
3. **每一次節點完成都必須在同一個原子操作中建立一個 State Checkpoint。** 一個已完成、卻沒有對應 checkpoint 的節點是一個 bug，不是可接受的缺口。
4. **節點狀態值是固定的列舉（enum）**（`pending`、`ready`、`in_progress`、`checkpointing`、`paused`、`completed`、`failed`、`skipped`、`blocked`）——沒有任何 role 可以自創臨場（ad hoc）的狀態。
5. **狀態寫入必須是原子性的、冪等的（idempotent），且可從當機中復原。** 寫到一半當機，絕不能留下一個看起來已完成、卻沒有經過驗證證據支撐的節點。
6. **帶有衝突證據的重複完成（duplicate completion）會被拒絕**，而不是被靜默合併或覆寫。
7. 這套同樣的紀律，依類比原則，也適用於各個 role 在建置 Auspex 本身時，於 `docs/implementation/vertical-slice/<role>.md` 下維護的後設層級（meta-level）進度產物：只存在於對話中的進度，在那裡同樣不算數（目前執行計畫 §9）。

## 7. 每個 agent 都必須遵守的規則

以下將 `AGENTS.md` 的「Required principles（必要原則）」推廣為具憲章位階（constitutional status）的規則——如果 `AGENTS.md` 與本節內容出現分歧，請修正 `AGENTS.md`：

1. Go 是唯一的正式環境執行期語言；TypeScript 僅侷限於 VS Code 延伸模組；Python 僅供離線研究使用，Go 執行期絕不依賴它。
2. 原始 prompt 與工具輸出預設不會被持久化。
3. Provider 的能力落差必須明確揭露，絕不靜默假設它不存在。
4. 未經文件化的逐字稿（transcripts）絕不在穩定路徑上被解析。
5. Git 與 provider 的程序執行一律使用 argv 呼叫，絕不使用 shell 指令字串。
6. Repository checkpoint 必須是原子性的，且絕不靜默地提交（commit）目前使用中的分支。
7. 未經校準的風險分數絕不會被標示為機率。
8. Graceful Pause 的完整保證僅適用於受管執行（managed execution）；native-hook 的行為會被明確降級，絕不靜默地宣稱與之等效。
9. Auto-resume 是選擇性加入（opt-in）、限定於工作區（workspace-scoped）、不會提升權限（permission-non-escalating）、可取消、有稽核紀錄，並且在執行前會重新驗證（ADD §6.10）。
10. 一個 agent 一次只實作一個里程碑／波次，不會提前加入之後某個里程碑才需要、而目前里程碑不需要的抽象層。
11. 一個 agent 若未具備 §6 所要求的持久性證據，不會宣告某項任務已完成。

## 8. 與 `docs/design/Auspex_ADD.md` 的關係

本 Constitution 不與 `docs/design/Auspex_ADD.md` 爭奪權威——
兩者治理不同的領域，彼此互不隸屬：

- `docs/design/Auspex_ADD.md` 對於「Auspex 是什麼、在執行期如何運作」（架構、領域模型、需求）擁有至高權威。
- 本 Constitution 對於「Auspex repository 及其貢獻者／agent 在建置它的過程中該如何行事」（流程、所有權、不變量的落實、治理）擁有至高權威。

當一項 Progress Tree 不變量（§6）同時既是執行期行為、又是開發流程規則時，本 Constitution 會說明它，因為它約束了 agent 必須如何建置該功能；而 `docs/design/Auspex_ADD.md`
則仍然是該功能本身具規範性的技術規格。
