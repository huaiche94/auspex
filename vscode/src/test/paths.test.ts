/**
 * paths.test.ts — per-OS directory resolution with injected env, proving
 * the TypeScript mirror agrees with internal/paths/paths.go on every OS
 * branch (the same all-branches-from-one-host strategy paths.go's own
 * tests use via their injectable Env).
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { Env, NoHomeDirError, resolveDirs, winJoin } from '../paths';

function fakeEnv(vars: Record<string, string>, home = '/home/alice'): Env {
  return {
    getenv: (key) => vars[key] ?? '',
    homedir: () => home,
  };
}

test('darwin mirrors paths.go resolveDarwin', () => {
  const dirs = resolveDirs('darwin', fakeEnv({}, '/Users/alice'));
  assert.equal(dirs.config, '/Users/alice/Library/Application Support/auspex');
  assert.equal(dirs.data, '/Users/alice/Library/Application Support/auspex');
  assert.equal(dirs.cache, '/Users/alice/Library/Caches/auspex');
  assert.equal(dirs.runtime, '/Users/alice/Library/Caches/auspex/run');
});

test('linux XDG defaults mirror paths.go resolveXDG', () => {
  const dirs = resolveDirs('linux', fakeEnv({}));
  assert.equal(dirs.config, '/home/alice/.config/auspex');
  assert.equal(dirs.data, '/home/alice/.local/share/auspex');
  assert.equal(dirs.cache, '/home/alice/.cache/auspex');
  // No XDG_RUNTIME_DIR: falls back to <cache base>/auspex/run.
  assert.equal(dirs.runtime, '/home/alice/.cache/auspex/run');
});

test('linux honors XDG overrides including XDG_RUNTIME_DIR', () => {
  const dirs = resolveDirs(
    'linux',
    fakeEnv({
      XDG_CONFIG_HOME: '/xdg/config',
      XDG_DATA_HOME: '/xdg/data',
      XDG_CACHE_HOME: '/xdg/cache',
      XDG_RUNTIME_DIR: '/run/user/1000',
    })
  );
  assert.equal(dirs.config, '/xdg/config/auspex');
  assert.equal(dirs.data, '/xdg/data/auspex');
  assert.equal(dirs.cache, '/xdg/cache/auspex');
  assert.equal(dirs.runtime, '/run/user/1000/auspex');
});

test('empty env values are treated as unset (paths.go Getenv contract)', () => {
  const dirs = resolveDirs('linux', fakeEnv({ XDG_DATA_HOME: '', XDG_RUNTIME_DIR: '' }));
  assert.equal(dirs.data, '/home/alice/.local/share/auspex');
  assert.equal(dirs.runtime, '/home/alice/.cache/auspex/run');
});

test('non-darwin non-windows platforms take the XDG branch', () => {
  // paths.go treats every other GOOS as XDG-conformant POSIX.
  const dirs = resolveDirs('freebsd', fakeEnv({}));
  assert.equal(dirs.data, '/home/alice/.local/share/auspex');
});

test('win32 mirrors paths.go resolveWindows with env set', () => {
  const dirs = resolveDirs(
    'win32',
    fakeEnv({
      APPDATA: 'C:\\Users\\alice\\AppData\\Roaming',
      LOCALAPPDATA: 'C:\\Users\\alice\\AppData\\Local',
    })
  );
  assert.equal(dirs.config, 'C:\\Users\\alice\\AppData\\Roaming\\auspex\\Config');
  assert.equal(dirs.data, 'C:\\Users\\alice\\AppData\\Local\\auspex\\Data');
  assert.equal(dirs.cache, 'C:\\Users\\alice\\AppData\\Local\\auspex\\Cache');
  assert.equal(dirs.runtime, 'C:\\Users\\alice\\AppData\\Local\\auspex\\Run');
});

test('win32 falls back to home AppData when env unset', () => {
  const dirs = resolveDirs('win32', fakeEnv({}, 'C:\\Users\\alice'));
  assert.equal(dirs.config, 'C:\\Users\\alice\\AppData\\Roaming\\auspex\\Config');
  assert.equal(dirs.runtime, 'C:\\Users\\alice\\AppData\\Local\\auspex\\Run');
});

test('winJoin trims stray backslashes like paths.go winJoin', () => {
  assert.equal(winJoin('C:\\base\\', 'auspex', 'Data'), 'C:\\base\\auspex\\Data');
  assert.equal(winJoin('', 'auspex'), 'auspex');
});

test('missing home directory throws NoHomeDirError', () => {
  assert.throws(() => resolveDirs('linux', fakeEnv({}, '')), NoHomeDirError);
  assert.throws(() => resolveDirs('darwin', fakeEnv({}, '')), NoHomeDirError);
  // windows only needs home when APPDATA/LOCALAPPDATA are unset:
  assert.throws(() => resolveDirs('win32', fakeEnv({}, '')), NoHomeDirError);
  const dirs = resolveDirs('win32', fakeEnv({ APPDATA: 'C:\\a', LOCALAPPDATA: 'C:\\b' }, ''));
  assert.equal(dirs.data, 'C:\\b\\auspex\\Data');
});

test('trailing slashes in XDG values collapse like Go path.Join', () => {
  const dirs = resolveDirs('linux', fakeEnv({ XDG_DATA_HOME: '/xdg/data/' }));
  assert.equal(dirs.data, '/xdg/data/auspex');
});
