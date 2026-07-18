# docs/ — 專案文件

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

除了程式碼本身之外，記錄 Auspex 的所有文件都存放於此。如果
你是新加入的，請先從根目錄的 [`README.md`](../README.md) 開始閱讀，
再回來看這份索引。

## 權威層級（Authority）

有兩份文件的位階高於其他所有文件（Constitution §1–§2）：

1. [`CONSTITUTION.md`](../CONSTITUTION.md)（儲存庫根目錄）——流程、
   治理（governance）、擁有權、不變量（invariant）。
2. [`design/Auspex_ADD.md`](design/Auspex_ADD.md) ——架構、領域
   模型、功能需求、路線圖——依已核准的 ADR（見 [`adr/`](adr/)）修訂。

此樹狀結構中的其餘內容，都屬於次要細節、歷史紀錄，或工作文件。

## 導覽（Map）

| 資料夾／檔案 | 內容 |
|---|---|
| [`design/`](design/README.md) | 三份權威設計文件：ADD（架構／需求規格）、predictor 設計補充文件，以及垂直切片並行執行計畫。 |
| [`adr/`](adr/README.md) | 已核准的 Architecture Decision Record，以 `NNNN-title.md` 編號。一旦核准即不可變更；由更新的 ADR 取代，但絕不修改原文。 |
| [`DECISION_LOG.md`](DECISION_LOG.md) | 每一項擁有者層級（owner-level）的決策，皆以 `D-##` 條目記錄，並附決策樹：考慮過的選項、最終選擇、後果、可逆性。以繁體中文撰寫。 |
| [`implementation/`](implementation/README.md) | 垂直切片實際的建置過程：執行 DAG、各角色的進度紀錄、合約凍結、經驗教訓，以及各波次（phase）後的分析。屬於歷史紀錄——適合考古，而非現行指引。 |
| [`methodology/`](methodology/README.md) | 本儲存庫所採用的多 agent、以證據為基礎的開發方法論，經提煉後可用於其他專案。 |
| [`backlog/`](backlog/README.md) | 已核准但尚未排程之工作的設計筆記，每項皆連結至一個追蹤 issue。 |
| [`archive/`](archive/README.md) | 已被取代、僅供歷史參考的文件。絕非現行實作指引。 |
| [`repository_inventory.md`](repository_inventory.md) | 對儲存庫中每一份 markdown 檔案的稽核：其權威層級與狀態。 |

## 語言

每一份文件都有一份對應的繁體中文版本，命名為
`<name>.zh-TW.md`，並在兩份檔案的開頭互相連結（ADR-049）。
**該文件原始撰寫所使用的語言，才是具規範性的文本。**
對於以英文撰寫的文件（除了兩份例外）而言，英文版是
規範版本，若翻譯有出入，翻譯即為錯誤（bug）。有兩份
文件是以繁體中文撰寫，其原文即具規範性，
沒有對應的 `.zh-TW.md` 版本：
[`design/Auspex_ADD.md`](design/Auspex_ADD.md) 與
[`DECISION_LOG.md`](DECISION_LOG.md)。
</content>
