# Auspex 儲存庫 Markdown 檔案清查

> 🌐 [English](repository_inventory.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 內容 |
|---|---|
| 範圍 | 儲存庫中每一個 `*.md` 檔案（共找到 13 個檔案，全部已開啟檢視） |
| 目的 | 第 0 階段（Phase 0）儲存庫正規化（normalization）——僅做分類 |
| 狀態 | **遷移（migration）已於 2026-07-12 執行；此後又記錄了多次後續重組。** §2 的表格描述遷移前的原始狀態（保留作為稽核軌跡）；之後每一次異動都會附加一份執行紀錄（execution log）——見 §6（正規化，normalization）、§7（角色重整 + Constitution）、§8（文件重組 + 雙語政策，ADR-049）。§8 的最終狀態表為目前現況。 |
| 產生時間 | 2026-07-12（最後更新於 2026-07-14，§8） |

---

## 1. 分類圖例

| 狀態 | 意義 |
|---|---|
| **authoritative（權威版）** | 該主題的治理性真實來源（source of truth）；發生衝突時以此為準 |
| **supporting（輔助）** | 補充細節、執行機制或鷹架，從屬於某份權威文件 |
| **obsolete（過時）** | 已被取代、與目前方向牴觸，或描述先前的產品／計畫 |
| **duplicate（重複）** | 與他處已存在的正規內容逐位元組相同或幾乎相同 |
| **archive（封存）** | 處置方式（非內容類型）：保留作為歷史紀錄，不在使用中路徑內 |

---

## 2. 清冊表

| 檔名 | 用途 | 負責人 | 目前狀態 | 是否應保留？ | 替代方案 | 參照 |
|---|---|---|---|---|---|---|
| `Auspex_ADD.md` | Auspex 完整的架構設計文件（Architecture Design Document）+ 實作規格（願景、需求、C4、領域模型、schema、路線圖 M0–M15、ADR）。文件自稱為「source of truth（真實來源）」。 | A00／架構負責人（依 vertical-slice plan §7 的專屬路徑清單） | **authoritative（權威版）** | 是——維持原樣 | — | 參照 `AGENTS.md`（缺失）、`README.md`（缺失）、`docs/adr/**`（缺失）、完整的 `docs/**`、`LICENSE`、`NOTICE`、`SECURITY.md`、`CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`、`GOVERNANCE.md`、`CHANGELOG.md`（全部缺失） |
| `Auspex_Parallel_Execution_Plan.md` | 垂直切片（vertical-slice）執行計畫：9 個 agent（A00–A08）的拓撲結構、負責範圍地圖、遷移編號範圍、合併順序、agent packets 附錄。 | A00／架構負責人 | **supporting（輔助）**（從屬於 ADD；屬於執行機制，非架構本身） | 是——維持原樣 | — | 參照 `Auspex_ADD.md`、`AGENTS.md`（缺失）、`docs/implementation/vertical-slice/CONTRACT_FREEZE.md`（缺失）、`docs/implementation/vertical-slice/A00.md`…`A08.md`（缺失） |
| `agent-packets/00-contract-integrator.md` | A00 packet 的獨立副本 | A00 | **duplicate（重複）**——與內嵌於 `Auspex_Parallel_Execution_Plan.md` 中的 `# A00 …` 段落逐位元組相同（已用 diff 驗證） | 視情況而定——見 §3 | `Auspex_Parallel_Execution_Plan.md` 的「Agent Packets」段落 | 隱含整份計畫（非自足文件） |
| `agent-packets/01-foundation-config-sqlite.md` | A01 packet 的獨立副本 | A01 | **duplicate（重複）**——與內嵌的 `# A01 …` 段落逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/02-claude-telemetry-hooks.md` | A02 packet 的獨立副本 | A02 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/03-progress-state-checkpoint.md` | A03 packet 的獨立副本 | A03 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/04-repository-checkpoint.md` | A04 packet 的獨立副本 | A04 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/05-predictor-policy.md` | A05 packet 的獨立副本 | A05 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/06-graceful-pause-scheduler.md` | A06 packet 的獨立副本 | A06 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/07-runtime-cli-api.md` | A07 packet 的獨立副本 | A07 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/08-qa-security-ci.md` | A08 packet 的獨立副本 | A08 | **duplicate（重複）**——逐位元組相同 | 視情況而定——見 §3 | 同上 | 同上 |
| `agent-packets/README.md` | 標題雖為「README」，實際上是 `Auspex_Parallel_Execution_Plan.md` 第 1–363 行的完整副本（第 1–13 節，Agent Packets 附錄以外的所有內容） | 無（孤兒副本） | **duplicate（重複）**——與計畫本文逐位元組相同（已用 diff 驗證，exit 0） | **否** | `Auspex_Parallel_Execution_Plan.md` | 無——兩份治理文件皆未連結至此 |
| `agent-packets/CONTRACT_FREEZE_TEMPLATE.md` | A00 實際交付物的佔位／鷹架文件，內含字面上的 `<sha>`、`<module>`、`<version>` 標記；標頭寫著 `Status: DRAFT` | A00（預定） | **supporting（輔助）**——尚未產出之權威產物（authoritative artifact）的範本 | 是，但需搬遷位置 | 一旦 A00 執行，將於 `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` 成為實際內容 | 對應 vertical-slice plan §6 所要求的結構 |
| `AgentGuard_Architecture.md` | 先前／不同命名產品（「AgentGuard」）的架構草稿：模組配置不同（`internal/telemetry`、`.agentguard/` 狀態目錄）、provider 組合不同（新增 Gemini/Cursor/OpenCode）、簡單的 Phase 1/2/3 路線圖。ADD 或 vertical-slice plan 皆未參照此文件。 | 無（目前無負責人） | **obsolete（過時）** | 封存（archive）（勿刪除） | `Auspex_ADD.md` | 無傳入參照；也無指向現行文件的傳出參照 |
| `execution_prompt.md` | 一份原始的啟動提示（kickoff-prompt）草稿：「建立一個**恰好四名成員**的 agent 團隊」、2 個 phase、tmux split-pane。這與 vertical-slice plan 及 agent-packets 中正式的**九**-agent（A00–A08）拓撲直接牴觸，也與目前現行「不得建立 teammates」的指示相牴觸。 | 無（看似為工作草稿，非受治理文件） | **obsolete（過時）**（已被核准的 vertical-slice plan 取代／牴觸） | 封存（archive）（勿刪除） | `Auspex_Parallel_Execution_Plan.md` §3–§8 | 參照 `Auspex_ADD.md`、`Auspex_Parallel_Execution_Plan.md`、`docs/implementation/vertical-slice/CONTRACT_FREEZE.md`（缺失）、`AGENTS.md`（缺失） |

---

## 3. 關於 `agent-packets/0X-*.md` 檔案的特別說明

這些**並非**純粹的雜物。vertical-slice plan 本身（§3）就指示：*「若每個 worker 都必須使用 Fable，不要把完整 161 KB 的 ADD 交給每個 worker。只給每個 worker：這份共同計畫；`CONTRACT_FREEZE.md`；它被指派的 ADD 章節；**它自己的 agent packet**。」*——這意味著獨立、逐 agent 分開的檔案本來就是設計上的交接產物（hand-off artifact），讓孤立的 agent／worktree 不需要整份計畫文件。

問題不在於它們的存在，而在於**現在同一份文字存在兩份手動維護的副本**（計畫文件中內嵌的段落，以及獨立檔案），卻沒有任何機制讓兩者保持同步。這是持續性的漂移風險（drift risk），而非一次性的冗餘。

`agent-packets/README.md` 則不同：它用一個容易誤導的檔名複製了整份計畫本文，並沒有任何獨立用途——它就是單純的重複檔案，不具備任何運作上的角色。

---

## 4. 被參照但缺失（尚未建立——僅供留意標記，未列入上方分類）

以下檔案被 ADD 及／或 vertical-slice plan 列為必要項目，但儲存庫中任何地方都不存在：

`AGENTS.md`, `README.md`, `LICENSE`, `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`, `CHANGELOG.md`, `docs/adr/**`, `docs/implementation/vertical-slice/CONTRACT_FREEZE.md`, `docs/implementation/vertical-slice/A00.md`…`A08.md`, `docs/providers/claude/**`, `docs/security/**`.

此外，儲存庫也沒有 `.git` 目錄（尚未初始化為 Git 儲存庫），而這正是 vertical-slice plan 所假設的前提條件（worktree、branch、commit）。

---

## 5. 建議的遷移計畫（尚未執行——等待核准）

依風險排序，風險最低者在前。除非另行明確標註，否則每個步驟都會保留歷史紀錄（封存／搬移，而非刪除）。

1. **建立 `docs/archive/`，將兩個過時（obsolete）檔案原封不動搬移至此。**
   - `AgentGuard_Architecture.md` → `docs/archive/AgentGuard_Architecture.md`
   - `execution_prompt.md` → `docs/archive/execution_prompt.md`
   - 理由：兩者皆已被 `Auspex_ADD.md` / `Auspex_Parallel_Execution_Plan.md` 取代；封存（而非刪除）能保留設計歷史，避免遺失先前的思路。

2. **移除 `agent-packets/README.md`。**
   - 它純粹是 `Auspex_Parallel_Execution_Plan.md`（第 1–13 節）的重複檔案，沒有獨立用途，也沒有任何地方連結至此。
   - 以一份簡短、真實的 README（僅數行）取代，內容需：(a) 說明這些檔案是從 `Auspex_Parallel_Execution_Plan.md` 擷取出的逐 agent 交接 packet，(b) 連結回該檔案作為正規版本，(c) 列出 9 個 packet 檔名及其對應的 agent ID。

3. **以單一真實來源（single source of truth）解決 `agent-packets/0X-*.md` 的重複問題。**
   - 有兩個選項，兩者都合理——這需要您來決定，而非由我決定：
     - **Option A（建議採用）：** 保留 `agent-packets/0X-*.md` 作為正規、可直接交付的檔案（依 vertical-slice plan §3，它們本來就是這樣使用的）。將 `Auspex_Parallel_Execution_Plan.md` 中內嵌的「Agent Packets」段落，替換為簡短摘要並連結至各個 `agent-packets/0X-*.md` 檔案，讓每個 packet 只有一處可編輯。
     - **Option B：** 保留計畫文件作為唯一正規檔案（目前的做法），並以機械化方式（例如一個小型指令稿／Makefile target）從中重新產生 `agent-packets/0X-*.md`，而非手動同時維護兩者。
   - 在此議題定案之前，任何 packet 內容都不應該只在單一位置編輯。

4. **搬遷 contract-freeze 範本。**
   - 將 `agent-packets/CONTRACT_FREEZE_TEMPLATE.md` 搬移至 `docs/implementation/vertical-slice/CONTRACT_FREEZE_TEMPLATE.md`（或維持原位，待 contract freeze 實際執行時由 A00 複製為 `docs/implementation/vertical-slice/CONTRACT_FREEZE.md`）。兩種做法皆可；在此標記是為了避免 `docs/implementation/vertical-slice/` 建置起來之後被遺忘。

5. **建置 ADD／vertical-slice plan 假設已存在、但實際缺失的檔案鷹架**（見上方 §4），至少包含：`AGENTS.md`、`README.md`、`docs/adr/`、`docs/implementation/vertical-slice/`。此項僅為求完整而列出；這屬於新檔案建立，而非既有內容的遷移，可能該歸屬於稍後階段，而非這次正規化（normalization）作業。

### 明確未提出的事項

- 不刪除任何檔案的內容——僅進行搬移／封存，以及一次真正的重複檔案移除（`agent-packets/README.md`），而且理論上這也能從 git 歷史還原……只不過**這目前還不是一個 Git 儲存庫**，所以刪除 `agent-packets/README.md` 將無法透過版本控制復原。建議在執行步驟 2 之前先初始化 Git，或者為求安全改為搬移至 `docs/archive/` 而非刪除。
- 不編輯 `Auspex_ADD.md`（依其自身規則，此為 A00 專屬）。
- 不對任何地方的檔案內容／文字做編輯——本計畫僅涉及搬移／移除。

---

## 6. 執行紀錄（2026-07-12）——正規化（normalization）已核准並執行

§5 的計畫已核准並執行，其中 §5 步驟 3 以**Option A**（建議選項）定案。未觸碰任何 Go 程式碼；未建立任何 teammates。

| 動作 | 結果 |
|---|---|
| `AgentGuard_Architecture.md` 搬移至 `docs/archive/` | 已完成——檔案內容未變動，僅在原標題上方加註一段簡短的「ARCHIVED — obsolete」提示 |
| `execution_prompt.md` 搬移至 `docs/archive/` | 已完成——檔案內容未變動，僅在原文字上方加註一段簡短的「ARCHIVED — obsolete」提示 |
| `agent-packets/README.md` 已重寫 | 已完成——不再是計畫文件的重複檔案；現在是 `agent-packets/` 目錄的真實、範疇明確的 README，並指明 `agent-packets/0X-*.md` 為正規版本 |
| `Auspex_Parallel_Execution_Plan.md` 的「Agent Packets」段落（第 366–1093 行） | 替換為一份簡短的索引表，連結至 `agent-packets/0X-*.md`。這些獨立檔案現在是 packet 內容的**單一真實來源**；計畫文件僅做摘要與連結。第 1–13 節（第 1–365 行）維持逐位元組不變。 |
| `agent-packets/CONTRACT_FREEZE_TEMPLATE.md` | 維持原位（未搬遷）——在 A00 實際產出 `docs/implementation/vertical-slice/CONTRACT_FREEZE.md` 之前，此處仍是正確的暫存位置；`agent-packets/README.md` 現已記載其用途 |
| 根目錄 `README.md` | 已建立（先前不存在）。指向 `Auspex_ADD.md` 作為唯一的架構權威，連結所有其他治理文件，並陳述專案的真實狀態（M0 之前）。 |
| 根目錄 `AGENTS.md` | 已建立（先前不存在），逐字取自 `Auspex_ADD.md` 附錄 G（「Initial AGENTS.md」）——為 ADD 自身規定的內容，並非另行編造的文字。 |
| `docs/implementation/vertical-slice/`、`docs/adr/`、`LICENSE`、`NOTICE`、`SECURITY.md`、`CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`、`GOVERNANCE.md`、`CHANGELOG.md` | **未建立。** 不在此次正規化作業的範圍內（沒有既有內容需要遷移）；仍在上方 §4 中標記為缺口。此項屬於里程碑 M0，而非儲存庫正規化。 |
| Git 儲存庫 | **仍未初始化。** 建議在進一步變更之前先執行 `git init` 並完成首次 commit，讓未來的搬移／刪除能透過版本控制復原，而非仰賴手動封存。 |

### 最終狀態：每個主題恰有一個真實來源

| 主題 | 單一真實來源 |
|---|---|
| 架構 | `Auspex_ADD.md` |
| vertical-slice 執行機制 | `Auspex_Parallel_Execution_Plan.md` |
| 逐 agent packet 內容 | `agent-packets/0X-*.md` |
| 貢獻者／agent 指示 | `AGENTS.md` |
| 專案概覽／入口點 | `README.md` |
| 儲存庫 markdown 稽核 | 本檔案 |
| 已取代／過時的資料 | `docs/archive/`（保留，未刪除，非權威版） |

---

## 7. 執行紀錄（2026-07-12，同日稍晚）——角色重整 + Constitution

儲存庫負責人要求將編號的 `agent-packets/0X-*.md` 檔案重新命名為語意化命名的檔案（一個 LLM 拿到 `predictor.md` 不需要額外脈絡就能知道自己負責什麼；拿到 `05-predictor-policy.md` 則需要），並將 9 個 bounded context 整併為 7 個，同時新增一份最高治理文件。已執行如下：

| 動作 | 結果 |
|---|---|
| `agent-packets/`（9 個編號 packet + README + template） | 搬移至 `docs/archive/agent-packets-v1/`，並附上「已取代」提示橫幅，**未刪除**——由於儲存庫現已完成 commit，git 歷史也會獨立保留此內容 |
| `agents/`（新） | 7 個語意化命名的角色檔案 + `README.md` + `CONTRACT_FREEZE_TEMPLATE.md`。`checkpoint.md` 整併了舊有的 `03-progress-state-checkpoint.md` + `04-repository-checkpoint.md`（內部保留為 Part A／Part B）。`runtime.md` 整併了舊有的 `06-graceful-pause-scheduler.md` + `07-runtime-cli-api.md`（同樣以 Part A／Part B 處理）。`contract-integrator.md`、`foundation.md`、`claude-provider.md`、`predictor.md`、`qa.md` 分別是 `00`、`01`、`02`、`05`、`08` 的直接改名，文內原本的「A0X」角色參照也已改寫為新名稱。 |
| `Auspex_Parallel_Execution_Plan.md` | 全篇改寫——§1 角色數量（8→6 個 bounded-context 角色 + 1 個 integrator）、§3 model 配置、§4 拓撲圖、§5 負責範圍地圖、§7 共用檔案／遷移分配政策、§8 worktree 設定、§9 協調產物路徑、§10 合併順序，以及末尾的角色索引——現在全部以 7 個角色名稱取代 `A00`–`A08`。第 1–2、6、11–13 節（範疇邊界、contract-freeze 關卡、demo script、切分順序、審查提示）內容維持不變；沒有任何產品決策改變，只改變了「誰做什麼」的標籤。 |
| `CONSTITUTION.md`（新，位於儲存庫根目錄） | 最高流程權威文件：單一真實來源層級、文件優先順序、ADR 規則、路徑所有權規則、provider 新增準則、Progress Tree 不變量（invariants），以及 agent 開發規則——全部源自既有 `Auspex_ADD.md` 的規範性內容（§0、§6、§8、§9、FR-100–FR-110），並非另行編造。其權威層級高於 `README.md`／`AGENTS.md`，但與 `Auspex_ADD.md` 並列（而非高於後者），因為兩者治理不同領域（流程 vs. 架構——見 `CONSTITUTION.md` §8）。 |
| `README.md`、`AGENTS.md` | 更新為優先指向 `CONSTITUTION.md`，並改為參照 `agents/` 而非 `agent-packets/`。 |
| `docs/implementation/vertical-slice/EXECUTION_DAG.md` | **目前已過時（stale）**——其中 83 個任務 ID（`A00-01`…`A08-09`）是依照舊有的 9 角色結構產生。此次未重新產生；標記為下一個明確步驟（在新的 7 角色邊界下進行一次「Dependency Review」，於任何團隊組建之前），依儲存庫負責人自訂的偏好流程順序：Inventory → Normalize → DAG → **Dependency Review** → Spawn Team → Phase 1 → Review → Phase 2。 |
| 先前（未核准、未組建）的 4-teammate 對應提案 | 同樣因過時而被取代——Stage 2 原本分散為 4 條平行分支（`A02`/`A03`/`A04`/`A05`）；在新結構下分散為 3 條（`claude-provider`/`checkpoint`/`predictor`）。將在 DAG 重新產生之後才會重做，而非之前。由於從未真正組建任何 agent，因此不需要對執行中的 agent 進行重做。 |

### 最終狀態：每個主題恰有一個真實來源（已更新）

| 主題 | 單一真實來源 |
|---|---|
| 流程、治理、不變量 | `CONSTITUTION.md` |
| 架構 | `Auspex_ADD.md` |
| vertical-slice 執行機制 | `Auspex_Parallel_Execution_Plan.md` |
| 逐角色定義 | `agents/*.md` |
| 貢獻者／agent 指示 | `AGENTS.md` |
| 專案概覽／入口點 | `README.md` |
| 儲存庫 markdown 稽核 | 本檔案 |
| 已取代／過時的資料 | `docs/archive/`（保留，未刪除，非權威版） |
| 任務層級的執行 DAG | `docs/implementation/vertical-slice/EXECUTION_DAG.md`——**已過時，待在 7 角色結構下重新產生** |

目前儲存庫中不再有任何兩份 markdown 檔案包含重疊的權威（authoritative）內容。

---

## 8. 執行紀錄（2026-07-14）——文件重組 + 雙語政策（ADR-049、D-17）

儲存庫負責人提出以下要求：將根目錄 `README.md` 改寫為適合初次瀏覽者閱讀、把根目錄的 markdown 檔案整理進 `docs/`、每個資料夾都要有一份 `README.md` 簡介、以及每份 markdown 文件都要有繁體中文版本。相關決策記錄於 ADR-049 及 `docs/DECISION_LOG.md` D-17。已執行如下：

| 動作 | 結果 |
|---|---|
| `Auspex_ADD.md`、`Auspex_Predictor_Design_Supplement.md`、`Auspex_Parallel_Execution_Plan.md` | 以 `git mv` 搬移至 `docs/design/`（檔名不變，讓依文件名稱的 `§` 引用仍可被 grep 到）。現行維護中的文件（`CONSTITUTION.md`、`CONTRIBUTING.md`、`GOVERNANCE.md`、`SECURITY.md`、`SUPPORT.md`、`AGENTS.md`、`agents/*.md`）現已引用新路徑。歷史紀錄（已接受的 ADR、`docs/archive/**`、`docs/implementation/**` 的進度日誌、Go 註解、JSON-schema 描述字串、已做過校驗碼的 `testdata/**` 固定測試資料）刻意維持不變。 |
| 根目錄 `README.md` | 改寫為適合初次瀏覽者閱讀：真實的 forecast-card 輸出範例、目前的指令樹（含 `daemon`/`init`/`run`/`gc`/`export`）、誠實揭露冷啟動（cold-start）的注意事項（#42/#11）、文件地圖。逐 phase 的整合表已搬移至 `docs/implementation/vertical-slice/README.md`。 |
| `docs/adr/0049-docs-reorg-bilingual.md`（新） | 記錄此次重組及雙語文件政策。 |
| 資料夾 `README.md` 檔案（新增約 68 個） | 現在每個資料夾都有一份——`docs/` 樹狀結構、`cmd/`、`pkg/` 樹狀結構、所有 `internal/` 套件、`schemas/`、`integrations/`、`research/calibration/`、`vscode/` 子資料夾、`testdata/` 根目錄。例外情形（ADR-049 §4）：其內容由測試逐一列舉或做校驗碼比對的固定測試資料目錄（`testdata/*` 葉層目錄、`internal/cli/testdata/`、`internal/managed/testdata/`）不建立 README；由最近的上層 README 記載說明。 |
| 雙語對應檔 `<name>.zh-TW.md` | 每份文件用的 markdown 檔案都新增了一份繁體中文對應檔，並在兩份檔案的開頭互相交叉連結。**規範性文字＝該文件原始撰寫所用的語言**：除 `docs/design/Auspex_ADD.md` 與 `docs/DECISION_LOG.md` 外，其餘檔案皆以英文為規範版本；這兩份文件本就以繁體中文撰寫，其原文即為規範版本，因此不再產生對應副本。作為 markdown 測試固定資料的檔案（`testdata/checkpoints/state/add-section-18-*.md`）並非文件，故不進行翻譯。 |
| `CHANGELOG.md` | 記錄了切片（slice）之後的工作進度（daemon #7、VS Code MVP #53、forecast surface #14、statusline v3 #41、session bootstrap #17、event correlation #1、real restore #6、turn correlation #54）；過時的「Known gaps」已替換為目前的項目（#42/#11、#9/#8、#50/#51）。 |
| `integrations/claude/README.md` | 由 phase 時期「展望性佔位（forward-looking stub）」的標頭，更新為反映目前現況：CLI 已上線、hooks 已上線（dogfooding #12）、延遲 session bootstrap（D-07/#17）、`--emit-line` v3 statusline 格式、`auspex init`。尚未解決的 REC-03 命名歧異紀錄則予以保留。 |
| 本檔案 | 標頭狀態已更新；本 §8 已附加；新增 `repository_inventory.zh-TW.md` 對應檔。 |

### 最終狀態：每個主題恰有一個真實來源（目前現況）

| 主題 | 單一真實來源 | 語言 |
|---|---|---|
| 流程、治理、不變量 | `CONSTITUTION.md` | 英文 |
| 架構 | `docs/design/Auspex_ADD.md` | **繁體中文（原文即為規範版本）** |
| 負責人決策紀錄 | `docs/DECISION_LOG.md` | **繁體中文（原文即為規範版本）** |
| vertical-slice 執行機制 | `docs/design/Auspex_Parallel_Execution_Plan.md` | 英文 |
| 逐角色定義 | `agents/*.md` | 英文 |
| 貢獻者／agent 指示 | `AGENTS.md` | 英文 |
| 專案概覽／入口點 | `README.md` | 英文 |
| 儲存庫 markdown 稽核 | 本檔案 | 英文 |
| 已取代／過時的資料 | `docs/archive/`（保留，未刪除，非權威版） | 英文 |
| 任務層級的執行 DAG | `docs/implementation/vertical-slice/EXECUTION_DAG.md`（已執行；歷史紀錄） | 英文 |

每一份 `.zh-TW.md` 檔案都是其英文對應檔的非規範性翻譯（ADR-049）；若兩者出現分歧，以原始語言文件為準，翻譯版本則視為錯誤（bug）。
