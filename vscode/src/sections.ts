/**
 * sections.ts — pure (vscode-free) view-model builders for the FR-162
 * session sections: Risk, Runway, Quota freshness, Progress, Checkpoint,
 * and Pause. tree.ts converts these plain items into vscode.TreeItem;
 * keeping the logic here makes the honesty rendering unit-testable under
 * plain `node --test` — the same reason client.ts avoids the vscode
 * import.
 *
 * Honesty rules (Constitution §7 / ADD §8.8), mirroring the server's
 * sessionstatus.Snapshot invariants (internal/sessionstatus/snapshot.go):
 *
 *  - JSON null / missing → an explicit "unknown / no data yet" item,
 *    never a fabricated zero;
 *  - calibrated:false → every score is labelled an uncalibrated ESTIMATE,
 *    never presented as a probability (Constitution principle #2);
 *  - quota age_seconds comes from the server (as_of − observed_at); the
 *    threshold that flags a window "stale" is a display-only choice,
 *    documented on QUOTA_STALE_AFTER_SECONDS below.
 */

import {
  JobView,
  SessionStatusSnapshot,
  SessionProgressNode,
} from './types';

/**
 * One rendered item: plain data only, so tests can assert on it without a
 * vscode module. tree.ts maps icon → vscode.ThemeIcon and id/contextValue
 * straight onto the TreeItem (contextValue "auspex.job.scheduled" is what
 * gates the inline FR-163 Cancel button — see package.json menus).
 */
export interface SectionItem {
  label: string;
  description?: string;
  tooltip?: string;
  /** Codicon id for vscode.ThemeIcon. */
  icon?: string;
  /** TreeItem.id passthrough ("auspex.job.<id>" wires the cancel command). */
  id?: string;
  /** TreeItem.contextValue passthrough. */
  contextValue?: string;
  children?: SectionItem[];
}

/**
 * Display-only staleness threshold for quota windows. The API serves the
 * measured age (age_seconds); calling >5 minutes "stale" is this
 * extension's presentation choice, not a server-side judgement — the
 * tooltip shows the raw age either way.
 */
export const QUOTA_STALE_AFTER_SECONDS = 300;

const NO_SESSION_TOOLTIP =
  'GET /v1/session/status answered 404: the daemon has not recorded any session yet ' +
  '(or an older daemon without the FR-162 endpoint is running). ' +
  'This is a normal state, not an error — nothing is rendered as zero.';

function noSessionItem(): SectionItem {
  return { label: 'no session data yet', icon: 'info', tooltip: NO_SESSION_TOOLTIP };
}

function unknownItem(label: string, tooltip: string): SectionItem {
  return { label, icon: 'question', tooltip };
}

/** "0.42" — risk scores and probabilities/estimates are shown to 2 dp. */
function fmtScore(n: number): string {
  return n.toFixed(2);
}

function fmtPercent(n: number): string {
  return `${n.toFixed(1)}%`;
}

/** The calibration badge (Constitution principle #2: uncalibrated scores
 * are estimates, NOT probabilities). */
function calibrationNote(calibrated: boolean): string {
  return calibrated ? 'calibrated' : 'uncalibrated estimate';
}

function confidenceNote(confidence: string): string {
  return confidence === '' ? 'confidence unknown' : `confidence ${confidence}`;
}

function reasonCodeChildren(codes: string[]): SectionItem[] {
  return codes.map((code) => ({
    label: code,
    icon: 'tag',
    tooltip: 'Reason code attached by the evaluator (ids/enums only — FR-171).',
  }));
}

/** Risk section: overall + component scores, calibration badge, reason
 * codes as children of the overall item. */
