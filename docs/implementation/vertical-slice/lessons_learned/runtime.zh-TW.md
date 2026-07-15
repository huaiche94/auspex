# 經驗教訓 — runtime（第 3 波：runtime-b01）

> 🌐 [English](runtime.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-b01 | M | S/M — 比 M 級預估的還要輕 | 6 | 6（doc.go、errors.go、errors_test.go、root.go、root_test.go、hook.go） | 350 LOC（DAG 預估） | 一次連續作業完成，此環境未記錄牆鐘時間 | 無 — `internal/domain/errors.go` 中凍結的 `Error` 結構，以及 `internal/buildinfo.String()`，兩者都已完全足夠；不需要向此角色不擁有的檔案新增任何相依性或欄位 | 除了 DAG 自身的 6 檔預估外沒有其他 — 把命名慣例的理由寫進 `doc.go`（而不是另立第 7 個獨立檔案）讓數量剛好與預估相符 | 無 — 唯一真正需要判斷的地方（hook 子命令要用 kebab-case 還是 PascalCase 命名，見 `ADR_Recommendations.md` REC-03）在任務簡報中已預先標示並給出明確答案可採用，因此只需要記錄而不需開放式研究 | `root_test.go` 的早期草稿曾加入一個防禦性的 `var _ = cobra.Command{}`，用來防範假想中未來編輯可能造成的未使用匯入；一旦檢查了實際最終的匯入集合，馬上就發現這是不必要的 — 只要先寫測試表格、只匯入實際需要的內容，而不是預先做防禦性匯入，就能便宜地避免這種浪費 | 把每個 P0 指令都建成一個回傳同一個共用 `notImplemented(command string) error` 輔助函式的 stub，而不是各自打造 N 個客製化的錯誤建構，使實作本身與其測試（`TestStubCommandsReturnNotImplemented`，以資料表驅動涵蓋全部 17 條非 version 的葉節點路徑）幾乎零成本 — 一個斷言函式，由同一張用來驗證樹狀註冊結構的路徑表驅動。建議把這種「一個型別化錯誤建構子 + 一張被兩種不同測試（樹狀結構、stub 行為）驗證的路徑表」模式，訂為未來任何一波中若某節點刻意採用 stub 層時的預設做法（例如若 `checkpoint`／`predictor` 未來也需要在相依項目就緒前放一個誠實的 stub 佔位），而不是讓每個 stub 指令各自臨時發明自己的錯誤值 |

## 跨節點觀察

- 這是 `runtime` 有史以來的第一個節點（沒有先前第 1／2 波的歷史紀錄可供比較，不像
  `predictor`／`foundation`／`claude-provider`／`checkpoint`，這些角色都有更早的波次）。
  DAG 對 `runtime-b01` 所估的 M／350 LOC／6 檔，在檔案數上準確命中，在 LOC 上則略為寬鬆
  （不含測試 387 行／含測試共 561 行）— 這與 `lessons_learned/predictor.md` 中已提到的跨角色
  模式一致：自成一體、相依性輕（沒有 I/O、沒有並行、除了已凍結的契約外沒有跨套件銜接）的
  套件，傾向落在 DAG 預估值或略低於預估值。
- 這個節點上，單一影響最大的非程式碼決策屬於流程性質，而非技術性質：在撰寫任何指令的
  `Use` 字串之前，先確認*哪一份*文件才是 hook 子命令命名方式的權威依據，而不是先選定一種
  慣例、之後才在審查失敗時才發現衝突。Constitution §2 的文件優先順序，加上
  `ADR_Recommendations.md` REC-03，讓這件事只花五分鐘查閱就能確定，而不必用猜的 —
  值得記下來作為一個正面案例：把衝突歷史記錄在一個可被找到的地方（`wave2-analysis/`），
  而不是只留在聊天記錄／PR 歷史中，因為一個全新的工作階段（像這次一樣，是第一次指派、
  沒有先前對話脈絡）看不到那些記錄。
- 沒有任何阻礙、非預期相依性或範疇意外嚴重到需要提出新的 ADR，或偏離已凍結的契約。
  `internal/domain/errors.go` 與 `internal/buildinfo` 已具備所需的一切，不需要透過本文件
  申請新增項目。

# 經驗教訓 — runtime（第 4 波：runtime-a01、runtime-b02）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a01 | S（DAG：150 LOC，3 個檔案） | M — SQL 本身是 S 等級，但正向外鍵（forward-FK）與 SQLite 串聯（cascade）解析之間的交互作用，把一項單純的轉寫工作變成了需要撰寫上呈說明的 schema 設計決策 | 3 | 4（3 個 .sql 檔 + 1 個測試檔，是 DAG 驗證指令隱含要求、但 3 檔預估未計入的部分） | 150 LOC | 一次連續作業；先寫出採用標準外鍵（canonical-FK）的第一版草稿，觀察到它讓 foundation 的串聯測試失敗，然後重寫 — 使這個節點實際寫下的 LOC 大約是最終交付版本的兩倍 | **兩項未宣告的相依性。**（1）ADD §12.2 中標準版 `pause_records` 外鍵參照了四張屬於 0010-0049 範圍、目前尚不存在的資料表；在 `PRAGMA foreign_keys = ON` 之下，SQLite 會對任何 DML *（包括來自 repositories／worktrees／tasks 的串聯）* 解析每一個父資料表，因此若直接交付標準外鍵，會讓 foundation 既有的測試以及全倉庫範圍內所有 task 刪除的 DML 全部壞掉。最後採用 0004_tasks.sql 的純指標（plain-pointer）先例來解決，並在進度文件中上呈說明。（2）foundation 的 migrate_test.go 斷言了精確的 migration 數量／版本（== 4），因此*任何*角色只要新增 migration，就會讓 foundation 的 3 項測試失敗 — 已提出變更請求，而非自行修正 | migrations_0050_pause_test.go 位於 internal/storage/sqlite/（foundation 的目錄）— 在 DAG 驗證指令的要求下無可避免；已提出所有權劃分的請求 | foundation 的 3 項過時斷言，會讓（未過濾的）`go test ./internal/storage/sqlite/...` 保持失敗狀態，直到其機械式修正落地為止；runtime 自身的驗證指令則是通過的 | 標準外鍵的第一版草稿是誠實的探索，不算浪費 — 但這種失敗模式（串聯 DML 汙染）其實可以在寫 SQL *之前*，藉由閱讀 0004_tasks.sql 中 active_node_id 的註解就先預見到，因為該註解點名的正是同一類問題 | 當 DAG 把一個會以外鍵參照到另一角色尚未落地範圍的 migration range 指派給某角色時，DAG 該列應明確說明要 (a) 宣告正向外鍵、(b) 像 0004 一樣採用純指標，還是 (c) 卡住等待父範圍就緒 — 每一個在 foundation 之後擁有 migration 的角色都會遇到完全相同的分岔點，而答案會實質改變 schema 與測試的樣貌。另外：由某個 range 所擁有的 migration 測試需要一個明確宣告的歸屬位置；「測試放在驗證指令所指向的地方」這條規則應該寫進執行計畫的共用檔案政策中 |
| runtime-b02 | M（DAG：300 LOC，4 個檔案） | S/M — 純粹是針對已凍結介面的組合式銜接工作；零 I/O、零並行 | 4 | 9（wiring.go、wiring_test.go，另加 7 個 fakes 檔案：doc、未設定輔助函式，以及每個 service 介面各一個）— 檔案數較多但總 LOC 相近；之所以選擇每個介面各自獨立的 fake 檔案，是為了讓 qa 之後能單獨演進某個 service 的替身而不動到其他部分 | 300 LOC | 一次連續作業完成，無需重做 | 無 — internal/app/ports.go 中的五個 service 介面原樣即已完全足夠；不需要新增 DTO 欄位，也不需要變更 go.mod（cobra 已經存在） | DAG 的 4 檔預估隱含假設 fakes 的檔案數會比較少；依介面拆分是刻意的結構選擇，而非範疇擴大 | 無 | 沒有值得一提之處 — 在寫任何 fake 之前先完整讀過一次 ports.go，避免了逐方法邊寫邊抄可能造成的所有簽章不符問題 | Fake<Interface> + <Method>Func + 未設定時大聲報錯（loud-unconfigured-error）的模式（延伸自 runtime-b01「一個型別化錯誤建構子」的心得），讓 5 個介面替身幾乎變成機械式工作；建議將其訂為整個 repo 的預設做法，適用於 qa 或任何角色需要為一個凍結 port 建立替身的情況。另外：wiring.New 的 fail-closed（欄位為 nil 即拒絕）驗證，兩次抓到了測試撰寫過程中自己打的錯字 — 建構期的組合驗證能立即回本，日後真正的 service 取代 fake 時也應保留這項驗證 |

## 第 4 波跨節點觀察

- 本波單一成本最高的教訓來自 runtime-a01：當各角色的 migration range 沒有按數字順序落地時，
  **標準版 schema 並不會自動等於一個可交付的 migration**。SQLite 在 CREATE 時對正向外鍵的
  寬容，加上在 DML 時嚴格要求整組父資料表都已解析完成，代表某個角色「完全符合 ADD」的
  migration，可能透過串聯鏈默默弄壞其他每一個角色的 DML。0004_tasks.sql 的先例（純指標 +
  註解 + 上呈說明）證實是通用的解法，目前在這個 repo 中已被套用兩次 — 也許應該把它
  正式、權威地寫下來一次（執行計畫 §7 或一份 ADR），而不是讓每個角色都各自對著一片
  紅色的測試結果重新發現它。
- 本波第一次浮現跨角色測試耦合的問題：foundation 針對共用 AllMigrations() 集合所做的
  精確計數斷言，只要碰到任何第二個角色的檔案就會壞掉。以 range 為範圍的斷言（每個角色
  只斷言自己那段 00X0-00X9）顯然是理所當然的慣例；依 Constitution §4.4，這被提報為
  變更請求，而非就地修正。

# 經驗教訓 — runtime（第 5 波：runtime-a02、runtime-a06、runtime-b03、runtime-b04、runtime-b05、runtime-b08）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a02 | L（DAG：400 LOC，4 個檔案） | M — 一旦三份文件之間的狀態名稱調和完成，轉換表本身就只是機械性工作；真正的工作在於那項調和，而不是程式碼 | 4 | 3（doc.go、statemachine.go、statemachine_test.go） | 400 LOC | 一次連續作業完成，無需重做 | 沒有新的相依性 — CONTRACT_FREEZE.md 中已凍結、有 12 個值的 domain.PauseStatus 列舉已完全足夠；所謂的「相依性」其實是要把 agents/runtime.md 的文字敘述路徑，以及 ADD §20.5 的圖示，調和到這個列舉上，這是一項閱讀／文件整理工作，而不是程式碼相依性 | 除了 DAG 的 4 檔預估外沒有其他 | 無 — Constitution §2 的文件優先順序（凍結列舉 > 文字敘述）讓這項調和變成單純查閱，而不是需要上呈的判斷 | 無 — 在寫任何表格項目之前先完整讀過 CONTRACT_FREEZE.md 的「Frozen state transitions」章節，避免了發明出第 13 個狀態，而這正是 Constitution 明確禁止的事 | 建議在任何未來描述狀態機、且來源文件不只一份（文字敘述 + 圖示 + 凍結列舉）的任務包中，明確說明命名不一致時以哪一份為準 — 這個節點得從 Constitution §2 的第一原理自行推導出答案，而不是靠任務包本身的指引 |
| runtime-a06 | L（DAG：400 LOC，4 個檔案） | L — 預估成立，但原因與預期不同：SQL／API 表面很直接，真正的困難在於要透過 database/sql 的連線池，把 BEGIN IMMEDIATE 的並行語意真正做對，過程中付出兩個真實 bug 的代價，皆在提交前被此節點自身的測試抓到（詳見進度文件的專屬章節） | 4 | 3（doc.go、lease.go、lease_test.go） | 400 LOC | 一次連續作業，但期間經歷兩次完整的「停下來－診斷－修正」循環（一次是 `-race` 測試掛起、在約 4 分鐘牆鐘時間後被強制終止；另一次是第一次執行就真正測試失敗）— 詳見下方 | 無 — internal/storage/sqlite 既有的 WAL + busy_timeout pragma，以及 *sql.DB／*sql.Conn API 已完全足夠；不需要新增套件或 go.mod 項目 | 除了 DAG 的 4 檔預估外沒有其他 | **兩個真實、自行抓到的 bug，而非來自外部相依性的阻礙**：(1) Claim 原本在重新讀取剛認領到的工作時，是透過已連線池化的 *sql.DB 去讀，但同時自己手上仍保留著一個已保留的 *sql.Conn 未釋放，一旦連線池（上限 8 條）被並行呼叫 Claim 的情況打滿，就會讓 database/sql 連線等待佇列中的每個 goroutine 自我死結 — 由 TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce 測試無限期掛起而抓到（該行程必須被強制終止，而不只是斷言失敗）。(2) Claim 原本的 SELECT 只比對 status='scheduled'，因此一筆已過期但仍標示為租用中（leased）的紀錄，在另一個獨立的 ReclaimExpired 呼叫執行之前對它是不可見的 — 由 TestLease_ExpiredLeaseReclaimedByAnotherWorker 在第一次執行就徹底失敗而抓到。兩者皆在提交前修正完畢。 | 對掛起行程的診斷（檢查 `ps -o pid,stat,time`，注意到儘管牆鐘時間已過好幾分鐘，CPU 時間卻幾乎是零）是區分「死結」與「只是慢」的關鍵訊號 — 值得記錄下來，作為本專案未來任何一個高度依賴 `-race` 的並行節點通用的除錯技巧，因為單純的逾時重試只會掩蓋真正的 bug，而不是把它揭露出來 | (1) 任何在 database/sql 連線池之上建構 SQLite 租約／鎖定原語的節點，都應該明確預留時間來處理正是這一類的自我死結 — 只要保留住一個 *sql.Conn，同時又有任何程式路徑會在第一個連線仍握著的狀態下再向連線池要求第二個連線，就是一個潛在的掛起隱患，而且要等到並行測試真正把連線池打滿時才會顯現出來。建議在 agents/runtime.md 或一份共用的「SQLite + Go 模式」筆記中明確點出這一點，供未來任何建構類似原語的角色參考（例如 checkpoint 自己的鎖定機制，若有的話）。(2) 當 DAG 的驗證指令直接點名某個必要測試的措辭（例如「expired lease reclaimed」「duplicate workers yield one resume」），應把該措辭當成實際的驗收標準，並以最字面的方式優先寫出這個測試 — 這個節點的第二個 bug 若能在撰寫 Claim 實作的 SELECT 子句之前、而不是之後，就先寫出過期租約的測試，其實可以更早被抓到 |
| runtime-b03 | M（DAG：300 LOC，3 個檔案） | S/M — 比 M 級預估更輕；幾乎純粹是針對已凍結介面的組合工作，樣貌與 runtime-b02 自身的 S/M 經驗相似 | 3 | 3（doc.go、evaluate.go、evaluate_test.go） | 300 LOC | 一次連續作業完成，無需重做 | 無 — internal/gitx（checkpoint 的 Git 銜接層）以及已凍結的 app.EvaluationService／app.ProgressTreeService 介面已完全足夠；唯一的設計決策（不新增 resolver port）是範疇邊界的判斷，而不是缺少相依性 | 除了 DAG 的 3 檔預估外沒有其他 | 無 | 無 — 在撰寫 EvaluateRequest 的欄位集合之前，先讀過 agents/runtime.md 中明確的 6 步驟流程清單，避免了在決定「不新增 resolver port」這個範疇邊界後還需要重寫 | fail-open／fail-closed 的區分（維運性觀察 vs. 實際的決策步驟）在本波四個 Part B 節點中的三個（b03、b04、b08）反覆出現，是一個乾淨、可重用的模式 — 值得在 agents/runtime.md 或某份共用文件中，把它提升為一個明確、有名稱的慣例，因為本波中它被獨立重新推導了三次（每次都正確、一致），而不是引用自同一個地方 |
| runtime-b04 | M（DAG：350 LOC，5 個檔案） | M — 預估成立；真正的複雜度在於要一致地把 FOUR 個不同 hook 指令各自的解析／正規化／評估／回應邏輯銜接起來，而不是其中任何單一部分 | 5 | 5（hooks.go、hooks_test.go、hook.go 修改、wiring.go 修改、wiring_test.go 修改）— 只要把「修改而非新增」的檔案用同樣方式計入，就與 DAG 的檔案數相符 | 350 LOC | 一次連續作業完成，無需重做 | 無 — claude-provider-04 的 parsers／Normalizer 已完全足夠且已整合完成，任務簡報中已明確確認這一點 | 除了 DAG 的 5 檔預估外沒有其他 | 無 | 提早一個節點（就是這個節點）在 wiring.go 中建立 `replaceSubcommand` 這個重構點，而不是等到第三個節點也需要相同的「尋找－移除－重建－加入」模式時才做，這件事在 runtime-b05／b08 中立刻回本（兩者都零重複地重用了它） | 建議明確訂立「在第二次要複製貼上某個模式時，就加入可重用的 wiring 輔助函式，而不是等到第三次」這條規則 — 這個節點小小的主動重構（在只多銜接一個子樹 hook 時就抽出 replaceSubcommand），讓接下來兩個節點的 wiring 變更變成極小的差異，而不是三次各自獨立的複製貼上 |
| runtime-b05 | M（DAG：300 LOC，3 個檔案） | M — 預估成立；順序保證本身很容易正確實作（兩次依序呼叫，第一次出錯就提早回傳），但鑑於 High 風險評級，需要的是一個真正有說服力的測試，而不只是實作 | 3 | 5（checkpoint.go、checkpoint_test.go、cli/checkpoint.go、wiring.go 修改、wiring_test.go 修改）— 比 DAG 的 3 檔預估多一個檔案；CLI 建構子與 wiring 的變更沒有被單獨編列預算，與先前波次的觀察一致：對於 orchestrator 節點，DAG 的檔案預估低估了 CLI + wiring 的銜接工作 | 300 LOC | 一次連續作業完成，無需重做 | 無 — StateCheckpointService 與 RepositoryCheckpointService 的 fake 都已完全足夠；不需要新增 fake 方法或 DTO 欄位 | cli/checkpoint.go，超出 3 檔預估之外（見 actual_files_changed） | 無 | 無 — 順序測試（記錄兩個 fake 實際被呼叫的順序，而不只是斷言兩者最終都被呼叫）是在實作之前就寫好的；這次沒有抓到任何問題，但若真的存在一個意外調換順序的錯誤，這個測試會立刻抓到 | 建議對任何未來「先呼叫 service A、再呼叫 B，絕不能反過來」的 orchestration 節點，都要求在同一個節點中同時具備「記錄呼叫順序」與「B 的 mock 記錄是否曾被呼叫」這兩種測試，就像這個節點所做的一樣 — 只斷言「兩者都被呼叫過」不足以證明順序，而只斷言「回傳了正確的錯誤」也不足以證明 B 從未被呼叫到 |
| runtime-b08 | S（DAG：200 LOC，3 個檔案） | S — 預估成立，是本波六個節點中風險評級最低的一個 | 3 | 6（diagnostics.go、diagnostics_test.go、cli/diagnostics.go、cli/diagnostics_test.go、wiring.go 修改、wiring_test.go 修改）— 是 DAG 3 檔預估的兩倍；這是目前為止最清楚的一次（b05 也曾出現）反覆模式的實例：DAG 的每節點檔案數，並未分別為 orchestrator 層檔案、其測試、CLI 層檔案、其測試，以及 wiring 變更編列預算 — DAG 視為一個節點的東西，實際上有五個不同的檔案「位置」 | 200 LOC | 一次連續作業完成，無需重做 | 無 — *sqlite.DB 在結構上就滿足本地宣告的 DBPinger 介面，不需要任何轉接程式碼，這點透過一個用完即丟的編譯期斷言確認（建立後即捨棄，未提交） | cli/diagnostics.go、cli/diagnostics_test.go（見 actual_files_changed） | 無 | 這個用完即丟的「型別 X 是否滿足介面 Y」編譯期檢查（建立一個暫時套件、隨即刪除）成本很低，避免了憑猜測寫出 DBPinger 的方法集合、卻要等到 wiring.go 嘗試使用時才發現不匹配 | 建議把「orchestrator + CLI + wiring 節點的檔案數被低估」這項觀察（目前已出現 3 次：b03／b04 較輕微的情況、b05、b08）一般化，變成未來任何 Part-B 型節點明確的 DAG 預估調整：把 orchestrator 檔案 + orchestrator 測試 + CLI 檔案 + CLI 測試 + wiring 差異，編列為五個獨立的位置，而不是併入純邏輯節點所用的同一套 3-4 檔預估 |

## 第 5 波跨節點觀察

- 本波一次完成了此角色目前所有已解鎖的前緣工作（六個節點），是此角色收到過最大的單波指派。
  把 Part A（兩者皆為 High 風險：狀態機與並行正確性）排在 Part B（四個相對低風險的
  銜接節點）之前的順序安排，效果正如預期 — 兩個 Part A 節點的測試都在提交前抓到了真實的
  bug（尤其是 runtime-a06 自行抓到的兩個並行 bug），而全部四個 Part B 節點都落在或低於
  DAG 預估，且無需重做。
- 本波單一影響最大的技術教訓來自 runtime-a06：**建構在 database/sql 連線池之上的租約原語，
  可能以一種看起來像掛起、而不是斷言失敗的方式發生自我死結** — 而找出它的診斷技巧
  （比較行程 CPU 時間與牆鐘時間，以區分「被卡住」和「只是慢」）值得作為一個有名稱的技巧
  延續下去，適用於本專案未來任何高度依賴並行的節點，而不只是這一個。
- fail-open／fail-closed 的區分（維運性觀察可優雅降級；實際的決策／變更步驟則原樣往上
  傳遞錯誤），本波中由三個不同節點（b03、b04、b08）各自從同一份 ADD §17.5 來源材料獨立
  正確地重新推導出來 — 這有力地證明了這個模式本身是健全的，但也表示應該把它明確地
  寫下來一次，而不是讓每個節點都各自重新推導。
- 一個較小、但反覆出現的預估教訓：對於任何交付內容橫跨 orchestrator 邏輯 + CLI 指令 +
  wiring 整合的節點（本波每個 Part B 節點皆是如此），DAG 的檔案數預估都一致地低估了
  大約一倍，因為它沒有在 orchestrator 檔案／測試之外，另外為 CLI 層檔案／測試以及 wiring
  差異編列預算。這在 b05 中可見，在 b08 中最為明顯（預估 3、實際 6）。日後對這類節點形態
  值得調整 DAG 的預估慣例。
- 本波不需要新的 ADR，也沒有任何凍結契約需要提出變更請求上呈 — 每一項 fake 相依性都已由
  任務簡報預先授權，而每一項設計判斷（狀態名稱調和、不新增 resolver port、Claim 放寬後的
  判斷條件、replaceSubcommand 重構）都能從已凍結的文件（Constitution §2 優先順序、
  CONTRACT_FREEZE.md、ADD 各章節）中得到解答，不需要向 contract-integrator 提出新問題。

# 經驗教訓 — runtime（第 6 波：runtime-a03、runtime-a04、runtime-a07）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a03 | M（DAG：300 點，約 3 小時） | S/M — 一旦把 ADD §17.6 中精確的數值參數（0.80 門檻、5 秒間隔、30 秒配額新鮮度、0.70 重置區間）轉寫成有名稱的常數、而不是內嵌的魔術數字，防彈跳／遲滯（debounce／hysteresis）邏輯本身就只是機械性工作 | 2（隱含） | 2（observe.go、observe_test.go） | 300 點 | 一次連續作業完成，無需重做 | 無 — internal/domain.RunwayForecast 既有的指標型別欄位（HitProbability、QuotaObservedAt、CurrentUsedPercent、EstimatedTimeToLimitP50Seconds）已完全足夠；不需要新增任何 domain 欄位 | 除了 2 檔預估外沒有其他 | 無 | 無 — 在敲定 resetsArm 的邊界條件（< 還是 <=）之前，先寫出遲滯重置的測試（一個維持在 >= 0.70 的中間樣本，絕不能清除觸發狀態），避免了一個一行的邊界誤差 bug，若測試寫得不夠字面化就會漏掉 | 任務簡報明確指示要在寫任何程式碼之前，先閱讀 agents/runtime.md 的「Day-one realism」章節（校準式 + 緊急式，各自不同的原因代碼），這讓兩條觸發路徑的設計變成單純查閱，而不是判斷 — 建議對任何未來含有硬編碼門檻值的節點，都點出這種模式（明確指出防彈跳／門檻節點必須從哪一個 ADD 子章節轉寫數值參數），因為這裡的數值轉寫錯誤會是一個沉默、難以測試出來的 bug，而不是編譯期就能發現的錯誤 |
| runtime-a04 | M（DAG：300 點，約 4 小時） | M — 預估成立；RequestPause 的冪等性邏輯本身很簡單，但設計 PauseStore 的範疇（內部縫隙 vs. 在 internal/app/ports.go 新增介面）需要在寫任何程式碼之前，刻意檢查 Constitution §7 rule 10，以避免對這個角色並不擁有的凍結 ports 檔案做出臆測性的擴增 | 3（隱含：requestpause + safepoint + 一個測試） | 4（requestpause.go、requestpause_test.go、safepoint.go、safepoint_test.go）— safepoint 被拆成自己獨立的檔案／測試對，而不是併入 requestpause.go，因為這兩項交付內容（冪等性 vs. 順序性）各自有獨立的必要測試，且沒有共用狀態 | 300 點 | 一次連續作業完成，無需重做 | 無 — runtime-b05 既有的 internal/orchestrator/checkpoint.go 順序模式（先 state 後 repository，第一次出錯就提早回傳），直接移植到 safe-point 邊界上，不需要任何新技巧 | 除了上述的 4 檔拆分外沒有其他 | 無 | 無 — 在撰寫 safepoint_test.go 自己的 recordingPersister／recordingInterrupter 之前，先讀過一次 internal/orchestrator/checkpoint_test.go 記錄呼叫順序的 fake 模式，避免了重新發明（或規格不足地重做）「斷言順序、而不只是存在」這項技巧，而這正是 lessons_learned 中已從 runtime-b05 標記為通用建議的內容 | 證實了 runtime-b05 自身的建議（第 5 波 lessons_learned）在一波之後確實承受住了考驗：「同時要求呼叫順序記錄測試，以及 B 的 mock 是否曾被呼叫的測試」被乾淨地移植到一個新的 orchestration 邊界（safe-point 先持久化再中斷）上，零重新發現成本，因為它被寫了下來，而不是只留在某一個節點的記憶裡 |
| runtime-a07 | M（DAG：300 點，約 3 小時） | S — 比 M 級預估更輕；runtime-a06 既有的 ReclaimExpired，幾乎給了 Restart 所需的全部 SQL 樣貌，因此真正的設計工作只在於決定「無條件釋放」還是「以過期為門檻的釋放」語意，而不是撰寫新的查詢邏輯 | 2（隱含） | 2（restart.go、restart_test.go） | 300 點 | 一次連續作業完成，無需重做 | 無 — internal/scheduler 既有的 DB／Clock／IDGenerator 縫隙以及 wake_jobs schema 已完全足夠；不需要新的 migration 或 domain 欄位 | 除了 2 檔預估外沒有其他 | 無 | 無 — 在撰寫 Restart 的判斷條件之前，先重讀 ADD §28.3 的啟動時調和清單，以及崩潰一致性矩陣中「wake job 已租用、daemon 卻死掉」那一列，避免了一開始逐字複製 ReclaimExpired 那個以過期為門檻的 WHERE 子句 — 那樣做在技術上會編譯通過、也能通過一個租約已過期的測試，卻會默默無法通過實際的必要測試（「restart 能救回 wake job」，且是一個*尚未*過期的租約） | 建議在任何未來以「行程重啟」變體來延伸既有租約／鎖定原語的節點中，都明確說明新行為應該是時間門檻式（像原語正常運作時的清掃）還是無條件式（像這個節點）— 這兩者很容易被混為一談，因為它們都碰觸相同的資料列與相同的狀態值，但正確性依據不同（經過的時間 vs. 行程確定性死亡），而且只有其中一種會滿足一個字面上以「restart」命名的必要測試 |

## 第 6 波跨節點觀察

- 這是此角色至今最快、摩擦最小的一波：三個節點都是純粹、自成一體的新增內容，建構在已凍結、
  已測試過的 Part A 先前成果之上（`runtime-a02` 的狀態機、`runtime-a06` 的租約儲存），
  不需要任何跨角色 fake，除了每個節點直接的實作 + 測試對之外也沒有新增任何檔案
  （`runtime-a04` 的 4 檔拆分是刻意的兩項關注點分離，而不是範疇蔓延）。
- 從先前波次重用、影響最大的單一技巧，是 `runtime-b05` 記錄呼叫順序的 fake 模式（第 5 波
  lessons_learned 明確建議的內容），直接套用到 `runtime-a04` 的 safe-point 順序測試上，
  零重新發現成本 — 這直接證明了把一項技巧寫下來、變成一個有名稱的建議（而不是只留在
  某一個節點的記憶裡），能夠跨波次回本，而不只是在同一波內回本。
- `runtime-a07` 是本波唯一一個真正「若不字面地閱讀必要測試，就會默默交付出 bug」的案例：
  一開始想直接重用 `ReclaimExpired` 那個以過期為門檻的判斷條件來寫 `Restart`，這樣做原本
  會編譯通過、通過一個過期租約的測試，卻仍然無法通過實際的必要測試（「restart 能救回
  wake job」），因為在重啟當下租約其實尚未過期的情況更貼近現實。改從「restart」在
  分類上究竟意味著什麼（現有的每一個租約持有者都確定已死，僅此而已）重新推導判斷條件，
  而不是從最接近的現有程式碼下手，避免了這個問題。這與第 5 波自身的教訓一致：把 DAG
  字面上的必要測試措辭當成實際的驗收標準，並優先寫出該測試。
- 本波沒有新的 ADR、沒有變更請求上呈、也沒有凍結契約的相關問題 — 每一項設計判斷
  （PauseStore 的範疇、TriggerReason／Boundary 作為套件內部詞彙、Restart 無條件釋放的
  語意）都能直接從 Constitution §6／§7、CONTRACT_FREEZE.md，以及相關的 ADD 章節
  （§17.6、§20.2、§20.4、§28.3、§29.6）中得到解答，不需要上呈。

# 經驗教訓 — runtime（第 7 波：runtime-a05、runtime-b07）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a05 | XL（DAG：500 點，約 4 小時） | XL — 預估成立；這確實是本波最難的節點，正如 DAG「整個 DAG 中風險第二高的任務」這個定調所預測的，因為五個寫入邊界中有兩個，在崩潰注入下需要一個真正的跨角色 service（Repository Checkpoint），而不只是一個 fake | 4（隱含：orchestrator 檔案 + 測試，另 2 個修改） | 4（persistphase.go、persistphase_test.go、requestpause.go 修改、lease.go 修改）— 只要把「修改而非新增」的檔案用先前波次同樣的方式計入，就與預估相符 | 500 點 | 一次連續作業完成，無需重做，但這是此角色至今建構過最重的一套測試骨架（真正經過 migrate 的 SQLite DB + 真正的暫存 Git repository + 真正的 repocheckpoint.Service，疊在 6 個崩潰注入測試之下） | 一個真實、自行發現的缺口：`scheduler.Store` 沒有依 (pause_id, job_kind) 查找的存取方法，而這在一次重試的 `Schedule` 呼叫撞上自身的 UNIQUE 限制之後，需要用來取回一個已排程 wake job 的 ID（正好是 phase 5 自身提交與本套件對結果做帳之間的崩潰時間窗） | 除了 4 檔預估外沒有其他 | 第一次執行測試就因外鍵限制錯誤而失敗（`wake_jobs.pause_id` 參照了真正的 `pause_records` 資料表，但這個節點的 `PersistPauseStore` 卻是一個記憶體內的 `pause.MemStore`）— 需要在每個測試中，除了記憶體內的紀錄之外，也一併種入一筆真正的 `pause_records` 資料，並且明確記錄下「一筆概念上的紀錄卻有兩個後端儲存」這個缺口，而不是默默地繞過它 | 無 — `HaltAfter`／`HaltError` 崩潰注入技巧是在寫任何程式碼之前，直接從 `internal/progress/complete_node_crash_test.go` 讀來的，完全依照任務簡報的指示，並且零重新設計地直接移植過來 | 在更大的規模上證實了第 6 波自身的教訓（把字面上的必要測試措辭當成驗收標準）：「每個階段之後崩潰都能正確恢復／調和」被字面地解讀為，除了「存在某個崩潰測試」之外，還必須要求每個階段邊界各有一個測試，加上一次完整的調和掃描 — 建議日後只要新節點的必要測試與程式碼庫中其他地方既有的模式相符，就持續明確點名要仿效的確切先例檔案／測試（就像這份任務簡報所做的一樣），因為這把一個真正困難的設計問題（5 個獨立寫入邊界、沒有單一扁平交易）變成了套用一項已證實技巧的機械式工作 |
| runtime-b07 | M（DAG：300 點，約 4 小時） | M — 預估成立；四個指令表面而不是一個，讓這個節點感覺比 runtime-b05 單一指令的先例更大，但每個表面各自都很簡單（針對已經是真實內部實作的薄層 orchestration） | 3（隱含） | 9（lifecycle.go、lifecycle_test.go、requestpause.go 修改、requestpause_test.go 修改、pauselifecycle.go、pauselifecycle_test.go、cli/pause.go、wiring.go 修改、wiring_test.go 修改）— 大約是 DAG 預估的 3 倍，與第 5 波已標記過的觀察一致：Part-B 型節點（orchestrator + CLI + wiring）被 DAG 低估，這次在更大的指令表面數量下再次得到證實 | 300 點 | 一次連續作業完成，無需重做 | 無 — 這是第一個 DAG 的相依性明確標示為「同一分支，不需要 fake」的節點（`internal/pause` 與 `internal/scheduler` 都是此角色自己先前的成果），這一點完全如描述成立：pauselifecycle.go 中任何地方都沒有使用 fake | requestpause.go／requestpause_test.go 修改（PauseStore 介面的擴充弄壞了一個既有的測試 fake `fakePauseStore`，需要新增兩個 stub 方法）— 未計入 DAG 的 3 檔預估 | 無 | 無 — 一旦內化了 runtime-b05 那套 CLI + wiring 模式（真正的 orchestrator 函式、真正的 Cobra 建構子、wiring.go 中的 `replaceSubcommand`），要把它套用到四個指令而不是一個，就只是機械性工作 | 「PauseStore 介面擴充弄壞一個既有測試 fake」的成本（這裡只花約 2 行就修好）本身就是一個小而可一般化的教訓：任何擴增內部、由套件自身擁有的介面（不只是凍結的 `internal/app/ports.go`）的節點，都應該在認定變更完成之前，對整個模組 grep 每一個既有的實作者，*包括*只用於測試的 fake，因為 `go vet`／`go build` 雖然會抓到，但那是事後才發現 — 建議把「在整個模組中 grep `var _ InterfaceName = `」訂為任何介面擴增節點提交前的標準最後一步 |

## 第 7 波跨節點觀察

- 本波的兩個節點之所以把 Part A 排在 Part B 之前，具體原因是 `runtime-b07` 對 Part A
  的內部實作（`pause.RequestPause`／`Cancel`／`Resume`、`scheduler.Store`）有真實的、同一
  分支的相依性 — `runtime-a05` 本身並沒有解鎖任何 `runtime-b07` 需要的東西（兩者是對相同
  底層套件各自獨立的新增內容），但先建構 persist-phase 的 orchestration，讓本波風險最高
  的工作排在最前面，與先前每一波已陳述的排序理由一致（Part A 的狀態機／並行正確性風險
  永遠先於 Part B 相對較低風險的銜接工作）。
- 本波單一最大的技術教訓來自 `runtime-a05`：要在 FIVE 個獨立的持久化儲存（其中兩個是真正的
  跨角色 service）之間協調一個崩潰注入的證明，測試難度實質上遠高於單一交易的協定，但底層的
  技巧（`HaltAfter`／`HaltError`、重播時冪等跳過）能夠不做任何修改，就從
  `internal/progress.CompleteNode` 的單一交易情境，一般化套用到這個節點的多重儲存情境 —
  困難之處在於測試骨架（真正的 DB、真正的 Git repo、為一筆概念上的紀錄種入兩個後端儲存），
  而不是核心演算法。
- 來自 `runtime-a05` 的第二個、較小的教訓：當一個節點自身的必要測試，需要一項手足套件
  沒有對外暴露的能力時（此處為 `scheduler.Store` 在限制衝突之後依自然鍵救回一個工作），
  應先檢查目前這個角色是否擁有那個套件，再決定是否要當成需要上呈的阻礙來處理 — Part A
  同時擁有 `internal/pause` 與 `internal/scheduler`，因此這個缺口直接在同一個節點中補上，
  這比針對一件本來就在範疇內的事情提出跨角色變更請求，更快也留下更乾淨的紀錄。
- 本波沒有新的 ADR、沒有變更請求上呈、也沒有凍結契約的相關問題。唯一被明確追蹤的缺口
  （`PersistPauseStore` 的記憶體內後端 vs. 真正的 `pause_records` SQL 資料表）刻意保持
  開放，留給後續的整合節點處理，而不是默默地解決掉，這與此角色一貫記錄而非隱藏已知
  不完整之處的做法一致。

# 經驗教訓 — runtime（第 8 波：runtime-a08）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a08 | L（DAG：350 點，約 3 小時） | L — 預估成立；四項檢查本身各自都很簡單，但要設計 repository 指紋檢查自己的窄縫隙（不是直接相依於 internal/gitx，而是仿照 safepoint.go 既有的 CheckpointPersister／Interrupter 先例），並把 fail-closed 情境下「錯誤 vs. CheckResult」的區分做對，需要真正的設計反覆修正 | 2（隱含） | 2（resumevalidation.go、resumevalidation_test.go） | 350 點 | 一次連續作業完成，過程中自行抓到一個真實的設計不一致（詳見下方） | 無 — app.RepositoryCheckpointService.Verify 與 app.EvaluationService.ConsumeAuthorization 已凍結的簽章已完全足夠；internal/domain.ReasonRepositoryChangedDuringSleep（已是一個凍結的 ReasonCode）被以引用方式重用，而非重複定義，用於 repo 重疊細節字串 | 除了 2 檔預估外沒有其他 | 沒有外部阻礙 — 有一個內部設計 bug，在提交前被此節點自身的測試套件抓到（詳見下方） | 早期草稿的套件文件宣稱 ValidateResume 在*任何*檢查器發生錯誤時，「會停在第一個出錯的檢查器」，但實際實作（正確地）把下游讀取失敗轉換成一個帶有 _UNAVAILABLE 原因代碼的失敗 CheckResult，而不是一個 Go error — 文件註解與程式碼互相矛盾。TestResumeValidation_ValidateResume_StopsAtFirstErroringDependency（依照那個*過時*的文件宣稱寫成）在對照正確的程式碼時失敗；重新去讀程式碼實際上做了什麼（回報一個原因代碼、繼續執行後續檢查），而不是反射性地「修正」程式碼去配合第一版草稿的文件註解，才是正確的做法，該測試被重寫，改為斷言實際上、也更好的行為 | 當一個節點自己的設計文件註解與自己的測試，跟實作彼此矛盾時，應把這當成一個訊號，重新對照任務所陳述的目標去推導*究竟哪一個*才是正確的（此處：「不受監督程式碼執行前的最後一道防線」意味著要 fail-closed，但完全沒有說明應該由哪一種 Go 層級的機制 — error 還是型別化的結果 — 來承載那個失敗；一個型別化的 CheckResult 原因代碼，對稽核軌跡而言，嚴格來說比一個不透明的 error 更有用），而不是機械式地讓程式碼去配合先寫出來的那一個。建議在 agents/runtime.md 中，為任何未來屬於驗證關卡形態的節點，明確陳述這種雙通道區分（相依組合性 bug 的 error vs. 型別化結果的檢查失敗），因為這是一項設計決策，而不是實作細節，而這個節點必須從 Constitution §6（在狀態完整性邊界上要 fail-closed，而非 fail-open）並結合 0052_resume_attempts.sql 自身 schema 所隱含的稽核軌跡要求（failure_code 欄位）中，把它推導出來 |

## 第 8 波跨節點觀察

- 儘管風險評級為 High、規模為 L，這卻是此角色以檔案數而言交付過表面最小的節點（2 個檔案）
  — 與這個模式一致：一個沒有跨角色 fake 銜接、也沒有 CLI／wiring 銜接工作的純邏輯節點
  （這個節點既不觸及 `internal/orchestrator`，也不觸及 `internal/cli`，不同於此角色大多數
  的 Part B 節點），會落在接近其點數預估的地方，而不會出現第 5／7 波 lessons_learned 中
  對 orchestrator + CLI + wiring 型節點所標記的檔案數膨脹。
- 本波唯一真正的教訓，是在提交*之前*、而不是之後，抓到了文件註解／測試／實作三方之間的
  矛盾：依照任務簡報自身的必要測試清單（「不安全的配額重新排程」、「repo 重疊時阻擋」、
  「不相關的 repo 變更依已設定的政策執行」）字面地寫出必要測試，首先揭露出早期文件註解中
  關於 error vs. CheckResult 處理方式的宣稱，與程式碼（正確地）所做的不相符，而一旦回溯到
  第一原理（Constitution §6 的 fail-closed + 稽核軌跡 schema 自身的 failure_code 欄位），
  正確的解法是修正*文件*與*對不上的測試*，而不是修正本來就正確的程式碼。
- 這個節點所使用的每一項相依性都已經是真實且可合併的（checkpoint-b04 的
  RepositoryCheckpointService，自第 5 波起就已整合），除了任務簡報中明確指名要用 fake 的
  那一項（predictor-10 的授權強化工作，本波同期的手足節點）— 這個節點中任何地方都不需要
  未宣告的 fake。
- 本波沒有新的 ADR、沒有變更請求上呈、也沒有凍結契約的相關問題。這個節點三個新的窄縫隙
  （QuotaSnapshotReader、RepoFingerprintReader、SessionCapabilityReader）全部都是
  internal/pause 套件內部的，遵循先前波次中 safepoint.go／persistphase.go 所建立的「只取
  這個節點需要的最窄縫隙」紀律 — 其中沒有任何一個擴增 internal/app/ports.go，此角色從未
  動過這個檔案。

# 經驗教訓 — runtime（第 9 波：runtime-a09、runtime-a10、runtime-b06）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a09 | M（DAG：300 點，約 3 小時） | M — 預估成立，但原因是 DAG 的風險評級低估的：`lifecycle.go` 既有的 `Cancel`／`Resume`（來自*更早*一波的 `runtime-b07`）存在一個真實、已交付的 TOCTOU 競態（`GetByID` 之後無條件執行 `UpdateStatus`），而不只是缺少測試 — 這個節點的工作，從「為一項既有且正確的保證補上測試」，變成「發現這項保證其實並不成立，然後修正它」 | 3（隱含：2 個新檔案 + 1 個修改） | 6（requestpause.go 修改、requestpause_test.go 修改、lifecycle.go 修改、wake.go 新增、wake_test.go 新增、splitbrain_test.go 新增）— 是隱含預估的兩倍，完全是因為修正 TOCTOU 競態需要擴增 `PauseStore` 本身（`CompareAndSwapStatus`），這是 DAG 的節點描述沒有預見到的，因為它假設 `Cancel`／`Resume` 本來就是正確的 | 300 點 | 一次連續作業完成，過程中自行抓到一個真實的測試設計 bug（詳見下方） | 一項未宣告、自行發現的相依性：`PauseStore` 需要一個在這個節點之前並不存在的新原子原語（`CompareAndSwapStatus`）— 這不是因為 DAG 把 Part A 的介面範疇定得太小，而是因為既有的 `Cancel`／`Resume` 實作（看起來正確、已經合併、也已針對它*自己*的非並行測試案例測試過）在此節點的必要測試要求它之前，從未在真正的並行情況下被驗證過 | 除了上述總計 6 檔外沒有其他 | 沒有外部阻礙 | **本波最有價值的一刻**：`TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState` 的早期草稿斷言「Cancel／Wake 恰好只有一個會成功」，並且在第一次執行就穩定失敗（而不是偶發性的）。調查原因時（加入暫時性的除錯輸出，因為失敗模式 — 兩次呼叫都回報成功 — 乍看之下像是一個真實的重複恢復 bug）發現其實是*斷言*錯了，而不是實作：`WakePending` 確實合法地存在一個 `EventCancel` 邊（statemachine.go），因此 Wake 之後緊接著 Cancel、稍後落到 WakePending -> Cancelled，是「cancel 勝出」按*設計*運作，而不是違反規則。正確的修法是重寫測試本身的性質（Cancel 最終必定贏得*這場*競態；最終狀態永遠是 Cancelled），而不是把它弱化成「配合實際發生的結果」。這些除錯輸出檔案在調查過程中被寫進一個真實的原始檔、確認發現後，在提交前刪除 — 這是第 5 波 `runtime-b08` 教訓中已命名的「用完即丟的編譯期／行為檢查，建立後即捨棄」技巧的一個真實但小型的例子 | 本波從*另一個*方向強化了第 5／6 波已陳述過的教訓：不僅應該把字面上的必要測試措辭當成驗收標準，而且當你剛寫好的測試在*第一次*執行就失敗（而且是穩定的，不是偶發的）時，預設的假設應該是「我測試中的斷言，對系統編碼了錯誤的心智模型」，並且要在假設「實作有 bug」之前先檢查這一點 — 兩者都是活生生的可能性，而這個節點在同一個工作階段中，接連發現了一個真實的實作 bug（TOCTOU 競態，確認為真並修正）與一個測試建模上的 bug（過度嚴格的「恰好一個成功」斷言，確認為誤並修正），並且以相同的方式處理兩者（暫時性除錯儀器化，接著做出有原則的修正，絕不憑猜測），這正是值得為任何未來的並行性證明節點明確命名的可重用技巧 |
| runtime-a10 | S（DAG：200 點，約 3 小時） | S — 預估完全成立；一旦回答了唯一真正的問題（這兩個介面中，是否有任何一個帶有超出其裸簽章之外的契約？），程式碼本身（兩個 fake + 一個通用的契約套件函式）就只是機械性工作 | 3（隱含） | 3（provider.go、providercontract.go、providercontract_test.go）— 完全相符 | 200 點 | 一次連續作業完成，無需重做，但在寫任何程式碼之前，先由一個專門的研究步驟（一個背景 agent）打頭陣 | 無 — internal/app/ports.go 的 TurnInterrupter／SessionResumer 依宣告的內容已完全足夠；研究步驟*確認*（而不只是假設）兩者都沒有額外的凍結行為契約，避免了把套件建得太少或太多 | 除了 3 檔預估外沒有其他 | 無 | 無 — 在撰寫契約套件之前，先把「ADD／CONTRACT_FREEZE.md／agents/claude-provider.md 是否有規定任何超出簽章之外的行為」這個問題交給一個專門的研究步驟去處理，避免了成本高得多的失敗模式：寫出的套件要嘛發明了未陳述的不變量（過度擬合），要嘛漏掉了一項真正有記載的不變量（擬合不足） | 建議對任何未來「為一個凍結介面撰寫可重用契約測試套件」的節點，明確編列一個研究步驟的預算（而不只是重讀一次該介面自己的文件註解），以便在為契約撰寫測試之前先確認契約的*範疇* — 光靠介面簽章本身，不足以證明沒有額外的行為契約存在，而這個節點「確認為否」的研究結果，本身就是有價值、可引用的證據，而不是浪費的心力 |
| runtime-b06 | M（DAG：300 點，約 3 小時） | M — 預估成立；一旦流程設計確定下來，orchestration 邏輯本身（issue／consume 兩條流程的拆分）就很簡單，但要針對*真正*的評估管線（而不是 fake）去證明必要測試，需要先反向工程出實際的風險評分公式，而 DAG 的節點描述隱含假設這件事會很直接 | 3（隱含） | 6（decision.go、decision_test.go、decision_realauth_test.go、cli/decision.go、wiring.go 修改、wiring_test.go 修改）— 是隱含預估的兩倍，與此角色先前交付過的每一個 Part-B 型節點一致（第 5／7 波已標記過的 orchestrator + CLI + wiring 低估模式，再次得到證實） | 300 點 | 一次連續作業完成，無需重做，但在此之前，由第二個專門的研究步驟（另一個背景 agent）打頭陣，足夠深入地反向工程出 internal/predictor/risk/combiner.go 實際的評分公式，以便建構一個 fake DataSource，能可靠地把*真正的*管線驅動到特定的風險等級 | 一項真實、有實質影響的相依性：`IssueAuthorization` 存在於具體的 `*evaluation.Service` 上，但刻意*不*被納入凍結的 `app.EvaluationService` 介面之中 — 這一點已被該套件自己的文件註解正確地預見到（在寫任何程式碼之前先讀過），因此這是一個已記載、而非新發現的缺口；儘管如此，仍需要在 `internal/orchestrator` 中新增一個本地的 `AuthorizationIssuer` 縫隙，仿照既有的 `UsageObservationLoader`／`GitSnapshotter` 先例 | 除了上述總計 6 檔外沒有其他 | 沒有外部阻礙 | 研究 agent 的固定資料（fixture）數學運算（預測特定的 `features.PromptFeatures`／`RepositoryFeatures`／`SessionFeatures` 欄位值，能把 `OverallRisk.Score` 驅動到特定等級）在第一次執行測試時就對照*真正*的管線驗證通過 — 高風險 fixture（透過一個暫時性的除錯測試確認，該測試撰寫後立即刪除，用來印出實際產生的 `PolicyAction`：`CHECKPOINT_AND_RUN`，也就是危急等級，而不僅僅是高風險等級）與低風險對照組 fixture（確認為 `PolicyRun`），都與預測完全相符，不需要任何 fixture 調校的反覆過程 | 在更大規模上證實了 `runtime-a10` 同一波的教訓：透過一個專門的研究步驟反向工程出*真正*系統精確的評分／決策公式，*然後*針對已驗證的公式撰寫 fixture，第一次嘗試就產生了可運作的高風險／低風險 fixture 組合 — 建議把「在撰寫任何 fixture 資料之前，先研究真正公式的精確門檻值與欄位對分數的對應關係」這個步驟，訂為未來任何節點的必要測試依賴於把一個真實（非 fake）的評分／分類管線驅動到特定等級時的預設做法，而不是反覆猜測欄位值再重新執行 |

## 第 9 波跨節點觀察

- 本波最重要的發現是：`runtime-a09` 被指派的「補上測試」這個定調，低估了實際的工作量：
  DAG 把 `runtime-a09` 描述成證明 `runtime-a07`／`runtime-a08`「已經在租約認領層級……大致
  上證明過」的保證，但 PAUSE 層級的保證（`lifecycle.go` 中的 `Cancel`／`Resume`，在更早
  一波的 `runtime-b07` 中交付）實際上從未在並行情況下被證明過，而一旦真正測試，就發現
  存在一個真實的 time-of-check-to-time-of-use 競態。這值得記錄為一個一般性風險，適用於
  任何未來針對一個已合併、已審查過的實作，措辭為「為 X 補上必要測試」的 DAG 節點：一個
  自身工作就是「證明這件事成立」的節點，應該為「它其實還不成立」這種可能性編列預算，
  而不只是為寫出證明編列預算。
- `runtime-a09` 以及（各自獨立地）`runtime-b06` 撰寫測試的階段，都各自產生了一個第一次
  執行就穩定失敗的測試，而兩個案例的根本原因都與最初的假設不同：`runtime-a09` 的第一次
  失敗，是測試斷言錯誤（而不是系統 bug）；`runtime-b06` 的 fixture 數學運算運作正確
  （沒有在那裡找到 bug），但 `runtime-a09` 的調查技巧 — 暫時性、原始碼層級的除錯儀器化
  （print 敘述或一個用完即丟的測試），寫進磁碟、執行、然後在提交前刪除，從不留在 diff
  中 — 被刻意地在 `runtime-b06` 中重用，用來確定地*確認*一個 fixture 精確的輸出等級，
  而不是盲目地信任研究 agent 的預測。建議把這項技巧（「暫時性的原始碼內儀器化，確認後即
  刪除，從不提交」）明確命名，訂為本專案的標準除錯步驟，與第 5 波已命名的「行程 CPU
  時間 vs. 牆鐘時間」掛起診斷技巧並列 — 兩者是相同的底層紀律（在接受或拒絕一項假設之前，
  先取得真實、直接的證據），套用在不同的失敗形態上。
- `runtime-a10` 與 `runtime-b06` 都受益於在寫任何程式碼之前，先派出一個專門的研究步驟
  （一個背景 agent），分別處理兩個不同但結構相似的問題：「這個凍結介面是否有超出其簽章之外
  的契約」（a10）以及「究竟是哪些欄位值，把這個真正的評分管線驅動到特定等級」（b06）。
  兩次研究步驟都回傳了已確認、可查核的發現（而不只是看似合理的猜測），並且在程式碼寫出後、
  經過直接驗證仍然成立 — 建議持續把「在撰寫依賴於其精確行為的測試之前，先研究真正的
  系統」訂為此類別中任何未來節點的預設做法，而不是對著真正的管線反覆試錯。
- 本波沒有新的 ADR、沒有跨角色變更請求上呈、也沒有凍結契約的相關問題。`internal/app/ports.go`、
  `internal/domain/**` 與 `internal/evaluation/**` 只被呼叫，從未被修改，完全符合每個節點
  明確的邊界；唯一被擴增的介面（`pause.PauseStore`，新增 `CompareAndSwapStatus`）是此角色
  自己的內部縫隙，而不是一個凍結的跨元件 port，因此不需要上呈 — 與 `runtime-a05` 在第 7
  波為同一類範疇內的內部介面變更所立下的先例一致。

# 經驗教訓 — runtime（第 10 波：runtime-a11、runtime-b09）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-a11 | XL（DAG：550 點，約 10 小時） | L/XL — 就某個意義而言比 XL 預估更輕（2 個檔案，皆為新增，無需重做先前節點自己的測試），但在 DAG 所預測的意義上確實很難：把五個以上各自已獨立正確的套件，組合成一個生命週期證明，恰好浮現出一個需要新生產程式碼的真實缺口，而找出它需要先逐項審查 9 項必要測試，而不是臆測性地寫測試 | 2（隱含：一個整合測試檔案） | 2（interrupt.go 新增、fulllifecycle_test.go 新增）— 就檔案數而言比 DAG 的 XL 規模所暗示的更輕，儘管 fulllifecycle_test.go 本身是此角色寫過最大的單一測試檔案（12 個測試函式、約 700 LOC） | 550 點 | 一次連續作業完成，事前由一個專門的研究步驟（一個背景 agent）打頭陣，過程中自行抓到兩個測試設計 bug（詳見下方） | 無 — 這個節點所組合的每一項相依性（persistphase.go、lifecycle.go、wake.go、resumevalidation.go、scheduler.Store、testutil/fakes）都已經是真實的，也都已經是此角色自己先前的成果；唯一「新」的部分（TurnInterrupterAdapter）是這個節點自己找出缺少的新生產程式碼，而不是外部相依性的缺口 | 除了 2 檔總計外沒有其他 | 沒有外部阻礙 | 兩個，都是這個節點自己第一次執行測試時自行抓到的，都是對照實際的轉換表／設計文件修正，而不是憑猜測：(1) 9 步驟崩潰掃描中一個過度嚴格的斷言「較早的事件絕不會再次觸發」失敗了，因為 EventResumeValid 在轉換表中確實合法地存在兩條不同的邊 — 重新推導出正確的不變量（狀態必須等於緊接在前一步驟自身的輸出，絕不能回退得更遠），而不是把檢查弱化成「不管發生什麼都算」；(2) 一個「BlockedConflict 是終態」的斷言是錯的 — 它刻意*不是*終態（ADD §20.9 中人工 Cancel 的邊）— 這一點是直接對照 statemachine.go 的 terminalStates 集合來檢查的，而不是從「Blocked」這個名字去假設 | 在更大的、整個角色的規模上證實了第 9 波的教訓（runtime-a09）：一個被定調為「證明*整個*堆疊都能正確組合」的節點，應該為「發現某一項必要測試底層的生產環境銜接其實根本還不存在」這種情況編列預算（此處：沒有任何程式碼呼叫 TurnInterrupter、並把結果事件套用到一筆真正的 PauseRecord 上 — safepoint.go 自己的 PersistThenInterrupt，依已記載的設計，在*更早*一波中刻意止步於此），而不只是為證明一件早已成立的事編列預算。事前的專門研究步驟（以 file:line 精確度審查全部 9 項必要測試對照既有涵蓋範圍）讓這個節點得以精確地說出「9 項中 5 項不需要新程式碼，3 項只需要更完整的組合測試，恰好 1 項需要新的生產程式碼」，而不是一句籠統的「全部審查過了，看起來沒問題」 |
| runtime-b09 | M（DAG：250 點，約 3 小時） | M — 在點數上預估成立，但 DAG 自己的 3 檔預估低估了，原因與第 5／7 波已標記過的 orchestrator + CLI + wiring 型節點相同：一項橫切性的修正，除了新的測試檔案與 errors.go 之外，還觸及共用的根指令建構路徑（root.go、wiring.go），本質上就比單一指令節點需要更多檔案 | 3（隱含） | 4（errorcontract_test.go 新增、errors.go 修改、root.go 修改、wiring.go 修改） | 250 點 | 一次連續作業完成，事前由第二個專門的研究步驟（另一個背景 agent）打頭陣，過程中自行抓到一個真實的 bug（詳見下方） | 無 — 每一個真正指令自己的錯誤／成功路徑程式碼本來就正確，不需要變更；這次修正完全在共用的根指令銜接工作中（errors.go／root.go／wiring.go），而這些是此角色本來就擁有的 | 除了 4 檔總計外沒有其他 | 沒有外部阻礙 | 一個真實、有實質影響的：一個早期的設計直覺（「保持 SilenceErrors: false，讓這項 JSON 輸出的新增純粹是附加性的，不改變任何既有行為」）本身就是錯的 — 它讓 Cobra 自己的純文字錯誤行列印，與新的 JSON 封裝*並存*，這直接違反了「machine 模式絕不輸出裝飾性文字」這項本節點自己正試圖滿足的要求。這個問題被這個節點自己的 TestErrorContract_NoDecorativeTextOnAnyCommand 在第一次執行時，對每一個指令都立刻抓到（100% 失敗率，不是偶發性的）。修法是把 SilenceErrors 改成 true — JSON 封裝*取代* Cobra 的預設文字，而不是與其並存 | 「純粹附加、不改變既有行為」這個直覺通常是對的（對於回傳的 Go error 值本身而言*確實*是對的 — 每一個既有的、以 errors.As 為基礎的測試都不需要任何變更），但並不會自動適用於一項修正的每一個面向；這個節點的 bug 是一個更一般性教訓的具體、可命名案例：當一項修正自身陳述的*目標*（沒有裝飾性文字）與服務於*另一個*目標（不改變既有行為）的設計選擇互相衝突時，應該優先為*陳述的目標*寫測試，讓它來裁決，而不是假設兩個目標在建構上必然能同時滿足。建議把「測試你實際上想滿足的要求，而不只是你想避免破壞的要求」訂為未來任何共用 root／wiring 檔案橫切性修正的一項具名紀律 |

## 第 10 波跨節點觀察

- 本波兩個節點的*形態*，與第 9 波的 `runtime-a10`／第 8 波的 `runtime-a08`，以及此角色
  自身對 `checkpoint-a09`／`checkpoint-b09`／`predictor-11` 這類節點已建立的模式相同：
  屬於全面性的最終證明／審查節點，而不是新功能節點，每一個都在寫任何程式碼之前，先由一個
  專門的背景 agent 研究步驟打頭陣，完全依照任務簡報的指示。兩次研究步驟都回傳了精確、
  附帶 file:line 引用的發現，並在被信任之前，先獨立對照實際的 repository 做過抽查，
  與第 9 波自身「查核研究 agent 的報告、而非盲目接受」的既有做法一致。
- 本波跨*兩個*節點得到驗證的單一最大發現：一個全面性的「這件事是否已經成立」節點，應該
  預期每次審查大約會找到*一個*真正的缺口，不是零個，也不是很多個 — `runtime-a11` 恰好
  找到一個（provider 中斷失敗的生產環境銜接，在審查的 9 項必要測試中），`runtime-b09` 也
  恰好找到一個（沒有 JSON 錯誤輸出層，在審查的完整 P0 指令表面中），每一個都被研究步驟
  確認為已經正確的若干領域所環繞，而不是被不必要地重建。這值得記錄為本專案未來任何
  「一起證明整個堆疊」節點的預期形態：應為「找到*一件*真實的事」編列預算，而不是為
  「什麼都沒找到」（這會有跳過一個真實缺口的風險）或「找到很多事」（這會暗示通往此節點
  之前那些個別節點其實沒有真正做對，而本波這兩個實例都不是這種情況）編列預算。
- 兩個節點各自撰寫測試的過程，都在*這同一個節點*自己的第一版草稿中抓到了一個真實 bug
  （不是在更早節點已交付的程式碼中，不同於第 9 波 `runtime-a09` 在第 7 波年代的
  `lifecycle.go` 中找到真實 bug）— 這與此角色現已確立的跨波次技巧一致，但屬於一個
  不同的子案例：在假設「受測系統壞了」或「測試本身只是偶發性不穩定」之前，先把測試第一次
  執行的失敗，當成「我自己的斷言或設計選擇可能有誤」，並直接查核，而不是憑猜測。
  `runtime-a11` 的兩處修正都是測試斷言的修正（直接對照 statemachine.go 查核）；
  `runtime-b09` 的那一處修正則是真正的設計選擇修正（SilenceErrors），而不只是測試修正
  — 值得明確區分這兩個子案例（測試錯了 vs. 與實作相鄰的設計選擇錯了），因為兩者都是
  第一次執行失敗可能指向的活生生可能性，而不只是此角色第 9 波 lessons_learned 已命名的
  那兩種（測試錯了 vs. 實作錯了）。
- 本波沒有新的 ADR、沒有跨角色變更請求上呈、也沒有凍結契約的相關問題。`internal/app/ports.go`、
  `internal/domain/**`，以及其他每個角色所擁有的套件，都只被呼叫，從未被修改。本波唯一的
  新生產程式碼（`internal/pause/interrupt.go` 的 `TurnInterrupterAdapter`／
  `InterruptAndSleep`）滿足 `pause.Interrupter`，這是此角色本來就擁有的既有內部縫隙
  （而不是一個凍結 port）；`internal/cli/errors.go` 新的 JSON 輸出輔助函式，是在一個
  本來就擁有的檔案中全新的程式碼，而不是擴增任何共用契約。
- `runtime-b10`（此角色在 vertical-slice 中最後一個節點）留待未來一波處理 — 依任務本身的
  指示，本波明確尚未開始。

# 經驗教訓 — runtime（第 11 波：runtime-b10 — 最終節點）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-b10 | L（DAG：450 點，約 8 小時） | L/XL — DAG 自己對重啟測試「High 風險」的定調完全成立，但原因是 DAG 的節點描述沒有點名的：最難的部分不是建構重啟證明本身，而是發現*兩種*同行程崩潰模擬技巧都會給出*錯誤*的結果（一個 `database/sql` 連線池記帳上的產物，而不是真正的 bug），這耗費了一整輪調查，才找到正確的技巧（一個真正的子行程 + SIGKILL） | 4（隱含：重啟測試檔案 + 一個新的 store + 其測試 + 一個 golden 測試檔案） | 7（sqlitestore.go、sqlitestore_test.go、restart_test.go、golden_test.go、3 個 golden fixture 檔案）— fixture 檔案對此角色而言是新領域（先前沒有任何節點交付過 `testdata/` fixture），DAG 的隱含預估並未把它們單獨計入 | 450 點 | 一次連續作業，經歷兩次完整的「停下來－診斷－修正」循環：(1) 測試*自己*的驅動輔助函式中，有一個 cobra flag 狀態外洩的 bug（在多次 `Execute()` 呼叫之間重用同一棵 `*cobra.Command` 樹），發現後改為每次呼叫都建立一個全新的 `RootCmd()` 來修正；(2) 崩潰模擬方法論本身，在兩次行程內嘗試都產生了真實但*具誤導性*的 `SQLITE_BUSY` 之後，需要改用一個真正的子行程 | 兩項，都是在寫任何重啟程式碼之前，透過直接研究自行發現的，而不是憑假設：(1) `pause.PauseStore` 在任何地方都沒有以 SQLite 為後端的實作 — 先前四波中的五個節點各自獨立地在自己的文件註解中延後處理過這個完全相同的缺口；補上它（`SQLiteStore`）在範疇內，因為 `internal/pause` 是此角色自己專屬的路徑，仿照 `runtime-a05` 在第 7 波立下的先例「一個在節點中途發現的同角色內部缺口，直接補上，而不是上呈」。(2)「CLI golden 測試」（agents/runtime.md Part B 自身的 Tests 清單）從未被 `b01` 到 `b09` 中任何一個節點建構過 — 以 grep 確認（`internal/cli` 底下任何地方都沒有「golden」的命中），以一個新的 `testdata/golden/` fixture 慣例補上，仿照 `claude-provider` 對同一技巧已建立的先例 | `internal/cli/testdata/golden/*.golden.json` — 此角色在先前 10 波中從未交付過的一種新檔案*類型*（已提交的 JSON fixture） | 兩次同行程崩潰模擬的嘗試（詳細記錄在 restart_test.go 自己的註解，以及本波進度文件的章節中）在 token 浪費的意義上並*不*算浪費心力 — 每一次都對「為什麼會發生 `SQLITE_BUSY`」產生了一個具體、可證偽、而最終*錯誤*的假設，而透過用完即丟的實驗（隔離出 `sql.Conn.Close()` 已記載的「在一個開啟中的 Tx 上會死結」行為）逐一具體地否證每一個假設，正是讓正確診斷（一個 Go `database/sql` 層級的產物，而不是 SQLite 或儲存層的 bug）真正變得*確定*、而不只是看似合理的關鍵 — 與此角色自身已確立的「信任前先確認」紀律一致，這裡是把它套用到一個除錯問題上，而不是一個 fixture 調校問題 | 本波的重啟安全性調查，為本專案揭露出一個真正*新的*、先前未命名的技巧：**同行程「捨棄一個 `*sql.Tx` 並讓它懸空」的行程崩潰模擬，並不是真正崩潰測試的較弱版本，而是一種*不同*、且可能*具誤導性*的測試** — `database/sql` 自身的連線池／交易記帳機制（已記載的行為：`DB.Close()` 會等待進行中的查詢完成；`Conn.Close()` 在一個開啟中的 `Tx` 上會死結）可能產生一個看起來完全像真正 SQLite 層級鎖定恢復 bug 的失敗，但實際上是 Go 自身連線生命週期規則的產物。 | 建議：任何*未來*的 Auspex 節點（不論此角色或其他角色）若需要測試真正的行程崩潰恢復，都應該一開始就預設採用子行程重新執行 + SIGKILL 的技巧（`os.Args[0]`、`-test.run=^Name$`、一個由環境變數控制的輔助 `Test` 函式、一個真正的 `syscall.SIGKILL`），而不是像這個節點一樣，用困難的方式重新發現「較簡單的行程內近似法，對這一類測試會給出錯誤的結果」這件事 |

## 第 11 波跨節點觀察（單節點波次 — 此角色的最後一波）

- 這是 `runtime` 被指派的最後一個 DAG 節點（`agents/runtime.md` 完整的 Part A + Part B
  範疇，橫跨 9 波共 21 個節點，現已 100% 完成）。不同於此角色先前交付過的每一個「最終
  關卡」節點（跨角色的 `checkpoint-a09`／`checkpoint-b09`／`predictor-11`，同角色的
  `runtime-a11`／`runtime-b09`，全都在第 10 波），這個節點不只結束了 Part B，而是結束了
  *整個*角色剩餘的 DAG 範疇 — 此角色在 vertical-slice 中不再被指派任何進一步的工作。
- 「全面性審查再結案的節點，每個子領域約找到 1 個真實缺口」這個模式（第 10 波首次命名）
  本波第*三*次和第*四*次得到證實：Part A 中一個真實缺口（`PauseStore` 缺少 SQLite 後端，
  一項生產程式碼修正），以及 Part B 自身 Tests 檢查清單中一個真實缺口（「CLI golden
  測試」，一項測試基礎設施修正）— 從不是零個、也從不是很多個，與本專案跨多個角色交付過的
  每一個「證明整個堆疊」節點一致。
- 崩潰模擬技巧的教訓（見上方該節點自己的「token 浪費觀察」欄位）是本波為此角色整個 9 波
  歷程的技巧庫，帶來的唯一一項真正*新*的內容 — 有別於（但建構在相同底層紀律之上）第 5 波
  的「行程 CPU 時間 vs. 牆鐘時間」掛起診斷技巧，以及第 9 波的「暫時性原始碼內儀器化，
  確認後即刪除」技巧。建議把它訂為本專案往後任何角色中，任何崩潰恢復形態測試的預設做法，
  而不是像本波必須重新發現它一次那樣，每個節點各自重新發現。
- 本波沒有新的 ADR、沒有跨角色變更請求上呈、也沒有凍結契約的相關問題 — 與此角色橫跨全部
  9 波不曾中斷的紀錄一致（第 4 波那一項針對 foundation 的變更請求，在第 5 波之前就已解決，
  仍是此角色在整個 21 個節點的歷史中，唯一需要過的跨角色上呈）。`internal/app/ports.go`、
  `internal/domain/**`，以及其他每個角色所擁有的套件，都只被呼叫，從未被修改。
  `internal/pause/sqlitestore.go` 新的 `SQLiteStore` 滿足 `pause.PauseStore`，這是此角色
  自己的內部縫隙（而不是一個凍結 port）— `internal/app/ports.go` 中沒有任何介面被擴增或
  觸碰，與此角色在全部 21 個節點中無一例外維持的紀律相同。

## 全程回顧（第 3 波 → 第 11 波，全部 21 個節點：a01-a11、b01-b10）

- **預估準確度**：沒有跨套件銜接工作的純邏輯 Part A 節點（`a02`、`a03`、`a06` 的核心
  邏輯、`a07`、`a08`）一致地落在 DAG 點數／檔案預估上、或略低於預估。每一個橫跨
  orchestrator + CLI + wiring 的 Part-B 型節點（`b03`-`b09`）一致地跑到 DAG 素樸檔案數
  預估的 2-3 倍，這個模式在第 5 波首次被標記，並在第 7、9、10 波無一例外地再次得到證實
  — 值得寫進 DAG 自身的預估慣例中，供任何未來採用相同執行計畫形態的專案參考，而不是像
  此角色五次各自重新發現它那樣。
- **風險最高的節點每一次都印證了自己的風險評級**：`a05`（XL，「整個 DAG 中風險第二高
  的任務」）需要此角色至今建構過最重的測試骨架；`a06`、`a09`，以及這個最終的 `b10`
  節點，各自找到了真實的並行或行程生命週期 bug（分別是一個自我死結、一個真實的 TOCTOU
  競態，以及一個崩潰模擬方法論的 bug），這些是風險較低節點較輕的測試門檻不會抓到的。
  在此角色的整個歷史中，DAG 自身的風險標籤，一直是預測真實 bug（而不只是 LOC）會出現
  在何處的可靠指標。
- **此角色在 21 個節點、9 波之中，從未需要過一份 ADR** — 這是此角色自身歷史中最有力的
  單一證據，證明 `agents/runtime.md` 加上凍結的 `CONTRACT_FREEZE.md`／`Auspex_ADD.md`
  契約，一波接一波，對於每次都全新接手此角色（波次之間沒有持續記憶）的 agent 而言，
  確實足以讓每一項真實的設計判斷，都能正確地從已發布的權威依據中做出，而不需要發明或
  上呈新的基礎事實。
- **跨波次被重用次數最多的單一技巧**：「當一個剛寫好的測試在第一次執行就穩定失敗時，
  在假設『系統有 bug』或『測試不穩定』之前，先把它當成『我自己的斷言、設計或方法論可能
  有誤』，並在做出選擇之前先取得直接證據」— 在五個不同波次中，至少六個不同的實例
  （`a06`、`a09` 在同一個工作階段中兩次、`a11` 兩次、`b09`，以及現在 `b10` 的崩潰模擬
  方法論 bug）各自獨立、正確地套用了這項技巧，每一次都正確地區分出幾個可能解釋中，
  究竟哪一個才是真的，而不是憑猜測。這是唯一一項值得抽取出來，寫進任何未來多波次、
  多 agent 專案自身共用工程實務文件中的技巧，與 Auspex 自身的領域無關。

# 經驗教訓 — runtime（最終整合關卡修正性追加項目：`pause.Service`）

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-final-gracefulpause-service（不是 DAG 節點） | M/L（由 lead 找出的問題，不存在 DAG 預估） | M — 每一段真正的邏輯都已經存在、也已經測試過；實際的工作是組合、DTO 形狀的轉換，以及發現／記錄兩個真實、先前被延後處理的缺口，而不是新的業務邏輯 | 2（一個新檔案，一個既有檔案擴充） | 4（`internal/pause/service.go` 新增、`internal/pause/service_test.go` 新增、`internal/pause/sqlitestore.go` 擴充 `PersistPauseStore`、本文件 + lessons_learned） | N/A | 一次連續作業完成 | 除了此角色自己本來就擁有的套件（`internal/pause`、`internal/scheduler`）外沒有其他 — 不需要任何跨角色相依性，這一點由一個背景研究 agent 獨立交叉查核確認：沒有任何地方的凍結 port 能把 `SessionID` 解析成 `TaskID` | 無 — 沒有新的檔案類型，不同於 `b10` 的 golden-fixture 先例 | 兩個真實的設計缺口，都是自行發現、並明確揭露出來，而不是默默掩蓋過去：(1) 凍結的 `app.PauseRequest`／`SafePoint`／`ResumeRequest` DTO 沒有攜帶 `TaskID`／`WorktreeID`／配額基準 — 以一個本地宣告的 `SessionContextResolver` 縫隙補上（尚未有真正的實作，明確留給未來的 wiring 節點），仿照 `resumevalidation.go` 自身窄縫隙的先例；(2) `PersistPauseStore` 從未被調和到 `SQLiteStore` 上（自第 7 波起已被五個節點點名為一個缺口）— 由於 `internal/pause` 是此角色專屬的路徑，直接就地補上 | `go vet ./internal/pause/...` 在此節點自身的驗證步驟中抓到一個真實 bug，而不是測試抓到的：`NewService(deps Service)` 的第一版草稿複製了一個包含 `sync.Mutex` 保護之 map 的 `Service` 值。修法是把 `Service` 建構子的輸入拆成一個獨立、不含 mutex 的 `ServiceDeps` 值型別 — 一旦抓到就很便宜，但提醒了一件事：`go vet` 是必要驗證關卡中，正好用來抓這一類 bug 的一部分，而不只是測試已經通過後才跑的形式性步驟 | 以一種新的形態，再一次證實了此角色自身在第 10／11 波所建立的模式：一次全面性的外部審查（這次來自 `contract-integrator-final`，而不是此角色自己的內部審查）恰好找到一個真實、先前被延後處理、也已經自行記錄過的缺口（缺少 `GracefulPauseService` 轉接器），而不是一大片未被發現的問題 — 因為先前每一個碰觸過這個邊界的節點（`a04`、`a05`、`a09`、`b10`）都已經明確地把這個缺口寫下來，而不是默默略過。建議持續維持本專案一貫的紀律：即使補上一個延後處理的缺口不在該節點自身的範疇內，也要在發現它的節點的文件註解中把它明確點名 — 正是這一點讓這次修正性追加項目變成一項快速、範疇明確的組合工作，而不是一場開放式的調查 |

## 最終追加項目 — 跨節點觀察

- 這是 `runtime` 自身最常反覆出現的流程教訓（上方第 11 波的回顧）的一次*證實*，而不是
  一個新的實例：這次追加項目所需要的每一項真實判斷 — `SessionContextResolver` 縫隙的
  形狀、`PersistPauseStore` 調和的欄位對應、`ServiceDeps`／`Service` 的拆分 — 都能直接
  從已發布的權威依據中解決（此套件自身先前的文件註解、Constitution §7 rule 3「明確揭露
  能力缺口」，以及 `go vet` 自身的診斷），不需要上呈，也不需要新的 ADR。
- 不同於此角色交付過的每一個編號 DAG 節點，這次追加項目的起點是一項審查發現，而不是一項
  執行計畫指派 — 值得記下來作為一個正面的資料點：此角色一貫的慣例（為未宣告的能力使用
  窄內部縫隙、在發現缺口的節點中明確記錄、重用既有的整合測試技巧而不是發明新的），乾淨地
  一般化套用到一個來源不同的任務上，而不只是套用到 DAG 自身預先規劃的形態上。
- 就最完整的意義而言，這是此角色實際上最後一項 Day-1 工作：每一個 DAG 節點都已經完成，
  而這次修正性追加項目，補上了那些節點自身文件註解早已標記出的、唯一剩下的具體缺口。
  預期此角色不再有任何進一步的工作。
