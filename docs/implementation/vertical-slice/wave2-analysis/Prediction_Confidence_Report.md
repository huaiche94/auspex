# Prediction Confidence Report

> 🌐 English | [繁體中文](Prediction_Confidence_Report.zh-TW.md)

| Field | Value |
|---|---|
| Phase | 3.8 — Post Phase 2 Analysis |
| Derived from | `Feature_Registry.md` (canonical). This document adds a confidence-sorted view and training-suitability recommendations; it does not restate descriptions, sources, or operational metadata already in the registry. |
| Status | Analysis only |

## 1. High-confidence features (confidence ≥ 0.80)

These are Observed, not predicted, and safe to trust as-is.

| Metric | Value (this phase) | Provenance | Confidence | Evidence source | Update frequency | Ground Truth | Rule | Statistical | ML |
|---|---|---|---|---|---|---|---|---|---|
| `ByteLength`/`RuneCount`/`LineCount` | Per-prompt, computed | Observed | 1.00 | `internal/features/prompt.go`, verified | Per turn | No | Yes | Yes | Yes |
| `Fingerprint.ComputeDigest()` | Per-repo-state, computed | Observed | 1.00 | `internal/gitx`, independently verified for determinism | Per checkpoint event | Yes | N/A (not a predictor input) | N/A | N/A |
| `Fingerprint.HeadOID`/`Branch` | Per-repo-state | Observed | 1.00 | same | Per checkpoint event | Yes | N/A | N/A | N/A |
| `Event.IdempotencyKey` | Per-event | Observed | 1.00 | `claude-provider-04`, verified deterministic | Per event | Yes | N/A (plumbing, not a feature) | N/A | N/A |
| `Event.SchemaVersion` | Constant | Observed | 1.00 | Bootstrap-frozen | Static | Yes | N/A | N/A | N/A |
| `UsageObservation.*` / `QuotaObservation.*` / `ContextObservation.*` (fixture-scoped) | Fixture-dependent | Observed **against fixtures only** | 1.00 vs. fixtures / 0.00 vs. live reality | `claude-provider-01`/`04`, verified | Per status-line update (once live) | No | Yes | Yes | Yes |
| `AcceptanceCriteriaCount` | Per-prompt | Observed (pattern match) | 0.80 | `internal/features/prompt.go` | Per turn | No | Yes | Yes | Yes |

**Read the `UsageObservation` row carefully**: its confidence is
context-dependent, not a single number — 1.00 against the fixtures it has
been tested with, 0.00 against a live session it has never seen. This
report lists it here because the *parser* is high-confidence; a future
reader must not mistake this for "Auspex has high-confidence real
usage data," which is false (see §2).

## 2. Medium-confidence features (0.30 ≤ confidence < 0.80)

Real signal exists but is heuristic, unverified against live reality, or
both.

| Metric | Value (this phase) | Provenance | Confidence | Evidence source | Update frequency | Ground Truth | Rule | Statistical | ML |
|---|---|---|---|---|---|---|---|---|---|
| `ExplicitPathCount` | Per-prompt | Observed (heuristic) | 0.60 | `internal/features/prompt.go` | Per turn | No | Yes | Yes | Yes |
| Verb-presence flags (5) | Per-prompt | Observed (keyword match) | 0.60 | same; self-flagged false-positive risk in `Wave2_Lessons.md` §1 issue #5 | Per turn | No | Yes | Yes | Yes |
| Keyword-indicator flags (5) | Per-prompt | Observed (keyword match) | 0.60 | same | Per turn | No | Yes | Yes | Yes |
| `ScopeEstimate.RequiresUnitTests/RequiresIntegration/CrossProject/MigrationLikely/SecuritySensitive` | Per-turn prediction | Estimated | 0.50-0.60 | `predictor-05`, verified for structural correctness, not accuracy | Per turn | No | Yes | Yes | Yes |
| `ApproxTokens` | Per-prompt | Estimated | 0.30 (ADD §14.7 mandates `confidence=low`) | `internal/features/prompt.go` | Per turn | No | Yes | Yes | Low weight (superseded once real usage exists) |
| `RunwayForecast.RiskScore` | Per-observation | Estimated (uncalibrated fallback) | Variable, always uncalibrated this phase | `internal/predictor/runway`, verified via 300-combination sweep | Continuous during active turn | No | Yes | Yes | Low weight (not a probability until calibrated) |
| `Fingerprint.Entries` (status entries) | Per-repo-state | Observed | 1.00 for the parse itself, but not listed in §1 because its *use* in checkpoint identity is not a predictor-confidence question | Observed | Per checkpoint event | Yes | N/A | N/A | N/A |

## 3. Low-confidence features (confidence < 0.30, but not zero — some signal exists)

