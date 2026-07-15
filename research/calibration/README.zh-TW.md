# research/calibration/ — 離線校準腳本（M13，issue #11）

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

這裡是 [`../README.md`](../README.md) 中所描述之離線校準流程背後的腳
本 —— 請先閱讀該文件，了解其紮根（grounding）原則（沒有資料就不得提
出係數方案；未達樣本門檻的世代（cohort）只予以回報，絕不擬合）以及端
到端的使用方式。

僅使用標準函式庫，Python ≥ 3.9；絕不作為 Go 執行檔的執行期相依項
目。輸入資料為去識別化後的 `auspex export` 資料集（FR-170/171）。

## 檔案

- `load.py` —— `auspex export calibration` JSONL
  （`auspex.calibration-export.v1`）的載入器與 schema 驗證。格式錯誤的
  資料列與未知的 schema 版本一律明確失敗（fail loudly）—— 損毀的資料
  集絕不能悄悄縮減成一個看似「乾淨」但較小的資料集。
- `observations.py` —— `auspex export observations` JSONL
  （`auspex.observations-export.v1`）的載入器與 schema 驗證，以及每回
  合成本／context 差值的推導。狀態列（statusline）總計是以 session 為
  單位累加的，因此每回合的數值屬於盡力而為的歸因模型（其限制已記載於
  模組 docstring 中）：沒有回合前基準值即代表無法推導 —— 絕不假設為
  0 —— 而來自 compaction 的負值 context 差值則原樣呈現，絕不做截斷
  （clamp）處理。可獨立執行（`--json` 可輸出機器可讀格式）。
- `report.py` —— 針對某份校準匯出資料的資料就緒度（data-readiness），
  以及（在資料允許的情況下）校準涵蓋率報告；加上
  `--observations observations.jsonl` 參數時，會併入由
  `observations.py` 推導出的每回合實際值就緒度區段，以及兩組以
  `turn_id` 進行的涵蓋率 join：**token 涵蓋率**（預測分位數對上
  managed-run 的 `total_tokens` 實際值——僅限 managed run，因為
  statusline 不帶每回合 token）與**成本區間涵蓋率**（預測成本區間
  `cost_low_usd..cost_high_usd` 對上 `observations.py` 推導的每回合成本
  差值）。成本涵蓋率是 #72 的 hook 模式突破口：每回合成本差值單靠原生
  hook 遙測即可推導，所以即便 token 無法 join，原生 hook 回合仍能在此
  join。它會回報區間命中率，並分開統計實際值落在區間之下（成本高估）
  與之上（成本低估）的筆數——這正是量化 #42／#66 低估現象的方向性訊號。
  它並會把該 join 依 #20 的 cohort 三元組分層（**逐 cohort 成本殘差**，
  #72 Phase 2）：對每個達到 §15.2 門檻（≥ 8 個**已 join** 的 turn）的
  cohort，擬合出「forecast 的 high bound 相對於真實成本低估了幾倍」的經驗
  倍率（`actual/high` 的中位數與 P90）；門檻以下或有未標記軸的 cohort 只回
  報、絕不擬合。Go forecast 不受影響——這些倍率是未來階段（#66 的 cache-aware
  成本模型）會取用的輸入。

這些報告最終要餵給的 predictor 位於 `internal/predictor/`；匯出器則位
於 `internal/retention/`（`export.go`、`observations.go`）。
