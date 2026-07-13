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

---

## Wave 4 (predictor-01, predictor-05c)

Branch: `day1/predictor`, continuing from `22fde28` (Wave 3, `predictor-05b`). Per explicit instruction,
`main` was merged first (`git merge main -m "Sync main (Wave 3) before predictor-01/05c"`) — a clean
fast-forward from `22fde28` to `ca7062f`, bringing in foundation-06/08, `predictor-05b`'s own already-merged
copy, `runtime-b01`, `qa-01/08`, plus a large batch of previously-unmerged foundation infrastructure
(`internal/cli`, `internal/config`, `internal/lock`, `internal/paths`, `internal/gitx`, `internal/storage/sqlite`
db/migrate engine, `internal/telemetry/claude`, CI/governance docs). Whole-repo `go build ./...` and
`go test ./...` both passed cleanly immediately after the merge, before any new code was written.
Re-read CONSTITUTION.md, CONTRACT_FREEZE.md's "Predictor pipeline ports (ADR-041)" section,
`docs/adr/0041-predictor-forecast-layer.md`, `agents/predictor.md`, `internal/predictor/token/forecaster.go`,
`internal/predictor/scope/estimator.go`, `internal/app/ports.go`'s ADR-041 section, `internal/domain/forecast.go`,
and `Preflight_ADD.md` §15.3/§15.9 before starting, per instruction.

```yaml
node: predictor-01
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0040_feature_vectors.sql
  - internal/storage/sqlite/migrations/0041_predictions.sql
  - internal/storage/sqlite/migrations/0042_runway_forecasts.sql
  - internal/storage/sqlite/migrations/0043_policy_decisions.sql
  - internal/storage/sqlite/migrations/0044_authorizations.sql
  - internal/storage/sqlite/migrate_predictor_test.go
validation:
  - "gofmt -l internal/storage/sqlite  # clean"
  - "go vet ./internal/storage/sqlite/...  # ok"
  - "go test ./internal/storage/sqlite/... -run Migration0040 -v  # PASS (4 top-level tests: predictor range loads/applies via AllMigrations, per-table column-shape spot-check via PRAGMA table_info across all 5 tables, policy_decisions FK relationships both within predictor's own range and into foundation's provider_sessions, authorizations UNIQUE(turn_id))"
  - "go build ./...  # ok, whole module"
  - "golangci-lint run ./...  # 0 issues repo-wide"
commit: <see final report>
next_action: predictor-05c (same wave, sequential)
assumptions:
  - "Table set per Preflight_ADD.md §12.2's canonical schema, scoped to predictor's migration range (0040-0049 per CONTRACT_FREEZE.md): feature_vectors, predictions, runway_forecasts, policy_decisions, authorizations. No literal `evaluations` table exists in the ADD's §12.2 schema (domain.EvaluationID/app.Evaluation are backed by the `predictions` table plus policy_decisions, not a separate table) — the task instruction's 'evaluations/predictions/authorizations' phrasing is read as referring to this whole persistence surface (agents/predictor.md deliverable #11 'Evaluation persistence' + #12 'authorization issuance/consumption'), not a literal fifth table name."
  - "internal/storage/sqlite/migrate_test.go is owned by foundation (not one of predictor's exclusive paths). Per Constitution §4.4 ('a role never edits a file it doesn't own; it works around the gap with a documented assumption'), predictor-01's own migration-range validation lives in a new file, migrate_predictor_test.go, in the same shared sqlite_test package — additive only, no existing file touched. This exactly mirrors foundation-06's own established pattern of putting per-range migration tests directly in that package (TestAllMigrations_LoadsCoreSchemaFiles / TestCoreMigrations_*), with test names containing 'Migration0040' so the DAG's literal validation command selects them."
  - "DISCOVERED INTEGRATION HAZARD (real, not hypothetical — flagging for contract-integrator/foundation): feature_vectors/predictions/runway_forecasts/authorizations conceptually FK into `turns` (claude-provider's 0010-0019 range) and authorizations also into `repository_checkpoints` (checkpoint Part B's 0030-0039 range), per the ADD's §12.2 schema. Neither table exists as a migration anywhere yet (checked day1/claude-provider and day1/checkpoint branches directly — no migration files present on either). SQLite's `PRAGMA foreign_keys = ON` (already set by db.go) does NOT merely skip-enforce a REFERENCES clause pointing at a nonexistent table until it's populated — it makes ANY cascading DELETE reachable through that table fail outright with 'no such table: main.turns', even for completely unrelated rows, because SQLite resolves every FK-referenced table in the schema's cascade graph at DML prepare time. This was caught empirically: adding these tables with real REFERENCES clauses broke 3 of foundation-06's own already-passing, already-merged tests (TestCoreMigrations_ForeignKeys_RepositoryToWorktree, TestCoreMigrations_ForeignKeys_TaskSessionSetNull, and indirectly the reopen tests) on a plain `DELETE FROM repositories` that has nothing to do with predictor's tables. Fix applied: turn_id (on feature_vectors/predictions/runway_forecasts/authorizations) and repository_checkpoint_id (on authorizations) are plain unconstrained TEXT columns with NO SQL-level FK — exactly the precedent 0004_tasks.sql already established for its own forward reference to progress_nodes ('SQLite has no deferred cross-table FK addition without recreating the table'). Documented in each migration file's own header. Real FKs are kept wherever the target table already exists on this branch: runway_forecasts.session_id -> provider_sessions (0003), runway_forecasts.task_id -> tasks (0004), and the two same-range FKs in policy_decisions -> predictions/runway_forecasts (0041/0042, this range)."
  - "Consequence for whole-repo `go test ./...`: after this fix, TestCoreMigrations_ForeignKeys_RepositoryToWorktree and TestCoreMigrations_ForeignKeys_TaskSessionSetNull (both in foundation's migrate_test.go) now PASS again. Two foundation tests still fail on this branch — TestAllMigrations_LoadsCoreSchemaFiles and TestCoreMigrations_FromEmptyDatabase/TestCoreMigrations_ReopenFromFile_AppliesOnce — but only because they hardcode `len(migrations) != 4` / `CurrentVersion != 4` as strict-equality assertions that assume foundation's 4 migrations are the *only* ones that will ever exist in the embedded directory. This is a pre-existing test-design limitation in a file predictor does not own and cannot fix without violating its path boundary; it will break the same way regardless of merge order the moment ANY other role's migration range lands (claude-provider, checkpoint, or predictor — whichever merges first). Flagged here for contract-integrator/foundation to relax those two assertions (e.g. `>=` instead of `==`) at the next integration point, not fixed unilaterally by this role."
  - "authorizations' UNIQUE(turn_id) constraint and predictor-10's future 'consumed_at IS NULL' service-layer check together implement CONTRACT_FREEZE.md's 'Authorization — one-time; consumption is exactly-once' contract; UNIQUE(turn_id) alone only guarantees exactly-once *issuance* per turn, not exactly-once *consumption* (that half is a service-layer transaction concern, deliberately out of scope for a migration-only node)."
blockers: []
```

