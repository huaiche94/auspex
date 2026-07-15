# Auspex

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

**Auspex 是為 AI 編碼代理（AI coding agents）打造的本地優先（local-first）預測式執行期守門系統（predictive runtime guard）。**
在每次與 Claude Code 這類 provider（供應商）互動的回合（turn）開始前及進行中，它會估算這個回合的成本——範疇（scope）、token／配額消耗、完成可能性、爆炸半徑（blast-radius）風險——然後套用政策（policy），決定放行、警告、要求先建立檢查點、拆分、優雅暫停（gracefully pause），或直接阻擋這個回合。

它回答的問題與 checkpoint／resume／memory 類工具不同：不是「我們該如何繼續？」，而是「這個回合我們一開始就該執行嗎？」（拉丁文 *auspex* 指的是在一件事展開之前解讀徵兆、裁定是否可以進行的占卜官。）

## 一次 session 就能看到的效果

一旦接上 Claude Code（見〔快速開始〕(#快速開始)），你送出的每一個 prompt 在執行前都會先被評估。以下是本專案自身某次真實開發 session 的實際輸出——Auspex 每天都在對自己做 dogfooding：

```text
Auspex forecast (uncalibrated estimate — scores are not probabilities):
  scope: ~1–4 files changed, ~30–180 lines, ~2–6 files read (P50–P90)
  tokens: P50 3782 / P80 5732 / P90 7564
  cost: ~$0.04–$0.38 USD (estimate)
  context: P90 ~3% of window (projected)
  risk: 0.50/1.00 overall — QUOTA_UNKNOWN, PREDICTION_COLD_START
  policy: WARN
```

這次評估的結果會餵給一個政策引擎（policy engine），引擎具備**八種凍結（frozen）動作**（`RUN`、`WARN`、`REQUIRE_CONFIRMATION`、`CHECKPOINT_AND_RUN`、`SPLIT`、`PAUSE`、`PAUSE_AND_AUTO_RESUME`、`BLOCK`）。這個決策會透過 hook 回應（response）回傳給 agent——被允許的 prompt 會照常通過，被阻擋的 prompt 則會附帶一個機器可讀（machine-readable）的原因，agent 本身可以根據它採取行動。除了逐一 prompt 的把關之外，Auspex 還維護：

- **一棵 Progress Tree（進度樹）**——具規範性、持久性的任務狀態（canonical, durable task state）。一個節點在沒有經過驗證器（validator）檢核的證據（檔案、資料庫紀錄、checksum，或 Git 快照）之前不得標記為完成；「agent 自己說已經完成」永遠不算數。
- **State（狀態）＋ repository（儲存庫）checkpoint**——每次節點完成都會原子性（atomically）地建立一個 State Checkpoint；repository checkpoint 則會擷取 worktree 的內容（並做過機密資訊遮蔽／redaction），但絕不會提交（commit）你的分支。
- **Graceful Pause（優雅暫停）**——當配額視窗（quota window）即將用盡時，Auspex 會建立檢查點、在安全點（safe point）中斷，並在 SQLite 中持久化一個到期喚醒工作（wake job）。daemon（`auspex daemon`）會在無人值守（unattended）的情況下執行到期的 wake job；恢復（resume）前會重新驗證 repository、配額、session 與授權（authorization）。

一切都在本機執行：一個靜態 Go 二進位檔、一個位於你作業系統使用者資料目錄下的 SQLite 資料庫，沒有任何雲端服務。原始的 prompt 文字與工具輸出預設永遠不會被持久化——只有萃取出的特徵（extracted features）會被保存。

<a id="quick-start"></a>
## 快速開始（Quick start）

需要 Go 1.26.5（版本已固定於 `go.mod`）；不需要 CGO，也不需要任何外部服務。

```bash
go build -o auspex ./cmd/auspex
./auspex version
./auspex doctor      # creates + migrates the SQLite DB, then verifies it
```

建置完成後立即執行 `doctor` 就有意義：第一次執行會在作業系統使用者資料目錄下建立資料庫（macOS：`~/Library/Application Support/auspex/`；Linux：`$XDG_DATA_HOME/auspex/`），並針對每一項檢查（`database`、`config`……）回報個別的檢查狀態。

若要把它接上 Claude Code，請依照
[`integrations/claude/`](integrations/claude/README.md) 的說明操作：裡面提供了
`hooks.json`／`plugin.json` 範例，會將 Claude Code 的
UserPromptSubmit / Stop / StopFailure / statusline 事件導向
`auspex hook claude <event>`，另外還有 `auspex init` 可以註冊目前的
repository。這些 hook 會**fail open（失效開放）**——Auspex 發生 crash 絕不會阻擋你的
session；直接執行 `auspex evaluate` 即可看到真正的錯誤訊息。

### 指令樹（The command tree）

```text
auspex evaluate               estimate a prompt before running it (--json)
auspex decision allow|deny    consume a one-time authorization (replays rejected)
auspex checkpoint create      state + repository checkpoint (never commits your branch)
auspex progress ...           inspect the Progress Tree; evidence-gated completion
auspex pause request|cancel   safe-point pause with a durable wake job
auspex resume                 re-verified resume
auspex scheduler run-once     execute due wake jobs without the daemon
auspex daemon ...             background daemon + authenticated loopback HTTP API
auspex run ...                run a provider one-shot prompt under the managed gate
auspex init                   register the current repository/session
auspex status | doctor        session/checkpoint/pause state; environment health
auspex gc                     tiered telemetry retention (90-day default, ADR-046)
auspex export                 de-identified datasets for offline analysis
auspex hook claude <event>    the four hook entrypoints Claude Code calls
```

每一個指令都會在 stdout 上輸出具 schema 版本（schema-versioned）的 JSON（`--json`，FR-160），並以單一種型別化（typed）的錯誤格式回報失敗，讓人類與 agent 都能消化這個輸出：

```json
{"schema_version":"auspex.error.v1","code":"validation",
 "message":"pause request: --reason must be one of \"calibrated_hit_probability\", \"emergency_uncalibrated\"",
 "retryable":false,"details":{"reason":"quota_hit"}}
```

VS Code 隨附延伸模組（companion extension，[`vscode/`](vscode/README.md)）會顯示
daemon 狀態、wake-job 佇列，以及排程恢復（scheduled resume）的內嵌取消按鈕；在
marketplace 發布者（publisher）完成註冊之前
（[#18](https://github.com/huaiche94/auspex/issues/18)），這個延伸模組僅能從原始碼或本機打包的 VSIX 使用。

## 專案現況

完整的 vertical slice（垂直切片）——涵蓋七個角色（role）共 85/85 個 DAG 節點，從
Bootstrap 一路到 Stage-5 整合閘門（integration gate）——已經整合進 `main`，緊接著是
slice 後續待辦清單（post-slice backlog）：具授權（authenticated）loopback API 的
daemon（[#7](https://github.com/huaiche94/auspex/issues/7)）、native-hook
session bootstrap（[#17](https://github.com/huaiche94/auspex/issues/17)）、
逐一 prompt 的預測（forecast）呈現介面
（[#14](https://github.com/huaiche94/auspex/issues/14)）、分層遙測（telemetry）
資料保留（ADR-046）、真正的 repository-checkpoint 還原
（[#6](https://github.com/huaiche94/auspex/issues/6)），以及 VS Code
companion 延伸模組的 MVP。本 repository 自身的 Claude Code session 每天都會把遙測資料餵給本機的一個 Auspex。

**誠實的但書：**每一個預測目前仍然是由 cold-start（冷啟動）規則產生，而非經過校準（calibrated）的模型。分數不是機率，並且在每一個呈現介面上都明確標示為如此（Constitution §7 第 7 條）。token 預測目前尤其幾乎不隨 prompt 內容而變動
（[#42](https://github.com/huaiche94/auspex/issues/42)）；根據累積的真實遙測資料進行校準是 M13 里程碑
（[#11](https://github.com/huaiche94/auspex/issues/11)）。外部研究是支撐而非推翻這個立場：一份對八個 frontier agent 在 SWE-bench 上的研究（Bai 等，[arXiv:2604.22750](https://arxiv.org/abs/2604.22750)，2026）發現，同一任務的不同執行 token 用量可差到 30×，而且模型對自身成本的預測只有弱相關（correlation ≤ 0.39、且系統性偏低）——因此粗略、未校準的區間是誠實的上限，而非暫時的權宜。所以 Auspex 的價值在於它所把關的**決策**——checkpoint、pause、resume、block——而不在於它印出那個數字的精確度。

尚待完成的路線圖里程碑：Codex provider 轉接器（adapter）（M7/M8，
[#9](https://github.com/huaiche94/auspex/issues/9)）、受管的 one-shot 與
shell 模式（M11，[#8](https://github.com/huaiche94/auspex/issues/8)）、
完整的 VS Code companion（M12，
[#10](https://github.com/huaiche94/auspex/issues/10)）、校準（calibration）
pipeline（M13，#11）。
[issue tracker](https://github.com/huaiche94/auspex/issues) 是即時更新的待辦清單。所有工作都受里程碑閘控（milestone-gated）：任何功能都不會在其里程碑之前被實作（`docs/design/Auspex_ADD.md` §31）。

從上述 Bai 等論文提煉出、以研究為依據的新增項目——cache-aware 四類成本模型、以*觀測*而非預測抓出原地打轉 turn 的重複檔案操作 risk 訊號，以及 phase-aware 條件式預測——已作為路線圖筆記（僅為外部先驗，絕非擬合數字）記錄於
[`docs/backlog/token-cost-prediction-research.md`](docs/backlog/token-cost-prediction-research.md)。

## 驗證一項變更

以下是本機 pre-commit 的基準線，也正是 CI
（[`.github/workflows/ci.yml`](.github/workflows/ci.yml)）在 ubuntu-latest、
macos-latest、windows-latest 上實際執行的內容——三者皆為硬性阻擋（hard-blocking）：

```bash
gofmt -l . && go build ./... && go vet ./...
go test ./... -race
golangci-lint run ./...
```

## Repository 結構

```text
cmd/auspex/           CLI entrypoint (thin main; wiring in internal/app)
internal/             application core, domain model, adapters (Go)
pkg/protocol/v1/      public wire protocol types
integrations/claude/  Claude Code hook wiring (hooks.json / plugin.json)
vscode/               VS Code companion extension (TypeScript)
schemas/              JSON Schemas for the frozen wire shapes
research/             offline Python analysis — never a runtime dependency
agents/               role definitions from the multi-agent build
docs/                 design docs, ADRs, decision log, build history
testdata/             cross-package fixtures (checkpoints, provider events)
```

每個資料夾都有自己的 `README.md` 介紹該處的內容，而且每一份文件都有一份繁體中文對照文件
（`<name>.zh-TW.md`，ADR-049）。以原著語言為規範文本：除了以繁體中文撰寫的
`docs/design/Auspex_ADD.md` 與 `docs/DECISION_LOG.md` 之外，其餘皆以英文版為準。

## 接下來該讀什麼

| 你想要…… | 請讀 |
|---|---|
| 了解架構 | [`docs/design/Auspex_ADD.md`](docs/design/Auspex_ADD.md)——唯一的權威架構／需求規格文件，以繁體中文撰寫（原文即規範文本）；僅能由 [`docs/adr/`](docs/adr/) 下的 ADR 修訂 |
| 貢獻（無論人類或 agent） | [`CONSTITUTION.md`](CONSTITUTION.md)（流程權威）→ [`CONTRIBUTING.md`](CONTRIBUTING.md) → [`AGENTS.md`](AGENTS.md) |
| 了解預測器（predictor）的運作方式 | [`docs/design/Auspex_Predictor_Design_Supplement.md`](docs/design/Auspex_Predictor_Design_Supplement.md) ＋ [`internal/predictor/`](internal/predictor/README.md) |
| 追溯這個 repository 是如何建置出來的 | [`docs/implementation/vertical-slice/`](docs/implementation/vertical-slice/README.md)——執行 DAG、逐波（wave-by-wave）整合歷史、各角色的進度紀錄 |
| 重用這套多代理（multi-agent）流程 | [`docs/methodology/`](docs/methodology/README.md) |
| 瀏覽所有文件 | [`docs/README.md`](docs/README.md) |

`CONSTITUTION.md` 治理流程；`docs/design/Auspex_ADD.md` 治理架構。當任何其他文件與這兩者不一致時，以這兩者為準（Constitution §1–§2）。

## 技術棧與授權

Go 1.26.5 單一靜態二進位檔搭配 SQLite（WAL）．僅在 VS Code 延伸模組中使用
TypeScript．Python 3.12+ 僅供離線研究使用．
Apache-2.0（詳見 [`LICENSE`](LICENSE)、[`NOTICE`](NOTICE)）。
