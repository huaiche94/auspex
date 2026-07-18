# foundation — 進度產出物

> 🌐 [English](foundation.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

## 交接紀錄（Constitution §6.7 / agents/foundation.md「Handoff」）

- **DB 建構子**：`sqlite.Open(ctx, path) (*DB, error)`
  （`internal/storage/sqlite/db.go`，foundation-05）。會開啟（若不存在則建立）
  一個透過 `modernc.org/sqlite` 存取的 SQLite 檔案，並套用 Auspex 固定的
  pragma 設定（ADD §12.1）——同時透過 DSN 的 `_pragma` query 參數（讓連線池中
  每個連線都會套用到）以及在開啟時明確執行一次 `applyPragmas`（雙重保險，
  避免不同 driver 對 DSN 解析行為不一致而出問題）。`path == ":memory:"`
  透過共享快取（shared-cache）DSN 支援測試情境使用。`Open` 本身不會建立
  schema 或執行 migration——需另外呼叫 `DB.Migrate`。
- **Transaction API**：`(*DB).WithTx(ctx, app.TxFunc) error` 精確實作了
  已凍結的 `app.TxRunner` port（在 `db.go` 中以
  `var _ app.TxRunner = (*DB)(nil)` 做編譯期斷言）。由於 `app.TxFunc` 的型別是
  `func(ctx context.Context) error`——它並不會直接收到 `*sql.Tx` 參數，而
  `internal/app/ports.go` 已凍結、foundation 無權變更——因此 `WithTx` 會把當下
  作用中的 `*sql.Tx` 存進傳給 `fn` 的 `ctx` 裡（透過一個未匯出的 `txKey{}`
  context key）。在 `TxFunc` 閉包內的呼叫端可透過 `sqlite.QuerierFromContext(ctx, db)`
  取得它：若是在 `WithTx` 內呼叫，會回傳當下作用中的 `*sql.Tx`；否則回傳
  `db` 本身的 `*sql.DB` connection pool——如此一來，儲存層程式碼只需針對
  `Querier` 介面（`ExecContext`/`QueryContext`/`QueryRowContext`）撰寫一次，
  不論是否處於交易中都能運作一致。**任何在此之上實作 store 的角色（例如
  `checkpoint`、`predictor`）都應遵循這個 `QuerierFromContext(ctx, db)`
  的模式，而不是把 `*sql.Tx` 穿線帶入自己的函式簽章。**
- **Migration 命名慣例**：`NNNN_name.sql`（4 位以上零填補數字、底線、
  符合 `[a-zA-Z0-9_]+` 的名稱、`.sql` 副檔名），由 `migrate.go` 的
  `migrationFilePattern` 正規表示式與 `LoadMigrationsFS` 強制檢查，並與
  `CONTRACT_FREEZE.md` 中 foundation 的編號範圍（0000-0009）以及 ADD
  §12.5 的 `0001_name.sql` 範例一致。`LoadMigrationsFS(fsys fs.FS, root
  string)` 會讀取 `root` 底下所有直接存在的 `*.sql` 檔案（通常是對
  `internal/storage/sqlite/migrations` 做的 `go:embed`），解析檔名後回傳依
  版本號遞增排序的 `[]Migration`——版本號重複或檔名格式錯誤都屬於硬性錯誤
  （fail-closed，而非略過並警告）。`(*DB).Migrate(ctx, migrations)` 會套用
  所有版本號高於資料庫目前已套用最高版本的 migration，每個 migration 都在
  自己的交易中執行，並記錄到自動建立的 `schema_migrations(version, name,
  applied_at)` 資料表中。若資料庫目前的版本「高於」傳入 migration 集合中的
  任何版本（代表這個執行檔比上次遷移過這個資料庫的版本還舊），`Migrate`
  會回傳 `ErrSchemaNewerThanBinary` 且不套用任何東西——呼叫端「必須」依
  ADD §12.5 將此視為 fail-closed／唯讀狀態處理。**目前尚未存在任何 migration
  `.sql` 檔案**——那是 foundation-06 的工作，明確排除在 foundation-05 的範圍
  之外；針對空的／nil migration slice 呼叫 `Migrate` 已有測試涵蓋，行為是
  no-op，但仍會建立 `schema_migrations` 資料表。
- **依賴套件需求**：目前沒有未處理的需求。`go.mod` 現在包含
  `github.com/google/uuid`（UUIDv7 IDGenerator 用）、`github.com/spf13/cobra`
  （CLI 用），以及 `go.yaml.in/yaml/v3`（YAML 設定檔載入用——在 foundation-03
  中從 cobra 的間接依賴提升為直接依賴；API 介面與常見的 `gopkg.in/yaml.v3`
  相同）。截至此 commit，沒有其他角色透過各自的 progress artifact 提出新的
  依賴套件需求。
- **設定檔優先序／合併演算法**（foundation-03）：`internal/config.Load`
  接收一個未排序的 `Layer{Source, Bytes}` slice，並依照 ADD §26.1 所定的
  固定順序（defaults < global_user_config < repo_config < repo_local <
  environment < cli_flags）合併，不論呼叫端傳入的順序為何。合併方式是
  「淺層」的頂層 key 覆蓋，而非遞迴式的深度合併——目前沒有任何區塊已有
  具型別、實際被使用的結構可供設計深度合併演算法。`Config.Raw` 是通用的
  解碼後 map；需要特定區塊的角色，等到有真正的使用端時再自行解碼。
  `schema_version` 必須等於 `auspex.config.v1`，否則 Load 會回傳錯誤。
  未知的頂層欄位預設只會產生警告，並收集到 `Config.UnknownFields` 中；
  設定 `Options.UnknownFieldPolicy = StrictUnknownFields` 可以改為讓 Load
  直接回傳錯誤（ADD §26.2）。

## 節點紀錄

```yaml
node: foundation-01
status: completed
artifacts:
  - cmd/auspex/main.go
  - cmd/auspex/main_test.go
  - internal/buildinfo/buildinfo.go
  - internal/clock/clock.go
  - internal/clock/clock_test.go
  - internal/idgen/idgen.go
  - internal/idgen/idgen_test.go
  - go.mod
  - go.sum
validation:
  - "gofmt -l cmd internal/buildinfo internal/clock internal/idgen   # empty output"
  - "go build ./cmd/auspex/... ./internal/buildinfo/... ./internal/clock/... ./internal/idgen/..."
  - "go vet ./cmd/auspex/... ./internal/buildinfo/... ./internal/clock/... ./internal/idgen/..."
  - "go test ./internal/clock/... ./internal/idgen/... ./cmd/auspex/... -v   # all PASS"
  - "go build -o auspex ./cmd/auspex && ./auspex version   # prints 0.0.0-dev"
commit: 797c450
next_action: foundation-02（受阻——本波次尚未開始；跨作業系統正確的 config/data/cache/runtime 路徑）
assumptions:
  - "internal/buildinfo.Version 目前是寫死的 \"0.0.0-dev\" 字串常數，
    尚未串接 ldflags／git describe。agents/foundation.md 明確允許
    foundation-01 這樣做（\"a hardcoded ... is fine ... do not
    over-build\"）；正式的發版版本管理在 release packaging 之前都不在
    範圍內（agents/foundation.md 中也明確排除在外）。"
  - "UUIDv7 透過 github.com/google/uuid 的 NewV7() 產生，符合
    CONTRACT_FREEZE.md 中「UUIDv7 at generation time（由 foundation 的
    internal/idgen 負責）」的要求。並未評估其他 UUIDv7 函式庫，因為
    google/uuid 事實上已是 Go 生態系的標準 UUID 套件，且已滿足此需求。"
  - "依任務指示（\"a stub auspex version command using Cobra now is
    preferable\"）選用 Cobra，而非單純以 flag 寫一個 stub；這也與
    Auspex_ADD.md 技術棧表格一致（CLI：Cobra，約在第 192 行）。"
  - "即使 agents/foundation.md 的 Required Tests 清單只抽象地提到
    「version command」而未指定檔案路徑，仍新增了
    cmd/auspex/main_test.go——視為已滿足該要求，且不需要額外新增獨立的
    internal/cli 套件，因為真正的 CLI 介面之後是由 runtime（而非
    foundation）負責（依 DAG 的 runtime-b01，屬於 internal/cli）。"
blockers: []
```

```yaml
node: foundation-02
status: completed
artifacts:
  - internal/paths/paths.go
  - internal/paths/env.go
  - internal/paths/fake_env_test.go
  - internal/paths/paths_test.go
validation:
  - "gofmt -l internal/paths   # empty output"
  - "go build ./internal/paths/..."
  - "go vet ./internal/paths/..."
  - "go test ./internal/paths/...   # PASS, 10 test cases"
commit: 2820015
next_action: foundation-03（YAML 設定檔載入與優先序）
assumptions:
  - "internal/paths 只解析「全域」、非 repository-local 的目錄
    （使用者家目錄或作業系統慣例下的 config/data/cache/runtime）。
    Repository-local 的 .auspex/config.yaml、.auspex/*.db、
    .auspex/checkpoints/、.auspex/runtime/（ADD §26.3）並不由這個套件
    解析——那些是相對於某個 repository root 的路徑，屬於另一個角色的
    職責（repository scoping 依 agents/foundation.md 不在 foundation
    的範圍內）。這個套件提供的是 ADD §26.1 優先序鏈中所謂「global user
    config」這一層，以及未來 repository-scoping 輔助函式可以把 repo
    路徑拼接上去的基底目錄。"
  - "Env 是一個只有兩個方法的介面（Getenv、UserHomeDir），只涵蓋路徑
    解析所需要的部分——刻意不做成完整的 os.Getenv/os.Environ 包裝，
    以避免違反 Constitution §4 對 God-interface 的警告（比照
    provider-interface 規則的類推）。"
  - "Linux 以及所有其他非 darwin／非 windows 的 GOOS 值，都透過同一份
    共用的 XDG Base Directory 實作解析（XDG_CONFIG_HOME／
    XDG_DATA_HOME／XDG_CACHE_HOME／XDG_RUNTIME_DIR，並附有文件化的
    fallback），因為 Auspex 的可攜性目標是「泛 POSIX」而非「僅限
    Linux」，且 ADD 文件中並未特別區分各家 BSD 系統的路徑差異。"
  - "macOS 上並沒有獨立於 XDG_CONFIG_HOME 之外、用來區分 'config' 與
    'data' 的作業系統慣例；兩者都對應到
    ~/Library/Application Support/auspex，與 macOS 上常見的 Go CLI
    慣例一致。macOS 上不存在對應的 XDG_RUNTIME_DIR，Linux 上若
    XDG_RUNTIME_DIR 未設定時也一樣；兩種情況都會 fallback 到 cache
    目錄底下的 `run/` 子目錄。"
  - "Windows 路徑是透過一個小型的 winJoin 輔助函式以字面上的反斜線
    拼接，而非使用 filepath.Join/path.Join——filepath.Join 的分隔符號
    會跟隨執行環境的 GOOS（在 macOS/Linux CI runner 上執行
    Windows-path-table 測試時，會悄悄產生正斜線），而 path.Join 則是
    寫死使用正斜線。這是在這台 darwin 開發主機上第一次執行必要的
    Windows/macOS/Linux path-table 測試（agents/foundation.md 的
    「Required tests」）時失敗才發現的問題，正好對應到 DAG 中
    foundation-02 風險提示（\"Windows path behavior needs CI matrix\"）
    所預期的情境——最後不需要 CI matrix 就解決了，因為測試是假造
    （fake）GOOS 輸入，而不是依賴主機實際的作業系統。"
  - "目前尚未引入任何 AUSPEX_* 環境變數慣例來覆寫這些目錄（例如尚無
    AUSPEX_CONFIG_DIR）——ADD 與 CONTRACT_FREEZE.md 都沒有指定這樣的
    慣例，現在自行發明會是 agents/foundation.md 所引用 Constitution
    §7 rule 10（不要為了「之後的里程碑可能需要、但目前這個不需要」的
    情境預先做抽象）所警告的投機性介面。CLI flag／環境變數在設定檔
    優先序層級的覆寫，是 foundation-03（ADD §26.1）的職責，不屬於
    這個套件。"
blockers: []
```

```yaml
node: foundation-03
status: completed
artifacts:
  - internal/config/config.go
  - internal/config/errors.go
  - internal/config/config_test.go
  - go.mod (go.yaml.in/yaml/v3 promoted to direct dependency)
  - go.sum
validation:
  - "gofmt -l internal/config   # empty output"
  - "go build ./internal/config/..."
  - "go vet ./internal/config/..."
  - "go test ./internal/config/...   # PASS, 15 test cases"
commit: 0164673
next_action: foundation-04，縮減範圍（僅 internal/lock）
assumptions:
  - "刻意不將 ADD §26.4 完整的預設設定樹（runtime/privacy/prediction/
    risk/state_checkpointing/repository_checkpoint/graceful_pause）
    建模成有型別的 Go struct。截至此節點，這個 repository 裡沒有任何
    套件會讀取任何一個設定欄位——predictor/policy/checkpoint/runtime
    的商業邏輯都還不存在，且明確不在 foundation 的範圍內。為完全沒有
    使用端的欄位發明有型別的 struct，會違反 Constitution §7 rule 10。
    因此 Config.Raw 是通用的解碼後 map；ADD §26.4 中的區塊名稱只被
    註冊在 knownTopLevelFields 裡，「僅」用於偵測未知欄位（讓 ADD 中
    真實記載的區塊名稱不會被誤判為未知欄位），並不驗證任何區塊內部的
    結構。之後真正需要使用某個區塊的角色（例如 predictor 讀取
    `prediction:`）屆時再自行把 Raw[\"prediction\"] 解碼成自己的
    型別化 struct——這個套件不會預先猜測那個結構長什麼樣子。"
  - "合併語意是「淺層」的頂層 key 覆蓋，而非遞迴式的深度合併。舉例來說：
    若 defaults 設定 `runtime: {a: 1, b: 2}`，而 repo_config 設定
    `runtime: {a: 9}`，合併結果會是 `runtime: {a: 9}`——b 會被捨棄，
    不會保留——因為 repo_config 的 `runtime` key 會完全取代 defaults 的
    `runtime` key。這是一個真實存在、已記錄在案的限制，而非疏漏：目前
    沒有任何區塊有具體、有實際使用端的結構可供設計並測試正確的深度合併
    演算法，貿然投機性地實作一個，反而有可能對一個根本還不存在的結構
    做出錯誤的合併語意。依 Constitution §4.4 在此標註，讓未來的角色
    （或 contract-integrator）在某個區塊真的需要深度合併時可以明確
    提出需求。"
  - "go.yaml.in/yaml/v3（而非 gopkg.in/yaml.v3）原本就已經是
    spf13/cobra 的間接依賴，這次透過 `go mod tidy` 將它提升為直接的
    頂層依賴，而不是另外加入第二個互相競爭的 YAML 函式庫。它是持續
    維護中的後繼者，API 介面與 gopkg.in/yaml.v3 相容（相同的套件名稱
    `yaml`，相同的 Marshal/Unmarshal 簽章）。"
  - "LoadFile 把檔案不存在視為空的 Layer（而非錯誤），符合 ADD §26.1：
    CLI flags／環境變數以下的每一層（global user config、repo config、
    repo local config）都是選用的。若呼叫端需要區分「檔案本來就合理地
    不存在」與「路徑寫錯」，應在呼叫 LoadFile 之前自行處理；這個套件
    不會猜測呼叫端的意圖。"
  - "並未加入任何 CLI 串接（`auspex config show/validate`）——ADD 的
    CLI/API 章節把這些指令歸在 `runtime` 的職責之下
    （agents/foundation.md：「runtime 角色負責面向使用者的指令」），
    而 foundation-03 在 DAG 中的範圍僅限於載入／優先序這個函式庫本身。"
blockers: []
```

```yaml
node: foundation-04
scope_note: >
  依任務指示縮減範圍。DAG 原本 foundation-04 這一列
  （"Clock/IDGen/lock impls"）只有一部分是這個節點的工作：clock 與
  idgen 已經在 Phase 1 的 foundation-01 中完整實作（見上方該節點的紀錄），
  這裡不會再去動它們或重新實作。這個節點實際的新工作只有 internal/lock。
status: completed
artifacts:
  - internal/lock/lock.go
  - internal/lock/process_unix.go
  - internal/lock/process_windows.go
  - internal/lock/lock_test.go
validation:
  - "gofmt -l internal/lock   # empty output"
  - "go build ./internal/lock/..."
  - "go vet ./internal/lock/..."
  - "go test ./internal/lock/...   # PASS, 8 test cases"
commit: 1ce3c50
next_action: foundation-05（SQLite engine）
assumptions:
  - "internal/domain 或 internal/app/ports.go 中都沒有任何已凍結的
    Lock 介面／型別（在開始這個節點之前已用 grep 確認過）——確切的鎖定
    機制依任務指示明確交由 foundation 自行決定（'your call on the exact
    mechanism'）。選擇了 PID-file 風格的 advisory lock（以 os.O_EXCL
    做互斥建立 + 檔案內容存 PID），而非作業系統層級的 flock/LockFileEx
    系統呼叫，原因是：(1) 不需要任何新的依賴（不需要
    golang.org/x/sys）；(2) Auspex_ADD.md SS1.4 把 runtime 架構定為
    單機的「modular monolith」，因此只需要同機互斥，不需要跨網路互斥；
    以及 (3) 這個設計在當機後可以輕易復原（見下一則），這對一個隨時
    可能在 turn 中途被砍掉的本機 daemon 來說，比嚴格的核心層強制互斥
    更重要。"
  - "陳舊鎖（stale-lock）復原：如果鎖檔存在，但其中記錄的 PID 對應的
    行程並非存活中的行程，Acquire 會將其視為遺留物（例如某個已當機的
    daemon 留下來的），將其移除後重新取得，而不是讓機器永久卡死。這是
    刻意設計的當機安全特性，並非削弱鎖的保證——純核心層的 flock 本來就
    會免費提供這個效果（持有鎖的行程死掉時鎖會自動釋放），但選用的
    O_EXCL 設計需要明確自行實作這個行為，因為單純的檔案存在與否，在
    當機後是會持續存在的。"
  - "processAlive 是平台相依的（透過 build tag 分成 process_unix.go /
    process_windows.go），因為 POSIX 與 Windows 判斷行程是否存活的
    基本手法完全不同：POSIX 使用 null signal（kill(pid, 0)），因為
    os.FindProcess 在 POSIX 上永遠不會失敗；Windows 的 os.FindProcess
    則會真的去開啟一個行程 handle，若該 PID 不存在就會失敗，因此在
    Windows 上光看是否成功就足夠了。這兩個檔案在這台 darwin 開發主機上
    透過 !windows/windows 的 build constraint 都能正常編譯；windows
    標籤那個檔案的正確性，則是依據 Go 標準函式庫文件中記載的行為
    （已對照 Go 的 os 套件文件確認），因為在沒有 Windows CI matrix
    （`qa-01`）的情況下，無法在這台 darwin 主機上實際跑一遍。"
  - "internal/lock 刻意不與 internal/storage/sqlite（foundation-05，
    尚未建置）整合，也不會把實際的 daemon 單一實例保護串接進
    cmd/auspex——那樣的串接屬於未來真正啟動長駐 daemon 行程的節點
    （runtime 角色，依 agents/foundation.md 不在 foundation 範圍內）。
    這個節點只交付可重複使用的基礎元件本身。"
blockers: []
```

```yaml
node: foundation-05
status: completed
artifacts:
  - internal/storage/sqlite/db.go
  - internal/storage/sqlite/migrate.go
  - internal/storage/sqlite/db_test.go
  - internal/storage/sqlite/migrate_test.go
  - go.mod (modernc.org/sqlite promoted to direct dependency)
  - go.sum
validation:
  - "gofmt -l internal/storage/sqlite   # empty output"
  - "go build ./internal/storage/sqlite/..."
  - "go vet ./internal/storage/sqlite/..."
  - "go test ./internal/storage/sqlite/...   # PASS, 24 test cases"
commit: b0ef5a0
next_action: foundation-09（Makefile/Taskfile/lint 設定）——foundation-06
  （實際的 migration .sql 檔案）明確不在本波次範圍內
assumptions:
  - "Pragma 設定（ADD SS12.1 / CONTRACT_FREEZE.md）刻意套用了「兩次」，
    這是設計如此，不是意外重複：(1) 依 modernc.org/sqlite 文件記載的
    DSN 慣例，把設定編碼成每次 Open() 呼叫時 DSN 上的 _pragma query
    參數，讓連線池之後開出的每個連線都會自動套用；「以及」(2) 在
    Open 之後，立刻透過 applyPragmas 明確對連線池的第一個連線再執行
    一次。考量到這個節點在 EXECUTION_DAG.md 中被標記為高風險
    （'WAL/busy-timeout/FK pragmas are load-bearing for every later
    role'），這是雙重保險——並以實際查詢每個 pragma 當下真實生效值
    （例如 PRAGMA journal_mode）的測試來驗證，而不只是確認 Open()
    沒有回傳錯誤，依任務指示明確警告不可抄這個捷徑。"
  - "TestBusyTimeout_ConcurrentWriteWaitsInsteadOfFailingImmediately
    驗證的是 busy_timeout pragma「實際」的效果（第二個寫入者會在一個
    尚未 commit 的交易後面等待，並在該交易 commit 後成功，而不是立即
    以 SQLITE_BUSY 失敗），而不只是斷言 pragma 回報的設定值——這正是
    agents/foundation.md 要求的「locked/busy behavior」測試。"
  - "app.TxFunc 已凍結的函式簽章（func(ctx context.Context) error，
    沒有 *sql.Tx 參數）迫使我們採用以 context 傳遞交易的設計：WithTx
    把當下作用中的 *sql.Tx 存進一個未匯出的 context key，呼叫端透過
    新增的 QuerierFromContext(ctx, db) 輔助函式取得。這並非可以自由
    選擇的設計——internal/app/ports.go 已凍結，且僅由
    contract-integrator 擁有（Constitution SS4.3），所以即使『直接把
    tx 傳進去』的設計會更簡單，foundation 也無法替 TxFunc 加上
    *sql.Tx 參數。這個模式（QuerierFromContext）是之後每個角色的
    store 為了與已凍結的 WithTx 邊界相容，都『必須』採用的做法——已在
    上方的交接紀錄段落中記載，並特別提醒
    checkpoint/predictor/claude-provider/runtime，不論最後是哪個角色
    在這個 engine 之上寫出第一個真正的 store。"
  - "modernc.org/sqlite（純 Go，不使用 CGO）是依 Auspex_ADD.md SS1.4
    明確的技術棧決策加入的，正如任務指示中預先授權的一樣。go mod tidy
    因此拉入了相當可觀的間接依賴樹（modernc.org/libc、cc/v4、ccgo/v4
    等）——這些都是這個 driver「純 Go 轉譯 C」做法下的標準附帶產物，
    並非 foundation 自己的設計選擇；由於 ADD 已明確指名這個 driver，
    並未評估其他替代方案。"
  - "LoadMigrationsFS 刻意設計得很嚴格：檔名不符合 NNNN_name.sql、
    或兩個檔案宣稱同一個版本號，都屬於硬性錯誤，而不是略過並警告。
    依 CONTRACT_FREEZE.md 對狀態完整性失敗（相對於可以 fail-open 的
    operational-observation 類失敗）所定的 fail-closed 規則，悄悄
    跳過或悄悄重新排序 migration 屬於狀態完整性的 bug，因此這裡選擇
    寧可拒絕繼續執行，也不去猜測意圖。"
  - "這裡並未建立 internal/storage/sqlite/migrations/ 目錄，也沒有
    建立任何實際的 .sql 檔案——那是 foundation-06 的範圍（核心
    migration 0000-0009），依任務指示明確「不」指派給 foundation
    這一波。Migrate()/LoadMigrationsFS() 已完整實作，並針對測試中
    合成出來的 migration（透過 testing/fstest 與內嵌的 Migration
    struct）做過完整測試，因此 foundation-06 有一個現成、已驗證過的
    engine 可以直接指向真正的 .sql 檔案，不需要再改動 engine 本身。"
  - "SetMaxOpenConns(8) 是一個保守、ADD 文件中並未記載的預設值——
    目前還沒有任何後續角色真實的併發需求可以拿來調校，ADD 也沒有指定
    連線池大小。在此標註為一項假設，等日後真正出現 daemon+CLI+scheduler
    同時存取的併發模式時，可能需要重新檢視。"
blockers: []
```

```yaml
node: foundation-09
status: completed
artifacts:
  - Taskfile.yml
  - Makefile
  - .golangci.yml
  - internal/lock/lock.go (lint fix: nilerr nolint annotation)
  - internal/lock/lock_test.go (lint fix: check Release() error in defers)
  - internal/storage/sqlite/db.go (lint fix: errorlint %w for rollback error)
  - internal/storage/sqlite/db_test.go (lint fix: check Close() error in
    defers; errors.Is instead of == for sentinel comparison)
  - internal/storage/sqlite/migrate.go (lint fix: errorlint %w for
    strconv error)
  - internal/storage/sqlite/migrate_test.go (lint fix: check Close()
    error in defers)
  - internal/clock/clock_test.go (lint fix: staticcheck QF1011 nolint
    annotation, assertion intentionally kept explicit)
  - internal/idgen/idgen_test.go (same as above)
validation:
  - "task lint   # go vet + golangci-lint run ./... -> 0 issues"
  - "task build   # go build -o bin/auspex ./cmd/auspex; ./bin/auspex version -> 0.0.0-dev"
  - "task test    # go test -race ./... -> all packages PASS"
  - "make lint && make build   # Makefile mirror verified equivalent to Taskfile"
commit: 2eac579
next_action: 無——這是本波次指派的最後一個節點（foundation-06、-07、-08
  明確不在範圍內；依任務指示 STOP）
assumptions:
  - "Taskfile.yml 是較完整／主要的工作執行器（預設 task 會執行
    fmt+lint+test；另外還有各自獨立的 fmt/fmt:fix、test/test:short、
    tidy、clean、run 這些 target）；Makefile 則是刻意做成一個精簡、
    不依賴任何額外工具的鏡像版本，涵蓋相同的一組 target，供只有
    `make` 可用的貢獻者／CI 步驟使用。agents/foundation.md 的
    exclusive-paths glob 兩個檔案都有列出，但沒有說哪個是主要的，因此
    讓兩者行為保持一致（而非分歧）是最安全的解讀方式，符合任務指示中
    「both files should exist and be consistent」的要求。"
  - "這台開發機上原本並未預先安裝 `task`（go-task/task）或
    `golangci-lint`；`brew install` 失敗（Xcode Command Line Tools
    版本過舊，擋住了 bottle 安裝，與這個 repo 無關）。改為透過
    `go install .../cmd/task@latest` 與
    `go install .../cmd/golangci-lint@latest` 安裝到
    $(go env GOPATH)/bin——這「不會」動到這個 repository 自己的
    go.mod/go.sum（用 `go install` 安裝另一個 module 的工具，與目前
    module 的依賴圖是獨立的），而且這兩個工具之後真的被拿來實際執行、
    驗證 `task lint` / `task build`，而不只是寫好就假設沒問題。"
  - ".golangci.yml 採用的是 golangci-lint v2 的設定 schema（頂層
    `version: \"2\"` 欄位；linters.default 使用標準集合再加上明確的
    enable 清單；formatters 區塊給 gofmt/goimports 用），因為透過
    `go install .../golangci-lint/v2/cmd/golangci-lint@latest` 裝進來
    的正是 v2，而驗證過程中實際執行的也是這個版本——用舊版 v1 schema
    的設定檔，就不會真的被一次真實的工具執行驗證過。"
  - "執行 golangci-lint 找出了 16 個真實、可修正的問題，散落在「這一波」
    較早節點（foundation-02 到 -05）的檔案裡，另外還有兩個來自
    foundation-01（Phase 1）：defer 呼叫的 Close()/Release() 沒有檢查
    回傳的錯誤（errcheck）、該用 %w 包裝錯誤卻用了 %v（errorlint）、
    對某個 sentinel error 直接用 == 比較而非 errors.Is（errorlint）、
    internal/lock 中一個刻意為之、但會讓 linter 起疑的「非 nil err
    檢查後回傳 nil」寫法（nilerr，已用附說明的 nolint 抑制，因為這個
    行為是刻意的，不是 bug），以及 Phase 1 測試檔案中兩個 staticcheck
    QF1011 提示，指出某個變數宣告上明確標注的介面型別，其實跟右側
    函式回傳型別重複（同樣以 nolint 抑制，因為明確標注型別是為了測試
    的說明價值，不是疏漏）。這些問題全部都實際修正，而不是在 linter
    設定層級一次性全部壓下去，因為 agents/foundation.md 的驗證門檻是
    `task lint` 要乾淨通過，而不是用一份寬鬆的設定把真正的訊號蓋掉。"
  - "即使 LICENSE 與 NOTICE 檔案都列在 foundation 的 exclusive-paths
    清單中、且目前這個 repository 完全沒有這兩個檔案，這裡也沒有新增
    它們。這個任務的節點清單（foundation-02 到 foundation-09）從未把
    建立 LICENSE/NOTICE 指派給這一波的任何節點，foundation-09 自己的
    驗證指令（`task lint && task build`）也不依賴它們存在。現在建立
    它們會是超出所指派節點範圍的 scope creep，而不是
    Makefile/Taskfile/.golangci.yml 工作自然而然的附帶產物——在此標註，
    讓未來某一波明確指派這項工作（依 exclusive-paths 清單，負責角色是
    foundation），而不是被默默完成、或默默遺忘。"
  - "bin/（task build/make build 建立出來的建置產物目錄）並未提交，
    在驗證後就刪除了；也沒有為它新增 .gitignore 項目，因為 .gitignore
    不在 foundation 宣告的 exclusive paths 清單中——在此標註為一個
    缺口，留給其他角色或未來的 foundation 節點視需要補上，這裡不主動
    修，以免動到不屬於這個角色所有權的檔案。"
blockers: []
```

## Phase 3

### foundation-06：SQLite 核心 schema migration（0001-0004）

範圍依 `agents/foundation.md` 交付項 6 與 `EXECUTION_DAG.md` 中
foundation-06 這一列：把 `Auspex_ADD.md` §12.2 標準的邏輯 schema 中，
之後每個角色的 migration 範圍都會以外鍵（FK）參照到的四張核心資料表——
`repositories`、`worktrees`、`provider_sessions`、`tasks`——逐字（逐欄位）
轉寫成 `internal/storage/sqlite/migrations/` 底下只能向前套用的 `.sql`
檔案，並串接 `migrate.go` 既有的 `LoadMigrationsFS`/`Migrate` engine
（foundation-05），透過新增的 `embed.FS` + `AllMigrations()` 函式，讓它
真正載入並套用這些檔案。

這裡明確「不」建立的資料表（依 `CONTRACT_FREEZE.md` 的 migration 範圍表，
不在這個範圍內）：`turns`/`turn_usage`/`quota_observations`/
`context_observations`（claude-provider，0010-0019）、
`progress_nodes`/`progress_edges`/`artifacts`/`state_checkpoints`
（checkpoint Part A，0020-0029）、
`repository_snapshots`/`file_changes`/`repository_checkpoints`
（checkpoint Part B，0030-0039）、
`feature_vectors`/`predictions`/`runway_forecasts`/`policy_decisions`/
`authorizations`（predictor，0040-0049）、
`pause_records`/`wake_jobs`/`resume_attempts`/`events`
（runtime Part A，0050-0059）。

```yaml
node: foundation-06
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0001_repositories.sql
  - internal/storage/sqlite/migrations/0002_worktrees.sql
  - internal/storage/sqlite/migrations/0003_provider_sessions.sql
  - internal/storage/sqlite/migrations/0004_tasks.sql
  - internal/storage/sqlite/migrate.go (added migrationsFS embed.FS +
    AllMigrations() — the real loader every later role's DB-open path and
    integration tests should call instead of hand-rolling an fs.FS)
  - internal/storage/sqlite/migrate_test.go (added TestAllMigrations_* and
    TestCoreMigrations_* covering load, apply-from-empty, idempotent
    reopen, FK cascade/set-null behavior, and unique constraints against
    the real embedded files, not just synthetic in-memory Migration
    values)
validation:
  - "go test ./internal/storage/sqlite/... -run Migration -> PASS (18 tests
    matched, all passing, including new TestAllMigrations_* /
    TestCoreMigrations_* tests)"
  - "go test ./internal/storage/sqlite/... -race -> PASS, all packages"
  - "go build ./... -> clean"
  - "go vet ./... -> clean"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
