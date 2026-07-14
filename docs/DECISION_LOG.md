# Decision Log（決策日誌）

| Field | Value |
|---|---|
| Purpose | 記錄每一個**由 repository owner 選擇**的決策：當時的選項、優缺點、選擇結果、後果與可逆性，供日後 review |
| Scope | Owner 層級的產品/架構/流程決策。純架構細節的完整論證在 `docs/adr/`（本文件與其互相連結，不重複內容） |
| Convention | 每個決策一個 `D-##` 條目；新決策**必須**在做成當下追加到本文件（樹 + 條目兩處）。被推翻的決策不刪除——加註 superseded 指向新條目 |
| Started | 2026-07-13（回溯涵蓋當日 issue triage session 起的全部決策） |

## Decision Tree

粗框 = 已選路徑；虛線葉 = 被否決的選項；每個節點對應下方同編號條目。

```mermaid
flowchart TD
    START([2026-07-13 issue triage session]) --> D1

    D1{D-01 P1 橋接架構}
    D1 ==>|✅ 顯式完成 + 事件關聯| D1A[EventCorrelator +<br/>auspex progress complete]
    D1 -.->|✗ 自動橋接完成| D1B[Stop event 自動 CompleteNode]
    D1 -.->|✗ 擱置等資料| D1C[維持 P1 掛起]

    D1A --> D2{D-02 主攻方向}
    D2 ==>|✅ 預估體驗線| D2A[ADR-043 → #14 → #12]
    D2 -.->|✗ daemon 基礎線| D2B[#7 先行]
    D2 -.->|✗ 第二 provider 線| D2C[#9 Codex 先行]
    D2 -.->|✗ continuity 補完線| D2D[#6 restore 先行]

    D2A --> D3{D-03 patch redaction 範圍}
    D3 ==>|✅ ADR 接受殘餘風險| D3A[ADR-042 + boundary pin test]
    D3 -.->|✗ 擴充掃 headers/檔名| D3B[redact 結構性改寫]

    D3A --> D4{D-04 dogfooding 時機}
    D4 ==>|✅ 現在就裝| D4A[當天裝 → 當天發現 #17]
    D4 -.->|✗ 等 #14 做完| D4B[體驗完整但晚幾天]
    D4 -.->|✗ 不裝本機| D4C[無真實資料]

    D4A --> D5{D-05 命名策略・第一輪}
    D5 -.->|✗ 公開名 preflight-agent| D5A[保留 Preflight 品牌]
    D5 -.->|✗ 全面 preflight-agent| D5B[binary 也改]
    D5 -.->|✗ 發布前再決定| D5C[維持現狀]
    D5 ==>|✅ 使用者改向：換全新名字| D6

    D6{D-06 新名字甄選}
    D6 ==>|✅ auspex| D6A[ADR-045 全 repo 改名<br/>+ 完整稽核 GO]
    D6 -.->|✗ spae| D6B[最乾淨但冷僻]
    D6 -.->|✗ presage| D6C[唯一可拿 .dev]
    D6 -.->|✗ orrery / precog / scry| D6D[各有碰撞或噪音]

    D6A --> D7{D-07 #17 bootstrap 機制}
    D7 ==>|✅ Lazy：hook 自動登記| D7A[hook 路徑 idempotent upsert]
    D7 -.->|✗ 顯式 auspex init| D7B[手動登記指令]
    D7 -.->|✗ 兩者都做| D7C[Lazy + init 診斷]

    D7A --> D8{D-08 context 出廠預設}
    D8 ==>|✅ 啟用保守閾值| D8A[P90>85% WARN / >95% CHECKPOINT<br/>信心不足不觸發，config 可關]
    D8 -.->|✗ 完全惰性| D8B[設定才啟用]

    D8A --> D9{D-09 本輪節奏}
    D9 ==>|✅ #17 + #13 context 增量| D9A[本 session 收預估體驗線]
    D9 -.->|✗ 只做 #17| D9B[最小關鍵路徑]
    D9 -.->|✗ 推到 #7 daemon| D9C[進度最多但品質風險]

    D9A --> D10{D-10 model/effort 預測參數 gap}
    D10 ==>|✅ backlog 文件 + issue #20 排序| D10A[docs/backlog/ 新文件<br/>#20 排 #13 後、與 #11 綑綁]
    D10 -.->|✗ 草案 ADR Proposed| D10B[佔 0047、立 Proposed 先例]
    D10 -.->|✗ 併入 #13 範圍| D10C[資源軸/特徵軸混流]
    D10 -.->|✗ 只留文件不開 issue| D10D[易被遺忘]

    D10A --> D11{D-11 rate-limit 視窗策略}
    D11 ==>|✅ 全部抓 + binding constraint| D11A[parser 泛型化 #21<br/>policy 取最早耗盡視窗]
    D11 -.->|✗ 只看 5hr| D11B[weekly 撞牆盲區]
    D11 -.->|✗ 只看最嚴 weekly| D11C[短程 5hr 盲區]

    D11A --> D12{D-12 下輪開場}
    D12 ==>|✅ #21+#20 Phase 0 捕捉先| D12A[capture-before-model 紀律<br/>晚一天=一天 unlabeled]
    D12 -.->|✗ #11 校準骨架先| D12B[缺標籤恐返工]
    D12 -.->|✗ #7 daemon 先| D12C[資料飛輪慢幾週]
```

