/**
 * extension.ts — activation, the status bar item, polling + SSE-driven
 * refresh, and the command palette surface (issue #10; ADD §8.4).
 *
 * FR coverage:
 *  - FR-162: status bar + tree view render what GET /v1/status and
 *    GET /v1/scheduler/jobs actually serve (tree.ts documents the honest
 *    gaps for fields the API does not expose yet).
 *  - FR-163: "Auspex: Cancel Scheduled Resume" and the inline tree-item
 *    Cancel both POST /v1/scheduler/jobs/{id}/cancel.
 *  - FR-164: the extension reads ONLY Auspex's own files (daemon.json,
 *    daemon.token — client.ts) and talks ONLY to the daemon's loopback
 *    API. It never reads another extension's private state and uses no
 *    vscode.extensions state access anywhere.
 *
 * Daemon-not-running is a NORMAL state: rendered as
 * "auspex: not running" in the status bar and tree — never an error
 * popup. Errors are logged to the "Auspex" output channel.
 */

import * as vscode from 'vscode';

import {
  cancelJob,
  DaemonApiError,
  DaemonConnection,
  discoverDaemon,
  EventStream,
  getJobs,
  getRaw,
  getStatus,
  hostDirs,
} from './client';
import { AuspexTreeProvider, ViewState } from './tree';
import { CANCELLED_BY_OPERATOR, JobView, ProtocolEvent, StatusResponse } from './types';

/** Poll interval for status/jobs between SSE pushes. SSE is the fast
 * path; polling is the safety net (and the only path while the stream is
 * reconnecting). */
const POLL_INTERVAL_MS = 15_000;