export function riskItems(session: SessionStatusSnapshot | undefined): SectionItem[] {
  if (!session) {
    return [noSessionItem()];
  }
  const risk = session.risk;
  if (!risk) {
    return [
      unknownItem(
        'risk: unknown — no prediction for this session yet',
        '`risk` is null in auspex.daemon.session_status.v1: no prediction row is linkable ' +
          'to this session yet. Rendered as unknown, never as a zero score.'
      ),
    ];
  }
  const items: SectionItem[] = [];
  items.push({
    label: `overall risk: ${fmtScore(risk.overall_risk_score)}`,
    description: `${confidenceNote(risk.confidence)} · ${calibrationNote(risk.calibrated)}`,
    icon: risk.calibrated ? 'shield' : 'beaker',
    tooltip:
      `Most recent prediction (turn ${risk.turn_id || '?'}, evaluated_at ${risk.evaluated_at || '?'}).\n` +
      (risk.calibrated
        ? 'calibrated: true — scores have been calibrated against outcomes.'
        : 'calibrated: false — scores are heuristic ESTIMATES, not probabilities (Constitution principle #2).'),
    children: reasonCodeChildren(risk.reason_codes),
  });
  const components: Array<[string, number | undefined]> = [
    ['quota', risk.quota_risk_score],
    ['context', risk.context_risk_score],
    ['completion', risk.completion_risk_score],
    ['blast radius', risk.blast_radius_risk_score],
  ];
  for (const [name, score] of components) {
    if (score === undefined) {
      items.push(unknownItem(`${name} risk: unknown`, `The payload did not carry a numeric ${name} risk score.`));
    } else {
      items.push({
        label: `${name} risk: ${fmtScore(score)}`,
        icon: 'circle-outline',
        tooltip: `Component score from the same prediction (${calibrationNote(risk.calibrated)}).`,
      });
    }
  }
  return items;
}

/** Runway section: forecast head item (+ reason codes), then ETA / burn
 * rate / current-used / hit leaves ONLY when the source field is non-null. */
export function runwayItems(session: SessionStatusSnapshot | undefined): SectionItem[] {
  if (!session) {
    return [noSessionItem()];
  }
  const rw = session.runway;
  if (!rw) {
    return [
      unknownItem(
        'runway: unknown — no forecast for this session yet',
        '`runway` is null in auspex.daemon.session_status.v1: no runway_forecasts row exists ' +
          'for this session. Rendered as unknown, never as a fabricated forecast.'
      ),
    ];
  }
  const items: SectionItem[] = [];
  items.push({
    label: `limit ${rw.limit_id}: risk ${fmtScore(rw.risk_score)}`,
    description: `${confidenceNote(rw.confidence)} · ${calibrationNote(rw.calibrated)}`,
    icon: rw.calibrated ? 'dashboard' : 'beaker',
    tooltip:
      `Runway forecast${rw.horizon_seconds !== undefined ? ` over ${formatDuration(rw.horizon_seconds)}` : ''}` +
      `${rw.quota_observed_at ? `, quota observed at ${rw.quota_observed_at}` : ''}.\n` +
      (rw.calibrated
        ? 'calibrated: true.'
        : 'calibrated: false — every number below is an uncalibrated estimate (Constitution principle #2).'),
    children: reasonCodeChildren(rw.reason_codes),
  });
  const p50 = rw.estimated_time_to_limit_p50_seconds;
  const p90 = rw.estimated_time_to_limit_p90_seconds;
  if (p50 !== undefined || p90 !== undefined) {
    const parts: string[] = [];
    if (p50 !== undefined) {
      parts.push(`p50 ~${formatDuration(p50)}`);
    }
    if (p90 !== undefined) {
      parts.push(`p90 ~${formatDuration(p90)}`);
    }
    items.push({
      label: `time to limit: ${parts.join(' · ')}`,
      description: rw.calibrated ? undefined : 'uncalibrated estimate',
      icon: 'watch',
      tooltip: 'estimated_time_to_limit_p50/p90_seconds from the persisted forecast.',
    });
  }
  if (rw.burn_rate_p50 !== undefined || rw.burn_rate_p90 !== undefined) {
    const parts: string[] = [];
    if (rw.burn_rate_p50 !== undefined) {
      parts.push(`p50 ${fmtScore(rw.burn_rate_p50)}%/min`);
    }
    if (rw.burn_rate_p90 !== undefined) {
      parts.push(`p90 ${fmtScore(rw.burn_rate_p90)}%/min`);
    }
    items.push({
      label: `burn rate: ${parts.join(' · ')}`,
      description: rw.calibrated ? undefined : 'uncalibrated estimate',
      icon: 'flame',
      tooltip:
        'Percentage points of quota consumed per minute ' +
        '(internal/predictor/runway estimateBurnRate: Δused_percent / Δminutes).',
    });
  }
  if (rw.current_used_percent !== undefined) {
    items.push({
      label: `used now: ${fmtPercent(rw.current_used_percent)}`,
      icon: 'pulse',
      tooltip: 'current_used_percent as of the forecast’s quota observation.',
    });
  }
  if (rw.hit_probability !== undefined) {
    items.push({
      label: rw.calibrated
        ? `hit probability: ${fmtScore(rw.hit_probability)}`
        : `hit estimate: ${fmtScore(rw.hit_probability)} (uncalibrated)`,
      icon: rw.calibrated ? 'graph' : 'beaker',
      tooltip: rw.calibrated
        ? 'Calibrated probability of hitting the limit within the horizon.'
        : 'calibrated: false — an uncalibrated score, NOT a probability (Constitution principle #2).',
    });
  }
  return items;
}