---

## D-01 — P1 橋接架構（issue #1）

- **日期／情境：** 2026-07-13。qa-09 唯一未解 P1：provider event 與 Progress Tree 完成之間無 production 接線。核心張力：Constitution §6「completed means evidenced」——Stop 事件本身不是完成證據。
- **選項：**
  1. **顯式完成 + 事件關聯（✅ 已選，推薦項）**——完成永遠由顯式呼叫帶 artifacts 觸發、validator 驗證；event 只填 TaskID/NodeID 供 audit/join。優：完全符合 evidence 原則、凍結契約幾乎不動。缺：非全自動。Blast radius：小。
  2. 自動橋接完成——Stop event 自動嘗試 CompleteNode。優：demo 全自動。缺：與 evidence 原則緊張、動五處凍結契約。Blast radius：大。
  3. 擱置到有 dogfooding 資料。優：避免臆測。缺：P1 持續掛著。
- **後果：** `EventCorrelator`（correlate.go）+ `auspex progress complete` CLI，commit `1f43bb6`，#1 關閉。KnownGap 測試翻轉為正向斷言。
- **可逆性：** 高——自動橋接可日後作為 opt-in 疊加，不需拆現有設計。

## D-02 — 大型 roadmap 主攻方向

- **日期／情境：** 2026-07-13。#6–#11/#13/#14 一次只能推一線。
- **選項：** ①**預估體驗線（✅）**：ADR-043 → #14 預估卡 → #12 dogfooding——產品核心、不依賴 daemon、立刻累積校準資料；②daemon 基礎線（解鎖 auto-resume/VS Code 但工程最大）；③第二 provider 線（驗證抽象）；④continuity 補完線（restore）。
- **後果：** ADR-043、#14（commit `68404ce`）、#12 全部落地；#6–#11 依此排序並在各 issue 留言記錄。
- **可逆性：** 高——只是順序，其他線都還在 backlog。

## D-03 — Patch redaction 範圍（issue #5，qa-09 P2）

- **選項：** ①**ADR 接受殘餘風險（✅）**：檔名/binary-diff headers 不改寫（patch 的 git-apply 有效性是 restore 前提、威脅模型不同），加 boundary pin test；②擴充 redaction 掃 headers/檔名（隱私最大化但需解決 patch 結構有效性，工程大）。
- **後果：** ADR-042 + `TestRedactPatchSecrets_ADR042_...` pin test，#5 關閉。
- **重啟條件（寫在 ADR-042）：** 真實洩漏實例出現／redact 具備結構性改寫能力／checkpoint 出現預設外傳路徑。

