# Backlog — 將 Provider / Model / Effort 作為 Prediction 輸入

> 🌐 [English](provider-model-effort-features.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 值 |
|---|---|
| 狀態 | **Phase 0–1 已落地**（2026-07-14）——擷取（#20 Phase 0）與 cohort 篩選（ADR-047）已上線；**Phase 2–3 待辦**（經驗校準，卡在 #11 資料；Codex adapter 接線） |
| 追蹤 | Issue [#20](https://github.com/huaiche94/auspex/issues/20)；排序決策見 `docs/DECISION_LOG.md` D-10 |
| 起源 | Owner 於 2026-07-13 提出的要求：「這個專案是否有考慮到不同家使用 claude(model, effort), codex(model, reasoning, speed) 當作參數來做預測公式/模型」——稽核後發現答案是*沒有*，本文件即為對應的 todo |
| 相關 | `Auspex_ADD.md` §15.2/§15.3、ADR-041（forecast 層）、ADR-043（multi-resource runway）、DECISION_LOG D-02（第二個 provider 這條線已延後）、issue #13、#11 |
| 立基紀律 | 與 `Predictor_Improvement_Suggestions.md` 相同：沒有資料就不提出係數方案。本文件僅提出**擷取與 cohort 機制**；每一項數值決策都延後到各 cohort 有樣本之後再說 |

## 1. 問題

Prediction 管線（Scope → Token Forecast → Quota Forecast → Risk，ADR-041）**對 provider、model 與 effort 一無所知**。最直接決定一個 turn 的 token 消耗量、延遲與美元成本的參數，完全不是任何公式的輸入：

- **claude**：model（opus／sonnet／haiku／fable）、reasoning effort（low／medium／high／xhigh／max）、fast mode
- **codex**：model、reasoning level、speed setting

同一個 prompt 在 `haiku` + low effort 與 `fable` + max effort 下執行，輸出 token 數可能相差一個數量級，美元成本的差距甚至更大——但目前兩者產生的 forecast 卻完全相同。

## 2. 現況（稽核於 2026-07-13）

三個不同的層次，各自有不同程度的「有被納入考量」：

| 參數 | 是否已設計？ | 是否已擷取進 schema？ | 是否已用於公式？ |
|---|---|---|---|
| provider（claude/codex） | 有——ADD §15.2/§15.3 cohort：「依 provider/model/task class」 | 有——`provider_sessions.provider` | **沒有** |
| model family | 有——同一個 cohort 定義 | 部分——`provider_sessions.model`（nullable，**session 層級**） | **沒有**——`internal/pricing/pricing.go` 有逐 model 的費率，但只有 forecast-card 的呈現層使用它們（渲染時的成本顯示） |
| effort / reasoning / speed | **沒有——整個設計文件體系中完全缺席** | 沒有 | **沒有** |

證據：

- `internal/predictor/token/forecaster.go`——`RecentSimilarTurnTokens` 這個 port 的註解，將理想的 cohort 定義為「provider + model family + task class + repository」（ADD §15.2），但實作完全沒有依這些條件篩選。
- `internal/evaluation/datasource_sql.go`（`RecentSimilarTurnTokens`）——實際的 cohort 是「這個確切 session 的近期 usage observation」；註解誠實記載了這項範圍縮減：usage 事件不帶有 task-class 標籤，因此完整的 cohort 被縮減了範圍。
- `internal/predictor/quota/coldstart.go`——quota 差值為硬編碼（P50 = 2.0 pp，P90 = 6.0 pp）；ADD §15.3 步驟 5 的「依 provider/model/task class 計算 empirical P50/P90」被明確標記為本波次無法觸及。
- `internal/storage/sqlite/migrations/0041_predictions.sql`——持久化的 prediction **沒有**任何 provider/model/effort 欄位，因此歷史 prediction 事後無法依 model 分層以供校準使用。
- Telemetry 會儲存 `reasoning_tokens`（FR-020，ADD §11.12）——但這是作為**結果（outcome）**量測值，從來不是輸入特徵。
- Codex 目前只出現在 `internal/statecheckpoint` 的測試 fixture 中；第二個 provider 這條線已依 DECISION_LOG D-02 延後。

**結論：** provider/model 的 cohort *已經設計但尚未實作*；執行參數這個維度（effort/reasoning/speed）*連設計都還沒有*。本文件將這個缺失的維度加入設計介面，並為其實作排序。

## 3. 設計限制（從稽核中得出，對任何實作皆具拘束力）

1. **Turn 層級，而非 session 層級。** Model 與 effort 會在 session 中途改變（`/model`、`/fast`、effort 切換）。`provider_sessions.model` 是錯誤的存放位置；擷取動作必須落在 turn 層級的 usage observation 上，並複製到每一筆持久化的 prediction 資料列。Session 層級的欄位會在使用者不知情的情況下錯誤分配 cohort。
2. **先擷取，後建模。** 在這些欄位被記錄下來之前，不進行任何公式方面的工作——這是本 repo 自身的紀律（`Predictor_Improvement_Suggestions.md` §2.3：在 n≈0 的情況下調參，與純粹猜測沒有分別）。每多一天沒有擷取這些欄位，就多累積一天校準（#11）永遠無法追回的無標記歷史資料。
3. **跨 provider 正規化。** 使用單一特徵三元組 `(provider, model_family, effort_class)`，並在旁保留 provider 專屬的原始值。Claude 的 effort tier 與 codex 的 reasoning/speed 設定，會對應進一個小型的共用 `effort_class` 列舉；原始字串則保留供稽核與重新對應之用。對應表屬於凍結合約的議題（ports/domain），因此在實作時會以 ADR 處理。
4. **Cohort 稀疏性需要後備階梯。** 新增維度會讓 cohort 數量倍增；`MinSimilarSamples`（ADD §15.2 的 cold-start 門檻）在早期將很少能被滿足。查詢必須明確地降級：精確 cohort → 捨棄 effort → 捨棄 model → session-recent（目前的行為）→ ADD §14.6 的 cold-start 預設值，並以 `Confidence`/reason codes 反映是哪一個 rung 給出答案。
5. **Prediction 必須持久化其特徵。** 在 `predictions` 資料表中新增 provider/model/effort 欄位，讓 #11 能夠依 cohort 對殘差進行分層分析。

## 4. 分階段 TODO

- [x] **Phase 0 — 擷取**（已於 2026-07-14 落地；#20 的擷取切片）：
  - [x] Turn 層級的身分已被擷取：statusline snapshot 餵入 `provider_sessions.model` + `effort`（migration 0005，COALESCE 式的「最後寫入者為準」——即解析快取），Stop-hook 的 payload 會將 turn 結束時的 `effort` 標記到 `provider.turn.completed` 事件上（hooks.md：hook payload 不帶 model 欄位，因此事件層級的三元組僅有 effort；**完整**的三元組存在於 prediction 資料列上，這正是校準 join 所依據的介面）。
  - [x] `predictions` 資料列持久化 `(provider, model_id, model_family, effort)`——migration 0046，於 EvaluateTurn 當下、依 session 最新觀測到的身分標記；若從未被觀測到則為 NULL。
  - [x] Forecast card 的成本估計會解析出已標記 model 所屬的價格 family（fable/mythos/opus/sonnet/haiku——fable/mythos family 與現行世代 opus 的價格已加入預設價目表），CostRange 標籤也會如實標示；只有在身分從未被觀測到時，才會退回 DefaultFamily。
- [x] **Phase 1 — cohort 篩選**（已於 2026-07-14 落地，ADR-047）：`RecentSimilarTurnTokens` 實作了 §3.4 的後備階梯（provider+family+effort → provider+family → provider → session-recent，由第一個滿足 §15.2 ≥8 門檻的 rung 給出答案；turn 端未被標記的 rung 會被跳過，絕不會被當作空集合也算相符），並回傳究竟是哪個 rung 給出答案；forecaster 對每一個經驗基準都會發出一個 `TOKEN_COHORT_*` reason code。Usage observation 現在帶有 `model_id`/`effort` 的 payload 標記（observation 粒度的擷取——這是 Phase 0 事件標記所遺漏的樣本端那一半）。Task class ＋ repository 仍誠實地被排除在此階梯之外（在樣本介面上依然缺席）；依 ADR-047 的「誠實範圍聲明」，在出現 total-token payload 欄位之前，此階梯處於休眠狀態。
- [ ] **Phase 2 — 經驗校準**（受阻於 Phase 0 ＋ #11 的資料）：
  - [ ] 以逐 cohort 的 quota 差值取代 `coldstart.go` 中的常數（ADD §15.3 步驟 5）。
  - [ ] 逐 model/effort 的 token 分位數，餵入倍率模型，或在樣本數足夠時逐 cohort 取代該模型。
  - [ ] 逐 model 的價目表成為一項 *forecast* 輸入（ADR-043／#13 的成本軸），而不只是渲染時的顯示用途。
- [ ] **Phase 3 — codex adapter 接線**：D-02 延後的第二個 provider 這條線；將 codex（model、reasoning、speed）對應進正規化三元組，並驗證 cohort 機制在非 claude provider 上依然成立。

## 5. 驗收標準（用於結案 #20）

- 一個 turn 的 model + effort 會以 turn 粒度被記錄下來，並且在持久化的 prediction 資料列上可見。
- `RecentSimilarTurnTokens` 依正規化三元組篩選，並具備明確、附帶 reason code 的後備階梯。
- 一旦樣本數通過門檻，quota 差值與 token 分位數的查詢即以逐 cohort 方式進行；門檻以下時 cold-start 行為維持不變。
- Cost forecast 使用已觀測到的 model 的價目表，並如實標示此點。
- Codex 的參數透過相同的路徑流動，predictor 端不需要任何特殊處理。

## 6. 非目標

- 本文件不提出任何倍率係數或 effort 分層權重——目前沒有資料可以支撐它們（見上方的立基紀律）。
- 本文件不決定 codex adapter *何時*落地——這仍是 D-02 的決定，將在其自身的決策時點重新檢視。
- 本文件不對任何凍結合約做出變更；合約變更需伴隨其實作用的 ADR 一併提出（依 Constitution §3）。
