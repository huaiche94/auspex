# Backlog — Independent Adversarial Verification

> 🌐 English | 繁體中文 (pair `independent-verification.zh-TW.md` — TODO)

| Field | Value |
|---|---|
| Status | **Draft — proposed, not scheduled.** Authored by a delegated agent 2026-07-17; pending owner acceptance. No code; graduating any phase past 0 requires an ADR (§ "Why ADR-gated"). |
| Tracking | Issue [#98](https://github.com/huaiche94/auspex/issues/98) |
| Origin | Owner delegated an "overnight supervisor" exploration, 2026-07-17. The audit found ~5/6 of that idea already exists in `internal/*` or is scheduled (M4/M10/§6.10); this note captures the **one genuinely net-new** capability. |
| Related | `CONSTITUTION.md` §6 (Progress Tree: "completed ⇒ evidenced"); `Auspex_ADD.md` M4 (Progress Tree / State Checkpointing), M5 (predictor/policy), M10 (runway/graceful pause); `internal/managed/provider.go` (existing subprocess-`claude` pattern); `internal/{progress,statecheckpoint}/`. A future ADR for the outbound-LLM dependency/contract. |
| Grounding discipline | Mechanics only. **No thresholds or "catch-rate" numbers without data.** How often an independent check would overturn a working agent's "done" is an empirical quantity that waits for Phase-0 capture on real runs. |

## 1. Problem

The Progress Tree invariant (`CONSTITUTION.md` §6.2) is *"a node may not be marked
`completed` without durable, validator-checked artifact evidence — a real file, DB
record, checksum, or Git snapshot."* That defends against **"no artifact"** and
**"artifact changed unexpectedly"**. It does **not** defend against
**"the artifact exists, and the working agent's claim about what it means is
false."**

Concrete motivating audit (2026-07-17): during an overnight autonomous run on a
separate repo, the working agent reported a fix as done — *"24 MP full-resolution
stills; the photo output now reports 24 MP."* An **independent check of the actual
saved JPEG's pixel dimensions** refuted it: the delivered file was still 12 MP. The
artifact existed and passed a naive "did a photo get produced" check; the *claim
about it* was wrong. A validator that only confirms artifact presence/checksum
would have accepted a false completion. **The agent that did the work cannot be
trusted to certify the work** — self-certification is structurally biased.

## 2. Current state (audited 2026-07-17)

| Layer | Designed? | Present? | Catches a false claim? |
|---|---|---|---|
| Artifact-presence evidence | Yes — Constitution §6.2 | Yes — `internal/progress/`, `internal/statecheckpoint/` | No — presence/checksum ≠ semantic correctness |
| Deterministic predictor/policy | Yes — M5, ADR-041 | Yes — `internal/{predictor,evaluation,policy}/` | No — forecasts risk, does not audit a claim |
| Independent (LLM) re-verification of a claim | **No — absent from the design corpus** | **No** | — |
| Outbound LLM/model client | Intentionally **no** (heuristic/rule-based; AGENTS.md "no cloud/ML deps before their milestone") | Only `internal/managed/` shells out to the `claude` CLI for the *managed runner*, not for verification | — |

Evidence:

- `CONSTITUTION.md` §6.2 — evidence is "validator-checked artifact"; validators
  (`internal/statecheckpoint`, `internal/progress`) check existence/manifest/
  checksum, not the truth of the agent's semantic claim.
- No outbound model/HTTP client exists anywhere in `internal/*` except the daemon
  loopback API; the only subprocess-to-`claude` path is `internal/managed/
  provider.go` (spawns `claude -p … stream-json` for `auspex run`).

## 3. Proposed mechanics (design up front; not implemented)

An **independent verifier**: given a completion candidate (the node's *claim* +
its diff/artifacts + the originating task), a *separate* agent with its own context
attempts to **refute** the claim rather than confirm it —

- re-run the checks the working agent skipped (tests/build/lint on the exact diff);
- when the claim is behavioral, **drive the real artifact** (run the built binary /
  hit the endpoint / read the produced file's real properties — the 24 MP case);
- compare **diff-vs-claim** ("does the change do what the commit message says?").

It emits a verdict `{refuted, confirmed, inconclusive}` + the evidence it gathered,
persisted alongside the node. Independence is the whole point: the verifier is not
the working agent and does not inherit its belief.

Two integration strengths, sequenced:

1. **Advisory** (safe, no contract change): record the verdict; surface `refuted`/
   `inconclusive` as a policy signal / escalation. Does **not** block completion.
2. **Gating** (contract change): a node's `completed` requires artifact evidence
   **and** a non-`refuted` verdict. This **amends Constitution §6.2's definition of
   sufficient evidence** ⇒ ADR + Constitution amendment required.

## 4. Why ADR-gated

- **Outbound LLM path** — none exists by design. A verifier needs one (shell out to
  the `claude` CLI mirroring `internal/managed/provider.go`, or a new client
  package). Either changes a dependency/provider contract ⇒ ADR (§3) before code.
- **Completion-evidence contract** — the *gating* form (§3.2 above) changes the
  Progress Tree completion invariant, a Constitution §6 rule ⇒ ADR + amendment.
- **Milestone ordering** — this is later-milestone work; introducing it now would
  be build-ahead (§7.10). It should not precede the current wave.

## 5. Phased TODO

- **Phase 0 — capture (no verifier, data first).** Log, on real runs, each
  working-agent completion *claim* + a pointer to its artifacts/diff, so we can
  later measure how often an independent check would overturn a "done" and where.
  No LLM, no gating; pure telemetry. *(Follows the backlog discipline: capture
  before any numeric decision.)*
- **Phase 1 — ADR for the outbound-LLM dependency/contract + the verifier
  interface** (advisory shape only).
- **Phase 2 — advisory verifier**: verdict recorded + surfaced as a signal; never
  blocks. Fixture-backed contract tests before merge (per §5.4 provider-test rule,
  by analogy).
- **Phase 3 — gating** (optional, separate ADR + Constitution §6 amendment):
  completion requires a non-`refuted` verdict. Any confidence/threshold comes from
  **Phase-0 data**, not invented here.

## 6. Non-goals / boundaries

- Not a replacement for the project's own tests; only as strong as the checks/e2e
  it can run.
- Does **not** judge whether a solution is the *right design* — that stays a
  park-and-ask (Graceful Pause / escalation), never an autonomous LLM verdict.
- Uncalibrated verdict confidence is never labeled a probability (§7.7).

## Follow-ups when this is accepted

- Add the row to `docs/backlog/README.md` and create the `.zh-TW.md` pair
  (bilingual policy, ADR-049) — left to the owner/role per path ownership.
- Record the scheduling decision in `docs/DECISION_LOG.md`.
