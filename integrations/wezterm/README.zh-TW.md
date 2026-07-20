# WezTerm 狀態列

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

把 Auspex 狀態列——最吃緊的配額視窗、距牆的剩餘跑道（runway）、今日
花費與步調（pace）、context 用量——顯示在 **WezTerm 自己的狀態列**，
且**完全不經 tmux**。支援 macOS、Linux，以及 Windows（Codex 跑在 WSL
內）。

```text
ax» gpt-5.6-sol │ ◷ 5h ~62% (resets 18:00) │ ⏳ runway ~38m │ today $12.40 · pace → ~$62 by 24:00 │ context [████··] 21.9% │ ✓ RUN
```

## 為什麼需要這個

Codex CLI 的 footer 是它自己畫的，**不開放任何第三方注入點**——沒有
statusline hook，`/statusline` 也只能在內建項目（git branch、context
window……）之間開關。所以無法把 Auspex 塞進 Codex 的 footer *裡面*。

改用 [`auspex hook codex status`](../../README.zh-TW.md)：這是一個不吃
stdin、直接讀 DB、fail-open 的命令，會印出最近一個 Codex session 的一
行狀態——專為任何週期性輪詢者設計。WezTerm 的
[`update-status`](https://wezterm.org/config/lua/window-events/update-status.html)
事件正是如此：它讓 WezTerm 每隔幾秒跑一個命令，並把結果畫在視窗邊
緣。這個目錄就是把兩者接起來。

這是畫在 **WezTerm** 的狀態列，不是 Codex 應用內的 footer——在不用
tmux 的前提下，這是最接近原生 footer 的做法。

## 需求

- **WezTerm**（任何近期版本；開發時對照 `20240203-110809`）。
- **`auspex`** 已安裝，且位於你的 agent 實際執行的機器上（預設
  `~/.local/bin/auspex`），並已接上 Codex 或 Claude Code 的 hook——見
  [`integrations/codex/`](../codex/hooks.json) /
  [`integrations/claude/`](../claude/README.zh-TW.md)。若尚無已記錄的
  session，該行會退成一個裸的 `ax»`（fail-open，絕不報錯）。

## Quick start —— macOS / Linux

1. 把兩個檔案複製進你的 WezTerm 設定目錄（`~/.config/wezterm/`）：

   ```bash
   mkdir -p ~/.config/wezterm
   cp integrations/wezterm/auspex-statusbar.lua ~/.config/wezterm/
   # 若你還沒有 wezterm.lua，直接用附帶的範例：
   cp integrations/wezterm/wezterm.example.lua ~/.config/wezterm/wezterm.lua
   ```

2. **已經有 `wezterm.lua`？** 別覆蓋它——加兩行就好：

   ```lua
   local auspex = require("auspex-statusbar")
   auspex.apply(config)
   ```

3. 重啟 WezTerm（或 `Ctrl+Shift+R` 重載），跑 `codex`，看視窗右下角。
   該行只有在某個 pane 正在跑 `codex` 時才顯示，離開就自動收起。

## Quick start —— Windows + WSL

WezTerm 跑在 **Windows 端**，但 Codex 和 `auspex` 在 **WSL** 內。因此
`update-status` callback 必須透過 `wsl.exe` 跨進 WSL——模組偵測到
Windows 時會自動這麼做。

1. 在 WSL 內：安裝 `auspex`（`~/.local/bin/auspex`），並確認它在你的
   **登入 shell** PATH 上：

   ```bash
   bash -lc 'auspex hook codex status'   # 應印出 ax» 那行
   ```

2. 在 Windows 端：把 `auspex-statusbar.lua` 與 `wezterm.example.lua`
   複製進 `%USERPROFILE%\.config\wezterm\`（把範例改名為
   `wezterm.lua`），或把那兩行 `auspex` 加進你現有的設定。

3. 在 WSL pane 內跑 `codex`，看右下角。

若 Codex 跑在**非預設** distro，或 `auspex` 不在登入 PATH 上，就把細節
傳進去：

```lua
auspex.apply(config, {
  wsl_distro = "Ubuntu-24.04",                            -- 非預設 distro
  wsl_cmd    = "/home/you/.local/bin/auspex hook codex status", -- 全路徑
})
```

> 在 Windows 上，「只在前景是 codex 時顯示」這個判斷**預設關閉**，因為
> WezTerm 跨 WSL 邊界的前景偵測不可靠——該行會出現在每個 WSL pane，且
> 沒有 Codex session 時退成 `ax»`。若你的 WezTerm *能*解析 WSL 前景程
> 式，可用 `auspex.apply(config, { only_when_codex = true })` 把判斷開
> 回來。

## 選項

`auspex.apply(config, opts)`——每個欄位皆可省略：

| 選項 | 預設 | 用途 |
| --- | --- | --- |
| `auspex_bin` | `~/.local/bin/auspex` | macOS/Linux 的 binary 路徑 |
| `wsl_distro` | WSL 預設 | Windows：要指向哪個 distro |
| `wsl_cmd` | `auspex hook codex status` | Windows：在 WSL 登入 shell 內執行的命令 |
| `interval_ms` | `2000`（mac/linux）/ `4000`（Windows） | 輪詢頻率 |
| `only_when_codex` | mac/linux `true`、Windows `false` | 是否依 pane 前景程式判斷 |
| `colors` | rose-pine 風 | `{ block=, warn=, ok=, idle= }` hex 覆寫；由 verdict 關鍵字決定顏色 |

## 疑難排解

- **右下角空白。** 手動跑 WezTerm 會跑的那個命令，確認它印得出一行：
  macOS/Linux `~/.local/bin/auspex hook codex status`；Windows（在
  cmd/PowerShell）`wsl.exe -- bash -lc "auspex hook codex status"`。若出
  現 `command not found`，代表 `auspex` 不在登入 PATH——把 `wsl_cmd`
  （或 `auspex_bin`）設成全路徑。
- **Lua 錯誤。** 開 WezTerm 的 debug overlay（`Ctrl+Shift+L`）查看。
- **只顯示 `ax»`。** 尚無已記錄的 Codex/Claude session——接上 provider
  hook 並跑一個回合。

## 檔案

- `auspex-statusbar.lua` —— 可重用的模組（`require` + `apply`）。
- `wezterm.example.lua` —— 給還沒有 `wezterm.lua` 的人的最小獨立設定。
