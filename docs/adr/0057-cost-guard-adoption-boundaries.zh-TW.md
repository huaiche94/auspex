# ADR-0057 — Cost Guard：成本治理採用集、產品邊界與 hard-budget 保證語意（#140–#149）

> 🌐 [English](0057-cost-guard-adoption-boundaries.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

Status：Accepted
Date：2026-07-29
Owner：owner-directed（2026-07-29 owner 指示採納成本治理 roadmap 分析；本 ADR 記錄該指示所批准的決策集）
Tracking：issues #140–#149；分析 artifact `.lavish/agent-cost-roadmap-analysis.html`（14 個外部來源）；外部證據：[Token FinOps](https://cloudandsre.com/blog/token-finops-the-third-budget/)、[The Harness Effect](https://arxiv.org/abs/2607.06906)、[Wattage](https://github.com/faizannraza/wattage)、[Stoke](https://github.com/Ozperium/stoke)；既有基礎：ADR-043（成本預算作為 policy 資源）、ADR-051/052（capture＋隱私 interning）、ADR-0055（四類經驗成本）

## Context（脈絡）

一份涵蓋 14 個來源的 agent 成本治理生態review（6 個社群討論、8 個
文章／開源實作）收斂到四種機制：hard budgets、outcome economics、
loop detection、attribution——以及一個 meta 發現：dashboard 太晚，
控制必須出現在下一個 call、下一個 turn、下一個 unsafe action 之前。

Auspex 的產品定位約束「如何採用」這些機制。Auspex 是 coding-agent
的 **governance and continuity layer**：local-first、capability-aware、
有 evidence-gated Progress Tree（Constitution §6）與嚴格的隱私姿態
（Constitution §7）。它刻意不作為 proxy 站在 agent 與 provider 之間、
不選模型、不改寫 prompt。分析中最值得守住的產品句子成為本 ADR 的
框架：**不要把 Auspex 變成 proxy；把成本變成 continuity policy。**

## Decision（決策）

### 1. 直接採用（7 項）—— Cost Guard 能力集

| # | 能力 | Issue | 首要 gate |
|---|---|---|---|
| 1 | Cost per evidenced Progress Tree node（outcome ledger：completed/failed/abandoned/manual-rescue） | #140 | report 切片可先行；M13 outcome labels |
| 2 | Managed task/session budget envelope（spent/reserved/remaining；safe-point 執法） | #141 | 本 ADR 定語意；M11 UX；M14 capability 標示 |
| 3 | Blocking policy 的 shadow mode（`would_action`、avoided spend、false-positive review） | #142 | 無——第一個實作步驟 |
| 4 | Spin gate（repeat-rate＋no-progress＋verifier trend），shadow-first | #143 | M13 閾值擬合；#68 |
| 5 | Cost regression baseline＋quality-aware `report diff` | #144 | M13 benchmark；M15 CI |
| 6 | Subagent 父子成本歸因 | #145 | provider tail；#91 |
| 7 | Cache/context hygiene＋right-sizing hints（僅描述性） | #146 | M13 cohort baselines；#66/#91 |

以上全部延伸既有的 telemetry、policy、Progress Tree、checkpoint 與
managed-runner 基礎；沒有任何一項引入新的外部依賴。

### 2. 調整採用（3 項）—— Auspex-native 改寫，不是移植

Priority reserve／budget borrowing 以 Progress Tree 的
**critical-path node budgets** 形式出現，不是通用 swarm allocator
（#147）。Tool-output／retry／fallback 異常 telemetry 以
**numbers-only** 形式、遵循 ADR-052 隱私紀律，schema 前需自己的
ADR review（#148）。Team 成本 rollup 是 **FR-170 去識別化匯出的
static merge**——沒有 server、不上傳（#149）。

### 3. 不進核心（4 項）—— governor／harness-owner 邊界

Universal LLM proxy、自動 model/effort routing、自動 prompt
compression/rewriting、payment credential firewall **不進**核心產品。
理由：每一項都會讓 Auspex 從 governor 變成 harness owner——一旦
Auspex 選模型、改寫 prompt 或計量 proxy，它就要對無法保證的品質
回歸與 provider 行為負責，也破壞 capability-aware、provider-native
的姿態。邊界形式：Auspex 可以**觀測、解釋、建議**（如描述性的
right-sizing hints，#146），外部工具（Turo、SpendGuard、Stoke）可以
在 Auspex **旁邊**整合；routing 決策、prompt 改寫與支付控制留在
外面。Model/effort routing 明確上限為描述性 hints（#146）——絕不
自動切換。

### 4. Hard-budget 保證語意（對所有執法功能具規範性）

「Hard budget」必須依 runtime mode＋provider capability 能實際保證
的內容標示；unknown 維持 unknown：

- **Managed＋live usage**（如 codex App Server
  `thread/tokenUsage/updated`）：最強保證。下一個 turn 前 reservation；
  接近 envelope 時停止 unsafe dispatch、等 safe point、checkpoint、
  interrupt（ADD §21.6／PR #137 機制）、再依 policy pause。mid-turn
  guard 可行。
- **Managed＋end-only usage**：bounded overshoot。保證＝不開始無法
  reserve 的新 turn；單一 active turn 可能超出預估。介面**必須**寫
  「最多可能超出一個 turn」，**不得**宣稱 token-exact hard cap。
- **Native hook**：僅 advisory。pre-turn block/warn/checkpoint；
  可靠的 mid-turn pause 不可能（ADD §8.8）；provider account cap
  仍是最後一道牆，Auspex 顯示自身能力降級。

執法一律走既有的凍結 pause 生命週期（safe point → checkpoints →
interrupt → durable wake job）——絕不 mid-flight 殺行程。

### 5. Shadow-first 與校準紀律

所有執法級 policy action（今日的 BLOCK；未來的 budget-envelope
pause 與 spin-gate stop）一律 **shadow-first** 出貨：記錄
`would_action`＋持久化 incident＋預估 avoided spend，評估 gate 通過
前不執法。Spin-gate 閾值（repeat-rate、verifier slope、no-progress
window）**必須**由 Auspex 自己的 telemetry 校準（M13）——外部係數
只是方向性證據，絕不是出貨數字（ADR-0053／ADD §15.6 紀律套用到
policy）。

### 6. 記帳與隱私規則

- **Reservation 誠實**：reservation 記錄 source、confidence 與 band；
  未校準的 `HighUSD` 上界是估計，絕不當保證呈現。
- **Canonical ownership**：parent session／subagent session／task／
  node／turn 的 rollup 為每一筆 spend 定義唯一 owner；聚合必須
  結構性防止重複計算（#140/#145 共用此規則）。
- **Numbers-only telemetry**：新的成本歸因訊號只持久化 bytes、
  counts、classes、deltas——不存 raw tool output、不超出 ADR-052
  ordinal interning 的路徑暴露、不存可逆的內容摘要。
- **餓死防護**：budget pause 介面必須顯示剩餘 critical path、
  completion evidence 與 override tradeoff；break-glass override 是
  single-use、audited、workspace-scoped 的授權（既有 `decision
  allow` 一次性消耗模式）。

### 7. Roadmap 對映（方向，非 ADD 條文）

M11：budget envelope＋shadow/enforce 開關＋override UX。M13：
outcome economics、spin 閾值擬合、cost regression baseline、
cache/context cohorts。M14：provider 執法能力協商（hard/advisory
標示）。M15：reservation 崩潰復原、並發 overshoot 測試、audit
bundle。Dynamic borrowing（#147）與 team governance（#149）等
outcome data 可靠後再做。ADD 仍是 roadmap source of truth——
milestone 條文修訂隨各實作切片落地，不隨本 ADR。

## Consequences（後果）

- 十個追蹤 issue（#140–#149）承載工作；實作順序依分析建議：
  shadow mode → outcome ledger → budget envelope → 資料 gate 的
  其餘項目。
- 拒絕清單是常設產品邊界：未來凡屬 proxy、routing、prompt 改寫或
  支付控制的提案，除非有後繼 ADR 取代，一律由本 ADR 回答。
- 保證語意標示（§4）自第一個 envelope 切片起，成為所有 budget
  相關介面的 UI／docs 義務。
