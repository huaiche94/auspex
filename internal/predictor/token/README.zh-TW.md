# internal/predictor/token/ — 第 2 階段：針對下一個 turn 的規則式 token 成本推估

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

`RuleTokenForecaster`（`forecaster.go`）實作了凍結的 `app.TokenForecaster` port（ADR-041、
predictor-05b）：依照 ADD §15.1（token 分解）與 §15.2（初版 token predictor），它把來自
[`scope/`](../scope/README.md) 的第 1 階段 `domain.ScopeEstimate`，加上
prompt／session／progress 特徵，轉換成 `domain.TokenForecast`（TokensP50/P80/P90）。

只有當 `RecentSimilarTurnTokens` 提供 >= 8 筆相似 turn 樣本時（由 ADR-047／#20 的
provider/model/effort 後備階梯選出，並以 reason code 揭露是哪一個 rung 回答的），基準
P50/P90 才是經驗值。低於這個門檻時，基準值就是一個 cold-start 常數：`baseTurnTokens`
（6000）× ADD §14.6 的相對 task-class 倍率（`coldstart.go`），且 P90 固定為 2× P50。
六個 ADD §15.2 倍率（scope、verification、complexity、retry、progress、ambiguity）以幾何
平均合併，個別上限 3.0，合併後上限 6.0。P80 是一項明文記載的假設：在 P50 與 P90 之間做
對數空間內插，因為 ADD §15.2 並未指定 P80 的基準值。

input/output 拆分（#65 第一階段，ADR-0053）：在具權威性的 total 之外，每個預測都攜帶相異的
input 與 output P50/P90 區間（`Input/OutputTokensP50/P90`），其中 **input 區間在結構上比
output 區間更寬**——這是 Bai et al. 2026 的發現：模型預測 input token 比 output 更差。加寬
幅度（`inputIntervalWideningFactor`，1.5）與中性的中心切分（`defaultInputTokenShare`，0.5）
都是受 #11 把關的未校準結構性預設值，絕非擬合係數；論文的 ~153:1 input:output 量級絕不引入。
此拆分絕不把 `Calibrated` 翻成 true。

Cold-start 誠實性備註（issue #42，尚未結案）：cold-start 數字是 bootstrap 常數，不是量測值。
在 #42 的修正落地之前，推估實質上對 prompt 是「盲」的——持久化的 turn payload 只帶有
hash／length／approx-tokens，讀回時每個 class 都會坍縮成 `unknown`，導致幾乎所有 prompt 的
P50 都落在 ~3210 左右。classifier 詞彙與 payload 的修正已經落地（驗收證明見
`internal/integrationtest/forecast_prompt_conditioned_test.go`，該測試斷言 P50 現在會依
task class 而不同，方向與 §14.6 的倍率一致），但在部署累積到 >= 8 筆相似樣本之前，推估仍然
只透過 class 倍率與套用在這些常數上的 §15.2 倍率來回應 prompt。本波每一個結果都是
`Calibrated=false`，Confidence 最高只到 medium——絕不是機率（Constitution §7 rule 7）。

輸出會提供給 [`quota/`](../quota/README.md)（delta 縮放）與
[`internal/pricing`](../../pricing/README.md)（成本範圍）。上述引用的 ADD 章節見
[Auspex_ADD.md](../../../docs/design/Auspex_ADD.md)。套件合約詳見 `doc.go`。
