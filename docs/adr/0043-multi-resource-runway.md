# ADR-043 — Generalize quota runway into a multi-resource forecast (context window, cost budget, rate limits)

Status: Accepted (direction); implementation staged with issue #14
Date: 2026-07-13
Owner: lead (predictor + policy surfaces), with contract-integrator for any frozen-port change
Approved by: repository owner, 2026-07-13 (issue #13 decision session)

## Context

Auspex's evaluation pipeline treats **provider quota** as the primary
exhaustible resource: `QuotaForecaster` (ADR-041 Stage 3,
`internal/predictor/quota`) projects rolling-window quota and context
percentages, and Graceful Pause's headline trigger is "a quota limit is
calibrated-likely to hit soon."

The provider landscape is moving under that assumption: Codex has removed
its 5-hour rolling limit, and Claude may follow. When hard provider
limits weaken or disappear, the user's dominant concern shifts from
*"will I be cut off?"* to *"will this run burn unbounded money, context,
or time?"* — and nothing in a limit-free provider stops an overnight
agent run from spending hundreds of dollars.

The architecture already anticipated this pivot: prediction and policy
are separated (ADD §6.6), provider capabilities are explicit and
degradable (§6.7, §8.6 `RollingQuotaUsage: false` is a legal state), and
`domain.QuotaForecast` already carries *both* a quota projection and a
context projection — quota was always just one resource among several.

## Decision

1. **Resource set.** The forecast layer covers four exhaustible resource
   classes, each producing the same shape of output (projected P50/P90
   utilization, fit-in-remaining-window verdict, reason codes,
   `DataQuality`):
   - **Provider quota / rate limits** — today's behavior, kept as-is
     where the provider still exposes limits (weekly caps and API 429
     classes do not disappear with the 5-hour window).
   - **Context window** — already projected by
     `projectContext`; promoted from a sub-field to a first-class
     resource.
   - **Cost budget** — new: token forecast × a pricing/consumption model,
     compared against *user-declared* budgets (per-turn, per-day) in
     config. No provider signal required.
   - **Wall-clock time budget** — new, optional: projected turn duration
     vs. a user-declared time budget. Ships last; requires duration
     telemetry that does not exist yet (issue #15 / #11).
2. **Policy inputs shift from provider-granted limits to user-declared
   budgets.** The policy engine's eight frozen actions are unchanged; a
   budget breach maps onto the same actions (`WARN`,
   `CHECKPOINT_AND_RUN`, `PAUSE`, `BLOCK`, …). Budgets live in YAML
   config with the existing precedence rules; absence of a budget means
   the resource is simply not policy-active (explicit degradation, never
   a guess).
3. **Pause/wake machinery is reused unchanged.** A wake job's trigger
   gains a resource-class discriminator (quota reset time → budget reset
   time / provider recovery), but the durable scheduler, lease, and
   resume-validation semantics stay exactly as built.
4. **Contract impact is additive.** `domain.QuotaForecast` stays (frozen
   contract); the generalization introduces a sibling
   `domain.ResourceForecast` list on the evaluation result populated per
   resource class. Nothing existing is renamed or removed; REC-05's
   multi-window question is answered "yes — one forecast entry per
   window/resource, same shape" rather than by widening one struct.

## Consequences

- Auspex's value proposition no longer depends on providers keeping
  hard limits; per-prompt cost/scope/risk estimation (issue #14) becomes
  the primary surface, with this ADR supplying its resource/cost model.
- A pricing table becomes a maintained artifact (per provider/model,
  config-overridable, never fetched at runtime by default — local-first).
- Uncalibrated forecasts remain labeled scores/ranges, never
  probabilities (Constitution principle #2), independent of resource
  class.

## Sequencing

Implemented incrementally on the issue-#14 line: cost forecast first
(largest user value, no new telemetry needed), context-window promotion
second, time budget last. Issue #13 tracks this ADR; #14 tracks the
surface; #11/#12 supply the calibration data.
