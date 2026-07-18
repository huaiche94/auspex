# docs/implementation/vertical-slice/lessons_learned/ — 各角色的回顧報告

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

每個 vertical-slice 角色各有一份回顧檔案。每份都是一張表格，每列對應
一個已執行的節點，比較預估與實際結果 —— 複雜度、變更檔案數、耗時 ——
並附上未預期的相依性／檔案、阻塞因素、token 浪費觀察，以及對 Auspex
本身的建議（畢竟這個產品正是要預測這一類工作，因此這些紀錄同時也是它
最早的、具訓練資料形態的資料）。檔案是隨著各波次完成而陸續附加的，因
此各角色的涵蓋程度不一。

| 檔案 | 涵蓋內容 |
|---|---|
| [`contract-integrator.md`](contract-integrator.md) | Bootstrap 階段（一列 `bootstrap-01`，對應契約凍結）。 |
| [`foundation.md`](foundation.md) | foundation-01 到 -09。 |
| [`claude-provider.md`](claude-provider.md) | claude-provider-01 到 -07。 |
| [`checkpoint.md`](checkpoint.md) | checkpoint-a01–a09 與 b01–b09，外加 `corrective-qa05` 修正（已追蹤檔案差異的遮蔽處理）。 |
| [`predictor.md`](predictor.md) | predictor-01 到 -11（含 -05c 與最終的 DataSource 工作；沒有 -05b 這一列）。 |
| [`runtime.md`](runtime.md) | 全部 21 個 runtime 節點（a01–a11、b01–b10），外加最後的 Graceful-Pause 服務一列。 |
| [`qa.md`](qa.md) | 僅有 qa-01 與 qa-08（Phase 3）；qa 較後面的節點（qa-02–07、-09，Phase 7–12）在此並無對應紀錄。 |

## 相關文件

- 逐節點的狀態與驗證證據（著重「發生了什麼」而非「與預估的比較」），
  位於上一層的各角色進度產出物：[`../README.md`](../README.md)。
- 這些檔案中最早的五份（在 `qa.md`／`runtime.md` 出現之前）被彙整進
  [`../wave2-analysis/Wave2_Lessons.md`](../wave2-analysis/Wave2_Lessons.md)，
  該文件依各角色間反覆出現的問題進行排序。
