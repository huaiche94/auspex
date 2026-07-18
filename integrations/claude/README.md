# Claude Code plugin/hooks wiring

> 🌐 English | [繁體中文](README.zh-TW.md)

Status: **live.** The `auspex` binary ships all five
`auspex hook claude ...` subcommands (`user-prompt-submit`,
`post-tool-use`, `stop`, `stop-failure`, `statusline`), and this wiring
runs end-to-end in real
sessions — this repository's own Claude Code sessions use it daily
(issue #12 dogfooding). The files here are the reference configuration
to copy into your Claude Code setup. Hooks fail open: an Auspex-side
error never blocks or hangs your session; run `auspex evaluate`
directly to surface real errors.

Sessions self-register: the hooks idempotently create
repository/worktree/session rows on first contact (issue #17, decision
D-07 "lazy bootstrap"), so there is no required setup step beyond the
hook wiring itself. `auspex init` also exists for explicit registration
of the current repository.

(This file began as `claude-provider-06`'s forward-looking stub during
the vertical-slice build; the naming-discrepancy record below is kept
from that era because it is still unresolved.)

## Files

- `plugin.json` — Claude Code plugin manifest, verbatim from
  `docs/design/Auspex_ADD.md` Appendix E.2 (this role's documented ownership:
  "Appendix E.2/E.3").
- `hooks.json` — Claude Code hook + status-line configuration wiring
  `UserPromptSubmit`, `PostToolUse` (issue #67 slice 3a, ADR-052; the
  matcher limits invocations to the file-touching tools
  `Read|Edit|Write|MultiEdit|NotebookEdit`), `Stop`, `StopFailure`, and
  the status line to
  `auspex hook claude ...` subcommands, per `docs/design/Auspex_ADD.md` §22.3/
  §22.4/§22.5 and Appendix E.3's shape (`{"hooks": {"<HookEventName>":
  [{"hooks": [{"type": "command", "command": "..."}]}]}}`).

## CLI subcommand naming: kebab-case (resolved by ADR-050)

Two governing documents name the same subcommands with different casing:

- `docs/design/Auspex_ADD.md` Appendix E.3 (priority 2, `agents/claude-provider.md`'s
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
2. it is standard Go CLI subcommand convention (`cobra`/`urfave/cli`
   style) — and it is what the shipped CLI actually implements
   (`auspex hook claude --help` lists the kebab-case forms).

**Resolved by [ADR-050](../../docs/adr/0050-hook-subcommand-kebab-case.md)**
(issue #61): the `auspex hook <provider> <subcommand>` argv is kebab-case,
and `docs/design/Auspex_ADD.md` Appendix E.1/E.3 + §24.3 were updated to
match the shipped CLI. The Constitution §2 priority order (which favored the
ADD on paper) was reconciled through the ADR mechanism (Constitution §3)
toward the lower-churn, idiomatic answer, rather than by a silent pick.
Claude Code's own `hook_event_name` payload field and the settings.json
hook-matcher keys stay PascalCase — a different namespace, unaffected.

Claude Code's own wire-level `hook_event_name` field (inside the JSON
payload piped to stdin) is unaffected by this either way — it stays
PascalCase (`UserPromptSubmit`, `PostToolUse`, `Stop`, `StopFailure`) per
the provider's own convention and per every fixture under
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
observation failures, and this role's Phase-1 progress artifact assumptions
for `claude-provider-02`). The wrapper itself (the code that reads stdin,
calls these functions, and writes the wire response documented in
`internal/hooks/claude/userpromptsubmit.go`'s
`EncodeUserPromptSubmitResponse`) was delivered as `runtime-b01`/Part B's
CLI plumbing and ships in the binary today — only the primitives it
calls were this role's deliverable.

## Status line: `--emit-line` (issue #14; resolves issue #12 friction #2)

Claude Code's `statusLine` command is expected to PRINT the visible
status-bar line — but `auspex hook claude statusline` was originally
ingest-only (parse + normalize + persist, no stdout), so wiring it
directly blanked the user's status bar (recorded as friction #2 on issue
#12; the dogfooding install worked around it with a tee-wrapper script).
`hooks.json`'s `statusLine` entry now uses `--emit-line`, which keeps the
exact same ingest behavior AND prints one compact display line (v3
format per D-15, issue #41; the original #14 line carried a token
segment, withdrawn until forecasts respond to the prompt, #42):

```text
ax» <model> │ ◷ weekly ~<pct>% │ context [<bar>] <cur>% (p90 ≤<pct>%) │ ✓ RUN
```

using the latest persisted evaluation/forecast for the session when one
exists, else just `ax» <model>`. Without the flag the command remains
byte-identical to its previous ingest-only behavior (no stdout), for any
installation that still composes Auspex with its own status-line
command. Cost is an estimated range from `internal/pricing`'s default
table (ADR-043) — an uncalibrated estimate, never a measured cost.

## Installer behavior not modeled here

`docs/design/Auspex_ADD.md` §22.6 ("Compose existing status line")
describes installer behavior — reading any pre-existing status-line
command, saving it, and composing Auspex's wrapper output with it rather
than clobbering it. `auspex init` registers the current repository but
does not implement this compose/merge step. `hooks.json` here sets
`statusLine` directly as a static example and does not attempt to model
that compose/merge behavior, since a static example file cannot express
"read what was there before" — that is inherently install-time logic.
