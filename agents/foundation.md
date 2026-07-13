# Foundation

## Model

A cheaper coding model is sufficient; use Fable only for migration/recovery review.

## ADD ownership

§10, core of §12, §26, bootstrap portions of §30, M0/M1.

## Mission

Create the buildable Go application foundation and the SQLite runtime used by every other role's package.

## Exclusive paths

```text
go.mod
go.sum
cmd/auspex/main.go
internal/buildinfo/**
internal/config/**
internal/paths/**
internal/storage/sqlite/db.go
internal/storage/sqlite/migrate.go
internal/storage/sqlite/migrations/0000-0009_*.sql
internal/clock/**
internal/idgen/**
internal/lock/**
Makefile
Taskfile.yml
.golangci.yml
LICENSE
NOTICE
```

Do not edit contract-integrator's files or another role's migration range.

## Deliverables

1. Go module and minimal `auspex version`.
2. OS-correct config/data/cache/runtime paths with injectable environment/home.
3. YAML config load and documented precedence for fields needed by the day-one flow.
4. SQLite open/migrate/transaction helpers.
5. WAL, busy timeout, foreign keys, and migration version checks per ADD.
6. Core tables required by foreign keys: repositories, worktrees, sessions, turns, tasks, provider installations/config metadata.
7. Migration test harness usable by every other role.
8. Basic repository initialization command support through a narrow app constructor; the runtime role owns user-facing commands.

## Required tests

- migration from empty database;
- reopen and idempotent migration;
- newer schema rejected safely;
- locked/busy behavior;
- invalid permissions and corrupt DB error classification;
- Windows/macOS/Linux path-table tests;
- config precedence and unknown field behavior;
- version command.

## Handoff

Document the DB constructor, transaction API, migration naming convention, and dependency requests in `docs/implementation/vertical-slice/foundation.md`.

## Out of scope

- telemetry feature tables beyond core session/turn references;
- HTTP daemon;
- complete release packaging;
- any predictor/provider/checkpoint business logic.
