# internal/pricing/ — 本機的逐模型價格表：token 推估 → 估計的美元成本範圍

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

ADR-043 increment 1（「cost forecast first」）：一個小型、本機、手動維護的價格表，把
token 推估值轉換成估計的成本範圍（RANGE）——絕不是單一數值，也絕不是實際量測到的成本。
價格是撰寫當下取自 Anthropic 公開牌價的預留（placeholder）預設值，絕不會在執行期間即時
抓取（local-first），且預期會隨時間漂移；它們對 batch 折扣或訂閱方案一無所知。forecast 的
「範圍」維持 cache-blind（2 類）；下方的四類 `FourClassCost`（#66）才是「四種 token class
皆已知」的 turn 所用的 cache-aware 成本原語。

主要進入點（`pricing.go`）：

- `Table` / `DefaultTable()` / `NewTable(overrides)`：不可變的 family → price 對照表
  （fable/mythos、opus、sonnet、haiku），以 model ID 的小寫子字串、依確定性的排序順序
  做匹配。
- `Table.Price(modelID)` 會解析出一個 `ModelPrice`（每 MTok 的美元價格，區分
  input/output），以及它所匹配到的 family；未知的模型會退回到 `DefaultFamily`
  （sonnet 等級——也就是 Claude Code 的預設等級；用 opus 會系統性高估 5 倍，用 haiku
  則會低估）。
- `Table.EstimateTurnCost(modelID, tokensLow, tokensHigh)` 產生一個 `CostRange`：
  由於 token 推估值是未區分 input/output 的總量，下界會把每個 token 都以 input
  價格、依低分位數計價，上界則以 output 價格、依高分位數計價——這個區間反映的是真實的
  不確定性，因此即使 P50 == P90 也會產生真正的範圍。`ModelFamily` 與 `Source` 會揭露
  是哪一個價格假設產生了這個數字。
- `Table.FourClassCost(modelID, nonCachedInput, cacheCreation, cacheRead, output)`（#66，
  arXiv:2604.22750，ADR-043 的成本軸）是 cache-aware 模型：對「四種 token class 皆已知」的
  turn 計算一個 POINT `CostBreakdown`，每一類各自以 Anthropic 顯式快取費率計價（cache read =
  input 的 10%、cache write = input 的 125%——`CacheReadInputMultiplier` /
  `CacheCreationInputMultiplier`，由基礎 input 費率推導，讓「這一項已公布的關係」只有一個
  來源）。重點在於：即便 cache-read token 是最便宜的一類，`CacheReadUSD` 通常仍是帳單中最大
  的一塊——因為累積的 context 會在一個 turn 的多次 round-trip 中被反覆讀取——這正是 #72
  Phase 2 那 ~7–8× 成本低估背後的機制。它完全不碰 forecast card（在四類 token「預測」出現
  之前維持 2 類）；四種 class 目前每個 managed turn 都已擷取，所以這一半是 gate 在 managed-run
  的資料量,而非擷取本身。任一類為負（未量測）時 `ok=false`；全為零則是合法的 `$0`。

消費端：[`internal/evaluation`](../evaluation/README.md) 的 forecast card（會把該範圍
標示為「uncalibrated estimate」，Constitution principle #2），以及
[`internal/policy`](../policy/README.md) 的 cost-budget 規則（`costbudget.go`），兩者
都由 [`internal/predictor/token`](../predictor/token/README.md) 的第 2 階段推估提供
資料。

以 YAML config 覆寫是一項已記載在案的後續工作，目前尚未建置——今天 `cmd/auspex` 尚未
接上任何正式環境的 config loader；`NewTable` 的 overrides 參數是目前既有的程式化
seam。本套件沒有 `doc.go`；套件合約就是 `pricing.go` 開頭的套件註解。
