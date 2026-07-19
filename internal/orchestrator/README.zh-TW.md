# internal/orchestrator/ — 將面向 provider 的輸入依序導入凍結服務

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

這是介於面向 provider 的輸入（CLI 旗標、經正規化的 hook 事件）與凍結的跨角色 port
（[`internal/app/ports.go`](../app/ports.go)）之間的一層——它本身不實作任何預測、政策、
checkpoint 或 pause 邏輯；只負責依序呼叫真正實作這些邏輯的服務（ADD §13——
[`docs/design/Auspex_ADD.md`](../../docs/design/Auspex_ADD.md)）。套件合約請見
[`doc.go`](doc.go)；請注意該文件描述的是最初的 Evaluate 管線節點，此後本套件已擴充出下列更多
介面。

進入點，每個指令家族各一個檔案（各自是一個 `Deps` 套件，加上回傳純結構（plain struct）供
[`internal/cli/`](../cli/README.md) 轉譯輸出的函式）：

- [`evaluate.go`](evaluate.go) —— `Evaluate`：resolve → load → snapshot → evaluate → decide 的管線。
- [`decision.go`](decision.go) —— `DecisionAllowCmd` ／ `DecisionDenyCmd`，接到真正的 evaluation 服務，並以儲存層保證的 exactly-once（恰好一次）授權消費機制。
- [`hooks.go`](hooks.go) —— 四個 Claude Code hook 處理器（`statusline`、`user-prompt-submit`、`stop`、`stop-failure`）與 `HookDeps`，並有文件記載的 nil-safe 降級行為。
- [`evaluateprompt.go`](evaluateprompt.go) ／ [`managedgate.go`](managedgate.go) —— `auspex evaluate`（fail-closed，失效阻擋）與 `auspex run` 的 pre-prompt 關卡（`EvaluateManagedPrompt`，由 [`internal/managed/`](../managed/README.md) 使用）；兩者共用 hook 路徑的 `evaluateSubmittedPrompt` 核心，而非各自重新實作。
- [`pauselifecycle.go`](pauselifecycle.go) —— `pause request` ／ `pause cancel` ／ `resume` ／ `scheduler run-once`（run-once 掃描只認領一個 job 就停止；實際執行是 daemon worker 的職責）。
- [`daemon.go`](daemon.go) —— `daemon run|status|stop|install|uninstall`，包含圍繞 [`internal/daemon/`](../daemon/README.md) 產生 `com.auspex.daemon` LaunchAgent plist 的邏輯。
- [`checkpoint.go`](checkpoint.go)、[`diagnostics.go`](diagnostics.go)（`status` ／ `doctor`）、[`gc.go`](gc.go)。
- [`sessionbootstrap.go`](sessionbootstrap.go) —— 在 hook 內以延遲（lazy）方式建立 repository／worktree／session 資料列（issue #17）；[`correlate.go`](correlate.go) —— 在 hook 持久化時，於語意明確的情況下填入 `Event.TaskID` ／ `Event.ProgressNodeID`（issue #1）；[`openturn.go`](openturn.go)、[`progresscomplete.go`](progresscomplete.go)。
- [`autocheckpoint.go`](autocheckpoint.go) —— `AutoCheckpointer`：ADR-0054 的 `CHECKPOINT_AND_RUN` 決策自動 pre-turn checkpoint（issue #116），由 UserPromptSubmit hook 與 managed runner 共用；契約上 fail-open，由 `state_checkpointing.on_checkpoint_and_run` 把關。

組裝發生在 [`internal/app/wiring/`](../app/wiring/README.md)；本套件只依賴介面型別，絕不依賴
具體的服務實作。
