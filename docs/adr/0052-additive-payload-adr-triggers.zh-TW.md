# ADR-052 — 事件 payload 與匯出表面的 ADR 觸發條件（解決 ADR-051 / 研究文件 §7.6 的張力）＋ 核准 #67 capture step

> 🌐 [English](0052-additive-payload-adr-triggers.md) | 繁體中文

Status: Accepted
Date: 2026-07-16
Owner: lead
Approved by: repository owner, 2026-07-16（契約張力裁決會議，選項「A」）
Tracking: issue #67（解鎖 slice 3a）；釐清 ADR-051；詮釋 Constitution §3

## Context（脈絡）

同日合併的兩份權威文件，對「在既有事件型別上**加法式新增 payload 欄位**是否屬於
凍結契約變更（需要 ADR）」給出相反認定：

- **ADR-051**（2026-07-15 Accepted）為 `provider.turn.completed` 加入每回合
  用量欄位，其 Consequences 寫道：「**無任何凍結契約變更。** payload 欄位為
  既有事件上的加法式 JSON。」
- **`docs/backlog/token-cost-prediction-research.md` §7.6**（同日經 PR #71
  合併）將「新增 `provider.turn.completed` payload 欄位」列為凍結契約面之一：
  「每一項皆為凍結契約面，故實作須附帶自己的 ADR（Constitution §3）。」

實務先例同樣分裂：cost rail（PR #73）與 duration rail（0062 + PR #80 報表側）
以「加法式免 ADR」先例落地、**未附** ADR；model/effort capture（ADR-047）與
transcript 用量 capture（ADR-051）則**附有** ADR。

凍結 envelope 本身（`pkg/protocol/v1/event.go`、CONTRACT_FREEZE.md）列舉的是
envelope 欄位並封閉 EventType 分類法，但 `Payload` 的型別為
`map[string]any` —— 鍵集刻意開放，且所有 consumer 必須容忍未知欄位
（§21.7；各處的 `unknown_fields` fixtures）。

## Decision（決定）

ADR 的觸發條件**不是**「新增 payload 欄位」這個動作，而是以下四種實質之一：

1. **解析新的資料來源。** 讀取 auspex 先前未讀取的表面（如 ADR-051 的
   transcript、provider 的 rollout/state 檔案）需要 ADR —— 新來源承載隱私
   與穩定性承諾。
2. **語義凍結或分歧。** 某鍵的意義偏離既有凍結詞彙、或新釘死一項語義
   （如 `total_tokens` = fresh input + output，而非含 cache 的總和），該決定
   必須被記錄。
3. **擴充版本化匯出表面。** 對受審查、帶 schema 版本的表面
   （`auspex.observations-export.v1` 白名單、校準匯出形狀、daemon API schema）
   新增欄位需要 ADR —— 這些鍵集依其自身「未經審查者不得進入」規則**即為契約**。
4. **任何非加法變更** —— 改名、移除、型別變更、語義重用 —— 依 Constitution §3
   原有規則。

反之，**加法式、僅數字/id、fail-open** 的 payload 鍵，若語義已寫入程式註解與
CHANGELOG、且**尚未**被任何版本化匯出表面消費，則不需要 ADR。

本裁決：
- **追認**免 ADR 先例（PR #73 成本欄位、duration rail）；
- **解釋 ADR-051**：它確實必要 —— 依觸發 1（新來源：transcript）與觸發 2
  （`total_tokens` 語義）—— 而非因加法欄位本身；其「無凍結契約變更」一句在
  envelope 意義上仍為真，其詮釋自此由本 ADR 取代；
- **維持 §7.6 對 #67 的要求成立**：capture step 觸發第 3 條（observations
  白名單擴充）；其 hook 子指令屬 CLI 表面，已由 ADR-050 治理。

### 核准 #67 capture step（slice 3a）

依上述裁決，本 ADR 核准 §7.6 的三項契約觸點：

1. 新 hook 子指令 `auspex hook claude post-tool-use`（kebab-case 依 ADR-050；
   stub-then-swap 接線同其他葉節點）。
2. `provider.turn.completed` 的加法式新欄位 —— §7.3 的五個每回合聚合值：
   `distinct_files_touched`、`total_file_ops`、`repeated_ops`、
   `repeat_rate`（`total_file_ops` = 0 時為 nil）、`max_ops_on_one_file`。
3. `auspex.observations-export.v1` 白名單擴充上述五欄位。

隱私不變量（§7.3/§7.8，重申為約束）：原始檔案路徑**永不以任何形式持久化 ——
含 hash**。路徑在行程記憶體內轉為每回合的不透明序號供計數後即丟棄；只有五個
聚合計數離開行程。工具分類：view = `Read`；modify = `Edit`、`Write`、
`MultiEdit`、`NotebookEdit`。採聚合而非逐工具呼叫事件為刻意設計：高頻事件
型別會與 ADR-046 retention 衝突；既有 `provider.tool.*` EventType 維持不用。

## Consequences（後果）

- Constitution §3 條文不變；本 ADR 記錄其對 payload/匯出表面的權威詮釋。
  日後的加法式 capture 依四觸發測試判斷，不再重複爭論。
- ADD：§33 增加本 ADR 鏡像條目（同一變更內）。其餘 ADD 章節不變；研究文件
  §7 仍為 #67 的實作規格。
- #67 slice 3a 解鎖，與本 ADR 同一變更落地。slice 3b（RiskCombiner 輸入、
  reason code、閾值）持續延後至捕捉資料足以支撐閾值選擇；#68 續受 3b 把關。
- Codex 的 PostToolUse hook 與 managed-stream 工具事件維持範圍外（§7.2），
  需要時另案提出。
