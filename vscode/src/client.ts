/**
 * client.ts — the Auspex daemon client: discovery, authenticated HTTP,
 * and the reconnecting SSE subscription.
 *
 * Deliberately free of any `vscode` import so the pure logic is unit- and
 * smoke-testable under plain Node (node --test / a script against a real
 * daemon). Uses only Node built-ins (fs, fetch) — no third-party HTTP
 * libraries.
 *
 * Discovery protocol (the §23.3 probe order the CLI's `daemon status`
 * also follows — internal/orchestrator/daemon.go DaemonStatus):
 *
 *   1. resolve the per-OS runtime dir (paths.ts, mirroring
 *      internal/paths/paths.go);
 *   2. read <runtime>/daemon.json (internal/daemon/metadata.go) — a
 *      missing file is the ORDINARY cold state ("daemon not running"),
 *      never an error;
 *   3. read the bearer token from the metadata's token_file
 *      (<data>/daemon.token, internal/daemon/token.go, D-16);
 *   4. talk to http://<metadata.address>/v1/... with
 *      `Authorization: Bearer <token>`.
 *
 * FR-164: this module reads ONLY Auspex's own files (daemon.json,
 * daemon.token) and talks ONLY to the Auspex daemon's loopback API. It
 * never touches another extension's storage or any provider credential.
 */

import * as fs from 'node:fs/promises';
import * as os from 'node:os';

import { resolveDirs, Dirs, Env } from './paths';
import { Backoff, SSEParser } from './sse';
import {
  CapabilitiesResponse,
  DaemonMetadata,
  ErrorEnvelope,
  HealthResponse,
  JobView,
  ProtocolEvent,
  StatusResponse,
  parseJobsResponse,
  parseJobView,
  parseMetadata,
  parseStatusResponse,
} from './types';

/** Env backed by the real process — the production counterpart of paths.go's OSEnv. */
export const processEnv: Env = {
  getenv: (key) => process.env[key] ?? '',
  homedir: () => {
    try {
      return os.homedir() ?? '';
    } catch {
      return '';
    }
  },
};

/** Resolve the host's Auspex dirs (paths.go ResolveHost equivalent). */
export function hostDirs(env: Env = processEnv): Dirs {
  return resolveDirs(process.platform, env);
}

/** A discovered, ready-to-call daemon connection. */
export interface DaemonConnection {
  metadata: DaemonMetadata;
  token: string;
}

/**
 * Error thrown when the daemon answered with the §23.5 error envelope
 * (internal/httpapi/httpapi.go writeError).
 */
export class DaemonApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly retryable: boolean = false
  ) {
    super(message);
    this.name = 'DaemonApiError';
  }
}

/**
 * Discover a running daemon via the runtime metadata file. Returns
 * undefined when no daemon has published metadata OR the token file is
 * unreadable — both are normal not-running/stale states the UI renders
 * calmly (never an error popup), matching DaemonStatus's found=false and
 * "token_unreadable" outcomes.
 */
export async function discoverDaemon(runtimeDir: string): Promise<DaemonConnection | undefined> {
  let raw: string;
  try {
    raw = await fs.readFile(`${runtimeDir}/daemon.json`, 'utf8');
  } catch {
    return undefined; // ordinary cold state (metadata.go: missing == not running)
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return undefined; // torn/foreign file; treat as not running
  }
  const metadata = parseMetadata(parsed);
  if (!metadata) {
    return undefined; // wrong schema_version — a daemon we don't speak to
  }
  let token: string;
  try {
    token = (await fs.readFile(metadata.token_file, 'utf8')).trim();
  } catch {
    return undefined; // token unreadable: stale metadata or permissions
  }
  if (token === '') {
    return undefined;
  }
  return { metadata, token };
}

/** Timeout for ordinary (non-SSE) requests, mirroring the CLI probe's 2s. */
const REQUEST_TIMEOUT_MS = 5000;

async function request(conn: DaemonConnection, method: string, path: string): Promise<unknown> {
  const resp = await fetch(`http://${conn.metadata.address}${path}`, {
    method,
    headers: { Authorization: `Bearer ${conn.token}` },
    signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
  });
  const body: unknown = await resp.json().catch(() => undefined);
  if (!resp.ok) {
    const envelope = body as ErrorEnvelope | undefined;
    if (envelope && typeof envelope === 'object' && envelope.error) {
      throw new DaemonApiError(
        resp.status,
        envelope.error.code,
        envelope.error.message,
        envelope.error.retryable
      );
    }
    throw new DaemonApiError(resp.status, 'AUSPEX_INTERNAL', `HTTP ${resp.status} from ${path}`);
  }
  return body;
}

/** GET /v1/health. */
export async function getHealth(conn: DaemonConnection): Promise<HealthResponse> {
  return (await request(conn, 'GET', '/v1/health')) as HealthResponse;
}

