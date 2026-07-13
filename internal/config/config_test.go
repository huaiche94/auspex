package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/config"
)

const validDefaults = `
schema_version: auspex.config.v1
runtime:
  daemon:
    enabled: true
`

func TestLoad_DefaultsOnly(t *testing.T) {
	cfg, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte(validDefaults)},
	}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchemaVersion != config.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", cfg.SchemaVersion, config.SchemaVersion)
	}
	if len(cfg.Layers) != 1 || cfg.Layers[0] != config.SourceDefaults {
		t.Errorf("Layers = %v, want [defaults]", cfg.Layers)
	}
}

// --- Precedence (agents/foundation.md "Required tests": config precedence) -

func TestLoad_Precedence_HigherSourceWins(t *testing.T) {
	defaults := `
schema_version: auspex.config.v1
runtime:
  request_timeout_ms: 2000
`
	globalUser := `
runtime:
  request_timeout_ms: 3000
`
	repoConfig := `
runtime:
  request_timeout_ms: 4000
`
	repoLocal := `
runtime:
  request_timeout_ms: 5000
`
	env := `
runtime:
  request_timeout_ms: 6000
`
	cliFlags := `
runtime:
  request_timeout_ms: 7000
`

	// Pass layers in a deliberately shuffled order to prove Load sorts by
	// fixed precedence rather than by argument order.
	cfg, err := config.Load([]config.Layer{
		{Source: config.SourceCLIFlags, Bytes: []byte(cliFlags)},
		{Source: config.SourceDefaults, Bytes: []byte(defaults)},
		{Source: config.SourceRepoLocal, Bytes: []byte(repoLocal)},
		{Source: config.SourceGlobalUser, Bytes: []byte(globalUser)},
		{Source: config.SourceEnvironment, Bytes: []byte(env)},
		{Source: config.SourceRepoConfig, Bytes: []byte(repoConfig)},
	}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	runtimeSection, ok := cfg.Raw["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("Raw[runtime] = %#v, want map", cfg.Raw["runtime"])
	}
	// CLI flags is highest precedence per ADD §26.1; must win over every
	// other layer.
	got := runtimeSection["request_timeout_ms"]
	if got != 7000 {
		t.Errorf("request_timeout_ms = %v, want 7000 (cli_flags layer)", got)
	}
}

func TestLoad_Precedence_PartialOverride_LowerLayerFieldsSurvive(t *testing.T) {
	defaults := `
schema_version: auspex.config.v1
runtime:
  request_timeout_ms: 2000
privacy:
  store_prompts: false
`
	repoConfig := `
runtime:
  request_timeout_ms: 9000
`

	cfg, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte(defaults)},
		{Source: config.SourceRepoConfig, Bytes: []byte(repoConfig)},
	}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// A field only defaults sets (privacy.store_prompts) must still be
	// present even though a higher layer overrode a different top-level
	// key (runtime).
	if _, ok := cfg.Raw["privacy"]; !ok {
		t.Errorf("Raw[privacy] missing after higher-precedence layer touched a different key")
	}
}

func TestLoad_MissingLayer_TreatedAsAbsent(t *testing.T) {
	// Every layer below CLI flags/environment is optional per ADD §26.1;
	// a Layer with no Bytes must not error and must not affect Layers.
	cfg, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte(validDefaults)},
		{Source: config.SourceRepoConfig}, // no Bytes: absent file
	}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, l := range cfg.Layers {
		if l == config.SourceRepoConfig {
			t.Errorf("Layers includes SourceRepoConfig despite empty Bytes: %v", cfg.Layers)
		}
	}
}

func TestLoad_DuplicateSource_Errors(t *testing.T) {
	_, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte(validDefaults)},
		{Source: config.SourceDefaults, Bytes: []byte(validDefaults)},
	}, config.Options{})
	if !errors.Is(err, config.ErrDuplicateSource) {
		t.Errorf("err = %v, want ErrDuplicateSource", err)
	}
}

// --- schema_version validation ---------------------------------------------

func TestLoad_MissingSchemaVersion_Errors(t *testing.T) {
	_, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte("runtime:\n  request_timeout_ms: 2000\n")},
	}, config.Options{})
	if !errors.Is(err, config.ErrInvalidSchemaVersion) {
		t.Errorf("err = %v, want ErrInvalidSchemaVersion", err)
	}
}

func TestLoad_WrongSchemaVersion_Errors(t *testing.T) {
	_, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte("schema_version: auspex.config.v2\n")},
	}, config.Options{})
	if !errors.Is(err, config.ErrInvalidSchemaVersion) {
		t.Errorf("err = %v, want ErrInvalidSchemaVersion", err)
	}
}

// --- Unknown field behavior (agents/foundation.md "Required tests": unknown
// field behavior; ADD §26.2 "Unknown fields: default warn; strict validation
// error") -------------------------------------------------------------------

