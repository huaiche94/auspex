# 治理（Governance）

> 🌐 [English](GOVERNANCE.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

本文件說明 Auspex 專案中的決策如何產生：維護者結構、架構決策如何被接受、發布授權（release authority），以及這些安排會如何隨專案成長而演變。它以
`docs/design/Auspex_ADD.md` §30.7（「Governance」）為根基；若本檔案與
ADD 有分歧，以 ADD 為準（`CONSTITUTION.md` §1），本檔案視為需要修正的錯誤。

`GOVERNANCE.md` 治理的是*對本專案的決策授權*。
`CONSTITUTION.md` 治理的是*建置過程中的日常流程紀律*（路徑所有權、
ADR 機制、Progress Tree 不變量）。兩者互補而非互相競爭——關於本文件在專案治理層級上遵循的一般模式，見 `CONSTITUTION.md` §8。

## 現階段：Initial（初始階段）

Auspex 目前處於**Initial（初始）**治理階段
（`docs/design/Auspex_ADD.md` §30.7）：

- 由單一的主導維護者（lead maintainer）持有最終決策權。
- 架構與流程決策會走公開的 ADR／issue 流程——提案是可見、可討論、有紀錄的，即使目前最終的接受權仍在一人手上。
- 任何貢獻者都可以提出一份 ADR（`CONSTITUTION.md` §3.2）；只有
  `contract-integrator` role／架構負責人可以接受它。

這反映的是專案今天實際所處的階段（早期、單一主導者、pre-1.0、
vertical slice 仍在建置中——見 `README.md` 的 phase roadmap），而不是一種期望。

## 成熟階段（未來）

一旦專案與貢獻者基礎足以支撐，Auspex 就會邁向**Mature（成熟）**治理階段。依
`docs/design/Auspex_ADD.md` §30.7，這個轉換的門檻是：

- **3 位以上的活躍維護者。**
- **敏感變更需要 2 位核可者**——特別是涉及安全性與
  provider 整合的變更，考量到 Auspex 在 AI 編碼代理執行路徑上的位置。
- **有文件紀錄的發布授權**——誰可以切割並發布一個發布版本，這與誰可以核可一個 PR 是不同的權限。
- **DCO 簽署**仍然是必要的（見 `CONTRIBUTING.md`）。
- **沒有 CLA**——這一點在兩個階段都成立；Contributor License
  Agreement 不屬於 Auspex 的貢獻模式，無論在初始階段或成熟階段皆然，依
  `docs/design/Auspex_ADD.md` §30.7 明確的「no CLA initially」
  （在此應理解為：除非有文件記載的相反決定，否則之後也不會另外新增）。

從 Initial 轉為 Mature 本身就是一項治理決策，會被正式記錄下來
（以 ADR 或對本檔案的明確修訂形式呈現），而不會被當作一次非正式、未記錄的變動處理。

## 架構決策紀錄（ADR）

Auspex 的架構透過 ADR 演進，而不是對
`docs/design/Auspex_ADD.md` 的臨場重新詮釋。完整機制以
`CONSTITUTION.md` §3 為規範版本；摘要如下：

- ADR 存放於 `docs/adr/NNNN-title.md`，依序編號。
- 任何 role 或貢獻者都可以提出一份 ADR。
- 只有 `contract-integrator` role（架構負責人）可以接受一份 ADR。
- 已被接受的 ADR 是不可變的歷史紀錄；要變更一項決策，意味著撰寫一份新的 ADR
  來取代舊的，絕不就地修改已接受 ADR 的決策內容。
- `docs/design/Auspex_ADD.md` 本身只能由 `contract-integrator`
  編輯，且僅限於出現真正的矛盾、必須修改時，並且對應的 ADR 必須在同一次變更中一併落地。

在變更 `CONSTITUTION.md` §3 所列清單中的任何一項之前，ADR 是**必要的**，而非可有可無——這包括正式環境執行期語言、daemon
傳輸方式、以不向後相容方式變更 SQLite schema、provider 整合合約、
checkpoint 格式、隱私預設值、公開的 CLI／API／協定相容性、OSS 授權條款，或是預測輸出從分數變為機率的變更。

## 隱私敏感變更

依 `docs/design/Auspex_ADD.md` §30.9，變更以下任何一項都需要一次隱私審查、
**加上**一份 ADR、**加上**一則 changelog 條目——這是比一般需要 ADR 的變更更嚴格的門檻，疊加在其上，而非取代：

- 原始 prompt 保留（retention）行為；
- 對外遙測（outbound telemetry）；
- auto-resume 的預設行為；
- 狀態產物（state artifact）內容；
- 遠端 checkpoint 行為。

## Vertical-slice 建置期間的路徑所有權

在 vertical slice 由多個並行的 agent role 建置期間，
`CONSTITUTION.md` §4 治理誰可以修改什麼：每個 role 都擁有一組互不重疊的路徑，宣告於其
`agents/*.md` 檔案中；共享／跨領域檔案專屬於
`contract-integrator` 所有；任何 role 都不得擴張自己的所有權。這是日常執行層面的紀律，而不是取代上述維護者／ADR
治理——它之所以存在，是因為專案目前這個階段是由多個 agent session
並行建置的，需要比單一人類團隊通常需要更嚴格、更機制化的「誰來決定」規則。

## 發布授權（Release authority）

發布流程與版本保證規範於
`docs/design/Auspex_ADD.md` §30.4–§30.6（發布目標、發布通路、
SemVer）。正式、指名的發布授權（誰可以標記並發布一個發布版本）會在專案抵達發布
pipeline（`.github/workflows/release.yml`，目前尚未建置）之後，記錄於此——在那之前，主導維護者依上述 Initial 階段的結構持有此授權。

## 安全性治理

漏洞揭露流程（私下的 GitHub Security Advisory、3
個工作天內的確認回覆目標，依
`docs/design/Auspex_ADD.md` §30.8）請見 [`SECURITY.md`](SECURITY.md)。

## 行為準則

社群標準與執行方式請見
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md)。
