# docs/implementation/ — build records of executed implementation efforts

> 🌐 English | [繁體中文](README.zh-TW.md)

One subfolder per executed (or executing) implementation effort. Each
holds the durable evidence trail of how that effort was actually built:
the frozen task DAG, the contract freeze, per-role progress artifacts
with node-by-node validation logs, retrospectives, and mid-build
analyses. These are **historical record** — useful for tracing how and
why something was built, never a substitute for the current design docs.

| Folder | What it holds |
|---|---|
| [`vertical-slice/`](vertical-slice/README.md) | The complete record of Auspex's first build: 85 DAG nodes across seven roles, executed by parallel agents over 13 integration phases, Bootstrap through the Stage-5 Final gate. Currently the only effort recorded here. |

## Neighbors

- What the product *is*: [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md)
  and its siblings in [`../design/`](../design/README.md).
- Decisions that changed contracts mid-build:
  [`../adr/`](../adr/README.md) (ADR-041 onward amended the
  vertical slice's frozen DAG and ports).
- The process these records instantiate, generalized for reuse:
  [`../methodology/`](../methodology/README.md).
- Constitution §6.7 is why these records exist: the evidence discipline
  for product task state ("completed means evidenced", §6.2) applies by
  analogy to each role's progress artifact here — conversation-only
  progress does not count.
