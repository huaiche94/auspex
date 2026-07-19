# Claude Code plugin/hooks 串接（wiring）

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：**已上線（live）。** `auspex` 執行檔已提供全部七個
`auspex hook claude ...` 子指令（`user-prompt-submit`、
`post-tool-use`、`pre-compact`、`post-compact`、`stop`、
`stop-failure`、`statusline`），此串接已在真實工作階段中完整
端對端運作——本儲存庫自身的 Claude Code 工作階段就每天在使用它
（issue #12 dogfooding）。此處的檔案即是可直接複製到你自己 Claude
Code 設定中的參考設定。Hooks 採 fail-open：Auspex 端的錯誤絕不會
阻擋或卡住你的工作階段；可直接執行 `auspex evaluate` 來顯示真正
的錯誤。

工作階段會自我註冊：hooks 會在首次接觸時，以冪等（idempotent）
方式建立 repository／worktree／session 資料列（issue #17，決策
D-07「lazy bootstrap（延遲啟動）」），因此除了 hook 串接本身之外，
不需要其他必要的設定步驟。`auspex init` 也可用來明確註冊目前的
儲存庫。

（本文件最初是 `claude-provider-06` 在 vertical-slice 建置階段的
前瞻性 stub；以下的命名不一致紀錄之所以仍保留自那個時期，是因為
該問題至今仍未解決。）

## 檔案

- `plugin.json` — Claude Code plugin manifest，逐字取自
  `docs/design/Auspex_ADD.md` Appendix E.2（此角色文件記載的擁有範圍：
  「Appendix E.2/E.3」）。
- `hooks.json` — Claude Code hook 及狀態列（status-line）設定，將
  `UserPromptSubmit`、`PostToolUse`（issue #67 slice 3a，ADR-052；
  matcher 僅限會觸碰檔案的工具 `Read|Edit|Write|MultiEdit|NotebookEdit`）、
  `PreCompact`（issue #114，M4/M10：在 provider 壓縮（compact）context
  之前先擷取 State Checkpoint + repository checkpoint——即 ADD §22.4 的
  「`PreCompact` 一律 capture state checkpoint」；`timeout: 30` 是因為
  checkpoint 擷取包含 git 與 archive 寫入，且此 hook 採 fail-open——
  擷取失敗只會記錄在事件上，絕不會阻擋 compaction）、`Stop`、
  `StopFailure` 與狀態列串接至
  `auspex hook claude ...` 子指令，依據 `docs/design/Auspex_ADD.md` §22.3/
  §22.4/§22.5 及 Appendix E.3 的格式（`{"hooks": {"<HookEventName>":
  [{"hooks": [{"type": "command", "command": "..."}]}]}}`）。
  `auspex hook claude post-compact` 指令存在（ADD §22.3 有列出該事件），
  但刻意**不**在此註冊：現今的 Claude Code 並未提供 PostCompact hook
  事件——壓縮完成後只能透過 source 為 `compact` 的 `SessionStart`
  觀察到——註冊一個 provider 永遠不會觸發的事件將會誤報能力。

## CLI 子指令命名：一項已記錄的不一致

兩份權威文件對相同子指令使用了不同的大小寫慣例：

- `docs/design/Auspex_ADD.md` Appendix E.3（優先序 2，屬於
  `agents/claude-provider.md` 文件記載的擁有範圍）寫作
  `auspex hook claude UserPromptSubmit`——PascalCase，與 Claude Code
  自身 wire-level 的 `hook_event_name` 欄位完全一致。
- `agents/runtime.md` 的 P0 指令清單，以及
  `docs/implementation/vertical-slice/EXECUTION_DAG.md` 針對此節點
  （`claude-provider-06`）自身的驗證指令，兩者都寫作
  `auspex hook claude user-prompt-submit`——kebab-case。

本檔案採用 **DAG 的驗證指令與 `agents/runtime.md`** 的寫法
（kebab-case：`user-prompt-submit`、`stop`、`stop-failure`、
`statusline`），原因是：

1. 它是此確切節點目前凍結（frozen）的驗證指令原文，且
2. 它符合標準 Go CLI 子指令慣例（`cobra`/`urfave/cli` 風格）——而且
   這也是實際出貨的 CLI 真正實作的方式（`auspex hook claude --help`
   列出的正是 kebab-case 形式）。

