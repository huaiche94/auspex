# internal/managed/ — `auspex run` 背後受管理（managed）的一次性（one-shot）執行器

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

`auspex run` 不含 CLI 的核心邏輯（issue #8 的 MVP 增量；ADD §8.1——
[`docs/design/Auspex_ADD.md`](../../docs/design/Auspex_ADD.md)；由 issue #9 M7 Phase 1
延伸支援 codex，ADD §21.8；再由 issue #122 加入 M10 Graceful Pause 自動觸發器）。
套件合約與完整的刻意排除清單請見 [`doc.go`](doc.go)。只負責三件事加上一個可選觸發器，
僅此而已：

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
4. **Graceful Pause 自動觸發器（可選；M10，issue #122）** —— 當 `Runner.Pause` 啟用時，
   [`pausedrive.go`](pausedrive.go) 會在 provider 執行期間，依 ADD §20.3 的 5 秒心跳觀測
   該 session 的 quota runway，把每個 forecast 樣本餵入 `internal/pause` 的
   debounce/hysteresis 觸發器（ADD §17.6/§20.2），並在觸發時端到端驅動既有的凍結暫停
   生命週期：request → safe point → checkpoints → provider 中斷 → sleeping 並排入可持久化
   的 wake job。中斷機制是依路徑選擇的 `providerStopper`：對已啟動的 provider 行程採
   graceful SIGINT、逾時升級為 kill；在 codex App Server 路徑上則是 ADD §21.6 的協定層級
   `turn/interrupt`、逾時升級為連線拆除（connection teardown），interrupted 終態通知由
   run loop 本身觀測（issue #9 slice C，[`appserver.go`](appserver.go)）。僅限 managed
   模式——native-hook 模式維持只觀測不行動，因為 hook 無法中斷 provider 的 turn
   （`internal/orchestrator/runwaydrive.go` 已文件化的限制）。校準的 0.80 觸發路徑受
   calibration gate 管制（M13 之前沒有任何 forecast 是 calibrated），因此今日 production
   只有 ADD §17.6 的 `emergency_uncalibrated` 路徑可能觸發；觸發失敗只會記錄並讓 run
   繼續（fail toward continuing work，永不朝終止 session 的方向失效）。

支援的 provider 即 `provider.go` 規格表的各列：`ProviderClaude` 與 `ProviderCodex`。CLI
那一半的程式碼位於 [`internal/cli/run.go`](../cli/run.go)。`testdata/` 存放 claude 串流
固定資料（fixture）；codex exec 的 fixture 與其他 codex payload fixture 同樣位於
[`testdata/provider-events/codex/exec`](../../testdata/README.md)。

codex App Server 是 ADR-013 的**主要**managed 路徑（[`appserver.go`](appserver.go)，
issue #9 Phase 2 slice B），`codex exec --json` 為明示的 fallback；自動暫停觸發器在此路徑
以協定層級中斷（`turn/interrupt`，見上方 slice C）。

尚未在此實作（屬於 issue #8／#9 之後的增量）：受管理的 shell 模式——`auspex shell`，ADD §8.2，
排定為 ADD 里程碑 M11——以及 daemon 整合、經驗證的自動 resume（含 §21.6 步驟 8 的
`thread/resume` + `turn/start` bootstrap；`thread_id` 定位器已持久化於
`provider.session.started` 事件），還有逐訊息（per-message）的即時用量建模。未被中斷的
行程會執行到結束；context 取消（cancellation）則會將其終止。
