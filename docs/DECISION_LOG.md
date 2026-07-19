# Decision Log（決策日誌）

> 🌐 本文件以繁體中文撰寫，即為決策紀錄的規範文本——與 `docs/design/Auspex_ADD.md` 同為 ADR-049「原著語言為規範」規則下的兩個中文原著，不另建會漂移的譯本。

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

    D12A --> D13{D-13 statusline 格式 v2}
    D13 ==>|✅ 白話機率 + weekly 段| D13A[probably 50% < N tokens<br/>去成本段、✓ RUN、◷ weekly limit]
    D13 -.->|✗ 保留分位數術語| D13B[est P50 Ntok 較難讀]

    D13A --> D14{D-14 context 投影顆粒度}
    D14 ==>|✅ A+B：顯示分離 + token 常數| D14A[measured→projected 顯示<br/>增長預設改 token 表達]
    D14 -.->|✗ 只修顯示 A| D14B[1M 窗投影仍悲觀 10 倍]
    D14 -.->|✗ 只等 #11/#20 校準| D14C[cold-start 悲觀持續數週]

    D14A --> D15{D-15 statusline v3 重設計}
    D15 ==>|✅ owner mock 版式| D15A[token 段下架 #42 gate<br/>實測優先 context、ax»]
    D15 -.->|✗ 保留 token 段| D15B[cold-start 常數無訊號]

    D15A --> D16{D-16 daemon 形態 + token 存放}
    D16 ==>|✅ 混合 + data dir 0600| D16A[前景程序 + launchd plist<br/>daemon.token 每啟動輪換]
    D16 -.->|✗ 純手動 / launchd 全自動| D16B[驗收靠使用者 / 平台綁死]
    D16 -.->|✗ Keychain / 不落地| D16C[darwin 專屬 / SSE 重連複雜]

    D16A --> D17{D-17 文件重組 + 雙語}
    D17 ==>|✅ docs/design/ + zh-TW 非規範| D17A[ADR-049：三設計文件搬遷<br/>每資料夾 README、原著語言為規範]
    D17 -.->|✗ 全部搬進 docs/| D17B[破壞 GitHub 社群檔慣例]
    D17 -.->|✗ 中文平行規範| D17C[雙 source of truth 風險]

    D17A --> D18{D-18 CHECKPOINT_AND_RUN 自動化}
    D18 ==>|✅ 自動 checkpoint・config 可關・fail-open| D18A[ADR-0054：兩個決策面接線<br/>on_checkpoint_and_run 預設開]
    D18 -.->|✗ 維持 advisory 只靠 #114| D18B[高風險 turn 窗口仍開著]
    D18 -.->|✗ 預設關閉 opt-in| D18C[產品主張對多數人仍未接線]
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

## D-13 — Statusline 格式 v2（issue #31）

- **日期／情境：** 2026-07-14。Owner 首次完整體驗 statusline（#27 修復遙測、#29 加 icon/顏色之後）後直接指定新格式：白話機率、去掉成本區間、加 weekly limit 段、RUN 改綠勾。
- **決定（owner 指定，未開選項）：** `ax✈ <model> │ 🔮 probably (50%) < N tokens │ context worst-case [██████··············] ~N% │ ◷ weekly limit ~N% │ ✓ RUN  WARN  CHECKPOINT_AND_RUN  BLOCK`。v2.1（owner 看過 v2 實機後追加）：context 段改用 20 格進度條（每格 5%，取代圓形量表）；policy 段顯示完整嚴重度量表——當前狀態亮色＋icon、其餘暗色，first-time user 不用先知道量表全貌就能讀懂位置；量表外的未知 action 單獨原樣顯示，不冒充量表成員。「(50%)」是渲染資料非固定標籤——#11 校準落地後分位數可以收緊而格式不變（owner 明言要機率顆粒度的成長空間）。weekly 段直接取自 live snapshot 的 seven_day 視窗（#27 之後是真資料），與預估卡無關、cold session 也會顯示；著色等 #21 的 binding-constraint policy 給出誠實閾值後再做。
- **Constitution #2 張力（已知會）：** 「probably」是機率語言，而 P50 目前是未校準規則值。緩解：括號保留分位數本體、卡片表面（additionalContext／`auspex evaluate`）維持完整 "uncalibrated estimate" 標籤；statusline 是最壓縮的表面，可讀性優先是 owner 的取捨。
- **可逆性：** 高——純 presenter 層字串，測試 byte-exact pin，改回一個 commit。

## D-14 — Context 投影的顆粒度與 1M 窗悲觀偏差（issue #31 後續）

- **日期／情境：** 2026-07-14。Owner 追問 context worst-case 的計算依據，稽核發現三層問題：①投影 = 實測 + cold-start 常數 `0.10 × window`——「window 比例」單位在 200k 窗年代合理（10% = 20k tokens），在 owner 的 Fable 5 1M 窗變成「每輪悲觀 107k tokens」，比實測單輪增量（1–3%）悲觀一個數量級；②predictor 輸入偏好 Claude Code 的整數 `used_percentage`（1% = 1M 窗的 10k token 糊掉），同 payload 的精確 `used_tokens` 反而只是 fallback；③statusline 只顯示投影終值，實測與預測成分不可分辨。
- **選項：** ①**A+B（✅）**——A 顯示層：context 段改 `[bar] 26.8% → worst-case ~29%`（實測一位小數、預測帶 ~ 標記），renderer 輸入重構為 `StatusLineInput` struct；B 常數修正：增長預設改用 token 表達（P50 6k / P90 20k tokens，= 舊 fraction 在 200k 窗的絕對值，200k 行為不變、大窗誠實縮放；window 未知時 fallback 回 fraction）＋輸入偏好翻轉為精確 tokens。②只做 A——1M 窗投影仍悲觀 10 倍。③只等 #11/#20——校準需累積遙測，悲觀顯示持續數週。
- **另釐清（owner 問）：** 「打字中即時預估」現行 statusline 架構做不到（payload 無輸入緩衝區、無 keystroke 觸發、最早鉤子是 UserPromptSubmit）——該需求屬 M12 VS Code 擴充（#10，blocked on M6）；「送出前預估」已有 `auspex evaluate --prompt-file -`。
- **可逆性：** 高——常數與顯示皆單點；#11 校準落地後 cold-start 常數整組被取代。

## D-15 — Statusline v3：owner 重設計（issue #41；token 段下架見 #42）

- **日期／情境：** 2026-07-14。Owner 以 mock 直接指定新版式（未開選項）：`ax» <model> │ ◷ weekly ~37% │ context [bar] 4.0% (p95 ≤5%) │ ✓ RUN`。背景：①token 段的 P50 被證實是 cold-start 常數（3210 = 6000 × 0.5 × 1.5^(1/6)，逆向工程對到小數，#42）——不會動的數字沒有訊號價值；②owner 實機觀察到 `14.8% → worst-case ~11%` 的矛盾（stale 投影錨在較早 baseline，D-14 的顯示拆分沒有下限保護）。
- **決定：** ①token 段自 statusline 下架，預估留在卡片表面（additionalContext／`auspex evaluate`），#42 讓預估會動之後才回來（回歸時 gate on 校準或 cohort 樣本數，不是無條件恢復）；②段序改 model│weekly│context│policy；③weekly 標籤去掉 "limit"；④context 段改實測優先——bar 與主數字都是 live 實測，投影降為括號上界 `(p90 ≤N%)`，渲染值 clamp 到 `max(投影, 實測)`（worst case 低於現值是矛盾不是資訊）；無實測時保留 projection-only 的 worst-case 措辭；無 card 時實測照樣渲染（snapshot 資料不依賴卡片）；⑤policy 只顯示當前 action（icon＋語意色），量表外 action 仍原樣顯示；⑥品牌字形 `ax✈` → `ax»`。
- **Constitution #2 修正：** owner mock 寫 `p95`，pipeline 算的是 P90（`ProjectedContextUsedP90`）——標籤渲染為 `p90`，量詞必須對得上實際計算的分位數。
- **可逆性：** 高——純 presenter 層，測試 byte-exact pin，D-13 v2.1 版式一個 commit 可回復。

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

## D-16 — M6 daemon 形態與 loopback token 存放（issue #7 開工決策）

- **日期／情境：** 2026-07-14。#7（M6 daemon）開工前，把「待決」清單裡的兩項以選項形式提請 owner 拍板。
- **決定①（daemon 形態）：混合** —— daemon 本體是 OS 無關的前景程序（`auspex daemon run`），另提供 `auspex daemon install` 產生 macOS LaunchAgent plist（之後可加 systemd unit）。「無人值守」由 launchd KeepAlive 達成，核心邏輯保持可測試、可手動跑。未選：純手動（驗收要靠使用者自行佈署）、launchd 全自動（平台綁定最深，解除安裝與升級流程被迫同批）。
- **決定②（token 存放）：data dir 0600 檔案** —— `<data>/daemon.token`，每次啟動輪換（ADD §27.5），與 Docker/k8s loopback 慣例一致、跨平台、可測試；#10 VS Code 擴充讀同一路徑即可。未選：macOS Keychain（darwin 專屬、CI/headless 麻煩）、每次啟動隨機不落地（客戶端每次重新發現 token，SSE 重連複雜）。
- **實作附帶（lead 判斷，未開選項）：** pauseContext（QuotaBaseline／GitHeadBaseline／WorktreeID／PausedWorkPaths）原為 Service 的 in-memory bookkeeping，跨行程的 daemon worker 拿不到——持久化進 `pause_records.metadata_json` 既有 free-form 欄位（encodePauseMetadata／persistProgressMetadata 已建立同欄位合併式編碼先例），不新增 migration（Constitution §4.7 range 紀律）。
- **可逆性：** 中——token 路徑與 plist 形態是單點；metadata_json 的 context 鍵一旦有 sleeping 中的 pause 依賴，變更需要 backfill。

## D-17 — 文件重組 + 雙語文件政策（ADR-049）

- **日期／情境：** 2026-07-14。Owner 要求：以 first-time viewer 視角重寫 `README.md`、根目錄 `.md` 整理進 `docs/`、每個資料夾都要有 `README.md` 介紹、所有 `.md` 提供繁體中文版本，且文件措辭避免模糊描述（如「CI 綠了」）。
- **決定：** ①三份設計文件（`Auspex_ADD.md`、`Auspex_Predictor_Design_Supplement.md`、`Auspex_Parallel_Execution_Plan.md`）→ `docs/design/`，檔名不變（`§` 引用照舊可 grep）；②九份 GitHub 社群慣例／流程權威檔（README、AGENTS、CHANGELOG、CODE_OF_CONDUCT、CONSTITUTION、CONTRIBUTING、GOVERNANCE、SECURITY、SUPPORT）留在根目錄；③活文件更新路徑引用，歷史紀錄（accepted ADRs、`docs/archive/`、`docs/implementation/` 進度日誌、Go 註解、checksummed fixtures）一律不改寫；④雙語政策：每份文件加 `<name>.zh-TW.md`，**以原著語言為規範文本**——除了兩份中文原著（`docs/design/Auspex_ADD.md` 本體即繁中、`docs/DECISION_LOG.md` 即本檔）以中文為規範、不另建會漂移的 zh-TW 副本外，其餘全部以英文版為準，分歧時中文版是 bug；⑤測試 fixture 的 `.md`（如 `testdata/checkpoints/state/add-section-18-*.md`）是測試輸入不是文件，不翻譯、其目錄不加 README（避免破壞列舉／checksum 測試）。完整條文見 ADR-049。
- **未選：** 全部 `.md` 搬進 `docs/`（破壞 GitHub 社群檔案慣例與 agent 工具對根目錄 `AGENTS.md` 的預期）；只加連結不搬移（根目錄依舊 12 檔）；中文版列為平行規範文本（製造第二個 source of truth，違反 Constitution §1 精神）。
- **可逆性：** 高——`git mv` ＋引用更新可單一 commit 回復；翻譯與資料夾 README 是純新增檔案，刪除即回復。

## D-18 — CHECKPOINT_AND_RUN 自動化：自動 checkpoint、config 可關、fail-open（issue #116）

- **日期／情境：** 2026-07-19。README 準確性稽核發現 `CHECKPOINT_AND_RUN` 是真實、有持久化的 policy 決策（ADR-043；critical 風險帶、高 blast radius、context/成本天花板都會發出），但**沒有任何程式碼對它採取行動**——pre-turn hook 與 managed runner 都只分支 `BLOCK`，checkpoint 只能靠操作者手動 `auspex checkpoint create`（D-08 當時的定調即「CHECKPOINT_AND_RUN 建議」）。issue #116 標記 `adr-needed` 提請拍板。
- **選項：** ①**自動 checkpoint、config 可關、fail-open（✅）**——決策為 `CHECKPOINT_AND_RUN` 且 gate 開啟（預設開）時，在 turn 繼續前自動建立 state + repository checkpoint 對，checkpoint ID 走既有 `DecisionAllowRequest.RepositoryCheckpointID` 機制（issue 後立即 consume 作為稽核紀錄）；任何失敗放行 turn 並記警告——安全網故障不得封鎖它要保護的 session。②維持 advisory、自動 checkpoint 只放 PreCompact（#114）——決策名字承諾的動作依然沒接線、高風險 turn 窗口仍開著。③預設關閉 opt-in——對從不改 config 的使用者，產品主張永遠是空話。
- **後果：** ADR-0054（就此動作取代 ADR-043/D-08 的「僅建議」定調；與 #114 的 PreCompact 路徑互補不互代）。實作：`internal/orchestrator/autocheckpoint.go`（`AutoCheckpointer`，兩個呼叫點共用）、hook 與 managed runner 的 `CHECKPOINT_AND_RUN` 分支接線、config 新鍵 `state_checkpointing.on_checkpoint_and_run`（預設 `true`；`false` = 明確 advisory）——這也是 M1 分層 config 鏈的第一個 production 消費者。熱路徑零成本：只有 `CHECKPOINT_AND_RUN` 分支才呼叫。
- **可逆性：** 高——gate 一行 config 即回 advisory；nil 組裝與 #116 前逐位元相同（有測試釘住）。

## 待決（尚未成為決策點）

- **#18 佔位動作**：需要 owner 本人註冊（auspex.tools、VS Code publisher、Open VSX）。
- **校準閾值重審**（D-08 的重審條件觸發時）。
