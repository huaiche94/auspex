# docs/implementation/vertical-slice/ — 第一個垂直切片的建置紀錄

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

Auspex 第一次建置的完整執行紀錄：橫跨七個角色、共 85 個 DAG 節點，由平行
代理人（agents）在 13 輪整合（稱為「phase」）中執行完成，從 Bootstrap 一
路到 Stage-5 最終關卡。這裡的一切都是**歷史紀錄**——閱讀它是為了追溯某項
東西「如何」以及「為何」被建造出來；若要了解產品*現在是什麼*，請閱讀
[`../../design/Auspex_ADD.md`](../../design/Auspex_ADD.md)。

## 這裡有什麼

| 檔案／資料夾 | 內容 |
|---|---|
| [`EXECUTION_DAG.md`](EXECUTION_DAG.md) | 本次建置實際執行的任務層級相依 DAG（依 ADR-041 修訂）：階段、各角色的任務 ID、相依邊。 |
| [`CONTRACT_FREEZE.md`](CONTRACT_FREEZE.md) | 所有角色據以建置的凍結跨角色契約（domain 型別、ports、events、schema 版本字串、migration 範圍），附修訂紀錄。 |
| [`contract-integrator.md`](contract-integrator.md)、[`foundation.md`](foundation.md)、[`claude-provider.md`](claude-provider.md)、[`checkpoint.md`](checkpoint.md)、[`predictor.md`](predictor.md)、[`runtime.md`](runtime.md)、[`qa.md`](qa.md) | 各角色的進度成品：逐節點的狀態、產出物（artifacts）、驗證紀錄。這些是 Constitution §6.7 要求的持久性證據軌跡。 |
| [`lessons_learned/`](lessons_learned/README.md) | 各角色於完成時撰寫的回顧報告。 |
| [`wave2-analysis/`](wave2-analysis/README.md) | 建置期間的分析輪次，用以重新規劃 Phase 3 以後：校準（calibration）、功能落差（feature-gap）、重播（replay）與信心度報告。 |

## 各 Phase 整合歷史

（於根目錄 `README.md` 為初次瀏覽者改寫時，從那裡搬遷至此 — ADR-049。）


此垂直切片橫跨 7 個角色，共 84 項任務 + 1 項最終整合（見
`EXECUTION_DAG.md`，依 ADR-041 修訂）。階段與任務相依關係以該 DAG 為準；
**phase（波）**則是實際出貨工作的整合輪次。以下 Phase 1–2 為實際執行結果。
Phase 3 之後則是暫定、依相依關係推導出的分組——每個 phase 開始前都由主導者
重新規劃（Phase 3 規劃的輸入見 `wave2-analysis/`），且必須遵守該 DAG 的階
段與相依順序。

下表中每組任務 ID 都連結到其負責角色的進度成品（逐節點的狀態／產出物／
驗證紀錄）；每個 commit hash 都連結到 GitHub 上對應的整合提交（commit）。

