// Package paths resolves the OS-correct, non-repository-local directories
// Auspex uses for global user configuration, persistent data, cache, and
// runtime (socket/pid/lock) files.
//
// Repository-local paths (.auspex/config.yaml, .auspex/*.db, etc. —
// ADD §26.3) are NOT this package's concern; those are resolved relative to
// a repository root by the role that owns repository scoping. This package
// only resolves the global, per-user directories referenced by ADD §26.1
// as "global user config" and the equivalent data/cache/runtime locations
// needed by the SQLite store, logs, and daemon socket/lock/pid files.
//
// Every resolution function takes an Env so callers (and tests) can inject
// environment variables and the home directory instead of reading the real
// process environment, per agents/foundation.md's "injectable
// environment/home" requirement.
package paths

import (
	"errors"
	"path"
	"runtime"
	"strings"
)

// Env is the injectable source of environment variables and home directory
// that path resolution reads from. Production code uses OSEnv; tests use a
// fake implementation to exercise every OS branch deterministically,
// regardless of which OS the test suite actually runs on.
type Env interface {
	// Getenv returns the value of the named environment variable, or ""
	// if it is unset or empty. Callers treat "" as "unset" — Auspex
	// never distinguishes an empty override from no override.
	Getenv(key string) string
	// UserHomeDir returns the current user's home directory, or an error
	// if it cannot be determined.
	UserHomeDir() (string, error)
}

// Dirs holds the resolved set of Auspex global directories.
type Dirs struct {
	// Config holds global user configuration (ADD §26.1 "global user
	// config"), e.g. the base auspex.yaml a repository's
	// .auspex/config.yaml layers on top of.
	Config string
	// Data holds persistent application data not safe to delete casually
	// (e.g. the global SQLite database, historical exports).
	Data string
	// Cache holds derived/regenerable data safe to delete at any time.
	Cache string
	// Runtime holds ephemeral, single-boot-lifetime files: daemon
	// socket, pidfile, lockfile. On POSIX this prefers a
	// permissions-restricted per-user runtime directory (e.g.
	// XDG_RUNTIME_DIR) and falls back to Cache when none is available,
	// since not all POSIX systems (notably macOS) set one.
	Runtime string
}

// AppName is the leaf directory/namespace segment used under each OS's base
// directory (e.g. "auspex" under XDG_CONFIG_HOME, or "Auspex" under
// macOS's Application Support).
const AppName = "auspex"

// ErrNoHomeDir is returned (wrapped) when a home directory is required to
// resolve a path but Env could not provide one.
var ErrNoHomeDir = errors.New("paths: could not determine home directory")

// Resolve returns the OS-correct Dirs for goos ("linux", "darwin",
// "windows", ...) using env for environment/home lookups. Passing
// runtime.GOOS as goos gives the real host's behavior; tests pass a fixed
// goos to exercise all three OS families from any host.
func Resolve(goos string, env Env) (Dirs, error) {
	switch goos {
	case "windows":
		return resolveWindows(env)
	case "darwin":
		return resolveDarwin(env)
	default:
		// Treat every other GOOS (linux, freebsd, openbsd, ...) as
		// XDG-conformant POSIX, which is correct for every OS Auspex
		// targets per Auspex_ADD.md's portability goals beyond the
		// three primary desktop OSes.
		return resolveXDG(env)
	}
}

// ResolveHost is a convenience wrapper for Resolve(runtime.GOOS, env).
func ResolveHost(env Env) (Dirs, error) {
	return Resolve(runtime.GOOS, env)
}

func resolveXDG(env Env) (Dirs, error) {
	home, err := requireHome(env)
	if err != nil {
		return Dirs{}, err
	}

	config := firstNonEmpty(env.Getenv("XDG_CONFIG_HOME"), path.Join(home, ".config"))
	data := firstNonEmpty(env.Getenv("XDG_DATA_HOME"), path.Join(home, ".local", "share"))
	cache := firstNonEmpty(env.Getenv("XDG_CACHE_HOME"), path.Join(home, ".cache"))
	// XDG_RUNTIME_DIR has no portable, spec-compliant fallback (the spec
	// says applications should handle its absence themselves); fall back
	// to a subdirectory of cache, which is always writable and per-user.
	rt := env.Getenv("XDG_RUNTIME_DIR")

	runtimeDir := path.Join(cache, AppName, "run")
	if rt != "" {
		runtimeDir = path.Join(rt, AppName)
	}

	return Dirs{
		Config:  path.Join(config, AppName),
		Data:    path.Join(data, AppName),
		Cache:   path.Join(cache, AppName),
		Runtime: runtimeDir,
	}, nil
}

func resolveDarwin(env Env) (Dirs, error) {
	home, err := requireHome(env)
	if err != nil {
		return Dirs{}, err
	}

	appSupport := path.Join(home, "Library", "Application Support", AppName)
	caches := path.Join(home, "Library", "Caches", AppName)

	return Dirs{
		// macOS has no separate conventional "config" directory distinct
		// from application data; Application Support serves both roles,
		// matching common Go CLI tool practice on macOS.
		Config: appSupport,
		Data:   appSupport,
		Cache:  caches,
		// No macOS equivalent of XDG_RUNTIME_DIR exists; use a
		// subdirectory of Caches, which is per-user and writable.
		Runtime: path.Join(caches, "run"),
	}, nil
}

func resolveWindows(env Env) (Dirs, error) {
	appData := env.Getenv("APPDATA")
	localAppData := env.Getenv("LOCALAPPDATA")

	if appData == "" || localAppData == "" {
		home, err := requireHome(env)
		if err != nil {
			return Dirs{}, err
		}
		if appData == "" {
			appData = winJoin(home, "AppData", "Roaming")
		}
		if localAppData == "" {
			localAppData = winJoin(home, "AppData", "Local")
		}
	}

	return Dirs{
		Config:  winJoin(appData, AppName, "Config"),
		Data:    winJoin(localAppData, AppName, "Data"),
		Cache:   winJoin(localAppData, AppName, "Cache"),
		Runtime: winJoin(localAppData, AppName, "Run"),
	}, nil
}

// winJoin joins path elements with a literal backslash, independent of the
// host OS running the code. It deliberately does not use filepath.Join or
// path.Join: filepath's separator follows the host GOOS (wrong when
// resolving Windows paths from a non-Windows test host) and path.Join
// always uses "/". Elements are expected to already be Windows-shaped
// (e.g. env values like "C:\Users\alice\AppData\Roaming"); this only
// controls the separator used to append AppName/subdirectory segments.
func winJoin(elems ...string) string {
	cleaned := make([]string, 0, len(elems))
	for _, e := range elems {
		e = strings.Trim(e, `\`)
		if e != "" {
			cleaned = append(cleaned, e)
		}
	}
	return strings.Join(cleaned, `\`)
}

func requireHome(env Env) (string, error) {
	home, err := env.UserHomeDir()
	if err != nil {
		return "", errors.Join(ErrNoHomeDir, err)
	}
	if home == "" {
		return "", ErrNoHomeDir
	}
	return home, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
