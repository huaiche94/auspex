# ADR-0053 — Input/output split for the token forecast, input interval wider (#65 Phase 1)

> 🌐 English | [繁體中文](0053-token-forecast-input-output-split.zh-TW.md)

Status: Accepted
Date: 2026-07-17
Owner: lead-executed (PROPOSAL — frozen-contract change, owner sign-off at merge)
Tracking: issue #65 (Phase 1 of `docs/backlog/token-cost-prediction-research.md` §3.A); research arXiv:2604.22750 (Bai et al. 2026); sequenced after #11 (calibration), alongside #42

## Context

`domain.TokenForecast` (frozen by ADR-041) reported a single total-token
band — `TokensP50/P80/P90` — with no distinction between input and output
tokens. Bai et al. 2026 (measured on SWE-bench across eight frontier
models, recorded as external rationale in
`docs/backlog/token-cost-prediction-research.md`) finds two things about
that undifferentiated band:

1. Models predict their **input** tokens *worse* than their output tokens
   (Pearson ≤ 0.39, best case is output; input worse), and systematically
   underestimate. The input axis is genuinely the harder one to forecast.
2. Input is also the dominant cost driver (mean input/output ratio ~153).

The paper's numbers are **priors and direction**, never Auspex
coefficients (the backlog note's grounding discipline, §5). They justify
the *direction* — the input interval should be **wider** than the output
interval — but the *magnitude* of that widening is a fitted number gated
on #11 calibration data, and the ~153:1 ratio is external SWE-bench
evidence that is never imported.

Splitting the forecast touches the frozen `domain.TokenForecast` contract,
so it needs its own ADR (Constitution §3), following the precedent set by
ADR-044's amendment for the cohort rung and ADR-047 for the fallback
ladder.

## Decision

1. **Additive split fields on the frozen type.** `domain.TokenForecast`
   gains four pointer-typed fields —
   `InputTokensP50/P90`, `OutputTokensP50/P90` — an additive decomposition
   of the upcoming turn's tokens into two distinct intervals. The frozen
   total (`TokensP50/P80/P90`) is **unchanged and stays authoritative**;
   `nil` means the forecaster does not distinguish the axes (the pre-#65
   behavior — unknown is not zero). Only P50/P90 per axis (no P80): the
   decomposition is rendered and consumed as a P50–P90 range, matching the
   scope and duration bands (migrations 0041/0047); P80 stays on the total
   alone.

2. **Input interval structurally wider (direction only).**
   `RuleTokenForecaster` decomposes the total band so the **input interval
   is wider than the output interval** — the one asymmetry the paper's
   direction sanctions. The output interval carries the total band's own
   base relative spread; the input interval widens it by
   `inputIntervalWideningFactor`. Nothing is artificially narrowed — the
   harder axis widens, the more-predictable axis is left as-is.

3. **Two uncalibrated structural defaults, clearly labeled, gated on #11.**
   - `inputIntervalWideningFactor = 1.5` — how much wider the input band's
     P90 tail is than the output band's. A deliberately round, conservative
     placeholder expressing **only** the paper's direction (input is the
     harder axis). Not fitted; not derived from any of the paper's
     coefficients.
   - `defaultInputTokenShare = 0.5` — the neutral central partition. This
     slice deliberately does **not** bake in an input-vs-output magnitude
     ratio; that ratio is gated on #11 and the paper's ~153:1 is never
     imported. 0.5 is not a claim that input and output counts are equal —
     it is a refusal to invent the dominance magnitude here. Input P50 +
     output P50 = total P50, so the partition loses no central mass.

   Both are structural bootstrap constants in the exact sense
   `baseTurnTokens` and the cold-start "P90 = 2× P50" spread already are:
   documented placeholders, expected to be replaced by #11-fitted values.
   The split is uncalibrated, so it never flips `Calibrated` to true
   (Constitution principle #2: score is not probability).

4. **Persisted for read-back, not re-derived signal (migration 0063).**
   The forecast card is built by reading back the persisted `predictions`
   row (`forecastcard.go` — read-back, not recompute), so the split is
   persisted as four additive nullable columns
   (`token_input_p50/p90`, `token_output_p50/p90`) to surface on the card
   and `auspex evaluate`. Migration number `0063` was pre-assigned to this
   slice; it sits in the 0060–0069 band by allocation convenience, not
   semantic claim (the columns belong to predictor's `predictions` table).

## Honest scope

- **Direction, not magnitude.** The only fitted-looking number this slice
  introduces is the widening factor, and it is explicitly an uncalibrated
  structural default. No SWE-bench prior enters as a coefficient.
- **The central split is neutral, on purpose.** A 50/50 partition
  visibly under-states input dominance — everyone knows input tokens
  dominate agentic coding — but stating the dominance *magnitude* requires
  a fitted ratio this slice must not invent. #11 replaces the share with a
  data-fitted value; until then the card carries only the width asymmetry
  the evidence supports.
- **Not propagated to the research export this slice.** Unlike migration
  0062 (which copied the duration forecast into `calibration_samples`),
  the split is **not** added to the export. Today's split is a
  *deterministic structural transform* of the already-exported total, so
  exporting it adds no independent calibration signal and opens no
  unlabeled-history hole (#11 can reconstruct it from `token_p50/p90`). The
  export extension is deferred to the phase where a calibrated forecaster
  estimates the axes **independently** — capture-before-model (D-10/D-12).
- **A future calibrated forecaster estimates the axes independently.** The
  contract field is genuine axis room, not merely a render-time transform:
  it reserves the shape a #11-calibrated forecaster needs, exactly as
  ADR-041 reserved the pipeline shape before implementations existed.

## Consequences

- `CONTRACT_FREEZE.md` gains an Amendments entry for the additive
  `domain.TokenForecast` fields; every construction site keeps compiling
  (additive pointers default `nil` = "no split").
- The forecast card and `auspex evaluate` show distinct input/output
  ranges with the input range visibly wider, labeled uncalibrated.
- When #11 lands, both structural defaults become fitted per-cohort
  values, and the research export can carry the (then-independent) split.
- The statusline is **not** touched: #90 Phase A already demoted per-turn
  forecast fragments off the bar; the split stays on the card surfaces.

## Alternatives considered

- **Presenter-only derivation (no contract change).** Compute the split in
  the forecast card from the persisted total, leaving `domain.TokenForecast`
  untouched. Rejected: the slice's purpose is to establish the input/output
  axes *as a forecast contract*, so a future calibrated forecaster has a
  place to put independent estimates; a presenter-only view forecloses that.
- **Bake in an input-majority magnitude split** (e.g. 0.75, or the paper's
  ~153:1). Rejected: that is a fitted magnitude gated on #11 / an imported
  SWE-bench prior — exactly what the grounding discipline forbids.
- **Persist P50/P80/P90 per axis.** Rejected: the decomposition renders as
  a range; P80 per axis adds storage and surface for no decision it drives.
  P80 stays on the authoritative total, matching the scope/duration bands.
- **Extend the research export now.** Rejected as premature: the split is
  reconstructible from the exported total this phase, so exporting it is
  redundant until a calibrated forecaster makes the axes independent.
