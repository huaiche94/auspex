// statecheckpointing.go: the typed decode of the `state_checkpointing`
// configuration section — the FIRST production consumer of Config.Raw,
// exactly the pattern config.go's package comment reserved ("a later role
// adding a genuinely consumed section registers it here and owns its own
// typed decode from Config.Raw"). Only fields something actually reads
// are modeled (Constitution §7 rule 10): as of ADR-0054 (issue #116)
// that is `on_checkpoint_and_run` alone; the section's other ADD §26.4
// keys (`enabled`, `on_node_completion`, ...) stay un-modeled until a
// consumer exists.
package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// DefaultsBytes is the SourceDefaults layer's YAML: the schema_version
// envelope alone. Every other default lives in Go code
// (DefaultStateCheckpointing and its future siblings), not in a parallel
// YAML document that could drift from it — the defaults layer exists so
// Load succeeds on a machine with no config files at all (schema_version
// is required, ADD §26.2).
const DefaultsBytes = "schema_version: " + SchemaVersion + "\n"

// DefaultsLayer returns the lowest-precedence layer callers pass to Load.
func DefaultsLayer() Layer {
	return Layer{Source: SourceDefaults, Bytes: []byte(DefaultsBytes)}
}

// StateCheckpointing is the decoded `state_checkpointing` section.
type StateCheckpointing struct {
	// OnCheckpointAndRun gates the ADR-0054 automatic pre-turn
	// checkpoint (issue #116): when true (the default), a
	// CHECKPOINT_AND_RUN policy decision makes Auspex create the state +
	// repository checkpoint pair BEFORE the turn proceeds (fail-open on
	// checkpoint failure); when false, the decision is explicitly
	// advisory and the operator runs `auspex checkpoint create` by hand
	// — the pre-#116 behavior.
	OnCheckpointAndRun bool
}

// DefaultStateCheckpointing returns the section's factory defaults
// (ADR-0054: the automatic pre-turn checkpoint ships enabled).
func DefaultStateCheckpointing() StateCheckpointing {
	return StateCheckpointing{OnCheckpointAndRun: true}
}

// stateCheckpointingYAML is the decode shape: pointer-typed so an absent
// key is distinguishable from an explicit false (unknown is not zero —
// an absent key means "use the default", never "off").
type stateCheckpointingYAML struct {
	OnCheckpointAndRun *bool `yaml:"on_checkpoint_and_run"`
}

// StateCheckpointingSection decodes the merged `state_checkpointing`
// section from c.Raw. An absent section, or a present section without
// the modeled keys, yields the defaults with a nil error. A section that
// exists but cannot be decoded returns the DEFAULTS alongside the error,
// so a caller choosing to fail open (the hook path must — ADD §17.5)
// still receives a usable value while a caller choosing to fail closed
// (e.g. a future `auspex config validate`) can surface the error.
func (c Config) StateCheckpointingSection() (StateCheckpointing, error) {
	out := DefaultStateCheckpointing()
	raw, ok := c.Raw["state_checkpointing"]
	if !ok || raw == nil {
		return out, nil
	}
	// Re-marshal the generic map back to YAML and decode into the typed
	// shape — the exact consumption pattern config.go's package comment
	// prescribes for section owners, keeping Load itself shape-agnostic.
	buf, err := yaml.Marshal(raw)
	if err != nil {
		return out, fmt.Errorf("config: re-encoding state_checkpointing section: %w", err)
	}
	var section stateCheckpointingYAML
	if err := yaml.Unmarshal(buf, &section); err != nil {
		return out, fmt.Errorf("config: decoding state_checkpointing section: %w", err)
	}
	if section.OnCheckpointAndRun != nil {
		out.OnCheckpointAndRun = *section.OnCheckpointAndRun
	}
	return out, nil
}
