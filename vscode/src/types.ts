/**
 * types.ts — TypeScript mirrors of the daemon's wire shapes.
 *
 * Every field here exists in the Go handlers; nothing is invented. The
 * sources of truth, per response type:
 *
 *  - internal/httpapi/httpapi.go: healthResponse, versionResponse,
 *    capabilitiesResponse, statusResponse, jobsResponse/jobView,
 *    jobResponse (cancel), sessionStatusResponse, errorEnvelope/errorBody.
 *  - internal/sessionstatus/snapshot.go: Snapshot and its section structs
 *    (auspex.daemon.session_status.v1, the FR-162 per-session read-model).
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

/**
 * GET /v1/session/status and GET /v1/session/{id}/status →
 * httpapi.sessionStatusResponse embedding sessionstatus.Snapshot
 * (internal/sessionstatus/snapshot.go) — the FR-162 per-session
 * read-model: risk, runway, quota freshness, progress tree, checkpoint,
 * and pause state.
 *
 * Null/empty semantics are the server's honesty invariant (ADD §8.8):
 * absent sections serialize as JSON null (risk/runway/checkpoint/pause);
 * sections with a natural empty serialize as empty arrays (quota windows,
 * progress nodes/edges); optional scalars are null when the source column
 * was NULL. The parsers below map null → undefined and never substitute
 * zeros — unknown must render as unknown (Constitution §7).
 */
export const SESSION_STATUS_SCHEMA_VERSION = 'auspex.daemon.session_status.v1';

export interface SessionStatusSnapshot {
  schema_version: string; // "auspex.daemon.session_status.v1"
  session_id: string;
  /** undefined ⇔ JSON null: no linkable prediction for this session yet. */
  risk?: SessionRisk;
  /** undefined ⇔ JSON null: no runway_forecasts row for this session yet. */
  runway?: SessionRunway;
  /** Always present; windows empty when no quota events were observed. */
  quota: SessionQuota;
  /** Always present; nodes/edges empty when the task has none (common today). */
  progress: SessionProgress;
  /** undefined ⇔ JSON null: the session's task has no state checkpoint. */
  checkpoint?: SessionCheckpoint;
  /** undefined ⇔ JSON null: the session has no pause record. */
  pause?: SessionPause;
}

/** sessionstatus.Risk. Scores are 0-1 estimates; calibrated=false means
 * they are NOT probabilities (Constitution principle #2). */
export interface SessionRisk {
  overall_risk_score: number;
  quota_risk_score?: number;
  context_risk_score?: number;
  completion_risk_score?: number;
  blast_radius_risk_score?: number;
  calibrated: boolean;
  confidence: string;
  reason_codes: string[];
  turn_id: string;
  evaluated_at: string;
}

/** sessionstatus.Runway. Optional fields ⇔ JSON null (source column NULL). */
export interface SessionRunway {
  limit_id: string;
  horizon_seconds?: number;
  risk_score: number;
  calibrated: boolean;
  confidence: string;
  current_used_percent?: number;
  hit_probability?: number;
  /** Percentage points of quota per MINUTE (predictor/runway estimateBurnRate). */
  burn_rate_p50?: number;
  burn_rate_p90?: number;
  estimated_time_to_limit_p50_seconds?: number;
  estimated_time_to_limit_p90_seconds?: number;
  quota_observed_at?: string; // RFC 3339
  reason_codes: string[];
}

/** sessionstatus.Quota — quota-freshness: latest observation per limit window. */
export interface SessionQuota {
  as_of: string; // server clock at read time, RFC 3339
  windows: SessionQuotaWindow[];
}

/** sessionstatus.QuotaWindow. used_percent/resets_at null when the source
 * event did not carry them; age_seconds is server-computed (as_of − observed_at). */
export interface SessionQuotaWindow {
  limit_id: string;
  used_percent?: number;
  resets_at?: string; // RFC 3339
  observed_at: string; // RFC 3339
  age_seconds?: number;
}

/** sessionstatus.Progress. Node title/description are omitted SERVER-side
 * (FR-171 content exclusion) — only structural fields are carried. */
export interface SessionProgress {
  task_id?: string;
  nodes: SessionProgressNode[];
  edges: SessionProgressEdge[];
}

export interface SessionProgressNode {
  id: string;
  parent_id?: string;
  ordinal?: number;
  kind: string;
  status: string;
  version?: number;
  started_at?: string;
  completed_at?: string;
  updated_at: string;
}

export interface SessionProgressEdge {
  from_node_id: string;
  to_node_id: string;
  kind: string;
}

/** sessionstatus.Checkpoint — latest state checkpoint (+ linked repo checkpoint). */
export interface SessionCheckpoint {
  state?: SessionStateCheckpoint;
  repository?: SessionRepoCheckpoint;
}

export interface SessionStateCheckpoint {
  id: string;
  task_id: string;
  progress_tree_version?: number;
  active_node_id?: string;
  completion_node_id?: string;
  repository_checkpoint_id?: string;
  integrity_sha256: string;
  created_at: string;
}

