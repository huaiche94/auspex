# WezTerm status bar

> 🌐 English | [繁體中文](README.zh-TW.md)

Show the Auspex status line — worst quota window, runway to the wall,
today's spend and pace, context fill — in **WezTerm's own status bar**,
with **no tmux**. Works on macOS, Linux, and Windows (with Codex running
in WSL).

```text
ax» gpt-5.6-sol │ ◷ 5h ~62% (resets 18:00) │ ⏳ runway ~38m │ today $12.40 · pace → ~$62 by 24:00 │ context [████··] 21.9% │ ✓ RUN
```

## Why this exists

Codex CLI draws its own footer and exposes **no third-party injection
point** — it has no statusline hook, and its `/statusline` command only
toggles built-in items (git branch, context window, …). So there is no
way to render Auspex *inside* Codex's footer.

Instead, [`auspex hook codex status`](../../README.md#the-command-tree)
is a stdin-less, DB-backed, fail-open command that prints one status
line for the latest Codex session — designed for any periodic poller.
WezTerm's [`update-status`](https://wezterm.org/config/lua/window-events/update-status.html)
event is exactly that: it lets WezTerm run a command every couple of
seconds and paint the result at the edge of the window. This directory
wires the two together.

This renders in **WezTerm's** status bar, not Codex's in-app footer —
the closest you can get to a native footer without tmux.

## Requirements

- **WezTerm** (any recent build; developed against `20240203-110809`).
- **`auspex`** installed and on the machine where your agent runs
  (`~/.local/bin/auspex` by default), with the Codex or Claude Code
  hooks wired — see [`integrations/codex/`](../codex/hooks.json) /
  [`integrations/claude/`](../claude/README.md). Without recorded
  sessions the line degrades to a bare `ax»` (fail-open, never an error).

## Quick start — macOS / Linux

1. Copy both files into your WezTerm config directory
   (`~/.config/wezterm/`):

   ```bash
   mkdir -p ~/.config/wezterm
   cp integrations/wezterm/auspex-statusbar.lua ~/.config/wezterm/
   # If you have NO wezterm.lua yet, use the bundled example:
   cp integrations/wezterm/wezterm.example.lua ~/.config/wezterm/wezterm.lua
   ```

2. **Already have a `wezterm.lua`?** Don't overwrite it — add two lines:

   ```lua
   local auspex = require("auspex-statusbar")
   auspex.apply(config)
   ```

3. Restart WezTerm (or `Ctrl+Shift+R` to reload), run `codex`, and look
   at the bottom-right. The line shows only while a pane is running
   `codex` and clears when you leave it.

## Quick start — Windows + WSL

WezTerm runs on the **Windows host**, but Codex and `auspex` live inside
**WSL**. The `update-status` callback therefore has to cross into WSL via
`wsl.exe` — the module does this automatically when it detects Windows.

1. In WSL: install `auspex` (`~/.local/bin/auspex`) and confirm it is on
   your **login-shell** PATH:

   ```bash
   bash -lc 'auspex hook codex status'   # should print the ax» line
   ```

2. On Windows: copy `auspex-statusbar.lua` and `wezterm.example.lua`
   into `%USERPROFILE%\.config\wezterm\` (rename the example to
   `wezterm.lua`), or add the two `auspex` lines to your existing config.

3. Run `codex` inside a WSL pane and look at the bottom-right.

If Codex runs in a **non-default** distro, or `auspex` is not on the
login PATH, pass the details in:

```lua
auspex.apply(config, {
  wsl_distro = "Ubuntu-24.04",                            -- non-default distro
  wsl_cmd    = "/home/you/.local/bin/auspex hook codex status", -- full path
})
```

> On Windows the "only show while codex is foreground" gate is **off** by
> default, because WezTerm's process detection across the WSL boundary is
> unreliable — the line shows in every WSL pane and degrades to `ax»`
> when there is no Codex session. If your WezTerm *does* resolve WSL
> foreground processes, re-enable the gate with
> `auspex.apply(config, { only_when_codex = true })`.

## Options

`auspex.apply(config, opts)` — every field is optional:

| Option | Default | Purpose |
| --- | --- | --- |
| `auspex_bin` | `~/.local/bin/auspex` | macOS/Linux path to the binary |
| `wsl_distro` | WSL default | Windows: which distro to target |
| `wsl_cmd` | `auspex hook codex status` | Windows: command run in WSL's login shell |
| `interval_ms` | `2000` (mac/linux) / `4000` (Windows) | poll cadence |
| `only_when_codex` | `true` mac/linux, `false` Windows | gate on the pane's foreground process |
| `colors` | rose-pine-ish | `{ block=, warn=, ok=, idle= }` hex overrides; the verdict word drives the color |

## Troubleshooting

- **Bottom-right is blank.** Run the exact command WezTerm runs and check
  it prints a line: macOS/Linux `~/.local/bin/auspex hook codex status`;
  Windows (from cmd/PowerShell) `wsl.exe -- bash -lc "auspex hook codex status"`.
  A `command not found` means `auspex` is not on the login PATH — set
  `wsl_cmd` (or `auspex_bin`) to the full path.
- **Lua errors.** Open WezTerm's debug overlay (`Ctrl+Shift+L`) to see
  them.
- **Only `ax»` shows.** No Codex/Claude session is recorded yet — wire
  the provider hooks and run a turn.

## Files

- `auspex-statusbar.lua` — the reusable module (`require` + `apply`).
- `wezterm.example.lua` — a minimal standalone config for people with no
  `wezterm.lua` yet.
