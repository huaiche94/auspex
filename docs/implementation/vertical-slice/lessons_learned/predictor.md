# Lessons Learned — predictor (Phase 1: predictor-02, -03, -04)

> 🌐 English | [繁體中文](predictor.zh-TW.md)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-02 | S | S — matched estimate | 3 | 3 (doc.go, prompt.go, prompt_test.go) | not tracked pre-execution (DAG has no duration field) | one continuous pass, no wall-clock instrumentation in this environment | None | `doc.go` (package-level privacy-boundary doc comment) was not separately anticipated as its own file but is small and paired naturally with the first package file in this directory | None | A `gofmt -w` pass was needed after the first draft (struct field alignment); trivial, but confirms `gofmt -l` should run before `go vet`/tests in the validation sequence, not after, so formatting noise doesn't get mixed into functional review | The privacy assertion (Constitution §7 "raw prompt text never crosses this package's exported boundary") is cheap to test exhaustively via reflection (walk every string-kind field + a whole-struct JSON marshal) plus a structural guard against *adding* new string fields later — this pattern is reusable for every other package with a similar raw-input boundary (e.g. any future tool-output redaction code) and should be documented as a standard test pattern, not reinvented per-role |
| predictor-03 | M | M — slightly lighter than expected | 5 | 4 (taskclass.go, dto.go, classifier.go, classifier_test.go) | not tracked pre-execution | one continuous pass | None beyond the expected `internal/app/ports.go` Task/ProgressNode shapes (already available, as the DAG predicted) | None | Mid-phase, a session interruption (model-availability rate limit) occurred between drafting `taskclass.go` (enum only, uncommitted) and finishing the classifier + DTOs; the coordinator's resume message correctly diagnosed exact on-disk state via independent `go build`/`go test` runs rather than trusting the interrupted session's self-report — this is the Progress Tree "evidence not claims" principle (Constitution §6.1-2) working as intended at the meta/tooling level, not just in-product | Three early classifier test prompts accidentally tripped unintended keyword matches from the heuristic word lists (e.g. "auth" inside a fix-verb test case also matched the security-indicator list, "why" inside a question-only test case also matched the investigate-verb list) — caught immediately by `go test`, fixed by rewording the test prompts; a small amount of iteration was spent because the keyword lists are broad by design (recall > precision for a day-one heuristic), so test-prompt authors must deliberately avoid overlapping trigger words when constructing single-signal test cases | The DAG estimated 5 files for predictor-03; 4 was sufficient because the task-class enum (taskclass.go) and the classifier (classifier.go) turned out to be cleanly separable from the DTOs (dto.go) as three implementation files plus one test file, not four separate implementation files — a scope-estimator training signal that "DTOs + a classifier that consumes them" tends to cluster into 3 implementation files, not more, when the DTOs are simple value structs |
| predictor-04 | M | S/M — simpler than the M estimate suggested | 4 | 3 (doc.go, quantile.go, quantile_test.go) | not tracked pre-execution | one continuous pass | None | None | None — this was the cleanest of the three nodes; the monotonicity guarantee falls out for free from using a single sorted-array interpolation function for all three quantiles (P50/P80/P90 are just three evaluations of the same monotonic-in-p function), so there was no separate monotonicity-enforcement logic to write or debug | A property-based test (2000 random trials across varied distributions: normal, low-cardinality/duplicate-heavy, large-magnitude negative, large-magnitude positive) found zero violations on the first run — for numerically well-behaved code (pure math over float64, no I/O, no external state), property testing is cheap (sub-100ms for 2000 trials) and should be the default over hand-picked table cases whenever the function has a clean mathematical invariant to check; recommend Auspex's own scope estimator weight "needs property test" tasks slightly lower in files/LOC than "needs new state machine" tasks of the same nominal complexity label, since the two M-labeled nodes in this phase (predictor-03 state/rule logic vs predictor-04 pure math) had different actual difficulty despite the same label |

## Phase 2 (predictor-05, predictor-06)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-05 | M | M — matched estimate | 4 | 4 (doc.go, coldstart.go, estimator.go, estimator_test.go) | 300 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | The frozen `app.EstimateScopeRequest` carries only IDs (SessionID/TaskID/RepositoryID), not the repository/session/prompt features a rule-based estimator actually needs — this is a real, DAG-invisible gap between the frozen port signature and what any concrete `ScopeEstimator` implementation requires to do useful work. Not a blocker (CONTRACT_FREEZE.md itself anticipates owning roles "may find they need additional fields"), but it did require designing an extra layer (`FeatureSource`) that the DAG's file-count estimate didn't obviously anticipate | None beyond `FeatureSource` (see above) — kept inside the 4-file estimate by putting it in `estimator.go` alongside the estimator itself rather than a separate file | None | ADD §14.6's cold-start table only names 8 of 16 task classes; filling the other 8 with a documented nearest-neighbor mapping took more careful reasoning than the raw table lookup itself — a "table has gaps, fill them explainably" tax that isn't visible from the DAG's complexity label alone | The `sortTriple` monotonicity choke-point (enforce P50<=P80<=P90 once, at the end, regardless of how many heuristic adjustments ran before it) proved cheaper and more robust than trying to keep every individual multiplier/blend step monotonicity-preserving by construction — recommend this as the default pattern any time a Phase-2+ predictor node combines multiple heuristic adjustments into a quantile triple, rather than re-deriving predictor-04's "single sorted-interpolation function" trick per adjustment |
| predictor-06 | L | M — lighter than the L estimate suggested | 4 | 3 (doc.go, runway.go, runway_test.go) | 350 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None — `GracefulPauseService.Observe`'s `RuntimeObservation{SessionID, Quota domain.QuotaObservation}` shape (single observation, not a slice) was already frozen and matched the ADD §15.4 instantaneous-rate model's natural per-call shape cleanly; no request/response gap like predictor-05's | None — no separate cold-start-table file was needed here (unlike predictor-05's coldstart.go), since ADD §15.7's uncalibrated fallback is a direct threshold function, not a lookup table, so it fit inline in runway.go | None | The ADD's full §15.5 "Empirical bootstrap implementation" (EWMA, N=1000 bootstrap draws, crossing-ratio simulation) is real complexity that this phase correctly did NOT implement, per the explicit instruction to produce a cold-start-safe, uncalibrated result and Constitution §7 rule 10 ("does not add abstractions a later milestone would need but the current one doesn't") — recommend the DAG's "L" complexity label for this node be read as "L if you build the calibrated bootstrap path" vs "M if you correctly stop at the uncalibrated fallback," since the ADD itself structures §15.4-15.7 as two tiers of very different size | Given this node's explicit "High risk — false pause triggers" flag, a broad property-style sweep test (`TestScoreNeverCalibratedNeverPanics`, ~300 input combinations across used%/delta/interval) was written before the individual threshold/outlier tests, catching shape issues (e.g. confirming RiskScore stays in [0,1] and Calibrated/HitProbability stay false/nil) early and cheaply — recommend this "sweep first, then targeted cases" ordering as the default for any node flagged High risk in the DAG, mirroring predictor-04's property-testing lesson from Phase 1 but applied one level up (a scoring function with branches, not pure math) |

