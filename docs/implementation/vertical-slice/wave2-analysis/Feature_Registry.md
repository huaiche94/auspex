# Feature Registry

> 🌐 English | [繁體中文](Feature_Registry.zh-TW.md)

| Field | Value |
|---|---|
| Phase | 3.7 — Post Phase 2 Analysis |
| Status | **Canonical at creation (Phase 3.7, Phase 2 analysis) — now a point-in-time snapshot.** When written, this was declared the single source of truth for every feature Auspex's predictor pipeline uses or will use, and predictor work of that phase referenced features through it (a Constitution-analogous rule the repository owner stated at the time). Because it lives in the frozen `wave2-analysis/` directory it is **not continuously maintained here**: the live sources of truth going forward are the code (`internal/features/**`, `internal/domain/**`) and the ADD's §14/§15/§16 feature lists — the same two sources every entry below is already traceable to. Read it as the Phase-2 reference it is, reconciling against those live sources. |
| Method | Every feature below is grounded in either (a) real, verified code (`internal/features/**`, `internal/domain/**`) or (b) the ADD's own already-specified §14/§15/§16 feature lists. No feature is invented for this registry that isn't traceable to one of those two sources. |

## How to read this registry

Each feature has two tables: **Identity & Provenance** (what it is, where
it comes from, how much to trust it right now) and **Suitability &
Operations** (which predictor tier can use it, what stage it feeds, and
its operational cost). Split for table-width readability — together they
cover every field the repository owner specified.

`Current Availability` values: **Available** (a real code path produces
this today, even if only from test fixtures, not yet live telemetry),
**Derived** (computed from other Available features, no independent
collection), **Estimated** (a cold-start default or heuristic substitutes
for it today), **Unknown** (no code path produces it at all yet).

---

## 1. Prompt Features

Source: `internal/features/prompt.go` (`PromptFeatures`, real,
Phase 1-built, verified). Ground truth for none of these — every field is
itself a derived signal *about* the prompt, not a fact the world confirms
or denies independently.

