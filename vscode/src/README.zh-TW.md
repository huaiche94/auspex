# vscode/src/ — VS Code 隨附擴充套件的 TypeScript 原始碼

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

此擴充套件的功能，以及它如何連接到 daemon，記載於
[`../README.md`](../README.md)；本文件則是檔案地圖。由 `tsc` 編譯至
`out/`（`npm run build`，見 [`../tsconfig.json`](../tsconfig.json)）。

## 擴充套件宿主層（引入 `vscode`；以人工方式驗證）

- `extension.ts` —— 啟動流程、狀態列項目、輪詢（polling）與 SSE 驅動
  的刷新，以及命令選單（command palette）介面（FR-162/163/164）。
- `tree.ts` —— Auspex 活動列的樹狀檢視。只渲染 daemon API 實際提供的
  欄位；FR-162 的 session 區塊（Risk、Runway、Quota freshness、
  Progress、Checkpoints、Pause state）只是把 `sections.ts` 的
  view-model 薄薄地映射到 `vscode.TreeItem`。

## 純邏輯層（不引入 `vscode`；可在一般 Node 環境下做單元測試）

- `client.ts` —— daemon 探索（`daemon.json` 中繼資料與 bearer token
  檔案）、對本機回送（loopback）API 的已驗證 HTTP 請求，以及可自動
  重連的 SSE 訂閱。僅使用 Node 內建模組。
- `sections.ts` —— FR-162 session 區塊的 view-model 建構器，資料來自
  `GET /v1/session/status`（`auspex.daemon.session_status.v1`）。
  內建誠實規則：null → 明確的「unknown／no data yet」項目（絕不虛構
  零值）、`calibrated:false` → 分數標示為未校準的估計值。
- `paths.ts` —— 依作業系統解析 Auspex 使用者目錄；與 Go 版 daemon 的
  `internal/paths/paths.go` 逐行對應的 TypeScript 鏡像版本，確保擴充
  套件與 daemon 對 `daemon.json`、`daemon.token` 的存放位置有一致認
  知。
- `sse.ts` —— 針對 daemon 的 `GET /v1/events/stream` 所寫的最小化
  Server-Sent Events 解析器。
- `types.ts` —— daemon 線上資料格式的 TypeScript 鏡像
  （`internal/httpapi/httpapi.go` 的回應、
  `internal/sessionstatus/snapshot.go` 的 per-session read-model、
  `internal/daemon/metadata.go`，
  以及 `pkg/protocol/v1` 的 `Event` —— 其 SSE payload 使用 PascalCase
  鍵名，因為對應的 Go struct 沒有 json tag）。每個欄位都存在於 Go 端
  的 handler 中；沒有任何一項是憑空杜撰的。

純邏輯層的單元測試位於 [`test/`](test/README.md)。
