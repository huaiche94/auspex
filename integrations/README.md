# integrations/ — provider integration configuration

> 🌐 English | [繁體中文](README.zh-TW.md)

Shipped configuration examples that wire an AI coding agent's own
extension points — or the terminal it runs in — to the `auspex` binary.
One subdirectory per integration:

- [`claude/`](claude/README.md) — Claude Code hook and plugin wiring
  (`hooks.json`, `plugin.json`): routes UserPromptSubmit / Stop /
  StopFailure / statusline events through `auspex hook claude
  <event>`. Its README documents the file shapes, a recorded CLI
  subcommand-naming discrepancy, and the `--emit-line` status-line
  behavior.
- [`codex/`](codex/hooks.json) — Codex CLI hook wiring (`hooks.json`):
  routes SessionStart / UserPromptSubmit / Stop events through
  `auspex hook codex <event>` (hook argv is kebab-case, ADR-050).
- [`wezterm/`](wezterm/README.md) — WezTerm status-bar recipe: a
  self-contained Lua module that renders `auspex hook codex status` in
  WezTerm's own status bar (macOS/Linux and Windows+WSL), no tmux.
  Because Codex CLI's footer has no injection point, this is the
  closest to a native footer without a multiplexer.

The root [`README.md`](../README.md) Quick start points here for
wiring Auspex into Claude Code. The Go-side counterparts of these
files are `internal/hooks/claude` and `internal/telemetry/claude`;
the raw payload fixtures those packages test against live under
[`../testdata/`](../testdata/README.md) `provider-events/claude/`.
A future provider adapter (e.g. Codex, M7/M8, issue #9) would add a
sibling directory here for its shipped configuration.
