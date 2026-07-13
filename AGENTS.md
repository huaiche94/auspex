# Auspex contributor instructions

Auspex is a local-first predictive runtime guard for AI coding agents.

## Source of truth

Read `CONSTITUTION.md` first — it is the supreme process authority for this
repository (single-source-of-truth hierarchy, ADR rules, path ownership,
Progress Tree invariants). Read `Auspex_ADD.md` before architectural
work. Accepted ADRs under `docs/adr/` may amend it. If a role is assigned,
also read its file under `agents/`.

## Scope discipline

Implement one roadmap milestone at a time. Do not introduce cloud services,
opaque ML dependencies, or future-provider abstractions before their milestone.

## Required principles

- Go is the production runtime.
- TypeScript is isolated to the VS Code extension.
- Python is offline research only.
- Raw prompts and tool output are not persisted by default.
- Provider capability gaps are explicit.
- Do not parse undocumented transcripts in stable paths.
- Do not use shell command strings for Git/provider execution.
- Repository checkpoints must be atomic.
- Progress Tree is canonical task state.
- A completed node requires durable artifact/evidence and State Checkpointing.
- Long documents are persisted one section at a time.
- Uncalibrated risk scores are not probabilities.
- Graceful Pause is fully guaranteed only in managed mode.
- Auto-resume is opt-in and requires repository/quota/session verification.

## Before editing

1. Identify the current milestone.
2. Inspect existing code/tests.
3. State conflicts with the ADD.
4. Create a durable implementation progress artifact.
5. Produce a focused plan.

## During work

After every logical work unit:

1. write source/docs/tests to physical files;
2. run local validation;
3. update progress state and next action;
4. do not keep completed work only in conversation context.

## Before finishing

Run relevant commands:

- `gofmt`
- `go vet ./...`
- `go test ./...`
- `go build ./cmd/auspex`
- milestone-specific acceptance checks

Report tests not run.
