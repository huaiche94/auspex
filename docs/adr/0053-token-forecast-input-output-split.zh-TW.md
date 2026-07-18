# ADR-0053 — Token 預測的 input/output 拆分，input 區間較寬（#65 第一階段）

> 🌐 [English](0053-token-forecast-input-output-split.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：已接受（Accepted）
日期：2026-07-17
負責人：由 lead 執行（提案 PROPOSAL——凍結合約變更，於合併時由 owner 簽核）
追蹤：issue #65（`docs/backlog/token-cost-prediction-research.md` §3.A 的第一階段）；研究 arXiv:2604.22750（Bai et al. 2026）；排序在 #11（校準）之後，與 #42 並行

## 背景

`domain.TokenForecast`（由 ADR-041 凍結）回報的是單一的 total-token 區間
——`TokensP50/P80/P90`——完全不區分 input 與 output token。Bai et al.
2026（在 SWE-bench 上跨八個前沿模型量測，作為外部理據記錄於
`docs/backlog/token-cost-prediction-research.md`）對這個未區分的區間提出兩項發現：

1. 模型預測自身 **input** token 的能力，比預測 output token *更差*
   （Pearson ≤ 0.39，最佳情況是 output；input 較差），並且系統性地低估。
   input 軸確實是較難預測的一軸。
2. input 同時也是主要的成本驅動因子（input/output 比值平均約 153）。

論文中的數字是**先驗與方向**，絕非 Auspex 的係數（backlog 筆記的 grounding
紀律，§5）。它們證成的是*方向*——input 區間應該比 output 區間**更寬**——但
這個加寬的*幅度*是一個受 #11 校準資料把關的擬合數字，而 ~153:1 這個比值是外部
SWE-bench 證據，絕不引入。

拆分預測會動到凍結的 `domain.TokenForecast` 合約，因此需要自己的 ADR
（Constitution §3），沿用 ADR-044 為 cohort rung 所做修訂、以及 ADR-047
後備階梯所建立的先例。

## 決策

1. **在凍結型別上增量新增拆分欄位。** `domain.TokenForecast` 新增四個
   pointer 型別欄位——`InputTokensP50/P90`、`OutputTokensP50/P90`——將即將發生
   的 turn 的 token 增量式地拆解為兩個相異的區間。凍結的 total
   （`TokensP50/P80/P90`）**維持不變且仍具權威性**；`nil` 代表預測器不區分
   兩軸（#65 之前的行為——unknown 不等於 zero）。每一軸只有 P50/P90（沒有
   P80）：此拆解以 P50–P90 range 呈現與消費，與 scope、duration band
   （migration 0041/0047）一致；P80 僅保留在 total 上。

2. **input 區間在結構上更寬（僅方向）。** `RuleTokenForecaster` 拆解 total
   區間，使 **input 區間比 output 區間更寬**——這是論文方向所授權的唯一
   不對稱性。output 區間沿用 total 區間自身的基準相對寬度；input 區間則以
   `inputIntervalWideningFactor` 加寬。沒有任何東西被人為縮窄——較難的一軸
   加寬，較可預測的一軸維持原樣。

3. **兩個未校準的結構性預設值，明確標示，受 #11 把關。**
   - `inputIntervalWideningFactor = 1.5`——input 區間的 P90 尾端比 output
     區間寬多少。一個刻意取整、保守的佔位值，**只**表達論文的方向（input
     是較難的一軸）。非擬合；也非從論文任何係數推導而來。
   - `defaultInputTokenShare = 0.5`——中心值的中性切分。此 slice 刻意**不**
     內建 input 對 output 的量級比值；該比值受 #11 把關，而論文的 ~153:1
     絕不引入。0.5 並非宣稱 input 與 output 的 token 數相等——它是拒絕在此處
     發明主導量級。input P50 + output P50 = total P50，因此切分不損失任何
     中心質量。

   兩者都是結構性 bootstrap 常數，其性質與 `baseTurnTokens`、以及 cold-start
   的「P90 = 2× P50」寬度完全相同：有紀錄的佔位值，預期會被 #11 擬合值取代。
   此拆分未校準，因此絕不會把 `Calibrated` 翻成 true（Constitution 原則 #2：
   score 不是機率）。

4. **為 read-back 而持久化，非可再推導的訊號（migration 0063）。** forecast
   card 是透過讀回已持久化的 `predictions` 資料列建構（`forecastcard.go`——
   read-back，非重新計算），因此此拆分以四個增量式可為 NULL 的欄位
   （`token_input_p50/p90`、`token_output_p50/p90`）持久化，以在 card 與
   `auspex evaluate` 上呈現。migration 編號 `0063` 是預先指派給此 slice 的；
   它落在 0060–0069 區段是配置上的便宜行事，而非語意主張（這些欄位屬於
   predictor 的 `predictions` 表）。

## 誠實範圍聲明

- **方向，而非量級。** 此 slice 唯一看起來像擬合數字的，是加寬係數，而它被
  明確定位為未校準的結構性預設值。沒有任何 SWE-bench 先驗以係數形式進入。
- **中心切分刻意保持中性。** 50/50 的切分明顯低估了 input 的主導地位——大家
  都知道 input token 在 agentic coding 中佔主導——但要陳述這個主導*量級*需要
  一個此 slice 不得發明的擬合比值。#11 會以資料擬合值取代此 share；在那之前
  card 只承載證據所支持的寬度不對稱性。
- **此 slice 不傳播到 research export。** 與 migration 0062（把 duration
  預測複製進 `calibration_samples`）不同，此拆分**不**加入 export。今日的
  拆分是已 export 之 total 的*確定性結構轉換*，因此 export 它不增加任何獨立的
  校準訊號，也不開啟 unlabeled-history 破口（#11 可從 `token_p50/p90` 重建
  它）。export 的擴充延後到「校準後的預測器獨立估計兩軸」的那個 wave——
  capture-before-model（D-10/D-12）。
- **未來校準後的預測器會獨立估計兩軸。** 此合約欄位是真正的軸空間，而不僅是
  render-time 的轉換：它為 #11 校準後的預測器所需的形狀預留位置，正如
  ADR-041 在實作存在之前就預留了 pipeline 形狀。

## 後果

- `CONTRACT_FREEZE.md` 新增一筆針對增量式 `domain.TokenForecast` 欄位的
  Amendments 條目；所有建構點維持可編譯（增量式 pointer 預設 `nil` = 「未
  拆分」）。
- forecast card 與 `auspex evaluate` 會顯示相異的 input/output range，且
  input range 明顯較寬，並標示為未校準。
- 當 #11 落地時，兩個結構性預設值都成為擬合的 per-cohort 值，而 research
  export 便可承載（屆時已獨立的）拆分。
- 不動 statusline：#90 第一階段已把 per-turn 的預測片段從狀態列移除；此拆分
  留在 card 各面向上。

## 已考慮的替代方案

- **僅 presenter 端推導（不變更合約）。** 在 forecast card 中從已持久化的
  total 計算拆分，讓 `domain.TokenForecast` 保持不變。已否決：此 slice 的目的
  是把 input/output 兩軸確立為*一項預測合約*，讓未來校準後的預測器有地方放置
  獨立估計；僅 presenter 的視圖會斷絕這條路。
- **內建 input 主導的量級切分**（例如 0.75，或論文的 ~153:1）。已否決：那是
  受 #11 把關的擬合量級／引入的 SWE-bench 先驗——正是 grounding 紀律所禁止的。
- **每一軸都持久化 P50/P80/P90。** 已否決：此拆解以 range 呈現；每軸的 P80
  只增加儲存與呈現面向，卻不驅動任何決策。P80 留在具權威性的 total 上，與
  scope/duration band 一致。
- **現在就擴充 research export。** 因為過早而否決：此 wave 的拆分可從已 export
  的 total 重建，因此在校準後的預測器讓兩軸獨立之前，export 它是多餘的。