### 1a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `ByteLength` | Raw prompt byte count | int / bytes | `ExtractPromptFeatures` | Observed | 1.00 | No | Available | Low |
| `RuneCount` | Raw prompt rune count | int / runes | same | Observed | 1.00 | No | Available | Low |
| `LineCount` | Raw prompt line count | int / lines | same | Observed | 1.00 | No | Available | Low |
| `ApproxTokens` | ADD §14.7 heuristic token approximation | int / tokens | same | Estimated | 0.30 (§14.7 mandates `confidence=low`) | No | Available | Medium |
| `TokenConfidence` | Confidence tag for `ApproxTokens` | `domain.Confidence` enum | same | Derived | N/A (a confidence value, not itself confidence-scored) | No | Available | Low |
| `ExplicitPathCount` | Count of whitespace-delimited path-shaped tokens | int | same | Observed (heuristic pattern match) | 0.60 | No | Available | Medium |
| `ListItemCount` | Bullet/numbered list line count (deliverable-count proxy) | int | same | Observed | 0.70 | No | Available | Low |
| `AcceptanceCriteriaCount` | Checkbox-style line count | int | same | Observed | 0.80 | No | Available | Medium |
| `HasFixVerb` / `HasImplementVerb` / `HasRefactorVerb` / `HasInvestigateVerb` / `HasMigrateVerb` | Verb-presence flags | bool ×5 | same | Observed (keyword match) | 0.60 (self-reported: broad-recall word lists, some false-positive risk per `Wave2_Lessons.md` §1 issue #5) | No | Available | High |
| `MentionsTests` / `MentionsSchemaOrAPI` / `MentionsSecurity` / `MentionsPerformance` / `MentionsDocumentation` | Keyword-indicator flags | bool ×5 | same | Observed (keyword match) | 0.60 | No | Available | High |
| `LongDocumentIndicator` | Chapter/section/report phrasing detected | bool | same | Observed | 0.60 | No | Available | Medium |
| `QuestionIndicator` | Question-only prompt detected | bool | same | Observed | 0.60 | No | Available | Medium |

### 1b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| `ByteLength`/`RuneCount`/`LineCount` | Yes | Yes | Yes | Scope Estimation | Per turn | Low (pure computation on already-in-memory prompt) | Not persisted independently — derived on demand from prompt hash lineage per ADD §14.7's privacy rule | N/A — never stored raw |
| `ApproxTokens` | Yes | Yes | Low weight (superseded by real usage once available) | Scope Estimation, Token Forecast | Per turn | Low | Same as above | Same as above |
| `ExplicitPathCount`/`ListItemCount`/`AcceptanceCriteriaCount` | Yes | Yes | Yes | Scope Estimation | Per turn | Low | Same as above | Same as above |
| Verb/keyword flags (10 fields) | Yes | Yes | Yes | Scope Estimation, Task Classification | Per turn | Low | Same as above | Same as above |

**Privacy note, not a separate column but load-bearing:** none of these
fields retain the raw prompt text itself — `predictor-02`'s privacy test
(Constitution §7) verifies this by reflection walk + JSON marshal + `%+v`
format check against a planted canary string. This registry entry exists
*because* of that guarantee, not despite it.

---

## 2. Repository Features

Source: `internal/features/dto.go` (`RepositoryFeatures`, type frozen and
tested via fakes in `predictor-05`, but **no real repository-introspection
producer exists yet** — see §0 caveat below).

### 2a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `TrackedFileCount` | Git-tracked file count | int | (declared, not wired) | Unknown | 0.00 | No | Unknown | Medium |
| `LanguageCount` | Distinct language count | int | (declared, not wired) | Unknown | 0.00 | No | Unknown | Low |
| `GoModuleCount` / `GoPackageCount` | Go module/package graph size | int | ADD §14.4's `go list -deps -json` (specified, not implemented) | Unknown | 0.00 | No | Unknown | Medium |
| `DotNetProjectRefs` | .NET project reference count | int | ADD §14.4's `dotnet list <project> reference` (specified, not implemented) | Unknown | 0.00 | No | Unknown | Low |
| `DirtyFileCount` / `DirtyLineCount` | Uncommitted change size | int | `internal/gitx` (Repository Checkpoint role, real code exists — `ParsePorcelainV2`, `DiffNumstat`) **but not yet wired to `RepositoryFeatures`** | Available (underlying gitx data), Unknown (as a `RepositoryFeatures` field) | 0.00 as wired; the underlying `gitx` primitives are Observed at 1.00 | No | Unknown (wiring gap, not a data gap — see §0) | High |
| `TargetDirFanOut` | Directory fan-out of the likely target | int | (declared, not wired) | Unknown | 0.00 | No | Unknown | Medium |
| `TestProjectCount` | Test project/package count | int | (declared, not wired) | Unknown | 0.00 | No | Unknown | Low |
| `IsMonorepo` / `IsWorktree` | Structural repo flags | bool | `internal/gitx` resolver (real code exists) **but not yet wired** | Available (underlying), Unknown (as wired feature) | 0.00 as wired | No | Unknown (wiring gap) | Low |
| `RecentChangedPathCount` | Recently-touched path count | int | (declared, not wired) | Unknown | 0.00 | No | Unknown | Medium |

**§0 caveat, important:** this is the clearest case in the whole registry
of a **wiring gap, not a data gap**. `internal/gitx` (Phase 1/2,
`checkpoint-b02`/`b03`) already computes dirty-file/line counts,
worktree-vs-main detection, and a full status/numstat parse — real,
tested, Observed-quality data. But no code path currently feeds that data
into a populated `RepositoryFeatures` value; `predictor-05`'s
`FeatureSource.Repository()` method exists only as an interface with a
test fake behind it, per this phase's own verification (§ code read
above). Closing this gap is wiring work, not new-data-collection work —
flagged explicitly in `Feature_Gap_Report.md` and `ADR_Recommendations.md`.

### 2b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| All `RepositoryFeatures` fields | Yes (once wired) | Yes (once wired) | Yes (once wired) | Scope Estimation | Per turn, cache-eligible (ADD §14.4: "background-refresh" for expensive calls) | Medium (Git/language-server calls; ADD explicitly warns against reading the whole repository) | Not yet defined — no persistence layer for repository snapshots exists | Not yet defined |

---

## 3. Session Features

Source: `internal/features/dto.go` (`SessionFeatures`, same status as
Repository Features: type frozen, no live producer).

