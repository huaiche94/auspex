# Claude Code plugin/hooks example (claude-provider-06)

Status: **forward-looking stub.** The `auspex` CLI binary and its `hook
claude ...` subcommands do not exist yet on this branch — that is
`runtime-b01`'s deliverable (Part B, `agents/runtime.md`), not this role's.
Per `docs/implementation/vertical-slice/EXECUTION_DAG.md`'s own note on
`claude-provider-06` ("Needs `runtime-b01` CLI skeleton for true
end-to-end (stub acceptable before then)"), the files here are example
configuration only: syntactically valid, internally consistent with this
role's Wave-1/Wave-2 deliverables, but not yet exercisable end-to-end
against a real `auspex` binary.

## Files

- `plugin.json` — Claude Code plugin manifest, verbatim from
  `Auspex_ADD.md` Appendix E.2 (this role's documented ownership:
  "Appendix E.2/E.3").
- `hooks.json` — Claude Code hook + status-line configuration wiring
  `UserPromptSubmit`, `Stop`, `StopFailure`, and the status line to
  `auspex hook claude ...` subcommands, per `Auspex_ADD.md` §22.3/
  §22.4/§22.5 and Appendix E.3's shape (`{"hooks": {"<HookEventName>":
  [{"hooks": [{"type": "command", "command": "..."}]}]}}`).

## CLI subcommand naming: a documented discrepancy

Two governing documents name the same subcommands with different casing:

- `Auspex_ADD.md` Appendix E.3 (priority 2, `agents/claude-provider.md`'s
  own documented ownership) writes `auspex hook claude UserPromptSubmit`
  — PascalCase, matching Claude Code's own wire-level `hook_event_name`
  field exactly.
- `agents/runtime.md`'s P0 commands list and
  `docs/implementation/vertical-slice/EXECUTION_DAG.md`'s own validation command for
  this node (`claude-provider-06`) both write
  `auspex hook claude user-prompt-submit` — kebab-case.

This file follows the **DAG's validation command and `agents/runtime.md`**
(kebab-case: `user-prompt-submit`, `stop`, `stop-failure`, `statusline`)
because:

1. it is the literal, currently-frozen validation command for this exact
   node, and
2. it is standard Go CLI subcommand convention (`cobra`/`urfave/cli` style),
   which is what `runtime-b01` (CLI skeleton, not yet built) will most
   likely use.

This is a **judgment call, not a resolution** of the Constitution §2
document-priority ordering, which would favor the ADD (priority 2) over
`agents/runtime.md` (priority 4) if the two are read as being in real
conflict. Flagged here, and in this role's progress artifact, for
`contract-integrator` to reconcile — e.g. by updating
`Auspex_ADD.md` Appendix E.3 to match the kebab-case CLI convention, or
by updating `agents/runtime.md`/the DAG to match the ADD's PascalCase. This
role does not have authority to edit either document (`Auspex_ADD.md`
and `agents/runtime.md` are both outside `claude-provider`'s exclusive
paths) and has not silently picked one without recording the conflict.

Claude Code's own wire-level `hook_event_name` field (inside the JSON
payload piped to stdin) is unaffected by this either way — it stays
PascalCase (`UserPromptSubmit`, `Stop`, `StopFailure`) per the provider's
own convention and per every fixture under
`testdata/provider-events/claude/**`; only the `auspex` CLI's own
argv subcommand spelling is in question.

## Internal consistency with claude-provider-02

The `UserPromptSubmit` entry's `timeout: 5` (seconds) assumes the hook
wrapper calls `internal/hooks/claude.ParseUserPromptSubmit`,
`internal/telemetry/claude.NormalizeUserPromptSubmit` (claude-provider-04),
and (once wired) an evaluation port, then falls back to
`internal/hooks/claude.FallbackAllowResponse()` on any internal error —
never leaving Claude Code's `UserPromptSubmit` hook hanging or blocking a
user's prompt on a Auspex-side bug (fail-open, per
`CONTRACT_FREEZE.md`'s fail-open/fail-closed split for operational
observation failures, and this role's Wave-1 progress artifact assumptions
for `claude-provider-02`). The wrapper itself (the code that reads stdin,
calls these functions, and writes the wire response documented in
`internal/hooks/claude/userpromptsubmit.go`'s
`EncodeUserPromptSubmitResponse`) is `runtime-b01`/Part B's CLI plumbing,
not this role's — only the primitives it will call are this role's
deliverable.

## Status line: `--emit-line` (issue #14; resolves issue #12 friction #2)

Claude Code's `statusLine` command is expected to PRINT the visible
status-bar line — but `auspex hook claude statusline` was originally
ingest-only (parse + normalize + persist, no stdout), so wiring it
directly blanked the user's status bar (recorded as friction #2 on issue
#12; the dogfooding install worked around it with a tee-wrapper script).
`hooks.json`'s `statusLine` entry now uses `--emit-line`, which keeps the
exact same ingest behavior AND prints one compact display line:

```text
pf✈ <model> | est P50 <tokens>tok ~$<low>–<high> | <policy action>
```

using the latest persisted evaluation/forecast for the session when one
exists, else just `pf✈ <model>`. Without the flag the command remains
byte-identical to its previous ingest-only behavior (no stdout), for any
installation that still composes Auspex with its own status-line
command. Cost is an estimated range from `internal/pricing`'s default
table (ADR-043) — an uncalibrated estimate, never a measured cost.

## Installer behavior not modeled here

`Auspex_ADD.md` §22.6 ("Compose existing status line") describes
installer behavior — reading any pre-existing status-line command, saving
it, and composing Auspex's wrapper output with it rather than clobbering
it — that a real installer (not yet built; likely `runtime-b01` or a future
CLI `auspex init` path) must implement. `hooks.json` here sets
`statusLine` directly as a static example and does not attempt to model
that compose/merge behavior, since a static example file cannot express
"read what was there before" — that is inherently install-time logic.
