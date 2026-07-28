# ADR-0055 — 執行期經驗校準啟動：hook 捕捉的 turn 餵入 cohort 階梯；四類經驗成本帶（#42／#66 item b）

> 🌐 [English](0055-runtime-empirical-calibration.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

Status: Accepted
Date: 2026-07-28
Owner: lead-executed（PROPOSAL——凍結契約變更，owner 於 merge 時簽核）
Tracking: issues #42 與 #66 item b；#20 Phase 2（`docs/backlog/provider-model-effort-features.md` §4）；透過 ADR-047 階梯消費 ADR-051 的捕捉；證據來自每週校準報告（2026-07-22／2026-07-28：成本上界低估中位數 7.0–7.3×、兩週穩定；token P90 只包住 56% 實際值；`claude/opus/xhigh` n=307 與 `claude/fable/xhigh` n=101 均已過 ADD §15.2 門檻）

## Context（背景）

兩個出貨中的預測缺陷同時被資料診斷、也被資料解鎖：

1. **Token 預測是 cold-start 常數（#42）。** ADR-047 的 cohort 階梯本是
   「休眠機器」設計——但 ADR-051 落地每回合 token 記帳之後它仍然休眠，
   原因有二，本 ADR 一併修正。其一，候選查詢只讀
   `provider.usage.observed`（managed run），從不讀 Stop hook 的
   `provider.turn.completed` 記帳——而匯出端的 token-actual join 兩者都讀
   （`tokenActualEventTypes`），造成校準端看得到、預測端看不到的遙測。
   其二，**候選池稀釋**：候選抓取先取該類型最新 N 筆事件、之後才過濾
   `total_tokens`，但帶 token 的列是少數（每一筆 statusline 快照都是不帶
   token 的 `usage.observed`）——在 owner 的真實資料庫上，最新 200 筆窗口
   只含 2 筆帶 token 的列，而庫裡有 200+ 筆真實捕捉的 turn。階梯在握有
   數百筆可用樣本的資料庫上餓死成 cold-start。

2. **成本帶對 cache 全盲（#66）。** 兩類帶只對 `total_tokens`
   （= fresh input + output）計價。已捕捉的四類實際值顯示 cache-read 佔
   重建帳單 ~56%，cache-盲視角整體少算 ~6.3×——實測為帶內率 6%、
   404 筆中 375 筆實際值在帶「上方」、各 cohort 上界殘差中位 7.0–7.3×、
   兩週報告穩定。ADR-043／#13 為此預留的消費者——
   `pricing.Table.FourClassCost`——存在但呼叫者為零。

修訂 ADR-044 凍結的 `app.FeatureDataSource` port（先例：ADR-047 本身）
與擴充版本化校準匯出形狀（ADR-052 觸發③）都需要 ADR——即本篇。

## Decision（決策）

1. **兩個 turn 級生產者都餵入階梯。** `cohortCandidates` 與 session rung
   同時讀取 `provider.usage.observed` 與 `provider.turn.completed`——
   與匯出端 token-actual join 相同的事件類型對,使預測樣本與校準實際值
   永不因事件類型選擇而分歧。每 turn 一筆候選,最新優先 latest-wins
   （鏡射匯出端的 re-entrant-Stop 規則）。以粗略的
   `LIKE '%"total_tokens"%'` SQL 預過濾修正池稀釋;最終仍由解碼後的
   JSON 決定。

2. **成本樣本的新 port 方法。**
   `app.FeatureDataSource.RecentSimilarTurnCosts`（對 ADR-044 凍結 port
   的加法式修訂）回傳 `features.SimilarTurnCosts`：近期同 cohort turn 的
   「已知」四類成本——每筆候選須帶齊 ADR-051 的四類、以其「自身」model
   經既有 `pricing.Table.FourClassCost` 計價。成本階梯是 ADR-047 階梯
   限縮到帶 model 的 rung（model+effort、model family）：美元樣本依
   family 計價，provider 全域與 session rung 永不回答——跨 family 的
   美元混合是無意義的帶，不是保守的後備。缺任一類的候選不供應成本樣本
   （以捏造的零 cache 類計價，低估幅度正是本機制要修正的 ~6×——
   unknown is not zero）。

3. **Pipeline 成本帶優先採用經驗估計器。** 同 cohort 成本樣本 >= 8 筆
   （同一 ADD §15.2 門檻常數）時,帶為其經驗 P50–P90,
   `CostRange.Source = "four-class-empirical"`（新增
   `pricing.SourceFourClassEmpirical`）;低於門檻維持 ADR-043 兩類帶,
   與先前逐位元相同。兩者都維持「未校準估計」標示。

4. **帶持久化、讀回逐字（migration 0064）。** `predictions` 新增可空的
   `cost_low_usd`、`cost_high_usd`、`cost_model_family`、`cost_source`。
   經驗帶取決於評估當下的 cohort 樣本——之後重算會顯示與 policy 階段
   比較值「不同」的數字,故 forecast card 與校準匯出逐字讀取持久值。
   0064 之前的列維持舊重算（對它們是精確的:其帶本為決定式）。匯出
   新增 `cost_source`（本 ADR 授權的 ADR-052 觸發③表面擴充）,且
   `report.py` 按估計器分層帶內率、並將 #72 Phase 2 殘差擬合限縮於
   兩類帶——絕不把兩代估計器平均成一個係數。

5. **Token 預測的 reason codes 進入持久列。** `RuleRiskCombiner` 只消費
   token 預測的數字,從不消費其 ReasonCodes,因此 ADR-047 的
   `TOKEN_COHORT_*` rung 揭露從未到達持久化 reason 集合——階梯不回答時
   看不出來,回答後就是不誠實（retry/progress 乘數殘留的
   `PREDICTION_COLD_START` 會被讀成全部真相）。evaluation service 現在
   將 `TokenForecast.ReasonCodes` 聯集進持久集合。

## Honest scope（誠實範圍）

- **經驗值不等於已校準。** 所有旗標維持 `Calibrated=false`,信心至多
  medium。對本地歷史取經驗分位數讓估計更銳利;機率宣稱仍需 ADD §15.6
  的 held-out 門檻（ECE/Brier）與其專屬 ADR（Constitution §3、§7 規則 7）。
- **這不是 M13 的 model artifact。** 對本地資料庫的執行期經驗分位數是
  ADR-019／M5 已核准的機制。沒有任何擬合係數燒進 binary;每個資料庫
  從自己的歷史回答、逐 cohort 遵守門檻。ADR-020 的 JSON model artifact、
  registry、held-out evaluator 仍是 M13 交付物,此處不預建（§31）。
- **牌價重建,不是實際花費。** 成本樣本以出貨的占位價目表計價
  （訂閱制邊際成本為 $0）——是消耗訊號,永遠標示 estimate。
- **Archived 列仍缺 token 實際值**（ADR-051 已接受缺口）:成本階梯只讀
  live 事件。0060–0069 範圍的 `calibration_samples` 加法式 migration
  仍是補該缺口的後續項。
- **Codex 維持兩類。** implicit-cache 公式（D-02）未建;codex turn 沒有
  explicit 四類分解,永不強行計價（#66 item b 的 implicit-cache 姊妹項）。
- **Statusline 不動。** #90 Phase A 已把每回合預測降出狀態列;D-15 的
  回歸條件（「gate on 校準或 cohort 樣本數」）如今有了 gate——持久列上的
  `TOKEN_COHORT_*` rung——但回歸本身屬於 #90 的 aggregate-first 策略,
  不屬本切片。

## Consequences（後果）

- 任何過門檻的資料庫上,token 基底從本地歷史回答並揭露 rung
  （owner 資料庫:輕量 prompt P50 由 3210 → ~5.6k、重 prompt ~6.6k–20k
  ——prompt 之間可見差異,即 #42 驗收）,且成本帶落在正確量級
  （同一 turn 實測 ~$2.1–$7.3 vs 兩類帶 $0.13–$1.28）。
- ADR-043 成本預算 policy 規則現在以務實量級的帶與宣告預算比較。
- `CONTRACT_FREEZE.md` 新增 Amendments 條目（port 方法 + DTO）;
  校準匯出 README 詞彙新增 `cost_source`。
- 每週報告的按估計器帶內率分層是成功指標:four-class-empirical 桶
  應包住多數實際值,而兩類桶過去只有 6%。

## Alternatives considered（曾考慮的替代方案）

- **把擬合的各 cohort 常數燒進 binary**（`fit.py` 產 Go 表）。否決:
  兩次擬合之間會漂移、把單一使用者的分位數出貨給所有資料庫、且繞過
  registry 與 held-out 門檻重複 M13 的 artifact 路徑。執行期分位數
  自我更新、逐資料庫遵守樣本門檻。
- **先預測四類、再計價**（各 cohort 類別份額 × token 預測）。本階段
  否決:真實資料上類別比例重尾極重（cache-read ÷ 可預測 total:
  中位 ~174–208×、P90 ~450–620×）,對「已知」每回合成本取帶才是穩健的
  第一個消費者。類別份額預測日後可在同一 `Source` 詞彙下取代之。
- **讀回時維持重算。** 否決:經驗帶是評估當下樣本的函數;重算會無聲
  顯示與 policy 階段比較值、與使用者所見不同的數字。
- **讓 provider rung 回答成本階梯。** 否決:混 family 就是混價位——
  opus 為主的池會以 family 費率比系統性低估 fable turn 的帶。
