# 安全性政策（Security Policy）

> 🌐 [English](SECURITY.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

Auspex 是一套本地優先（local-first）的預測式執行期守門系統，位於 AI 編碼代理（Codex、Claude Code，以及未來其他 provider）回合（turn）的執行路徑上。它的威脅模型（threat model）比一般 CLI 工具更重要：它會觀察 hook payload、配額／用量訊號，以及 repository 狀態，並代替使用者做出暫停／恢復與 checkpoint 決策。我們認真看待漏洞回報，並請你負責任地回報。

## 回報一項漏洞

**請不要以公開的 GitHub issue 回報安全性漏洞。**

請透過本 repository 的 **GitHub Security Advisories** 私下回報
（「Security」分頁 →「Report a vulnerability」）。這會建立一個僅維護者與你本人可見的私下 advisory，讓我們能在細節公開之前協調修復與揭露時程。

- **確認回覆目標：** 3 個工作天。
- 請包含：受影響的版本／commit、重現步驟（若實際重現本身就不安全，請改用清楚的文字描述）、以及你認為的影響範圍。
- 如果該回報涉及特定的 provider 整合（例如 Claude
  Code 的 hook payload 問題），請明確說明——與 provider 相關的回報，除了我們自己的流程之外，可能還需要與該 provider 自身的揭露流程協調。

此流程依 `docs/design/Auspex_ADD.md` §30.8 為規範版本。若本檔案與 ADD 有分歧，以
ADD 為準（見 `CONSTITUTION.md` §1），本檔案視為需要修正的錯誤。

## 支援的版本

Auspex 目前處於 pre-1.0 階段（vertical slice 仍在活躍、里程碑閘控的建置中——見
`README.md` 的 phase roadmap）。在 1.0 發布之前，安全性修復僅會落地於
`main`；目前還沒有長期支援（long-term-support）分支。1.0 發布之後，本章節會依
`docs/design/Auspex_ADD.md` §30.6 的 SemVer／穩定性保證，更新為真正的支援矩陣。

## 適用範圍（Scope）

範圍之內：

- Go 執行期（`cmd/`、`internal/`、`pkg/protocol/v1/`）及其 SQLite
  儲存層。
- Provider 轉接器（adapter）（例如 `internal/providers/claude`、
  `internal/hooks/claude`），以及它們如何解析／正規化不受信任的 provider 輸入。
- VS Code companion 延伸模組（一旦它存在）。
- CI／發布供應鏈設定（`.github/**`）（一旦發布 workflow 存在）。

範圍之外：

- 上游相依套件中的漏洞，且沒有展示出 Auspex 專屬的可利用路徑（請改向上游回報；但如果某個相依套件漏洞可從
  Auspex 自身的攻擊面觸及，我們仍然想知道）。
- 針對回報者自己已完全掌控的單一本機機器所提出的社交工程、實體存取，或阻斷服務（denial-of-service）回報
  （依 `docs/design/Auspex_ADD.md` §1.4，Auspex 是本地優先、單一機器的工具——「攻擊者已經在你的機器上取得
  shell」對這個專案而言不是一個有用的威脅模型，這與大多數本機開發者工具一致）。

## 我們在這裡認定為安全性問題的項目

Auspex 的設計做出了具體、可測試的安全與隱私承諾
（見 `agents/qa.md` 的「Security assertions」，隨專案成熟由 `qa`
role 自身的測試套件驗證）。針對以下任一項的回報，都明確屬於範圍之內：

- 原始 prompt 文字或工具輸出逃脫了其宣告的不持久化邊界（在本該不會被持久化、記錄，或傳輸時卻發生了——見
  `docs/design/Auspex_ADD.md` 的「Unknown is not zero」／隱私優先預設值原則，以及
  `CONTRACT_FREEZE.md` 的隱私合約）。
- Bearer token、API key，或其他憑證，未經遮蔽（unredacted）地出現在
  日誌、資料庫匯出、checkpoint manifest，或支援包（support bundle）中。
- ADD 要求具備身分驗證、卻缺少驗證的 loopback／本機 API，或是可以從本機以外的地方存取。
- 未設定大小限制而被處理的 hook payload（透過惡意或故障的 provider hook 造成資源耗盡）。
- 在支援限制檔案權限的平台上，SQLite 資料庫檔案或 checkpoint／產物檔案卻以過於寬鬆的檔案權限建立。
- 外部指令執行（git、provider CLI）以 shell 字串插值（string
  interpolation）建構，而非以 argv 陣列呼叫，造成指令或參數注入（injection）風險。
- Repository Checkpoint／產物解壓縮（extraction）可以寫入其預定目的地目錄之外（路徑穿越／symlink 逃逸）。
- Auto-resume 在沒有明確、限定於工作區（workspace-scoped）的事先使用者同意下被觸發，或是以相對於原始 session
  被提升的權限恢復執行。

## 隱私敏感變更

依 `docs/design/Auspex_ADD.md` §30.9，任何對原始 prompt 保留行為、
對外遙測、auto-resume 預設值、狀態產物內容，或遠端 checkpoint
行為的變更，都需要一次隱私審查、一份 ADR（見
`CONSTITUTION.md` §3），以及一則 changelog 條目——而不只是一次程式碼審查。如果你正在提出這樣的變更（無論是否出於安全性考量），請在 PR 描述中明確說明，讓審閱者套用這個標準。

## 協同揭露（Coordinated disclosure）

除非回報者要求不要具名，我們會在最終公開的 advisory 中致謝回報者（以姓名或代稱，依回報者偏好）。我們的目標是在修復發布後盡快公開一份
advisory；如果某個回報最終並非漏洞，我們仍然會在上述確認回覆時限內回覆並說明原因。
