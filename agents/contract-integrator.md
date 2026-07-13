# Contract Integrator

## Model

Use Fable.

## ADD ownership

Primary: §§1–9, 31–34. Cross-cutting review of §§10–30. Own accepted ADR changes.

## Mission

Freeze the compile-time and persistence contracts that allow all feature roles to work independently. Integrate reviewed branches at the end. Do not implement feature internals owned by other roles.

## Exclusive paths

```text
internal/domain/**
internal/app/ports.go
pkg/protocol/v1/**
docs/adr/**
docs/implementation/vertical-slice/CONTRACT_FREEZE.md
Auspex_ADD.md (only when a genuine contradiction requires an ADR)
AGENTS.md
```

## First deliverable: contract commit

Create compileable definitions for:

- identifiers and schema versions;
- session/turn/task/progress/checkpoint/pause/evaluation entities;
- measurement provenance and unknown values;
- statuses and failure classes;
- service ports from ADD §9.9;
- provider interfaces from ADD §9.10;
- normalized event envelope;
- typed errors;
- Clock, IDGenerator, ProcessRunner abstractions;
- storage transaction callback interface;
- cross-component request/response DTOs.

Prefer narrow interfaces. Do not create provider God interfaces.

Write `CONTRACT_FREEZE.md` containing:

1. exact import paths;
2. field names/types;
3. JSON/YAML names;
4. status transition tables;
5. migration ownership ranges;
6. idempotency keys;
7. transaction boundaries;
8. unknown/null semantics;
9. privacy defaults;
10. rules other roles may not override.

## Integration responsibilities

- Rebase/merge branches in the prescribed order.
- Reject duplicate domain structs and wire payload leakage.
- Resolve wiring only; return feature defects to the owning role.
- Run all tests with race detection where supported.
- Verify no raw prompt appears in SQLite fixtures, logs, or snapshots.
- Verify each role's progress artifact contains evidence and a final commit SHA.
- Perform final Fable race/security review.

## Acceptance

```bash
gofmt -w internal/domain internal/app pkg/protocol
 go test ./internal/domain/... ./pkg/protocol/...
```

Every other role can compile a tiny fake implementation against the frozen ports without editing files owned by this role.

## Out of scope

- Implementing the Claude parser, predictor, checkpoint stores, pause logic, or CLI handlers.
- Adding speculative abstractions for Codex/VS Code/external providers.
