# integrations/ — provider 整合設定

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

隨產品附帶的設定範例，用來將某個 AI 程式輔助代理人（AI coding agent）
自身的擴充點——或它所執行的終端機——接上 `auspex` 執行檔。每個整合各
自對應一個子目錄：

- [`claude/`](claude/README.zh-TW.md) —— Claude Code 的 hook 與 plugin
  接線設定（`hooks.json`、`plugin.json`）：將 UserPromptSubmit／Stop／
  StopFailure／statusline 事件透過 `auspex hook claude <event>` 導向。
  其 README 記載了檔案結構、一項已記錄的 CLI 子指令命名差異，以及
  `--emit-line` 狀態列（status-line）行為。
- [`codex/`](codex/hooks.json) —— Codex CLI 的 hook 接線設定
  （`hooks.json`）：將 SessionStart／UserPromptSubmit／Stop 事件透過
  `auspex hook codex <event>` 導向（hook 的 argv 採 kebab-case，
  ADR-050）。
- [`wezterm/`](wezterm/README.zh-TW.md) —— WezTerm 狀態列做法：一個自足
  的 Lua 模組，把 `auspex hook codex status` 顯示在 WezTerm 自己的狀態
  列（macOS/Linux 與 Windows+WSL），不經 tmux。因為 Codex CLI 的 footer
  沒有注入點，這是在不用 multiplexer 前提下最接近原生 footer 的做法。

根目錄的 [`README.md`](../README.md)「Quick start」章節會指向此處，說
明如何將 Auspex 接上 Claude Code。這些檔案在 Go 端對應的實作是
`internal/hooks/claude` 與 `internal/telemetry/claude`；這些套件測試
所用的原始 payload fixture，則位於 [`../testdata/`](../testdata/README.md)
的 `provider-events/claude/`。未來若有新的 provider adapter（例如
Codex、M7/M8，issue #9），會在此處新增一個對應的子目錄來放置其隨附設
定。
