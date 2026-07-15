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
  calibration-coverage report over a calibration export. **Token
  coverage** (predicted quantiles vs same-turn `total_tokens` actuals,
  joined on `turn_id`) is always computed: since #72 item 4 the Stop
  hook reads the session transcript's exact per-turn usage onto the
  `turn.completed` event and the calibration export carries it as
  `actual_*_tokens` fields (managed-run captures land in the same
  fields), so native hook turns join too — only history from before
  that capture can never join. **Duration-band coverage** (#62) is
  likewise always computed: the predicted
  `duration_p50_ns..duration_p90_ns` band vs the same record's
  `actual_duration_ms` (ns→ms reconciled in the report), with
  within/below/above-band splits. `--observations observations.jsonl`
  folds in the per-turn actuals readiness section derived by
  `observations.py`, managed-run usage rows as an additional
  token-actual source (the pre-#72 path, still honored for older
  exports), and **cost-band coverage** (the predicted cost band
  `cost_low_usd..cost_high_usd` vs the per-turn cost delta
  `observations.py` derives), which reports band containment and,
  separately, actuals landing below (cost over-forecast) vs above
  (cost under-forecast) the band — the directional signal that
  quantifies the #42/#66 under-forecast.

The predictor these reports are meant to eventually feed lives in
`internal/predictor/`; the exporters live in `internal/retention/`
(`export.go`, `observations.go`).
