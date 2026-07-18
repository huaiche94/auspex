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
  quantifies the #42/#66 under-forecast. It also
  stratifies that join by the #20 cohort triple (**per-cohort cost
  residual**, #72 Phase 2): for each cohort meeting the §15.2 gate (≥ 8
  *joined* turns) it fits the empirical factor by which the forecast's
  high bound under-forecasts real cost (median and P90 of `actual/high`);
  cohorts below the gate or with an unlabeled axis are reported, never
  fitted. The Go forecast is untouched — these factors are inputs a future
  phase (#66's cache-aware cost model) would consume, and the descriptive
  half of that phase now ships as `cost_classes.py` (below).
- `runway.py` — the runway calibration backtest (#90 Phase B): every
  persisted `runway_forecasts` row (migration 0042) scored against the
  realized quota trajectory reconstructed from
  `provider.quota.observed`/`provider.rate_limit.hit` events. Reads the
  local SQLite DB directly (those forecasts live only there, not in either
  JSONL export), strictly **read-only** (URI `mode=ro`); a missing or
  unreadable DB is a disclosed gap, never a crash. `report.py` folds its
  section in automatically. All numbers stay descriptive — the model's
  `risk_score` is an uncalibrated score, and the per-bucket hit rates are
  OBSERVED frequencies over correlated samples, never the model's
  probability.
- `cost_classes.py` — the four-class cost **decomposition** (#66 item a,
  the descriptive/research side of the cache-aware cost model). Prices the
  captured per-turn four-class token actuals
  (fresh/cache-creation/cache-read/output on `provider.turn.completed` and
  managed `provider.usage.observed`) with the **explicit-cache**
  `FourClassCost` formula (mirroring `internal/pricing`), and reports the
  per-class **dollar shares** plus the cache-blind under-count factor —
  quantifying empirically that cache-read dominates the bill though its
  unit price is the cheapest class, the mechanism behind the ~7–9× cost
  under-forecast `report.py`'s cost residual measures. Reads the DB
  directly, strictly **read-only**, like `runway.py`; a missing/unreadable
  DB is a disclosed gap, never a crash. Codex/GPT **implicit-cache** turns
  (which the explicit formula does not price) and pre-ADR-051 turns without
  four-class capture are disclosed and skipped, never force-priced (unknown
  is not zero). It is DESCRIPTIVE only — shares of a PAST bill priced with
  list-price placeholders, never a forecast (Constitution §7); the
  four-class *predicted* cost is #66 item b, gated on #11 data.

The predictor these reports are meant to eventually feed lives in
`internal/predictor/`; the exporters live in `internal/retention/`
(`export.go`, `observations.go`).
