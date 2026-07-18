# Backlog — Provider / Model / Effort as Prediction Inputs

> 🌐 English | [繁體中文](provider-model-effort-features.zh-TW.md)

| Field | Value |
|---|---|
| Status | **Phases 0–1 landed** (2026-07-14) — capture (#20 Phase 0) and cohort filtering (ADR-047) shipped; **Phases 2–3 remain** (empirical calibration, blocked on #11 data; Codex adapter wiring) |
| Tracking | Issue [#20](https://github.com/huaiche94/auspex/issues/20); ordering decision in `docs/DECISION_LOG.md` D-10 |
| Origin | Owner request, 2026-07-13: "這個專案是否有考慮到不同家使用 claude(model, effort), codex(model, reasoning, speed) 當作參數來做預測公式/模型" — audit found the answer is *no*, this document is the todo |
| Related | `Auspex_ADD.md` §15.2/§15.3, ADR-041 (forecast layer), ADR-043 (multi-resource runway), DECISION_LOG D-02 (second-provider line deferred), issues #13, #11 |
| Grounding discipline | Same as `Predictor_Improvement_Suggestions.md`: no coefficient proposals without data. This document proposes **capture and cohort mechanics only**; every numeric decision is deferred until per-cohort samples exist |

## 1. Problem

The prediction pipeline (Scope → Token Forecast → Quota Forecast → Risk, ADR-041)
is **provider-blind, model-blind, and effort-blind**. The parameters that most
directly determine a turn's token consumption, latency, and dollar cost are not
inputs to any formula:

- **claude**: model (opus / sonnet / haiku / fable), reasoning effort
  (low / medium / high / xhigh / max), fast mode
- **codex**: model, reasoning level, speed setting

The same prompt on `haiku` + low effort and on `fable` + max effort can differ
by an order of magnitude in output tokens and by more than that in USD — yet
today both produce the same forecast.

## 2. Current state (audited 2026-07-13)

Three distinct layers, three different degrees of "considered":

| Parameter | Designed? | Captured in schema? | Used in formulas? |
|---|---|---|---|
| provider (claude/codex) | Yes — ADD §15.2/§15.3 cohort:「依 provider/model/task class」 | Yes — `provider_sessions.provider` | **No** |
| model family | Yes — same cohort definition | Partial — `provider_sessions.model` (nullable, **session-level**) | **No** — `internal/pricing/pricing.go` has per-model rates but only the forecast-card presenter uses them (render-time cost display) |
| effort / reasoning / speed | **No — absent from the entire design corpus** | No | **No** |

Evidence:

- `internal/predictor/token/forecaster.go` — the `RecentSimilarTurnTokens` port
  comment names the ideal cohort "provider + model family + task class +
  repository" (ADD §15.2) but the implementation does not filter by any of it.
- `internal/evaluation/datasource_sql.go` (`RecentSimilarTurnTokens`) — actual
  cohort is "recent usage observations for this exact session"; the comment
  honestly documents the narrowing: the usage event carries no task-class tag,
  so the full cohort was descoped.
- `internal/predictor/quota/coldstart.go` — quota deltas hardcoded
  (P50 = 2.0 pp, P90 = 6.0 pp); ADD §15.3 step 5's「依 provider/model/task
  class 計算 empirical P50/P90」 is explicitly marked unreachable this phase.
- `internal/storage/sqlite/migrations/0041_predictions.sql` — persisted
  predictions carry **no** provider/model/effort columns, so historical
  predictions cannot be stratified by model for calibration after the fact.
- Telemetry stores `reasoning_tokens` (FR-020, ADD §11.12) — but as an
  *outcome* measure, never an input feature.
- Codex appears only in `internal/statecheckpoint` test fixtures; the
  second-provider line is deferred by DECISION_LOG D-02.

**Conclusion:** provider/model cohorts are *designed but unimplemented*; the
execution-parameter dimension (effort/reasoning/speed) is *not even designed*.
This document adds the missing dimension to the design surface and sequences
the implementation.

## 3. Design constraints (learned from the audit, binding on any implementation)

1. **Turn-level, not session-level.** Model and effort change mid-session
   (`/model`, `/fast`, effort switches). `provider_sessions.model` is the wrong
   home; capture must live on the turn-level usage observation and be copied
   onto each persisted prediction row. A session-level column would silently
   mis-assign cohorts.
2. **Capture before model.** No formula work until the fields are recorded —
   the repo's own discipline (`Predictor_Improvement_Suggestions.md` §2.3:
   tuning against n≈0 is indistinguishable from guessing). Every day the
   fields are not captured is unlabeled history that calibration (#11) can
   never recover.
3. **Normalize across providers.** One feature triple
   `(provider, model_family, effort_class)` with provider-specific raw values
   preserved alongside. claude effort tiers and codex reasoning/speed settings
   map into a small shared `effort_class` enum; raw strings kept for audit and
   re-mapping. Mapping table is a frozen-contract concern (ports/domain), so it
   gets ADR treatment when implemented.
4. **Cohort sparsity needs a fallback ladder.** Adding dimensions multiplies
   cohort count; `MinSimilarSamples` (ADD §15.2 cold-start gate) will rarely be
   met early. Lookup must degrade explicitly: exact cohort → drop effort →
   drop model → session-recent (today's behavior) → ADD §14.6 cold-start
   default, with `Confidence`/reason codes reflecting which rung answered.
5. **Predictions must persist their features.** Add provider/model/effort
   columns to the `predictions` table so #11 can stratify residuals per cohort.

## 4. Phased TODO

- [x] **Phase 0 — capture** (landed 2026-07-14; #20's capture slice):
  - [x] Turn-level identity is captured: statusline snapshots feed
        `provider_sessions.model` + `effort` (migration 0005, COALESCE
        last-writer-wins — the resolution cache), Stop-hook payloads stamp
        the turn-end `effort` onto `provider.turn.completed` events
        (hooks.md: hook payloads carry no model field, so the event-level
        triple is effort-only; the FULL triple lives on the prediction
        row, which is the surface calibration joins against).
  - [x] `predictions` rows persist `(provider, model_id, model_family,
        effort)` — migration 0046, stamped at EvaluateTurn time from the
        session's latest observed identity; NULL when never observed.
  - [x] Forecast card's cost estimate resolves the stamped model's price
        family (fable/mythos/opus/sonnet/haiku — fable/mythos families and
        current-generation opus prices added to the default table) and the
        CostRange label says so; DefaultFamily fallback only when the
        identity was never observed.
- [x] **Phase 1 — cohort filtering** (landed 2026-07-14, ADR-047):
  `RecentSimilarTurnTokens` implements the §3.4 fallback ladder
  (provider+family+effort → provider+family → provider → session-recent,
  first rung meeting the §15.2 ≥8 gate answers; turn-side-unlabeled rungs
  skipped, never matched-as-empty) and returns which rung answered; the
  forecaster emits one `TOKEN_COHORT_*` reason code per empirical base.
  Usage observations now carry `model_id`/`effort` payload labels
  (observation-granularity capture — the sample-side half Phase 0's
  event stamp left out). Task class + repository remain honestly out of
  the ladder (still absent from the sample surface); the ladder is
  dormant until a total-token payload field exists, per ADR-047's
  "honest scope".
- [ ] **Phase 2 — empirical calibration** (blocked on Phase 0 + #11 data):
  - [ ] Per-cohort quota deltas replacing `coldstart.go` constants
        (ADD §15.3 step 5).
  - [ ] Per-model/effort token quantiles feeding the multiplier model, or
        replacing it per cohort where samples suffice.
  - [ ] Per-model price table becomes a *forecast* input (cost axis of
        ADR-043 / #13), not just a render-time display.
- [ ] **Phase 3 — codex adapter wiring**: the D-02-deferred second-provider
  line; map codex (model, reasoning, speed) into the normalized triple and
  validate that cohort mechanics hold for a non-claude provider.

## 5. Acceptance criteria (for closing #20)

- A turn's model + effort are recorded at turn granularity and visible on the
  persisted prediction row.
- `RecentSimilarTurnTokens` filters by the normalized triple with an explicit,
  reason-coded fallback ladder.
- Quota-delta and token-quantile lookups are per-cohort once sample gates pass;
  cold-start behavior unchanged below the gates.
- Cost forecasts use the observed model's price table, and say so.
- Codex parameters flow through the same path with no predictor special-casing.

## 6. Non-goals

- No multiplier coefficients or effort-tier weights are proposed here — no
  data exists to ground them (see Grounding discipline above).
- No decision on *when* the codex adapter lands — that remains D-02's call,
  revisited at its own decision point.
- No change to frozen contracts in this document; contract changes arrive with
  their implementing ADR (per Constitution §3).
