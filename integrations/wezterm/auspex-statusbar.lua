-- auspex-statusbar.lua — render `auspex hook codex status` in WezTerm's status
-- bar, so a Codex (or Claude Code) session's quota / runway / spend / context
-- line rides along at the edge of the window WITHOUT tmux.
--
-- Why this exists: Codex CLI draws its own footer and exposes no third-party
-- injection point (no statusline hook; its `/statusline` toggles built-in
-- items only). `auspex hook codex status` is instead a stdin-less, DB-backed,
-- fail-open render that ANY periodic poller can call — and WezTerm's
-- `update-status` event is exactly such a poller. See README.md alongside this
-- file for the quick start and the full rationale.
--
-- Usage (in your wezterm.lua):
--     local auspex = require("auspex-statusbar")
--     auspex.apply(config)                                    -- defaults
--     auspex.apply(config, { wsl_distro = "Ubuntu-24.04" })   -- override
--
-- Cross-platform:
--   • macOS / Linux — calls the local `auspex` binary directly.
--   • Windows       — WezTerm runs on the Windows host but codex/auspex live
--                     in WSL, so it crosses the boundary via `wsl.exe`.

local wezterm = require("wezterm")

local M = {}

local function is_windows()
  return wezterm.target_triple:find("windows") ~= nil
end

-- Strip ANSI escape sequences: set_right_status does not interpret them, and
-- `auspex` always emits color (it ignores NO_COLOR / TERM=dumb).
local function strip_ansi(s)
  s = s:gsub("\27%[[%d;]*%a", "") -- CSI sequences: ESC [ ... final letter
  s = s:gsub("\27", "") -- any stray ESC
  return s
end

-- Foreground process basename: normalize separators, drop the path, strip a
-- trailing `.exe`. Returns nil when WezTerm cannot resolve it.
local function foreground_basename(pane)
  local ok, name = pcall(function()
    return pane:get_foreground_process_name()
  end)
  if not ok or name == nil or name == "" then
    return nil
  end
  return name:gsub("[\\/]", "/"):gsub("^.*/", ""):gsub("%.exe$", "")
end

-- pane cwd — newer WezTerm returns a Url object (.file_path), older a string.
local function pane_cwd(pane)
  local ok, cwd = pcall(function()
    return pane:get_current_working_dir()
  end)
  if not ok or cwd == nil then
    return nil
  end
  if type(cwd) == "userdata" then
    return cwd.file_path -- Url object
  end
  local p = tostring(cwd):gsub("^file://[^/]*", "") -- file://host/abs/path
  if p == "" then
    return nil
  end
  return p
end

-- apply wires the status bar into `config`. Options (all optional):
--   auspex_bin      mac/linux path to the binary (default ~/.local/bin/auspex)
--   wsl_distro      Windows: WSL distro to target (default: WSL's default)
--   wsl_cmd         Windows: command run inside WSL's login shell
--                   (default "auspex hook codex status")
--   interval_ms     poll cadence (default 2000 mac/linux, 4000 Windows)
--   only_when_codex show only when the pane's foreground is codex
--                   (default true on mac/linux, false on Windows where
--                   WSL-boundary process detection is unreliable)
--   colors          { block=, warn=, ok=, idle= } hex overrides
function M.apply(config, opts)
  opts = opts or {}
  local win = is_windows()

  local auspex_bin = opts.auspex_bin or (wezterm.home_dir .. "/.local/bin/auspex")
  local wsl_distro = opts.wsl_distro -- nil = WSL default distro
  local wsl_cmd = opts.wsl_cmd or "auspex hook codex status"
  local only_when_codex = opts.only_when_codex
  if only_when_codex == nil then
    only_when_codex = not win
  end
  local colors = opts.colors or {}
  local c_block = colors.block or "#eb6f92"
  local c_warn = colors.warn or "#f6c177"
  local c_ok = colors.ok or "#9ccfd8"
  local c_idle = colors.idle or "#908caa"

  config.status_update_interval = opts.interval_ms or (win and 4000 or 2000)

  local function verdict_color(line)
    if line:find("BLOCK") then
      return c_block
    elseif line:find("WARN") then
      return c_warn
    elseif line:find("OK") then
      return c_ok
    end
    return c_idle
  end

  -- Build the argv: Windows crosses into WSL via wsl.exe; elsewhere call the
  -- local binary directly.
  local function status_argv(cwd)
    if win then
      local argv = { "wsl.exe" }
      if wsl_distro then
        table.insert(argv, "-d")
        table.insert(argv, wsl_distro)
      end
      -- Login shell so ~/.local/bin lands on PATH; no --cwd on Windows (WSL
      -- path translation is fiddly, and auspex falls back to the latest codex
      -- session when cwd is omitted).
      table.insert(argv, "--")
      table.insert(argv, "bash")
      table.insert(argv, "-lc")
      table.insert(argv, wsl_cmd)
      return argv
    end
    local argv = { auspex_bin, "hook", "codex", "status" }
    if cwd then
      table.insert(argv, "--cwd")
      table.insert(argv, cwd)
    end
    return argv
  end

  wezterm.on("update-status", function(window, pane)
    if pane == nil then
      window:set_right_status("")
      return
    end
    if only_when_codex then
      local base = foreground_basename(pane)
      if base == nil or base:find("^codex") == nil then
        window:set_right_status("")
        return
      end
    end

    -- run_child_process throws a Lua error if the executable is missing (e.g.
    -- auspex not installed, or no wsl.exe) → pcall it; any failure is silent.
    local ran, ok, stdout = pcall(wezterm.run_child_process, status_argv(pane_cwd(pane)))
    if not ran or not ok or stdout == nil or stdout == "" then
      window:set_right_status("")
      return
    end

    local line = strip_ansi(stdout):gsub("%s+$", "")
    window:set_right_status(wezterm.format({
      { Foreground = { Color = verdict_color(line) } },
      { Text = line .. "  " },
    }))
  end)
end

return M
