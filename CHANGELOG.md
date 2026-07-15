# Changelog

> 🌐 English | [繁體中文](CHANGELOG.zh-TW.md)

All notable changes to Auspex are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow [SemVer](https://semver.org/) once releases begin.

## [Unreleased]

### Fixed

- **Migration runner applies backfilled (gap-numbered) migrations**
  ([#22](https://github.com/huaiche94/auspex/issues/22)): `Migrate` now
  computes pending work as a set difference against `schema_migrations`
  instead of "everything above `MAX(version)`". Under the per-domain
  migration-range numbering (CONTRACT_FREEZE.md), a migration can land
  with a lower number than ranges already applied — 0045 (predictions
  context projection) landed after 0050–0052 shipped and was silently
  skipped forever on databases already at 52, breaking every
  `EvaluateTurn` insert invisibly behind the hooks' fail-open contract.
  The `ErrSchemaNewerThanBinary` fail-closed check is unchanged (still
  keyed on the maximum applied version). Databases affected by the 0045
  gap self-heal on next startup.

### Changed

- **Renamed the product Preflight → Auspex** (ADR-045, supersedes
  ADR-001): Go module `github.com/huaiche94/auspex`, binary `auspex`,
  schema-version prefixes `auspex.*.v1`, user-data directory `auspex/`.
  Pre-release rename with no migration; old local `preflight/` data
  directories are abandoned in place. GitHub redirects the old repo URL.

### Documentation

- **Hook subcommand casing ratified as kebab-case** (ADR-050,
  [#61](https://github.com/huaiche94/auspex/issues/61)): resolves REC-03 —
  the ADD's Appendix E.1/E.3 hook-install templates and §24.3 examples wrote
  `auspex hook claude UserPromptSubmit` (PascalCase) while the shipped CLI,
  `agents/runtime.md`, and the DAG validation command use kebab-case
  (`user-prompt-submit`). ADR-050 ratifies kebab-case (idiomatic Cobra, least
  churn) and updates the ADD's argv to match; the provider's own
  `hook_event_name` payload field and the settings.json hook-matcher keys stay
  PascalCase (a different namespace). No code change — the CLI already shipped
  kebab-case; `internal/cli/doc.go`'s REC-03 note now points at the resolving
  ADR. No frozen contract touched.
- **Documentation reorganization + Traditional Chinese translations**
  (ADR-049, D-17): the three design documents moved from the repository
  root to `docs/design/` (living documents updated to cite the new
  path; historical records intentionally unchanged), `README.md`
  rewritten for first-time viewers, every folder gained a `README.md`
  introduction, and every documentation file gained a non-normative
  `<name>.zh-TW.md` Traditional Chinese sibling. English remains the
  sole normative text.

### Added

- **Per-turn token usage captured from the Stop transcript (ADR-051)**
  ([#72](https://github.com/huaiche94/auspex/issues/72)): at Stop, the hook
  parses the completed turn's slice of the Claude Code transcript
  (`transcript_path`) and enriches `provider.turn.completed` with numbers
  only — `input_tokens`, `output_tokens`, `cache_read_input_tokens`,
  `cache_creation_input_tokens`, `total_tokens`, `api_call_count`,
  `model_id` (requestId-deduplicated, main chain only, bounded read,
  strictly fail-open: any failure yields the pre-ADR-051 byte-identical
  event). The calibration export joins these as `actual_*` fields and
  `report.py token_coverage()` consumes them directly — hook-mode token
  joins are exact going forward, unblocking the capture prerequisites of
  [#66](https://github.com/huaiche94/auspex/issues/66)/[#65](https://github.com/huaiche94/auspex/issues/65)
  and the token side of
  [#11](https://github.com/huaiche94/auspex/issues/11)/[#42](https://github.com/huaiche94/auspex/issues/42).
  No frozen-contract change, no migration. See
  `docs/adr/0051-turn-usage-from-stop-transcript.md`.

- **`duration_coverage()` finishes the #62 calibration rail (report side)**
  ([#62](https://github.com/huaiche94/auspex/issues/62)):
  `research/calibration/` now loads the export's `duration_p50_ns` /
  `duration_p90_ns` / `actual_duration_ms` fields and reports predicted-band
  vs actual per-turn duration coverage, symmetric to the cost section.

- **Cost-forecast calibration rail (Phase 1)**
  ([#72](https://github.com/huaiche94/auspex/issues/72)): the calibration
  export now carries the predicted cost band per row (`cost_low_usd` /
  `cost_high_usd` / `cost_model_family`), priced from the token quantiles
  by the same `internal/pricing` table the forecast card renders — so the
  calibration measures the exact cost the user was shown, with no second
  price table to drift (`internal/retention/export.go`, additive fields,
  no migration). `research/calibration/report.py` gains a **cost-band
  coverage** section that joins that predicted band against the per-turn
  cost delta `observations.py` derives from the session-cumulative
  `total_cost_usd` series. This is the hook-mode opening #72 identifies:
  unlike a `total_tokens` actual (managed-run only — the statusline
  carries no per-turn tokens), a per-turn **cost** delta is derivable from
  native hook telemetry alone, so native-hook turns finally join a
  prediction to an actual (156/157 on the owner's first field dataset)
  without `auspex run`. The report separates actuals landing below (cost
  over-forecast) vs above (under-forecast) the band; the first real run
  shows 91% landing above it — the systematic under-forecast the token
  cold-start (#42) and cache-blind pricing (#66) predicted, now quantified
  from real data. The per-turn cost ACTUAL stays a research-layer
  attribution (`observations.py`), never computed by the
  capture-before-model Go bridges. Additive export fields →
  backward-compatible → no ADR (Constitution §3). Phase 2 (fitting the
  cost residual per labeled cohort — the `claude/opus/xhigh` and
  `claude/fable/xhigh` cohorts already meet the §15.2 gate) remains gated
  on #11.
- **Per-turn duration forecast (Phase 1)** (#62): the scope estimator now
  populates the reserved `ScopeEstimate.DurationP50/P90` fields with a
  cold-start wall-clock estimate derived from the classified scope
  (`internal/predictor/scope/duration.go`), so it responds to the prompt
  rather than being a frozen constant. Persisted per prediction
  (migration 0047, `predictions.duration_p50/p90`, nanoseconds), carried
  into `calibration_samples` alongside a new `actual_duration_ms` column
  joined from the turn's `provider.usage.observed` `total_duration_ms`
  (migration 0062) so predicted-vs-actual duration pairs accumulate for
  calibration (#11) and survive archival — turn-attributable today on the
  managed-run (`auspex run`) path, NULL (honest gap) for session-cumulative
  statusline usage until turn-stamped coverage grows (#1). Surfaced as a
  `time:` line on the forecast card / UserPromptSubmit `additionalContext`,
  a `duration` block in `auspex evaluate --json`, and the calibration
  export (`duration_p50_ns` / `actual_duration_ms`).
  Labeled uncalibrated (Constitution §7) and deliberately **not** shown on
  the statusline until it is calibrated (#11) or otherwise made
  prompt-responsive there (#42) — the D-15/#42 lesson that a static
  cold-start number carries no statusline signal. Phase 2 (calibration
  against the `total_duration_ms` telemetry Claude Code already reports)
  remains gated on #11.
- **Turn correlation for terminal hook events** (PR
  [#54](https://github.com/huaiche94/auspex/pull/54)): Stop/StopFailure
  events now join back to the turn's evaluation, so
  prediction-vs-actual outcome pairs accumulate for the M13 calibration
  pipeline ([#11](https://github.com/huaiche94/auspex/issues/11)).
- **Background daemon + authenticated loopback HTTP API** (M6, D-16,
  [#7](https://github.com/huaiche94/auspex/issues/7)): `auspex daemon
  run` foreground process with `auspex daemon install` generating a
  macOS LaunchAgent plist; bearer token at `<data>/daemon.token` (0600,
  rotated each start); SSE event stream at `/v1/events/stream`. Due wake
  jobs now execute unattended.
- **VS Code companion extension MVP**
  ([#10](https://github.com/huaiche94/auspex/issues/10), PR
  [#53](https://github.com/huaiche94/auspex/pull/53)): status-bar
  daemon liveness, activity-bar views (status / progress / checkpoints /
  pause / wake jobs), inline cancel for scheduled resumes; used from
  source or local VSIX until the marketplace publisher is registered
  ([#18](https://github.com/huaiche94/auspex/issues/18)).
- **Per-prompt forecast surface**
  ([#14](https://github.com/huaiche94/auspex/issues/14)) with the
  statusline iterated to v3
  ([#31](https://github.com/huaiche94/auspex/issues/31),
  [#41](https://github.com/huaiche94/auspex/issues/41)): measured-first
  context segment, weekly-window segment, single policy badge; the
  static cold-start token segment is withdrawn until forecasts respond
  to the prompt ([#42](https://github.com/huaiche94/auspex/issues/42)).
- **Native-hook session bootstrap**
  ([#17](https://github.com/huaiche94/auspex/issues/17)): hooks
  idempotently register repository/worktree/session rows, so the
  evaluation pipeline works in real provider sessions with zero setup.
- **Event correlation + `auspex progress complete`** (D-01,
  [#1](https://github.com/huaiche94/auspex/issues/1)): provider events
  now correlate to Progress Tree nodes; completion stays explicit and
  evidence-gated.
- **Real repository-checkpoint restore**
  ([#6](https://github.com/huaiche94/auspex/issues/6)), closing the
  checkpoint-b08 dry-run deferral.
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

- All forecasts are cold-start rules — uncalibrated scores, not
  probabilities. The token forecast barely responds to the prompt
  ([#42](https://github.com/huaiche94/auspex/issues/42)); calibration
  from real telemetry is milestone M13
  ([#11](https://github.com/huaiche94/auspex/issues/11)).
- Claude Code is the only provider adapter; Codex (M7/M8) is tracked in
  [#9](https://github.com/huaiche94/auspex/issues/9). Managed one-shot /
  shell modes (M11) are tracked in
  [#8](https://github.com/huaiche94/auspex/issues/8).
- Prompt-feature extraction runs multiple O(n) passes on the blocking
  hook path ([#51](https://github.com/huaiche94/auspex/issues/51)) and
  its payload schema lacks an extraction-version tag
  ([#50](https://github.com/huaiche94/auspex/issues/50)).