export interface SessionRepoCheckpoint {
  id: string;
  status: string;
  recoverability: string;
  git_head: string;
  index_diff_hash: string;
  worktree_diff_hash: string;
  total_bytes?: number;
  created_at: string;
  verified_at?: string;
}

/** sessionstatus.Pause — most recent pause record (any lifecycle state)
 * plus the wake jobs scheduled against it (the FR-163 cancel targets). */
export interface SessionPause {
  id: string;
  task_id: string;
  turn_id?: string;
  status: string; // domain.PauseStatus wire strings ("predicted", "sleeping", ...)
  auto_resume_enabled: boolean;
  runway_forecast_id: string;
  state_checkpoint_id?: string;
  repository_checkpoint_id?: string;
  requested_at: string;
  safe_point_at?: string;
  paused_at?: string;
  expected_reset_at?: string;
  cancelled_at?: string;
  failure_code?: string;
  /** sessionstatus.WakeJob shares jobView's fields; nulls map to undefined. */
  wake_jobs: JobView[];
}

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

/**
 * Parse the FR-162 session-status envelope. Fail-soft: a foreign
 * schema_version (or a non-object) returns undefined; a section that does
 * not match its expected shape degrades to undefined ("unknown"), never to
 * zero-filled values.
 */
export function parseSessionStatus(raw: unknown): SessionStatusSnapshot | undefined {
  if (
    !isRecord(raw) ||
    raw.schema_version !== SESSION_STATUS_SCHEMA_VERSION ||
    typeof raw.session_id !== 'string'
  ) {
    return undefined;
  }
  return {
    schema_version: raw.schema_version,
    session_id: raw.session_id,
    risk: parseSessionRisk(raw.risk),
    runway: parseSessionRunway(raw.runway),
    quota: parseSessionQuota(raw.quota),
    progress: parseSessionProgress(raw.progress),
    checkpoint: parseSessionCheckpoint(raw.checkpoint),
    pause: parseSessionPause(raw.pause),
  };
}

export function parseSessionRisk(raw: unknown): SessionRisk | undefined {
  if (!isRecord(raw)) {
    return undefined; // JSON null: no prediction yet
  }
  if (typeof raw.overall_risk_score !== 'number' || typeof raw.calibrated !== 'boolean') {
    return undefined; // required fields missing: unknown, not zero
  }
  return {
    overall_risk_score: raw.overall_risk_score,
    quota_risk_score: optNum(raw.quota_risk_score),
    context_risk_score: optNum(raw.context_risk_score),
    completion_risk_score: optNum(raw.completion_risk_score),
    blast_radius_risk_score: optNum(raw.blast_radius_risk_score),
    calibrated: raw.calibrated,
    confidence: optStr(raw.confidence) ?? '',
    reason_codes: strArray(raw.reason_codes),
    turn_id: optStr(raw.turn_id) ?? '',
    evaluated_at: optStr(raw.evaluated_at) ?? '',
  };
}

export function parseSessionRunway(raw: unknown): SessionRunway | undefined {
  if (!isRecord(raw)) {
    return undefined; // JSON null: no forecast yet (honest — may be empty today)
  }
  if (
    typeof raw.limit_id !== 'string' ||
    typeof raw.risk_score !== 'number' ||
    typeof raw.calibrated !== 'boolean'
  ) {
    return undefined;
  }
  return {
    limit_id: raw.limit_id,
    horizon_seconds: optNum(raw.horizon_seconds),
    risk_score: raw.risk_score,
    calibrated: raw.calibrated,
    confidence: optStr(raw.confidence) ?? '',
    current_used_percent: optNum(raw.current_used_percent),
    hit_probability: optNum(raw.hit_probability),
    burn_rate_p50: optNum(raw.burn_rate_p50),
    burn_rate_p90: optNum(raw.burn_rate_p90),
    estimated_time_to_limit_p50_seconds: optNum(raw.estimated_time_to_limit_p50_seconds),
    estimated_time_to_limit_p90_seconds: optNum(raw.estimated_time_to_limit_p90_seconds),
    quota_observed_at: optStr(raw.quota_observed_at),
    reason_codes: strArray(raw.reason_codes),
  };
}

export function parseSessionQuota(raw: unknown): SessionQuota {
  if (!isRecord(raw)) {
    return { as_of: '', windows: [] }; // malformed: no observations, not 0% used
  }
  const windows: SessionQuotaWindow[] = [];
  if (Array.isArray(raw.windows)) {
    for (const w of raw.windows) {
      if (!isRecord(w) || typeof w.limit_id !== 'string') {
        continue;
      }
      windows.push({
        limit_id: w.limit_id,
        used_percent: optNum(w.used_percent),
        resets_at: optStr(w.resets_at),
        observed_at: optStr(w.observed_at) ?? '',
        age_seconds: optNum(w.age_seconds),
      });
    }
  }
  return { as_of: optStr(raw.as_of) ?? '', windows };
}