### 3a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `RecentTurnUsageP50/P80/P90` | Empirical recent-turn token usage quantiles | `*float64` ×3 | (declared, not wired — needs A1/B1 telemetry, see `Missing_Telemetry_Report.md`) | Unknown | 0.00 | No | Unknown | Critical |
| `ChangedFilesRecentP50/P90` / `ChangedLinesRecentP50/P90` | Empirical recent-turn scope quantiles | `*float64` ×4 | same | Unknown | 0.00 | No | Unknown | High |
| `RetryRate` / `TestFailureRate` | Empirical recent failure rates | `*float64` ×2 | same | Unknown | 0.00 | No | Unknown | High |
| `ToolOutputBytesP50` | Empirical tool-output size | `*int64` | same | Unknown | 0.00 | No | Unknown | Low |
| `ContextGrowthRateP50` | Empirical context growth rate | `*float64` | same | Unknown | 0.00 | No | Unknown | High |
| `CompactionCount` | Session compaction count | int | same | Unknown | 0.00 | No | Unknown | Medium |
| `CheckpointAge` | Time since last checkpoint | `*time.Duration` | Partially derivable from `domain.StateCheckpoint.CreatedAt` (frozen type, no live producer — Progress Tree/State Checkpointing, `checkpoint-a01`+, not built) | Unknown | 0.00 | No | Unknown | Medium |

All `SessionFeatures` fields use pointer semantics — `nil` correctly means
unknown, not zero, per the type's own doc comment (verified in source).
This is a design strength worth noting even though every field's current
availability is `Unknown`: when this feature set *does* get wired to real
data, the "unknown vs. zero" discipline is already built in, not something
to retrofit.

### 3b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| All `SessionFeatures` fields | Yes (once wired) | Yes (once wired, this is exactly the tier this data primarily serves — Version 2 "Statistical Predictor" per the Design Supplement) | Yes (once wired) | Scope Estimation, Token Forecast | Per turn, requires historical accumulation (ADD §15.2: needs `count(similar) >= 8` before exiting cold-start) | Medium (requires querying persisted turn history) | Not yet defined — `foundation-06`'s `turns`/`turn_usage` tables (ADD §12.2), not built | Not yet defined |

---

## 4. Provider Features

Source: `internal/domain/capability.go` (`ProviderCapabilities`, real,
Bootstrap-frozen), `internal/domain/usage.go` (`UsageObservation`,
`QuotaObservation`, `ContextObservation`, real, Bootstrap-frozen),
`claude-provider-01`/`-04` (real parsers/normalizers, tested against
fixtures — never a live session).

### 4a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `ProviderCapabilities.*` (19 bool fields) | Per-provider capability flags | bool ×19 | ADD §8.6, frozen type; populated by design intent, not yet by a live `ProviderCapabilityReader` implementation | Unknown (no real reader built) | 0.00 | No | Unknown | High |
| `UsageObservation.{Input,CachedInput,CacheCreation,CacheRead,Output,Reasoning}Tokens` | Per-turn token breakdown | `*int64` ×6 | `claude-provider` status-line parser (real code) | Available **only against fixtures**; Unknown against live data | 1.00 against fixtures, 0.00 against a real session | No (fixture-derived, not from a real turn) | Available (fixture-scoped only) | Critical |
| `QuotaObservation.UsedPercent` | Five-hour/seven-day quota percentage | `*float64` | same | Available (fixture-scoped only) | same caveat as above | No | Available (fixture-scoped only) | Critical |
| `QuotaObservation.ResetsAt` | Quota window reset timestamp | `*time.Time` | same | Available (fixture-scoped only) | same caveat | No | Available (fixture-scoped only) | High |
| `ContextObservation.{UsedTokens,WindowTokens,UsedPercent}` | Context window usage | `*int64`/`*int64`/`*float64` | same | Available (fixture-scoped only) | same caveat | No | Available (fixture-scoped only) | Critical |

**The "Available (fixture-scoped only)" pattern recurs across this whole
section and deserves one explicit, unambiguous statement:** `claude-provider`'s
parsers and normalizer are real, tested, working code — verified
independently by the lead during Phase 1/2 review, including privacy and
idempotency tests. But every test they pass is against a hand-constructed
JSON fixture, not a byte ever emitted by a real Claude Code session. This
registry deliberately does not call that data "Available" in the
unqualified sense used for e.g. `internal/gitx`'s repository data (§2),
because a fixture proves the *parser* works, not that the *feature* has
ever been observed in the wild.

