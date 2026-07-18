# docs/implementation/vertical-slice/wave2-analysis/ — the mid-build analysis round that re-planned Phase 3+

> 🌐 English | [繁體中文](README.zh-TW.md)

After Phase 2 integrated (19 executed nodes: Bootstrap + Phases 1–2), the
build paused for a ten-part analysis phase (numbered 3.1–3.10) before
any Phase 3 assignment. Its ground rule was "Unknown is preferred over
invented": several reports exist precisely to state that data was absent
rather than to fill the requested format with numbers. Reports are
analysis/recommendation only — none modified implementation.

| Report | Phase | What it is |
|---|---|---|
| [`Prediction_Error_Report.md`](Prediction_Error_Report.md) | 3.1 | Estimate-vs-actual per executed node, every value labeled Observed / Estimated / Unknown. |
| [`Calibration_Report.md`](Calibration_Report.md) | 3.2 | Calibration observations derived from 3.1, with the n=19 / one-repo / one-day caveat stated up front. |
| [`Wave2_Lessons.md`](Wave2_Lessons.md) | 3.3 | Aggregation of the five then-existing [`../lessons_learned/`](../lessons_learned/README.md) files; recurring issues ranked by independent-observation frequency. |
| [`Predictor_Improvement_Suggestions.md`](Predictor_Improvement_Suggestions.md) | 3.4 | Rule-predictor-tier suggestions, each labeled evidence-based or speculative. |
| [`Historical_Replay_Report.md`](Historical_Replay_Report.md) | 3.5 | Records that **no replay was performed** and precisely why the preconditions were absent. |
| [`Missing_Telemetry_Report.md`](Missing_Telemetry_Report.md) | 3.6 | Product telemetry never captured (no live session had run) and process telemetry the build itself failed to capture. |
| [`Feature_Registry.md`](Feature_Registry.md) | 3.7 | Registry of every predictor feature, with identity/provenance and suitability/operations tables. Declared **canonical** at creation — its own status field says future predictor work must reference features through it. |
| [`Feature_Gap_Report.md`](Feature_Gap_Report.md) | 3.7 | Companion to the registry: why each Unknown/fixture-scoped feature gap exists, impact, closing approach, ranked. |
| [`Prediction_Confidence_Report.md`](Prediction_Confidence_Report.md) | 3.8 | Confidence-sorted view of the registry plus training-suitability recommendations. |
| [`ADR_Recommendations.md`](ADR_Recommendations.md) | 3.9 | Contract-level proposals ("proposals only" at the time; REC-01 was later accepted as [ADR-044](../../../adr/0044-frozen-feature-lookup-port.md)). |
| [`Wave3_Recommendation.md`](Wave3_Recommendation.md) | 3.10 | The unlocked-node analysis and proposed Phase 3 assignment, explicitly awaiting owner approval. |

## Neighbors

The phase-by-phase record these reports fed back into is
[`../README.md`](../README.md); the accepted decisions they produced are
in [`../../../adr/`](../../../adr/README.md); the deferred-with-data
discipline they established carries on in
[`../../../backlog/`](../../../backlog/README.md).
