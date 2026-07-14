# ADR-047 — Similar-turn cohort fallback ladder for the token forecaster (#20 Phase 1)

Status: Accepted
Date: 2026-07-14
Owner: lead-executed
Tracking: issue #20 (Phase 1 of `docs/backlog/provider-model-effort-features.md` §4); ordering per DECISION_LOG D-10

## Context

ADD §15.2 defines the token forecaster's empirical base over "recent turns
matching: provider + model family + task class + repository cohort" — but
the implementation (`SQLDataSource.RecentSimilarTurnTokens`) filtered by
session only, and the frozen feature-lookup port (ADR-044) returned a bare
`[]float64`, leaving no channel to say which cohort actually answered.
Phase 0 (#20, D-10) made the turn's identity capturable: statusline ingest
maintains `provider_sessions.model/effort` as a latest-observed resolution
cache, and prediction rows persist the `(provider, model_id, model_family,
effort)` stamp.

Two gaps remained for cohort mechanics:

1. **Sample-side labels.** Usage observations (`provider.usage.observed`)
   carried no identity labels, so even once a total-token field exists,
   samples could not be assigned to cohorts at turn granularity —
   session-level joins would mis-assign history after a mid-session
   `/model` or `/fast` switch (backlog §3 constraint 1).
2. **Rung visibility.** The backlog's sparsity constraint (§3 constraint 4)
   requires an explicit degradation ladder with "Confidence/reason codes
   reflecting which rung answered" — impossible through a `[]float64`.

## Decision

1. **Label the sample surface (capture, additive).** The claude telemetry
   normalizer stamps `model_id` and `effort` from the statusline snapshot
   onto every `provider.usage.observed` payload. Labels are metadata: they
   never gate event emission, and absent identity stamps nothing.
2. **Amend the frozen port (sanctioned by ADR-044's "changes require an
   ADR").** `FeatureDataSource.RecentSimilarTurnTokens` now returns
   `features.SimilarTurnTokens{Samples []float64, Rung SimilarTurnCohortRung}`.
   The consumer-side narrow view (`internal/predictor/token.FeatureSource`)
   changes identically; interface segregation is preserved.
3. **Fallback ladder in `SQLDataSource`** (backlog §3.4), most- to
   least-specific, first rung with ≥ 8 samples (the ADD §15.2 gate,
   mirrored by `minSimilarTurnSamples`) answers:
   - `provider + model family + effort` (`CohortRungModelEffort`)
   - `provider + model family` (`CohortRungModelFamily`)
   - `provider` (`CohortRungProvider`)
   - session-recent (`CohortRungSession`) — the pre-ladder behavior,
     verbatim, as the terminal fallback (also the answer when the turn's
     provider was never observed).
   A rung whose turn-side label is unobserved is skipped, never
   matched-as-empty (unknown is not zero). The turn's identity resolves
   from `provider_sessions` (the same source as the prediction stamp);
   sample model IDs resolve to families through the same pricing table
   rules as the stamp's `model_family` column, so the two can never
   disagree by construction.
4. **Reason codes (additive to the ADD §16.4 taxonomy, same sanction as
   ADR-043's codes).** Exactly one of `TOKEN_COHORT_MODEL_EFFORT` /
   `TOKEN_COHORT_MODEL_FAMILY` / `TOKEN_COHORT_PROVIDER_ONLY` /
   `TOKEN_COHORT_SESSION_ONLY` accompanies an empirical base; cold-start
   forecasts keep emitting `PREDICTION_COLD_START` unchanged. An unknown
   future rung value maps to the session-only code — the most conservative
   claim. Confidence semantics are unchanged (empirical ⇒ at most
   ConfidenceMedium, never calibrated this wave).

## Honest scope

- **Task class and repository stay out of the ladder.** Neither exists on
  the sample surface (classification is a derived, post-hoc signal never
  persisted onto usage events; statusline ingest does not populate
  `events.repository_id`). The `class` parameter remains accepted but
  unused, documented at the query — the same honesty discipline as the
  pre-ladder implementation.
- **The ladder is dormant machinery today.** No payload carries
  `total_tokens` yet, so every rung yields zero samples and behavior is
  byte-identical to before (session rung, empty, cold-start default). When
  a future wave adds the field, the ladder activates for free — the same
  contract the pre-ladder query documented for itself.
- **Effort matches on raw strings.** Claude is the only provider emitting
  effort today; the normalized cross-provider `effort_class` mapping is a
  frozen-contract concern deferred to Phase 3 (codex wiring) per the
  backlog's own sequencing.
- **No numeric decisions.** Gate (8) and recency limit (50) are the
  existing ADD §15.2 / implementation constants; the candidate-pool bound
  is derived (4 × limit, one per identity rung plus spare), not tuned.

## Consequences

- Calibration (#11) can stratify both predictions AND token samples by the
  same normalized identity, with no unlabeled-history hole from this point
  forward.
- `CONTRACT_FREEZE.md` gains an Amendments entry for the port change; every
  implementer/fake updated in the same commit (Go's type system enforces
  completeness).
- Phase 2 (empirical per-cohort quota deltas / quantiles) builds on the
  same rung vocabulary; Phase 3 maps codex (model, reasoning, speed) into
  the same triple.

## Alternatives considered

- **Resolve cohorts through `provider_sessions` joins instead of payload
  labels** — rejected: session-level labels mis-assign turn-level history
  (backlog §3 constraint 1's explicit warning).
- **Keep the port and log rung selection out-of-band** — rejected: reason
  codes are the pipeline's explanation channel (ADD §16.4); a forecast
  whose cohort specificity is invisible to the persisted prediction row
  defeats the calibration purpose that motivated #20.
- **Filter cohorts in SQL via json_extract** — rejected: model-family
  resolution lives in `internal/pricing`'s Go rules; duplicating them as
  SQL string patterns would let cohort membership and the prediction
  stamp's family drift apart.
