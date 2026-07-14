/**
 * types.ts — TypeScript mirrors of the daemon's wire shapes.
 *
 * Every field here exists in the Go handlers; nothing is invented. The
 * sources of truth, per response type:
 *
 *  - internal/httpapi/httpapi.go: healthResponse, versionResponse,
 *    capabilitiesResponse, statusResponse, jobsResponse/jobView,
 *    jobResponse (cancel), errorEnvelope/errorBody.
 *  - internal/daemon/metadata.go: Metadata (auspex.daemon.v1).
 *  - pkg/protocol/v1/event.go: Event — NOTE that the Go struct carries NO
 *    json tags, so encoding/json marshals it with Go's exported field
 *    names verbatim ("SchemaVersion", "EventType", "OccurredAt", ...).
 *    The SSE `data:` payload therefore uses PascalCase keys, unlike the
 *    snake_case REST responses.
 */

/** GET /v1/health → httpapi.healthResponse. */
export interface HealthResponse {
  schema_version: string; // "auspex.daemon.health.v1"
  status: string; // "ok"
  version: string;
}

/** GET /v1/version → httpapi.versionResponse. */
export interface VersionResponse {
  schema_version: string; // "auspex.daemon.version.v1"
  version: string;
  protocol_version: string; // "v1"
}

/** GET /v1/capabilities → httpapi.capabilitiesResponse. */
export interface CapabilitiesResponse {
  schema_version: string; // "auspex.daemon.capabilities.v1"
  endpoints: string[];
  sse: boolean;
}

/** GET /v1/status → httpapi.statusResponse. */
export interface StatusResponse {
  schema_version: string; // "auspex.daemon.status.v1"
  version: string;
  started_at: string; // RFC 3339
  uptime_seconds: number;
  /** Per-status wake-job counts (scheduler statuses: scheduled/leased/done/dead). */
  jobs: Record<string, number>;
}

/**
 * One wake job — httpapi.jobView. Status vocabulary is
 * internal/scheduler/lease.go's constants: "scheduled" | "leased" |
 * "done" | "dead".
 */
export interface JobView {
  id: string;
  pause_id: string;
  kind: string;
  status: string;
  run_after: string; // RFC 3339
  lease_owner?: string;
  lease_expires_at?: string;
  attempts: number;
  max_attempts: number;
  last_error?: string;
}

/** GET /v1/scheduler/jobs → httpapi.jobsResponse. */
export interface JobsResponse {
  schema_version: string; // "auspex.daemon.jobs.v1"
  jobs: JobView[];
}

/** POST /v1/scheduler/jobs/{id}/cancel → httpapi.jobResponse. */
export interface JobResponse {
  schema_version: string; // "auspex.daemon.job.v1"
  job: JobView;
}

/**
 * last_error value of an operator-cancelled job —
 * internal/scheduler/cancel.go CancelledByOperator. Used to distinguish
 * "cancelled" from "retries exhausted" among status=dead jobs.
 */
export const CANCELLED_BY_OPERATOR = 'cancelled by operator';

/** §23.5 error envelope — httpapi.errorEnvelope/errorBody. */
export interface ErrorEnvelope {
  error: {
    code: string; // "AUSPEX_NOT_FOUND", "AUSPEX_CONFLICT", ...
    message: string;
    retryable: boolean;
    details?: Record<string, string>;
  };
}

/**
 * <runtime>/daemon.json — internal/daemon/metadata.go Metadata
 * (schema "auspex.daemon.v1"). How a client discovers a running daemon.
 */
export interface DaemonMetadata {
  schema_version: string;
  pid: number;
  address: string; // loopback host:port to dial
  token_file: string; // absolute path to the bearer token (D-16)
  started_at: string;
  version: string;
}

export const METADATA_SCHEMA_VERSION = 'auspex.daemon.v1';

/**
 * SSE event payload — pkg/protocol/v1 Event, marshalled WITHOUT json tags
 * (PascalCase keys; see module doc). Only the fields the extension reads
 * are typed strictly; the rest stay available via the index signature.
 */
export interface ProtocolEvent {
  SchemaVersion: string; // "auspex.event.v1"
  EventID: string;
  EventType: string; // e.g. "pause.wake.triggered" (pkg/protocol/v1 EventType)
  OccurredAt: string;
  ObservedAt: string;
  Source: string; // "daemon" for worker-emitted events
  Payload: Record<string, unknown> | null;
  [key: string]: unknown;
}

/** Parse helpers: fail soft (return undefined) rather than throwing on
 * unexpected shapes — a daemon a few versions ahead/behind must degrade
 * to "unknown", never crash the extension host. */

export function parseStatusResponse(raw: unknown): StatusResponse | undefined {
  if (!isRecord(raw) || typeof raw.schema_version !== 'string') {
    return undefined;
  }
  if (raw.schema_version !== 'auspex.daemon.status.v1') {
    return undefined;
  }
  const jobs: Record<string, number> = {};
  if (isRecord(raw.jobs)) {
    for (const [k, v] of Object.entries(raw.jobs)) {
      if (typeof v === 'number') {
        jobs[k] = v;
      }
    }
  }
  return {
    schema_version: raw.schema_version,
    version: typeof raw.version === 'string' ? raw.version : '',
    started_at: typeof raw.started_at === 'string' ? raw.started_at : '',
    uptime_seconds: typeof raw.uptime_seconds === 'number' ? raw.uptime_seconds : 0,
    jobs,
  };
}

export function parseJobsResponse(raw: unknown): JobView[] | undefined {
  if (!isRecord(raw) || raw.schema_version !== 'auspex.daemon.jobs.v1' || !Array.isArray(raw.jobs)) {
    return undefined;
  }
  const jobs: JobView[] = [];
  for (const j of raw.jobs) {
    const view = parseJobView(j);
    if (view) {
      jobs.push(view);
    }
  }
  return jobs;
}

export function parseJobView(raw: unknown): JobView | undefined {
  if (!isRecord(raw) || typeof raw.id !== 'string' || typeof raw.status !== 'string') {
    return undefined;
  }
  return {
    id: raw.id,
    pause_id: typeof raw.pause_id === 'string' ? raw.pause_id : '',
    kind: typeof raw.kind === 'string' ? raw.kind : '',
    status: raw.status,
    run_after: typeof raw.run_after === 'string' ? raw.run_after : '',
    lease_owner: typeof raw.lease_owner === 'string' ? raw.lease_owner : undefined,
    lease_expires_at: typeof raw.lease_expires_at === 'string' ? raw.lease_expires_at : undefined,
    attempts: typeof raw.attempts === 'number' ? raw.attempts : 0,
    max_attempts: typeof raw.max_attempts === 'number' ? raw.max_attempts : 0,
    last_error: typeof raw.last_error === 'string' ? raw.last_error : undefined,
  };
}

export function parseMetadata(raw: unknown): DaemonMetadata | undefined {
  if (
    !isRecord(raw) ||
    raw.schema_version !== METADATA_SCHEMA_VERSION ||
    typeof raw.address !== 'string' ||
    typeof raw.token_file !== 'string'
  ) {
    return undefined;
  }
  return {
    schema_version: raw.schema_version,
    pid: typeof raw.pid === 'number' ? raw.pid : 0,
    address: raw.address,
    token_file: raw.token_file,
    started_at: typeof raw.started_at === 'string' ? raw.started_at : '',
    version: typeof raw.version === 'string' ? raw.version : '',
  };
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}
