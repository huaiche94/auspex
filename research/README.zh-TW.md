# research/ — 離線校準（calibration）管線（M13，issue #11）

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

校準（calibration）迴圈的離線那一半：讀取 `auspex export
calibration` 與 `auspex export observations` 產生的 JSONL，回報資料
就緒程度（readiness），推導出每回合（per-turn）的實際（ACTUAL）成本／
上下文差值，並在——通過各群組（cohort）樣本門檻之後——產生實證
分位數（empirical quantiles）與殘差報告，回饋係數給 predictor。

## 依據紀律（強制性規定）

與 `Predictor_Improvement_Suggestions.md` §2.3 及
`docs/backlog/provider-model-effort-features.md` 相同的規則：
**沒有資料就不提出係數。** 此管線絕不會針對未達樣本門檻的群組輸出
擬合出的數值；未達門檻時，會回報缺口（「insufficient samples」、
「actuals unknown」、「unlabeled rows」）取代之。針對 n≈0 進行調校，
與純粹用猜的並無區別。

## 用法

```sh
# 1. Export the datasets (de-identified by construction, FR-170/171):
auspex export calibration --out calibration.jsonl
auspex export observations --out observations.jsonl

# 2. Data-readiness report (works from day zero — an empty dataset is a
#    valid, honest input). --observations adds the per-turn actuals
#    readiness section:
python3 research/calibration/report.py calibration.jsonl \
    --observations observations.jsonl

# 3. Per-turn actual cost/context deltas (best-effort attribution —
#    see observations.py's docstring for the model and its limits):
python3 research/calibration/observations.py observations.jsonl
```

不需要任何第三方相依套件——僅使用標準函式庫，因此只要有 Python
≥ 3.9 的環境即可執行此報告。

## 目前報告會呈現什麼內容

在資料為零或稀疏的情況下，最有用的輸出是 *readiness*（就緒程度）
區段：有多少筆預測資料列存在、其中有多少筆帶有身分標籤
（provider／model_family／effort——#20 Phase 0）、有多少筆已成功
關聯（join）到實際結果（`actual_known`，即 ADR-046 所定義的誠實
join），以及 issue #11 記錄的三項擷取缺口中，哪些仍阻擋真正的校準：

1. **actuals 覆蓋率** — 結果事件需要回合關聯（turn correlation）
   （#1 的管線；目前在真實工作階段中，只有 `provider.turn.started`
   帶有 turn_id）；
2. **token 實際值** — 目前尚無任何 payload 帶有每回合的
   `total_tokens`（ADR-047 的階梯機制因同一原因處於休眠狀態）；
3. **樣本量** — 未達 ADD §15.2 門檻（8）的群組會被回報，但絕不會被
   拿去擬合。

一旦通過門檻，`report.py` 也會輸出各群組的預測值 vs. 實際值覆蓋率
（實際值是否落在 ≤ P50 / ≤ P80 / ≤ P90），這是仰賴回放（replay）
的校準證據——`Historical_Replay_Report.md` 當時無法產出的內容。

## 每回合實際值（observations 匯出）

狀態列（Statusline）的使用量總計是「工作階段累計」性質
（SESSION-CUMULATIVE，`total_cost_usd` 只會持續增加），因此
「這個回合花了 $0.12」是跨快照（snapshot）相減得出的——這是一個
建模步驟，Go 端的 bridge 拒絕承擔（capture-before-model 原則：
先擷取、不建模）。因此 `auspex export observations` 只提供原始
序列資料（usage／context／quota 快照）以及回合邊界事件，而
`calibration/observations.py` 才在「這裡」——也就是允許建模之
處——推導出差值。其歸因（attribution）方式明確是盡力而為
（best-effort）：

- 快照會落後於它們所量測的工作，因此介於某回合終止事件與下一回合
  開始之間的樣本，會被歸屬給已結束的那個回合；
- 若某回合沒有回合前（pre-turn）的基準樣本，則視為**無法推導**，
  絕不會假設其從 0 開始（已恢復的工作階段與因保留期限而截斷的
  序列，都會讓「基準值為 0」的假設變成一種捏造）；
- compaction（壓縮）可能會讓 `used_tokens` 縮小，因此**負值的
  上下文差值是真實存在的，會原樣呈現並附上說明——絕不會被靜默
  地限制（clamp）在 0**。

## 提示詞特徵萃取世代（`prompt_feature_version`，#50）

當 Phase 2 校準（#11/#20）開始取用 `provider.turn.started` 上衍生的
提示詞特徵訊號（動詞／領域布林值、計數、`prompt_approx_tokens`）時，
必須尊重萃取世代標記 `prompt_feature_version`（常數
`features.PromptFeatureVersion`，由 `internal/features` 的 codec 蓋上）。
這些訊號並非由一把長期穩定的尺量出來：

- **缺少此鍵的事件早於 #50 的標記機制**，並且混雜了多個萃取世代
  ——#42 之前（僅大小）、#42 的初版詞彙、#47 擴充後的詞彙——且無從
  分辨屬於哪一代。在舊詞彙下寫入的 refactor 提示詞，其
  `has_refactor_verb=false`，但同樣文字在今日會判為 `true`。切勿在
  「已標記／未標記」的邊界上默默混用已量測特徵；未標記事件應視為
  誠實地「世代未知」，絕不可當成當前世代。
- **`prompt_approx_tokens` 換的是尺，不只是提示詞。** bytes/4 估計值
  已改為 ADD §14.7 免 tokenizer 估計器（與加入特徵是同一次提交），
  因此原始值趨勢會在部署邊界出現一個反映「尺」而非「提示詞」的階梯
  變化（在 CJK、標點密集、空白密集的輸入上差異最大）。此外，§14.7
  不計空白，因此對「僅空白」的提示詞回傳 **0**，而 bytes/4 會回傳一個
  微小的非零值——舊有的 empty(0) 與 tiny(1) 之區別因而消失。估計器
  本身刻意不再更動（那會多出第三個未標記世代）；版本標記才是讓此
  不連續「可被偵測」的手段。注意此欄位目前依建構方式被排除於
  observations 匯出白名單之外（見 `internal/retention/observations.go`），
  因此尚無任何 research 消費端讀取它——本說明是未來一旦有消費端時的
  約束契約。

## 目錄結構

- `calibration/load.py` — JSONL 讀取器 ＋ schema 驗證
  （`auspex.calibration-export.v1`）。
- `calibration/observations.py` — JSONL 讀取器 ＋ schema 驗證
  （`auspex.observations-export.v1`），以及每回合成本／上下文差值的
  推導邏輯。可獨立執行（文字模式或 `--json`），並提供資料給
  report.py 的每回合實際值區段。
- `calibration/report.py` — 就緒程度 ＋（資料允許時的）覆蓋率報告；
  `--observations observations.jsonl` 會加上每回合實際值的就緒程度
  區段。輸出為純文字至 stdout；`--json` 則為機器可讀格式。

去識別化（de-identification）說明：匯出內容僅包含不透明的資料列 ID、
enum、數字與時間戳記（詳見 `internal/retention/export.go` 的套件註解；
observations 匯出是一種 payload 白名單（WHITELIST）投影，詳見
`internal/retention/observations.go`）。此目錄中的任何內容都不得被
用來反向關聯回提示詞（prompts）、路徑或身分——因為根本沒有可供關聯
的對象。
