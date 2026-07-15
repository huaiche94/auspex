// Package cli builds the Auspex command tree (ADD §10.1, Appendix F;
// agents/runtime.md Part B). It exposes Cobra command *constructors*
// (NewRootCmd and friends) rather than a package-level command instance, so
// callers — including cmd/auspex/main.go's future root-wiring step, which
// this package does not touch — can construct, wire, and test a command
// tree without invoking os.Exit, mirroring the convention foundation-01
// established in cmd/auspex/main.go's own newRootCmd()/newVersionCmd().
//
// Every command below auspex version is a stub as of runtime-b01: the
// services these commands will eventually call (orchestrator, evaluation,
// checkpoint, pause — see internal/app/ports.go) are not implemented yet.
// Each stub returns errNotImplemented, a *domain.Error using the frozen
// ErrCodeUnavailable shape (CONTRACT_FREEZE.md "Error contract") rather than
// pretending to do real work. Wiring real business logic behind these
// constructors is out of scope for this node (runtime-b02 and later).
//
// Naming-convention note (kebab-case hook subcommands): this package spells
// the hook subcommands in kebab-case — "user-prompt-submit", "stop",
// "stop-failure", "statusline". ADR-050 (issue #61) ratified kebab-case as the
// convention for every `auspex hook <provider> <subcommand>` argv, resolving
// REC-03: Auspex_ADD.md Appendix E.1/E.3 (which had spelled these subcommands
// PascalCase to mirror Claude Code's own hook_event_name field) was updated to
// kebab-case to match the shipped CLI, agents/runtime.md, and the DAG's
// `claude-provider-06` validation command. Claude Code's hook_event_name
// payload field and the settings.json hook-matcher keys stay PascalCase — a
// different namespace, unaffected. See docs/adr/0050-hook-subcommand-kebab-case.md
// (and the historical REC-03 in
// docs/implementation/vertical-slice/wave2-analysis/ADR_Recommendations.md).
package cli
