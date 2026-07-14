#!/usr/bin/env node
/**
 * run-tests.js — deterministic launcher for the compiled unit tests.
 *
 * Why this exists instead of `node --test out/test/` in package.json
 * (the CI regression this replaced): the test runner's positional-path
 * semantics differ across Node versions —
 *
 *  - Node 20.x scans a directory argument for test files;
 *  - Node 22.x treats an explicit path argument as a module to execute,
 *    so a DIRECTORY dies with ERR_MODULE_NOT_FOUND ("Cannot find module
 *    .../out/test"), which is exactly how the pinned-22.11.0 CI job
 *    failed while a Node 20 host passed;
 *  - and on Node >= 21 an unmatched pattern like out/test/*.test.js is
 *    interpreted by node itself as a glob matching NOTHING, which runs
 *    zero tests and exits 0 — a silent pass if the build ever stopped
 *    emitting test files.
 *
 * So: enumerate the compiled *.test.js files ourselves, FAIL LOUDLY if
 * none exist (zero discovered tests must never look green), and hand
 * node --test an explicit file list, which behaves identically on every
 * supported Node version.
 */
'use strict';

const { readdirSync } = require('node:fs');
const { spawnSync } = require('node:child_process');
const path = require('node:path');

const testDir = path.join(__dirname, '..', 'out', 'test');

let files = [];
try {
  files = readdirSync(testDir)
    .filter((f) => f.endsWith('.test.js'))
    .sort()
    .map((f) => path.join(testDir, f));
} catch {
  // fall through to the empty-list failure below
}

if (files.length === 0) {
  console.error(`run-tests: no compiled *.test.js files found in ${testDir}`);
  console.error('run-tests: did the build run? (npm run build compiles src/test/ -> out/test/)');
  process.exit(1);
}

console.log(`run-tests: ${files.length} test file(s): ${files.map((f) => path.basename(f)).join(', ')}`);
const result = spawnSync(process.execPath, ['--test', ...files], { stdio: 'inherit' });
process.exit(result.status ?? 1);
