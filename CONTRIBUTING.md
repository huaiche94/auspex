# Contributing to Auspex

Thank you for your interest in contributing. Auspex is under active,
milestone-gated, multi-agent construction — please read this document
in full before proposing or implementing any change, and read the
documents it points to before your first PR.

## Read these first

In order:

1. [`CONSTITUTION.md`](CONSTITUTION.md) — supreme process authority for
   this repository: document precedence, ADR rules, path ownership, and
   the Progress Tree invariants every change must respect.
2. [`Auspex_ADD.md`](Auspex_ADD.md) — the single authoritative
   architecture and implementation specification. Code, issues, PRs, or
   comments that conflict with it are wrong; it is not.
3. [`AGENTS.md`](AGENTS.md) — contributor/agent quick-reference.

This is the same reading order `README.md`'s "Contributing" section
already names; this file expands on it with the concrete mechanics
below rather than replacing it.

## Ground rules

- **Work is milestone-gated** (`Auspex_ADD.md` §31). Do not implement
  ahead of the current milestone or add speculative abstractions for
  providers or features that are not yet in scope
  (`CONSTITUTION.md` §7 rule 10).
- **Every role/contributor owns a disjoint set of paths.** If the
  execution plan or an `agents/*.md` file assigns a path to a specific
  role, do not edit it from outside that role without going through the
  request process in `CONSTITUTION.md` §4.4 — this applies to human
  contributors collaborating with the in-repo agent roles just as much
  as it applies between roles.
- **Shared/cross-cutting files** — `Auspex_ADD.md`, `CONSTITUTION.md`,
  `AGENTS.md`, `internal/domain/**`, `internal/app/ports.go`,
  `pkg/protocol/v1/**`, `docs/adr/**` — are owned exclusively by the
  `contract-integrator` role. Do not send a PR that edits these
  directly; propose the change and let that role land it.
- **Architecture changes need an ADR first**, not after
  (`CONSTITUTION.md` §3) — this includes changes to the production
  runtime language, daemon transport, SQLite schema (backward-
  incompatibly), a provider integration contract, the checkpoint format,
  Graceful Pause/Auto-Resume semantics, a privacy default, public
  CLI/API/protocol compatibility, the OSS license, or a prediction
  output changing from score to probability.
- **"Completed" means evidenced**, not claimed. A change is not done
  because it compiles locally or because a description says so — it
  needs the durable evidence (tests, artifacts) `CONSTITUTION.md` §6
  describes.

## Development setup

Requires Go 1.26.x. From the repository root:

```bash
task fmt     # gofmt check (fails if any file is unformatted; does not rewrite)
task lint    # go vet + golangci-lint run ./...
task build   # builds ./bin/auspex
task test    # go test -race ./...
```

Equivalent `make` targets exist for contributors/CI steps without
`task` installed (`make fmt`, `make lint`, `make build`, `make test`) —
see the `Makefile`, which is kept as a deliberately thin mirror of
`Taskfile.yml`. `task` (the default target, `task` with no arguments)
runs `fmt` + `lint` + `test` together and is the closest local
equivalent to what CI checks on every PR (`.github/workflows/ci.yml`).

`Auspex_ADD.md` §30.2 additionally names `task bootstrap`,
`task test:race`, `task test:e2e`, `task vscode:test`,
`task research:test`, and `task verify` as the project's eventual full
local-task surface. Several of these do not exist in `Taskfile.yml` yet
because the trees they operate on (`vscode/`, `research/`,
end-to-end fixtures under `internal/integrationtest/`) are still being
built out — they will be added as those trees land, not invented ahead
of them.

## Making a change

1. Confirm the change fits the current milestone/wave (see
   `README.md`'s "Wave roadmap" and
   `docs/implementation/vertical-slice/EXECUTION_DAG.md`).
2. Confirm the files you need to touch are inside a path you're allowed
   to edit (see "Ground rules" above).
3. Write tests. Untested behavior is not considered complete per
   `CONSTITUTION.md` §6.
4. Run `task fmt && task lint && task test` locally before opening a PR
   — this is what CI runs, and CI is expected to be green
   (`.github/workflows/ci.yml`, Ubuntu/macOS/Windows matrix).
5. Open a PR with a description of *why*, not just *what* — reviewers
   need to check the change against `Auspex_ADD.md` and
   `CONSTITUTION.md`, not just against the diff.

## Sign off your commits (DCO)

Auspex requires the [Developer Certificate of
Origin](https://developercertificate.org/) on every commit — this
certifies you wrote the contribution or otherwise have the right to
submit it under the project's license. Sign off using:

```bash
git commit -s
```

This appends a `Signed-off-by: Your Name <your.email@example.com>` line
using your configured git identity. PRs with unsigned commits will be
asked to amend before merge.

**There is no separate Contributor License Agreement (CLA)** at this
stage — DCO sign-off is the only contribution-provenance requirement
(`Auspex_ADD.md` §30.7). This may change only via the ADR process in
`CONSTITUTION.md` §3, since changing the contribution-licensing model
is itself an architecture-adjacent decision.

## License

Auspex is licensed under Apache-2.0 (`README.md`'s "Tech stack"
table). By contributing, you agree your contribution is licensed under
the same terms.

## Security issues

Do not file security vulnerabilities as regular issues or PRs — see
[`SECURITY.md`](SECURITY.md) for the private disclosure process.

## Conduct

Participation in this project is governed by
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## Governance

For how maintainer decisions, ADR acceptance, and release authority
work, see [`GOVERNANCE.md`](GOVERNANCE.md).
