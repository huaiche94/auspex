/**
 * paths.ts — per-OS resolution of Auspex's global user directories.
 *
 * This is a line-for-line TypeScript mirror of the Go daemon's own
 * resolver, internal/paths/paths.go (Resolve/resolveXDG/resolveDarwin/
 * resolveWindows/winJoin). The extension MUST agree with the daemon about
 * where the runtime metadata (<runtime>/daemon.json) and bearer token
 * (<data>/daemon.token) live, so the semantics here are copied precisely
 * rather than reimplemented from taste:
 *
 *  - darwin:  Config = Data = ~/Library/Application Support/auspex,
 *             Cache = ~/Library/Caches/auspex, Runtime = <Cache>/run
 *             (macOS has no XDG_RUNTIME_DIR equivalent).
 *  - windows: Config = %APPDATA%\auspex\Config,
 *             Data   = %LOCALAPPDATA%\auspex\Data,
 *             Cache  = %LOCALAPPDATA%\auspex\Cache,
 *             Runtime= %LOCALAPPDATA%\auspex\Run; when APPDATA/LOCALAPPDATA
 *             are unset, fall back to <home>\AppData\Roaming|Local.
 *  - everything else (linux, *bsd — treated as XDG-conformant POSIX):
 *             Config = $XDG_CONFIG_HOME|~/.config /auspex,
 *             Data   = $XDG_DATA_HOME|~/.local/share /auspex,
 *             Cache  = $XDG_CACHE_HOME|~/.cache /auspex,
 *             Runtime= $XDG_RUNTIME_DIR/auspex when set, otherwise
 *             <cache base>/auspex/run (the XDG spec has no portable
 *             runtime-dir fallback; paths.go falls back to cache).
 *
 * Like paths.go's Env, the environment and home directory are injected so
 * unit tests can exercise every OS branch deterministically from any host.
 *
 * FR-164 note: these paths are Auspex's OWN files. The extension never
 * reads any other extension's storage, VS Code private state, or provider
 * credentials — see README.md.
 */

/** Injectable environment, mirroring internal/paths/paths.go's Env. */
export interface Env {
  /**
   * Value of the named environment variable, or "" when unset. Callers
   * treat "" as "unset" — Auspex never distinguishes an empty override
   * from no override (paths.go Getenv doc).
   */
  getenv(key: string): string;
  /** The current user's home directory, or "" when undeterminable. */
  homedir(): string;
}

/** Resolved Auspex global directories, mirroring paths.go's Dirs. */
export interface Dirs {
  config: string;
  data: string;
  cache: string;
  runtime: string;
}

/** Leaf namespace segment under each OS base dir (paths.go AppName). */
export const APP_NAME = 'auspex';

/** Thrown when a home directory is required but unavailable (paths.go ErrNoHomeDir). */
export class NoHomeDirError extends Error {
  constructor() {
    super('auspex: could not determine home directory');
    this.name = 'NoHomeDirError';
  }
}

/**
 * Resolve the OS-correct Dirs for `platform` (Node's process.platform
 * vocabulary: "darwin" | "win32" | "linux" | ...) using env for
 * environment/home lookups. Mirrors paths.go Resolve, with "win32"
 * standing in for Go's GOOS "windows".
 */
export function resolveDirs(platform: string, env: Env): Dirs {
  switch (platform) {
    case 'win32':
      return resolveWindows(env);
    case 'darwin':
      return resolveDarwin(env);
    default:
      // Every other platform is treated as XDG-conformant POSIX, exactly
      // like paths.go's default branch.
      return resolveXDG(env);
  }
}

function resolveXDG(env: Env): Dirs {
  const home = requireHome(env);
  const config = firstNonEmpty(env.getenv('XDG_CONFIG_HOME'), posixJoin(home, '.config'));
  const data = firstNonEmpty(env.getenv('XDG_DATA_HOME'), posixJoin(home, '.local', 'share'));
  const cache = firstNonEmpty(env.getenv('XDG_CACHE_HOME'), posixJoin(home, '.cache'));
  // XDG_RUNTIME_DIR has no portable, spec-compliant fallback; fall back to
  // a subdirectory of cache, exactly like paths.go resolveXDG.
  const rt = env.getenv('XDG_RUNTIME_DIR');
  const runtime = rt !== '' ? posixJoin(rt, APP_NAME) : posixJoin(cache, APP_NAME, 'run');
  return {
    config: posixJoin(config, APP_NAME),
    data: posixJoin(data, APP_NAME),
    cache: posixJoin(cache, APP_NAME),
    runtime,
  };
}

function resolveDarwin(env: Env): Dirs {
  const home = requireHome(env);
  const appSupport = posixJoin(home, 'Library', 'Application Support', APP_NAME);
  const caches = posixJoin(home, 'Library', 'Caches', APP_NAME);
  return {
    // macOS has no separate conventional "config" directory distinct from
    // application data; Application Support serves both (paths.go).
    config: appSupport,
    data: appSupport,
    cache: caches,
    // No macOS XDG_RUNTIME_DIR equivalent exists; Caches/run (paths.go).
    runtime: posixJoin(caches, 'run'),
  };
}

function resolveWindows(env: Env): Dirs {
  let appData = env.getenv('APPDATA');
  let localAppData = env.getenv('LOCALAPPDATA');
  if (appData === '' || localAppData === '') {
    const home = requireHome(env);
    if (appData === '') {
      appData = winJoin(home, 'AppData', 'Roaming');
    }
    if (localAppData === '') {
      localAppData = winJoin(home, 'AppData', 'Local');
    }
  }
  return {
    config: winJoin(appData, APP_NAME, 'Config'),
    data: winJoin(localAppData, APP_NAME, 'Data'),
    cache: winJoin(localAppData, APP_NAME, 'Cache'),
    runtime: winJoin(localAppData, APP_NAME, 'Run'),
  };
}

/**
 * Join with a literal backslash, independent of the host OS — mirrors
 * paths.go winJoin (elements are already Windows-shaped; this only
 * controls the separator used to append segments, trimming stray
 * backslashes off each element first).
 */
export function winJoin(...elems: string[]): string {
  const cleaned: string[] = [];
  for (let e of elems) {
    e = e.replace(/^\\+|\\+$/g, '');
    if (e !== '') {
      cleaned.push(e);
    }
  }
  return cleaned.join('\\');
}

/**
 * Join with "/" like Go's path.Join as used by paths.go (which uses
 * path.Join — always "/" — for POSIX and darwin shapes). Deliberately NOT
 * Node's path.join: that would emit "\" when the extension host runs on
 * Windows while resolving a POSIX shape in tests.
 */
function posixJoin(...elems: string[]): string {
  // Collapse duplicate separators the way Go's path.Join (path.Clean)
  // does for the shapes we produce (env values with trailing "/"); the
  // inputs here never contain "." / ".." segments, so full Clean
  // semantics are not needed.
  return elems
    .filter((e) => e !== '')
    .join('/')
    .replace(/\/{2,}/g, '/');
}

function requireHome(env: Env): string {
  const home = env.homedir();
  if (home === '') {
    throw new NoHomeDirError();
  }
  return home;
}

function firstNonEmpty(...vals: string[]): string {
  for (const v of vals) {
    if (v !== '') {
      return v;
    }
  }
  return '';
}
