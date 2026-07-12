# Wave 2 Lessons Aggregation

| Field | Value |
|---|---|
| Phase | 3.3 — Post Wave 2 Analysis |
| Source | All 5 `lessons_learned.md` files: `contract-integrator`, `foundation`, `claude-provider`, `checkpoint`, `predictor` (Bootstrap + Wave 1 + Wave 2 entries, 19 node-rows total) |
| Status | Aggregation only. No implementation changed. |

## 1. Recurring issues, ranked by frequency

Each issue below lists every node where it was independently observed.
"Independently" matters: none of the five lessons-learned files were
written with visibility into the others, so repetition across files is a
genuine convergent signal, not one observer's opinion echoed five times.

### #1 — DAG file-count estimate conflates implementation and test files (5 occurrences)

Observed in: `foundation-01`, `foundation-02`(implicitly, via the split
into 4 files where 3 were estimated), `checkpoint-b02`, `predictor-03`,
and named explicitly as a general pattern in `checkpoint-b03`'s
recommendations. This is the single most-repeated specific finding in
the entire dataset. See `Calibration_Report.md` §1 for the full analysis.

### #2 — DAG has no duration field, no token field, at all (4 explicit mentions + true for all 19 nodes)

Explicitly named as a gap in: `contract-integrator` ("DAG has no duration
field; not tracked pre-execution"), `foundation-01` ("EXECUTION_DAG.md
gives LOC and file estimates but no duration estimate/unit at all"),
`checkpoint-b02` ("n/a — DAG has no duration field"), and implicitly true
for every single row of `Prediction_Error_Report.md`. This is not a
"recurring issue" in the sense of a mistake repeated — it is one structural
gap whose absence was independently noticed and flagged by 4 of 5 roles.

### #3 — A harness-level session interruption occurred mid-node, requiring lead re-verification (3 occurrences)

Observed in: `claude-provider-03` (Wave 1, rate-limit interruption between
drafting `stop.go` and writing its test), `checkpoint-b02` (Wave 1,
interruption after implementation files were written but before
tests/docs/commit), `predictor-03` (Wave 1, interruption between drafting
`taskclass.go`'s enum and finishing the classifier). All three were
recovered the same way: the lead independently re-ran `gofmt`/`go
build`/`go vet`/`go test` against on-disk state rather than trusting the
interrupted session's self-report, then resumed the teammate with an exact
description of what existed vs. what was missing. Zero rework was needed
in any of the three cases — the interruption cost lead-verification time,
not implementation time.

### #2b — Frozen contract lacked a field/port a role actually needed, worked around locally rather than editing the contract (3 occurrences)

Observed in: `claude-provider-02` (no frozen allow-response shape for
UserPromptSubmit — made and documented a judgment call), `predictor-05`
(no repository/session feature-lookup port in `app.EstimateScopeRequest`
— introduced a package-local `FeatureSource` interface instead of editing
`ports.go`), `foundation-04` (no frozen `Lock` interface existed anywhere
— the mechanism was left as the role's own explicit design choice, not a
gap requiring escalation). In all three cases the role stayed inside its
owned paths and treated the gap as "CONTRACT_FREEZE.md itself anticipates
owning roles may find they need additional fields" rather than as a
blocker requiring a STOP.

### #4 — Cross-document inconsistency in the frozen docs themselves (2 occurrences)

Observed in: `claude-provider-06` (PascalCase vs. kebab-case CLI
subcommand casing between `Preflight_ADD.md` Appendix E.3 and three other
frozen documents — independently confirmed by the lead reading the ADD
text directly) and, at a smaller scale, `foundation-04` (the DAG's stale
full-scope estimate left standing next to an explicit reduced-scope
instruction, discussed in `Prediction_Error_Report.md`).

### #5 — A fixture/test disagreed with the implementation it was meant to test, requiring reconciliation (2 occurrences)

Observed in: `claude-provider-03` (`unknown_category.json`'s `status_code:
599` was actually correctly classified by the 5xx-range fallback rule,
meaning the fixture — not the classifier — was wrong for what it claimed
to test) and `predictor-03` (three early classifier test prompts
accidentally tripped unintended keyword matches from the heuristic's
intentionally-broad word lists). Both were caught by the test suite
itself (`go test` failing), not by manual review, and both root-cause to
the same pattern: fixtures and implementation were authored in parallel
from intuition rather than derived from one shared decision table.

### #6 — A pre-existing/leftover file needed inspection before trusting it (1 occurrence, but with a clear generalizable lesson)

Observed in: `claude-provider-01` — 5 fixture files survived an earlier
interrupted attempt and were kept after verifying they matched ADD §22.5's
field list, rather than being blindly trusted or blindly discarded.

## 2. Unexpected dependencies (all instances, not just repeated ones)

| Node | Unexpected dependency | Resolution |
|---|---|---|
| Bootstrap | Go toolchain version mismatch (1.19.1 installed vs. 1.26.x required) | Repository owner approved a `brew upgrade` |
| Bootstrap | `go.mod` bootstrap ownership conflicted with Constitution's own path-ownership rule | Repository owner ruled: lead-only, one-line exception |
| `foundation-01` | `github.com/google/uuid`, `github.com/spf13/cobra` + transitive deps | Pre-anticipated by the task brief; not a surprise in practice, only relative to the DAG's silent "None" |
| `foundation-05` | `modernc.org/sqlite`'s transitive tree (`modernc.org/libc`, `ccgo/v4`, `cc/v4`, etc.) | Fully anticipated by the ADD's pure-Go/no-CGO driver decision; heavier `go mod tidy` output than earlier nodes, not a design surprise |
| `foundation-09` | `task` (go-task) and `golangci-lint` were not preinstalled; `brew install` failed on outdated Xcode CLT | Both installed via `go install` into GOPATH bin, independent of the module's own dependency graph |
| `claude-provider-04` | Used `domain.Clock`/`domain.IDGenerator` (already-frozen ports) instead of waiting on `foundation-06`'s not-yet-built `internal/idgen` | First consumer of that seam; consistent with CONTRACT_FREEZE.md's stated flexibility |
| `predictor-05` | `app.EstimateScopeRequest` lacked feature-lookup fields (see #2b above) | Local `FeatureSource` interface |

No unexpected dependency in this dataset required a contract change, an
ADR, or a deviation from a frozen path boundary — every one was resolved
either by pre-authorization, a documented local workaround, or (twice,
for Bootstrap) an explicit repository-owner ruling.

## 3. Unexpected files (all instances)

| Node | Unexpected file(s) | Why |
|---|---|---|
| Bootstrap | `go.mod`, `internal/domain/status_test.go`, `pkg/protocol/v1/event_test.go` | Test files weren't separately enumerated as deliverables but were required by the Completion Definition |
| `foundation-01` | `go.sum` | Mechanical follow-on of `go.mod` changes; DAG estimates don't appear to count it |
| `foundation-02` | `fake_env_test.go` split out from `paths_test.go` | Readability choice, not scope creep |
| `foundation-04` | `process_unix.go` / `process_windows.go` split via build tags | Unavoidable once real cross-platform liveness checking was needed |
| `foundation-09` | 9 pre-existing files touched only for lint fixes | First-time lint enablement retroactively surfaces issues in earlier code — see `Calibration_Report.md` §1 |
| `claude-provider-02` | `response_allow.golden.json`, `response_block.golden.json` | Required by the packet's "block/allow response golden files" test requirement, not named in the DAG's artifact list |
| `claude-provider-06` | `integrations/claude/README.md` | Needed to document the forward-looking stub status and the casing discrepancy, not silently pick a resolution |
| `checkpoint-b02` | Test files split three ways (`gitx_test.go`, `resolver_test.go`, `porcelain_test.go`) rather than one file | Design choice; DAG's file count likely assumed one undifferentiated test file |
| `predictor-02` | `doc.go` | Package-level privacy-boundary doc comment, small and natural to pair with the first file in a new directory |

## 4. Estimation failures

Covered exhaustively and quantitatively in `Prediction_Error_Report.md`
and `Calibration_Report.md`. Summarized here only as a pointer, per
Constitution §1 (single source of truth) — not duplicated: **files
changed and LOC were both systematically under-estimated** (82.4% and
100% of comparable nodes respectively exceeded their DAG estimate), and
**duration and token usage were never estimated at all**, for any node,
in the frozen DAG.

## 5. Integration problems

**None.** Zero merge conflicts occurred across all 4 Wave 2 branch merges
into the integration branch, and zero across all 4 Wave 1 branch merges
before that (verified via `git diff --name-only` cross-branch overlap
checks before each integration — see the Wave 1 Integration Report and
this conversation's Wave 2 verification steps). This is a direct,
measurable consequence of the Constitution's exclusive-path-ownership
rule being followed by every teammate in both waves — zero instances of
two teammates touching the same file were found in either wave.

## 6. Ownership issues

**One structural issue, resolved by explicit repository-owner ruling, not
by a teammate violating a boundary:** the Wave 1 kickoff deadlock, where
`go.mod` needed to exist before any teammate could be assigned a root
node, but `go.mod` is Foundation-owned and Foundation itself was blocked
on `contract-integrator-07`, and no named Wave 1 teammate was
`contract-integrator`. This was resolved by introducing "Bootstrap" as a
formally separate, lead-only, pre-Wave-1 stage — a process fix, not an
ownership violation. See `docs/adr/` context and this conversation's
transcript for the full resolution.

**Zero instances of a teammate editing another teammate's or the lead's
owned paths.** Every path-scope check performed during independent
verification (both waves, all 10 nodes checked individually via `git diff
--name-only` against each teammate's declared exclusive paths) came back
clean.

## 7. Positive patterns worth repeating (not just problems)

Not requested explicitly by the Phase 3.3 prompt, but recorded because
several lessons-learned entries flagged them as recommendations, and
omitting them would understate what worked:

- **Property-based / sweep testing for numerically well-behaved or
  high-risk-of-false-trigger code** (`predictor-04`'s 2000-trial quantile
  sweep, `predictor-06`'s ~300-combination runway sweep) — cheap to write,
  caught issues early, explicitly recommended by both nodes as a default
  pattern for similar future work.
- **A single monotonicity choke-point** (`predictor-05`'s `sortTriple`,
  applied once at the end) proved more robust than trying to keep every
  intermediate heuristic step individually monotonic.
- **Canary-string privacy tests** (reflection-walk every string field +
  whole-struct JSON marshal + `%+v` format check, searching for a planted
  literal) — used independently by `predictor-02`, `claude-provider-02`,
  and `claude-provider-04`'s test suites, and explicitly recommended as a
  reusable pattern rather than something to reinvent per role.
- **Lead re-verification of every "complete" claim** (independent
  `gofmt`/`build`/`vet`/`test` re-run, path-diff check, and reading at
  least one non-trivial claim's actual code) caught zero false-completion
  claims across 19 nodes, but the discipline is exactly what prevented the
  three session-interruption incidents (#3 above) from becoming silent
  data loss.
