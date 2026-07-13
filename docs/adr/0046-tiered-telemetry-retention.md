# ADR-046 — Tiered telemetry retention: hot raw window → rollup → gzip archive → delete

Status: Accepted
Date: 2026-07-13
Owner: lead (storage/CLI surfaces), with contract-integrator for the new
migration-range assignment
Approved by: repository owner, 2026-07-13 (issue #19 decision session)

## Context

Auspex's SQLite database grows without bound. Every hook invocation
appends `events` rows (up to four per statusline snapshot), every
evaluated prompt appends `feature_vectors` + `predictions` +
`policy_decisions` (+ `runway_forecasts` when the runway path runs),
every issued gate appends `authorizations`, and every node completion
appends `state_checkpoints`/`node_completions` rows plus — for repository
checkpoints — an on-disk artifact directory under
`<data-dir>/checkpoints/`. Nothing ever deletes any of it. A dogfooding
installation (issue #17/#12) accretes this data on every single prompt,
so unbounded growth is a today problem, not a someday problem.

At the same time, M13 calibration (issue #11) NEEDS long-lived
prediction-vs-actual pairs, and the Progress Tree/checkpoint invariants
(Constitution §6) forbid deleting evidence a resumable task still
depends on. Retention therefore cannot be a blunt `DELETE WHERE old`.

## Decision

Adopt **three-tier retention**, executed by a new `internal/retention`
engine surfaced as `auspex gc` (schema-versioned output `auspex.gc.v1`):

1. **Hot raw window** — raw rows younger than the retention window are
   untouched. Default window: **90 days** (`retention.Policy`,
   `DefaultRetentionDays`), overridable via `auspex gc
   --retention-days`. One window for all classes; per-class overrides
   are deliberately not offered until a real need surfaces (no
   speculative abstraction).
2. **Rollup summary tables** (migration `0060_retention.sql`) — before
   raw rows leave the hot tier, the aggregates worth keeping forever are
   distilled into compact tables, in the same delete transaction:
   - `usage_rollups_daily`: per (UTC day, provider, session, event
     type) counts plus only the aggregates today's persisted payloads
     honestly carry (see the migration header for the exact
     field-by-field derivation and its gaps).
   - `calibration_samples`: one row per expired prediction pairing its
     predicted quantiles with the actual turn outcome **where one is
     derivable**, `actual_known = 0` + NULL actuals otherwise — the
     ADD-principle-1 ("unknown is not zero") honest cold start. This is
     what preserves M13 calibration's raw prediction-vs-actual pairs
     across archival.
3. **Gzip JSONL archive, then delete — fail-closed.** Expired raw rows
   are written, one JSON object per row with full column fidelity, to
   `<data-dir>/archive/<table>/<YYYY-MM>/<table>-<UTC
   timestamp>-<runID>.jsonl.gz` via the same temp-file → fsync → rename
   discipline as `internal/repocheckpoint/atomicwrite.go`. The archive
   is then **re-opened and re-read**, and its row count and SHA-256
   content digest are verified against what was selected. Only after
   every class's archive verifies do the deletes run — all classes in
   one transaction, with affected-row counts checked against the
   selected sets. Any failure before that transaction leaves every raw
   row untouched and records a failed `retention_runs` row. There is no
   partial-delete state.

### Classes covered and their per-class rules

| Class | Expiry rule | Rollup |
|---|---|---|
| `events` | `occurred_at` older than window | `usage_rollups_daily` |
| `feature_vectors` | `created_at` older than window | — |
| `predictions` + `policy_decisions` | prediction `created_at` older than window; decisions tied to an expired prediction go with it (they would cascade anyway — archiving them explicitly keeps the archive equal to the delete set); orphan decisions (`prediction_id IS NULL`) by their own `decided_at` | `calibration_samples` |
| `runway_forecasts` | `created_at` older than window, **except** rows still referenced by a surviving `policy_decisions` row (deleting those would `ON DELETE SET NULL`-mutate a row we are keeping) | — |
| `authorizations` | only rows **both** consumed (`consumed_at IS NOT NULL`) **and** expired (`expires_at`) longer than the window — an unconsumed or unexpired authorization is never GC'd | — |
| checkpoints (`state_checkpoints`, `node_completions`, `repository_checkpoints` + on-disk artifact dirs) | only for tasks whose `tasks.status` is terminal (`completed`/`failed`) with `completed_at` older than the window | — |

Checkpoint safeguards, in full:

- **The most recent state checkpoint and the most recent repository
  checkpoint per task are always kept**, regardless of age — a resumable
  safety anchor. The repository checkpoint referenced by the kept state
  checkpoint (`repository_checkpoint_id`) is kept as well, so the anchor
  never dangles.
- A terminal task with `completed_at IS NULL` is **skipped entirely**
  and named in the run summary — completion age is then not cleanly
  derivable, and the Constitution's evidence rules say guessing is worse
  than keeping.
- Non-terminal tasks are untouched no matter how old.
- On-disk artifact directories (`repository_checkpoints.artifact_root`)
  are removed only **after** the delete transaction commits (an orphaned
  directory is safe; a deleted directory behind a surviving row is not),
  and only when the path resolves inside the Auspex data directory — a
  root outside it is left in place and noted in the summary rather than
  `RemoveAll`'d on faith.

### Space reclamation

`internal/storage/sqlite/db.go`'s pragma bootstrap sets no `auto_vacuum`,
so every Auspex database runs SQLite's default `auto_vacuum = NONE`:
deletes only move pages to the freelist; the file never shrinks on its
own. The engine reads `PRAGMA auto_vacuum` at runtime rather than
assuming: if a database were ever in `incremental` mode it runs `PRAGMA
incremental_vacuum` automatically after a deleting pass; for today's
actual `NONE` databases it instead exposes `auspex gc --vacuum`, which
runs a full `VACUUM` (rewrites the whole file under an exclusive lock —
correct but briefly blocking, hence opt-in). The `auspex.gc.v1` output
reports the freelist-derived `reclaimable_bytes_estimate` honestly
instead of claiming bytes were returned to the OS when they were not.

### Dry run

`auspex gc --dry-run` is **truly side-effect-free**: it selects and
reports per-table counts and would-be rollups, but writes no archive
files, no rollup rows, and no `retention_runs` row. (The alternative —
recording a `dry_run=1` audit row — was considered and rejected: a
mode named "dry run" that writes to the database invites exactly the
class of surprise this feature exists to prevent.)

### Migration range 0060–0069 is assigned to retention/gc

Retention is cross-cutting (it touches every role's tables) and owned by
no vertical-slice role, so it gets its own range rather than squatting in
someone else's. `CONTRACT_FREEZE.md`'s migration-range table gains the
row `0060–0069 retention/gc` citing this ADR. `0060_retention.sql` is
its first migration.

### Calibration honesty: what today's payloads can and cannot populate

Verified against `internal/telemetry/claude/normalizer.go` (the sole
producer of persisted event payloads) and
`internal/orchestrator/hooks.go` (the only place `Event.TurnID` is
stamped):

- **Derivable:** `actual_outcome` (`completed`/`failed`/`interrupted`
  from a `provider.turn.*` event carrying the prediction's `turn_id`),
  `actual_failure_class` (the `failure_class` payload field on
  `provider.turn.failed`), `actual_outcome_at`, and `session_id` (from
  the turn's own events).
- **Not derivable today, therefore NOT a column:** actual per-turn token
  usage. No persisted payload carries it — `provider.turn.completed`
  carries only `stop_hook_active`; statusline `provider.usage.observed`
  carries session-cumulative cost/duration/lines, and
  `provider.context.observed` carries context-window fill, neither
  attributable to a single turn. Adding a fabricated or misattributed
  number would violate ADD principle 1; when a payload gains real
  per-turn tokens, a new migration in the 0060–0069 range adds the
  column.
- **Cold-start reality:** only `provider.turn.started` events are
  stamped with a `turn_id` today (the Stop/StopFailure hook payloads
  carry no turn identity), so real Claude sessions currently produce
  `actual_known = 0` samples. That is the honest answer, recorded as
  such — the join is by `turn_id`, and samples upgrade automatically
  once outcome events gain turn correlation (issue #1's line of work).

## Alternatives considered

- **TTL-only (`DELETE WHERE old`)** — rejected: destroys the raw
  prediction-vs-actual pairs M13 calibration (#11) needs, and destroys
  the usage history the ADR-043 budget forecasts want, with no recovery
  path.
- **Rollup-only (aggregate then delete, no archive)** — rejected: a
  rollup schema is a bet about which aggregates matter; the gzip JSONL
  archive is the hedge that makes a wrong bet recoverable (re-derive
  from archives) instead of permanent data loss.
- **Size-cap ring buffer** — rejected: evicts by volume, not by age, so
  one chatty session could evict another session's still-hot rows; and
  it has the same calibration-destroying property as TTL-only.

## Consequences

- The database stops growing without bound; long-horizon signal
  (daily usage, calibration pairs) survives in compact tables and
  everything else survives as `.jsonl.gz` under the data dir, which the
  user can delete at their own discretion — Auspex itself never deletes
  archives.
- `retention_runs` gives every pass a durable audit row (fail or
  succeed), consistent with Constitution §6's evidence discipline.
- A full `VACUUM` remains opt-in; switching the database to
  `auto_vacuum = incremental` would require a from-empty rebuild or an
  explicit migration decision and is out of scope here.
- The archive directory is append-only from Auspex's perspective;
  archive pruning/compaction, if ever wanted, is a future decision.
