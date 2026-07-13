# Wave 3 Recommendation

| Field | Value |
|---|---|
| Phase | 3.10 — Post Wave 2 Analysis |
| Status | **Recommendation only. No teammates assigned. No execution. Wait for approval.** |
| Precondition assumed | This analysis treats Wave 2's four branches as if integrated (per DAG dependency semantics), since they were independently verified, validated, and are the immutable Wave 2 record per this phase's own framing. **They have not actually been merged into `main`** — that merge itself is a pending action this document does not perform and is not authorized to perform. |
| Inputs used | `docs/implementation/vertical-slice/EXECUTION_DAG.md` (frozen), `Auspex_Predictor_Design_Supplement.md`, `Prediction_Error_Report.md`, `Missing_Telemetry_Report.md`, `Feature_Registry.md`, `Prediction_Confidence_Report.md`, `Feature_Gap_Report.md`, `ADR_Recommendations.md` |

## 1. Newly unlocked nodes

Determined by checking every not-yet-completed DAG node's dependency
list against the full set of completed nodes (Bootstrap + Wave 1 + Wave
2 = 19 nodes). Six nodes are newly or still-unassigned-but-unlocked:

| Node | Dependencies | All satisfied? | Newly unlocked this wave, or unlocked earlier and never assigned? |
|---|---|---|---|
| `foundation-06` | `foundation-05`, `contract-integrator-01` | Yes (`foundation-05` completed Wave 2) | Newly unlocked |
| `foundation-08` | `foundation-02`, `foundation-03` | Yes (both completed Wave 2) | Newly unlocked |
| `predictor-05b` | `predictor-05` | Yes (completed Wave 2) | Newly unlocked |
| `runtime-b01` | `contract-integrator-07`, `foundation-01` | Yes (both completed **Wave 1**) | Unlocked since end of Wave 1 — never assigned because "runtime" was never one of the 4 named Wave 1/2 teammates |
| `qa-01` | `foundation-09`, `contract-integrator-07` | Yes (`foundation-09` completed Wave 2) | Newly unlocked |
| `qa-08` | — (no dependencies) | Yes (vacuously) | Unlocked since Bootstrap — never assigned because "qa" was never one of the 4 named teammates |

**This is itself a finding worth surfacing plainly:** two nodes
(`runtime-b01`, `qa-08`) have been executable since Wave 1 ended and were
never picked up, purely because the team roster (4 teammates:
`foundation`, `claude-provider`, `checkpoint`, `predictor`) never included
`runtime` or `qa`. This is not a DAG problem — it is a team-composition
decision the repository owner made deliberately for Waves 1-2 — but Wave 3
planning should treat it as an explicit choice to make again, not an
oversight to silently correct.

## 2. Still-blocked nodes (for completeness — not exhaustive of the whole DAG, only the direct frontier)

| Node | Blocked on |
|---|---|
| `foundation-07` | `foundation-06` |
| `claude-provider-05` | `foundation-06` |
| `claude-provider-07` | `claude-provider-05` |
| `checkpoint-a01` | `foundation-06` |
| `checkpoint-b01` | `foundation-06` |
| `checkpoint-b04` | `checkpoint-b01` |
| `predictor-01` | `foundation-06` |
| `predictor-05c` | `predictor-05b` |
| `predictor-07` | `predictor-05c` |
| `runtime-a01` | `foundation-06` |
| `runtime-b02` | `foundation-06` |

**Observation:** `foundation-06` alone directly blocks 7 of these 11
nodes — it is the dominant bottleneck in the entire remaining graph, not
just one option among several. This drives the ranking in §4.

## 3. Per-node estimates for unlocked nodes

Every cell states its provenance explicitly. Where a value is Derived, the
derivation and its uncertainty are stated inline — none of these are
invented.

### foundation-06

