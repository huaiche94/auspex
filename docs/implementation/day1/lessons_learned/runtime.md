# Lessons Learned — runtime (Wave 3: runtime-b01)

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_preflight |
|---|---|---|---|---|---|---|---|---|---|---|---|
| runtime-b01 | M | S/M — lighter than the M estimate suggested | 6 | 6 (doc.go, errors.go, errors_test.go, root.go, root_test.go, hook.go) | 350 LOC (DAG estimate) | one continuous pass, no wall-clock instrumentation in this environment | None — `internal/domain/errors.go`'s frozen `Error` shape and `internal/buildinfo.String()` were both already exactly sufficient; no new dependency or field was needed from a file this role doesn't own | None beyond the DAG's own 6-file estimate — `doc.go` carrying the naming-convention rationale (rather than a 7th standalone file) kept the count exactly at the estimate | None — the only real judgment call (kebab-case vs PascalCase hook subcommand casing, `ADR_Recommendations.md` REC-03) was pre-flagged by the task brief itself with an explicit answer to use, so it required documentation rather than open-ended research | An early draft of `root_test.go` added a defensive `var _ = cobra.Command{}` unused-import guard against a hypothetical future edit; caught immediately as unnecessary once the actual final import set was checked — cheap to avoid by writing the test table first and only importing what it needs, rather than importing defensively up front | Building every P0 command as a stub returning one shared `notImplemented(command string) error` helper, rather than N bespoke error constructions, made both the implementation and its test (`TestStubCommandsReturnNotImplemented`, table-driven over all 17 non-version leaf paths) nearly free — one assertion function, driven by the same path table used to assert tree registration. Recommend this "one typed-error constructor + one path table exercised by two different tests (tree-shape, stub-behavior)" pattern as the default whenever a future wave's node is a deliberate stub layer (e.g. if `checkpoint`/`predictor` ever need a similar honest-stub placeholder ahead of a dependency), rather than each stub command inventing its own ad hoc error value inline |

## Cross-node observations

- This was `runtime`'s first-ever node (no prior Wave 1/2 history to compare against, unlike
  `predictor`/`foundation`/`claude-provider`/`checkpoint`, all of which had earlier waves). The
  DAG's M/350-LOC/6-file estimate for `runtime-b01` proved accurate on file count and slightly
  generous on LOC (387 non-test / 561 total including tests) — consistent with the cross-role
  pattern already noted in `lessons_learned/predictor.md` that self-contained, dependency-light
  packages (no I/O, no concurrency, no cross-package wiring beyond already-frozen contracts) tend
  to land at or slightly under DAG estimates.
- The single most consequential non-code decision on this node was procedural, not technical:
  confirming *which* document was authoritative for hook-subcommand casing before writing any
  command `Use` strings, rather than picking one convention and discovering the conflict later via
  a failing review. Constitution §2's document priority order plus `ADR_Recommendations.md` REC-03
  together made this a five-minute lookup instead of a guess — worth noting as a positive case for
  keeping conflict history recorded in a findable place (`wave2-analysis/`) rather than only in
  chat/PR history that a fresh session (like this one, a first-ever assignment with no prior
  conversational context) cannot see.
- No blockers, unexpected dependencies, or scope surprises were severe enough to require raising a
  new ADR or deviating from the frozen contracts. `internal/domain/errors.go` and
  `internal/buildinfo` had everything needed without requesting an addition via this artifact.
