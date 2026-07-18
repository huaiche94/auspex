# Auspex 開發方法論（PDM）

> 🌐 [English](Auspex_Development_Methodology.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 內容 |
|---|---|
| 版本 | 1.0.0 |
| 狀態 | 萃取自 Auspex 專案自身 Phase 0-3 的實際執行過程。尚未套用於第二個專案——詳見 §9。 |
| 目的 | 為由 AI agent 主導的軟體專案，提供一套具版本號、可供引用的開發流程，讓未來的專案得以直接引用 `Follow Auspex Development Methodology v1.0`，而不必每次都重新以數百行的提示詞重述此流程。 |
| 起源 | 本文件是萃取（distillation）而來，並非全新設計——以下每一條規則，都至少曾在 Auspex 專案自身的實際執行中演練過一次，才被寫入此處。凡因某個具體失敗而制定的規則，皆會引用該失敗作為佐證，而非抽象斷言。 |

## 0. 本文件的用途，以及本文件並非什麼

這是一套**流程（process）**方法論：描述工作如何從一個構想，經過
多個 AI agent 在同一份程式碼庫上協作、跨越多個工作階段（session），
最終演變為已整合、已驗證的程式碼，同時不遺失狀態、不重複投入
心力，也不會悄悄偏離已核准的架構。本文件並非：

- 架構範本（architecture template）（那是專案特定的內容——Auspex
  自身的 `Auspex_ADD.md` 只是 Phase 1 產出物的一個*範例*，並非本
  方法論的一部分）。
- 正確性的保證——它規範的是驗證*如何*進行（獨立複查、以證據優先於
  自我回報），而非針對任何特定專案定義「正確」代表什麼。
- 用來取代核准關卡（approval gate）上的人類判斷。以下每個 phase
  都以一個明確的人類決策點作結；本方法論定義的是在該時間點應呈現
  在人類面前的證據，而非他們應該做出什麼決定。

## 1. Phase 順序

```text
Phase 0 — Repository Discovery & Normalization
        ↓
Phase 1 — Architecture Freeze
        ↓
Phase 2 — Bootstrap (lead-only, pre-team)
        ↓
Phase 3 — Team Formation & Phase Execution
        ↓
Phase 4 — Review Gate
        ↓
Phase 5 — Integration
        ↓
Phase 6 — Post-Phase Analysis
        ↓
[loop back to Phase 3 for the next phase, or Phase 7 if analysis
 surfaces an architecture gap]
```

Phase 7（Architecture Amendment，架構修訂）並非此順序中固定的一個
步驟——它是任何 phase 在證據要求時都能開啟的一道側門，並受制於與
其他任何凍結文件變更相同的 ADR 紀律規範。§8 之所以另闢篇幅說明，
是因為它由證據觸發，而非依排程觸發。

## 2. Phase 0 — 儲存庫探索與正規化（Repository Discovery & Normalization）

**目標：** 在任何架構或程式碼工作開始之前，確保每個主題都恰好只有
一份權威文件。

**步驟：**

1. 列舉儲存庫中的每一份文件（不只是你預期重要的文件——本方法論
   之所以存在，部分原因正是像 `AgentGuard_Architecture.md` 這類
   散落的前身文件，或是彼此矛盾的啟動提示詞，都可能無限期地潛藏在
   儲存庫中而未被發現）。
2. 將每份文件恰好歸類為以下其中一種：**authoritative（權威）**、
   **supporting（輔助）**、**obsolete（過時）**、**duplicate（重複）**、
   **archive（歸檔）**（這是一種處置方式，而非內容類型——依照下方
   規則，歸檔的資料是保留而非刪除）。
3. 在動到任何檔案之前，先產出一份書面清冊（Auspex 的作法是
   `docs/repository_inventory.md`）。
4. 在執行任何搬移／歸檔／刪除之前，先取得對該清冊與遷移計畫的
   明確核准。
5. 執行遷移：將過時的資料以簡短的「superseded by X」（已由 X 取代）
   橫幅標註後歸檔，而非直接刪除（git 歷史紀錄並非總是安全的替代
   方案——一個全新的儲存庫甚至可能尚未初始化 `.git`），將重複內容
   改寫為單一的正本（canonical copy），並更新每一處交叉引用。
6. 在任何後續的重新架構之後，再次進行稽核（Auspex 做過兩次——一次
   是初始清冊，另一次是在把 9 個角色整併為 7 個之後——稽核軌跡本身
   應該持續累積，而不是每次都丟棄重寫）。

**為什麼這是 Phase 0，而非「有做更好」的加分項：** 之後每一個
phase 都假設「架構文件」與「流程文件」是明確且唯一的參照對象。
略過這個步驟，代表未來每一個 agent 都得從頭重新裁定文件的權威性，
而這正是本方法論所要防範的「每次都要用數百行提示詞重述」的問題。

## 3. Phase 1 — 架構凍結（Architecture Freeze）

**目標：** 一份凍結的架構文件、一份凍結的流程憲章（Constitution）
（兩份不同的文件——見 §3.1），以及一個可供機器檢查的執行 DAG，三者
彼此交叉引用且一致。

### 3.1 兩份文件，而非一份，且兩者不可互換

- **架構文件**（Auspex 的 `Auspex_ADD.md`）對於*系統是什麼、
  行為如何*擁有最高權威。
- **儲存庫憲章（Repository Constitution）**（Auspex 的
  `CONSTITUTION.md`）對於*專案如何建置*擁有最高權威——文件優先序、
  ADR 規則、路徑擁有權、不變量（invariant）的強制執行、agent 開發
  規則等。

把兩者合併為一份文件的做法，曾在 Auspex 早期（Constitution 尚未
存在時）被隱含地嘗試過，並正好產生了這種拆分所要修正的失敗：流程
規則（誰能編輯什麼、何時需要 ADR）必須不斷從架構文件的敘述中推論，
而非直接查閱。從一開始就把兩者寫成各自獨立的文件，並明確說明兩者
的關係（Auspex Constitution §8 即是可參考的實例）。

### 3.2 執行 DAG

每一個實作工作單元都會成為一個 DAG 節點，至少須包含：ID、owner
（負責人）、dependencies（相依關係）、estimated complexity（預估
複雜度）、estimated LOC（預估程式碼行數）、estimated files（預估
檔案數）、**estimated duration（預估耗時）、estimated token cost
（預估 token 成本）**、validation command（驗證指令）、merge order
（合併順序）、risk（風險）、blockers（阻擋因素）。以 Mermaid 圖表
與拓樸排序（topologically sorted）清單兩種形式呈現此圖——圖表供人
類審閱，排序清單則是 agent 實際執行時所依循的對象。

欄位語意定義（之所以在此定義，是因為模糊性本身就是一個被實際
量測到的誤差來源——見 Auspex 的 `Prediction_Error_Report.md` §3）：

- **estimated LOC** ＝預期的 `git` 新增行數，實作程式碼與測試合計，
  不含 progress-artifact／lessons 文件。
- **estimated files** ＝該節點自身 commit 所建立或修改的檔案數，
  拆分為 `impl+test`（例如 `3+2`）——把兩者混為一談，是 Auspex 各
  phase 中重複出現次數最多的估算錯誤（`Wave2_Lessons.md` §1
  issue #1）。
- **estimated duration** ＝單一 agent 呼叫所需的實際經過時間
  （wall-clock minutes），僅為估算精度（區間值，而非單一數值）。
- **estimated token cost** ＝執行該任務的 agent 之輸出 token 區間。
  後兩項欄位允許明確填寫 `n/a — declared out of scope`（不適用——
  聲明為範疇外），但該欄位本身必須存在並如實說明。

**歷史紀錄：** Auspex 自身的 DAG 在橫跨 84 個以上節點、十二個 phase
的過程中，從未有過 duration 或 token-cost 欄位；5 個實作角色中有 4
個，在彼此毫無溝通的情況下各自獨立發現了這個缺口
（`Wave2_Lessons.md` §1、`ADR_Recommendations.md` REC-02）。上述
schema 已針對 2026-07-13 之後的所有規劃解決了 REC-02（issue #15）；
vertical-slice 的 DAG 並未回溯補註——其估算值僅作為歷史紀錄保留。

**量測紀律（估算值只是 REC-02 的一半）：** 實際值必須能夠依節點
擷取，否則估算值永遠無法被評分。以下兩種協定，符合其一即可：

1. **在允許平行處理的情況下，每次 agent 呼叫對應一個節點**——如此
   一來 harness 的 usage 區塊便能直接回報各節點的 token 用量，而
   lead 自身的 tool-call 時間戳記即可界定耗時範圍（不需要 agent
   自行回報）。
2. 否則採用**由 lead 端計時**——lead 以自身的時鐘記錄呼叫的起訖
   時間，並將該次呼叫的 token 總量歸屬給整個節點*集合*，且明確標註
   為「未拆分」（絕不可平均分攤後假裝是逐節點的資料——見
   `Prediction_Error_Report.md` §0 的捏造禁止規則）。

### 3.3 核准關卡

在 Phase 2 開始之前，須由人類一併審閱並明確核准架構文件、
Constitution 與 DAG 三者。三者都不應被個別、單獨核准——若 DAG 是
針對某個之後又變動的架構所核准的，那麼它一出爐就已經過時。

## 4. Phase 2 — Bootstrap（啟動）

**這個 phase 之所以存在，是因為一個具體、真實發生過的死結
（deadlock），而非憑空設計出來的流程。** 在 Auspex 的實際執行中，
Phase 1 一度無法啟動：每一位 root-node 隊員的第一項任務，都仰賴已
凍結的 domain／contract 型別存在，但建立這些型別本身就是某個 DAG
角色的工作，而該角色從來都不在 Phase 1 指定的隊員名單之內，且 lead
在當下被指示不得實作任何 production 程式碼。在當時的規則之下，沒有
任何一個節點是可指派的。

**修正方式，現已一般化為一項規則：** 將 contract-freezing（契約
凍結——domain 型別、跨元件介面、事件／協定封套、儲存交易慣例）視為
一個**具名、僅限 lead 執行、位於 Phase 之前的階段**，由 lead 直接
執行，不委派給隊員，也不併入「Phase 1」。這並非權宜之計——它反映的
事實是：在這些產物尚未存在之前，除了 lead 之外沒有任何角色能有意義
地擁有它們，因為其他每個角色的工作都是以它們為基礎所定義的。

**步驟：**

1. Lead 凍結以下項目：共用的識別字／enum、跨元件介面（維持精簡——
   不允許「God interface」）、wire／事件封套、具型別的錯誤、可注入的
   Clock／IDGenerator／ProcessRunner 風格介面縫（seam）、儲存交易
   邊界慣例。
2. Lead 撰寫一份等同於 `CONTRACT_FREEZE.md` 的文件：確切的 import
   路徑、欄位名稱、JSON／wire 名稱、狀態轉換表、ID／冪等性
   （idempotency）規則、unknown／null 語意、隱私預設值、交易邊界、
   migration 擁有權範圍。
3. Lead 進行驗證（build、vet、test）並提交（commit）。
4. 唯有在此 commit 完成之後，Phase 3 才能開始。

**必要的環境先決條件也屬於此階段**，Auspex 自身的 Bootstrap 便是
明證：工具鏈版本不符（已安裝的執行期版本，與架構所釘選的版本不同）、
以及建置檔案缺失的缺口（module manifest 根本不存在，而建立它本身
又是某角色所擁有的工作，卻被此 phase 所要解決的同一個死結卡住）——
這兩者都必須由 lead 在取得明確的人類核准後解決，才能產生任何隊員。
應把「每一個可指派節點的工具鏈是否真的能夠執行」視為 Bootstrap
退出條件的一部分，而非一項假設。

## 5. Phase 3 — 團隊組成與 Phase 執行（Team Formation & Phase Execution）

**目標：** 平行、路徑隔離（path-isolated）的實作，並附帶逐節點的
證據，而非一次性、僅在 phase 結束時提出單一報告的批次工作。

### 5.1 團隊組成規則

- 產生的隊員數量須恰好等於已核准的數量——未經明確核准不得多產生，
  也不得因為另一項任務看起來比較簡單，就擅自替換某隊員的指派內容。
- 每位隊員都應獲得彼此互不重疊、專屬的路徑集合。在產生隊員*之前*
  就要驗證這些路徑集合確實互不重疊（一個簡單的跨分支檔案 diff
  檢查即可），而非事後才驗證。
- 只指派那些相依關係**真正已完成**的節點——已合併且已驗證，而非
  僅僅「已指派給某人」。若相依於一個尚未整合的同層節點，該相依關係
  並未被滿足。
- 若真正解鎖的節點數量少於可用隊員數量，就讓隊員閒置。**閒置是
  正當的。** 不要為了讓產能保持忙碌而捏造工作——這是 Auspex 自身
  執行過程中明確、且反覆下達的指示，之所以在此一般化為規則，是因為
  另一種做法（因為某人有空就指派工作給他）正是範疇蔓延（scope
  creep）與「DAG 與現實脫節」開始的起點。

### 5.2 逐節點執行紀律

在單一隊員被指派多個節點的情況下，應**嚴格依序**執行，而非批次
進行：

```text
for each assigned node, in dependency order:
    implement only this node
    run this node's validation command(s)
    update the progress artifact (durable, structured — not a chat claim)
    update the lessons-learned artifact (estimate vs. actual, honestly)
    commit (one commit per node, or per logical unit within a node)
    confirm the next node's dependencies are genuinely satisfied
    → only then proceed to the next node
```

**這為何重要，有實證支持：** Auspex 執行過程中每一次工作階段中斷
事件（兩個 phase 中共發生 3 次），之所以能夠零重工地復原，正是因為
實作產物早已持久化寫入磁碟，且以節點為範圍的 commit，使得「實際
完成了什麼」可被獨立驗證，而不必仰賴信任一個被中斷的 agent 對自身
進度的記憶。

### 5.3 隊員絕不可做的事

- 編輯其他隊員或 lead 所擁有的路徑。
- 為了繞過某個缺口而編輯已凍結的 contract 檔案——應在自己擁有的
  路徑範圍內以區域方式（locally）繞過該缺口，並回報此缺口。
  （Auspex 的 `predictor` 角色曾兩次遇到這種情況——一次是 request
  DTO 欄位不足，另一次是缺少 response 形狀——兩次都正確地在
  package 範圍內建立區域性的權宜作法，而非編輯
  `internal/app/ports.go`。）
- 在已指派的範疇完成後，「因為還有閒置產能」而擅自繼續做後面的
  節點。在指派邊界處停下來是正確的行為，並非表現不佳。
- 將自己的分支合併進 `main`，或合併進另一位隊員的分支。

## 6. Phase 4 — 審查關卡（Review Gate）

**核心規則：絕不在沒有獨立驗證的情況下，接受「已完成」的宣稱。**
這並非為了謹慎而謹慎——在 Auspex 執行過程中已完成的 19 個節點裡，
lead 的獨立複查並未抓到任何一次「假完成」宣稱，但確實抓到了
(a) 一次自行回報的 commit hash 格式差異，經檢查後判定為無害，以及
(b) 原本足以抓到任何因工作階段中斷而悄悄產生不完整產物的情況——而
這正是實際發生過三次的狀況，且都在被回報為「已完成」之前，就被
正確地攔截下來。

對於每一個節點，在將其判定為 Validated（已驗證）之前：

1. 親自重新執行每一項驗證指令——不要相信「測試通過」的回報；
   要親自跑一次測試。
2. 將變動檔案清單與該角色宣告的專屬路徑做 diff 比對——確認沒有
   任何超出範疇的編輯。
3. 檢查是否存在與凍結 contract 重複／衝突的型別或介面定義——用
   grep 檢查，不要憑假設。
4. 至少親自閱讀一項非瑣碎宣稱的實際實作程式碼，而不只是看測試
   輸出結果。測試套件通過是必要條件，但並非充分證據，不足以證明
   底層的性質（例如「這個 digest 與順序無關」、「這個呼叫絕不使用
   shell 字串」）是真實成立的。
5. 確認所需的產物（進度紀錄、lessons-learned 條目）確實存在且
   內容真實，而非佔位文字。

**若某節點未通過審查：** 將其退回給*原本*負責的隊員，並附上確切
未達成的判準與確切相關的檔案。Lead 不會去修正隊員擁有的程式碼。
不要重跑整個 phase——只有未通過節點的負責人重新執行，然後再次進入
Review Gate。

## 7. Phase 5 — 整合（Integration）

1. 在整合之前，確認每個分支的相依項目都已存在於目標分支上，且
   工作目錄（working tree）是乾淨的。
2. 依照**凍結 DAG 的相依順序**進行整合，而非依字母順序或圖方便的
   順序。若多個分支彼此之間沒有相依關係，則依 DAG 文件中所列出的
   順序進行整合，以維持一致性，而非任意排序。
3. 使用一個專門的整合分支——在整合分支上通過整個儲存庫的驗證之前，
   不要將候選分支直接合併進 `main`。
4. 每完成一次個別合併後：在進行下一次合併之前，重新執行完整的
   驗證套件，如此一來任何衝突或迴歸（regression）都能歸因於引入
   它的那個分支。
5. 所有合併完成後：對整個儲存庫執行 format／build／vet／test
   （若支援的話，也包含 race detection），重新執行每一個節點自身的
   驗證指令，若專案有隱私不變量（privacy invariant）則進行隱私／
   洩漏掃描，並明確檢查：沒有遺失任何擁有權相關產物、沒有引入重複
   型別、沒有出現超出此 phase 範疇的內容。
6. 將整合分支合併進 `main`，以**單一整合 commit** 的形式呈現，
   commit 訊息須記錄合併順序，以及每一個被整合的分支／commit SHA——
   這個 commit 才是該 phase 的持久紀錄，而非各個節點的個別 commit
   （這些 commit 仍保留於分支歷史中可供查閱，不會被刪除）。
7. 產出一份 Integration Report（整合報告）：順序、SHA、衝突（若
   有）及其解決方式、驗證結果、剩餘風險、新解鎖的 DAG 節點。停下來
   等待核准，之後才能規劃下一個 phase。

## 8. Phase 6 — Phase 後分析（Post-Phase Analysis）

**核心規則：`Unknown`（未知）是一個有效、且必要時必須給出的答案。
絕不可捏造一個實際上並未被觀測到的指標。** 每一個回報的數值都須
標註以下四種來源標籤之一——**Observed**（直接量測所得）、
**Estimated**（自我回報，並非獨立量測）、**Derived**（由其他已標註
的數值計算而得，並附有自身的信賴度）、或 **Unknown**（不存在資料，
應直白說明，而非略過不提或憑空捏造）。

此 phase 產出以下內容（可依專案調整名稱／範疇，但須維持相同架構）：

1. **Prediction Error Report（預測誤差報告）** — 針對每一個已完成
   節點比較估算值與實際值，當比較的雙方數值確實都存在時，列出絕對
   誤差與百分比誤差，若不存在則明確標示 `Unknown`（例如 Auspex 的
   DAG 從未有過 duration／token 的估算值，因此每個節點這些誤差計算
   都是 `Unknown`／`N/A`，而非 `0`）。
2. **Calibration Report（校準報告）** — 針對誤差報告進行系統性
   偏誤分析，並在一開始就明白說明樣本數的限制，而非藏在後面。在
   n=2 時觀察到的模式，應以低信賴度的假說形式回報，而不應誇大為
   一項「發現」。
3. **Lessons Aggregation（經驗彙整）** — 彙整每個角色的
   lessons-learned 檔案，並依照有多少*獨立*隊員在彼此未溝通的情況下
   各自提出相同問題，對重複出現的問題進行排序（獨立收斂
   （independent convergence）是這個 phase 所能產出最強的訊號）。
4. **Improvement Suggestions（改善建議）** — 僅為建議，明確不在
   此 phase 中實作。每一項建議都須註明其是否有實證依據（並引用該
   證據），或屬於推測性質（並明確說明）。
5. **Historical Replay（歷史回放）**（若適用）— 針對真實歷史資料，
   比較目前與提議中的 predictor／heuristic 行為。**若尚無真實歷史
   資料存在，須明確說明，不得以捏造的比較取而代之**——這正是
   Auspex 自身 Phase 3.5 的結果：每一項要求的準確度指標都正確地
   標示為 `Unknown`，因為當時從未收集過任何即時遙測（telemetry）
   資料。
6. **Missing Telemetry Report（缺失遙測報告）** — 針對 predictor／
   heuristic 需要、但目前無法量測的每一項指標：說明原因、任何
   provider／環境上的限制、可能的確定性（deterministic）權宜作法、
   若持續缺漏的預期影響，以及建議的未來實作路徑。
7. **Feature Registry（特徵登錄表）** — 系統決策邏輯所依賴的每一項
   feature／signal 的正本、具版本號的資料字典。這將成為往後的
   單一事實來源——未來任何新增至決策邏輯中的 feature，都必須有對應
   的登錄項目。每一筆項目須註明資料型別、來源、來源證據
   （provenance）、信賴度、目前可用性（Available／Derived／
   Estimated／Unknown），以及適用於哪些預測／決策層級。
8. **Feature Gap Report（特徵缺口報告）** — 作為登錄表的輔助文件：
   針對每一項缺失或僅部分可用的 feature，說明缺失原因、影響、建議
   的補齊方式、複雜度，以及預期改善幅度——並加以排序，供下一個
   phase 決定優先順序。
9. **Confidence Report（信賴度報告）** — 一份基於登錄表衍生出的
   檢視（不重複登錄表的內容），將 feature 分類為高／中／低／零信賴
   度，並建議哪些適合作為未來的訓練標籤（training label）、哪些應
   維持為輔助輸入，以及哪些應明確絕不可作為訓練標籤（例如：一旦
   真正的 ground truth 存在，一個低信賴度的 heuristic 代理指標就
   不應成為訓練目標——一旦真實標籤可用，仍針對該代理指標進行訓練，
   實際上會使統計／ML 層級的表現變差，而非有所幫助）。
10. **ADR Recommendations（ADR 建議）** — 僅為提議，尚未核准，
    也尚未實作。每一項須說明問題、證據、受影響的 package、相容性
    影響，以及建議做法。
11. **Next-Phase Recommendation（下一波建議）** — 新解鎖的節點
    （須檢查真正的相依關係是否已滿足，而非憑假設——Auspex 自身的
    分析發現，有兩個節點早在*前一個* phase 就已解鎖，卻始終未被
    指派，原因是團隊名單從未涵蓋其擁有角色），附上完整標註來源／
    信賴度／不確定性的逐節點估算值，並依 (a) 相依解鎖價值、
    (b) 執行風險、(c) 預估耗時、(d) 合併複雜度 進行排序。**此 phase
    中不得指派隊員，也不得執行任何工作**——這僅是提供給下一次明確
    核准的規劃輸入。

**產出這些內容後即停止。等待核准後，Phase 3 才能再次開始。**

## 9. Phase 7 — 架構修訂（Architecture Amendment）（由證據觸發，非排程觸發）

若 Post-Phase Analysis（或任何其他 phase）浮現出一個真正的架構
缺口——有實證支持，而非純屬推測——不要悄悄地繞過它修補，也不要悄悄地
重新設計它：

1. **停下來。** 精確地回報這項不一致之處：缺少什麼、為何重要、
   它阻擋了什麼。
2. 撰寫一份提議中的 ADR：背景脈絡（context）、決策（decision）、
   以及它確切會改變凍結架構／contract／DAG 中的哪些部分。
3. 取得明確核准——核准的須是 ADR 本身，而非用來代替它的 stub 或
   佔位文字。（Auspex 的 ADR-041——在 predictor 管線中補上缺失的
   Token／Quota Forecast 層——即是可參考的實例：該缺口是透過一份
   輔助設計文件發現的，並直接對照凍結的架構文字加以確認，而非單憑
   信任認定，之後才將其實作為真正、最終定案的 contract——而非日後
   會被取代的暫時性 stub。）
4. 在恢復正常的 phase 執行之前，將此變更串連（cascade）至每一份
   引用舊形狀的文件（架構文件、contract-freeze 紀錄、DAG——包括
   修正任何本身就是同一缺口症狀的 DAG 相依邊，而不只是新增節點）。
5. 重新產生任何依據現已過時的 DAG 所建立的 phase 計畫，並明確註明
   有哪些改變，以及重新產生的計畫與它所取代的舊計畫相比，有何不同
   （或並無不同）。

## 10. 貫穿所有 phase 的不變量（Invariants）

以下這些並非特定於某個 phase——不論身處哪個 phase，違反其中任何
一項都是一種缺陷：

1. 每個主題**永遠只有單一事實來源**。兩份文件互相牴觸是需要修正
   的 bug，而不是每次出現時再臨場判斷一次的取捨。
2. **Lead 負責統籌（orchestrate）；lead 不實作隊員擁有的程式碼。**
   Bootstrap（Phase 2）是唯一、明確且範圍狹窄的例外，因為在流程的
   那個時間點，contract-freezing 沒有其他擁有者。
3. **永遠以證據優先於自我回報。** 「已完成」的宣稱需要持久性的
   證據（檔案、通過的驗證、結構化紀錄）——僅在對話中宣稱完成並不是
   證據。
4. 在每一份報告、每一項估算、每一個指標中，**寧可標示 Unknown，
   也不捏造。** 說明你不知道什麼、以及為什麼不知道，而不要用看似
   合理的內容填補空白。
5. **路徑擁有權是專屬的，且須以驗證來落實，而非僅靠指示。** 要
   檢查 diff；不要假設邊界有被遵守。
6. **在指派邊界處停下來是正確的行為。** 閒置的產能不是一個需要
   靠捏造範疇來解決的問題。
7. **凍結文件唯有透過 ADR 流程才能變更**，即使變更看似微小，即使
   它「顯然」是正確的。正是這個流程，防止了「顯然正確」的變更悄悄
   累積成一個未被記錄、且不再符合其自身書面描述的架構。

## 11. 版本管理與如何引用本方法論

本文件為 **v1.0.0**。未來的修訂版本應審慎地標註版本號（採語意化
版本（semantic-versioning）風格：對 phase 順序或不變量的破壞性
變更視為主版號（major）遞增；純粹補充說明的修訂視為次版號
（minor）遞增），且本文件自身的變更，也應經過與其他任何凍結流程
文件相同的 ADR 式審查。

**若要在新專案中引用本方法論**，提示詞只需要恰好一行：

```text
Follow Auspex Development Methodology v1.0.
```

這句話的意思應理解為：執行 §1 中的 phase 順序、遵守 §10 中的每一項
不變量，並產出各 phase 小節中所指名的產物——依新專案實際的架構與
領域加以調整，但遵循這個流程架構，而非從零重新推導，也不必在
提示詞中重新說明一次。

## 12. 明確聲明「本文件不主張」的事項

本文件並不主張：這套流程是最佳解、它已在多於一個專案上驗證過、
每一條規則在 Go／多 agent／單一儲存庫的情境之外都能完美地一般化，
或是遵循此流程就能保證得到正確的架構。它僅主張：文件中的每一條
規則，都曾在 Auspex 專案上，因某個真實的原因，至少被實際演練過
一次，並被寫在此處，讓下一個專案不必透過相同的失敗，重新發現同樣
的教訓。
