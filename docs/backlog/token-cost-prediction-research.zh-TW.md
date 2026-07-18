# Backlog — Token 成本預測：以研究為依據的路線圖

> 🌐 [English](token-cost-prediction-research.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 內容 |
|---|---|
| 狀態 | **Backlog / TODO** —— 已記錄，尚未排入目前波次（phase） |
| 追蹤 | Issues [#65](https://github.com/huaiche94/auspex/issues/65)（Phase 1）、[#66](https://github.com/huaiche94/auspex/issues/66)（Phase 2）、[#67](https://github.com/huaiche94/auspex/issues/67)（Phase 3）、[#68](https://github.com/huaiche94/auspex/issues/68)（Phase 4），2026-07-14 建立；排序在 #11（校準）、#13（成本軸）、#20（provider／model／effort）之後 |
| 來源 | 擁有者請求，2026-07-14：完整讀完 arXiv:2604.22750，並要求把其發現納入路線圖 |
| 相關 | `Auspex_ADD.md` §14–§17、`../design/Auspex_Predictor_Design_Supplement.md`（「External Evidence」）、ADR-041（forecast 層）、ADR-043（多資源 runway／成本軸，#13）、ADR-047（cohort fallback ladder）、issues #11、#14、#20、#42 |
| 紮根（grounding）原則 | 與其他 backlog 筆記相同：**沒有資料就不提出係數。** 論文的數字在此僅作為*外部先驗（prior）與設計依據*，是在 SWE-bench 上、對其他模型量測得到的——絕不當成 Auspex 自身世代（cohort）的擬合係數。公式以我們自己的符號重寫；文字為轉述，非照抄。 |

## 1. 來源

> Longju Bai、Zhemin Huang、Xingyao Wang、Jiao Sun、Rada Mihalcea、Erik
> Brynjolfsson、Alex Pentland、Jiaxin Pei。*How Do AI Agents Spend Your
> Money? Analyzing and Predicting Token Consumption in Agentic Coding
> Tasks.* arXiv:2604.22750（v2，2026-04-29）。
> <https://arxiv.org/abs/2604.22750>

這是一份對八個 frontier LLM 執行 SWE-bench 軌跡的系統性研究：量測 token
（與金錢）實際花在哪裡，並評估模型能否在執行*前*預測自己的 token 成本。
它幾乎就是替 Auspex 所做的事做了一次可行性研究——而其結論正好落在 Auspex
早已押注的那一側。

## 2. 論文確立了什麼（外部證據，轉述）

以下數字皆為論文在 SWE-bench 上、對其他模型的量測結果。它們是**先驗與
依據**，不是 Auspex 的係數。

**預測本質上就很難（支撐 uncalibrated 立場）：**

- *同一個*任務的不同執行，總 token 可差到 **30×**；成本最高的任務跨執行
  變異最大。論文自身的結論就是：預測 token 用量與 agent 定價本質上困難。
- 模型對自身 token 用量的預測只有**弱到中等**（Pearson ≤ **0.39**，最佳
  是 Sonnet 4.5 的 *output* token；*input* 更差），且**系統性低估**真實
  用量——input 尤其嚴重。
- 專家評定的難度與真實消耗只有弱相關（Kendall **τ_b = 0.32**）：6.7% 被
  標為「< 15 分鐘」的任務，比平均「> 1 小時」的任務還貴；11.1% 的
  「> 1 小時」任務，比平均「< 15 分鐘」還便宜。表面感知的難度不是可靠的
  成本代理指標。

**錢花在哪（支撐 cache-aware 成本模型）：**

- Agentic 編碼的成本約為單輪 reasoning 呼叫的 **3500×**、多輪 chat 的
  **1200×**，且成本由 **input** token 主導（平均 input/output 比約
  **153**）。
- 在顯式快取定價（Claude）下，四類 token 分別計價，而 **cache-read token
  在每個 phase 都是金額佔比最大的一類**——即便 output 的單價約為
  cache-read 的 80 倍——純粹因為累積的 context 被反覆重讀太多次。因此
  「總 token × 平均單價」的成本模型會實質失真。
- 更多 token 買不到更高準確度：準確度在**中等成本達到峰值後飽和甚至下降**。
  高成本區間往往是 agent 在原地打轉，而非更努力。

**可觀測的失敗訊號（支撐「觀測而非預測」的 gating）：**

- 高成本、失敗的執行有一個明確的行為特徵：**對同一檔案反覆 `view`／
  `modify`**。低效模型約 **50%** 的檔案操作是重複觸碰同一檔案；高效者
  （GPT-5）則遠低於此。論文將其解讀為冗餘的來回操作，膨脹 context 與 token
  卻沒有對等進展。
- 模型缺乏「這任務不可解——該停」的內建機制：會持續探索、重試、重讀
  context，累積成本卻無進展；這種超額花費的大小是模型特定的。
- 每條軌跡可拆成五個 phase——Setup ~10%、Explore ~30%、Fix ~34%、
  Validate ~17%、Closeout ~10%——各 phase 的主導成本來源不同（Setup 由
  output 主導的規劃；Explore 轉為 input 主導的讀檔；Fix/Validate 為混合）。
  成本的*形狀*取決於 phase。

## 3. 設計啟示 → 路線圖項目

依論文提供的三類價值，對應三組工作。

### A. 支撐誠實呈現面的先驗（rationale，低成本）

30× 變異、≤0.39 的自我預測上限、系統性低估、τ_b = 0.32，是 Auspex 既有
紀律最強的外部背書：uncalibrated 分數、寬區間、絕不把分數叫作機率
（Constitution §7 第 7 條、#42）。兩個具體後果：

- forecast 應給 **input-token 比 output-token 更寬的區間**，因為 input 既
  是成本主導、又是較難預測的一軸。目前的單一乘數並未區分兩者。
- 未來若走自我預測路線（讓模型估自己的成本），必須內建**向上偏誤修正**，
  因為模型天生報低。

*本次先落地：* 這些數字已作為依據記入 predictor 補充文件的「External
Evidence」段與 README 誠實但書（本次變更）。區間加寬本身屬 Phase 1。

### B. Cache-aware 成本模型（ADR-043／#13 的成本軸）

forecast card 目前的成本估算本質上是「總量 × 各模型單價」。論文 Appendix B
的拆解——以我們自己的符號重寫，非照抄——分別為四類計價：

```text
顯式快取 provider（Claude 類）：
  non_cached_input = total_input − cache_read
  turn_cost = non_cached_input · r_in
            + output          · r_out
            + cache_creation  · r_cache_create
            + cache_read      · r_cache_read

隱式快取 provider（GPT-5 類）：
  non_cached_input = total_input − implicit_cache_read
  turn_cost = non_cached_input     · r_in
            + implicit_cache_read  · 0.2 · r_in   （快取讀取 ≈ 基礎 input 的 ⅕）
            + output               · r_out
```

要帶進模型的關鍵洞察：**估「錢」等於估這個 session 的 context 會長到多大、
會被重讀幾次**，而不是估 output。這正是 ADR-043／#13 已預留的成本維度；它
需要的就是這套拆解與各類單價表。

### C. 可觀測的執行期訊號（觀測，而非預測）

論文最能立即行動的禮物：不需預測 token 數、僅靠觀測既成事實就能抓到危險
turn 的訊號。

- **重複檔案操作 risk factor。** 追蹤每個 turn 對同一檔案 `view`／`edit`
  的重複率；超過門檻即是「此 turn 正在原地打轉」的強觀測訊號。把它接成
  `RiskCombiner` 的輸入並帶自己的 reason code，可觸發 `WARN` /
  `CHECKPOINT_AND_RUN`。這是 Auspex 真正的工程貢獻，不是複製——論文提供
  證據，機制由我們實作。
- **不可解即停 gate。** 模型所缺的「不收斂就停」機制，正是 Auspex 的職責：
  把重複率訊號（以及缺乏 progress-tree 證據）納入 pause/checkpoint 決策。
- **Phase-aware 條件式預測。** 若 Auspex 能推斷目前所處 phase（Setup／
  Explore／Fix／Validate／Closeout），就能條件式地預測接下來成本的*形狀*，
  而非給一個無條件的平均數——用同一組特徵做更好的事。

## 4. 分階段 TODO

- [x] **Phase 0 — 依據落地**（本次變更）：在 predictor 補充文件
  （「External Evidence」）與 README 誠實但書引用本論文；建立本路線圖筆記。
  不改公式、不改程式碼。
- [ ] **Phase 1 — input/output 區間分離**
  （[#65](https://github.com/huaiche94/auspex/issues/65)）：讓 token
  forecast 給出各自的 input／output 區間，input 更寬（§3.A）。方向由論文
  佐證；*幅度*仍待 #11 資料。
- [ ] **Phase 2 — cache-aware 成本拆解**
  （[#66](https://github.com/huaiche94/auspex/issues/66)；§3.B；ADR-043／
  #13 的成本軸）：四類 turn 成本搭配各模型 cache 單價表；受阻於尚未擷取
  每輪 `cache_read`／`cache_creation`（目前僅擷取 `total_tokens`，ADR-047）。
- [ ] **Phase 3 — 重複檔案操作 risk factor**
  （[#67](https://github.com/huaiche94/auspex/issues/67)；§3.C）：需要目前
  尚未擷取的 turn 級工具操作遙測（各檔案 view/edit 次數）；再接
  `RiskCombiner` 輸入 + reason code + policy 對應。**詳細 capture-step 設計見 §7。**
- [ ] **Phase 4 — phase 推斷 + 不可解即停 gate**
  （[#68](https://github.com/huaiche94/auspex/issues/68)；§3.C）：推斷軌跡
  phase；條件式預測；把重複率 + 無進展納入 pause/checkpoint 決策。

Phase 2–4 都需先有擷取步驟才能建模——與 predictor 其餘部分相同的
「capture-before-model」原則。

## 5. 非目標（Non-goals）

- **不把論文的數字當成 Auspex 係數。** 30×、0.39、τ = 0.32、比值 153、
  各 phase 百分比——都是在 SWE-bench 上、對其他模型量測的。它們佐證的是
  *方向與形狀*，絕非某個擬合的 Auspex 門檻。擬合數字仍只來自 #11 資料。
- **不在里程碑閘門之前實作**（ADD §31）。本筆記是路線圖記錄；每個 phase
  各自帶 issue 落地，若觸及凍結契約，則帶各自的 ADR。
- **不照抄論文的文字、表格或圖。** 公式屬事實，以我們自己的變數命名與定價
  面重寫；文字為轉述並註明出處。

## 6. 出處與致謝（Attribution）

§3.B 的成本拆解是我們以 Auspex 變數命名，對來源 Appendix B（arXiv:2604.22750）
定價恆等式的重新表述；公式不受著作權保護，且未重製任何文字或表格。§2 的所有
量化陳述皆轉述自同一來源並註明出處，並非以 Auspex 的量測結果呈現。

## 7. Phase 3 — capture-step 設計（#67）

擁有者決定，2026-07-15：重複檔案操作 risk factor 的 capture step 採
**native 優先**（Claude Code `PostToolUse` hook），先做**回溯式**消費。本節
是設計；不隨此變更寫任何 code（里程碑閘控，ADD §31）。

### 7.1 這一步要補的缺口

**今天沒有任何來源**能觀測到每檔案的工具操作：

- Native hooks 只註冊 `UserPromptSubmit` / `Stop` / `StopFailure` /
  statusline（`integrations/claude/hooks.json`）——**沒有 PostToolUse**，所以
  native session（每日 dogfood 的路徑）從來看不到工具操作。
- Managed run 的 stream *有*帶 `tool_use` block
  （`claude -p --output-format stream-json --verbose`，
  `internal/managed/run.go`），但 `internal/managed/stream.go` 只數
  `AssistantLines`、從不解碼它們——而且只覆蓋 `auspex run`。

所以這是**新增一個擷取來源**，不是接線——與 token actuals 當初碰到的
managed 有／native 沒有的不對稱一模一樣（ADR-047）。

### 7.2 來源：native PostToolUse（主力）

新增一個 hook entrypoint `auspex hook claude <post-tool-use>`，並在
`hooks.json` 註冊。Claude Code 會對每次工具呼叫觸發 PostToolUse，帶
`tool_name` 與 `tool_input`。碰檔案的工具就是訊號：

- **view** — `Read`
- **modify** — `Edit`、`Write`、`MultiEdit`、`NotebookEdit`

managed-stream 那條——在 `stream.go` 解碼同一批 `tool_use` block——維持為
選用熱身：它能在現成流動的資料上驗證機制，但真實覆蓋稀疏（只有
`auspex run`）。此處不排程。

子指令大小寫沿用 #61（REC-03）對 hook 子指令的決定——這個新 entrypoint
不得搶先那份 ADR。

### 7.3 擷取什麼（隱私優先：只存聚合，絕不存路徑）

原始檔案路徑具識別性，**絕不持久化**（匯出紀律：任何東西都不得回連到
prompt、路徑或身分）。所以 hook 在*行程內*把路徑化約成每 turn 的聚合，
只持久化計數：

- `distinct_files_touched` — 本 turn 觸碰的相異路徑數
- `total_file_ops` — view+modify 操作總數
- `repeated_ops` — 對「被觸碰超過一次的檔案」的操作數
- `repeat_rate` = `repeated_ops / total_file_ops`（`total_file_ops = 0` 時為 nil）
- `max_ops_on_one_file` — 單一檔案的最嚴重 churn

路徑會被 intern 成每 turn 的不透明序號以供計數，然後丟棄；不會有任何路徑
字串離開行程。（缺席保持誠實：每個欄位皆「unknown 而非 zero」，同
observations 匯出的指標指標作法。）

### 7.4 資料路徑

```text
PostToolUse hook (tool_name, tool_input.file_path)
  -> 每 turn 聚合（intern 序號，只留計數）                       [7.3]
  -> 把聚合掛在該 turn 的 terminal event payload
     (provider.turn.completed —— managed token actuals 用的同一個載體)
  -> events 儲存（既有 JSON payload map）
  -> 把這五個欄位加進 observations 匯出白名單
     (internal/retention/observations.go)，讓 #11／研究看得到
  -> 上游：由近幾個完成 turn 的 repeat_rate 衍生回溯式 execution-risk
     指標（session 特徵，同 RecentSimilarTurn）-> 新的 ScopeEstimate
     ReasonCode + 純量
  -> RiskCombiner 讀它 (ports.go CombineRiskRequest 是 stateless；
     訊號在上游算好，combiner 從不自己查詢)
```

聚合避免了每次工具呼叫都持久化一個事件——高頻事件型別會與 ADR-046
保留策略衝突。每 turn 聚合只是在一個已存在的事件上多掛一塊 payload。

### 7.5 消費：先回溯式

Auspex 在**執行前（pre-turn）**把關；repeat rate 是**turn 執行中**才能觀測到
的事實，因此無法用於同一個 turn 的把關。第一種消費是**回溯式**：近幾個
turn 的 repeat_rate 抬高**下一個** turn 的 execution-risk（貼合現有 pre-turn
gate）。**即時 turn 內中斷**——在執行中偵測打轉並 pause/checkpoint——是更強
的用法，屬於 **#68**（不可解即停 gate），不在此處。

### 7.6 觸及的凍結契約（實作時 → ADR）

- 新的 hook 子指令（與 #61 協調）
- `provider.turn.completed` 的新 payload 欄位
- observations 匯出白名單的新欄位（`auspex.observations-export.v1`）
- 新的 `ScopeEstimate` ReasonCode

每一項都是凍結契約面，因此實作各自帶 ADR 落地（Constitution §3）。

### 7.7 切片

- [ ] **3a — 擷取**：PostToolUse hook + 每 turn 聚合 + 掛上
  `provider.turn.completed` + observations 白名單。純擷取；不接 risk。
- [ ] **3b — 回溯式 risk**：對近期 repeat_rate 的 session 特徵 →
  `ScopeEstimate` ReasonCode → `RiskCombiner` 的 execution-risk → policy。
- [ ] **3c — 門檻**：由真實資料校準 repeat_rate 門檻（data-gated，同 #11——
  在那之前沒有數字）。

### 7.8 延後／非目標

- 現在不定門檻值（capture-before-model；3c 等資料）。
- 不做即時 turn 內中斷（那是 #68）。
- managed-stream 那條是選用熱身，此處不排程。
- 永遠不持久化路徑字串（§7.3）。
