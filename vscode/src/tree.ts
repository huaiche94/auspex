/**
 * tree.ts — the "Auspex" activity-bar tree view (FR-162).
 *
 * Honesty rule (repo discipline): every rendered value comes from a field
 * the daemon API actually serves (internal/httpapi/httpapi.go). Where
 * FR-162 names data the current /v1/status payload
 * (auspex.daemon.status.v1) does not yet expose — risk scores, runway,
 * quota freshness, the progress-tree snapshot, checkpoints, pause-record
 * state — the section renders as an explicit "not exposed by the daemon
 * API yet" placeholder with a tooltip naming the gap, rather than
 * inventing an endpoint or fabricating values. Those gaps are listed as
 * follow-ups in the PR body for issue #10.
 */

import * as vscode from 'vscode';

import { JobView, ProtocolEvent, StatusResponse } from './types';

/** The controller state the tree renders from (one immutable snapshot). */
export interface ViewState {
  /** undefined => daemon not running / unreachable (the NORMAL cold state). */
  status: StatusResponse | undefined;
  jobs: JobView[];
  address: string | undefined;
  /** Last SSE event observed this session (live view only; broker.go has no replay). */
  lastEvent: { type: string; event: ProtocolEvent | undefined; receivedAt: Date } | undefined;
  /** Human-readable connection note ("not running", "unreachable", ...). */
  connectionNote: string;
}

type Section = 'status' | 'progress' | 'checkpoints' | 'pause' | 'jobs';

const SECTION_LABELS: Record<Section, string> = {
  status: 'Status',
  progress: 'Progress',
  checkpoints: 'Checkpoints',
  pause: 'Pause state',
  jobs: 'Scheduled wake jobs',
};

class Node extends vscode.TreeItem {
  children: Node[] = [];

  constructor(label: string, collapsible = vscode.TreeItemCollapsibleState.None) {
    super(label, collapsible);
  }
}

/** Tooltip used by every not-yet-exposed FR-162 section. */
function gapTooltip(what: string): string {
  return (
    `${what} is not exposed by the daemon API yet ` +
    '(GET /v1/status serves auspex.daemon.status.v1: version, uptime, wake-job counts only — ' +
    'internal/httpapi/httpapi.go). Tracked as an issue #10 follow-up; this extension does not invent data.'
  );
}

export class AuspexTreeProvider implements vscode.TreeDataProvider<Node> {
  private readonly emitter = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.emitter.event;

  private state: ViewState = {
    status: undefined,
    jobs: [],
    address: undefined,
    lastEvent: undefined,
    connectionNote: 'discovering…',
  };

  setState(state: ViewState): void {
    this.state = state;
    this.emitter.fire(undefined);
  }

  getTreeItem(element: Node): vscode.TreeItem {
    return element;
  }

  getChildren(element?: Node): Node[] {
    if (element) {
      return element.children;
    }
    return (Object.keys(SECTION_LABELS) as Section[]).map((s) => this.buildSection(s));
  }

  private buildSection(section: Section): Node {
    const node = new Node(SECTION_LABELS[section], vscode.TreeItemCollapsibleState.Expanded);
    node.contextValue = `auspex.section.${section}`;
    switch (section) {
      case 'status':
        node.children = this.statusChildren();
        break;
      case 'progress':
        node.children = [
          gapLeaf('No progress tree in the status payload yet', gapTooltip('The progress-tree snapshot')),
        ];
        break;
      case 'checkpoints':
        node.children = [
          gapLeaf('No checkpoint listing in the daemon API yet', gapTooltip('Checkpoint state')),
        ];
        break;
      case 'pause':
        node.children = this.pauseChildren();
        break;
      case 'jobs':
        node.children = this.jobChildren();
        break;
    }
    return node;
  }