commit: b79df6b
next_action: foundation-08（路徑／設定檔優先序測試）——foundation-07
  依任務指示明確不在本波次範圍內
assumptions:
  - "Migration 編號從 0001 開始，而非 0000，符合 ADD §12.5 文件中明確
    記載的範例（\"Migration file: 0001_name.sql\"），也符合這份檔案自己
    先前既有的交接紀錄。這不是隨意的風格選擇——見下面因此浮現出來的
    真實 bug。"
  - "找到並繞過了一個真實的 BUG（並未在 migrate.go 中修正）：
    foundation-05 的 migration engine，其 `Migrate()` 把
    `currentVersion() == 0` 當作「目前尚未套用任何 migration」的哨兵值，
    並以 `m.Version <= current` 判斷某個 migration 是否已經套用過。
    因此在一個全新的資料庫上，`Version: 0` 的 migration 會滿足
    `0 <= 0`，被悄悄視為「已套用」——直接被「跳過」，不會被執行——而且
    不會回傳任何錯誤。我一開始把這四個 migration 編號為 0000-0003
    （對照這個範圍自己顯示慣例上寫的「0000-0009」），結果正好踩中這個
    問題：`repositories` 資料表（版本 0）悄悄地永遠不會被建立，但
    `CurrentVersion()` 卻仍然回報已套用 4 個 migration，也從未回傳
    任何錯誤。之所以能發現這個問題，只是因為
    TestCoreMigrations_FromEmptyDatabase 斷言的是該資料表「實際存在」
    （透過查詢 sqlite_master），而不只是斷言 CurrentVersion 有沒有
    前進。修正方式是重新編號為 0001-0004，這也正是 ADD §12.5 本來就
    要求的編號方式，所以這是符合規格的修正，而不是為了方便而繞過去的
    workaround。刻意「沒有」去改動 migrate.go 的哨兵語意（例如改用 -1
    或一個布林值來表示「尚未套用任何 migration」），即使那樣做同樣能
    修好這個問題，原因是：(a) migrate.go 是一個共用、已經過
    foundation-05 測試驗證的檔案，其他角色可能已經在依賴它目前記載的
    行為；(b) ADD 自己的慣例本來就意味著版本 0 在真實的 migration
    檔案中根本不應該出現，因此這個潛藏的 bug，依照文件記載的命名規則，
    現在已經變成不可能被觸發，而不是被「patch 掉」；以及 (c) 在沒有
    任何跡象顯示其他角色真的需要版本 0 的情況下，去改動已接近凍結的
    engine 語意，感覺會超出這個節點的範圍。在此標註，供未來若有某個
    角色的 migration 範圍，一時被誘惑想從自己範圍的零邊界開始編號
    （例如把某個未來的範圍 \"0020-0029\" 理解成範圍內編號從 0 開始）時
    參考——不要這樣做；永遠依 CONTRACT_FREEZE.md 文件記載的下界，從
    範圍中第一個檔案該有的編號開始（0001、0020、0030、0040、0050），
    絕對不要從單純的 0 開始。"
  - "tasks 上的 active_node_id（0004_tasks.sql）刻意沒有 FK
    constraint：因為它要參照的 progress_nodes，要到 checkpoint 的
    0020-0029 範圍才會存在，而 SQLite 無法對一個尚未存在的資料表加上
    向前參照。這一點已記錄在該 migration 檔案自己的檔頭註解中，這樣
    checkpoint-a01 就不需要透過重新閱讀 migrate 的歷史紀錄才能發現
    這件事。"
  - "provider_sessions.metadata_json 以及 tasks 其他帶有 DEFAULT 的
    欄位，都完全依照 ADD §12.2 的規格逐字轉寫（包括 DEFAULT '{}' 與
    DEFAULT 0），而不是簡化過的版本，因為其他角色的 migration（例如
    claude-provider 要寫入 provider_sessions 資料列、checkpoint 要讀取
    tasks.active_node_id）都依賴這個確切、已凍結的結構，而不是
    foundation 自行簡化過的近似版本。"
  - "ADD §12.3 中，那些只間接參照 foundation 所擁有資料表的索引
    （§12.3 列出的索引沒有一個是「純粹」只建立在
    repositories/worktrees/provider_sessions/tasks 上的——它們都同時
    參照 turns、progress_nodes、quota_observations 等屬於後續角色的
    資料表）並未在這個節點中新增；索引的建立，留給實際擁有被索引之
    資料表的那個角色的 migration 範圍去負責。"
