/**
 * tree.ts — the "Auspex" activity-bar tree view (FR-162).
 *
 * Honesty rule (repo discipline): every rendered value comes from a field
 * the daemon API actually serves. Since PR #84 the daemon exposes the
 * FR-162 per-session read-model (GET /v1/session/status, schema
 * auspex.daemon.session_status.v1 — internal/sessionstatus/snapshot.go),
 * so the Risk / Runway / Quota freshness / Progress / Checkpoints / Pause
 * sections render real data. The honesty invariant carries through:
 * sections the server serves as JSON null (no prediction, no forecast, no
 * checkpoint, no pause yet) render as explicit "unknown / no data yet"
 * items — never fabricated zeros — and uncalibrated scores are labelled
 * estimates, not probabilities (Constitution §7). The rendering logic
 * lives in sections.ts (vscode-free, unit-tested); this module only maps
 * those plain items onto vscode.TreeItem.
 */

import * as vscode from 'vscode';

import {
  SectionItem,
  checkpointItems,
  formatDuration,
  iconForJobStatus,
  pauseItems,
  progressItems,
  quotaItems,
  riskItems,
  runwayItems,
} from './sections';
import { JobView, ProtocolEvent, SessionStatusSnapshot, StatusResponse } from './types';

export { formatDuration } from './sections';

/** The controller state the tree renders from (one immutable snapshot). */
export interface ViewState {
  /** undefined => daemon not running / unreachable (the NORMAL cold state). */
  status: StatusResponse | undefined;
  jobs: JobView[];
  /** undefined => no session recorded yet (404 — a normal state) or the
   * session endpoint was unavailable; sections render "no data yet". */
  session: SessionStatusSnapshot | undefined;
  address: string | undefined;
  /** Last SSE event observed this session (live view only; broker.go has no replay). */
  lastEvent: { type: string; event: ProtocolEvent | undefined; receivedAt: Date } | undefined;
  /** Human-readable connection note ("not running", "unreachable", ...). */
  connectionNote: string;
}

type Section = 'status' | 'risk' | 'runway' | 'quota' | 'progress' | 'checkpoints' | 'pause' | 'jobs';

const SECTION_LABELS: Record<Section, string> = {
  status: 'Status',
  risk: 'Risk',
  runway: 'Runway',
  quota: 'Quota freshness',
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

export class AuspexTreeProvider implements vscode.TreeDataProvider<Node> {
  private readonly emitter = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.emitter.event;

  private state: ViewState = {
    status: undefined,
    jobs: [],
    session: undefined,
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
      case 'risk':
        node.children = this.sessionSection(riskItems);
        break;
      case 'runway':
        node.children = this.sessionSection(runwayItems);
        break;
      case 'quota':
        node.children = this.sessionSection(quotaItems);
        break;
      case 'progress':
        node.children = this.sessionSection(progressItems);
        break;
      case 'checkpoints':
        node.children = this.sessionSection(checkpointItems);
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

  /**
   * A session-scoped FR-162 section: daemon-down renders the cold-state
   * leaf; otherwise the (vscode-free) builder decides between real data
   * and its honest "no data yet" items.
   */
  private sessionSection(build: (s: SessionStatusSnapshot | undefined) => SectionItem[]): Node[] {
    if (!this.state.status) {
      return [gapLeaf(`daemon: ${this.state.connectionNote}`, 'Start it with: auspex daemon run. This is a normal state, not an error.')];
    }
    return build(this.state.session).map(toNode);
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
    // The session the FR-162 sections below are scoped to (most recent).
    if (this.state.session) {
      const sessionLeaf = new Node(`session: ${this.state.session.session_id}`);
      sessionLeaf.iconPath = new vscode.ThemeIcon('person');
      sessionLeaf.tooltip =
        'Most recent session (GET /v1/session/status, auspex.daemon.session_status.v1). ' +
        'The Risk / Runway / Quota freshness / Progress / Checkpoints / Pause sections are scoped to it.';
      children.push(sessionLeaf);
    } else {
      const sessionLeaf = new Node('session: none recorded yet');
      sessionLeaf.iconPath = new vscode.ThemeIcon('info');
      sessionLeaf.tooltip =
        'GET /v1/session/status answered 404: the daemon has not recorded any session yet ' +
        '(or the endpoint was unavailable — see the Auspex output channel). A normal state.';
      children.push(sessionLeaf);
    }
    return children;
  }

  private pauseChildren(): Node[] {
    const children: Node[] = [];
    // The live, session-local SSE signal (pkg/protocol/v1 event.go;
    // internal/daemon/worker.go publish()) — kept alongside the persisted
    // pause record because the broker has no history.
    const last = this.state.lastEvent;
    if (last && last.type.startsWith('pause.')) {
      const leaf = new Node(`last pause event: ${last.type}`);
      leaf.description = last.receivedAt.toLocaleTimeString();
      leaf.iconPath = new vscode.ThemeIcon('broadcast');
      const pauseID = last.event?.Payload?.['pause_id'];
      leaf.tooltip = `Live SSE event from the daemon worker${typeof pauseID === 'string' ? ` (pause ${pauseID})` : ''}. The broker keeps no history; this is session-local.`;
      children.push(leaf);
    }
    children.push(...this.sessionSection(pauseItems));
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

/** Map one vscode-free SectionItem (sections.ts) onto a TreeItem. */
function toNode(item: SectionItem): Node {
  const hasChildren = item.children !== undefined && item.children.length > 0;
  const node = new Node(
    item.label,
    hasChildren ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.None
  );
  if (item.description !== undefined) {
    node.description = item.description;
  }
  if (item.tooltip !== undefined) {
    node.tooltip = item.tooltip;
  }
  if (item.icon !== undefined) {
    node.iconPath = new vscode.ThemeIcon(item.icon);
  }
  if (item.id !== undefined) {
    node.id = item.id;
  }
  if (item.contextValue !== undefined) {
    node.contextValue = item.contextValue;
  }
  if (hasChildren) {
    node.children = (item.children ?? []).map(toNode);
  }
  return node;
}

function gapLeaf(label: string, tooltip: string): Node {
  const leaf = new Node(label);
  leaf.iconPath = new vscode.ThemeIcon('info');
  leaf.tooltip = tooltip;
  return leaf;
}
