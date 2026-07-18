# docs/adr/ — accepted Architecture Decision Records

> 🌐 English | [繁體中文](README.zh-TW.md)

One file per accepted architecture decision, named `NNNN-title.md`. An
accepted ADR is **immutable history** (Constitution §3.3): to change a
decision, write a new ADR that supersedes the old one — never edit an
accepted ADR in place. Only the `contract-integrator` role accepts ADRs
and edits this directory (Constitution §4.3); any role may propose one.

This directory starts at 0041. Decisions 001–040 predate it: they were
recorded as summary entries in [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md)
§33 ("Architecture Decision Records") and still live there — e.g.
ADR-001 (product name, since superseded by ADR-045) through ADR-040
(OS wake out of scope). §33 also carries short mirror entries for later
ADRs whose full text is here.

| ADR | Decision |
|---|---|
| [`0041`](0041-predictor-forecast-layer.md) | Predictor pipeline gets an explicit Forecast layer: `TokenForecast`/`QuotaForecast` (ADD §15) added to the frozen contract, execution DAG amended. |
| [`0042`](0042-patch-redaction-residual-surface.md) | Patch redaction covers only `+`/`-` line bodies; filenames and binary-diff headers are an accepted residual surface (from qa-09's P2 finding). |
| [`0043`](0043-multi-resource-runway.md) | Generalize quota runway into a multi-resource forecast (context window, cost budget, rate limits); implementation staged with issue #14. |
| [`0044`](0044-frozen-feature-lookup-port.md) | Freeze the repository/session feature-lookup port (wave2-analysis REC-01), unifying three package-local seams. |
| [`0045`](0045-rename-to-auspex.md) | Rename the product from Preflight to Auspex (supersedes ADR-001); archives and git history are deliberately not rewritten. |
| [`0046`](0046-tiered-telemetry-retention.md) | Tiered telemetry retention: hot raw window → rollup → gzip archive → delete. |
| [`0047`](0047-token-cohort-fallback-ladder.md) | Similar-turn cohort fallback ladder for the token forecaster (issue #20 Phase 1 of the [backlog note](../backlog/provider-model-effort-features.md)). |
| [`0048`](0048-repository-checkpoint-restore.md) | Real repository checkpoint restore (issue #6), ending the vertical slice's capture-only deferral. |
| [`0049`](0049-docs-reorg-bilingual.md) | Documentation reorganization: design docs under `docs/design/`, per-folder READMEs, Traditional Chinese translations. |
| [`0050`](0050-hook-subcommand-kebab-case.md) | Hook subcommand argv is kebab-case (ratifies the shipped CLI over ADD Appendix E.3's PascalCase); the provider's `hook_event_name` and settings.json matcher keys stay PascalCase (issue #61, REC-03). |
| [`0053`](0053-token-forecast-input-output-split.md) | Token forecast gains an additive input/output split on frozen `domain.TokenForecast`, the input interval structurally wider (models predict input worse — Bai et al. 2026 direction); widening is an uncalibrated structural default gated on #11 (issue #65 Phase 1). |

Neighbors: ADRs amend [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md)
(what an ADR must state is defined in Constitution §3.4); owner-level
decision sessions that led to many of these are logged as `D-##` entries
in [`../DECISION_LOG.md`](../DECISION_LOG.md); superseded pre-ADR drafts
sit in [`../archive/`](../archive/README.md).
