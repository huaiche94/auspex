# 經驗回顧 — qa（Phase 3：qa-01、qa-08）

> 🌐 [English](qa.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

| task_id | estimated_complexity | actual_complexity | estimated_files_changed | actual_files_changed | estimated_duration | actual_duration | unexpected_dependencies | unexpected_files | blockers_encountered | token_waste_observations | recommendations_for_auspex |
|---|---|---|---|---|---|---|---|---|---|---|---|
| qa-01 | S（DAG：200 LOC，4 個檔案） | S — 與估計相符 | 4 | 1（`.github/workflows/ci.yml`） | 執行前未追蹤（DAG 沒有 duration 欄位） | 一次連續的 pass，此環境中沒有 wall-clock 量測工具 | `task`／`golangci-lint` 在這個全新的 worktree 中並未預先安裝；但兩者其實已存在於 `$(go env GOPATH)/bin`，因為這個 worktree 與主要 checkout 共用 host 的 GOPATH，而 `foundation-09` 已在該處安裝過兩者 — 本次 session 只需將該目錄加進 `PATH` 即可，並非真正的新相依 | `actionlint` — 純粹為了在單純的 `yaml.safe_load` 解析之外，進一步強化對 GitHub Actions YAML 的驗證，而在本機安裝（`go install github.com/rhysd/actionlint/cmd/actionlint@latest`）；並非 repository 的相依套件，也未被任何受版控的檔案參照 | 無 | DAG 對 qa-01 估計需要 4 個檔案；實際上 1 個檔案（workflow 本身）就已足夠 — 一個呼叫既有 Taskfile、而非把檢查邏輯內嵌重寫一遍的 CI workflow，把原本可能是多檔案的 scaffold（各自獨立的 lint/test/build 設定）收斂成一個含少數幾個 job 的 YAML 檔案。這與 predictor 在 Phase 1 的心得（`predictor-03`）互相呼應：DAG 對「在既有 primitive 上做簡單封裝（thin orchestration）」這類工作的檔案數估計，實際結果往往會低於估計值 | 除了本機 dry-run（`task fmt/lint/build/test`）之外，最有用的單一驗證步驟是 `actionlint` — 它能抓到一般 YAML parser 與本機 shell dry-run 完全抓不到的 GitHub Actions 專屬錯誤（錯誤的 `uses:` 參照、錯誤的 `runner.os` 比較、無效的 expression 語法），因為兩者都不會真的解析 Actions schema 或求值 `${{ }}` expression。建議未來任何 `.github/workflows/*.yml` 的變更都將 `actionlint` 視為標準、成本低廉的 pre-commit 檢查，而不只是這第一次才用 |
| qa-08 | S（DAG：300 LOC 文件，4 個檔案） | S — 與估計相符 | 4 | 4（`SECURITY.md`、`CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`、`GOVERNANCE.md`） | 執行前未追蹤 | 一次連續的 pass | 無 — ADD §30.7／§30.8 直接提供了具體的政策內容，除了閱讀 README.md 既有的 Contributing 章節與 `agents/qa.md` 本身的 Security assertions 清單之外，不需要額外的跨角色查找 | 無 | 此 repository 目前尚無 `LICENSE`／`NOTICE` 檔案（這是 `foundation-09` 先前已自行標記為超出其自身 phase 範圍的缺口）。這並不會阻擋 qa-08 四份文件的撰寫 — Apache-2.0 已被明確點名，與 `README.md` 的 Tech stack 表格一致 — 但這確實是一個真實存在、原本就有的缺口，如今已從 `CONTRIBUTING.md`／`GOVERNANCE.md` 交叉參照，並在此 progress artifact 的 `blockers` 欄位中重新歸檔給 `foundation`／`contract-integrator`，而非在此直接修復，因為 `LICENSE`／`NOTICE` 並不在 `agents/qa.md` 的 exclusive-paths 清單之中 | 無 | ADD §30.2 點名了幾個 `task` target（`task bootstrap`、`task test:race`、`task test:e2e`、`task vscode:test`、`task research:test`、`task verify`），這些目前在實際的 `Taskfile.yml` 中都還不存在，因為它們所要操作的目錄樹（`vscode/`、`research/`）本身也還不存在。這次只記錄了 ADD §30.2 清單中、`Taskfile.yml` 今天真正有實作的子集（而非 ADD 完整的理想清單），避免寫出一份會叫新貢獻者去執行一個立刻就會失敗的指令的 CONTRIBUTING.md — 這值得歸納成一條通則：治理／貢獻者文件應該只斷言經過 `git grep` 驗證、目前確實存在的工具能力，並將 ADD 中更完整的未來清單明確標示為「尚未實作」，而不是悄悄地承諾成好像已經可用。 |

## 跨節點觀察

- 這兩個 node 確實彼此獨立（除了同樣受 Constitution §4 的 path-ownership 規範約束之外，qa-01 與 qa-08
  之間沒有任何共用的檔案或概念），並依照本 phase 的任務指示，依序執行、兩者之間各自完整走過一次
  validation-and-commit 週期 — 這個過程很順利，因為一旦另一個 node 的工作成果可見後，都不需要回頭修改
  任一 node 的產出或假設。
- 與 predictor 在 Phase 1／2 的 node 不同，qa-01／qa-08 完全沒有觸及 `internal/domain` 或
  `internal/app/ports.go` 的介面 — 兩者都是純粹的 infrastructure／文件類 node，不存在 frozen-contract
  相依風險，這也與 execution DAG 將兩者都標記為「Low」風險、且未列出任何 blocker 的情況一致。
- qa 角色自己的 packet（`agents/qa.md`）明確禁止在初次 pass 中更動 feature production code（「針對
  owner 提出缺陷回報；唯有 contract-integrator 能授權跨 owner 的修正」）— 這一點在 qa-08 中透過
  LICENSE/NOTICE 的缺口被真實驗證過：正確的作法是把這個缺口記錄在此 progress artifact 的 `blockers`
  欄位中，而不是直接建立缺少的檔案，儘管這麼做在技術上並不困難、甚至可以說頗有幫助。建議把這種克制
  明確納入未來任何跨領域角色（其文件會引用到其他角色缺少之產出物的角色）的 onboarding 中，作為預期的
  標準模式，而非邊緣案例。
- progress artifact 中記錄 commit SHA 的欄位存在一個天生的雞生蛋、蛋生雞問題：commit 的 SHA 是以內容
  定址（content-addressed）計算而來，其中包含了 commit message 本身，因此 artifact 自己的 `commit:`
  欄位不可能在該 commit 存在之前就先得知。這個 repository 自身的先例（commit `940c5cb`，"Record
  Bootstrap commit SHA in CONTRACT_FREEZE.md and progress artifact"）已經確立了解法 — 先 commit
  實際的工作內容，再用一個緊接著的小型 follow-up commit，把 SHA 記錄進 progress artifact 中。qa-01
  與 qa-08 在本 phase 中都遵循了同樣的模式，而沒有另外發明其他慣例。
