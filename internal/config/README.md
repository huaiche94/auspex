# internal/config/ — layered YAML configuration loading with a fixed precedence chain

> 🌐 English | [繁體中文](README.zh-TW.md)

Package `config` loads Auspex's YAML configuration per the precedence chain fixed
by Auspex_ADD.md §26.1 (the ADD lives at
[docs/design/Auspex_ADD.md](../../docs/design/Auspex_ADD.md)):

```text
CLI flags > environment > .auspex/local.yaml > .auspex/config.yaml
> global user config > defaults
```

The package contract is the package comment at the top of `config.go` (no separate
`doc.go`). It deliberately does **not** model every field of ADD §26.4's
illustrative default configuration as typed Go structs — modeling fields nothing
reads would violate Constitution §7 rule 10. What it does own:

- the `schema_version` envelope;
- layered YAML loading in the correct precedence order (`Load(layers, opts)`,
  `LoadFile(source, path)`, with `Layer`/`Source` identifying each layer);
- unknown-field warn-vs-strict validation (ADD §26.2, `Options` /
  `UnknownFieldPolicy`);
- a documented merge/precedence algorithm exposed through `Config.Raw` — a decoded
  map, not yet field-mapped into structs — that later roles build their own typed
  config sections on top of;
- typed decodes for sections that have gained a production consumer — the first
  is `state_checkpointing` (`statecheckpointing.go`: `StateCheckpointingSection`,
  `DefaultsLayer`), carrying ADR-0054's `on_checkpoint_and_run` gate for the
  automatic pre-turn checkpoint (default `true`; consumed by `cmd/auspex`'s
  composition root, fail-open to the defaults on any load error).

Neighbors: global-user-config file *locations* come from
[`../paths`](../paths/README.md); repository-local `.auspex/*` paths are resolved
by the role that owns repository scoping, not here.
