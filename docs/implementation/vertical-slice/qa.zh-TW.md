# qa — Progress Artifact

> 🌐 [English](qa.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

## 交接說明（Constitution §6.7 / agents/qa.md）

- **CI 進入點**：`.github/workflows/ci.yml`（qa-01）。只呼叫
  `Taskfile.yml` 的目標（`task fmt`、`task lint`、`task build`、
  `task test`、`task test:short`）——不直接呼叫 `go`／
  `golangci-lint`，所以未來若要改變這些檢查的執行方式（例如新增一條
  lint 規則、一個新的 build flag），只需要改 `Taskfile.yml`／
  `Makefile`，不必動 workflow 檔案，這是遵照本節點「不得與
  `foundation` 的 task runner 重複或衝突」的指示。`golangci-lint`
  本身是透過 `golangci/golangci-lint-action@v6`（釘住主要版本號，工具
  本身的版本用 `version: latest`）安裝於 CI 中，而不是單純 `go
  install`，因為該 action 還會接上 GitHub 原生的 lint annotation，
  或相容於 `.golangci.yml` v2 schema 的設定格式。
- **Race detector 平台拆分**：`task test`（`go test -race ./...`）
  只在 `ubuntu-latest` 與 `macos-latest` 上執行；`windows-latest`
  改跑 `task test:short`（不帶 `-race`）。這是刻意且有記載的選擇
  （見 workflow 檔案內的行內註解），不是疏漏——理由記在下方 qa-01
  節點紀錄的 `assumptions` 裡。
- **尚無 VS Code／JSON Schema／migration 測試的 CI job**：ADD §30.3
  列出了 `ci.yml` 應有的 VS Code lint／test／build job、JSON Schema
  檢查、文件連結／fence 檢查，以及 migration 測試。這一波裡這些目錄
  樹都還不存在（沒有 `vscode/`、沒有 JSON Schema 產出物、也沒有
  `internal/storage/sqlite/migrations/*.sql`——根據
  `docs/implementation/vertical-slice/foundation.md`，`foundation-06`
  仍未完成）。針對根本不存在的路徑搭建 CI job 會違反 Constitution §7
  規則 10（不要做之後某個里程碑才需要、但目前用不到的抽象）——在此
  標記出來，讓未來新增這些目錄樹的 qa／CI 節點記得同步擴充
  `ci.yml`，而不是讓這個缺口被悄悄遺忘。
- **`security.yml`、`provider-contract.yml`、`release.yml`**：ADD
  §30.3 有提到這幾個檔案，但不在 qa-01 的範圍內（根據 execution
  DAG，qa-01 的驗證目標窄定為「CI 在一個 trivial PR 上（Ubuntu／
  macOS／Windows）跑綠」）。這一波沒有建立這些檔案。
- **治理文件**（qa-08）：儲存庫根目錄的 `SECURITY.md`、
  `CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`、`GOVERNANCE.md`，在 ADD
  有明確規定內容之處均逐字依據 `Auspex_ADD.md` §30.7（Governance）
  與 §30.8（Security disclosure），彼此互相交叉引用而非重複規範性
  文字——各檔案內容的確切出處，以及由此浮現、屬於跨角色缺陷的
  LICENSE／NOTICE 缺口，詳見下方該節點的紀錄。

## 節點紀錄

```yaml
node: qa-04
status: completed
artifacts:
  - internal/integrationtest/duplicate_outoforder_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run 'Duplicate|OutOfOrder' -v   # 5/5 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./... -race   # whole repo, all packages PASS, zero regressions"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <記錄於下方>
next_action: 無——qa-04 是本波 qa 的完整任務指派；依任務指示 STOP；向 lead 回報發現以便後續分派
assumptions:
  - "在寫任何測試之前，先做了一次全 repo 的調查（grep 尋找是否有檔案
    同時 import internal/telemetry/claude 與 internal/progress，或
    同時 import pkg/protocol/v1 與 internal/progress；grep 尋找
    adapter/bridge/consumer/dispatcher 相關檔案；並完整讀過
    internal/orchestrator/hooks.go、internal/app/wiring/wiring.go、
    internal/app/ports.go），確認一個已持久化的 claude-provider
    v1.Event，目前在 production 程式碼中究竟（如果有的話）是怎麼
    驅動 internal/progress.CompleteNode.Run 的。調查結果：完全沒有
    任何地方這樣做。完整的證據鏈見下方的 findings 小節，以及本檔案
    （internal/integrationtest/duplicate_outoforder_test.go）自己的
    package doc comment。這個結果決定了測試的設計方式：盡量把真實
    元件串在一起，串到凍結契約（v1.Event、CompleteNodeInput、
    CompleteNodeRequest）實際允許的範圍為止，再加上一個明確標示為
    TEST-ONLY 的 `deriveCompleteNodeInput` 膠水函式，暫時頂替缺失
    的 production adapter——這個函式從未被加進 production 程式碼，
    遵守 agents/qa.md「不得更動 feature production 程式碼」的規則。"
  - "情境 1（重複的 provider 事件，端對端）：用真實的
    claudehooks.ParseStop + claudetelemetry.Normalizer.NormalizeStop，
    對真實的 testdata/provider-events/claude/stop/normal.json fixture
    進行處理，再透過真實的 claudetelemetry.EventStore 持久化進一個
    真實的、落在磁碟上的（temp-file，不是 :memory:）SQLite 資料庫
    ——這個資料庫同時也放著 checkpoint-a07 的
    progress_nodes／node_completions／state_checkpoints 資料表（就是
    真實程序會用的同一個 DB），而不是兩個各自獨立的 in-memory fake。
    驗證了 EventStore 層的去重（CountByIdempotencyKey==1、
    GetByEventID 不變、重送事件自己的 EventID 從未被另外存一份），
    並且用衍生出來的 CompleteNodeInput 確認：以同一真實事件的
    IdempotencyKey 再次嘗試完成時會 replay（Replayed=true、同一個
    checkpoint ID），而不是報錯或重複完成。第二個測試
    （DifferentChannel_DifferentKey_SameEvidence_Replayed）用一個
    真實的 StopFailure fixture 及另外獨立挑選的第二把 key 重覆這個
    流程，專門演練 checkpoint-a07 以 evidence-digest 為準（而非以
    key 為準）的重複偵測邏輯。"
  - "情境 2（送達順序錯亂，端對端）：一個真實的 Stop fixture 事件，
    完全依照 internal/orchestrator/hooks.go 的 HandleStop 在
    production 中的做法，經過真實 pipeline 解析／正規化／持久化，
    再（透過同一個 derive helper）用來觸發一個「子節點」的完成，
    而其「父節點」被刻意留在 `pending`（從未轉為 in_progress）——
    藉此模擬父節點自己的 in-progress 訊號相對於子節點的完成訊號被
    延遲或遺失的情況。確認結果：以 domain.ErrCodeConflict 拒絕、
    Retryable=true（精確符合 checkpoint-a07 文件上記載的語意，而
    不只是「某種錯誤」）；儘管完成被拒絕，真實且已持久化的 provider
    事件仍然完整保留在儲存中（證明「事件持久化」與「節點完成」這兩
    道完整性邊界確實各自獨立）；子節點維持在 in_progress，沒有被
    破壞；一旦父節點（合理地）轉為 in_progress 後，用完全相同的輸入
    重試就會成功，證明先前的拒絕確實只是順序問題，而不是衍生輸入
    本身有其他缺陷。另有一個搭配測試，獨立驗證 EventStore 層自己
    文件記載的、與順序無關的行為（store.go：「沒有可變的
    current-state 資料列……不論順序都能正確持久化」）——做法是在
    真實的 turn.started 事件之前先持久化一個真實的 turn.completed
    事件，並確認兩者都各自獨立、正確地落地成資料列——證明儲存層
    對順序的寬容，以及 CompleteNode 對順序的嚴格要求，是兩個不同
    層級上刻意設計、彼此並不矛盾的行為。"
  - "所有 fixture 都是真的：
    testdata/provider-events/claude/{stop,
    stopfailure,userpromptsubmit}/*.json，直接從磁碟讀取，經過真實
    的 claudehooks.Parse*／claudetelemetry.Normalizer pipeline——本
    檔案裡沒有任何手刻的 v1.Event 值，唯一的例外是另外明確、獨立
    驗證儲存層與順序無關這項契約的地方。"
  - "測試替身模式（fixedClock／seqIDs 風格、openTestDB、seedTask、
    newDocumentNode、newCompleteNodeHarness、moveNodeToInProgress）
    是 internal/progress 自己的測試套件、以及 qa-05 的
    leakage_scanner_test.go 已經建立的同一套 helper 的小型複製版——
    兩邊都是各自 test package 內未匯出（unexported）的東西，所以
    在這裡重新宣告同樣的最小結構（加上 qa04 前綴以避免和本 package
    中 qa-05 自己同名的 helper 衝突），沿用的正是那些檔案自己的
    doc comment 已經記載過、針對這種跨檔案重複的先例。"
blockers: []
findings:
  - severity: P1
    title: "沒有任何 production 程式碼路徑把一個已持久化的
      claude-provider v1.Event 接到 internal/progress.CompleteNode.Run
      ——qa-04 被要求做整合測試的這兩個元件，只在本測試檔案自己的
      TEST-ONLY 膠水程式碼裡被接在一起，production 程式碼中並沒有"
    file: "internal/orchestrator/hooks.go（HandleStop／
      HandleUserPromptSubmit／HandleStopFailure／HandleStatusLine
      只做正規化＋持久化就停手——HandleStop 自己的 doc comment 寫
      著：『完整的 Progress Tree／Git／artifact 對帳……其結果標記
      的深度超出本節點範圍』）；internal/telemetry/claude/normalizer.go
      （沒有任何 producer 會設定 Event.TaskID 或
      Event.ProgressNodeID——每個事件的 envelope() helper 只會設定
      SessionID）；internal/progress/complete_node.go 的
      CompleteNodeInput 與 internal/app/ports.go 的
      CompleteNodeRequest（兩者都被凍結為恰好 {NodeID,
      IdempotencyKey, Artifacts[, RepositoryCheckpointID]}——完全
      沒有 v1.Event／EventID／EventType 欄位）；
      internal/progress/node_store.go 的 Node.ProviderNodeID 欄位
      （有寫入也讀得回來，已用 grep 確認，但沒有任何程式碼會用
      ProviderNodeID 反查節點）；internal/app/wiring/wiring.go
      （沒有接上 internal/telemetry/claude 與 internal/progress 之
      間的橋接；Services.ProgressTree 仍然只是那個凍結的裸介面，
      尚未實作，這是該 package 自己 doc comment 寫的）。"
    reproduction: "go test ./internal/integrationtest/... -run TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists -v
      ——解析並正規化一個真實的 Stop fixture，並斷言 ev.TaskID==\"\"
      與 ev.ProgressNodeID==\"\"（目前兩者皆為真）；同時搭配一次全
      repo 的 grep（記載於本檔案自己的 package doc comment），尋找
      是否有檔案同時 import internal/telemetry/claude 與
      internal/progress（零筆命中）、或同時 import pkg/protocol/v1
      與 internal/progress（零筆命中），以及是否存在任何
      adapter／bridge／consumer／dispatcher 檔案（不存在）。"
    expected_invariant: "Auspex_ADD.md 的 Progress Tree 設計上應該由
      真實的 provider 觀測來推進（provider.turn.completed 訊號正是
      那種應該能觸發節點完成的真實世界事件）——Constitution §6.1
      （『Progress Tree 是唯一權威、持久的任務狀態……絕不是 agent
      自己聲稱完成』）意味著必須有某種真實訊號能驅動真正的完成，
      而不只是測試框架手動組出一個 CompleteNodeInput。但目前完全
      沒有這種機制：事件 pipeline（claude-provider）與完成
      pipeline（checkpoint／progress）各自獨立來看都是正確的、也
      都各自測試得很完整，但兩者之間確實缺了一層銜接。這正是
      qa-04 自己的任務說明所要找的那種「只在整合層面才會出現的
      缺口」（『事件型別或欄位對應不一致，claude-provider 的真實
      事件其實沒有帶著 checkpoint 順序檢查邏輯所預期的資訊』）。"
    owning_role: "contract-integrator（在凍結的
      v1.Event／CompleteNodeRequest 契約上需要一個新的跨元件
      port／欄位，或者需要一個有記載的決定：TaskID／ProgressNodeID
      的解析改走另一條尚未建立的查找路徑——Constitution §4.2 把
      pkg/protocol/v1/** 與 internal/app/ports.go 專屬保留給這個
      角色），並與 claude-provider（一旦有了解析機制，就需要在
      產生的事件上填入 TaskID／ProgressNodeID）以及
      checkpoint（不論最終由哪個角色建置實際的
      consumer／adapter）協調處理。之所以不列為 P0：這個缺口所涉及
      的每一個獨立元件本身都是正確的、也都通過自己的測試，沒有任何
      既有不變性被違反，而且 vertical-slice 凍結的 DAG 這一波也從未
      把「建置這個 adapter」明確指派給任何節點——這是一個往前看的
      整合缺口，是本節點的職責所在，用來把它揭露出來，而不是一個
      回歸缺陷。"
  - severity: P2
    title: "當由真實的 claude-provider 事件端對端驅動時，
      checkpoint-a07 的重複／順序錯亂語意仍然正確成立，不只是在
      手刻的 CompleteNodeInput 值上成立——兩個元件自己的邏輯都沒有
      發現缺陷"
    file: "internal/telemetry/claude/store.go（claude-provider-05）；
      internal/progress/idempotency.go, internal/progress/complete_node.go
      （checkpoint-a07）"
    reproduction: "N/A——不是缺陷。go test ./internal/integrationtest/... -run 'Duplicate|OutOfOrder' -v
      （本節點自己的測試套件，5/5 通過）就是收斂證據：
      TestDuplicateProviderEvent_EndToEnd_StoredOnceAndCompletionReplayed
      與
      TestDuplicateProviderEvent_DifferentChannel_DifferentKey_SameEvidence_Replayed
      證明了重複的情況；
      TestOutOfOrderDelivery_EndToEnd_ChildCompletionBeforeParentStarted_Rejected
      與
      TestOutOfOrderDelivery_EndToEnd_EventStoreAcceptsEitherArrivalOrder
      證明了順序錯亂的情況，包括「父節點一旦開始、重試就會成功」的
      正向路徑，以及 EventStore 層與 CompleteNode 層彼此不矛盾這件
      事。"
    expected_invariant: "上游兩個節點自己的單元測試都已經各自獨立
      證明過自己的邏輯正確；qa-04 自己在 DAG 上的這一列，存在的目的
      就是要證明——當兩者之間真正流動的欄位（一把從真實 fixture
      衍生出來、以確定性方式做 digest 的 IdempotencyKey，而不是一個
      字串字面值）被實際使用時，這個正確性依然成立。依
      agents/qa.md「記錄任何發現，即使是非阻塞性的」這項指示而記錄
      於此。"
    owning_role: "qa（本節點）——僅供參考；針對這個特定行為，
      claude-provider 或 checkpoint 都不需要採取任何行動。"
```

```yaml
node: qa-05
status: completed
artifacts:
  - internal/integrationtest/leakage_scanner_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run LeakageScanner -v   # 6/6 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./... -race   # whole repo, all 33 packages PASS, zero regressions from merging origin/main (Phases 4/5/6)"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <記錄於下方>
next_action: 無——qa-05 是本波 qa 的完整任務指派；依任務指示 STOP；向 lead 回報發現以便後續分派
assumptions:
  - "把 internal/integrationtest 建成一個全新的 package（在本節點之前
    並不存在），因為它屬於 qa 的專屬路徑，且之前沒有任何 qa 節點碰
    過它。所有測試替身（fixedClock、seqIDs、checkpointRepoBuilder）
    都是 internal/telemetry/claude/normalizer_test.go 與
    internal/repocheckpoint/helpers_test.go 已經建立的同一套模式的
    小型複製版——那兩者都是各自 test package 內未匯出的東西，所以
    在這裡重新宣告同樣的最小結構（而不是嘗試跨 package 邊界
    import 一個 internal 測試 helper，Go 語言本身不允許這麼做），
    沿用的正是那些 package 自己的 doc comment 已經為這種跨檔案重複
    建立的先例。"
  - "情境 1（SQLite 匯出）：驅動真實的 internal/telemetry/claude
    Normalizer + EventStore，對著一個真實、落在磁碟上的 temp-file
    SQLite 資料庫（sqlite.Open 用真實路徑，不是 ':memory:'），重複
    使用 claude-provider-07 自己 allRawTextFixtures 表
    （fixture_suite_test.go）裡完全相同的 needle 字串，讓本節點檢查
    的是那個節點已經在單元測試層級證明過不存在的同一批已知敏感
    字串——qa-05 的工作是證明這個結論在真實、落在磁碟上的檔案位元
    組層級也一樣成立，而不是重新衍生新的 needle。在 PersistAll 之
    後，強制執行 `PRAGMA wal_checkpoint(FULL)` 再 Close()，接著直接
    用 os.ReadFile 讀取原始的 .db 檔案（並且保守起見也讀了
    -wal／-shm 這兩個附屬檔案路徑，以防未來的呼叫端 checkpoint
    紀律不同）——不是透過 StoredEvent 或任何型別化查詢——滿足了
    任務說明「直接讀檔案……不只是透過型別化查詢」的指示。"
  - "情境 2（repository checkpoint）：驅動真實的
    internal/repocheckpoint.Capture（就是
    internal/repocheckpoint/untracked_test.go 自己呼叫的那個進入
    點，不是 mock），對著一個 scratch Git repo，裡面放了一個
    untracked 的、長得像密鑰的檔案（GitHub token 樣式），以及一個
    untracked 的、貼近 prompt 內容的自由文字檔案，在一個暫時的
    ArtifactsRoot 下產生真實、落在磁碟上的 manifest.json／
    summary.md／patch.gz 配對／untracked.zip。掃描了每一種
    artifact 類型：解壓縮後的 gzip patch、zip 內的項目（解壓縮
    後）、以及純文字檔案（manifest.json／summary.md／
    skipped-files.json）。"
  - "重複使用 internal/redact 已匯出的 Scan API（ScanContent 用於
    已經從封存檔解壓縮／解出的記憶體內緩衝區，ScanPath 用於可證偽
    性檢查中的獨立檔案），而不是自己重新實作任何樣式比對邏輯，這
    是遵照本節點把 internal/redact 當成唯讀依賴的明確指示。"
  - "可證偽性／負向對照測試
    （TestLeakageScanner_Falsifiability_DetectsPlantedSecretInRawFile）：
    把一個已知的 sk-ant-... 密鑰與一個已知的 prompt needle，直接寫
    進一個原始檔案（略過真實 pipeline，依任務說明的明確指示），然
    後斷言 scanBytesForSecrets 與 scanBytesForNeedles（本檔案裡其他
    每個測試都仰賴的那兩個函式）都能偵測到它，另外也獨立斷言
    redact.ScanPath 同樣會比對到。這是用來證明快樂路徑測試「零發
    現」的結果是有意義的，而不是掃描器因為根本沒有真正在檢查任何
    東西而空洞地通過。"
  - "用一個用完即丟的除錯測試（在最終 commit 之前已移除）做健全性
    檢查（sanity check），確認真實的 DB 匯出檔案是一個實質內容約
    236 KiB 的 SQLite 檔案（不是空的或接近空的），且 checkpoint 情
    境確實產生了全部六個預期的 artifact 檔案，包括一個有實質內容
    的 untracked.zip（253 bytes，內含 scratch-notes.txt，但不含那
    個長得像密鑰的檔案）與 skipped-files.json（75 bytes，記錄了這
    次跳過）——確認掃描目標是真實、有內容的 artifact，而不是意外
    變成空殼的替代品。"
blockers: []
findings:
  - severity: P1
    title: "一個已被 TRACKED 的檔案，其 staged／unstaged diff 裡長得
      像密鑰的內容，從來不會被 internal/redact 過濾——只有
      UNTRACKED 檔案的封存檔會被掃描"
    file: "internal/repocheckpoint/capture.go（Capture 呼叫
      gitClient.DiffPatch 取得 staged／unstaged 內容，這條路徑上完
      全沒有呼叫任何 redact.Scan*）；internal/repocheckpoint/untracked.go
      的 buildUntrackedArchive 是唯一會呼叫 internal/redact 的地方"
    reproduction: "go test ./internal/integrationtest/... -run TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered -v
      ——stage 一個 tracked 檔案，其新內容是一個長得像 GitHub token
      的字串（`ghp_...` 加 38 字元後綴），執行一次真實的
      repocheckpoint.Capture，然後直接掃描產生出來的
      staged.patch.gz；那個長得像密鑰的字串被逐字找到，完全沒有被
      過濾，由 github_token 偵測器在解壓縮後的 patch 位元組上觸發
      確認。"
    expected_invariant: "Auspex_ADD.md §19.5／§27.8 的密鑰掃描預設
      政策，以及 Constitution §7 規則 2（原始 prompt 與敏感內容預
      設不得被持久化／外洩），最自然的讀法應該是適用於 Repository
      Checkpoint 捕捉到的一切內容，而不只是 untracked 檔案的封存
      檔。一個被貼進 tracked 設定檔並 stage 起來的密鑰（一個完全
      稀鬆平常、不小心 commit 進去的情境），從那一刻起會原封不動地
      存在於之後每一個 checkpoint artifact 裡，完全沒有被 redact。"
    owning_role: "checkpoint（Part B／repocheckpoint）——請注意這不
      是新發現：internal/repocheckpoint/untracked_test.go 自己的
      TestCapture_Untracked_SecretScan_NeverAppliesToTrackedDiffContent
      早就把這個確切的邊界記載為 checkpoint-b06 刻意、且已被承認的
      範圍決定，並明確點名『未來一次 qa-05 風格的 patch 內容掃描』
      作為後續工作。這次 qa-05 的發現就是那個後續：獨立在整合層再
      次確認這個缺口是真實存在的（不只是單元測試註解裡的斷言），
      並正式把它送去做範圍決定——要嘛是一個由 ADR 背書、接受此風
      險的決定（patch 是 tracked 內容的 diff，跟 untracked 檔案的
      捕捉是不同問題），要嘛是一個後續節點，在寫入
      staged.patch.gz／unstaged.patch.gz 之前擴充 internal/redact
      的掃描範圍到 patch 內容。這裡沒有把它列為 P0，因為這是一個
      既有的、已經記載過、已經出貨的範圍邊界，不是新的回歸；而且
      ADD §19.5 的密鑰掃描條文出現在 untracked 檔案封存的段落底
      下，而不是 patch 捕捉的段落——所以它有可能是設計上本來就在
      範圍內，而不是一個被打破的不變性。應由 contract-integrator
      確認哪一種讀法才對；在那之前先列為 P1（必須在 demo 之前解
      決，因為『在高風險 turn 之前做 checkpoint』正是使用者的
      tracked 檔案密鑰真的有可能存在的那種情境）。"
  - severity: P2
    title: "claude-provider-07 自己的隱私把關措施，當初把範圍正確地
      限定在 package 層級的單元測試存取；qa-05 是第一個針對真實、
      落在磁碟上的 SQLite 檔案／WAL，以及一個真實的 Repository
      Checkpoint artifact 目錄，做端對端原始 prompt／密鑰外洩驗證
      的節點——沒有發現缺口，但這個結果關閉了一個先前尚未處理的範
      圍項目，在此記錄下來是為了可追溯性，而不是因為發現了缺陷。"
    file: "internal/telemetry/claude/fixture_suite_test.go
      （claude-provider-07）；internal/redact/doc.go（checkpoint-b06）"
    reproduction: "N/A——不是缺陷。go test ./internal/integrationtest/... -run LeakageScanner -v
      （本節點自己的測試套件）就是收斂證據。"
    expected_invariant: "上游兩個節點自己的文件都點名了這個確切的缺
      口，並指名 qa-05 是負責關閉它的節點；依 agents/qa.md「記錄任
      何發現，即使是非阻塞性的」這項指示而記錄於此。"
    owning_role: "qa（本節點）——僅供參考，其他角色不需要採取任何
      行動。"
```

```yaml
node: qa-01
status: completed
artifacts:
  - .github/workflows/ci.yml
validation:
  - "python3 -c \"import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))\"   # YAML parses without error"
  - "actionlint .github/workflows/ci.yml   # 0 findings (installed via go install github.com/rhysd/actionlint/cmd/actionlint@latest)"
  - "task fmt   # PASS, no unformatted files"
  - "task lint   # go vet + golangci-lint run ./... -> 0 issues"
  - "task build   # go build -o bin/auspex ./cmd/auspex -> succeeds"
  - "task test   # go test -race ./... -> all 18 packages PASS (14 with tests, 4 no-test-files packages)"
  - "task test:short   # go test ./... (no -race, the Windows-job path) -> all PASS"
commit: c523650
next_action: qa-08（治理文件）
assumptions:
  - "workflow 裡 `run:` 步驟呼叫的每一個指令（`task fmt`、`task
    lint`、`task build`、`task test`、`task test:short`），都是在寫
    workflow 之前直接從 Taskfile.yml 讀出來的、真實已存在的目標——
    沒有一個是憑空發明的。`golangci-lint` 本身沒有在任何 `run:` 步
    驟裡被直接呼叫；`task lint` 依賴 `vet`，然後照著
    `.golangci.yml`／Taskfile.yml 既有的定義去執行 `golangci-lint
    run ./...`，所以這個 workflow 沒有新增任何 lint 設定或重複
    規則。"
  - "lint／fmt 只在 ubuntu-latest 上跑一次，不橫跨整個 OS matrix
    跑。gofmt／go vet／golangci-lint 作用在原始碼文字與 Go AST 上，
    這些不會因為主機 OS 而不同（這個 repo 目前還沒有任何透過
    build tag 分岔、會讓 lint 輸出跨平台不同的 Go 原始碼）——
    foundation 的 internal/lock package 確實有
    process_unix.go／process_windows.go 這種 build-tag 拆分的檔
    案，但不論*執行 linter 的*是哪個 OS，兩者依然是被
    gofmt／vet／lint 一視同仁解析的普通 Go 原始碼；只有*編譯*每個
    檔案才需要對應的目標 OS，而那是 build-and-test 這個 matrix
    job（不是 lint job）在做的事。對一個 trivial PR 跑三次 lint，
    純粹是多餘的成本、沒有帶來任何額外訊號，這和「讓 trivial PR
    綠燈檢查保持快速」的目標相違背。"
  - "Race detector：`-race` 需要 CGO_ENABLED=1 以及一套可運作的 C
    toolchain。ubuntu-latest 與 macos-latest 這兩種 GitHub 代管
    runner 預設就有 gcc／clang，這符合本節點任務指示的內容
    （『Windows 在 race detector 上歷來有一些限制……如果比較安
    全，就只在 ubuntu／macos 上跑 race，並把理由記下來』）。
    windows-latest 這種 GitHub 代管 runner 目前其實也帶著一套可運
    作的 MinGW gcc，所以 `-race` 大概也能在上面跑，但它一直有紀錄
    在案、是 CI 裡最不可靠的組合（instrumentation 較慢、偶爾出現
    與 Go 版本／MinGW 版本搭配有關的不穩定失敗，Go 自己的 release
    notes 也在某些舊版本中提過 Windows 上 race detector 支援較
    窄）——考量到 qa-01 的驗證門檻是「CI 在一個 trivial PR 上跑
    綠」，在這一波用不到的涵蓋範圍（目前除了
    internal/lock／internal/storage/sqlite 之外，沒有其他大量並行
    的程式碼）上，在某一條 matrix 分支引入一個已知的不穩定來源，
    判斷並不值得。這不代表整個專案放棄 race 涵蓋範圍：三條
    matrix 分支裡有兩條（ubuntu、macos）每個 PR 都會跑完整的
    `-race` 套件，而 `task test:short` 在 Windows 上仍然會跑完整
    的非 race 套件，所以 Windows 專屬的編譯／邏輯回歸依然會被抓
    到，只是抓不到 Windows 專屬的 data race。"
  - "lint job 裡安裝 golangci-lint 的方式是透過
    `golangci/golangci-lint-action@v6`，而不是 `go install
    .../golangci-lint/v2/cmd/golangci-lint@latest`（根據
    docs/implementation/vertical-slice/foundation.md，那是
    foundation-09 在本機採用的做法）。這個 action 是在 Actions 裡
    跑 golangci-lint 有文件記載、有快取、GitHub 原生支援的方式，
    而且原生就懂 `.golangci.yml` 已經在用的 v2 設定 schema；
    `go install` 也能用，但每次執行都要重新從原始碼下載／建置
    linter，沒有內建快取。`version: latest` 讓它維持在 v2 這條線
    上（`.golangci.yml` 開頭是 `version: \"2\"`），而不用把版本號
    寫死、之後還要手動維護這個 workflow。"
  - "這個 darmin 開發用 worktree 裡也沒有預先安裝 `task`／
    `golangci-lint`（跟 foundation-09 在自己開發機上記載的缺口一
    樣）——兩者都能在 $(go env GOPATH)/bin 找到，因為這個
    worktree 和 foundation-09 用 `go install` 安裝它們的那個主要
    checkout 共用同一個 GOPATH；本節點除了把那個目錄加進驗證
    session 的 PATH 之外，不需要另外安裝。`actionlint`（先前沒有
    任何角色安裝過）則是新透過 `go install
    github.com/rhysd/actionlint/cmd/actionlint@latest` 安裝的，純
    粹是為了強化本節點自己的 YAML／schema 驗證，超越單純的
    `yaml.safe_load` 解析——這是一次性的本機驗證工具，不是 repo
    的依賴，go.mod、Taskfile.yml 或 workflow 檔案本身都沒有引用
    它。"
  - "`arduino/setup-task@v2` 用來在 build-and-test job 裡安裝
    `task` 這個執行檔（那裡需要它是因為那個 job 會呼叫
    `task build`／`task test`），而 lint job 只需要
    `golangci-lint`（透過它自己的 action 安裝）加上
    `task fmt`／`task lint`——所以 `setup-task` 是在兩個 job 裡各
    自重複安裝，而不是共用，因為 GitHub Actions 的 job 是跑在各自
    獨立、用完即丟的 runner 上，同一個 workflow run 裡的不同 job
    之間沒有共用的檔案系統狀態。"
  - "本節點沒有建立 `security.yml`、`provider-contract.yml` 或
    `release.yml`——ADD §30.3 四個都有點名，但 qa-01 自己在 DAG
    上這一列的範圍（驗證項：『CI 在一個 trivial PR 上（Ubuntu／
    macOS／Windows）跑綠』）只涵蓋最基本的 build／lint／test
    workflow。另外三個依賴的基礎建設（provider fixture 語料庫、
    release／簽章 pipeline、govulncheck／CodeQL 接線）這一波都還
    不存在，超出本節點的範圍即是範圍蔓延（scope creep）。"
blockers: []
```

```yaml
node: qa-08
status: completed
artifacts:
  - SECURITY.md
  - CONTRIBUTING.md
  - CODE_OF_CONDUCT.md
  - GOVERNANCE.md
validation:
  - "test -s SECURITY.md && test -s CONTRIBUTING.md && test -s CODE_OF_CONDUCT.md && test -s GOVERNANCE.md   # all four exist and are non-empty (113/136/140/124 lines respectively)"
  - "manual doc review against Auspex_ADD.md §30.7 (Governance) and §30.8 (Security disclosure), and against README.md's existing 'Contributing' section and Tech stack table, for contradictions -> none found"
  - "grep -rn \"CLA\" --include=*.md .   # only CONTRIBUTING.md/GOVERNANCE.md/Auspex_ADD.md mention it, all consistent ('no CLA')"
commit: a4ab0b2
next_action: 無——qa-01 與 qa-08 是本波 qa 的完整任務指派；依任務指示 STOP
assumptions:
  - "SECURITY.md 的揭露管道是一個私有的 GitHub Security
    Advisory，承諾 3 個工作天內回應，逐字取自 ADD §30.8（『Private
    GitHub Security Advisory; ack target 3 business days』）——沒
    有發明其他管道（例如 security@ 這類 email 別名），因為 ADD 就
    只點名了這一種機制，而 Constitution §1 把 ADD 當成這部分內容
    唯一的權威來源。"
  - "SECURITY.md 也列出了 qa.md 角色說明包自己的『Security
    assertions』清單（loopback／API 驗證、預設不含 prompt 文字、
    bearer token／API key 的 redaction、hook payload 大小限制、限
    縮的 SQLite／artifact 權限、只用 argv 呼叫外部指令、解壓縮的
    path-traversal 安全性、opt-in 的 auto-resume），把它們當成本
    專案目前的安全態勢／回報應該拿來測試的對象，因為 qa 同時擁有
    這份文件與那份斷言清單，而一份沒有指名實際驗證過什麼的安全政
    策，會比本專案自己既有的內部標準還要單薄。"
  - "CONTRIBUTING.md 要求 DCO 簽署（`git commit -s`），並依 ADD
    §30.7 逐字聲明『no CLA』（『DCO sign-off; no CLA initially』）。
    它也要求在提出變更之前先讀過 CONSTITUTION.md、Auspex_ADD.md 與
    AGENTS.md，並描述了里程碑閘門規則，這兩者都是從 README.md 既
    有的『Contributing』小節照搬過來的（沒有牴觸，只是用 ADD
    §30.2 具體的本機 task 指令，以及 README.md 本身沒寫的
    DCO／授權要求做了擴充）——之所以先讀過 README.md 自己的
    『Contributing』小節，是為了刻意避免搞出第二套、彼此分歧的貢
    獻流程。"
  - "CONTRIBUTING.md 的本機 task 清單（`task fmt`、`task lint`、
    `task test`、`task build`）是 ADD §30.2 完整清單裡，
    Taskfile.yml 目前真正存在的子集（`task bootstrap`、
    `task test:race`、`task test:e2e`、`task vscode:test`、
    `task research:test`、`task verify` 是 ADD 指定、Taskfile.yml
    目前還沒有的未來目標——根據 README.md 的儲存庫版面配置，
    `vscode/` 與 `research/` 這兩個目錄樹也還不存在）。只記載今天
    真的能跑的目標，可以避免叫新貢獻者去跑一個會失敗的指令；那些
    ADD 點名但尚未存在的目標，用一個註腳承認其存在，而不是悄悄略
    過，這樣 CONTRIBUTING.md 就不會不動聲色地和 ADD §30.2 最終的
    完整清單互相矛盾。"
  - "CONTRIBUTING.md／GOVERNANCE.md 裡把授權寫成 Apache-2.0，與
    README.md 的 Tech stack 表格（『License: Apache-2.0』）一致——
    但儲存庫根目錄目前還沒有 LICENSE 檔案
    （docs/implementation/vertical-slice/foundation.md 裡
    foundation-09 的紀錄已經把這點標記為一個已知、尚未指派負責人
    的缺口：『雖然 LICENSE 與 NOTICE 都列在 foundation 的專屬路徑
    清單裡，但兩者都沒有被加進來』）。依 agents/qa.md 自己的專屬
    路徑清單，LICENSE／NOTICE 是 foundation 擁有的路徑（不在 qa
    的清單裡），所以在這裡建立它們超出範圍；CONTRIBUTING.md／
    GOVERNANCE.md 只是點名了授權的名稱（與 README.md 一致），並沒
    有斷言 LICENSE 檔案本身確實存在，這個缺口在下方重新標記給
    contract-integrator／foundation。"
  - "CODE_OF_CONDUCT.md 逐字採用 Contributor Covenant v2.1（一份標
    準、可自由重複使用、廣泛採用的文字），這是依照本節點「採用一
    份標準、廣為人知的行為準則」的指示，因為 ADD 對這個檔案沒有指
    定任何客製化內容。只填入了執行聯絡窗口，指向 SECURITY.md 用的
    同一個私有 GitHub Security Advisory／repository-owner 聯絡管
    道，因為 ADD 或 README 裡都沒有點名任何另外、專屬於行為準則的
    聯絡方式（例如 conduct@ 這類 email），發明一個出來只會是一個
    無法驗證、大概率是死信箱的聯絡地址。"
  - "GOVERNANCE.md 逐字記載了 ADD §30.7 裡的兩個成熟度階段：
    Initial（『lead maintainer + 公開的 ADR／issue 流程』）與
    Mature（『≥3 位活躍維護者；敏感的安全／provider 變更需要 2 位
    核准；有記載的發版權限；DCO 簽署；一開始不需要 CLA』），明確
    聲明*目前*所處的階段是 Initial（符合儲存庫目前的實際狀態：一
    位 lead、尚未有維護者團隊），Mature 則是有記載的未來標準，而
    不是現在就成立的宣稱。它也記載了 CONSTITUTION.md §3 的 ADR 流
    程（只有 contract-integrator 能接受一份 ADR；任何角色都可以提
    案），因為從貢獻者的角度來看，「儲存庫本身的治理」與「架構決
    策的治理」是同一件事，把它們拆到兩份文件、讓讀者還要交叉參
    照，會比用一份文件把 CONSTITUTION.md 當成 ADR 機制的權威來源
    還要糟（這樣做也避免逐字重複 CONSTITUTION.md 的規範性文字而冒
    著兩者走樣的風險——CONSTITUTION.md 本身依其自己的標題就是最高
    權威，只有 contract-integrator 可以修訂它）。"
  - "ADD §30.9（隱私治理——變更原始 prompt 的保留方式、對外遙測、
    auto-resume 的預設值、state artifact 的內容，或遠端
    checkpoint，都需要隱私審查＋ADR＋changelog）被逐字收錄進
    GOVERNANCE.md，作為一個獨立命名、有別於一般敏感變更審查的特
    別審查類別，因為它明確是一條治理規則（誰必須核准哪一種變
    更），而不是安全揭露或貢獻流程的規則，所以它屬於
    GOVERNANCE.md，而不是 SECURITY.md 或 CONTRIBUTING.md。"
blockers:
  - "LICENSE／NOTICE 檔案（ADD §30.1『Required files』清單裡的項
    目）在儲存庫根目錄還不存在。這不是 qa-08 的阻礙（完全不在 qa
    的專屬路徑範圍內——依其自己的專屬路徑清單，這是 foundation 擁
    有的路徑），但 CONTRIBUTING.md／GOVERNANCE.md 都把 Apache-2.0
    點名為專案授權，與 README.md 一致，而一個去找實際 LICENSE 檔
    案的貢獻者目前還找不到。依 Constitution §4.4（一個角色若需要
    變更自己不擁有的檔案，應該透過自己的 progress artifact 提出
    請求，而不是自行動手修改），把這件事在此提報給
    foundation／contract-integrator 作為一個跨角色缺陷——這是重新
    標記 foundation-09 已經在自己的 progress artifact 裡提過的同
    一個缺口，不是新發現。"
```

```yaml
node: qa-05-followup
status: completed（測試已更新；在本 branch 上尚無法通過——詳見下方說明）
artifacts:
  - internal/integrationtest/leakage_scanner_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -v   # 9/10 PASS, 1 EXPECTED FAIL (see below)"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: <記錄於下方>
next_action: 無——這是一個修正性任務，不是一個新的 DAG 節點；一旦 commit 完成就 STOP。把更新後的測試重新驗證到真正 PASS，要等 lead 把 vertical-slice/checkpoint 合併進 vertical-slice/qa 之後才會發生；強制促成那次合併不是本節點的工作。
assumptions:
  - "checkpoint 已經獨立修好了這一波 qa-05 的 P1 發現（『一個已被
    TRACKED 的檔案，其 staged／unstaged diff 裡長得像密鑰的內容，
    從來不會被 internal/redact 過濾』），透過
    vertical-slice/checkpoint 的 commit f981bde（『checkpoint:
    extend secret scanning to tracked-file diff content (fixes
    qa-05 P1 finding)』），新增了
    internal/repocheckpoint/patchredact.go，並在 staged／unstaged
    的 DiffPatch 呼叫之後、封存之前，把它接進 Capture
    （capture.go）。這一點是獨立驗證過的：用唯讀的方式跑
    `git show vertical-slice/checkpoint:internal/repocheckpoint/patchredact.go`
    與
    `git show vertical-slice/checkpoint:internal/repocheckpoint/capture.go`
    讀了這兩個檔案（vertical-slice/checkpoint 從未被合併或
    checkout 進這個 worktree；internal/repocheckpoint/** 仍然是
    checkpoint 的專屬路徑，這裡完全沒有動它）——不是單純相信
    lead 的說法。"
  - "patchredact.go 的 redactPatchSecrets 只會用
    internal/redact.ScanContent 掃描 staged／unstaged patch 裡以
    '+'／'-' 開頭的行內容（明確排除 '+++'／'---' 檔案標頭行、
    '@@ ... @@' hunk 標頭，以及所有 context 行），一旦比對到，就把
    *整行*內容換成一個固定、不會回顯原文的佔位常數：
    `redactedLinePlaceholder = \"[REDACTED: secret-shaped content
    removed by auspex checkpoint capture]\"`。行首的前綴位元組與結
    尾的換行符號都會保留。這是刻意選擇「原地 redact」（而不是「跳
    過並在 manifest 加註記」）的設計，目的就是要讓
    checkpoint-b08 的 restore-dry-run（`git apply --check`）在
    patch 其餘部分依然能正常運作。"
  - "把
    TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered
    重新命名為
    TestLeakageScanner_SecretInTrackedFileDiff_NowFiltered，因為原
    本記載的缺口已經不再是一個被接受／已知的缺口——它現在是一項已
    確認修復、往後由這個測試守護的不變性。斷言的方向也翻轉了：測
    試現在斷言 (a) scanBytesForSecrets 在 staged.patch.gz 裡找不到
    任何東西，(b) 原始密鑰字串也不是 patch 裡逐字出現的子字串（相
    對於掃描器本身的另一重保險），以及 (c) patchredact.go 那個精
    確的 redaction 佔位字串，確實出現在 patch 裡密鑰原本所在的位
    置——這是一個精確的正向斷言，而不只是檢查「不存在」，用來確
    認原地 redact 是照設計發生的，而不是例如整個 patch／整行以其
    他方式被丟掉。另外加了一個輕量的 assertPatchApplies
    helper，會複製 scratch repo、把（解壓縮後的）已 redact patch
    寫進一個檔案，再對它跑 `git apply --check`，確認 redact 沒有破
    壞 patch 的可套用性——這只是一個健全性檢查，不是重新測試
    checkpoint-b08 自己的 restore-dry-run 邏輯，那仍然不在本節點範
    圍內。"
  - "這個測試現在單獨在 vertical-slice/qa 上*不可能*通過，這是預期
    中的事，不是回歸：internal/repocheckpoint/patchredact.go 在這
    個 branch 上要等到 lead 把 vertical-slice/checkpoint 整合進
    vertical-slice/qa（或兩者都併入 main）之後才會存在。在本機跑
    了完整的更新後測試來確認：它會精確地以『secret-shaped content
    leaked into staged.patch.gz unredacted』失敗（github_token 偵
    測器依然會觸發，因為這個 branch 上的 Capture 還沒有
    redaction 這一步）——也就是說，這個新測試在這個 branch 上正確
    地仍然偵測到舊的／修復前的行為，一旦 checkpoint 的修復真的存
    在，就會翻轉為 PASS。internal/integrationtest 裡其他 9 個測試
    （5 個 qa-04 的 duplicate／out-of-order 測試，加上另外 4 個
    qa-05 的 leakage-scanner 測試）都不受影響，照常通過。"
  - "依本任務的明確限制，沒有動 internal/repocheckpoint/**，也沒有
    把 vertical-slice/checkpoint 合併進 vertical-slice/qa——
    checkpoint 的 branch 只用
    `git show vertical-slice/checkpoint:<path>` 以唯讀方式檢視
    過。"
blockers: []
findings:
  - severity: informational
    title: "qa-05 的 P1 發現（『一個已被 TRACKED 的檔案，其
      staged／unstaged diff 裡長得像密鑰的內容，從來不會被過
      濾』），現在已經由 checkpoint 在上游修好了
      （vertical-slice/checkpoint@f981bde，
      internal/repocheckpoint/patchredact.go）——本節點的測試已更
      新為斷言修正後的行為；等 lead 的整合把
      vertical-slice/checkpoint 與 vertical-slice/qa 併在一起之
      後，才會被真正重新驗證。"
    file: "internal/integrationtest/leakage_scanner_test.go（本節
      點）；internal/repocheckpoint/patchredact.go、
      internal/repocheckpoint/capture.go（checkpoint，僅作唯讀參
      考）"
    reproduction: "go test ./internal/integrationtest/... -run TestLeakageScanner_SecretInTrackedFileDiff_NowFiltered -v
      ——單獨在 vertical-slice/qa 上（checkpoint 的修復尚未存在）
      目前會如預期般以『secret-shaped content leaked into
      staged.patch.gz unredacted』失敗；一旦
      vertical-slice/checkpoint@f981bde 被整合進來，同一個指令預
      期會 PASS，斷言 staged.patch.gz 裡沒有任何原始密鑰留存，取
      而代之的是 checkpoint 那個精確的 redaction 佔位字串，而且
      redact 後的 patch 依然可以被 git-apply。"
    expected_invariant: "整合完成之後，任何被 stage／unstage 進一
      個 tracked 檔案、長得像密鑰的內容，都不會未經 redact 就留存
      在 Repository Checkpoint 的 patch artifact 裡，而且 redact
      後的 patch 在結構上依然有效（可以被 git-apply）——關閉這一
      波 qa-05 的 P1 發現。"
    owning_role: "qa（本節點）負責測試；checkpoint（已依 f981bde
      交付）負責修復；lead 負責最終整合與重新驗證。"
```

## Phase（Stage 4 completion）— qa-02、qa-03、qa-06、qa-07、qa-09

這一波把 qa 剩下的 DAG 範圍*整個*都指派了下來：qa-02
（vertical-slice demo）、qa-03（同一個 DB 上重啟、跨角色）、qa-06
（獨立的惡意 fixture）、qa-07（scheduler 雙 worker race，整合層
級），以及 qa-09（本篇最終報告）。先合併了 `origin/main`
（fast-forward，乾淨）——Phase 8-11 都已整合，意味著
claude-provider、checkpoint、predictor、runtime 在這一波開始時，
各自的整個 DAG 範圍都已經完成，所以 qa-02 點名的每一個依賴項
（『全部現在都已整合』）都是真實存在的，沒有任何一項需要造假。依
明確的任務指示，下方每一個節點都是各自獨立驗證並 commit——不批次
處理。

```yaml
node: qa-02
status: completed
artifacts:
  - internal/integrationtest/e2e_highrisk_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run E2EHighRisk -v   # 1/1 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: abad4d9
next_action: qa-03（本波下一個指派節點）
assumptions:
  - "依任務說明的明確偏好，設計了『一個』連貫的『高風險 turn』敘
    事，而不是六個互不相干的子測試：一個模擬的 session，其
    status-line 顯示真實的 quota／context 壓力
    （testdata/provider-events/claude/statusline/high_usage.json
    ——context 用量 98.85%、five-hour quota 用量 97.3%），合理地
    促使同一個 session 的下一個 prompt 被評估為高風險，需要一次
    checkpoint、一次 one-time allow、一個 Stop 結果，然後（因為
    quota 壓力其實從未改善）進入一次帶完整喚醒復原的 Graceful
    Pause——六個步驟講的是同一個故事，而不是六個獨立 fixture 拼湊
    起來。"
  - "步驟 2（prompt 觸發 auspex 攔截）沿用了 runtime-b06 自己記載
    過的技術（internal/orchestrator/decision_realauth_test.go 的
    newHighRiskDataSource），可靠地把真實的 predictor pipeline
    （scope／token／quota／risk／policy，全部都是真的）推進到危急
    風險區間：大量的變更檔案／行數分位數，加上
    internal/predictor/risk/combiner.go 實際會讀取的每一個
    completion／blast-radius 旗標（security-sensitive、
    migration-likely、cross-layer、open-ended scope）。確認這會
    落在 PolicyCheckpointAndRun（本次執行的結果正是如此）或
    PolicyRequireConfirmation，兩者依同一個先例自己的測試都算是
    『夠高風險』。"
  - "步驟 3 用到了 checkpoint 兩條真實完成路徑，而不只是其中一條：
    (a) internal/progress.CompleteNode，對一個真實的 Progress
    Tree 節點做完成（Constitution §6.3 的原子節點完成＋State
    Checkpoint），以及 (b) 一次真實的
    orchestrator.CheckpointCreate 呼叫（statecheckpoint.Service ＋
    repocheckpoint.Service，對著一個真實的 scratch Git repo），對
    應 PolicyCheckpointAndRun 決策在允許 turn 繼續之前實際要求的
    那個獨立、當下狀態的 checkpoint——這是兩個刻意不同的
    checkpoint 進入點（statecheckpoint 自己的 package doc
    comment：Create 是『一個獨立、隨需的快照進入點』，不是包在
    CompleteNode 路徑外面的包裝），這個情境真實地演練了這兩者，而
    不是把它們混為一談。"
  - "目前還沒有任何 production 的
    ProgressTreeService／GracefulPauseService adapter（用同一個
    grep 確認過，runtime-b10 自己的 restart_test.go doc comment
    已經記載：只有 internal/testutil/fakes 實作了這兩個完整
    port 的其中之一）——因此這個情境直接透過真實的
    internal/progress.CompleteNode 來驅動 Progress Tree 節點完成
    （就是 qa-04 自己測試檔案演練過的同一個、真實、已整合的元
    件），並直接透過真實的 internal/pause 自由函式來驅動 pause
    生命週期（Apply／CompareAndSwapStatus／InterruptAndSleep／
    Wake／ValidateResume／Resume，跟
    internal/pause/fulllifecycle_test.go 自己的
    runFullLifecycleToSleeping helper 用的是同一套技巧），而不是
    去等一個不在這一波範圍內要建置的 port adapter。這整段用的都
    是真實的 production 程式碼，不是拿 fake 頂替一個缺口——只是
    接在那個尚不存在的統一 port 底下一層而已，做法和
    runtime-b10 自己的重啟測試對同樣這兩個服務所做的完全一樣。"
  - "one-time allow 流程（步驟 4）與 resume 流程（步驟 6），都透過
    runtime-b06 建置的同一個真實 evaluation.Service 與
    orchestrator.DecisionAllowCmd，發出並消費一個真實、由儲存支撐
    的 Authorization——replay 會被拒絕這件事，在本檔案裡被證明了
    兩次（一次是原始 turn 的授權，另一次由 resume 流程自己那組全
    新的 issue-then-ValidateResume-consume 序列隱含地再次證
    明）。"
  - "Pause／wake 復原（步驟 6）用的是真實、由 SQLite 支撐的
    pause.SQLiteStore（runtime-b10），不是
    pause.NewMemStore()——這正是 qa-03 另外針對一次真實重啟做壓力
    測試的同一個持久性保證；本節點自己要證明的重點，是「完整生命
    週期能組合成一個貼近真實的流程」，而它確實做到了。"
blockers: []
findings:
  - severity: informational
    title: "vertical-slice demo 跨越每個真實角色的成果，能乾淨地端
      對端組合起來——這一輪沒有發現任何缺陷。"
    file: "internal/integrationtest/e2e_highrisk_test.go"
    reproduction: "N/A——不是缺陷。go test ./internal/integrationtest/... -run E2EHighRisk -v
      就是收斂證據。"
    expected_invariant: "字面意義上的 vertical-slice demo 情境
      （status-line -> auspex 攔截 -> checkpoint -> one-time
      allow -> Stop -> pause／wake 復原）從頭到尾對著真實實作端對
      端運作。"
    owning_role: "qa（本節點）——僅供參考；其他角色不需要採取任何
      行動。"
```

```yaml
node: qa-03
status: completed
artifacts:
  - internal/integrationtest/restart_sameDB_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run RestartSameDB -v   # 1/1 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: a1d376a
next_action: qa-06（本波下一個指派節點）
assumptions:
  - "依任務的明確指示，建立在 runtime-b10 自己那套「同一個
    SQLite 檔案、行程內重啟」技術之上
    （internal/app/wiring/restart_test.go：開一個真實、落在磁碟上
    的 temp-file（不是 :memory:）DB；透過它跑真實工作；丟棄*每一
    個*行程內的 Go 值，包括 *sqlite.DB 本身；對著*同一個*檔案路徑
    開一個全新的 *sqlite.DB；重新跑 migrate 並確認冪等性；證明狀
    態撐過重啟*而且*依然可寫），而不是從零重複造一份——但在
    fixture 層級把本節點*自己*的獨立性劃出範圍：各自獨立的
    task／session／worktree／repo ID，一個專屬、以 qa03 為前綴的
    ID 產生器與 Git scratch repo，以及一個獨立的低風險 DataSource
    字面值，所以這是一次真正獨立演練這項保證的過程。"
  - "本節點相對於 runtime-b10 自己那次證明（那次已經透過 hook 指令
    間接涵蓋了 claude-provider-04 的 normalizer 輸出、checkpoint
    的 State／Repository 服務、predictor 的 evaluation 服務，以及
    本角色自己的 pause／scheduler store，全部都是透過
    wiring.App／cobra CLI 層驅動）的*增量*，在於證明同樣的跨角色
    共存性，但*完全不經過* wiring／CLI 層——直接對著同一個共用檔
    案呼叫每個角色自己真實的建構函式
    （claudetelemetry.NewEventStore、
    progress.NewNodeStore／CompleteNode、
    statecheckpoint.NewService、repocheckpoint.NewService、
    evaluation.New、pause.NewSQLiteStore、scheduler.NewStore），
    確認跨角色共存的保證並不依賴 wiring.App 自己特定的組裝順序，
    或任何 CLI 層的行為——這是一個和 runtime-b10 自己那次由上到
    下、以指令驅動的證明，真正不同（更底層）的整合層級。"
  - "驗證了每個角色儲存層「重啟安全性」的*兩半*：(a) 重啟前的狀
    態，在重啟後透過一個全新的 Service／Store 實例是*讀得到*的
    （event GetByEventID、node Get、state checkpoint
    Snapshot+Verify、repository checkpoint Verify、pause
    GetByID、對既有 job 的 wake job Claim），以及 (b) 寫入路徑在
    重啟後是完全*活著*的，不只是讀舊資料列（一次全新的
    CheckpointCreate、一次全新的
    EvaluateTurn／Decide／IssueAuthorization／ConsumeAuthorization
    循環、一次全新的 RequestPause＋Schedule＋Claim）——證明在這五
    個角色共用同一個檔案的各自資料表上，重啟後不會殘留任何孤兒鎖
    （orphaned lock）。"
  - "在這個情境裡，重啟前發出（已發出、尚未消費）的
    Authorization，其恰好一次（exactly-once）保證
    （predictor-10 強化過的 ConsumeAuthorization）被明確證明能*撐
    過重啟依然持久*：重啟後透過一個全新的 *evaluation.Service
    實例成功消費，接著對*同一個* authorization 再嘗試一次
    replay（依然是在重啟之後）會被拒絕——證明的是恰好一次這個狀
    態*本身*（而不只是強制執行它的那個服務實例）撐過了重啟。"
blockers: []
findings:
  - severity: informational
    title: "跨角色狀態在一次重啟前後依然乾淨地共存——這一輪沒有發
      現任何缺陷。"
    file: "internal/integrationtest/restart_sameDB_test.go"
    reproduction: "N/A——不是缺陷。go test ./internal/integrationtest/... -run RestartSameDB -v
      就是收斂證據。"
    expected_invariant: "當 claude-provider 的事件、checkpoint 的
      Progress Tree／State／Repository checkpoint、predictor 的
      evaluation／authorization，以及 runtime 的 pause／scheduler
      紀錄，共存在同一個 SQLite 檔案裡時，全部都能撐過一次真正的
      行程重啟，而且每個角色真實的服務，之後依然既能讀取舊狀態、
      也能寫入新狀態。"
    owning_role: "qa（本節點）——僅供參考；其他角色不需要採取任何
      行動。"
```

```yaml
node: qa-06
status: completed
artifacts:
  - internal/integrationtest/malicious_fixture_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run 'PathTraversal|Symlink|MaliciousFixture' -v   # 3/3 PASS (one with 2 sub-tests)"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: 4d81590
next_action: qa-07（本波下一個指派節點）
assumptions:
  - "在寫任何東西之前，先完整讀過
    docs/implementation/vertical-slice/checkpoint.md 裡
    checkpoint-b09 自己的最終報告，確認*精確*找出那個節點自己的對
    抗性稽核已經涵蓋了什麼（它自己真正的發現：Verify 把
    manifest.Artifacts[].Path 直接拼接到 ArtifactRoot 上，完全沒
    有任何 traversal／symlink 防護，後來用一個新的
    safeArtifactPath 檢查修好；再加上兩個主動強化：
    writeArtifactDir 的 files-map-key 驗證，以及 Capture 自己的
    CheckpointID 驗證），這樣本節點自己的 fixture 才能瞄準真正不
    同的攻擊形態，而不是把同樣三個發現換個檔名重新衍生一次。"
  - "本檔案裡每一個情境，都*只*呼叫真實、凍結的
    app.RepositoryCheckpointService port
    （repocheckpoint.NewService）——絕不直接呼叫
    internal/repocheckpoint 自己 package 內部的自由函式
    （Capture／Verify／RestoreDryRun），而那正是 checkpoint-b09
    自己的對抗性測試所呼叫的（白箱，package
    repocheckpoint/repocheckpoint_test）。這正是任務說明要求的那
    種真正不同、外部、黑箱的視角：跟一個真實呼叫端（runtime 的
    orchestrator.CheckpointCreate，或 qa-02 自己的 E2E 情境）實際
    看到的是同一個表面。"
  - "情境 1（連鎖雙重 symlink 逃逸）：一條兩跳的 symlink 鏈
    （evidence.txt -> sub/hop1 -> 一個外部的密鑰檔案），相對於
    checkpoint-b09 自己單跳 symlink 與「父目錄本身是 symlink」的
    案例（兩者都已由那個節點自己的測試套件涵蓋）——確認逃逸內容
    從未出現在封存檔裡，*而且*一個合法的 untracked 手足檔案*確
    實*有出現，證明這是一次真實、有效的捕捉，而不是一次空洞的
    『全部都被跳過』式通過。"
  - "情境 2（竄改 manifest 造成的路徑穿越）：專門瞄準
    staged.patch.gz 這個 artifact，而不是 checkpoint-b09 自己的回
    歸測試
    （TestVerify_ManifestArtifactPathTraversal_Rejected）瞄準的
    untracked.zip 項目——證明 safeArtifactPath 這個修復能通用到不
    同種類的 artifact，而不只是 b09 剛好測到的那一個項目。此外，
    還讓它經過真實的 Service.Restore port（dry-run）驗證，比 b09
    自己那個只測 Verify 的回歸測試多走了一層，確認這個修復在整條
    capture -> archive -> verify -> restore-dry-run pipeline 上都
    成立，這正是本節點任務說明明確點名的範圍。確認 Restore 會回
    傳 ErrCodeConflict，並在 Details 裡帶有 traversal 相關的問題
    描述，而且密鑰檔案的內容完全不會外洩到 Details 或
    Message 裡。"
  - "情境 3（惡意 CheckpointID）：透過一個惡意的
    domain.IDGenerator，經由真實的 Service.Create 接縫觸發（模擬
    一個被入侵或有 bug 的 ID 產生依賴），而不是像
    checkpoint-b09 自己的測試那樣，直接把一個字面的
    CheckpointID 字串手動塞給那個自由函式 Capture——這是一個不同
    的攻擊面（呼叫端提供的依賴表現異常，相對於直接惡意的字面參
    數）。嘗試了兩種不同的穿越形態（一個以 './..' 開頭的巢狀穿
    越，以及一個藏在看似普通的前綴片段之後的巢狀穿越），兩者都跟
    b09 自己的兩個案例（'../../escape-checkpoint-id' 與一個裸的絕
    對路徑）不一樣。"
  - "沒有發現新的缺陷——每一個情境都確認了 checkpoint-b09 的修
    復，從這個外部視角、面對這些獨立設計的攻擊形態，依然成立。這
    是一個獨立驗證節點預期中、成功的結果，而不是蓋橡皮圖章：實際
    建置並執行了三種真正不同的攻擊構造，而不只是重新匯入既有的。"
blockers: []
findings:
  - severity: informational
    title: "checkpoint-b09 的路徑穿越／symlink 修復，從一個獨立、
      外部、黑箱的視角、用真正不同的攻擊 fixture 來看依然成立——
      沒有發現新的缺陷。"
    file: "internal/integrationtest/malicious_fixture_test.go; internal/repocheckpoint/security.go, verify.go, capture.go, atomicwrite.go (checkpoint, read-only reference only)"
    reproduction: "N/A——不是缺陷。go test ./internal/integrationtest/... -run 'PathTraversal|Symlink|MaliciousFixture' -v
      （3/3 通過）就是收斂證據。"
    expected_invariant: "repository checkpoint pipeline 裡針對路徑
      穿越、symlink 逃逸與惡意輸入的防護，在獨立演練、透過真實
      service port、使用 qa 自己設計（而非沿用 checkpoint 自己測
      試套件）的 fixture 時依然成立。"
    owning_role: "qa（本節點）——僅供參考；其他角色不需要採取任何
      行動。"
```

```yaml
node: qa-07
status: completed
artifacts:
  - internal/integrationtest/scheduler_doubleworker_test.go
validation:
  - "gofmt -l internal/integrationtest   # clean, no output"
  - "go build ./internal/integrationtest/...   # PASS"
  - "go vet ./internal/integrationtest/...   # PASS"
  - "go test ./internal/integrationtest/... -run DoubleWorkerRace -v   # 2/2 PASS"
  - "go test ./internal/integrationtest/... -race   # PASS"
  - "go test ./internal/integrationtest/... -run DoubleWorkerRace -race -count=20   # PASS, stable, ~90s, no flakiness across 20 outer repetitions (each containing its own internal 20-attempt loop for the single-job scenario — effectively 400 race trials for that scenario alone)"
  - "go build ./... && go test ./...   # whole repo, all 34 packages PASS, zero regressions"
  - "golangci-lint run ./internal/integrationtest/...   # 0 issues"
commit: 09b2be8
next_action: qa-09（本波最終節點——也就是你正在讀的這份報告）
assumptions:
  - "DAG 自己針對這一列的驗證指令
    （`go test ./internal/scheduler/... -run DoubleWorkerRace
    -race -count=20`）瞄準的是 internal/scheduler/...，那是
    runtime 的專屬路徑，不是 qa 的——qa 不能編輯
    internal/scheduler/** 或 internal/pause/** 底下的任何東西
    （agents/qa.md 自己的專屬路徑清單兩者都沒有列入）。依這一波明
    確的分派指示，改為在 internal/integrationtest 裡建了一個獨立
    的測試，只呼叫 runtime 已匯出的真實 API
    （scheduler.NewStore／Schedule／Claim／Get、
    pause.NewSQLiteStore、pause.Wake）——完全沒有編輯這兩個被排除
    的 package。"
  - "runtime-a09 已經在 package 層級把這個確切的 race 證明過兩次：
    internal/scheduler/lease_test.go 自己的
    TestLease_ConcurrentWorkersYieldOneClaim（真實、落在磁碟上的
    SQLite，但只在 scheduler 層），以及
    internal/pause/wake_test.go 自己的
    TestDuplicateWake_WorkersYieldOneResume（只在狀態機層，依那個
    檔案自己記載的設計選擇，明確是對著 pause.NewMemStore()，不是
    真實的 SQLite 支撐 store）。本節點真正的增量，是把這兩個真
    實、由磁碟上 SQLite 支撐的層級組合成*一個* race：N 個
    worker 各自嘗試一個 production scheduler worker 會執行的完整
    真實序列——先 Claim，然後（只有在贏了的情況下）針對被 claim
    的 job 自己真實的 PauseID 做 Wake——證明這兩個各自獨立證明過
    的恰好一次保證之間的接縫，不會引入新的 race 窗口，這是兩個上
    游測試各自都沒有真正回答過的問題。"
  - "延伸出第二個情境（多個獨立的 job 被多個 worker 同時競逐，比
    照 lease_test.go 自己的
    TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce 模
    式，但把 Wake 這一步也組合進去），確認每個 job／每個 pause 的
    恰好一次保證，在*交叉*競爭下依然成立（worker 競逐好幾個到期
    job 裡的*任何一個*，而不只是一個共用的 job），而不只是單一競
    爭點才會出現的產物。"
  - "依 DAG 自己的風險註記（『天生容易不穩定；需要重複執行』），
    同時跑了一個內部的重複競爭迴圈（在單一 job 測試裡明確跑
    20 次嘗試，比照 wake_test.go 自己記載的『qa-07 自己的
    -count=20 重複競爭紀律』50 次嘗試的先例），*以及* DAG 字面指
    名的外層 `go test -count=20`——在所有重複執行中都很穩定，沒
    有觀察到任何不穩定現象。"
  - "每一個 worker goroutine 的 panic 都會被明確捕捉並回報
    （recover() + t.Errorf，而不是讓 goroutine 悄悄崩潰，那可能讓
    wg.Wait() 永遠掛住，或把一個真正的失敗偽裝成測試逾時，而不是
    一個可據以行動的錯誤）。"
blockers: []
findings:
  - severity: informational
    title: "組合起來的 scheduler-claim ＋ pause-wake 雙 worker
      race 正確成立——這兩個各自獨立證明過的恰好一次保證之間的接
      縫，沒有出現新的 race 窗口。"
    file: "internal/integrationtest/scheduler_doubleworker_test.go; internal/scheduler/lease.go, internal/pause/wake.go (runtime, read-only reference only)"
    reproduction: "N/A——不是缺陷。go test ./internal/integrationtest/... -run DoubleWorkerRace -race -count=20
      （20 次重複執行都很穩定）就是收斂證據。"
    expected_invariant: "在並行競爭下，不論是競逐一個共用
      job，還是同時競逐多個獨立 job，每個到期 job 都恰好只有一個
      worker 完整的 Claim-then-Wake 序列會成功，且
      scheduler 與 pause 這兩層一起用的都是真實、由磁碟上
      SQLite 支撐的 store。"
    owning_role: "qa（本節點）——僅供參考；其他角色不需要採取任何
      行動。"
```

## qa-09：最終報告（依嚴重度排序，涵蓋整個角色範圍）

這是 qa 被指派的最後一個 DAG 節點。依照 `agents/qa.md` 的「最終報
告」小節，以及這一波自己的任務指示，本報告涵蓋的是 qa 這個角色在
vertical-slice 裡*整個*範圍——qa-01 到 qa-09——而不只是這一波的四
個節點。`go test ./... -race` 已重新執行，作為本節點自己必要的驗
證（見下方「全倉庫測試健康度」）；`golangci-lint run ./...`（整個
倉庫）也已重新執行，結果乾淨。

### 全倉庫測試健康度（qa-09 自己的驗證）

```text
go build ./...                 -> OK, no errors
golangci-lint run ./...        -> 0 issues
go test ./... -race            -> ALL 34 packages PASS (32 with real
                                   tests, 2 no-test-files packages:
                                   internal/app, internal/buildinfo)
```

完整、逐一 package 的 `go test ./... -race` 輸出（本次執行）：

```text
ok  	github.com/huaiche94/auspex/cmd/auspex	1.449s
?   	github.com/huaiche94/auspex/internal/app	[no test files]
ok  	github.com/huaiche94/auspex/internal/app/wiring	11.339s
ok  	github.com/huaiche94/auspex/internal/artifacts	1.968s
?   	github.com/huaiche94/auspex/internal/buildinfo	[no test files]
ok  	github.com/huaiche94/auspex/internal/cli	3.271s
ok  	github.com/huaiche94/auspex/internal/clock	2.010s
ok  	github.com/huaiche94/auspex/internal/config	2.547s
ok  	github.com/huaiche94/auspex/internal/domain	1.422s
ok  	github.com/huaiche94/auspex/internal/evaluation	127.047s
ok  	github.com/huaiche94/auspex/internal/features	1.542s
ok  	github.com/huaiche94/auspex/internal/gitx	26.602s
ok  	github.com/huaiche94/auspex/internal/hooks/claude	1.719s
ok  	github.com/huaiche94/auspex/internal/idgen	1.904s
ok  	github.com/huaiche94/auspex/internal/integrationtest	(cached)
ok  	github.com/huaiche94/auspex/internal/lock	2.823s
ok  	github.com/huaiche94/auspex/internal/orchestrator	10.818s
ok  	github.com/huaiche94/auspex/internal/paths	1.901s
ok  	github.com/huaiche94/auspex/internal/pause	39.852s
ok  	github.com/huaiche94/auspex/internal/policy	1.658s
ok  	github.com/huaiche94/auspex/internal/predictor	1.969s
ok  	github.com/huaiche94/auspex/internal/predictor/quota	1.498s
ok  	github.com/huaiche94/auspex/internal/predictor/risk	1.608s
ok  	github.com/huaiche94/auspex/internal/predictor/runway	1.403s
ok  	github.com/huaiche94/auspex/internal/predictor/scope	1.455s
ok  	github.com/huaiche94/auspex/internal/predictor/token	1.471s
ok  	github.com/huaiche94/auspex/internal/progress	43.714s
ok  	github.com/huaiche94/auspex/internal/providers/claude	1.335s
ok  	github.com/huaiche94/auspex/internal/redact	37.609s
ok  	github.com/huaiche94/auspex/internal/repocheckpoint	46.425s
ok  	github.com/huaiche94/auspex/internal/scheduler	26.277s
ok  	github.com/huaiche94/auspex/internal/statecheckpoint	25.590s
ok  	github.com/huaiche94/auspex/internal/storage/sqlite	16.658s
ok  	github.com/huaiche94/auspex/internal/telemetry/claude	9.739s
ok  	github.com/huaiche94/auspex/internal/testutil/fakes	1.283s
ok  	github.com/huaiche94/auspex/pkg/protocol/v1	1.269s
```

這次執行，以及上面針對 qa-07 自己 scheduler race 的專屬
`-count=20` 壓力測試，都沒有觀察到任何回歸或不穩定失敗。

### 依嚴重度排序的發現（P0／P1／P2）

依 `agents/qa.md`：`P0 blocks merge`（P0 阻擋合併）、`P1 must fix
before demo`（P1 必須在 demo 之前修好）、`P2 documented
follow-up`（P2 記錄後續追蹤）。依同一小節的要求，每個項目都點名確
切的檔案、重現步驟、預期不變性，以及負責角色。

**P0 ——阻擋合併：無。**

截至本報告為止，qa 在 vertical-slice 範圍內的任何地方都不存在
P0 發現。本角色被賦予驗證職責的任何不變性（冪等性、重啟安全性、路
徑穿越／symlink 安全性、密鑰／原始 prompt 外洩、race 安全性、治理
文件是否存在、CI 是否跑綠），目前都沒有被任何真實、可重現的缺陷違
反到會讓合併目前的 `vertical-slice/qa` branch 變得不安全的程度。

**P1 ——必須在 demo 之前修好：**

1. **沒有任何 production 程式碼路徑，把一個已持久化的
   claude-provider `v1.Event` 接到
   `internal/progress.CompleteNode.Run`**（最早由 qa-04 發現，截
   至本報告仍未解決）。
   - 檔案：`internal/orchestrator/hooks.go`
     （HandleStop／HandleUserPromptSubmit／HandleStopFailure／
     HandleStatusLine 把一個 provider 事件正規化並持久化之後就停
     手）；`internal/telemetry/claude/normalizer.go`（沒有任何
     producer 會設定 `Event.TaskID`／`Event.ProgressNodeID`——確
     認這一波仍未改變：這兩個欄位在 `pkg/protocol/v1.Event` 上依
     然是普通的 `string` 欄位，每一條真實的 normalizer 呼叫路徑
     都沒有設定它們）；`internal/progress/complete_node.go` 的
     `CompleteNodeInput` 與 `internal/app/ports.go` 的
     `CompleteNodeRequest`（依然凍結為恰好 `{NodeID,
     IdempotencyKey, Artifacts[, RepositoryCheckpointID]}`——完全
     沒有 `v1.Event`／`EventID`／`EventType` 欄位，這一波重新讀過
     `internal/app/ports.go` 確認過）；
     `internal/app/wiring/wiring.go`（依然沒有接上
     `internal/telemetry/claude` 與 `internal/progress`／一個真
     實的 `app.ProgressTreeService` 之間的橋接——透過
     runtime-b10 自己 `restart_test.go` 的 package doc
     comment 確認過，那份文件獨立再次確認了這個確切的缺口：
     『ProgressTree 是 checkpoint Part A 的缺口……沒有任何單一型
     別完整實作了 7 個方法的 `app.ProgressTreeService`
     port』）。
   - 重現步驟：`go test ./internal/integrationtest/... -run TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists -v`
     ——依然通過，也就是說這個缺口依然真實存在（一個真實、經過正
     規化的 Stop 事件，其 `TaskID`／`ProgressNodeID` 依然是空字
     串）。這一波也透過 qa-02 自己的端對端情境再次確認：要驅動字
     面意義上的 vertical-slice demo，就需要這個測試自己的檔案，
     直接透過 `internal/progress.CompleteNode` 來完成一個
     Progress Tree 節點，用的是手刻的 `CompleteNodeInput`，而不
     是任何真實由事件驅動的觸發——完全同一個缺口，現在也明顯在
     最高風險的 demo 情境裡是不可或缺的，而不只是一個抽象的發
     現。
   - 預期不變性：某種真實的 provider 觀測，應該要能端對端驅動一
     次真實的 Progress Tree 節點完成（Constitution §6.1 的『
     Progress Tree 是唯一權威、持久的任務狀態……絕不是 agent 自己
     聲稱完成』意味著必須有某種真實訊號能驅動它往前推進）——但今
     天完全沒有這種機制；事件 pipeline 與完成 pipeline 各自獨立
     來看都是正確的、也都各自測試得很完整，但連接兩者的中間層在
     production 程式碼裡並不存在。
   - 負責角色：`contract-integrator`（在凍結的
     `v1.Event`／`CompleteNodeRequest` 契約上需要一個新的跨元件
     port／欄位，或者需要一個有記載的決定：這個解析改走另一條尚
     未建立的查找路徑——Constitution §4.2 把
     `pkg/protocol/v1/**` 與 `internal/app/ports.go` 專屬保留給
     這個角色），並與 `claude-provider`（一旦有了解析機制，就需
     要填入 `TaskID`／`ProgressNodeID`）以及
     `checkpoint`／`runtime`（不論最終由哪個角色建置實際的
     consumer／adapter，以及 runtime-b10 自己的 doc comment 獨立
     標記出來、在同一個領域裡依然缺失的統一
     `app.ProgressTreeService`／`app.GracefulPauseService`
     adapter）協調處理。
   - 為什麼是 P1 而不是 P0：這個缺口所涉及的每一個獨立元件
     （claude-provider 的 normalizer、checkpoint 的
     CompleteNode、runtime 的 hook handler）本身都是正確的，也都
     通過自己的測試；沒有任何既有不變性被違反；vertical-slice 凍
     結的 DAG 這一波也從未把「建置這個 adapter」明確指派給任何節
     點。這是一個真實、往前看的整合缺口，不是回歸——但它恰恰卡在
     『字面意義上的 vertical-slice demo』（qa-02）能否在一個*真
     實*部署系統裡端對端運作的路徑正中央（相對於這個測試套件自
     己的 TEST-ONLY 膠水頂替它），所以應該在任何正式 demo 之前解
     決，而不只是被追蹤成一個「有一天會做」的 nice-to-have。

**P2 ——記錄在案的後續追蹤：**

1. **儲存庫根目錄不存在 LICENSE／NOTICE 檔案**（最早由
   qa-08 標記，這一波重新確認依然缺席）。
   - 檔案：儲存庫根目錄（不存在 `LICENSE`／`NOTICE`
     檔案；`foundation` 自己的專屬路徑清單有點名它們，而且
     `foundation-09` 自己的 progress artifact 已經獨立標記過這個
     缺口）。
   - 重現步驟：在儲存庫根目錄執行 `ls LICENSE NOTICE 2>&1`——兩者
     都回報『No such file or directory』。
   - 預期不變性：`Auspex_ADD.md` §30.1 的『Required files』清
     單，以及 `CONTRIBUTING.md`／`GOVERNANCE.md`（兩者都是
     qa-08 自己的產出物）都把 Apache-2.0 點名為專案授權，與
     `README.md` 的 Tech stack 表格一致——但目前還沒有實際的
     `LICENSE` 檔案能支撐這個宣稱。
   - 負責角色：`foundation`（路徑擁有者）／
     `contract-integrator`（最終簽核）——完全不在 qa 自己的專屬
     路徑範圍內，依 Constitution §4.4 在此重新標記，不是新發現。
   - 不列為 P1：這是一個文件／合規完整性的缺口，不是運行中系統的
     安全性、正確性或安全（security）缺陷；它不會阻擋一次功能性
     demo，只影響最終 OSS 發版的整潔度。

2. **checkpoint 的 Repository Checkpoint patch 捕捉，只會對
   `+`／`-` 行內容做密鑰形態的 redact，從不處理 `.git` 追蹤的
   binary-diff 標頭或檔名本身**（一個範圍註記，不是回歸——記錄下
   來是為了可追溯性）。
   - 檔案：`internal/repocheckpoint/patchredact.go`（依那個檔案
     自己的 doc comment，設計上只會 redact staged／unstaged
     patch 裡以 "+"／"-" 開頭的行內容，這樣
     `git apply --check` 才能在 patch 其餘部分繼續正常運作）。
   - 重現步驟：N/A——本測試套件裡沒有任何測試建構過長得像密鑰的
     *檔名*，或長得像密鑰的 binary-diff 標頭；這是一個理論上的殘
     餘攻擊面，不是已確認的外洩。
   - 預期不變性：Constitution §7 規則 2『原始 prompt 與敏感內容
     預設不得被持久化』若套用到最大限度，也應該涵蓋出現在
     patch 標頭裡、長得像密鑰的檔名——目前這超出 redaction 這一步
     的範圍，這是一個站得住腳、刻意的設計選擇（在修復當下已記
     載），而不是疏漏。
   - 負責角色：`checkpoint`（如果未來判斷值得關閉這個缺口）／
     `qa`（如果 lead 判斷這個殘餘攻擊面值得一個未來的專屬測
     試）。依本報告自身『要全面……不是蓋橡皮圖章』的指示，記錄在
     此作為一個記錄在案的後續追蹤，並不是因為已經展示出一個具體
     的攻擊利用。

### 全部九個 qa 節點各自結果的總覽，供一站式參考

| Node | Deliverable | Outcome |
|---|---|---|
| qa-01 | 跨平台 CI | 已完成；有記載、刻意的 Windows race detector 平台拆分；無缺陷 |
| qa-02 | E2E 高風險 Claude fixture 流程（vertical-slice demo） | 已完成；一個連貫的真實端對端情境通過；揭露出 qa-04 的 P1 缺口是不可或缺的（見上文） |
| qa-03 | 同一 DB 上的重啟測試 | 已完成；跨角色狀態（5 個角色的儲存）在同一個共用檔案裡撐過一次真實重啟；無缺陷 |
| qa-04 | 重複／順序錯亂事件測試 | 已完成；發現並轉送了上方的 **P1** 發現（依然開放中） |
| qa-05 | 原始 prompt／密鑰外洩掃描器 | 已完成；發現一個 **P1**（tracked 檔案 diff 裡長得像密鑰的內容未被過濾），已由 checkpoint（`f981bde`）**轉送並修復**，這一波重新驗證通過 |
| qa-06 | 路徑穿越／symlink／惡意 fixture 測試 | 已完成；獨立的對抗性 fixture 確認 checkpoint-b09 自己的修復（那個節點自己發現並自行修復的一個真實 P1／安全性發現）從外部視角來看依然成立；無新缺陷 |
| qa-07 | Scheduler 雙 worker／lease race 測試 | 已完成；獨立、整合層級地把這個 race 組合到兩個真實 SQLite 支撐的層級上；無缺陷 |
| qa-08 | Support-bundle／doctor 隱私基準（透過治理文件） | 已完成；標記了上方的 **P2** LICENSE／NOTICE 缺口（依然開放中，不屬於 qa 擁有） |
| qa-09 | 最終報告 ＋ `go test ./...` 證據 | 本報告 |

### 結語

這份報告完成了 qa 這個角色**整個 vertical-slice DAG 範圍**——
`docs/implementation/vertical-slice/EXECUTION_DAG.md` 裡
qa-01 到 qa-09，全部九個節點，現在都是 `status: completed`。不再
有任何 qa 擁有的 DAG 節點剩下。上面那一個開放中的 **P1**（缺失的
provider-event 到 node-completion 的 adapter）與一個開放中的
**P2**（LICENSE／NOTICE），依 Constitution §4.4 都已經正確轉送給
各自負責的角色——兩者都不是 qa 該負責修復的，依 `agents/qa.md` 的
硬性規則，本報告不會嘗試修復任何一項。lead 自己的最終整合關卡
（`contract-integrator-final`）是整個專案剩下的唯一節點；本報告的
用意，是給它一份精確、誠實的待辦清單，而不是蓋一個橡皮圖章。
