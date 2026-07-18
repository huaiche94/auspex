# docs/ — project documentation

> 🌐 English | [繁體中文](README.zh-TW.md)

Everything that documents Auspex beyond the code itself lives here. If
you are new, start with the root [`README.md`](../README.md), then come
back to this index.

## Authority

Two documents outrank everything else (Constitution §1–§2):

1. [`CONSTITUTION.md`](../CONSTITUTION.md) (repository root) — process,
   governance, ownership, invariants.
2. [`design/Auspex_ADD.md`](design/Auspex_ADD.md) — architecture, domain
   model, requirements, roadmap — as amended by accepted ADRs under
   [`adr/`](adr/).

Everything else in this tree is either subordinate detail, a historical
record, or a working document.

## Map

| Folder / file | What it holds |
|---|---|
| [`design/`](design/README.md) | The three authoritative design documents: the ADD (architecture/requirements spec), the predictor design supplement, and the vertical-slice parallel execution plan. |
| [`adr/`](adr/README.md) | Accepted Architecture Decision Records, numbered `NNNN-title.md`. Immutable once accepted; superseded by newer ADRs, never edited. |
| [`DECISION_LOG.md`](DECISION_LOG.md) | Every owner-level decision as a `D-##` entry plus a decision tree: options considered, what was chosen, consequences, reversibility. Written in Traditional Chinese. |
| [`implementation/`](implementation/README.md) | How the vertical slice was actually built: the execution DAG, per-role progress logs, contract freeze, lessons learned, and post-phase analyses. Historical record — useful for archaeology, not current guidance. |
| [`methodology/`](methodology/README.md) | The multi-agent, evidence-based development methodology this repo was built with, distilled for reuse on other projects. |
| [`backlog/`](backlog/README.md) | Design notes for accepted-but-not-yet-scheduled work, each tied to a tracking issue. |
| [`archive/`](archive/README.md) | Superseded documents kept for historical reference. Never implementation guidance. |
| [`repository_inventory.md`](repository_inventory.md) | Audit of every markdown file in the repository: its authority level and status. |

## Language

Every documentation file has a Traditional Chinese sibling named
`<name>.zh-TW.md`, cross-linked at the top of both files (ADR-049).
**The document's original authorship language is the normative text.**
For the English-authored documents (all but two), the English version
is normative and the translation is the bug when they diverge. Two
documents are authored in Traditional Chinese and are normative as
written, with no `.zh-TW.md` sibling:
[`design/Auspex_ADD.md`](design/Auspex_ADD.md) and
[`DECISION_LOG.md`](DECISION_LOG.md).
