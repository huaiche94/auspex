# Predictor — Day-1 Progress Artifact

Role packet: `agents/predictor.md`. Frozen contracts: `docs/implementation/day1/CONTRACT_FREEZE.md`.
Branch: `day1/predictor`. This wave covered exactly the root nodes unblocked by `contract-integrator`
per the frozen execution DAG: `predictor-02`, `predictor-03`, `predictor-04`.

`predictor-01` (SQLite migrations 0040-0049) is **not** in this wave — it depends on `foundation-06`
(core SQLite migration harness), which is not yet complete. Per Constitution §6/§4 and the DAG, it is
queued, not skipped, not substituted.

---

```yaml
node: predictor-02
status: completed
artifacts:
  - internal/features/doc.go
  - internal/features/prompt.go
  - internal/features/prompt_test.go
validation:
  - "gofmt -l internal/features  # clean"
  - "go build ./internal/features/...  # ok"
  - "go vet ./internal/features/...  # ok"
  - "go test ./internal/features/... -run PromptFeatures -v  # PASS (7 tests, incl. reflection-based no-raw-text-leak assertion)"
commit: 4c22e0b
next_action: predictor-01 (blocked on foundation-06, not started this wave); predictor-05 (blocked on predictor-03/-04 chain continuing next wave)
assumptions:
  - "Prompt-path detection (ExplicitPathCount) uses a lightweight heuristic (contains '/' or a known code-file extension); it is a signal for downstream scope estimation, not a path-security control."
  - "ApproxTokens follows ADD §14.7's formula exactly; TokenConfidence is always domain.ConfidenceLow because it is a tokenizer-free approximation, never exact."
blockers:
  - "predictor-01 blocked pending foundation-06"
```

```yaml
node: predictor-03
status: completed
artifacts:
  - internal/features/taskclass.go
  - internal/features/dto.go
  - internal/features/classifier.go
  - internal/features/classifier_test.go
validation:
  - "gofmt -l internal/features  # clean"
  - "go build ./internal/features/...  # ok"
  - "go vet ./internal/features/...  # ok"
  - "go test ./internal/features/... -run Classifier -v  # PASS (11 subtests, incl. explicit-unknown-on-insufficient-signal cases)"
  - "go test ./internal/features/... -v  # PASS, full package (predictor-02 tests unaffected by predictor-03 additions)"
commit: 6ed8657
next_action: predictor-01 (blocked on foundation-06, not started this wave); predictor-05 (blocked on predictor-03/-04 chain continuing next wave)
assumptions:
  - "RepositoryFeatures/SessionFeatures/ProgressFeatures field sets are this role's own design (agents/predictor.md deliverable 2 leaves 'exact fields' to the owning role); they follow ADD §14.2's four feature-source categories and CONTRACT_FREEZE.md's unknown-is-nil-pointer rule, not a frozen DTO from internal/app/ports.go itself."
  - "ClassifyTask's rule precedence (security > migration > long-doc > ... ) is a day-one heuristic ordering, not derived from any frozen document; it is documented inline and covered by golden-style table tests so a future change is visible as a diff, not a silent behavior shift."
  - "A prompt is classified TaskClassUnknown when ApproxTokens < 2 or when it has neither an actionable verb nor a domain indicator nor a progress-tree document-section hint — this threshold is a conservative placeholder, tuned only by the requirement that unknown must not be a guess."
blockers:
  - "predictor-01 blocked pending foundation-06"
```

```yaml
node: predictor-04
status: completed
artifacts:
  - internal/predictor/doc.go
  - internal/predictor/quantile.go
  - internal/predictor/quantile_test.go
validation:
  - "gofmt -l internal/predictor  # clean"
  - "go build ./internal/predictor/...  # ok"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -run QuantileMonotonic -v  # PASS (16 degenerate-input subtests + 2000-trial random property test)"
  - "go test ./internal/predictor/... -v  # PASS, full package"
commit: 3bbd49f
next_action: predictor-01 (blocked on foundation-06, not started this wave); predictor-05/predictor-06 (blocked on predictor-03/predictor-04, next wave)
assumptions:
  - "EmpiricalQuantiles uses linear interpolation between closest ranks (the 'type 7' estimator, same family R/NumPy default to) rather than nearest-rank; this was not specified in ADD §15.2/§14.1 beyond 'empirical quantiles', so it is a documented implementation choice pinned by TestQuantileKnownValues."
  - "Quantiles{} zero value (P50=P80=P90=0, SampleCount=0) is the designated empty-input sentinel; callers must branch on SampleCount==0 rather than treat 0 as a measured result (mirrors the ADD 'unknown is not zero' principle at the call-site level, even though this leaf utility itself returns plain float64, not *float64, per its narrow scope)."
blockers:
  - "predictor-01 blocked pending foundation-06"
```