export function parseSessionProgress(raw: unknown): SessionProgress {
  if (!isRecord(raw)) {
    return { task_id: undefined, nodes: [], edges: [] };
  }
  const nodes: SessionProgressNode[] = [];
  if (Array.isArray(raw.nodes)) {
    for (const n of raw.nodes) {
      if (!isRecord(n) || typeof n.id !== 'string' || typeof n.status !== 'string') {
        continue;
      }
      nodes.push({
        id: n.id,
        parent_id: optStr(n.parent_id),
        ordinal: optNum(n.ordinal),
        kind: optStr(n.kind) ?? '',
        status: n.status,
        version: optNum(n.version),
        started_at: optStr(n.started_at),
        completed_at: optStr(n.completed_at),
        updated_at: optStr(n.updated_at) ?? '',
      });
    }
  }
  const edges: SessionProgressEdge[] = [];
  if (Array.isArray(raw.edges)) {
    for (const e of raw.edges) {
      if (!isRecord(e) || typeof e.from_node_id !== 'string' || typeof e.to_node_id !== 'string') {
        continue;
      }
      edges.push({ from_node_id: e.from_node_id, to_node_id: e.to_node_id, kind: optStr(e.kind) ?? '' });
    }
  }
  return { task_id: optStr(raw.task_id), nodes, edges };
}

export function parseSessionCheckpoint(raw: unknown): SessionCheckpoint | undefined {
  if (!isRecord(raw)) {
    return undefined; // JSON null: no state checkpoint for the task
  }
  const state = parseStateCheckpoint(raw.state);
  const repository = parseRepoCheckpoint(raw.repository);
  if (!state && !repository) {
    return undefined;
  }
  return { state, repository };
}

function parseStateCheckpoint(raw: unknown): SessionStateCheckpoint | undefined {
  if (!isRecord(raw) || typeof raw.id !== 'string') {
    return undefined;
  }
  return {
    id: raw.id,
    task_id: optStr(raw.task_id) ?? '',
    progress_tree_version: optNum(raw.progress_tree_version),
    active_node_id: optStr(raw.active_node_id),
    completion_node_id: optStr(raw.completion_node_id),
    repository_checkpoint_id: optStr(raw.repository_checkpoint_id),
    integrity_sha256: optStr(raw.integrity_sha256) ?? '',
    created_at: optStr(raw.created_at) ?? '',
  };
}

function parseRepoCheckpoint(raw: unknown): SessionRepoCheckpoint | undefined {
  if (!isRecord(raw) || typeof raw.id !== 'string') {
    return undefined;
  }
  return {
    id: raw.id,
    status: optStr(raw.status) ?? '',
    recoverability: optStr(raw.recoverability) ?? '',
    git_head: optStr(raw.git_head) ?? '',
    index_diff_hash: optStr(raw.index_diff_hash) ?? '',
    worktree_diff_hash: optStr(raw.worktree_diff_hash) ?? '',
    total_bytes: optNum(raw.total_bytes),
    created_at: optStr(raw.created_at) ?? '',
    verified_at: optStr(raw.verified_at),
  };
}

export function parseSessionPause(raw: unknown): SessionPause | undefined {
  if (!isRecord(raw)) {
    return undefined; // JSON null: no pause record for the session
  }
  if (typeof raw.id !== 'string' || typeof raw.status !== 'string') {
    return undefined;
  }
  const wakeJobs: JobView[] = [];
  if (Array.isArray(raw.wake_jobs)) {
    for (const j of raw.wake_jobs) {
      // sessionstatus.WakeJob carries jobView's fields with explicit nulls
      // for the absent optionals; parseJobView maps null → undefined.
      const view = parseJobView(j);
      if (view) {
        wakeJobs.push(view);
      }
    }
  }
  return {
    id: raw.id,
    task_id: optStr(raw.task_id) ?? '',
    turn_id: optStr(raw.turn_id),
    status: raw.status,
    auto_resume_enabled: raw.auto_resume_enabled === true,
    runway_forecast_id: optStr(raw.runway_forecast_id) ?? '',
    state_checkpoint_id: optStr(raw.state_checkpoint_id),
    repository_checkpoint_id: optStr(raw.repository_checkpoint_id),
    requested_at: optStr(raw.requested_at) ?? '',
    safe_point_at: optStr(raw.safe_point_at),
    paused_at: optStr(raw.paused_at),
    expected_reset_at: optStr(raw.expected_reset_at),
    cancelled_at: optStr(raw.cancelled_at),
    failure_code: optStr(raw.failure_code),
    wake_jobs: wakeJobs,
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

/** JSON null / absent / wrong-typed number → undefined (never 0). */
function optNum(v: unknown): number | undefined {
  return typeof v === 'number' ? v : undefined;
}

/** JSON null / absent / wrong-typed string → undefined (never ''). */
function optStr(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined;
}

function strArray(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
}
