# docs/methodology/ — 蒸餾後可重複使用的開發流程

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

本目錄將建構 Auspex 所採用的多代理人（multi-agent）、以證據為本的開發
流程，萃取為一份具版本編號、可供引用的文件，讓未來的專案可以直接引用
「遵循 Auspex Development Methodology v1.0」，而不必重新以數百行的提示
詞複述整個流程。

| 檔案 | 涵蓋內容 |
|---|---|
| [`Auspex_Development_Methodology.md`](Auspex_Development_Methodology.md) | PDM v1.0.0 —— 各階段順序（Phase 0 Repository Discovery 到 Phase 7 Architecture Amendment）、跨階段不變量（invariants）、版本編號／引用規則，以及明確聲明「不涵蓋」的事項。每一條規則至少都曾在 Auspex 本身上實際執行過一次才寫下；失敗案例是以證據形式引用，而非抽象斷言。其自身的狀態欄位註明尚未套用於第二個專案（§9／§12）。 |

## 適用範圍

這是一份**流程**方法論 —— 說明工作如何從構想，經由多個 AI 代理人與多
次工作階段，推進到整合且經過驗證的程式碼。它明確**不是**架構範本
（[ADD](../design/Auspex_ADD.md) 只是 Phase 1 產出物的一個*範例*，並非
方法論的一部分），也不能取代其中定義的人工核准關卡。

## 相關文件

- 本儲存庫**具約束力、不可協商**的規則，記載於
  [`../../CONSTITUTION.md`](../../CONSTITUTION.md)；此方法論則是可重複
  使用的通用化版本。
- 本方法論所蒸餾自的實際執行紀錄，記錄於
  [`../implementation/vertical-slice/`](../implementation/vertical-slice/README.md)；
  其中 Phase 6（「Post-Phase Analysis」）對應到
  [`wave2-analysis/`](../implementation/vertical-slice/wave2-analysis/README.md)。