func TestLoad_UnknownField_WarnByDefault(t *testing.T) {
	cfg, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte("schema_version: auspex.config.v1\nfrobnicate: true\n")},
	}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v (want success under default warn policy)", err)
	}
	if len(cfg.UnknownFields) != 1 || cfg.UnknownFields[0] != "frobnicate" {
		t.Errorf("UnknownFields = %v, want [frobnicate]", cfg.UnknownFields)
	}
}

func TestLoad_UnknownField_StrictErrors(t *testing.T) {
	_, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte("schema_version: auspex.config.v1\nfrobnicate: true\n")},
	}, config.Options{UnknownFieldPolicy: config.StrictUnknownFields})
	if !errors.Is(err, config.ErrUnknownFields) {
		t.Errorf("err = %v, want ErrUnknownFields", err)
	}
}

func TestLoad_KnownSections_NoWarning(t *testing.T) {
	full := `
schema_version: auspex.config.v1
runtime:
  daemon:
    enabled: true
privacy:
  store_prompts: false
prediction:
  predictor: empirical_heuristic_v1
risk:
  low_below: 0.45
state_checkpointing:
  enabled: true
repository_checkpoint:
  backend: local_patch
graceful_pause:
  mode: pause_and_notify
`
	cfg, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte(full)},
	}, config.Options{UnknownFieldPolicy: config.StrictUnknownFields})
	if err != nil {
		t.Fatalf("Load: %v (all sections are ADD §26.4-documented, should not be unknown)", err)
	}
	if len(cfg.UnknownFields) != 0 {
		t.Errorf("UnknownFields = %v, want none", cfg.UnknownFields)
	}
}

// --- Malformed YAML ----------------------------------------------------

func TestLoad_MalformedYAML_Errors(t *testing.T) {
	_, err := config.Load([]config.Layer{
		{Source: config.SourceDefaults, Bytes: []byte("schema_version: [unterminated\n")},
	}, config.Options{})
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

// --- LoadFile ----------------------------------------------------------

func TestLoadFile_MissingFile_ReturnsEmptyLayer(t *testing.T) {
	layer, err := config.LoadFile(config.SourceRepoConfig, filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if layer.Bytes != nil {
		t.Errorf("Bytes = %v, want nil for missing file", layer.Bytes)
	}
	if layer.Source != config.SourceRepoConfig {
		t.Errorf("Source = %v, want SourceRepoConfig", layer.Source)
	}
}

func TestLoadFile_ExistingFile_ReadsBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(validDefaults), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	layer, err := config.LoadFile(config.SourceRepoConfig, path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if string(layer.Bytes) != validDefaults {
		t.Errorf("Bytes = %q, want %q", layer.Bytes, validDefaults)
	}
}

func TestLoadFile_UnreadableFile_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-permission.yaml")
	if err := os.WriteFile(path, []byte(validDefaults), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) }) // allow cleanup to remove it

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	_, err := config.LoadFile(config.SourceRepoConfig, path)
	if err == nil {
		t.Fatal("expected error reading a permission-denied file")
	}
}

// End-to-end: full precedence chain via LoadFile, matching how a real
// caller (a future `runtime` role config command) would assemble layers.
func TestLoad_EndToEnd_FileBackedPrecedenceChain(t *testing.T) {
	dir := t.TempDir()

	globalPath := filepath.Join(dir, "global.yaml")
	repoConfigPath := filepath.Join(dir, "repo-config.yaml")
	repoLocalPath := filepath.Join(dir, "repo-local.yaml") // intentionally not written: absent

	mustWrite(t, globalPath, "schema_version: auspex.config.v1\nruntime:\n  request_timeout_ms: 111\n")
	mustWrite(t, repoConfigPath, "runtime:\n  request_timeout_ms: 222\n")

	globalLayer, err := config.LoadFile(config.SourceGlobalUser, globalPath)
	if err != nil {
		t.Fatalf("LoadFile(global): %v", err)
	}
	repoConfigLayer, err := config.LoadFile(config.SourceRepoConfig, repoConfigPath)
	if err != nil {
		t.Fatalf("LoadFile(repoConfig): %v", err)
	}
	repoLocalLayer, err := config.LoadFile(config.SourceRepoLocal, repoLocalPath)
	if err != nil {
		t.Fatalf("LoadFile(repoLocal): %v", err)
	}

	cfg, err := config.Load([]config.Layer{globalLayer, repoConfigLayer, repoLocalLayer}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	runtimeSection := cfg.Raw["runtime"].(map[string]any)
	if runtimeSection["request_timeout_ms"] != 222 {
		t.Errorf("request_timeout_ms = %v, want 222 (repo_config beats global_user)", runtimeSection["request_timeout_ms"])
	}
	for _, l := range cfg.Layers {
		if l == config.SourceRepoLocal {
			t.Errorf("Layers includes repo_local despite the file not existing: %v", cfg.Layers)
		}
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
