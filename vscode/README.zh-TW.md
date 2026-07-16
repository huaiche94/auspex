# Auspex — VS Code 隨附擴充套件（MVP）

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

[Auspex](https://github.com/huaiche94/auspex) daemon 的隨附
（companion）UI（issue #10；ADD §8.4，FR-162/163/164）：即時 daemon
狀態、喚醒工作（wake-job）佇列，以及一項可執行的變更操作——取消
已排程的 resume。

> **Publisher 備註：** `publisher` 目前設定為 `auspex`，但**尚未
> 註冊為 VS Code Marketplace／Open VSX 的 publisher**——註冊是僅限
> 擁有者（owner-only）的待辦動作，追蹤於 issue #18。在此之前，此
> 擴充套件僅能以原始碼方式，或透過本機封裝的 VSIX 使用。

## 功能內容

- **狀態列項目（Status bar item）** — daemon 存活狀態與喚醒工作
  摘要（`auspex: not running` 是一種*正常*狀態，會平靜地顯示，
  絕不會跳出錯誤彈窗）；工具提示另外顯示目前 session 的風險與
  runway（尚無資料時誠實顯示「unknown」）。
- **Auspex activity-bar 檢視畫面** — 六個 FR-162 區塊加上 daemon
  狀態與喚醒工作佇列：**Status**、**Risk**、**Runway**、
  **Quota freshness**、**Progress**、**Checkpoints**、**Pause state**、
  **Scheduled wake jobs**。session 區塊呈現 daemon 的 per-session
  read-model（`GET /v1/session/status`，schema
  `auspex.daemon.session_status.v1` — `internal/sessionstatus`）。狀態為
  `scheduled` 的喚醒工作會附帶一個內嵌的 **Cancel** 按鈕（FR-163），
  在佇列區塊與其所屬 pause 記錄之下皆可使用。
- **指令** — `Auspex: Refresh`、`Auspex: Cancel Scheduled Resume`、
  `Auspex: Show Raw Status`。
- **即時更新** — 訂閱 daemon 的 SSE 串流（`GET /v1/events/stream`），
  採指數退避（exponential-backoff）重連機制，另外每 15 秒輪詢一次
  作為安全網。daemon 的 broker 不保留事件歷史記錄
  （`internal/daemon/broker.go`），因此不支援 `Last-Event-ID` 重播：
  每次（重新）連線都會從 status／jobs／session 端點重新讀取目前
  狀態。

## 連線方式（以及絕不會碰觸的部分）

探索（discovery）流程與 CLI 自身的探測順序一致：

1. 解析 Auspex 依作業系統而異的執行期目錄（`src/paths.ts`，是
   daemon `internal/paths/paths.go` 的精確 TypeScript 對應實作）。
2. 讀取 `<runtime>/daemon.json`（`internal/daemon/metadata.go`）——
   檔案不存在即代表「daemon 未執行」。
3. 從 metadata 的 `token_file` 讀取 bearer token
   （`<data>/daemon.token`，權限 0600，每次 daemon 重新啟動即輪替——
   D-16）。
4. 以 `Authorization: Bearer <token>` 呼叫 `http://<address>/v1/...`。

**FR-164：** 此擴充套件**僅讀取 Auspex 自身的檔案**（上述兩項），
且**僅與 Auspex daemon 的 loopback API 通訊**。它不會讀取任何其他
擴充套件的私有狀態、不會碰觸 provider 憑證，也完全不含
`vscode.extensions` 狀態存取。

## 誠實呈現（FR-162）

daemon 現已提供完整的 FR-162 per-session read-model：
`GET /v1/session/status`（最近一個 session——預設檢視）與
`GET /v1/session/{id}/status`，schema 為
`auspex.daemon.session_status.v1`
（`internal/sessionstatus/snapshot.go`）。過去的「daemon API 尚未
提供此資訊」占位文字已移除；各區塊依伺服器端的誠實不變量
（ADD §8.8、Constitution §7）呈現真實資料：

- **未知絕不顯示為零。** 伺服器以 JSON `null` 回應的區塊（尚無
  prediction、尚無 runway forecast、尚無 checkpoint、尚無 pause
  記錄），以及「尚無任何 session」的 404，都會顯示為明確的
  「unknown／no data yet」項目——絕不虛構分數、百分比或預測。為
  null 的可選純量（`used_percent`、burn rate、重置時間……）顯示為
  「unknown」或直接省略，不會顯示為 0。
- **`calibrated: false` 代表估計值，不是機率。** 來自未校準模型的
  風險與 runway 分數會標示為「uncalibrated estimate」；此情況下
  `hit_probability` 也會改稱「hit estimate」（Constitution 原則
  #2）。
- **Quota 過期標示僅屬顯示層。** 伺服器計算每個視窗的
  `age_seconds`；此擴充套件在超過 5 分鐘時標示該視窗為
  「stale」（`src/sections.ts` 的 `QUOTA_STALE_AFTER_SECONDS`）——
  這是顯示層的選擇並記載於項目工具提示中，並非伺服器的判斷。

仍然為真、且在 UI 中誠實陳述的缺口：**progress tree** 目前通常是
空的（多數 session 尚無任何東西寫入 `progress_nodes`）、**risk** 在
prediction 可連結到該 session 之前為 null、**runway** 在 forecast
寫入之前為 null（native-hook 模式自 PR #85 起有即時資料）。payload
僅承載數字與 id——節點標題、checkpoint manifest 與檔案系統路徑皆已
在伺服器端排除（FR-171）。

## 開發

```bash
cd vscode
npm ci
npm run build       # tsc → out/
npm test            # builds, then scripts/run-tests.js (node --test with
                    # an explicit file list; fails loudly if zero test
                    # files are discovered — the script's comment explains
                    # the Node 20 vs 22 --test path-semantics difference)
```

相依套件版本一律**精確釘選**（不使用 `^`／`~` 浮動版本），與此
儲存庫的 CI 版本釘選政策一致（詳見 `.github/workflows/ci.yml`）。

### 測試涵蓋範圍

以 Node 內建的測試執行器進行單元測試（不需下載 VS Code）：

- `src/paths.ts` — 涵蓋每一種作業系統分支，並注入環境變數／home
  路徑（`src/test/paths.test.ts`）；
- `src/sse.ts` — 針對 daemon 串流確切格式的 SSE 剖析，包含
  chunk 切分、CRLF、心跳（heartbeats）、退避排程
  （`src/test/sse.test.ts`）；
- `src/types.ts` — 依照從 Go handler 逐欄位複製而來的 fixture，
  驗證 response／metadata 剖析，包含完整填值、全 null 與格式錯誤的
  session-status 形狀（`src/test/types.test.ts`）；
- `src/sections.ts` — FR-162 的誠實呈現：每個 session 區塊的
  unknown-vs-present、校準標示、quota 過期標示、progress 階層、
  取消按鈕串接（`src/test/sections.test.ts`）；
- `src/client.ts` — `getSessionStatus` 的 URL／auth／404 行為，
  以真實的 loopback `node:http` 伺服器驗證
  （`src/test/client.test.ts`）。

未涵蓋於自動化測試的部分：`src/extension.ts`／`src/tree.ts`
（extension-host 的 UI 串接——`tree.ts` 只是把已測試的
`sections.ts` view-model 薄薄地映射到 `vscode.TreeItem`，以人工方式
驗證）以及 `src/client.ts` 中 SSE 的網路路徑（以真實 daemon 進行
smoke test；詳見該 PR 的驗證備註）。刻意在 MVP 階段省略
`@vscode/test-electron` 測試框架，以維持工具鏈的精簡。

從原始碼執行：在 VS Code 中開啟 `vscode/`，執行
`npm ci && npm run build`，然後按下 F5（「Run Extension」——標準的
extension-development host）。