/** Quota-freshness section: one item per limit window; flags stale ages. */
export function quotaItems(session: SessionStatusSnapshot | undefined): SectionItem[] {
  if (!session) {
    return [noSessionItem()];
  }
  const quota = session.quota;
  if (quota.windows.length === 0) {
    return [
      unknownItem(
        'no quota observations yet',
        '`quota.windows` is empty: the daemon has not recorded any provider quota events for ' +
          'this session. Rendered as no-data, never as 0% used.'
      ),
    ];
  }
  return quota.windows.map((w) => {
    const used = w.used_percent !== undefined ? `${fmtPercent(w.used_percent)} used` : 'used: unknown';
    const stale = w.age_seconds !== undefined && w.age_seconds > QUOTA_STALE_AFTER_SECONDS;
    const age = w.age_seconds !== undefined ? `age ${formatDuration(w.age_seconds)}` : 'age unknown';
    return {
      label: `${w.limit_id}: ${used}`,
      description: stale ? `${age} (stale)` : age,
      icon: stale ? 'warning' : 'pulse',
      tooltip: [
        `Latest observation for limit window \`${w.limit_id}\`.`,
        `- observed_at: ${w.observed_at || 'unknown'}`,
        `- resets_at: ${w.resets_at ?? 'unknown'}`,
        `- age: ${w.age_seconds !== undefined ? `${w.age_seconds}s (server-computed: as_of − observed_at)` : 'unknown'}`,
        `- as_of (server clock): ${quota.as_of || 'unknown'}`,
        stale
          ? `Flagged stale because age exceeds ${QUOTA_STALE_AFTER_SECONDS}s — a display-only threshold of this extension, not a server judgement.`
          : undefined,
      ]
        .filter((l): l is string => l !== undefined)
        .join('\n'),
    };
  });
}

/** Progress section: task summary + the node hierarchy when non-empty. */
export function progressItems(session: SessionStatusSnapshot | undefined): SectionItem[] {
  if (!session) {
    return [noSessionItem()];
  }
  const progress = session.progress;
  if (progress.task_id === undefined) {
    return [
      unknownItem(
        'no active task for this session yet',
        '`progress.task_id` is null: the session has not been linked to a task, so there is no ' +
          'progress tree to show.'
      ),
    ];
  }
  if (progress.nodes.length === 0) {
    return [
      unknownItem(
        `task ${progress.task_id}: progress tree is empty`,
        '`progress.nodes` is empty — nothing has recorded progress rows for this task yet ' +
          '(the common state today). An empty tree is honest no-data, not "0% done".'
      ),
    ];
  }
  const summary: SectionItem = {
    label: `task ${progress.task_id}`,
    description: `${progress.nodes.length} node${progress.nodes.length === 1 ? '' : 's'} · ${progress.edges.length} edge${progress.edges.length === 1 ? '' : 's'}`,
    icon: 'list-tree',
    tooltip:
      'Progress-tree snapshot (structural fields only — node titles/descriptions are omitted ' +
      'server-side per FR-171 content exclusion).',
  };
  return [summary, ...progressHierarchy(progress.nodes)];
}

/** Build the parent_id hierarchy; orphans (parent not in the payload)
 * render as roots rather than being dropped. Siblings sort by ordinal. */
