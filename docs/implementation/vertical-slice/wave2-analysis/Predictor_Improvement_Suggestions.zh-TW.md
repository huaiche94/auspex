# 預測器改進建議

> 🌐 [English](Predictor_Improvement_Suggestions.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 值 |
|---|---|
| 階段 | 3.4 — Phase 2 後分析 |
| 目標 | 僅限規則預測器（Rule Predictor）層級（`Auspex_Predictor_Design_Supplement.md` 演進路線圖中的 Version 1） |
| 狀態 | 僅為建議。本文件未修改 `internal/predictor/**`。 |
| 依據 | 下列每項建議皆標註為**有實證依據**（evidence-based，根據 Phase 1/2 資料並附引用）或**推測性**（speculative，目前尚無 Auspex 執行資料——改為根據 ADD 自身已明訂的公式，或標註為未經測試） |

## 1. 本波資料能夠說明與無法說明的乘數

ADD 已經為（尚未建置的）Token 預測器（§15.2）與風險組合器（Risk Combiner，§16.2）明訂了一套乘數框架：`scope_multiplier`、`verification_multiplier`、`complexity_multiplier`（其中包含 `cross_layer`、`migration`、`security_sensitive`、`repository_wide` 等具名項）、`retry_multiplier`、`progress_multiplier`、`ambiguity_multiplier`。本波建置了範圍估算器（Scope Estimator，`predictor-05`）與續航預測器（Runway Forecaster，`predictor-06`），但未建置 Token 預測器或風險組合器（`predictor-05b`／`-05c`／`-07`，依 ADR-041 刻意延後）。因此，本波對於編碼代理（coding-agent）回合的 token 成本乘數**沒有任何直接的執行資料**——`Prediction_Error_Report.md` 中的資料集衡量的是*實作 Auspex 本身*的成本，而不是 Auspex 意圖預測的 Claude Code 回合成本（參見 `Calibration_Report.md` §5 的類別錯誤警示）。以下每項建議都會明確說明其究竟是根據本波的真實資料，還是在真正的編碼代理遙測資料出現之前的推測性建議。

## 2. 建議

### 2.1 身分驗證／安全敏感乘數

**推測性——無資料。** Phase 1/2 沒有任何節點涉及身分驗證或安全敏感的程式碼路徑，因此本波對於 ADD §16.2 中 `security_sensitive` 項的權重（目前 `blast_radius_risk` 公式中為 `0.15`）沒有任何正面或反面的證據。建議：在沒有證據之前，不要調整這個係數與 ADD 現有值的差異；將其標記為一個待觀察的校準目標，留待第一個涉及身分驗證相關工作的波次，並明確記錄該工作實際的範圍／重工情形以供此用途。

### 2.2 重試／重工乘數

**有實證依據，但衡量的是與 ADD 用語不同種類的「重試」。** ADD §15.2 的 `retry_multiplier` 模擬的是編碼代理在單一回合內重新嘗試失敗的工具呼叫或測試。本波的資料呈現的則是兩個**測試資料／實作重工**的案例——`claude-provider-03` 的 `unknown_category.json` 狀態碼不一致，以及 `predictor-03` 的關鍵字重疊測試提示（`Wave2_Lessons.md` §1，issue #5）。兩者都是被 `go test` 立即攔截、僅需一次迭代即可修正的問題，並非多回合的反覆掙扎。這是薄弱、間接的證據，說明存在一種「測試資料／規格不一致」的失敗模式，且在自動化測試攔截到時修正成本低廉——但它並不能直接校準 ADD 回合層級的重試乘數。建議：將其記錄為一個獨立且具名的風險訊號（例如未來的 `SPEC_FIXTURE_MISMATCH` 原因代碼），而不是併入現有的重試乘數，因為它的觸發原因不同（撰寫時的不一致，而非執行期失敗），緩解方式也不同（依兩個節點自身的建議，在撰寫測試資料與程式碼之前先建立共用的決策表）。

### 2.3 儲存庫專屬乘數

**無資料——標記為結構性缺口，而非係數建議。** 本波僅有 n=1 個儲存庫。`Calibration_Report.md` §4 已涵蓋此點：目前無法確認或排除任何儲存庫專屬的偏誤。建議：在至少有第二個儲存庫的資料之前，不要新增儲存庫專屬的乘數項；現在就新增等於是針對樣本數為一的資料調整係數，與純粹用猜的沒有分別。

### 2.4 上下文乘數

**推測性，但本波提供了一項具體的實作註記。** ADD §15.2 的 `context_multiplier` 公式（`1 + (current_context_tokens / context_window) × 0.5`）需要 `current_context_tokens`，這是一個 `domain.ContextObservation` 值——本波的 `claude-provider-04` 正規化器（normalizer）已經產生了這個值（`EventProviderContextObserved`），但 `predictor-05`／`-06` 目前尚未消費它（本波範圍之外）。建議：等到未來建置 `predictor-05b`（Token 預測器）時，確認 `ForecastTokensRequest` 的 `Scope domain.ScopeEstimate` 欄位是否足夠，或請求 DTO 是否需要新增 `ContextObservation` 欄位——這與 `predictor-05` 先前已遇過一次的同類契約缺口（`EstimateScopeRequest`，`Wave2_Lessons.md` §1，issue #2b）屬於同一類問題，值得提前檢查，而非等到節點進行中才重新發現。

### 2.5 不確定性調整

**有實證依據，且是本文件中最有力、最具體的建議。** `predictor-05` 的冷啟動（cold-start）處理（ADD §14.6 的表格僅列出 16 個任務類別中的 8 個）需要為另外 8 個類別發明一套「有文件記載的最近鄰對應」——這是一個真實執行過的、非推測性的設計決策。建議：在 `predictor-05b`／`-05c` 建置之前，將此模式正式化。具體而言：(a) 要求預測器管線中每一張冷啟動查找表都明確列出其涵蓋缺口，並為每個缺口說明最近鄰（或其他）備援規則，而不是讓缺口保持隱含；(b) 既然 `predictor-05` 已經為 `ScopeEstimate` 建置過這套模式一次，可考慮是否能將同樣的最近鄰邏輯抽取為 `internal/predictor/**` 底下的共用輔助套件，而非每個階段各自重新推導——這是一項「不要把同一個設計決策重複做三次」的觀察，而非係數調整建議。

### 2.6 整合測試乘數

**無資料——唯一可用的訊號是 ADD 自身的公式。** 本波沒有任何節點觸發 `integration_tests` 項（ADD §16.2 的 `completion_risk` 公式將其權重設為 `0.12`；§15.2 的 `verification_multiplier` 將其權重設為 `0.45`）。建議：不做變更；這個係數從未被 Auspex 自身的執行所驗證過，因此本波的資料中沒有任何東西可用來校準它。給未來波次的提醒：`qa-01` 的 CI 矩陣與 `qa-02` 的 E2E 測試（皆尚未建置）本身就是整合測試密集的節點，一旦建置完成，會是很自然的第一批真實資料點。

### 2.7 跨平台／作業系統條件邏輯乘數（新建議）

**有實證依據，n=2，依 `Calibration_Report.md` §6 明確標記為低信心。** 本波資料集中兩個需要作業系統條件邏輯的節點（`foundation-02` 的路徑解析、`foundation-04` 的行程存活檢查）都揭露了同等名目複雜度的同平台工作所沒有出現的真實錯誤或無可避免的檔案拆分。這不是 ADD 現有具名乘數項之一。建議：考慮是否應在 `domain.ScopeEstimate` 或 `ScopeEstimator` 的特徵輸入中加入一個 `cross_platform` 布林訊號，類比於 `MigrationLikely`／`SecuritySensitive`——但在至少再有一個波次的資料之前不要新增，因為 n=2 尚不足以支撐新增一個凍結契約欄位。在此記錄下來是為了不讓這個假設在波次之間遺失，而不是作為可立即實作的建議。

## 3. 本文件明確排除的範圍

依 Phase 3.4 的指示，撰寫本文件並未變更 `internal/predictor/**`、`internal/features/**` 或 ADD §15/§16 虛擬碼中的任何係數值、公式或程式碼。以上每一項「建議」都屬於 (a)「在更多資料出現前先不要動」、(b)「在下一波次觀察這個特定事項」，或 (c)「這是一個值得重複使用的設計模式」——沒有一項是要求變更凍結數值的指示。
