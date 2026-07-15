# research/calibration/ — offline calibration scripts (M13, issue #11)

> 🌐 English | [繁體中文](README.zh-TW.md)

The scripts behind the offline calibration pipeline described in
[`../README.md`](../README.md) — read that first for the grounding
discipline (no coefficient proposals without data; cohorts below their
sample gate are reported, never fitted) and the end-to-end usage.

Standard library only, Python ≥ 3.9; never a runtime dependency of the
Go binary. Inputs are the de-identified `auspex export` datasets
(FR-170/171).

## Files

- `load.py` — loader + schema validation for `auspex export
  calibration` JSONL (`auspex.calibration-export.v1`). Malformed lines
  and unknown schema versions fail loudly — a corrupt dataset must
  never silently shrink into a smaller "clean" one.
- `observations.py` — loader + schema validation for `auspex export
  observations` JSONL (`auspex.observations-export.v1`) and the
  per-turn cost/context delta derivation. Statusline totals are
  session-cumulative, so per-turn figures are a best-effort
  attribution model (documented with its limits in the module
  docstring): no pre-turn baseline means underivable — never an
  assumed 0 — and negative context deltas from compaction are surfaced
  as-is, never clamped. Runs standalone (`--json` for machine form).
- `report.py` — data-readiness + (data permitting)
  calibration-coverage report over a calibration export;
  `--observations observations.jsonl` folds in the per-turn actuals
  readiness section derived by `observations.py`, plus two coverage
  joins on `turn_id`: **token coverage** (predicted quantiles vs
  managed-run `total_tokens` actuals — managed runs only, since the
  statusline carries no per-turn tokens) and **cost-band coverage** (the
  predicted cost band `cost_low_usd..cost_high_usd` vs the per-turn cost
  delta `observations.py` derives). Cost coverage is the #72 hook-mode
  opening: a per-turn cost delta is derivable from native hook telemetry
  alone, so native-hook turns join here even when tokens cannot. It
  reports band containment and, separately, actuals landing below (cost
  over-forecast) vs above (cost under-forecast) the band — the
  directional signal that quantifies the #42/#66 under-forecast.

The predictor these reports are meant to eventually feed lives in
`internal/predictor/`; the exporters live in `internal/retention/`
(`export.go`, `observations.go`).
