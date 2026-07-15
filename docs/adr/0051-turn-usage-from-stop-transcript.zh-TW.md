# ADR-051 — Stop hook 由 transcript 擷取每回合 token 用量（僅數字）

> 🌐 [English](0051-turn-usage-from-stop-transcript.md) | 繁體中文

Status: Accepted
Date: 2026-07-15
Owner: lead
Approved by: repository owner, 2026-07-15（issue #72 解決指示）
Tracking: issue #72（提案第 4 項）；解除 #66/#65 的 capture 前置與 #11/#42 的 token 面阻塞

## Context（脈絡）

Native hook 模式的 token 實際值 join 數為**零**：Stop hook payload 不含任何用量
欄位，statusline 快照只有 session 累積的 `total_cost_usd` —— 因此
`predictions.token_p50/p90` 永遠無法與每回合實際值比對（issue #72；2026-07-15
校準就緒報告：167 筆預測、0 筆 join）。PR #73 以**成本**差分 rail 作為過渡開口；
但精確 token 在 hook 模式仍無來源，且 #66 成本模型所需的四類 cache token 也
無處擷取。

Provider 實際暴露的資訊：Stop hook stdin 帶有 `transcript_path`，而 session
transcript 的 `type=="assistant"` 條目帶有 `message.usage` ——
`input_tokens`、`output_tokens`、`cache_read_input_tokens`、
`cache_creation_input_tokens` —— 以及 `message.model` 與 `requestId`。
已對真實 Claude Code 2.1.x transcript 驗證：同一次 API 呼叫會以多行 JSONL 出現、
共用 `requestId` 且用量逐位元組相同（天真加總會重複計算，擷取必須以 requestId
去重）；子代理活動存於獨立的 sidechain 檔案，因此主 transcript 正好對應預測
所針對的主迴圈回合。

## Decision（決定）

Stop 時，auspex 解析剛完成回合在 transcript 中的切片，並以**僅數字**欄位
豐富 `provider.turn.completed` 事件 payload：

- `input_tokens`、`output_tokens`、`cache_read_input_tokens`、
  `cache_creation_input_tokens`、`total_tokens`（= input + output，對齊凍結的
  `managedUsageEvent` 詞彙；原始 cache 類別並列附帶）、`api_call_count`
  （唯一 `requestId` 數）、`model_id`（最後一個非 synthetic 值）。
- 回合切片 = 最後一個 prompt 邊界（非 meta、非 sidechain、內容不含
  `tool_result` 區塊的 user 條目）之後的 assistant 條目，以 `requestId` 去重；
  有界讀取（32 MiB 尾端視窗、8 MiB 單行上限）。
- 校準匯出將其以 `actual_*` 欄位併入 live 列（`provider.turn.completed` 與
  帶回合戳記的 `provider.usage.observed` 兩來源取最新）；`report.py` 的
  `token_coverage()` 直接使用。

本 ADR 固定的不變量：

1. **僅數字。** 任何文字內容 —— prompt、回覆、工具輸出、檔案內容 —— 永不進入
   任何持久化欄位。Constitution §7 隱私預設不變。
2. **Fail-open 且非承重。** 任何缺檔、解析失敗、超長行或視窗外回合，一律退化為
   與 ADR-051 之前逐位元組相同的事件 —— 絕不使 hook 失敗、絕不捏造零值。
   Constitution §7 規則 4（「未文件化的 transcript 永不作為穩定路徑解析」）是
   本文記錄的張力：transcript 屬未文件化的 provider 產物，故此豐富化為
   **嚴格可選** —— provider 格式改變只會靜默停用豐富化，不會破壞任何功能。
   此取捨（允許可選豐富化；仍禁止穩定路徑依賴）即為本決定。
3. **僅主鏈。** 排除子代理 sidechain（獨立檔案；防禦性 `isSidechain` 過濾並以
   測試釘住）—— 歸因對齊被校準的主迴圈預測。
4. **Managed run 仍為權威。** 分工不變：managed 模式 = provider 回報用量；
   hook 模式 = transcript 推導用量。

## Consequences（後果）

- Hook 模式的 token join **自此起精確**；歷史資料無法回補 join（不做追溯擷取）。
  由此解除：#42 的實證路徑、#11 的 token cohort 校準、#66 的 cache 類別
  capture 前置，並使 #65 的 input/output 拆分可量測。
- **無任何凍結契約變更。** payload 欄位為既有事件上的加法式 JSON；無 schema
  migration；匯出欄位為加法式（與 PR #73 / #62 duration rail 同一先例）。
- ADD：§33 增加本 ADR 的鏡像條目（同一變更內）；其餘 ADD 章節不受影響。
- 已接受的缺口：`calibration_samples` 無 token 實際值欄位，故 archived 列缺
  `actual_*` 欄位（live 列匯出有）。未來可在 retention 範圍（0060–0069）以
  加法式 migration 補齊。
- `provider.turn.failed` / StopFailure 回合尚未豐富化 —— 之後可在相同不變量下
  簡單擴充。
