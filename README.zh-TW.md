# Auspex

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

## 為什麼需要 Auspex？agent 不是已經會自己管理 context 了嗎？

確實會——而且愈來愈厲害。Claude Code 內建分層壓縮（layered compaction）：龐大的工具輸出會提早卸載（offload）到磁碟、對話在接近 context 上限時會自動摘要（auto-summarize），事後再把最近的檔案與 todo 重新補水（rehydrate）回來，讓 session 維持動能。Codex CLI 則透過專用端點（endpoint）在伺服器端做壓縮，並在每一輪之後重新讀取最近編輯過的檔案。兩家 vendor 現在都已在各自的 API 中直接開放壓縮能力。「回收一整個 context 視窗」已經是一個被解決、而且正在快速商品化（commoditizing）的問題。

Auspex 並不與上述任何一項競爭。它涵蓋的是原生壓縮做不到的三件事：

**1. 配額不等於 context。**
壓縮能讓 session 撐過 context 上限，但當你的用量視窗（usage window）在凌晨兩點見底時，它什麼也做不了。session 就這樣死掉，而在你醒來之前的每一個小時都被浪費掉。Auspex 的 Graceful Pause（優雅暫停）會盯著配額剩餘跑道（quota runway），在撞牆之前找到安全的停止點、對進度建立檢查點，並把一個喚醒工作（wake task）寫進本機 SQLite。daemon 會重新驗證配額與 repo 狀態，然後恢復執行——過程中還能挺過 crash 與重開機。原生壓縮對此毫無對策；這正是 Auspex 的主場。

**2. 壓縮是有損的，而且沒有人稽核結果。**
每一次摘要都會丟掉先前回合累積下來的細節——這是壓縮的本質，不是可以修掉的 bug。原生機制信任它自己產生的摘要；沒有任何獨立檢查會去驗證 agent 在壓縮之後是否仍走在正軌上。Auspex 不試圖把摘要做到完美，而是讓「損失」變得可以存活（survivable）：State Checkpointing（狀態存證）要求在任何工作單元被標記為完成之前，必須提出可驗證的證據——測試產物、checksum、Git 快照。一個在壓縮後開始偏離（drift）的 agent 會卡在證據閘門（evidence gate）前，而不是默默把 regression 送出去。這道閘門存在於 context 視窗之外，所以它永遠不會遺忘。

**3. session 會結束，但工作不該結束。**
原生的 context 管理與行程（process）同生共死。Auspex 把進度樹（progress tree）、喚醒工作與決策持久化在 SQLite 中，因此一個被中斷的執行——配額耗盡、crash、重開機——會從中斷處接續，而不是從頭來過。

**一句話版本**

Auspex 不做壓縮，它監督壓縮。

agent 負責翻頁（page-turn）；Auspex 負責確保翻頁之前狀態已經固化（`CHECKPOINT_AND_RUN`）、翻頁之後的輸出仍然通得過證據閘門，以及讓配額中斷變成暫停、而不是死掉的 session。這讓 Auspex 與各家 vendor 的路線圖互補、而非與之競速：原生壓縮做得愈好，人們愈敢放手讓它無人值守（unattended）跑更長的任務——而一層監督（supervision）機制也就愈重要。

**Auspex 是為 AI 編碼代理（AI coding agents）打造的本地優先（local-first）飛行記錄器（flight recorder）兼執行期守門系統（runtime guard）。**
它精確量測你的 agent 花掉的一切——每一類 token、每一塊錢、每一個配額視窗，逐回合、也逐日——盯著你朝配額牆逼近的燃燒速率（burn rate），並在高風險的時刻把關：大變更之前先建立檢查點、撞牆之前先暫停、阻擋政策所禁止的事、帶著完好無缺的證據恢復執行。