```yaml
node: predictor-05c
status: completed
artifacts:
  - internal/predictor/quota/doc.go
  - internal/predictor/quota/coldstart.go
  - internal/predictor/quota/forecaster.go
  - internal/predictor/quota/forecaster_test.go
validation:
  - "gofmt -l internal/predictor/quota  # clean"
  - "go vet ./internal/predictor/quota/...  # ok"
  - "go test ./internal/predictor/... -run QuotaForecast -v  # PASS (19 tests: never-calibrated-this-wave across 4 input shapes, unknown-when-no-observation for both quota and context, nil-UsedPercent treated as unknown not zero, forward projection from current usage for both quota and context, context UsedTokens/WindowTokens fallback + zero-WindowTokens guard, near-limit reason codes for quota/context/Reached-flag, multi-window max-combination, reset-soon delta suppression vs reset-far-away delta application, TokenForecast-scaled delta (small vs large forecast, zero-value-behaves-as-absent), determinism, degenerate-input no-panic sweep incl. negative/huge/MaxInt64 values)"
  - "go build ./...  # ok, whole module"
  - "go test ./internal/predictor/... ./internal/features/... -race  # PASS, full packages, no regressions"
  - "golangci-lint run ./...  # 0 issues repo-wide"
commit: <see final report>
next_action: none — predictor-01/predictor-05c are the two assigned Wave 4 nodes; predictor-07 (Risk Combiner) is explicitly out of scope this wave (blocked on predictor-05c completing review, per instruction), not started
assumptions:
  - "New sibling package internal/predictor/quota (alongside internal/predictor/scope, internal/predictor/token, internal/predictor/runway), matching Preflight_Predictor_Design_Supplement.md's own naming: 'RuleQuotaForecaster — Version 1 — deterministic delta model, §15.3'. Unlike scope/token, this stage needs no package-local FeatureSource abstraction: app.ForecastQuotaRequest already carries everything Stage 3 needs directly (Quota []domain.QuotaObservation, Context domain.ContextObservation, TokenForecast domain.TokenForecast) — no session/repository/progress feature-lookup gap to bridge, so RuleQuotaForecaster is stateless (mirrors internal/predictor/runway.Scorer's own stateless design more than scope/token's FeatureSource-backed one)."
  - "Cold-start-only scope, exactly as CONTRACT_FREEZE.md's ADR-041 section explicitly anticipates and licenses for this node: 'QuotaForecaster implementations MAY produce a deterministic current-observation-plus-default-delta estimate ... before durable historical telemetry exists. This is not a stub to be later thrown away.' No durable telemetry store exists on this branch (claude-provider-05's persistence layer is a sibling Wave 4 node on a different, not-yet-merged branch), so ADD §15.3 step 5's empirical-P50/P90-from-samples branch is unreachable by construction — every result is Calibrated=false, Confidence=ConfidenceLow, ReasonPredictionColdStart always present."
  - "ADD §15.3/§15.9 do not name exact default-delta values (unlike §14.6's token-multiplier table). coldstart.go documents this package's own conservative bootstrap constants: defaultQuotaDeltaP50/P90 = 2.0/6.0 percentage points per turn; defaultContextGrowthP50/P90Fraction = 0.03/0.10 (3%/10% of context window capacity) — explicitly flagged as bootstrap starting points, not measured values, expected to be replaced by StatisticalQuotaForecaster's empirical quantiles (Version 2) once durable per-window delta samples exist."
  - "TokenForecast fallback (per app.ForecastQuotaRequest's own doc comment: 'MAY use TokenForecast as a fallback input when the provider does not expose quota percentage directly; MUST NOT require it'): tokenAdjustedDelta scales the default P90 delta/growth by TokensP90 relative to a nominal 6000-token 'typical turn' baseline (nominalTurnTokens, deliberately matching internal/predictor/token.baseTurnTokens's value but declared independently rather than imported across packages, mirroring internal/predictor/token/coldstart.go's own established rationale for keeping cold-start tables independent between packages that measure different quantities). Bounded to [0.5x, 3.0x] (tokenScaledDeltaFloor/Ceiling) so one extreme TokenForecast cannot blow up or erase the conservative default — same capping discipline as internal/predictor/token's per-multiplier caps, reused here since §15.3 gives no equivalent explicit cap. A zero-value TokenForecast (TokensP90<=0, i.e. no forecast supplied) leaves the default delta unscaled, verified by TestQuotaForecastZeroTokenForecastUsesUnscaledDefault."
  - "Multi-window combination (ForecastQuotaRequest.Quota is a slice, domain.QuotaForecast.ProjectedQuotaUsedP90 is a single scalar): reuses the same conservative max-across-windows rule already established by internal/predictor/runway.CombineWindows for the identical ADD §15.5 'v1 預設取 max，避免錯誤獨立假設' reasoning, rather than an independence-assuming combination formula — the worst (highest-projected) window drives the single returned value."
  - "Reset-awareness (ADD §15.8: 'resets_at 是 schedule hint'): projectOneQuotaWindow suppresses the delta (projection stays at current usage, not compounded forward) when ResetsAt falls within a fixed turnHorizon look-ahead (10 minutes, matching internal/predictor/runway.DefaultHorizon — no turn-duration forecast is wired up this wave to know precisely how long the upcoming turn will take, so a fixed conservative horizon is used as a documented assumption, same pattern runway already established for its own default horizon)."
  - "near-limit threshold (ReasonQuotaNearLimit/ReasonContextNearLimit): no exact percentage is named in ADD §15.3/§15.9 (unlike §15.7's explicit runway thresholds). 90% is used, chosen to mirror the P90 framing already used throughout this pipeline stage — a documented, conservative default, not a spec-derived value. QuotaObservation.Reached=true always triggers ReasonQuotaNearLimit regardless of UsedPercent, since Reached is the provider's own authoritative signal and must not be overridden by a percentage heuristic."
  - "ContextObservation fallback: when UsedPercent is nil but UsedTokens/WindowTokens are both present and WindowTokens>0, current usage is derived as UsedTokens/WindowTokens*100 (an equally valid measurement per usage.go's own field set) rather than treated as unknown; WindowTokens<=0 is explicitly guarded (TestContextForecastZeroWindowTokensIsUnknown) to avoid division by zero, falling back to unknown (nil), never a fabricated value."
blockers: []
```

## Wave 4 summary

Both assigned nodes (`predictor-01`, `predictor-05c`) are `completed` with durable artifacts and passing
validation commands, on branch `day1/predictor`. `main` was merged first per instruction (clean
fast-forward, `22fde28` -> `ca7062f`), confirmed building/testing cleanly before any new code was
written. `predictor-01` surfaced and fixed a real cross-role SQLite foreign-key hazard (documented above
in its own `assumptions` block) that would otherwise have silently broken foundation-06's already-merged
cascade-delete tests at the next integration point, regardless of merge order. `predictor-05c` is a new,
self-contained `internal/predictor/quota` package requiring no FeatureSource abstraction, unlike its
`scope`/`token` siblings. `predictor-07` (Risk Combiner) was explicitly out of scope this wave (blocked
on `predictor-05c` completing and being reviewed) and was not started, stubbed, or scaffolded. No other
role's paths were touched. `golangci-lint run ./...` reports 0 issues repository-wide as of this wave's
final commit.

---

## Wave 5 (predictor-07)