blockers: []
```

### foundation-08：跨套件的路徑／設定檔優先序測試

範圍依 `EXECUTION_DAG.md` 中 foundation-08 這一列與任務指示：把
`internal/paths` 與 `internal/config` 兩者「一起」的優先序測試覆蓋率
加強，而不只是各自套件既有、獨立測試的優先序（`paths_test.go` 的
XDG／環境變數覆寫表格已經涵蓋了 `paths` 自己的優先序；`config_test.go`
的 `TestLoad_Precedence_*` 與
`TestLoad_EndToEnd_FileBackedPrecedenceChain` 已經涵蓋了 `config` 自己
六層的優先序鏈，前提是輸入的 bytes 已經解析好）。這兩個既有的測試組合，
都沒有真正演練過未來 `runtime` 的設定檔指令實際會跑的那條真實流程：先用
`paths.Resolve`（由環境變數驅動）找出全域設定檔實際「放在哪裡」，再把
該解析出來的位置實際讀到的內容，連同其他層一起餵進 `config.Load` 自己的
優先序鏈——也就是把 paths 的「哪個環境變數決定放在『哪裡』」這個軸，與
config 的「哪一層決定是『哪些 bytes』」這個軸組合在一起測試，而不只是
各自獨立證明正確。

```yaml
node: foundation-08
status: completed
artifacts:
  - internal/config/precedence_paths_test.go (new — 3 tests: an
    XDG_CONFIG_HOME override changing which file is actually loaded
    end-to-end through paths.Resolve -> config.LoadFile -> config.Load;
    a paths-resolved global layer still losing to higher-precedence
    config layers (repo_config, environment) exactly as config's own
    precedence rules require, now proven against a real resolved file
    path rather than a hand-built []byte; an unrelated paths env var
    (XDG_RUNTIME_DIR) proven NOT to perturb config's precedence result,
    i.e. the two packages' env-driven axes are independent, not
    accidentally coupled through shared process environment state)
