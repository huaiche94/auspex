package config_test

// Cross-package precedence tests (foundation-08): the config precedence
// chain (defaults < global_user < repo_config < repo_local < environment <
// cli_flags, ADD §26.1) composed with internal/paths' env-var-driven
// directory resolution — the two packages' own test files each cover their
// own precedence rules in isolation (paths_test.go: XDG/env var overrides
// per OS; config_test.go: layer precedence given already-resolved bytes),
// but neither exercises them together as a real caller (a future
// `runtime` config command) would: use paths.Resolve to find WHERE the
// global config file lives, honoring env var overrides for that location
// itself, then feed whatever is actually at that location into
// config.Load's own precedence chain alongside other layers.
//
// Names contain "Precedence" so
// `go test ./internal/paths/... ./internal/config/... -run Precedence`
// (the DAG's foundation-08 validation command) selects them.

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/huaiche94/preflight/internal/config"
	"github.com/huaiche94/preflight/internal/paths"
)

// pathsFakeEnv is a local, minimal paths.Env fake (internal/paths' own
// fakeEnv in fake_env_test.go is unexported to that package and cannot be
// reused across package boundaries). Only Getenv/UserHomeDir are needed to
// drive paths.Resolve deterministically.
type pathsFakeEnv struct {
	vars map[string]string
	home string
}

func (f *pathsFakeEnv) Getenv(key string) string     { return f.vars[key] }
func (f *pathsFakeEnv) UserHomeDir() (string, error) { return f.home, nil }

var _ paths.Env = (*pathsFakeEnv)(nil)

// TestPrecedence_PathsEnvOverride_ChangesWhichConfigFileLoads proves that an
// env var override at the PATHS layer (XDG_CONFIG_HOME, resolved before any
// config byte is ever read) changes which file config.LoadFile reads from —
// i.e. the two packages' precedence concerns are independent axes that
// still compose correctly: paths decides WHERE, config decides WHICH BYTES
// WIN once loaded, and an override at the paths layer must not be silently
// masked or duplicated by config's own layering.
func TestPrecedence_PathsEnvOverride_ChangesWhichConfigFileLoads(t *testing.T) {
	defaultConfigRoot := t.TempDir()
	overrideConfigRoot := t.TempDir()

	writeGlobalConfig(t, defaultConfigRoot, 1111)
	writeGlobalConfig(t, overrideConfigRoot, 2222)

	home := t.TempDir()

	// Case 1: no XDG_CONFIG_HOME override -> paths.Resolve falls back to
	// $HOME/.config, which is neither of the two tempdirs above, so no
	// file exists there and the global layer contributes nothing (per
	// config.LoadFile's documented "missing file is not an error"
	// behavior) — defaults alone should win.
	noOverrideEnv := &pathsFakeEnv{vars: map[string]string{}, home: home}
	dirsNoOverride, err := paths.Resolve("linux", noOverrideEnv)
	if err != nil {
		t.Fatalf("Resolve (no override): %v", err)
	}
	cfgNoOverride := loadWithGlobalLayer(t, dirsNoOverride.Config)
	assertRequestTimeout(t, cfgNoOverride, 500) // default value, see writeDefaultsLayer

	// Case 2: XDG_CONFIG_HOME points at overrideConfigRoot -> paths.Resolve
	// must resolve Config to overrideConfigRoot/preflight, and loading
	// from THAT resolved path must surface the override file's value
	// (2222), not the default-root file's value (1111) which was never
	// consulted, and not the config-package default (500).
	overrideEnv := &pathsFakeEnv{
		vars: map[string]string{"XDG_CONFIG_HOME": overrideConfigRoot},
		home: home,
	}
	dirsOverride, err := paths.Resolve("linux", overrideEnv)
	if err != nil {
		t.Fatalf("Resolve (override): %v", err)
	}
	wantConfigDir := filepath.Join(overrideConfigRoot, "preflight")
	if dirsOverride.Config != wantConfigDir {
		t.Fatalf("Resolve Config = %q, want %q", dirsOverride.Config, wantConfigDir)
	}
	cfgOverride := loadWithGlobalLayer(t, dirsOverride.Config)
	assertRequestTimeout(t, cfgOverride, 2222)
}

