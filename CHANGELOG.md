# Changelog

All notable changes to Auspex are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow [SemVer](https://semver.org/) once releases begin.

## [Unreleased]

### Changed

- **Renamed the product Preflight → Auspex** (ADR-045, supersedes
  ADR-001): Go module `github.com/huaiche94/auspex`, binary `auspex`,
  schema-version prefixes `auspex.*.v1`, user-data directory `auspex/`.
  Pre-release rename with no migration; old local `preflight/` data
  directories are abandoned in place. GitHub redirects the old repo URL.

### Added

- **`auspex gc` — tiered telemetry retention** (ADR-046,
  [#19](https://github.com/huaiche94/auspex/issues/19)): raw
  events/features/predictions/decisions/forecasts/consumed
  authorizations and terminal tasks' superseded checkpoints older than
  the 90-day hot window (`--retention-days`) are rolled up into
  `usage_rollups_daily` + `calibration_samples` (preserving
  prediction-vs-actual pairs for #11 calibration), archived as gzip
  JSONL under the data dir with full column fidelity, verified by
  read-back, and only then deleted — fail-closed, never partially. The
  latest state/repository checkpoint per task is always kept. `--dry-run`
  is side-effect-free; `--vacuum` opts into a full `VACUUM` (the
  database runs `auto_vacuum=NONE`, so deletes alone only free pages for
  reuse). Migration range 0060–0069 is now assigned to retention/gc.
- Complete vertical slice (85/85 DAG nodes, Bootstrap through the Stage-5
  Final integration gate): frozen domain/port/event contracts, SQLite
  storage with migration ranges per role, Claude Code provider parsers +
  hook handlers + idempotent telemetry persistence, Progress Tree with
  evidence-gated atomic CompleteNode, State Checkpointing with startup
  reconciliation, Repository Checkpoint (create/verify/patch/untracked
  archive with secret redaction, restore dry-run), predictor pipeline
  (prompt features → task classifier → scope estimator → token/quota
  forecasters → risk combiner → runway score), cold-start policy engine
  over eight frozen actions, one-time authorizations with replay
  rejection, graceful-pause state machine + durable scheduler with lease
  recovery, fully wired `auspex` CLI (`evaluate`, `decision`,
  `checkpoint`, `pause`/`resume`/`scheduler`, `status`, `doctor`,
  `hook claude ...`), cross-platform CI, and the qa security/integration
  suite (E2E demo, leakage scanner, path-traversal fixtures, race tests).

### Known gaps

- No production adapter yet connects persisted provider events to
  Progress Tree node completion
  ([#1](https://github.com/huaiche94/auspex/issues/1)).
- Unattended wake/resume requires the future daemon
  ([#7](https://github.com/huaiche94/auspex/issues/7)); wake jobs run
  via `auspex scheduler run-once` until then.