validation:
  - "go test ./internal/paths/... ./internal/config/... -run Precedence ->
    PASS (internal/paths reports \"no tests to run\" under this filter,
    which is correct and expected — paths' own precedence-flavored tests
    are named TestResolve_*_XDGOverrides, not *Precedence*; the filter's
    purpose per the DAG is to select the NEW cross-package tests, which
    live in internal/config and do match)"
  - "go test ./internal/paths/... ./internal/config/... -race -> PASS, all
    tests including pre-existing ones"
  - "go build ./... -> clean"
  - "go vet ./... -> clean"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
commit: 13e05ae
next_action: 無——這是本波次指派的最後一個節點（依任務指示：兩個節點都
  Validated 之後就立刻 STOP；foundation-07 明確不在範圍內，留待 Phase 4
  再決定）
assumptions:
  - "internal/paths 自己的 fakeEnv（fake_env_test.go）並未匯出給
    paths_test 套件之外使用，跨套件邊界無法從 internal/config 的測試
    套件重複使用它；因此改在 precedence_paths_test.go 內部另外定義一個
    小型、滿足已匯出 paths.Env 介面的本地 pathsFakeEnv，而不是把 paths
    的 fake 匯出、或把它提升為一個共用的 testutil 套件——後者會是這一波
    範圍並未要求的新抽象／新套件（Constitution §7 rule 10），而重複
    寫大約 10 行單純的 fake，比這兩種替代方案都更划算。"
  - "這個新測試檔案放在 internal/config/ 底下（foundation 的其中一個
    exclusive path），而不是 internal/paths/ 底下，因為一個跨套件的
    整合測試，概念上應該跟著「使用端」放（未來的設定檔載入呼叫端，是
    使用 paths 的輸出，而不是反過來）——不論放在哪一個目錄，都同樣屬於
    foundation 所有，所以這只是一個不具約束力的擺放選擇，不是合約層級
    的決定。"
  - "config.Load 要求 schema_version 必須出現在「所有層合併之後的
    結果」中（config.go：在所有層都合併完之後才檢查
    `merged[\"schema_version\"]`，而不是逐層檢查）——這是
    foundation-03 就已經存在的 config.go 既有行為，並非 foundation-08
    改動的結果。有一個新測試一開始只寫了一個純粹「覆寫值」的
    global-config fixture（沒有 schema_version 這個 key，比照一份
    真實的全域設定檔在 defaults 已經提供了整個外層結構時，實際上可能
    長的樣子），卻完全沒有包含 defaults 這一層，結果正確地失敗並回傳
    ErrInvalidSchemaVersion——修正方式是額外加上一個只帶有
    schema_version 的明確 defaults 層，這與 config_test.go 自己既有的
    測試中，已經確立的「外層結構通常是透過 defaults 這一層送進來」的
    做法一致，並不是 config.go 的 bug。"
