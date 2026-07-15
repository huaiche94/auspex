# Backlog — Token-Cost Prediction: Research-Grounded Roadmap

> 🌐 English | [繁體中文](token-cost-prediction-research.zh-TW.md)

| Field | Value |
|---|---|
| Status | **Backlog / TODO** — recorded, not scheduled into an active wave |
| Tracking | Issues [#65](https://github.com/huaiche94/auspex/issues/65) (Phase 1), [#66](https://github.com/huaiche94/auspex/issues/66) (Phase 2), [#67](https://github.com/huaiche94/auspex/issues/67) (Phase 3), [#68](https://github.com/huaiche94/auspex/issues/68) (Phase 4), filed 2026-07-14; sequenced after #11 (calibration), #13 (cost axis), #20 (provider/model/effort) |
| Origin | Owner request, 2026-07-14: read arXiv:2604.22750 end-to-end and asked to fold its findings into the roadmap |
| Related | `Auspex_ADD.md` §14–§17, `../design/Auspex_Predictor_Design_Supplement.md` ("External Evidence"), ADR-041 (forecast layer), ADR-043 (multi-resource runway / cost axis, #13), ADR-047 (cohort fallback ladder), issues #11, #14, #20, #42 |
| Grounding discipline | Same as the other backlog notes: **no coefficient proposals without data.** The paper's numbers enter here as *external priors and design rationale* measured on SWE-bench across other models — never as fitted coefficients for Auspex's own cohorts. Formulas are reimplemented with our own symbols; prose is paraphrased, not copied. |

## 1. Source

> Longju Bai, Zhemin Huang, Xingyao Wang, Jiao Sun, Rada Mihalcea, Erik
> Brynjolfsson, Alex Pentland, Jiaxin Pei. *How Do AI Agents Spend Your
> Money? Analyzing and Predicting Token Consumption in Agentic Coding
> Tasks.* arXiv:2604.22750 (v2, 2026-04-29).
> <https://arxiv.org/abs/2604.22750>

A systematic study of eight frontier LLMs running SWE-bench trajectories:
it measures where the tokens (and dollars) actually go, and evaluates
whether models can predict their own token cost *before* executing. It is,
in effect, a feasibility study for the exact thing Auspex does — and its
conclusions land squarely on the side Auspex already bet on.

## 2. What the paper establishes (external evidence, paraphrased)

Numbers are the paper's, measured on SWE-bench across other models. They
are **priors and rationale**, not Auspex coefficients.

**Prediction is fundamentally hard (backs the uncalibrated stance):**

- Runs of the *same* task can differ by up to **30×** in total tokens;
  cross-run variance is largest on the most expensive tasks. The paper's
  own conclusion is that predicting token usage and agent pricing is
  fundamentally difficult.
- Models predict their own token usage only **weakly-to-moderately**
  (Pearson ≤ **0.39**, best case Sonnet 4.5 *output* tokens; *input*
  tokens worse), and **systematically underestimate** real usage — input
  especially.
- Expert-rated task difficulty correlates only weakly with real
  consumption (Kendall **τ_b = 0.32**): 6.7% of tasks rated "< 15 min"
  cost more than the average "> 1 hr" task, and 11.1% of "> 1 hr" tasks
  cost less than the average "< 15 min" task. Surface-perceived
  difficulty is not a reliable cost proxy.

**Where the money goes (backs a cache-aware cost model):**

- Agentic coding costs ~**3500×** a single-turn reasoning call and
  ~**1200×** a multi-turn chat, with cost dominated by **input** tokens
  (mean input/output ratio ~**153**).
- Under explicit-cache pricing (Claude) the four token classes price
  separately, and **cache-read tokens are the largest share of the
  dollar amount in every phase** — even though output's unit price is
  ~80× a cache-read's — purely because accumulated context is re-read so
  many times. A "total tokens × average rate" cost model is therefore
  materially wrong.
- More tokens do not buy more accuracy: accuracy **peaks at intermediate
  cost and saturates or declines** above it. High-cost regions are often
  an agent spinning, not trying harder.

**Observed failure signals (backs observe-don't-predict gating):**

- High-cost, failing runs share a clear behavioural tell: **repeated
  `view`/`modify` of the same file.** Inefficient models spend ~**50%**
  of file operations re-touching the same file; the efficient one (GPT-5)
  far less. The paper reads this as redundant back-and-forth that inflates
  context and tokens without matching progress.
- Models lack an innate "this task is unsolvable — stop now" mechanism:
  they keep exploring, retrying, and re-reading context, accruing cost
  with no progress; the size of this over-spend is model-specific.
- Each trajectory splits into five phases — Setup ~10%, Explore ~30%,
  Fix ~34%, Validate ~17%, Closeout ~10% — with different dominant cost
  drivers per phase (Setup output-led planning; Explore input-led file
  reading; Fix/Validate a mix). Cost *shape* is conditional on phase.

## 3. Design implications → roadmap items

Three groups, mapped from the three kinds of value the paper offers.

### A. Priors that back the honest surface (rationale, low effort)

The 30× variance, ≤0.39 self-prediction ceiling, systematic
underestimation, and τ_b = 0.32 are the strongest external backing for
Auspex's existing discipline: uncalibrated scores, wide ranges, and
never calling a score a probability (Constitution §7 rule 7, #42). Two
concrete consequences:

- The forecast should give **input-token ranges wider than output-token
  ranges**, because input is both the cost driver and the harder axis to
  predict. Today's single multiplier does not distinguish them.
- Any future self-prediction path (asking the model to estimate its own
  cost) must carry a built-in **upward bias correction**, because models
  report low.

*Captured now:* the numbers are recorded as rationale in the predictor
supplement's "External Evidence" section and the README honest caveat
(this change). The interval-widening itself is a Phase-1 change.

### B. Cache-aware cost model (the cost axis of ADR-043 / #13)

The forecast card's cost estimate today is essentially total × per-model
rate. The paper's Appendix B decomposition — reimplemented with our own
symbols, not copied — prices four classes separately:

```text
explicit-cache providers (Claude-style):
  non_cached_input = total_input − cache_read
  turn_cost = non_cached_input · r_in
            + output          · r_out
            + cache_creation  · r_cache_create
            + cache_read      · r_cache_read

implicit-cache providers (GPT-5-style):
  non_cached_input = total_input − implicit_cache_read
  turn_cost = non_cached_input     · r_in
            + implicit_cache_read  · 0.2 · r_in   (cache read ≈ ⅕ base input)
            + output               · r_out
```

Key insight to carry into the model: **estimating dollars means
estimating how large a session's context grows and how many times it is
re-read**, not estimating output. This is the cost dimension ADR-043/#13
already reserves; the decomposition and the per-class rate table are what
it needs.

### C. Observed runtime signals (observe, don't predict)

The paper's most actionable gift: signals that catch a dangerous turn
*without* predicting a token count, because they are observed fact.

- **Repeated-file-operation risk factor.** Track per-turn repeat rate of
  `view`/`edit` on the same file; above a threshold it is a strong
  observed signal that the turn is spinning. Wire it as a `RiskCombiner`
  input with its own reason code, able to trigger `WARN` /
  `CHECKPOINT_AND_RUN`. This is a genuine Auspex engineering contribution,
  not a reproduction — the paper supplies the evidence, not the mechanism.
- **Unsolvable-stop gate.** The "stop when it's not converging" mechanism
  the models lack is precisely Auspex's remit: fold the repeat-rate signal
  (and lack of progress-tree evidence) into a pause/checkpoint decision.
- **Phase-aware conditional forecasting.** If Auspex can infer the current
  phase (Setup/Explore/Fix/Validate/Closeout), it can forecast the *shape*
  of upcoming cost conditionally instead of emitting one unconditional
  average — a better use of the same features.

## 4. Phased TODO

- [x] **Phase 0 — rationale capture** (this change): cite the paper in the
  predictor supplement ("External Evidence") and the README honest caveat;
  record this roadmap note. No formula or code change.
- [ ] **Phase 1 — input-vs-output interval split**
  ([#65](https://github.com/huaiche94/auspex/issues/65)): give the token
  forecast distinct input/output ranges, input wider (§3.A). Grounded by
  the paper's *direction*; the *magnitude* still waits for #11 data.
- [ ] **Phase 2 — cache-aware cost decomposition**
  ([#66](https://github.com/huaiche94/auspex/issues/66); §3.B; the cost
  axis of ADR-043/#13): four-class turn cost with a per-model cache-rate
  table; blocked on capturing per-turn `cache_read`/`cache_creation` (only
  `total_tokens` is captured today, ADR-047).
- [ ] **Phase 3 — repeated-file-operation risk factor**
  ([#67](https://github.com/huaiche94/auspex/issues/67); §3.C): needs
  turn-level tool-op telemetry (per-file view/edit counts) that is not
  captured yet; then a `RiskCombiner` input + reason code + policy mapping.
  **Detailed capture-step design in §7.**
- [ ] **Phase 4 — phase inference + unsolvable-stop gate**
  ([#68](https://github.com/huaiche94/auspex/issues/68); §3.C): infer
  trajectory phase; conditional forecast; fold repeat-rate + no-progress
  into the pause/checkpoint decision.

Phases 2–4 each need a capture step before any modelling — the same
capture-before-model rule the rest of the predictor follows.

## 5. Non-goals

- **No importing the paper's numbers as Auspex coefficients.** 30×, 0.39,
  τ = 0.32, ratio 153, the phase percentages — all measured on SWE-bench
  across other models. They justify *direction and shape*, never a fitted
  Auspex threshold. Fitted numbers still come only from #11 data.
- **No implementation ahead of milestone gates** (ADD §31). This note is
  roadmap capture; each phase lands with its own issue and, where it
  touches a frozen contract, its own ADR.
- **No copying the paper's text, tables, or figures.** Formulas are facts
  and are reimplemented with our own variable names and pricing surface;
  prose is paraphrased with attribution.

## 6. Attribution

The cost decomposition in §3.B is our own re-expression of the pricing
identities in the source's Appendix B (arXiv:2604.22750) using Auspex
variable names; formulas are not copyrightable and no text or table is
reproduced. All quantitative claims in §2 are paraphrased from the same
source and attributed to it, not represented as Auspex measurements.

## 7. Phase 3 — capture-step design (#67)

Owner decision, 2026-07-15: the repeated-file-operation risk factor's
capture step lands **native-first** (a Claude Code `PostToolUse` hook),
consumed **retrospectively** first. This section is the design; no code
lands with it (milestone-gated, ADD §31).

### 7.1 The gap this closes

There is **no source today** that observes per-file tool operations:

- Native hooks register only `UserPromptSubmit` / `Stop` / `StopFailure` /
  statusline (`integrations/claude/hooks.json`) — **no PostToolUse**, so
  native sessions (the daily dogfood path) never see tool operations.
- The managed run's stream *carries* `tool_use` blocks
  (`claude -p --output-format stream-json --verbose`,
  `internal/managed/run.go`), but `internal/managed/stream.go` only counts
  `AssistantLines` and never decodes them — and it covers `auspex run` only.

So this is a **new capture source**, not a wiring change — the same
managed-has-it / native-lacks-it asymmetry token actuals hit (ADR-047).

### 7.2 Source: native PostToolUse (primary)

Add a hook entrypoint `auspex hook claude <post-tool-use>` and register it
in `hooks.json`. Claude Code fires PostToolUse per tool call with
`tool_name` and `tool_input`. The file-touching tools are the signal:

- **view** — `Read`
- **modify** — `Edit`, `Write`, `MultiEdit`, `NotebookEdit`

The managed-stream track — decoding the same `tool_use` blocks in
`stream.go` — stays an optional warm-up: it proves the mechanic on data
that already flows, but yields sparse real coverage (`auspex run` only).
Not scheduled here.

Subcommand casing follows whatever #61 (REC-03) decides for hook
subcommands — this new entrypoint must not pre-empt that ADR.

### 7.3 What is captured (privacy-first: aggregates, never paths)

Raw file paths are identifying and are **never persisted** (the export
discipline: nothing may join back to prompts, paths, or identities). So
the hook reduces paths to a per-turn aggregate *inside the process* and
persists only counts:

- `distinct_files_touched` — number of distinct paths this turn
- `total_file_ops` — total view+modify ops
- `repeated_ops` — ops on any file touched more than once
- `repeat_rate` = `repeated_ops / total_file_ops` (nil when `total_file_ops = 0`)
- `max_ops_on_one_file` — the worst single-file churn

A path is interned to an opaque per-turn ordinal for counting and then
discarded; no path string leaves the process. (Absence stays honest:
every field is unknown-not-zero, like the observations export's pointers.)

### 7.4 Data path

```text
PostToolUse hook (tool_name, tool_input.file_path)
  -> per-turn aggregate (interned ordinals, counts only)        [7.3]
  -> stamp aggregate on the turn's terminal event payload
     (provider.turn.completed — the carrier managed token actuals use)
  -> events store (existing JSON payload map)
  -> add the five fields to the observations export whitelist
     (internal/retention/observations.go) so #11/research can see them
  -> upstream: derive a retrospective execution-risk indicator from the
     repeat_rate of recent completed turns (a session feature, like
     RecentSimilarTurn) -> a new ScopeEstimate ReasonCode + scalar
  -> RiskCombiner reads it (ports.go CombineRiskRequest is stateless;
     the signal is computed upstream, never queried by the combiner)
```

Aggregation avoids one persisted event per tool call — a high-frequency
event type would fight ADR-046 retention. The per-turn aggregate is one
extra payload block on an event that already exists.

### 7.5 Consumption: retrospective first

Auspex gates **pre-turn**; repeat rate is an **intra-turn** observation, so
it cannot inform the same turn's gate. First consumption is
**retrospective**: recent turns' repeat_rate raises the *next* turn's
execution-risk (fits the existing pre-turn gate). The **live intra-turn
interrupt** — detecting a spinning turn mid-run and pausing/checkpointing —
is the more powerful use and belongs to **#68** (unsolvable-stop gate),
not here.

### 7.6 Contract touches (→ ADR when implemented)

- new hook subcommand (coordinate with #61)
- new `provider.turn.completed` payload fields
- new observations-export whitelist fields (`auspex.observations-export.v1`)
- new `ScopeEstimate` ReasonCode

Each is a frozen-contract surface, so implementation lands with its own ADR
(Constitution §3).

### 7.7 Slices

- [ ] **3a — capture**: PostToolUse hook + per-turn aggregate + stamp on
  `provider.turn.completed` + observations whitelist. Pure capture; no risk
  wiring.
- [ ] **3b — retrospective risk**: session feature over recent repeat_rate →
  `ScopeEstimate` ReasonCode → execution-risk in `RiskCombiner` → policy.
- [ ] **3c — threshold**: calibrate the repeat_rate threshold from real data
  (data-gated, like #11 — no number until then).

### 7.8 Deferred / non-goals

- No threshold value now (capture-before-model; 3c waits for data).
- No live intra-turn interrupt (that is #68).
- Managed-stream track is an optional warm-up, not scheduled here.
- No path strings persisted, ever (§7.3).