### 4b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| `ProviderCapabilities.*` | Yes | Yes | Yes | All stages (capability gates every downstream feature) | Per session (capabilities don't change mid-session) | Low once implemented (one detection call) | Not yet defined | Not yet defined |
| `UsageObservation.*` | Yes | Yes | Yes | Token Forecast, Quota Forecast | Per turn (or per status-line update, higher frequency) | Low (already-emitted provider data, just needs parsing — done) to Medium (persistence, not done) | `foundation-06`'s `turn_usage` table (ADD §12.2), not built | Per ADD §27, raw values retained; prompt text never retained (unrelated concern, correctly separate) |
| `QuotaObservation.*` | Yes | Yes | Yes | Quota Forecast, Runway Prediction | Per status-line update | Low (parsing done) / Medium (persistence not done) | `foundation-06`'s `quota_observations` table, not built | Not yet defined |
| `ContextObservation.*` | Yes | Yes | Yes | Quota Forecast (context projection), Risk Combination | Per turn | Low / Medium (same split as above) | `foundation-06`'s `context_observations` table, not built | Not yet defined |

---

## 5. Execution Features

Source: `internal/domain/forecast.go` (`ScopeEstimate`, `TokenForecast`,
`QuotaForecast`, real, ADR-041-frozen), `internal/predictor/scope`
(`RuleScopeEstimator`, real, `predictor-05`), `internal/predictor/runway`
(real, `predictor-06`).

### 5a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `ScopeEstimate.FilesReadP50/P80/P90` | Predicted files-read quantiles | `*int64` ×3 | `RuleScopeEstimator` (real, Phase 2) | Estimated | Cold-start default or session-blend, never ground-truthed | No — this is a *prediction*, not an observation | Estimated | High |
| `ScopeEstimate.FilesChangedP50/P80/P90` | Predicted files-changed quantiles | `*int64` ×3 | same | Estimated | same | No | Estimated | Critical |
| `ScopeEstimate.LinesChangedP50/P80/P90` | Predicted lines-changed quantiles | `*int64` ×3 | same | Estimated | same | No | Estimated | Critical |
| `ScopeEstimate.ToolCallsP50/P90`, `VerificationP50/P90`, `RetryLoopsP50/P90`, `DurationP50/P90` | Predicted tool/verification/retry/duration | `*int64` ×8 | same | Unknown | N/A | No | Unknown (deliberately left `nil` this phase — verified by `TestEstimateScopeUnknownFieldsStayNil`) | Medium |
| `ScopeEstimate.RequiresUnitTests/RequiresIntegration/CrossProject/MigrationLikely/SecuritySensitive` | Predicted boolean signals | bool ×5 | same | Estimated (keyword/heuristic-derived) | 0.50-0.60 | No | Available (populated, low confidence) | High |
| `TokenForecast.TokensP50/P80/P90` | Predicted token cost | int64 ×3 | (not built — `predictor-05b`, deferred past Phase 2) | Unknown | 0.00 | No | Unknown | Critical |
| `QuotaForecast.ProjectedQuotaUsedP90` / `ProjectedContextUsedP90` | Projected quota/context position | `*float64` ×2 | (not built — `predictor-05c`, deferred past Phase 2) | Unknown | 0.00 | No | Unknown | Critical |
| `RunwayForecast.RiskScore` | 10-minute quota-hazard score | float64 | `internal/predictor/runway` (real, `predictor-06`) | Estimated (uncalibrated fallback per ADD §15.7) | Always uncalibrated this phase (verified by `TestScoreNeverCalibratedNeverPanics`) | No | Available (score only, not probability) | Critical |
| `RunwayForecast.HitProbability` | Calibrated 10-minute hit probability | `*float64` | same | Unknown | 0.00 (ADR-026/ADD §15.6: correctly always `nil` until calibration gate passes) | No | Unknown (by design) | Critical |

