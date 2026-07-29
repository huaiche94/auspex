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
| [`0054`](0054-auto-checkpoint-and-run.md) | `CHECKPOINT_AND_RUN` 決策在兩個決策面自動建立 pre-turn checkpoint 對（state + repository），由 `state_checkpointing.on_checkpoint_and_run` 把關（預設啟用），checkpoint 失敗時 fail-open；就此動作取代「僅建議」的定調（issue #116）。 |
| [`0055`](0055-runtime-empirical-calibration.md) | 執行期經驗校準啟動：ADR-047 cohort 階梯改讀兩個 turn 級生產者（managed usage 事件＋ADR-051 Stop-hook 捕捉），配合 per-turn 去重與候選池稀釋預過濾，>= 8 經驗 token 基準就此生效（#42）；成本帶改為同 cohort turn 已知四類成本的經驗 P50–P90（`Source = "four-class-empirical"`，migration 0064 持久化），門檻以下維持兩類帶（#66 item b）。Calibrated 一律維持 false。 |
| [`0056`](0056-codex-appserver-data-source.md) | Codex App Server JSON-RPC stdio 串流成為經授權的解析資料來源(ADR-052 觸發①):newline 分隔框架已對 0.144.5 驗證、型別化穩定子集只命名識別碼/數字(diff/plan/錯誤文字只量測、絕不保留)、通知對映維持在封閉 EventType 分類學內(plan 更新餵 Progress Tree 提案、不是事件)、審批請求只上呈絕不自動核准(issue #9 M7 Phase 2)。 |
| [`0057`](0057-cost-guard-adoption-boundaries.md) | Cost Guard:直接採用 7 項成本治理能力(outcome ledger、budget envelope、shadow mode、spin gate、cost regression、subagent 歸因、hygiene hints——#140–#146),3 項改寫為 Auspex-native 版本(#147–#149),proxy/routing/prompt 改寫/支付防火牆不進核心(governor-not-harness-owner 邊界);規範性的 hard-budget 保證階梯(managed+live/managed+end-only bounded overshoot/native-hook advisory)與 shadow-first 執法紀律。 |
| [`0058`](0058-resume-bootstrap-prompt.zh-TW.md) | Resume bootstrap(§21.6 step 8):wake 時的 prompt 只由第一方耐久狀態建構(從不重播、從不持久化——原始 prompt 在任何地方都不存在);單一 provider 中立 builder 兩種 profile(同 session/thread 用 `re_entry` 差異簡報,新 session 依 FR-151 用 `cold_bootstrap` 以 Progress Tree 重建任務框架);暫停時 checkpoint 填入 manifest 既有的 `ProviderInfo`/`NextActionInfo`/`ResumeInfo` 欄位,session/thread locator 成為第一級耐久資料;第一個 production `SessionResumer` 重新武裝 stopper;wake job 維持不帶 payload(issue #9 收尾切片,餵給 #8/#11)。 |

相關文件：ADR 會修訂 [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md)
（ADR 必須陳述的內容定義於 Constitution §3.4）；促成其中多項決策的擁有者
層級（owner-level）決策會議，記錄為 [`../DECISION_LOG.md`](../DECISION_LOG.md)
中的 `D-##` 條目；已被取代的 ADR 前身草稿則存放於
[`../archive/`](../archive/README.md)。
