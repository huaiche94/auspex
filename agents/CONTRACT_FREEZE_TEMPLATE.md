# Auspex Vertical-Slice Contract Freeze

Status: DRAFT — contract-integrator must replace every placeholder before other roles' branches rebase.
Contract commit: `<sha>`
Go module: `<module>`
Schema baseline: `<version>`

## Import paths

| Concern | Package |
|---|---|
| Domain entities | `<path>` |
| Cross-component ports | `<path>` |
| Event protocol | `<path>` |
| SQLite runtime | `<path>` |

## Schema-version strings

```text
auspex.event.v1
auspex.progress-tree.v1
auspex.state-checkpoint.v1
auspex.repository-checkpoint.v1
auspex.pause.v1
auspex.api.v1
```

## ID and idempotency rules

Document each entity ID, operation/event idempotency key, and replay behavior.

## Unknown/null semantics

Document unknown usage, quota, context, probability, provider capability, and reset timestamp handling.

## Transaction boundaries

Document `CompleteNode`, checkpoint creation, authorization consumption, pause persist, and wake lease transactions.

## Error contract

Document stable error codes and fail-open/fail-closed classification.

## Privacy contract

Raw prompts, transcripts, secrets, and repository artifacts policy.

## Migration ranges

- 0000–0009 foundation
- 0010–0019 claude-provider
- 0020–0029 checkpoint (Part A — progress/state)
- 0030–0039 checkpoint (Part B — repository)
- 0040–0049 predictor
- 0050–0059 runtime (Part A — pause/scheduler)

## Frozen state transitions

Insert turn, progress node, checkpoint, pause, wake job, and authorization state tables.
