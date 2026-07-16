/**
 * types.test.ts — response parsing against payloads shaped EXACTLY like
 * the Go handlers produce (internal/httpapi/httpapi.go,
 * internal/daemon/metadata.go). The fixtures below are field-for-field
 * copies of the Go structs' JSON tags — if a handler shape changes, the
 * mirrors in types.ts and these fixtures must change together.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  parseJobsResponse,
  parseJobView,
  parseMetadata,
  parseSessionStatus,
  parseStatusResponse,
} from '../types';

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

/**
 * A fully populated sessionStatusResponse, field-for-field per
 * internal/sessionstatus/snapshot.go (JSON null for the nullable scalars
 * the server serializes explicitly — no omitempty on those tags).
 */
const POPULATED_SESSION_STATUS = {
  schema_version: 'auspex.daemon.session_status.v1',
  session_id: 'sess-1',
  risk: {
    overall_risk_score: 0.42,
    quota_risk_score: 0.61,
    context_risk_score: 0.2,
    completion_risk_score: 0.1,
    blast_radius_risk_score: 0.05,
    calibrated: false,
    confidence: 'medium',
    reason_codes: ['quota_pressure', 'cold_start'],
    turn_id: 'turn-9',
    evaluated_at: '2026-07-16T10:00:00Z',
  },
  runway: {
    limit_id: 'weekly',
    horizon_seconds: 3600,
    risk_score: 0.55,
    calibrated: false,
    confidence: 'low',
    current_used_percent: 37.2,
    hit_probability: 0.3,
    burn_rate_p50: 0.8,
    burn_rate_p90: 1.4,
    estimated_time_to_limit_p50_seconds: 7500,
    estimated_time_to_limit_p90_seconds: 4200,
    quota_observed_at: '2026-07-16T09:59:30Z',
    reason_codes: ['observed_burn'],
  },
  quota: {
    as_of: '2026-07-16T10:00:00Z',
    windows: [
      {
        limit_id: 'weekly',
        used_percent: 37.2,
        resets_at: '2026-07-17T00:00:00Z',
        observed_at: '2026-07-16T09:59:18Z',
        age_seconds: 42,
      },
      {
        limit_id: 'session_5h',
        used_percent: null,
        resets_at: null,
        observed_at: '2026-07-16T08:00:00Z',
        age_seconds: 7200,
      },
    ],
  },
  progress: {
    task_id: 'task-1',
    nodes: [
      {
        id: 'n-root',
        parent_id: null,
        ordinal: 0,
        kind: 'task',
        status: 'in_progress',
        version: 3,
        started_at: '2026-07-16T09:00:00Z',
        completed_at: null,
        updated_at: '2026-07-16T09:55:00Z',
      },
      {
        id: 'n-child',
        parent_id: 'n-root',
        ordinal: 1,
        kind: 'step',
        status: 'completed',
        version: 1,
        started_at: '2026-07-16T09:05:00Z',
        completed_at: '2026-07-16T09:30:00Z',
        updated_at: '2026-07-16T09:30:00Z',
      },
    ],
    edges: [{ from_node_id: 'n-root', to_node_id: 'n-child', kind: 'contains' }],
  },
  checkpoint: {
    state: {
      id: 'sc-1',
      task_id: 'task-1',
      progress_tree_version: 3,
      active_node_id: 'n-child',
      completion_node_id: null,
      repository_checkpoint_id: 'rc-1',
      integrity_sha256: 'abc123',
      created_at: '2026-07-16T09:30:00Z',
    },
    repository: {
      id: 'rc-1',
      status: 'verified',
      recoverability: 'full',
      git_head: 'deadbeefcafe1234',
      index_diff_hash: 'h-index',
      worktree_diff_hash: 'h-worktree',
      total_bytes: 1024,
      created_at: '2026-07-16T09:30:00Z',
      verified_at: '2026-07-16T09:31:00Z',
    },
  },
  pause: {
    id: 'pause-1',
    task_id: 'task-1',
    turn_id: 'turn-9',
    status: 'sleeping',
    auto_resume_enabled: true,
    runway_forecast_id: 'rf-1',
    state_checkpoint_id: 'sc-1',
    repository_checkpoint_id: 'rc-1',
    requested_at: '2026-07-16T09:40:00Z',
    safe_point_at: '2026-07-16T09:41:00Z',
    paused_at: '2026-07-16T09:42:00Z',
    expected_reset_at: '2026-07-17T00:00:00Z',
    cancelled_at: null,
    failure_code: null,
    wake_jobs: [
      {
        id: 'wj-1',
        pause_id: 'pause-1',
        kind: 'pause_resume',
        status: 'scheduled',
        run_after: '2026-07-17T00:05:00Z',
        attempts: 0,
        max_attempts: 5,
        lease_owner: null,
        lease_expires_at: null,
        last_error: null,
      },
    ],
  },
};