## D-04 — Dogfooding 安裝時機（issue #12）

- **選項：** ①**現在就裝（✅）**：立即累積真實 turns、首次驗證 install 不破壞既有 hooks；②等 #14 一起裝（第一印象完整）；③不裝本機。
- **後果：** 當天安裝、當天收到真實遙測、**當天發現 #17**（native hook mode 缺 session 登記——dogfooding 的第一個 production bug）。此決策的回報直接證明了選項①的論點。

## D-05 — 命名策略・第一輪（issue #16 稽核後）

- **情境：** preflight 稽核發現：preflight.sh 同賽道活躍競品、`preflight` binary 被 Replicated/Red Hat 佔用 PATH、三網域被註冊、VS Code 顯示名被佔。
- **選項（原提案）：** ①公開名 preflight-agent + binary 保留 preflight（推薦）；②全面 preflight-agent；③發布前再決定。
- **結果：** **使用者未選任一——改向要求「簡短、有預測意涵、無碰撞的全新名字」**→ 進入 D-06。這是一次有價值的提案被推翻：三個選項都預設保留 Preflight 品牌，使用者判斷品牌本身已不值得保。

## D-06 — 新名字甄選

- **候選（全部經 GitHub/npm/Homebrew/.dev 查核）：** auspex（語義冠軍：羅馬鳥卜官，行動前讀兆決定放行——與產品定位一字相承）、spae（最乾淨但冷僻）、presage（唯一可拿 .dev，但有 Rust Signal 庫同名）、orrery（隱喻美、拼寫難）、precog（流行文化直白、鄰近碰撞）、scry（字首噪音大）；vates 出局（XCP-ng 母公司）。
- **選擇：** **auspex（✅）**＋全 repo 改名＋完整稽核。
- **後果：** ADR-045（廢止 ADR-001）、347 檔改名（commit `e1bbc40`）、GitHub repo → huaiche94/auspex、完整稽核 **GO**（詳見 #16 留言）、#18 追蹤佔位與監控（getauspex.com 是唯一需監控項）。
- **可逆性：** 中——1.0 前更名便宜（本次證明一個 session 可完成）、1.0 後昂貴。fallback 已寫進稽核結論。

## D-07 — #17 session bootstrap 機制

- **選項：** ①**Lazy：hook 自動登記（✅）**——idempotent upsert、fail-open、零使用者摩擦；缺點是 hook 多一次 git 呼叫、本機 DB 存 repo 絕對路徑（local-first 可接受）；②顯式 `auspex init`——可預期但多安裝摩擦、忘記 init 就永遠沒預估卡；③兩者都做——工作量最大。
- **既定約束（無選項）：** 非 git 目錄的 session 因 schema 外鍵一律降級為現狀。
- **後果：** 已落地（commit `fdeb61f`，#17 關閉）：`SessionBootstrapper` 接進全部四個 hook handlers，零預置資料的端到端驗收通過——真實 git repo + production deps 下 Resolve 成功、預估卡出現在 additionalContext。無需新 migration（既有 unique constraints 足夠）。

## D-08 — Context window 升格後的出廠預設（issue #13 增量 2）

- **前提（不重議）：** 金錢預算依 ADR-043 定案「未設定即不啟用」。context 不同——它非使用者宣告、有客觀上限、撞上即災難。
- **選項：** ①**出廠即啟用保守閾值（✅）**：投影 P90 context >85% → WARN、>95% → CHECKPOINT_AND_RUN 建議；cold-start 信心不足不觸發（沿用 Confidence 紀律）；config 可關可調。風險：未校準投影可能誤報。②完全惰性——零驚訝但核心保護形同虛設。
- **後果：** 已落地（commit `c8496c8`）：`internal/policy/context.go` 閾值規則 + never-downgrade 動作強度階梯 + migration 0045 持久化投影 + 預估卡/statusline/evaluate JSON 的 context 行。注意：現行 RuleQuotaForecaster 一律 cold-start，所以閾值「已啟用但暫不觸發」——warmed forecaster（#11）落地當天即自動生效（有 warmed-stub 端到端測試證明整條鏈）。
- **重審條件：** 校準資料（#11）就緒後重新檢視閾值；若誤報率高於預期，降級為惰性是一行 config 預設值的事。

