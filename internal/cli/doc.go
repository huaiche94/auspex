// Package cli builds the Preflight command tree (ADD §10.1, Appendix F;
// agents/runtime.md Part B). It exposes Cobra command *constructors*
// (NewRootCmd and friends) rather than a package-level command instance, so
// callers — including cmd/preflight/main.go's future root-wiring step, which
// this package does not touch — can construct, wire, and test a command
// tree without invoking os.Exit, mirroring the convention foundation-01
// established in cmd/preflight/main.go's own newRootCmd()/newVersionCmd().
//
// Every command below preflight version is a stub as of runtime-b01: the
// services these commands will eventually call (orchestrator, evaluation,
// checkpoint, pause — see internal/app/ports.go) are not implemented yet.
// Each stub returns errNotImplemented, a *domain.Error using the frozen
// ErrCodeUnavailable shape (CONTRACT_FREEZE.md "Error contract") rather than
// pretending to do real work. Wiring real business logic behind these
// constructors is out of scope for this node (runtime-b02 and later).
//
// Naming-convention note (kebab-case hook subcommands): Preflight_ADD.md
// Appendix E.3 spells Claude Code hook subcommands in PascalCase (e.g.
// "UserPromptSubmit"), matching Claude's own hook-event-name casing. Three
// other frozen documents — agents/runtime.md's own P0 command list, this
// node's DAG validation command, and the Day-1 execution plan's demo
// script — independently use kebab-case ("user-prompt-submit"). This
// discrepancy is tracked but not yet resolved by an ADR; see
// docs/implementation/day1/wave2-analysis/ADR_Recommendations.md REC-03.
// This package follows kebab-case, matching agents/runtime.md verbatim (the
// document that is authoritative for this role's command surface per
// CONSTITUTION.md §2 priority order) and the DAG's own `preflight --help`
// validation expectation. This is a documented judgment call, not a silent
// third answer — see docs/implementation/day1/runtime.md.
package cli
