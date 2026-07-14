# research/ — offline calibration pipeline (M13, issue #11)

The offline half of the calibration loop: reads `auspex export
calibration` and `auspex export observations` JSONL, reports data
readiness, derives per-turn ACTUAL cost/context deltas, and — once
per-cohort sample gates pass — produces the empirical quantiles and
residual reports that feed coefficients back into the predictor.

## Grounding discipline (binding)

Same rule as `Predictor_Improvement_Suggestions.md` §2.3 and
`docs/backlog/provider-model-effort-features.md`: **no coefficient
proposals without data.** This pipeline never emits a fitted number from
a cohort below its sample gate; below the gate it reports the gap
("insufficient samples", "actuals unknown", "unlabeled rows") instead.
Tuning against n≈0 is indistinguishable from guessing.

## Usage

```sh
# 1. Export the datasets (de-identified by construction, FR-170/171):
auspex export calibration --out calibration.jsonl
auspex export observations --out observations.jsonl

# 2. Data-readiness report (works from day zero — an empty dataset is a
#    valid, honest input). --observations adds the per-turn actuals
#    readiness section:
python3 research/calibration/report.py calibration.jsonl \
    --observations observations.jsonl

# 3. Per-turn actual cost/context deltas (best-effort attribution —
#    see observations.py's docstring for the model and its limits):
python3 research/calibration/observations.py observations.jsonl
```

No third-party dependencies — standard library only, so the report runs
anywhere Python ≥ 3.9 exists.

## What the report says today

With zero or sparse data, the useful output is the *readiness* section:
how many prediction rows exist, how many carry identity labels
(provider/model_family/effort — #20 Phase 0), how many have a joined
actual outcome (`actual_known`, ADR-046's honest join), and which of the
three capture gaps documented in issue #11 still block real calibration:

1. **actuals coverage** — outcome events need turn correlation (#1's
   pipeline; today only `provider.turn.started` carries a turn_id in
   real sessions);
2. **token actuals** — managed runs (`auspex run`) capture per-turn
   `total_tokens` (the provider result line's `usage`, input + output,
   turn-stamped — which also wakes the ADR-047 cohort ladder); native
   hook turns still lack a source (the statusline carries no per-turn
   tokens), so token coverage speaks for managed-run turns only;
3. **sample volume** — cohorts below the ADD §15.2 gate (8) are
   reported, never fitted.

With `--observations`, `report.py` emits the token-coverage section:
per-turn predicted `token_p50/p80/p90` joined with the same turn's
actual `total_tokens` on turn_id (only turns with BOTH sides count, and
the join count is reported), yielding the fraction of turns whose actual
landed ≤ P50 / ≤ P80 / ≤ P90 — the replay-backed calibration evidence
`Historical_Replay_Report.md` could not produce.

## Per-turn actuals (observations export)

Statusline usage totals are SESSION-CUMULATIVE (`total_cost_usd` only
grows), so "this turn cost $0.12" is a subtraction across snapshots —
a modeling step the Go bridges refuse (capture-before-model discipline).
`auspex export observations` therefore ships the raw series
(usage/context/quota snapshots) plus turn boundary events, and
`calibration/observations.py` derives the deltas HERE, where modeling is
allowed. The attribution is explicitly best-effort:

- snapshots lag the work they measure, so samples between a turn's
  terminal event and the next turn's start are attributed to the
  finished turn;
- a turn with no pre-turn baseline sample is **underivable**, never
  assumed to start from 0 (resumed sessions and retention-truncated
  series make a 0 baseline a fabrication);
- compaction can shrink `used_tokens`, so **negative context deltas are
  real and surfaced as-is with a note — never clamped silently**.

Managed-run usage rows are the exception that needs no modeling:
`auspex run` persists a per-turn, turn-stamped usage event carrying the
provider result line's own token accounting
(`input_tokens`/`output_tokens`/`cache_*_input_tokens`/`total_tokens`),
so those actuals are read off directly and joined on `turn_id`.

## Layout

- `calibration/load.py` — JSONL loader + schema validation
  (`auspex.calibration-export.v1`).
- `calibration/observations.py` — JSONL loader + schema validation
  (`auspex.observations-export.v1`) and the per-turn cost/context delta
  derivation. Runs standalone (text or `--json`) and feeds report.py's
  per-turn actuals section.
- `calibration/report.py` — readiness + (data permitting) coverage
  report; `--observations observations.jsonl` adds the per-turn actuals
  readiness section. Output is plain text to stdout; `--json` for
  machine form.

De-identification note: the exports contain opaque row IDs, enums,
numbers, and timestamps only (see `internal/retention/export.go`'s
package comment; the observations export is a payload WHITELIST
projection, see `internal/retention/observations.go`). Nothing in this
directory may join them back to prompts, paths, or identities — there is
nothing to join to.