test('parses a fully populated session status (sessionstatus/snapshot.go)', () => {
  const snap = parseSessionStatus(POPULATED_SESSION_STATUS);
  assert.ok(snap);
  assert.equal(snap.session_id, 'sess-1');

  assert.ok(snap.risk);
  assert.equal(snap.risk.overall_risk_score, 0.42);
  assert.equal(snap.risk.calibrated, false);
  assert.deepEqual(snap.risk.reason_codes, ['quota_pressure', 'cold_start']);

  assert.ok(snap.runway);
  assert.equal(snap.runway.limit_id, 'weekly');
  assert.equal(snap.runway.estimated_time_to_limit_p50_seconds, 7500);
  assert.equal(snap.runway.burn_rate_p90, 1.4);

  assert.equal(snap.quota.windows.length, 2);
  assert.equal(snap.quota.windows[0].used_percent, 37.2);
  // JSON null scalars map to undefined — NEVER to 0 (honesty rule).
  assert.equal(snap.quota.windows[1].used_percent, undefined);
  assert.equal(snap.quota.windows[1].resets_at, undefined);

  assert.equal(snap.progress.task_id, 'task-1');
  assert.equal(snap.progress.nodes.length, 2);
  assert.equal(snap.progress.nodes[0].parent_id, undefined); // null parent
  assert.equal(snap.progress.nodes[1].parent_id, 'n-root');
  assert.equal(snap.progress.edges.length, 1);

  assert.ok(snap.checkpoint?.state);
  assert.equal(snap.checkpoint.state.id, 'sc-1');
  assert.equal(snap.checkpoint.state.completion_node_id, undefined); // null
  assert.equal(snap.checkpoint.repository?.git_head, 'deadbeefcafe1234');
  assert.equal(snap.checkpoint.repository?.total_bytes, 1024);

  assert.ok(snap.pause);
  assert.equal(snap.pause.status, 'sleeping');
  assert.equal(snap.pause.cancelled_at, undefined); // null
  assert.equal(snap.pause.wake_jobs.length, 1);
  assert.equal(snap.pause.wake_jobs[0].id, 'wj-1');
  // Explicit nulls in sessionstatus.WakeJob (no omitempty) → undefined.
  assert.equal(snap.pause.wake_jobs[0].lease_owner, undefined);
  assert.equal(snap.pause.wake_jobs[0].last_error, undefined);
});

test('parses the all-null session status (a brand-new session) without fabricating values', () => {
  const snap = parseSessionStatus({
    schema_version: 'auspex.daemon.session_status.v1',
    session_id: 'sess-new',
    risk: null,
    runway: null,
    quota: { as_of: '2026-07-16T10:00:00Z', windows: [] },
    progress: { task_id: null, nodes: [], edges: [] },
    checkpoint: null,
    pause: null,
  });
  assert.ok(snap);
  assert.equal(snap.risk, undefined);
  assert.equal(snap.runway, undefined);
  assert.equal(snap.checkpoint, undefined);
  assert.equal(snap.pause, undefined);
  assert.deepEqual(snap.quota.windows, []);
  assert.equal(snap.progress.task_id, undefined);
  assert.deepEqual(snap.progress.nodes, []);
});

test('rejects a session status with a foreign schema_version or shape', () => {
  assert.equal(
    parseSessionStatus({ schema_version: 'auspex.daemon.session_status.v2', session_id: 's' }),
    undefined
  );
  assert.equal(parseSessionStatus({ schema_version: 'auspex.daemon.session_status.v1' }), undefined);
  assert.equal(parseSessionStatus('not an object'), undefined);
  assert.equal(parseSessionStatus(undefined), undefined);
});

test('degrades a malformed risk section to unknown, never zero-filled', () => {
  const snap = parseSessionStatus({
    schema_version: 'auspex.daemon.session_status.v1',
    session_id: 'sess-odd',
    risk: { overall_risk_score: 'not a number', calibrated: false },
    runway: null,
    quota: null, // malformed: still yields the empty-windows shape
    progress: null,
    checkpoint: null,
    pause: null,
  });
  assert.ok(snap);
  assert.equal(snap.risk, undefined); // unknown, not overall 0
  assert.deepEqual(snap.quota, { as_of: '', windows: [] });
  assert.deepEqual(snap.progress, { task_id: undefined, nodes: [], edges: [] });
});
