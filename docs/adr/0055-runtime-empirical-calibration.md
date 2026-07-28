# ADR-0055 — Runtime empirical calibration goes live: hook-captured turns feed the cohort ladder; four-class empirical cost band (#42 / #66 item b)

> 🌐 English | [繁體中文](0055-runtime-empirical-calibration.zh-TW.md)

Status: Accepted
Date: 2026-07-28
Owner: lead-executed (PROPOSAL — frozen-contract change, owner sign-off at merge)
Tracking: issues #42 and #66 item b; #20 Phase 2 (`docs/backlog/provider-model-effort-features.md` §4); consumes the ADR-051 capture through the ADR-047 ladder; evidence from the weekly calibration reports (2026-07-22 / 2026-07-28: cost high-bound under-forecasts 7.0–7.3× median, stable across both weeks; token P90 contains only 56% of actuals; `claude/opus/xhigh` n=307 and `claude/fable/xhigh` n=101 both past the ADD §15.2 gate)

## Context

Two shipped-forecast defects were data-diagnosed and data-unblocked at once:

1. **The token forecast was a cold-start constant (#42).** ADR-047's
   cohort ladder was built as dormant machinery — but it stayed dormant
   even after ADR-051 landed per-turn token accounting, for two reasons
   this ADR fixes. First, the candidate query read only
   `provider.usage.observed` (managed runs), never the Stop hook's
   `provider.turn.completed` accounting — the export's token-actual join
   reads both (`tokenActualEventTypes`), so the calibration side saw
   telemetry the forecast side ignored. Second, **pool dilution**: the
   candidate fetch took the newest N events of the type and filtered for
   `total_tokens` afterwards, but token-bearing rows are a small minority
   (every statusline snapshot is a tokenless `usage.observed`) — on the
   owner's real database the newest-200 window contained 2 token-bearing
   rows against 200+ real captured turns. The ladder starved to
   cold-start on a database holding hundreds of usable samples.

2. **The cost band was cache-blind (#66).** The two-class band prices
   `total_tokens` (= fresh input + output) only. The captured four-class
   actuals show cache-read is ~56% of the reconstructed bill and the
   cache-blind view under-counts it ~6.3× in aggregate — measured as
   band containment of 6% with 375/404 actuals ABOVE the band, and a
   per-cohort high-bound residual of 7.0–7.3× median, stable across two
   weekly reports. The consumer ADR-043/#13 reserved for this —
   `pricing.Table.FourClassCost` — existed with zero callers.

Amending the ADR-044-frozen `app.FeatureDataSource` port (precedent:
ADR-047 itself) and extending the versioned calibration-export shape
(ADR-052 trigger 3) both require an ADR — this one.

## Decision

1. **Both turn-exact producers feed the ladder.** `cohortCandidates` and
   the session rung read `provider.usage.observed` AND
   `provider.turn.completed` — the same event-type pair as the export's
   token-actual join, so forecast samples and calibration actuals can
   never diverge by event-type selection. One candidate per turn,
   newest-first latest-wins (the export's re-entrant-Stop rule,
   mirrored). A coarse `LIKE '%"total_tokens"%'` SQL prefilter fixes the
   pool dilution; the decoded JSON still decides.

2. **New port method for cost samples.**
   `app.FeatureDataSource.RecentSimilarTurnCosts` (additive amendment to
   the ADR-044 frozen port) returns `features.SimilarTurnCosts`: recent
   same-cohort turns' KNOWN four-class costs — each candidate carrying
   all four ADR-051 classes priced under its OWN model via the
   already-shipped `pricing.Table.FourClassCost`. The cost ladder is
   ADR-047's ladder restricted to its model-bearing rungs (model+effort,
   model family): dollar samples are family-priced, so the provider-wide
   and session rungs never answer — a cross-family dollar blend is a
   meaningless band, not a conservative fallback. Candidates missing any
   class supply no cost sample (pricing them with fabricated zero cache
   classes would understate by the very ~6× factor this exists to
   correct — unknown is not zero).

3. **The pipeline's cost band prefers the empirical estimator.** With
   >= 8 cohort cost samples (the same ADD §15.2 gate constant), the band
   is their empirical P50–P90 with
   `CostRange.Source = "four-class-empirical"` (new
   `pricing.SourceFourClassEmpirical`); below the gate the ADR-043
   two-class band stays, byte-identical to before. Both remain labeled
   uncalibrated estimates.

4. **The band is persisted, and read-back is verbatim (migration 0064).**
   `predictions` gains nullable `cost_low_usd`, `cost_high_usd`,
   `cost_model_family`, `cost_source`. The empirical band depends on the
   cohort's samples at evaluation time — a later recompute would show a
   DIFFERENT number than the policy stage compared, so the forecast card
   and the calibration export read the persisted band verbatim.
   Pre-0064 rows keep the legacy recompute, which is exact for them
   (their bands were deterministic). The export gains `cost_source`
   (the ADR-052 trigger-3 surface extension this ADR authorizes), and
   `report.py` stratifies containment by estimator and restricts the
   #72 Phase 2 residual fit to two-class bands — never averaging two
   estimator generations into one factor.

5. **The token forecast's reason codes reach the persisted row.**
   `RuleRiskCombiner` consumes the token forecast's numbers only, so
   ADR-047's `TOKEN_COHORT_*` rung disclosure never reached the
   persisted reason set — invisible while the ladder never answered,
   dishonest once it does (a leftover `PREDICTION_COLD_START` from the
   retry/progress multipliers would read as the whole story). The
   evaluation service now unions `TokenForecast.ReasonCodes` into the
   persisted set.

## Honest scope

- **Empirical is not calibrated.** Every flag stays `Calibrated=false`,
  confidence at most medium. An empirical quantile over local history
  sharpens the estimate; a probability claim still requires the ADD
  §15.6 held-out gate (ECE/Brier) and its own ADR (Constitution §3,
  §7 rule 7).
- **This is not the M13 model artifact.** Runtime empirical quantiles
  over the local database are the ADR-019 / M5-sanctioned mechanism.
  No fitted coefficient is burned into the binary; each database answers
  from its own history and honors the gate per cohort. The ADR-020 JSON
  model artifact, registry, and held-out evaluator remain M13
  deliverables, not pre-built here (§31).
- **List-price reconstruction, not spend.** Cost samples are priced with
  the shipped placeholder table (a subscription's marginal cost is $0) —
  a consumption signal, always labeled estimate.
- **Archived rows still lack token actuals** (ADR-051's accepted gap):
  the cost ladder reads live events only. The additive
  `calibration_samples` migration in the 0060–0069 range remains the
  follow-up closing that gap for long-horizon research.
- **Codex stays two-class.** The implicit-cache formula (D-02) is
  unbuilt; codex turns carry no explicit four-class decomposition and
  are never force-priced (#66 item b's implicit-cache sibling).
- **The statusline is untouched.** #90 Phase A demoted per-turn
  forecasts off the bar; D-15's return condition ("gate on calibration
  or cohort sample count") now has its gate — the `TOKEN_COHORT_*` rung
  on the persisted row — but the return itself belongs to #90's
  aggregate-first strategy, not this slice.

## Consequences

- On any database past the gate, the token base answers from local
  history with the rung disclosed (owner's database: trivial-prompt P50
  moved 3210 → ~5.6k, heavy-prompt ~6.6k–20k — prompts now visibly
  differ, #42's acceptance), and the cost band lands in the right
  magnitude (~$2.1–$7.3 observed vs the two-class $0.13–$1.28 on the
  same turn).
- The ADR-043 cost-budget policy rule now compares a band of realistic
  magnitude against declared budgets.
- `CONTRACT_FREEZE.md` gains an Amendments entry (port method + DTO);
  the calibration-export README vocabulary gains `cost_source`.
- The weekly report's by-estimator containment split is the success
  metric: the four-class-empirical bucket should contain a majority of
  actuals where the two-class bucket contained 6%.

## Alternatives considered

- **Burn fitted per-cohort constants into the binary** (a `fit.py`
  emitting a Go table). Rejected: drifts between fits, ships one user's
  quantiles to every database, and duplicates M13's artifact path
  without its registry or held-out gate. Runtime quantiles self-refresh
  and honor the sample gate per database.
- **Predict the four classes, then price** (per-cohort class shares ×
  the token forecast). Rejected this phase: the class ratios are
  extremely heavy-tailed on real data (cache-read ÷ forecastable total:
  median ~174–208×, P90 ~450–620×), so banding KNOWN per-turn costs is
  the robust first consumer. A class-share forecast can supersede it
  later under the same `Source` vocabulary.
- **Keep recomputing the band at read-back.** Rejected: the empirical
  band is a function of evaluation-time samples; recomputing would
  silently show a different number than the one the policy stage
  compared and the user saw.
- **Let the provider rung answer the cost ladder.** Rejected: mixing
  families mixes price levels — an opus-dominated pool would
  systematically under-band a fable turn by the families' rate ratio.
