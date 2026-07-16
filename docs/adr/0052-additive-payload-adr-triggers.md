# ADR-052 — What triggers an ADR on event payloads and export surfaces (resolves the ADR-051 / research-doc §7.6 tension) + approves the #67 capture step

> 🌐 English | [繁體中文](0052-additive-payload-adr-triggers.zh-TW.md)

Status: Accepted
Date: 2026-07-16
Owner: lead
Approved by: repository owner, 2026-07-16 (ruling "A" in the contract-tension
decision session)
Tracking: issue #67 (unblocks slice 3a); clarifies ADR-051; interprets
Constitution §3

## Context

Two same-day authorities disagreed on whether **additive payload fields on an
existing event type** are a frozen-contract change requiring an ADR:

- **ADR-051** (Accepted 2026-07-15) added per-turn usage fields to
  `provider.turn.completed` and stated in its Consequences: "**No frozen
  contract changes.** The payload fields are additive JSON on an existing
  event."
- **`docs/backlog/token-cost-prediction-research.md` §7.6** (merged the same
  day, PR #71) lists "new `provider.turn.completed` payload fields" among
  frozen-contract surfaces: "each is a frozen-contract surface, so
  implementation lands with its own ADR (Constitution §3)."

Practice was also split: the cost rail (PR #73) and duration rail (0062 +
PR #80's report side) landed additive fields **without** ADRs, citing the
additive-no-ADR precedent; the model/effort capture (ADR-047) and transcript
usage capture (ADR-051) landed **with** ADRs.

The frozen envelope itself (`pkg/protocol/v1/event.go`, CONTRACT_FREEZE.md)
enumerates envelope fields and closes the EventType taxonomy, but types
`Payload` as `map[string]any` — the key set is deliberately open, and every
consumer must tolerate unknown fields (§21.7; the `unknown_fields` fixtures).

## Decision

The ADR trigger is **not** the act of adding a payload field. It is one of
four substantive events:

1. **A new data source is parsed.** Reading a surface auspex did not read
   before (e.g. ADR-051's transcript; a provider's rollout/state files)
   requires an ADR — new sources carry privacy and stability commitments.
2. **A semantic freeze or divergence.** A key whose meaning departs from or
   newly pins a frozen vocabulary (e.g. `total_tokens` = fresh input + output,
   NOT the cached-inclusive sum) requires the decision to be recorded.
3. **A versioned export surface is extended.** Adding fields to a reviewed,
   schema-versioned surface (`auspex.observations-export.v1` whitelist,
   calibration-export shape, daemon API schemas) requires an ADR — those
   key sets ARE contract, per their own "an unreviewed type must stay out"
   rule.
4. **Any non-additive change** — rename, removal, type change, or semantic
   repurposing of an existing key — under the original Constitution §3 rules.

Conversely, an **additive, numbers/ids-only, fail-open** payload key on an
existing event type, whose semantics are documented in code and CHANGELOG,
and which is **not yet consumed by any versioned export surface**, does not
require an ADR.

This ruling:
- **ratifies** the no-ADR precedents (PR #73 cost fields, the duration rail);
- **explains ADR-051**: it was required — for trigger 1 (new source:
  transcript) and trigger 2 (`total_tokens` semantics) — not for the additive
  fields per se; its "no frozen contract changes" sentence remains true in
  the envelope sense and is superseded in interpretation by this ADR;
- **upholds §7.6 for #67**: the capture step trips trigger 3 (observations
  whitelist extension); its hook subcommand is CLI surface already governed
  by ADR-050.

### Approval of the #67 capture step (slice 3a)

Under the ruling above, this ADR approves the three §7.6 contract touches:

1. New hook subcommand `auspex hook claude post-tool-use` (kebab-case per
   ADR-050; stub-then-swap wiring like every other leaf).
2. New additive `provider.turn.completed` payload fields — the five per-turn
   aggregates of §7.3: `distinct_files_touched`, `total_file_ops`,
   `repeated_ops`, `repeat_rate` (nil when `total_file_ops` = 0),
   `max_ops_on_one_file`.
3. Extension of the `auspex.observations-export.v1` whitelist with those five
   fields.

Privacy invariant (§7.3/§7.8, restated as binding): raw file paths are
**never persisted in any form — hashes included**. Paths are interned to
opaque per-turn ordinals in process memory for counting and discarded; only
the five aggregate counts leave the process. Tool classification: view =
`Read`; modify = `Edit`, `Write`, `MultiEdit`, `NotebookEdit`. Aggregation
(not per-tool-call events) is deliberate: a high-frequency event type would
fight ADR-046 retention; the existing `provider.tool.*` EventTypes remain
unused by this design.

## Consequences

- Constitution §3's text is unchanged; this ADR records its authoritative
  interpretation for payload/export surfaces. Future additive captures follow
  the four-trigger test instead of relitigating.
- ADD: §33 gains this ADR's mirror entry (same change). No other ADD section
  changes; research-doc §7 remains the implementation spec for #67.
- #67 slice 3a is unblocked and lands with this ADR in the same change.
  Slice 3b (RiskCombiner input, reason code, thresholds) stays deferred until
  captured data supports threshold selection; #68 stays gated on 3b.
- The Codex PostToolUse hook and managed-stream tool events remain out of
  scope (§7.2), to be proposed separately when wanted.
