# docs/implementation/vertical-slice/wave2-analysis/ — 重新規劃 Phase 3 以後工作的建置期間分析階段

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

Phase 2 整合完成後（已執行 19 個節點：Bootstrap + Phase 1–2），建置工作
暫停，進入一個分為十個部分（編號 3.1–3.10）的分析階段，之後才展開任何
Phase 3 的指派。其基本原則是「寧可未知，也不憑空捏造」：有幾份報告存在
的目的，正是要明確指出資料並不存在，而非硬套進所要求的格式填入數字。
這些報告僅屬分析／建議性質 —— 沒有任何一份修改了實作本身。

| 報告 | 階段 | 內容說明 |
|---|---|---|
| [`Prediction_Error_Report.md`](Prediction_Error_Report.md) | 3.1 | 每個已執行節點的預估與實際值比較，每個數值皆標註為 Observed／Estimated／Unknown。 |
| [`Calibration_Report.md`](Calibration_Report.md) | 3.2 | 由 3.1 衍生出的校準觀察結果，並在開頭即註明 n=19、僅一個 repo、僅一天資料的限制。 |
| [`Wave2_Lessons.md`](Wave2_Lessons.md) | 3.3 | 彙整當時已存在的五份 [`../lessons_learned/`](../lessons_learned/README.md) 檔案；依各角色間獨立觀察到的頻率，對反覆出現的問題進行排序。 |
| [`Predictor_Improvement_Suggestions.md`](Predictor_Improvement_Suggestions.md) | 3.4 | 針對規則式 predictor 各層級的建議，每項皆標註為「有證據佐證」或「推測性」。 |
| [`Historical_Replay_Report.md`](Historical_Replay_Report.md) | 3.5 | 記錄**並未執行任何重播（replay）**，以及前置條件為何不存在的確切原因。 |
| [`Missing_Telemetry_Report.md`](Missing_Telemetry_Report.md) | 3.6 | 從未擷取到的產品遙測資料（因為沒有任何實際 session 執行過），以及建置過程本身未能擷取的流程遙測資料。 |
| [`Feature_Registry.md`](Feature_Registry.md) | 3.7 | 所有 predictor 特徵的登記清單，含身分／來源資訊與適用性／運作面向的表格。此文件在建立當下即宣告為**權威版本（canonical）** —— 其自身的狀態欄位指出未來的 predictor 工作必須透過此登記清單來參照特徵。 |
| [`Feature_Gap_Report.md`](Feature_Gap_Report.md) | 3.7 | 與登記清單相輔相成：說明每個 Unknown／僅限於測試 fixture 的特徵缺口為何存在、其影響，以及排序後的補齊做法。 |
| [`Prediction_Confidence_Report.md`](Prediction_Confidence_Report.md) | 3.8 | 依信心程度排序後的登記清單檢視，外加訓練適用性方面的建議。 |
| [`ADR_Recommendations.md`](ADR_Recommendations.md) | 3.9 | 契約層級的提案（當時僅為「提案」；REC-01 後續被核准為 [ADR-044](../../../adr/0044-frozen-feature-lookup-port.md)）。 |
| [`Wave3_Recommendation.md`](Wave3_Recommendation.md) | 3.10 | 針對已解鎖節點的分析，以及提議中的 Phase 3 指派方案，明確標註為等待擁有者核准。 |

## 相關文件

這些報告回饋所依據的逐波次紀錄位於 [`../README.md`](../README.md)；由
此產生、已核准的決策位於 [`../../../adr/`](../../../adr/README.md)；這
些報告所建立的「延後但需有資料」原則，延續於
[`../../../backlog/`](../../../backlog/README.md)。