/** GET /v1/capabilities. */
export async function getCapabilities(conn: DaemonConnection): Promise<CapabilitiesResponse> {
  return (await request(conn, 'GET', '/v1/capabilities')) as CapabilitiesResponse;
}

/** GET /v1/status (parsed defensively — see types.parseStatusResponse). */
export async function getStatus(conn: DaemonConnection): Promise<StatusResponse | undefined> {
  return parseStatusResponse(await request(conn, 'GET', '/v1/status'));
}

/** GET /v1/scheduler/jobs. */
export async function getJobs(conn: DaemonConnection): Promise<JobView[]> {
  return parseJobsResponse(await request(conn, 'GET', '/v1/scheduler/jobs')) ?? [];
}

/** Raw GET, for the "Show Raw Status" command (pretty-printed verbatim). */
export async function getRaw(conn: DaemonConnection, path: string): Promise<unknown> {
  return request(conn, 'GET', path);
}

/**
 * POST /v1/scheduler/jobs/{id}/cancel (FR-163). Resolves to the updated
 * job (status "dead", last_error "cancelled by operator" —
 * internal/scheduler/cancel.go); throws DaemonApiError with code
 * AUSPEX_CONFLICT when the job was already leased/done/dead, or
 * AUSPEX_NOT_FOUND for an unknown id.
 */
export async function cancelJob(conn: DaemonConnection, jobID: string): Promise<JobView | undefined> {
  const body = (await request(
    conn,
    'POST',
    `/v1/scheduler/jobs/${encodeURIComponent(jobID)}/cancel`
  )) as { job?: unknown };
  return parseJobView(body?.job);
}

/** Options for the reconnecting event stream. */
export interface EventStreamOptions {
  /** Called for each decoded protocol event. */
  onEvent: (eventType: string, event: ProtocolEvent | undefined) => void;
  /** Called when a connection attempt fails or the stream drops. */
  onDisconnect?: (error: unknown) => void;
  /** Called when the stream (re)connects successfully. */
  onConnect?: () => void;
}

/**
 * A reconnecting subscription to GET /v1/events/stream.
 *
 * The broker keeps no history (internal/daemon/broker.go), so there is no
 * Last-Event-ID to resume from — on reconnect the caller re-reads current
 * state from the status/jobs endpoints (the controller does this in its
 * onConnect). Backoff is exponential (1s..30s) and resets after a
 * connection that delivered bytes.
 */
export class EventStream {
  private stopped = false;
  private abort: AbortController | undefined;
  private timer: NodeJS.Timeout | undefined;
  private readonly backoff = new Backoff();

  constructor(
    private readonly connect: () => Promise<DaemonConnection | undefined>,
    private readonly options: EventStreamOptions
  ) {}

  start(): void {
    this.stopped = false;
    void this.loop();
  }

  stop(): void {
    this.stopped = true;
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    this.abort?.abort();
  }

  private async loop(): Promise<void> {
    while (!this.stopped) {
      const conn = await this.connect();
      if (!conn) {
        // Daemon not running: poll for its appearance on the same backoff
        // schedule rather than burning a hot loop.
        await this.sleep(this.backoff.nextDelayMs());
        continue;
      }
      let deliveredBytes = false;
      let reportedDisconnect = false;
      try {
        this.abort = new AbortController();
        const resp = await fetch(`http://${conn.metadata.address}/v1/events/stream`, {
          headers: { Authorization: `Bearer ${conn.token}` },
          signal: this.abort.signal,
        });
        if (!resp.ok || !resp.body) {
          throw new DaemonApiError(resp.status, 'AUSPEX_UNAUTHORIZED', `SSE connect: HTTP ${resp.status}`);
        }
        this.options.onConnect?.();
        const parser = new SSEParser((msg) => {
          let event: ProtocolEvent | undefined;
          try {
            event = JSON.parse(msg.data) as ProtocolEvent;
          } catch {
            event = undefined; // still surface the event TYPE from the SSE field
          }
          this.options.onEvent(msg.event, event);
        });
        const decoder = new TextDecoder();
        for await (const chunk of resp.body as unknown as AsyncIterable<Uint8Array>) {
          deliveredBytes = true;
          this.backoff.reset();
          parser.feed(decoder.decode(chunk, { stream: true }));
          if (this.stopped) {
            break;
          }
        }
      } catch (err) {
        if (!this.stopped) {
          this.options.onDisconnect?.(err);
          reportedDisconnect = true;
        }
      } finally {
        this.abort = undefined;
      }
      if (this.stopped) {
        return;
      }
      if (deliveredBytes && !reportedDisconnect) {
        // The stream ended without an error (server shutdown cut it —
        // "they reconnect, that is their protocol", daemon.go).
        this.options.onDisconnect?.(new Error('event stream ended'));
      }
      await this.sleep(this.backoff.nextDelayMs());
    }
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => {
      this.timer = setTimeout(() => {
        this.timer = undefined;
        resolve();
      }, ms);
    });
  }
}
