# docs/implementation/vertical-slice/ — build record of the first vertical slice

> 🌐 English | [繁體中文](README.zh-TW.md)

The complete execution record of Auspex's first build: 85 DAG nodes
across seven roles, executed by parallel agents over 13 integration
rounds ("phases"), Bootstrap through the Stage-5 Final gate. Everything
here is **historical record** — read it to trace how and why something
was built; read [`../../design/Auspex_ADD.md`](../../design/Auspex_ADD.md)
for what the product *is*.

## What's here

| File / folder | What it holds |
|---|---|
| [`EXECUTION_DAG.md`](EXECUTION_DAG.md) | The task-level dependency DAG the build executed (as amended by ADR-041): stages, per-role task IDs, dependency edges. |
| [`CONTRACT_FREEZE.md`](CONTRACT_FREEZE.md) | The frozen cross-role contracts (domain types, ports, events, schema-version strings, migration ranges) every role built against, with its amendment log. |
| [`contract-integrator.md`](contract-integrator.md), [`foundation.md`](foundation.md), [`claude-provider.md`](claude-provider.md), [`checkpoint.md`](checkpoint.md), [`predictor.md`](predictor.md), [`runtime.md`](runtime.md), [`qa.md`](qa.md) | Per-role progress artifacts: node-by-node status, artifacts, validation logs. These are the durable evidence trail Constitution §6.7 requires. |
| [`lessons_learned/`](lessons_learned/README.md) | Per-role retrospectives written at role completion. |
| [`wave2-analysis/`](wave2-analysis/README.md) | The mid-build analysis round that re-planned Phase 3+: calibration, feature-gap, replay, and confidence reports. |

## Phase-by-phase integration history

(Relocated from the root `README.md` when it was rewritten for
first-time viewers — ADR-049.)


The vertical slice is 84 tasks + 1 final integration across 7 roles
(see `EXECUTION_DAG.md`, as amended by ADR-041).
Stages and task dependencies are canonical in that DAG; **phases** are the
integration rounds the work actually ships in. Phases 1–2 below are as
executed. Phase 3 onward is a provisional, dependency-derived grouping —
each phase is re-planned by the lead before it starts (see
`wave2-analysis/` for the inputs to Phase 3
planning) and must respect the DAG's stage and dependency order.

Each task-ID group below links to the owning role's progress artifact
(per-node status/artifact/validation logs); each commit hash links to the
integration commit on GitHub.

