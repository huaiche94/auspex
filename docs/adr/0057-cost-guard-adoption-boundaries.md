# ADR-0057 — Cost Guard: cost-governance adoption set, product boundaries, and hard-budget guarantee semantics (#140–#149)

> 🌐 English | [繁體中文](0057-cost-guard-adoption-boundaries.zh-TW.md)

Status: Accepted
Date: 2026-07-29
Owner: owner-directed (2026-07-29 instruction adopting the cost-governance roadmap analysis; this ADR records the decision set that instruction ratified)
Tracking: issues #140–#149; analysis artifact `.lavish/agent-cost-roadmap-analysis.html` (14 external sources); external evidence: [Token FinOps](https://cloudandsre.com/blog/token-finops-the-third-budget/), [The Harness Effect](https://arxiv.org/abs/2607.06906), [Wattage](https://github.com/faizannraza/wattage), [Stoke](https://github.com/Ozperium/stoke); prior substrate: ADR-043 (cost budget as policy resource), ADR-051/052 (capture + privacy interning), ADR-0055 (four-class empirical cost)

## Context

A 14-source review of the agent cost-governance landscape (6 community
discussions, 8 articles/implementations) converged on four mechanisms:
hard budgets, outcome economics, loop detection, and attribution — and
on one meta-finding: dashboards are too late; control must exist before
the next call, the next turn, or the next unsafe action.

Auspex's identity constrains HOW those mechanisms are adopted. Auspex
is a coding-agent **governance and continuity layer**: local-first,
capability-aware, with an evidence-gated Progress Tree (Constitution
§6) and a strict privacy posture (Constitution §7). It deliberately
does not sit between the agent and the provider as a proxy, does not
choose models, and does not modify prompts. The analysis's strongest
product sentence is adopted as this ADR's frame: **do not turn Auspex
into a proxy; turn cost into continuity policy.**

## Decision

### 1. Adopt (7) — the Cost Guard capability set

| # | Capability | Issue | First gate |
|---|---|---|---|
| 1 | Cost per evidenced Progress Tree node (outcome ledger: completed/failed/abandoned/manual-rescue) | #140 | report slice now; M13 outcome labels |
| 2 | Managed task/session budget envelope (spent/reserved/remaining; safe-point enforcement) | #141 | ADR'd here; M11 UX; M14 capability labels |
| 3 | Shadow mode for blocking policies (`would_action`, avoided spend, false-positive review) | #142 | none — first implementation step |
| 4 | Spin gate (repeat-rate + no-progress + verifier trend), shadow-first | #143 | M13 threshold fit; #68 |
| 5 | Cost regression baseline + quality-aware `report diff` | #144 | M13 benchmark; M15 CI |
| 6 | Subagent parent-child cost attribution | #145 | provider tail; #91 |
| 7 | Cache/context hygiene + right-sizing hints (descriptive only) | #146 | M13 cohort baselines; #66/#91 |

These extend existing telemetry, policy, Progress Tree, checkpoint, and
managed-runner substrate; none introduces a new external dependency.

### 2. Adapt (3) — Auspex-native rewrites, not ports

