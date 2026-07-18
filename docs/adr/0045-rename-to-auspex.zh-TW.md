# ADR-045 — 將產品由 Preflight 更名為 Auspex（取代 ADR-001）

> 🌐 [English](0045-rename-to-auspex.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：已接受（Accepted）
日期：2026-07-13
負責人：lead
核准人：repository owner，2026-07-13（命名決策會議）

## 背景

ADR-001 將產品命名為 Preflight。issue #16 的 pre-release 命名稽核（2026-07-13）發現，這個名稱實質上難以成為可辨識品牌：

- **preflight.sh**——一個仍活躍、近期才推出的 pre-deploy 掃描 CLI 工具，具備 Claude Code/Cursor agent-skill 整合：與本產品幾乎是同一個利基市場。
- 透過 Replicated troubleshoot 與 Red Hat openshift-preflight（兩者皆為仍在持續發行的 Kubernetes 工具），**`preflight` 這個 binary 早已存在於許多開發者的 PATH 上**。
- VS Code 的顯示名稱「Preflight」已被同一利基市場的 AI code-review 擴充套件佔用；GitHub handle 以及三個候選網域（preflight.dev／preflight.sh／getpreflight.com）皆已被註冊；「preflight」在 prepress、CORS、Tailwind 與 CI 詞彙中，是個已存在 30 年的通用詞——識別度趨近於零。

## 決策

將產品、binary、module、協定前綴與狀態目錄更名為 **Auspex**／`auspex`：

- 拉丁文 *auspex*：意指在**一件事開始之前**觀察鳥占以裁定是否可以進行的占卜者——用單一詞彙濃縮了本產品的一句話定位（「這一個 turn 到底該不該開始？」）。英文的 *auspices*（"under the auspices of"）便源自此字。
- 候選名稱稽核（GitHub/npm/Homebrew/.dev RDAP，2026-07-13）：僅有少數不相關的小型專案（星數最高者為 42⭐）、沒有 Homebrew formula、沒有同利基市場的產品、也沒有 binary-on-PATH 衝突。與此次更名同步進行的，還有一次完整、達 #16 等級的註冊／商標稽核；§4.4 保留了發布檢查清單。

隨本 ADR 執行的更名範圍：

1. Go module path `github.com/huaiche94/preflight` → `github.com/huaiche94/auspex`；GitHub repo 已更名（舊網址會重新導向）。
2. Binary `preflight` → `auspex`；`cmd/preflight` → `cmd/auspex`。
3. 凍結的 schema 版本字串重新加上前綴（`preflight.error.v1` → `auspex.error.v1` 等）——之所以允許，僅僅是因為本專案尚處於 pre-release、沒有任何外部使用者；此變更已記錄為 CONTRACT_FREEZE.md 的一項 amendment。在首次公開發布之後，這類變更將被禁止。
4. 作業系統使用者資料目錄 `preflight/` → `auspex/`（pre-release：未出貨任何 migration；既有的本地資料庫將原地棄置）。
5. 具權威性的文件已更名（`Preflight_ADD.md` → `Auspex_ADD.md`、execution plan、predictor supplement、methodology）。
6. `docs/archive/` 與 git 歷史**不會**被改寫——它們是歷史紀錄，將保留舊名稱；ADR-001 在 §33 的條目會被標記為「已取代（superseded）」，而不是被編輯成彷彿 Auspex 一直都是這個名字。

## 影響

- ADR-001 已被取代。ADD §4.4 的檢查清單現在追蹤的是 Auspex 的註冊確認事項；由於 auspex.dev 已被註冊，網域策略轉向複合網域（auspex.tools／getauspex.dev）。
- Statusline 的圖示前綴由 `pf✈` 改為 `ax✈`（鳥的符號保留——auspicium 字面上的意思本來就是觀鳥占卜）。
- 歷史性產物（issue 標題、`docs/archive/` 中 phase 時期的進度文件、commit 訊息）刻意仍維持 Preflight 這個名稱。
