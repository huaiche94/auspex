# ADR 建議

> 🌐 [English](ADR_Recommendations.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| 欄位 | 值 |
|---|---|
| 階段 | 3.9 — Phase 2 後分析 |
| 狀態 | **僅為提案。這些提案均未經核准，均未實作。本文件未曾異動任何列為「受影響」的檔案。** |
| 來源 | 來自 `Feature_Registry.md`、`Feature_Gap_Report.md`、`Wave2_Lessons.md` 的發現，以及本次對話自身的獨立驗證工作 |

以下每項建議都遵循相同的結構：問題、證據、受影響套件、相容性影響、建議。核准與實作是另外的未來步驟，本文件明確未執行這些步驟。

---

## ADR-REC-01：將儲存庫／工作階段特徵查詢正式化為凍結的 `app` port

**問題：** `predictor-05` 的 `RuleScopeEstimator` 需要儲存庫與工作階段衍生的特徵才能發揮作用，但 `app.EstimateScopeRequest`（凍結的合約）只攜帶 ID。該角色依指示以套件內部（package-local）的 `FeatureSource` 介面（`internal/predictor/scope/estimator.go`）繞過此問題，而非修改 `internal/app/ports.go`。依 Phase 2 的規則，這是正確的做法，但這代表特徵查詢能力現在完全存在於 `predictor` 自己的套件內，對 `internal/app` 的跨元件合約不可見——任何未來的 predictor 層級（Statistical、ML）或任何其他需要相同儲存庫／工作階段資料的角色，都得重新發明相同介面，或直接匯入 `predictor` 的內部套件，而這正是窄 port 原則（narrow-ports discipline）存在的目的所要防止的那種意外耦合。

**證據：** `Feature_Gap_Report.md` §1.1（排名第 1，「現在即可補上、改善幅度高」）記載 `internal/gitx` 已產生底層資料（dirty 檔案數量、worktree 結構），完全不需要新的資料蒐集工作——僅剩下一個連接（wiring）缺口尚待補上，而該連接目前沒有一個規範的（canonical）歸屬位置。

**受影響套件：** `internal/app/ports.go`（將新增 port／DTO）、`internal/predictor/scope/estimator.go`（將把自己的本地 `FeatureSource` 遷移為符合新的凍結形狀，而非目前這種臨時（ad hoc）版本）、`internal/gitx`（將新增一個使用端，預期 `gitx` 本身不會有變動）。

**相容性影響：** 若謹慎處理，僅屬新增性質——在 `ports.go` 中新增介面不會破壞 `EvaluationService`、`ProgressTreeService`，或任何既有的凍結型別。風險範圍狹窄：現在選定的任何形狀，都會依 Constitution §3 的「公開 CLI／API／協定相容性」ADR 觸發條件成為一項相容性承諾，因此應該一次就審慎設計好，而不是等其他角色開始依賴之後才就地反覆調整。

**建議：** 在 Phase 3 指派任何會消費此資料的節點（例如未來的 `predictor-05` 後續工作，或 `predictor-05b`／`-05c`）之前，值得撰寫一份正式的 ADR。實作成本低（依 `Feature_Gap_Report.md` 為 S），槓桿效益顯著（可補上該報告中排名最高的單一缺口）。

---

## ADR-REC-02：為 DAG 任務表結構新增耗時與 token 成本欄位——或明確聲明這些欄位永久排除在範圍之外

**問題：** `docs/implementation/vertical-slice/EXECUTION_DAG.md` 在其 84 個以上的節點中，橫跨兩個完整的 phase，從未有過耗時或 token 欄位。這個缺口在 5 份經驗教訓（lessons-learned）檔案中，有 4 份各自獨立提出（`Wave2_Lessons.md` §1，issue #2），而這些檔案彼此並不知道對方的存在。`Prediction_Error_Report.md` 對目前已執行的 19 個節點，沒有一個能夠計算出耗時或 token 誤差，原因是缺乏可供比較的估計值——而不是缺乏實際資料（依該報告所述，實際資料以部分細緻度存在）。

**證據：** `Prediction_Error_Report.md` §2（「存在 `estimated_duration` 的節點數：19 個中的 0 個」），`Calibration_Report.md` §8（將此列為改善未來 phase 信心度的第 3 優先事項）。

**受影響套件：** 無（這是文件／流程產物 `docs/implementation/vertical-slice/EXECUTION_DAG.md`，不是 Go 程式碼）——之所以列在此處，是因為儲存庫擁有者在 Phase 2 的指示中，已明確將 DAG 檔案本身凍結，變更需經 ADR 核准，因此即使只是對其結構做純文件性質的變更，也適用此流程。

**相容性影響：** 對執行中的程式碼沒有影響。對未來維護 DAG 的人會有一些成本（每個節點要多填兩個欄位）。

**建議：** 有兩條站得住腳的路徑，而非單一明顯答案：(a) 新增這些欄位，並要求未來的 DAG 撰寫者填入，即使只是概略估計，讓估計器至少有*某個*可供比對的基準；或 (b) 在 DAG 自身的標頭中明確記載，耗時／token 依設計就不在此產物的範圍內（例如因為它們依賴 provider 與模型的程度，是 LOC／檔案數／複雜度所沒有的），這至少能避免同一個缺口在每個 phase 都被各自重新發現一次。建議採用 (a)——依本專案自身「『未知』是有效的答案，但缺口不能不予調查」的精神，一個結果證明錯誤的粗略估計，也比永遠空白的欄位更有用。

---

## ADR-REC-03：解決 CLI hook 子指令大小寫命名不一致的問題

**問題：** `Auspex_ADD.md` 附錄 E.3 將 Claude Code 的 hook 子指令規定為 PascalCase（`auspex hook claude UserPromptSubmit`，與 Claude 自身的 hook 事件名稱大小寫一致）。而 `agents/runtime.md` 的 P0 指令清單、`docs/implementation/vertical-slice/EXECUTION_DAG.md` 中 `claude-provider-06` 的驗證指令，以及 `Auspex_Parallel_Execution_Plan.md` 的示範腳本，都各自獨立使用 kebab-case（`auspex hook claude user-prompt-submit`）。這是由 `claude-provider-06` 發現，並在 Phase 2 審查期間由負責人直接閱讀 ADD 文字後獨立確認——這是真實存在的問題，不是誤讀。

**證據：** `Wave2_Lessons.md` §1，issue #4；相關文字存在於 `Auspex_ADD.md` 第 ~6152-6157 行（PascalCase），與另外三份文件（kebab-case）相對照，目前全部都已凍結。

**受影響套件：** `integrations/claude/hooks.json`（已建置完成，依循 kebab-case，屬於一項有記載的判斷取捨），以及——尚未建置的——`runtime-b01` 真正的 CLI 指令樹（`internal/cli`），該指令樹在實際將 `auspex hook claude ...` 實作為 Cobra 指令時，將需要選定一種慣例。

**相容性影響：** 若現在解決，影響低（目前沒有任何外部項目依賴任一種大小寫慣例）；若在 `runtime-b01` 以某一種大小寫方式上線、且真實使用者／腳本開始依賴它*之後*才解決，就會變成一次破壞性的 CLI 變更。

**建議：** 在 `runtime-b01` 被指派之前解決，而非之後。現在修正成本小又便宜，之後修正則會破壞相容性——這正是值得趁還「免費」時，一次就審慎決定好的那種決策。

---

## ADR-REC-04：在 SQLite 結構中新增明確的 `events` 持久化資料表，或記載原始事件不會被持久保存

**問題：** `pkg/protocol/v1.Event`（凍結的正規化事件封包，ADD §11）在 ADD §12.2 明確列出的 SQLite 結構清單中，並沒有對應的資料表。其他每一個主要的凍結型別（`turns`、`progress_nodes`、`state_checkpoints`、`pause_records` 等）都有具名的資料表；`Event` 沒有。

**證據：** `Feature_Registry.md` §8b 中直接指出：「ADD §11 隱含需要 `events` 資料表，但在 §12.2 的明確資料表清單中並未命名。」

**受影響套件：** `Auspex_ADD.md` §12.2（結構定義，由 contract-integrator 負責），最終則是 `foundation-06` 的 migration 範圍（0000-0009），或某個負責該功能角色的 migration 範圍，實際取決於最後由哪個角色負責事件儲存。

**相容性影響：** 目前尚無影響（尚無任何 migration 存在）。一旦 `foundation-06` 在沒有 events 資料表的情況下推出 migration，而之後的 phase 想要回頭補上一個，就會變成結構版本（schema-versioning）的問題。

**建議：** 基於與 ADR-REC-03 相同的「現在便宜、之後昂貴」理由，應在 `foundation-06` 被指派之前解決。有兩個誠實的選項：現在就新增資料表，或明確決定（並在 ADD 中記載）原始事件是刻意不做持久保存的——只有其衍生結果（turn 記錄、usage observation 等）才會保存——這本身可以是一項合理、注重隱私的設計選擇，但必須是一項明確聲明過的決策，而不是一個悄悄存在的缺口。

---

## ADR-REC-05（未決問題，非確定性建議）：`RunwayForecast` 是否應該像 `QuotaForecast` 當初設計的那樣，支援多個並行的 window？

**問題：** ADD §15.5 明確討論了多個 quota window（「多 windows 取 `P_any = 1 - Π(1 - P_i)`... v1 預設取 max」），而 `predictor-06` 的 `CombineWindows` 函式（已於 Phase 2 審查期間驗證）已經實作了跨 window 的 `max()` 組合方式。但 `domain.RunwayForecast` 本身（ADR-041，已凍結）是單一 window 的 struct——`CombineWindows` 操作的是呼叫端提供的 slice，而不是一個凍結的多 window 型別。與此同時，`QuotaForecast`（同樣是 ADR-041）依明確指示，刻意只保留兩個純量欄位（`ProjectedQuotaUsedP90`、`ProjectedContextUsedP90`），而非陣列，以避免本 phase 出現不必要的多 window 推測性複雜度。

**證據：** Phase 2 驗證期間直接閱讀程式碼所得（`internal/predictor/runway/runway.go` 的 `CombineWindows`、`TestCombineWindowsTakesMax`）。

**受影響套件：** `internal/domain/usage.go`（`RunwayForecast`）、`internal/domain/forecast.go`（`QuotaForecast`）——若任一形狀有所變更的話。

**相容性影響：** 會對兩個已凍結的型別造成破壞性變更。

**建議：** 並非建議變更——而是標記為一個值得審慎決定的設計問題（是，維持單一 window 加上呼叫端組合；或否，將多 window 正式化為一等公民型別），留待下次因其他不相關原因重新檢視任一型別形狀時再做決定，而不是在沒有人刻意做出選擇的情況下，讓慣例持續分歧下去。

---

## 摘要

| # | 建議 | 急迫程度 | 若核准的實作成本 |
|---|---|---|---|
| REC-01 | 凍結的特徵查詢 port | 下一個 predictor 節點之前 | S |
| REC-02 | DAG 耗時／token 欄位 | Phase 3 規劃使用估計值之前 | XS（結構）／持續（填寫成本） |
| REC-03 | CLI 大小寫命名解決 | `runtime-b01` 之前 | XS |
| REC-04 | `events` 資料表決策 | `foundation-06` 之前 | XS（決策）／S（若新增資料表） |
| REC-05 | 多 window Runway（未決問題） | 不急迫——伺機重新檢視 | 未知，尚未界定範圍 |

這些提案均未經核准，均未實作。本文件唯一的目的，是在每項決策被遺忘或變得昂貴難改之前，讓它們變得可見、並有證據支持。