blockers: []
```

## Phase 4

### foundation-07：migration 測試 harness 強化

範圍依 Phase 4 DAG 中 foundation-07 這一列與任務指示：針對
`internal/storage/sqlite` 的 migration 測試 harness，在 foundation-05/06
之外，加強競態（race-condition）與可靠性方面的測試覆蓋率，具體來說是
`agents/foundation.md`「Required tests」清單中三項尚未完全覆蓋到的項目：
**併發存取下**的重新開啟並冪等套用 migration（既有測試只有依序重新開啟）、
**migration 本身執行期間**的 locked/busy 行為（`db_test.go` 中既有的
`TestBusyTimeout_*` 只涵蓋一般的 `INSERT`，沒有涵蓋 `Migrate()`），以及
**專門在 `Migrate()` 執行期間**發生的權限錯誤／資料庫損毀錯誤分類（`db_test.go`
中既有的 `TestOpen_CorruptFile_*` / `TestOpen_UnwritableDirectory_*` 只涵蓋
`Open()` 期間發生的失敗）。

在撰寫「併發重新開啟」測試的過程中，發現了兩個真實、可重現、且屬於這個
角色所擁有程式碼中的 bug——兩者都在同一個節點中修正，因為它們都很小、
在範圍內（`db.go`/`migrate.go`），而且正是「測試 harness 強化」這種節點
存在的目的：在後續角色在其上疊加建置之前，先抓出並解決這類問題：

1. **`Migrate()` 存在 TOCTOU 競態**（foundation-05/06 原本的實作）：它會
   先讀取資料庫目前的版本，然後把各個 migration 當成各自獨立、彼此沒有
   同步的陳述式／交易來套用，因此兩個併發呼叫、針對同一個檔案的
   `Migrate()`，都可能在對方 commit 之前觀察到 `current=0`，兩者都嘗試
   `CREATE TABLE`，最後以「table already exists」失敗。修正方式是讓
   `Migrate()` 保留一個專用的 `*sql.Conn`，在讀取目前版本之前先發出
   `BEGIN IMMEDIATE`，在「讀取版本、套用所有待處理 migration」這整個
   流程中都持有 SQLite 的寫入鎖，最後才 `COMMIT`。現在第二個併發的
   `Migrate()` 呼叫會排在第一個後面等待（受 `busy_timeout` 限制），而不是
   跟它競爭。
2. **`applyPragmas`（`Open()` 的 pragma 初始化）可能在一個全新連線上，
   因為與另一個連線持有的寫入鎖競爭而立刻以 `SQLITE_BUSY` 失敗**（在上面
   那個修正之後最容易被觸發，因為 `Migrate()` 現在會持有真正的寫入鎖
   更長的時間）：一個全新連線的第一個陳述式——包括 `PRAGMA busy_timeout`
   本身——尚未受到任何 busy-wait 保護，因為 `busy_timeout` 要等到「這個
   陳述式本身」成功執行之後才會生效。在 `go test -race -count=20` 下，
   依機器負載不同，大約以 5%-30% 的機率重現。以兩種方式修正：(a) 重新
   排序 `pragmaStatements` 與 DSN 的 `_pragma` 清單，讓 `busy_timeout`
   最先套用（多一層防護——driver 內部本來就會依其自己的 DSN 解析邏輯
   優先處理 `busy_timeout`，這點已透過閱讀 `modernc.org/sqlite` 的原始碼
   確認過，但明確排序不需要任何成本，還能記錄意圖）；(b) 在每一個
   `applyPragmas` 陳述式周圍，加上一個小型、有上限的重試搭配退避
   （`execWithBusyRetry`，最多 10 次嘗試／總共約 500ms，遠低於 5000ms 的
   `busy_timeout`），依一個型別化的 `errors.As` 檢查
   `*modernc.Error.Code() == 5`（SQLITE_BUSY）來判斷，而不是用字串比對。

第三個相關的行為變化（不是 bug 修正，而是把 `Migrate()` 從「每個
migration 各自一個交易」改成「一整個交易」之後很自然的結果）：一批
`[migration N（合法）、migration N+1（不合法）]` 現在失敗時會把「兩個」
migration 都回滾，而舊的「每個 migration 各自交易」設計，則會讓
migration N 永久 commit 下去，即使 `Migrate()` 已經對這整批回傳了
錯誤——這是一種部分套用的 migration 執行結果，正是
`CONTRACT_FREEZE.md` 錯誤合約中，狀態完整性失敗「必須」fail closed、
不能部分成功的那種情況。已由一個新增的回歸測試涵蓋
（`TestMigration_PartialBatchFailure_RollsBackEntireBatch`）。

```yaml
node: foundation-07
status: completed
artifacts:
  - internal/storage/sqlite/migrate.go (Migrate() rewritten to run its
    entire read-current-version-then-apply-all-pending-migrations sequence
    as one BEGIN IMMEDIATE ... COMMIT transaction on a single reserved
    connection, fixing a real concurrent-caller TOCTOU race;
    currentVersionOn(ctx, Querier) extracted so both CurrentVersion's
    plain-pool read and Migrate's in-progress-transaction read share one
    implementation; applyMigration removed, folded into Migrate directly)
  - internal/storage/sqlite/db.go (pragmaStatements/dataSourceName reordered
    busy_timeout-first; applyPragmas now retries transient SQLITE_BUSY via
    new execWithBusyRetry/isBusyError helpers, fixing a real Open()
    bootstrap race under concurrent access)
  - internal/storage/sqlite/migrate_test.go (6 new tests: 2 concurrent-reopen
    tests proving convergence and no duplicate schema_migrations rows under
    N simultaneous Open+Migrate callers; 1 test proving Migrate itself waits
    behind another connection's held write lock rather than failing
    immediately, then succeeds; 1 test proving Migrate fails with a
    classified error against a corrupted database file where Open itself
    still succeeds; 1 test proving the same for a read-only database file;
    1 regression test for the whole-batch-rollback behavior change)
