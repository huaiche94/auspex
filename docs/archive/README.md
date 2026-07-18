# docs/archive/ — superseded historical documents

> 🌐 English | [繁體中文](README.zh-TW.md)

Documents that have been superseded in full and are kept **for
historical reference only — never as implementation guidance**. Each
file opens with an `ARCHIVED` banner naming what replaced it. Contents
deliberately retain the old product names ("AgentGuard", "Preflight")
and old file references: ADR-045 decided that `docs/archive/` and git
history are not rewritten on rename, and ADR-049 kept that rule for the
docs reorganization — old citations still resolve by grep.

| File / folder | What it is |
|---|---|
| [`AgentGuard_Architecture.md`](AgentGuard_Architecture.md) | The earliest architecture sketch, under the precursor name "AgentGuard" — superseded in full by the ADD (its banner still cites it as `Preflight_ADD.md`, now [`../design/Auspex_ADD.md`](../design/Auspex_ADD.md)). |
| [`execution_prompt.md`](execution_prompt.md) | Early draft kickoff prompt for a four-teammate, two-phase team — conflicts with the approved nine-agent (A00–A08) topology and was never executed. |
| [`agent-packets-v1/`](agent-packets-v1/README.md) | The numbered nine-role (A00–A08) hand-off packets (nine packet files plus a contract-freeze template), replaced by the semantically named seven-role files at [`../../agents/`](../../agents/). |

## Neighbors

- What superseded these: [`../design/`](../design/README.md) (the
  authoritative ADD, predictor supplement, execution plan) and the live
  role packets at [`../../agents/`](../../agents/).
- The rename decision that froze these contents:
  [`../adr/0045-rename-to-auspex.md`](../adr/0045-rename-to-auspex.md).
- The actual build record (not archived; also historical but accurate):
  [`../implementation/vertical-slice/`](../implementation/vertical-slice/README.md).

If you find yourself implementing against anything in this folder,
stop — the ADD and accepted ADRs win (Constitution §1–§3).