function progressHierarchy(nodes: SessionProgressNode[]): SectionItem[] {
  const byId = new Map<string, SessionProgressNode>();
  for (const n of nodes) {
    byId.set(n.id, n);
  }
  const childrenOf = new Map<string, SessionProgressNode[]>();
  const roots: SessionProgressNode[] = [];
  for (const n of nodes) {
    if (n.parent_id !== undefined && byId.has(n.parent_id)) {
      const siblings = childrenOf.get(n.parent_id) ?? [];
      siblings.push(n);
      childrenOf.set(n.parent_id, siblings);
    } else {
      roots.push(n);
    }
  }
  const byOrdinal = (a: SessionProgressNode, b: SessionProgressNode): number =>
    (a.ordinal ?? 0) - (b.ordinal ?? 0);
  const toItem = (n: SessionProgressNode): SectionItem => ({
    label: `${n.kind || 'node'} · ${n.status}`,
    description: n.ordinal !== undefined ? `#${n.ordinal}` : undefined,
    icon: iconForNodeStatus(n.status),
    tooltip: [
      `Progress node \`${n.id}\`${n.version !== undefined ? ` (v${n.version})` : ''}.`,
      `- status: ${n.status}`,
      `- started_at: ${n.started_at ?? 'not started'}`,
      `- completed_at: ${n.completed_at ?? 'not completed'}`,
      `- updated_at: ${n.updated_at || 'unknown'}`,
      'Title/description are omitted server-side (FR-171).',
    ].join('\n'),
    children: (childrenOf.get(n.id) ?? []).sort(byOrdinal).map(toItem),
  });
  return roots.sort(byOrdinal).map(toItem);
}

/** domain.ProgressNodeStatus wire strings (internal/domain/status.go). */
function iconForNodeStatus(status: string): string {
  switch (status) {
    case 'pending':
      return 'circle-outline';
    case 'ready':
      return 'circle-large-outline';
    case 'in_progress':
      return 'sync';
    case 'checkpointing':
      return 'save';
    case 'paused':
      return 'debug-pause';
    case 'completed':
      return 'check';
    case 'failed':
      return 'error';
    case 'skipped':
      return 'debug-step-over';
    case 'blocked':
      return 'circle-slash';
    default:
      return 'question';
  }
}

/** Checkpoint section: latest state checkpoint + linked repo checkpoint. */
export function checkpointItems(session: SessionStatusSnapshot | undefined): SectionItem[] {
  if (!session) {
    return [noSessionItem()];
  }
  const ck = session.checkpoint;
  if (!ck) {
    return [
      unknownItem(
        'no checkpoint for this session yet',
        '`checkpoint` is null in auspex.daemon.session_status.v1: the session’s task has no ' +
          'state checkpoint rows (or no task at all). Rendered as no-data.'
      ),
    ];
  }
  const items: SectionItem[] = [];
  const state = ck.state;
  if (state) {
    items.push({
      label: `state checkpoint ${shortId(state.id)}`,
      description: state.created_at || undefined,
      icon: 'save',
      tooltip: [
        `Latest state checkpoint \`${state.id}\` (task \`${state.task_id}\`).`,
        `- progress_tree_version: ${state.progress_tree_version ?? 'unknown'}`,
        `- active_node: ${state.active_node_id ?? 'none'}`,
        `- completion_node: ${state.completion_node_id ?? 'none'}`,
        `- integrity_sha256: ${state.integrity_sha256 || 'unknown'}`,
        `- repository_checkpoint: ${state.repository_checkpoint_id ?? 'none linked'}`,
      ].join('\n'),
    });
  } else {
    items.push(unknownItem('state checkpoint: unknown', 'The payload carried no parseable state checkpoint.'));
  }
  const repo = ck.repository;
  if (repo) {
    items.push({
      label: `repository @ ${shortId(repo.git_head)}`,
      description: `${repo.status || '?'} · ${repo.recoverability || '?'}`,
      icon: 'git-commit',
      tooltip: [
        `Repository checkpoint \`${repo.id}\`.`,
        `- git_head: ${repo.git_head || 'unknown'}`,
        `- index_diff_hash: ${repo.index_diff_hash || 'unknown'}`,
        `- worktree_diff_hash: ${repo.worktree_diff_hash || 'unknown'}`,
        `- total_bytes: ${repo.total_bytes ?? 'unknown'}`,
        `- created_at: ${repo.created_at || 'unknown'}`,
        `- verified_at: ${repo.verified_at ?? 'not verified'}`,
      ].join('\n'),
    });
  } else {
    items.push({
      label: 'repository checkpoint: none linked',
      icon: 'info',
      tooltip: 'The state checkpoint references no repository checkpoint (`repository` is null).',
    });
  }
  return items;
}

/** Pause section: the pause record + its wake jobs (FR-163 cancel wiring
 * is carried on the wake-job items’ id/contextValue). */