它也做預測——但很謹慎，而且只在預測真正行得通的地方。我們先把預測堆疊（prediction stack）建了起來，然後拿真實用量去量測它；資料毫不含糊（見下方[誠實的數字](#what-auspex-measures-vs-what-it-predicts)）。單一回合會花多少，事前幾乎不可知；而一個 *session* 朝它的配額牆燒得多快，卻是可知的，因為彙總（aggregation）會把雜訊平均掉。所以 Auspex 以它能量測、能外推（extrapolate）的東西打頭陣，逐回合估計則只以寬幅、有標示的參考區間（reference band）印出。

（拉丁文 *auspex* 指的是在一件事展開之前解讀徵兆的占卜官。我們這位占卜官已經從自己的遙測資料中學會了哪些徵兆真正可讀——而且會照實說。）

## 一次 session 就能看到的效果

一旦接上 Claude Code 或 Codex CLI（見[快速開始](#quick-start)），你每天盯著看的那些呈現介面，都以**量測到的現實**（measured reality）打頭陣——以下都是本 repository 自身真實開發 session 的實際輸出；Auspex 每天都在對自己做 dogfooding：

**狀態列（status line）**（Claude Code statusline，或供 tmux 使用的 `auspex hook codex status`）——最吃緊的配額視窗、距牆的剩餘跑道（runway）、今日花費與步調（pace）：

```text
ax» Opus 4.1 │ ◷ 5h ~62% (resets 18:00) │ ⏳ runway ~38m │ today $62.19 · pace → ~$312 by 24:00 │ context [████··] 21.9% │ ✓ RUN
```

**每週之鏡（weekly mirror）**——`auspex report --window 7d`：各類 token 的精確總量、按模型 × effort 拆分的花費、cache 衛生（cache hygiene）、配額事故（quota incidents），以及你最貴的五個回合。這正是週五五分鐘自我檢視（self-review）的工具：*那五個回合值得它們的價錢嗎？例行工作是不是跑在昂貴的模型上？哪些 session 在折騰（thrash）cache？*

```text
turns 228 · sessions 22 · cost $1,189.66 (205/228 attributed; the rest say unknown, not $0)
tokens: fresh 158k / cache read 167.5M / cache creation 4.1M / output 746k
claude opus/xhigh 141 turns $648.53 · fable/xhigh 71 turns $528.42
cache read/fresh ratio 1057.9× · 2 sessions flagged for creation churn
top turn: $43.94
```

**回合前閘門（pre-turn gate）**——每一個 prompt 在執行前仍然會先被評估，而估計值會以它誠實的本來面目印出：一條餵給政策決策的寬幅、未校準參考區間，而不是一個承諾：

```text
Auspex forecast (uncalibrated estimate — scores are not probabilities):
  scope: ~1–4 files changed, ~30–180 lines (P50–P90)
  tokens: P50 3782 / P90 7564 · cost: ~$0.04–$0.38 (reference band)
  risk: 0.50/1.00 — QUOTA_UNKNOWN, PREDICTION_COLD_START
  policy: WARN
```

這次評估的結果會餵給一個政策引擎（policy engine），引擎具備**八種凍結（frozen）動作**（`RUN`、`WARN`、`REQUIRE_CONFIRMATION`、`CHECKPOINT_AND_RUN`、`SPLIT`、`PAUSE`、`PAUSE_AND_AUTO_RESUME`、`BLOCK`）。這個決策會透過 hook 回應（response）回傳給 agent——被允許的 prompt 會照常通過，被阻擋的 prompt 則會附帶一個機器可讀（machine-readable）的原因，agent 本身可以根據它採取行動。除了逐一 prompt 的把關之外，Auspex 還維護：

- **一棵 Progress Tree（進度樹）**——具規範性、持久性的任務狀態（canonical, durable task state）。一個節點在沒有經過驗證器（validator）檢核的證據（檔案、資料庫紀錄、checksum，或 Git 快照）之前不得標記為完成；「agent 自己說已經完成」永遠不算數。
- **State（狀態）＋ repository（儲存庫）checkpoint**——每次節點完成都會原子性（atomically）地建立一個 State Checkpoint；repository checkpoint 則會擷取 worktree 的內容（並做過機密資訊遮蔽／redaction），但絕不會提交（commit）你的分支。
- **Graceful Pause（優雅暫停）**——當配額視窗（quota window）即將用盡時，Auspex 會建立檢查點、在安全點（safe point）中斷，並在 SQLite 中持久化一個到期喚醒工作（wake job）。daemon（`auspex daemon`）會在無人值守（unattended）的情況下執行到期的 wake job；恢復（resume）前會重新驗證 repository、配額、session 與授權（authorization）。

一切都在本機執行：一個靜態 Go 二進位檔、一個位於你作業系統使用者資料目錄下的 SQLite 資料庫，沒有任何雲端服務。原始的 prompt 文字與工具輸出預設永遠不會被持久化——只有萃取出的特徵（extracted features）與計數（counts）會被保存；檔案路徑則無論任何形式——包括雜湊（hash）在內——都絕不保存（ADR-051／052）。

<a id="what-auspex-measures-vs-what-it-predicts"></a>
## Auspex 量測什麼 vs. 預測什麼

這份誠實的切分來自我們自己的實地資料，也與外部研究一致（Bai 等，
[arXiv:2604.22750](https://arxiv.org/abs/2604.22750)：同一任務的不同執行 token 用量可差到 30×；模型對自身成本的預測相關性 ≤ 0.39）：

| 呈現介面 | 性質 | 可信度 |
|---|---|---|
| 逐回合 token（四類）、成本、時長 | 於 Stop 時**量測**（transcript／rollout） | 精確——可放心引用 |
| 配額視窗（5h／每週）、context % | 逐回合**量測** | 精確 |
| 今日花費與步調（pace） | 由量測值**彙總**（aggregated）而來 | 是算術，不是建模 |
| 檔案操作彙總（重複率——「是否在原地打轉？」） | 逐回合**觀測** | 是關於該回合的事實，不是猜測 |
| session 距配額牆的剩餘跑道（runway） | 由燃燒速率**外推** | 唯一可駕馭（tractable）的預測——彙總會把逐回合的雜訊平均掉；校準（M13）首先瞄準這裡 |
| 逐回合 scope／token／成本估計 | **預測** | 一條寬幅參考區間，且標示為未校準——我們的第一批實地資料顯示 cold-start 成本在中位數偏差約 7–9×（[#90](https://github.com/huaiche94/auspex/issues/90)）；把它當作脈絡（context）看待，永遠不要當作可以據以規劃的數字 |

這樣的排序是一個產品決策，不是偶然
（[#90](https://github.com/huaiche94/auspex/issues/90)）：Auspex 把電錶、油量表與原地打轉偵測器放在最前面；水晶球放在後排，並清楚標示。

<a id="quick-start"></a>
## 快速開始（Quick start）

需要 Go 1.26.5（版本已固定於 `go.mod`）；不需要 CGO，也不需要任何外部服務。

```bash
go build -o auspex ./cmd/auspex
./auspex version
./auspex doctor      # creates + migrates the SQLite DB, then verifies it
```

建置完成後立即執行 `doctor` 就有意義：第一次執行會在作業系統使用者資料目錄下建立資料庫（macOS：`~/Library/Application Support/auspex/`；Linux：`$XDG_DATA_HOME/auspex/`），並針對每一項檢查（`database`、`config`、擷取健康狀態……）回報個別的檢查狀態——其中包括 **token 擷取覆蓋率（token-capture coverage）**，讓悄悄壞掉的擷取大聲失敗，而不是讓你的資料默默斷炊。

若要把它接上 Claude Code，請依照
[`integrations/claude/`](integrations/claude/README.md) 的說明操作：裡面提供了
`hooks.json`／`plugin.json` 範例，會將 Claude Code 的
UserPromptSubmit / Stop / StopFailure / PostToolUse / statusline 事件導向
`auspex hook claude <event>`，另外還有 `auspex init` 可以註冊目前的
repository。Codex CLI 也以同樣的方式接上：
[`integrations/codex/hooks.json`](integrations/codex/hooks.json) 會將它的
SessionStart / UserPromptSubmit / Stop 事件導向
`auspex hook codex <event>`（hook 的 argv 採 kebab-case，ADR-050）。兩者的
Stop 端擷取都會記錄精確的逐回合（per-turn）token 用量——完整四類 token，
Claude 來自 session transcript（ADR-051）、Codex 來自 session rollout
JSONL——僅數字，絕不保存 prompt 或輸出文字。這些 hook
會**fail open（失效開放）**——Auspex 發生 crash 絕不會阻擋你的
session；直接執行 `auspex evaluate` 即可看到真正的錯誤訊息。

### 指令樹（The command tree）

```text
auspex report                 your usage, mirrored back: spend, tokens by class,
                              model×effort split, cache hygiene, quota incidents,
                              costliest turns (--window 7d, --json)
auspex evaluate               estimate a prompt before running it (--json)
auspex decision allow|deny    consume a one-time authorization (replays rejected)
auspex checkpoint create      state + repository checkpoint (never commits your branch)
auspex progress ...           inspect the Progress Tree; evidence-gated completion
auspex pause request|cancel   safe-point pause with a durable wake job
auspex resume                 re-verified resume
auspex scheduler run-once     execute due wake jobs without the daemon
auspex daemon ...             background daemon + authenticated loopback HTTP API
auspex run ...                one-shot prompt under the managed gate (claude|codex)
auspex init                   register the current repository/session
auspex status | doctor        session/checkpoint/pause state; capture health
auspex gc                     tiered telemetry retention (90-day default, ADR-046)
auspex export                 de-identified datasets for offline analysis
auspex hook claude <event>    the hook entrypoints Claude Code calls
auspex hook codex <event>     the Codex CLI hook entrypoints (same gate)
auspex hook codex status      stdin-less status line for tmux/scripts (--cwd DIR)
```

每一個指令都會在 stdout 上輸出具 schema 版本（schema-versioned）的 JSON（`--json`，FR-160），並以單一種型別化（typed）的錯誤格式回報失敗，讓人類與 agent 都能消化這個輸出：

```json
{"schema_version":"auspex.error.v1","code":"validation",
 "message":"pause request: --reason must be one of \"calibrated_hit_probability\", \"emergency_uncalibrated\"",
 "retryable":false,"details":{"reason":"quota_hit"}}
```

VS Code 隨附延伸模組（companion extension，[`vscode/`](vscode/README.md)）會呈現
daemon 的逐 session 狀態視圖——風險（risk）、剩餘跑道（runway）、配額新鮮度（quota
freshness）、進度、checkpoint 與暫停狀態；未知（unknown）就呈現為「未知」，絕不以捏造的零代替——外加
wake-job 佇列與排程恢復（scheduled resume）的內嵌取消按鈕；在
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
companion 延伸模組（[#10](https://github.com/huaiche94/auspex/issues/10)）——後者現在由
daemon 的 session-status API（`GET /v1/session/status`，
`auspex.daemon.session_status.v1`）供應資料。Codex CLI 已是第一級（first-class）的第二個
provider（[#9](https://github.com/huaiche94/auspex/issues/9)）：native
hook（`auspex hook codex <event>`）與受管
one-shot（`auspex run --provider codex`，經由 `codex exec --json`）皆已出貨；#9
剩下的是 M7 Phase 2 的尾巴——app-server 訂閱、graceful
interrupt、`codex exec resume`。native-hook session 能為兩個
provider 擷取精確的逐回合（per-turn）token 用量；由這些真實配額遙測算出的即時剩餘跑道（runway）預測，會觸發
policy 的 runway reason code 並顯示於 statusline（`⏳ runway ~Ns`、今日花費與步調）；而逐回合的檔案操作彙總（ADR-052，
[#67](https://github.com/huaiche94/auspex/issues/67)）也正持續累積，朝原地打轉（spin）偵測閘門邁進。本 repository 自身的 session 每天都會把遙測資料餵給本機的一個 Auspex。

**誠實的但書——如今是一個產品決策。**每一個逐回合預測目前仍然是由 cold-start（冷啟動）規則產生，而非經過校準（calibrated）的模型。分數不是機率，並且在每一個呈現介面上都明確標示為如此（Constitution §7 第 7 條）。我們的第一批實地資料已經量化了差距——cold-start
成本預測在中位數低估了實際成本約 7–9×，主因是對 cache-read 視而不見的計價
（[#66](https://github.com/huaiche94/auspex/issues/66)）——而外部研究（前述 Bai 等）指出這個上限是結構性的，不是暫時的缺口：同一任務的不同執行 token 用量可差到 30×。我們的回應是把整個產品繞著它重新排序
（[#90](https://github.com/huaiche94/auspex/issues/90)）：量測與彙總的呈現介面（精確用量、花費步調、配額剩餘跑道、原地打轉觀測）在每個地方都居於首位；逐回合的點估計被降格為有標示的參考區間；而校準里程碑（M13，
[#11](https://github.com/huaiche94/auspex/issues/11)）會先瞄準那個*確實*可駕馭的預測——session 層級的剩餘跑道命中機率（hit-probability）——之後才會回頭處理逐回合 token。Auspex 的價值在於它所把關的**決策與它映照回來的現實**——checkpoint、pause、resume、block，以及一份你實際花費的精確帳目——而不在於逐回合猜測的精確度。

尚待完成的路線圖里程碑：Codex M7 Phase 2 的尾巴——app-server 訂閱、graceful
interrupt、`codex exec resume`
（[#9](https://github.com/huaiche94/auspex/issues/9)）；受管 shell
模式（M11，[#8](https://github.com/huaiche94/auspex/issues/8)）；校準（calibration）的擬合與回饋（fit-and-feed-back）pipeline，剩餘跑道優先（runway-first）（M13，
[#11](https://github.com/huaiche94/auspex/issues/11)）；發布前的命名空間（namespace）認領
（[#18](https://github.com/huaiche94/auspex/issues/18)）；建立在如今持續累積之檔案操作彙總上的原地打轉（spin）偵測閘門
（[#68](https://github.com/huaiche94/auspex/issues/68)，以資料為閘）；源自研究的預測升級
（[#65](https://github.com/huaiche94/auspex/issues/65)、[#66](https://github.com/huaiche94/auspex/issues/66)
的預測那一半、[#42](https://github.com/huaiche94/auspex/issues/42)、
[#20](https://github.com/huaiche94/auspex/issues/20)——以資料為閘）；尾隨（tail）rollout、能捕捉
IDE 外掛與 subagent 執行緒的監看器（watcher）
（[#92](https://github.com/huaiche94/auspex/issues/92)）；以及團隊用量彙整（team usage rollup，
[#91](https://github.com/huaiche94/auspex/issues/91)）。
[issue tracker](https://github.com/huaiche94/auspex/issues) 是即時更新的待辦清單。所有工作都受里程碑閘控（milestone-gated）：任何功能都不會在其里程碑之前被實作（`docs/design/Auspex_ADD.md` §31）。

從上述 Bai 等論文提煉出、以研究為依據的新增項目——cache-aware 四類成本模型（其擷取那一半已落地；預測那一半仍開放於 #66）、以*觀測*而非預測抓出原地打轉 turn 的重複檔案操作 risk 訊號（其擷取那一半已透過 ADR-052／#67 落地），以及 phase-aware 條件式預測——已作為路線圖筆記（僅為外部先驗，絕非擬合數字）記錄於
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
integrations/codex/   Codex CLI hook wiring (hooks.json)
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
