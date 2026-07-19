package config

import (
	"strings"
	"testing"
)

func loadFromYAML(t *testing.T, layers ...Layer) Config {
	t.Helper()
	cfg, err := Load(layers, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestStateCheckpointing_DefaultsLayerAlone_GateEnabled(t *testing.T) {
	// A machine with no config files at all: the defaults layer satisfies
	// the schema_version requirement and the factory default applies.
	cfg := loadFromYAML(t, DefaultsLayer())
	section, err := cfg.StateCheckpointingSection()
	if err != nil {
		t.Fatalf("StateCheckpointingSection: %v", err)
	}
	if !section.OnCheckpointAndRun {
		t.Error("OnCheckpointAndRun = false, want true (ADR-0054 default: enabled)")
	}
}

func TestStateCheckpointing_SectionPresentWithoutKey_KeepsDefault(t *testing.T) {
	cfg := loadFromYAML(t, DefaultsLayer(), Layer{
		Source: SourceRepoConfig,
		Bytes:  []byte("schema_version: auspex.config.v1\nstate_checkpointing:\n  enabled: true\n"),
	})
	section, err := cfg.StateCheckpointingSection()
	if err != nil {
		t.Fatalf("StateCheckpointingSection: %v", err)
	}
	if !section.OnCheckpointAndRun {
		t.Error("OnCheckpointAndRun = false, want true — an absent key means default, never off (unknown is not zero)")
	}
}

func TestStateCheckpointing_ExplicitFalse_DisablesGate(t *testing.T) {
	cfg := loadFromYAML(t, DefaultsLayer(), Layer{
		Source: SourceRepoConfig,
		Bytes:  []byte("schema_version: auspex.config.v1\nstate_checkpointing:\n  on_checkpoint_and_run: false\n"),
	})
	section, err := cfg.StateCheckpointingSection()
	if err != nil {
		t.Fatalf("StateCheckpointingSection: %v", err)
	}
	if section.OnCheckpointAndRun {
		t.Error("OnCheckpointAndRun = true, want false when explicitly disabled")
	}
}

func TestStateCheckpointing_RepoLocalOverridesRepoConfig(t *testing.T) {
	// ADD §26.1 precedence: .auspex/local.yaml beats .auspex/config.yaml.
	// Note Load's documented shallow merge: the higher layer's whole
	// section replaces the lower one's.
	cfg := loadFromYAML(t,
		DefaultsLayer(),
		Layer{Source: SourceRepoConfig, Bytes: []byte("schema_version: auspex.config.v1\nstate_checkpointing:\n  on_checkpoint_and_run: true\n")},
		Layer{Source: SourceRepoLocal, Bytes: []byte("state_checkpointing:\n  on_checkpoint_and_run: false\n")},
	)
	section, err := cfg.StateCheckpointingSection()
	if err != nil {
		t.Fatalf("StateCheckpointingSection: %v", err)
	}
	if section.OnCheckpointAndRun {
		t.Error("OnCheckpointAndRun = true, want false — repo local layer must win")
	}
}

func TestStateCheckpointing_MalformedSection_ReturnsDefaultsAndError(t *testing.T) {
	cfg := loadFromYAML(t, DefaultsLayer(), Layer{
		Source: SourceRepoConfig,
		Bytes:  []byte("schema_version: auspex.config.v1\nstate_checkpointing: [not, a, map]\n"),
	})
	section, err := cfg.StateCheckpointingSection()
	if err == nil {
		t.Fatal("StateCheckpointingSection: want an error for a non-map section")
	}
	if !strings.Contains(err.Error(), "state_checkpointing") {
		t.Errorf("error = %v, want it to name the section", err)
	}
	if !section.OnCheckpointAndRun {
		t.Error("OnCheckpointAndRun = false, want the fail-open default (true) alongside the error")
	}
}
