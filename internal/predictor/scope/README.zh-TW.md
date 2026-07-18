# internal/predictor/scope/ — 第 1 階段：針對下一個 turn 的規則式範疇估算

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

`RuleScopeEstimator`（`estimator.go`）實作了凍結的 `app.ScopeEstimator` port（ADR-041、
predictor-05）：它會根據 prompt、repository、session 與 Progress-Tree 的特徵，預測某個
turn 預期需要多少工作量——以 P50/P80/P90 三元組表示的讀取／變更檔案數與變更行數，再加上
布林值需求旗標（單元測試／整合測試、跨專案、可能涉及 migration、安全敏感）。

輸入透過 `FeatureSource` 取得，這是凍結的 `app.FeatureDataSource` port（ADR-044）在
consumer 端的一個窄化視角，在正式環境中由 [`internal/evaluation`](../../evaluation/README.md)
的 `SQLDataSource` 滿足。

估算值如何產生：

- Cold-start 基準值：ADD §14.6 的 bootstrap 表（`coldstart.go`），對 ADD 指名的 8 個 class
  逐字採用，另外 8 個 §14.3 class 則有明確記載的最近鄰（nearest-neighbor）備援。ADD 明確
  指出這些是 bootstrap 值，並非放諸四海皆準的 benchmark。
- 經驗混合：一旦某個 session 提供了近期 turn 的 quantile（沿用 ADD §15.2「>= 8 筆樣本」的
  門檻，此處以 `MinSessionSamples` 表示），便會與基準值取平均——是混合，而非取代。這會把
  Confidence 提升到 medium，但絕不會把 Calibrated 設為 true。
- 僅放寬（widening-only）的調整：repository 的 fan-out 與較長的剩餘關鍵路徑會拉寬 P90
  尾端；prompt 中明確點名的路徑則會為讀取檔案數估算設下下限（floor）。
- `sortTriple` 無條件強制 P50 <= P80 <= P90，這與
  [`internal/predictor.Quantiles`](../README.md) 自身的保證一致。

`ToolCallsP50/P90`、`VerificationP50/P90`、`RetryLoopsP50/P90` 與 `DurationP50/P90` 在本波
（phase）維持 nil——目前尚未接上 tool-call 或 verification 的 telemetry，而 nil 代表未知，
絕不是零。

輸出的 `domain.ScopeEstimate` 會提供給 [`token/`](../token/README.md)（作為倍率）與
[`risk/`](../risk/README.md)（completion/blast-radius 項）。上述引用的 ADD 章節見
[Auspex_ADD.md](../../../docs/design/Auspex_ADD.md)。套件合約詳見 `doc.go`。
