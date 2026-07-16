/**
 * sections.test.ts — the FR-162 honesty rendering, unit-tested against
 * the vscode-free view-model builders in sections.ts:
 *
 *  - null / missing sections render an explicit "unknown / no data yet"
 *    item, never a fabricated zero;
 *  - calibrated:false labels every score an uncalibrated estimate;
 *  - quota staleness flags come from the server-computed age vs the
 *    display-only QUOTA_STALE_AFTER_SECONDS threshold;
 *  - the progress hierarchy nests by parent_id and sorts by ordinal;
 *  - pause wake jobs carry the FR-163 cancel wiring (contextValue
 *    auspex.job.scheduled, id auspex.pausejob.<id>).
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  QUOTA_STALE_AFTER_SECONDS,
  checkpointItems,
  formatDuration,
  pauseItems,
  progressItems,
  quotaItems,
  riskItems,
  runwayItems,
} from '../sections';
import { SessionStatusSnapshot } from '../types';

/** The honest empty session: server answered, but every section is null. */
function emptySession(): SessionStatusSnapshot {
  return {
    schema_version: 'auspex.daemon.session_status.v1',
    session_id: 'sess-empty',
    risk: undefined,
    runway: undefined,
    quota: { as_of: '2026-07-16T10:00:00Z', windows: [] },
    progress: { task_id: undefined, nodes: [], edges: [] },
    checkpoint: undefined,
    pause: undefined,
  };
}

