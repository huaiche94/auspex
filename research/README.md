# research/ — offline calibration pipeline (M13, issue #11)

The offline half of the calibration loop: reads `auspex export
calibration` JSONL, reports data readiness, and — once per-cohort sample
gates pass — produces the empirical quantiles and residual reports that
feed coefficients back into the predictor.

## Grounding discipline (binding)

Same rule as `Predictor_Improvement_Suggestions.md` §2.3 and
`docs/backlog/provider-model-effort-features.md`: **no coefficient
proposals without data.** This pipeline never emits a fitted number from
a cohort below its sample gate; below the gate it reports the gap
("insufficient samples", "actuals unknown", "unlabeled rows") instead.
Tuning against n≈0 is indistinguishable from guessing.

## Usage

```sh
# 1. Export the dataset (de-identified by construction, FR-170/171):
auspex export calibration --out calibration.jsonl

# 2. Data-readiness report (works from day zero — an empty dataset is a
#    valid, honest input):
python3 research/calibration/report.py calibration.jsonl
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
2. **token actuals** — no payload carries per-turn `total_tokens` yet
   (the ADR-047 ladder is dormant for the same reason);
3. **sample volume** — cohorts below the ADD §15.2 gate (8) are
   reported, never fitted.

Once gates pass, `report.py` also emits per-cohort predicted-vs-actual
coverage (did the actual land ≤ P50 / ≤ P80 / ≤ P90) — the replay-backed
calibration evidence `Historical_Replay_Report.md` could not produce.

## Layout

- `calibration/load.py` — JSONL loader + schema validation
  (`auspex.calibration-export.v1`).
- `calibration/report.py` — readiness + (data permitting) coverage
  report. Output is plain text to stdout; `--json` for machine form.

De-identification note: the export contains opaque row IDs, enums,
numbers, and timestamps only (see `internal/retention/export.go`'s
package comment). Nothing in this directory may join it back to prompts,
paths, or identities — there is nothing to join to.
