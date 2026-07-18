# runtime — 進度產物

> 🌐 [English](runtime.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

> **Phase 11 章節附加於 Phase 10 節點紀錄之後** — 參見「Phase 11」
> 標題。Phase 11 完成了 `runtime-b10`，也就是 Part B 最終的
> 整合／可靠性關卡，同時也是這個角色被指派的最後一個 DAG 節點——
> 本波次結束後，所有曾指派給 `runtime` 的節點（`a01`-`a11`、
> `b01`-`b10`，共 21 個節點）全數完成。核心成果：一項真實的
> 「同一 SQLite 檔案下的行程內重啟」（in-process-restart-same-SQLite-file）
> 證明（同時涵蓋一次乾淨關閉後的重啟，以及一次真正被 SIGKILL 的子行程
> 崩潰重啟），並補上了過程中發現的一個真實且被反覆標記的缺口
>（`pause.PauseStore` 一直沒有 SQLite 版實作，只有記憶體版的
> `MemStore`——先前五個 Part A 節點各自都把這件事延後處理），
> 以及一個確實存在的 Part B「測試」缺口（「CLI 黃金測試」，
> 先前 b01-b09 從未有任何節點建置過）。本波次沒有新增 ADR，
> 也沒有跨角色變更請求。

> **Phase 10 章節附加於 Phase 9 節點紀錄之後** — 參見「Phase 10」
> 標題。Phase 10 依序完成兩個節點，各自獨立驗證並提交：
> `runtime-a11`（最終的 Part A 整合關卡——完整生命週期的
> 崩潰注入掃描，並補上這一輪發現的唯一真實缺口，也就是
> provider 中斷失敗狀態機的整合）以及 `runtime-b09`
>（所有 P0 CLI 指令的一致錯誤合約與隱私閘門稽核，補上了
> 這一輪發現的 JSON 錯誤呈現缺口）。本波次沒有新增 ADR，
> 也沒有跨角色變更請求。`runtime-b10`（此角色的最後一個節點）
> 留待未來的波次。

> **Phase 9 章節附加於 Phase 8 節點紀錄之後** — 參見「Phase 9」
> 標題。Phase 9 依序完成三個節點，各自獨立驗證並提交：
> `runtime-a09`（重複喚醒的恰好一次保證 + 取消優先於競態）、
> `runtime-a10`（provider 中斷者／恢復者的 fake 合約測試）、
> `runtime-b06`（決策允許／拒絕改為串接真正的
> `internal/evaluation.Service`，取代 runtime-b03 原本的 fake）。
> 本波次沒有新增 ADR，也沒有跨角色變更請求。

> **Phase 5 章節附加於 Phase 4 節點紀錄之後** — 參見「Phase 5」
> 標題。Phase 5 在一輪之內完成六個節點：這個角色目前所有已解鎖的
> 前緣節點（`runtime-a02`、`runtime-a06`、`runtime-b03`、
> `runtime-b04`、`runtime-b05`、`runtime-b08`）。本波次沒有新增
> 跨角色變更請求；Phase 4 對 foundation 的 migrate_test.go
> 變更請求已在本波次開始前解決（已確認：本分支上
> `go test ./internal/storage/sqlite/...` 完全綠燈）。

> **Phase 4 章節附加於 Phase 3 節點紀錄之後** — 參見「Phase 4」
> 標題。Phase 4 新增 `runtime-a01`（Part A 的遷移範圍
> 0050-0059，此角色第一個 Part A 節點）與 `runtime-b02`
>（應用程式接線），並包含**一項對 `foundation` 的跨角色變更請求**
>（`internal/storage/sqlite/migrate_test.go` 中過時的精確筆數／
> 版本斷言），合併整合者在合併此分支前應先閱讀。

這是 `runtime` 的第一份進度產物。根據 `agents/runtime.md`，這個
角色整合了兩個內部子元件——**Part A**（Graceful Pause、Safe
Points、Durable Scheduler）與 **Part B**（應用程式協調、CLI、
Local API）。Phase 3 指派的節點 `runtime-b01` 僅涵蓋 Part B；
Part A（`internal/pause/**`、`internal/scheduler/**`）尚未被
此產物觸及，目前也還沒有相關紀錄。

## 交接紀錄（Constitution §6.7 / agents/runtime.md「Handoff」）

- **CLI 套件結構**：`internal/cli.NewRootCmd() *cobra.Command` 是
  唯一匯出的進入點，呼應 foundation-01 直接在 `cmd/auspex/main.go`
  中建立的建構子慣例（`newRootCmd()`／`newVersionCmd()`，未匯出，
  因為該檔案屬於執行檔自己的套件）。由於 `internal/cli` 是一個
  獨立套件，預計未來的根接線步驟會匯入它，因此 `NewRootCmd` 是
  匯出的；套件內其他每個建構子（`newVersionCmd`、`newHookCmd`、
  `newHookClaudeCmd`、`newInitCmd`、`newEvaluateCmd`、
  `newDecisionCmd`、`newCheckpointCmd`、`newProgressCmd`、
  `newStateCmd`、`newPauseCmd`、`newResumeCmd`、`newSchedulerCmd`、
  `newStatusCmd`、`newDoctorCmd`）維持未匯出，與它們最終會呼叫的
  ports／DTO 的細緻程度一致。
- **Stub 錯誤形狀**：`version` 以下的每個指令都回傳
  `notImplemented(command string) error`（`internal/cli/errors.go`），
  建立凍結版的 `*domain.Error`（`internal/domain/errors.go`、
  `CONTRACT_FREEZE.md`「Error contract」），其中
  `Code: ErrCodeUnavailable`、`Retryable: true`，且
  `Details["command"]` 設為以點分隔的指令路徑（例如
  `"hook claude user-prompt-submit"`）。刻意選用 `ErrCodeUnavailable`
  而非 `ErrCodeInternal`：指令介面本身是正確的，一旦對應的服務
  （`EvaluationService`、`ProgressTreeService`、
  `GracefulPauseService` 等——`internal/app/ports.go`）由後續節點
  （`runtime-b02` 起）接上線，就能正常運作；這是操作上的
  「尚未可用」，而不是程式碼缺陷。`version` 是唯一的例外——
  它沒有服務依賴（僅使用 `internal/buildinfo.String()`），是完全
  真實的實作。
- **指令樹**：`NewRootCmd` 在一次呼叫中註冊了 `agents/runtime.md`
  Part B 所列的全部 18 個 P0 葉節點指令，為求可讀性拆成兩個檔案——
  `internal/cli/root.go`（根指令及除 `hook` 子樹外的所有指令）與
  `internal/cli/hook.go`（`hook claude {statusline,
  user-prompt-submit, stop, stop-failure}`，之所以獨立出來是因為
  它是三層子樹而非單一指令，且有足夠自身的命名慣例討論——見下方——
  值得擁有自己的檔案與套件文件段落）。
- **`cmd/auspex/main.go` 未被更動。** 依 `agents/runtime.md`
  （「不要編輯 `cmd/auspex/main.go`；contract-integrator 與
  foundation 角色負責整合根接線。在自己擁有的路徑下新增指令
  建構子。」）以及 Phase 3 任務簡報，本節點僅建置 `internal/cli`
  的建構子。`cmd/auspex/main.go` 目前仍只接了 `version`
  （來自 foundation-01，Phase 1），尚未呼叫 `cli.NewRootCmd()`——
  那項整合明確不在此角色的範圍內，屬於後續步驟中
  `contract-integrator`／`foundation` 的工作。DAG 的驗證指令是對
  *既有*的 `cmd/auspex` 執行檔（仍只有 `version`）執行，用以確認
  `internal/cli` 能乾淨地編譯進此模組，且不會破壞既有建置；
  `internal/cli` 自身的 `--help` 行為（完整 P0 樹）則直接在套件層級
  由 `internal/cli/root_test.go` 的 `TestHelpSucceeds` 驗證，因為
  目前還沒有任何自有的執行檔目標會接上完整指令樹。
- **依賴請求**：無。Cobra（`github.com/spf13/cobra`）與
  `internal/buildinfo`／`internal/domain` 已經可用
  （foundation-01、Bootstrap）；不需要新增 `go.mod` 項目。

## 命名慣例的判斷取捨：kebab-case 的 hook 子指令

`docs/implementation/vertical-slice/wave2-analysis/ADR_Recommendations.md`
的 REC-03 記載了一個真實、至今仍未解決的分歧：`Auspex_ADD.md`
附錄 E.3 以 PascalCase 拼寫 Claude Code 的 hook 子指令
（例如 `UserPromptSubmit`，對應 Claude 自身 hook 事件名稱的大小寫），
而 `agents/runtime.md` 自身的 P0 指令清單、本節點的 DAG 驗證指令
（`docs/implementation/vertical-slice/EXECUTION_DAG.md` 中
`runtime-b01` 那一列）、以及垂直切片執行計畫的展示腳本，
則各自獨立採用 kebab-case（`user-prompt-submit`）。REC-03 明確指出
`runtime-b01` 真正的 CLI 指令樹，是這項決定第一次成為現實的地方，
並建議在此節點之前、而非之後，透過 ADR 解決——截至此次提交，
該 ADR 尚未撰寫。

本節點採用 **kebab-case**（`auspex hook claude
user-prompt-submit`、`stop-failure`），基於兩個各自獨立的理由：

1. 依 Constitution §2 的文件優先順序，`agents/runtime.md`
   （角色範圍的操作性文件，第 4 層）是明確指出此角色實際指令介面
   最具體的文件，且它逐字使用 kebab-case。`Auspex_ADD.md`（第 2 層）
   整體上架構層級較高，但 Constitution §1「每個主題僅有一份權威文件」
   的表格中，並未特別針對 CLI 子指令字串大小寫指定唯一的權威來源，
   而三份各自獨立撰寫、已凍結的文件都收斂到 kebab-case（相對於
   只有一份指向 PascalCase），這本身就是專案其餘部分實際依照哪種
   拼法建置的證據（依 REC-03，`integrations/claude/hooks.json`
   也已經使用 kebab-case）。
2. 本節點自身的 DAG 驗證指令（`go build ./internal/cli/... &&
   auspex --help`）並不會單獨測試子指令的大小寫，但指派此節點的
   任務簡報很明確：使用 kebab-case，對應 `agents/runtime.md`
   自身的 P0 清單，並把這個決定記錄下來，而不是悄悄發明第三種答案。

**這並不是對 REC-03 的解決。** 目前沒有撰寫任何 ADR；`runtime`
也沒有權限接受 ADR（Constitution §3.2——只有 `contract-integrator`
可以接受 ADR）。若日後透過已接受的 ADR 確認 `Auspex_ADD.md`
附錄 E.3 才是預定的大小寫寫法，修正是機械式的：重新命名
`internal/cli/hook.go` 的 `newHookClaudeCmd` 中四個 `Use` 字串，
並更新 `root_test.go`／`errors_test.go` 的路徑表即可——不會影響
任何其他檔案，因為每個 stub 指令除了 `Use` 字串外其餘完全相同。
在此明確標記，讓未來的波次不必重新發現這件事：**REC-03 仍應
被正式提出為一份真正的 ADR**，即使本節點已經做出了有記錄、
不會阻塞後續工作的判斷，暫時以 kebab-case 進行下去。

## 節點紀錄

```yaml
node: runtime-b01
status: completed
artifacts:
  - internal/cli/doc.go
  - internal/cli/errors.go
  - internal/cli/errors_test.go
  - internal/cli/root.go
  - internal/cli/root_test.go
  - internal/cli/hook.go
validation:
  - "gofmt -l internal/cli   # empty output"
  - "go build ./internal/cli/...   # OK"
  - "go vet ./internal/cli/...   # OK"
  - "go test ./internal/cli/... -race -v   # all PASS"
  - "go build -o <tmp> ./cmd/auspex && <tmp> --help   # OK (existing version-only binary; unaffected by this package)"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: a6a3eaa
next_action: runtime-b02 (App wiring) — blocked/not started this phase per explicit instruction to stop once runtime-b01 is Validated; Part A (internal/pause/**, internal/scheduler/**) also not started this phase, out of scope per task brief
assumptions:
  - "Kebab-case for `auspex hook claude ...` subcommands — see the
    dedicated section above. Documented, not silent; REC-03 remains open
    and should still be resolved by an accepted ADR."
  - "Every command below `version` is an honest stub returning
    domain.Error{Code: ErrCodeUnavailable, Retryable: true} rather than
    any real behavior, per explicit task instruction: none of
    orchestrator/evaluation/checkpoint/pause services exist yet this phase,
    and the DAG's own validation command
    (`go build ./internal/cli/... && auspex --help`) only requires
    `go build` and `--help` to work, not working commands."
  - "internal/cli/root.go groups most P0 leaf commands (version, init,
    evaluate, decision, checkpoint, progress, state, pause, resume,
    scheduler, status, doctor) into a single file rather than one file per
    command. The DAG estimated 6 files/350 LOC for runtime-b01; one file
    per command (13 top-level constructors) would have produced far more
    files than that estimate for what is, this phase, structurally
    identical boilerplate per command (a Use/Short/RunE stub). `hook` was
    split out on its own because it is a three-level subtree with its own
    naming-convention discussion, which justified a dedicated file and
    package-doc paragraph the other commands don't need yet. This may be
    resplit into per-domain files (e.g. a checkpoint.go, a pause.go) once
    real business logic lands behind each command in runtime-b02 onward
    and the single-file grouping stops being the natural shape."
  - "NewRootCmd is exported (capital N) unlike foundation-01's
    unexported newRootCmd in cmd/auspex/main.go, because
    internal/cli is a separate package a future root-wiring step needs to
    import; cmd/auspex/main.go's own newRootCmd stays package-private
    since nothing outside that package needs it. Both conventions coexist
    correctly per Go visibility rules; this is not a contradiction of
    foundation's established pattern, just the same pattern applied at a
    package boundary that didn't exist yet when foundation-01 was written."
blockers: []
```

---

# Phase 4

分支：`vertical-slice/runtime`，在 Phase 4 開始前透過 fast-forward
從 `main`（Phase 3 整合狀態，`664436d`）同步——這是為了讓
foundation-06 的遷移引擎與 0001-0004 核心 schema 檔案存在於此分支上。
本波次指派的節點依序執行：`runtime-a01`（Part A 遷移
0050-0059），接著是 `runtime-b02`（應用程式接線）。

## runtime-a01 — Graceful Pause/Scheduler 核心遷移

### 交付內容

- `internal/storage/sqlite/migrations/0050_pause_records.sql` ——
  `pause_records` + `idx_pause_status`（ADD §12.2/§12.3）。
- `internal/storage/sqlite/migrations/0051_wake_jobs.sql` ——
  `wake_jobs` + `idx_wake_jobs_due`，包含
  `UNIQUE(pause_id, job_kind)`（schema 層級的恰好一次喚醒錨點）
  以及 ADD §12.4 租約查詢所需的欄位組合（`status`、`run_after`、
  `lease_owner`、`lease_expires_at`、`attempts`、`max_attempts`）。
- `internal/storage/sqlite/migrations/0052_resume_attempts.sql` ——
  `resume_attempts` 稽核追蹤表。
- `internal/storage/sqlite/migrations_0050_pause_test.go` ——
  此範圍的測試（全部命名為 `TestMigration0050_*`，讓 DAG 的驗證指令
  `go test ./internal/storage/sqlite/... -run Migration0050` 恰好
  選中這些測試）：內嵌檔案載入、從空白狀態套用（資料表 + §12.3
  索引皆存在）、可重複套用的冪等性、對 foundation 的
  `tasks`／`provider_sessions` 強制外鍵（拒絕未知 id；完整的
  repository → worktree → task → pause 串聯）、`runway_forecast_id`
  NOT NULL、wake-job 串聯刪除 + 種類唯一性、resume-attempt
  在 wake-job 被刪除時存活（SET NULL）但在 pause 被刪除時不存活
  （CASCADE）。

### 與 ADD §12.2 標準外鍵之間有記錄的偏離（需要 contract-integrator 過目；呼應 0004_tasks.sql 的先例）

ADD §12.2 宣告 `pause_records.turn_id/runway_forecast_id/
state_checkpoint_id/repository_checkpoint_id` 為 `REFERENCES` 指向
`turns`（claude-provider 0010-0019）、`runway_forecasts`（predictor
0040-0049）、`state_checkpoints`（checkpoint 0020-0029）、以及
`repository_checkpoints`（checkpoint 0030-0039）。這些遷移檔目前
都還不存在。SQLite 在 `CREATE` 時接受向前參照的外鍵宣告，但在
`PRAGMA foreign_keys = ON` 之下，任何觸及子表的 DML 都會解析
*每一個*父表——**包含由 `repositories`／`worktrees`／`tasks`
刪除所觸發的串聯處理**。實測結果（本節點的第一版草稿使用了標準
外鍵）：foundation 自己的 `TestCoreMigrations_ForeignKeys_*` 測試
在單純的 `DELETE FROM repositories` 上立即失敗，錯誤是
`no such table: main.repository_checkpoints`，也就是說向前外鍵
會讓整個 repo 中無關的 DML 中毒，並且會因為其他三個角色的範圍
而硬性擋住 `runtime-a02`（暫停狀態機，DAG 排程僅依賴
runtime-a01）。

解法：這四個欄位以純 `TEXT` 指標形式出貨，正是 foundation-06 在
`0004_tasks.sql` 中為 `tasks.active_node_id` → `progress_nodes`
所立下的先例。今天就能被強制執行的外鍵（指向 `tasks`、
`provider_sessions`，以及本範圍內 `wake_jobs`／`resume_attempts` →
`pause_records`）都已宣告並測試。**向 contract-integrator 提案：**
待 0010-0049 全數到位後，可以 (a) 永久接受這個純指標的先例
（與 0004 一致），或是 (b) 指派 runtime 在自己的範圍內
（0053+）做一個後續遷移，透過 SQLite 的 copy-drop-rename 模式，
以標準外鍵集重建 `pause_records`。無論哪一種，這個決定都應該由
更上層來做；本節點並沒有悄悄地永久選擇 (a)——它選的是今天唯一能
讓 repo 的 DML 正常運作的選項，並在此標記出這個選擇。

### 變更請求 → foundation（Constitution §4.4——非 runtime 編輯）

`internal/storage/sqlite/migrate_test.go`（foundation 的檔案）中
有三個斷言限制過嚴，只要*任何*後續角色的遷移範圍一到位就會失敗——
這與 `migrate.go` 自身的設計註解（「後續角色的遷移……會在存在時
自動被納入，這裡不需要任何變更」）互相矛盾：

1. `TestAllMigrations_LoadsCoreSchemaFiles` 斷言
   `len(migrations) == 4`——應改為只過濾 foundation 自己的
   0000-0009 範圍（就像
   `TestMigration0050_AllMigrationsIncludesPauseRange` 過濾出
   0050-0059 那樣）。
2. `TestCoreMigrations_FromEmptyDatabase` 斷言
   `CurrentVersion == 4`——應改為斷言 `>= 4`，或從
   `AllMigrations()` 推導期望值。
3. `TestCoreMigrations_ReopenFromFile_AppliesOnce` 斷言
   `CurrentVersion == 4`——同樣的修法。

在 foundation 套用這個機械式修正之前，`go test
./internal/storage/sqlite/...`（完整套件，不加 `-run` 過濾）在此
分支上會回報這三個失敗。**沒有任何 runtime 擁有的測試失敗**，
且這些失敗屬於斷言過時，而非行為問題：foundation 的外鍵／串聯／
唯一性行為測試在合併後的 0001-0052 schema 下全數通過。依
Constitution §4.4，runtime 沒有編輯該檔案，也沒有閒置等待；在此
標記給 foundation 與合併整合者知悉。

### 節點紀錄

```yaml
node: runtime-a01
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0050_pause_records.sql
  - internal/storage/sqlite/migrations/0051_wake_jobs.sql
  - internal/storage/sqlite/migrations/0052_resume_attempts.sql
  - internal/storage/sqlite/migrations_0050_pause_test.go
validation:
  - "go test ./internal/storage/sqlite/... -run Migration0050   # all 6 PASS"
  - "gofmt -l internal/storage/sqlite   # empty"
next_action: runtime-a02 (pause state machine) — NOT this phase, per explicit scope
assumptions:
  - "Plain TEXT (no FK) for pause_records' four references into
    not-yet-landed migration ranges — see the deviation section above;
    decision (a)-vs-(b) escalated to contract-integrator."
  - "migrations_0050_pause_test.go lives in internal/storage/sqlite/
    (foundation's directory) because the DAG's validation command
    requires tests selectable there and migration SQL is not testable
    from any runtime-owned Go package; the file is named with this
    range's 0050 prefix and contains only runtime-range tests. If
    contract-integrator prefers a different ownership carve-out
    (e.g. adding the filename to runtime's exclusive paths), that is a
    one-line agents/runtime.md change — requested here rather than
    self-granted."
blockers:
  - "foundation's migrate_test.go stale exact-count assertions (see
    CHANGE REQUEST above) — does not block this node's validation
    command, but blocks a fully green `go test ./...` until foundation's
    3-line fix lands."
```

## runtime-b02 — 應用程式接線（行程內組合層）

### 交付內容

- `internal/app/wiring/wiring.go` —— 組合容器：`Services`
  （每個凍結服務介面各一個欄位：`Evaluation`、`ProgressTree`、
  `StateCheckpoint`、`GracefulPause`、`RepositoryCheckpoint`——
  `internal/app/ports.go`）、`New(Services) (*App, error)`
  （fail-closed 建構：任何欄位為 nil 都會回傳凍結版的
  `domain.Error`，`ErrCodeValidation`、`Retryable: false`，且
  `Details["missing_services"]` 列出每一個缺口——組合上的錯誤會在
  啟動時就浮現，而不是在第一個碰到它的處理器中變成 nil 指標 panic）、
  每個服務各一個存取子，以及 `App.RootCmd()`——這是接線層與 CLI
  之間的介面，目前回傳 `internal/cli.NewRootCmd()` 的樹，未來
  runtime-b03 起會在此把真正的服務串進各個指令處理器。
- `internal/testutil/fakes/` —— 這個目錄第一次有內容
  （agents/runtime.md：「與 qa 角色協調」）：`doc.go`（模式合約）、
  `unconfigured.go`（共用的未設定 Func 行為），以及每個凍結服務
  介面各一個檔案（`evaluation.go`、`progresstree.go`、
  `statecheckpoint.go`、`gracefulpause.go`、
  `repositorycheckpoint.go`）。模式：`Fake<Interface>` 結構體，
  每個方法各有一個選填的 `<Method>Func` 欄位；編譯期斷言
  `var _ app.X = (*FakeX)(nil)`；呼叫未設定的方法會明確地以
  `domain.Error{Code: ErrCodeUnavailable, Retryable: false,
  Details: {fake, method}}` 失敗，而不是悄悄回傳零值。沒有呼叫
  紀錄／計數機制——需要的測試自行在各自的閉包中建置（Constitution
  §7 規則 10：不建立這個里程碑不需要的抽象）。
- `internal/app/wiring/wiring_test.go` —— 驗證：全部使用 fake
  時建構成功；每一個單獨缺少的服務都會以正確的代碼／可重試性／
  細節 fail closed；全部缺少時會列出全部五個；存取子回傳注入的
  實例（同一性）；透過容器呼叫時會抵達所設定的 fake 閉包，參數
  完整無誤（單純傳遞，不重新解讀）；未設定的 fake 方法透過容器
  呼叫時會明確失敗；`RootCmd()` 產出來自 runtime-b01、共 13 個
  頂層指令的完整 P0 樹。

### 交接紀錄

- **給 qa**：`internal/testutil/fakes` 刻意保持精簡且易於增量擴充。
  若整合測試需要有紀錄能力的 fake，請先在測試本地的閉包中加上
  該行為；只有在多個測試套件各自都需要同一件事時，才把共用機制
  提升進這個套件。
- **給 contract-integrator／foundation（根接線）**：預期的執行檔
  組合方式是 `wiring.New(Services{...真正的實作...})`，接著呼叫
  `app.RootCmd()`。`cmd/auspex/main.go` 依 agents/runtime.md 規定
  仍不由此角色更動。
- **給 runtime-b03 以後（本角色）**：當各指令逐漸取得真正的
  處理器時，把 `RootCmd` 目前直接呼叫 `cli.NewRootCmd()` 的做法，
  換成把 `a.services` 的介面傳入 cli 建構子；已經透過
  `App.RootCmd()` 呼叫的呼叫端不會受影響。

### 節點紀錄

```yaml
node: runtime-b02
status: completed
artifacts:
  - internal/app/wiring/wiring.go
  - internal/app/wiring/wiring_test.go
  - internal/testutil/fakes/doc.go
  - internal/testutil/fakes/unconfigured.go
  - internal/testutil/fakes/evaluation.go
  - internal/testutil/fakes/progresstree.go
  - internal/testutil/fakes/statecheckpoint.go
  - internal/testutil/fakes/gracefulpause.go
  - internal/testutil/fakes/repositorycheckpoint.go
validation:
  - "go test ./internal/app/wiring/...   # all PASS (DAG validation command)"
  - "go test ./internal/cli/... ./internal/app/wiring/... -race   # all PASS"
  - "gofmt -l internal/app/wiring internal/testutil   # empty"
  - "go vet ./internal/app/wiring/... ./internal/testutil/...   # OK"
  - "golangci-lint run ./...   # 0 issues, whole repo"
next_action: runtime-b03+ (real handler logic) and runtime-a02 (pause state machine) — NOT this phase, per explicit scope
assumptions:
  - "TxRunner and the ADR-041 predictor pipeline stages
    (ScopeEstimator/TokenForecaster/QuotaForecaster/RiskCombiner) are NOT
    fields of wiring.Services yet: the CLI's P0 commands consume the five
    high-level services only; pipeline stages are wired inside predictor's
    own EvaluationService implementation, and storage transactions are a
    per-service concern. Adding a field later is additive and
    non-breaking; adding it now would be speculative structure
    (Constitution §7 rule 10)."
  - "App.RootCmd() returning the still-stubbed runtime-b01 tree is the
    correct b02 shape: the DAG's validation command tests wiring
    construction, not handler behavior, and handler logic is explicitly
    runtime-b03+ scope."
blockers: []
```

---

# Phase 5

分支：`vertical-slice/runtime`，在 Phase 5 開始前透過 fast-forward
合併從 `main`（Phase 4 整合狀態，`5470e4d`）同步——乾淨無衝突
（此角色只擁有自己的路徑）。這次合併帶入了 `foundation` 的
migrate_test.go 範圍限定斷言修正、`checkpoint` 的 Part A/B 核心
遷移（0020-0039）、`predictor` 的配額預測器
（`internal/predictor/quota`），以及 `claude-provider` 的遙測
事件儲存（`internal/telemetry/claude/store.go`）。

本波次指派的節點依序執行，每個節點各自獨立驗證並提交：
`runtime-a02`（暫停狀態轉換驗證器）→ `runtime-a06`（持久排程
租約）→ `runtime-b03`（Evaluate 管線）→ `runtime-b04`（hook
指令處理器）→ `runtime-b05`（checkpoint 建立協調）→
`runtime-b08`（status／doctor 指令）——依任務簡報，Part A 排在
Part B 之前，因為 Part A 的兩個節點都被標記為高風險（狀態機與
並行正確性），而 Part B 的四個節點相對而言是建立在
runtime-b02 既有接線容器之上、風險較低的管線工作。

## runtime-a02 — 暫停狀態轉換驗證器

### 交付內容

- `internal/pause/doc.go` —— 套件文件，把三份文件的狀態名稱
  用詞對應到十二個凍結的 `domain.PauseStatus` 傳輸字串上：
  agents/runtime.md「必要狀態路徑」的敘述、`Auspex_ADD.md`
  §20.5 的 mermaid 圖，以及凍結列舉本身
  （`internal/domain/status.go`，由 `CONTRACT_FREEZE.md` 驗證）。
  敘述性文件中好幾個命名步驟（`observing`／`Active`、
  `safe_point_reached`、`persisting`、`wake_due`、
  `EmergencyInterrupt`、`MinimalCheckpoint`）各自對應到某一個
  凍結狀態——這裡明確記錄下來，而不是悄悄挑一個。
- `internal/pause/statemachine.go` —— 明確的合法轉換表
  （P0 交付項目 1）：`Event` 詞彙、`transitionTable`
  （每一條邊都以 `(from, event)` 為鍵）、`terminalStates`、
  `Validate`／`Apply`／`IsTerminal`／`IsKnownState`／
  `ValidEvents`，以及一個能區分未知狀態／終止狀態／無此邊
  三種拒絕原因的 `*TransitionError` 型別。
- `internal/pause/statemachine_test.go` —— 17 個測試，涵蓋完整
  的正常路徑、ADD §17.6 的緊急跳躍、ADD §20.15 的 checkpoint
  失敗 fail-closed 規則，以及所有僅憑狀態機層級就能證明的 Part A
  必要測試：配額不安全時重新排程、repo 重疊時阻擋、取消優先於
  競態中的喚醒、provider 中斷失敗可復原，再加上終止狀態／未知
  狀態／不合法邊的拒絕測試，以及完整的轉換表完整性結構檢查。

### 節點紀錄

```yaml
node: runtime-a02
status: completed
artifacts:
  - internal/pause/doc.go
  - internal/pause/statemachine.go
  - internal/pause/statemachine_test.go
validation:
  - "gofmt -l internal/pause   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/...   # OK"
  - "go test ./internal/pause/... -run StateTransition -race -v   # 17/17 PASS"
  - "go test ./internal/pause/... -race -v   # all PASS (same 17 — StateTransition is the whole package this node)"
commit: 7b125fc
next_action: runtime-a06
assumptions:
  - "State-name reconciliation across agents/runtime.md prose, ADD
    §20.5's diagram, and the frozen 12-value domain.PauseStatus enum —
    documented in doc.go, not silently picked. No new PauseStatus value
    was invented (Constitution §6 rule 4)."
  - "Interrupting has no cancel edge (a provider interrupt signal
    already in flight cannot be cancelled out from under itself) —
    a deliberate narrowing, tested explicitly
    (TestStateTransition_InterruptingHasNoCancelEdge) so a future
    reader doesn't have to reverse-engineer the omission from the table."
blockers: []
```

## runtime-a06 — 持久排程租約

### 交付內容

- `internal/scheduler/doc.go` —— 套件文件，把 ADD §12.4 的
  租約取得交易概念與 §12.7 的租約／重試參數，對應到這個 store
  的設計上。
- `internal/scheduler/lease.go` —— `Store`，對 `wake_jobs`
  資料表（runtime-a01 的遷移 0051）提供 `Schedule`／`Get`／
  `Claim`／`Renew`／`Complete`／`Fail`／`ReclaimExpired`。
  `Claim` 保留單一實體的 `*sql.Conn`（而非池化的 `*sql.Tx`），
  並直接在其上發出 `BEGIN IMMEDIATE`／`COMMIT`／`ROLLBACK`，
  對應 ADD §12.4 字面上的鎖定意圖。`Claim` 的判斷條件刻意放寬，
  超出 ADD §12.4 字面上的 `status='scheduled'` 文字，也會比對
  租約已過期的 `leased` 資料列，讓「過期租約被回收」這件事直接
  由 `Claim` 本身成立，而不僅僅透過另外的 `ReclaimExpired`
  重啟復原掃描（ADD §28.3 第 2 步，該掃描仍然存在，用於啟動時
  診斷）。
- `internal/scheduler/lease_test.go` —— 17 個測試：排程 + 取得、
  租約續約／完成／帶退避的失敗／耗盡最大嘗試次數的失敗
  （每一項都包含錯誤擁有者衝突的情境）、過期租約被回收（透過
  裸 `Claim` 及透過明確的 `ReclaimExpired`）、驗證，以及 DAG
  所要求風險對應的兩項並行性證明：
  `TestLease_ConcurrentWorkersYieldOneClaim`（多個 goroutine
  競爭同一個工作，恰好一個勝出）與
  `TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce`
  （N 個工作、M 個 worker，每個工作恰好被取得一次）——兩者都在
  `-race` 之下執行。

### 本節點自身測試在提交前抓到並修好的兩個真實錯誤

1. **自我死結（Self-deadlock）**：`Claim` 最初的實作在仍持有
   自己保留的交易用 `*sql.Conn` 開啟時，透過池化的 `*sql.DB`
   （`s.Get`）重新取得剛剛取得的工作。在連線池完全飽和時
   （許多並行的 `Claim` 呼叫者，`internal/storage/sqlite.DB`
   把連線池上限設為 8），這個重新取得的連線請求永遠無法被滿足——
   每個 goroutine 最終都同時卡在 `database/sql` 的連線等待佇列
   中。`TestLease_ConcurrentWorkersAcrossManyJobsEachClaimedOnce`
   在第一次執行時無限期卡住（實際經過約 4 分鐘、CPU 時間不到
   1 秒後只能用 `kill -9` 中止，這正是等待佇列死結、而非自旋
   的特徵）。修法是新增一個 `getJob(ctx, Querier, id)` 輔助函式，
   讓 `Claim` 在 `COMMIT` 之前對自己保留的連線呼叫它，而不是
   回到連線池。
2. **過期租約的盲點**：`Claim` 最初的 SELECT 只比對
   `status = 'scheduled'`，所以一筆 `leased` 但已過期的資料列
   （正是「重複 worker／過期租約」的情境）在另一個獨立的
   `ReclaimExpired` 呼叫先重設它之前，對 `Claim` 是不可見的——
   `TestLease_ExpiredLeaseReclaimedByAnotherWorker` 在第一次執行時
   失敗（`second Claim: Found = false, want true`）。修法是放寬
   SELECT／UPDATE 的判斷條件，使其也比對 `lease_expires_at`
   已過期的 leased 資料列（見上方「交付內容」）。

這兩個錯誤都是在任何提交發生之前，被本節點自身要求的測試抓到——
而不是之後被某個手足角色或在整合階段才發現。

### 節點紀錄

```yaml
node: runtime-a06
status: completed
artifacts:
  - internal/scheduler/doc.go
  - internal/scheduler/lease.go
  - internal/scheduler/lease_test.go
validation:
  - "gofmt -l internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/scheduler/...   # OK"
  - "go test ./internal/scheduler/... -run Lease -race -v   # 17/17 PASS"
  - "go test ./internal/scheduler/... -race -count=3   # stable across 3 runs, no flakes"
commit: d5948d9
next_action: runtime-b03
assumptions:
  - "Claim's SELECT/UPDATE predicate widened beyond ADD §12.4's literal
    text to also match expired-leased rows (not just scheduled ones) —
    documented deviation, justified by the required test's own name
    ('expired lease reclaimed') and by ADD §12.4 itself being labeled a
    concept (\"概念\"), not verbatim-mandatory SQL."
  - "wake_jobs.status values (scheduled/leased/done/dead) are this
    package's own vocabulary, per 0051_wake_jobs.sql's header leaving
    the column deliberately un-enumerated at the schema level for the
    owning role (this one) to define."
blockers: []
```

## runtime-b03 — Evaluate 管線

### 交付內容

- `internal/orchestrator/doc.go` —— 套件文件，將本節點的範圍限定在
  agents/runtime.md Part B 管線步驟 1-6，並說明為何沒有發明新的
  repository／worktree／session 解析器 port（目前還沒有針對這件事
  凍結的 port；`EvaluateRequest` 直接使用已經解析好的 ID，這是
  對於已經握有這些 ID 的 hook 處理器或 CLI 指令來說較實際的形狀）。
- `internal/orchestrator/evaluate.go` —— `Evaluate(ctx, Deps,
  EvaluateRequest) (EvaluateResult, error)`：（在提供 `TaskID` 時）
  載入 Progress Tree、透過一個窄範圍的本地
  `UsageObservationLoader` 介面載入使用量觀測資料、透過
  `internal/gitx`（checkpoint 角色的公開 Git 底層工具，本節點只是
  使用者而非擁有者）快照 Git 狀態，接著呼叫
  `app.EvaluationService.EvaluateTurn`，再呼叫 `.Decide`。三個
  操作性觀測步驟（Progress Tree／觀測資料／Git 快照）採
  fail-open——降級 `EvaluateResult` 的 `Has*` 旗標，但不中止；
  `EvaluateTurn`／`Decide` 本身（管線真正的目的）則採
  fail-closed（錯誤原樣往外傳遞）。
- `internal/orchestrator/evaluate_test.go` —— 16 個測試，涵蓋
  正常路徑（兩個服務呼叫依序都有發生）、驗證、服務為 nil 時
  fail-closed、兩種 fail-closed 傳遞情境，以及全部三種
  fail-open 降級情境（每一種都各自有一個「錯誤仍會降級、不會
  中止」測試，加上一個「有值時會載入該值」測試）。

### 節點紀錄

```yaml
node: runtime-b03
status: completed
artifacts:
  - internal/orchestrator/doc.go
  - internal/orchestrator/evaluate.go
  - internal/orchestrator/evaluate_test.go
validation:
  - "gofmt -l internal/orchestrator   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/...   # OK"
  - "go test ./internal/orchestrator/... -run Evaluate -race -v   # 16/16 PASS"
commit: 38dc881
next_action: runtime-b04
assumptions:
  - "No new resolver port invented for repository/worktree/session
    resolution (Constitution §7 rule 10) — EvaluateRequest takes
    already-resolved IDs directly; documented in doc.go."
  - "internal/gitx (checkpoint role's Git plumbing) is consumed
    directly as a public package, not faked — it is not one of the
    frozen app ports this phase's fakes cover, and it already has its
    own real, tested implementation from checkpoint's earlier phases."
blockers: []
```

## runtime-b04 — Hook 指令處理器

### 交付內容

- `internal/orchestrator/hooks.go` —— `HandleStatusLine`／
  `HandleUserPromptSubmit`／`HandleStop`／`HandleStopFailure`：
  每一個都透過 claude-provider-04 真正的、已整合的解析器
  （`internal/providers/claude`、`internal/hooks/claude`）解析，
  透過 claude-provider-04 真正的 `Normalizer`
  （`internal/telemetry/claude`）正規化，透過可注入、nil 安全的
  `EventPersister` 盡力儲存，並且（僅 `HandleUserPromptSubmit`）
  執行評估管線（runtime-b03 的協作物件），以呈現 ADD §22.3 的
  阻擋／允許回應形狀。每個處理器在輸入格式不正確時都採
  fail-open。
- `internal/orchestrator/hooks_test.go` —— 16 個測試，針對
  `testdata/provider-events/claude/**` 下的真實 fixture 檔案，
  包含 `TestHookHandlers_UserPromptSubmit_NeverSeesRawPromptText`，
  斷言 `EvaluateTurn` 收到的雜湊值是一個 64 字元的十六進位摘要，
  而不是 fixture 中原始的提示詞文字。
- `internal/cli/hook.go` —— 新增匯出的
  `NewHookClaudeCmd(HookDeps)`，也就是真正的指令樹，與既有的
  套件私有 stub 樹並存（原本的 `newHookClaudeCmd` 改名為
  `newHookClaudeStubCmd`，獨立的 `NewRootCmd()` 仍使用它）。
- `internal/app/wiring/wiring.go` —— `RootCmd()` 現在會用
  `NewHookClaudeCmd` 真正的子樹取代 stub 版的 `hook` 子樹，
  這是由一個新的選填欄位 `Services.Hooks`（`HookSupport`：
  `Clock`／`IDs`／`Persister`／`TxRunner`）建置而成，未設定時會
  回退為真正的 `domain.Clock`／`domain.IDGenerator`。

### 節點紀錄

```yaml
node: runtime-b04
status: completed
artifacts:
  - internal/orchestrator/hooks.go
  - internal/orchestrator/hooks_test.go
  - internal/cli/hook.go (modified)
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/app/wiring   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/...   # OK"
  - "go test ./internal/orchestrator/... -run HookHandlers -race -v   # 16/16 PASS"
  - "go test ./internal/cli/... ./internal/app/wiring/... -race   # all PASS"
commit: 624b27a
next_action: runtime-b05
assumptions:
  - "claude-provider-04's parsers/Normalizer are called directly (real,
    not faked), per the task brief's explicit instruction that they are
    already integrated."
  - "HookDeps.Evaluation is app.EvaluationService (fake this phase, see
    Fakes Used section below) — same dependency runtime-b03 already
    tracks, not a new gap."
  - "ADD §22.6's status-line compose-with-previous-command installer
    mechanism is out of scope this phase — HandleStatusLine
    normalizes+persists only; no internal/statusline package exists
    yet to own the composition step."
blockers: []
```

## runtime-b05 — Checkpoint 建立協調

### 交付內容

- `internal/orchestrator/checkpoint.go` —— `CheckpointCreate(ctx,
  Deps, Request) (Result, error)`：依序呼叫
  `app.StateCheckpointService.Create`，接著才呼叫
  `app.RepositoryCheckpointService.Create`，順序不會反過來。
  任一個依賴為 nil 時 fail closed（在任何呼叫之前就先檢查），
  並將任一服務的錯誤原樣傳遞；State 失敗時，Repository 甚至
  完全不會被嘗試呼叫。
- `internal/orchestrator/checkpoint_test.go` —— 6 個測試，其中
  最重要的兩個是 `TestCheckpointCreate_CallsStateBeforeRepository`
  （透過兩個 fake 記錄實際的呼叫順序）與
  `TestCheckpointCreate_StateFailureNeverCallsRepository`
  （證明 State 失敗時 Repository 完全不會被觸及——不是被呼叫後
  忽略，而是根本沒有被觸及）。
- `internal/cli/checkpoint.go` —— `NewCheckpointCmd
  (CheckpointCreateDeps)`，讀取 `--task-id`／`--worktree-id`
  旗標（沒有解析器 port，與 runtime-b03 記錄在案的範圍界線相同）。
- `internal/app/wiring/wiring.go` —— 新增 `replaceSubcommand`
  輔助函式（從 runtime-b04 內嵌的 hook 子樹取代迴圈中重構出來，
  讓兩個節點共用同一套機制），並透過它接上 `checkpoint`。

### 節點紀錄

```yaml
node: runtime-b05
status: completed
artifacts:
  - internal/orchestrator/checkpoint.go
  - internal/orchestrator/checkpoint_test.go
  - internal/cli/checkpoint.go
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/app/wiring   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/...   # OK"
  - "go test ./internal/orchestrator/... -run CheckpointCreate -race -v   # 6/6 PASS"
  - "go test ./internal/cli/... ./internal/app/wiring/... ./internal/orchestrator/... -race   # all PASS"
commit: aa7130e
next_action: runtime-b08
assumptions:
  - "Both StateCheckpoint and RepositoryCheckpoint wired against fakes
    this phase (checkpoint-a04/b04 not integrated yet, per the task
    brief's explicit instruction to use fakes for both regardless of
    checkpoint-b04's in-progress sibling-branch status this phase)."
blockers: []
```

## runtime-b08 — Status／Doctor 指令

### 交付內容

- `internal/orchestrator/diagnostics.go` —— `Status(ctx,
  StatusDeps, StatusRequest) (StatusResult, error)`：盡力提供
  session／Progress Tree 摘要，在 ProgressTree 依賴缺失／失敗時
  fail-open。沒有 pause 狀態欄位：凍結的 `GracefulPauseService`
  port 沒有被動的讀取查詢（只有狀態轉換動作），所以一個讀取型
  指令不應該只為了呈現摘要就去呼叫一個狀態轉換動作。
  `Doctor(ctx, DoctorDeps) DoctorResult`：資料庫可連線
  （`Conn().PingContext`）且已遷移（`CurrentVersion > 0`）、
  設定可載入（窄範圍的 `ConfigLoader` 介面）、必要目錄存在且
  可寫入（透過建立後刪除的暫存檔探測，並確認不留下殘留）。
  每一項檢查都各自獨立為選填（未接線時為 `CheckSkipped`）；
  只有在某項檢查真的 `CheckFail` 時，整體的 `Healthy` 才會是
  false。
- `internal/orchestrator/diagnostics_test.go` —— 12 個測試，
  包含 `TestDoctor_DoesNotMutateFilesystem`（Doctor 執行前後
  目錄項目數量不變）以及一個使用真實、已遷移的 `*sqlite.DB`
  的測試，針對實際內嵌的遷移集合證明資料庫檢查的正常路徑，
  而不只是對 fake 做測試。
- `internal/cli/diagnostics.go` —— `NewStatusCmd`／
  `NewDoctorCmd`，無論個別檢查是否失敗都一律以結束碼 0 退出，
  搭配穩定、有 schema 版本號的 JSON 內容（一項失敗的 doctor
  檢查是報告中的內容，而不是指令執行的錯誤）。
- `internal/app/wiring/wiring.go` —— 新增
  `Services.Diagnostics`（`DiagnosticsSupport`：`DB`／`Config`／
  `RequiredDirs`，全部選填），並透過 `replaceSubcommand` 接上
  兩個指令。`*sqlite.DB` 在結構上滿足
  `orchestrator.DBPinger`，`orchestrator` 並未因此新增對
  `sqlite` 套件的依賴（開發期間曾以一個未提交的臨時編譯期斷言
  驗證過這點）。

### 節點紀錄

```yaml
node: runtime-b08
status: completed
artifacts:
  - internal/orchestrator/diagnostics.go
  - internal/orchestrator/diagnostics_test.go
  - internal/cli/diagnostics.go
  - internal/cli/diagnostics_test.go
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/app/wiring   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/...   # OK"
  - "go test ./internal/cli/... -run 'Status|Doctor' -race -v   # 6/6 PASS"
  - "go test ./internal/orchestrator/... ./internal/cli/... ./internal/app/wiring/... -race   # all PASS"
commit: deaf094
next_action: none — all six Phase 5 nodes complete; runtime-a03/a04/a05/a07/a08/a09/a10/a11/b06/b07/b09/b10 remain, out of scope this phase
assumptions:
  - "No fakes tracked for follow-up on this node: DBPinger/ConfigLoader
    are narrow interfaces this node owns outright, satisfied directly
    by *sqlite.DB and (once wiring supplies one) a real config loader —
    no sibling-role service dependency to swap later."
blockers: []
```

## 節點後的全 repo lint 掃描

六個節點全部完成後，`golangci-lint run ./...` 找到 11 個問題，
全部都在本波次自己新增的檔案中（errcheck x1、errorlint x5、
nilerr x3、staticcheck x1、unused x1）。在一個獨立於六個節點
提交之外的專屬提交（`90078c3`）中全數修好，遵循同一套
「絕不把不相關的工作混在一起提交」的紀律——這個提交是對已提交
工作的清理，不算是第七個節點。`golangci-lint run ./...` 現在
回報整個 repo 0 個問題；`go test ./... -race` 在每個套件上都完全
綠燈，包含 `internal/storage/sqlite`（確認 Phase 4 對 foundation
migrate_test.go 的變更請求已在本波次開始前解決，正如任務簡報
所述）。

## 本波次使用的 fake（供整合追蹤）

以下每一項都經任務簡報明確授權，可在本波次以軟性／fake 依賴代替；
在此逐一列出，讓之後的整合階段能找到每個仍需要換上真正實作的地方。

| 節點 | 用 fake 取代的對象 | 位置 |
|---|---|---|
| runtime-b03 | `predictor-08`／`predictor-09`（Policy／Evaluation 持久化）—— `app.EvaluationService` | `internal/orchestrator/evaluate.go` 的 `Deps.Evaluation`，測試中及（在 predictor 到位前）`wiring` 中都接上 `fakes.FakeEvaluationService` |
| runtime-b04 | 同一個 `app.EvaluationService` fake（UserPromptSubmit 的阻擋／允許決策） | `internal/orchestrator/hooks.go` 的 `HookDeps.Evaluation` |
| runtime-b05 | `checkpoint-a04`（真正的 `CompleteNode`／state-checkpoint 原子協定）—— `app.StateCheckpointService` | `internal/orchestrator/checkpoint.go` 的 `Deps.StateCheckpoint` |
| runtime-b05 | `checkpoint-b04`（repository checkpoint；由手足隊友在同一波次建置中，尚未合併）—— `app.RepositoryCheckpointService` | `internal/orchestrator/checkpoint.go` 的 `Deps.RepositoryCheckpoint` |

本波次依任務簡報明確**不**使用 fake、並直接在本產物的節點紀錄中
驗證過的項目：

- `claude-provider-04` 的 hook payload 解析器與 Normalizer
  （`internal/providers/claude`、`internal/hooks/claude`、
  `internal/telemetry/claude`）——真實、已整合（Phase 2），由
  `runtime-b04` 的 hook 處理器直接呼叫。
- `internal/gitx`（checkpoint 角色的 Git 底層工具，由
  `runtime-b03` 的 Evaluate 管線使用）——真實、已整合。
- `runtime-a02`／`runtime-a06`（Part A）完全沒有任何手足角色
  依賴——兩者都是純粹、自成一體的狀態機／儲存層節點，沒有
  需要 fake 的東西。
- `runtime-b08` 的 `DBPinger`／`ConfigLoader`——本節點完全擁有
  的窄範圍介面；沒有需要 fake 的東西。

# Phase 6

分支：`vertical-slice/runtime`，在 Phase 6 開始前透過 `git fetch
origin && git merge origin/main`（fast-forward，乾淨——沒有衝突，
此角色只擁有自己的路徑）從 `main` 同步。這次帶入了 Phase 5 的
整合狀態，包含 `checkpoint` 真正的 `checkpoint-b04`（repository
checkpoint）已併入 `main`，以及 `predictor` 的風險組合器
（`internal/predictor/risk`）。依任務簡報，`runtime-b05` 既有的
`checkpoint-b04` 內部 fake 本波次刻意維持原樣（換掉它不是本波次
的指派工作）——在此註記，而非悄悄變更。

本波次指派的節點皆屬於 Part A，依序執行，每個節點各自獨立驗證
並提交：`runtime-a03`（Observe 抖動去彈跳／遲滯）→
`runtime-a04`（RequestPause 冪等性 + 安全點協調器）→
`runtime-a07`（逾期／已租用工作的重啟復原）。`runtime-a03`／
`runtime-a04` 都直接建立在 `runtime-a02` 的狀態機之上；
`runtime-a07` 建立在 `runtime-a06` 的排程租約 store 之上。
本波次沒有 Part B 的工作。

## runtime-a03 — Observe 抖動去彈跳／遲滯

### 交付內容

- `internal/pause/observe.go` —— `Observer`（每個
  `domain.SessionID` 各自的抖動去彈跳／遲滯狀態）與
  `Observe(sessionID, forecast, observedAt) ObserveDecision`，
  以 `ObserveConfig` 實作 ADD §17.6／§20.2 的確切參數
  （門檻 0.80、最小間隔 5 秒、配額新鮮度 30 秒、重設帶
  0.70），再加上獨立的 ADD §17.6 緊急觸發條件（已使用 ≥98%
  或 P50 到達上限時間 ≤60 秒），各自有自己的 `TriggerReason`
  （`TriggerReasonCalibrated`／`TriggerReasonEmergency`），
  讓兩條路徑依「第一天就要貼近現實」的要求可被區分。緊急路徑
  一律優先無條件檢查，且不會消耗／清除一個已經在進行中的
  校準態預備。遲滯重設要求 RiskScore 真的降到 0.70 以下——
  一個介於中間、不合格的樣本不會清掉預備狀態，所以被雜訊
  隔開的兩個合格樣本仍然能正確觸發。
- `internal/pause/observe_test.go` —— 13 個測試：兩個必要測試
  逐字對應（兩個合格觀測會觸發；單一尖峰不會）以及
  太快而仍保持預備、遲滯帶、過期配額樣本、缺少
  `QuotaObservedAt` 時 fail-closed、未校準時永遠不符合校準路徑
  資格、兩種緊急分支、緊急不會消耗預備狀態、每個 session 各自
  獨立，以及 `Reset`。

### 節點紀錄

```yaml
node: runtime-a03
status: completed
artifacts:
  - internal/pause/observe.go
  - internal/pause/observe_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/pause/... -run Observe -v   # 13/13 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -v   # all PASS"
commit: 8ff0190
next_action: runtime-a04
assumptions:
  - "TriggerReason is this package's own closed vocabulary (mirrors
    Event), not part of any frozen contract or predictor's
    domain.ReasonCode list (which explains a risk score's composition,
    not a pause trigger's decision) — no frozen enum was extended or
    reused out of scope."
  - "Emergency's 'limit reached' branch (ADD §17.6) is modeled via
    CurrentUsedPercent/EstimatedTimeToLimitP50Seconds only, since
    domain.RunwayForecast has no separate boolean field for a
    provider-reported hard limit; a future node wiring the real
    predictor-06 output can set CurrentUsedPercent to 100 (or a
    provider-supplied percent) to represent that case without an
    Observer signature change."
  - "Observer is per-process, keyed by domain.SessionID, with no
    persistence of its own — restart behavior for in-flight (armed but
    not yet fired) debounce state is out of scope for this node (a
    single missed arm after a crash just requires one more qualifying
    sample post-restart, which is a safe, conservative default, not a
    correctness gap)."
blockers: []
```

## runtime-a04 — RequestPause 冪等性 + 安全點協調器

### 交付內容

- `internal/pause/requestpause.go` —— `PauseKey`（天然的
  `(TaskID, SessionID)` 冪等性鍵——`pause_records` 沒有另外
  一欄呼叫端提供的冪等性鍵，所以這個天然鍵扮演的角色，等同於
  CONTRACT_FREEZE.md 對 `CompleteNodeRequest.IdempotencyKey`
  所描述的角色）、一個窄範圍的內部 `PauseStore` port
  （`FindActiveByKey`／`Insert`，刻意宣告在這個套件內，而不是
  `internal/app/ports.go`——這是已凍結的 `GracefulPauseService`
  邊界背後的內部介面，而不是一份新的跨元件合約）、
  `RequestPause(ctx, store, ids, req) (RequestPauseResult, error)`，
  以及 `MemStore`（記憶體版的參考／測試用 `PauseStore`——DAG
  自身的註記說明本節點開始時不需要具體的 store；預期
  `runtime-a05` 會針對同一個介面新增 SQLite 版的
  `PauseStore`）。
- `internal/pause/safepoint.go` —— `Boundary` 詞彙，對應 ADD
  §20.4 確切的安全／不安全邊界清單，`SafePointCoordinator`
  介面加上 `TurnBoundaryCoordinator`（具體的 turn／段落邊界
  實作），以及 `PersistThenInterrupt`，它針對窄範圍的
  `CheckpointPersister`／`Interrupter` 介面，安排先持久化、
  後中斷的順序——這呼應了 `runtime-b05` 的
  `internal/orchestrator.CheckpointCreate` 排序模式（先 state
  後 repository、遇到第一個錯誤就提早回傳），只是提升了一層，
  發生在安全點邊界而非 checkpoint 角色邊界。本波次 checkpoint
  那一側只使用 fake，依 DAG 明確的註記，並與 `runtime-b05`
  的先例一致。
- `internal/pause/requestpause_test.go` —— 7 個測試：第一次
  呼叫會建立、冪等重放不會重複（多次重複呼叫都收斂到同一筆
  紀錄）、以不同原因重放仍保持冪等（在校準暫停進行中途出現
  緊急情況，不會分岔出第二筆紀錄）、前一個暫停終止後允許
  開始新的一輪、每個鍵各自獨立、請求驗證，以及 store 錯誤
  的傳遞。
- `internal/pause/safepoint_test.go` —— 6 個測試：必要測試
  逐字對應（「安全點會在中斷前持久化 checkpoint」，透過記錄
  呼叫順序的 fake 證明——不只是「兩者都被呼叫過」），
  持久化失敗絕不會觸及中斷、不安全邊界會在任一協作物件執行前
  就被拒絕（涵蓋每一個 ADD §20.4 不安全邊界，加上一個無法辨識
  的邊界），每一個 ADD 安全邊界都被接受，以及輸入／nil
  協作物件的驗證。

### 節點紀錄

```yaml
node: runtime-a04
status: completed
artifacts:
  - internal/pause/requestpause.go
  - internal/pause/requestpause_test.go
  - internal/pause/safepoint.go
  - internal/pause/safepoint_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/pause/... -run 'RequestPause|SafePoint' -v   # 13/13 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -v   # all PASS"
commit: d849d01
next_action: runtime-a07
assumptions:
  - "PauseStore is declared in internal/pause, not internal/app/ports.go
    — it is an implementation seam behind the already-frozen
    GracefulPauseService boundary (RequestPause/ReachSafePoint/etc.),
    not a new cross-component contract; adding it to ports.go would be
    the kind of speculative widening Constitution §7 rule 10 warns
    against before a real store actually needs a wider surface."
  - "A differing Reason on a RequestPause replay (e.g. emergency arriving
    while a calibrated pause is already in flight for the same key) is
    NOT treated as a conflict — unlike CONTRACT_FREEZE.md's
    CompleteNodeRequest.IdempotencyKey rule for a differing payload.
    Escalating an in-flight pause's urgency is a real, ADD-anticipated
    signal (ADD §17.6's emergency path exists precisely to skip ahead
    faster), not a caller error; any actual escalation policy (e.g.
    shortening the quiesce timeout) is left to a later node."
  - "CheckpointPersister/Interrupter (safepoint.go) are deliberately
    narrower than the frozen app.StateCheckpointService/
    app.TurnInterrupter — this node only needs to prove ordering, not
    wire the full real contracts; runtime-a05 (the full persist-phase
    orchestrator) is where the real adapters get built, per the DAG's
    scope split between this node and that one."
blockers: []
```

## runtime-a07 — 逾期／已租用工作的重啟復原

### 交付內容

- `internal/scheduler/restart.go` —— `Store.Restart(ctx)
  (RestartReport, error)`，設計為在任何 worker 呼叫 `Claim`
  之前，於行程啟動時呼叫一次。與 `ReclaimExpired`
  （runtime-a06，只有在 `lease_expires_at` 真的已經過期時才會
  釋放租約——在任何其他時間點這都是正確行為）不同，`Restart`
  會無條件釋放每一筆 `leased` 資料列：因為依定義，在這次呼叫
  之前資料庫中所記錄的每一個租約擁有者，都屬於現在已經死亡的
  前一個行程實例，所以等待一個過期租約剩餘的 TTL 走完，只會
  延遲復原、沒有任何好處（ADD §28.3 第 2／8 步、§20.7「在下次
  daemon 啟動時處理逾期工作」、崩潰矩陣「wake job 已租用後
  daemon 死亡 → 租約過期後被回收」、§29.6 情境 11「daemon
  重啟會重建工作」）。`done`／`dead` 資料列不受影響（不會讓
  已完成或已耗盡的工作復活）；`Restart` 本身從不取得或執行
  任何工作，而是依賴 `Claim` 既有的 `BEGIN IMMEDIATE`
  序列化機制，防止一筆資料列一旦重新可被取得後被重複執行。
  `RestartReport`（`RecoveredLeased`、`OverdueClaimable`）
  被回傳，供未來的啟動報告步驟（ADD §28.3 第 10 步）使用。
- `internal/scheduler/restart_test.go` —— 6 個測試：必要測試
  逐字對應（「重啟會復原 wake job」——一個已租用但從未完成、
  租約尚未過期的工作，被一個針對同一底層資料庫的全新 `Store`
  實例復原並重新可取得，且透過 Attempts 計數以及一次被拒絕的
  過期 `Complete` 呼叫，證明沒有重複執行），加上已過期租約的
  涵蓋（證明 `Restart` 是 `ReclaimExpired` 的超集合，而不是
  更窄的替代品）、done／dead 工作不受影響、多工作掃描、
  `OverdueClaimable` 計數，以及靜止狀態下的無操作。

### 節點紀錄

```yaml
node: runtime-a07
status: completed
artifacts:
  - internal/scheduler/restart.go
  - internal/scheduler/restart_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/scheduler/... -run Restart -v   # 6/6 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -v   # all PASS"
commit: 6cce24a
next_action: runtime-a05 (persist phase orchestration) or runtime-a08 — NOT this phase, per explicit scope (three nodes only)
assumptions:
  - "Restart's unconditional-release semantics (ignoring
    lease_expires_at entirely) are correct ONLY at process-startup time,
    precisely because every existing lease owner is categorically dead
    by then — this is NOT a general replacement for ReclaimExpired's
    narrower, expiry-gated behavior, which remains correct and necessary
    for a lease expiring while the SAME daemon process keeps running.
    Both methods coexist on Store; Restart is documented as
    startup-only in its own doc comment so a future caller does not
    accidentally invoke it mid-run."
  - "RestartReport.OverdueClaimable is informational only (feeds a
    future startup-report step) — Restart does not itself claim or
    execute overdue jobs; that remains Claim's responsibility, called
    separately by whatever startup sequence wires this node in."
blockers: []
```

## Phase 6 跨節點觀察

- 三個節點全部都在 DAG 估計值以內或以下完成（各自 M／300 點／
  約 3-4 小時），沒有任何返工、沒有任何阻礙——這是此角色至今
  摩擦最小的一個波次，與三個節點都直接建立在已凍結、已測試的
  先前工作之上（`runtime-a02` 的狀態機、`runtime-a06` 的租約
  store）而非整合新的跨角色依賴一致。
- `runtime-a03` 與 `runtime-a04` 都需要一個小型、範圍明確的
  套件本地詞彙（`TriggerReason`、`Boundary`），而不是重用或
  擴充某個凍結的列舉——兩者在新增前都先對照
  CONTRACT_FREEZE.md 與 Constitution §6 規則 4，確認這只是
  這個套件自己的記錄用途，而不是一個狀態值。
- `runtime-a07` 唯一真正的設計決策——在重啟時無條件回收每一筆
  已租用的資料列，而不是重用 `ReclaimExpired` 那個以過期時間
  為門檻的判斷條件——直接源自對「重啟」這件事在範疇上意味著
  什麼的推理（每一個先前的租約擁有者都已經死亡），而不是來自
  任何新的外部依賴；在本節點自己的文件註解中明確記錄下來，
  避免未來的讀者誤以為這是 `ReclaimExpired` 多餘的重複。
- 本波次沒有需要新增任何 ADR，也沒有任何凍結合約需要提出
  變更請求。`checkpoint-b04` 本波次真正併入 `main`，並未要求
  此分支做任何變更，依任務簡報明確指示，`runtime-b05` 既有的
  fake 維持原樣，直到未來波次的整合步驟。
- 明確確認：本波次只觸及 `internal/pause/**` 與
  `internal/scheduler/**`（Part A 專屬的路徑）——沒有為了編輯
  而讀取或修改 `internal/progress/**`（一位手足隊友同一個
  Phase 6 中、明顯不同的 `checkpoint-a04` 節點的並行工作）下的
  任何檔案，也沒有觸及任何 Part B runtime 路徑
  （`internal/orchestrator/**`、`internal/cli/**`、
  `internal/httpapi/**`、`internal/daemon/**`、
  `internal/app/wiring/**`、`internal/testutil/fakes/**`）。

# Phase 7

分支：`vertical-slice/runtime`，在 Phase 7 開始前透過 `git fetch
origin && git merge origin/main`（fast-forward，乾淨——沒有衝突）
從 `main` 同步，落在 `1440f4c`。這次帶入了 Phase 6 的整合狀態：
`checkpoint` 真正的 `CompleteNode`／State Checkpoint 工作
（`internal/progress`、`internal/statecheckpoint`，遷移
0023-0024）以及 `predictor` 真正的 Policy 層
（`internal/policy`）。依任務簡報，`checkpoint-a05`（State
Checkpoint 服務）以及凍結的 `app.StateCheckpointService` 真正的
實作**不**包含在這次合併中——它們是手足隊友在同一波次進行中的
並行工作——所以本波次針對這一項特定依賴，仍然依指示使用
`internal/testutil/fakes.FakeStateCheckpointService`。

本波次指派的節點依序執行，每個節點各自獨立驗證並提交：
`runtime-a05`（持久化階段協調）→ `runtime-b07`（pause／resume／
scheduler CLI 接線）。

## runtime-a05 — 持久化階段協調

### 交付內容

- `internal/pause/persistphase.go` —— `Persist(ctx, PersistDeps,
  PersistRequest) (PersistResult, error)`：依 CONTRACT_FREEZE.md
  「Transaction boundaries」章節所命名的五項持久寫入——Progress
  Tree 快照、State Checkpoint、Repository Checkpoint、Pause
  Record、Wake Job——依固定順序排序，每一步都依 `PauseRecord`
  上新增的 `PersistProgress` 欄位做冪等跳過。`HaltAfter`／
  `HaltError` 完全對應 `internal/progress.CompleteNode` 自身的
  崩潰注入詞彙與技巧，依任務簡報明確指示遵循該先例。一個新的
  `PersistPauseStore` 介面（`GetProgress`／`SaveProgress`）是
  這個檔案唯一新增的儲存介面；`pause.MemStore` 直接實作它，
  並擴充了依 `PauseID` 查找的功能（`findByID`），因為
  `PersistProgress` 是以 `PauseID` 為鍵，而不是 `MemStore` 原本
  map 使用的 `PauseKey`。
- `internal/scheduler/lease.go` —— 新增 `Store.GetByPauseKind`，
  一個依 `(pauseID, kind)` 查找的唯讀方法——用於在重試的
  `Schedule` 呼叫撞上既有的 `UNIQUE(pause_id, job_kind)`
  限制時（`Schedule` 自身提交與 `Persist` 記錄產生的
  `WakeJobID` 之間的崩潰視窗），復原一個已排程 wake job 的
  身分。此處直接新增，而不是留給後續節點當作缺口，因為 Part A
  直接擁有 `internal/scheduler`。
- `internal/pause/persistphase_test.go` —— 必要測試逐字對應
  （「每個階段之後崩潰都能正確恢復／協調」）：每個階段邊界
  各一個測試（`runToHalt` 對應 `internal/progress` 自己的
  輔助函式），每個都證明中止狀態恰好只暴露該階段的持久證據，
  且後續的 `Persist` 呼叫能恢復並完成，不會重新建立任何已經
  持久化的 checkpoint，再加上橫跨全部五個邊界的完整協調掃描，
  以及驗證／nil 依賴／未知 pause 紀錄的 fail-closed 情境。
  Repository Checkpoint 這一步的測試，針對一個真實、已遷移的
  暫存檔 SQLite 資料庫，以及一個真實的暫存 Git repository
  （若 `git` 不可用則跳過），建立了一個真正的
  `internal/repocheckpoint.Service`——這條路徑上沒有任何 fake，
  依任務簡報指示。

### 設計筆記：一個概念上的暫停紀錄，兩個底層儲存

`wake_jobs.pause_id` 帶有一個真正指向 `pause_records` SQL
資料表的外鍵（`0051_wake_jobs.sql`），但這些測試中的
`PersistPauseStore` 是 `pause.MemStore`——一個記憶體內的 store，
並非由該資料表支撐。因此每個崩潰注入測試都同時建立種子資料：
記憶體內的紀錄（這個套件自己的持久進度記錄）**以及**一筆對應
的真實 `pause_records` 資料列（讓第 5 階段的 `Schedule` 呼叫
滿足外鍵）。這在此明確標記為留給後續整合節點追蹤的缺口
（一個真正 SQLite 版的 `PauseStore`，針對 `wake_jobs` 已經
參照的同一張 `pause_records` 資料表實作 `PersistPauseStore`），
而不是悄悄繞過。

### 節點紀錄

```yaml
node: runtime-a05
status: completed
artifacts:
  - internal/pause/persistphase.go
  - internal/pause/persistphase_test.go
  - internal/pause/requestpause.go (modified — PauseRecord.Persist field, MemStore.GetProgress/SaveProgress)
  - internal/scheduler/lease.go (modified — Store.GetByPauseKind)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/pause/... -run PersistPhase -race -v   # 10/10 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: f5b3205
next_action: runtime-b07
assumptions:
  - "State Checkpoint step uses internal/testutil/fakes.FakeStateCheckpointService
    (checkpoint-a05's real implementation is a sibling teammate's
    concurrent, not-yet-mergeable work this same phase, per the task
    brief's explicit instruction)."
  - "Progress Tree snapshot step calls the general
    app.ProgressTreeService.Snapshot method (also faked this phase via
    fakes.FakeProgressTreeService) rather than a dedicated snapshot-only
    port — no such narrower port exists in the frozen contract, and the
    task brief authorized a fake here regardless of checkpoint-a04's real
    integration status elsewhere in the codebase."
  - "Repository Checkpoint step uses the REAL internal/repocheckpoint.Service
    (checkpoint-b04, integrated on main since Phase 5) — no fake, per the
    task brief's explicit instruction."
  - "PersistPauseStore is a new interface distinct from PauseStore
    (requestpause.go) — kept separate because RequestPause's
    FindActiveByKey/Insert operate on PauseKey while persist-phase
    resumption operates on an already-known PauseID; PauseRecord itself
    is the single shared durable type both interfaces read/write."
  - "Two backing stores for one conceptual pause record during this phase's
    tests (in-memory PersistPauseStore + real SQL pause_records row) — see
    the dedicated design note above; tracked as a gap for a later
    integration node, not silently resolved."
blockers: []
```

## runtime-b07 — Pause／Resume／SchedulerRunOnce CLI ＋ orchestrator 接線

### 交付內容

- `internal/pause/lifecycle.go` —— `Cancel`（透過既有的轉換表
  套用 `EventCancel`，並持久化結果）以及 `Resume`（依呼叫端
  提供的判定——`Valid`／`QuotaUnsafe`／`Conflict`，三者恰好
  擇一——驅動 `WakePending -> Validating -> {Resuming -> Resumed
  | Sleeping | BlockedConflict}`）。`Resume` 的文件註解明確
  指出，真正的 resume 驗證（配額／repository／session／授權
  檢查，ADD §20.8）屬於 `runtime-a08` 尚未建置的範圍；本節點
  只實作狀態機的那一半，依 Constitution §7 規則 3（「能力上的
  缺口要明確地被標示出來，絕不悄悄假裝它不存在」）。
- `internal/pause/requestpause.go` —— `PauseStore` 新增了
  `GetByID`／`UpdateStatus`（都在 `MemStore` 上實作），之所以
  需要，是因為 Cancel／Resume 接收的是呼叫端提供的 `PauseID`
  （例如 `--pause-id` CLI 旗標），而不是 `RequestPause` 既有
  方法所使用的 `PauseKey`。
- `internal/orchestrator/pauselifecycle.go` —— `PauseRequestCmd`、
  `PauseCancelCmd`、`ResumeCmd`、`SchedulerRunOnceCmd`：對本
  角色自己真正的 `pause.RequestPause`／`Cancel`／`Resume` 以及
  `scheduler.Store.Claim` 做薄層協調——這個檔案中完全沒有
  fake，依 DAG「現在是硬依賴……同一個分支，不需要 fake」的
  註記。`SchedulerRunOnceCmd` 每次掃描只取得（不執行）一筆
  到期工作——「執行單次排程掃描後結束」指的是取得這一步，
  而不是完整的喚醒到恢復管線（`runtime-a09` 的範圍）。
- `internal/cli/pause.go` —— `NewPauseCmd`／`NewResumeCmd`／
  `NewSchedulerCmd`，真正的 Cobra 建構子（有 schema 版本號的
  JSON 輸出、有型別的錯誤、不外洩原始提示詞／日誌），以與
  先前波次 `NewCheckpointCmd`／`NewStatusCmd` 相同的方式取代
  `root.go` 的 stub 樹——`root.go` 本身依同一先例未被更動
  （獨立的 stub 樹仍作為裸 `NewRootCmd()` 的備援）。
- `internal/app/wiring/wiring.go` —— 新增選填欄位
  `Services.PauseLifecycle`（`orchestrator.PauseLifecycleDeps`）；
  只有在真的設定了 `Store`／`WakeJobs` 時，`RootCmd` 才會換上
  真正的 `pause`／`resume`／`scheduler` 指令樹，否則維持原本的
  stub 樹（呼應 `Diagnostics` 既有的選填欄位、全部跳過時的
  備援先例）。

### 節點紀錄

```yaml
node: runtime-b07
status: completed
artifacts:
  - internal/pause/lifecycle.go
  - internal/pause/lifecycle_test.go
  - internal/pause/requestpause.go (modified — GetByID/UpdateStatus)
  - internal/pause/requestpause_test.go (modified — fakePauseStore stub methods)
  - internal/orchestrator/pauselifecycle.go
  - internal/orchestrator/pauselifecycle_test.go
  - internal/cli/pause.go
  - internal/app/wiring/wiring.go (modified)
  - internal/app/wiring/wiring_test.go (modified)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/orchestrator/... -run 'PauseRequest|Resume|SchedulerRunOnce' -race -v   # 11/11 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "go test ./internal/app/wiring/... -race -v   # all PASS, including 4 new end-to-end CLI-tree tests"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: fdb5911
next_action: none — both Phase 7 nodes complete; runtime-a08/a09/a10/a11/b06/b09/b10 remain, out of scope this phase
assumptions:
  - "Resume's Valid/QuotaUnsafe/Conflict verdict is caller-supplied this
    phase, not independently computed — see lifecycle.go's package comment.
    ResumeCmdRequest defaults to Valid when neither --quota-unsafe nor
    --conflict is passed, keeping the common CLI case usable without
    requiring a08's not-yet-built checks; documented, not silent."
  - "SchedulerRunOnceCmd claims but does not execute/resume the claimed
    job — left leased for a later worker loop (a09) to actually drive
    through EventWakeDue/Resume; consistent with the command's own P0
    description naming a sweep, not a pipeline."
blockers: []
```

## Phase 7 跨節點觀察

- `runtime-a05` 是本波次（依 DAG 自身的風險排名，也是本角色
  至今）風險最高的節點——橫跨五個獨立寫入邊界的崩潰注入測試，
  其中兩個是真正的跨角色服務（Repository Checkpoint）而非
  行程內 fake，需要比先前任何 Part A 節點都更重的測試骨架
  （真實 SQLite 資料庫 + 真實暫存 Git repository）。從
  `internal/progress.CompleteNode`（Phase 6 手足節點
  `checkpoint-a04` 的工作，現已在 `main` 上）移植過來的
  `HaltAfter`／`HaltError` 先例，套用得乾淨俐落、無需重新
  設計——這直接證明了這項技巧能推廣到它最初被驗證的那個套件
  以外。
- 找到並補上了一個真實、雖然範圍不大的缺口，而不是延後處理：
  `scheduler.Store` 原本沒有辦法在一次重試的 `Schedule` 呼叫
  撞上自己的 `UNIQUE(pause_id, job_kind)` 限制後，復原一個
  已排程工作的身分。由於 Part A 直接擁有
  `internal/scheduler`，`GetByPauseKind` 就在同一個節點中
  新增，而不是丟給未來的 a06／a09 後續處理——DAG「不需要
  fake、同一個分支」的原則，依同樣的邏輯，也適用於節點進行
  途中發現的、本角色自己內部的缺口。
- `runtime-b07` 的接線形狀幾乎完全對應
  `runtime-b05`（Phase 5）（orchestrator 檔案 + 測試、CLI
  檔案、wiring.go 差異），只是這次涵蓋四個指令介面，而不是
  單一的 `checkpoint create`——與 Phase 5 經驗教訓中「一旦把
  CLI ＋ wiring 都算進去，Part B 形狀的節點檔案數量會高於
  DAG 估計」的觀察一致；本節點實際的檔案數量（9 個，包含
  兩個被修改的既有檔案）與這個既定模式相符，不是新的意外。
- 本波次沒有新增 ADR，沒有變更請求升級，也沒有任何凍結合約
  的疑問。唯一明確追蹤中的缺口（`PersistPauseStore` 作為
  一個獨立於 `wake_jobs` 已經參照的真正 `pause_records` SQL
  資料表之外的記憶體內 store）已在上方 `runtime-a05` 自己的
  段落中標記，供未來的整合節點處理，而不是被悄悄解決或
  變得無從發現。

# Phase 8

分支：`vertical-slice/runtime`，在 Phase 8 開始前透過 `git fetch
origin && git merge origin/main`（fast-forward，乾淨——沒有衝突）
從 `main` 同步，落在 `2b7c29c`。這次帶入了 Phase 7 的整合狀態：
`checkpoint` 真正的 `internal/statecheckpoint.Service`
（`app.StateCheckpointService`）以及
`internal/repocheckpoint` 的孤兒掃描／崩潰安全強化，還有
`predictor` 真正的 `internal/evaluation.Service`
（`app.EvaluationService`／`ConsumeAuthorization`，
`predictor-09`）。依任務簡報，`predictor-10` 的授權強化
工作是同一波次中並行的手足節點，尚無法合併——本波次針對
本節點唯一一次 `ConsumeAuthorization` 呼叫，仍然使用
`internal/testutil/fakes.FakeEvaluationService`，與既定的
「先 fake、後替換」模式一致。

本波次指派的節點：`runtime-a08`（Resume 驗證）。

## runtime-a08 — Resume 驗證

### 交付內容

- `internal/pause/resumevalidation.go` —— `lifecycle.go` 套件
  註解中明確指出屬於自身缺口的那項真正檢查：四個各自可獨立
  替換的檢查器，各自對應 agents/runtime.md Part A 交付項目 8
  的其中一項檢查，每一個都回傳統一的
  `CheckResult{Pass, Reason, Detail}`：
  - `CheckQuotaSafety`（`QuotaSnapshotReader` 介面）：對
    暫停時記錄基準所使用的同一個額度上限，重新讀取目前的配額，
    若情況變得更糟（`UsedPercent` 更高，或新轉入 `Reached`）
    則判定失敗——在比較兩側任一方無法讀取時，絕不假設安全
    （任一側 `UsedPercent` 為 nil 都會 fail closed）。
  - `CheckRepositoryCompatibility`（
    `app.RepositoryCheckpointService.Verify`——真實實作，
    checkpoint-b04，自 Phase 5 起已整合——加上一個套件本地的
    `RepoFingerprintReader` 介面，用於讀取「目前」的 repository
    狀態）：先確認暫停時的 checkpoint 本身依然驗證完整，
    再將其記錄的 `GitHead` 與目前的指紋比較。完全沒有變更時
    直接通過；與暫停中工作自身檔案有重疊的變更一律阻擋
    （`ReasonRepositoryOverlapBlocks`，不受政策影響）；不重疊
    （「無關」）的變更則依呼叫端提供的 `RepoChangePolicy`
    （預設 `RepoChangePolicyAllowUnrelated`，或
    `RepoChangePolicyBlockAny`）決定允許或阻擋。
  - `CheckSessionCapability`（`SessionCapabilityReader` 介面）：
    provider session 目前必須回報 `Resumable`，並可選擇性地
    要求明確的 `domain.ProviderCapabilities.SessionResume`。
  - `CheckAuthorization`（`app.EvaluationService.
    ConsumeAuthorization`——本波次為 FAKE，見下方）：被拒絕／
    過期／已被消耗的授權，與一個真正無法連線的授權服務，兩者
    都算失敗，但各有不同的原因代碼
    （`ReasonAuthorizationInvalid` 對比
    `ReasonAuthorizationServiceUnavailable`），讓呼叫端／稽核
    紀錄能分辨「我們問了，答案是否」與「我們根本問不到」。

  `ValidateResume(ctx, ResumeValidationDeps,
  ResumeValidationRequest) (ResumeValidationResult, error)`
  依固定順序協調全部四項：配額 → repository → session →
  authorization（最便宜／最容易重新排程的排最前；authorization——
  這個一次性、不可逆的資源——排最後）。它**不會**在第一個
  失敗的檢查就停止（一個要建立完整稽核軌跡的呼叫端，或是一個
  透過 ADD §20.9 UI 解決 `BlockedConflict` 的人類，需要知道
  每一項檢查各自的結果）；任何一個檢查器內部的下游讀取失敗，
  會以帶有 `_UNAVAILABLE` 原因代碼的失敗 `CheckResult` 回報，
  而不是 Go error，因此在結果中的可見度，與任何其他拒絕
  完全相同。回傳 Go error 嚴格保留給組合上的錯誤（nil 依賴、
  缺少 `SessionID`），且會在執行任何檢查之前就立即中止。
  `ResumeValidationResult.Verdict()` 把四項結果對應到
  `lifecycle.go` 既有的 `ResumeRequest{Valid, QuotaUnsafe,
  Conflict}` 三態判定：全部通過 → `Valid`；只有配額失敗
  （其他每一項都通過）→ `QuotaUnsafe`（重新排程，依必要測試
  所述）；其他任何失敗（repository、session 或 authorization，
  單獨或與配額失敗合併發生）→ `Conflict`（阻擋）——配額與
  repository 同時失敗仍然會阻擋，不會悄悄跳過一個未解決的
  衝突而逕自重新排程。

  `RescheduleWakeJobOnQuotaUnsafe` 在排程器整合層級證明
  「配額不安全時重新排程」，而不只是 `lifecycle.go` 既有的
  `Resume` 已經涵蓋的暫停紀錄狀態機層級：當
  `ValidateResume` 的判定為 `QuotaUnsafe` 時，它會對相關的
  wake job 呼叫 `scheduler.Store.Fail`（透過一個
  `*scheduler.Store` 直接滿足的窄範圍
  `WakeJobRescheduler` 介面）——重用 `Fail` 既有的 ADD §20.7
  退避後重試或死亡機制（runtime-a06），而不是另外發明第二套
  重新排程機制，因為從 wake job 自己的角度來看，一次配額不安全
  的 resume 嘗試本身就是一次失敗的嘗試。對 `Valid` 或
  `Conflict` 判定而言，它是無操作（不會呼叫 `Fail`）。在一條
  完整的喚醒到恢復管線中（取得 → 驗證 → 驅動
  `EventWakeDue`／`Resume` → 重新排程或阻擋），從真正被排程器
  取得的 wake job 出發驅動這一切，仍然是 `runtime-a09` 的範圍，
  依 DAG 所述（`runtime-a09` 依賴 `runtime-a08`，並涵蓋
  `DuplicateWake`／`Cancel`）；本節點證明的是重新排程機制本身
  是正確且可用的。

- `internal/pause/resumevalidation_test.go` —— 42 個測試，全部
  命名為 `TestResumeValidation_*`（或
  `TestResumeValidationResult_*`），讓 DAG 精確的驗證指令
  （`-run ResumeValidation`）選中整個檔案。逐字涵蓋
  agents/runtime.md Part A 交付項目 8 的每一項必要測試：
  「配額不安全時重新排程」（同時在 `CheckQuotaSafety`／
  `Verdict()` 層級，以及另外直接針對排程器、透過
  `RescheduleWakeJobOnQuotaUnsafe` 真的呼叫
  `scheduler.Store.Fail` 來證明）、「repo 重疊時阻擋」（同時在
  `CheckRepositoryCompatibility` 與 `ValidateResume` 層級證明，
  並顯示不論政策為何都成立）、「無關的 repo 變更依所設定的
  政策處理」（兩種政策、兩個層級皆有）——再加上本節點自身
  設計簡報中列出的每一個 fail-closed 情境：未知／nil 的配額
  比較、checkpoint 無效、指紋無法讀取、session 不可恢復、
  能力被明確確認不存在、授權被拒絕對比授權服務不可用
  （不同的原因代碼）、五個 `ResumeValidationDeps` 欄位各自的
  nil 依賴驗證、格式不正確的請求驗證、「即使先前有檢查失敗
  （而非錯誤），每一項檢查仍會照常執行」的順序保證，以及本
  節點設計刻意劃分的「下游讀取失敗會以 CheckResult 呈現，
  而不是 Go error，也不會阻擋後續的檢查」這項區別。

### 設計筆記：刻意選擇的兩個失敗管道

`ValidateResume`／每個檢查器都回傳 `(CheckResult, error)`，
兩者代表不同的意義：下游服務在被詢問時發生錯誤（配額讀取
失敗、`Verify` 出錯、session／能力讀取失敗、
`ConsumeAuthorization` 出錯）會被捕捉為一個帶有
`_UNAVAILABLE` 後綴原因代碼的失敗 `CheckResult`——仍然是
fail-closed（該檢查不算通過），但透過與一般拒絕相同的管道
呈現，讓未來的 `resume_attempts` 稽核紀錄列（
`0052_resume_attempts.sql` 已經有恰好合適的欄位：
`repository_fingerprint_before/after`、
`quota_used_percent`、`failure_code`）或是一位解決
`BlockedConflict` 的人類，看到的是一個有標籤的原因，而不是
一個泛用的錯誤。回傳的 Go `error` 則保留給組合上的錯誤
（nil 依賴、格式不正確的請求），並且會在任何檢查執行之前
就中止。較早的草稿曾把這兩者混為一談（把每一個檢查器的
error 都當成「全部中止」的訊號）；本節點自己的測試套件在
提交前就抓到了這個不一致（見「經驗教訓」），設計因此修正為
此處描述的雙管道劃分，這也讓
`RescheduleWakeJobOnQuotaUnsafe` 的失敗原因字串
（`result.FirstFailure()`）真正有意義——它永遠有東西可以
回報。

### 節點紀錄

```yaml
node: runtime-a08
status: completed
artifacts:
  - internal/pause/resumevalidation.go
  - internal/pause/resumevalidation_test.go
validation:
  - "gofmt -l internal/pause internal/scheduler   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/...   # OK"
  - "go test ./internal/pause/... -run ResumeValidation -v   # 42/42 PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... -race -count=1   # all PASS"
  - "go build ./... && go test ./... -race -count=1   # all PASS, whole repo, zero regressions"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <recorded below>
next_action: runtime-a09 (duplicate-wake + cancel) — NOT this phase, per explicit scope; a09 is where a real scheduler-claimed wake job is driven through EventWakeDue -> ValidateResume -> Resume/RescheduleWakeJobOnQuotaUnsafe end to end
assumptions:
  - "app.EvaluationService.ConsumeAuthorization is FAKED this phase
    (internal/testutil/fakes.FakeEvaluationService) — predictor-10's
    authorization-hardening pass is a concurrent sibling this same phase,
    not yet mergeable, per the task brief's explicit instruction, consistent
    with the established fake-then-swap pattern (runtime-a05/b05 did the
    same for checkpoint-a05/b04 in earlier phases)."
  - "RepoFingerprintReader is this package's OWN narrow interface, not
    internal/gitx.Fingerprint directly — internal/pause does not take a
    compile-time dependency on checkpoint's Git plumbing package merely to
    declare a seam; a future integration node adapts a real
    gitx.Fingerprint onto RepoFingerprint (HeadOID + ChangedPaths)."
  - "QuotaSnapshotReader/SessionCapabilityReader are also this package's own
    narrow seams, not direct uses of the frozen app.QuotaReader or a
    claude-provider capability port — a future integration node adapts the
    real, wider signals behind these narrower interfaces, mirroring this
    package's existing CheckpointPersister/Interrupter (safepoint.go)
    precedent for 'declare the narrowest seam this node needs, let a later
    wiring node adapt the real thing.'"
  - "RescheduleWakeJobOnQuotaUnsafe requires the caller to already hold the
    wake job's lease (scheduler.Store.Fail's own precondition) — correct
    for the scheduler-driven wake pipeline (a09's scope) but not applicable
    to a manual `auspex resume` invocation that never claimed a lease;
    a manual resume's quota-unsafe verdict is still correctly reflected on
    the PAUSE RECORD via Resume/Verdict regardless."
  - "PausedWorkPaths (the paths RepositoryCompatibility's overlap check
    compares against) is caller-supplied on ResumeValidationRequest, not
    derived by this node — deriving 'which paths did the paused work
    touch' from the Progress Tree/repository checkpoint's own manifest is
    a future integration node's concern, not part of the validation LOGIC
    this node builds."
blockers: []
```

## Phase 8 跨節點觀察

- 本波次唯一一個節點，補上了 `lifecycle.go` 的 `Resume` 中
  最後一個被明確點名的缺口（它自己的套件註解點名
  runtime-a08 為「真正 resume 驗證」的擁有者，當時尚未建置）——
  `Resume` 本身沒有被修改；`ValidateResume`／`Verdict()` 是
  增量新增的，設計為餵給 `Resume` 既有的、由呼叫端提供判定的
  參數，不需要對 `lifecycle.go` 先前波次已凍結的形狀做任何
  變更。
- 與本角色既定的做法一致，唯一一個具有跨波次影響的真正判斷
  （雙管道失敗設計：一般的 `CheckResult` 失敗對比組合錯誤的
  Go error）已在上方自己的段落中明確記錄，而非留作隱含——
  未來擴充任一檢查器的讀者，應該遵循相同的劃分，而不是重新
  引入本節點自己的測試最先抓到的混合版本。
- 本波次沒有新增 ADR，沒有變更請求升級，也沒有任何凍結合約
  的疑問。`app.RepositoryCheckpointService.Verify` 與
  `app.EvaluationService.ConsumeAuthorization` 凍結的簽章
  （`internal/app/ports.go`）都完全依宣告使用，沒有要求任何
  新增。

## Phase 9

本波次共三個節點，依任務簡報明確指示，每個都各自獨立驗證並
提交（絕不批次處理）：`runtime-a09`、`runtime-a10`、
`runtime-b06`。先合併 `origin/main`（fast-forward，乾淨），
帶入 Phase 8 的整合狀態：predictor 真正、已強化的
`ConsumeAuthorization`，以及 checkpoint 已完成的 Part A/B
最終關卡。

### runtime-a09：重複喚醒的恰好一次保證 + 取消優先於競態

**本節點找到並修好的真實錯誤。** `lifecycle.go` 的 `Cancel`
與 `Resume`（runtime-b07，前一波次）原本的寫法是先一次
`GetByID`，接著執行一次或多次無條件的 `UpdateStatus` 呼叫。
這個形狀有一個真實的「檢查時刻到使用時刻」間隙：兩個並行
呼叫端同時作用在**同一個** `PauseID` 上（本節點任務簡報
明確點名的分裂大腦情境——一個租約在看似過期後被回收，但
原本的持有者其實仍然存活、且渾然不覺），兩者都可能讀到同一個
起始狀態，並雙雙持久化「成功」，悄悄地互相覆蓋。這不是假設性
的問題：本節點自己的
`TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState`
測試的早期版本（見「經驗教訓」）就抓到了測試自身假設中一個
*不同的*、更細微的問題，而這反過來確認了底層的修法是必要且
正確的。

**修法**：在 `PauseStore` 介面（`requestpause.go`）中新增
`CompareAndSwapStatus(ctx, id, expected, next) (ok, found
bool, err error)`，在 `MemStore` 上以 store 既有的互斥鎖
實作（這是記憶體參考實作，對應真正 SQLite 版 store 的
`UPDATE ... WHERE status = ?` 條件式更新——完全呼應
`internal/scheduler.Store.Complete/Fail/Renew` 自身
`WHERE status = ? AND lease_owner = ?` 的模式，這裡把它
套用在 `pause_records` 而非 `wake_jobs` 上）。`Cancel` 與
`Resume`（`lifecycle.go`）被重寫，圍繞著一個共用的
`applyCASVerb`／`applyCASFrom`／`tryApplyCAS` 重試迴圈輔助
函式：現在每一次狀態轉換都是一個原子的「讀取-`Apply`-交換」
單元，在輸掉競態時重試，而不是覆蓋掉一個並行寫入者，也不是
悄悄放棄。

**新增**：`wake.go` 的 `Wake(ctx, store,
WakeRequest{PauseID})`——排程器與暫停狀態機之間的橋樑，
套用 `EventWakeDue`（`Sleeping -> WakePending`），此前完全
沒有任何程式碼實作過這件事（以 grep 確認：本節點之前，
`EventWakeDue` 只在轉換表與測試中被提及）。這補上了建置一個
真正端對端「重複喚醒」測試所需的缺口，而不只是孤立地測試
既有的租約取得互斥性（`internal/scheduler`）。

**測試**（`internal/pause/wake_test.go`、
`splitbrain_test.go`）：
- `TestDuplicateWake_WorkersYieldOneResume`／
  `_WorkersAcrossManyPausesEachWokenOnce`——多個 goroutine
  （20 個，重複 50 次）在同一個 `PauseID` 上競爭 `Wake`，
  恰好一個成功，每一個輸掉的呼叫都是一個真正的
  `*pause.TransitionError`；並延伸到 N 個各自獨立休眠中的
  暫停被並行競爭。
- `TestDuplicateWake_SplitBrainReclaimedLeaseOriginalHolderStillAlive_OnlyOneWakeSucceeds`
  ——使用真正的 `scheduler.Store` 呈現字面上的分裂大腦情境：
  worker A 取得一個短租約，租約在 A 仍然存活（且不知情）時
  過期，worker B 依 `scheduler.Store` 自身的過期租約規則
  合法地將其回收，A 與 B 兩者並行地對同一個 `PauseID` 呼叫
  `Wake`——恰好一個成功，且租約層自己的 `Complete` 也獨立
  確認只有 B（真正目前的持有者）能夠完成這個工作。
- `TestCancelAndWake_ConcurrentRaceNeverLeavesInconsistentState`、
  `TestCancel_WinsAgainstAlreadyInFlightWake`、
  `TestCancel_CannotWinAfterResumeStarted`——證明取消在對抗
  喚醒的真實競態中永遠勝出（`Sleeping` 與 `WakePending`
  都有 Cancel 邊），即使喚醒已經先落地，直到 `Resume`
  真正開始為止（`Resuming` 沒有 Cancel 邊——ADD §20.11 的
  競態視窗恰好在那裡關閉，不早也不晚）。
- `TestMemStore_CompareAndSwapStatus_*`——對這個新原語的
  直接單元測試涵蓋，包含一個 25 個 goroutine／重複 30 次的
  並行序列化證明，獨立於 `Cancel`／`Resume`／`Wake` 自身的
  語意之外。

這些涵蓋範圍設計為直接餵給 `qa-07` 專屬的
`DoubleWorkerRace -race -count=20` 壓力測試——DAG 將
`runtime-a09` 列為 `qa-07` 唯一的依賴。

```yaml
node: runtime-a09
status: completed
artifacts:
  - internal/pause/requestpause.go (CompareAndSwapStatus added to PauseStore + MemStore)
  - internal/pause/requestpause_test.go (fakePauseStore stub method added)
  - internal/pause/lifecycle.go (Cancel/Resume rewritten around CAS retry loop)
  - internal/pause/wake.go (new: Wake)
  - internal/pause/wake_test.go (new)
  - internal/pause/splitbrain_test.go (new)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/...   # OK"
  - "go test ./internal/scheduler/... ./internal/pause/... -run 'DuplicateWake|Cancel' -race -v   # all PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/... -race -v   # all PASS"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: e7d37be
next_action: runtime-a11 (Required tests — crash-after-every-phase, expired-lease-reclaim, XL) — NOT this phase
assumptions:
  - "PauseStore.CompareAndSwapStatus is a NEW interface method (not a
    frozen cross-component port — PauseStore itself is this package's own
    internal seam, per requestpause.go's existing doc comment) — widening
    it was in-bounds since only this package's own MemStore implements it
    today (confirmed by grep before making the change)."
  - "Wake does not itself claim or complete a scheduler lease — that
    remains internal/scheduler.Store's job; a future scheduler-worker loop
    composes Claim -> Wake -> ValidateResume/Resume -> Complete, with
    Wake's CAS guarantee covering only the pause-record-mutating middle
    step regardless of what the lease layer decided."
  - "splitbrain_test.go models the split-brain scenario against
    pause.MemStore (not a real SQLite-backed PauseStore), since no such
    adapter exists yet — the same documented gap persistphase_test.go's
    own seedPauseRecordRow comment already calls out (pause_records the
    SQL table vs. PauseStore the in-memory interface are two different
    backing stores today for what is conceptually one pause record)."
blockers: []
```

### runtime-a10：provider 中斷者／恢復者的 fake 合約測試

首先研究了 `app.TurnInterrupter`／`app.SessionResumer`
（`internal/app/ports.go`）除了其單純的方法簽章之外，是否還
帶有任何額外凍結的行為合約——查閱了 `Auspex_ADD.md` §9.10、
§20.6 第 4 階段、§20.15、§28.4、`CONTRACT_FREEZE.md`，以及
`agents/claude-provider.md` 自己的延伸目標章節。確認結果：
沒有。兩者都是刻意窄範圍、單一方法的介面，凍結合約本身沒有
任何文件註解；ADD 唯一相關的指引是操作性的（§20.15：
「provider 中斷逾時 → 終止受管行程，標記為不確定」；§28.4：
「檢視 provider，做協調」）——這是疊加在這些呼叫之上、
pause 套件層級的事，而不是介面本身需要的第二個回傳管道。

圍繞著 `internal/pause` 自身呼叫端實際依賴的那些性質，
建置了這套合約測試組：一次形式正確的呼叫會成功，並回傳內部
一致的資料（`SessionResumer.Resume` 的 `RunHandle.SessionID`
必須與請求相符，絕不悄悄替換）；失敗會以單純回傳的錯誤呈現，
絕不 panic（「provider 中斷失敗會留下可復原狀態」這件事取決於
再往上一層——`EventInterruptFailed` 只能套用在一個普通的
錯誤值上）；除非某個實作明確選擇退出
（`SkipContextCancellation`），否則會尊重 context 取消。

**新增**：
- `internal/testutil/fakes/provider.go` —— `FakeTurnInterrupter`／
  `FakeSessionResumer`，完全遵循此套件既有的 Func 欄位慣例。
- `internal/testutil/fakes/providercontract.go` ——
  `ProviderInterrupterContract`／`ProviderSessionResumerContract`，
  各自接受一個建構子閉包加上一個
  `ArrangeSuccess`／`ArrangeFailure` 設定，讓任何實作——
  今天的這些 fake，或未來 `claude-provider` 真正的訊號中斷／
  session 恢復介接器（該角色自己記錄在案的延伸目標）——都能
  執行同一套測試組來證明自己合規，`runtime` 不需要為每個
  介接器各自撰寫測試。
- `internal/testutil/fakes/providercontract_test.go` ——
  對兩個 fake 都執行這套測試組，包含未設定 fake 的預設路徑，
  以及一個明確示範 context 取消退出選項的測試。

```yaml
node: runtime-a10
status: completed
artifacts:
  - internal/testutil/fakes/provider.go (new)
  - internal/testutil/fakes/providercontract.go (new)
  - internal/testutil/fakes/providercontract_test.go (new)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/...   # OK"
  - "go test ./internal/testutil/fakes/... -run ProviderContract -v -race   # all PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/... -race -v   # all PASS"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: e246ee1
next_action: none named for this node in the DAG ("None" in the Blockers column) — claude-provider's own future stretch-goal adapter is the natural future consumer of this suite, not a runtime follow-up
assumptions:
  - "Neither interface has additional frozen behavioral invariants beyond
    the bare signature — confirmed by direct research against
    Auspex_ADD.md/CONTRACT_FREEZE.md/agents/claude-provider.md before
    writing the suite, not assumed. The suite therefore deliberately tests
    only the properties internal/pause's own call sites actually rely on,
    not speculative invariants no document states."
  - "The suite is a function taking a constructor closure (newX func() X)
    plus an Arrange* configuration struct, not a fixed instance or a
    zero-config default — this is what lets a future real adapter's own
    contract test file supply its own success/failure arrangement without
    this package guessing at how a real adapter fails on demand."
blockers: []
```

### runtime-b06：決策允許／拒絕接上真正的 EvaluationService

把真正的 `internal/evaluation.Service`（predictor-09/10，
都已整合到 `main`）接上 `auspex decision allow`／
`decision deny`，取代 runtime-b03 的 fake——依 DAG 註記，這是
一項硬依賴，因為只有真正、以儲存為後盾的
`ConsumeAuthorization`，才能端對端證明重放拒絕的保證，而不只是
模擬它。

**設計**：`agents/runtime.md` Part B 管線步驟 10／11
（「`decision allow` 發出一次性授權」／「重新提交的提示詞
在允許前恰好消耗一次授權」）描述的是同一條流程中兩個**不同**
的時刻，而不是同一次呼叫。`DecisionAllowCmd`
（`internal/orchestrator/decision.go`）依呼叫端是否提供
`AuthorizationID` 在兩者之間選擇：
- **發出流程**（沒有 `AuthorizationID`）：讀取評估的
  `Decide` 結果，然後透過一個新的本地 `AuthorizationIssuer`
  介面發出一個全新的一次性 `app.Authorization`——
  `IssueAuthorization` 是 `*evaluation.Service` 上真實存在的
  方法，但刻意**不**屬於凍結的 `app.EvaluationService`
  介面（透過閱讀 `internal/evaluation/service.go` 自身的
  文件註解確認，該註解正是預期了這個未來的呼叫端），所以本
  套件為它需要的這一個額外方法宣告了自己窄範圍的介面，呼應
  `evaluate.go` 既有的
  `UsageObservationLoader`／`GitSnapshotter` 先例。
- **消耗流程**（提供了 `AuthorizationID`——重新提交的情況）：
  直接以呼叫端的 `TurnID`／`PromptHash` 呼叫真正的
  `ConsumeAuthorization`，絕不重新推導新的決策或新的授權。

`DecisionDenyCmd` 透過 `Decide` 讀回決策（讀回，而非重新
計算，依 `internal/evaluation/doc.go` 自身記錄的慣例），
沒有任何授權上的副作用——沒有所謂「撤銷授權」這回事；單純
從不發出／不消耗授權，就已經達成了「拒絕」。

**接線**：`internal/cli/decision.go`（`NewDecisionCmd`）與
`internal/app/wiring/wiring.go` 中新增的 `Services.Decision`
欄位，以 `Decision.Issuer != nil` 為門檻（而不是單看
`Evaluation` 是否非 nil——一個 fake 完全可以妥善實作
`app.EvaluationService`，卻不同時滿足
`AuthorizationIssuer`，所以 `Issuer` 才是正確、最小化的
「真正接線已就緒」訊號）——與 runtime-b05／b07 為
`checkpoint`／`pause` 建立的「先 stub、接線後才換上」既定
慣例一致。

**真實管線測試骨架**（`decision_realauth_test.go`）：針對
真正的 predictor 管線各階段（`scope`、`token`、`quota`、
`risk`、`policy`）與一個已遷移的 SQLite 資料庫，建置了一個
真正的 `*evaluation.Service`——這個檔案中完全沒有 fake 版的
`app.EvaluationService`。一個 fake 的 `DataSource`，依
`internal/predictor/risk/combiner.go` 實際的評分公式調校
（大量變更檔案／行數的分位數，加上真正管線會讀取的每一個
完成度／影響範圍旗標：安全敏感、可能涉及遷移、跨層、範圍
開放式），穩定地把 `OverallRisk.Score` 推到 1.0（危急級別，
透過直接檢視確認——`app.PolicyCheckpointAndRun`），並搭配一個
另外的低風險對照 fixture，確認會落在 `app.PolicyRun`，證明
高風險 fixture 測試的是真實的東西，而不是「每個輸入都被標記」
的假象。

全部四項必要測試皆已逐字證明：
- **「高風險阻擋與允許一次流程」**：高風險 fixture 抵達
  `PolicyCheckpointAndRun`，且 `DecisionAllowCmd` 的發出
  流程成功為它發出一個真正、尚未被消耗的 `Authorization`。
- **「第二次授權重放被拒絕」**：發出 → 消耗（恰好成功一次）
  → 對同一個 `AuthorizationID` 第三次重放 → 被以
  `ErrCodeConflict` 拒絕，這是針對真正、經 predictor-10
  強化過的 `markAuthorizationConsumed` 條件式更新——不是
  一個單純斷言「這應該會發生」的 fake。延伸到一個 20 次
  緊密循序重放的迴圈（呼應 predictor-10 自身強化測試的
  風格）——20 次中恰好一次成功。
- **「重新提交的提示詞在允許前恰好消耗一次授權」**：消耗
  流程的測試直接證明了這一點——正是必要測試所點名的確切
  情境。
- **「checkpoint 失敗時不會發出授權」**：以真實的呼叫順序
  建模（一個 `checkpointThenDecisionAllow` 輔助函式，先呼叫
  真正的 `CheckpointCreate`，一旦出錯就提早結束，永遠不會
  抵達 `DecisionAllowCmd`），搭配一個間諜版的 `Issuer`，
  只要真的被呼叫就會讓測試失敗——以建構方式證明，而不只是
  推論。

```yaml
node: runtime-b06
status: completed
artifacts:
  - internal/orchestrator/decision.go (new: DecisionAllowCmd, DecisionDenyCmd, AuthorizationIssuer)
  - internal/orchestrator/decision_test.go (new: structural/validation coverage against fakes)
  - internal/orchestrator/decision_realauth_test.go (new: real evaluation.Service integration)
  - internal/cli/decision.go (new: NewDecisionCmd)
  - internal/app/wiring/wiring.go (Services.Decision field + RootCmd swap)
  - internal/app/wiring/wiring_test.go (decision wiring tests)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/...   # OK"
  - "go test ./internal/orchestrator/... -run 'DecisionAllow|ReplayRejected' -v   # all PASS"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... ./internal/testutil/fakes/... ./internal/app/wiring/... -race -v   # all PASS"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: e150b35
next_action: runtime-b09 (JSON/error contract across all P0 commands) — NOT this phase
assumptions:
  - "AuthorizationIssuer is a NEW local interface in internal/orchestrator
    (not internal/app/ports.go, which is contract-integrator-owned and not
    touched) — IssueAuthorization is real on *evaluation.Service but
    intentionally outside the frozen app.EvaluationService interface, per
    that package's own doc comment anticipating this exact caller."
  - "Services.Decision is gated on Issuer specifically, not on Evaluation
    being non-nil, since only the real concrete Service satisfies both
    seams simultaneously — a fake EvaluationService alone must not
    trigger the swap to a command path that assumes real authorization
    semantics."
  - "DecisionAllowRequest's SnapshotFingerprint/RepositoryCheckpointID are
    issue-flow-only, threaded verbatim from the caller (which is expected
    to have already run `checkpoint create` upstream) — this command does
    not itself create a checkpoint, mirroring CheckpointCreate's own
    two-step, not-blurred-together precedent."
blockers: []
```

## Phase 9 跨節點觀察

- 三個節點都依明確的任務指示各自獨立驗證並提交——沒有批次
  處理。每個節點自己的驗證指令（來自 `EXECUTION_DAG.md`）都
  先執行並確認綠燈，才會進到下一個；每個節點之後，除了本身
  擁有套件的完整測試套件，還額外執行了一次全 repo 的
  `go build`／`go test`，而不只是在波次結束時執行一次。
- `runtime-a09` 是本波次唯一一個，其自己的測試套件在提交前，
  在它**自己**的第一版草稿中（而不是既有程式碼）就抓到真正
  設計缺陷的節點——完整經過請見「經驗教訓」。修正的是測試的
  斷言，而不是實作，但這個過程正是為什麼 CAS 重試迴圈實作本身
  被撰寫並測試得如此嚴謹的原因：一個天真的第一版實作嘗試
  （單純的 `GetByID` + `UpdateStatus` 序列，也就是本波次之前
  `Cancel`／`Resume` 原本的樣子）在一個正確撰寫的同一測試下，
  會可靠地失敗，而不是偶發性地失敗——這一點已直接驗證過
  （見「經驗教訓」）。
- `runtime-a10` 與 `runtime-b06` 在撰寫任何程式碼之前都需要
  一輪專門的研究：分別確認 `TurnInterrupter`／
  `SessionResumer` 除了單純的簽章之外沒有任何額外凍結的
  行為合約，以及逆向工程真正的風險評分公式
  （`internal/predictor/risk/combiner.go`），使其足以建置
  一個 fake 的 `DataSource`，能可靠地把真正的管線推進到特定
  的風險等級，而不只是從一個被模擬的 `Decide` 呼叫斷言某個
  政策動作。這兩輪研究都反映在其相關程式碼確切的位置的
  註解中，而不只是寫在這份進度產物裡。
- 本波次沒有新增 ADR，沒有跨角色變更請求升級，也沒有任何
  凍結合約的疑問。`internal/app/ports.go`、
  `internal/domain/**`、以及 `internal/evaluation/**` 都
  只是被呼叫，從未被修改，依任務明確的邊界。

## Phase 10

兩個依序的節點，各自獨立驗證並提交：`runtime-a11`（最終的
Part A 整合關卡）與 `runtime-b09`（Part B 的錯誤合約 +
隱私閘門稽核）。兩者都是全面性的證明／稽核節點，而不是新功能
節點——任務簡報明確指出，在什麼都沒發現的情況下製造忙碌工作
是錯誤的結果；兩個節點都是先做研究，再確實補上研究發現的
真實缺口，並在沒有缺口存在之處精確回報「沒有」。

### runtime-a11：完整 Part A 生命週期整合證明 + provider 中斷失敗缺口的補齊

一個專門的研究代理人，在撰寫任何程式碼之前，逐一比對
`agents/runtime.md` Part A「必要測試」清單中的每一項，
對照所有既有的涵蓋範圍（`internal/pause/**`、
`internal/scheduler/**`），精確到檔案與行號。依必要測試逐項
列出發現：

- **「每個階段之後崩潰都能正確恢復／協調」**：`persistphase_test.go`
  既有的 `HaltAfter`／`HaltError` 骨架（runtime-a05）已經
  完整涵蓋 5 個 PERSIST 子階段。**真實缺口**：沒有任何測試
  對其他約 9 個頂層生命週期轉換（`Predicted->Requested` 一路
  到 `Resuming->Resumed`）做過崩潰注入。以
  `TestFullLifecycle_CrashAfterEveryTransition_ResumesOrReconciles`
  （一個 9 步驟的掃描，每次轉換後都「崩潰」——從持久 store
  重新讀取——並斷言沒有工作遺失或重複）補齊，加上
  `TestFullLifecycle_CrashDuringQuiescing_EmergencyShortCircuitReconciles`
  處理緊急短路的邊界情況。
- **「重啟會復原 wake job」**：a07 的 `restart_test.go` 已在
  排程器／租約層級孤立證明這一點。**真實缺口**：沒有任何測試
  把 `scheduler.Store.Restart` 與 `pause.Wake` 以及一次真正的
  `ValidateResume` 呼叫組合在同一條流程中。以
  `TestFullLifecycle_RestartRecoversWakeJob_ThenReEntersResumeValidation`
  補齊。
- **「配額不安全時重新排程」／「repo 重疊時阻擋」／「無關的
  repo 變更依所設定的政策處理」**：a08 的
  `resumevalidation_test.go` 已直接在 `ValidateResume`
  函式層級證明這些。**真實缺口**：沒有任何測試把這些透過
  **完整**生命週期驅動過去（`RequestPause` → 持久化轉換 →
  `Wake` → `ValidateResume` → 透過 `Verdict()` 的 `Resume`）。
  以 `TestFullLifecycle_QuotaUnsafeReschedules_EndToEnd`、
  `TestFullLifecycle_RepoOverlapBlocks_EndToEnd`、
  `TestFullLifecycle_UnrelatedRepoChangeFollowsPolicy_EndToEnd`
  （兩種政策分支皆有）補齊。
- **「多個 worker 競爭恰好一個成功恢復」／「過期租約被回收」／
  「取消優先於與喚醒的競態」**：a09 已經證明這些，包括一次
  真實的組合（`splitbrain_test.go` 把一個真正的
  `scheduler.Store` 與 `pause.Wake` 配對）。本節點在生命週期
  中再往下**多一層**重新跑了等價的競態（透過一次真正的
  `ValidateResume`／`Resume` 呼叫），專門用來抓住較窄的 a09
  組合可能漏掉的任何交互影響——呼應了 a09 自身如何在上一波次
  抓到既有程式碼中一個真實錯誤。結果：**沒有發現新的錯誤；
  以 CAS 為基礎的保證在更完整的組合下依然成立，這是精確確認
  過的，而不是假設的。** 詳見
  `TestFullLifecycle_DuplicateWakeRace_ThroughFullValidateResume`、
  `TestFullLifecycle_ExpiredLeaseReclaimed_ThenFullValidateResume`、
  `TestFullLifecycle_CancelWinsRace_EvenDuringValidation`。
- **「provider 中斷失敗會留下可復原狀態」**：本節點找到的**唯一
  真實生產程式碼缺口**。轉換表的邊
  （`{Interrupting, interrupt_failed} -> Failed`）以及一個
  單純 `Apply` 層級的測試已經存在，runtime-a10 也已經建置了
  `FakeTurnInterrupter`——但 `internal/pause` 中沒有任何生產
  程式碼真正呼叫過 `TurnInterrupter`，並把結果事件套用到一筆
  真正的 `PauseRecord` 上。`safepoint.go` 的
  `PersistThenInterrupt`（runtime-a04）依其自身記錄的範圍，
  刻意只證明順序，從不觸及 `PauseStore`／`Apply`。**修法**：
  新增 `internal/pause/interrupt.go`——`TurnInterrupterAdapter`
  （把凍結的 `app.TurnInterrupter` 橋接到本套件以 `PauseID`
  為鍵的介面上，正如 `safepoint.go` 自身的文件註解早已預期
  未來某個節點會做的那樣）以及 `InterruptAndSleep`（透過
  `lifecycle.go`／`wake.go` 已建立的同一套
  `CompareAndSwapStatus` 紀律，驅動
  `Interrupting -> {Sleeping | Failed}`）。由
  `TestFullLifecycle_ProviderInterruptFailure_LeavesRecoverableState`
  （紀錄持久地落在 `Failed`，可讀取，絕不卡在 `Interrupting`）
  加上一個成功路徑的對照測試，以及一個錯誤起始狀態被拒絕的
  測試證明。

**本節點自己的第一版草稿中，抓到並修好的兩個測試設計錯誤**
（不是實作的錯誤，這與本角色既定的做法一致：先假設「我的
斷言可能編碼了錯誤的心智模型」，而不是先假設有真實錯誤）：
1. 崩潰掃描測試中一個過於嚴格的「先前步驟的事件絕不能再次
   觸發」斷言，在 `Validating->Resuming` 上失敗，因為
   `EventResumeValid` 在轉換表中合法地擁有**兩條**邊
   （`WakePending->Validating` 與 `Validating->Resuming`，
   `statemachine.go`）——因此重新推導出正確的不變性（協調後
   的狀態必須恰好等於緊接在前那一步自己的輸出，且絕不能
   倒退回兩步或更早之前的狀態）。
2. `TestFullLifecycle_RepoOverlapBlocks_EndToEnd` 斷言
   `domain.PauseBlockedConflict` 是終止狀態——它刻意**不是**
   （`statemachine.go` 的 `terminalStates` 集合排除了它；
   ADD §20.9 的手動解決 UI 透過一條記錄在案的 `EventCancel`
   邊抵達它）。把斷言修正為檢查真正的性質：沒有任何**自動**
   事件擁有從 `BlockedConflict` 出發的邊，只有記錄在案的手動
   `Cancel`。

```yaml
node: runtime-a11
status: completed
artifacts:
  - internal/pause/interrupt.go (new: TurnInterrupterAdapter, InterruptAndSleep)
  - internal/pause/fulllifecycle_test.go (new: 12 test functions)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/app/wiring internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/pause/... ./internal/scheduler/... -race   # PASS (DAG's literal validation command)"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "golangci-lint run ./internal/pause/...   # 0 issues"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: 084d002
next_action: runtime-b09 (error contract + privacy gate audit) — same phase, done next
assumptions:
  - "TurnInterrupterAdapter/InterruptAndSleep are new production code in
    internal/pause (this role's own exclusive path), not a widening of any
    frozen internal/app/ports.go interface — app.TurnInterrupter itself is
    untouched; the adapter satisfies pause.Interrupter (safepoint.go's own
    existing, narrower seam), exactly as that file's doc comment already
    anticipated a later node would do."
  - "This node's job (per the task brief) was to find and close GENUINE
    gaps only, not manufacture busywork — of the 9 required tests audited,
    5 were already fully proven and needed no new code; 3 needed a fuller
    lifecycle composition (new tests, no new production code, and no bugs
    found); exactly 1 (provider interrupt failure) needed new production
    code because no prior node had actually wired that call path."
blockers: []
```

### runtime-b09：所有 P0 指令的一致錯誤合約 + 隱私閘門稽核

一個專門的研究代理人，在撰寫任何程式碼之前，對照
`agents/runtime.md`「JSON and errors」合約，逐一稽核每個 P0
指令的錯誤路徑、成功路徑、schema 版本控管，以及隱私處理，
精確到檔案與行號。發現如下：

- 每一個真正（非 stub）的指令（`checkpoint create`、
  `decision allow`／`deny`、`pause request`／`cancel`、
  `resume`、`scheduler run-once`、`status`、`doctor`）內部
  在每一條錯誤路徑上，都已經建構了一個 `*domain.Error`，
  且在成功時也已經輸出了自己 schema 版本化的 JSON。合約的
  這一部分本來就是正確的——這是確認過的，而不是假設的。
- **唯一真實、可修復的缺口**：沒有任何指令的型別化錯誤曾被
  序列化為 JSON。每個指令都建構了正確型別的 Go 值，但
  Cobra 自身預設的錯誤印表機（`SilenceErrors: false`）會把它
  攤平成一行單純的 `.Error()` 純文字，輸出到 stderr——本節點
  之前，`internal/cli/errors.go` 只有一個輔助函式
  （`notImplemented`），完全沒有任何 JSON 呈現路徑。
- **修法**：`internal/cli/errors.go` 新增了
  `SchemaVersionError`（`"auspex.error.v1"`）、`RenderErrorJSON`
  （把任何錯誤轉成凍結的信封格式，把非 `*domain.Error` 降級為
  `ErrCodeInternal` 而不是什麼都不輸出），以及
  `WithJSONErrorRendering`（走訪整個指令樹，包裝每個葉節點的
  `RunE`，讓它**同時**把 JSON 信封寫到 stderr，並原封不動地
  回傳原本的錯誤，讓每一個既有的 `errors.As` 呼叫端／測試都
  能繼續正常運作——純增量新增）。接上 `cli.NewRootCmd()` 與
  `internal/app/wiring.App.RootCmd()` 兩者（後者在每次
  `replaceSubcommand` 替換後都會重新套用一次，因為新建置的
  真正子樹會是未包裝的；一個 `Annotations` 標記讓重複包裝一個
  已包裝過的葉節點成為安全的無操作，所以呼叫兩次絕不會把
  信封寫兩次）。
- **由本節點自己的測試抓到、而非被假設不存在的真實錯誤**：
  維持 `SilenceErrors: false`（早期草稿「純增量新增，不改變
  既有行為」的直覺）直接違反了「機器模式絕不輸出裝飾性文字」
  ——Cobra 自己的純文字行，仍然會在每一個指令的新 JSON 信封
  之後被印出，第一次執行時就被
  `TestErrorContract_NoDecorativeTextOnAnyCommand` 在整個
  指令樹上抓到。修法：`root.go` 中設為
  `SilenceErrors: true`——JSON 信封是嚴格意義上更好的替代品，
  而不是與 Cobra 自身文字並存的附加物。
- **既有的已知缺口，現在以明確、有檢查的測試記錄下來**
  （而非悄悄修好，因為修好它們是比本節點任務範圍更大的設計
  決定）：`init`／`evaluate`／`progress show`／`state show`
  在整個 repository 中都沒有任何真正的 CLI 建構子（永久性的
  `notImplemented` stub，經研究確認）——
  `TestErrorContract_KnownIncompleteCommands_AreStubsOnly`
  在未來某個節點新增真正的建構子、卻沒有更新這則註記時，
  會大聲失敗。`version` 的成功輸出是一個單純字串，而不是
  schema 版本化的 JSON——變更一個已經整合的指令輸出形狀，
  被判定超出本次稽核的範圍（Constitution §7 規則 10：不做
  範圍以外的臆測性變更）——
  `TestErrorContract_VersionCommand_KnownGap_PlainStringNotJSON`
  明確記錄了這一點。
- `internal/httpapi` 在整個 repository 中並不存在（已確認：
  沒有目錄、沒有檔案）——這是 ADD／`agents/runtime.md` 明確的
  延伸目標，尚未建置（「HTTP daemon 對一個可運作的 CLI 而言
  是次要的」）。DAG 的驗證指令中提到
  `go test ./internal/httpapi/... ./internal/cli/... -run
  ErrorContract`；執行它可以確認這個組合會失敗（結束碼 1），
  純粹是因為 `internal/httpapi` 沒有目錄可供 `go test`，而
  單獨執行 `go test ./internal/cli/... -run ErrorContract`
  則乾淨通過——這正是任務簡報預期的、記錄在案的無操作，
  而不是真實的缺口，也沒有為了掩蓋它而建置任何佔位套件。
- **隱私閘門**：每一個觸及提示詞相關資料的指令（`decision
  allow`／`deny` 的 `--prompt-hash`、hook 的
  `user-prompt-submit`）都只會傳遞一個 `PromptHash`（在抵達
  這一層之前就已經是雜湊值，`internal/app/ports.go` 凍結的
  欄位），絕不傳遞原始提示詞文字——經 grep 稽核確認（在
  `internal/cli`／`internal/orchestrator`／
  `internal/app/wiring` 中，沒有任何原始提示詞欄位穿越這些
  邊界），並由
  `TestErrorContract_NoRawPromptInAnyErrorOrOutput`（在全部
  18 個 P0 指令路徑上做金絲雀字串掃描）以及
  `TestErrorContract_DecisionAllow_RealPath_NeverEchoesPromptHashAsRawText`
  （一個真正、非 stub 的發出流程測試，證明金絲雀字串絕不會
  外洩進 `decision allow` 自己的 JSON 輸出——而該輸出本來就
  正確地沒有 `prompt_hash` 欄位）直接證明。

```yaml
node: runtime-b09
status: completed
artifacts:
  - internal/cli/errorcontract_test.go (new: 10 test functions)
  - internal/cli/errors.go (SchemaVersionError, RenderErrorJSON, WithJSONErrorRendering)
  - internal/cli/root.go (SilenceErrors: true; wires WithJSONErrorRendering)
  - internal/app/wiring/wiring.go (re-applies WithJSONErrorRendering after replaceSubcommand)
validation:
  - "gofmt -l internal/pause internal/scheduler internal/orchestrator internal/cli internal/app/wiring internal/testutil/fakes   # empty"
  - "go build ./...   # OK"
  - "go vet ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/cli/... -run ErrorContract -v   # all PASS (httpapi half of the DAG's combined command is a confirmed no-op, not a real target — internal/httpapi does not exist)"
  - "go test ./internal/pause/... ./internal/scheduler/... ./internal/orchestrator/... ./internal/cli/... -race -v   # all PASS"
  - "golangci-lint run ./...   # 0 issues, whole repo"
  - "go build ./... && go test ./...   # all PASS, whole repo, zero regressions"
commit: ad335b2
next_action: runtime-b10 (this role's final node) — NOT this phase
assumptions:
  - "internal/httpapi is out of vertical-slice scope per agents/runtime.md's own
    stretch-goal framing; the DAG's validation command referencing it is
    a documented no-op for this node, not a gap this node closes by
    building a placeholder package."
  - "WithJSONErrorRendering's returned Go error is intentionally UNCHANGED
    from today's behavior — every existing test that asserts on the
    returned error (errors_test.go, root_test.go, wiring_test.go) needed
    zero changes; the JSON envelope write to stderr is the only new
    behavior, confirmed by running the full existing suite unmodified
    after this change and seeing zero regressions."
  - "version's plain-string output and init/evaluate/progress-show/
    state-show's permanent-stub status are DOCUMENTED gaps, not silently
    fixed ones — both are checked by a dedicated test that fails loudly
    the moment either changes, per Constitution §6's evidence discipline
    applied to a known-gap claim, not just a completed-node claim."
blockers: []
```

## Phase 10 跨節點觀察

- 兩個節點都依明確的任務指示各自獨立驗證並提交——沒有批次
  處理。每個節點自己的 DAG 驗證指令都先執行並確認（對
  `runtime-b09` 而言，則確認 `internal/httpapi` 那一半是
  記錄在案的無操作）才會進到下一個；每個節點之後，除了本身
  擁有套件的完整測試套件，還額外執行了一次全 repo 的
  `go build`／`go test`，而不只是在波次結束時執行一次。
- 兩個節點的形狀都與上一波次的
  `checkpoint-a09`／`checkpoint-b09`／`predictor-11` 相同：
  全面性的最終證明／稽核節點，而不是新功能節點。兩者都被
  明確指示要精確回報「沒有發現新東西」，而不是製造忙碌工作
  ——本波次，`runtime-a11` 在稽核的九項必要測試中恰好找到
  一個真實的生產程式碼缺口（provider 中斷失敗狀態機接線），
  `runtime-b09` 恰好找到一個真實、可修復的缺口（沒有 JSON
  錯誤呈現層），加上兩個既有、超出範圍的缺口，選擇記錄下來
  而非悄悄修好。
- 兩個節點自己撰寫測試的過程，都在**這同一個節點自己的**
  第一版草稿中抓到真實的錯誤，而不是在先前波次的程式碼中——
  這與 `runtime-a09` 在 Phase 9 抓到**較早**節點程式碼中錯誤的
  先例一致，卻又不盡相同。`runtime-a11` 的崩潰掃描測試與
  `BlockedConflict` 終止狀態的斷言，都是對照
  `statemachine.go` 實際的轉換表修正的；`runtime-b09` 的
  `SilenceErrors: false`「純增量新增」直覺，在它自己新的
  裝飾性文字測試抓到 Cobra 的純文字行仍然與新的 JSON 信封
  一起被印出後，被修正為 `true`。兩者都是本角色既定技巧
  （Phase 9 經驗教訓）的實例：當剛寫好的測試在第一次執行時
  就可靠地失敗，預設先假設「我的斷言／設計編碼了錯誤的心智
  模型」，直接驗證，再精確修正——絕不在查證之前就假設第一個
  假說（錯誤、測試建模錯誤、或設計錯誤）是對的。
- 本波次沒有新增 ADR，沒有跨角色變更請求升級，也沒有任何
  凍結合約的疑問。`internal/app/ports.go`、
  `internal/domain/**`，以及每一個其他角色擁有的套件，都
  只是被呼叫，從未被修改。`internal/pause/interrupt.go` 新增的
  `TurnInterrupterAdapter` 滿足 `pause.Interrupter`（本套件
  自己的內部介面，而不是凍結的 port），正如 `safepoint.go`
  早已預期的那樣；`internal/app/ports.go` 中沒有任何介面被
  放寬或觸及。
- `runtime-b10`（本角色的最後一個節點）依任務明確指示不在
  此時開始，留待未來的波次。

# Phase 11

分支：`vertical-slice/runtime`，在 Phase 11 開始前透過
`git fetch origin && git merge origin/main`（fast-forward，
乾淨——沒有衝突）從 `main` 同步，落在 `b7346a4`（Phase 10
已完整整合）。指派的節點：`runtime-b10`——依任務簡報明確
所述，這是本角色**最後**一個垂直切片節點，在此之後不再有
指派給本角色的節點。

## runtime-b10 — 同一 SQLite 檔案下的行程內重啟（最終的 Part B 關卡）

### 依其自身 DAG 風險註記，本節點必須證明的事

`EXECUTION_DAG.md` 明確點名本節點的風險：「包含
同一 SQLite 檔案下的行程內重啟測試」——並指出這是 `qa-02`
端對端展示，以及 `qa-03` 專屬的 `RestartSameDB` 整合測試的
關卡。任務簡報自身列出的四項編號要求：(1) 針對一個真實的
磁碟檔案建置一整套 `wiring.Services`／`App`，透過一連串
貼近真實的指令序列執行，然後捨棄，再針對**同一個**檔案
建置一個全新的實例，證明它能看到先前所有的狀態並繼續正常
運作；(2) 即使前一個行程的連線**沒有**被乾淨關閉（真正的
崩潰，而不是優雅關機），同樣的保證仍然成立；(3) 把這件事
建置得足夠穩固，讓 `qa` 能有信心地在它之上繼續建置，而不必
從零開始；(4) 稽核 agents/runtime.md Part B「測試」清單，
找出任何尚未被 `b01`-`b09` 涵蓋的真實缺口，恰好補上真實
存在的部分，不多做。

### 找到並補上的兩個真實、既有的生產／測試缺口——不是
### 製造出來的忙碌工作

在撰寫任何重啟證明的程式碼之前，本節點先做了研究（直接研究，
再加上一個背景研究代理人的交叉查核，獨立確認了相同的發現），
確認 `wiring.Services` 的哪些欄位在整個 repository 中有真正、
非 fake 的實作。確認結果：`StateCheckpoint`
（`internal/statecheckpoint.Service`）、`RepositoryCheckpoint`
（`internal/repocheckpoint.Service`），以及
`Evaluation`＋`AuthorizationIssuer`
（`internal/evaluation.Service`）全都是真實的、以 SQLite 為
後盾。兩個**不是**：`ProgressTreeService` 在任何地方都沒有
統一的介接器（只有
`internal/testutil/fakes.FakeProgressTreeService`——這是
`checkpoint` 角色自己的缺口，不是本角色該補的），而
`GracefulPauseService` 同樣沒有介接器架在 `internal/pause`
的自由函式之上（這是本角色**自己** Part A 中一個真實的
缺口，但建置一個六方法的介接器，把 `Observe`／
`RequestPause`／`ReachSafePoint`／`EnterSleep`／`Resume`／
`Cancel` 橋接到既有、形狀不同的自由函式上，是一項實質的
新生產功能，而不是重啟安全性的證明——明確不在本節點的範圍
內，而且本角色擁有的 P0 指令今天沒有任何一個真正呼叫
`GracefulPauseService`；CLI 的 `pause`／`resume`／
`scheduler` 指令，是透過較窄的
`orchestrator.PauseLifecycleDeps.Store` 介面抵達暫停子系統
的）。

同一次研究浮現出**唯一一個真實、有實際影響、先前未被發現的
缺口**，本節點以新的生產程式碼補上：**`pause.PauseStore`
（`PauseLifecycleDeps.Store` 所要求的介面）在任何地方都沒有
SQLite 版的實作——只有 `pause.MemStore`，一個記憶體內的
參考／測試替身。** 先前四個波次中的五個 Part A 節點
（`runtime-a04`、`a05`、`a07`、`a09`）都各自在自己的文件
註解中記錄過同一個缺口，並刻意延後處理（「未來的整合節點會
把 `PersistPauseStore` 對應到一個真正 SQLite 版的
`PauseStore` 上」，`persistphase_test.go` 自己的
`seedPauseRecordRow` 註解）。若 `pause request`／
`pause cancel`／`resume` 仍然架在一個記憶體內的 store 上，
卻要證明重啟安全性，那會是不誠實的——依其建構方式，
`MemStore` 在其擁有的 `App` 被捨棄的瞬間就會一併被捨棄，
所以任何架在它之上的重啟測試都無法證明任何真實的事。
**修法**：`internal/pause/sqlitestore.go` 新增的
`SQLiteStore`，一個架在既有 `pause_records` 資料表
（遷移 0050，本角色自己的範圍）之上、真正的 `PauseStore`
實作——`FindActiveByKey`／`Insert`／`GetByID`／
`UpdateStatus`，加上透過與
`internal/scheduler.Store.Complete`／`Fail`／`Renew` 為
`wake_jobs` 已經確立的同一套條件式 `UPDATE...WHERE` 慣用法
實作的 `CompareAndSwapStatus`。刻意只涵蓋 `PauseStore`，
**不**涵蓋 `PersistPauseStore`（`GetProgress`／
`SaveProgress`，`runtime-a05` 自己較窄的五步驟持久化階段
介面）——把那個對應起來，依然是原本就已追蹤、仍然開放的
同一個缺口；把本節點的範圍擴大到也補上那個，會是超出
「證明既有事物的重啟安全性」之外的範圍蔓延，變成一個沒有人
要求本節點建置的新持久化階段功能。

第二個缺口，依任務項目 4 的稽核：agents/runtime.md Part B
「測試」清單列了 9 個項目；其中 7 個已經被 `b01`-`b09`
充分涵蓋（在得出「沒有缺口」的結論之前，先直接確認到檔案與
行號——而不是假設）。「行程結束碼」被證明與本套件自己的
測試中已經被驗證超過 38 次的同一個訊號相同
（`Execute()` 回傳的錯誤，由 `cmd/auspex/main.go`——不是
本角色的路徑——機械式地轉換為 `os.Exit(1)`）；「無 TTY 行為」
在結構上本來就有保證（`internal/cli`／`internal/orchestrator`
中沒有任何 TTY 偵測程式碼——經 grep 確認——且每一個既有測試
本來就已經透過一個非 TTY 的 `bytes.Buffer` 驅動每一個指令）。
**「CLI 黃金測試」是一個真實、先前未補上的缺口**：每一個既有
的成功路徑測試，都是把 JSON 解碼成 `map[string]any` 再檢查
個別的鍵——一個真實指令輸出中，意外新增、移除、改名、或
重新排序的欄位，會悄悄通過每一個既有的測試。`claude-provider`
已經為自己的套件確立了完全相同的慣例
（`internal/hooks/claude/testdata/*.golden.json`、
`userpromptsubmit_test.go` 的 `assertJSONEqual`）——本角色
自己的 CLI 介面沒有對應的東西。**修法**：新增
`internal/cli/golden_test.go` + 三個新的 fixture，位於
`internal/cli/testdata/golden/` 之下（本角色自己專屬的
路徑），涵蓋 `checkpoint create`（巢狀的雙服務結果）、
`decision allow` 發出流程（有條件欄位的結果），以及
`doctor`（結果陣列，最容易悄悄增減元素的形狀）——採結構化
比對（對解碼後的 JSON 做 `reflect.DeepEqual`，而不是逐位元組
字面比對，這樣無關緊要的格式差異絕不會造成偽陽性失敗），
比對已加入版本控管的 fixture，並提供一個
`AUSPEX_UPDATE_GOLDEN=1` 的逃生艙口，供未來刻意的輸出形狀
變更使用（呼應每一套黃金檔測試設定的標準慣例）。在認定這件事
已經補齊之前，驗證了這個比對真的能抓到一個真實的迴歸
（暫時性地弄壞一個 fixture，確認測試以清楚的差異失敗，再
復原它）。

### 完整的「同一 SQLite 檔案下的行程內重啟」測試設計

`internal/app/wiring/restart_test.go`，兩個測試：

1. **`TestRestart_SameSQLiteFile_FullLifecycleSurvivesProcessRestart`**
   （字面上要求的那個測試）。建置一個真正的磁碟 SQLite 檔案
   （絕不是 `:memory:`）、一個真正的暫存 Git repository，以及
   一整套架在真正的 `StateCheckpoint`／`RepositoryCheckpoint`／
   `Evaluation`＋`AuthorizationIssuer`／`PauseLifecycle`
   （本節點新的 `SQLiteStore`）／`scheduler.Store`／
   `Diagnostics` 之上的 `wiring.App`（只有 `ProgressTree`／
   `GracefulPause` 使用 fake，依上方記錄在案、非不誠實的範圍
   界線）。透過**真正的** cobra 指令樹（`App.RootCmd()`，
   每次指令呼叫都全新建置——理由見下方），驅動一連串貼近
   真實的操作：`checkpoint create` → 真正的 `EvaluateTurn`＋
   `Decide` → `decision allow` 發出流程 → `decision allow`
   消耗流程 → `pause request` → 排程並取得一個真正的
   wake job → `doctor`。接著把那個 `*sqlite.DB` 完全關閉，
   針對**同一個**檔案路徑建構一個**全新**的
   `wiring.App`／`*sqlite.DB` 配對——透過重啟後 `App` 自己
   真正的指令（絕不直接讀取資料庫，那樣只能證明儲存層，
   別處已經涵蓋過），證明：(a) 重新 `Migrate` 前後
   `CurrentVersion` 完全相同（沒有重複遷移）；(b) 重啟前的
   授權，在重啟後重放，仍然被拒絕（恰好一次的消耗，在一整套
   App／DB 重建之後依然持久，而不只是行程內的不變性）；
   (c) 重啟前的暫停紀錄仍然可讀取**且**可變更（`pause
   cancel` 成功——證明 `pause_records` 上沒有孤兒鎖）；
   (d) 重啟後可以排程**並**取得一個全新的 wake job（證明
   排程器的寫入＋取得鎖路徑，而不只是讀取）；(e) 重啟後，
   一次全新的 `checkpoint create`，以及一次全新的
   「發出→消耗→重放被拒絕」`decision allow` 循環都成功
   （證明每一條寫入路徑，而不只是先前已提交的讀取，都重新
   活了過來）。

2. **`TestRestart_SameSQLiteFile_UncleanShutdown_UncommittedWriteDoesNotCorruptFile`**
   （要求 2——「即使舊行程的 SQLite 連線沒有被乾淨關閉」）。
   這個測試**自己的第一版草稿**嘗試了兩種同行程內的模擬
   （在呼叫 `db.Close()` 之前放棄一個從 App 自己的連線池借來的
   `*sql.Tx`；接著第二種，另一個完全獨立的 `*sqlite.DB`
   單純從不 `Close()`），**兩者都**在緊接著的下一次
   `Migrate()` 呼叫上，真的以 `SQLITE_BUSY` 失敗——追溯到一個
   真實的 Go 語言層級事實，而不是儲存層的錯誤：
   `database/sql.DB.Close()` 的文件記載它會「等待所有已經開始
   處理的查詢……完成」，而不是強制關閉一個被放棄的交易，且
   在一個仍有開啟中 `*sql.Tx` 的連線上呼叫 `sql.Conn.Close()`
   會直接**死結**（在下這個結論之前，先以一個用完即丟的
   實驗直接驗證過，而不是假設）。單靠 `database/sql` 的公開
   API，同行程內的模擬無法忠實重現一次真正 OS 層級的行程死亡
   ——這是一個與先前任何波次的節點都不同、真正不同的失敗模式。
   **修法**：正是針對這種情況的標準 Go 慣用法——測試執行檔
   把**自己**重新以一個真正的子 OS 行程執行
   （`os/exec`、`os.Args[0]`、
   `-test.run=^TestZZZCrashWriterHelper$`），那個子行程開啟
   同一個磁碟檔案，開始一個真正的寫入交易，執行它，透過
   stdout 發出就緒訊號，然後阻塞；父行程一讀到就緒訊號，就
   發送一個真正的 `SIGKILL`。是作業系統——而不是本套件自己的
   記錄機制——回收子行程的檔案描述子，以及它們持有的任何
   SQLite 層級鎖定狀態，正如一次真正的崩潰會做的那樣。存活
   下來的（父）行程接著對同一個檔案開啟一個全新的連線，並
   證明：`Migrate` 依然成功（沒有 BUSY，沒有掛起）；被殺死
   的寫入者未提交的 UPDATE **沒有**套用（對同一個暫停 ID
   執行 `pause cancel` 成功，證明這筆紀錄讀回的是崩潰前的
   狀態，而不是被放棄的寫入所指向的目標狀態）；一次全新的
   `doctor` 與一次全新的 `checkpoint create` 都成功（證明
   沒有殘留的鎖延續到新連線中）。`TestZZZCrashWriterHelper`
   在任何一般的 `go test` 呼叫下都會自我跳過（以一個只有
   父測試會設定的環境變數為門檻），所以它在一般執行中絕不會
   污染，也不會被算作真正的測試。

兩個測試在被認定完成之前，都在 `-race` 之下重跑了 3 到 5
次，零偶發性失敗（Phase 6「行程 CPU 時間對比時鐘時間」的
掛起診斷技巧，以及 Phase 9「暫時性的原始碼內埋樁，確認後刪除」
的技巧，都在這次崩潰寫入者的調查中被重用——一個用完即丟的
`busytest` 實驗套件，建置在 `internal/app/wiring` 內部
——這是唯一能合法容納這種實驗的路徑——在本節點斷定確實需要
一個真正的子行程之前，精確地確認了 `sql.Conn.Close()` 的
死結，隨後在提交前刪除，從未留在 diff 中）。

### 設計筆記：每次指令呼叫都用一棵全新的 `App.RootCmd()`，而非重複使用同一棵樹

`execCmd`／`execCmdExpectError`（測試自己的驅動輔助函式）
為每一次指令呼叫都建置一棵**全新的** `a.RootCmd()`，而不是
在多次 `Execute()` 呼叫之間重複使用同一棵 `*cobra.Command`
樹——這個檔案自己的第一版草稿採用了後者，並發現了一個真實的
測試設計錯誤：cobra 以 `StringVar` 綁定的旗標變數，是在
指令樹建置時被擷取一次的，所以在**同一棵**樹上，之後某次
`Execute()` 呼叫若省略某個旗標，會悄悄維持**先前**那次呼叫
留下的值，而不是重設為預設值。一次 `decision allow` 發出
流程的呼叫，若接在同一棵重複使用的樹上、先前一次
`decision allow ... --authorization-id X` 呼叫之後執行，
會因為過期的旗標值仍然被設定，而被悄悄導向**消耗**流程。
每次呼叫都建置一棵全新的 `a.RootCmd()`，完全繞開了這個問題，
同時也是更忠實的重啟安全性證明：每一次對 `auspex` 執行檔的
真實呼叫，都是它自己全新的行程，只會建置一次自己全新的
cobra 樹，所以一個在多次呼叫之間重複使用同一棵樹的測試，
測的是真實執行檔實際上從不會做的事。

### 節點紀錄

```yaml
node: runtime-b10
status: completed
artifacts:
  - internal/pause/sqlitestore.go (new: SQLiteStore, a real PauseStore)
  - internal/pause/sqlitestore_test.go (new: unit + concurrent-CAS proof)
  - internal/app/wiring/restart_test.go (new: the two required restart tests)
  - internal/cli/golden_test.go (new: closes the "CLI golden tests" gap)
  - internal/cli/testdata/golden/checkpoint_create_success.golden.json (new)
  - internal/cli/testdata/golden/decision_allow_issue_success.golden.json (new)
  - internal/cli/testdata/golden/doctor_all_skipped_success.golden.json (new)
validation:
  - "gofmt -l internal/orchestrator internal/cli internal/pause internal/scheduler internal/app/wiring   # empty"
  - "go build ./...   # OK, whole repo"
  - "go vet ./internal/orchestrator/... ./internal/cli/...   # OK"
  - "go test ./internal/cli/... ./internal/orchestrator/... -race -v   # all PASS"
  - "go test ./internal/app/wiring/... ./internal/pause/... -race -v   # all PASS, including both restart tests and the new SQLiteStore suite"
  - "go build ./... && go test ./... -race   # all PASS, whole repo, zero regressions"
  - "golangci-lint run ./...   # 0 issues, whole repo"
commit: <recorded below>
next_action: NONE — this was runtime's FINAL assigned DAG node. Every node ever assigned to this role (a01-a11, b01-b10) is now complete.
assumptions:
  - "SQLiteStore implements PauseStore only, not PersistPauseStore — the
    latter (runtime-a05's own narrower persist-phase progress-bookkeeping
    interface) remains the same already-tracked, still-open gap it was
    before this node; reconciling it is out of this node's scope (proving
    restart safety of what exists, not building a new persist-phase
    feature)."
  - "GracefulPauseService and ProgressTreeService remain fake in this
    node's restart harness — neither has a real adapter anywhere in the
    repository (confirmed directly + via independent background-agent
    cross-check), and no P0 command this role owns calls
    GracefulPauseService at all (pause/resume/scheduler commands reach
    internal/pause through the narrower PauseLifecycleDeps.Store seam
    instead) — documented explicitly as a non-dishonest scope boundary,
    not silently papered over."
  - "The crash-writer subprocess technique (TestZZZCrashWriterHelper,
    os.Args[0] re-exec + SIGKILL) is the standard Go idiom for testing
    real process-crash recovery and was adopted only after two same-process
    simulation attempts were tried and found to give false results (a
    database/sql-level artifact, not a storage-layer bug) — documented in
    both the code comments and this artifact so a future reader does not
    have to rediscover why the simpler approach doesn't work."
  - "internal/httpapi, internal/daemon remain out of vertical-slice scope, unchanged
    from every prior phase's same observation — no code added there this
    node, consistent with agents/runtime.md's own stretch-goal framing."
blockers: []
```

## Phase 11 跨節點觀察——以及完整的角色回顧

由於這是 `runtime` 的最後一個垂直切片節點，本節同時總結
本波次以及整個角色歷程（Bootstrap 只有 lead 參與，依
`CONTRACT_FREEZE.md`；本角色自身的歷史橫跨 Phase 3 到
Phase 11，共 21 個 DAG 節點：`a01`-`a11`、`b01`-`b10`）。

**本波次自己的兩項發現**：與本角色先前每一個「最終關卡」
形狀的節點一致（Phase 10 的 `runtime-a11`、`runtime-b09`；
已經記錄過的跨角色 Phase 10 模式中的
`checkpoint-a09`／`checkpoint-b09`／`predictor-11`），一個
全面性的證明節點，在每個被稽核的子領域中，大約找到一個真實、
確實存在的缺口——一個在 Part A（`pause.PauseStore` 缺少
SQLite 後盾，以新的生產程式碼補上），一個在 Part B 自己的
測試清單中（「CLI 黃金測試」，以新的測試基礎設施補上）——
從不是零個，也從不是很多個。這是連續第四個「最終關卡」節點
（跨角色的 Phase 10 中 `checkpoint-a09`／`b09`／
`predictor-11`；同樣是 Phase 10 的 `runtime-a11`／`b09`；
現在是 Phase 11 的 `runtime-b10`）恰好落在這個形狀上，這是
強而有力、重複出現的證據，證明這個模式具有普遍性：本專案中
一個全面性的「稽核後補齊」節點，可靠地浮現出少量、真實、
可數的真實缺口——這從來不是先前節點建置得草率的跡象（並沒有；
每一個被找到的缺口，都已經被延後它的節點明確、誠實地記錄過），
也從來不是「沒有東西可找」的跡象（總是有東西可找，因為真正
端對端的整合，會驗證任何單一先前節點較窄的範圍都無法驗證的
交互作用）。

**崩潰模擬的教訓，是本波次唯一真正全新的技巧**，與先前任何
波次經驗教訓中已經點名過的都不同：Phase 5／9 的「行程 CPU 時間
對比時鐘時間」（診斷掛起）與「暫時性的原始碼內埋樁，確認後
刪除」（在信任一個假說之前先確認它），這裡都適用，但實際
需要的**修法**——把測試執行檔重新以一個真正的子行程執行，
並對它下 SIGKILL——是本角色技巧庫中新增的一項，值得為任何
**未來** Auspex 節點（無論是本角色或任何其他角色）明確點名，
只要它需要測試真正的行程崩潰復原，而不是行程內的近似值：
**`database/sql` 自己的連線池記錄機制，會讓行程內的崩潰模擬
不只是比較弱，而是主動具有誤導性**——它可能產生一個假陽性
的失敗（被放棄交易的 `SQLITE_BUSY`，這是 Go 自身連線池語意
的產物，不是被測試的儲存層本身），看起來完全像是一個真實的
崩潰復原錯誤，直到被追溯到它真正的源頭。本程式碼庫中任何
未來的崩潰復原測試，都應該從一開始就預設採用子行程＋SIGKILL
的技巧，而不是費力地重新發現同一個假陽性。

**完整的角色回顧（Phase 3 到 Phase 11，21 個節點）**：

- **排序紀律貫穿了整個歷程。** 每一個同時包含 Part A 與
  Part B 工作的波次，都把 Part A（狀態機／並行正確性風險）
  排在 Part B（相對而言風險較低、建立在 Part A**之上**的
  管線工作）之前，正如 Phase 5 最先確立的那樣，且每一個
  後續波次（6、7、9、10）都毫無偏離地遵循。本波次自己的節點
  （`b10`，純 Part B）是這項紀律自然的壓軸：等到 Part B
  需要一個最終整合關卡時，Part A 已經有自己 11 個節點份量的
  審視（在 Phase 10 `runtime-a11` 自身完整生命週期的證明中
  達到頂點），所以本節點真正剩餘的風險，幾乎全部集中在
  Part B 自己的管線工作，加上自 Phase 6 以來就被誠實延後、
  而非隱藏的**唯一**一個跨領域缺口（`PauseStore` 的 SQLite
  後盾）。
- **整個歷程中最常被重複、最常被驗證的技術教訓**：當一個
  剛寫好的測試第一次執行就失敗時，先假設「我自己的斷言、
  設計、或模擬技巧可能編碼了錯誤的心智模型」，而不是先假設
  「被測系統有錯誤」或「這個測試只是碰巧不穩定」——並且在
  對三種解釋中的任何一種下定論之前，永遠先取得**直接**證據
  （一個用完即丟的除錯埋樁、一次行程狀態檢查、一個孤立的
  重現）。這在 `runtime-a06`（Phase 5，一個真實的自我死結
  錯誤）、`runtime-a09`（Phase 9，一個真實的 TOCTOU 競態
  **加上**一個錯誤的測試斷言，接連發生，並被正確地區分開來）、
  `runtime-a11`（Phase 10，兩個錯誤的測試斷言，零個實作錯誤）、
  `runtime-b09`（Phase 10，一個真實的設計選擇錯誤，不是測試
  的錯誤），以及現在的 `runtime-b10`（Phase 11，一個錯誤的
  **模擬技巧**——一個先前五個實例都不曾出現過的新子情況，
  因為這次的錯誤既不在測試的斷言中，也不在被測系統中，而是
  在一個無效的同行程內崩潰模擬方法論中）中，都被獨立地
  重新推導出來並正確地套用。同一套底層紀律的五種不同子情況，
  每一種都被正確診斷，沒有一次是用猜的。
- **「全面性稽核後補齊，會找到約 1 個真實缺口」這個模式**，
  最先在 Phase 10 的跨節點觀察中被清楚點名，本波次是第三次
  也是第四次成立（Part A 的 `PauseStore` 缺口、Part B 的
  黃金測試缺口）——現在橫跨兩個波次、共五個實例，且跨角色
  來看，至少還有另外三個角色自己對應的最終節點也是如此，
  已獲得確認。這是整個歷程中最有力的一項流程證據：Auspex
  逐節點、逐波次、記錄在案的缺口紀律（Constitution §4.4
  「透過進度產物提出請求，不要閒置等待」+ 本角色一貫的做法：
  明確點名一個延後的缺口，而不是悄悄跳過它）正是**造就**
  這個模式得以成立的原因——本節點與其 Phase 10 前輩找到的
  每一個缺口，都已經被更早的節點寫下來，是可被找到的，而不是
  無從發現的技術債。
- **在整個 21 個節點的歷程中，從未需要任何新的 ADR**，
  跨角色變更請求也很罕見且規模很小（Phase 4 對 foundation
  migrate_test.go 過時斷言的請求，已在 Phase 5 開始前解決；
  沒有其他）。本角色在 21 個節點中做出的每一個真正的設計
  判斷——暫停紀錄的雙後端儲存缺口、`PauseStore` 的範圍界線
  （內部介面對比凍結 port）、`CompareAndSwapStatus` 這個
  原語、CheckResult 對比 error 的雙管道劃分、`SilenceErrors`，
  以及現在本節點 SQLiteStore 的範圍界線與崩潰模擬技巧——都
  能直接從已經凍結的文件（Constitution §§2/6/7、
  `CONTRACT_FREEZE.md`、相關的 ADD 章節）中得到解決，不需要
  升級處理，這是一個直接、有實際份量的結果，證明
  `agents/runtime.md` 與凍結合約，對於任何一個在任何一個
  波次中新接手本角色的代理人來說，包括這最後一個波次，都是
  真正足夠的原始材料。
- **本角色自己「檔案數量被低估」的觀察（最先在 Phase 5 被
  提出，Phase 7／9／10 再度確認）**——任何 Part-B 形狀、橫跨
  orchestrator 邏輯 + CLI 指令 + wiring 整合的節點，實際檔案
  數量大約是 DAG 天真估計值的 2 到 3 倍——本波次並沒有以
  同樣的形狀重現，因為 `runtime-b10` 刻意沒有新增任何新的
  CLI 指令介面（它證明的是既有的介面，再加上一個小型的新
  儲存層檔案）——值得註記的是，這是唯一一個那項特定估計模式
  不適用的波次，因為本節點實際的形狀（整合證明，而非新功能）
  與原始觀察所依據的每一個節點，在種類上就不同。

本節點完成了 `runtime` 完整的垂直切片 DAG 範圍。本角色
不再有任何指派中的節點。

---

# 最終整合關卡的修正性新增 —— `pause.Service`

分支：`day1/runtime`，在此工作開始前透過
`git fetch origin && git merge origin/main`（fast-forward，
乾淨）從 `main` 同步，帶入了 Phase 12（`qa` 的最後一個波次，
加上 `LICENSE` 檔案）。這**不是一個編號的 DAG 節點**——本角色
曾被指派的每一個 DAG 節點（`a01`-`a11`、`b01`-`b10`），
截至 Phase 11 的提交 `ef1c43d` 為止都已完成。這是一項來自
最終整合關卡審查（`contract-integrator-final`）、由 lead
發現的問題，被路由到本角色，因為它恰好就是本角色專屬路徑上的
工作，依 Constitution §4 用於一般 DAG 指派的同一個原則
（一個角色自己專屬的路徑，就是該角色自己合約中出現缺口時，
應該補上的地方，不論是哪一輪審查發現了它）。

## 發現了什麼缺失，以及為何先前沒有任何節點補上它

`internal/app/ports.go` 凍結了一個六方法的
`GracefulPauseService` 介面（`Observe`、`RequestPause`、
`ReachSafePoint`、`EnterSleep`、`Resume`、`Cancel`）。Part A
十一個節點中的每一個，都建置並詳盡測試了這個介面所描述之
暫停／恢復機制的一個真實部分——狀態轉換驗證器（`a02`）、
`Observer` 的抖動去彈跳／遲滯（`a03`）、`RequestPause` 的
冪等性（`a04`）、安全點協調器與 `PersistThenInterrupt` 的
排序（`a04`）、五階段的 `Persist` 協調器（`a05`）、持久
排程器（`a06`／`a07`）、resume 驗證（`a08`）、
`Wake`／`Cancel`／`Resume` 的比較後交換恰好一次紀律
（`a09`）、`InterruptAndSleep`（`a11`），以及一個真正的
`SQLiteStore`（`b10`）——但**從來沒有任何單一節點被指派把
這一切組裝成一個滿足凍結介面確切六方法形狀的具體型別。**
`runtime-b10` 自己的文件註解（上方 Phase 11 段落）明確點名
這是一個真實的缺口，並明確把它排除在範圍之外：「建置一個
六方法的介接器，把 `Observe`／`RequestPause`／
`ReachSafePoint`／`EnterSleep`／`Resume`／`Cancel` 橋接到
既有、形狀不同的自由函式上，是一項實質的新生產功能，而不是
重啟安全性的證明——明確不在本節點的範圍內。」那個被延後的
缺口，正是最終整合關卡審查所抓到的：在這項新增之前，
`grep -rn "var _ app.GracefulPauseService"` 對整個
repository 掃描，只找到
`internal/testutil/fakes.FakeGracefulPauseService`（一個
測試替身）以及一處內嵌在測試中的滿足——從來沒有一個真實的
生產實作。這也是為什麼 `cmd/auspex/main.go` 從未接上真正
服務的原因：對任何一個 port 而言，應用程式根節點都無法在
一個沒有具體實作的凍結 port 上做組合。

## 這項新增是什麼——僅止於組合與 DTO 形狀轉換

`internal/pause.Service`（`internal/pause/service.go`）是
真正、具體的型別。它沒有重新實作任何業務邏輯：每一次狀態
轉換、每一條抖動去彈跳／遲滯規則、`Persist` 五步驟排序的
每一個階段、每一項比較後交換的競態安全保證，以及
`ValidateResume` 四項檢查中的每一項，都是原封不動地透過
它們既有、已經測試過的實作來呼叫。`Service` 自己的工作是
(a) 依每個方法把對這些元件的呼叫排出正確的順序，以及
(b) 在凍結的 `app.*` DTO（刻意窄範圍——
`PauseRequest{SessionID, Reason}`、`SafePoint{PauseID,
At}`、`ResumeRequest{PauseID}`）與本套件自己較豐富的內部
形狀（`PauseKey{TaskID, SessionID}`、
`RequestPauseRequest`、`ResumeValidationRequest` 等）之間
做轉譯。

針對凍結的形狀做組合，浮現出兩個真實的缺口，是先前任何單一
節點都不可能找到的，因為在同一個地方滿足完整的六方法介面，
是本角色第一次需要同時用到每一個元件：

1. **`app.PauseRequest` 完全沒有攜帶 `TaskID`、
   `WorktreeID`、或暫停中工作的檔案集合**——只有
   `{SessionID, Reason}`。本套件自己的 `PauseKey` 同時需要
   `TaskID` 與 `SessionID`，而 `Persist` 額外還需要一個
   `WorktreeID`。一次橫跨整個 repo 的調查（直接調查，加上
   一個獨立背景研究代理人的交叉查核，呼應 `runtime-b10`
   自己的雙重驗證技巧）確認了整個系統中沒有任何凍結的 port
   能把一個 `SessionID` 解析成它目前的 `TaskID`／
   `WorktreeID`——`internal/cli/pause.go` 自己的文件註解也
   獨立點名了同一個缺口，只是針對它自己、形狀不同的 CLI
   旗標（「還沒有解析器 port」）。`Service` 以自己窄範圍、
   明確記錄的介面 `SessionContextResolver` 補上這一點，
   宣告在 `internal/pause` 中（而不是
   `internal/app/ports.go`——本角色不能單方面凍結一份新的
   跨元件合約），並留給未來一個能存取 `tasks`／
   `provider_sessions` 資料表（由其他角色的專屬路徑擁有）
   的接線節點去做真正的實作。這完全呼應
   `resumevalidation.go` 自己的
   `QuotaSnapshotReader`／`RepoFingerprintReader`／
   `SessionCapabilityReader` 先例。
2. **`PersistPauseStore`（`GetProgress`／`SaveProgress`）
   從未被對應到 `SQLiteStore` 上**——`persistphase_test.go`
   自己的 `seedPauseRecordRow` 文件註解早在 Phase 7 就明確
   點名了這個確切的缺口（「未來的整合節點會把
   `PersistPauseStore` 對應到一個架在同一張資料表之上、
   真正 SQLite 版的 `PauseStore`」）。這項新增補上了它：
   `SQLiteStore`（`internal/pause/sqlitestore.go`，本角色
   自己專屬的路徑）現在也實作了 `PersistPauseStore`，架在
   `pause_records` 自己的 `state_checkpoint_id`／
   `repository_checkpoint_id` 欄位之上（自遷移 0050 起就已
   存在），再加上 `metadata_json` 既有的自由格式 JSON 欄位
   （原本就用來存放 `TriggerReason`），把它放寬為一個小型、
   固定四個鍵的形狀，用來存放兩個布林階段標記與
   `WakeJobID` 純量。沒有新的遷移，沒有 schema 變更——
   `PersistProgress` 需要的每一個欄位，在 `Insert` 已經
   建立的資料列中都已經有地方可以存放。（這連帶需要一項
   相鄰的修正：`decodePauseMetadata` 原本針對單一鍵
   `{"reason":"X"}` 形狀做嚴格的前綴／後綴比對，一旦
   `SaveProgress` 第一次把某一列的 `metadata_json` 放寬，
   就會悄悄回傳 `""`——在這出貨之前就被抓到，
   `decodePauseMetadata` 現在使用與 `GetProgress` 相同的
   通用鍵值擷取器。）

同樣需要處理、且在本項新增自己的驗證過程中被 `go vet`
抓到（而不是被某個測試抓到）：`NewService` 的第一版草稿
是以值的方式接收 `Service` 本身，這會複製 `Service` 內嵌、
受 `sync.Mutex` 保護的 map——這正是 `go vet` 的
`copylocks` 檢查存在的目的，用來抓這種真實錯誤。修法是把
`Service` 單純資料的建構子輸入，拆成它自己的
`ServiceDeps` 值型別（沒有互斥鎖），並讓
`NewService(deps ServiceDeps) *Service` 從它建置出一個
全新、配置在堆積上的 `Service`——`Service` 本身在建構完成
之後絕不會被複製。

## 對應每一個凍結的方法

- **`Observe`**：`internal/predictor/runway.Scorer.Score`
  產生凍結簽章所回傳的 `domain.RunwayForecast`（ADD 把
  跑道預測器視為與 `Observer` 消費端的抖動去彈跳／遲滯
  分開的獨立關注點——見 `ports.go` 自己關於 `RiskCombiner`
  不消費 `RunwayForecast` 的套件註解）。由於凍結的
  `RuntimeObservation` 只攜帶目前這一筆 `QuotaObservation`
  樣本，從不攜帶歷史紀錄，而 `runway.Scorer` 刻意設計為
  無狀態（它自己的文件註解：「所有歷史紀錄都必須由呼叫端
  透過 `ScoreRequest.Previous` 傳入」），`Service` 自己
  追蹤每個 `(SessionID, LimitID)` 最近一次的樣本，並在
  第一次呼叫之後的每一次呼叫都把它當作 `Previous` 提供。
  產生的預測結果接著被送進 `Observer.Observe`，進行它
  帶副作用的抖動去彈跳／遲滯記錄；產生的 `ObserveDecision`
  （`Fire`／`Event`／`Reason`）刻意不對外呈現（凍結的簽章
  沒有為它保留欄位）——需要對一次觸發的觸發器採取行動的
  呼叫端，會另外呼叫 `RequestPause`，這對應了 ADD
  §20.2／§20.3 把 Observe 定位為一個持續的背景重新計算，
  與請求決策本身是不同的事。`Observe` 也會記住每個
  `SessionID`（不論 `LimitID`）最新的配額樣本，作為
  `RequestPause` 之後唯一可取得、供
  `ResumeValidationRequest.QuotaBaseline` 使用的來源——
  凍結的 `PauseRequest` 本身完全不攜帶任何配額樣本。
- **`RequestPause`**：透過 `SessionContextResolver`
  （見上方缺口 1）解析 `SessionID`，把凍結的純字串
  `Reason` 對應到本套件封閉的 `TriggerReason` 詞彙上，
  並委派給真正、未經修改的 `pause.RequestPause` 執行實際
  冪等的建立或回傳邏輯——經測試確認：以相同的
  `SessionID`／`Reason` 第二次呼叫，會回傳同一個
  `PauseID`，而不是重複建立。
- **`ReachSafePoint`**：把 `PersistThenInterrupt` 的排序
  保證（`safepoint.go`）與真正五階段的 `Persist`
  （`persistphase.go`）組合起來作為 `CheckpointPersister`
  的那一半，以及 `InterruptAndSleep`（`interrupt.go`，
  透過 `TurnInterrupterAdapter`）作為 `Interrupter`
  的那一半——所以 `Persist` 五項持久寫入必須全部成功，
  真正的 `app.TurnInterrupter` 才會被呼叫，完全依照
  ADD §20.15。也透過 `lifecycle.go` 已經確立的同一套
  `Apply` + `CompareAndSwapStatus` 紀律，驅動
  `Checkpointing` 之前的轉換
  （`Predicted`→`Requested`→`Quiescing`→
  `Checkpointing`），因為凍結的 `SafePoint{PauseID, At}`
  DTO 沒有給呼叫端任何其他掛鉤來驅動它們。經測試在正常
  路徑（紀錄抵達 `Sleeping`，每個 Persist 階段都持久
  記錄）與失敗路徑（某個 `Persist` 協作物件失敗，代表
  中斷器絕不會被呼叫，紀錄也絕不會超越失敗前的狀態）
  兩者上都確認過。
- **`EnterSleep`**：依凍結的狀態路徑，等到這個方法可被
  抵達時，`ReachSafePoint` 早已持久地排程了 wake job
  （`Persist` 自己的第 5 階段）並把紀錄驅動到 `Sleeping`
  （`InterruptAndSleep`）——所以 `EnterSleep` 的工作，
  比聽起來的還要窄：回報那個已經排程好的 `WakeJob`，
  透過 `scheduler.Store.GetByPauseKind` 查找（與
  `persistphase.go` 自己崩潰復原路徑所使用的同一個冪等
  復原讀取），而不是執行一次全新的轉換。若在紀錄真正
  抵達 `Sleeping` 之前就被呼叫，會以一個 `TransitionError`
  fail closed。
- **`Resume`**：在呼叫狀態機的 `Resume` **之前**，先執行
  真正的 `ValidateResume` 檢查清單（配額、repository、
  session、authorization），並透過
  `ResumeValidationResult.Verdict()` 對應結果——這正是
  `resumevalidation.go` 自己的文件註解自 Phase 8 起就點名為
  其記錄在案缺口的那項組合（「把一個真正的檢查接上去，明確是
  a08 的工作……**未來某個整合節點**」）。使用
  `RequestPause`／`Observe` 為這個 `PauseID` 所記住的
  `SessionContext` 與 `QuotaBaseline`。配額不安全的判定，
  還會另外透過 `RescheduleWakeJobOnQuotaUnsafe` 重新排程
  底層的 wake job。經測試在全部三種判定路徑上確認：完全
  有效（抵達 `Resumed`）、配額不安全（重新排程回
  `Sleeping`，非終止狀態）、以及 repository 重疊（在
  `BlockedConflict` 阻擋）。
- **`Cancel`**：最單純的對應——凍結的簽章是一個單純的
  `domain.PauseID`（沒有包裝的請求），被轉譯成
  `CancelRequest{PauseID}` 並委派給真正、未經修改的
  `pause.Cancel`。已透過真正的 `Service`（而不只是單純的
  函式）重新確認：取消依然能在對抗後續 `Resume` 呼叫的
  競態中勝出。

## 測試與驗證

`internal/pause/service_test.go` 新增了必要的
`var _ app.GracefulPauseService = (*pause.Service)(nil)`
編譯期斷言，加上針對六個方法各自的整合風格測試，重用
`fulllifecycle_test.go` 自己的技巧（透過 `openMigratedDB`／
`seedChain` 把真正的 `Service` 架在一個真實、已遷移的
SQLite 資料庫上驅動，並使用
`resumevalidation_test.go`／`fulllifecycle_test.go` 已經
建立的同一批
`okQuotaReader`／`okRepoFingerprintReader`／
`okSessionReader`／`okEvaluations`／`fakeTurnInterrupter`
替身），而不是從零開始。完整驗證，全部綠燈：

```text
gofmt -l internal/pause internal/scheduler         # clean
go build ./...                                     # clean
go vet ./internal/pause/...                         # clean (after the
                                                     #   ServiceDeps fix)
go test ./internal/pause/... -race -v               # all pass
go build ./... && go test ./... -race               # zero regressions,
                                                     #   whole repo
golangci-lint run ./...                             # 0 issues
```

## 明確排除在範圍之外的部分

`SessionContextResolver` 還沒有真正的實作——那需要
`tasks`／`provider_sessions` 資料表，由其他角色的專屬路徑
擁有，這正是 Constitution §7 規則 3 要求必須明確呈現、
而不是悄悄被假設掉的那種能力缺口。把一個真正的
`pause.Service` 接進 `cmd/auspex/main.go`／
`internal/app/wiring/**`——包括提供一個真正的
`SessionContextResolver`——是 lead 的根接線整合工作，
依本任務自身明確的指示，不屬於這項新增。
