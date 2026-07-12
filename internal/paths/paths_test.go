package paths_test

import (
	"errors"
	"testing"

	"github.com/huaiche94/preflight/internal/paths"
)

// --- Windows/macOS/Linux path-table tests (agents/foundation.md "Required
// tests" bullet) -----------------------------------------------------------

func TestResolve_Linux_XDGDefaults(t *testing.T) {
	env := newFakeEnv("/home/alice")

	dirs, err := paths.Resolve("linux", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := paths.Dirs{
		Config:  "/home/alice/.config/preflight",
		Data:    "/home/alice/.local/share/preflight",
		Cache:   "/home/alice/.cache/preflight",
		Runtime: "/home/alice/.cache/preflight/run",
	}
	assertDirsEqual(t, dirs, want)
}

func TestResolve_Linux_XDGOverrides(t *testing.T) {
	env := newFakeEnv("/home/alice").
		with("XDG_CONFIG_HOME", "/custom/config").
		with("XDG_DATA_HOME", "/custom/data").
		with("XDG_CACHE_HOME", "/custom/cache").
		with("XDG_RUNTIME_DIR", "/run/user/1000")

	dirs, err := paths.Resolve("linux", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := paths.Dirs{
		Config:  "/custom/config/preflight",
		Data:    "/custom/data/preflight",
		Cache:   "/custom/cache/preflight",
		Runtime: "/run/user/1000/preflight",
	}
	assertDirsEqual(t, dirs, want)
}

func TestResolve_FreeBSD_TreatedAsXDG(t *testing.T) {
	// Any non-windows/non-darwin GOOS should fall through to the XDG
	// resolver, since Preflight's portability goal is POSIX-general, not
	// Linux-specific.
	env := newFakeEnv("/home/bob")

	dirs, err := paths.Resolve("freebsd", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dirs.Config != "/home/bob/.config/preflight" {
		t.Errorf("Config = %q, want XDG default", dirs.Config)
	}
}

func TestResolve_Darwin_Defaults(t *testing.T) {
	env := newFakeEnv("/Users/alice")

	dirs, err := paths.Resolve("darwin", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := paths.Dirs{
		Config:  "/Users/alice/Library/Application Support/preflight",
		Data:    "/Users/alice/Library/Application Support/preflight",
		Cache:   "/Users/alice/Library/Caches/preflight",
		Runtime: "/Users/alice/Library/Caches/preflight/run",
	}
	assertDirsEqual(t, dirs, want)
}

func TestResolve_Windows_Defaults(t *testing.T) {
	env := newFakeEnv(`C:\Users\alice`).
		with("APPDATA", `C:\Users\alice\AppData\Roaming`).
		with("LOCALAPPDATA", `C:\Users\alice\AppData\Local`)

	dirs, err := paths.Resolve("windows", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := paths.Dirs{
		Config:  `C:\Users\alice\AppData\Roaming\preflight\Config`,
		Data:    `C:\Users\alice\AppData\Local\preflight\Data`,
		Cache:   `C:\Users\alice\AppData\Local\preflight\Cache`,
		Runtime: `C:\Users\alice\AppData\Local\preflight\Run`,
	}
	assertDirsEqual(t, dirs, want)
}

func TestResolve_Windows_FallsBackToHomeWhenEnvUnset(t *testing.T) {
	env := newFakeEnv(`C:\Users\alice`)

	dirs, err := paths.Resolve("windows", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	const wantConfig = `C:\Users\alice\AppData\Roaming\preflight\Config`
	if dirs.Config != wantConfig {
		t.Errorf("Config = %q, want %q", dirs.Config, wantConfig)
	}
}

// --- Injectable environment/home behavior ----------------------------------

func TestResolve_MissingHomeDir_ReturnsError(t *testing.T) {
	env := &fakeEnv{vars: map[string]string{}, homeErr: errors.New("no home")}

	if _, err := paths.Resolve("linux", env); err == nil {
		t.Fatal("expected error when home directory cannot be determined")
	} else if !errors.Is(err, paths.ErrNoHomeDir) {
		t.Errorf("error = %v, want wrapped ErrNoHomeDir", err)
	}
}

func TestResolve_EmptyHomeDir_ReturnsError(t *testing.T) {
	env := newFakeEnv("")

	if _, err := paths.Resolve("linux", env); !errors.Is(err, paths.ErrNoHomeDir) {
		t.Errorf("error = %v, want ErrNoHomeDir", err)
	}
}

func TestResolve_Windows_DoesNotRequireHomeWhenEnvSet(t *testing.T) {
	// APPDATA/LOCALAPPDATA fully set means UserHomeDir should never be
	// consulted; simulate an environment where it would error if called.
	env := &fakeEnv{
		vars: map[string]string{
			"APPDATA":      `C:\Users\alice\AppData\Roaming`,
			"LOCALAPPDATA": `C:\Users\alice\AppData\Local`,
		},
		homeErr: errors.New("home should not be consulted"),
	}

	if _, err := paths.Resolve("windows", env); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

// --- ResolveHost / real OSEnv smoke test -----------------------------------

func TestResolveHost_UsesRealEnv(t *testing.T) {
	dirs, err := paths.ResolveHost(paths.NewOSEnv())
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if dirs.Config == "" || dirs.Data == "" || dirs.Cache == "" || dirs.Runtime == "" {
		t.Errorf("ResolveHost returned an empty directory: %+v", dirs)
	}
}

func assertDirsEqual(t *testing.T, got, want paths.Dirs) {
	t.Helper()
	if got != want {
		t.Errorf("Dirs mismatch:\n got  = %+v\n want = %+v", got, want)
	}
}
