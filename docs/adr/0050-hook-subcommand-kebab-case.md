# ADR-050 — Hook subcommand argv is kebab-case (ratifies the shipped CLI over ADD Appendix E.3's PascalCase)

> 🌐 English | [繁體中文](0050-hook-subcommand-kebab-case.zh-TW.md)

Status: Accepted
Date: 2026-07-15
Owner: lead
Approved by: repository owner, 2026-07-15 (issue #61, recommending Option A)
Tracking: issue #61; self-documented as REC-03 in `internal/cli/doc.go` and
`docs/implementation/vertical-slice/wave2-analysis/ADR_Recommendations.md`

## Context

Two governing documents named the same `auspex hook <provider> <subcommand>`
CLI invocation with different casing, and the discrepancy was tracked (REC-03)
but never resolved by an ADR:

- **`docs/design/Auspex_ADD.md` Appendix E.3** (and E.1 for Codex, plus the
  §24.3 examples) spelled the subcommand argv in **PascalCase** —
  `auspex hook claude UserPromptSubmit` — matching Claude Code's own
  wire-level `hook_event_name` field.
- **`agents/runtime.md`**'s P0 command list, **`EXECUTION_DAG.md`**'s
  `claude-provider-06` validation command, and the vertical-slice demo script
  spelled it in **kebab-case** — `auspex hook claude user-prompt-submit`.

What shipped: the CLI implements **kebab-case**. `auspex hook claude --help`
lists `user-prompt-submit`, `stop`, `stop-failure`, `statusline`
(`internal/cli/hook.go`). So the code and Appendix E.3 disagreed;
`integrations/claude/README.md` already followed the shipped kebab-case while
flagging the conflict.

Per Constitution §2/§3 a frozen-document discrepancy needs a decision, not a
silent pick. Constitution §2's priority order places the ADD (priority 2)
above `agents/*.md`, so on paper the PascalCase ADD "wins" — which is exactly
why the shipped kebab-case needs an ADR to become legitimate rather than a
silent override of a higher-priority document.

**Scope clarification — two different casings live here; only one is in
question.** Claude Code's (and Codex's) own hook-event names — both the
`hook_event_name`/`hookEventName` payload field AND the settings.json
hook-matcher keys (`"UserPromptSubmit": [ … ]` in the Appendix E.1/E.3
templates) — are the **provider's** wire format. They are PascalCase
regardless, are unaffected by this decision, and MUST stay PascalCase or the
templates stop matching. Only the **auspex CLI's own argv**
(`auspex hook <provider> <subcommand>`) is decided here; it never shares a
token position with the provider event name, so the two namespaces cannot
collide.

## Decision

The `auspex hook <provider> <subcommand>` CLI argv is **kebab-case**,
provider-agnostic (`claude` and `codex` alike): `user-prompt-submit`, `stop`,
`stop-failure`, `statusline` (the four shipped today), and any future
subcommand derived from a provider event name by lowercasing and hyphenating
its word boundaries (e.g. `PostToolUseFailure` → `post-tool-use-failure`).

Rationale:

1. **Idiomatic Cobra / Unix CLI.** Every other subcommand in the binary is
   lowercase/kebab (`daemon start`, `decision allow`, `telemetry export`). A
   single PascalCase subcommand family would be the lone exception a user has
   to remember.
2. **Least churn, ratifies reality.** The CLI, `agents/runtime.md`, the DAG
   validation command, the demo script, and `integrations/claude/README.md`
   already use kebab-case. Only Appendix E.1/E.3 and two §24.3 examples were
   the outliers.
3. **No collision with the provider wire format.** The provider's PascalCase
   event names live in a different namespace (settings.json matcher keys and
   the `hook_event_name` payload), so kebab-case argv introduces no ambiguity.

**Action:** `docs/design/Auspex_ADD.md` is updated to kebab-case for every
`auspex hook <provider> <subcommand>` argv in Appendix E.1 (Codex), Appendix
E.3 (Claude), and the §24.3 "Internal hooks" examples. The settings.json
hook-matcher KEYS in those same templates (`"SessionStart"`,
`"UserPromptSubmit"`, …) are left PascalCase — they are the provider's event
names, not auspex argv. This is the priority-2 document being corrected to
match the shipped contract: the ADR is the authorized mechanism (Constitution
§3) for the lower-churn, more idiomatic answer to win over the on-paper
priority order.

## Consequences

- Appendix E.1/E.3 hook-install templates now paste-and-run against the
  shipped CLI (previously each `command` argv named an unknown subcommand).
- `internal/cli/doc.go`'s REC-03 note changes from "tracked but not yet
  resolved by an ADR" to "resolved by ADR-050." No code changes — the CLI
  already shipped kebab-case; this ADR ratifies it.
- The convention binds future subcommands: a new provider event surfaced as an
  `auspex hook` subcommand is spelled kebab-case, even though its
  `hook_event_name` payload value stays PascalCase.
- `ADR_Recommendations.md` REC-03 (a frozen wave2-analysis record, ADR-045/
  ADR-049) is **not** edited; this ADR is its resolution, cross-referenced from
  `internal/cli/doc.go`.
- No frozen contract changes: hook subcommand argv is a CLI surface, not a
  frozen `internal/app/ports.go` port or a `pkg/protocol` schema.
