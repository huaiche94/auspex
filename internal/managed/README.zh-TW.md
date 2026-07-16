# internal/managed/ — `auspex run` 背後受管理（managed）的一次性（one-shot）執行器

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

`auspex run` 不含 CLI 的核心邏輯（issue #8 的 MVP 增量；ADD §8.1——
[`docs/design/Auspex_ADD.md`](../../docs/design/Auspex_ADD.md)；由 issue #9 M7 Phase 1
延伸支援 codex，ADD §21.8）。套件合約與完整的刻意排除清單請見 [`doc.go`](doc.go)。
只負責三件事，僅此而已：

1. **Pre-prompt 關卡** —— 與 `UserPromptSubmit` hook 所執行的正式（production）evaluate/decide
   路徑完全相同，透過 [`internal/orchestrator`](../orchestrator/README.md) 的
   `EvaluateManagedPrompt`，在 provider 行程存在之前就先套用：若決策為 BLOCK，則 provider
   根本不會被啟動（spawn）。
2. **Provider 子行程生命週期** —— [`run.go`](run.go) 的 `Runner.Run` 依
   [`provider.go`](provider.go) 的規格表、只以 argv 陣列方式啟動 provider（絕不組成 shell
   字串）：`claude -p <prompt> --output-format stream-json --verbose` 或
   `codex exec --json <prompt>`。[`stream.go`](stream.go)／[`codexstream.go`](codexstream.go)
   以防禦性方式解析輸出，並採 fail-open（失效放行）策略——無法辨識的行只會計入略過計數，
   絕不會造成當機。依 Constitution §7 的隱私規則，結果／訊息內容從不保留（只保留長度）。
3. **結果歸因（outcome attribution）** —— 終態結果會透過各 provider 自己的 telemetry 套件
   （`internal/telemetry/claude`、`internal/telemetry/codex`）正規化為凍結的事件信封，並以
   best-effort（盡力而為）方式，透過與 hook 路徑相同的介接點持久化，並以單一 `TurnID`
   作為關聯鍵。

支援的 provider 即 `provider.go` 規格表的各列：`ProviderClaude` 與 `ProviderCodex`。CLI
那一半的程式碼位於 [`internal/cli/run.go`](../cli/run.go)。`testdata/` 存放 claude 串流
固定資料（fixture）；codex exec 的 fixture 與其他 codex payload fixture 同樣位於
[`testdata/provider-events/codex/exec`](../../testdata/README.md)。

尚未在此實作（屬於 issue #8／#9 之後的增量）：受管理的 shell 模式——`auspex shell`，ADD §8.2，
排定為 ADD 里程碑 M11——以及執行過程中的 turn 中斷／安全點暫停、daemon／事件串流／app-server
整合、經驗證的自動 resume（含 `codex exec resume`），還有逐訊息（per-message）的即時用量建模。
已啟動的行程會執行到結束；context 取消（cancellation）則會將其終止。