## Cross-node observations

- All three nodes' actual file counts were at or below the DAG's estimates (3/3, 4/5, 3/4) — in this
  phase, "M" complexity for a self-contained, dependency-light package (no I/O, no concurrency, no
  cross-package wiring beyond `internal/domain`) tended to overestimate file count by about one file.
  This is a small sample (n=3) and should not be over-generalized into a global calibration correction
  without more phases of data.
- The one process-level "blocker" encountered (a mid-session interruption) was not a task blocker in
  the DAG sense — it was resolved by the coordinator re-verifying on-disk state with build/test commands
  rather than trusting conversational memory, which is exactly the Progress Tree philosophy
  (Constitution §6 rule 1: "Conversation context ... is never the source of truth") applied one level up,
  to how a coordinator supervises a role's session. Worth noting as a positive case study for Auspex's
  own value proposition: a build/test-verified resume avoided silently losing or duplicating work.
- No blockers, unexpected dependencies, or scope surprises were severe enough to require raising an ADR
  or deviating from the frozen contracts. `internal/domain` and `internal/app/ports.go` had every shape
  needed (Confidence, RunwayForecast, Task, ProgressNode) without requesting an addition.

## Phase 4 (predictor-01, predictor-05c)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-01 | S | M — a genuine cross-role SQLite FK hazard surfaced mid-node, not visible from the DAG's "S" label | 3 | 6 (5 migration files + 1 new test file) | 150 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | `internal/storage/sqlite/migrate_test.go` is owned by foundation, not predictor, so predictor-01's own validation had to live in a new sibling file (`migrate_predictor_test.go`) in the same `sqlite_test` package rather than editing the existing one — anticipated in principle (Constitution §4.4) but not spelled out anywhere for this specific node | `migrate_predictor_test.go` itself (not separately counted in the DAG's 3-file estimate, since the DAG's file-count estimates for `-01` nodes elsewhere in this project implicitly assume "just the .sql files") | A real, previously-undocumented SQLite behavior: `PRAGMA foreign_keys = ON` makes *any* cascading DELETE anywhere in the schema fail with "no such table: X" if *any* table (even one unrelated to the DELETE) declares `REFERENCES X(...)` and X does not yet exist — not merely "unenforced until populated" as CREATE TABLE's own forward-reference tolerance would suggest. This broke 3 of foundation-06's already-merged, already-passing tests on this branch purely by adding predictor's migrations with their ADD-literal FK clauses into `turns` (claude-provider's not-yet-landed range). Root-caused by isolating the exact failing FK in a standalone `sqlite3` CLI repro before touching any Go code — much faster than iterating on the full Go test suite. Fixed by following 0004_tasks.sql's own already-established precedent (omit the FK constraint syntactically for forward references to tables outside the current migration batch, keep the column plain TEXT) rather than either (a) weakening the ADD's schema by dropping columns, or (b) trying to stub the missing tables into a real migration (which would create tables outside predictor's own range, a boundary violation) | The DAG's complexity/file-count estimates for "write N migration files matching a frozen schema" nodes implicitly assume the migrations are schema-inert until every FK target exists — that assumption is false under `foreign_keys=ON` the moment *any* forward-referencing migration lands ahead of its target. Recommend: (1) this class of hazard should be called out explicitly in a shared cross-role note (e.g. CONTRACT_FREEZE.md's migration-ranges section) so every future `-01`-style node knows to check for it up front rather than discovering it via a broken downstream test suite; (2) `-01`-style "just write migrations matching a frozen schema" nodes should default to one complexity tier higher than "S" whenever the schema's own FK graph crosses migration ranges that haven't landed yet, since verifying and fixing this is real, non-mechanical work, not transcription |
| predictor-05c | M | S/M — lighter than the M estimate, since no FeatureSource abstraction was needed (unlike scope/token) | 4 | 4 (doc.go, coldstart.go, forecaster.go, forecaster_test.go) | 300 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None beyond the already-known cold-start-telemetry gap (same as predictor-05/predictor-05b/predictor-06) — `app.ForecastQuotaRequest` already carried every field this node needed directly (`Quota []domain.QuotaObservation`, `Context domain.ContextObservation`, `TokenForecast domain.TokenForecast`), so unlike predictor-05/predictor-05b there was no package-local `FeatureSource` interface to design | None — the whole node fit cleanly into the same 4-file shape (doc/coldstart/forecaster/forecaster_test) already established by `token`, with no extra file needed | None | ADD §15.3/§15.9 give the projection *formula* (`current + predicted_delta_p90`) but, unlike §14.6's token-multiplier table, name no concrete default-delta values at all — required deriving and documenting original bootstrap constants (2%/6% quota delta P50/P90, 3%/10% context-growth-fraction P50/P90) from first principles (percentage-point-per-turn reasoning) rather than transcribing a table, similar in kind to predictor-06's "ADD gives thresholds but not all of them" tax but for a formula with *zero* named constants rather than a partially-named table | Reusing an already-established pattern from a sibling node paid off twice this node: (1) `runway.CombineWindows`'s "max across correlated windows" rule (ADD §15.5) transferred directly to combining `[]domain.QuotaObservation` into ProjectedQuotaUsedP90's single scalar, with no new design needed; (2) `runway.DefaultHorizon` (10 minutes) was reused as-is for "is a quota reset imminent relative to this turn" rather than inventing a new constant. Recommend: when a new Stage-N forecaster node's interface signature already resembles an earlier stage's (e.g. "multiple observations combine into one scalar", "needs a bounded look-ahead window"), actively search sibling packages for a reusable rule/constant before deriving a new one from scratch — cheaper and more consistent than an independently-invented equivalent |

