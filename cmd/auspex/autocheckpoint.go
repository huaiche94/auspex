// autocheckpoint.go composes the ADR-0054 automatic pre-turn checkpoint
// (issue #116) for the binary: load the layered YAML configuration chain
// (the FIRST production Config.Raw consumer — closing the "no production
// consumer of internal/config exists yet" gap wire.go recorded), read the
// `state_checkpointing.on_checkpoint_and_run` gate, and — when enabled
// (the default) — build the orchestrator.AutoCheckpointer over the same
// already-constructed services wire.go composes.
//
// Config loading here is fail-open end to end (ADD §17.5): a missing
// file is "nothing to contribute" (config.LoadFile's own contract), and a
// malformed file or section degrades to the factory defaults — the
// composition root must never refuse to build the CLI because a YAML file
// is broken (doctor is the surface that reports config problems, not
// every hook invocation).
package main

import (
	"os"
	"path/filepath"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/config"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/paths"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// loadStateCheckpointingConfig loads ADD §26.1's precedence chain —
// defaults < global user config (<Config dir>/config.yaml) <
// .auspex/config.yaml < .auspex/local.yaml, the repo-level files resolved
// from the process working directory (hooks and managed runs execute in
// the workspace) — and returns the typed state_checkpointing section.
// The environment/CLI-flag layers of §26.1 have no field mapping defined
// for this section yet, matching internal/config's own recorded status.
// Every failure path fails open to the factory defaults.
func loadStateCheckpointingConfig(dirs paths.Dirs) config.StateCheckpointing {
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
		return config.DefaultStateCheckpointing()
	}
	section, err := cfg.StateCheckpointingSection()
	if err != nil {
		return config.DefaultStateCheckpointing()
	}
	return section
}

// composeAutoCheckpointer builds the AutoCheckpointer when the ADR-0054
// gate is enabled; nil (the orchestrator's documented "advisory"
// degrade) when disabled. Every dependency is an already-constructed
// instance from buildRootCmd — the same reuse-only discipline wire.go's
// own doc comment prescribes: the evaluation service doubles as both
// DecisionDeps halves (it satisfies app.EvaluationService and
// orchestrator.AuthorizationIssuer, exactly as `decision allow`'s wiring
// expects), and the SQLDataSource doubles as the task resolver (the same
// narrow orchestrator.SessionResolver view the event correlator uses).
func composeAutoCheckpointer(
	enabled bool,
	db *sqlite.DB,
	evaluationService *evaluation.Service,
	stateCheckpoint app.StateCheckpointService,
	repositoryCheckpoint app.RepositoryCheckpointService,
	sessions orchestrator.SessionResolver,
) *orchestrator.AutoCheckpointer {
	if !enabled {
		return nil
	}
	return &orchestrator.AutoCheckpointer{
		Checkpoints: orchestrator.CheckpointCreateDeps{
			StateCheckpoint:      stateCheckpoint,
			RepositoryCheckpoint: repositoryCheckpoint,
		},
		Decision: orchestrator.DecisionDeps{
			Evaluation: evaluationService,
			Issuer:     evaluationService,
		},
		Sessions:  sessions,
		Worktrees: &orchestrator.SessionWorktreeStore{DB: db},
	}
}
