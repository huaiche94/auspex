# ADR-050 — Hook 子指令的 argv 採 kebab-case（將已出貨的 CLI 正式化，取代 ADD 附錄 E.3 的 PascalCase）

> 🌐 [English](0050-hook-subcommand-kebab-case.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

狀態：已接受（Accepted）
日期：2026-07-15
負責人：lead
核准人：repository owner，2026-07-15（issue #61，建議採 Option A）
追蹤：issue #61；於 `internal/cli/doc.go` 與
`docs/implementation/vertical-slice/wave2-analysis/ADR_Recommendations.md` 中自記為 REC-03

## 背景

兩份治理文件以不同的大小寫，稱呼同一個 `auspex hook <provider> <subcommand>`
CLI 呼叫，且此不一致雖被追蹤（REC-03），卻從未由任何 ADR 解決：

- **`docs/design/Auspex_ADD.md` 附錄 E.3**（以及 Codex 的 E.1、加上 §24.3 的範例）
  將子指令 argv 寫成 **PascalCase**——`auspex hook claude UserPromptSubmit`——
  以對齊 Claude Code 自身 wire 層級的 `hook_event_name` 欄位。
- **`agents/runtime.md`** 的 P0 指令清單、**`EXECUTION_DAG.md`** 中
  `claude-provider-06` 的驗證指令，以及 vertical-slice 的 demo 腳本，則寫成
  **kebab-case**——`auspex hook claude user-prompt-submit`。

實際出貨的情況：CLI 實作的是 **kebab-case**。`auspex hook claude --help` 列出
`user-prompt-submit`、`stop`、`stop-failure`、`statusline`（`internal/cli/hook.go`）。
因此程式碼與附錄 E.3 互相矛盾；`integrations/claude/README.md` 一方面沿用已出貨的
kebab-case，一方面標記出此衝突。

依 Constitution §2/§3，凍結文件之間的不一致需要一個決定，而非默默擇一。Constitution
§2 的優先序將 ADD（優先序 2）置於 `agents/*.md` 之上，因此就紙面而言 PascalCase 的
ADD「勝出」——這正是為什麼已出貨的 kebab-case 需要一份 ADR，才能成為正當結果，而不是
默默地推翻一份更高優先序的文件。

**範圍釐清——這裡存在兩種不同的大小寫，只有其中一種是被討論的對象。** Claude Code
（與 Codex）自身的 hook 事件名稱——包含 `hook_event_name`/`hookEventName` payload 欄位，
以及附錄 E.1/E.3 範本中 settings.json 的 hook-matcher key（`"UserPromptSubmit": [ … ]`）
——都是 **provider** 的 wire 格式。它們無論如何都是 PascalCase，不受本決定影響，而且
**必須**維持 PascalCase，否則範本將無法比對成功。本文件只決定 **auspex CLI 自身的
argv**（`auspex hook <provider> <subcommand>`）；它與 provider 事件名稱從不共用同一個
token 位置，因此兩個命名空間不會衝突。

## 決策

`auspex hook <provider> <subcommand>` 的 CLI argv 採 **kebab-case**，且與 provider
無關（`claude` 與 `codex` 一致）：`user-prompt-submit`、`stop`、`stop-failure`、
`statusline`（今日已出貨的四個），以及任何未來由 provider 事件名稱經「全部小寫並在字界
插入連字號」推導出的子指令（例如 `PostToolUseFailure` → `post-tool-use-failure`）。

理由：

1. **符合 Cobra／Unix CLI 慣例。** binary 中其他每一個子指令都是小寫／kebab
   （`daemon start`、`decision allow`、`telemetry export`）。單獨一個 PascalCase 的子指令
   家族，會成為使用者必須額外記住的唯一例外。
2. **變動最小，正式化既有現實。** CLI、`agents/runtime.md`、DAG 驗證指令、demo 腳本，
   以及 `integrations/claude/README.md` 都已使用 kebab-case。唯一的離群者是附錄 E.1/E.3
   與兩處 §24.3 範例。
3. **與 provider wire 格式不衝突。** provider 的 PascalCase 事件名稱存在於不同的命名空間
   （settings.json 的 matcher key 與 `hook_event_name` payload），因此 kebab-case 的 argv
   不會引入任何歧義。

**行動：** `docs/design/Auspex_ADD.md` 中，附錄 E.1（Codex）、附錄 E.3（Claude）與 §24.3
「Internal hooks」範例裡每一處 `auspex hook <provider> <subcommand>` 的 argv，皆更新為
kebab-case。這些同一批範本中的 settings.json hook-matcher **key**（`"SessionStart"`、
`"UserPromptSubmit"`……）維持 PascalCase——它們是 provider 的事件名稱，不是 auspex 的 argv。
這是讓優先序 2 的文件被更正以對齊已出貨契約：ADR 正是那個經授權的機制（Constitution §3），
讓變動較小、較符合慣例的答案得以勝過紙面上的優先序。

## 影響

- 附錄 E.1/E.3 的 hook 安裝範本現在可以直接貼上、對著已出貨的 CLI 執行（先前每一個
  `command` argv 都指向一個不存在的子指令）。
- `internal/cli/doc.go` 的 REC-03 註記由「已追蹤但尚未由 ADR 解決」改為「由 ADR-050 解決」。
  無程式碼變更——CLI 早已以 kebab-case 出貨，本 ADR 只是將其正式化。
- 此慣例約束未來的子指令：一個以 `auspex hook` 子指令形式呈現的新 provider 事件，其拼寫
  採 kebab-case，即使它的 `hook_event_name` payload 值仍維持 PascalCase。
- `ADR_Recommendations.md` 的 REC-03（一份凍結的 wave2-analysis 紀錄，ADR-045／ADR-049）
  **不**被編輯；本 ADR 即為其解決方案，並由 `internal/cli/doc.go` 交叉引用。
- 不涉及任何凍結契約變更：hook 子指令 argv 是 CLI 介面，不是凍結的
  `internal/app/ports.go` port，也不是 `pkg/protocol` schema。