| Phase | Scope (task IDs) | Status |
|---|---|---|
| Bootstrap | [contract-integrator-01…07](contract-integrator.md) — contract freeze (Stage 0) | ✅ Integrated ([`940c5cb`](https://github.com/huaiche94/auspex/commit/940c5cb)) |
| Phase 1 | [foundation-01](foundation.md) · [claude-provider-01/02/03](claude-provider.md) · [checkpoint-b02](checkpoint.md) · [predictor-02/03/04](predictor.md) | ✅ Integrated ([`3fb37ce`](https://github.com/huaiche94/auspex/commit/3fb37ce)) |
| Phase 2 | [foundation-02/03/04(reduced)/05/09](foundation.md) · [claude-provider-04/06](claude-provider.md) · [checkpoint-b03](checkpoint.md) · [predictor-05/06](predictor.md) | ✅ Integrated ([`528b6ad`](https://github.com/huaiche94/auspex/commit/528b6ad)) |
| Phase 3 | [foundation-06/08](foundation.md) · [predictor-05b](predictor.md) · [runtime-b01](runtime.md) · [qa-01/08](qa.md) (ADR-041 Token Forecaster; first-ever nodes for **runtime** and **qa**, unassigned since Phase 1/Bootstrap respectively) | ✅ Integrated ([`ca7062f`](https://github.com/huaiche94/auspex/commit/ca7062f)) |
| Phase 4 | [foundation-07](foundation.md) · [claude-provider-05](claude-provider.md) · [checkpoint-a01/b01](checkpoint.md) · [predictor-01/05c](predictor.md) · [runtime-a01/b02](runtime.md) | ✅ Integrated ([`a0b10f2`](https://github.com/huaiche94/auspex/commit/a0b10f2)) — includes a corrective fix to `migrate_test.go`'s hardcoded migration-count assertions, confirmed necessary by 5 independent cross-role reports before any sibling role's migrations could coexist with foundation's in one tree |
| Phase 5 | [claude-provider-07](claude-provider.md) · [checkpoint-a02/a03/b04](checkpoint.md) · [predictor-07](predictor.md) · [runtime-a02/a06/b03/b04/b05/b08](runtime.md) | ✅ Integrated ([`dabaa9f`](https://github.com/huaiche94/auspex/commit/dabaa9f)) — the DAG's real unlocked frontier after Phase 4 was larger than originally guessed (six runtime nodes unlocked at once, no `predictor-05d` ever existed); `b03`/`b04`/`b05` still run against fakes for `predictor-08`/`predictor-09`/`checkpoint-a04`, swapped to real implementations at a later integration |
| Phase 6 | [checkpoint-a04/b05/b06](checkpoint.md) · [predictor-08](predictor.md) · [runtime-a03/a04/a07](runtime.md) | ✅ Integrated ([`f5f0f28`](https://github.com/huaiche94/auspex/commit/f5f0f28)) — checkpoint-a04 (CompleteNode atomic protocol) is now real, with crash-injection and concurrent-completion-race proofs independently re-verified; predictor-08's cold-start "probability: null" invariant independently traced to exactly two gated call sites |
| Phase 7 | [checkpoint-a05/a07/b07](checkpoint.md) · [predictor-09](predictor.md) · [runtime-a05/b07](runtime.md) · [qa-05](qa.md) | ✅ Integrated ([`25e3d40`](https://github.com/huaiche94/auspex/commit/25e3d40)) — qa's first Stage-4 node since Phase 3; found one real P1 (secret filtering doesn't cover tracked-file diffs, only untracked-file archives), not fixed here per qa's file-don't-fix boundary, routed to checkpoint |
| Phase 8 | [checkpoint-a06/a08/b08](checkpoint.md) · [predictor-10](predictor.md) · [runtime-a08](runtime.md) · [qa-04](qa.md) | ✅ Integrated ([`b5a1937`](https://github.com/huaiche94/auspex/commit/b5a1937)) — includes a corrective fix extending secret redaction to tracked-file diffs (closing Phase 7's P1), and predictor-10's adversarial audit found and fixed a real authorization prompt-binding bypass |
| Phase 9 | [checkpoint-a09/b09](checkpoint.md) · [predictor-11](predictor.md) · [runtime-a09/a10/b06](runtime.md) | ✅ Integrated ([`192e4b9`](https://github.com/huaiche94/auspex/commit/192e4b9)) — completes **checkpoint** (a01-a09/b01-b09) and **predictor** (01-11) entirely; found and fixed a real path-traversal vulnerability (checkpoint) and a real TOCTOU race (runtime) |
| Phase 10 | [runtime-a11 · runtime-b09](runtime.md) | ✅ Integrated ([`a249ca2`](https://github.com/huaiche94/auspex/commit/a249ca2)) — closed two genuine gaps: a missing TurnInterrupter-to-PauseRecord wiring path, and no CLI command ever serialized its typed error to JSON (Cobra's default printer flattened it to plain text) |
| Phase 11 | [runtime-b10](runtime.md) | ✅ Integrated ([`2fbc0c8`](https://github.com/huaiche94/auspex/commit/2fbc0c8)) — completes **runtime** entirely (a01-a11/b01-b10, 21 nodes across 9 phases); proved in-process restart on the same SQLite file, including a real OS-process SIGKILL crash test |
| Phase 12 | [qa-02/03/06/07/09](qa.md) | ✅ Integrated ([`a91c239`](https://github.com/huaiche94/auspex/commit/a91c239)) — completes **qa** entirely; the literal vertical-slice E2E demo runs real code end-to-end. Final report: no P0s, one open P1 (provider-event-to-node-completion wiring), fully documented |
| Final | [contract-integrator-final](contract-integrator.md) (Stage 5) | ✅ Integrated ([`3b6cfcb`](https://github.com/huaiche94/auspex/commit/3b6cfcb) + [`faca171`](https://github.com/huaiche94/auspex/commit/faca171)) — found and closed the composition gap the gate exists to catch: `cmd/auspex/main.go` was never wired to real services. See [`contract-integrator.md`](contract-integrator.md)'s Stage 5 section |

Phase 5 onward is intentionally not fixed in detail — each phase is
re-derived from the DAG's actual dependency edges once the prior phase
integrates (see `wave2-analysis/Wave3_Recommendation.md`
for the method), not planned far in advance against a DAG that keeps
changing shape as work lands.

`→` marks in-phase sequencing on a role's branch; `·` separates parallel
role branches within the same phase.

