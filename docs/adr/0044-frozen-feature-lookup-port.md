# ADR-044 — Freeze the repository/session feature-lookup port (REC-01)

Status: Accepted
Date: 2026-07-13
Owner: contract-integrator (lead-executed)
Approved by: repository owner, 2026-07-13 (issue #4 decision session)

## Context

Bootstrap deliberately deferred a repository/session/progress
feature-lookup port ("What Bootstrap did NOT freeze",
`CONTRACT_FREEZE.md`). Three packages consequently grew their own local
seams for the same capability:

- `internal/predictor/scope.FeatureSource` (predictor-05)
- `internal/predictor/token.FeatureSource` (predictor-05b)
- `internal/evaluation.DataSource` (predictor-09; superset of both, 11
  methods, implemented by `SQLDataSource` and wired into the real binary
  at the Final integration gate)

`wave2-analysis/ADR_Recommendations.md` REC-01 flagged this as the
top-ranked closeable gap: any future predictor tier or other role needing
the same data would either reinvent the interface or import another
package's internals — the coupling the narrow-ports discipline exists to
prevent. `Feature_Gap_Report.md` §1.1 independently ranked the
repository-features wiring it enables as the #1 critical gap.

## Decision

1. **Promote `evaluation.DataSource`'s shape verbatim into the frozen
   contract** as `app.FeatureDataSource` (+ `app.ResolvedSession`),
   in `internal/app/ports.go`. Verbatim promotion — rather than a
   redesign — because the shape has already survived real use: it is
   implemented by a production SQLite adapter, consumed by the full
   evaluation pipeline, and exercised by the E2E suite.
2. **`internal/evaluation` aliases the frozen types**
   (`type DataSource = app.FeatureDataSource`), keeping every consumer,
   implementation, and test compiling unchanged.
3. **The two predictor-side `FeatureSource` interfaces remain** as
   consumer-side narrow views (interface segregation): each declares only
   the subset it consumes, and production adapters back both with the
   same `app.FeatureDataSource` implementation. The freeze fixes the
   canonical shape, not the consumption pattern.
4. `internal/app` gains an import of `internal/features` (pure DTO
   package that imports only `domain` — dependency direction stays
   clean: `app → features → domain`).

## Consequences

- The feature-lookup shape is now a compatibility commitment per
  Constitution §3; changes require an ADR, not a package-local edit.
- Future consumers (Statistical/ML predictor tiers, the issue-#14
  forecast surface, the ADR-043 cost forecaster) depend on
  `app.FeatureDataSource` instead of reaching into `internal/evaluation`.
- `CONTRACT_FREEZE.md` gains an Amendments section recording this change;
  the Bootstrap deferral note is closed by reference rather than
  rewritten.

## Alternatives considered

- **Redesign the port before freezing** — rejected: no consumer has
  surfaced a need the current shape fails; redesigning without a driving
  requirement is speculative abstraction (README/ADD contribution rule).
- **Consolidate the predictor-side interfaces away entirely** — rejected:
  their narrowness is load-bearing for test fakes and honest
  dependencies; Go structural typing makes the views free.
