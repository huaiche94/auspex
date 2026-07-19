# internal/config/ — 具固定優先序鏈的分層 YAML 設定載入

> 🌐 [English](README.md) | 繁體中文
>
> 本文件為非規範翻譯，內容以英文版為準（ADR-049）。

`config` 套件依照 Auspex_ADD.md §26.1 所固定的優先序鏈（該 ADD 位於
[docs/design/Auspex_ADD.md](../../docs/design/Auspex_ADD.md)）載入 Auspex 的 YAML 設定：

```text
CLI flags > environment > .auspex/local.yaml > .auspex/config.yaml
> global user config > defaults
```

套件契約就是 `config.go` 檔案開頭的套件註解（沒有另外的 `doc.go`）。它刻意**不會**把 ADD
§26.4 範例預設設定中的每一個欄位都建模成有型別的 Go struct — 為沒有任何程式碼會讀取的欄位建模，將違反
Constitution §7 rule 10。它實際擁有的是：

- `schema_version` envelope；
- 依正確優先序載入分層 YAML（`Load(layers, opts)`、`LoadFile(source, path)`，並以
  `Layer`／`Source` 標示各層）；
- unknown-field 的 warn 與 strict 驗證（ADD §26.2，`Options` / `UnknownFieldPolicy`）；
- 透過 `Config.Raw` 對外公開、有完整文件記載的 merge／precedence 演算法 — 這是一個已解碼但尚未映射成
  struct 欄位的 map — 供之後的角色在其上建立自己的型別化設定區塊；
- 對已有 production 消費者的區塊提供型別化解碼 — 第一個是 `state_checkpointing`
  （`statecheckpointing.go`：`StateCheckpointingSection`、`DefaultsLayer`），承載
  ADR-0054 自動 pre-turn checkpoint 的 `on_checkpoint_and_run` gate（預設
  `true`；由 `cmd/auspex` 組裝根消費，任何載入錯誤 fail-open 回預設值）。

相鄰套件：全域使用者設定檔的*位置*來自 [`../paths`](../paths/README.md)；repository-local
的 `.auspex/*` 路徑則是由擁有 repository scoping 的角色負責解析，而非在這裡。
