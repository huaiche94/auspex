# Claude Provider

## Model

Use Fable for hook semantics review; routine parser/fixture work can use a cheaper model.

## ADD ownership

§11 provider normalization portions, all §22, Appendix E.2/E.3, Claude cases in §29.7.

## Exclusive paths

```text
internal/providers/claude/**
internal/hooks/claude/**
internal/telemetry/claude/**
integrations/claude/**
testdata/provider-events/claude/**
internal/storage/sqlite/migrations/0010-0019_*.sql
docs/providers/claude/**
docs/implementation/vertical-slice/claude-provider.md
```

## Mission

Implement fixture-backed Claude Code integration without scraping the TUI. Normalize status-line and lifecycle hook payloads into frozen Auspex events and provider-compatible hook responses.

## P0 deliverables

1. Parsers with unknown-field tolerance for:
   - status-line snapshot;
   - `UserPromptSubmit`;
   - `Stop`;
   - `StopFailure`;
   - optional `TaskCreated`, `TaskCompleted`, `PreCompact` when fixtures are available.
2. Normalize:
   - session/prompt identifiers;
   - five-hour usage percentage/reset timestamp when present;
   - context usage/window;
   - input/output/cache tokens when present;
   - cumulative cost/duration/LOC;
   - failure class including rate limit;
   - turn boundary and provider capability observations.
3. Idempotent telemetry persistence keyed by provider event identity or deterministic digest.
4. Provider-compatible allow/block response encoder for `UserPromptSubmit`.
5. Claude plugin/hooks example invoking `auspex hook claude ...`.
6. Fixtures for normal, missing/null, compacted, high-usage, duplicate, unknown-field, Stop, and rate-limit StopFailure payloads.

## Privacy

- Never persist raw prompt by default.
- Produce prompt SHA-256, byte length, and approximate token count only.
- Redact fixture secrets.
- Transcript path is metadata, not permission to read transcript.

## Tests

- table-driven parser tests;
- duplicate event idempotency;
- null quota/context behavior;
- unknown fields accepted;
- malformed payload produces typed error and valid hook fallback;
- block/allow response golden files;
- raw-prompt absence assertion across persisted rows/log output.

## Interface behavior

This role does not call the concrete predictor. It emits a normalized `EvaluateTurnRequest` or calls the contract-integrator's evaluation port. Use a fake in package tests.

## Stretch

Managed stream-json runner, signal interruption, and session resume adapter. Do not compromise the P0 hook path to complete these.