Priority reserve / budget borrowing arrives as **critical-path node
budgets** on the Progress Tree, not a generic swarm allocator (#147).
Tool-output/retry/fallback anomaly telemetry arrives **numbers-only**
under the ADR-052 privacy discipline, with its own ADR review before
schema (#148). Team cost rollup arrives as a **static merge of FR-170
de-identified exports** — no service, no upload (#149).

### 3. Reject from core (4) — the governor/harness-owner boundary

Universal LLM proxy, automatic model/effort routing, automatic prompt
compression/rewriting, and a payment credential firewall do **not**
enter the core product. Rationale: each moves Auspex from governor to
harness owner — once Auspex picks models, rewrites prompts, or meters a
proxy, it owns quality regressions and provider behavior it cannot
guarantee, and it breaks the capability-aware, provider-native posture.
Boundary form: Auspex may **observe, explain, and recommend** (e.g.
descriptive right-sizing hints, #146), and external tools (Turo,
SpendGuard, Stoke) may be integrated BESIDE Auspex; routing decisions,
prompt mutation, and payment control stay outside. Model/effort routing
specifically is capped at descriptive hints (#146) — never automatic
switching.

### 4. Hard-budget guarantee semantics (normative for every enforcement feature)

"Hard budget" MUST be labeled by what the runtime mode + provider
capability can actually guarantee; unknown stays unknown:

- **Managed + live usage** (e.g. codex App Server
  `thread/tokenUsage/updated`): strongest guarantee. Reservation before
  the next turn; near-envelope stops unsafe dispatch, waits for a safe
  point, checkpoints, interrupts (ADD §21.6 / PR #137 machinery), then
  pauses per policy. Mid-turn guard is possible.
- **Managed + end-only usage**: bounded overshoot. Guarantee = no new
  turn that cannot be reserved; one active turn may exceed its
  estimate. Surfaces MUST say "may exceed by up to one turn" and MUST
  NOT claim a token-exact hard cap.
- **Native hook**: advisory only. Pre-turn block/warn/checkpoint;
  reliable mid-turn pause is impossible (ADD §8.8); the provider
  account cap remains the last wall, and Auspex displays its own
  degraded capability.

Enforcement is always the existing frozen pause lifecycle (safe point →
checkpoints → interrupt → durable wake job) — never a mid-flight
process kill.

### 5. Shadow-first and calibration discipline

Every enforcement-grade policy action (BLOCK today; budget-envelope
pause and spin-gate stop when they arrive) ships **shadow-first**:
recorded `would_action` + persisted incident + estimated avoided spend,
enforcement off until its evaluation gate passes. Spin-gate thresholds
(repeat-rate, verifier slope, no-progress window) MUST be calibrated
from Auspex's own telemetry (M13) — external coefficients are
directional evidence only, never shipped numbers (the ADR-0053/ADD
§15.6 discipline applied to policy).

### 6. Accounting and privacy rules

- **Reservations are honest**: a reservation records its source,
  confidence, and band; the uncalibrated `HighUSD` bound is an
  estimate, never presented as a guarantee.
- **Canonical ownership**: parent session / subagent session / task /
  node / turn rollups define exactly one owner per unit of spend;
  aggregation must be double-count-safe by construction (#140/#145
  share this rule).
- **Numbers-only telemetry**: new cost-attribution signals persist
  bytes, counts, classes, and deltas only — no raw tool output, no
  additional path exposure beyond ADR-052's ordinal interning, no
  reversible content digests.
- **Starvation guard**: a budget pause surface must show remaining
  critical path, completion evidence, and the override tradeoff; the
  break-glass override is a single-use, audited, workspace-scoped
  authorization (the existing `decision allow` consumable pattern).

### 7. Roadmap mapping (direction, not ADD text)

M11: budget envelope + shadow/enforce switch + override UX. M13:
outcome economics, spin threshold fit, cost regression baseline,
cache/context cohorts. M14: provider enforcement-capability
negotiation (hard/advisory labels). M15: reservation crash recovery,
concurrency overshoot tests, audit bundle. Dynamic borrowing (#147)
and team governance (#149) wait for reliable outcome data. The ADD
remains the roadmap source of truth — milestone text amendments land
with their implementing slices, not with this ADR.

## Consequences

- Ten tracked issues (#140–#149) carry the work; implementation order
  follows the analysis: shadow mode → outcome ledger → budget envelope
  → data-gated remainder.
- The reject list is a standing product boundary: future proposals that
  amount to proxying, routing, prompt mutation, or payment control are
  answered by this ADR unless a successor supersedes it.
- Guarantee-semantics labels (§4) become a UI/docs obligation for every
  budget-related surface from the first envelope slice onward.