## Phase 5 (predictor-07)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-07 | L | M — lighter than the L estimate, since ADD §16.2 names the formula's exact coefficients (unlike predictor-05c/predictor-06, which had to derive their own bootstrap constants from first principles) | 4 | 4 (doc.go, coldstart.go, combiner.go, combiner_test.go) | 400 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None beyond the already-known cold-start-telemetry gap shared with every other Stage-N node this phase's dependencies rest on; `app.CombineRiskRequest` already carried every field this node's *formula* needed structurally (Scope, TokenForecast, QuotaForecast), so — like predictor-05c — no package-local FeatureSource abstraction was needed | None — fit cleanly into the same 4-file shape already established by `quota`/`token` (doc/coldstart/combiner/combiner_test) | A real, DAG-invisible contract gap (see below) | ADD §16.2 names four completion_risk/blast_radius_risk terms (open_ended_scope, recent_retry_rate, recent_test_failure_rate, unresolved_progress_blockers, public_api_change) that have no direct field on the frozen `domain.ScopeEstimate` struct — only a `ReasonCodes` channel through which some of them happen to already be surfaced by `scope.RuleScopeEstimator`. Diagnosing "is this term unavailable, or am I missing a field" required re-reading `internal/domain/forecast.go`'s full struct twice and cross-checking against `internal/features/dto.go` before concluding the bridge had to go through `ReasonCodes`, not a request-shape change — this is exactly the kind of gap CONTRACT_FREEZE.md's "may find they need additional fields" clause anticipates, but it costs real reasoning time to confirm rather than guess | The formula-verbatim nodes (this one) are meaningfully cheaper than the derive-your-own-constants nodes (predictor-05c, predictor-06, predictor-05b) even at the same nominal DAG size label, because there is zero judgment-call surface on the arithmetic itself — all the judgment-call surface moves to "how do I map the ADD's named terms onto the frozen struct's actual fields," which is a one-time cost per missing term, not a per-formula cost. Recommend the DAG's complexity label for "combine upstream forecasts via an ADD-named closed-form formula" nodes be read as scaling with (formula terms not already present as struct fields) rather than raw formula line count — this node's four bridged terms were the whole story of why it came in lighter than L despite having the longest formula of any predictor node so far |

## predictor-07 terminology cross-check

`docs/adr/0041-predictor-forecast-layer.md`'s "Terminology note" and `CONTRACT_FREEZE.md`'s matching
section both resolve a naming fork: `Auspex_Predictor_Design_Supplement.md` calls the third risk term
`execution_risk`; `Auspex_ADD.md` §16.1/§16.2 calls the identical concept `completion_risk` with a full
formula. ADR-041 keeps the ADD's name. `internal/app/ports.go`'s frozen `CombineRiskResult` struct field
is literally named `CompletionRisk`. This implementation uses `completion_risk`/`CompletionRisk`
exclusively — the string `execution_risk` appears nowhere in `internal/predictor/risk/`'s code, only in
`doc.go`'s own comment explaining why it was deliberately avoided (so the terminology decision is
auditable in place, not just in the ADR).

## predictor-01 x predictor-05c interaction

No interaction — predictor-01 (migrations) and predictor-05c (in-memory forecaster, no storage
dependency this phase) are fully independent within Phase 4; predictor-05c does not read from or write to
any of predictor-01's new tables (that wiring belongs to a later node, predictor-09's evaluation
persistence layer). Both were still done sequentially per instruction, not in parallel.

## Phase 4 cross-node observation

- The single most valuable debugging move for predictor-01's FK hazard was reproducing the exact failure
  in the standalone `sqlite3` CLI against a 4-line synthetic schema before touching Go code at all —
  isolating "is this a Go-layer bug or a genuine SQLite semantics question" in under a minute, rather than
  re-running the full `go test ./internal/storage/sqlite/...` suite repeatedly while guessing at fixes.
  Recommend this as a standard first move any time a migration-layer test failure's error message names a
  table that "should" exist per a REFERENCES clause but doesn't yet on the current branch.

