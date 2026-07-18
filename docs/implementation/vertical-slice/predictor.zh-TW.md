# Predictor — 垂直切片進度產出物

> 🌐 [English](predictor.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

角色包（role packet）：`agents/predictor.md`。凍結合約：`docs/implementation/vertical-slice/CONTRACT_FREEZE.md`。
分支：`vertical-slice/predictor`。本波（phase）僅涵蓋依凍結執行 DAG、由 `contract-integrator` 解除阻塞的根節點：
`predictor-02`、`predictor-03`、`predictor-04`。

`predictor-01`（SQLite migrations 0040-0049）**不**在本波範圍內——它依賴尚未完成的 `foundation-06`
（核心 SQLite migration harness）。依 Constitution §6/§4 與該 DAG，此節點列入排隊，既非跳過，也非以其他項目替代。

---

```yaml
node: predictor-02
status: completed
artifacts:
  - internal/features/doc.go
  - internal/features/prompt.go
  - internal/features/prompt_test.go
validation:
  - "gofmt -l internal/features  # clean"
  - "go build ./internal/features/...  # ok"
  - "go vet ./internal/features/...  # ok"
  - "go test ./internal/features/... -run PromptFeatures -v  # PASS (7 tests, incl. reflection-based no-raw-text-leak assertion)"
commit: 4c22e0b
next_action: predictor-01（因 foundation-06 受阻，本波未開始）；predictor-05（因 predictor-03/-04 鏈尚未完成而受阻，將於下一波繼續）
assumptions:
  - "提示路徑偵測（Prompt-path detection，ExplicitPathCount）採用輕量啟發式（含有 '/' 或已知程式碼副檔名）；此為供下游 scope estimation 使用的訊號，並非路徑安全控管。"
  - "ApproxTokens 完全依照 ADD §14.7 的公式計算；TokenConfidence 恆為 domain.ConfidenceLow，因為這是不使用 tokenizer 的近似值，從不精確。"
blockers:
  - "predictor-01 因等待 foundation-06 而受阻"
```

```yaml
node: predictor-03
status: completed
artifacts:
  - internal/features/taskclass.go
  - internal/features/dto.go
  - internal/features/classifier.go
  - internal/features/classifier_test.go
validation:
  - "gofmt -l internal/features  # clean"
  - "go build ./internal/features/...  # ok"
  - "go vet ./internal/features/...  # ok"
  - "go test ./internal/features/... -run Classifier -v  # PASS (11 subtests, incl. explicit-unknown-on-insufficient-signal cases)"
  - "go test ./internal/features/... -v  # PASS, full package (predictor-02 tests unaffected by predictor-03 additions)"
commit: 6ed8657
next_action: predictor-01（因 foundation-06 受阻，本波未開始）；predictor-05（因 predictor-03/-04 鏈尚未完成而受阻，將於下一波繼續）
assumptions:
  - "RepositoryFeatures／SessionFeatures／ProgressFeatures 的欄位集合是本角色自行設計的（agents/predictor.md 交付項 2 將『確切欄位』留給負責角色決定）；它們遵循 ADD §14.2 的四個 feature-source 類別，以及 CONTRACT_FREEZE.md 的『未知即為 nil 指標』規則，而不是源自 internal/app/ports.go 本身的凍結 DTO。"
  - "ClassifyTask 的規則優先順序（security > migration > long-doc > …）是第一天就採用的啟發式順序，並非源自任何凍結文件；此順序已在程式碼內以註解記錄，並由 golden-style 表格測試涵蓋，因此未來若有變動會以 diff 的形式顯現，而不是悄悄改變行為。"
  - "當 ApproxTokens < 2，或提示文字既無可執行動詞、也無領域指標、也無 progress-tree 文件段落線索時，會被歸類為 TaskClassUnknown——此門檻是保守的暫定值，唯一的調校依據是『unknown 不可以是用猜的』這項要求。"
blockers:
  - "predictor-01 因等待 foundation-06 而受阻"
```

```yaml
node: predictor-04
status: completed
artifacts:
  - internal/predictor/doc.go
  - internal/predictor/quantile.go
  - internal/predictor/quantile_test.go
validation:
  - "gofmt -l internal/predictor  # clean"
  - "go build ./internal/predictor/...  # ok"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -run QuantileMonotonic -v  # PASS (16 degenerate-input subtests + 2000-trial random property test)"
  - "go test ./internal/predictor/... -v  # PASS, full package"
commit: 3bbd49f
next_action: predictor-01（因 foundation-06 受阻，本波未開始）；predictor-05／predictor-06（因 predictor-03／predictor-04 而受阻，於下一波進行）
assumptions:
  - "EmpiricalQuantiles 在最接近的兩個 rank 之間採用線性內插（即『type 7』估計法，與 R／NumPy 預設採用的是同一族方法），而非採用 nearest-rank 法；ADD §15.2／§14.1 除了『empirical quantiles』之外並未進一步指定，因此這是一項已記錄在案的實作選擇，並由 TestQuantileKnownValues 固定其行為。"
  - "Quantiles{} 的零值（P50=P80=P90=0，SampleCount=0）是刻意設計的『空輸入』哨兵值；呼叫端必須依 SampleCount==0 分支判斷，不可把 0 當作實測結果（這是在呼叫端層級呼應 ADD『unknown 不等於 0』的原則，儘管這個底層工具函式本身依其狹窄的職責範圍回傳的是單純的 float64，而非 *float64）。"
blockers:
  - "predictor-01 因等待 foundation-06 而受阻"
```

## 波次總結

本波指派的三個節點（`predictor-02`、`predictor-03`、`predictor-04`）皆已 `completed`，具備可留存的產出物（已提交的檔案）且驗證指令全數通過，位於 `vertical-slice/predictor` 分支，尚未 push，也尚未 merge。依該 DAG 與 Constitution §6/§7，`predictor-01`（因 `foundation-06` 受阻）以及 `predictor-05` 及之後的節點（因本波節點需先完成才能銜接下一波）均未開始任何工作。

本波未觸碰 `internal/policy/**` 或 `internal/evaluation/**`——依指示，這些路徑維持不變。

---

## 第 2 波（predictor-05、predictor-06）

分支：`vertical-slice/predictor`，在本波開始前已由 lead 以 fast-forward 方式 merge 至 `main @ 4f96d7f`（Bootstrap + 第 1 波整合 + ADR-041）。依指示，開始前重新閱讀了 CONSTITUTION.md、CONTRACT_FREEZE.md 的「Predictor pipeline ports (ADR-041)」章節、`docs/adr/0041-predictor-forecast-layer.md`、`internal/domain/forecast.go`、`internal/app/ports.go` 的 ADR-041 章節，以及 `agents/predictor.md`。本波僅指派 `predictor-05` 與 `predictor-06`；`predictor-05b`／`predictor-05c`（Token/Quota Forecaster）明確不在範圍內，保留給未來波次，本波未開始、未 stub、也未 scaffold。

```yaml
node: predictor-05
status: completed
artifacts:
  - internal/predictor/scope/doc.go
  - internal/predictor/scope/coldstart.go
  - internal/predictor/scope/estimator.go
  - internal/predictor/scope/estimator_test.go
validation:
  - "gofmt -l internal/predictor internal/features  # clean"
  - "go build ./internal/predictor/... ./internal/features/...  # ok"
  - "go vet ./internal/predictor/... ./internal/features/...  # ok"
  - "go test ./internal/predictor/... -run Scope  # PASS (internal/predictor: no tests to run, expected; internal/predictor/scope: 6 top-level tests incl. 8-way monotonicity table, unknown-fields test, determinism test, error-propagation test)"
  - "go test ./internal/predictor/... ./internal/features/...  # PASS, full packages, no regressions"
commit: <see final report>
next_action: predictor-06（其依賴的 predictor-04 已經滿足；依 ADR-041 的結構獨立性，predictor-06 並不需要 predictor-05 完成）
assumptions:
  - "app.EstimateScopeRequest（已凍結）只帶有 SessionID／TaskID／RepositoryID——internal/app/ports.go 目前尚無 repository／session 的 feature-lookup port（CONTRACT_FREEZE.md 明確將此事延後處理：『Request/response DTOs ... have minimal fields sufficient to compile ... owning roles MAY find they need additional fields』）。RuleScopeEstimator 並未去修改 internal/app/ports.go（不屬於本角色的路徑），也沒有臆測出一個 DTO 形狀塞進去，而是依賴自己 package 內部的 FeatureSource 介面（internal/predictor/scope/estimator.go），未來波次以儲存為後盾的實作即可滿足此介面。這是已記錄的假設，而不是悄悄偏離合約。"
  - "ADD §14.6 的 cold-start 表僅列出 §14.3 十六個 task class 中的 8 個。其餘 8 個（question、inspection、test-only、bugfix-cross-layer、refactor-local、performance-investigation、security-sensitive、unknown）採用已記錄的 nearest-neighbor 備援表（internal/predictor/scope/coldstart.go 的 coldStartFallback），而不是自行在 ADD 表中新增列，也不是放著不處理。"
  - "MinSessionSamples（8）是類比 ADD §15.2 中 token-predictor 的樣本門檻（『count(similar) >= 8』）而來，因為 ADD §14 本身並未針對 scope estimation 明確指定 session-history 樣本門檻。"
  - "ToolCallsP50/P90、VerificationP50/P90、RetryLoopsP50/P90、DurationP50/P90 皆留為 nil（本波尚未接上任何 tool-call／verification-run 的 telemetry 來源）——這是 forecast.go 自身文件註解，以及 DAG 中此節點所述範圍（『Scope estimates for files read/changed and LOC』）明確允許的。"
blockers: []
```

```yaml
node: predictor-06
status: completed
artifacts:
  - internal/predictor/runway/doc.go
  - internal/predictor/runway/runway.go
  - internal/predictor/runway/runway_test.go
validation:
  - "gofmt -l internal/predictor/runway  # clean"
  - "go build ./internal/predictor/runway/...  # ok"
  - "go vet ./internal/predictor/runway/...  # ok"
  - "go test ./internal/predictor/runway/...  # PASS (15 tests, incl. a broad property-style sweep over used%/delta/interval combinations asserting no panic/NaN/Inf/out-of-range RiskScore, plus explicit outlier-rule and threshold tests)"
  - "go build ./...  # ok, whole module"
  - "go test ./internal/...  # PASS, whole module, no regressions"
commit: <see final report>
next_action: 無——第 2 波兩個節點皆已完成；依指示，predictor-05b／predictor-05c 明確延後至未來波次；本波到此為止
assumptions:
  - "GracefulPauseService.Observe（internal/app/ports.go）是目前唯一具名、會使用 domain.RunwayForecast 的凍結消費者；本波實作的是評分函式（runway.Scorer.Score），供該角色的 Observe 實作在每次 runtime 觀測時呼叫，而不是觀測迴圈或 pause 編排本身——未新增任何 runtime／排程程式碼，符合 predictor 角色的邊界（『No provider JSON parsing, Git commands, checkpoint creation, or process interruption』）。"
  - "依 ADD §15.6 的校準關卡（>=20 個有效 runway 樣本、held-out cohort 評估、ECE<=0.08、記錄 Brier score、model artifact 的 calibrated=true、quota 樣本新鮮度）以及 agents/predictor.md 的 cold-start 合約，本波 HitProbability 恆為 nil、Calibrated 恆為 false——目前尚無可長期保存的 burn-rate telemetry 儲存（要等後續波次的 claude-provider／foundation SQLite 工作完成，正如 ADR-041 對 predictor-05c 的說明）。RiskScore 則一律會產出（絕不為 nil），採用 ADD §15.7 未校準時的備援門檻（current>=95% -> critical/1.0；projected_used_p90 在 horizon 內 >=100% -> high/0.85；projected_used_p90>=95% -> medium-high/0.65；其餘則依剩餘 headroom 連續縮放），讓 policy 仍有一個可用、且明確標示為未校準的訊號。"
  - "Scorer 是無狀態的：每次呼叫僅接收目前的 QuotaObservation，加上一個選擇性的單一 Previous 觀測值（對應 GracefulPauseService.Observe 每次呼叫的 RuntimeObservation{SessionID, Quota domain.QuotaObservation} 形狀，一次一筆觀測），而不是自行保存長期的多樣本歷史——歷史儲存屬於擁有 observation store 的角色，不在本角色邊界之內。由於只有一個區間可用，BurnRateP50 與 BurnRateP90 會收斂為同一個觀測速率（沒有分佈可供重新取樣）；ADD §15.5 完整的 N=1000 次經驗自助抽樣（bootstrap）模擬是受 §15.6 把關的已校準路徑，本波刻意不嘗試（Constitution §7 rule 10）。"
  - "離群值（outlier）處理直接依照 ADD §15.4：負的 delta 視為 reset／修正，而非負速率；區間 < 2 秒者不予計入；速率高於保守的預設合理性上限（每分鐘 50 個百分點；因尚未接上任何實際 telemetry，目前無 provider 專屬上限）者標記為異常並捨棄；樣本超過 5 分鐘（相對於預設 10 分鐘 horizon 選定；ADD 並未指定確切的過時時間）則降低 Confidence，而不是直接捨棄。"
  - "多個同時存在的 QuotaObservation 限額視窗（limit window）透過 CombineWindows 合併，取各視窗間 RiskScore 的最大值——對應 ADD §15.5 明確的 v1 預設（『若 windows 高度相關，policy 可用保守 max(P_i)；v1 預設取 max，避免錯誤獨立假設』），而不是假設彼此獨立的 1-Π(1-P_i) 公式，後者保留給已校準路徑使用。"
  - "ADD §15.8 的 reset-awareness（重置感知）：當 ResetsAt 落在被評分的 horizon 之內時，無論目前的使用量／消耗速率為何，RiskScore 都會被拉低至『尚有餘裕』的數值，因為該視窗在重置前實際上不會被耗盡。"
blockers: []
```

## 第 2 波總結

本波指派的兩個節點（`predictor-05`、`predictor-06`）皆已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。`predictor-05b`（Token Forecaster）與 `predictor-05c`（Quota Forecaster）依明確指示刻意未開始——保留給未來波次，儘管 `predictor-05b` 名義上依賴的 `predictor-05` 現已完成。並未撰寫任何 `RuleTokenForecaster` 或 `RuleQuotaForecaster`（或任何滿足 `TokenForecaster`／`QuotaForecaster` 介面的東西）。未觸碰任何其他角色的路徑。本波未對 `main` 執行任何 merge／rebase（分支在本波開始前已由 lead 以 fast-forward merge 更新至 `4f96d7f`，已是最新狀態）。

---

## Lint 修正（第 2 波後，跨角色整合檢查）

一次跨角色整合驗證檢查（對已 merge 完成的第 2 波完整程式碼樹執行 `golangci-lint run`）在本角色所擁有的檔案中發現 3 項問題。這是針對這些發現所做的修正性 commit，並非新的 DAG 節點——並未開始 `predictor-05b`／`predictor-05c`／`predictor-07` 或其他任何工作。

- `internal/predictor/runway/runway.go:120` — gocritic 的 `appendAssign`（append 的結果被指派給和被附加對象不同的 slice：`forecast.ReasonCodes = append(reasons, ...)`）。已調查 `reasons` 之後是否會被重複使用：它就在此分支正上方被重新宣告為 `nil`，且該分支緊接在這一行之後就 return，因此不存在與後續程式碼的別名（aliasing）問題——**純屬外觀問題，並非真正的 bug**。修正方式為直接對 `forecast.ReasonCodes` 執行 append，而不是對區域變數 `reasons`，行為完全不變。
- `internal/predictor/scope/estimator_test.go:64,67` — staticcheck QF1001（De Morgan 定律化簡）。將 `if !(*p50 <= *p80) {` 改寫為 `if *p50 > *p80 {`，並將 `if !(*p80 <= *p90) {` 改寫為 `if *p80 > *p90 {`，邏輯完全相同。

validation:
  - "gofmt -l internal/predictor  # clean"
  - "go build ./internal/predictor/...  # ok"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -race  # PASS, all packages"
  - "golangci-lint not installed in this environment; underlying gocritic/staticcheck patterns fixed per their documented rules"

只有異動這兩個具名檔案；未觸碰任何其他角色的路徑。

---

## 第 3 波（predictor-05b）

分支：`vertical-slice/predictor`，接續自 `4285e12`（第 2 波 + lint 修正），已完全 merge 進 `main`。依指示，開始前重新閱讀了 CONSTITUTION.md、CONTRACT_FREEZE.md 的「Predictor pipeline ports (ADR-041)」章節、`docs/adr/0041-predictor-forecast-layer.md`、`internal/domain/forecast.go`、`internal/app/ports.go` 的 ADR-041 章節、`agents/predictor.md`、`Auspex_ADD.md` §15.1-15.2，以及 `Auspex_Predictor_Design_Supplement.md` 的「Stage 2 — Token Prediction」／「MVP Heuristic Formula」章節。本波未對 `main` 執行、也不需要任何 merge／rebase——predictor-05、predictor-04 的 quantile 工具，以及 ADR-041 凍結的型別皆已在此分支上。本波僅指派 `predictor-05b`（Token Forecaster）；`predictor-05c`（Quota Forecaster）與 `predictor-07`（Risk Combiner）明確不在範圍內，未開始、未 stub、也未 scaffold。

```yaml
node: predictor-05b
status: completed
artifacts:
  - internal/predictor/token/doc.go
  - internal/predictor/token/coldstart.go
  - internal/predictor/token/forecaster.go
  - internal/predictor/token/forecaster_test.go
validation:
  - "gofmt -l internal/predictor/token  # clean"
  - "go build ./...  # ok, whole module"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -run TokenForecast -v  # PASS (7 top-level tests: monotonicity table across 9 cases, never-calibrated-this-phase gate check across 3 sample-count cases, cold-start reason code, determinism, source-error propagation across 4 sources, multiplier-cap explosion guard, degenerate/negative-sample no-panic sweep)"
  - "go test ./internal/predictor/... ./internal/features/... -race  # PASS, full packages, no regressions"
  - "golangci-lint run ./...  # zero issues in files owned by this role; 3 pre-existing issues remain in internal/hooks/claude, internal/clock, internal/idgen (not owned by predictor — noted, not fixed)"
commit: <see final report>
next_action: 無——predictor-05b 是本波唯一指派的節點；依指示，predictor-05c／predictor-07 明確延後；本波到此為止
assumptions:
  - "app.ForecastTokensRequest（已凍結）只帶有 SessionID 與 Stage-1 的 domain.ScopeEstimate——internal/app/ports.go 目前尚無 task-classification 或 session-token-history 的查詢 port（與 predictor-05 的 FeatureSource 已記錄的 Bootstrap 缺口相同）。RuleTokenForecaster 依賴自己 package 內部的 internal/predictor/token.FeatureSource 介面（Classification、Session、Progress、RecentSimilarTurnTokens），而不是修改 internal/app/ports.go（不屬於本角色的路徑），也不是臆測出一個 DTO 形狀塞進去。未來波次以儲存為後盾的實作即可滿足此介面；測試中則以 fake 滿足它。"
  - "僅限 cold-start 的範圍：本波尚無可長期保存的歷史 telemetry 儲存（與 predictor-05／predictor-06 僅限 cold-start 實作已建立的缺口相同——ADR-041 對 predictor-05c 的 cold-start 說明，依同樣的推理也適用於 predictor-05b，因為兩者都要等 claude-provider-05／foundation-06 在後續波次完成）。>=8 個相似樣本的經驗分支（ADD §15.2 的確切門檻）已實作並由測試涵蓋，因此未來若有以實際儲存為後盾的 FeatureSource，就能自動啟用此分支；但本波每一筆結果的 Calibrated 皆為 false，Confidence 絕不超過 ConfidenceMedium（只有透過經驗基礎分支才會達到；其餘情況為 ConfidenceLow）——絕不捏造已校準的結果。"
  - "P80 相關假設（此節點範圍明確標示的部分）：ADD §15.2 的 base-quantile 描述只列出了 base_p50／base_p90（『weighted_quantile(tokens, 0.50)』／『0.90』），並無 base_p80。與其臆測出一個不相關的第三個經驗分位數，TokensP80 是在（經 multiplier 調整過的）P50 與 P90 之間，於 log 空間中以『朝 P90 方向 60%』的權重內插得出（internal/predictor/token/forecaster.go 的 interpolateP80），這與 Auspex_Predictor_Design_Supplement.md 自身 P50/P80/P95 範例（38000/61000/94000）的右偏形狀相符。這是一項已記錄的假設，而非源自規格的數值。"
  - "ADD §14.6 的 cold-start 表針對每個 task class 給出的是「相對 token 乘數」，而非絕對 token 數量。internal/predictor/token/coldstart.go 把這個相對尺度錨定在一個 bootstrap 用的絕對常數 baseTurnTokens=6000 上（文件註明這是 bootstrap 起始點，明確不是實測得出的通用基準，呼應 ADD 自身對姊妹 files/lines 表的免責聲明）。ADD §14.6 未列出的那 8 個 task class，使用一份已記錄的 nearest-neighbor 備援表，此表獨立於（而非匯入自）scope/coldstart.go 自己的備援表，因為這兩張表衡量的是不同的量，不應悄悄耦合在一起。"
  - "verification_multiplier 的 build_required 項目本波未接上任何直接的 ScopeEstimate／PromptFeatures 訊號；它被視為由 RequiresIntegration 隱含（假設需要整合測試的 turn 也需要 build），而不是放著不計入——這是已記錄的假設。"
  - "complexity_multiplier 的 repository_wide 項目沒有直接對應的 ScopeEstimate 布林欄位；以 FilesChangedP90 >= 15 近似，呼應 scope.RuleScopeEstimator 自身的 ReasonLargeFileScope 門檻，而不是放著不計入。"
  - "retry_multiplier 與 progress_multiplier 分別讀取 SessionFeatures.RetryRate 與 ProgressFeatures.CompletedRatio（nil 或 !ok 皆代表「未知」 -> 中性乘數 1.0，並附上 cold-start 的 reason code，絕不捏造為 0）。progress_multiplier 的『remaining_critical_path_cost / original_task_cost』比率（ADD §15.2）以 1 - CompletedRatio 近似，因為目前尚無獨立的成本模型——這是已記錄的假設，ADD §15.2 並未指定 critical-path 成本應如何衡量。"
  - "ambiguity_multiplier 把 ADD 所列的四個級距（1.0／1.2／1.5／2.0）對應到 PromptFeatures 訊號的方式（明確路徑 + 具名驗收標準 -> 1.0；僅有明確路徑 -> 1.2；無明確路徑且非開放式 -> 1.5；OpenEndedIndicator -> 2.0），是本 package 自行記錄的詮釋，因為 ADD 只列出了級距與乘數，並未指定確切的『特徵對應級距』規則。"
  - "單一乘數上限（3.0）與組合幾何平均上限（6.0）是本 package 自訂的保守預設值，用以實作 ADD §15.2 明確要求的『避免乘數爆炸，設定上限』指示——ADD 並未指定確切的上限數值。此設計由 TestTokenForecastMultiplierCapsPreventExplosion 驗證，使用刻意極端／荒謬的輸入（10^9 行變更、retry rate 為 100、負的 completed ratio），斷言結果仍落在由上限推導出的界限內，且維持非負／單調。"
  - "在本 repository 中並未找到任何 Missing_Telemetry_Report.md 檔案（已徹底搜尋）；此節點僅限 cold-start 的範圍，改為直接由 ADR-041 自身文字佐證（predictor-05c 的 cold-start 說明，依同樣推理適用於 predictor-05b），以及此分支相依項目中（claude-provider-05／foundation-06 尚未 land）並無任何長期 telemetry 儲存這項事實佐證——這裡記下指派指示與 repository 實際內容之間的一項落差，但未進一步處理，因為結論（僅限 cold-start）並不受影響。"
blockers: []
```

## 第 3 波總結

本波唯一指派的節點（`predictor-05b`）已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依明確指示，`predictor-05c`（Quota Forecaster）與 `predictor-07`（Risk Combiner）刻意未開始，儘管 `predictor-05c` 名義上依賴的 `predictor-05b` 現已完成。並未撰寫任何 `RuleQuotaForecaster`、`RiskCombiner` 實作，或除了 `RuleTokenForecaster` 以外的任何東西。未觸碰任何其他角色的路徑。本波未對 `main` 執行、也不需要任何 merge／rebase。

---

## 第 4 波（predictor-01、predictor-05c）

分支：`vertical-slice/predictor`，接續自 `22fde28`（第 3 波，`predictor-05b`）。依明確指示，先 merge 了 `main`（`git merge main -m "Sync main (Phase 3) before predictor-01/05c"`）——從 `22fde28` 到 `ca7062f` 的乾淨 fast-forward，帶入了 foundation-06/08、`predictor-05b` 自身已 merge 的副本、`runtime-b01`、`qa-01/08`，以及一大批先前尚未 merge 的 foundation 基礎建設（`internal/cli`、`internal/config`、`internal/lock`、`internal/paths`、`internal/gitx`、`internal/storage/sqlite` 的 db/migrate 引擎、`internal/telemetry/claude`、CI／governance 文件）。merge 完成後、在撰寫任何新程式碼之前，全 repo 的 `go build ./...` 與 `go test ./...` 皆乾淨通過。依指示，開始前重新閱讀了 CONSTITUTION.md、CONTRACT_FREEZE.md 的「Predictor pipeline ports (ADR-041)」章節、`docs/adr/0041-predictor-forecast-layer.md`、`agents/predictor.md`、`internal/predictor/token/forecaster.go`、`internal/predictor/scope/estimator.go`、`internal/app/ports.go` 的 ADR-041 章節、`internal/domain/forecast.go`，以及 `Auspex_ADD.md` §15.3／§15.9。

```yaml
node: predictor-01
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0040_feature_vectors.sql
  - internal/storage/sqlite/migrations/0041_predictions.sql
  - internal/storage/sqlite/migrations/0042_runway_forecasts.sql
  - internal/storage/sqlite/migrations/0043_policy_decisions.sql
  - internal/storage/sqlite/migrations/0044_authorizations.sql
  - internal/storage/sqlite/migrate_predictor_test.go
validation:
  - "gofmt -l internal/storage/sqlite  # clean"
  - "go vet ./internal/storage/sqlite/...  # ok"
  - "go test ./internal/storage/sqlite/... -run Migration0040 -v  # PASS (4 top-level tests: predictor range loads/applies via AllMigrations, per-table column-shape spot-check via PRAGMA table_info across all 5 tables, policy_decisions FK relationships both within predictor's own range and into foundation's provider_sessions, authorizations UNIQUE(turn_id))"
  - "go build ./...  # ok, whole module"
  - "golangci-lint run ./...  # 0 issues repo-wide"
commit: <see final report>
next_action: predictor-05c（同一波，依序進行）
assumptions:
  - "資料表集合依 Auspex_ADD.md §12.2 的標準 schema，範圍限定在 predictor 的 migration 區間（依 CONTRACT_FREEZE.md 為 0040-0049）：feature_vectors、predictions、runway_forecasts、policy_decisions、authorizations。ADD §12.2 的 schema 中並不存在字面上名為 evaluations 的資料表（domain.EvaluationID／app.Evaluation 是以 predictions 資料表加上 policy_decisions 為後盾，而非獨立的資料表）——任務指示中『evaluations/predictions/authorizations』的措辭，理解為指涉整個持久化層面（agents/predictor.md 交付項 #11『Evaluation persistence』+ #12『authorization issuance/consumption』），而不是字面上的第五張資料表名稱。"
  - "internal/storage/sqlite/migrate_test.go 歸 foundation 所有（不屬於 predictor 的專屬路徑）。依 Constitution §4.4（『角色絕不編輯自己不擁有的檔案；以已記錄的假設繞過此缺口』），predictor-01 自身的 migration 範圍驗證，放在同一個共用 sqlite_test package 底下的新檔案 migrate_predictor_test.go 中——純屬新增，未觸碰任何既有檔案。這正好呼應 foundation-06 自身已建立的模式，即把各範圍的 migration 測試直接放在該 package 中（TestAllMigrations_LoadsCoreSchemaFiles／TestCoreMigrations_*），測試名稱包含『Migration0040』，讓 DAG 中字面上指定的驗證指令能夠選中它們。"
  - "發現的整合風險（真實存在，並非假設——提報給 contract-integrator／foundation）：依 ADD §12.2 的 schema，feature_vectors／predictions／runway_forecasts／authorizations 在概念上 FK 指向 turns（claude-provider 的 0010-0019 範圍），authorizations 還 FK 指向 repository_checkpoints（checkpoint Part B 的 0030-0039 範圍）。這兩張表目前都還沒有任何 migration 存在（已直接查驗 vertical-slice/claude-provider 與 vertical-slice/checkpoint 分支——兩者皆無任何 migration 檔案）。SQLite 的 PRAGMA foreign_keys = ON（db.go 已設定）並不只是在目標表被填入資料之前『暫不執行』一條指向不存在資料表的 REFERENCES 子句——它會讓任何可經由該表觸及的 cascading DELETE 直接以『no such table: main.turns』失敗，即使是完全不相關的資料列也一樣，因為 SQLite 會在 DML prepare 階段就解析 schema cascade graph 中每一張被 FK 參照的資料表。這是實際驗證出來的：加入這些帶有真正 REFERENCES 子句的資料表，會破壞 foundation-06 自身 3 個已通過、已 merge 的測試（TestCoreMigrations_ForeignKeys_RepositoryToWorktree、TestCoreMigrations_ForeignKeys_TaskSessionSetNull，以及間接影響 reopen 測試），原因只是一句與 predictor 資料表完全無關的 DELETE FROM repositories。採取的修正方式：turn_id（於 feature_vectors／predictions／runway_forecasts／authorizations）以及 repository_checkpoint_id（於 authorizations）皆為單純、無約束的 TEXT 欄位，不帶 SQL 層級的 FK——這正是 0004_tasks.sql 早已為其對 progress_nodes 的前向參照建立的先例（『SQLite 若不重建資料表，就無法延後新增跨表 FK』）。此決定已記錄在各 migration 檔案自身的檔頭註解中。凡是目標資料表已存在於此分支上者，皆保留真正的 FK：runway_forecasts.session_id -> provider_sessions（0003）、runway_forecasts.task_id -> tasks（0004），以及 policy_decisions -> predictions／runway_forecasts 這兩個同範圍內的 FK（0041/0042，皆屬本範圍）。"
  - "對全 repo go test ./... 的影響：經此修正後，TestCoreMigrations_ForeignKeys_RepositoryToWorktree 與 TestCoreMigrations_ForeignKeys_TaskSessionSetNull（皆位於 foundation 的 migrate_test.go）已重新 PASS。此分支上仍有兩個 foundation 測試失敗——TestAllMigrations_LoadsCoreSchemaFiles 與 TestCoreMigrations_FromEmptyDatabase／TestCoreMigrations_ReopenFromFile_AppliesOnce——但原因僅僅是它們把 len(migrations) != 4／CurrentVersion != 4 寫死為嚴格相等的斷言，假設 foundation 的 4 個 migration 是嵌入目錄中「唯一」會存在的 migration。這是一項既有的測試設計限制，存在於 predictor 不擁有、也無法在不違反路徑邊界的情況下修復的檔案中；無論 merge 順序為何，只要任何其他角色的 migration 範圍一旦 land（claude-provider、checkpoint 或 predictor，不論誰先 merge），都會以相同方式壞掉。在此提報給 contract-integrator／foundation，建議在下一個整合節點放寬這兩個斷言（例如改用 >= 而非 ==），而不是由本角色單方面修正。"
  - "authorizations 的 UNIQUE(turn_id) 約束，加上未來 predictor-10 服務層的『consumed_at IS NULL』檢查，兩者共同實作 CONTRACT_FREEZE.md『Authorization — one-time；consumption is exactly-once』的合約；單靠 UNIQUE(turn_id) 只能保證每個 turn 恰好一次的核發，並不能保證恰好一次的消費（後者屬於服務層交易層面的考量，對於一個只處理 migration 的節點而言，刻意不在範圍內）。"
blockers: []
```

```yaml
node: predictor-05c
status: completed
artifacts:
  - internal/predictor/quota/doc.go
  - internal/predictor/quota/coldstart.go
  - internal/predictor/quota/forecaster.go
  - internal/predictor/quota/forecaster_test.go
validation:
  - "gofmt -l internal/predictor/quota  # clean"
  - "go vet ./internal/predictor/quota/...  # ok"
  - "go test ./internal/predictor/... -run QuotaForecast -v  # PASS (19 tests: never-calibrated-this-phase across 4 input shapes, unknown-when-no-observation for both quota and context, nil-UsedPercent treated as unknown not zero, forward projection from current usage for both quota and context, context UsedTokens/WindowTokens fallback + zero-WindowTokens guard, near-limit reason codes for quota/context/Reached-flag, multi-window max-combination, reset-soon delta suppression vs reset-far-away delta application, TokenForecast-scaled delta (small vs large forecast, zero-value-behaves-as-absent), determinism, degenerate-input no-panic sweep incl. negative/huge/MaxInt64 values)"
  - "go build ./...  # ok, whole module"
  - "go test ./internal/predictor/... ./internal/features/... -race  # PASS, full packages, no regressions"
  - "golangci-lint run ./...  # 0 issues repo-wide"
commit: <see final report>
next_action: 無——predictor-01／predictor-05c 是本波指派的兩個節點；依指示，predictor-07（Risk Combiner）明確不在本波範圍內（要等 predictor-05c 完成審查後才會解除阻塞），未開始
assumptions:
  - "新的姊妹 package internal/predictor/quota（與 internal/predictor/scope、internal/predictor/token、internal/predictor/runway 並列），命名對應 Auspex_Predictor_Design_Supplement.md 自身的命名：『RuleQuotaForecaster — Version 1 — deterministic delta model, §15.3』。與 scope／token 不同，這個階段不需要 package 內部的 FeatureSource 抽象層：app.ForecastQuotaRequest 已經直接帶有 Stage 3 所需的一切（Quota []domain.QuotaObservation、Context domain.ContextObservation、TokenForecast domain.TokenForecast）——沒有 session／repository／progress 的 feature-lookup 缺口需要橋接，因此 RuleQuotaForecaster 是無狀態的（比起 scope／token 以 FeatureSource 為後盾的設計，更接近 internal/predictor/runway.Scorer 自身無狀態的設計）。"
  - "僅限 cold-start 的範圍，正如 CONTRACT_FREEZE.md 的 ADR-041 章節明確預期並允許此節點採用的方式：『QuotaForecaster implementations MAY produce a deterministic current-observation-plus-default-delta estimate ... before durable historical telemetry exists. This is not a stub to be later thrown away.』此分支上並無長期的 telemetry 儲存（claude-provider-05 的持久化層是同一波的姊妹節點，位於另一個尚未 merge 的分支上），因此 ADD §15.3 步驟 5 的『從樣本得出經驗 P50/P90』分支就結構上而言無法觸及——每一筆結果的 Calibrated 皆為 false、Confidence 為 ConfidenceLow，且恆有 ReasonPredictionColdStart。"
  - "ADD §15.3／§15.9 並未指定確切的預設 delta 數值（不同於 §14.6 的 token-multiplier 表）。coldstart.go 記錄了本 package 自訂的保守 bootstrap 常數：defaultQuotaDeltaP50/P90 = 每個 turn 2.0／6.0 個百分點；defaultContextGrowthP50/P90Fraction = 0.03／0.10（context window 容量的 3%／10%）——明確標示為 bootstrap 起始值，而非實測數值，預期未來一旦有長期的逐視窗 delta 樣本，就會被 StatisticalQuotaForecaster 的經驗分位數（Version 2）取代。"
  - "TokenForecast 備援（依 app.ForecastQuotaRequest 自身的文件註解：『MAY use TokenForecast as a fallback input when the provider does not expose quota percentage directly; MUST NOT require it』）：tokenAdjustedDelta 以 TokensP90 相對於一個名目 6000-token『典型 turn』基準（nominalTurnTokens，刻意與 internal/predictor/token.baseTurnTokens 的值一致，但獨立宣告而非跨 package 匯入，呼應 internal/predictor/token/coldstart.go 自身『衡量不同量的 package 之間 cold-start 表應保持獨立』的既有理由）來縮放預設的 P90 delta／成長率。此縮放限制在 [0.5x, 3.0x]（tokenScaledDeltaFloor/Ceiling）之內，以免單一極端的 TokenForecast 使結果爆增或抵銷保守的預設值——這與 internal/predictor/token 各乘數上限的紀律相同，在此重複使用，因為 §15.3 並未給出對應的明確上限。零值的 TokenForecast（TokensP90<=0，即未提供 forecast）會讓預設 delta 維持不縮放，由 TestQuotaForecastZeroTokenForecastUsesUnscaledDefault 驗證。"
  - "多視窗合併（ForecastQuotaRequest.Quota 是一個 slice，domain.QuotaForecast.ProjectedQuotaUsedP90 則是單一純量）：沿用 internal/predictor/runway.CombineWindows 已建立的保守『取各視窗最大值』規則，理由與 ADD §15.5『v1 預設取 max，避免錯誤獨立假設』完全相同，而不是採用假設各視窗獨立的合併公式——由最差（預估值最高）的視窗決定唯一回傳的數值。"
  - "重置感知（ADD §15.8：『resets_at 是 schedule hint』）：當 ResetsAt 落在固定的 turnHorizon 前瞻範圍內（10 分鐘，對應 internal/predictor/runway.DefaultHorizon——本波尚未接上任何 turn 時長預測，因此無法確切得知即將到來的 turn 會花多久，故以固定的保守 horizon 作為已記錄的假設，與 runway 為自身預設 horizon 已建立的模式相同）時，projectOneQuotaWindow 會抑制 delta（預測值維持在目前使用量，不再向前複合累加）。"
  - "接近上限的門檻（ReasonQuotaNearLimit／ReasonContextNearLimit）：ADD §15.3／§15.9 並未指定確切的百分比（不同於 §15.7 明確的 runway 門檻）。此處採用 90%，選定理由是呼應這個 pipeline 階段中已普遍使用的 P90 框架——這是一項已記錄的保守預設值，而非源自規格的數值。QuotaObservation.Reached=true 無論 UsedPercent 為何，一律會觸發 ReasonQuotaNearLimit，因為 Reached 是 provider 自身權威的訊號，不應被百分比啟發式規則覆蓋。"
  - "ContextObservation 備援：當 UsedPercent 為 nil，但 UsedTokens／WindowTokens 皆存在且 WindowTokens>0 時，目前使用量會以 UsedTokens/WindowTokens*100 推算出來（依 usage.go 自身欄位集合，這是同樣有效的量測方式），而不是視為未知；WindowTokens<=0 則明確設有防護（TestContextForecastZeroWindowTokensIsUnknown）以避免除以零，並回退為未知（nil），絕不捏造數值。"
blockers: []
```

## 第 4 波總結

本波指派的兩個節點（`predictor-01`、`predictor-05c`）皆已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依指示先 merge 了 `main`（乾淨的 fast-forward，`22fde28` -> `ca7062f`），並確認在撰寫任何新程式碼之前建置／測試皆乾淨通過。`predictor-01` 揭露並修正了一個真實的跨角色 SQLite 外鍵風險（詳見上方其自身的 `assumptions` 區塊），否則在下一個整合節點，無論 merge 順序為何，都會悄悄破壞 foundation-06 已 merge 的 cascade-delete 測試。`predictor-05c` 是一個新的、自成一體的 `internal/predictor/quota` package，不像它的 `scope`／`token` 姊妹那樣需要 FeatureSource 抽象層。`predictor-07`（Risk Combiner）本波明確不在範圍內（要等 `predictor-05c` 完成並經過審查），未開始、未 stub、也未 scaffold。未觸碰任何其他角色的路徑。截至本波最終 commit 為止，`golangci-lint run ./...` 回報全 repo 0 個問題。

---

## 第 5 波（predictor-07）

分支：`vertical-slice/predictor`，接續自 `1fa92cf`（第 4 波，`predictor-01`／`predictor-05c`）。依明確指示，先 merge 了 `origin/main`（`git fetch origin && git merge origin/main`）——從 `1fa92cf` 到 `5470e4d` 的乾淨 fast-forward，帶入第 4 波整合後的狀態（foundation-07、claude-provider-05、checkpoint-a01/b01、`predictor-01`／`predictor-05c` 已 merge 的副本、runtime-a01/b02、`internal/app/wiring`、`internal/telemetry/claude`、新的 SQLite migrations 0010-0052、`internal/testutil/fakes`）。merge 完成後、在撰寫任何新程式碼之前，全 repo 的 `go build ./...` 與 `go test ./...` 皆乾淨通過。依指示，開始前重新閱讀了 CONSTITUTION.md、`agents/predictor.md`、`docs/implementation/vertical-slice/EXECUTION_DAG.md`（`predictor-07` 更正後的條目）、`docs/implementation/vertical-slice/CONTRACT_FREEZE.md` 的「Predictor pipeline ports (ADR-041)」章節、`docs/adr/0041-predictor-forecast-layer.md`（包含其關於 `execution_risk` 與 `completion_risk` 的「Terminology note」）、`internal/app/ports.go` 的 `RiskCombiner`／`CombineRiskRequest`／`CombineRiskResult` 章節、`internal/domain/forecast.go`，以及 `Auspex_ADD.md` §16.1-16.2／§16.3／§16.4。本波僅指派 `predictor-07`（Risk Combiner）。

```yaml
node: predictor-07
status: completed
artifacts:
  - internal/predictor/risk/doc.go
  - internal/predictor/risk/coldstart.go
  - internal/predictor/risk/combiner.go
  - internal/predictor/risk/combiner_test.go
validation:
  - "gofmt -l internal/predictor  # clean"
  - "go build ./internal/predictor/...  # ok"
  - "go vet ./internal/predictor/...  # ok"
  - "go test ./internal/predictor/... -run RiskComponents -v  # PASS (9 top-level tests, 16 subtests: quota/context sigmoid formula incl. midpoint=0.5 exact case, nil-projection-is-unknown-not-zero for both quota/context, completion risk formula incl. base/maxed-out-clamped-to-1.0/reason-code-derived-term-delta/cold-start-propagation, blast-radius risk formula incl. base/security+migration/public-API-change-delta/monotonicity-in-files-changed, overall=max() with calibrated-only-if-all-inputs-calibrated and reason-code union, 500-trial NaN/Inf/out-of-range property sweep across extreme+nil inputs, 20-trial determinism check, cold-start-never-fabricates-calibration across all 5 components, reason-code golden test, frozen-interface satisfaction check)"
  - "go test ./internal/predictor/... -race  # PASS, all packages"
  - "go build ./...  # ok, whole module"
  - "go test ./...  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide"
commit: <see final report>
next_action: 無——predictor-07 是本波唯一指派的節點；predictor-08（Policy）明確不在範圍內，未開始、未 stub、也未 scaffold
assumptions:
  - "package 路徑 internal/predictor/risk（與 internal/predictor/{scope,token,quota,runway} 並列），對應本角色自身已建立的『每個 pipeline 階段一個 package』慣例，以及 Auspex_Predictor_Design_Supplement.md 的命名模式（『RuleXForecaster — Version N』）。無論是 internal/app/ports.go 還是 CONTRACT_FREEZE.md，都沒有為 RiskCombiner 指定實作 package 路徑（CONTRACT_FREEZE.md 的『What Bootstrap did NOT freeze』章節明確將『predictor internals』交由負責角色決定），因此這裡沿用了與 scope／token／quota 相同的判斷。"
  - "RuleRiskCombiner 逐字實作 ADD §16.2 的『Initial explainable formula』，包括其確切係數（例如 completion_risk 的 0.10 base + 0.04*files_changed_p90 + …；blast_radius_risk 的 0.05 base + 0.03*files_changed_p90 + …）——不同於 predictor-05c／predictor-05b，這個公式的常數在 ADD 中已完整列出，因此這裡不需要推導任何 bootstrap 常數（coldstart.go 只是列出 ADD 自身的係數，方便逐行對照 ADD 文字進行稽核，並未發明新的預設值）。"
  - "術語：全篇一律使用 completion_risk／CompletionRisk，絕不使用 execution_risk，依 ADR-041 明確的『Terminology note』（Auspex_Predictor_Design_Supplement.md 的 execution_risk = P(task_requires_multiple_turns)，與 ADD §16.1 的 completion_risk 指的是同一個概念；ADR-041 將 ADD 的名稱凍結保留，『重新命名會讓同一個概念分裂成兩個名稱，這正是 Constitution §1 要防止的事』），以及 internal/app/ports.go 中 CombineRiskResult.CompletionRisk 本身凍結的欄位名稱。此點已明確記錄在 risk/doc.go 的 package 註解中，因此不必重讀 ADR 也能稽核。"
  - "發現的缺口（真實存在，並非假設——提報給 contract-integrator／predictor-08）：ADD §16.2 的 completion_risk／blast_radius_risk 公式列出了五個項目（open_ended_scope、recent_retry_rate、recent_test_failure_rate、unresolved_progress_blockers、public_api_change），在凍結的 domain.ScopeEstimate 結構（internal/domain/forecast.go）上並沒有直接對應的欄位——不同於 files_changed_p90／lines_changed_p90／integration_tests／migration／cross_layer／security_sensitive／cross_project，這些欄位與 ScopeEstimate 是一對一對應的。這些底層訊號其實存在於下面一層的 internal/features 中（PromptFeatures.OpenEndedIndicator、SessionFeatures.RetryRate／TestFailureRate、ProgressFeatures.UnresolvedBlockers），但凍結的 app.CombineRiskRequest（ADR-041）只帶有 Scope／TokenForecast／QuotaForecast，並不包含這些 feature DTO。與其擴大 CombineRiskRequest（不屬於此節點可編輯的路徑——internal/app/ports.go 歸 contract-integrator 所有），或悄悄把這些項目一律當作 0，本實作改為從 scope.ReasonCodes 讀取它們，作為布林形式的『是否出現』指標（domain.ReasonOpenEndedScope、domain.ReasonHighRecentRetryRate、domain.ReasonHighRecentTestFailureRate、domain.ReasonProgressBlocked、domain.ReasonPublicAPIChange）——這是目前 internal/predictor/scope.RuleScopeEstimator 已經對外呈現部分這類訊號的唯一管道。這是一項已記錄的、以布林值（而非連續速率）近似的做法：一旦某個 reason code 出現，就貢獻該公式係數的全額，絕不會依實際速率打折扣，因為 CombineRiskRequest 對這些項目並未帶有連續數值。此點已在 combiner.go 的 completionRiskTermsFromReasonCodes 文件註解中完整記錄。建議 predictor-08（Policy）以及未來重新檢視此 pipeline 的任何 ADR，評估是否應該讓 CombineRiskRequest 直接擁有這些欄位，而不是讓每個下游消費者各自重新推導同樣的 reason-code 橋接邏輯。"
  - "quota_risk／context_risk 在投影值為 nil 時（依 ADD §16.3『unknown，並非零』）的備援分數為 sigmoid(0)=0.5——即 sigmoid 本身的中點——之所以選擇它，是因為對於真正缺失的輸入而言，這是最站得住腳的『分數形狀』佔位值（並搭配明確的 QUOTA_UNKNOWN／CONTEXT_UNKNOWN reason code，以及該元件自身的 Confidence／Calibrated，這些都誠實地從上游未知的 QuotaForecast 傳遞而來，絕不捏造成高信心）。ADD §16.2 並未定義 sigmoid 對缺失輸入應有的行為，因此這是一項已記錄的實作選擇，而非源自規格的數值。"
  - "QuotaForecast.ReasonCodes 是單一共用欄位，同時涵蓋 quota 與 context 兩種子訊號（凍結的結構中並沒有依欄位拆分的 reason-code——這對應 predictor-05c 的 RuleQuotaForecaster 自身把 quotaReasons／contextReasons 附加進同一個合併 slice 的做法）。因此 quotaRiskComponent 與 contextRiskComponent 都會回顯完整的 qf.ReasonCodes，而不是經過篩選的子集——這一點由 TestRiskComponentsReasonCodeGolden 明確驗證，將這種交叉回顯行為固定為預期（而非意外）的形狀。"
  - "overall_risk（ADD §16.2：overall = max(quota, context, completion, blast_radius)）另外會把 Calibrated 計算為四個元件各自 Calibrated 的邏輯 AND（整體的宣稱絕不能比其中最不確定的輸入更確定），並透過一份已記錄的 confidenceRank 排序（unavailable < low < medium < high < exact），把 Confidence 計算為四者中最低（最保守）的一個——ADD §16.2 只列出了 Score 公式，並未說明 Calibrated／Confidence／ReasonCodes 應如何組合，因此這是本 package 自身已記錄的保守延伸，符合 Constitution §7 rule 7。"
  - "clamp01 對 NaN 的處理：NaN 分數會被限制為 1.0（最保守／風險最高的值），而不是 0.0 或維持為 NaN，理由是若分數計算產生 NaN，反映的是上游資料出了問題，而本 package 整體的紀律（呼應 quota_risk 自身『unknown 不等於零』的偏向）是寧可揭露偏高的風險，也不要悄悄低估。這一點由 TestRiskComponentsNeverNaNOrInf 的 500 次隨機性質測試涵蓋，其中隨機組合包含 math.MaxFloat64／-MaxFloat64／math.MaxInt64／math.MinInt64 以及 nil 指標輸入。"
blockers: []
```

## 第 5 波總結

本波唯一指派的節點（`predictor-07`）已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依指示先 merge 了 `origin/main`（乾淨的 fast-forward，`1fa92cf` -> `5470e4d`），並確認在撰寫任何新程式碼之前建置／測試皆乾淨通過。`internal/predictor/risk` 是一個新的、自成一體、無狀態的 package（不需要 FeatureSource 抽象層，這點對應的是 `predictor-05c` 的先例，而非 `predictor-05`／`predictor-05b`），依 ADR-041 凍結的 `app.RiskCombiner` 介面，逐字實作 ADD §16.2 的風險合併公式。發現並記錄了一個真實的缺口（ADD §16.2 公式中有五個項目在 `ScopeEstimate` 上沒有直接對應的欄位），並透過 `scope.ReasonCodes` 加以橋接，而不是悄悄忽略，也不是透過修改凍結合約檔案來繞過。全篇術語一律使用 `completion_risk`，對應 ADR-041 對 `execution_risk`／`completion_risk` 命名分歧的明確裁定。未觸碰任何其他角色的路徑；`internal/policy/**` 與 `internal/evaluation/**` 維持不變。截至本波最終 commit 為止，`golangci-lint run ./...` 回報全 repo 0 個問題。

---

## 第 6 波（predictor-08）

分支：`vertical-slice/predictor`，接續自 `216c92b`（第 5 波，`predictor-07`）。依明確指示，先 merge 了 `origin/main`（`git fetch origin && git merge origin/main`）——從 `216c92b` 到 `abce1d0` 的乾淨 fast-forward，帶入第 5 波整合後的狀態（`claude-provider-07`、`checkpoint-a02/a03/b04`、本角色自身已 merge 的 `predictor-07`、`runtime-a02/a06/b03/b04/b05/b08`，以及新的 package `internal/artifacts`、`internal/orchestrator`、`internal/pause`、`internal/progress`、`internal/repocheckpoint`、`internal/scheduler`、`internal/gitx`、新的 CLI checkpoint／diagnostics 指令，以及擴充後的 `internal/app/wiring`）。merge 完成後、在撰寫任何新程式碼之前，全 repo 的 `go build ./...` 與 `go test ./...` 皆乾淨通過。依指示，開始前重新閱讀了 `CONSTITUTION.md`、`agents/predictor.md`、`docs/implementation/vertical-slice/EXECUTION_DAG.md`（`predictor-08` 更正後的條目——依賴 `predictor-07`、`predictor-06`，對應 ADR-041「Policy consumes Runway directly」的更正）、`docs/implementation/vertical-slice/CONTRACT_FREEZE.md` 的「Predictor pipeline ports (ADR-041)」章節，以及 `docs/adr/0041-predictor-forecast-layer.md`。本波僅指派 `predictor-08`（Policy）。

```yaml
node: predictor-08
status: completed
artifacts:
  - internal/policy/doc.go
  - internal/policy/coldstart.go
  - internal/policy/decide.go
  - internal/policy/policy_test.go
  - internal/policy/coldstart_test.go
validation:
  - "gofmt -l internal/policy  # clean"
  - "go build ./internal/policy/...  # ok"
  - "go vet ./internal/policy/...  # ok"
  - "go test ./internal/policy/... -run ColdStart -v  # PASS (9 top-level tests: literal-contract-shape match, all 4 reachable risk bands with uncalibrated inputs, emergency-PAUSE-is-not-a-probability across 3 emergency trigger conditions, mandatory-checkpoint-boundary-is-not-a-probability, explicit-deny/integrity-failure never-probability, calibrated-runway-may-legitimately-report-probability control case, direct-construction check across all 8 frozen PolicyAction values, and a full-grid randomized sweep over risk score x runway score x all 4 boolean gates x prior-confirmed asserting Probability stays nil throughout)"
  - "go test ./internal/policy/... -race  # PASS"
  - "go test ./internal/policy/... -bench=. -benchmem -run '^$'  # BenchmarkDecide: 52.83 ns/op, 16 B/op, 1 allocs/op — well under ADD §29.11's <1ms policy target"
  - "go build ./...  # ok, whole module"
  - "go test ./... -race  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide"
commit: <see final report>
next_action: 無——predictor-08 是本波唯一指派的節點；predictor-09（Evaluation persistence）明確不在範圍內，未開始、未 stub、也未 scaffold
assumptions:
  - "internal/app/ports.go 並沒有專屬的凍結 Policy 介面——最接近的凍結 policy-decision port 是 EvaluationService.Decide(ctx, DecideRequest{EvaluationID}) (DecisionResult, error)，但它本身並不帶有 risk／runway 的內容。與其臆測一個更寬的形狀塞進 ports.go（不屬於本角色的路徑——Constitution §4.3），或是就此卡住，此節點建構了 policy.Decider／policy.Decision／policy.DecideRequest，作為本 package 自身已記錄的橋接型別：Decision.Action 恆為八個凍結 app.PolicyAction 值之一，而 Decision 本身則帶有更豐富的欄位（RiskScore、Probability、Confidence、reason codes），未來的 evaluation-persistence 節點（predictor-09）可以將其攤平為 app.Evaluation／app.DecisionResult，本 package 無需自行發明一個與凍結合約競爭的合約。這對應 CONTRACT_FREEZE.md 自身的預期：『owning roles MAY find they need additional fields; requests for additions go through the role's progress artifact ... not silent edits to internal/app/ports.go.』"
  - "agents/predictor.md 交付項 #10 的動作名稱（ALLOW、WARN、CHECKPOINT、SPLIT、PAUSE、ABORT）對應到凍結的 app.PolicyAction 列舉如下：ALLOW->PolicyRun、WARN->PolicyWarn、CHECKPOINT->PolicyCheckpointAndRun、SPLIT->PolicySplit（本波未使用——agents/predictor.md 最初的 policy 建議或 ADD §17 都沒有列出任何 SPLIT 觸發條件）、PAUSE->PolicyPause、ABORT->PolicyBlock（對應 ADD §17.3 優先層級 1 的『explicit deny/security』，以及 ADD §21.9 的『block』JSON decision 字面值——凍結的列舉中並沒有字面上的 ABORT，PolicyBlock 是硬性中止拒絕最貼近語意的對應）。PolicyRequireConfirmation 與 PolicyPauseAndAutoResume（此節點交付清單未另外具名的兩個凍結動作）分別用於 ADD §17.3『high risk -> require confirmation』的優先層級，以及供未來角色在普通的 PAUSE 之上疊加自動恢復授權——並非發明出來的，兩者皆已凍結於 ports.go 中。"
  - "Decide 的優先順序逐字實作 ADD §17.3（explicit deny/security > integrity failure > active graceful-pause trigger > mandatory state checkpoint boundary > risk bands），依此固定順序逐一評估各關卡，一旦命中即回傳。ExplicitDeny／IntegrityFailure／MandatoryCheckpointBoundary 皆由呼叫端提供布林值，並非由本 package 自行偵測——偵測安全性拒絕、checkpoint checksum 不符，或 Progress Tree 節點種類／轉換，都需要本角色邊界明確排除的能力（『No provider JSON parsing, Git commands, checkpoint creation, or process interruption』）。"
  - "ADD §16.5 的級距表（<0.45 low/ALLOW、0.45-0.65 medium/WARN、0.65-0.85 high/REQUIRE_CONFIRMATION 或 CHECKPOINT、>=0.85 critical/CHECKPOINT）的實作方式，是在每個下界都採用『>=』（分數剛好等於門檻時，進入較高的級距），並由 0.45／0.65／0.85 三個確切邊界的表格測試驗證。特別是在『high』級距內，agents/predictor.md 自身的細化規則（『predicted P90 exceeds available headroom or high blast radius: CHECKPOINT』）實作為：當 BlastRadiusRisk.Score 本身也 >=0.85 時，優先選擇 CHECKPOINT_AND_RUN 而非 REQUIRE_CONFIRMATION——這是一項已記錄、僅限本 package 內部的門檻（blastRadiusHighThreshold），因為 ADD 並未指定有別於共用級距表的 blast-radius 專屬 checkpoint 門檻。"
  - "ADD §17.4 的已校準自動暫停規則（hit_probability >= 0.80）與 §17.6 的 debounce（需連續兩次符合條件的觀測，而非一次）皆已實作，但本 package 每次呼叫都是無狀態的（對應 runway.Scorer 自身的先例）——由呼叫端透過 DecideRequest.PriorRunwayHitConfirmed 提供 debounce 所需的跨呼叫歷史這一個 bit。§17.6 的其他分支（間隔至少 5 秒、quota 樣本新鮮度 <=30 秒、風險必須先降到 0.70 以下才能重新武裝）並未在本 package 中另外重新實作：這些分支都不需要 domain.RunwayForecast 與 PriorRunwayHitConfirmed 在單次 Decide 呼叫中已經帶入的資訊之外的任何東西，而重新推導本 package 自身無法觀測到的區間／過時性記錄（它從不會看到跨呼叫的原始時間戳記）不是悄悄用猜的，就是重複呼叫端本來就必須自己保有的狀態——這一點已在 coldstart.go 中明確記錄，而不是悄悄略過。"
  - "ADD §17.6 的緊急觸發條件（provider 回報已達上限，或 used>=98%，或估計距上限的 P50 時間 <=60 秒）以 isRunwayEmergency 實作，直接檢查 domain.RunwayForecast.CurrentUsedPercent 與 EstimatedTimeToLimitP50Seconds，並把 RiskScore==1.0 且 Confidence==high 視為已經內含『provider 回報已達上限』訊號的情況（RunwayForecast 本身並沒有針對該條件的獨立布林欄位——runway.Scorer 自身的 Score 實作，在更上一層已經把 QuotaObservation.Reached 收斂為正好這種 RiskScore／Confidence 組合，依 runway.go 自身 Score 的文件註解）。此緊急路徑一律回傳 Probability=nil，並在 PolicyReasonCodes 中包含字面字串『emergency_threshold』（ReasonEmergencyThreshold），絕不會是機率值——這正是 agents/predictor.md 最初 policy 建議中的確切字面值。"
  - "Decision.ReasonCodes 只帶有凍結的 domain.ReasonCode 列舉值（來自 CombineRiskResult 各元件的 ReasonCodes）；Decision.PolicyReasonCodes 則是另一個獨立的、僅限本 package 內部的純字串 slice，用於這個 pipeline 階段自身的詞彙（呼應 internal/predictor/runway 已建立的先例，即擁有一個獨立於 domain.ReasonCode 的純字串 reason-code 命名空間），同時也是 runway 來源 reason 字串的存放處，因為 domain.RunwayForecast.ReasonCodes 凍結為 []string，而非 []domain.ReasonCode（runway/runway.go 自身的文件註解說明了原因：RunwayForecast 的出現早於 ADR-041 導入具型別的 ReasonCode）。這種雙欄位拆分，避免從 runway 的純字串或本 package 自身的觸發條件名稱中，捏造出新的 domain.ReasonCode 列舉值，讓凍結的列舉依 Constitution §1 保持封閉。"
  - "clamp01Risk 完全對應 internal/predictor/risk.clamp01（NaN 會被限制為 1.0，即最保守／風險最高的值，絕不會是平淡的低分），並套用在本 package 回傳的每一個 RiskScore 上，無論其讀取自哪一個上游輸入（CombineRiskResult.OverallRisk.Score、RunwayForecast.RiskScore，或用於 high-band blast-radius 檢查的 BlastRadiusRisk.Score）——這是在初版實作的 fail-open／fail-closed 測試抓到一個實際存在的 NaN／Inf 傳播缺口後才加上的（RiskScore 在四條 Decide 路徑中有三條未經限制就直接傳遞下去）；修正本身與抓到它的測試都在本波的 commit 中，並未延後處理。"
  - "專屬的 ColdStart 測試套件（coldstart_test.go）之所以撰寫，是為了明確證明範圍較窄、也是正確的主張：『未校準絕不會變成機率』，而不是另一個不同（且錯誤）的主張『機率恆為 nil』——TestColdStartArmedButNotYetConfirmedRunwayIsCalibratedAndMayReportProbability 是一個刻意設計的對照案例，用來證明：如果 Decide 矯枉過正，即使在一個真正已校準的 runway 輸入正當地要求填入 Probability 時，也從不填入，這個測試套件就會失敗。"
blockers: []
```

## 第 6 波總結

本波唯一指派的節點（`predictor-08`）已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依指示先 merge 了 `origin/main`（乾淨的 fast-forward，`216c92b` -> `abce1d0`），並確認在撰寫任何新程式碼之前建置／測試皆乾淨通過。`internal/policy` 是一個新的無狀態 package，依兩個直接來自 ADR-041 凍結的 pipeline 輸入（來自 `predictor-07` 之 `RiskCombiner` 的 `app.CombineRiskResult`；來自 `predictor-06` 之 Runway Predictor 的 `domain.RunwayForecast`，依 ADR-041「Policy consumes Runway directly」的更正直接消費——絕不透過 `RiskCombiner`），實作 ADD §17 的 Policy Engine（優先順序 §17.3、風險級距 §16.5、debounce／緊急情況 §17.6、已校準自動暫停 §17.4）。`internal/app/ports.go` 目前尚無專屬的凍結 `Policy` 介面；此節點依 CONTRACT_FREEZE.md 明確預期「owning roles 可能需要 Bootstrap 時期最小 DTO 之外的額外欄位」，建構了完全以凍結的 `app.PolicyAction` 列舉表達的自有橋接型別 `policy.Decider`／`Decision`／`DecideRequest`。此節點唯一具承載性的不變條件（Constitution §6/§7：未校準的分數絕不可標示為機率）以結構性方式強制執行——整個 package 中只有恰好兩處呼叫點會把非 nil 值指派給 `Decision.Probability`，且皆在指派前緊接著一個明確的 `rf.Calibrated &&` 檢查——並由一個專屬的 `coldstart_test.go` 測試套件（9 個頂層測試，`-run ColdStart` 可選中整個檔案）證明，涵蓋每一個可觸及的風險級距、緊急 PAUSE 路徑、強制 checkpoint 邊界路徑、明確拒絕／完整性失敗路徑、一次全網格隨機性掃描，以及一個刻意設計的對照案例，證明已校準的 runway 輸入仍然可以正當地產出機率（因此本節點證明的不變條件是「未校準絕不會變成機率」，而不是更強、也錯誤的「機率恆為 nil」）。本波自身的 fail-open／fail-closed 測試抓到一個真實的 bug（RiskScore 在四條 Decide 路徑中有三條未經 NaN／Inf 限制就傳遞下去），並在同一個 commit 中修正，並未延後。`BenchmarkDecide` 測得 52.83 ns/op、16 B/op、1 allocs/op——遠低於 ADD §29.11 的 <1ms policy 目標。未觸碰任何其他角色的路徑；`internal/evaluation/**` 維持不變。截至本波最終 commit 為止，`golangci-lint run ./...` 回報全 repo 0 個問題。

## 第 7 波（predictor-09）

分支：`vertical-slice/predictor`，接續自 `21c7dfd`（第 6 波，`predictor-08`）。依明確指示，先 merge 了 `origin/main`（`git fetch origin && git merge origin/main`）——從 `21c7dfd` 到 `1440f4c` 的乾淨 fast-forward，帶入第 6 波整合後的狀態（本角色自身已 merge 的 `predictor-08`，加上 `checkpoint` 的 Part A progress／state-checkpoint package（`internal/pause`、`internal/progress`、`internal/redact`、`internal/scheduler`、`internal/statecheckpoint`）以及新的 migrations `0023`／`0024`）。merge 完成後、在撰寫任何新程式碼之前，全 repo 的 `go build ./...` 與 `go test ./...` 皆乾淨通過。依指示，開始前重新閱讀了 `CONSTITUTION.md`、`agents/predictor.md`、`docs/implementation/vertical-slice/EXECUTION_DAG.md`（`predictor-09` 的條目——依賴 `predictor-01`、`predictor-08`）、`docs/implementation/vertical-slice/CONTRACT_FREEZE.md`，以及 `internal/app/ports.go` 中凍結的 `EvaluationService` 介面與 DTO 形狀。本波僅指派 `predictor-09`（Evaluation persistence）。

**路徑確認**：`internal/evaluation` 未出現在 `CONTRACT_FREEZE.md` 的「Import paths」表中（該表只列出 `internal/domain`、`internal/app`、`pkg/protocol/v1`、`internal/storage/sqlite`），因此依 `agents/predictor.md` 自身的指示（「If internal/evaluation is absent from the frozen layout, use the exact path assigned by the contract-integrator; do not create a competing package」），此節點在建置前先查驗了本角色自己在第 4 波撰寫的 migration 檔案註解以確認。migration `0041_predictions.sql` 的註解寫道：「predictor's persistence layer (predictor-09) is responsible for keeping it consistent with turns.id」；`0043_policy_decisions.sql` 與 `0044_authorizations.sql` 同樣以確切的 ID「predictor-09」／「predictor-10」對應這個確切的路徑。除此之外並無另外的 contract-integrator 簽核（Bootstrap 依 CONTRACT_FREEZE.md 自身「What Bootstrap did NOT freeze」章節，明確將「predictor internals」延後處理）——這是正確、不會與其他 package 競爭的路徑。

```yaml
node: predictor-09
status: completed
artifacts:
  - internal/evaluation/doc.go
  - internal/evaluation/datasource.go
  - internal/evaluation/store.go
  - internal/evaluation/service.go
  - internal/evaluation/pipeline.go
  - internal/evaluation/helpers_test.go
  - internal/evaluation/service_test.go
  - internal/evaluation/authorization_test.go
validation:
  - "gofmt -l internal/evaluation  # clean"
  - "go build ./internal/evaluation/...  # ok"
  - "go vet ./internal/evaluation/...  # ok"
  - "go test ./internal/evaluation/... -v  # PASS (20 top-level tests: EvaluateTurn persistence/validation/determinism/error-propagation, GetEvaluation lookup/not-found/validation, Decide read-back/not-found/validation, ConsumeAuthorization consume-exactly-once, concurrent-replay-only-one-wins, wrong-session-rejected, wrong-prompt-rejected, stale/expired-rejected, exact-boundary-succeeds, exact-expiry-rejected, unknown-id-not-found, empty-ids-rejected, default-TTL)"
  - "go test ./internal/evaluation/... -race  # PASS, including the dedicated 8-goroutine concurrent-replay test"
  - "go build ./...  # ok, whole module"
  - "go test ./... -race  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide (one errorlint finding in a first draft — a bare `err.(*domain.Error)` type assertion in a test helper — caught and fixed by switching to errors.As before this phase's final commit)"
commit: <see final report>
next_action: 無——predictor-09 是本波唯一指派的節點；predictor-10／predictor-11 明確不在範圍內，除了 app.EvaluationService 的 ConsumeAuthorization 方法為了能夠編譯並正確運作所必須存在的部分之外，未開始、未 stub、也未 scaffold
assumptions:
  - "本波在三個來源之間存在一項真實、已記錄的衝突：任務指示（明確要求把完整的 ConsumeAuthorization——恰好一次消費、過期、prompt／session 綁定、重放拒絕——作為 predictor-09 的一部分建構出來，並詳細列出所需測試）、docs/implementation/vertical-slice/EXECUTION_DAG.md（把正是這個行為指派給另一個獨立的下游節點 predictor-10——『One-time authorization』，依賴：predictor-09，它自己有一個專屬的 -run Authorization 驗證關卡），以及 migration 0044_authorizations.sql 自身的註解（由本角色在第 4 波撰寫），該註解寫道恰好一次消費『enforced by predictor-10's service logic』。解決方式為：現在就實際、原子性地建構出真正的 ConsumeAuthorization——Go 介面無法只部分實作，且 Constitution §7 rule 11 禁止在沒有可長期留存證據的情況下宣告任務完成，因此 stub 會比任何一個替代方案都更糟——同時在 doc.go 的『ConsumeAuthorization scope note』中明確記錄這項衝突，並將 predictor-10 自身專屬的驗證關卡，視為此行為在未來波次針對真實 runtime-a08／runtime-b06 整合需求重新驗證／延伸的權威場所，依 Constitution §4『以已記錄的假設繞過此缺口』的指示，而不是悄悄挑選其中一個指示、隱藏其餘的落差。"
  - "Decide 讀回的是 EvaluateTurn 已經為給定的 EvaluationID 計算並持久化的 policy_decisions 資料列，而不是透過 internal/policy.Decider 重新計算。這一點有直接證據佐證，並非用猜的：internal/orchestrator/evaluate.go（已 merge、屬於姊妹角色的程式碼）在 EvaluateTurn 之後立刻呼叫 Decide(ctx, app.DecideRequest{EvaluationID: evaluation.ID})，且並無 risk／runway 內容可供傳遞——app.DecideRequest 本身只帶有一個 EvaluationID 欄位（凍結於 internal/app/ports.go）。在此重新計算不僅不理想，就 Decide 實際收到的輸入而言根本不可能做到。此點已在 doc.go 的『Decide: read-back, not recompute』章節中明確記錄，依 agents/predictor.md 自身要求對此選擇要明確說明的指示。"
  - "EvaluateTurn 本身並不會計算新的 domain.RunwayForecast——ADR-041 說明獨立的 Runway Predictor 是接入 GracefulPauseService.Observe，一個由 runtime 擁有的不同凍結 port，並明確指出它並非 RiskCombiner 的輸入。此節點的 DataSource.RunwayForecast 方法只是把目前最近一次計算出的 forecast（若有）呈現出來，純粹是為了讓 policy.Decider 由 runway 驅動的 PAUSE 關卡能在同一次 pipeline 呼叫中被評估；若缺少 forecast（hasRunway=false，例如全新的 session），會退化為零值的 domain.RunwayForecast{}，而 policy.Decider 依其自身已記錄的 fail-open 紀律，早已把這種情況視為 Calibrated=false／不值得暫停——這裡並未發明任何新的退化規則。"
  - "DataSource 是本 package 自身狹窄的橋接介面（並非凍結的 internal/app port），沿用 internal/predictor/scope.FeatureSource 與 internal/predictor/token.FeatureSource 已建立的確切先例：app.EvaluateTurnRequest 只帶有 SessionID/TurnID/Provider/PromptHash（已凍結，受隱私合約限制——絕不含原始 prompt 文字），因此其他所有 pipeline 輸入（repository／task 解析、分類、repository／session／progress 特徵、quota／context 觀測、prior-runway-hit-confirmed 的 debounce bit）都透過這個 package 自有的介面解析，此處由測試用的 fake 滿足，未來波次則由某個 wiring 角色提供的具體、以儲存為後盾的查詢滿足。兩個姊妹階段同名的 FeatureSource.Progress 方法彼此簽章不同（scope.FeatureSource.Progress 接收 *domain.TaskID；token.FeatureSource.Progress 接收 domain.SessionID）——這是透過並列閱讀兩者才確認的，而不是假設結構一致；本 package 的測試輔助函式，逐一方法明確地把 DataSource 轉接到各階段自身的 FeatureSource，而不是依賴 struct 內嵌提升，因為那樣可能會悄悄編譯出錯誤的方法形狀。"
  - "IssueAuthorization（核發）是在凍結的 app.EvaluationService 介面（該介面只定義了 ConsumeAuthorization，沒有核發方法）之外，對 Service 新增的方法——呼應 internal/policy.Decider 自身『在嚴格凍結的合約之外建構一個已記錄的 package 內部橋接型別／方法』的先例，理由相同：agents/predictor.md 交付項 #12 把『one-time authorization issuance/consumption』列為一項交付項目，而若沒有真正的核發路徑，ConsumeAuthorization 便無物可消費。EvaluateTurn 本身在本波並不會自動呼叫 IssueAuthorization——決定哪些 PolicyAction 需要授權、以及要綁定何種 SnapshotFingerprint／RepositoryCheckpointID，都是編排層（orchestration-layer）的決策，不在本 package 的邊界之內（不建立 checkpoint、不執行 Git 指令）；預期未來的 wiring 層會明確呼叫它。"
  - "feature_vectors／predictions／policy_decisions 在每次 EvaluateTurn 中，都會在同一個 app.TxRunner.WithTx 呼叫內持久化，儘管 CONTRACT_FREEZE.md 的 transaction-boundary 章節明確為本 package 具名的只有 ConsumeAuthorization——但『部分寫入即為無效狀態』的相同推理可直接類推適用（一筆沒有對應 policy_decisions 資料列的 predictions 資料列，就是一次不完整的 evaluation），而且 checkpoint 自身的 ProgressTreeService.CompleteNode（屬於另一個角色的 package）早已為多表完成寫入建立了完全相同的模式，因此這裡是遵循既有的專案慣例，而不是發明新的做法。"
  - "ConsumeAuthorization 恰好一次的保證，是在與綁定／過期檢查相同的交易內（store.go 的 markAuthorizationConsumed），執行單一條件式的 UPDATE authorizations SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL，逐字對應 migration 0044 自身的註解（『enforced by predictor's service logic checking consumed_at IS NULL before consuming, inside the same transaction』）。這樣可以杜絕應用層級『先讀、再檢查、再寫入』模式會出現的 TOCTOU 競態；除了 go test -race 之外，另外由一個專屬的並行 goroutine 測試直接驗證（8 個 goroutine 競爭同一個 authorization ID，斷言恰好 1 次成功），因為 -race 本身只能證明不存在資料競態（data race），無法證明不存在邏輯競態（logic race）。"
  - "ConsumeAuthorization 中錯誤 session／錯誤 prompt 的檢查，會在重放（已消費）檢查之前評估，因此若呼叫端提供了不符的綁定，會得到 ErrCodeUnauthorized，而不是令人困惑的衝突／重放代碼——呼叫端錯誤（錯誤的 ID／綁定）與正當的重放嘗試是不同的失敗類別，不應共用同一個錯誤代碼。PromptHash 綁定僅在 req.PromptHash 非空時才檢查（呼應 app.ConsumeAuthorizationRequest 自身欄位在凍結 DTO 中實質上是選填的——並無驗證要求呼叫端必須永遠提供它），而 TurnID 綁定則永遠會檢查（必填，在方法入口處驗證非空）。"
blockers: []
```

```yaml
node: predictor-10
status: completed
artifacts:
  - internal/evaluation/service.go
  - internal/evaluation/authorization_test.go
  - internal/evaluation/doc.go
validation:
  - "gofmt -l internal/evaluation  # clean"
  - "go build ./internal/evaluation/...  # ok"
  - "go vet ./internal/evaluation/...  # ok"
  - "go test ./internal/evaluation/... -run Authorization -v  # PASS (21 tests — see full enumeration below)"
  - "go test ./internal/evaluation/... -race -count=1  # PASS, whole package"
  - "go build ./...  # ok, whole module"
  - "go test ./... -race -count=1  # PASS, whole module, no regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide (three errcheck findings on unused *domain.Error returns from a new test helper, caught and fixed by prefixing the standalone-statement call sites with `_ =` before this phase's final commit)"
commit: <see final report>
next_action: 無——predictor-10 是第 8 波唯一指派的節點；predictor-11 明確不在範圍內，未開始
assumptions:
  - "這是一個稽核／強化節點，而非重建：先完整重讀了 predictor-09 的 ConsumeAuthorization／IssueAuthorization 實作，然後針對超出其原始 8-goroutine 並行重放測試範圍的情境進行對抗性測試，依本波明確指示，只修正真實存在的缺口，不對已經正確的行為製造多餘的工作。"
  - "發現並修正了一個真實的缺口：prompt-hash 綁定檢查（if req.PromptHash != \"\" && row.PromptHash != req.PromptHash）只要「請求」本身省略了 PromptHash，就會略過比對，而不管該 authorization「資料列」在核發時實際綁定的是什麼。一個只知道 AuthorizationID + TurnID（沒有 prompt hash——例如透過 log 洩漏，或是有 bug／惡意的呼叫端）的呼叫端，只要不提供 PromptHash，就能消費一個原本綁定特定 prompt 的授權，使 prompt 綁定這項控管形同虛設。這是一個潛在的缺口，目前這個程式碼樹中尚無任何已接線的呼叫端會觸發它（目前尚無呼叫端呼叫 ConsumeAuthorization——未來波次的 runtime-b06 會是第一個），但這確實是該函式自身防禦性合約中的一項真實缺陷，而 predictor-09 自身已記錄的假設 #530 明確把這個確切行為（『PromptHash binding is checked only when req.PromptHash is non-empty』）當成一項已記錄、但未經稽核的設計選擇——這正是本節點的對抗性檢查要驗證、而非照單全收的那類主張。修正方式為改以 authorization「資料列」自身的 PromptHash 作為略過的判斷依據（row.PromptHash != \"\" && row.PromptHash != req.PromptHash）：現在綁定的評估依據，是該授權實際核發時綁定的內容，而不是呼叫端是否選擇主張它；唯一合法的略過情境（一個刻意核發時完全沒有 prompt hash 的授權，row.PromptHash == \"\"）依然可行，並且現在由一個專屬測試（TestConsumeAuthorization_AllowsOmittedPromptWhenAuthorizationHasNone）固定下來，讓未來的『修正』不會再次把這兩種情境混為一談而重新引入這個繞過漏洞。這項修正的真實性（而非假設性）已透過還原 service.go 並確認新的對抗性測試（TestConsumeAuthorization_RejectsOmittedPromptAgainstBoundAuthorization）在沒有它時會失敗、恢復後則會通過，驗證屬實。"
  - "其餘每一項對抗性測試情境——更高並行度的重放（64 個 goroutine，相對於 predictor-09 原本的 8 個）、對一個已消費的授權進行 200 次緊湊的循序重放迴圈、在確切過期邊界附近競爭的重放嘗試（在過期前 1 秒發起一波並行消費者，接著以一次循序後續呼叫，確認贏家的消費結果會被保留，且之後回報的是衝突而非過期）、奈秒級相鄰的過期邊界（ExpiresAt 前後各 1 奈秒，比 predictor-09 原本 1 秒粒度的邊界測試更嚴格）、僅有空白字元差異的 prompt-hash 不符、TurnID 與 PromptHash 兩項綁定的大小寫敏感度，以及位元組不同但正規化後等價的 unicode 形式（預先組合的 U+00E9 對比分解後的 e+U+0301）——這些測試在 predictor-09 既有、未經改動的邏輯下，第一次嘗試就全數通過。透過直接檢視程式碼確認：本程式碼庫在 internal/storage/sqlite/migrations/0044_authorizations.sql 中任何地方都沒有 COLLATE NOCASE，沒有任何不分大小寫的 Go 字串比對（沒有 strings.EqualFold／ToLower／ToUpper 觸及 TurnID／PromptHash），internal/evaluation 中任何地方也都沒有 TrimSpace／正規化步驟——所有綁定比對都是對原始字串使用純粹的 Go !=，這正是正確、不出人意料的安全姿態（位元組精確比對，不會意外因大小寫或空白而混淆），因此不需要任何程式碼變更，只需要確認測試即可。"
  - "儲存層恰好一次的保證（單一條件式的 UPDATE authorizations SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL）已確認其在設計上就與並行程度無關，而不只是在 8 個 goroutine 時運氣好：internal/storage/sqlite/db.go 設定了 WAL journal mode，加上 5000ms 的 busy_timeout PRAGMA（依 foundation-07 自身已記錄的順序要求，busy_timeout 在 journal_mode 之前套用），以及最多 8 個開啟連線的連線池，因此並行寫入者會在 busy_timeout 視窗內序列化並重試，而不是在較高並行度下發生不可預期的錯誤——這正是為什麼把並行測試從 8 個提升到 64 個 goroutine 時，預期（也確實）在不改動任何程式碼的情況下通過，現在這已成為一項永久的迴歸防護（TestConsumeAuthorization_HighContentionReplayOnlyOneWins），而不只是一次性的人工檢查。"
  - "authorization_test.go 經過重新整理（而不只是附加內容），分成四個具名區塊——恰好一次／重放、prompt／session 綁定、過期精確度、基準／管線配置——讓 go test ./internal/evaluation/... -run Authorization 產出的結果，能讓外部稽核人員獨立閱讀，視為一份條理一致的安全性測試套件，依本波明確指示辦理。所有 predictor-09 原本就通過的測試，其原始斷言與行為皆維持不變（僅有少數幾個把手動的 errors.As／domain.Error 樣板程式碼，改為共用的 requireDomainError 測試輔助函式以提升可讀性——這是純粹的重構，已透過在改動前後執行完整套件確認）。"
blockers: []
```

## 第 7 波總結

本波唯一指派的節點（`predictor-09`）已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依指示先 merge 了 `origin/main`（乾淨的 fast-forward，`21c7dfd` -> `1440f4c`），並確認在撰寫任何新程式碼之前建置／測試皆乾淨通過。`internal/evaluation` 是一個新的 package，透過把先前每一個 predictor 角色的 package 串接成一條真正的 pipeline：Scope Estimator（`internal/predictor/scope`）→ Token Forecaster（`internal/predictor/token`）→ Quota Forecaster（`internal/predictor/quota`）→ Risk Combiner（`internal/predictor/risk`）→ Policy（`internal/policy`），實作了凍結的 `app.EvaluationService`（ADD §9.9），並在 `app.TxRunner.WithTx` 交易內，把結果持久化到本角色自有的 `feature_vectors`／`predictions`／`policy_decisions`／`authorizations` 資料表（migrations 0040-0044，第 4 波／`predictor-01`）。`var _ app.EvaluationService = (*Service)(nil)` 斷言此凍結介面被精確滿足，未加以擴充。發現並解決了一項真實的、三方之間的已記錄衝突，而不是悄悄挑一邊：任務指示要求在 predictor-09 之下建構完整的 `ConsumeAuthorization`，而 DAG 與本角色自己先前的 migration 檔案註解，則把這個行為指派給另一個獨立的下游節點 `predictor-10`。`ConsumeAuthorization` 被實際建構出來（透過單一條件式的 `UPDATE ... WHERE consumed_at IS NULL` 達成恰好一次消費、透過注入的 `domain.Clock`——絕不直接使用 `time.Now()`——實現以時鐘為準的過期判斷，以及 prompt／session 綁定檢查），因為凍結的 Go 介面無法只部分實作，且 Constitution §7 rule 11 禁止以 stub 宣告任務完成；這項衝突本身已記錄在 `doc.go` 與本文件中，而非隱藏起來，並且 predictor-10 自身專屬的 `-run Authorization` 驗證關卡，仍被視為此行為在未來波次重新驗證／延伸的權威場所。`Decide` 讀回已持久化的 policy decision，而不是重新計算一次，這一點的正確性，是透過閱讀已 merge 的姊妹角色程式碼（`internal/orchestrator/evaluate.go`）確認的，該程式碼呼叫時只帶有 `EvaluationID` 可用——而不是單憑凍結的 DTO 形狀用猜的。20 個頂層測試涵蓋相同輸入下的確定性輸出、恰好一次消費（包含一個 8-goroutine 並行重放的競態測試）、錯誤 session／錯誤 prompt 的拒絕，以及在確切邊界內外以時鐘為準的過期判斷。未觸碰任何其他角色的路徑；`internal/policy/**` 與 `internal/predictor/**` 維持 `predictor-08`／更早波次留下的原樣。截至本波最終 commit 為止，`golangci-lint run ./...` 回報全 repo 0 個問題（初稿中的一項 `errorlint` 發現，已在最終狀態之前抓到並修正）。

## 第 8 波總結

本波（第 8 波）唯一指派的節點（`predictor-10`）已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依指示先 merge 了 `origin/main`（乾淨的 fast-forward，`efd0601` -> `2b7c29c`，帶入第 7 波整合後的狀態），並確認在撰寫任何新程式碼之前建置／測試皆乾淨通過。這是一個針對 predictor-09 既有 `ConsumeAuthorization`／`IssueAuthorization` 實作的稽核／強化節點，而非重建——先重讀了 predictor-09 自身的程式碼、Constitution、`agents/predictor.md`、`CONTRACT_FREEZE.md`，以及 EXECUTION_DAG 中 predictor-10 的條目。此次稽核發現並修正了恰好一項真實的缺口：`ConsumeAuthorization` 的 prompt-hash 綁定檢查，過去只要「請求」省略了 `PromptHash`（`req.PromptHash != "" && ...`）就會略過比對，而不管該授權「資料列」在核發時實際綁定的是什麼——一個只知道 `AuthorizationID` 與 `TurnID` 的呼叫端，可以單純不提供 prompt hash 就完全繞過 prompt 綁定。修正方式改為以授權資料列自身的 `PromptHash` 作為略過的判斷依據（`row.PromptHash != "" && row.PromptHash != req.PromptHash`），因此綁定現在依授權實際核發時綁定的內容來評估。這項修正已驗證為真實存在、而非假設性的缺陷：還原這一行修正後重新執行新的對抗性測試（`TestConsumeAuthorization_RejectsOmittedPromptAgainstBoundAuthorization`），會以「expected an error with code unauthorized, got nil」失敗；恢復修正後則會通過。本波執行的其餘每一項對抗性情境——64-goroutine 高並行重放（predictor-09 原本的 8 倍）、200 次緊湊循序重放迴圈、在確切過期邊界附近競爭的重放嘗試、奈秒級相鄰的過期邊界（`ExpiresAt` 前後各 1 奈秒）、僅有空白字元差異的 prompt-hash 不符、兩個綁定欄位的大小寫敏感度，以及位元組不同但 unicode 正規化後等價的形式——皆在 predictor-09 既有邏輯下第一次嘗試就通過，確認（而非假設）此程式碼庫在 authorization 路徑中，任何地方都沒有意外的不分大小寫或正規化比對（migration 0044 中沒有 `COLLATE NOCASE`，`internal/evaluation` 中任何地方也沒有 `strings.EqualFold`／`ToLower`／`TrimSpace` 觸及 `TurnID`／`PromptHash`）。`authorization_test.go` 已重新整理成四個具名區塊（恰好一次／重放、prompt／session 綁定、過期精確度、基準／管線配置），依本波指示，讓其專屬的 `-run Authorization` 驗證關卡，能產出可供外部稽核人員獨立閱讀的測試套件——所有 predictor-09 原本就通過的測試，行為皆維持不變。未觸碰任何其他角色的路徑。截至本波最終 commit 為止，`golangci-lint run ./...` 回報全 repo 0 個問題（針對一個新的共用測試輔助函式中未使用的 `*domain.Error` 回傳值，有三項 `errcheck` 發現，已在最終 commit 前透過在獨立陳述式呼叫處加上 `_ =` 前綴修正）。

## 第 9 波（predictor-11 — 最終 DAG 節點）

分支：`vertical-slice/predictor`，接續自 `379b7cf`（第 8 波，`predictor-10`）。依明確指示，先 merge 了 `origin/main`（`git fetch origin && git merge origin/main`）——從 `379b7cf` 到 `36e7ffb` 的乾淨 fast-forward，帶入第 8 波整合後的狀態（`checkpoint-a06/a08/b08` 加上一項 tracked-diff redaction 修正、本角色自身已 merge 的 `predictor-10`、`runtime-a08`、`qa-04`，以及新的 package `internal/gitx/patch.go`、`internal/pause/resumevalidation.go`、`internal/repocheckpoint/patchredact.go` + `restoredryrun.go`、`internal/statecheckpoint/reconcile.go`）。merge 完成後、在撰寫任何新程式碼之前，全 repo 的 `go build ./...` 與 `go test ./...` 皆乾淨通過。依指示，開始前完整重新閱讀了 `CONSTITUTION.md`、`agents/predictor.md`（尤其是「Required tests」）、`docs/implementation/vertical-slice/EXECUTION_DAG.md` 中 `predictor-11` 的條目、`docs/implementation/vertical-slice/CONTRACT_FREEZE.md`、`docs/adr/0041-predictor-forecast-layer.md`，以及先前每一波自身的產出物（`internal/features/**`、`internal/predictor/{quantile,scope,token,quota,runway,risk}/**`、`internal/policy/**`、`internal/evaluation/**`）。

依 DAG 與任務指示，此節點的工作並非新功能，而是一次全面、最終的證明：整個 Scope Estimator -> Token Forecaster -> Quota Forecaster -> Risk Combiner -> Policy -> Evaluation persistence/authorization 鏈，在真實情境的綜合負載下能夠端對端（END-TO-END）正確運作，且速度足夠快——具體涵蓋全鏈路的性質測試、確定性輸出、reason-code golden 測試、每個階段交接處的對抗性 fail-open／fail-closed 測試、完整的 `EvaluateTurn -> Decide -> ConsumeAuthorization` 流程，以及全鏈路 benchmark——這些都不是任何單一 predictor-0N 節點自身的 package 層級測試會綜合涵蓋到的。

```yaml
node: predictor-11
status: completed
artifacts:
  - internal/evaluation/pipeline_e2e_test.go
  - internal/evaluation/helpers_test.go (extended: error-injecting DataSource fields, four errInjecting*
    pipeline-stage wrappers, testStages bundle, newTestServiceWithStages)
validation:
  - "gofmt -l internal/predictor internal/policy internal/evaluation internal/features  # clean"
  - "go build ./...  # ok, whole module"
  - "go vet ./internal/predictor/... ./internal/policy/... ./internal/evaluation/...  # ok"
  - "go test ./internal/predictor/... ./internal/policy/... ./internal/evaluation/... -race -bench=. -benchmem -v  # PASS, 113 top-level PASS lines, 0 FAIL, across 8 packages (predictor, predictor/quota, predictor/risk, predictor/runway, predictor/scope, predictor/token, policy, evaluation)"
  - "go build ./... && go test ./... -race  # PASS, whole module (33 packages), zero regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide (one copyloopvar finding in a first draft — a redundant Go-1.22+ loop-var copy — caught and fixed before the final commit)"
commit: <see final report>
next_action: 無——predictor-11 是本角色最後一個指派的 DAG 節點；predictor-01 至 predictor-11 每一個節點現皆已 completed；本角色範圍內已無任何剩餘工作
assumptions:
  - "此節點的範圍（依 agents/predictor.md 的 Boundary 與任務指示）僅限 internal/predictor/**、internal/policy/**、internal/evaluation/**——internal/features/** 已讀取並重新驗證，但未修改，因為此次稽核並未發現可歸因於該 package 的缺口。"
  - "以寬表格驅動的全鏈路模糊測試（TestFullPipeline_WideTableFuzz，internal/evaluation/pipeline_e2e_test.go）結合了 11 個手工挑選的 fixture（cold-start、完整填入的低風險情境、高 scope／高 quota 壓力情境、quota 的 Reached 旗標為 true、負值／零值的退化輸入、極端／MaxFloat64／MaxInt64 量級的輸入、三種涵蓋 armed／confirmed／emergency debounce 狀態的 runway-forecast 形狀、nil-TaskID 與空 slice），加上 200 筆以程式產生的隨機案例（quota／context 的 UsedPercent 掃過大約 [-75,175] 的範圍、token-history 樣本數 0-40、runway RiskScore 偶爾 >1.0）——刻意包含病態／超出規格的數值（負百分比、RiskScore>1.0），因為一個真正的交接錯誤，同樣可能來自上游階段自身的退化輸出，就跟來自直接惡意的外部輸入一樣可能。"
  - "fail-open／fail-closed 對抗性測試（pipeline_e2e_test.go 第 4 節）需要擴充 internal/evaluation/helpers_test.go 既有的 fakeDataSource（predictor-09 原始的 fixture，原本只用來練習 Resolve 錯誤路徑），在 DataSource 九個方法的每一個上加上可注入的錯誤欄位，再加上四個新的 errInjecting* 包裝型別（errInjectingScopeEstimator、errInjectingTokenForecaster、errInjectingQuotaForecaster、errInjectingRiskCombiner），直接實作 ADR-041 的四個 app 介面，讓測試可以強制恰好一個 pipeline 階段失敗／退化，而其餘三個階段對照正式的 wiring 真實執行（realStages + newTestServiceWithStages）。這只是對既有的測試專用檔案的新增，並未變更任何正式程式碼，也未變更 fakeDataSource 既有零值（所有欄位皆未設定）的預設行為，所有既有的 predictor-09／predictor-10 測試仍不受影響地依賴它。"
  - "對於真正的上游錯誤（有別於合法的 cold-start／已退化但仍存在的結果），「朝安全方向失敗」的定義與驗證方式為：EvaluateTurn 回傳非 nil 的錯誤，且不會在 predictions 資料表中為該 TurnID 留下任何資料列（在 TestFullPipeline_UpstreamErrorsFailClosed_NeverSilentAllow 中直接以 SQL 檢查，而不只是檢視回傳的錯誤）——對應 CONTRACT_FREEZE.md 的 transaction-boundary 章節，把部分持久化，而不只是錯誤的回傳值，具名為一個 WithTx 包裹的操作必須防止的特定失敗模式。"
  - "DAG 自身具名的情境（「TokenForecaster returns all-nil」）被模擬為一種與 Go 錯誤回傳不同的失敗「形狀」——errInjectingTokenForecaster.nilResult 回傳一個零值的 domain.TokenForecast，錯誤為 NIL，模擬一個退化但不報錯的階段。TestFullPipeline_StageErrorsFailClosed/token_forecaster_returns_all_nil 斷言這個合法上不同的結果：EvaluateTurn 仍會完成（這是一種合法的、cold-start 形狀的退化，而不是當機），但結果的 Evaluation.Calibrated 必須為 false——證明此 pipeline 誠實地退化，而不是當機，也不是悄悄回報虛假的信心。"
  - "完整的 EvaluateTurn -> Decide -> ConsumeAuthorization 流程測試（第 5 節），透過既有的 IssueAuthorization 橋接方法（predictor-09 自身在凍結的 app.EvaluationService 介面之外，已記錄的新增方法）核發一個 Authorization，綁定的是一對真實的 EvaluateTurn／Decide 呼叫所產出的「真實」TurnID／PromptHash，重新確認恰好一次／錯誤綁定／以時鐘為準的過期，在接上真實決策時依然成立——而不只是像 predictor-09／predictor-10 自身測試那樣，只針對合成的 authorization 資料列。這裡並未發現任何缺口：每一項不變條件，無論對照真實綁定還是合成綁定，皆一致成立。"
  - "全鏈路 benchmark（BenchmarkEvaluateTurn_FullPipeline、BenchmarkEvaluateTurnThenDecide_FullPipeline）使用的是一個熱（非 cold-start）的代表性 fixture（benchDataSource），而不是永遠處於 cold-start 的預設值，因為 ADD §29.11 的「warm evaluate」目標，才是穩態熱路徑 benchmark 正確的比較基準——一個永遠 cold-start 的 benchmark 走過的分支較少，會低估真實穩態下的成本。實測結果（非 -race，benchtime=1000x）：BenchmarkEvaluateTurn_FullPipeline 約 98 微秒／op（6.8 KB/op、136 allocs/op）；BenchmarkEvaluateTurnThenDecide_FullPipeline 約 189 微秒／op（12 KB/op、310 allocs/op）。在此節點自身要求的 -race 驗證指令下，兩者都因為 race-detector 的檢測開銷，大致放大了一個數量級（分別約 1.3ms 與 3.4ms/op）。與 Auspex_ADD.md §29.11 所述目標相比（warm evaluate P50 < 25 ms、P95 < 100 ms）：即使是經 -race 放大後的數值，相對於 P50 仍有約 20-70 倍餘裕，相對於 P95 仍有約 30-75 倍餘裕；非 -race 的數值則有約 130-250 倍餘裕。predictor-08 自身的 BenchmarkDecide（單獨的 Policy）依然遠低於另一個獨立的「policy < 1ms」子預算，未受影響（此次執行在 -race 下測得 127.5 ns/op，與第 6 波非 -race 的 52.83 ns/op 數字一致）。"
  - "這次全面檢查並未發現任何真實的跨階段 bug——每一個 DataSource 層級與 pipeline 階段層級的失敗模式，原本就已正確失敗（錯誤會傳播、不會有部分持久化；唯一合法的『退化但不報錯』情境，本來就已依設計產生 Calibrated=false）。這與 predictor-10 那一波（發現並修正了一個真實的 prompt-binding 繞過漏洞）是不同、但同樣合法的結果——依任務指示自身對這種結果的明確允許，這裡明確記錄下來，而不是為了有東西可以回報，就製造一個表面上的改動。"
blockers: []
```

## 第 9 波總結

本波唯一指派的節點（`predictor-11`）已 `completed`，具備可留存的產出物，驗證指令全數通過，位於 `vertical-slice/predictor` 分支。依指示先 merge 了 `origin/main`（乾淨的 fast-forward，`379b7cf` -> `36e7ffb`），並確認在撰寫任何新程式碼之前建置／測試皆乾淨通過。這是 predictor 角色最後一個指派的 DAG 節點：**`predictor-01` 至 `predictor-11` 每一個節點現皆已 `completed`**，本角色的完整範圍（`internal/features/**`、`internal/predictor/**`、`internal/policy/**`、`internal/evaluation/**`）就此結束，已無任何剩餘工作。

這份全面的全鏈路測試套件（`internal/evaluation/pipeline_e2e_test.go`，約 900 行，六個具名區塊，呼應 predictor-10 自身 authorization_test.go 的慣例），透過真正的 `evaluation.Service`（而非 pipeline 本身的 mock）證明了：

1. 全鏈路性質測試（quantile 單調性、unknown 傳播、無 NaN／Inf／除以零），透過 11 個手工挑選的 fixture 表，加上 200 筆隨機案例，貫穿整個 Scope->Token->Quota->Risk->Policy->Decide 鏈——零 panic、零 NaN／Inf 外洩、零無效的 `Confidence` 值。
2. 相同輸入在整個 pipeline 中的確定性輸出，針對同一份寬表格中的每一個 fixture 重新執行（而不只是 predictor-09 原本測試的單一 cold-start 案例）。
3. Reason-code golden 測試，確認最終的 `Evaluation.ReasonCodes` 構成一套條理一致的解釋（cold-start -> `PREDICTION_COLD_START`；quota 接近上限 -> `QUOTA_NEAR_LIMIT`；context 接近上限 -> `CONTEXT_NEAR_LIMIT`）。
4. 每一個交接處的對抗性 fail-open／fail-closed 測試：全部九個 `DataSource` 方法，以及全部四個 ADR-041 pipeline 階段介面（`ScopeEstimator`／`TokenForecaster`／`QuotaForecaster`／`RiskCombiner`），逐一被強制失敗或退化，確認 `EvaluateTurn` 在真正的上游錯誤下永遠 fail closed（回傳錯誤、不留下任何資料列），且唯一合法的「退化但不報錯」情境（全為 nil 的 `TokenForecast`）依然回報 `Calibrated=false`，絕不捏造出有信心的結果——另外還有一項專屬測試，確認即使其他所有 pipeline 輸入都是 cold-start／空值，一個未校準的 runway 緊急狀況仍會強制產生 `PolicyPause`。
5. 完整的 `EvaluateTurn -> Decide -> ConsumeAuthorization` 流程（恰好一次、錯誤 session／錯誤 prompt 的拒絕、以時鐘為準的過期），對照一個綁定「真實」`TurnID`／`PromptHash`（由一對真實的 `EvaluateTurn`／`Decide` 呼叫產出）的 `Authorization` 重新確認——而不只是 predictor-09／predictor-10 已測試過的合成綁定。
6. 全鏈路 benchmark：`BenchmarkEvaluateTurn_FullPipeline` 約 98us/op、`BenchmarkEvaluateTurnThenDecide_FullPipeline` 約 189us/op（非 -race、熱 fixture），皆比 `Auspex_ADD.md` §29.11 的 warm-evaluate P50<25ms／P95<100ms 目標低 130-250 倍；在此節點自身要求的 `-race` 驗證指令下約為 1.3ms／3.4ms（仍低於預算 20-75 倍）。

**本波並未發現任何真實的跨階段 bug**——每一個交接處原本就已正確地朝安全方向失敗。依任務指示自身對此結果的明確允許——一次乾淨的稽核也是一種合法的結果，不構成製造多餘工作的理由——這裡把它記錄為一個與 predictor-10 那一波（發現並修正了一個真實的 prompt-binding 繞過漏洞）合法、但形狀不同的結果。截至本波最終 commit 為止，`golangci-lint run ./...` 回報全 repo 0 個問題（初稿中的一項 `copyloopvar` 發現——一個沿用舊寫法、多餘的 Go-1.22+ 迴圈變數複製——已在最終 commit 前抓到並修正）。未觸碰任何其他角色的路徑；`internal/features/**` 已重新閱讀但未修改。

---

## 最終整合關卡（Final-integration-gate）修正：`SQLDataSource`（真正的 `DataSource` 實作）

**並非編號的 DAG 節點。** 這是最終整合關卡審查（`contract-integrator-final`）中由 lead 發現的問題，之所以轉交給本角色處理，是因為它恰好落在本角色的專屬路徑（`internal/evaluation/**`）之內，儘管 `predictor-01` 至 `predictor-11` 每一個 DAG 節點皆已 `completed`（見上方第 9 波總結）。發現的問題：`internal/evaluation.DataSource`——`evaluation.Service` 依賴此介面取得 `app.EvaluateTurnRequest` 本身未帶有的一切——**整個程式碼庫中沒有任何真正的正式實作**，透過 `grep -rn "var _ evaluation.DataSource\|var _ DataSource"` 確認，只找到僅限測試的 fake（`helpers_test.go` 中的 `fakeDataSource`，被本 package 之外的 `restart_test.go`、`restart_sameDB_test.go`、`e2e_highrisk_test.go`、`decision_realauth_test.go`，以及本 package 自身的 `pipeline_e2e_test.go`／`authorization_test.go` 使用）。每一個 pipeline 階段都是真實且經過深度測試的；但把來自實際系統的真實訊號餵給它們的那一層，卻從未被建構出來，這正是為什麼 `cmd/auspex/main.go` 至今仍無法在正式環境中接上一個真正的 `EvaluationService`。

依指示，開始前先執行了 `git fetch origin && git merge origin/main`——從 `2e32032` 到 `35bdd73` 的乾淨 fast-forward，帶入了 `docs/implementation/**` 全面的 `day1/` -> `vertical-slice/` 術語重新命名、checkpoint 新的 `internal/progress` 服務（`ProgressTreeService` adapter、`NodeStore`／`EdgeStore`／`ArtifactStore`），以及 runtime 新的 `internal/pause.Service`（`GracefulPauseService` adapter）——這兩者都是其他角色 Final-integration-gate 的修正性新增，證實了本任務的前提：這是一次專案全面性、由整合關卡驅動的清理作業，並非只針對本角色。merge 完成後、在撰寫任何新程式碼之前，`go build ./...` 與 `go test ./...` 皆乾淨通過。重新閱讀了 `CONSTITUTION.md`、`agents/predictor.md`、`internal/evaluation/datasource.go`（介面本身未修改——依任務明確指示，僅針對其實作），`internal/evaluation/doc.go`、本 package 自身的 migrations 0040-0044、claude-provider 的 `0010_events.sql`、checkpoint 的 `0020_progress_nodes.sql`／`0021_progress_edges.sql`／`0022_artifacts.sql`，以及 foundation 的 `0001_repositories.sql`／`0002_worktrees.sql`／`0003_provider_sessions.sql`／`0004_tasks.sql`。

```yaml
node: predictor-final-datasource (corrective addition, not a DAG node)
status: completed
artifacts:
  - internal/evaluation/datasource_sql.go
  - internal/evaluation/datasource_sql_test.go
validation:
  - "gofmt -l internal/evaluation  # clean"
  - "go build ./...  # ok, whole module"
  - "go vet ./internal/evaluation/...  # ok"
  - "go test ./internal/evaluation/... -race -v  # PASS, 28 new SQLDataSource tests + all pre-existing predictor-09/-10/-11 tests, zero regressions"
  - "go test ./...  -race  # PASS, whole module, zero regressions"
  - "golangci-lint run ./...  # 0 issues repository-wide"
commit: <see final report>
next_action: 無——一旦此變更 land，由 lead 處理最終的根層 wiring 整合（cmd/auspex/main.go、internal/app/wiring/**）；此修正明確不包含這部分
assumptions:
  - "型別名稱：evaluation.SQLDataSource，透過 NewSQLDataSource(db *sqlite.DB) *SQLDataSource 建構（若 db 為 nil 則 panic，對應本 package 自身為 Service 建立的 New 建構子慣例）。編譯期斷言 var _ DataSource = (*SQLDataSource)(nil) 同時存在於正式檔案與測試檔案中（後者是透過 _test package scope 匯出的 evaluation.SQLDataSource 型別）。"
  - "真正以儲存為後盾的方法（9 個中的 7 個）：Resolve（provider_sessions -> worktrees -> repositories，加上一項已記錄的 task 解析啟發式——優先選擇綁定於呼叫端自身 session、最近建立的 task，若無則退回 worktree 中任何地方最近建立的 task，若都不存在則為 nil）；Classification（從最近一筆 provider.turn.started 事件的 prompt_sha256／prompt_byte_length／prompt_approx_tokens payload 欄位，建構出「真正」、未經捏造的 features.PromptFeatures——依 Constitution §7 rule 2，這是 claude-provider 唯一會持久化的 prompt 衍生訊號——接著送入真正、未經修改的 features.ClassifyTask；由於原始文字從未可得，每一個動詞／領域指標布林值都維持 false，因此這在實務上大多數時候會合法地產生 TaskClassUnknown，這是 ClassifyTask 自身對訊號不足所做的正確回應，而不是這座橋接層的限制）；Progress（直接呼叫 checkpoint 真正、已匯出的 internal/progress.NodeStore.ListByTask／EdgeStore.ListByTask，唯讀方式，取得真正的 CompletedRatio／UnresolvedBlockers／目前節點訊號，並附有一項已記錄的『最近更新的非終端節點』作為目前節點的啟發式規則）；RecentSimilarTurnTokens（查詢 claude-provider 的 events 資料表中的 provider.usage.observed 資料列，擷取 total_tokens payload 欄位——目前真實的 normalizer payload 形狀尚未帶有該欄位，因此在實務上會誠實地產出空 slice，一旦未來某個 claude-provider 波次加入該欄位，此方法便會自動啟用，完全對應 predictor-05b 自身 >=8 樣本經驗分支的先例）；Quota（查詢 events，取得每個 limit_id 最新一筆 provider.quota.observed 資料列，解碼 used_percent／resets_at——真實、支援多視窗）；Context（查詢 events 取得最新一筆 provider.context.observed 資料列，解碼 used_tokens／window_tokens／used_percent——真實，若無事件則為零值／nil 指標，絕不捏造為零）；RunwayForecast（查詢本 package 自身的 runway_forecasts 資料表，取得每個 session 最新的一筆資料列——針對凍結的 schema 而言真實且正確，儘管截至此次修正為止，尚無任何正式程式碼路徑會寫入該資料表：internal/pause.Service.Observe 透過 runway.Scorer.Score 計算出 domain.RunwayForecast 並回傳給呼叫端，但本身並不會持久化任何資料列——已透過 grep -rn \"runway_forecasts\" internal/ | grep -v _test.go 確認任何地方都沒有 INSERT；一旦未來某個波次接上該持久化——這不屬於本角色專屬路徑可新增的範圍——此方法便會自動啟用，不需要再對此檔案做任何進一步變更）；PriorRunwayHitConfirmed（查詢本 package 自身的 policy_decisions 資料表，並透過 predictions／events join 以還原 session 範圍，因為這兩張表是以 turn 為範圍、而非以 session 為範圍，檢查最近一筆決策的 reason_codes_json，尋找 internal/policy/decide.go 的 runwayPauseDecision 唯一產生的兩個確切字面標記字串：runway_hit_probability_armed_pending_confirmation 與 runway_hit_probability_confirmed_twice）。"
  - "誠實的 cold-start 方法（9 個中的 2 個），依設計恆為 ok=false，而非投入不足所致：Repository 與 Session。features.RepositoryFeatures 的欄位（TrackedFileCount、LanguageCount、GoModuleCount、DirtyFileCount、IsMonorepo 等）描述的是 repository 內容／working tree 狀態的訊號，本 package 專屬路徑可觸及的任何資料表都沒有持久化這些訊號——repositories／worktrees 只帶有身分／路徑欄位，repository_checkpoints（checkpoint 的範圍）只帶有 diff hash 與位元組總數，並沒有檔案／語言普查——要填入這些欄位，需要新增一個 Git 掃描能力（本角色的邊界明確排除：『No ... Git commands』），或是一張新的跨角色 telemetry 資料表（此次修正的 schema 是凍結的）。features.SessionFeatures 的欄位（RecentTurnUsageP50/80/90、ChangedFilesRecentP50/90、RetryRate、TestFailureRate 等）是在一個 events schema 無法誠實重建的視窗上取得的經驗分位數／速率：沒有任何 EventType 帶有逐 turn 檔案／行數變更的 payload 欄位，也沒有任何事件帶有『這個 turn 是重試』的旗標——僅憑 provider.turn.failed／provider.turn.started 的次數就發明一個『重試』的代理定義，會是一項真實、前所未有的建模決策（什麼算重試？在什麼視窗內？），而本程式碼庫中沒有任何依據可以錨定它，不同於 RecentSimilarTurnTokens，後者有 internal/predictor/token 自身的 cold-start／經驗分位數機制，已經精確定義了它預期的輸入形狀。依本 package 自身已建立的紀律（predictor-05 至 predictor-11：『不捏造，cold-start 是合法的答案』）以及該介面自身的文件註解（『zero-value／ok=false 的回傳……代表尚不可得，而不是錯誤』），這兩個方法誠實的答案就是 ok=false，而不是發明出來的數值。"
  - "對其他角色真實已匯出型別／資料表所採取的唯讀跨 package／跨資料表依賴（供 lead 獨立驗證皆未遭修改）：(1) internal/progress.NodeStore／EdgeStore（checkpoint 已匯出的 Go store，透過 progress.NewNodeStore(db, nil)／progress.NewEdgeStore(db) 建構——只呼叫它們的唯讀方法 Get／ListByTask；傳入 nil 的 domain.Clock，因為這些唯讀方法從不使用它，避免讓本檔案背負一個原本不需要的 Clock 依賴）；(2) 針對 foundation 的 provider_sessions／worktrees／repositories／tasks 資料表、claude-provider 的 events 資料表，以及本 package 自身的 runway_forecasts／predictions／policy_decisions 資料表所發出的原始唯讀 SQL（只有 SELECT，透過 grep -n \"INSERT\\|UPDATE\\|DELETE\\|ExecContext\" internal/evaluation/datasource_sql.go 確認零匹配）（後三張表在 store.go 中已有 Go 資料列型別，但 RunwayForecast／PriorRunwayHitConfirmed 需要的查詢形狀——依 session 取最近一筆、以及一個 session 範圍 join——與 store.go 既有以 turn／prediction ID 為鍵的輔助函式不同，因此本檔案發出自己的 SELECT，而不是重複／擴充那些函式）。未新增任何 migration；internal/evaluation/** 之外沒有任何檔案被修改（此次修正後 git status --porcelain 顯示恰好兩個新檔案，皆位於 internal/evaluation/ 之下）。"
  - "internal/policy 並未為它兩個 runway-hit-probability 標記用的 reason-code 字串，匯出任何可供匯入的常數（它們是 decide.go 的 runwayPauseDecision 中的內嵌字面值）——與其在此次修正範圍之外，為一個不打算擴充的路徑新增匯出項，PriorRunwayHitConfirmed 在 datasource_sql.go 中把這兩個字面字串複製為未匯出的常數，並附上文件註解交叉參照 decide.go 作為其意義的權威來源，這樣一來，未來若 internal/policy 的字面值有所變更卻未在此同步反映，至少可以透過 grep 發現，而不會悄悄產生分歧。"
  - "在此次修正自身的驗證過程中，抓到並修正了一個真實、屬於既有模式的 bug，並非由審查者發現：RunwayForecast 的初稿宣告並檢查了一個對應 domain.RunwayForecast.SampleCount 的 sampleCount 形狀掃描變數，但 runway_forecasts（migration 0042）並沒有 sample_count 欄位——該變數永遠是 !Valid（死程式碼，SampleCount 悄悄地永遠為零，就技術上而言這仍算誠實，因為那是該型別的零值，但卻誤導性地暗示存在一個實際上並不存在的支援欄位）。已完全移除該死程式碼變數及其防護，改以一則明確指出此缺口的文件註解取代，而不是留下看起來具有承載作用、但實則無用的程式碼。"
  - "所需的測試：一個 var _ DataSource = (*SQLDataSource)(nil) 編譯期斷言（兩個檔案中皆有），加上 28 個整合風格的測試（datasource_sql_test.go），對照一個真實、已套用 migration 的 SQLite DB（openMigratedDB，本 package 自身既有、未經改動的測試輔助函式），並在 repositories／worktrees／provider_sessions／tasks／events／progress_nodes／progress_edges／runway_forecasts／predictions／policy_decisions 中植入貼近真實的種子資料——涵蓋每一個方法的真實資料路徑「以及」其 cold-start／空值路徑，另外還有兩個端對端測試（TestSQLDataSource_WiredIntoRealService_*），把 SQLDataSource 接進一個真正的 evaluation.Service，並搭配正式 wiring 會使用的「同一套」真實 scope／token／quota／risk／policy pipeline 階段 package（而非以 fakeDataSource 為基礎的輔助函式），確認 SQLDataSource 確實滿足 EvaluateTurn／Decide 對 DataSource 所做的每一項交接，無論是 cold-start 還是有真實種子資料的情況。"
blockers: []
```

### 總結

`evaluation.SQLDataSource` 是本 package 自身 `DataSource` 介面的一個真實、具體、以 SQLite 為後盾的實作，建構過程中未修改 `datasource.go` 的介面宣告，未觸碰 `internal/evaluation/**` 之外的任何檔案，也未新增任何 migration（此次修正的 schema 是凍結的——每一項查詢都針對既已存在的資料表運作）。九個方法中有七個以 foundation、claude-provider、checkpoint 以及本 package 自身 migration 範圍所擁有的資料表／store 為後盾，具備真實資料；另外兩個（`Repository`、`Session`）誠實地回傳 cold-start（`ok=false`），因為此次修正範圍所限定要處理的凍結 schema，對這兩個 DTO 所承諾的特定欄位並沒有真正的支援訊號，而捏造一個出來，對本角色自身波次以來已建立的「不捏造，cold-start 是合法答案」紀律而言，弊大於利。這呼應了本角色自 predictor-05（FeatureSource 缺口）、predictor-07（`ScopeEstimate` 公式項目欄位缺失），以及 predictor-09（`ConsumeAuthorization` 範圍衝突）以來，一貫展現的誠實橋接範圍的判斷——每一個發現的缺口，都附上為何它是缺口的具體理由並加以記錄，而不是悄悄繞過，也不是悄悄不提。所有必要的驗證指令皆通過，全 repo 零問題、零迴歸。lead 自身的根層 wiring 步驟（`cmd/auspex/main.go`、`internal/app/wiring/**`）明確不在範圍內，未被觸碰。