| Metric | Value (this phase) | Provenance | Confidence | Evidence source | Update frequency | Ground Truth | Rule | Statistical | ML |
|---|---|---|---|---|---|---|---|---|---|
| `ScopeEstimate.FilesReadP50/P80/P90` | Cold-start default or session-blend | Estimated | Low (never ground-truthed, per `Feature_Gap_Report.md` §1.1) | `predictor-05` | Per turn | No | Yes | Yes | Yes |
| `ScopeEstimate.FilesChangedP50/P80/P90` | Cold-start default or session-blend | Estimated | Low, for the same reason — `RepositoryFeatures` isn't wired | `predictor-05` | Per turn | No | Yes | Yes | Yes |
| `ScopeEstimate.LinesChangedP50/P80/P90` | Cold-start default or session-blend | Estimated | Low | `predictor-05` | Per turn | No | Yes | Yes | Yes |

## 4. Zero-confidence / Unknown features (confidence = 0.00, no data exists at all)

The largest bucket, per `Feature_Registry.md` §9 (~31% of all registered
fields). Not re-listed individually here (that would duplicate the
registry) — grouped by cause instead:

| Cause bucket | Representative features | Count (approx.) | Provenance |
|---|---|---|---|
| No live telemetry ever observed | `TokenForecast.*`, `SessionFeatures.*`, actual token usage, actual duration | ~20 | Unknown |
| No wiring between existing data and predictor input | `RepositoryFeatures.*` (Git data exists, not connected) | ~9 | Unknown (wiring gap specifically, per `Feature_Gap_Report.md` §1.1) |
| Component not built yet | `ArtifactRef.*`, `StateCheckpoint.*`, `QuotaForecast.*`, `ProviderCapabilities.*` (real detection) | ~10 | Unknown |
| Requires accumulated volume, not a build task | ECE, Brier score | 2 | Unknown |

## 5. Classification and training recommendations

### 5.1 Which features should become future training labels

Training labels are the *outcomes* a Statistical/ML predictor learns to
predict — not inputs. From this registry, the candidates are:

- **`UsageObservation.*` (once live)** — the direct target for a Token
  Forecaster. Currently `Unknown` at live-data confidence; recommended as
  the #1 training-label candidate once `Missing_Telemetry_Report.md` A1
  closes.
- **`QuotaObservation.UsedPercent` (once live)** — direct target for a
  Quota Forecaster.
- **Historical outcome labels** (`completed_normally`/`hit_usage_limit`/etc.,
  `Missing_Telemetry_Report.md` A5) — direct target for any
  classification-style Version 2/3 predictor.

None of these are usable as training labels *today* — every one is
`Unknown` in §4. This is a forward-looking recommendation, not a
statement that training can begin now.

### 5.2 Which features should remain auxiliary (inputs, never labels)

- All `Prompt Features` (§1 in the Registry) — these describe the input,
  not an outcome; there is no sense in which a model would be trained to
  "predict" `ByteLength`.
- All `Repository Features` and `Session Features` — same reasoning;
  these are context, not outcomes.
- `ProviderCapabilities.*` — a session-level constant, not a
  per-turn-varying signal to predict.

### 5.3 Which features should NOT be used for training, even once available

- **`ApproxTokens`** — explicitly a low-confidence heuristic
  *substitute* for real token counts (ADD §14.7). Once real
  `UsageObservation` data exists, training against `ApproxTokens` instead
  of real usage would be training against a proxy for the label, not the
  label itself — actively harmful once the real signal is available.
  Recommendation: use `ApproxTokens` as a Rule Predictor input only, and
  retire it from any statistical/ML feature set the moment real usage
  data exists in sufficient volume.
- **`RunwayForecast.RiskScore` while `Calibrated: false`** — training a
  model to reproduce an explicitly-uncalibrated heuristic's output would
  bake in that heuristic's biases rather than learning from real outcomes.
  ADR-026/033 already forbid presenting this as a probability; this
  recommendation extends the same logic to training data hygiene.
- **Any keyword/verb-presence flag used as a label** — these are
  themselves heuristic *inputs* derived from the prompt; there is no
  outcome they represent. Listed here only to preempt a plausible mistake
  (e.g. "train a model to predict `HasMigrateVerb`" is meaningless — it's
  already deterministic from the prompt text).

## 6. Summary counts

| Bucket | Approx. field count | Share of registry |
|---|---|---|
| High confidence (≥0.80) | ~15 (mostly plumbing/identity fields, plus fixture-scoped observations) | ~16% |
| Medium confidence (0.30-0.79) | ~20 | ~21% |
| Low confidence (<0.30, nonzero) | ~9 | ~9% |
| Zero confidence / Unknown | ~41 | ~43% (matches Registry §9's ~31% Unknown + ~16% fixture-scoped-only, reclassified here by confidence rather than availability status) |

This confidence distribution is the honest current state of Auspex's
predictor: a solid, well-tested Rule Predictor foundation (§1-2 above)
sitting on top of a large majority of features that either don't exist
yet or have never been checked against reality. Neither half of that
sentence should be read as more true than the other.