### 5b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| `ScopeEstimate.*` | Yes (this IS the Rule Predictor's output) | Yes (as a Version 2 input feature) | Yes | Scope Estimation | Per turn | Low (pure computation, no I/O once inputs are available) | `predictor-09` evaluation persistence, not built | Not yet defined |
| `TokenForecast.*` | Not yet built | Not yet built | Not yet built | Token Forecast | Per turn (once built) | Low (computation, per ADD §15.2 formula) | Same as above | Not yet defined |
| `QuotaForecast.*` | Not yet built | Not yet built | Not yet built | Quota Forecast, Context Forecast | Per turn (once built) | Low-Medium (per ADD §15.3/15.9) | Same as above | Not yet defined |
| `RunwayForecast.RiskScore` | Yes | Yes | Low weight (score, not calibrated probability, until ADD §15.6 gate passes) | Runway Prediction | Continuous during an active managed turn (ADD §15.4) | Low (arithmetic over live burn-rate samples) | Not yet defined | Not yet defined |
| `RunwayForecast.HitProbability` | No (never populated pre-calibration) | No (same) | No (same) | Runway Prediction | N/A until calibrated | N/A | N/A | N/A |

---

## 6. Checkpoint Features

Source: `internal/domain/artifact.go` (`ArtifactRef`, `EvidenceRef`),
`internal/domain/checkpoint.go` (`StateCheckpoint`), `internal/gitx`
(`Fingerprint`, real, `checkpoint-b02`/`b03`).

### 6a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `Fingerprint.ComputeDigest()` | Canonical repository-state digest | string (SHA-256 hex) | `internal/gitx` (real, `checkpoint-b03`, independently verified for determinism/order-independence) | Observed | 1.00 | Yes — a cryptographic digest of real Git state | Available | High |
| `Fingerprint.HeadOID` / `Branch` | Repository identity | string ×2 | same | Observed | 1.00 | Yes | Available | High |
| `Fingerprint.Entries` (status entries) | Working-tree/index status | `[]Entry` | same | Observed | 1.00 | Yes | Available | Medium |
| `ArtifactRef.SHA256` / `Bytes` | Artifact identity/size | string / int64 | `internal/domain/artifact.go` (frozen type; no producer built — Progress Tree/State Checkpointing role, `checkpoint-a01`+, not built) | Unknown | 0.00 | Yes (would be, once produced — a checksum is inherently ground truth) | Unknown | Critical |
| `StateCheckpoint.ProgressTreeVersion` / `CompletedNodeIDs` | Progress state snapshot | int64 / `[]ProgressNodeID` | same (frozen type, no producer) | Unknown | 0.00 | Yes (once produced) | Unknown | Critical |
| `StateCheckpoint.IntegritySHA256` | Checkpoint integrity digest | string | same | Unknown | 0.00 | Yes (once produced) | Unknown | Critical |

### 6b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| `Fingerprint.*` | N/A — used for resume-safety verification, not prediction | N/A | N/A | Not a prediction-stage feature; consumed by Graceful Pause/Resume validation (ADD §15.8, FR-149) | Per checkpoint event | Low (Git plumbing calls, already argv-safe per verified implementation) | Not yet defined — `repository_checkpoints` table (ADD §12.2), not built | Not yet defined |
| `ArtifactRef.*` / `StateCheckpoint.*` | N/A (same reason) | N/A | N/A | Not a prediction-stage feature | Per node completion | Not yet measurable (not built) | `state_checkpoints`/`artifacts` tables (ADD §12.2), not built | Not yet defined |

**Note:** Checkpoint Features are included in this registry for
completeness (the repository owner's instruction names this as one of the
8 required groups) but are correctly *not* predictor inputs — they are
integrity/resume-safety primitives. Their inclusion here is about
completeness of the canonical feature dictionary, not an implication that
they feed Scope/Token/Quota/Risk prediction.

---

## 7. Calibration Features

Source: `internal/domain/measurement.go` (`Confidence`, `Calibrated`
pattern used throughout every forecast/observation type), ADD §15.6.

### 7a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `Confidence` enum (`Exact`/`High`/`Medium`/`Low`/`Unavailable`) | Per-observation trust tag | enum, 5 values | `internal/domain/measurement.go` (real, Bootstrap-frozen) | Observed (the enum itself is real code); Derived (any given instance's value is a judgment call by its producer) | N/A (a confidence value is not itself confidence-scored — this is intentional, not a gap) | No | Available | Critical |
| `Calibrated` bool (present on `TokenForecast`, `QuotaForecast`, `RiskComponent`, `RunwayForecast`) | Whether a score has passed the calibration gate | bool | same pattern, real | Observed | N/A | No | Available (always `false` in every producer this phase, correctly) | Critical |
| ECE (Expected Calibration Error) | Calibration quality metric | float64, 0-1 | ADD §15.6 (specified, not implemented — needs ≥20 valid samples) | Unknown | 0.00 | Yes (once computed, it is a real statistic over real outcomes) | Unknown | Critical |
| Brier score | Calibration quality metric | float64 | same | Unknown | 0.00 | Yes (once computed) | Unknown | High |

