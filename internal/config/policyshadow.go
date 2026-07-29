// policyshadow.go: the typed decode of the `policy` configuration
// section — the second production consumer of Config.Raw, following
// statecheckpointing.go's exact registration pattern ("a later role
// adding a genuinely consumed section registers it here and owns its
// own typed decode from Config.Raw"). Only fields something actually
// reads are modeled (Constitution §7 rule 10): as of ADR-0057 §5 /
// issue #142 that is `shadow_enforcement` alone; other policy keys
// (threshold overrides, budget declarations) stay un-modeled until a
// consumer exists.
package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// PolicySection is the decoded `policy` section.
type PolicySection struct {
	// ShadowEnforcement gates ADR-0057 §5's shadow-first enforcement
	// discipline at the evaluation layer (issue #142): when true, a
	// STATISTICAL enforcement-grade decision (BLOCK / PAUSE /
	// PAUSE_AND_AUTO_RESUME driven by estimates) is served downgraded
	// to WARN and its original action recorded in
	// policy_decisions.would_action for false-positive review. The two
	// fail-closed fact gates (explicit deny, integrity failure) are
	// never shadowed. Default false: the pre-#142 enforcement behavior
	// is unchanged until the operator opts into shadow evaluation.
	ShadowEnforcement bool
}

// DefaultPolicySection returns the section's factory defaults
// (shadow off — enforcement behaves exactly as before #142).
func DefaultPolicySection() PolicySection {
	return PolicySection{ShadowEnforcement: false}
}

// policySectionYAML is the decode shape: pointer-typed so an absent key
// is distinguishable from an explicit false (unknown is not zero — an
// absent key means "use the default", never "off").
type policySectionYAML struct {
	ShadowEnforcement *bool `yaml:"shadow_enforcement"`
}

// PolicyConfigSection decodes the merged `policy` section from c.Raw.
// An absent section, or a present section without the modeled keys,
// yields the defaults with a nil error. A section that exists but
// cannot be decoded returns the DEFAULTS alongside the error, the same
// fail-open-usable / fail-closed-checkable split
// StateCheckpointingSection established.
func (c Config) PolicyConfigSection() (PolicySection, error) {
	out := DefaultPolicySection()
	raw, ok := c.Raw["policy"]
	if !ok || raw == nil {
		return out, nil
	}
	buf, err := yaml.Marshal(raw)
	if err != nil {
		return out, fmt.Errorf("config: re-encoding policy section: %w", err)
	}
	var section policySectionYAML
	if err := yaml.Unmarshal(buf, &section); err != nil {
		return out, fmt.Errorf("config: decoding policy section: %w", err)
	}
	if section.ShadowEnforcement != nil {
		out.ShadowEnforcement = *section.ShadowEnforcement
	}
	return out, nil
}