validation:
  - "go test ./internal/storage/sqlite/... -run TestMigration -race -v ->
    PASS, 6 tests matched and passing"
  - "go test ./internal/storage/sqlite/... -run TestMigration -race
    -count=30 -> PASS, no flakes across 180 total test executions (up from
    a ~5-30%-per-run flake rate on the concurrent-reopen test before the
    applyPragmas fix)"
  - "go test ./internal/storage/sqlite/... -race -count=15 -> PASS, full
    package suite (36 tests), no regressions, no flakes"
  - "go test ./... -race -count=3 -> PASS, all packages, whole repo"
  - "go build ./... -> clean"
  - "go vet ./... -> clean"
  - "gofmt -l internal/storage/sqlite -> empty output"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
commit: 042ed54
next_action: 無——foundation-07 是這一波唯一指派的節點；依任務指示，
  Validated 之後就 STOP
assumptions:
  - "選擇實際修正這兩個發現的真實 bug（Migrate 的 TOCTOU 競態、
    applyPragmas 的初始化 SQLITE_BUSY 缺口），而不是只記錄下來，是一個
    判斷取捨，因為任務指示把這個節點的範圍定為『測試 harness 強化』，
    不是『engine 重構』。認定它們屬於範圍內的理由是：(a) 兩個修正都很
    小，而且完全落在 foundation 自己的 exclusive paths 內
    （migrate.go、db.go）；(b) 這兩者正是『競態與可靠性覆蓋』這種節點
    存在的目的所要抓出來、並在後續角色（runtime，負責串接真正的
    daemon 啟動流程）在此 engine 之上建置之前修好的那種潛藏 bug，
    否則後續角色會建立在一個併發存取下會悄悄弄丟 migration 的 engine
    之上；以及 (c) 這也呼應了 foundation-06 已經為同一個檔案立下的
    先例（在節點進行途中發現並修正自己版本 0 哨兵值的 bug，而不只是
    標註出來）——對這個角色來說，這是一貫的做法，不是新創的做法。"
  - "這個 repository 裡目前沒有任何呼叫端把 Migrate() 與 internal/lock
    （foundation-04 的 PID-file advisory lock）串接在一起——在確定這個
    併發問題是一個真實、可觸及的缺口，而不是已經被某個更高層的單一
    實例保護機制擋掉之前，已用 grep 確認過這一點。internal/lock 已經
    存在，但沒有任何 DB-open 路徑會呼叫它；把一個真正的單一實例保護
    串接進真實的 daemon 啟動流程，明確不在 foundation 的範圍內
    （agents/foundation.md：『runtime 角色負責面向使用者的指令』），
    而且 foundation-04 自己的假設中也已經這樣標註過。這代表 Migrate()
    自身內部的併發安全性（這個節點的修正），並不會與其他角色已經提供的
    某個更高層鎖是重複的——截至這個 commit 為止，這是這個程式碼庫中，
    唯一能防止併發 Migrate 造成損毀的保護機制。"
  - "isBusyError 使用 errors.As，對照 modernc.org/sqlite 已匯出的
    *sqlite.Error 型別及其 Code() 方法（在 db.go 的 import 中別名為
    `modernc`，以避免跟這個套件自己的名稱 `sqlite` 撞名），並與一個
    本地命名的 sqliteBusyCode = 5 比較，而不是額外 import
    modernc.org/sqlite/lib 去拿 sqlite3.SQLITE_BUSY 這個常數——頂層的
    modernc.org/sqlite 套件並沒有重新匯出那個常數，而為了一個整數字面值
    去 import 它內部的 lib 套件，感覺是錯誤的依賴方向。SQLITE_BUSY 的
    代碼（5）本來就是 SQLite 自己長期穩定的公開 C API 的一部分
    （https://www.sqlite.org/rescode.html#busy），並非
    modernc.org/sqlite 自己發明的，因此在本地把它寫死成一個具名常數，
    並加上指向來源出處的註解，會比脆弱的錯誤訊息字串比對、或是更重的
    import 都更好。"
  - "execWithBusyRetry 的上限（10 次嘗試 x 50ms = 最多約 500ms）是一個
    判斷取捨，而不是規格值——沒有任何 ADD/CONTRACT_FREEZE.md 的文字，
    針對這個「初始化時特有」的缺口指定過重試預算（busy_timeout 本身的
    5000ms，管的是 pragma 已經生效之後、一般進行中陳述式的爭用，那是
    另一個、已經涵蓋過的情境）。選擇這個值的目的，是讓它舒服地落在
    5000ms 的 busy_timeout 之內，讓 Open() 本身永遠不會變成一個要花上
    好幾秒的異常值，同時又要長到足以讓修正後、在 30 次完整重跑
    （180 次併發重新開啟測試的執行）中，都無法再重現那個原本
    觀察到約 5%-30% 機率的競態。"
  - "測試名稱都以 TestMigration_ 開頭（而不是 foundation-05/06 已經
    用過的 TestMigrate_ 或 TestCoreMigrations_），這樣這一波 DAG 的
    驗證指令（`-run TestMigration`）才能真正選到它們——在寫任何測試
    之前，已先確認過任務中字面上寫的驗證指令，並不會比對到零個既有
    測試（Go 的 -run 是不加錨點的正規表示式，因此單純 `-run Migration`
    本來就會同時比對到全部 28 個既有測試，但任務裡字面上寫的指令是
    `TestMigration`，依慣例視同有前綴錨點，即使就正規表示式本身而言
    並非如此）。這呼應了這份檔案裡 foundation-06／foundation-08 之前
    就已經觀察到的現象：DAG 上寫的驗證指令，與套件實際的測試命名慣例，
    可能會出現落差，值得在寫測試之前先檢查一次，而不是事後才發現。"
  - "『資料庫損毀發生在 Migrate 期間』這個測試
    （TestMigration_CorruptDatabase_FailsDuringMigrateNotOpen）需要一種
    特定的損毀方式，才能真正把『在 Open 期間失敗』與『在 Migrate 期間
    失敗』區分開來：損毀第 1 頁／檔案 header 會觸發 Open() 自己的
    PRAGMA 陳述式（journal_mode 本身就會走訪檔案中足夠多的部分，藉此
    偵測到 header 層級的損毀），這部分已經由 db_test.go 中的
    TestOpen_CorruptFile_FailsOnFirstQuery 涵蓋過了。只有損毀「後面」
    的某一頁（先插入約 2000 筆填充資料列，逼出一個多頁的檔案，再把檔案
    最後 20% 的位元組清零），才能讓 Open() 成功、卻讓 Migrate() 之後讀取
    currentVersion 時失敗——在寫真正的測試之前，先透過一個用完即丟的
    探測測試確認過這一點，這正是 foundation-06 的經驗教訓條目中，已經
    建議過用在這類調查上的「用探測逐步定位」做法。"