| Metric | Value | Provenance | Confidence | Uncertainty |
|---|---|---|---|---|
| Files changed | DAG estimate: 10. Calibration-adjusted: ~15 (10 × 1.544, `Calibration_Report.md` §1's mean files-changed ratio) | DAG: Observed. Adjusted: Derived | Low-Medium (n=17 sample backing the 1.544 ratio, single repository) | ±5 files plausible given the ratio's own spread (1.0×-2.25× observed range) |
| Files read | Unknown | Unknown | 0.0 | N/A — never measured for any node (`Missing_Telemetry_Report.md` B3) |
| LOC | DAG estimate: 300. Calibration-adjusted: ~627 (300 × 2.089, `Calibration_Report.md` §2's mean LOC ratio) | DAG: Observed. Adjusted: Derived | Low-Medium (n=13 sample, 100% one-directional but wide individual spread) | Wide — this node's own risk flag ("load-bearing... schema mistakes cascade") suggests it may need more careful, hence longer, code than a typical M-node |
| Duration | Unknown | Unknown | 0.0 | No DAG estimate exists (`Prediction_Error_Report.md` §0); no M-complexity reference class in this dataset with a *high-risk* flag comparable to this node — `foundation-05` (M, High risk, ~45 min self-reported) is the closest analog but is itself only one data point |
| Token usage | Unknown | Unknown | 0.0 | No per-node token data exists at this granularity for any prior node except `foundation-01`/`checkpoint-b03`, neither a good analog for a High-risk migration-schema node |
| Complexity | M (DAG) | Observed | High (DAG label) | Two independent lessons-learned entries (`foundation-05`, this wave's Calibration Report) suggest High-risk-flagged M nodes tend to run long relative to ordinary M nodes |
| Execution risk | High — DAG's own words: "every feature role's migrations FK into these tables... schema mistakes cascade to claude-provider/checkpoint/predictor/runtime migration ranges" | Observed (DAG text) | High confidence this is genuinely high-risk, not just labeled so — `foundation-05` (the closest analog, same author role, same subsystem) already proved out real pragma/transaction design care was needed | This is the single highest-leverage, highest-risk node in the unlocked set |

### foundation-08

| Metric | Value | Provenance | Confidence | Uncertainty |
|---|---|---|---|---|
| Files changed | DAG: 4. Adjusted: ~6 (4 × 1.544) | Observed / Derived | Low-Medium | ±2 |
| Files read | Unknown | Unknown | 0.0 | Same as all nodes |
| LOC | DAG: 200. Adjusted: ~418 (200 × 2.089) | Observed / Derived | Low-Medium | Wide |
| Duration | ~22 min (Derived reference-class mean from this wave's S-complexity self-reports: `foundation-01` ~25min, `foundation-02` ~20min, `foundation-04` ~20min) | Derived (weak — self-reported source data, n=3) | Low | ±10 min plausible |
| Token usage | Unknown at per-node granularity (would share a Wave 3 invocation total with whatever else its teammate is assigned) | Unknown | 0.0 | N/A |
| Complexity | S (DAG) | Observed | High (DAG label; this is a small, well-bounded precedence-test node, consistent with S in this wave's pattern of S-nodes holding their estimate) | Low |
| Execution risk | Low — DAG's own text; explicitly cross-platform-test-dependent ("Needs Windows/macOS/Linux CI (qa-01) for full signal") | Observed | Medium — `Calibration_Report.md` §6 flags cross-platform work as a weak (n=2) hypothesis for hidden risk beyond the nominal label | This node's own DAG note names `qa-01` as a dependency for *full* signal, even though it isn't a hard blocking dependency — worth sequencing after `qa-01` if both are in the same wave |

### predictor-05b (Token Forecaster)

| Metric | Value | Provenance | Confidence | Uncertainty |
|---|---|---|---|---|
| Files changed | DAG: 4. Adjusted: ~6 (4 × 1.544) | Observed / Derived | Low-Medium | ±2 |
| Files read | Unknown | Unknown | 0.0 | N/A |
| LOC | DAG: 400. Adjusted: ~836 (400 × 2.089) | Observed / Derived | Low-Medium | Wide; this node's own risk flag suggests possibly more design-iteration LOC than a typical L node (cf. `predictor-05`'s cold-start-table-gap-filling cost, a similar "design iteration" pattern) |
| Duration | Unknown | Unknown | 0.0 | **No L-complexity node in this entire 19-node dataset has a clean, non-interrupted self-reported duration** — `checkpoint-b02` (L) was interrupted mid-session with no clean wall-clock. There is genuinely no reference class to derive from; reporting a number here would be fabrication. |
| Token usage | Unknown | Unknown | 0.0 | Same reasoning as duration |
| Complexity | L (DAG) | Observed | High | `predictor-06` (also originally L) was self-assessed as actually M — a real possibility this node also over-labels, per `Calibration_Report.md` §3's finding that ADD §15 tends to bundle a small uncalibrated-fallback tier with a much larger calibrated tier under one label. `predictor-05b`'s own scope (ADD §15.1-15.2's MVP formula) is the smaller tier by the same pattern — plausible this runs more like M than L, but this is a hypothesis, not a finding, since predictor-05b has never been built |
| Execution risk | High — DAG's own text: "feeds RiskCombiner's quota/context risk terms; a systematic bias here propagates into every downstream policy decision" | Observed | High confidence this risk framing is correct — a token-forecast bias genuinely does compound through every later stage in the frozen pipeline (ADR-041) | This is a correctness-critical node, not just a large one |

### runtime-b01 (CLI skeleton)

| Metric | Value | Provenance | Confidence | Uncertainty |
|---|---|---|---|---|
| Files changed | DAG: 6. Adjusted: ~9 (6 × 1.544) | Observed / Derived | Low-Medium | ±3 |
| Files read | Unknown | Unknown | 0.0 | N/A |
| LOC | DAG: 350. Adjusted: ~731 (350 × 2.089) | Observed / Derived | Low-Medium | Wide |
| Duration | ~30 min (Derived reference-class mean from this wave's M-complexity self-reports: `foundation-03` ~25min, `foundation-05` ~45min, `checkpoint-b03` ~20min — wide spread, mean is a weak central estimate) | Derived (weak, n=3, spread 20-45min) | Low | ±15 min plausible — wider than the S-complexity reference class |
| Token usage | Unknown | Unknown | 0.0 | N/A |
| Complexity | M (DAG) | Observed | Medium | This node explicitly cannot touch `cmd/auspex/main.go` (owned by `contract-integrator`/`foundation` per the vertical-slice plan) — meaning its actual merge requires lead coordination beyond a typical single-role M node, a cost the DAG's complexity label doesn't capture |
| Execution risk | Low — DAG's own text | Observed | Medium — the root-wiring-coordination requirement is a real, DAG-invisible integration risk even though the DAG's own risk column says "Low," similar to how `Calibration_Report.md` found DAG risk labels sometimes miss integration-shaped risk vs. implementation-shaped risk | Recommend treating this as Low-Medium, not simply Low |

### qa-01 (CI scaffolding)

| Metric | Value | Provenance | Confidence | Uncertainty |
|---|---|---|---|---|
| Files changed | DAG: 4. Adjusted: ~6 (4 × 1.544) | Observed / Derived | Low-Medium | ±2 |
| Files read | Unknown | Unknown | 0.0 | N/A |
| LOC | DAG: 200. Adjusted: ~418 (200 × 2.089) | Observed / Derived | Low-Medium | Wide |
| Duration | ~22 min (same weak S-complexity reference class as `foundation-08`) | Derived (weak) | Low | ±10 min |
| Token usage | Unknown | Unknown | 0.0 | N/A |
| Complexity | S (DAG) | Observed | Medium — `qa` is an entirely new role with zero prior nodes executed in this dataset; there is no `qa`-specific track record to validate the S label against, unlike every other node in this table which has at least one same-role prior node | Genuinely more uncertain than its S label alone suggests, purely because it would be this role's first-ever executed node |
| Execution risk | Low — DAG's own text | Observed | Low-Medium — first-node-for-a-new-role risk (see Complexity row) isn't captured by the DAG's per-node risk column, which only assesses the node's own content | Cross-platform CI (3 OSes) genuinely can surface real issues per `Wave2_Lessons.md` §1 issue #1's Windows-bug pattern — this node is precisely the mechanism meant to catch those, so treat "Low" with mild skepticism |

### qa-08 (Governance docs)

| Metric | Value | Provenance | Confidence | Uncertainty |
|---|---|---|---|---|
| Files changed | DAG: 4. Adjusted: ~6 (4 × 1.544) | Observed / Derived | Low-Medium | ±2 |
| Files read | Unknown | Unknown | 0.0 | N/A |
| LOC | DAG: 300 (marked "(doc)" in the DAG itself — likely prose, not code, so the code-LOC calibration ratio may not transfer cleanly) | Observed | Low — this is the one node in this set where applying the LOC calibration factor is questionable, since that factor was derived entirely from Go code+test LOC, not prose | Flagged explicitly rather than silently applying a code-calibrated ratio to a docs node |
| Duration | ~22 min (weak S-complexity reference class, same caveat as above re: docs vs. code) | Derived (weak, and possibly the wrong reference class) | Low | Wide |
| Token usage | Unknown | Unknown | 0.0 | N/A |
| Complexity | S (DAG) | Observed | Medium — same "first `qa` node ever" caveat as `qa-01` | Same as `qa-01` |
| Execution risk | Low — DAG's own text: "None — can run in parallel with everything, no code dependency" | Observed | High confidence this is genuinely low-risk — it is pure documentation with zero code dependency, the DAG's own text is unusually strong here (not just "Low" but explicitly "no code dependency") | Lowest-uncertainty risk assessment of any node in this table |

## 4. Ranking (per the four requested criteria, in order)

| Rank | Node | Dependency unlock value | Execution risk | Est. duration (derived) | Merge complexity |
|---|---|---|---|---|---|
| 1 | `foundation-06` | **7 direct dependents** (dominant — see §2) | High | Unknown (no comparable high-risk M-node duration reference exists) | High (schema decisions every later role's migrations depend on) |
| 2 | `predictor-05b` | 1 direct dependent, but leads the entire remaining predictor chain (`-05c` → `-07` → `-08`+) | High | Unknown (no L-complexity duration reference exists at all) | Medium (self-contained package, but correctness-critical per its own risk note) |
| 3 | `runtime-b01` | 1 direct dependent (`runtime-b09`, shared with 6 others — partial credit only) | Low (DAG) / Low-Medium (adjusted, per root-wiring coordination need) | ~30 min (weak derived estimate) | Medium (cannot touch `cmd/auspex/main.go`; needs lead-coordinated root wiring) |
| 4 | `qa-08` | 1 direct dependent (`qa-09`, shared with 6 others — partial credit only) | Low (high-confidence label) | ~22 min (weak derived estimate, docs-vs-code caveat) | Low (pure documentation, no code) |
| 5 | `foundation-08` | 0 direct dependents | Low | ~22 min (weak derived estimate) | Low (single-role, well-isolated precedence tests) |
| 6 | `qa-01` | 0 direct dependents | Low (label) / Low-Medium (adjusted, first-node-for-new-role uncertainty) | ~22 min (weak derived estimate) | Medium (CI workflow files, cross-cutting `.github/**` tooling, must actually pass on 3 OSes) |

**How to read this ranking:** ranks 1-2 are not close calls — `foundation-06`'s
7-node unlock value and `predictor-05b`'s position as the sole path to
unblocking the rest of the predictor pipeline both dominate every other
criterion for their respective rows. Ranks 3-6 are genuinely close
(all Low DAG-risk, all roughly S/M duration, all partial-or-zero unlock
value) — the ordering among them leans on the weaker, more qualitative
signals (merge complexity, first-node-for-a-role uncertainty) rather than
a single dominant factor, and a reasonable planner could defensibly
reorder 3-6 without contradicting this report's evidence.

## 5. What this document does not do

Per explicit instruction: no teammates are assigned above, no node has
been started, and no branch/commit/merge has been created by writing this
document. This is planning input only, for the repository owner's next
decision.
