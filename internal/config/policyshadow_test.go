package config

import "testing"

func loadPolicyFromYAML(t *testing.T, body string) (PolicySection, error) {
	t.Helper()
	layers := []Layer{DefaultsLayer()}
	if body != "" {
		layers = append(layers, Layer{Source: SourceGlobalUser, Bytes: []byte(body)})
	}
	cfg, err := Load(layers, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg.PolicyConfigSection()
}

func TestPolicyConfigSection_AbsentYieldsDefaults(t *testing.T) {
	section, err := loadPolicyFromYAML(t, "")
	if err != nil {
		t.Fatalf("PolicyConfigSection: %v", err)
	}
	if section.ShadowEnforcement {
		t.Error("ShadowEnforcement default = true, want false (pre-#142 enforcement unchanged)")
	}
}

func TestPolicyConfigSection_ExplicitTrue(t *testing.T) {
	section, err := loadPolicyFromYAML(t, "policy:\n  shadow_enforcement: true\n")
	if err != nil {
		t.Fatalf("PolicyConfigSection: %v", err)
	}
	if !section.ShadowEnforcement {
		t.Error("ShadowEnforcement = false, want true")
	}
}

func TestPolicyConfigSection_ExplicitFalseAndUnmodeledKeys(t *testing.T) {
	// An explicit false plus un-modeled sibling keys must decode cleanly
	// (unknown keys tolerated, absent-vs-false distinguished).
	section, err := loadPolicyFromYAML(t, "policy:\n  shadow_enforcement: false\n  future_key: 3\n")
	if err != nil {
		t.Fatalf("PolicyConfigSection: %v", err)
	}
	if section.ShadowEnforcement {
		t.Error("ShadowEnforcement = true, want explicit false")
	}
}

func TestPolicyConfigSection_MalformedSectionFailsOpenToDefaults(t *testing.T) {
	section, err := loadPolicyFromYAML(t, "policy:\n  shadow_enforcement: [not, a, bool]\n")
	if err == nil {
		t.Fatal("PolicyConfigSection: want a decode error for a non-bool shadow_enforcement")
	}
	if section.ShadowEnforcement {
		t.Error("on decode error the returned value must be the defaults (shadow off)")
	}
}

func TestBudgetConfigSection(t *testing.T) {
	if s, err := loadPolicyFromYAMLBudget(t, ""); err != nil || s.SessionUSD != 0 {
		t.Errorf("absent budget = (%+v, %v), want zero default", s, err)
	}
	if s, err := loadPolicyFromYAMLBudget(t, "budget:\n  session_usd: 25.5\n"); err != nil || s.SessionUSD != 25.5 {
		t.Errorf("declared budget = (%+v, %v), want 25.5", s, err)
	}
	if s, err := loadPolicyFromYAMLBudget(t, "budget:\n  session_usd: -3\n"); err != nil || s.SessionUSD != 0 {
		t.Errorf("negative budget = (%+v, %v), want ignored (zero)", s, err)
	}
}

func loadPolicyFromYAMLBudget(t *testing.T, body string) (BudgetSection, error) {
	t.Helper()
	layers := []Layer{DefaultsLayer()}
	if body != "" {
		layers = append(layers, Layer{Source: SourceGlobalUser, Bytes: []byte(body)})
	}
	cfg, err := Load(layers, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg.BudgetConfigSection()
}