## Wave summary

All three assigned nodes (`predictor-02`, `predictor-03`, `predictor-04`) are `completed` with durable
artifacts (committed files) and passing validation commands, on branch `day1/predictor`, not pushed and
not merged. No work was started on `predictor-01` (blocked on `foundation-06`) or `predictor-05`
onward (blocked on this wave's nodes feeding forward into next wave), per the DAG and Constitution §6/§7.

Nothing in this wave touched `internal/policy/**` or `internal/evaluation/**` — those remain untouched,
as instructed.

---

## Wave 2 (predictor-05, predictor-06)

Branch: `day1/predictor`, fast-forward-merged onto `main @ 4f96d7f` (Bootstrap + Wave 1 integration +
ADR-041) by the lead before this wave started. Re-read CONSTITUTION.md, CONTRACT_FREEZE.md's
"Predictor pipeline ports (ADR-041)" section, `docs/adr/0041-predictor-forecast-layer.md`,
`internal/domain/forecast.go`, `internal/app/ports.go`'s ADR-041 section, and `agents/predictor.md`
before starting, per instruction. Assigned exactly `predictor-05` and `predictor-06` this wave;
`predictor-05b`/`predictor-05c` (Token/Quota Forecaster) are explicitly out of scope, reserved for a
future wave, and were not started, stubbed, or scaffolded.

```yaml
node: predictor-05
status: completed
artifacts:
  - internal/predictor/scope/doc.go
  - internal/predictor/scope/coldstart.go
  - internal/predictor/scope/estimator.go
  - internal/predictor/scope/estimator_test.go
validation:
  - "gofmt -l internal/predictor internal/features  # clean"
  - "go build ./internal/predictor/... ./internal/features/...  # ok"
  - "go vet ./internal/predictor/... ./internal/features/...  # ok"
  - "go test ./internal/predictor/... -run Scope  # PASS (internal/predictor: no tests to run, expected; internal/predictor/scope: 6 top-level tests incl. 8-way monotonicity table, unknown-fields test, determinism test, error-propagation test)"
  - "go test ./internal/predictor/... ./internal/features/...  # PASS, full packages, no regressions"
commit: <see final report>
next_action: predictor-06 (dependency predictor-04 already satisfied; predictor-05 completion not required by predictor-06 per ADR-041's structural independence)
assumptions:
  - "app.EstimateScopeRequest (frozen) carries only SessionID/TaskID/RepositoryID — no repository/session feature-lookup port exists yet in internal/app/ports.go (CONTRACT_FREEZE.md explicitly defers this: 'Request/response DTOs ... have minimal fields sufficient to compile ... owning roles MAY find they need additional fields'). Rather than editing internal/app/ports.go (not this role's path) or guessing a DTO shape into it, RuleScopeEstimator depends on its own package-local FeatureSource interface (internal/predictor/scope/estimator.go) that a later wave's storage-backed implementation can satisfy. This is a documented assumption, not a silent contract deviation."
  - "ADD §14.6's cold-start table only names 8 of the 16 §14.3 task classes. The remaining 8 (question, inspection, test-only, bugfix-cross-layer, refactor-local, performance-investigation, security-sensitive, unknown) use a documented nearest-neighbor fallback table (internal/predictor/scope/coldstart.go's coldStartFallback) rather than inventing new ADD-table rows or leaving those classes unhandled."
  - "MinSessionSamples (8) mirrors ADD §15.2's token-predictor sample gate ('count(similar) >= 8') by analogy, since ADD §14 itself does not name an exact session-history sample threshold for scope estimation specifically."
  - "ToolCallsP50/P90, VerificationP50/P90, RetryLoopsP50/P90, DurationP50/P90 are left nil (no tool-call/verification-run telemetry source wired up this wave) — explicitly permitted by forecast.go's own doc comment and the DAG's stated scope for this node ('Scope estimates for files read/changed and LOC')."
blockers: []
```

```yaml
node: predictor-06
status: completed
artifacts:
  - internal/predictor/runway/doc.go
  - internal/predictor/runway/runway.go
  - internal/predictor/runway/runway_test.go
validation:
  - "gofmt -l internal/predictor/runway  # clean"
  - "go build ./internal/predictor/runway/...  # ok"
  - "go vet ./internal/predictor/runway/...  # ok"
  - "go test ./internal/predictor/runway/...  # PASS (15 tests, incl. a broad property-style sweep over used%/delta/interval combinations asserting no panic/NaN/Inf/out-of-range RiskScore, plus explicit outlier-rule and threshold tests)"
  - "go build ./...  # ok, whole module"
  - "go test ./internal/...  # PASS, whole module, no regressions"
commit: <see final report>
next_action: none — both Wave 2 nodes complete; predictor-05b/predictor-05c explicitly deferred to a future wave per instruction; stopping here
assumptions:
  - "GracefulPauseService.Observe (internal/app/ports.go) is the sole frozen consumer named for domain.RunwayForecast; this wave implements the scoring function (runway.Scorer.Score) that role's Observe implementation calls into per runtime observation, not the observation loop or pause orchestration itself — no runtime/scheduling code was added, consistent with the predictor role boundary ('No provider JSON parsing, Git commands, checkpoint creation, or process interruption')."
  - "Per ADD §15.6's calibration gate (>=20 valid runway samples, held-out cohort evaluation, ECE<=0.08, Brier score recorded, model artifact calibrated=true, quota sample freshness) and the cold-start contract in agents/predictor.md, HitProbability is always nil and Calibrated is always false this wave — no durable burn-rate telemetry store exists yet (depends on claude-provider/foundation SQLite work in a later wave, exactly as ADR-041 notes for predictor-05c). RiskScore is always populated (never nil) using the ADD §15.7 uncalibrated fallback thresholds (current>=95% -> critical/1.0, projected_used_p90>=100% within horizon -> high/0.85, projected_used_p90>=95% -> medium-high/0.65, else scaled continuously by remaining headroom) so policy still has a usable, explicitly-uncalibrated signal."
  - "Scorer is stateless: it takes the current QuotaObservation plus an optional single Previous observation per call (matching GracefulPauseService.Observe's own per-call RuntimeObservation{SessionID, Quota domain.QuotaObservation} shape, one observation at a time) rather than owning durable multi-sample history itself — history storage belongs to whichever role owns the observation store, outside this role's boundary. With only one interval available, BurnRateP50 and BurnRateP90 collapse to the same single observed rate (no distribution to resample from); ADD §15.5's full N=1000-draw empirical-bootstrap simulation is the calibrated path gated by §15.6 and is deliberately not attempted this wave (Constitution §7 rule 10)."
  - "Outlier handling follows ADD §15.4 directly: negative delta => treated as reset/correction, not a negative rate; interval < 2s => not counted; rate above a conservative default sanity cap (50 percentage-points/minute; no provider-specific cap exists yet with no live telemetry wired up) => marked anomalous and dropped; sample staler than 5 minutes (chosen relative to the 10-minute default horizon; ADD does not name an exact staleness duration) => lowers Confidence rather than being dropped."
  - "Multiple simultaneous QuotaObservation limit windows are combined via CombineWindows, which takes max(RiskScore) across windows — matching ADD §15.5's explicit v1 default ('若 windows 高度相關，policy 可用保守 max(P_i)；v1 預設取 max，避免錯誤獨立假設') rather than the independence-assuming 1-Π(1-P_i) formula, which is reserved for the calibrated path."
  - "ADD §15.8 reset-awareness: when ResetsAt falls within the scored horizon, RiskScore is pulled down toward a low headroom-available value regardless of current usage/burn rate, since the window will not actually be exhausted before it resets."
blockers: []
```

## Wave 2 summary

Both assigned nodes (`predictor-05`, `predictor-06`) are `completed` with durable artifacts and passing
validation commands, on branch `day1/predictor`. `predictor-05b` (Token Forecaster) and `predictor-05c`
(Quota Forecaster) were deliberately not started — reserved for a future wave per explicit instruction,
despite `predictor-05b` nominally depending on `predictor-05` which is now done. No `RuleTokenForecaster`
or `RuleQuotaForecaster` (or anything satisfying the `TokenForecaster`/`QuotaForecaster` interfaces) was
written. No other role's paths were touched. No merge/rebase onto `main` was performed this wave (branch
was already up to date at `4f96d7f` from the lead's fast-forward merge before this wave began).

---

## Lint correction (post-Wave 2, cross-role integration pass)

A cross-role integration validation pass (`golangci-lint run` against the full merged Wave 2 tree)
surfaced 3 findings in files owned by this role. This is a corrective commit responding to those
findings, not a new DAG node — no `predictor-05b`/`predictor-05c`/`predictor-07` or other work was
started.

- `internal/predictor/runway/runway.go:120` — gocritic `appendAssign` (append result assigned to a
  different slice than the one appended to: `forecast.ReasonCodes = append(reasons, ...)`). Investigated
  whether `reasons` was reused afterward: it is declared fresh (`nil`) immediately above this branch and
  the branch returns right after this line, so there was no aliasing with later code — **cosmetic only,
  not a real bug**. Fixed by appending directly to `forecast.ReasonCodes` instead of the local `reasons`
  variable, preserving identical behavior.
- `internal/predictor/scope/estimator_test.go:64,67` — staticcheck QF1001 (De Morgan's law
  simplification). Rewrote `if !(*p50 <= *p80) {` as `if *p50 > *p80 {` and
  `if !(*p80 <= *p90) {` as `if *p80 > *p90 {`, logically identical.

validation:
  - "gofmt -l internal/predictor  # clean"
  - "go build ./internal/predictor/...  # ok"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -race  # PASS, all packages"
  - "golangci-lint not installed in this environment; underlying gocritic/staticcheck patterns fixed per their documented rules"

Only the two named files were touched; no other role's paths were touched.

---

## Wave 3 (predictor-05b)

Branch: `day1/predictor`, continuing from `4285e12` (Wave 2 + lint correction), already fully merged
into `main`. Re-read CONSTITUTION.md, CONTRACT_FREEZE.md's "Predictor pipeline ports (ADR-041)"
section, `docs/adr/0041-predictor-forecast-layer.md`, `internal/domain/forecast.go`,
`internal/app/ports.go`'s ADR-041 section, `agents/predictor.md`, `Preflight_ADD.md` §15.1-15.2, and
`Preflight_Predictor_Design_Supplement.md`'s "Stage 2 — Token Prediction"/"MVP Heuristic Formula"
sections before starting, per instruction. No merge/rebase onto `main` was performed or needed —
predictor-05, predictor-04's quantile utilities, and ADR-041's frozen types were all already on this
branch. Assigned exactly `predictor-05b` (Token Forecaster) this wave; `predictor-05c` (Quota
Forecaster) and `predictor-07` (Risk Combiner) were explicitly out of scope and were not started,
stubbed, or scaffolded.

```yaml
node: predictor-05b
status: completed
artifacts:
  - internal/predictor/token/doc.go
  - internal/predictor/token/coldstart.go
  - internal/predictor/token/forecaster.go
  - internal/predictor/token/forecaster_test.go
validation:
  - "gofmt -l internal/predictor/token  # clean"
  - "go build ./...  # ok, whole module"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -run TokenForecast -v  # PASS (7 top-level tests: monotonicity table across 9 cases, never-calibrated-this-wave gate check across 3 sample-count cases, cold-start reason code, determinism, source-error propagation across 4 sources, multiplier-cap explosion guard, degenerate/negative-sample no-panic sweep)"
  - "go test ./internal/predictor/... ./internal/features/... -race  # PASS, full packages, no regressions"
  - "golangci-lint run ./...  # zero issues in files owned by this role; 3 pre-existing issues remain in internal/hooks/claude, internal/clock, internal/idgen (not owned by predictor — noted, not fixed)"
commit: <see final report>
next_action: none — predictor-05b is the sole assigned Wave 3 node; predictor-05c/predictor-07 explicitly deferred per instruction; stopping here
assumptions:
  - "app.ForecastTokensRequest (frozen) carries only SessionID and the Stage-1 domain.ScopeEstimate — no task-classification or session-token-history lookup port exists yet in internal/app/ports.go (same Bootstrap gap already documented for predictor-05's FeatureSource). RuleTokenForecaster depends on its own package-local internal/predictor/token.FeatureSource interface (Classification, Session, Progress, RecentSimilarTurnTokens) rather than editing internal/app/ports.go (not this role's path) or guessing a DTO shape into it. A later wave's storage-backed implementation can satisfy this interface; a fake satisfies it in tests."
  - "Cold-start-only scope: no durable historical telemetry store exists yet this wave (the same gap already established for predictor-05/predictor-06's cold-start-only implementations — ADR-041's own cold-start note for predictor-05c applies by the same reasoning to predictor-05b, since both depend on claude-provider-05/foundation-06 landing in a later wave). The >=8-similar-samples empirical branch (ADD §15.2's exact gate) is implemented and exercised by tests, so a future FeatureSource backed by real storage activates it for free, but every result this wave is Calibrated=false with Confidence never exceeding ConfidenceMedium (reached only via the empirical-base branch; ConfidenceLow otherwise) — never a fabricated calibrated claim."
  - "P80 assumption (explicitly flagged by this node's scope): ADD §15.2's base-quantile description names only base_p50/base_p90 ('weighted_quantile(tokens, 0.50)' / '0.90'), no base_p80. Rather than inventing an unrelated third empirical quantile, TokensP80 is interpolated between the (multiplier-adjusted) P50 and P90 in log-space at a 60%-of-the-way-to-P90 weight (internal/predictor/token/forecaster.go's interpolateP80), matching the right-skewed shape of Preflight_Predictor_Design_Supplement.md's own P50/P80/P95 worked example (38000/61000/94000). This is a documented assumption, not a spec-derived value."
  - "ADD §14.6's cold-start table gives a *relative token multiplier* per task class, not an absolute token count. internal/predictor/token/coldstart.go anchors that relative scale to a bootstrap absolute baseTurnTokens=6000 constant (documented as a bootstrap starting point, explicitly not a measured universal benchmark, mirroring the ADD's own disclaimer for the sibling files/lines table). The 8 task classes ADD §14.6 does not name use a documented nearest-neighbor fallback table, independent from (not imported from) scope/coldstart.go's own fallback table, since the two tables measure different quantities and must not silently couple."
  - "verification_multiplier's build_required term has no direct ScopeEstimate/PromptFeatures signal wired up this wave; it is treated as implied by RequiresIntegration (an integration-test-requiring turn is assumed to also require a build) rather than left uncounted — documented assumption."
  - "complexity_multiplier's repository_wide term has no direct ScopeEstimate boolean; approximated by FilesChangedP90 >= 15, mirroring scope.RuleScopeEstimator's own ReasonLargeFileScope threshold, rather than left uncounted."
  - "retry_multiplier and progress_multiplier read SessionFeatures.RetryRate and ProgressFeatures.CompletedRatio respectively (nil or !ok both mean 'unknown' -> neutral multiplier 1.0 with a cold-start reason code, never a fabricated zero). progress_multiplier's 'remaining_critical_path_cost / original_task_cost' ratio (ADD §15.2) is approximated as 1 - CompletedRatio, since no separate cost model exists yet — documented assumption, ADD §15.2 does not specify how critical-path cost is measured."
  - "ambiguity_multiplier's mapping from the ADD's four named bands (1.0/1.2/1.5/2.0) to PromptFeatures signals (explicit paths + acceptance criteria named -> 1.0; explicit paths only -> 1.2; no explicit paths, not open-ended -> 1.5; OpenEndedIndicator -> 2.0) is this package's own documented interpretation, since the ADD names the bands and multipliers but not the exact feature-to-band rule."
  - "Per-multiplier cap (3.0) and combined geometric-mean cap (6.0) are this package's own conservative defaults implementing ADD §15.2's explicit 'avoid multiplier explosion, do caps' instruction — no exact cap values are specified in the ADD. Verified by TestTokenForecastMultiplierCapsPreventExplosion with intentionally extreme/absurd inputs (10^9 lines changed, retry rate of 100, negative completed ratio) asserting the result stays within a cap-derived bound and remains non-negative/monotonic."
  - "No Missing_Telemetry_Report.md file was found anywhere in this repository (searched exhaustively); the cold-start-only scope for this node is instead corroborated directly by ADR-041's own text (predictor-05c's cold-start note, same reasoning applies to predictor-05b) and by the absence of any durable telemetry store in this branch's dependencies (claude-provider-05/foundation-06 not yet landed) — noted here as a discrepancy between the assigning instruction and repository contents, not acted on further since the conclusion (cold-start-only) is unaffected."
blockers: []
```

## Wave 3 summary

The single assigned node (`predictor-05b`) is `completed` with durable artifacts and passing validation
commands, on branch `day1/predictor`. `predictor-05c` (Quota Forecaster) and `predictor-07` (Risk
Combiner) were deliberately not started, per explicit instruction, despite `predictor-05c` nominally
depending on `predictor-05b` which is now done. No `RuleQuotaForecaster`, `RiskCombiner` implementation,
or anything beyond `RuleTokenForecaster` was written. No other role's paths were touched. No merge/rebase
onto `main` was performed or needed this wave.
