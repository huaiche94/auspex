# ADR-0056 — Codex App Server JSON-RPC 串流列為解析資料來源;通知對映維持在封閉 EventType 分類學之內(#9 M7 Phase 2)

> 🌐 [English](0056-codex-appserver-data-source.md) | 繁體中文
>
> 本文件為非規範翻譯,內容以英文版為準(ADR-049)。

Status: Accepted
Date: 2026-07-28
Owner: lead-executed(PROPOSAL——新資料來源承諾,owner 於 merge 時簽核)
Tracking: issue #9(M7 Phase 2);架構依 ADR-013(App Server = managed 主路徑)與 ADD §21.2/§21.6/§21.7;因 ADR-052 觸發①(解析新資料來源)而必須立 ADR;協定已對 codex-cli 0.144.5 驗證(`generate-json-schema` + 實測握手)

## Context(背景)

ADR-013 已定架構——Codex 的 managed 主路徑是 stable App Server,
`exec --json` 為後備——且 #83/#87 已出貨 native-hook 與 exec-JSONL 兩個
adapter。App Server client(thread/turn 生命週期、live 事件串流、
`turn/interrupt`、`thread/resume`)是 M7 Phase 2 剩餘範圍,而它引入了
已出貨 adapter 沒有的東西:一個**新的解析資料來源**——App Server 的
JSON-RPC stdio 串流。ADR-052 觸發①規定解析新 provider 來源是 ADR 級
承諾,因為來源本身承載獨立於「哪些欄位騎在哪些事件上」的隱私與穩定性
義務(ADR-051 對 Stop-hook transcript 是同型決策)。

對 pinned binary(0.144.5)實測的線上事實:協定為 stdio 上的
newline-delimited JSON-RPC 2.0(無 Content-Length 框架);回應省略
`jsonrpc` 欄位;伺服器在 `initialize` 後立即主動推播通知;存在
server→client 請求(審批詢問同時帶 `id` 與 `method`)。協定 schema 由
binary 本身生成(`codex app-server generate-json-schema`)——ADD §21.7
的 fixture 錨點。

## Decision(決策)

1. **App Server 串流成為經授權的解析來源**,承擔與所有 provider 來源
   相同的隱私承諾(Constitution §7):型別化解碼結構只命名識別碼、
   數字與列舉狀態。帶文字的欄位只量測、絕不保留——turn 錯誤訊息解碼為
   位元組長度(codexstream 先例),`turn/diff/updated` 解碼為
   `{threadId, turnId, diffByteLen}`,`turn/plan/updated` 解碼為
   `{threadId, turnId, planSteps}`。item 本體(18 變體、帶內容的
   union)只解碼 `{id, type}`。原始 frame 永不持久化。

2. **傳輸 client 位於 `internal/providers/codex/appserver`**,只擁有
   傳輸、關聯與型別化穩定子集(§21.7 紀律的程式化):未知通知方法
   照常遞送且可計數,格式錯誤行與不可路由 frame 計數(`Stats`)、
   永不致命;派發緩衝溢位時丟棄並計數,而非阻塞讀取迴圈。fixtures 為
   vendored 生成 schema(單一權威 pin + 對所消費定義的漂移絆線測試)
   加上實錄線上 transcript;契約測試對行程內 fake server 執行
   (Constitution §5 rule 4——不對真實帳號測試)。

3. **通知對映維持在封閉 EventType 分類學之內——不新增 EventType。**
   後續正規化切片依此表(分類學問題就此一次定案):

   | App Server 通知 | 凍結 EventType |
   |---|---|
   | `thread/tokenUsage/updated` | `provider.usage.observed`(live 每回合用量) |
   | `account/rateLimits/updated`、`account/rateLimits/read` | `provider.quota.observed` |
   | `turn/started` / `turn/completed` | `provider.turn.started` / `provider.turn.completed` |
   | `turn/completed` 且 status `interrupted` | `provider.turn.interrupted` |
   | `turn/completed` 且 status `failed` | `provider.turn.failed` |
   | `item/started` / `item/completed`(工具型 item) | `provider.tool.started` / `provider.tool.completed` |
   | `turn/diff/updated` | `provider.file_change.observed`(payload 僅位元組長度) |
   | context compaction 訊號 | `provider.session.compacted` |
   | `turn/plan/updated` | **無事件**——依 ADD §21.3 / ADR-027/028 作為 Progress Tree「提案」節點消費(provider plan 是 observation,不是事件日誌事實) |

   token 詞彙維持凍結語義 pin:codex `inputTokens` 含
   `cachedInputTokens`;正規化拆出 fresh input 與 cache read、
   `total_tokens` = fresh input + output——與 rollout/managed-exec
   正規化器完全一致,故 ADR-0055 的 cohort 階梯零改動即可消費
   App Server 樣本。

4. **Server→client 請求只上呈、絕不自動核准。** client 在專用 channel
   遞送審批詢問;接線的消費端(managed runner 切片)必須明確回答,
   否則 turn 停滯——停滯的 turn 是誠實的,無聲自動核准是本層不得做的
   policy 決策。

5. **保留的 invocation mode `managed_app_server`**(ADD §16 詞彙,
   自 #87 起在程式碼中保留)由 managed-run 整合切片填入;本 ADR 固定
   其語義:事件由本 client 在存活 App Server 連線下產生的 session。

## Honest scope(誠實範圍)

- 本 ADR 涵蓋「來源」與對映詞彙。正規化into `pkg/protocol/v1` 事件、
  managed-runner 整合、capability 翻真、§21.6 interrupt/resume 序列
  屬後續切片——各切片須落在本對映內,否則回來修訂。
- `account/usage/read` 上游 capability-gated,本階段不消費。
- App Server 連線中斷的重連/退避政策此處刻意不定(實作自由;斷線
  依 ADR-013 降級到 exec-JSONL 後備路徑)。

## Consequences(後果)

- Fixtures:`testdata/codex-schema` 以版本註記 pin 生成協定 schema;
  `testdata/transcripts` 實錄消毒後的線上交換;schema-pin 測試在
  重新生成丟失被消費定義時可見地失敗(§21.7「缺必要欄位 ⇒ capability
  降級」變成測試可見)。
- 凍結 EventType 清單不動;ADR-052 的封閉分類學規則以「對映」而非
  「擴充」維持。
- 本切片不需修訂 `CONTRACT_FREEZE.md`(無 port 變更);實作保留 port
  `app.ManagedRunner`/`LiveObserver`/`SessionResumer` 的 managed-runner
  切片將引用本 ADR 與 ADR-013。

## Alternatives considered(曾考慮的替代方案)

- **為 plan/diff/live-usage 通知新增 EventType。** 否決:每個被消費的
  通知都能語義對映到既有型別;plan 更新依設計是 Progress Tree
  observation(§21.3)。為方便打開封閉分類學,違反 ADR-052 觸發④的
  精神且無資訊增益。
- **持久化原始通知 payload 供日後挖掘。** 直接否決:diff 本體與 item
  內容是使用者程式碼與 agent 文字——§7 隱私預設禁止;僅數字的豐富化
  是本來源遵循的 ADR-051 先例。
- **client 內自動核准伺服器審批請求。** 否決:審批是 policy 不是
  傳輸;client 上呈、policy 層決定(寧可 turn 停滯後被 interrupt,
  絕不無聲提權——§6.10)。