| Phase | 範圍（任務 ID） | 狀態 |
|---|---|---|
| Bootstrap | [contract-integrator-01…07](contract-integrator.md) — 契約凍結（Stage 0） | ✅ 已整合（[`940c5cb`](https://github.com/huaiche94/auspex/commit/940c5cb)） |
| Phase 1 | [foundation-01](foundation.md) · [claude-provider-01/02/03](claude-provider.md) · [checkpoint-b02](checkpoint.md) · [predictor-02/03/04](predictor.md) | ✅ 已整合（[`3fb37ce`](https://github.com/huaiche94/auspex/commit/3fb37ce)） |
| Phase 2 | [foundation-02/03/04(reduced)/05/09](foundation.md) · [claude-provider-04/06](claude-provider.md) · [checkpoint-b03](checkpoint.md) · [predictor-05/06](predictor.md) | ✅ 已整合（[`528b6ad`](https://github.com/huaiche94/auspex/commit/528b6ad)） |
| Phase 3 | [foundation-06/08](foundation.md) · [predictor-05b](predictor.md) · [runtime-b01](runtime.md) · [qa-01/08](qa.md)（ADR-041 Token Forecaster；**runtime** 與 **qa** 首次出現的節點，分別自 Phase 1／Bootstrap 起便一直未被指派） | ✅ 已整合（[`ca7062f`](https://github.com/huaiche94/auspex/commit/ca7062f)） |
| Phase 4 | [foundation-07](foundation.md) · [claude-provider-05](claude-provider.md) · [checkpoint-a01/b01](checkpoint.md) · [predictor-01/05c](predictor.md) · [runtime-a01/b02](runtime.md) | ✅ 已整合（[`a0b10f2`](https://github.com/huaiche94/auspex/commit/a0b10f2)）— 包含對 `migrate_test.go` 中寫死（hardcoded）的 migration 數量斷言的修正，此修正經 5 份獨立的跨角色報告確認為必要，才能讓任何手足角色的 migration 與 foundation 的 migration 在同一棵樹中共存 |
| Phase 5 | [claude-provider-07](claude-provider.md) · [checkpoint-a02/a03/b04](checkpoint.md) · [predictor-07](predictor.md) · [runtime-a02/a06/b03/b04/b05/b08](runtime.md) | ✅ 已整合（[`dabaa9f`](https://github.com/huaiche94/auspex/commit/dabaa9f)）— Phase 4 之後 DAG 實際解鎖的前緣範圍比原先猜測的更大（一次解鎖了六個 runtime 節點，且從未存在過 `predictor-05d`）；`b03`／`b04`／`b05` 當時仍針對 `predictor-08`／`predictor-09`／`checkpoint-a04` 使用替身（fakes）執行，稍後的整合中才換成真實實作 |
| Phase 6 | [checkpoint-a04/b05/b06](checkpoint.md) · [predictor-08](predictor.md) · [runtime-a03/a04/a07](runtime.md) | ✅ 已整合（[`f5f0f28`](https://github.com/huaiche94/auspex/commit/f5f0f28)）— checkpoint-a04（CompleteNode 原子協定）現已為真實實作，當機注入（crash-injection）與並行完成競爭（concurrent-completion-race）的證明均經獨立複驗；predictor-08 冷啟動時「probability: null」的不變量經獨立追蹤，確認恰好只存在於兩個受閘控的呼叫點 |
| Phase 7 | [checkpoint-a05/a07/b07](checkpoint.md) · [predictor-09](predictor.md) · [runtime-a05/b07](runtime.md) · [qa-05](qa.md) | ✅ 已整合（[`25e3d40`](https://github.com/huaiche94/auspex/commit/25e3d40)）— qa 自 Phase 3 以來的第一個 Stage-4 節點；發現一個真實的 P1（機密過濾未涵蓋已追蹤檔案的 diff，只涵蓋未追蹤檔案的封存），依 qa「只回報不修正」的界線，此處不修正，轉交 checkpoint 處理 |
| Phase 8 | [checkpoint-a06/a08/b08](checkpoint.md) · [predictor-10](predictor.md) · [runtime-a08](runtime.md) · [qa-04](qa.md) | ✅ 已整合（[`b5a1937`](https://github.com/huaiche94/auspex/commit/b5a1937)）— 包含將機密遮蔽（redaction）擴大涵蓋已追蹤檔案 diff 的修正（結掉 Phase 7 的那個 P1），且 predictor-10 的對抗式稽核發現並修正了一個真實的授權提示詞綁定繞過漏洞 |
| Phase 9 | [checkpoint-a09/b09](checkpoint.md) · [predictor-11](predictor.md) · [runtime-a09/a10/b06](runtime.md) | ✅ 已整合（[`192e4b9`](https://github.com/huaiche94/auspex/commit/192e4b9)）— 完整完成 **checkpoint**（a01-a09/b01-b09）與 **predictor**（01-11）；發現並修正了一個真實的路徑穿越漏洞（checkpoint）與一個真實的 TOCTOU 競爭（runtime） |
| Phase 10 | [runtime-a11 · runtime-b09](runtime.md) | ✅ 已整合（[`a249ca2`](https://github.com/huaiche94/auspex/commit/a249ca2)）— 結掉兩個真實落差：缺少 TurnInterrupter 到 PauseRecord 的接線路徑，以及沒有任何 CLI 指令曾將其型別化錯誤序列化為 JSON（Cobra 的預設印表器會將其攤平成純文字） |
| Phase 11 | [runtime-b10](runtime.md) | ✅ 已整合（[`2fbc0c8`](https://github.com/huaiche94/auspex/commit/2fbc0c8)）— 完整完成 **runtime**（a01-a11/b01-b10，橫跨 9 個 phase 共 21 個節點）；證明了在同一個 SQLite 檔案上的行程內（in-process）重啟，包含一次真實的作業系統行程 SIGKILL 當機測試 |
| Phase 12 | [qa-02/03/06/07/09](qa.md) | ✅ 已整合（[`a91c239`](https://github.com/huaiche94/auspex/commit/a91c239)）— 完整完成 **qa**；字面意義上的垂直切片 E2E 展示，端到端執行真實程式碼。最終報告：無 P0，一項未結的 P1（provider-event-to-node-completion 接線），完整記載 |
| Final | [contract-integrator-final](contract-integrator.md)（Stage 5） | ✅ 已整合（[`3b6cfcb`](https://github.com/huaiche94/auspex/commit/3b6cfcb) + [`faca171`](https://github.com/huaiche94/auspex/commit/faca171)）— 發現並結掉了這道關卡本該攔截的組裝落差：`cmd/auspex/main.go` 從未接上真實服務。詳見 [`contract-integrator.md`](contract-integrator.md) 的 Stage 5 一節 |

Phase 5 以後刻意不預先訂出細節——每個 phase 都是在前一個 phase 整合完成後，
依 DAG 實際的相依邊重新推導出來的（方法請見
`wave2-analysis/Wave3_Recommendation.md`），而不是針對一個隨著工作落地
不斷改變形狀的 DAG 提前做出詳細規劃。

`→` 標示同一角色分支內、phase 內的先後順序；`·` 則用來分隔同一個 phase 中
平行的不同角色分支。

