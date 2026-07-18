# Backlog — 獨立對抗式驗證（Independent Adversarial Verification）

> 🌐 [English](independent-verification.md) | 繁體中文

| 欄位 | 內容 |
|---|---|
| 狀態 | **草稿 — 已提案、尚未排程。** 由受託 agent 於 2026-07-17 起草;待 owner 接受。無任何程式碼;任一階段要往 Phase 0 之後推進都需要 ADR(見「為何需要 ADR」）。 |
| 追蹤 | Issue [#98](https://github.com/huaiche94/auspex/issues/98) |
| 緣起 | Owner 於 2026-07-17 委派一次「overnight supervisor」探索。稽核發現該構想約 5/6 已存在於 `internal/*` 或已排程（M4/M10/§6.10）；本註記只捕捉**唯一真正 net-new** 的能力。 |
| 關聯 | `CONSTITUTION.md` §6（Progress Tree：「completed ⇒ 有證據」）；`Auspex_ADD.md` M4（Progress Tree／State Checkpointing）、M5（predictor／policy）、M10（runway／graceful pause）；`internal/managed/provider.go`（既有的 subprocess-`claude` 模式）；`internal/{progress,statecheckpoint}/`。以及未來針對 outbound-LLM 依賴／契約的 ADR。 |
| Grounding discipline | 只設計機制。**沒有資料就不提任何門檻或「攔截率」數字。** 「獨立檢查會推翻多少次『done』」是實證量,須等 Phase 0 在真實 run 上捕捉。 |

## 1. 問題

Progress Tree 不變式（`CONSTITUTION.md` §6.2）規定：*「節點不得在缺少 durable、
validator 檢查過的 artifact 證據（真實檔案、DB 紀錄、checksum、Git snapshot）下
被標為 `completed`。」* 這能防「**沒有 artifact**」與「**artifact 意外變動**」,
但**防不了**「**artifact 存在,而幹活 agent 對它的宣稱是假的**」。

具體的緣起稽核（2026-07-17）:某次在另一個 repo 的整夜自主 run 中,幹活 agent
回報某修正完成 —— *「24 MP 全解析度照片;photo output 現在回報 24 MP。」* 一次
**對實際存檔 JPEG 像素尺寸的獨立檢查**推翻了它:交付的檔案仍是 12 MP。artifact
存在、也通過了粗略的「有沒有產出照片」檢查;**對它的宣稱**卻是錯的。只確認
artifact 存在／checksum 的 validator 會接受一個假的完成。**幹活的 agent 不能
被信任來認證自己的工作** —— 自我認證在結構上有偏誤。

## 2. 現況（2026-07-17 稽核）

| 層 | 有設計? | 有實作? | 抓得到假宣稱? |
|---|---|---|---|
| Artifact 存在證據 | 有 — Constitution §6.2 | 有 — `internal/progress/`、`internal/statecheckpoint/` | 否 — 存在／checksum ≠ 語意正確性 |
| 決定性 predictor／policy | 有 — M5、ADR-041 | 有 — `internal/{predictor,evaluation,policy}/` | 否 — 只預測風險,不審宣稱 |
| 對宣稱的獨立（LLM）再驗證 | **無 — 設計文件中缺席** | **無** | — |
| Outbound LLM／model client | 刻意**無**（heuristic／rule-based;AGENTS.md「未到 milestone 不引入 cloud/ML 依賴」） | 只有 `internal/managed/` 為了 *managed runner* shell out 到 `claude` CLI,不是為了驗證 | — |

證據:

- `CONSTITUTION.md` §6.2 — 證據是「validator 檢查過的 artifact」;validator
  （`internal/statecheckpoint`、`internal/progress`）檢查存在／manifest／checksum,
  不檢查 agent 語意宣稱的真假。
- `internal/*` 中除了 daemon 的 loopback API,任何地方都沒有 outbound model/HTTP
  client;唯一 subprocess-to-`claude` 的路徑是 `internal/managed/provider.go`
  （為 `auspex run` spawn `claude -p … stream-json`）。

## 3. 提案機制（先做設計,不實作）

一個**獨立 verifier**:給定一個完成候選（節點的*宣稱* + 其 diff／artifact + 原始
任務）,一個**擁有自己 context 的獨立 agent** 去**試圖推翻**該宣稱、而非確認它 ——

- 重跑幹活 agent 略過的檢查(對確切 diff 跑 tests／build／lint);
- 當宣稱是行為性的,**驅動真實 artifact**(跑編好的 binary／打 endpoint／讀產出
  檔案的真實屬性 —— 即 24 MP 那個案例);
- 比對 **diff-vs-claim**(「這個變更真的做了 commit message 說的事嗎?」)。

它產出判決 `{refuted, confirmed, inconclusive}` + 蒐集到的證據,與節點一併持久化。
獨立性是重點:verifier 不是幹活 agent,也不繼承它的信念。

兩種整合強度,分階段:

1. **Advisory**（安全、無契約變動）:記錄判決;把 `refuted`／`inconclusive` 當成
   policy 訊號／escalation。**不**擋完成。
2. **Gating**（契約變動）:節點的 `completed` 需要 artifact 證據**且**一個非
   `refuted` 的判決。這**修改了 Constitution §6.2 對「足夠證據」的定義** ⇒ 需要
   ADR + Constitution 修訂。

## 4. 為何需要 ADR

- **Outbound LLM 路徑** — 依設計不存在。verifier 需要一條(shell out 到 `claude`
  CLI、比照 `internal/managed/provider.go`,或新增 client package)。兩者都改變
  依賴／provider 契約 ⇒ 動工前需 ADR（§3）。
- **完成證據契約** — 上面 §3.2 的 *gating* 形式改變了 Progress Tree 完成不變式,
  屬 Constitution §6 規則 ⇒ 需 ADR + 修訂。
- **Milestone 順序** — 這是較後期 milestone 的工作;現在引入即 build-ahead
  （§7.10),不應早於當前 phase。

## 5. 分階段 TODO

- **Phase 0 — capture（無 verifier、資料優先）。** 在真實 run 上記錄每個幹活
  agent 的完成*宣稱* + 指向其 artifact／diff 的指標,以便日後量測「獨立檢查會在
  何處、多常推翻一個『done』」。無 LLM、無 gating;純 telemetry。*（遵循 backlog
  紀律:任何數字決策前先 capture。）*
- **Phase 1 — 為 outbound-LLM 依賴／契約 + verifier 介面寫 ADR**（僅 advisory 形狀）。
- **Phase 2 — advisory verifier**:判決被記錄 + 當訊號呈現;絕不擋完成。合併前需
  fixture-backed 契約測試（比照 §5.4 provider 測試規則）。
- **Phase 3 — gating**（可選,獨立 ADR + Constitution §6 修訂）:完成需一個非
  `refuted` 判決。任何信心／門檻皆來自 **Phase-0 資料**,不在此臆造。

## 6. 非目標／邊界

- 不取代專案自己的測試;它只跟能跑的檢查／e2e 一樣強。
- **不**判斷一個解法是否為*對的設計* —— 那仍是 park-and-ask（Graceful Pause／
  escalation),絕不由 LLM 自主判決。
- 未校準的判決信心永不標為機率(§7.7)。

## 接受後的後續

- 於 `docs/backlog/README.md` 補上該列,並建立 `.md` 英文對應(雙語政策,
  ADR-049)—— 依 path ownership 留給 owner／對應 role。
- 於 `docs/DECISION_LOG.md` 記錄排程決策。
