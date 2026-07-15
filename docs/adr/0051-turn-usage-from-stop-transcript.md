# ADR-051 — Per-turn token usage captured from the Stop-hook transcript (numbers only)

> 🌐 English | [繁體中文](0051-turn-usage-from-stop-transcript.zh-TW.md)

Status: Accepted
Date: 2026-07-15
Owner: lead
Approved by: repository owner, 2026-07-15 (issue-#72 resolution directive)
Tracking: issue #72 (proposal item 4); unblocks the capture prerequisites of
#66/#65 and the token side of #11/#42

## Context

Native hook mode had **zero token-actual joins**: the Stop-hook payload carries
no usage fields and the statusline snapshot carries only a session-cumulative
`total_cost_usd` — so `predictions.token_p50/p90` could never be compared to a
per-turn actual (issue #72; the 2026-07-15 calibration-readiness report:
167 predictions, 0 joins). PR #73 landed the **cost**-delta rail as the interim
opening; exact tokens still had no hook-mode source, and the four cache classes
that #66's cost model needs were not captured anywhere.

What the provider does expose: the Stop-hook stdin includes `transcript_path`,
and the session transcript's `type=="assistant"` entries carry
`message.usage` — `input_tokens`, `output_tokens`, `cache_read_input_tokens`,
`cache_creation_input_tokens` — plus `message.model` and a `requestId`.
Verified against real Claude Code 2.1.x transcripts: one API call streams as
multiple JSONL lines sharing a `requestId` with byte-identical usage (so naive
summing multi-counts; extraction must dedupe), and subagent activity lives in
separate sidechain files, so the main transcript is exactly the main-loop turn
the prediction was made for.

## Decision

At Stop, auspex parses the completed turn's slice of the transcript and
enriches the `provider.turn.completed` event payload with **numbers only**:

- `input_tokens`, `output_tokens`, `cache_read_input_tokens`,
  `cache_creation_input_tokens`, `total_tokens` (= input + output, matching the
  frozen `managedUsageEvent` vocabulary — the raw cache classes ride alongside),
  `api_call_count` (unique `requestId`s), `model_id` (last non-synthetic).
- Turn slice = assistant entries after the last prompt boundary (a non-meta,
  non-sidechain user entry whose content carries no `tool_result` block),
  deduplicated by `requestId`; bounded read (32 MiB tail window, 8 MiB line cap).
- The calibration export joins these as `actual_*` fields on live rows
  (latest-wins between `provider.turn.completed` and a turn-stamped
  `provider.usage.observed`); `report.py token_coverage()` consumes them
  directly.

Invariants this ADR fixes:

1. **Numbers only.** No text content — prompt, response, tool output, or file
   content — is ever read into a persisted field. The Constitution §7 privacy
   default is unchanged.
2. **Fail-open and non-load-bearing.** Any absence, parse failure, oversized
   line, or out-of-window turn degrades to the pre-ADR-051 byte-identical
   event — never a hook failure, never fabricated zeros. Constitution §7 rule 4
   ("undocumented transcripts are never parsed on a stable path") is the
   recorded tension: the transcript is an undocumented provider artifact, so
   this enrichment is *strictly optional* — a provider format change silently
   disables it rather than breaking anything. That trade-off (optional
   enrichment permitted; stable-path dependency still forbidden) is the
   decision.
3. **Main chain only.** Subagent sidechains are excluded (separate files;
   defensive `isSidechain` filter pinned by test) — attribution matches the
   main-loop prediction being calibrated.
4. **Managed runs remain authoritative.** The split stands: managed mode =
   provider-reported usage; hook mode = transcript-derived usage.

## Consequences

- Hook-mode token joins become **exact going forward**; history cannot join
  (no retroactive capture). This unblocks: the empirical path of #42, token
  cohort calibration under #11, the cache-class capture prerequisite of #66,
  and makes #65's input/output split measurable.
- **No frozen contract changes.** The payload fields are additive JSON on an
  existing event; no schema migration; export fields are additive (same
  precedent as PR #73 / the #62 duration rail).
- ADD: §33 gains this ADR's mirror entry (same change); no other ADD section
  is affected.
- Accepted gap: `calibration_samples` has no token-actual columns, so archived
  rows lack the `actual_*` fields (live-row export carries them). A future
  additive migration in the retention range (0060–0069) may close this.
- `provider.turn.failed` / StopFailure turns are not yet enriched — trivially
  extendable later under the same invariants.
