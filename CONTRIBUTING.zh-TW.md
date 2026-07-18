# 為 Auspex 貢獻（Contributing to Auspex）

> 🌐 [English](CONTRIBUTING.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

感謝你有興趣參與貢獻。Auspex 目前處於活躍、里程碑閘控（milestone-gated）、多代理（multi-agent）的建置過程中——請在提出或實作任何變更之前完整閱讀本文件，並在你的第一個 PR 之前，先讀完本文件所指向的其他文件。

## 請先讀這些文件

依序：

1. [`CONSTITUTION.md`](CONSTITUTION.md)——本 repository 至高的流程權威：文件位階、ADR
   規則、路徑所有權，以及每一次變更都必須遵守的 Progress Tree 不變量。
2. [`docs/design/Auspex_ADD.md`](docs/design/Auspex_ADD.md)——唯一的權威
   架構與實作規格文件。與它衝突的程式碼、issue、PR 或留言是錯的；它不是。
3. [`AGENTS.md`](AGENTS.md)——貢獻者／agent 速查表。

這與 `README.md`「Contributing」章節所指名的閱讀順序相同；本檔案在其基礎上，以下方具體的實務機制加以擴充，而非取代它。

## 基本規則（Ground rules）

- **工作是里程碑閘控的**（`docs/design/Auspex_ADD.md` §31）。不要在目前里程碑之前提前實作，也不要為尚未在範疇內的
  provider 或功能加入推測性的抽象層
  （`CONSTITUTION.md` §7 第 10 條）。
- **每個 role／貢獻者都擁有一組互不重疊的路徑。** 如果執行計畫或某個
  `agents/*.md` 檔案把某個路徑指派給特定 role，就不要在該 role
  之外編輯它，除非透過 `CONSTITUTION.md` §4.4 的請求流程——這項規則同樣適用於人類貢獻者與 repository 內 agent role 之間的協作，跟 role 彼此之間一樣。
- **共享／跨領域檔案**——`docs/design/Auspex_ADD.md`、`CONSTITUTION.md`、
  `AGENTS.md`、`internal/domain/**`、`internal/app/ports.go`、
  `pkg/protocol/v1/**`、`docs/adr/**`——專屬於
  `contract-integrator` role 所有。不要送出直接編輯這些檔案的 PR；請先提出變更建議，讓該 role 來落地。
- **架構變更需要事先（而非事後）先有一份 ADR**
  （`CONSTITUTION.md` §3）——這包括對正式環境執行期語言、daemon
  傳輸方式、（以不向後相容方式）變更 SQLite schema、provider 整合合約、checkpoint 格式、
  Graceful Pause／Auto-Resume 語意、隱私預設值、公開的
  CLI／API／協定相容性、OSS 授權條款，或是預測輸出從分數變為機率的變更。
- **「完成」代表有證據佐證**，而不是自稱完成。一項變更不會因為它在本機能編譯，或描述裡這麼寫，就算完成——它需要
  `CONSTITUTION.md` §6 所描述的持久性證據（測試、產物）。

## 開發環境設定

需要 Go 1.26.5（版本固定於 `go.mod`）。在 repository 根目錄下：

```bash
task fmt     # gofmt check (fails if any file is unformatted; does not rewrite)
task lint    # go vet + golangci-lint run ./...
task build   # builds ./bin/auspex
task test    # go test -race ./...
```

若貢獻者／CI 步驟沒有安裝 `task`，也有對應的 `make` 目標可用
（`make fmt`、`make lint`、`make build`、`make test`）——
詳見 `Makefile`，它刻意維持為 `Taskfile.yml` 的一層薄鏡像（thin mirror）。`task`
（預設目標，不帶任何參數的 `task`）會一次執行 `fmt` + `lint` + `test`，是本機最接近
CI 在每個 PR 上實際檢查內容的等效指令（`.github/workflows/ci.yml`）。

`docs/design/Auspex_ADD.md` §30.2 另外還指名了 `task bootstrap`、
`task test:race`、`task test:e2e`、`task vscode:test`、
`task research:test`，以及 `task verify`，作為本專案最終完整的本機
task 介面。其中有幾個目前還不存在於 `Taskfile.yml` 中，因為它們所操作的目錄樹
（`vscode/`、`research/`，以及 `internal/integrationtest/` 下的端對端 fixture）
仍在建置中——它們會隨著這些目錄樹的落地而新增，而不會在此之前被憑空發明。

## 提出一項變更

1. 確認這項變更符合目前的里程碑／波次（見
   `README.md` 的「Phase roadmap」與
   `docs/implementation/vertical-slice/EXECUTION_DAG.md`）。
2. 確認你需要動到的檔案，落在你被允許編輯的路徑之內（見上方「基本規則」）。
3. 撰寫測試。依 `CONSTITUTION.md` §6，未經測試的行為不視為完成。
4. 在開 PR 之前，先在本機執行 `task fmt && task lint && task test`
   ——這正是 CI 會執行的內容，而 CI 理應是綠燈
   （`.github/workflows/ci.yml`，Ubuntu/macOS/Windows 矩陣）。
5. 開一個 PR，並在描述中說明*為什麼*而不只是*做了什麼*——審閱者需要對照
   `docs/design/Auspex_ADD.md` 與 `CONSTITUTION.md` 來檢查這項變更，而不只是看 diff 本身。

## 為你的 commit 簽署（DCO）

Auspex 要求每一個 commit 都附上
[Developer Certificate of Origin](https://developercertificate.org/)（開發者原創證書）
——這是在證明你自己撰寫了這項貢獻，或以其他方式擁有依本專案授權條款提交它的權利。使用以下方式簽署：

```bash
git commit -s
```

這會使用你設定的 git 身分，附加一行
`Signed-off-by: Your Name <your.email@example.com>`。含有未簽署 commit 的 PR，會被要求在合併前補簽。

**目前階段沒有另外的 Contributor License Agreement（CLA，貢獻者授權合約）**
——DCO 簽署是唯一的貢獻來源證明要求
（`docs/design/Auspex_ADD.md` §30.7）。這一點只能透過
`CONSTITUTION.md` §3 的 ADR 流程來變更，因為變更貢獻授權模式本身就是一項與架構相關的決策。

## 授權條款（License）

Auspex 採用 Apache-2.0 授權（見 `README.md` 的「Tech stack」表格）。透過貢獻，你同意你的貢獻採用相同條款授權。

## 安全性問題

請不要以一般 issue 或 PR 的形式提報安全性漏洞——私下揭露流程請見
[`SECURITY.md`](SECURITY.md)。

## 行為規範

參與本專案須遵守
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md)。

## 治理

關於維護者決策、ADR 接受流程，以及發布授權如何運作，請見
[`GOVERNANCE.md`](GOVERNANCE.md)。