blockers: []
```

### 修正性調整（foundation-07 之後）：migrate_test.go 的 migration 數量脆弱性問題

這不是一個新的 DAG 節點——而是對 foundation 自己的 `migrate_test.go`
所做的一次修正性調整，是直接被要求的（不是透過一般的 DAG 流程），起因是
在同一波中，被五個各自獨立的來源分別確認過同一個問題：lead（兩次，透過
直接驗證）以及三個屬於同一個 Phase 4 的兄弟角色（claude-provider、
checkpoint、predictor、runtime），每一個角色都在建置自己 Phase 4 的
migration 時，各自撞上了完全相同的失敗，並針對 foundation 提出變更請求，
而不是直接去改一個不屬於自己 exclusive paths 的檔案。

根本原因：`migrate_test.go` 中有三個測試
（`TestCoreMigrations_FromEmptyDatabase`、
`TestCoreMigrations_ReopenFromFile_AppliesOnce`、
`TestMigration_ConcurrentReopen_SerializesAndConverges`）斷言
`CurrentVersion == 4`（寫死的數字），還有一個
（`TestAllMigrations_LoadsCoreSchemaFiles`）斷言
`len(migrations) == 4`（寫死的數字），這些斷言都是針對
`sqlite.AllMigrations()`——這個真正、以 `embed.FS` 為底的載入器，依設計
（見 `migrate.go` 自己的文件註解）會在其他任何角色的 migration 檔案，
一旦落到 `internal/storage/sqlite/migrations/` 底下的當下，自動把它們
一併載入（claude-provider 0010-0019、checkpoint 0020-0039、predictor
0040-0049、runtime 0050-0059，依 `CONTRACT_FREEZE.md`）。「剛好 4 個」
這件事，只有在 foundation 自己的 0001-0004 是磁碟上唯一存在的檔案時才
成立——這在 foundation-05 到 foundation-07 自己的分支上是成立的，但一旦
任何一個兄弟分支的 migration 合併進同一棵樹，就會立刻不成立，而這正是
Phase 4 整合時，同時橫跨四個分支所會發生的情況。

套用的修正（全部四處都改成「至少要有」而非「剛好等於」的判斷方式——依其
名稱／註解來看，沒有任何一個測試真正的意圖是「驗證 0001-0004 在完全沒有
其他東西存在的情況下套用」；那種隔離已經隱含存在於較早的
`TestMigrate_*` 測試中，這些測試使用手動建構的合成
`[]sqlite.Migration{}` slice，而不是 `AllMigrations()`，因此針對
「只限定自己範圍」再重寫一次會是多餘的）：

- `TestAllMigrations_LoadsCoreSchemaFiles`：`len(migrations) != len(want)`
  （剛好 4 個）改成 `len(migrations) < len(want)`（至少 4 個）；仍然
  斷言前四筆項目依序恰好是 `{1,repositories}`、`{2,worktrees}`、
  `{3,provider_sessions}`、`{4,tasks}`——這部分沒有改動，因為測試意圖
  的這一部分（foundation 自己的 migration 能正確載入，並排序在最前面）
  仍然正確，也仍然值得強制檢查。
- `TestCoreMigrations_FromEmptyDatabase`：`version != 4` 改成
  `version < 4`。針對 foundation 四張資料表（`repositories`、
  `worktrees`、`provider_sessions`、`tasks`）是否存在的斷言沒有改動——
  依這個測試名稱來看，它真正的意圖（「核心資料表被正確建立」）從來就
  沒有要求「不存在其他資料表」。
- `TestCoreMigrations_ReopenFromFile_AppliesOnce`：`version != 4` 改成
  `version < 4`。這個測試的意圖（重新開啟並再次 `Migrate()` 是冪等的）
  與總共存在多少 migration 無關。
- `TestMigration_ConcurrentReopen_SerializesAndConverges`：`version != 4`
  改成 `version < 4`。這個測試的意圖（併發的 `Migrate()` 呼叫最後會
  收斂，而不是互相競爭）與 migration 總數無關。
- `TestMigration_ConcurrentReopen_NoDuplicateSchemaMigrationsRows`
  （已檢查，未改動）：原本就是動態地拿 `len(migrations)` 來比較，而不是
  寫死的字面數字——原本就是對的，不屬於這次修正的一部分。

```yaml
node: foundation-07-correction
status: completed
type: corrective_fix
reason: "跨角色共同確認（這一波共有 5 份獨立回報：lead x2、
  claude-provider、checkpoint、predictor、runtime）的測試脆弱性問題，
  出在 migrate_test.go——三個測試把 CurrentVersion/len(migrations) 寫死
  斷言為 4，而這只有在 foundation 自己的 migration 是
  internal/storage/sqlite/migrations/ 底下唯一存在的檔案時才成立。
  只要任何兄弟角色的 Phase 4 migration 一落進同一棵樹就會壞掉，導致
  Phase 4 整合完全卡住，因為每個角色的測試都是跑在同一個合併後的
  migrations/ 目錄上。"
