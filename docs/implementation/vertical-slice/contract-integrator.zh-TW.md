# contract-integrator — Progress Artifact

> 🌐 [English](contract-integrator.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

以**啟動階段（Bootstrap stage）**執行（僅限主導者，不是 Phase 1 隊友任務 —
參見 `CONSTITUTION.md` 待批修訂案，以及專案負責人於 2026-07-12 針對 Phase 1
僵局所下達的指示）。

```yaml
node: bootstrap-01
status: completed
artifacts:
  - internal/domain/ids.go
  - internal/domain/measurement.go
  - internal/domain/status.go
  - internal/domain/status_test.go
  - internal/domain/failure.go
  - internal/domain/artifact.go
  - internal/domain/checkpoint.go
  - internal/domain/clock.go
  - internal/domain/errors.go
  - internal/domain/capability.go
  - internal/domain/usage.go
  - internal/app/ports.go
  - pkg/protocol/v1/event.go
  - pkg/protocol/v1/event_test.go
  - docs/implementation/vertical-slice/CONTRACT_FREEZE.md
  - go.mod
validation:
  - gofmt -l internal/domain internal/app pkg/protocol   # empty output
  - go build ./internal/domain/... ./internal/app/... ./pkg/protocol/...
  - go vet ./internal/domain/... ./internal/app/... ./pkg/protocol/...
  - go test ./internal/domain/... ./internal/app/... ./pkg/protocol/...
commit: 4262b4b
next_action: Commit Bootstrap, then spawn Phase 1 teammates (foundation, claude-provider, checkpoint, predictor) per repository owner's directive
assumptions:
  - CompleteNode's atomic transaction boundary is documented at the contract
    level (CONTRACT_FREEZE.md) but the actual state machine and transaction
    implementation belong to the checkpoint role, not this stage.
  - Request/response DTOs in internal/app/ports.go carry minimal fields
    sufficient to compile; owning roles may request additions through their
    own progress artifact rather than editing ports.go themselves.
  - Go toolchain upgraded 1.19.1 -> 1.26.5 via Homebrew (approved by
    repository owner) as an environment prerequisite, not a Bootstrap task.
blockers: []
```

## Stage 5: contract-integrator-final (Final integration gate)

以**最後一個 DAG 節點**執行（僅限主導者，依 `EXECUTION_DAG.md` 本身條目：
`deps: qa-09 | Stage 5 | Risk: High — last chance to catch cross-role
contradictions | Cannot start until qa's final report exists`），範圍依
[議題 #2](https://github.com/huaiche94/auspex/issues/2) 界定。

### 1. 完整的 `go test ./... -race` 掃描

橫跨全部 37 個套件均為綠燈（Green），隨著本階段每次修正性新增落地，已重複
多次確認。沒有不穩定（flaky）或被跳過的測試。整合提交的順序請見
`git log --oneline`，其中每一筆提交在合併前都各自獨立重新執行過完整測試
套件。

### 2. 跨角色矛盾審查 — 本次審查的核心發現

每個角色的個別工作在其落地當下（Phase 1-12）都各自獨立驗證過，且每一波
（phase）的全倉庫 `go test ./... -race` 皆通過。但**每波組合測試通過，
不等於實際二進位檔已真正接上真實服務** — 本階段自身的風險註記（「六個
角色各自正確，不代表組合起來就是正確的」）精準預言了後來確實發生的那一
類落差：

`cmd/auspex/main.go` 當時仍是**foundation-01 最初的樁（stub）**——只接上
了 `auspex version`。調查發現，五個凍結的 `app.*` 服務介面中有三個
（`ProgressTreeService`、`GracefulPauseService`），加上 `internal/evaluation`
自己內部的 `DataSource` 介面銜接點，在整個程式碼庫中**完全沒有真正組裝
完成的正式實作**——以 `grep -rn "var _ app.<X>Service"` 確認，只找得到
`internal/testutil/fakes` 的替身（doubles）和測試專屬的滿足實作。每一個
個別的環節（狀態機、原子性、冪等性、當機復原、安全控制）都是真實且經深
度測試的；但最後的組裝步驟——把這些環節組合成凍結介面的精確形狀，再把
`cmd/auspex/main.go` 接上去建構並使用它們——從來不是 DAG 中編號的任務，
因而在逐波（phase-by-phase）流程中被遺漏。

透過三項修正性新增結案（每一項都轉交給對應的負責角色處理，從未由主導者
直接實作，依 Constitution §7）：
- `internal/progress.Service`（checkpoint）— 將 NodeStore/EdgeStore/
  ArtifactStore/CompleteNode/Reconciler 組合成 `app.ProgressTreeService`。
- `internal/pause.Service`（runtime）— 將完整的暫停生命週期機制組合成
  `app.GracefulPauseService`；過程中透過 `go vet` 發現並修正了一個真實
  的 `sync.Mutex` 複製（copy）錯誤。
- `internal/evaluation.SQLDataSource`（predictor）— 一個真實、以儲存體
  為後盾的 `DataSource`，可跨 foundation/claude-provider/checkpoint 查詢
  資料表（唯讀）；9 個方法中有 7 個為真實實作，另外 2 個誠實地僅提供
  冷啟動（cold-start）結果，因為凍結的 schema 在這兩處並未攜帶可支撐的
  訊號。

接著由主導者直接接上 `cmd/auspex/main.go`（這是本階段自身保留的工作，
依 `agents/*.md` 以及 `internal/app/wiring` 自己的文件註解：「根層級的
接線（root wiring）不是本套件的職責……由 contract-integrator／foundation
角色負責把這個容器組裝進二進位檔」）——包括 `cmd/auspex/wire.go`（組裝
根）以及 `cmd/auspex/adapters.go`（各負責套件都各自記載為「留給未來接線
節點處理」的小型 DTO 形狀轉換銜接點）。剩下兩處銜接點——受管理供應商中
斷（managed provider interrupt）與受管理工作階段回復（managed session
resume）——皆接上「失敗即關閉」（fail-closed）的樁，而非捏造出的真實行
為，因為這兩者都是明確且多次記載過、本次垂直切片中從未建置的延伸目標
（claude-provider 與 runtime 自身的角色文件都逐字這麼寫）。

手動對編譯完成的二進位檔進行了 smoke test：`version`、`doctor`（真實的資
料庫連線、真實的 migration 數量）、`status`／`pause request`（真實的驗
證錯誤），以及一個真實的 SQLite 外鍵（foreign-key）限制正確地拒絕了針對
不存在的 task/session 資料列所發出的暫停請求——證明真實的後端儲存體確實
是端到端強制生效，而不是被靜默接受。

### 3. 競爭條件／安全性複審

除了先前各波自身的安全掃描已經找出並修正的問題之外，未發現新的問題。以
下彙整整個建置過程中發現並修正的每一個真實錯誤（皆由主導者在整合前獨立
驗證過，並非自我回報）：
- foundation-07：核心 SQLite 引擎中的一個 TOCTOU 競爭（race）以及一個
  `SQLITE_BUSY` 啟動期競爭。
- checkpoint-b09：Repository Checkpoint 的 `Verify` 中存在路徑穿越
  （path-traversal）漏洞（遭竄改的 manifest 可能使其讀取檢查點目錄以外
  的任意檔案）。
- runtime-a09：暫停 `Cancel`／`Resume` 中的一個 TOCTOU 競爭（已透過
  compare-and-swap 修正）。
- predictor-10：一個授權（authorization）提示詞綁定（prompt-binding）
  繞過漏洞（該檢查原本是以*呼叫端請求*是否提供了 prompt hash 為依據，
  而不是以*授權資料列*本身是否綁定了 prompt hash 為依據）。
- runtime（本階段）：在建構 `GracefulPauseService` 轉接器（adapter）時，
  由 `go vet` 抓到的一個 `sync.Mutex` 複製錯誤。

### 4. `CONTRACT_FREEZE.md` 修訂稽核

`git log --oneline -- internal/app/ports.go internal/domain/
pkg/protocol/v1/` 顯示整個專案歷史中恰好只有兩筆提交：最初的 Bootstrap
凍結，以及 ADR-041（Token/Quota Forecast 層的插入）——沒有任何其他提交
曾經動過凍結契約的檔案。ADR-041 已在
`docs/adr/0041-predictor-forecast-layer.md` 中完整記載，並反映在本檔案
自身的「Predictor pipeline ports (ADR-041)」一節中。不存在任何未記載的
修訂；凍結契約層在整個建置期間都維持不變。

### 5. 已知且刻意保持未結案的項目（不構成本關卡的阻擋因素）

- [議題 #1](https://github.com/huaiche94/auspex/issues/1)（P1，
  qa-04/qa-09）：沒有任何正式環境轉接器（adapter）把已持久化的
  claude-provider 事件接到 Progress Tree 節點完成狀態——這與本階段自身
  的發現是真正不同的落差（是一個*根本還不存在*的新轉接器，而不是一個
  *尚未組裝*的既有轉接器），正確地保留給未來的 phase 處理。
- 另有 14 項待辦（backlog）議題（#3-#16），涵蓋安全性後續追蹤、需要 ADR
  的建議事項，以及切片完成後的路線圖里程碑（M6-M13）——皆明確不在本次
  垂直切片範圍內，已歸檔供未來規劃使用。

```yaml
node: contract-integrator-final
status: completed
artifacts:
  - internal/progress/service.go (checkpoint, routed corrective addition)
  - internal/progress/task_store.go (checkpoint, routed corrective addition)
  - internal/pause/service.go (runtime, routed corrective addition)
  - internal/pause/sqlitestore.go (runtime, extended — PersistPauseStore)
  - internal/evaluation/datasource_sql.go (predictor, routed corrective addition)
  - cmd/auspex/main.go (lead, root wiring)
  - cmd/auspex/wire.go (lead, root wiring)
  - cmd/auspex/adapters.go (lead, root wiring)
  - docs/implementation/vertical-slice/contract-integrator.md (this section)
validation:
  - go build ./...                        # clean
  - go vet ./...                          # clean
  - gofmt -l . (excluding testdata/)      # empty
  - go test ./... -race                  # all 37 packages pass
  - golangci-lint run ./...              # 0 issues
  - manual smoke test of the compiled binary (version/doctor/status/pause request)
commit: 3b6cfcb
next_action: Retire the six vertical-slice/* branches and auspex-* worktrees (all merged); update README to slice-complete status
assumptions:
  - The three "real service assembly" gaps found during this stage are a
    process gap (no DAG node ever explicitly covered "assemble the frozen
    ports into main.go"), not a defect in any individual role's own work —
    every underlying piece was already correct and tested.
  - Managed provider interrupt/resume remain intentional stretch-scope
    stubs, not gaps, per claude-provider's and runtime's own role docs.
blockers: []
```
