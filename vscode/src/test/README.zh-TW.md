# vscode/src/test/ — 擴充套件純邏輯層的單元測試

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

測試使用 Node 內建的測試執行器（`node:test`）—— 不需下載 VS Code，也
不使用 `@vscode/test-electron` 測試框架（這是 MVP 階段刻意省略的部
分；詳見 [`../../README.md`](../../README.md)）。

- `paths.test.ts` —— 針對 [`../paths.ts`](../paths.ts) 各作業系統分
  支的測試，並注入自訂的環境變數／家目錄。
- `sse.test.ts` —— 針對 daemon 串流確切格式的 SSE 解析測試，涵蓋
  chunk 切分、CRLF、心跳（heartbeat），以及重連退避（backoff）排程
  （[`../sse.ts`](../sse.ts)）。
- `types.test.ts` —— 針對回應／中繼資料的解析測試，比對逐欄位自 Go
  handler 複製而來的 fixture，包含完整填值、全 null 與格式錯誤的
  `auspex.daemon.session_status.v1` 形狀
  （[`../types.ts`](../types.ts)）。
- `sections.test.ts` —— [`../sections.ts`](../sections.ts) 的 FR-162
  誠實呈現測試：每個 session 區塊的 unknown-vs-present、校準標示、
  quota 過期標示、progress 階層，以及喚醒工作的取消串接。
- `client.test.ts` —— [`../client.ts`](../client.ts) 中
  `getSessionStatus` 的 URL／auth／404 行為，以真實的 loopback
  `node:http` 伺服器驗證。

## 執行方式

`npm test` 會先將 `src/` 編譯成 `out/`（`pretest` 建置步驟），接著由
[`../../scripts/run-tests.js`](../../scripts/run-tests.js) 列舉已編譯
的 `out/test/*.test.js` 檔案，並交給 `node --test` 明確的檔案清單，若
發現零個測試檔案則明確失敗 —— 絕不會在空跑（empty run）時悄悄顯示綠
燈。CI 在
[`../../../.github/workflows/ci.yml`](../../../.github/workflows/ci.yml)
的 `vscode` job 中，於固定為 22.11.0 的 Node 版本上執行同一個
`npm test` 步驟。

本目錄未涵蓋：`extension.ts`／`tree.ts`（擴充套件宿主 UI 接線，以人工
方式驗證——`tree.ts` 只是把已測試的 `sections.ts` view-model 薄薄地映
射到 `vscode.TreeItem`）以及 `client.ts` 中 SSE 的網路路徑（以真實
daemon 進行冒煙測試）。