## Phase 6 (predictor-08)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-08 | L | L — matched estimate, heavier than predictor-07's "came in lighter than L" pattern because this node has no single frozen interface to implement against (see below) | 4-5 | 5 (doc.go, coldstart.go, decide.go, policy_test.go, coldstart_test.go) | 400 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | `internal/app/ports.go` has no dedicated frozen `Policy`/`Decider` interface at all — the closest thing, `EvaluationService.Decide(ctx, DecideRequest{EvaluationID})`, takes only an ID and returns a bare `{ID, Action}`, with no room for RiskScore/Probability/Confidence/reason codes. Every prior predictor-0N node (05/05b/05c/06/07) implemented directly against a named, frozen `app.XForecaster`/`app.RiskCombiner` interface; this node had to design its own bridge type (`policy.Decision`/`DecideRequest`) from ADD §17.2's `PolicyResult` shape instead, since no frozen port existed to implement against — a qualitatively different kind of node than any predictor-0N node so far | None beyond the bridge-type design itself (folded into decide.go, not a separate file) | A real bug caught by this node's own required fail-open/fail-closed test, not by a reviewer: `riskBandDecision`'s initial draft propagated `overall.Score`/`rf.RiskScore` into `Decision.RiskScore` unclamped, so a NaN/Inf upstream RiskComponent.Score (constructed directly in a test, bypassing risk.RuleRiskCombiner's own clamp01) leaked NaN/Inf straight through Decide. Fixed by adding a package-local `clamp01Risk` (deliberately mirroring internal/predictor/risk.clamp01's exact NaN-favors-highest-risk behavior) and applying it at all 6 RiskScore assignment sites, not just the one the failing test happened to exercise | This is the first predictor-0N node to consume two independent upstream pipeline outputs at once (CombineRiskResult AND RunwayForecast, per ADR-041's explicit correction) rather than one upstream stage's output — mapping "which of two inputs' Calibrated flag governs Probability" correctly (only Runway's, never Risk's, since RiskCombiner's Score is defined as never-a-probability regardless of Calibrated per risk/combiner.go's own doc comment) required rereading risk/combiner.go's "Score is not probability" comment and ADR-041's exact wording twice before writing riskBandDecision, to avoid the tempting-but-wrong shortcut of gating Probability on "is anything here calibrated" rather than "is Runway specifically calibrated" | Recommend the DAG's complexity/file-count estimate for "terminal-stage node with no frozen interface to implement against" (this node) be read as structurally different from "implement against a named frozen interface" nodes (predictor-05 through -07): the former requires designing and documenting a bridge type from prose (ADD §17.2's PolicyResult) before any decision logic can be written, which is real, non-mechanical design work with no formula or interface signature to anchor against — closer in kind to predictor-05's FeatureSource-abstraction tax than to predictor-07's "formula-verbatim, lighter than L" experience |

### predictor-08 cold-start invariant verification approach

The DAG's own risk note for this node ("High — must never label an uncalibrated score a probability")
and the task instruction's explicit ask for "a dedicated, unambiguous test suite proving the
cold-start/uncalibrated-never-becomes-probability invariant holds for every single policy action" drove a
two-layer verification strategy, not just more test cases:

