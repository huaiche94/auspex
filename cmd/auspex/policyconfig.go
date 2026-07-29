package main

import (
	"os"
	"path/filepath"

	"github.com/huaiche94/auspex/internal/config"
	"github.com/huaiche94/auspex/internal/paths"
)

// loadPolicyShadowConfig loads the ADD §26.1 file layers (global user
// config + repo config + repo local, resolved exactly like
// loadStateCheckpointingConfig — per-file duplicate by this repo's
// convention) and returns the typed `policy` section (issue #142,
// ADR-0057 §5). Every failure path fails open to the factory defaults:
// shadow enforcement OFF, i.e. the pre-#142 enforcement behavior — a
// broken config file must never silently disable real enforcement, and
// defaulting shadow ON from a failure would do exactly that.
func loadPolicyShadowConfig(dirs paths.Dirs) config.PolicySection {
	layers := []config.Layer{config.DefaultsLayer()}
	if dirs.Config != "" {
		if layer, err := config.LoadFile(config.SourceGlobalUser, filepath.Join(dirs.Config, "config.yaml")); err == nil {
			layers = append(layers, layer)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if layer, err := config.LoadFile(config.SourceRepoConfig, filepath.Join(cwd, ".auspex", "config.yaml")); err == nil {
			layers = append(layers, layer)
		}
		if layer, err := config.LoadFile(config.SourceRepoLocal, filepath.Join(cwd, ".auspex", "local.yaml")); err == nil {
			layers = append(layers, layer)
		}
	}
	cfg, err := config.Load(layers, config.Options{})
	if err != nil {
		return config.DefaultPolicySection()
	}
	section, err := cfg.PolicyConfigSection()
	if err != nil {
		return config.DefaultPolicySection()
	}
	return section
}

// loadBudgetConfig mirrors loadPolicyShadowConfig for the `budget`
// section (issue #141): fail-open to the factory default (no envelope
// declared) on every failure path.
func loadBudgetConfig(dirs paths.Dirs) config.BudgetSection {
	layers := []config.Layer{config.DefaultsLayer()}
	if dirs.Config != "" {
		if layer, err := config.LoadFile(config.SourceGlobalUser, filepath.Join(dirs.Config, "config.yaml")); err == nil {
			layers = append(layers, layer)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if layer, err := config.LoadFile(config.SourceRepoConfig, filepath.Join(cwd, ".auspex", "config.yaml")); err == nil {
			layers = append(layers, layer)
		}
		if layer, err := config.LoadFile(config.SourceRepoLocal, filepath.Join(cwd, ".auspex", "local.yaml")); err == nil {
			layers = append(layers, layer)
		}
	}
	cfg, err := config.Load(layers, config.Options{})
	if err != nil {
		return config.DefaultBudgetSection()
	}
	section, err := cfg.BudgetConfigSection()
	if err != nil {
		return config.DefaultBudgetSection()
	}
	return section
}