Branch: `day1/predictor`, continuing from `1fa92cf` (Wave 4, `predictor-01`/`predictor-05c`). Per explicit
instruction, `origin/main` was merged first (`git fetch origin && git merge origin/main`) — a clean
fast-forward from `1fa92cf` to `5470e4d`, bringing in Wave 4's integrated state (foundation-07,
claude-provider-05, checkpoint-a01/b01, `predictor-01`/`predictor-05c` already-merged copies,
runtime-a01/b02, `internal/app/wiring`, `internal/telemetry/claude`, new SQLite migrations 0010-0052,
`internal/testutil/fakes`). Whole-repo `go build ./...` and `go test ./...` both passed cleanly
immediately after the merge, before any new code was written. Re-read CONSTITUTION.md,
`agents/predictor.md`, `docs/implementation/day1/EXECUTION_DAG.md` (`predictor-07`'s corrected entry),
`docs/implementation/day1/CONTRACT_FREEZE.md`'s "Predictor pipeline ports (ADR-041)" section,
`docs/adr/0041-predictor-forecast-layer.md` (including its "Terminology note" on `execution_risk` vs.
`completion_risk`), `internal/app/ports.go`'s `RiskCombiner`/`CombineRiskRequest`/`CombineRiskResult`
section, `internal/domain/forecast.go`, and `Preflight_ADD.md` §16.1-16.2/§16.3/§16.4 before starting, per
instruction. Assigned exactly `predictor-07` (Risk Combiner) this wave.

```yaml
node: predictor-07
status: completed
artifacts:
  - internal/predictor/risk/doc.go
  - internal/predictor/risk/coldstart.go
  - internal/predictor/risk/combiner.go
  - internal/predictor/risk/combiner_test.go
validation:
  - "gofmt -l internal/predictor  # clean"
  - "go build ./internal/predictor/...  # ok"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -run RiskComponents -v  # PASS (9 top-level tests, 16 subtests: quota/context sigmoid formula incl. midpoint=0.5 exact case, nil-projection-is-unknown-not-zero for both quota/context, completion risk formula incl. base/maxed-out-clamped-to-1.0/reason-code-derived-term-delta/cold-start-propagation, blast-radius risk formula incl. base/security+migration/public-API-change-delta/monotonicity-in-files-changed, overall=max() with calibrated-only-if-all-inputs-calibrated and reason-code union, 500-trial NaN/Inf/out-of-range property sweep across extreme+nil inputs, 20-trial determinism check, cold-start-never-fabricates-calibration across all 5 components, reason-code golden test, frozen-interface satisfaction check)"
  - "go test ./internal/predictor/... -race  # PASS, all packages"
  - "go build ./...  # ok, whole module"
  - "go test ./...  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide"
commit: <see final report>
next_action: none — predictor-07 is the sole assigned Wave 5 node; predictor-08 (Policy) explicitly out of scope, not started, stubbed, or scaffolded
assumptions:
  - "Package path internal/predictor/risk (sibling to internal/predictor/{scope,token,quota,runway}), matching this role's own established one-package-per-pipeline-stage convention and Preflight_Predictor_Design_Supplement.md's naming pattern ('RuleXForecaster — Version N'). Neither internal/app/ports.go nor CONTRACT_FREEZE.md names an implementation package path for RiskCombiner (CONTRACT_FREEZE.md's 'What Bootstrap did NOT freeze' section explicitly defers 'predictor internals' to the owning role), so this follows the same judgment call already made for scope/token/quota."
  - "RuleRiskCombiner implements ADD §16.2's 'Initial explainable formula' verbatim, including its exact coefficients (e.g. completion_risk's 0.10 base + 0.04*files_changed_p90 + ... ; blast_radius_risk's 0.05 base + 0.03*files_changed_p90 + ...) — unlike predictor-05c/predictor-05b, this formula's constants ARE fully named in the ADD, so no bootstrap-constant derivation was needed here (coldstart.go just names the ADD's own coefficients for line-by-line auditability against the ADD text, it does not invent new defaults)."
  - "Terminology: uses completion_risk / CompletionRisk throughout, never execution_risk, per ADR-041's explicit 'Terminology note' (Preflight_Predictor_Design_Supplement.md's execution_risk = P(task_requires_multiple_turns) and ADD §16.1's completion_risk name the same concept; ADR-041 keeps the ADD's name as frozen, 'renaming it would fork one concept under two names, which Constitution §1 exists to prevent') and CombineRiskResult.CompletionRisk's own frozen field name in internal/app/ports.go. Documented explicitly in risk/doc.go's package comment so this is auditable without re-reading the ADR."
  - "DISCOVERED GAP (real, not hypothetical — flagging for contract-integrator/predictor-08): ADD §16.2's completion_risk/blast_radius_risk formulas name four terms (open_ended_scope, recent_retry_rate, recent_test_failure_rate, unresolved_progress_blockers, public_api_change) that have NO direct field on the frozen domain.ScopeEstimate struct (internal/domain/forecast.go) — unlike files_changed_p90/lines_changed_p90/integration_tests/migration/cross_layer/security_sensitive/cross_project, which map onto ScopeEstimate fields one-to-one. The underlying signals exist one layer down in internal/features (PromptFeatures.OpenEndedIndicator, SessionFeatures.RetryRate/TestFailureRate, ProgressFeatures.UnresolvedBlockers), but the frozen app.CombineRiskRequest (ADR-041) carries only Scope/TokenForecast/QuotaForecast, not those feature DTOs. Rather than widening CombineRiskRequest (not this node's path to edit — internal/app/ports.go is contract-integrator-owned) or silently treating these terms as always-0, this implementation reads them from scope.ReasonCodes as boolean presence indicators (domain.ReasonOpenEndedScope, domain.ReasonHighRecentRetryRate, domain.ReasonHighRecentTestFailureRate, domain.ReasonProgressBlocked, domain.ReasonPublicAPIChange) — the one channel through which internal/predictor/scope.RuleScopeEstimator already surfaces some of these signals today. This is a documented, boolean (not continuous-rate) approximation: a present reason code contributes its full formula coefficient, never a partial one scaled by the actual rate, since CombineRiskRequest carries no continuous value for these terms. Fully documented in combiner.go's completionRiskTermsFromReasonCodes doc comment. Recommend predictor-08 (Policy) and any future ADR revisiting this pipeline check whether CombineRiskRequest should gain these fields directly, rather than each downstream consumer re-deriving the same reason-code bridge independently."
  - "quota_risk/context_risk's nil-projection ('unknown, not zero' per ADD §16.3) fallback score is sigmoid(0)=0.5 — the sigmoid's own midpoint — chosen as the most defensible score-shaped placeholder for a genuinely missing input (paired with an explicit QUOTA_UNKNOWN/CONTEXT_UNKNOWN reason code and the component's own Confidence/Calibrated, honestly propagated from the unknown upstream QuotaForecast, never manufactured as high-confidence). ADD §16.2 does not define sigmoid's behavior for a missing input, so this is a documented implementation choice, not a spec-derived value."
  - "QuotaForecast.ReasonCodes is a single shared field covering both the quota and context sub-signals (no per-field reason-code split exists in the frozen struct — matches how predictor-05c's RuleQuotaForecaster itself appends quotaReasons/contextReasons into one combined slice). Consequently quotaRiskComponent and contextRiskComponent both echo the full qf.ReasonCodes, not a filtered subset — verified explicitly by TestRiskComponentsReasonCodeGolden, which pins this exact cross-echo behavior as the expected (not accidental) shape."
  - "overall_risk (ADD §16.2: overall = max(quota, context, completion, blast_radius)) additionally computes Calibrated as the logical AND of all four components' own Calibrated (an overall claim can never be more certain than its least-certain input) and Confidence as the lowest (most conservative) of the four via a documented confidenceRank ordering (unavailable < low < medium < high < exact) — ADD §16.2 names only the Score formula, not how Calibrated/Confidence/ReasonCodes should combine, so this is this package's own documented, conservative extension consistent with Constitution §7 rule 7."
  - "clamp01's NaN handling: a NaN score is clamped to 1.0 (the most conservative/highest-risk value), not 0.0 or left as NaN, on the reasoning that a score computation producing NaN reflects an upstream data problem and this package's overall discipline (matching quota_risk's own 'unknown is not zero' bias) is to favor disclosing elevated risk over silently understating it. Exercised by TestRiskComponentsNeverNaNOrInf's 500-trial property sweep, which includes math.MaxFloat64/-MaxFloat64/math.MaxInt64/math.MinInt64 and nil-pointer inputs in random combination."
blockers: []
```

## Wave 5 summary

The single assigned node (`predictor-07`) is `completed` with durable artifacts and passing validation
commands, on branch `day1/predictor`. `origin/main` was merged first per instruction (clean fast-forward,
`1fa92cf` -> `5470e4d`), confirmed building/testing cleanly before any new code was written.
`internal/predictor/risk` is a new, self-contained, stateless package (no FeatureSource abstraction
needed, matching `predictor-05c`'s precedent rather than `predictor-05`/`predictor-05b`'s) implementing
ADD §16.2's risk-combination formula verbatim against the ADR-041-frozen `app.RiskCombiner` interface. A
real gap was discovered and documented (five ADD §16.2 formula terms with no direct `ScopeEstimate`
field) and bridged via `scope.ReasonCodes`, not silently ignored or worked around by editing a frozen
contract file. Terminology is `completion_risk` throughout, matching ADR-041's explicit resolution of the
`execution_risk`/`completion_risk` naming fork. No other role's paths were touched; `internal/policy/**`
and `internal/evaluation/**` remain untouched. `golangci-lint run ./...` reports 0 issues repository-wide
as of this wave's final commit.

---

## Wave 6 (predictor-08)

Branch: `day1/predictor`, continuing from `216c92b` (Wave 5, `predictor-07`). Per explicit instruction,
`origin/main` was merged first (`git fetch origin && git merge origin/main`) — a clean fast-forward from
`216c92b` to `abce1d0`, bringing in Wave 5's integrated state (`claude-provider-07`, `checkpoint-a02/a03/b04`,
this role's own already-merged `predictor-07`, `runtime-a02/a06/b03/b04/b05/b08`, plus new packages
`internal/artifacts`, `internal/orchestrator`, `internal/pause`, `internal/progress`, `internal/repocheckpoint`,
`internal/scheduler`, `internal/gitx`, new CLI checkpoint/diagnostics commands, and expanded
`internal/app/wiring`). Whole-repo `go build ./...` and `go test ./...` both passed cleanly immediately
after the merge, before any new code was written. Re-read `CONSTITUTION.md`, `agents/predictor.md`,
`docs/implementation/day1/EXECUTION_DAG.md` (`predictor-08`'s corrected entry — deps `predictor-07`,
`predictor-06`, per ADR-041's "Policy consumes Runway directly" correction), `docs/implementation/day1/
CONTRACT_FREEZE.md`'s "Predictor pipeline ports (ADR-041)" section, and `docs/adr/0041-predictor-forecast-
layer.md` before starting, per instruction. Assigned exactly `predictor-08` (Policy) this wave.

```yaml
node: predictor-08
status: completed
artifacts:
  - internal/policy/doc.go
  - internal/policy/coldstart.go
  - internal/policy/decide.go
  - internal/policy/policy_test.go
  - internal/policy/coldstart_test.go
validation:
  - "gofmt -l internal/policy  # clean"
  - "go build ./internal/policy/...  # ok"
  - "go vet ./internal/policy/...  # ok"
  - "go test ./internal/policy/... -run ColdStart -v  # PASS (9 top-level tests: literal-contract-shape match, all 4 reachable risk bands with uncalibrated inputs, emergency-PAUSE-is-not-a-probability across 3 emergency trigger conditions, mandatory-checkpoint-boundary-is-not-a-probability, explicit-deny/integrity-failure never-probability, calibrated-runway-may-legitimately-report-probability control case, direct-construction check across all 8 frozen PolicyAction values, and a full-grid randomized sweep over risk score x runway score x all 4 boolean gates x prior-confirmed asserting Probability stays nil throughout)"
  - "go test ./internal/policy/... -race  # PASS"
  - "go test ./internal/policy/... -bench=. -benchmem -run '^$'  # BenchmarkDecide: 52.83 ns/op, 16 B/op, 1 allocs/op — well under ADD §29.11's <1ms policy target"
  - "go build ./...  # ok, whole module"
  - "go test ./... -race  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide"
commit: <see final report>
next_action: none — predictor-08 is the sole assigned Wave 6 node; predictor-09 (Evaluation persistence) explicitly out of scope, not started, stubbed, or scaffolded
assumptions:
  - "internal/app/ports.go has no dedicated frozen `Policy` interface — the closest frozen policy-decision port is `EvaluationService.Decide(ctx, DecideRequest{EvaluationID}) (DecisionResult, error)`, which by itself carries no risk/runway payload. Rather than guessing a wider shape into ports.go (not this role's path — Constitution §4.3) or blocking, this node builds `policy.Decider`/`policy.Decision`/`policy.DecideRequest` as this package's own documented bridge type: `Decision.Action` is always one of the eight frozen `app.PolicyAction` values, and `Decision` itself carries the richer fields (RiskScore, Probability, Confidence, reason codes) a future evaluation-persistence node (predictor-09) can flatten into `app.Evaluation`/`app.DecisionResult` without this package inventing a competing frozen contract. Matches CONTRACT_FREEZE.md's own anticipation: 'owning roles MAY find they need additional fields; requests for additions go through the role's progress artifact ... not silent edits to internal/app/ports.go.'"
  - "agents/predictor.md's deliverable-#10 action names (ALLOW, WARN, CHECKPOINT, SPLIT, PAUSE, ABORT) are mapped onto the frozen `app.PolicyAction` enum as: ALLOW->PolicyRun, WARN->PolicyWarn, CHECKPOINT->PolicyCheckpointAndRun, SPLIT->PolicySplit (unused this wave — no SPLIT trigger condition is named anywhere in agents/predictor.md's initial policy suggestion or ADD §17), PAUSE->PolicyPause, ABORT->PolicyBlock (matched to ADD §17.3 priority 1's 'explicit deny/security' and ADD §21.9's 'block' JSON decision literal — the frozen enum has no literal ABORT, and PolicyBlock is the closest semantic match for a hard-stop deny). `PolicyRequireConfirmation` and `PolicyPauseAndAutoResume` (two frozen actions this node's deliverable list does not separately name) are used for ADD §17.3's 'high risk -> require confirmation' priority tier and are available for a later role to layer auto-resume authorization onto a plain PAUSE, respectively — not invented, both already frozen in ports.go."
  - "Decide's priority order implements ADD §17.3 verbatim (explicit deny/security > integrity failure > active graceful-pause trigger > mandatory state checkpoint boundary > risk bands), evaluating gates in that fixed order and returning on first match. ExplicitDeny/IntegrityFailure/MandatoryCheckpointBoundary are caller-supplied booleans, not detected by this package — detecting a security deny, a checkpoint checksum mismatch, or a Progress Tree node kind/transition all require capabilities this role's Boundary explicitly excludes ('No provider JSON parsing, Git commands, checkpoint creation, or process interruption')."
  - "ADD §16.5's band table (<0.45 low/ALLOW, 0.45-0.65 medium/WARN, 0.65-0.85 high/REQUIRE_CONFIRMATION or CHECKPOINT, >=0.85 critical/CHECKPOINT) is implemented with '>=' at each lower boundary (score exactly at a threshold enters the higher band), verified by exact-boundary table tests at 0.45/0.65/0.85. Inside the 'high' band specifically, agents/predictor.md's own refinement ('predicted P90 exceeds available headroom or high blast radius: CHECKPOINT') is implemented as: prefer CHECKPOINT_AND_RUN over REQUIRE_CONFIRMATION when BlastRadiusRisk.Score alone is also >=0.85 — a documented, package-local threshold (blastRadiusHighThreshold) since the ADD names no blast-radius-specific checkpoint threshold distinct from the shared band table."
  - "ADD §17.4's calibrated auto-pause rule (hit_probability >= 0.80) and §17.6's debounce (two consecutive qualifying observations, not one) are both implemented, but this package is stateless per call (matching runway.Scorer's own precedent) — the caller supplies DecideRequest.PriorRunwayHitConfirmed as the one bit of cross-call history needed for debounce. §17.6's other legs (min 5s apart, quota sample age <=30s, risk-must-fall-below-0.70-before-re-arming) are NOT separately re-implemented in this package: none of them require anything beyond what domain.RunwayForecast and PriorRunwayHitConfirmed already carry into a single Decide call, and re-deriving interval/staleness bookkeeping this package cannot itself observe (it never sees raw timestamps across calls) would mean either silently guessing or duplicating state the caller must own anyway — documented explicitly in coldstart.go rather than silently skipped."
  - "ADD §17.6's emergency trigger (provider reports limit reached OR used>=98% OR estimated-time-to-limit-P50<=60s) is implemented as isRunwayEmergency, checking domain.RunwayForecast.CurrentUsedPercent and EstimatedTimeToLimitP50Seconds directly, plus treating RiskScore==1.0 with Confidence==high as the already-folded-in 'provider reports limit reached' signal (RunwayForecast has no separate boolean of its own for that condition — runway.Scorer's own Score implementation already collapses QuotaObservation.Reached into exactly that RiskScore/Confidence combination, one layer up, per runway.go's own Score doc comment). This emergency path always returns Probability=nil and PolicyReasonCodes containing the literal string 'emergency_threshold' (ReasonEmergencyThreshold), never a probability — the exact literal from agents/predictor.md's initial policy suggestion."
  - "Decision.ReasonCodes carries only frozen domain.ReasonCode enum values (from CombineRiskResult component ReasonCodes); Decision.PolicyReasonCodes is a separate, package-local plain-string slice for this pipeline stage's own vocabulary (mirroring internal/predictor/runway's precedent of a plain-string reason-code namespace distinct from domain.ReasonCode) plus a home for runway-sourced reason strings, since domain.RunwayForecast.ReasonCodes is frozen as []string, not []domain.ReasonCode (runway/runway.go's own doc comment explains why: RunwayForecast predates ADR-041's typed ReasonCode introduction). This two-field split avoids fabricating new domain.ReasonCode enum values from either runway's plain strings or this package's own trigger-condition names, keeping the frozen enum closed per Constitution §1."
  - "clamp01Risk mirrors internal/predictor/risk.clamp01 exactly (NaN clamps to 1.0, the most conservative/highest-risk value, never a placid low score) and is applied to every RiskScore this package ever returns, regardless of which upstream input (CombineRiskResult.OverallRisk.Score, RunwayForecast.RiskScore, or BlastRadiusRisk.Score for the high-band blast-radius check) it was read from — added after an initial implementation's fail-open/fail-closed test caught a live NaN/Inf-propagation gap (RiskScore was passed through unclamped in three of the four Decide paths); the fix and the test that caught it are both in this wave's commit, not deferred."
  - "The dedicated ColdStart suite (coldstart_test.go) is written to prove the narrower, correct claim explicitly: 'uncalibrated never becomes a probability,' not the different (and wrong) claim 'probability is always nil' — TestColdStartArmedButNotYetConfirmedRunwayIsCalibratedAndMayReportProbability is a deliberate control case proving the suite would fail if Decide over-corrected into never populating Probability even when a genuinely calibrated runway input justifies it."
blockers: []
```

## Wave 6 summary

The single assigned node (`predictor-08`) is `completed` with durable artifacts and passing validation
commands, on branch `day1/predictor`. `origin/main` was merged first per instruction (clean fast-forward,
`216c92b` -> `abce1d0`), confirmed building/testing cleanly before any new code was written.
`internal/policy` is a new, stateless package implementing ADD §17's Policy Engine (priority order §17.3,
risk bands §16.5, debounce/emergency §17.6, calibrated-auto-pause §17.4) against the two direct
ADR-041-frozen pipeline inputs (`app.CombineRiskResult` from `predictor-07`'s `RiskCombiner`,
`domain.RunwayForecast` from `predictor-06`'s Runway Predictor, consumed directly per ADR-041's "Policy
consumes Runway directly" correction — never through `RiskCombiner`). `internal/app/ports.go` has no
dedicated frozen `Policy` interface yet; this node built `policy.Decider`/`Decision`/`DecideRequest` as its
own documented bridge type expressed entirely in terms of the frozen `app.PolicyAction` enum, per
CONTRACT_FREEZE.md's explicit anticipation that owning roles may need additional fields beyond the
Bootstrap-era minimal DTOs. The single load-bearing invariant for this node (Constitution §6/§7: an
uncalibrated score must never be labeled a probability) is enforced structurally — exactly two call sites
in the whole package ever assign `Decision.Probability` a non-nil value, both gated by an explicit
`rf.Calibrated &&` check immediately before the assignment — and is proven by a dedicated
`coldstart_test.go` suite (9 top-level tests, `-run ColdStart` selects the whole file) covering every
reachable risk band, the emergency-PAUSE path, the mandatory-checkpoint-boundary path, the explicit-
deny/integrity-failure paths, a full-grid randomized sweep, and a deliberate control case proving
calibrated runway input still legitimately produces a probability (so the invariant proven is "uncalibrated
never becomes a probability," not the stronger and wrong "probability is always nil"). A real bug (RiskScore
passed through without NaN/Inf clamping in three of four Decide paths) was caught by this wave's own
fail-open/fail-closed test and fixed in the same commit, not deferred. `BenchmarkDecide` measured 52.83
ns/op, 16 B/op, 1 allocs/op — far under ADD §29.11's <1ms policy target. No other role's paths were
touched; `internal/evaluation/**` remains untouched. `golangci-lint run ./...` reports 0 issues
repository-wide as of this wave's final commit.

## Wave 7 (predictor-09)

Branch: `day1/predictor`, continuing from `21c7dfd` (Wave 6, `predictor-08`). Per explicit instruction,
`origin/main` was merged first (`git fetch origin && git merge origin/main`) — a clean fast-forward from
`21c7dfd` to `1440f4c`, bringing in Wave 6's integrated state (this role's own already-merged
`predictor-08`, plus `checkpoint`'s Part A progress/state-checkpoint packages (`internal/pause`,
`internal/progress`, `internal/redact`, `internal/scheduler`, `internal/statecheckpoint`) and new
migrations `0023`/`0024`). Whole-repo `go build ./...` and `go test ./...` both passed cleanly immediately
after the merge, before any new code was written. Re-read `CONSTITUTION.md`, `agents/predictor.md`,
`docs/implementation/day1/EXECUTION_DAG.md` (`predictor-09`'s entry — deps `predictor-01`, `predictor-08`),
`docs/implementation/day1/CONTRACT_FREEZE.md`, and `internal/app/ports.go`'s frozen `EvaluationService`
interface and DTO shapes before starting, per instruction. Assigned exactly `predictor-09` (Evaluation
persistence) this wave.

**Path confirmation**: `internal/evaluation` is absent from `CONTRACT_FREEZE.md`'s "Import paths" table
(which lists only `internal/domain`, `internal/app`, `pkg/protocol/v1`, `internal/storage/sqlite`), so per
`agents/predictor.md`'s own instruction ("If `internal/evaluation` is absent from the frozen layout, use
the exact path assigned by the contract-integrator; do not create a competing package") this node checked
this role's own Wave-4-authored migration file comments for confirmation before building. Migration
`0041_predictions.sql`'s comment states: "predictor's persistence layer (predictor-09) is responsible for
keeping it consistent with turns.id"; `0043_policy_decisions.sql` and `0044_authorizations.sql` similarly
name "predictor-09"/"predictor-10" by exact ID against this exact path. No separate contract-integrator
sign-off exists beyond that (Bootstrap explicitly deferred "predictor internals" per CONTRACT_FREEZE.md's
own "What Bootstrap did NOT freeze" section) — this is the correct, non-competing path.

```yaml
node: predictor-09
status: completed
artifacts:
  - internal/evaluation/doc.go
  - internal/evaluation/datasource.go
  - internal/evaluation/store.go
  - internal/evaluation/service.go
  - internal/evaluation/pipeline.go
  - internal/evaluation/helpers_test.go
  - internal/evaluation/service_test.go
  - internal/evaluation/authorization_test.go
validation:
  - "gofmt -l internal/evaluation  # clean"
  - "go build ./internal/evaluation/...  # ok"
  - "go vet ./internal/evaluation/...  # ok"
  - "go test ./internal/evaluation/... -v  # PASS (20 top-level tests: EvaluateTurn persistence/validation/determinism/error-propagation, GetEvaluation lookup/not-found/validation, Decide read-back/not-found/validation, ConsumeAuthorization consume-exactly-once, concurrent-replay-only-one-wins, wrong-session-rejected, wrong-prompt-rejected, stale/expired-rejected, exact-boundary-succeeds, exact-expiry-rejected, unknown-id-not-found, empty-ids-rejected, default-TTL)"
  - "go test ./internal/evaluation/... -race  # PASS, including the dedicated 8-goroutine concurrent-replay test"
  - "go build ./...  # ok, whole module"
  - "go test ./... -race  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide (one errorlint finding in a first draft — a bare `err.(*domain.Error)` type assertion in a test helper — caught and fixed by switching to errors.As before this wave's final commit)"
commit: <see final report>
next_action: none — predictor-09 is the sole assigned Wave 7 node; predictor-10/predictor-11 explicitly out of scope, not started, stubbed, or scaffolded beyond what already had to exist for app.EvaluationService's ConsumeAuthorization method to compile and behave correctly
assumptions:
  - "A real, documented conflict exists between three sources for this wave: the task instructions (explicitly directing a full `ConsumeAuthorization` — exactly-once consumption, expiry, prompt/session binding, replay rejection — to be built as part of predictor-09, with required tests spelled out in detail), `docs/implementation/day1/EXECUTION_DAG.md` (which assigns that exact behavior to a separate downstream node, `predictor-10` — \"One-time authorization\", deps: predictor-09, its own dedicated `-run Authorization` validation gate), and migration `0044_authorizations.sql`'s own comment, written by this role in Wave 4, stating exactly-once consumption is \"enforced by predictor-10's service logic.\" Resolved by building `ConsumeAuthorization` for real, atomically, now — a Go interface cannot be partially implemented, and Constitution §7 rule 11 forbids declaring a task complete without durable evidence, so a stub would have been worse than either alternative — while documenting this conflict explicitly in `doc.go`'s \"ConsumeAuthorization scope note\" and treating predictor-10's own dedicated validation gate as still the authoritative place this behavior is re-verified/extended against real `runtime-a08`/`runtime-b06` integration needs in a later wave, per Constitution §4's \"works around the gap with a documented assumption\" instruction rather than silently picking one instruction and hiding the discrepancy."
  - "`Decide` reads back the `policy_decisions` row `EvaluateTurn` already computed and persisted for the given `EvaluationID`, rather than recomputing via `internal/policy.Decider`. This is directly evidenced, not guessed: `internal/orchestrator/evaluate.go` (already-merged, a sibling role's code) calls `Decide(ctx, app.DecideRequest{EvaluationID: evaluation.ID})` immediately after `EvaluateTurn`, with no risk/runway payload available to pass — `app.DecideRequest` itself carries only an `EvaluationID` field (frozen, `internal/app/ports.go`). Recomputing is not merely undesirable here, it is impossible from the inputs `Decide` actually receives. Documented explicitly in `doc.go`'s \"Decide: read-back, not recompute\" section, per `agents/predictor.md`'s own instruction to be explicit about this choice."
  - "`EvaluateTurn` does not itself compute a new `domain.RunwayForecast` — ADR-041 states the independent Runway Predictor plugs into `GracefulPauseService.Observe`, a different frozen port owned by `runtime`, and explicitly is not a `RiskCombiner` input. This node's `DataSource.RunwayForecast` method surfaces whatever the most recently computed forecast is (if any) purely so `policy.Decider`'s runway-driven PAUSE gate can be evaluated during the same pipeline call; a missing forecast (`hasRunway=false`, e.g. a brand-new session) degrades to the zero `domain.RunwayForecast{}`, which `policy.Decider` already treats as `Calibrated=false`/not-pause-worthy per its own documented fail-open discipline — no new degradation rule was invented here."
  - "`DataSource` is this package's own narrow bridge interface (not a frozen `internal/app` port), following the exact precedent already established by `internal/predictor/scope.FeatureSource` and `internal/predictor/token.FeatureSource`: `app.EvaluateTurnRequest` carries only `SessionID/TurnID/Provider/PromptHash` (frozen, privacy-contract-constrained — no raw prompt text, ever), so every other pipeline input (repository/task resolution, classification, repository/session/progress features, quota/context observations, prior-runway-hit-confirmed debounce bit) is resolved through this package-owned interface, satisfied by a test fake here and, in a later wave, by whatever concrete storage-backed lookup a wiring role provides. Two sibling stages' same-named `FeatureSource.Progress` methods have different signatures from each other (`scope.FeatureSource.Progress` takes `*domain.TaskID`; `token.FeatureSource.Progress` takes `domain.SessionID`) — confirmed by reading both side by side rather than assuming structural consistency; this package's test helper adapts `DataSource` to each stage's own `FeatureSource` explicitly, per method, rather than relying on struct-embedding promotion, which would have silently compiled the wrong method shape."
  - "`IssueAuthorization` (issuance) is an addition to `Service` beyond the frozen `app.EvaluationService` interface (which defines only `ConsumeAuthorization`, no issuance method) — mirrors `internal/policy.Decider`'s own precedent of building a documented package-local bridge type/method beyond the strictly frozen contract, justified the same way: `agents/predictor.md` deliverable #12 names \"one-time authorization issuance/consumption\" as one deliverable, and `ConsumeAuthorization` has nothing to consume without a real issuance path. `EvaluateTurn` itself does not call `IssueAuthorization` automatically this wave — deciding which `PolicyAction`s require an authorization and what `SnapshotFingerprint`/`RepositoryCheckpointID` to bind it to are orchestration-layer decisions outside this package's Boundary (no checkpoint creation, no Git commands); a future wiring layer is expected to call it explicitly."
  - "feature_vectors/predictions/policy_decisions are persisted inside one `app.TxRunner.WithTx` call per `EvaluateTurn`, even though CONTRACT_FREEZE.md's transaction-boundary section only names `ConsumeAuthorization` explicitly for this package — the same partial-write-is-invalid-state reasoning applies by direct analogy (a `predictions` row with no corresponding `policy_decisions` row is a partial evaluation), and `checkpoint`'s own `ProgressTreeService.CompleteNode` (a different role's package) already established this exact pattern for a multi-table completion write, so this is following existing project convention, not inventing a new one."
  - "`ConsumeAuthorization`'s exactly-once guarantee is a single conditional `UPDATE authorizations SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL` inside the same transaction as the binding/expiry checks (store.go's `markAuthorizationConsumed`), matching migration `0044`'s own comment verbatim (\"enforced by predictor's service logic checking consumed_at IS NULL before consuming, inside the same transaction\"). This closes the TOCTOU race an application-level \"read, check, then write\" pattern would have; verified directly by a dedicated concurrent-goroutine test (8 goroutines racing one authorization ID, asserting exactly 1 success) in addition to `go test -race`, since `-race` alone proves absence of a data race, not absence of a logic race."
  - "Wrong-session/wrong-prompt checks in `ConsumeAuthorization` are evaluated before the replay (already-consumed) check, so a caller supplying a binding mismatch gets `ErrCodeUnauthorized` rather than a confusing conflict/replay code — a caller error (wrong ID/binding) and a legitimate replay attempt are different failure classes and should not share an error code. `PromptHash` binding is checked only when `req.PromptHash` is non-empty (mirrors `app.ConsumeAuthorizationRequest`'s own field being effectively optional in the frozen DTO — no validation requires callers to always supply it), while `TurnID` binding is always checked (required, validated non-empty at the method's entry)."
blockers: []
```

```yaml
node: predictor-10
status: completed
artifacts:
  - internal/evaluation/service.go
  - internal/evaluation/authorization_test.go
  - internal/evaluation/doc.go
validation:
  - "gofmt -l internal/evaluation  # clean"
  - "go build ./internal/evaluation/...  # ok"
  - "go vet ./internal/evaluation/...  # ok"
  - "go test ./internal/evaluation/... -run Authorization -v  # PASS (21 tests — see full enumeration below)"
  - "go test ./internal/evaluation/... -race -count=1  # PASS, whole package"
  - "go build ./...  # ok, whole module"
  - "go test ./... -race -count=1  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide (three errcheck findings on unused *domain.Error returns from a new test helper, caught and fixed by prefixing the standalone-statement call sites with `_ =` before this wave's final commit)"
commit: <see final report>
next_action: none — predictor-10 is the sole assigned Wave 8 node; predictor-11 explicitly out of scope, not started
assumptions:
  - "This was an audit/hardening node, not a rebuild: predictor-09's ConsumeAuthorization/IssueAuthorization implementation was re-read in full first, then adversarially tested against scenarios beyond its original 8-goroutine concurrent-replay test, per this wave's explicit instruction to fix only real gaps and not manufacture busywork around already-correct behavior."
  - "One real gap was found and fixed: the prompt-hash binding check (`if req.PromptHash != \"\" && row.PromptHash != req.PromptHash`) skipped the comparison whenever the REQUEST omitted PromptHash, regardless of what the authorization ROW was actually bound to at issuance. A caller who knew only AuthorizationID + TurnID (no prompt hash — e.g. leaked via logs, or a buggy/malicious caller) could consume an authorization that WAS bound to a specific prompt by simply not supplying PromptHash, defeating prompt binding as a control. This was a latent gap, not yet exploited by any wired caller in this tree (no caller invokes ConsumeAuthorization yet — runtime-b06, a later wave, is the first) but a real defect in the function's own defensive contract, and predictor-09's own recorded assumption #530 explicitly named this exact behavior (\"PromptHash binding is checked only when req.PromptHash is non-empty\") as a documented-but-unaudited design choice — precisely the kind of claim this node's adversarial pass exists to verify, not just take on faith. Fixed by keying the skip on the AUTHORIZATION ROW's own PromptHash (`row.PromptHash != \"\" && row.PromptHash != req.PromptHash`): binding is now evaluated against what the authorization was actually issued with, not whether the caller chose to assert it; the one legitimate skip case (an authorization deliberately issued with no prompt hash at all, row.PromptHash == \"\") still works, and is now pinned down by its own dedicated test (TestConsumeAuthorization_AllowsOmittedPromptWhenAuthorizationHasNone) so a future 'fix' can't reintroduce the bypass by conflating the two cases again. Verified as a real, non-hypothetical fix by reverting service.go and confirming the new adversarial test (TestConsumeAuthorization_RejectsOmittedPromptAgainstBoundAuthorization) fails without it, then passes with it restored."
  - "Every other adversarial scenario tested — higher-contention concurrent replay (64 goroutines vs predictor-09's original 8), a 200-iteration tight sequential replay loop against an already-consumed authorization, replay attempts racing the exact expiry boundary (burst of concurrent consumers 1 second before expiry, then a sequential follow-up confirming the winner's consumption sticks and is reported as conflict, not expiry, afterward), nanosecond-adjacent expiry boundaries (1ns before/after ExpiresAt, tightening predictor-09's original 1-second-granularity boundary tests), whitespace-only prompt-hash mismatches, case-sensitivity of both TurnID and PromptHash bindings, and byte-distinct-but-canonically-equivalent unicode normalization forms (precomposed U+00E9 vs decomposed e+U+0301) — passed on the FIRST try against predictor-09's existing logic, unchanged. Confirmed by direct code inspection that this codebase has no COLLATE NOCASE anywhere in internal/storage/sqlite/migrations/0044_authorizations.sql, no case-insensitive Go string comparison (no strings.EqualFold/ToLower/ToUpper touching TurnID/PromptHash), and no TrimSpace/normalization step anywhere in internal/evaluation — all binding comparisons are plain Go `!=` on raw strings, which is exactly the correct, unsurprising security posture (byte-exact, no accidental case- or whitespace-folding) and required no code change, only confirming tests."
  - "The storage-layer exactly-once guarantee (a single conditional `UPDATE authorizations SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`) was confirmed contention-independent by design, not just empirically lucky at 8 goroutines: internal/storage/sqlite/db.go configures WAL journal mode + a 5000ms busy_timeout PRAGMA (applied busy_timeout-before-journal_mode per foundation-07's own documented ordering requirement) and a max-8-open-connections pool, so concurrent writers serialize and retry within the busy_timeout window rather than erroring unpredictably under higher contention — this is why raising the concurrent test from 8 to 64 goroutines was expected to (and did) pass without any code change, and is now a permanent regression guard (TestConsumeAuthorization_HighContentionReplayOnlyOneWins) rather than a one-off manual check."
  - "authorization_test.go was reorganized (not just appended to) into four labeled sections — exactly-once/replay, prompt/session binding, expiry precision, baseline/plumbing — so `go test ./internal/evaluation/... -run Authorization` produces output an external auditor can read in isolation as a coherent security test suite, per this wave's explicit instruction. Every predictor-09 test that already passed was kept with its original assertions and behavior unchanged (only a few had their manual errors.As/domain.Error boilerplate replaced by a shared requireDomainError test helper for readability — a pure refactor, confirmed by running the full suite before and after)."
blockers: []
```

## Wave 7 summary

The single assigned node (`predictor-09`) is `completed` with durable artifacts and passing validation
commands, on branch `day1/predictor`. `origin/main` was merged first per instruction (clean fast-forward,
`21c7dfd` -> `1440f4c`), confirmed building/testing cleanly before any new code was written.
`internal/evaluation` is a new package implementing the frozen `app.EvaluationService` (ADD §9.9) by
wiring together every prior predictor-role package into one real pipeline: Scope Estimator
(`internal/predictor/scope`) → Token Forecaster (`internal/predictor/token`) → Quota Forecaster
(`internal/predictor/quota`) → Risk Combiner (`internal/predictor/risk`) → Policy (`internal/policy`),
persisting the result across this role's own `feature_vectors`/`predictions`/`policy_decisions`/
`authorizations` tables (migrations 0040-0044, Wave 4/`predictor-01`) inside `app.TxRunner.WithTx`
transactions. `var _ app.EvaluationService = (*Service)(nil)` asserts the frozen interface is satisfied
exactly, with no widening. A real, three-way documented conflict was found and resolved rather than
silently picked one way: the task instructions asked for a full `ConsumeAuthorization` under predictor-09,
while the DAG and this role's own earlier migration-file comments assign that behavior to a separate
downstream node, `predictor-10`. `ConsumeAuthorization` was built for real (exactly-once consumption via a
single conditional `UPDATE ... WHERE consumed_at IS NULL`, clock-bound expiry via the injected
`domain.Clock` — never `time.Now()` directly — and prompt/session binding checks), since a frozen Go
interface cannot be partially implemented and Constitution §7 rule 11 forbids a stub-and-claim-complete;
the conflict itself is documented in `doc.go` and this file rather than hidden, and predictor-10's own
dedicated `-run Authorization` validation gate is treated as the still-authoritative place this behavior is
re-verified/extended in a later wave. `Decide` reads back the persisted policy decision rather than
recomputing one, confirmed correct by reading already-merged sibling-role code
(`internal/orchestrator/evaluate.go`) that calls it with only an `EvaluationID` available — not guessed
from the frozen DTO shape alone. 20 top-level tests cover deterministic output for identical inputs,
consume-exactly-once (including an 8-goroutine concurrent-replay race test), wrong-session/wrong-prompt
rejection, and clock-bound expiry at and around the exact boundary. No other role's paths were touched;
`internal/policy/**` and `internal/predictor/**` remain exactly as `predictor-08`/earlier waves left them.
`golangci-lint run ./...` reports 0 issues repository-wide as of this wave's final commit (one `errorlint`
finding in a first draft was caught and fixed before that final state).

## Wave 8 summary

The single assigned node (`predictor-10`) is `completed` with durable artifacts and passing validation
commands, on branch `day1/predictor`. `origin/main` was merged first per instruction (clean fast-forward,
`efd0601` -> `2b7c29c`, bringing in Wave 7's integrated state), confirmed building/testing cleanly before
any new code was written. This was an audit/hardening node against predictor-09's existing
`ConsumeAuthorization`/`IssueAuthorization` implementation, not a rebuild — predictor-09's own code, the
Constitution, `agents/predictor.md`, `CONTRACT_FREEZE.md`, and the EXECUTION_DAG's predictor-10 entry were
all re-read first. The audit found and fixed exactly one real gap: `ConsumeAuthorization`'s prompt-hash
binding check used to skip the comparison whenever the *request* omitted `PromptHash`
(`req.PromptHash != "" && ...`), regardless of what the authorization *row* was actually bound to at
issuance — a caller who knew only an `AuthorizationID` and `TurnID` could bypass prompt binding entirely by
not supplying a prompt hash. Fixed by keying the skip on the authorization row's own `PromptHash` instead
(`row.PromptHash != "" && row.PromptHash != req.PromptHash`), so binding is now evaluated against what the
authorization was actually issued with. This was verified as a real, not hypothetical, defect: reverting
the one-line fix and re-running the new adversarial test
(`TestConsumeAuthorization_RejectsOmittedPromptAgainstBoundAuthorization`) makes it fail with "expected an
error with code unauthorized, got nil"; restoring the fix makes it pass. Every other adversarial scenario
exercised this wave — 64-goroutine high-contention replay (8x predictor-09's original), a 200-iteration
tight sequential replay loop, replay attempts racing the exact expiry boundary, nanosecond-adjacent expiry
boundaries (1ns before/after `ExpiresAt`), whitespace-only prompt-hash mismatches, case-sensitivity of both
binding fields, and byte-distinct unicode-normalization-equivalent forms — passed on the first try against
predictor-09's existing logic, confirming (rather than assuming) that the codebase has no accidental
case-insensitive or normalized comparison anywhere in the authorization path (no `COLLATE NOCASE` in
migration 0044, no `strings.EqualFold`/`ToLower`/`TrimSpace` touching `TurnID`/`PromptHash` anywhere in
`internal/evaluation`). `authorization_test.go` was reorganized into four labeled sections (exactly-once/
replay, prompt/session binding, expiry precision, baseline/plumbing) so its dedicated `-run Authorization`
validation gate produces a test suite readable in isolation by an external auditor, per this wave's
instruction — every predictor-09 test that already passed was kept with unchanged behavior. No other
role's paths were touched. `golangci-lint run ./...` reports 0 issues repository-wide as of this wave's
final commit (three `errcheck` findings against a new shared test helper's unused `*domain.Error` return
value, caught and fixed by prefixing the standalone-statement call sites with `_ =` before the final
commit).

## Wave 9 (predictor-11 — final DAG node)

Branch: `day1/predictor`, continuing from `379b7cf` (Wave 8, `predictor-10`). Per explicit instruction,
`origin/main` was merged first (`git fetch origin && git merge origin/main`) — a clean fast-forward from
`379b7cf` to `36e7ffb`, bringing in Wave 8's integrated state (`checkpoint-a06/a08/b08` plus a tracked-diff
redaction fix, this role's own already-merged `predictor-10`, `runtime-a08`, `qa-04`, and new packages
`internal/gitx/patch.go`, `internal/pause/resumevalidation.go`, `internal/repocheckpoint/patchredact.go` +
`restoredryrun.go`, `internal/statecheckpoint/reconcile.go`). Whole-repo `go build ./...` and
`go test ./...` both passed cleanly immediately after the merge, before any new code was written. Re-read
`CONSTITUTION.md`, `agents/predictor.md` in full (especially "Required tests"), `docs/implementation/day1/
EXECUTION_DAG.md`'s `predictor-11` entry, `docs/implementation/day1/CONTRACT_FREEZE.md`,
`docs/adr/0041-predictor-forecast-layer.md`, and every prior wave's own artifacts
(`internal/features/**`, `internal/predictor/{quantile,scope,token,quota,runway,risk}/**`,
`internal/policy/**`, `internal/evaluation/**`) before starting, per instruction.

This node's job, per the DAG and the task instruction, is not new features but a comprehensive final proof
that the entire Scope Estimator -> Token Forecaster -> Quota Forecaster -> Risk Combiner -> Policy ->
Evaluation persistence/authorization chain works correctly END-TO-END, under realistic combined load, and
is fast enough — specifically covering full-pipeline property tests, deterministic output, reason-code
golden tests, adversarial fail-open/fail-closed at every stage hand-off, the full
`EvaluateTurn -> Decide -> ConsumeAuthorization` flow, and full-pipeline benchmarks — none of which any
individual predictor-0N node's own package-level tests exercise in combination.

```yaml
node: predictor-11
status: completed
artifacts:
  - internal/evaluation/pipeline_e2e_test.go
  - internal/evaluation/helpers_test.go (extended: error-injecting DataSource fields, four errInjecting*
    pipeline-stage wrappers, testStages bundle, newTestServiceWithStages)
validation:
  - "gofmt -l internal/predictor internal/policy internal/evaluation internal/features  # clean"
  - "go build ./...  # ok, whole module"
  - "go vet ./internal/predictor/... ./internal/policy/... ./internal/evaluation/...  # ok"
  - "go test ./internal/predictor/... ./internal/policy/... ./internal/evaluation/... -race -bench=. -benchmem -v  # PASS, 113 top-level PASS lines, 0 FAIL, across 8 packages (predictor, predictor/quota, predictor/risk, predictor/runway, predictor/scope, predictor/token, policy, evaluation)"
  - "go build ./... && go test ./... -race  # PASS, whole module (33 packages), zero regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide (one copyloopvar finding in a first draft — a redundant Go-1.22+ loop-var copy — caught and fixed before the final commit)"
commit: <see final report>
next_action: none — predictor-11 is this role's final assigned DAG node; every node predictor-01 through predictor-11 is now completed; nothing remains in this role's scope
assumptions:
  - "This node's scope (per agents/predictor.md's Boundary and the task instruction) is internal/predictor/**, internal/policy/**, internal/evaluation/** only — internal/features/** was read and re-verified but not modified, since the audit found no gap traceable to that package specifically."
  - "The wide-table-driven full-chain fuzz (TestFullPipeline_WideTableFuzz, internal/evaluation/pipeline_e2e_test.go) combines 11 hand-picked fixtures (cold-start, fully-populated-low-risk, high-scope-high-quota-pressure, quota-Reached-flag-set, negative/zero degenerate inputs, extreme/MaxFloat64/MaxInt64 magnitude inputs, three runway-forecast shapes covering armed/confirmed/emergency debounce states, nil-TaskID-and-empty-slices) with 200 programmatically randomized cases (quota/context UsedPercent swept across roughly [-75,175], token-history sample counts 0-40, runway RiskScore occasionally >1.0) — deliberately including pathological/out-of-spec values (negative percentages, RiskScore>1.0) since a real hand-off bug is exactly as likely to surface from an upstream stage's own degenerate output as from a directly-malicious external input."
  - "Fail-open/fail-closed adversarial testing (Section 4 of pipeline_e2e_test.go) required extending internal/evaluation/helpers_test.go's existing fakeDataSource (predictor-09's original fixture, which only ever exercised a Resolve-error path) with an injectable error field on every one of DataSource's nine methods, plus four new errInjecting* wrapper types (errInjectingScopeEstimator, errInjectingTokenForecaster, errInjectingQuotaForecaster, errInjectingRiskCombiner) implementing the four ADR-041 app interfaces directly, so a test can force exactly one pipeline stage to fail/degrade while the other three run for real against production wiring (realStages + newTestServiceWithStages). This is additive to the existing test-only file, not a change to any production code or to fakeDataSource's existing zero-value (all-fields-unset) default behavior, which every pre-existing predictor-09/predictor-10 test still relies on unchanged."
  - "'Fails toward the safe direction' for a genuine upstream error (as distinct from a legitimate cold-start/degraded-but-present result) is defined and verified as: EvaluateTurn returns a non-nil error AND persists zero rows to the predictions table for that TurnID (checked directly via SQL in TestFullPipeline_UpstreamErrorsFailClosed_NeverSilentAllow, not just by inspecting the returned error) — matching CONTRACT_FREEZE.md's transaction-boundary section naming partial persistence, not merely a wrong return value, as the specific failure mode a WithTx-wrapped operation must prevent."
  - "The DAG's own named scenario ('TokenForecaster returns all-nil') is modeled as a distinct failure SHAPE from a Go error return — errInjectingTokenForecaster.nilResult returns a zero-value domain.TokenForecast with a NIL error, simulating a stage that degrades without erroring. TestFullPipeline_StageErrorsFailClosed/token_forecaster_returns_all_nil asserts this legitimately different outcome: EvaluateTurn still completes (this is a valid cold-start-shaped degradation, not a crash), but the resulting Evaluation.Calibrated must be false — proving the pipeline degrades honestly rather than either crashing or silently reporting false confidence."
  - "The full EvaluateTurn -> Decide -> ConsumeAuthorization flow tests (Section 5) issue an Authorization via the existing IssueAuthorization bridge method (predictor-09's own documented addition beyond the frozen app.EvaluationService interface) bound to the REAL TurnID/PromptHash that came out of a real EvaluateTurn/Decide call pair, re-confirming exactly-once/wrong-binding/clock-bound-expiry hold when wired to a real decision — not just against a synthetic authorization row as predictor-09/predictor-10's own tests already did. No gap was found here: every invariant held identically against real and synthetic bindings."
  - "Full-pipeline benchmarks (BenchmarkEvaluateTurn_FullPipeline, BenchmarkEvaluateTurnThenDecide_FullPipeline) use a warm (non-cold-start) representative fixture (benchDataSource) rather than the perpetually-cold-start default, since ADD §29.11's 'warm evaluate' target is the correct comparison point for a steady-state hot-path benchmark — a perpetually-cold-start benchmark would take fewer branches and understate real steady-state cost. Measured (non -race, benchtime=1000x): BenchmarkEvaluateTurn_FullPipeline ~98 microseconds/op (6.8 KB/op, 136 allocs/op); BenchmarkEvaluateTurnThenDecide_FullPipeline ~189 microseconds/op (12 KB/op, 310 allocs/op). Under this node's own required -race validation command, both scale up roughly an order of magnitude (~1.3ms and ~3.4ms/op respectively) from race-detector instrumentation overhead alone. Compared against Preflight_ADD.md §29.11's stated targets (warm evaluate P50 < 25 ms, P95 < 100 ms): even the -race-inflated numbers carry roughly 20-70x headroom against P50 and 30-75x against P95; the non-race numbers carry roughly 130-250x headroom. predictor-08's own BenchmarkDecide (Policy alone) remains far under the separate 'policy < 1ms' sub-budget unchanged (measured 127.5 ns/op under -race this run, consistent with Wave 6's 52.83 ns/op non-race figure)."
  - "This comprehensive pass found no real cross-stage bug — every DataSource-level and pipeline-stage-level failure mode already failed correctly (error propagates, zero partial persistence; the one legitimate degrade-without-erroring case already produces Calibrated=false by construction). This is a different, equally legitimate outcome from predictor-10's wave (which found and fixed a real prompt-binding bypass) — documented explicitly rather than manufacturing a cosmetic change to have something to report, per the task instruction's own explicit permission for this outcome."
blockers: []
```

## Wave 9 summary

The single assigned node (`predictor-11`) is `completed` with durable artifacts and passing validation
commands, on branch `day1/predictor`. `origin/main` was merged first per instruction (clean fast-forward,
`379b7cf` -> `36e7ffb`), confirmed building/testing cleanly before any new code was written. This is the
predictor role's final assigned DAG node: **every node from `predictor-01` through `predictor-11` is now
`completed`**, closing out this role's full scope (`internal/features/**`, `internal/predictor/**`,
`internal/policy/**`, `internal/evaluation/**`) with nothing remaining.

The comprehensive full-pipeline test suite (`internal/evaluation/pipeline_e2e_test.go`, ~900 lines, six
labeled sections mirroring predictor-10's own authorization_test.go convention) proved, through the real
`evaluation.Service` (not mocks of the pipeline itself):

1. Full-chain property tests (quantile monotonicity, unknown propagation, no-NaN/Inf/divide-by-zero) via
   an 11-fixture hand-picked table plus 200 randomized cases driven through the whole
   Scope->Token->Quota->Risk->Policy->Decide chain — zero panics, zero NaN/Inf escapes, zero invalid
   `Confidence` values.
2. Deterministic output for identical inputs across the full pipeline, re-run against every fixture in the
   same wide table (not just the single cold-start case predictor-09 originally tested).
3. Reason-code golden tests confirming the final `Evaluation.ReasonCodes` forms a coherent explanation
   (cold-start -> `PREDICTION_COLD_START`; quota near limit -> `QUOTA_NEAR_LIMIT`; context near limit ->
   `CONTEXT_NEAR_LIMIT`).
4. Adversarial fail-open/fail-closed at every hand-off: all nine `DataSource` methods and all four
   ADR-041 pipeline-stage interfaces (`ScopeEstimator`/`TokenForecaster`/`QuotaForecaster`/`RiskCombiner`)
   forced to fail or degrade, one at a time, confirming `EvaluateTurn` always fails closed (returns an
   error, persists zero rows) on a genuine upstream error, and that the one legitimate degrade-without-
   erroring case (an all-nil `TokenForecast`) still reports `Calibrated=false`, never a fabricated
   confident result — plus a dedicated test confirming an uncalibrated runway emergency condition still
   forces `PolicyPause` even when every other pipeline input is cold-start/empty.
5. The full `EvaluateTurn -> Decide -> ConsumeAuthorization` flow (exactly-once, wrong-session/wrong-
   prompt rejection, clock-bound expiry) re-confirmed against an `Authorization` bound to a REAL
   `TurnID`/`PromptHash` produced by a real `EvaluateTurn`/`Decide` call pair, not just the synthetic
   bindings predictor-09/predictor-10 already tested.
6. Full-pipeline benchmarks: `BenchmarkEvaluateTurn_FullPipeline` ~98us/op and
   `BenchmarkEvaluateTurnThenDecide_FullPipeline` ~189us/op (non-race, warm fixture), both 130-250x under
   `Preflight_ADD.md` §29.11's warm-evaluate P50<25ms/P95<100ms targets; ~1.3ms/~3.4ms under this node's
   own required `-race` validation command (still 20-75x under budget).

**No real cross-stage bug was found this wave** — every hand-off already failed toward the safe direction
correctly. This is documented as a legitimate, differently-shaped outcome from predictor-10's wave (which
found and fixed a real prompt-binding bypass), per the task's own explicit instruction that a clean audit
is a valid result, not grounds to manufacture busywork. `golangci-lint run ./...` reports 0 issues
repository-wide as of this wave's final commit (one `copyloopvar` finding in a first draft — a redundant
Go-1.22+ loop-variable copy left over from an older idiom — caught and fixed before the final commit). No
other role's paths were touched; `internal/features/**` was re-read but not modified.
