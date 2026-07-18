# Phase 2 經驗教訓彙整

> 🌐 [English](Wave2_Lessons.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 值 |
|---|---|
| 階段 | 3.3 — Phase 2 後分析 |
| 來源 | 全部 5 份 `lessons_learned.md` 檔案：`contract-integrator`、`foundation`、`claude-provider`、`checkpoint`、`predictor`（Bootstrap + Phase 1 + Phase 2 條目，共 19 個節點列） |
| 狀態 | 僅為彙整，未變更任何實作。 |

## 1. 依發生頻率排序的重複性問題

以下每個問題都列出所有獨立觀察到該問題的節點。「獨立」這一點很重要：五份經驗教訓檔案彼此在撰寫時都看不到對方的內容，因此跨檔案的重複是一種真實的收斂訊號，而不是同一位觀察者的意見被重複講了五次。

### #1 — DAG 檔案數量估計把實作檔案與測試檔案混為一談（5 次）

觀察於：`foundation-01`、`foundation-02`（隱含地，透過拆分為 4 個檔案、其中估計了 3 個的方式）、`checkpoint-b02`、`predictor-03`，並在 `checkpoint-b03` 的建議中被明確指出為一種一般性模式。這是整個資料集中重複次數最多的單一具體發現。完整分析請見 `Calibration_Report.md` §1。

### #2 — DAG 完全沒有工期欄位、也沒有 token 欄位（4 次明確提及 + 全部 19 個節點皆屬實）

明確指出為缺口的節點包括：`contract-integrator`（「DAG 沒有工期欄位；執行前未追蹤」）、`foundation-01`（「EXECUTION_DAG.md 提供了 LOC 與檔案估計，但完全沒有工期估計／單位」）、`checkpoint-b02`（「不適用 — DAG 沒有工期欄位」），並且對 `Prediction_Error_Report.md` 的每一列都隱含成立。這並非「重複發生的問題」意義上的錯誤重複 — 而是一個結構性缺口，其缺失被 5 個角色中有 4 個獨立注意到並提出。

### #3 — 節點執行途中發生了 harness 層級的工作階段中斷，需要負責人重新驗證（3 次）

觀察於：`claude-provider-03`（Phase 1，在草擬 `stop.go` 與撰寫其測試之間發生 rate-limit 中斷）、`checkpoint-b02`（Phase 1，在實作檔案已寫入、但測試／文件／commit 尚未完成前發生中斷）、`predictor-03`（Phase 1，在草擬 `taskclass.go` 的 enum 與完成分類器之間發生中斷）。這三次都以同樣方式復原：負責人並未信任中斷工作階段的自我回報，而是獨立針對磁碟上現存狀態重新執行 `gofmt`／`go build`／`go vet`／`go test`，接著以「哪些已存在、哪些仍缺少」的精確描述讓隊友繼續工作。三個案例都沒有產生任何重工 — 這次中斷耗費的是負責人驗證的時間，而非實作時間。

### #2b — 凍結合約缺少某角色實際需要的欄位／port，該角色選擇本地繞道處理而非修改合約（3 次）

觀察於：`claude-provider-02`（UserPromptSubmit 沒有凍結的 allow-response 形狀 — 做出並記錄了一個判斷性決定）、`predictor-05`（`app.EstimateScopeRequest` 中沒有 repository／session 的 feature-lookup port — 因而引入了套件內部的 `FeatureSource` 介面，而非修改 `ports.go`）、`foundation-04`（完全不存在凍結的 `Lock` 介面 — 該機制被留給角色自行明確設計決定，而非視為需要升級處理的缺口）。這三個案例中，該角色都留在自己所擁有的路徑範圍內，並將此缺口視為「CONTRACT_FREEZE.md 本身就預期擁有角色可能會發現自己需要額外欄位」，而不是視為需要 STOP 的阻礙。

### #4 — 凍結文件彼此之間存在跨文件不一致（2 次）

觀察於：`claude-provider-06`（`Auspex_ADD.md` 附錄 E.3 與另外三份凍結文件之間，CLI 子指令命名採用 PascalCase 還是 kebab-case 的不一致 — 由負責人直接閱讀 ADD 文字後獨立確認）；以及規模較小的 `foundation-04`（DAG 中過時的全範圍估計，與明確的縮減範圍指示並列未更新，於 `Prediction_Error_Report.md` 中討論）。

### #5 — fixture／測試與其原本要測試的實作結果不一致，需要協調解決（2 次）

觀察於：`claude-provider-03`（`unknown_category.json` 中的 `status_code: 599` 實際上被 5xx 範圍的 fallback 規則正確分類，這代表有問題的是 fixture 本身、而非分類器，與其宣稱要測試的內容不符）以及 `predictor-03`（三個較早期的分類器測試提示，意外觸發了該啟發式演算法刻意設計得較寬泛的關鍵字清單中未預期的比對）。這兩者都是由測試套件本身（`go test` 失敗）發現，而非透過人工審查，且兩者的根本原因相同：fixture 與實作是各自憑直覺平行撰寫，而非由同一份共用的決策表推導而來。

### #6 — 一個既存／殘留檔案在被信任之前需要先檢查（1 次，但具有明確可推廣的教訓）

觀察於：`claude-provider-01` — 5 個 fixture 檔案是先前一次中斷嘗試遺留下來的，在驗證其與 ADD §22.5 的欄位清單相符後才予以保留，而不是盲目信任或盲目捨棄。

## 2. 未預期的相依（列出所有實例，不只重複出現的）

| 節點 | 未預期的相依 | 解決方式 |
|---|---|---|
| Bootstrap | Go 工具鏈版本不符（已安裝 1.19.1，需要 1.26.x） | 儲存庫擁有者核准執行 `brew upgrade` |
| Bootstrap | `go.mod` 的 bootstrap 所有權與 Constitution 自身的路徑所有權規則相衝突 | 儲存庫擁有者裁定：僅限負責人處理，作為單行例外 |
| `foundation-01` | `github.com/google/uuid`、`github.com/spf13/cobra` 及其遞移相依套件 | 任務簡報中已預先預期；實務上並非意外，僅相對於 DAG 沉默地標示「無」而言才算意外 |
| `foundation-05` | `modernc.org/sqlite` 的遞移相依樹（`modernc.org/libc`、`ccgo/v4`、`cc/v4` 等） | 完全在 ADD 純 Go／不使用 CGO 的驅動程式決策預期之內；`go mod tidy` 輸出比先前節點更多，但不是設計上的意外 |
| `foundation-09` | `task`（go-task）與 `golangci-lint` 並未預先安裝；`brew install` 因 Xcode CLT 版本過舊而失敗 | 兩者皆改以 `go install` 安裝至 GOPATH bin 目錄，與模組自身的相依圖無關 |
| `claude-provider-04` | 使用了 `domain.Clock`／`domain.IDGenerator`（已凍結的 port），而非等待 `foundation-06` 尚未建置完成的 `internal/idgen` | 該介接點的第一個使用者；符合 CONTRACT_FREEZE.md 所聲明的彈性 |
| `predictor-05` | `app.EstimateScopeRequest` 缺少 feature-lookup 欄位（見上方 #2b） | 本地 `FeatureSource` 介面 |

本資料集中沒有任何一項未預期的相依需要變更合約、撰寫 ADR，或偏離凍結的路徑邊界 — 每一項都是透過事前授權、有文件記錄的本地繞道處理，或者（兩次，皆為 Bootstrap）儲存庫擁有者的明確裁定來解決。

## 3. 未預期的檔案（所有實例）

| 節點 | 未預期的檔案 | 原因 |
|---|---|---|
| Bootstrap | `go.mod`、`internal/domain/status_test.go`、`pkg/protocol/v1/event_test.go` | 測試檔案未被單獨列為交付項目，但 Completion Definition 有此要求 |
| `foundation-01` | `go.sum` | `go.mod` 變更的機械式連帶結果；DAG 估計似乎並未將其計入 |
| `foundation-02` | 從 `paths_test.go` 拆分出的 `fake_env_test.go` | 為求可讀性所做的選擇，並非範圍蔓延 |
| `foundation-04` | 透過 build tag 拆分出的 `process_unix.go`／`process_windows.go` | 一旦需要真正的跨平台存活檢查，此拆分即無可避免 |
| `foundation-09` | 9 個既有檔案僅因 lint 修正而被觸及 | 首次啟用 lint 回溯揭露了先前程式碼中的問題 — 見 `Calibration_Report.md` §1 |
| `claude-provider-02` | `response_allow.golden.json`、`response_block.golden.json` | 該封包「block/allow response golden files」測試要求所需，但未列於 DAG 的產出物清單中 |
| `claude-provider-06` | `integrations/claude/README.md` | 需要記錄前瞻性的 stub 狀態與命名不一致之處，而非悄悄自行選定一個解法 |
| `checkpoint-b02` | 測試檔案拆成三份（`gitx_test.go`、`resolver_test.go`、`porcelain_test.go`）而非單一檔案 | 設計選擇；DAG 的檔案數量估計可能假設只有一個未區分的測試檔案 |
| `predictor-02` | `doc.go` | 套件層級的隱私邊界文件註解，篇幅小，與新目錄中的第一個檔案自然搭配 |

## 4. 估計失準

`Prediction_Error_Report.md` 與 `Calibration_Report.md` 已對此做了詳盡且量化的涵蓋。此處僅依 Constitution §1（單一事實來源）的原則作為指引性摘要 — 不重複詳述：**變更檔案數與 LOC 皆被系統性低估**（分別有 82.4% 與 100% 的可比對節點超出其 DAG 估計值），且**在凍結的 DAG 中，任何節點都完全未曾估計工期與 token 使用量**。

## 5. 整合問題

**無。** 全部 4 個 Phase 2 分支合併進整合分支的過程中，合併衝突次數為零；在此之前全部 4 個 Phase 1 分支合併也同樣為零（每次整合前皆透過 `git diff --name-only` 進行跨分支重疊檢查來驗證 — 見 Phase 1 整合報告與本次對話中 Phase 2 的驗證步驟）。這是 Constitution 的專屬路徑所有權規則在兩個 phase 中都被每位隊友確實遵守後，一項直接且可衡量的結果 — 在任一個 phase 中，都未發現有兩位隊友觸碰同一個檔案的情形。

## 6. 所有權問題

**一項結構性問題，透過儲存庫擁有者的明確裁定解決，並非隊友違反邊界所致：** Phase 1 的啟動死鎖 — 必須先有 `go.mod` 存在，才能將任何根節點指派給隊友，但 `go.mod` 屬於 Foundation 所有，而 Foundation 本身又卡在 `contract-integrator-07` 上，且 Phase 1 命名的隊友中並沒有 `contract-integrator` 這個角色。此問題透過引入「Bootstrap」— 一個正式獨立、僅限負責人執行、在 Phase 1 之前的階段 — 來解決，這是流程上的修正，而非所有權上的違反。完整的解決過程請見 `docs/adr/` 相關內容與本次對話紀錄。

**沒有任何一次隊友編輯了其他隊友或負責人所擁有路徑的情形。** 在獨立驗證過程中所執行的每一次路徑範圍檢查（兩個 phase、全部 10 個節點，皆個別透過 `git diff --name-only` 對照每位隊友宣告的專屬路徑進行檢查）結果都是乾淨的。

## 7. 值得延續的正面模式（不只是問題）

雖然 Phase 3.3 的提示並未明確要求，但由於數份經驗教訓條目都將其標記為建議事項，若省略不記錄，將無法呈現真正有效的做法，因此仍記錄於此：

- **針對數值行為良好或高誤觸發風險程式碼的 property-based／掃描式（sweep）測試**（`predictor-04` 的 2000 次試驗分位數掃描、`predictor-06` 的約 300 種組合的 runway 掃描）— 撰寫成本低、能及早捕捉問題，兩個節點皆明確建議將其作為未來類似工作的預設模式。
- **單一單調性收斂點**（`predictor-05` 的 `sortTriple`，僅在最後套用一次）被證實比試圖讓每個中間啟發式步驟都各自保持單調更為穩健。
- **Canary 字串隱私測試**（以 reflection 走訪每個字串欄位 + 整個 struct 的 JSON marshal + `%+v` 格式檢查，尋找植入的字面值）— 由 `predictor-02`、`claude-provider-02` 與 `claude-provider-04` 的測試套件各自獨立採用，並被明確建議作為可重複使用的模式，而非每個角色各自重新發明。
- **負責人對每一個「完成」宣告進行重新驗證**（獨立重新執行 `gofmt`／`build`／`vet`／`test`、路徑差異檢查，並至少閱讀一項非瑣碎宣告的實際程式碼）在全部 19 個節點中並未捕捉到任何一次假完成宣告，但正是這項紀律，防止了上述三次工作階段中斷事件（見 #3）演變成無聲的資料遺失。