## D-09 — 本輪推進節奏

- **選項：** ①**#17 + #13 context 增量（✅）**——預估體驗線一次收乾淨；②只做 #17——最小關鍵路徑；③一路推到 #7 daemon——不建議（安全敏感大工程塞在長 session 尾端）。
- **後果：** 本 session 範圍鎖定；#7 daemon 為下一 session 的第一優先。

## D-10 — Predictor 的 provider/model/effort 參數 gap（issue #20）

- **日期／情境：** 2026-07-13。Owner 詢問預測公式是否把 claude（model, effort）／codex（model, reasoning, speed）當輸入參數。稽核結論：provider/model cohort 在 ADD §15.2/§15.3 **有設計但未實作**（實際 cohort 只有同 session 近期觀測、quota delta 寫死 2.0/6.0）；effort/reasoning/speed **連設計都不存在**；`predictions` 表未存 model/effort 欄位，歷史預測無法事後分層校準。另發現 model/effort 是 turn 級變數（mid-session 可 `/model`、`/fast` 切換），現有 session 級 `provider_sessions.model` 放錯層。
- **選項（文件形式）：** ①**backlog 設計文件（✅）**——`docs/backlog/` 新類別，零 blast radius，日後可升格 ADR；②草案 ADR（Proposed）——佔 0047 編號、破全 Accepted 慣例，且尚無決策可記；③併入 #13——資源軸（context/cost/rate limit）與特徵軸（誰在跑、用多少力）混流，#13 膨脹。
- **選項（追蹤）：** ①**開 issue + 建議排序（✅）**——#20 建議排在 #13 之後、Phase 0（capture）與 #11 綑綁：校準要分層的正是這些欄位，晚一天記錄就是一天的 unlabeled history；②開 issue 不排序；③只留文件——repo 無純文件 todo 先例，易被遺忘。
- **後果：** `docs/backlog/provider-model-effort-features.md`（四階段：capture → cohort filtering → empirical calibration → codex wiring；capture-before-model 紀律，本文件不提任何係數）＋ issue #20。優先順序調整為 #13 →（#11 ＋ #20 Phase 0）→ #7 → #6 → #9 → #8 → #10。
- **可逆性：** 高——純新增文件與 issue；排序調整只是順序，#20 Phase 1+ 仍可獨立重排。

## D-11 — Rate-limit 視窗策略（issue #21）

