# docs/methodology/ — the development process, distilled for reuse

> 🌐 English | [繁體中文](README.zh-TW.md)

This folder extracts the multi-agent, evidence-based process Auspex was
built with into a versioned, referenceable document, so a future project
can invoke it as "Follow Auspex Development Methodology v1.0" instead of
restating the process as a several-hundred-line prompt.

| File | What it covers |
|---|---|
| [`Auspex_Development_Methodology.md`](Auspex_Development_Methodology.md) | PDM v1.0.0 — the phase sequence (Phase 0 Repository Discovery through Phase 7 Architecture Amendment), cross-phase invariants, versioning/invocation rules, and explicit non-claims. Every rule was exercised at least once on Auspex itself before being written down; failures are cited as evidence, not asserted abstractly. Its own status field notes it has not yet been applied to a second project (§9 / §12). |

## Scope

This is a **process** methodology — how work moves from idea to
integrated, validated code across multiple AI agents and sessions. It is
explicitly not an architecture template (the
[ADD](../design/Auspex_ADD.md) is an *example* of a Phase 1 artifact,
not part of the methodology) and not a replacement for the human
approval gates it defines.

## Neighbors

- The binding, non-negotiable rules for **this** repository live in
  [`../../CONSTITUTION.md`](../../CONSTITUTION.md); the methodology is
  the reusable generalization.
- The execution the methodology was distilled from is recorded in
  [`../implementation/vertical-slice/`](../implementation/vertical-slice/README.md);
  its Phase 6 ("Post-Phase Analysis") corresponds to
  [`wave2-analysis/`](../implementation/vertical-slice/wave2-analysis/README.md).
