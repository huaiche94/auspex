# testdata/ — 跨套件測試 fixture

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

供多個 `internal/` 套件測試共用的 fixture。依
[ADR-049](../docs/adr/0049-docs-reorg-bilingual.md) §Decision 4，葉層級
的 fixture 目錄刻意**不**設置自己的 `README.md`（`repositories/` 的那份
是政策施行前就存在的例外）——fixture 內容對測試而言是關鍵依據
（load-bearing），因此本檔案負責記載整個子樹。請勿隨意新增、重新命名或
編輯 fixture 檔案：例如
`internal/telemetry/claude/fixture_suite_test.go` 的隱私閘門（privacy
gate）內嵌了逐字複製自 provider-event fixture 的 needle 字串，一旦兩者
不一致就會大聲失敗。

## 子樹

- `checkpoints/state/` — `sample-manifest.json` 由
  `internal/statecheckpoint/serialize_test.go` 讀取；可透過
  `AUSPEX_GENERATE_FIXTURES=1`（`fixture_gen_test.go`）重新產生。
  `add-section-18-*.md` 這些檔案是**測試 fixture，不是文件**（絕不
  翻譯——ADR-049 §Decision 5）：作為 `internal/artifacts` 標題／程式碼
  區塊（fence）檢查的 artifact-validator 輸入。
- `checkpoints/repository/` — `sample-manifest.json`，一份針對真實暫存
  repository 執行實際 Capture 後產生的 Repository Checkpoint manifest，
  並已對照 [`../schemas/`](../schemas/README.md) 中的 schema 驗證過。
- `progress-trees/` — `sample-task.json`，轉錄自 `Auspex_ADD.md` 附錄 A
  （該檔案位於
  [`../docs/design/Auspex_ADD.md`](../docs/design/Auspex_ADD.md)），並已
  對照 `../schemas/progress-tree.schema.json` 驗證過。
- `provider-events/claude/` — 原始的 Claude Code hook payload
  （`statusline/`、`stop/`、`stopfailure/`、`userpromptsubmit/`），
  涵蓋正常／格式錯誤／欄位缺漏／未知欄位等變體，另附 `.golden.json`
  回應；由 `internal/hooks/claude`、`internal/telemetry/claude`、
  `internal/providers/claude`、`internal/cli` 的測試套件所使用。
- `provider-events/codex/` — Codex CLI fixture：hook payload
  （`sessionstart/`、`userpromptsubmit/`、`stop/`）、session rollout
  JSONL（`rollout/`），以及 `exec/` — 依 ADD §21.8 與 v0.144.4 執行檔
  內嵌事件 schema（`thread.started`／`turn.started`／`item.*`／
  `turn.completed`／`turn.failed`／`error`）手寫合成的
  `codex exec --json` JSONL 事件串流；**並非**任何真實 session 的錄製
  ——數值皆為便於斷言而挑選的 fixture 值，`FIXTURE-*` 佔位文字僅供
  隱私測試證明不被保留。由 `internal/hooks/codex`、
  `internal/telemetry/codex`、`internal/managed`、
  `internal/integrationtest` 所使用。
- `repositories/` — 供 `internal/repocheckpoint`／`internal/gitx` 使用的
  repository *內容* fixture；詳見
  [`repositories/README.md`](repositories/README.md)（未內含真正的
  `.git` 目錄；測試會另行建立暫存 repository）。