### 7b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| `Confidence` / `Calibrated` | Yes — required on every output | Yes | Yes | All stages | Per prediction | Low (metadata, not separately collected) | Alongside whatever record it annotates | Same as the annotated record |
| ECE / Brier score | No (Rule Predictor tier never claims calibration) | Yes (this is the gate that promotes a predictor from uncalibrated to calibrated, ADD §15.6) | Yes | Meta — governs whether any tier's output may be shown as a probability | Per held-out evaluation cycle | High (requires accumulated labeled outcomes, A5/A6 in `Missing_Telemetry_Report.md`) | Not yet defined | Not yet defined |

---

## 8. Telemetry Features

Source: `pkg/protocol/v1/event.go` (`Event`, real, Bootstrap-frozen),
`internal/telemetry/claude/normalizer.go` (real, `claude-provider-04`).

### 8a. Identity & Provenance

| Feature | Description | Data type / Unit | Source | Provenance | Confidence | Ground Truth | Current Availability | Importance |
|---|---|---|---|---|---|---|---|---|
| `Event.EventType` | Closed taxonomy tag (52 values) | enum | `pkg/protocol/v1` (real) | Observed | 1.00 | No (a classification, not a fact) | Available | High |
| `Event.IdempotencyKey` | Deduplication key | string | `claude-provider-04` normalizer (real, tested) | Observed | 1.00 | Yes (deterministic digest, verified) | Available | High |
| `Event.SchemaVersion` | Wire-format version tag | string, e.g. `auspex.event.v1` | Bootstrap-frozen constant | Observed | 1.00 | Yes | Available | Medium |
| `Event.Payload` | Normalized, redacted event body | `map[string]any` | `claude-provider-04` normalizer | Observed (against fixtures only — same caveat as §4) | 1.00 against fixtures | No | Available (fixture-scoped only) | Critical |
| `Event.ObservedAt` / `OccurredAt` | Timestamps | `time.Time` ×2 | same | Observed (against fixtures/`domain.Clock` injection) | 1.00 | Yes | Available (fixture-scoped only) | Medium |

### 8b. Suitability & Operations

| Feature | Rule Predictor | Statistical Predictor | ML Predictor | Prediction Stage | Update frequency | Collection cost | Storage location | Retention |
|---|---|---|---|---|---|---|---|---|
| `Event.*` (all fields) | Indirect — feeds the observation types in §4, not consumed directly | Indirect | Indirect | Feeds Provider Features (§4), which feed all prediction stages | Per provider event (potentially very high frequency during an active turn) | Low (parsing/normalization is done; persistence is not — see B1 in `Missing_Telemetry_Report.md`) | `events` table implied by ADD §11 but not named in §12.2's explicit table list — flagged as a gap in `Feature_Gap_Report.md` | Not yet defined; ADD §27 governs raw-payload redaction requirements, already enforced by `claude-provider`'s privacy tests |

---

## 9. Registry completeness statement

This registry covers every feature this codebase's frozen types and real
implementations currently define, plus every feature the ADD's §14-§16
text specifies but no code yet produces. It does **not** invent features
beyond those two sources. Total feature count: **~95 individual fields
across 8 groups** (exact count depends on how pointer-array fields like
`FilesReadP50/P80/P90` are counted — as 1 feature-family or 3 discrete
fields; this registry lists them as discrete fields above for precision).

**Availability summary across the whole registry:**

| Current Availability | Approx. field count | Share |
|---|---|---|
| Available | ~35 | ~37% |
| Available (fixture-scoped only, not yet live) | ~15 | ~16% |
| Estimated | ~15 | ~16% |
| Unknown | ~30 | ~31% |

See `Feature_Gap_Report.md` for what closing the Unknown/fixture-scoped
gaps would require, and `Prediction_Confidence_Report.md` for the
derived confidence view over this same data.
