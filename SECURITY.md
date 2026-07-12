# Security Policy

Preflight is a local-first predictive runtime guard that sits in the
path of AI coding agent turns (Codex, Claude Code, and eventually
others). Its threat model matters more than a typical CLI tool's: it
observes hook payloads, quota/usage signals, and repository state, and
it makes pause/resume and checkpoint decisions on the user's behalf. We
take vulnerability reports seriously and ask that you report them
responsibly.

## Reporting a vulnerability

**Do not open a public GitHub issue for a security vulnerability.**

Report it privately via **GitHub Security Advisories** for this
repository ("Security" tab → "Report a vulnerability"). This creates a
private advisory visible only to maintainers and you, and lets us
coordinate a fix and disclosure timeline before the details become
public.

- **Acknowledgement target:** 3 business days.
- Please include: the affected version/commit, a reproduction (or a
  clear description if a live reproduction would itself be unsafe to
  share), and the impact you believe it has.
- If the report concerns a specific provider integration (e.g. a Claude
  Code hook payload issue), say so explicitly — provider-adjacent
  reports may need coordination with that provider's own disclosure
  process in addition to ours.

This process is normative per `Preflight_ADD.md` §30.8. If this file
and the ADD ever disagree, the ADD wins (see `CONSTITUTION.md` §1) and
this file is a bug to be fixed.

## Supported versions

Preflight is currently pre-1.0 (Day-1 vertical slice under active,
milestone-gated construction — see `README.md`'s wave roadmap). Until a
1.0 release, security fixes land on `main` only; there is no
long-term-support branch yet. Once 1.0 ships, this section will be
updated with a real support matrix per `Preflight_ADD.md` §30.6's
SemVer/stability guarantees.

## Scope

In scope:

- The Go runtime (`cmd/`, `internal/`, `pkg/protocol/v1/`) and its
  SQLite storage layer.
- Provider adapters (e.g. `internal/providers/claude`,
  `internal/hooks/claude`) and how they parse/normalize untrusted
  provider input.
- The VS Code companion extension, once it exists.
- CI/release supply-chain configuration (`.github/**`) once release
  workflows exist.

Out of scope:

- Vulnerabilities in upstream dependencies without a demonstrated
  Preflight-specific exploitation path (report those upstream instead;
  we do still want to know if a dependency vulnerability is reachable
  from Preflight's own attack surface).
- Social-engineering, physical-access, or denial-of-service reports
  against a single local machine the reporter already fully controls
  (Preflight is a local-first, single-machine tool per
  `Preflight_ADD.md` §1.4 — "attacker already has a shell on your
  machine" is not a useful threat model for this project, consistent
  with most local developer tooling).

## What we consider a security issue here

Preflight's design makes specific, testable security and privacy
promises (`agents/qa.md`'s "Security assertions", verified by the `qa`
role's own test suite as the project matures). A report against any of
the following is squarely in scope:

- Raw prompt text or tool output escaping its declared non-persistence
  boundary (persisted, logged, or transmitted when it should not be —
  `Preflight_ADD.md`'s "Unknown is not zero" / privacy-by-default
  principles, `CONTRACT_FREEZE.md`'s privacy contract).
- Bearer tokens, API keys, or other credentials appearing unredacted in
  logs, DB exports, checkpoint manifests, or support bundles.
- A loopback/local API that lacks authentication where the ADD requires
  it, or that is reachable from outside the local machine.
- Hook payloads processed without a size limit (resource-exhaustion via
  a malicious or malfunctioning provider hook).
- SQLite database files or checkpoint/artifact files created with
  overly permissive filesystem permissions on platforms that support
  restricting them.
- External command execution (git, provider CLIs) constructed via shell
  string interpolation instead of argv-array calls, creating a command
  or argument-injection risk.
- Repository Checkpoint/artifact extraction that can write outside its
  intended destination directory (path traversal / symlink escape).
- Auto-resume triggering without explicit, workspace-scoped, prior user
  consent, or resuming with escalated permissions relative to the
  original session.

## Privacy-sensitive changes

Per `Preflight_ADD.md` §30.9, any change to raw prompt retention,
outbound telemetry, the auto-resume default, state artifact content, or
remote checkpoint behavior requires a privacy review, an ADR (see
`CONSTITUTION.md` §3), and a changelog entry — not just a code review.
If you are proposing such a change (security-motivated or otherwise),
say so in the PR description so reviewers apply that bar.

## Coordinated disclosure

We will credit reporters (by name or handle, at the reporter's
preference) in the eventual public advisory unless asked not to. We aim
to publish an advisory promptly once a fix is released; if a report
turns out not to be a vulnerability, we will still respond explaining
why within the acknowledgement window above.
