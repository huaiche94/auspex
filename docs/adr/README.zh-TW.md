# docs/adr/ — 已通過的架構決策紀錄（Architecture Decision Records）

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

每一項已通過的架構決策各自對應一個檔案，命名為 `NNNN-title.md`。已通過的
ADR 是**不可變更的歷史紀錄**（Constitution §3.3）：若要變更某項決策，必須
撰寫一份新的 ADR 來取代舊決策 —— 絕不可就地修改已通過的 ADR。只有
`contract-integrator` 角色可以通過 ADR 並編輯此目錄（Constitution
§4.3）；任何角色皆可提出提案。

此目錄的編號從 0041 開始。決策 001–040 早於此目錄成立，以摘要條目的形式
記錄在 [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md) §33
（「Architecture Decision Records」）中，目前仍留存於該處 —— 例如
ADR-001（產品名稱，已由 ADR-045 取代）到 ADR-040（作業系統喚醒不在範圍
內）。§33 也為全文收錄於本目錄、編號較後面的 ADR 保留了簡短的鏡射條目。

| ADR | 決策內容 |
|---|---|
| [`0041`](0041-predictor-forecast-layer.md) | Predictor 流程新增明確的 Forecast 層：將 `TokenForecast`／`QuotaForecast`（ADD §15）納入凍結契約，並修訂執行 DAG。 |
| [`0042`](0042-patch-redaction-residual-surface.md) | Patch 遮蔽（redaction）僅涵蓋 `+`／`-` 行本文；檔名與二進位差異（binary-diff）標頭屬於可接受的殘留曝露面（源自 qa-09 的 P2 發現）。 |
| [`0043`](0043-multi-resource-runway.md) | 將配額續航（quota runway）廣義化為多資源預測（context window、成本預算、速率限制）；實作隨 issue #14 分階段進行。 |
| [`0044`](0044-frozen-feature-lookup-port.md) | 凍結 repository／session 特徵查詢埠（feature-lookup port）（wave2-analysis REC-01），統一三個套件內部各自的介接點（seam）。 |
| [`0045`](0045-rename-to-auspex.md) | 將產品由 Preflight 更名為 Auspex（取代 ADR-001）；archive 與 git 歷史刻意不予重寫。 |
| [`0046`](0046-tiered-telemetry-retention.md) | 分層遙測資料保留：熱資料原始窗口 → rollup 彙總 → gzip 封存 → 刪除。 |
| [`0047`](0047-token-cohort-fallback-ladder.md) | Token 預測器的相似回合世代（cohort）備援階梯（issue #20，[backlog 筆記](../backlog/provider-model-effort-features.md)第一階段）。 |
| [`0048`](0048-repository-checkpoint-restore.md) | 真正的 repository checkpoint 還原（issue #6），結束 vertical slice 中「僅擷取、不還原」的延遲事項。 |
| [`0049`](0049-docs-reorg-bilingual.md) | 文件重組：設計文件移至 `docs/design/`、每個目錄各自的 README、繁體中文翻譯。 |
| [`0050`](0050-hook-subcommand-kebab-case.md) | Hook 子指令 argv 採 kebab-case（正式化已出貨的 CLI，取代 ADD 附錄 E.3 的 PascalCase）；provider 的 `hook_event_name` 與 settings.json matcher key 維持 PascalCase（issue #61，REC-03）。 |
| [`0053`](0053-token-forecast-input-output-split.md) | Token 預測在凍結的 `domain.TokenForecast` 上增量新增 input/output 拆分，input 區間在結構上更寬（模型預測 input 較差——Bai et al. 2026 方向）；加寬幅度是受 #11 把關的未校準結構性預設值（issue #65 第一階段）。 |

相關文件：ADR 會修訂 [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md)
（ADR 必須陳述的內容定義於 Constitution §3.4）；促成其中多項決策的擁有者
層級（owner-level）決策會議，記錄為 [`../DECISION_LOG.md`](../DECISION_LOG.md)
中的 `D-##` 條目；已被取代的 ADR 前身草稿則存放於
[`../archive/`](../archive/README.md)。
