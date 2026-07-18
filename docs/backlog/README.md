# docs/backlog/ — design notes for accepted-but-unscheduled work

> 🌐 English | [繁體中文](README.zh-TW.md)

Each file here is a design note for work the repository owner has
accepted but that is not (fully) scheduled into an active phase. A
backlog note is tied to a tracking GitHub issue, records the audit that
motivated it, and carries its own phased TODO — so scheduling later
never has to reconstruct the reasoning. Notes follow the same grounding
discipline as the wave2 analyses: capture and mechanics may be
designed up front, but numeric decisions (coefficients, thresholds)
wait for real data.

| File | What it covers |
|---|---|
| [`provider-model-effort-features.md`](provider-model-effort-features.md) | Making provider / model / effort prediction inputs (the pipeline was provider-, model-, and effort-blind per the 2026-07-13 audit). Tracking: issue #20, ordering in [`../DECISION_LOG.md`](../DECISION_LOG.md) D-10. Its §4 phases 0 (capture) and 1 (cohort filtering, [ADR-047](../adr/0047-token-cohort-fallback-ladder.md)) landed 2026-07-14; phase 2 (empirical calibration) is blocked on per-cohort data. |
| [`token-cost-prediction-research.md`](token-cost-prediction-research.md) | Research-grounded roadmap derived from arXiv:2604.22750 (Bai et al., 2026): a cache-aware four-class cost model, a repeated-file-operation risk factor, and phase-aware conditional forecasting. The paper's numbers enter as external priors/rationale (backing the uncalibrated, wide-range surface), never as fitted Auspex coefficients. Phase 0 (rationale capture in the predictor supplement + README) landed 2026-07-14; later phases each need a capture step first. |
| [`independent-verification.md`](independent-verification.md) | Independent adversarial verification — a *separate* skeptic agent that tries to **refute** a working agent's "done" (re-run checks, drive the real artifact, diff-vs-claim), since the Progress Tree §6 evidence model catches "no artifact" but not "artifact exists, the claim about it is false". Motivating audit: a 24 MP self-certification an independent on-device check refuted. **Draft/proposed**, tracking issue #98; Phase-0 capture first; ADR-gated for the outbound-LLM path and any §6 completion-evidence amendment. |

## Neighbors

- When a backlog phase graduates into a contract-level decision, it
  becomes an ADR in [`../adr/`](../adr/README.md) (as ADR-047 did).
- The formulas a note defers to are specified in
  [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md) §15 and the
  [predictor supplement](../design/Auspex_Predictor_Design_Supplement.md).
- Gap analyses that feed the backlog live in
  [`../implementation/vertical-slice/wave2-analysis/`](../implementation/vertical-slice/wave2-analysis/README.md).