artifacts:
  - internal/storage/sqlite/migrate_test.go (4 assertions relaxed from
    exact-match to at-least-match against sqlite.AllMigrations()'s real
    embedded migration count/version:
    TestAllMigrations_LoadsCoreSchemaFiles's len(migrations) check,
    TestCoreMigrations_FromEmptyDatabase's CurrentVersion check,
    TestCoreMigrations_ReopenFromFile_AppliesOnce's CurrentVersion check,
    TestMigration_ConcurrentReopen_SerializesAndConverges's CurrentVersion
    check; no other file touched, no new DAG node started)
validation:
  - "gofmt -l internal/storage/sqlite -> empty output"
  - "go build ./internal/storage/sqlite/... -> clean"
  - "go vet ./internal/storage/sqlite/... -> clean"
  - "go test ./internal/storage/sqlite/... -race -v -> PASS, all 36 tests"
  - "golangci-lint run ./... (whole repo) -> 0 issues"
  - "Manual sanity check: temporarily added a throwaway
    migrations/0099_scratch_test.sql (trivial CREATE TABLE, version 99,
    outside foundation's 0001-0009 range) to prove the fixed tests
    tolerate 'more than 4 migrations exist' for real, not just
    theoretically — ran the full package suite (-race) with it present,
    confirmed all tests still PASS (CurrentVersion correctly read as 5,
    every relaxed assertion held), then deleted the throwaway file before
    committing; it is not part of this fix's commit."
commit: dc8d2a1
next_action: 無——這是對既有檔案的一次範圍限定的修正性調整，不是新節點；
  依任務指示，驗證並 commit 之後就 STOP。
assumptions:
  - "四處修正都採用『斷言至少 4 個』的做法，而不是任務中另一個選項
    『限縮到 foundation 自己的 0001-0009 範圍』，因為沒有任何被標出來的
    測試，其名稱／意圖真正是『驗證 migration 在完全沒有其他東西存在的
    情況下獨立套用』——那個情境已經由更早、使用合成 migration 的
    TestMigrate_* 測試（foundation-05）涵蓋過了，這些測試根本不會呼叫
    AllMigrations()。如果把以 AllMigrations() 為底的測試改寫成過濾到只
    使用一個合成的、僅限 0001-0009 的 fs.FS，會重複既有的覆蓋範圍，
    同時讓這些以 AllMigrations() 為底的測試，在它們真正該做的工作上
    變得更差：也就是證明真實的內嵌載入器，與一個真實、多角色的 schema
    能正確收斂——這正是 Phase 4 現在需要驗證的、整合時期的行為，因為
    兄弟角色的 migration 現在確實已經存在。"
  - "TestMigration_ConcurrentReopen_NoDuplicateSchemaMigrationsRows
    已檢查過，並未改動：它的資料列數量斷言，原本就是拿
    len(migrations)（同一個測試中 AllMigrations() 回傳的動態 slice）
    來比較，而不是寫死的字面數字，所以從來就不受這個 bug 影響，也不需要
    任何修正。"
blockers: []
```