// TestPrecedence_PathsResolvedGlobal_LosesToRepoAndEnvLayers proves the two
// packages' precedence axes do not get conflated: even once paths.Resolve
// has picked a global config file and its bytes have won at the PATH
// layer, that same file's content must still lose to higher-precedence
// CONFIG layers (repo_config, repo_local, environment, cli_flags) exactly
// as config_test.go's own single-package tests already prove for
// synthetic layers — this test's contribution is proving it holds when the
// global layer's bytes come from a real paths.Resolve-determined file on
// disk, not a hand-constructed []byte.
func TestPrecedence_PathsResolvedGlobal_LosesToRepoAndEnvLayers(t *testing.T) {
	configRoot := t.TempDir()
	writeGlobalConfig(t, configRoot, 1000)

	home := t.TempDir()
	env := &pathsFakeEnv{
		vars: map[string]string{"XDG_CONFIG_HOME": configRoot},
		home: home,
	}
	dirs, err := paths.Resolve("linux", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	globalLayer, err := config.LoadFile(config.SourceGlobalUser, filepath.Join(dirs.Config, "config.yaml"))
	if err != nil {
		t.Fatalf("LoadFile(global): %v", err)
	}

	// schema_version is supplied by the defaults layer, as it realistically
	// would be — writeGlobalConfig's fixture (like a real global config
	// file) only overrides the fields it cares about, not the envelope.
	defaultsLayer := config.Layer{
		Source: config.SourceDefaults,
		Bytes:  []byte("schema_version: preflight.config.v1\n"),
	}
	repoConfigLayer := config.Layer{
		Source: config.SourceRepoConfig,
		Bytes:  []byte("runtime:\n  request_timeout_ms: 3000\n"),
	}
	envLayer := config.Layer{
		Source: config.SourceEnvironment,
		Bytes:  []byte("runtime:\n  request_timeout_ms: 6000\n"),
	}

	cfg, err := config.Load([]config.Layer{defaultsLayer, globalLayer, repoConfigLayer, envLayer}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Environment (highest precedence among the three layers present)
	// must win over both the paths-resolved global file (1000) and
	// repo_config (3000).
	assertRequestTimeout(t, cfg, 6000)

	// The paths-resolved global layer must still have been consulted
	// (Layers records it), proving its loss was due to config precedence,
	// not because paths.Resolve pointed somewhere the file didn't exist.
	found := false
	for _, l := range cfg.Layers {
		if l == config.SourceGlobalUser {
			found = true
		}
	}
	if !found {
		t.Errorf("Layers = %v, want SourceGlobalUser present (file exists at paths-resolved location)", cfg.Layers)
	}
}

// TestPrecedence_PathsRuntimeDirEnvOverride_IndependentOfConfigPrecedence
// proves that overriding a DIFFERENT paths-layer env var (XDG_RUNTIME_DIR,
// which config never reads) does not perturb config's own precedence
// result — the two packages' env-driven resolution axes are independent,
// not accidentally coupled through shared global environment-variable
// state.
func TestPrecedence_PathsRuntimeDirEnvOverride_IndependentOfConfigPrecedence(t *testing.T) {
	configRoot := t.TempDir()
	writeGlobalConfig(t, configRoot, 1234)
	home := t.TempDir()

	env := &pathsFakeEnv{
		vars: map[string]string{
			"XDG_CONFIG_HOME": configRoot,
			"XDG_RUNTIME_DIR": "/run/user/9999", // unrelated to config resolution
		},
		home: home,
	}
	dirs, err := paths.Resolve("linux", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dirs.Runtime != "/run/user/9999/preflight" {
		t.Fatalf("Runtime = %q, want override honored", dirs.Runtime)
	}

	cfg := loadWithGlobalLayer(t, dirs.Config)
	// The unrelated XDG_RUNTIME_DIR override must have no bearing on which
	// config value wins; the global file's own value (1234) is still the
	// only layer present, exactly as it would be without the runtime-dir
	// override.
	assertRequestTimeout(t, cfg, 1234)
}

// --- helpers -----------------------------------------------------------

// writeGlobalConfig writes a config.yaml under configRoot/preflight/ (the
// AppName-suffixed layout paths.Resolve's XDG branch produces) with the
// given request_timeout_ms value, so tests can distinguish "which file did
// paths.Resolve actually point at" by the value each contains.
func writeGlobalConfig(t *testing.T, configRoot string, timeoutMs int) {
	t.Helper()
	dir := filepath.Join(configRoot, paths.AppName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	contents := "runtime:\n  request_timeout_ms: " +
		strconv.Itoa(timeoutMs) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// loadWithGlobalLayer loads defaults (request_timeout_ms: 500) plus
// whatever global-user layer exists at resolvedConfigDir/config.yaml (or
// nothing, if paths.Resolve pointed somewhere with no file — LoadFile's
// documented "missing file is not an error" contract).
func loadWithGlobalLayer(t *testing.T, resolvedConfigDir string) config.Config {
	t.Helper()
	defaultsLayer := config.Layer{
		Source: config.SourceDefaults,
		Bytes:  []byte("schema_version: preflight.config.v1\nruntime:\n  request_timeout_ms: 500\n"),
	}
	globalLayer, err := config.LoadFile(config.SourceGlobalUser, filepath.Join(resolvedConfigDir, "config.yaml"))
	if err != nil {
		t.Fatalf("LoadFile(global): %v", err)
	}
	cfg, err := config.Load([]config.Layer{defaultsLayer, globalLayer}, config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func assertRequestTimeout(t *testing.T, cfg config.Config, want int) {
	t.Helper()
	runtimeSection, ok := cfg.Raw["runtime"].(map[string]any)
	if !ok {
		t.Fatalf("Raw[runtime] = %#v, want map", cfg.Raw["runtime"])
	}
	got := runtimeSection["request_timeout_ms"]
	if got != want {
		t.Errorf("request_timeout_ms = %v, want %v", got, want)
	}
}