1. **Structural**: exactly two call sites in the entire package (`decide.go`'s `runwayPauseDecision`, both
   gated by an explicit `rf.Calibrated &&` check immediately before the assignment) ever construct a
   non-nil `Decision.Probability`. Every other Decision literal in the package sets it to a literal `nil`.
   This is verifiable by inspection/grep, not just by running tests — a future edit that tries to sneak a
   probability in from a different code path is a one-line diff away from breaking a documented, named
   invariant (`Decision.Probability`'s own doc comment names the exact guard), not a silent behavioral
   drift.
2. **Behavioral**: `coldstart_test.go` proves the *narrower and correct* claim ("uncalibrated never becomes
   a probability") rather than the *broader and wrong* claim ("probability is always nil") by including a
   deliberate control case
   (`TestColdStartArmedButNotYetConfirmedRunwayIsCalibratedAndMayReportProbability`) where a genuinely
   calibrated Runway input legitimately produces a non-nil Probability. Recommend this "prove the narrow
   claim, include a control case for the broader wrong claim" pattern for any future Auspex invariant
   test suite guarding a boolean-gated field — a suite that only ever asserts "field is nil" everywhere
   would pass even if the gating logic were deleted entirely, silently making the invariant meaningless
   while still green.

## Phase 6 cross-node observation

- This phase's fail-open/fail-closed required test (agents/predictor.md's "no divide-by-zero/NaN/Inf" test,
  reused from predictor-04/-06/-07's own precedent) earned its keep immediately: it caught a real,
  non-hypothetical clamping gap in the first draft rather than merely confirming an already-correct
  implementation, reinforcing predictor-06/-07's lesson that this property-style sweep should be written
  and run *before* declaring a High-risk node's decision logic finished, not as a final rubber-stamp pass.

## Phase 7 (predictor-09)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-09 | M | M/L — matched the M label for implementation, but genuinely required more design work than "wire four existing stages together" suggests | 4 | 6 (doc.go, datasource.go, store.go, service.go, pipeline.go, plus two test files helpers_test.go/service_test.go/authorization_test.go — 8 total counting tests) | 300 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | A real, documented conflict surfaced between three sources: this phase's task instructions (which explicitly asked for a full `ConsumeAuthorization` — expiry, replay rejection, prompt/session binding — to be built as part of predictor-09), `docs/implementation/vertical-slice/EXECUTION_DAG.md` (which assigns that exact behavior to a separate, not-yet-started downstream node, `predictor-10`, "One-time authorization", with its own dedicated `-run Authorization` validation gate), and migration `0044_authorizations.sql`'s own comment (written by this role in an earlier phase) stating consumption is "enforced by predictor-10's service logic." Resolved by building `ConsumeAuthorization` for real now (a frozen Go interface cannot be partially implemented, and Constitution §7 rule 11 forbids a stub-and-claim-complete), while documenting the conflict explicitly in `doc.go` and treating predictor-10's own dedicated validation gate as still the authoritative place this behavior is re-verified/extended in a later phase — not silently picking one instruction and hiding the discrepancy | None beyond the DataSource bridge interface itself (datasource.go), which was anticipated going in (mirrors scope/token's own FeatureSource precedent) | `app.DecideRequest` carries only an `EvaluationID`, no risk/runway payload — confirmed by reading `internal/orchestrator/evaluate.go` (a sibling role's already-merged code calling `Decide(ctx, DecideRequest{EvaluationID: evaluation.ID})` immediately after `EvaluateTurn`, with nothing else available to pass) that `Decide` cannot plausibly recompute a policy decision and must read back what `EvaluateTurn` already persisted. This was resolved by direct evidence (reading the actual calling code) rather than guessing from the DTO shape alone — recommend this as a default move whenever a frozen DTO looks under-specified for its own interface method: check whether another already-merged role's code already calls it, before assuming the DTO gap implies free design latitude | Also confirmed, by reading `internal/predictor/scope/estimator.go` and `internal/predictor/token/forecaster.go` side by side, that two same-named `FeatureSource.Progress` methods across sibling packages have genuinely different signatures (`*domain.TaskID` vs `domain.SessionID`) — a test-helper adapter that assumed structural/embedding compatibility across both would have silently compiled wrong (Go structural typing does not catch a same-name-different-signature mismatch at the embedding call site the way it would at a direct interface-satisfaction check) until `go vet`/`go test` surfaced it; cost about one extra edit cycle, not a real blocker | Migration files 0040-0044 (already written in Phase 4/predictor-01) turned out to double as load-bearing design documentation for this node — each one's own comment named "predictor-09" explicitly and described exactly what this node was expected to persist and how (including the predictor-10 authorization-consumption split noted above). Recommend this as a reusable pattern: a `-01`-style "just write migrations" node's SQL comments should keep naming the specific downstream node ID that will consume each table, since those comments turned out to be more precise and more load-bearing than the DAG's own one-line description when this node actually started building against them |

## Phase 7 cross-node observation

- This is the first predictor node to persist real state across a full multi-stage pipeline call inside a
  single `app.TxRunner.WithTx` transaction (feature_vectors + predictions + policy_decisions as one atomic
  write) — mirrors `checkpoint`'s `ProgressTreeService.CompleteNode` discipline (CONTRACT_FREEZE.md's
  transaction-boundary section) one layer up in a different role's package, confirming that discipline
  transfers cleanly to a read-heavy-then-write-once pipeline shape, not just a single-entity update.
- `ConsumeAuthorization`'s exactly-once guarantee is enforced by a single conditional `UPDATE ... WHERE
  consumed_at IS NULL` inside the same transaction as the read that validates binding/expiry, not by an
  application-level "check then write" pattern — the latter has a TOCTOU race under concurrent callers
  that the former closes structurally. Verified directly by a dedicated concurrent-goroutine test
  (`TestConsumeAuthorization_ConcurrentReplayOnlyOneWins`, 8 goroutines racing one authorization ID,
  asserting exactly 1 success) in addition to `go test -race`, since `-race` alone proves the absence of a
  data race, not the absence of a logic race (two goroutines can each cleanly acquire their own
  transaction and still both "succeed" if the UPDATE's WHERE clause were missing) — recommend this
  distinction (data race vs logic race, and that `-race` alone does not catch the latter) be called out
  explicitly for any future exactly-once/idempotency node in this project.
- Every fixed-point-in-time assertion in this node's tests (expiry boundary, deterministic-output) is
  driven by a local `fakeClock` with an explicit `Advance` method, never `time.Now()` — continuing the
  project-wide convention already established in `internal/scheduler/lease_test.go`, confirming this
  pattern (rather than a shared package-wide fake) remains the right default even for a node with heavier
  clock-sensitivity (expiry) than most prior predictor nodes.

## Phase 8 (predictor-10)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-10 | M | M — matched estimate, but the value was almost entirely in the audit reasoning, not the code volume (the real fix is a one-line diff) | 4 (DAG estimate) | 3 (service.go one-line-condition fix + comment, authorization_test.go rewritten/expanded, doc.go addendum) | 350 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None — this node's whole point was to audit its own already-merged package, not integrate with any other role's code | None | None | The single real finding (prompt-hash binding skippable by omitting the request field, regardless of what the authorization was actually bound to) was hiding in plain sight in predictor-09's own recorded assumption #530 ("PromptHash binding is checked only when req.PromptHash is non-empty") — the previous phase had already noticed and documented the asymmetry between TurnID (always checked) and PromptHash (conditionally checked) without flagging it as a risk, because at the time it was framed as "mirrors the frozen DTO's own optional field" rather than "a caller can choose not to be bound." This is a distinct kind of gap from a missing test: the behavior was tested (`TestConsumeAuthorization_RejectsWrongPrompt` always supplied a non-empty, wrong PromptHash) but the boundary case that mattered — omitting it entirely against a bound authorization — was never exercised, so green tests coexisted with a real bypass. Recommend that any "field is optional in the DTO" design note attached to a security-relevant binding check be treated as a required adversarial-test prompt for the next audit pass, not just a settled assumption | Reverting the one-line fix and re-running the new adversarial test to confirm it fails without the fix (and passes with it) took under a minute and produced unambiguous, load-bearing proof that the finding was real rather than a defensive-but-unnecessary addition — recommend this "revert, confirm red, restore, confirm green" step as a mandatory part of any audit-node report claiming a fix, not just a nice-to-have, since without it a reviewer has no way to distinguish "found and fixed a real gap" from "added a redundant test around code that was already correct" |

## Phase 8 cross-node observation

- An audit/hardening node (re-verify existing, already-shipped logic rather than build new logic) has a
  different cost profile than a build node: almost all the effort went into deciding which adversarial
  scenarios were worth testing and reading the existing code/docs/history closely enough to find the one
  real asymmetry (prompt-hash binding conditioned on the request, not the row), while the fix itself and
  most of the new tests were mechanical once the gap was identified. Recommend the DAG continue
  distinguishing "audit" nodes from "build" nodes explicitly (as predictor-10 already was, via its
  dedicated `-run Authorization` gate and "re-verified/extended" framing in predictor-09's own scope note)
  since raw LOC/file-count estimates are a weak signal for this node shape — the real cost driver is
  reasoning time against existing code and documented assumptions, not new surface area.
- Confirmed empirically (not just by reading `internal/storage/sqlite/db.go`'s comments) that this
  package's SQLite WAL + `busy_timeout=5000` configuration makes the `UPDATE ... WHERE consumed_at IS
  NULL` exactly-once guarantee contention-independent within a wide range: raising the concurrent-replay
  test from predictor-09's original 8 goroutines to 64 required no code change and no increase in test
  flakiness. Recommend treating "raise the goroutine count on an existing concurrent test by 8x with zero
  code change and confirm it still holds" as a cheap, standard hardening-pass technique for any future
  exactly-once/idempotency audit node, rather than assuming a low-concurrency test that already passes is
  sufficient evidence at higher contention.
- A shared `requireDomainError(t, err, wantCode) *domain.Error` test helper (returning the asserted error
  for the few call sites that need to inspect it further, e.g. checking `Retryable`) replaced repeated
  `errors.As`/`domain.Error` boilerplate across the rewritten test file — but this introduced a real
  `golangci-lint` `errcheck` finding at call sites that didn't use the return value as a bare statement.
  Recommend that any shared test helper returning a value only some callers need should be written with
  that in mind from the start (e.g. document that bare-statement callers must use `_ = helper(...)`),
  rather than discovering the lint gap only after golangci-lint runs — it cost one extra edit pass here.

## Phase 9 (predictor-11 — final DAG node, role closes out)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| predictor-11 | L | M/L — the required test-writing itself was L-shaped, but no new production code was needed since the audit found nothing to fix | 1 new file (DAG's own description implies "add tests", not a file-count target) | 2 (pipeline_e2e_test.go new; helpers_test.go extended with error-injecting DataSource fields + four errInjecting* stage wrappers + a testStages bundle) | 450 (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None — this node is explicitly scoped to internal/predictor/**, internal/policy/**, internal/evaluation/**, all already owned and already merged; no other role's paths were touched | `helpers_test.go` (existing, from predictor-09) needed extending rather than a wholly new fixture file, since the fail-open/fail-closed adversarial suite (this node's highest-risk required test) needed error-injection hooks on every `DataSource` method AND wrapper types for all four ADR-041 pipeline-stage interfaces (`ScopeEstimator`/`TokenForecaster`/`QuotaForecaster`/`RiskCombiner`) that predictor-09's original fixture never needed, since predictor-09 only ever tested one failure mode (`Resolve`) | None | The DAG's own scenario list ("ScopeEstimator errors, TokenForecaster returns all-nil, QuotaForecaster times out") is phrased as three different FAILURE SHAPES, not just three different injection points — a generic `err error` field per stage would have missed this distinction. Modeling `errInjectingTokenForecaster.nilResult` (succeeds with a zero-value result, no error) as structurally different from `errInjectingQuotaForecaster.timeout` (a specific retryable domain.Error) forced writing two different assertions (`Calibrated` must stay false vs. the call must fail closed) rather than one generic "returns an error" check — this distinction mattered and would have been lost if the adversarial suite treated "stage returns an error" as the only failure mode worth testing | The audit found zero real defects — every hand-off in the chain (DataSource-level and stage-level) already failed correctly: an upstream error propagates as a `runPipeline` error (no partial persistence, confirmed directly by querying the `predictions` table after each forced failure, not just checking the returned error), and the one legitimate "degrade without erroring" case (TokenForecaster returning an all-nil result) already produces `Calibrated=false` by construction, never a fabricated confident result. This is a genuinely different, equally valid outcome from predictor-10's phase (which found and fixed a real bug) — recommend the DAG/Constitution continue treating "audited thoroughly, found nothing" as a first-class, non-inferior outcome to "audited and fixed a bug," provided the audit's negative result is backed by the same standard of evidence (a suite that would have failed had the bug existed, not just green tests that never really exercised the failure path) |

## predictor-11 verification approach: proving an audit found nothing, not just asserting it

Unlike predictor-10 (Phase 8), which found and fixed one real cross-call bug, this node's fail-open/
fail-closed adversarial pass (`TestFullPipeline_UpstreamErrorsFailClosed_NeverSilentAllow`,
`TestFullPipeline_StageErrorsFailClosed`, `TestFullPipeline_DegradedRunwayNeverSilentlyAllowsWhenEmergency`,
`TestFullPipeline_NeverProducesPolicyRunFromMissingCriticalSignals`) found the existing chain already
correct at every injected failure point. Per Constitution §6/§7 discipline ("completed means evidenced," not
"claimed"), a negative audit result is only meaningful if it is falsifiable — this suite is: every
DataSource method and every one of the four ADR-041 pipeline-stage interfaces has a dedicated error-
injection path (nine `DataSource` methods, four stage wrappers), each wired through a real
`evaluation.Service` (not a mock of the service itself), each asserting the specific safe outcome
(`EvaluateTurn` returns an error AND leaves zero rows in `predictions` for that TurnID — checked directly
via SQL, not just "err != nil"). A version of this suite that only checked "err != nil" without also
confirming zero partial persistence would have been strictly weaker, since CONTRACT_FREEZE.md's
transaction-boundary section names partial persistence (not just a wrong return value) as the specific
failure mode a `WithTx`-wrapped operation must prevent.

## predictor-11 cross-node observation: the wide-table-driven full-chain fuzz caught nothing new, and that
is itself informative

`TestFullPipeline_WideTableFuzz` (11 hand-picked fixtures spanning cold-start/fully-populated/pathological-
value/extreme-magnitude/runway-emergency/runway-debounce shapes, plus 200 programmatically randomized
input combinations across quota/context UsedPercent ranges including out-of-[0,100] values and 0-40-sample
token histories) found zero panics, zero NaN/Inf escapes, and zero invalid Confidence values across the
full Scope->Token->Quota->Risk->Policy->Decide chain. This corroborates (rather than merely repeats) each
individual stage's own prior property tests (predictor-04/-06/-07/-08's own "no NaN/Inf" sweeps): the
absence of a NEW failure at the chain level, after each stage already independently proved this property in
isolation, is evidence the per-stage clamping/degradation disciplines (`clamp01`/`clamp01Risk`, "unknown is
not zero" pointer semantics, cold-start `Calibrated=false` gating) compose correctly across hand-offs, not
just within a single stage's own boundary. This is the specific kind of confidence a package-level test
suite cannot provide by itself, regardless of how thorough it is, and is likely valuable general guidance
for how any future multi-stage Auspex pipeline (not just this one) should close out its final integration
node: a wide, adversarial, chain-level property fuzz is worth running even when every individual stage
already has its own property tests, precisely because a hand-off bug is invisible to per-stage tests by
construction.

## predictor-11 benchmark results vs. ADD §29.11

`BenchmarkEvaluateTurn_FullPipeline` (one full `EvaluateTurn` call: Scope -> Token -> Quota -> Risk ->
Policy, plus the `feature_vectors`/`predictions`/`policy_decisions` transactional persistence, against a
real migrated SQLite DB, `benchtime=1000x`, no `-race`): **~98 microseconds/op, ~6.8 KB/op, 136 allocs/op**.
`BenchmarkEvaluateTurnThenDecide_FullPipeline` (the same, plus the immediately-following `Decide` read-back
a real caller always performs per `internal/orchestrator/evaluate.go`'s existing wiring): **~189
microseconds/op, ~12 KB/op, 310 allocs/op**. Under `-race` (this node's own required validation command),
both numbers scale up by roughly an order of magnitude (~1.3ms and ~3.4ms/op respectively) purely from
race-detector instrumentation overhead, not from the production code path itself. Compared against
`Auspex_ADD.md` §29.11's stated targets — **warm evaluate P50 < 25 ms, P95 < 100 ms** — even the inflated
`-race` numbers carry roughly 20-70x headroom against the P50 target and 30-75x against the P95 target; the
non-`-race` numbers carry roughly 130-250x headroom. `predictor-08`'s own `BenchmarkDecide` (Policy alone,
~53-128ns/op depending on machine load) remains far under the separate `policy < 1ms` sub-budget, unchanged
by this phase. Recommend future roles benchmarking a SQLite-transaction-backed hot path always report both
the `-race` and non-`-race` numbers side by side against an ADD-stated budget, since the two can differ by
an order of magnitude and a reviewer comparing only the `-race` number against a tight external budget could
otherwise wrongly conclude a real regression exists.

## predictor-11 role retrospective: full arc across Phases 1-9

This is the predictor role's last assigned DAG node — every node from `predictor-01` through `predictor-11`
is now `completed`, closing out the role's full scope (`internal/features/**`, `internal/predictor/**`,
`internal/policy/**`, `internal/evaluation/**`). Looking back across all nine phases:

- **The pipeline grew by contract amendment, not by silent scope creep.** ADR-041 (accepted before any
  Phase-2 code was written) split what the original Bootstrap DAG conflated (`predictor-07` depending on
  `predictor-06`) into the correct five-stage chain (Scope -> Token -> Quota -> Risk -> Policy, with Runway
  independent) plus two new nodes (`predictor-05b`/`predictor-05c`). Every subsequent phase built directly
  against that corrected contract; no later phase had to retroactively patch a structural mistake, because
  the mistake was caught and fixed at the contract layer before implementation began, exactly as
  Constitution §6/§7's "a real gap found before code is written is fixed at the contract layer" principle
  intends.
- **Two distinct real bugs were found across the whole arc, both by adversarial testing built specifically
  for a High-risk-flagged node, never by incidental discovery**: predictor-08's unclamped NaN/Inf
  RiskScore leak (Phase 6, caught by that phase's own required fail-open/fail-closed test) and predictor-10's
  prompt-hash-binding bypass (Phase 8, caught by a dedicated adversarial audit pass). predictor-11's own
  adversarial pass (Phase 9) found no third bug — a legitimate, differently-shaped but equally rigorous
  outcome, not a lesser one, provided (as documented above) the negative result is itself falsifiable
  evidence, not an absence of looking.
- **Every stage that needed to invent its own bootstrap constants documented exactly why and how**
  (predictor-05's cold-start fallback table, predictor-05b's token multiplier caps and P80 interpolation,
  predictor-05c's default quota/context deltas, predictor-06's outlier thresholds) — none of these were
  silently asserted as measured values; every one is traceable to "the ADD names the mechanism but not this
  specific constant" and flagged for replacement once real historical telemetry lands in a later phase. This
  discipline, applied consistently for nine phases, is what let predictor-11's cross-stage audit focus
  entirely on hand-off correctness rather than re-litigating whether any individual stage's numbers were
  defensible — that question was already answered, phase by phase, as each stage was built.
- **The cold-start-safe, uncalibrated-is-not-a-probability invariant (Constitution §6/§7, this role's single
  most load-bearing rule) held everywhere it was tested, at every layer**: `domain.RiskComponent`/
  `TokenForecast`/`QuotaForecast`'s own `Calibrated` field, `policy.Decision.Probability`'s two-call-site-only
  structural guard, and now this phase's full-chain fuzz and fail-open/fail-closed suite confirming the same
  property holds end-to-end through real persisted `Evaluation`s. No phase ever found a violation of this
  specific invariant — the closest was predictor-08's NaN/Inf leak, a related but distinct bug (a
  score-magnitude bug, not a calibration-claim bug).
- **Every phase merged `origin/main` first and confirmed a clean whole-repo build/test before writing new
  code**, catching integration drift early rather than discovering it at a later merge. This phase's merge
  (`379b7cf` -> `36e7ffb`, Phase 8's integrated state) was clean and required no adaptation before predictor-11's
  own work began, consistent with every prior phase's experience on this branch.
- **Recommendation for any future project structuring a similar multi-stage predictor/policy pipeline**:
  reserve an explicit final "prove the chain" node (this project's `predictor-11`) distinct from the
  per-stage build nodes, even when every stage already has thorough package-level tests — the value is not
  redundant coverage, it is coverage of the hand-offs themselves, which are by construction invisible to any
  single stage's own test suite, and this phase's zero-new-bugs result is itself only meaningful because that
  distinct node existed and was held to the same evidentiary standard as a bug-finding phase.

## Final-integration-gate correction (`SQLDataSource`)

| task_id | estimated_complexity | actual_complexity | files_changed | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|
| predictor-final-datasource | not DAG-estimated (lead-identified finding) | M/L — nine methods each needed independent, evidence-based real-vs-cold-start judgment, not a single formula to transcribe | 2 (datasource_sql.go, datasource_sql_test.go — both new) | None blocking; the main cost was reading enough of six other roles' schemas/exported types (foundation's four core tables, claude-provider's events+normalizer, checkpoint's progress store, this package's own four migrations) before writing a single query, to avoid guessing a payload field name or a table's real column set | The single most valuable move was grepping `internal/telemetry/claude/normalizer.go` for the EXACT payload key strings (`prompt_sha256`, `used_percent`, `total_tokens` absence, etc.) before writing any SQL against `events.payload_json` — a plausible-sounding guessed key name (e.g. `"total_token_count"` instead of the actually-absent `"total_tokens"`) would have silently compiled and silently always returned nothing, with no test able to catch the mismatch unless the test itself independently re-derived the same wrong guess | This task's finding (a frozen interface with zero production implementations, satisfied only by test fakes, six phases after the interface itself was designed) is a class of gap that `grep -rn "var _ <Interface> = "` across a whole repo makes cheap to detect systematically — recommend Auspex's own integration-gate checklist include this exact grep pattern for every frozen or package-local interface as a standing, repeatable check, not a one-off manual finding, since it would have caught this gap at any of the six intervening phases rather than only at the Final gate |

### Real-vs-cold-start judgment calls were the actual work, not the SQL

Unlike a typical predictor-0N node (implement one formula against one frozen interface), this correction's
nine methods each required an independent judgment call — read the interface's own doc comment and the
actual schema, then decide honestly whether a real query exists or whether the honest answer is
`ok=false` — with no single precedent to mechanically apply across all nine. Two decisions turned out to
be genuinely different in kind from the other seven:

- `RecentSimilarTurnTokens` and `RunwayForecast` are both "real query, honestly empty/absent in practice
  today" — the query is correct and will activate automatically the moment upstream data exists (a future
  claude-provider payload field, a future `internal/pause` persistence phase respectively), mirroring
  predictor-05b's own ">=8-sample branch is real but unreachable this phase" precedent exactly.
- `Repository` and `Session` are "no query would help" — every field those two DTOs promise describes a
  signal category (repository file/language census, empirical session-turn quantiles) that literally has
  no persisted representation anywhere in the schema this correction was scoped to work within, and
  synthesizing one (e.g. guessing a "retry" definition from `turn.failed`/`turn.started` counts) would have
  been inventing a new, undocumented modeling decision under the guise of "implementing an interface" —
  recommend future roles facing a similar "the interface promises N fields, only some have real backing
  data" situation explicitly distinguish these two cases in the write-up (as this correction did), since
  conflating them either overclaims coverage or underclaims effort.

### Reading other roles' migration-file comments as load-bearing documentation, again

Consistent with predictor-09's own Phase 7 lesson ("migration files ... double as load-bearing design
documentation"), this correction's `RunwayForecast`/`PriorRunwayHitConfirmed` implementations depended on
`0042_runway_forecasts.sql`'s and `0043_policy_decisions.sql`'s own header comments (documenting the
turn_id-not-session-id scoping, and cross-referencing which downstream role/method consumes each table) as
much as the column list itself — recommend continuing to write migration headers this way project-wide, as
this is now the second independent instance where a migration's own prose, not just its DDL, was the
correct primary source for a downstream implementer's design decision.