class AuspexController implements vscode.Disposable {
  private readonly output = vscode.window.createOutputChannel('Auspex');
  private readonly statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 90);
  private readonly tree = new AuspexTreeProvider();
  private readonly runtimeDir: string;
  private stream: EventStream | undefined;
  private pollTimer: NodeJS.Timeout | undefined;
  private refreshing = false;

  private state: ViewState = {
    status: undefined,
    jobs: [],
    address: undefined,
    lastEvent: undefined,
    connectionNote: 'discovering…',
  };

  constructor(context: vscode.ExtensionContext) {
    this.runtimeDir = hostDirs().runtime;
    this.output.appendLine(`Auspex runtime dir: ${this.runtimeDir} (mirrors internal/paths/paths.go)`);

    context.subscriptions.push(
      this.output,
      this.statusBar,
      vscode.window.registerTreeDataProvider('auspexStatus', this.tree),
      vscode.commands.registerCommand('auspex.refresh', () => this.refresh()),
      vscode.commands.registerCommand('auspex.showRawStatus', () => this.showRawStatus()),
      vscode.commands.registerCommand('auspex.cancelScheduledResume', () => this.cancelViaQuickPick()),
      vscode.commands.registerCommand('auspex.cancelJob', (item?: { id?: string }) =>
        this.cancelFromTreeItem(item)
      ),
      this
    );

    this.statusBar.name = 'Auspex';
    this.statusBar.command = 'auspexStatus.focus';
    this.statusBar.show();
    this.renderNotRunning('not running');

    // SSE keeps the view live; each (re)connect re-reads current state,
    // which is the broker's documented no-replay contract
    // (internal/daemon/broker.go: read status/jobs instead of history).
    this.stream = new EventStream(() => discoverDaemon(this.runtimeDir), {
      onConnect: () => {
        this.output.appendLine('event stream connected');
        void this.refresh();
      },
      onDisconnect: (err) => {
        this.output.appendLine(`event stream disconnected: ${String(err)}`);
        void this.refresh();
      },
      onEvent: (type, event) => this.onEvent(type, event),
    });
    this.stream.start();

    this.pollTimer = setInterval(() => void this.refresh(), POLL_INTERVAL_MS);
    void this.refresh();
  }

  dispose(): void {
    this.stream?.stop();
    if (this.pollTimer !== undefined) {
      clearInterval(this.pollTimer);
    }
  }

  private onEvent(type: string, event: ProtocolEvent | undefined): void {
    this.output.appendLine(`event: ${type}`);
    this.state = { ...this.state, lastEvent: { type, event, receivedAt: new Date() } };
    // Any worker event implies job/pause state moved — re-read it.
    void this.refresh();
  }

  /** Re-discover the daemon and re-read status + jobs. Never throws;
   * daemon-not-running renders calmly (FR-162's ordinary cold state). */
  async refresh(): Promise<void> {
    if (this.refreshing) {
      return; // coalesce bursts (SSE event storms, manual refresh spam)
    }
    this.refreshing = true;
    try {
      const conn = await discoverDaemon(this.runtimeDir);
      if (!conn) {
        this.renderNotRunning('not running');
        return;
      }
      let status: StatusResponse | undefined;
      let jobs: JobView[] = [];
      try {
        [status, jobs] = await Promise.all([getStatus(conn), getJobs(conn)]);
      } catch (err) {
        // Metadata present but the daemon did not answer: stale file
        // after a crash (metadata.go documents this state) — still not
        // an error popup, just a distinct note.
        this.output.appendLine(`daemon unreachable: ${String(err)}`);
        this.renderNotRunning('unreachable (stale metadata?)');
        return;
      }
      this.state = {
        ...this.state,
        status,
        jobs,
        address: conn.metadata.address,
        connectionNote: 'ok',
      };
      this.tree.setState(this.state);
      this.renderStatusBar();
    } finally {
      this.refreshing = false;
    }
  }

  private renderNotRunning(note: string): void {
    this.state = { ...this.state, status: undefined, jobs: [], address: undefined, connectionNote: note };
    this.tree.setState(this.state);
    this.statusBar.text = '$(circle-slash) auspex: not running';
    this.statusBar.tooltip = new vscode.MarkdownString(
      'Auspex daemon is not running (a normal state).\n\n' +
        'Start it with `auspex daemon run`.\n\n' +
        `Discovery: \`${this.runtimeDir}/daemon.json\` (internal/daemon/metadata.go)`
    );
    this.statusBar.backgroundColor = undefined;
  }

  private renderStatusBar(): void {
    const s = this.state.status;
    if (!s) {
      return;
    }
    const scheduled = s.jobs['scheduled'] ?? 0;
    const leased = s.jobs['leased'] ?? 0;
    const parts = [`$(pulse) auspex`];
    parts.push(scheduled > 0 ? `${scheduled} scheduled` : 'idle');
    if (leased > 0) {
      parts.push(`${leased} running`);
    }
    this.statusBar.text = parts.join(' · ');
    // FR-162 asks for policy action / risk / runway freshness here; the
    // status payload does not carry them yet (httpapi.go statusResponse),
    // so the tooltip says so instead of showing fabricated values.
    this.statusBar.tooltip = new vscode.MarkdownString(
      [
        `**Auspex daemon** v${s.version} at \`${this.state.address ?? '?'}\``,
        `- uptime: ${s.uptime_seconds}s`,
        `- wake jobs: ${JSON.stringify(s.jobs)}`,
        '',
        '_Risk / policy action / runway freshness are not exposed by the daemon API yet (issue #10 follow-up)._',
      ].join('\n')
    );
  }

  private async showRawStatus(): Promise<void> {
    const conn = await discoverDaemon(this.runtimeDir);
    if (!conn) {
      void vscode.window.showInformationMessage('Auspex daemon is not running (auspex daemon run).');
      return;
    }
    const raw: Record<string, unknown> = {};
    for (const path of ['/v1/health', '/v1/version', '/v1/capabilities', '/v1/status', '/v1/scheduler/jobs']) {
      try {
        raw[path] = await getRaw(conn, path);
      } catch (err) {
        raw[path] = { unreachable: String(err) };
      }
    }
    const doc = await vscode.workspace.openTextDocument({
      language: 'json',
      content: JSON.stringify(raw, null, 2),
    });
    await vscode.window.showTextDocument(doc, { preview: true });
  }

  /** FR-163 via the command palette: pick a scheduled job, cancel it. */
  private async cancelViaQuickPick(): Promise<void> {
    const conn = await discoverDaemon(this.runtimeDir);
    if (!conn) {
      void vscode.window.showInformationMessage('Auspex daemon is not running (auspex daemon run).');
      return;
    }
    let jobs: JobView[];
    try {
      jobs = await getJobs(conn);
    } catch (err) {
      this.output.appendLine(`list jobs failed: ${String(err)}`);
      void vscode.window.showWarningMessage('Auspex daemon did not answer; see the Auspex output channel.');
      return;
    }
    const scheduled = jobs.filter((j) => j.status === 'scheduled');
    if (scheduled.length === 0) {
      void vscode.window.showInformationMessage('No scheduled wake jobs to cancel.');
      return;
    }
    const picked = await vscode.window.showQuickPick(
      scheduled.map((j) => ({
        label: `${j.kind} — pause ${j.pause_id}`,
        description: `run_after ${j.run_after}`,
        detail: `job ${j.id} · attempts ${j.attempts}/${j.max_attempts}`,
        jobID: j.id,
      })),
      { title: 'Cancel which scheduled resume?' }
    );
    if (!picked) {
      return;
    }
    await this.doCancel(conn, picked.jobID);
  }

  /** FR-163 via the tree item's inline Cancel button. */
  private async cancelFromTreeItem(item?: { id?: string }): Promise<void> {
    // tree.ts sets TreeItem.id = "auspex.job.<jobID>".
    const jobID = item?.id?.startsWith('auspex.job.') ? item.id.slice('auspex.job.'.length) : undefined;
    if (!jobID) {
      return this.cancelViaQuickPick();
    }
    const conn = await discoverDaemon(this.runtimeDir);
    if (!conn) {
      void vscode.window.showInformationMessage('Auspex daemon is not running (auspex daemon run).');
      return;
    }
    await this.doCancel(conn, jobID);
  }

  private async doCancel(conn: DaemonConnection, jobID: string): Promise<void> {
    try {
      const job = await cancelJob(conn, jobID);
      const note =
        job?.last_error === CANCELLED_BY_OPERATOR
          ? `Cancelled wake job ${jobID}.`
          : `Wake job ${jobID} is now ${job?.status ?? 'unknown'}.`;
      void vscode.window.showInformationMessage(note);
    } catch (err) {
      if (err instanceof DaemonApiError && err.code === 'AUSPEX_CONFLICT') {
        // The claim-vs-cancel race resolved the other way
        // (scheduler/cancel.go): the job is already running or finished.
        void vscode.window.showWarningMessage(
          `Could not cancel ${jobID}: ${err.message}`
        );
      } else if (err instanceof DaemonApiError && err.code === 'AUSPEX_NOT_FOUND') {
        void vscode.window.showWarningMessage(`Wake job ${jobID} no longer exists.`);
      } else {
        this.output.appendLine(`cancel failed: ${String(err)}`);
        void vscode.window.showWarningMessage('Cancel failed; see the Auspex output channel.');
      }
    }
    await this.refresh();
  }
}

export function activate(context: vscode.ExtensionContext): void {
  new AuspexController(context);
}

export function deactivate(): void {
  // Disposal is handled via context.subscriptions (AuspexController.dispose).
}
