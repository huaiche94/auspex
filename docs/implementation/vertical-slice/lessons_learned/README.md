# docs/implementation/vertical-slice/lessons_learned/ — per-role retrospectives

> 🌐 English | [繁體中文](README.zh-TW.md)

One retrospective file per vertical-slice role. Each is a table with one
row per executed node, comparing estimate against outcome — complexity,
files changed, duration — plus unexpected dependencies/files, blockers,
token-waste observations, and recommendations for Auspex itself (the
product being built is a predictor of exactly this kind of work, so
these rows double as its earliest training-shaped data). Files were
appended as phases completed, so coverage varies by role.

| File | Covers |
|---|---|
| [`contract-integrator.md`](contract-integrator.md) | Bootstrap stage (one `bootstrap-01` row for the contract freeze). |
| [`foundation.md`](foundation.md) | foundation-01 through -09. |
| [`claude-provider.md`](claude-provider.md) | claude-provider-01 through -07. |
| [`checkpoint.md`](checkpoint.md) | checkpoint-a01–a09 and b01–b09, plus the `corrective-qa05` fix (tracked-file diff redaction). |
| [`predictor.md`](predictor.md) | predictor-01 through -11 (incl. -05c and the final DataSource work; no -05b row). |
| [`runtime.md`](runtime.md) | All 21 runtime nodes (a01–a11, b01–b10) plus a final Graceful-Pause-service row. |
| [`qa.md`](qa.md) | qa-01 and qa-08 (Phase 3) only — qa's later nodes (qa-02–07, -09, Phases 7–12) have no rows here. |

## Neighbors

- Node-by-node status and validation evidence (what happened, rather
  than how it compared to estimates) is in the per-role progress
  artifacts one level up: [`../README.md`](../README.md).
- The first five of these files (before `qa.md`/`runtime.md` existed)
  were aggregated into
  [`../wave2-analysis/Wave2_Lessons.md`](../wave2-analysis/Wave2_Lessons.md),
  which ranked the recurring issues across roles.
