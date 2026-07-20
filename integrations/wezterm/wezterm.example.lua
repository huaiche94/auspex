-- Minimal WezTerm config that shows the Auspex status line for Codex / Claude
-- Code sessions in the bottom-right of the window (no tmux).
--
-- Quick start:
--   1. Copy this file to ~/.config/wezterm/wezterm.lua  (or ~/.wezterm.lua)
--   2. Copy auspex-statusbar.lua into the SAME directory
--   3. Restart WezTerm, run `codex` (or Claude Code), look bottom-right
--
-- Already have a wezterm.lua? Don't overwrite it — just add the two `auspex`
-- lines below to your own config instead. See README.md for details.

local wezterm = require("wezterm")
local config = wezterm.config_builder()

-- Make `require` resolve auspex-statusbar.lua from THIS file's directory,
-- so it works no matter how WezTerm loads the config.
package.path = (wezterm.config_dir or ".") .. "/?.lua;" .. package.path

local auspex = require("auspex-statusbar")
auspex.apply(config)

-- Windows + WSL on a non-default distro? Pass it explicitly:
--   auspex.apply(config, { wsl_distro = "Ubuntu-24.04" })
-- auspex not on your WSL login PATH? Give the full command:
--   auspex.apply(config, { wsl_cmd = "/home/you/.local/bin/auspex hook codex status" })

return config
