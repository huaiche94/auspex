# docs/backlog/ — 已核准但尚未排程工作的設計筆記

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

此處每個檔案都是一份設計筆記，記錄儲存庫擁有者已核准、但尚未（完全）
排入目前波次（wave）的工作。每份 backlog 筆記都對應一個追蹤用的
GitHub issue，記錄促成該筆記產生的稽核過程，並附帶自身的分階段
TODO —— 因此日後排程時，無須重新建構當初的推理過程。這些筆記遵循與
wave2 分析相同的紮根（grounding）原則：擷取（capture）與運作機制可以
預先設計，但數值性的決策（係數、閾值）需等待真實資料。

| 檔案 | 涵蓋內容 |
|---|---|
| [`provider-model-effort-features.md`](provider-model-effort-features.md) | 讓 provider／model／effort 成為預測輸入項（依 2026-07-13 稽核，該流程原本對 provider、model、effort 皆無感知）。追蹤來源：issue #20，排序見 [`../DECISION_LOG.md`](../DECISION_LOG.md) D-10。其 §4 的第 0 階段（擷取）與第 1 階段（世代篩選 cohort filtering，[ADR-047](../adr/0047-token-cohort-fallback-ladder.md)）已於 2026-07-14 完成；第 2 階段（實證校準）則受限於缺乏各世代（per-cohort）資料而暫緩。 |
| [`token-cost-prediction-research.md`](token-cost-prediction-research.md) | 以 arXiv:2604.22750（Bai 等，2026）為依據的路線圖：cache-aware 四類成本模型、重複檔案操作 risk factor，以及 phase-aware 條件式預測。論文的數字僅作為外部先驗／依據（支撐 uncalibrated、寬區間的呈現面），絕非擬合的 Auspex 係數。Phase 0（在 predictor 補充文件與 README 落地依據）已於 2026-07-14 完成；後續各階段都需先有擷取步驟。 |

## 相關文件

- 當某個 backlog 階段升級為契約層級的決策時，會成為
  [`../adr/`](../adr/README.md) 中的一份 ADR（如同 ADR-047 的情形）。
- 筆記所延後處理的公式，定義於
  [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md) §15 及
  [predictor 補充文件](../design/Auspex_Predictor_Design_Supplement.md)。
- 提供 backlog 素材的落差分析，位於
  [`../implementation/vertical-slice/wave2-analysis/`](../implementation/vertical-slice/wave2-analysis/README.md)。
