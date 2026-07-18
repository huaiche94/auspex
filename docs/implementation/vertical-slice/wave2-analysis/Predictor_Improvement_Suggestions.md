# Predictor Improvement Suggestions

> 🌐 English | [繁體中文](Predictor_Improvement_Suggestions.zh-TW.md)

| Field | Value |
|---|---|
| Phase | 3.4 — Post Phase 2 Analysis |
| Target | The Rule Predictor tier only (Version 1 in `Auspex_Predictor_Design_Supplement.md`'s Evolution Roadmap) |
| Status | Recommendations only. `internal/predictor/**` is unmodified by this document. |
| Grounding | Every suggestion below is labeled either **evidence-based** (grounded in Phase 1/2 data, cited) or **speculative** (no Auspex execution data exists yet — grounded in the ADD's own already-specified formulas instead, or flagged as untested) |

## 1. Multipliers this phase's data can and cannot speak to

The ADD already specifies a multiplier framework for the (not-yet-built)
Token Forecaster (§15.2) and Risk Combiner (§16.2): `scope_multiplier`,
`verification_multiplier`, `complexity_multiplier` (with named terms for
`cross_layer`, `migration`, `security_sensitive`, `repository_wide`),
`retry_multiplier`, `progress_multiplier`, `ambiguity_multiplier`. This
phase built the Scope Estimator (`predictor-05`) and Runway Forecaster
(`predictor-06`) but not the Token Forecaster or Risk Combiner
(`predictor-05b`/`-05c`/`-07`, deliberately deferred per ADR-041). So this
phase has **zero direct execution data** on token-cost multipliers for
coding-agent turns — the dataset in `Prediction_Error_Report.md` measures
the cost of *implementing Auspex itself*, not the cost of a Claude Code
turn Auspex is meant to forecast (see `Calibration_Report.md` §5's
category-error warning). Every suggestion below states plainly whether it
draws on this phase's real data or is speculative pending real coding-agent
telemetry.

## 2. Suggestions

### 2.1 Authentication / security-sensitive multiplier

**Speculative — no data.** No node in Phase 1/2 touched authentication or
security-sensitive code paths, so this phase has no evidence for or against
the ADD §16.2 `security_sensitive` term's weight (currently `0.15` in the
`blast_radius_risk` formula). Recommendation: do not adjust this
coefficient from the ADD's existing value without evidence; flag it as an
open calibration target for the first phase that includes an
authentication-adjacent task, and log that task's actual scope/rework
explicitly for this purpose.

### 2.2 Retry / rework multiplier

**Evidence-based, but measuring a different kind of "retry" than the ADD's
term.** ADD §15.2's `retry_multiplier` models a coding agent re-attempting
a failed tool call or test within one turn. This phase's data instead shows
two cases of **fixture/implementation rework** — `claude-provider-03`'s
`unknown_category.json` status-code mismatch and `predictor-03`'s
keyword-overlap test prompts (`Wave2_Lessons.md` §1, issue #5). Both were
one-iteration fixes caught immediately by `go test`, not multi-turn
struggles. This is weak, indirect evidence that a "fixture/spec
disagreement" failure mode exists and is cheap to fix when caught by an
automated test — it does not directly calibrate the ADD's turn-level retry
multiplier. Recommendation: track this as a distinct, named risk signal
(e.g. a future `SPEC_FIXTURE_MISMATCH` reason code) rather than folding it
into the existing retry multiplier, since it has a different trigger
(authoring inconsistency, not runtime failure) and a different mitigation
(a shared decision table authored before fixtures and code, per both
nodes' own recommendation).

### 2.3 Repository-specific multiplier

**No data — flagged as a structural gap, not a coefficient suggestion.**
This phase has n=1 repository. `Calibration_Report.md` §4 already covers
this: no repository-specific bias can be identified or ruled out.
Recommendation: do not add a repository-specific multiplier term until at
least a second repository's data exists; adding one now would be tuning a
coefficient against a sample of one, which is indistinguishable from
guessing.

### 2.4 Context multiplier

**Speculative, but with a concrete implementation note from this phase.**
ADD §15.2's `context_multiplier` formula (`1 + (current_context_tokens /
context_window) × 0.5`) needs `current_context_tokens`, which is a
`domain.ContextObservation` value — a type this phase's `claude-provider-04`
normalizer already produces (`EventProviderContextObserved`) but that
`predictor-05`/`-06` do not yet consume (out of scope this phase).
Recommendation: when `predictor-05b` (Token Forecaster) is eventually
built, verify `ForecastTokensRequest`'s `Scope domain.ScopeEstimate` field
is sufficient, or whether the request DTO needs a `ContextObservation`
field added — this is the same class of contract-gap `predictor-05`
already hit once with `EstimateScopeRequest` (`Wave2_Lessons.md` §1, issue
#2b), and is worth checking proactively rather than rediscovering it
mid-node.

### 2.5 Uncertainty adjustment

**Evidence-based, and this is the strongest, most concrete
recommendation in this document.** `predictor-05`'s cold-start handling
(ADD §14.6's table names only 8 of 16 task classes) required inventing a
"documented nearest-neighbor mapping" for the other 8 — a real, executed,
non-speculative design decision. Recommendation: formalize this pattern
before `predictor-05b`/`-05c` are built. Specifically: (a) require every
cold-start lookup table in the predictor pipeline to explicitly enumerate
its coverage gaps and state the nearest-neighbor (or other) fallback rule
for each, rather than leaving gaps implicit; (b) since `predictor-05`
already had to build this exact pattern once for `ScopeEstimate`, consider
whether the same nearest-neighbor logic can be factored into a shared
helper `internal/predictor/**` package rather than re-derived per stage —
this is a "don't repeat a design decision three times" observation, not a
coefficient tuning suggestion.

### 2.6 Integration-test multiplier

**No data — the ADD's own formula is the only available signal.** No node
this phase triggered the `integration_tests` term (ADD §16.2's
`completion_risk` formula weights it at `0.12`; §15.2's
`verification_multiplier` weights it at `0.45`). Recommendation: no
change; this coefficient has never been exercised by Auspex's own
execution, so there is nothing in this phase's data to calibrate it
against. Note for future phases: `qa-01`'s CI matrix and `qa-02`'s E2E test
(neither built yet) are themselves integration-test-heavy nodes and would
be a natural first real data point once built.

### 2.7 Cross-platform / OS-conditional-logic multiplier (new suggestion)

**Evidence-based, n=2, flagged explicitly as low-confidence per
`Calibration_Report.md` §6.** Both nodes in this phase's dataset that
required OS-conditional logic (`foundation-02`'s path resolution,
`foundation-04`'s process-liveness check) surfaced a real bug or
unavoidable file-split that same-OS work of equivalent nominal complexity
did not. This is not one of the ADD's existing named multiplier terms.
Recommendation: consider whether a `cross_platform` boolean signal
belongs in `domain.ScopeEstimate` or `ScopeEstimator`'s feature inputs,
analogous to `MigrationLikely`/`SecuritySensitive` — but do not add this
without at least one more phase's data, since n=2 is not enough to justify
a new frozen-contract field. Recorded here so the hypothesis isn't lost
between phases, not as a ready-to-implement recommendation.

## 3. Explicitly out of scope for this document

Per the Phase 3.4 instruction, no coefficient values, formulas, or code in
`internal/predictor/**`, `internal/features/**`, or the ADD's §15/§16
pseudocode were changed by writing this document. Every "recommendation"
above is either (a) "wait for more data before touching this," (b) "watch
this specific thing in the next phase," or (c) "here is a design pattern
worth reusing" — none is an instruction to alter a frozen number.
