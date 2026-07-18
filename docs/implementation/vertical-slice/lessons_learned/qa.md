# Lessons Learned — qa (Phase 3: qa-01, qa-08)

> 🌐 English | [繁體中文](qa.zh-TW.md)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| qa-01 | S (DAG: 200 LOC, 4 files) | S — matched estimate | 4 | 1 (`.github/workflows/ci.yml`) | not tracked pre-execution (DAG has no duration field) | one continuous pass, no wall-clock instrumentation in this environment | `task`/`golangci-lint` were not preinstalled in this fresh worktree; both were already available at `$(go env GOPATH)/bin` because this worktree shares the host's GOPATH with the main checkout where `foundation-09` installed them — only needed to add that dir to `PATH` for this session, not a real new dependency | `actionlint` — installed locally (`go install github.com/rhysd/actionlint/cmd/actionlint@latest`) purely to strengthen validation of the GitHub Actions YAML beyond a bare `yaml.safe_load` parse; not a repository dependency, not referenced by any tracked file | None | The DAG estimated 4 files for qa-01; 1 file (the workflow itself) was sufficient — a CI workflow that calls into an already-existing Taskfile rather than reimplementing checks inline collapses what might otherwise be a multi-file scaffold (separate lint/test/build configs) into one YAML file with a handful of jobs. This mirrors predictor's Phase-1 lesson (`predictor-03`) that DAG file-count estimates for "thin orchestration over an existing primitive" tend to run below estimate | The single most useful validation step beyond local dry-run (`task fmt/lint/build/test`) was `actionlint` — it catches GitHub Actions-specific mistakes (bad `uses:` refs, wrong `runner.os` comparisons, invalid expression syntax) that a generic YAML parser and a local shell dry-run both miss entirely, since neither actually parses the Actions schema or evaluates `${{ }}` expressions. Recommend `actionlint` be treated as a standard, cheap pre-commit check for any future `.github/workflows/*.yml` change, not just this first one |
| qa-08 | S (DAG: 300 LOC doc, 4 files) | S — matched estimate | 4 | 4 (`SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`) | not tracked pre-execution | one continuous pass | None — ADD §30.7/§30.8 supplied concrete policy content directly, no cross-role lookup needed beyond reading README.md's existing Contributing section and `agents/qa.md`'s own Security assertions list | None | The repository has no `LICENSE`/`NOTICE` file yet (a gap `foundation-09` had already self-flagged as out of its own phase's scope). Not a blocker to writing qa-08's four docs — Apache-2.0 is stated by name, consistent with `README.md`'s Tech stack table — but it is a real, pre-existing gap now cross-referenced from `CONTRIBUTING.md`/`GOVERNANCE.md`, re-filed in this progress artifact's `blockers` for `foundation`/`contract-integrator` rather than fixed here, since `LICENSE`/`NOTICE` are not in `agents/qa.md`'s exclusive-paths list | None | ADD §30.2 names several `task` targets (`task bootstrap`, `task test:race`, `task test:e2e`, `task vscode:test`, `task research:test`, `task verify`) that do not exist in the actual `Taskfile.yml` yet, because the trees they'd operate on (`vscode/`, `research/`) don't exist yet either. Documenting only the subset of ADD §30.2's list that Taskfile.yml actually implements today (rather than the ADD's full aspirational list) avoided writing a CONTRIBUTING.md that would send a new contributor to run a command that fails immediately — worth normalizing as a general rule: governance/contributor docs should assert only what `git grep`-verified, currently-real tooling supports, with the ADD's fuller future list called out explicitly as "not yet implemented," not silently promised as already working |

## Cross-node observations

- Both nodes were genuinely independent (qa-01 has zero shared files or concepts with qa-08 beyond both
  living under Constitution §4's path-ownership discipline) and were executed sequentially with a full
  validation-and-commit cycle between them, per this phase's task instruction — this was straightforward
  because neither node's artifacts or assumptions needed revision once the other node's work was visible.
- Unlike predictor's Phase 1/2 nodes, qa-01/qa-08 touch zero `internal/domain` or `internal/app/ports.go`
  surface — both are pure infrastructure/documentation nodes with no frozen-contract dependency risk, which
  is consistent with the execution DAG marking both "Low" risk and having no blockers listed.
- The qa role's own packet (`agents/qa.md`) explicitly forbids altering feature production code in the
  initial pass ("File defects against the owner; only the contract-integrator authorizes cross-owner
  fixes") — this was tested for real in qa-08 via the LICENSE/NOTICE gap: the correct action was to
  document the gap in this progress artifact's `blockers` field, not to create the missing files directly,
  even though doing so would have been technically trivial and arguably helpful. Recommend this restraint
  be called out explicitly in onboarding for any future cross-cutting role (a role whose docs reference
  another role's missing artifact) as the expected pattern, not an edge case.
- The commit-SHA-in-progress-artifact field has an inherent chicken-and-egg problem: a commit's SHA is
  content-addressed and includes the commit message, so the artifact's own `commit:` field cannot be known
  before the commit exists. This repository's own precedent (commit `940c5cb`, "Record Bootstrap commit SHA
  in CONTRACT_FREEZE.md and progress artifact") already established the resolution — commit the real work
  first, then a small immediate follow-up commit records the SHA in the progress artifact. Both qa-01 and
  qa-08 followed that same pattern in this phase rather than inventing a different convention.
