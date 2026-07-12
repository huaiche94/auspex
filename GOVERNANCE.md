# Governance

This document describes how decisions get made in the Preflight
project: maintainer structure, how architecture decisions are accepted,
release authority, and how this will evolve as the project grows. It is
grounded in `Preflight_ADD.md` §30.7 ("Governance"); where this file and
the ADD disagree, the ADD wins (`CONSTITUTION.md` §1) and this file is a
bug to be fixed.

`GOVERNANCE.md` governs *decision-making authority over the project*.
`CONSTITUTION.md` governs *day-to-day process discipline while building
it* (path ownership, ADR mechanics, Progress Tree invariants). The two
are complementary, not competing — see `CONSTITUTION.md` §8 for the
general pattern this document follows at the project-governance level.

## Current stage: Initial

Preflight is currently in the **Initial** governance stage
(`Preflight_ADD.md` §30.7):

- A single lead maintainer holds final decision authority.
- Architecture and process decisions go through a public ADR/issue
  process — proposals are visible, discussable, and recorded, even
  though final acceptance currently rests with one person.
- Any contributor may propose an ADR (`CONSTITUTION.md` §3.2); only the
  `contract-integrator` role/architecture lead accepts one.

This reflects where the project actually is today (early, single-lead,
pre-1.0, Day-1 vertical slice under construction — see `README.md`'s
wave roadmap), not an aspiration.

## Mature stage (future)

Preflight will move to a **Mature** governance stage once the project
and contributor base justify it. The bar for that transition, per
`Preflight_ADD.md` §30.7:

- **3 or more active maintainers.**
- **Sensitive changes require 2 approvals** — specifically security-
  and provider-integration-affecting changes, given Preflight's
  position in the AI coding agent's execution path.
- **Documented release authority** — who may cut and publish a release,
  distinct from who may approve a PR.
- **DCO sign-off** remains required (see `CONTRIBUTING.md`).
- **No CLA** — this holds at both stages; a Contributor License
  Agreement is not part of Preflight's contribution model, initially or
  at maturity, per `Preflight_ADD.md` §30.7's explicit "no CLA
  initially" (read here as: not planned as a later addition either,
  absent a documented decision to the contrary).

The transition from Initial to Mature is itself a governance decision
and will be recorded (an ADR or an explicit amendment to this file),
not treated as an informal, undocumented shift.

## Architecture Decision Records (ADRs)

Preflight's architecture evolves through ADRs, not ad hoc
reinterpretation of `Preflight_ADD.md`. Full mechanics are normative in
`CONSTITUTION.md` §3; summarized:

- ADRs live at `docs/adr/NNNN-title.md`, numbered sequentially.
- Any role or contributor may propose one.
- Only the `contract-integrator` role (architecture lead) accepts an
  ADR.
- An accepted ADR is immutable history; changing a decision means
  writing a new ADR that supersedes the old one, never editing an
  accepted ADR's decision in place.
- `Preflight_ADD.md` itself may only be edited by `contract-integrator`,
  and only when a genuine contradiction requires it, with the
  corresponding ADR landing in the same change.

An ADR is **required**, not optional, before changing anything on the
list in `CONSTITUTION.md` §3 — this includes the production runtime
language, daemon transport, SQLite schema in a backward-incompatible
way, a provider integration contract, the checkpoint format, a privacy
default, public CLI/API/protocol compatibility, the OSS license, or a
prediction output changing from score to probability.

## Privacy-sensitive changes

Per `Preflight_ADD.md` §30.9, changes to any of the following require a
privacy review **and** an ADR **and** a changelog entry — this is a
stricter bar than an ordinary ADR-requiring change, layered on top of
it, not a substitute for it:

- raw prompt retention behavior;
- outbound telemetry;
- the auto-resume default;
- state artifact content;
- remote checkpoint behavior.

## Path ownership during Day-1 construction

While the Day-1 vertical slice is under construction by multiple
parallel agent roles, `CONSTITUTION.md` §4 governs who may modify what:
every role owns a disjoint set of paths declared in its `agents/*.md`
file; shared/cross-cutting files are owned exclusively by
`contract-integrator`; no role may expand its own ownership. This is
day-to-day execution discipline, not a substitute for the maintainer/ADR
governance described above — it exists because this phase of the
project is built by several agent sessions in parallel and needs a
stronger, more mechanical form of "who decides" than a single human
team would otherwise need.

## Release authority

Release process and versioning guarantees are specified in
`Preflight_ADD.md` §30.4–§30.6 (release targets, distribution channels,
SemVer). Formal, named release authority (who may tag and publish a
release) will be documented here once the project reaches a release
pipeline (`.github/workflows/release.yml`, not yet built) — until then,
the lead maintainer holds this authority as part of the Initial-stage
structure above.

## Security governance

See [`SECURITY.md`](SECURITY.md) for the vulnerability disclosure
process (private GitHub Security Advisory, 3-business-day acknowledgement
target, per `Preflight_ADD.md` §30.8).

## Code of Conduct

See [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) for community standards
and enforcement.
