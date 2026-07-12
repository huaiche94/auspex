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