export function pauseItems(session: SessionStatusSnapshot | undefined): SectionItem[] {
  if (!session) {
    return [noSessionItem()];
  }
  const pause = session.pause;
  if (!pause) {
    return [
      unknownItem(
        'no pause record for this session yet',
        '`pause` is null in auspex.daemon.session_status.v1: the session has never entered the ' +
          'pause lifecycle. Rendered as no-data.'
      ),
    ];
  }
  const items: SectionItem[] = [];
  items.push({
    label: `pause: ${pause.status}`,
    description: `auto-resume ${pause.auto_resume_enabled ? 'on' : 'off'}`,
    icon: iconForPauseStatus(pause.status),
    tooltip: [
      `Pause record \`${pause.id}\` (task \`${pause.task_id}\`${pause.turn_id ? `, turn \`${pause.turn_id}\`` : ''}).`,
      `- status: ${pause.status}`,
      `- requested_at: ${pause.requested_at || 'unknown'}`,
      `- safe_point_at: ${pause.safe_point_at ?? 'not reached'}`,
      `- paused_at: ${pause.paused_at ?? 'not paused'}`,
      `- expected_reset_at: ${pause.expected_reset_at ?? 'unknown'}`,
      `- cancelled_at: ${pause.cancelled_at ?? 'not cancelled'}`,
      `- failure_code: ${pause.failure_code ?? 'none'}`,
      `- runway_forecast: \`${pause.runway_forecast_id || '?'}\``,
      `- state_checkpoint: ${pause.state_checkpoint_id ?? 'none'}`,
      `- repository_checkpoint: ${pause.repository_checkpoint_id ?? 'none'}`,
    ].join('\n'),
  });
  if (pause.wake_jobs.length === 0) {
    items.push({
      label: 'no wake jobs for this pause',
      icon: 'info',
      tooltip: '`pause.wake_jobs` is empty: nothing is scheduled against this pause record.',
    });
  } else {
    for (const job of pause.wake_jobs) {
      items.push(wakeJobItem(job));
    }
  }
  return items;
}

/**
 * One wake job as a section item. contextValue matches the Scheduled wake
 * jobs section, so the inline FR-163 Cancel button (gated on
 * viewItem == auspex.job.scheduled) works here too. The id uses a
 * DIFFERENT prefix ("auspex.pausejob.") because the same job also renders
 * in the Scheduled wake jobs section and vscode requires TreeItem ids to
 * be unique across the whole tree; extension.ts strips either prefix.
 */
export function wakeJobItem(job: JobView): SectionItem {
  return {
    label: `${job.kind} · ${job.status}`,
    id: `auspex.pausejob.${job.id}`,
    description: `run_after ${job.run_after}`,
    icon: iconForJobStatus(job.status),
    contextValue: job.status === 'scheduled' ? 'auspex.job.scheduled' : `auspex.job.${job.status}`,
    tooltip: [
      `Wake job \`${job.id}\` (pause \`${job.pause_id}\`).`,
      `- status: ${job.status}`,
      `- run_after: ${job.run_after}`,
      `- attempts: ${job.attempts}/${job.max_attempts}`,
      job.lease_owner ? `- lease: ${job.lease_owner} until ${job.lease_expires_at ?? '?'}` : undefined,
      job.last_error ? `- last_error: ${job.last_error}` : undefined,
    ]
      .filter((l): l is string => l !== undefined)
      .join('\n'),
  };
}

/** Scheduler statuses (internal/scheduler/lease.go). */
export function iconForJobStatus(status: string): string {
  switch (status) {
    case 'scheduled':
      return 'clock';
    case 'leased':
      return 'sync';
    case 'done':
      return 'check';
    case 'dead':
      return 'error';
    default:
      return 'question';
  }
}

/** domain.PauseStatus wire strings (internal/domain/status.go). */
function iconForPauseStatus(status: string): string {
  switch (status) {
    case 'predicted':
      return 'lightbulb';
    case 'requested':
    case 'quiescing':
    case 'checkpointing':
    case 'interrupting':
      return 'loading';
    case 'sleeping':
      return 'debug-pause';
    case 'wake_pending':
    case 'validating':
    case 'resuming':
      return 'sync';
    case 'resumed':
      return 'check';
    case 'blocked_conflict':
      return 'warning';
    case 'cancelled':
      return 'circle-slash';
    case 'failed':
      return 'error';
    default:
      return 'question';
  }
}

/** "2h 5m" / "3m 20s" / "42s" — shared by uptime, ETA, and quota ages. */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) {
    return `${h}h ${m}m`;
  }
  if (m > 0) {
    return `${m}m ${sec}s`;
  }
  return `${sec}s`;
}

function shortId(id: string): string {
  if (id === '') {
    return '?';
  }
  return id.length > 12 ? `${id.slice(0, 12)}…` : id;
}