function populatedSession(): SessionStatusSnapshot {
  return {
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
          used_percent: undefined,
          resets_at: undefined,
          observed_at: '2026-07-16T08:00:00Z',
          age_seconds: 7200,
        },
      ],
    },
    progress: {
      task_id: 'task-1',
      nodes: [
        // Deliberately out of ordinal order to prove sibling sorting.
        {
          id: 'n-b',
          parent_id: 'n-root',
          ordinal: 2,
          kind: 'step',
          status: 'pending',
          version: 1,
          updated_at: '2026-07-16T09:20:00Z',
        },
        {
          id: 'n-root',
          parent_id: undefined,
          ordinal: 0,
          kind: 'task',
          status: 'in_progress',
          version: 3,
          started_at: '2026-07-16T09:00:00Z',
          updated_at: '2026-07-16T09:55:00Z',
        },
        {
          id: 'n-a',
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
      edges: [{ from_node_id: 'n-root', to_node_id: 'n-a', kind: 'contains' }],
    },
    checkpoint: {
      state: {
        id: 'sc-1',
        task_id: 'task-1',
        progress_tree_version: 3,
        active_node_id: 'n-a',
        completion_node_id: undefined,
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
      cancelled_at: undefined,
      failure_code: undefined,
      wake_jobs: [
        {
          id: 'wj-1',
          pause_id: 'pause-1',
          kind: 'pause_resume',
          status: 'scheduled',
          run_after: '2026-07-17T00:05:00Z',
          attempts: 0,
          max_attempts: 5,
        },
      ],
    },
  };
}

// --- no session at all (404 / daemon has never seen a session) ---

test('every session section renders a single no-session item when the snapshot is undefined', () => {
  for (const build of [riskItems, runwayItems, quotaItems, progressItems, checkpointItems, pauseItems]) {
    const items = build(undefined);
    assert.equal(items.length, 1);
    assert.equal(items[0].label, 'no session data yet');
  }
});

// --- risk ---

test('risk: null renders unknown, never a zero score', () => {
  const items = riskItems(emptySession());
  assert.equal(items.length, 1);
  assert.match(items[0].label, /^risk: unknown/);
  for (const item of items) {
    assert.ok(!item.label.includes('0.00'), `fabricated zero in ${item.label}`);
  }
});

test('risk: populated renders overall + components, uncalibrated badge, reason codes as children', () => {
  const items = riskItems(populatedSession());
  assert.equal(items[0].label, 'overall risk: 0.42');
  assert.ok(items[0].description?.includes('confidence medium'));
  assert.ok(items[0].description?.includes('uncalibrated estimate'));
  assert.deepEqual(
    items[0].children?.map((c) => c.label),
    ['quota_pressure', 'cold_start']
  );
  const labels = items.map((i) => i.label);
  assert.ok(labels.includes('quota risk: 0.61'));
  assert.ok(labels.includes('context risk: 0.20'));
  assert.ok(labels.includes('completion risk: 0.10'));
  assert.ok(labels.includes('blast radius risk: 0.05'));
});

test('risk: calibrated scores are NOT labelled uncalibrated', () => {
  const session = populatedSession();
  session.risk = { ...session.risk!, calibrated: true };
  const items = riskItems(session);
  assert.ok(!items[0].description?.includes('uncalibrated'));
  assert.ok(items[0].description?.includes('calibrated'));
});

// --- runway ---

test('runway: null renders unknown, no fabricated forecast', () => {
  const items = runwayItems(emptySession());
  assert.equal(items.length, 1);
  assert.match(items[0].label, /^runway: unknown/);
});

test('runway: populated renders ETA p50/p90, burn rate, used-now, and an uncalibrated hit estimate', () => {
  const items = runwayItems(populatedSession());
  const labels = items.map((i) => i.label);
  assert.equal(labels[0], 'limit weekly: risk 0.55');
  assert.ok(labels.includes('time to limit: p50 ~2h 5m · p90 ~1h 10m'));
  assert.ok(labels.includes('burn rate: p50 0.80%/min · p90 1.40%/min'));
  assert.ok(labels.includes('used now: 37.2%'));
  // calibrated:false → "estimate", never "probability" (Constitution §7).
  assert.ok(labels.includes('hit estimate: 0.30 (uncalibrated)'));
  assert.ok(!labels.some((l) => l.includes('probability')));
  assert.deepEqual(
    items[0].children?.map((c) => c.label),
    ['observed_burn']
  );
});

test('runway: absent optional fields render NO leaf at all (only-if-present), not zeros', () => {
  const session = populatedSession();
  session.runway = {
    limit_id: 'weekly',
    risk_score: 0.2,
    calibrated: false,
    confidence: 'low',
    estimated_time_to_limit_p50_seconds: 600,
    reason_codes: [],
  };
  const items = runwayItems(session);
  const labels = items.map((i) => i.label);
  assert.ok(labels.includes('time to limit: p50 ~10m 0s')); // p90 alone omitted
  assert.ok(!labels.some((l) => l.startsWith('burn rate')));
  assert.ok(!labels.some((l) => l.startsWith('used now')));
  assert.ok(!labels.some((l) => l.startsWith('hit ')));
});

test('runway: calibrated forecast labels hit as probability', () => {
  const session = populatedSession();
  session.runway = { ...session.runway!, calibrated: true };
  const labels = runwayItems(session).map((i) => i.label);
  assert.ok(labels.includes('hit probability: 0.30'));
});

// --- quota freshness ---

test('quota: no windows renders no-data, never 0% used', () => {
  const items = quotaItems(emptySession());
  assert.equal(items.length, 1);
  assert.equal(items[0].label, 'no quota observations yet');
  assert.ok(!items[0].label.includes('0'));
});

test('quota: fresh window shows used% + age; missing used_percent stays unknown', () => {
  const items = quotaItems(populatedSession());
  assert.equal(items.length, 2);
  assert.equal(items[0].label, 'weekly: 37.2% used');
  assert.equal(items[0].description, 'age 42s');
  assert.equal(items[0].icon, 'pulse');
  assert.ok(items[0].tooltip?.includes('resets_at: 2026-07-17T00:00:00Z'));
  // null used_percent → "unknown", not 0.0%.
  assert.equal(items[1].label, 'session_5h: used: unknown');
  assert.ok(items[1].tooltip?.includes('resets_at: unknown'));
});

test('quota: a window older than the display threshold is flagged stale', () => {
  const items = quotaItems(populatedSession());
  assert.ok(7200 > QUOTA_STALE_AFTER_SECONDS);
  assert.equal(items[1].description, 'age 2h 0m (stale)');
  assert.equal(items[1].icon, 'warning');
  assert.ok(items[1].tooltip?.includes('display-only threshold'));
});

// --- progress ---

test('progress: no task renders no-data', () => {
  const items = progressItems(emptySession());
  assert.equal(items.length, 1);
  assert.equal(items[0].label, 'no active task for this session yet');
});

test('progress: empty tree says so explicitly (not "0% done")', () => {
  const session = emptySession();
  session.progress = { task_id: 'task-1', nodes: [], edges: [] };
  const items = progressItems(session);
  assert.equal(items.length, 1);
  assert.equal(items[0].label, 'task task-1: progress tree is empty');
  assert.ok(!items[0].label.includes('%'));
});

test('progress: non-empty renders the count summary plus the parent_id hierarchy sorted by ordinal', () => {
  const items = progressItems(populatedSession());
  assert.equal(items[0].label, 'task task-1');
  assert.equal(items[0].description, '3 nodes · 1 edge');
  const root = items[1];
  assert.equal(root.label, 'task · in_progress');
  assert.deepEqual(
    root.children?.map((c) => c.label),
    ['step · completed', 'step · pending'] // ordinal 1 before ordinal 2
  );
});

// --- checkpoint ---

test('checkpoint: null renders no-data', () => {
  const items = checkpointItems(emptySession());
  assert.equal(items.length, 1);
  assert.equal(items[0].label, 'no checkpoint for this session yet');
});

test('checkpoint: populated renders the latest state + linked repository refs', () => {
  const items = checkpointItems(populatedSession());
  assert.equal(items[0].label, 'state checkpoint sc-1');
  assert.ok(items[0].tooltip?.includes('progress_tree_version: 3'));
  assert.ok(items[0].tooltip?.includes('integrity_sha256: abc123'));
  assert.equal(items[1].label, 'repository @ deadbeefcafe…');
  assert.equal(items[1].description, 'verified · full');
  assert.ok(items[1].tooltip?.includes('git_head: deadbeefcafe1234'));
});

test('checkpoint: a state checkpoint without a linked repo says "none linked"', () => {
  const session = populatedSession();
  session.checkpoint = {
    state: { ...session.checkpoint!.state!, repository_checkpoint_id: undefined },
    repository: undefined,
  };
  const items = checkpointItems(session);
  assert.equal(items[1].label, 'repository checkpoint: none linked');
});

// --- pause ---

test('pause: null renders no-data', () => {
  const items = pauseItems(emptySession());
  assert.equal(items.length, 1);
  assert.equal(items[0].label, 'no pause record for this session yet');
});

test('pause: populated renders the record and its wake jobs with FR-163 cancel wiring', () => {
  const items = pauseItems(populatedSession());
  assert.equal(items[0].label, 'pause: sleeping');
  assert.equal(items[0].description, 'auto-resume on');
  assert.ok(items[0].tooltip?.includes('expected_reset_at: 2026-07-17T00:00:00Z'));
  const job = items[1];
  assert.equal(job.label, 'pause_resume · scheduled');
  // The cancel command strips "auspex.pausejob." (distinct from the jobs
  // section's "auspex.job." so TreeItem ids stay tree-unique); the same
  // contextValue gates the inline Cancel button.
  assert.equal(job.id, 'auspex.pausejob.wj-1');
  assert.equal(job.contextValue, 'auspex.job.scheduled');
});

test('pause: a pause with no wake jobs says so instead of hiding the row', () => {
  const session = populatedSession();
  session.pause = { ...session.pause!, wake_jobs: [] };
  const items = pauseItems(session);
  assert.equal(items[1].label, 'no wake jobs for this pause');
});

// --- shared formatting ---

test('formatDuration renders h/m/s buckets', () => {
  assert.equal(formatDuration(42), '42s');
  assert.equal(formatDuration(200), '3m 20s');
  assert.equal(formatDuration(7500), '2h 5m');
  assert.equal(formatDuration(-5), '0s');
});
