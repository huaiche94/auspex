/**
 * types.test.ts — response parsing against payloads shaped EXACTLY like
 * the Go handlers produce (internal/httpapi/httpapi.go,
 * internal/daemon/metadata.go). The fixtures below are field-for-field
 * copies of the Go structs' JSON tags — if a handler shape changes, the
 * mirrors in types.ts and these fixtures must change together.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { parseJobsResponse, parseJobView, parseMetadata, parseStatusResponse } from '../types';

test('parses statusResponse (httpapi.go handleStatus)', () => {
  const status = parseStatusResponse({
    schema_version: 'auspex.daemon.status.v1',
    version: '0.1.0',
    started_at: '2026-07-14T10:00:00Z',
    uptime_seconds: 42,
    jobs: { scheduled: 2, done: 1 },
  });
  assert.ok(status);
  assert.equal(status.version, '0.1.0');
  assert.equal(status.uptime_seconds, 42);
  assert.deepEqual(status.jobs, { scheduled: 2, done: 1 });
});

test('rejects a status payload with a foreign schema_version', () => {
  assert.equal(parseStatusResponse({ schema_version: 'something.else.v9', jobs: {} }), undefined);
  assert.equal(parseStatusResponse('not an object'), undefined);
  assert.equal(parseStatusResponse(undefined), undefined);
});

test('parses jobsResponse with optional fields present and absent (httpapi.go jobView)', () => {
  const jobs = parseJobsResponse({
    schema_version: 'auspex.daemon.jobs.v1',
    jobs: [
      {
        id: 'wj-1',
        pause_id: 'pause-1',
        kind: 'pause_resume',
        status: 'scheduled',
        run_after: '2026-07-14T15:00:00Z',
        attempts: 0,
        max_attempts: 5,
      },
      {
        id: 'wj-2',
        pause_id: 'pause-2',
        kind: 'pause_resume',
        status: 'leased',
        run_after: '2026-07-14T15:00:00Z',
        lease_owner: 'daemon-worker',
        lease_expires_at: '2026-07-14T15:01:00Z',
        attempts: 1,
        max_attempts: 5,
        last_error: 'resume validation: quota still unsafe',
      },
    ],
  });
  assert.ok(jobs);
  assert.equal(jobs.length, 2);
  assert.equal(jobs[0].lease_owner, undefined);
  assert.equal(jobs[1].lease_owner, 'daemon-worker');
  assert.equal(jobs[1].last_error, 'resume validation: quota still unsafe');
});

test('parses the cancel response job (httpapi.go jobResponse)', () => {
  const job = parseJobView({
    id: 'wj-1',
    pause_id: 'pause-1',
    kind: 'pause_resume',
    status: 'dead',
    run_after: '2026-07-14T15:00:00Z',
    attempts: 0,
    max_attempts: 5,
    last_error: 'cancelled by operator',
  });
  assert.ok(job);
  assert.equal(job.status, 'dead');
  assert.equal(job.last_error, 'cancelled by operator');
});

test('parses daemon.json metadata (metadata.go, schema auspex.daemon.v1)', () => {
  const meta = parseMetadata({
    schema_version: 'auspex.daemon.v1',
    pid: 4242,
    address: '127.0.0.1:53211',
    token_file: '/data/auspex/daemon.token',
    started_at: '2026-07-14T10:00:00Z',
    version: '0.1.0',
  });
  assert.ok(meta);
  assert.equal(meta.address, '127.0.0.1:53211');
  assert.equal(meta.token_file, '/data/auspex/daemon.token');
});

test('rejects metadata with the wrong schema_version (a daemon we do not speak to)', () => {
  assert.equal(
    parseMetadata({ schema_version: 'auspex.daemon.v2', address: 'x', token_file: 'y' }),
    undefined
  );
});