  private statusChildren(): Node[] {
    const s = this.state.status;
    if (!s) {
      const leaf = new Node(`daemon: ${this.state.connectionNote}`);
      leaf.iconPath = new vscode.ThemeIcon('circle-slash');
      leaf.tooltip =
        'No daemon metadata found (or the daemon did not answer). Start it with: auspex daemon run. ' +
        'This is a normal state, not an error.';
      return [leaf];
    }
    const children: Node[] = [];
    const health = new Node(`daemon: ok (v${s.version})`);
    health.iconPath = new vscode.ThemeIcon('pass');
    health.description = this.state.address ?? '';
    children.push(health);
    const uptime = new Node(`uptime: ${formatDuration(s.uptime_seconds)}`);
    uptime.iconPath = new vscode.ThemeIcon('watch');
    uptime.tooltip = `started_at: ${s.started_at}`;
    children.push(uptime);
    const counts = Object.entries(s.jobs)
      .map(([k, v]) => `${k}: ${v}`)
      .sort()
      .join(', ');
    const jobsLeaf = new Node(`wake jobs: ${counts === '' ? 'none' : counts}`);
    jobsLeaf.iconPath = new vscode.ThemeIcon('list-ordered');
    children.push(jobsLeaf);
    // FR-162 names risk / runway / quota freshness — not in this payload.
    const gap = gapLeaf('risk / runway / quota freshness: not exposed yet', gapTooltip('Risk, runway, and quota freshness'));
    children.push(gap);
    return children;
  }

  private pauseChildren(): Node[] {
    const children: Node[] = [];
    // The daemon has no pause-record read endpoint yet; the only live,
    // API-sourced signal is the pause.* SSE events the worker publishes
    // (pkg/protocol/v1 event.go; internal/daemon/worker.go publish()).
    const last = this.state.lastEvent;
    if (last && last.type.startsWith('pause.')) {
      const leaf = new Node(`last pause event: ${last.type}`);
      leaf.description = last.receivedAt.toLocaleTimeString();
      leaf.iconPath = new vscode.ThemeIcon('broadcast');
      const pauseID = last.event?.Payload?.['pause_id'];
      leaf.tooltip = `Live SSE event from the daemon worker${typeof pauseID === 'string' ? ` (pause ${pauseID})` : ''}. The broker keeps no history; this is session-local.`;
      children.push(leaf);
    }
    children.push(
      gapLeaf('No pause-record endpoint in the daemon API yet', gapTooltip('Pause-record state'))
    );
    return children;
  }

  private jobChildren(): Node[] {
    if (!this.state.status) {
      return [gapLeaf('daemon not running', 'Start it with: auspex daemon run')];
    }
    if (this.state.jobs.length === 0) {
      return [gapLeaf('queue empty', 'GET /v1/scheduler/jobs returned no wake jobs.')];
    }
    return this.state.jobs.map((job) => {
      const leaf = new Node(`${job.kind} · ${job.status}`);
      leaf.id = `auspex.job.${job.id}`;
      leaf.description = `run_after ${job.run_after}`;
      leaf.tooltip = new vscode.MarkdownString(
        [
          `**Wake job** \`${job.id}\``,
          `- pause: \`${job.pause_id}\``,
          `- status: **${job.status}**`,
          `- run_after: ${job.run_after}`,
          `- attempts: ${job.attempts}/${job.max_attempts}`,
          job.lease_owner ? `- lease: ${job.lease_owner} until ${job.lease_expires_at ?? '?'}` : undefined,
          job.last_error ? `- last_error: ${job.last_error}` : undefined,
        ]
          .filter((l): l is string => l !== undefined)
          .join('\n')
      );
      leaf.iconPath = new vscode.ThemeIcon(iconForJobStatus(job.status));
      // Only 'scheduled' jobs are cancellable (scheduler/cancel.go): the
      // inline Cancel button is gated on this contextValue.
      leaf.contextValue = job.status === 'scheduled' ? 'auspex.job.scheduled' : `auspex.job.${job.status}`;
      return leaf;
    });
  }
}

function gapLeaf(label: string, tooltip: string): Node {
  const leaf = new Node(label);
  leaf.iconPath = new vscode.ThemeIcon('info');
  leaf.tooltip = tooltip;
  return leaf;
}

function iconForJobStatus(status: string): string {
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