這是一項**判斷取捨，而非正式裁定**——Constitution §2 的文件優先序，
若將兩者視為真正衝突，理應偏向 ADD（優先序 2）而非
`agents/runtime.md`（優先序 4）。此處已標記此問題，並記錄於此角色的
progress artifact 中，交由 `contract-integrator` 裁決——例如更新
`docs/design/Auspex_ADD.md` Appendix E.3 以配合 kebab-case 的 CLI 慣例，或更新
`agents/runtime.md`／DAG 以配合 ADD 的 PascalCase。此角色並無權限
編輯上述任一文件（`docs/design/Auspex_ADD.md` 與 `agents/runtime.md` 皆不在
`claude-provider` 的專屬路徑範圍內），且並未在未記錄衝突的情況下
逕自選擇其中一種寫法。

Claude Code 自身 wire-level 的 `hook_event_name` 欄位（位於透過 stdin
傳入的 JSON payload 內）不受此影響——它仍維持 PascalCase
（`UserPromptSubmit`、`Stop`、`StopFailure`），依循該 provider 自身的
慣例，以及 `testdata/provider-events/claude/**` 下每一份 fixture 的
寫法；有疑義的僅是 `auspex` CLI 自身 argv 子指令的拼寫方式。

## 與 claude-provider-02 的內部一致性

`UserPromptSubmit` 條目的 `timeout: 5`（秒）假設 hook wrapper 會呼叫
`internal/hooks/claude.ParseUserPromptSubmit`、
`internal/telemetry/claude.NormalizeUserPromptSubmit`
（claude-provider-04），以及（一旦串接完成後）一個 evaluation port，
並在發生任何內部錯誤時 fallback 至
`internal/hooks/claude.FallbackAllowResponse()`——絕不讓 Claude Code
的 `UserPromptSubmit` hook 卡住，或因 Auspex 端的錯誤而阻擋使用者的
提示詞（fail-open，依據 `CONTRACT_FREEZE.md` 針對操作性觀測失敗所定義
的 fail-open/fail-closed 區分，以及此角色 Phase-1 progress artifact 對
`claude-provider-02` 的假設）。wrapper 本身（讀取 stdin、呼叫這些函式，
並依照 `internal/hooks/claude/userpromptsubmit.go` 的
`EncodeUserPromptSubmitResponse` 所記載的方式寫出 wire response 的
程式碼）係作為 `runtime-b01`／Part B 的 CLI 管線工作交付，如今已隨
binary 出貨——此角色的交付項目僅止於它所呼叫的基礎元件（primitives）。

## 狀態列：`--emit-line`（issue #14；解決 issue #12 friction #2）

Claude Code 的 `statusLine` 指令原本應該 PRINT（印出）可見的狀態列
文字——但 `auspex hook claude statusline` 原本僅供擷取用（解析＋
正規化＋持久化，不輸出 stdout），因此直接串接會讓使用者的狀態列
變成空白（記錄為 issue #12 的 friction #2；dogfooding 安裝時以一個
tee-wrapper script 暫時繞過此問題）。`hooks.json` 的 `statusLine`
條目現在改用 `--emit-line`，在維持原本擷取行為完全不變的同時，
另外印出一行精簡的顯示文字（依 D-15、issue #41 的 v3 格式；原本
#14 的那一行帶有 token 區段，已撤回，待預測功能能夠回應提示詞後
再行加回，#42）：

```text
ax» <model> │ ◷ weekly ~<pct>% │ context [<bar>] <cur>% (p90 ≤<pct>%) │ ✓ RUN
```

若該工作階段（session）已有先前持久化的評估／預測結果可用，就使用
最新一筆；否則僅顯示 `ax» <model>`。未加上此旗標時，指令行為與先前
純擷取模式（無 stdout）完全位元組相同（byte-identical），以相容於
仍將 Auspex 與自身狀態列指令組合使用的安裝環境。成本為
`internal/pricing` 預設對照表（ADR-043）所給出的估計區間——是一項
尚未校準的估計值，絕非實際量測到的成本。

## 此處未建模的安裝程式行為

`docs/design/Auspex_ADD.md` §22.6（「Compose existing status line」）
描述了安裝程式應有的行為——讀取任何既有的狀態列指令、將其保存下來，
並將 Auspex 的 wrapper 輸出與其組合，而非直接覆蓋。`auspex init`
會註冊目前的儲存庫，但並未實作這項組合／合併步驟。此處的
`hooks.json` 直接將 `statusLine` 設為靜態範例，並未嘗試模擬該
組合／合併行為，因為靜態範例檔案本質上無法表達「讀取先前已存在的
內容」——那本質上是安裝時期（install-time）才會發生的邏輯。