- **日期／情境：** 2026-07-13。Owner 提出：Claude 有 session (5hr)、weekly (all models)、weekly (Fable) 多種限制，該看哪個？稽核發現 parser 寫死只認 `five_hour`/`seven_day`，per-model weekly 被靜默丟棄；domain 層本已支援 N 視窗。
- **選項：** ①**全部抓 + binding constraint（✅）**——parser 泛型化（未知視窗照收，forward compatible），policy 對每視窗投影後取最早耗盡者。優：永遠看到真瓶頸、provider 改限制免改程式。缺：卡片多一行。Blast radius：小。②只看 5hr——weekly 撞牆全盲，且 Codex 已移除 5hr、此路正在變窄。③只看最嚴 weekly——短 session 內 5hr binding 時會錯過即時風險，且 per-model 視窗不看 model 歸屬會誤報。
- **後果：** issue #21 追蹤實作；與 #20 Phase 0 綁定（weekly (Fable) 是否 binding 取決於 turn 的 model）。第一步是在 owner 機器抓一次真實 statusline payload 確認 per-model 視窗的 JSON key。
- **首個真實資料點（2026-07-14，issue #27 事故）：** 真實 payload 的 `resets_at` 是 **Unix epoch 秒數（數字）**，非 fixtures 手寫時假定的 RFC3339 字串——parser 因此整包拒收，且 `rate_limits` 只在 session 首次 API 回應後才出現，導致每個真實 session 只有開場一筆 ingest、之後 quota/context/usage 全斷（statusline 也退化成光禿 glyph、預估卡永遠 QUOTA_UNKNOWN）。hotfix #27 修復：`flexTimestamp` 同時接受 epoch/RFC3339、無法辨識的形狀降級為 unknown 而非錯誤；fixtures 改為 epoch 形狀。另確認現行 schema（Claude Code v2.1.208 文件）僅有 `five_hour`/`seven_day` 兩視窗——per-model weekly 尚未出現在 payload，#21 的視窗泛型化維持原計畫。教訓再次驗證「capture before model」：fixtures 必須源自捕捉的真實 payload，不能手寫假定。
- **可逆性：** 高——純捕捉面擴充，policy 選擇邏輯獨立可調。

## D-12 — 下輪開場優先序（E2E 回饋 session 定案）

- **選項：** ①**#21 + #20 Phase 0 捕捉先（✅）**——capture-before-model 紀律：晚一天記錄就是一天 unlabeled 歷史，#11 校準要分層的正是這些欄位；②#11 校準骨架先（缺標籤恐返工）；③#7 daemon 先（資料飛輪慢幾週）。
- **首次 E2E 回饋紀錄（2026-07-13）：** owner 的互動 session 仍掛改名前設定（見到舊 `pf✈`、未見預估卡）——結論：hooks 在 session 啟動時快照，改設定需重開 session。卡片觀感回饋延至 owner 實際體驗後（下次 session 結尾再問）。
- **更正（2026-07-13，同日稍晚）：** 上述歸因不完整——重開 session 後仍見 `pf✈`，追查發現 ADR-045 決定的 `pf✈`→`ax✈` 從未在程式碼落地（`git log -S 'ax✈'` 無任何 commit；改名 commit 只動了文件）。已改 `StatusLineText` 及全部測試並重新部署 `~/.local/bin/auspex`。教訓：ADR 的 Consequences 條列若含程式碼變更，落地 commit 應在同一 PR 或明確開 issue 追蹤，不能只寫在 ADR 裡。

---

## Lead 自行判斷（未開選項、已知會）

這些是 lead 依既有原則直接執行、事後報告的判斷，列出供 review：

| 判斷 | 依據 | 位置 |
|---|---|---|
| `docs/archive/` 與 git 歷史在兩次全域改名（Day-1、Auspex）中均不改寫 | 封存文件是歷史紀錄；改寫等於竄改 | Day-1 rename commit `6c7c99d`、ADR-045 §Decision 6 |
| ADR-001 的 §33 條目保留歷史原文＋廢止註記，而非 sed 成新名 | 不假裝產品一直叫 Auspex | `Auspex_ADD.md` §33 |
| ADR-044 將 evaluation.DataSource **原樣**升格凍結（不重設計） | 形狀已經過真實使用驗證；無驅動需求的重設計是投機抽象 | ADR-044 §Alternatives |
| schema-version 字串更名只在 pre-release 窗口允許 | 零外部使用者是唯一可行時機 | CONTRACT_FREEZE.md Amendments |
| 實作 agent 的產出一律不 commit、lead 審查後才進 main | 品質閘門 | 工作慣例（記錄於 memory） |

## 待決（尚未成為決策點）

- **#7 daemon 的形態**：launchd/systemd 自啟 vs 手動啟動、loopback auth token 的存放——開工前會以選項形式提問。
- **#18 佔位動作**：需要 owner 本人註冊（auspex.tools、VS Code publisher、Open VSX）。
- **校準閾值重審**（D-08 的重審條件觸發時）。
