#!/usr/bin/env python3
"""Data-readiness + calibration-coverage report over a calibration export.

Usage:
    python3 research/calibration/report.py calibration.jsonl [--json] \\
        [--observations observations.jsonl]

Grounding discipline (research/README.md): cohorts below the ADD §15.2
sample gate (8) are REPORTED as insufficient, never fitted. With sparse
data the readiness section is the report's whole value; the coverage
sections activate by themselves as capture fills in.

Always computed from the calibration export alone:

  * TOKEN COVERAGE: records carry per-turn predicted token_p50/p80/p90,
    and the same record now carries the turn's ACTUAL total_tokens where
    one was captured (#72 item 4: the Stop hook reads the session
    transcript's exact per-turn usage onto turn.completed, and the Go
    exporter joins it — managed-run usage rows land in the same field).
    Joining predicted vs actual on turn_id yields the fraction of turns
    whose actual landed <= P50 / <= P80 / <= P90. Only turns with BOTH
    sides count, and the join count is always reported. Hook turns from
    before #72's capture have no actual anywhere and can never join — an
    honest, permanent gap for that history.
  * DURATION COVERAGE (#62): records carry the predicted wall-clock band
    duration_p50_ns..duration_p90_ns and, where a turn-stamped usage
    event existed, the same turn's actual_duration_ms; joining the two
    on turn_id reports band containment (within/below/above), ns→ms
    reconciled here.

Independently of the exports, the report appends the RUNWAY CALIBRATION
BACKTEST (#90 Phase B / #11 — runway.py): every persisted runway
forecast (runway_forecasts, migration 0042) scored against the realized
quota trajectory reconstructed from provider.quota.observed /
provider.rate_limit.hit events. The forecasts live in the local SQLite
DB, not in either JSONL export, so this section reads the DB directly —
strictly read-only (URI mode=ro) — from --db, or the standard local
location when --db is omitted (which is how the weekly job picks it up
automatically). No DB found, or a DB that cannot be opened, is a
disclosed readiness gap, never a crash. All of its numbers stay
descriptive: the model's risk_score is an uncalibrated score, and the
per-bucket hit rates are OBSERVED frequencies over correlated samples
(disclosed in runway.py), never the model's probability.

It also appends the FOUR-CLASS COST DECOMPOSITION (#66 item a —
cost_classes.py): the captured per-turn four-class token actuals
(fresh/cache-creation/cache-read/output on provider.turn.completed and
managed provider.usage.observed) priced with the explicit-cache
FourClassCost formula, decomposed into per-class dollar shares. Like the
runway backtest it reads the SQLite DB directly (read-only), so no DB /
an unreadable DB is a disclosed gap, never a crash. It is DESCRIPTIVE,
not a forecast: the shares describe where a PAST bill went on this
dataset (priced with list-price placeholders), quantifying empirically
that cache-read dominates the bill though its unit price is the cheapest
class — the mechanism behind the ~7–9× cost under-forecast the per-cohort
cost residual measures. It does NOT build the four-class PREDICTED cost
(that is #66 item b, gated on #11 data).

With --observations, the report additionally folds in:

  * per-turn ACTUALS readiness derived from `auspex export observations`
    (observations.py's best-effort attribution): how many turns exist,
    how many are closed by a terminal event, and how many have derivable
    cost/context deltas;
  * COST-BAND COVERAGE (#72 Phase 1): the predicted cost band
    (cost_low_usd..cost_high_usd, priced from the token forecast by
    internal/pricing — the exact band the forecast card showed) joined
    against the per-turn cost delta observations.py derives. Unlike
    tokens, a per-turn cost delta IS derivable from native hook telemetry,
    so hook turns join here — the calibration opening #72 identifies.
  * PER-COHORT COST RESIDUAL (#72 Phase 2): the cost join stratified by
    the #20 cohort triple; for each cohort meeting the §15.2 gate (>= 8
    JOINED turns), the empirical factor by which the forecast's high bound
    under-forecasts real cost (median and P90 of actual/high). Cohorts
    below the gate are reported, never fitted — the Go forecast is
    untouched; these factors are inputs a future phase (#66) would consume.
  * managed-run usage rows as an additional token-actual source for the
    token join (the pre-#72 path, still honored for older exports).
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from collections import Counter
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import cost_classes  # noqa: E402
import runway  # noqa: E402
from load import Record, load  # noqa: E402
from observations import derive_turn_actuals, parse_ts, summarize_turn_actuals  # noqa: E402
from observations import load as load_observations  # noqa: E402
from observations import TurnActuals  # noqa: E402

# ADD §15.2's "count(similar) >= 8" gate, the same constant the Go side
# uses (RuleTokenForecaster.MinSimilarSamples, minSimilarTurnSamples).
SAMPLE_GATE = 8


def compute_runway(db_path: Path | None) -> tuple[dict | None, str | None]:
    """Run the runway calibration backtest (runway.py) against the local
    Auspex DB, strictly read-only, and degrade every failure to a
    disclosed readiness gap — this report must never crash on a missing or
    unreadable DB (the forecasts live only in SQLite, not in either JSONL
    export). Returns (result, None) on success, or (None, gap) when there
    is no DB to score or it cannot be opened read-only.

    With no --db, the standard local location is probed (the same path the
    weekly job resolves), so the section activates by itself once a DB
    exists — mirroring how the coverage sections activate as capture fills
    in."""
    path = db_path if db_path is not None else runway.default_db_path()
    if path is None:
        return None, (
            "runway calibration backtest skipped: no Auspex DB at the "
            "standard local location and no --db given — pass --db to score "
            "persisted runway forecasts against realized quota trajectories"
        )
    if not path.is_file():
        return None, (
            f"runway calibration backtest skipped: no DB at {path} — pass an "
            "existing --db path (opened read-only) to score runway forecasts"
        )
    try:
        return runway.run_backtest(path), None
    except sqlite3.Error as exc:
        return None, (
            f"runway calibration backtest skipped: could not open {path} "
            f"read-only ({exc}) — the DB is unreadable, disclosed as a gap"
        )


def compute_cost_classes(db_path: Path | None) -> tuple[dict | None, str | None]:
    """Run the four-class cost decomposition (cost_classes.py) against the
    local Auspex DB, strictly read-only, and degrade every failure to a
    disclosed readiness gap — the per-turn four-class actuals live only in
    SQLite (provider.turn.completed / managed provider.usage.observed), not
    in either JSONL export, so this section reads the DB directly like the
    runway backtest. Returns (result, None) on success, or (None, gap) when
    there is no DB to price or it cannot be opened read-only.

    DESCRIPTIVE, not calibrated: the returned per-class shares describe
    where a PAST bill went on this dataset (priced with list-price
    placeholders), never a forecast — the same posture as the runway
    section's uncalibrated scores (Constitution §7)."""
    path = db_path if db_path is not None else cost_classes.default_db_path()
    if path is None:
        return None, (
            "four-class cost decomposition skipped: no Auspex DB at the "
            "standard local location and no --db given — pass --db to price "
            "captured per-turn four-class actuals"
        )
    if not path.is_file():
        return None, (
            f"four-class cost decomposition skipped: no DB at {path} — pass "
            "an existing --db path (opened read-only) to price captured "
            "four-class actuals"
        )
    try:
        return cost_classes.run_decomposition(path), None
    except sqlite3.Error as exc:
        return None, (
            f"four-class cost decomposition skipped: could not open {path} "
            f"read-only ({exc}) — the DB is unreadable, disclosed as a gap"
        )


def token_coverage(records: list[Record], observations=()) -> dict:
    """Join per-turn predicted token quantiles with same-turn actual
    total_tokens — record-embedded (#72's export-side join covers both
    hook-transcript and managed-run captures) and, when an observations
    export is supplied, managed-run usage rows too — and report coverage.

    Honest gates: only turns with BOTH a prediction and an actual count
    (the join count is part of the result); each quantile's rate is
    computed over the joined rows that actually predicted that quantile;
    zero evaluable rows yields a rate of None, never a fabricated 0 or
    100 (unknown is not zero).
    """
    # Latest actual per turn. The Go side's turn-scoped idempotency key
    # makes more than one usage row per turn unexpected; should one
    # appear anyway (e.g. a re-captured turn), the latest observation
    # wins — mirroring the parser's own last-wins result-line rule —
    # rather than silently double-counting the turn.
    timed: dict = {}
    for obs in observations:
        if obs.event_type != "provider.usage.observed":
            continue
        if obs.turn_id is None or obs.total_tokens is None:
            continue
        ts = parse_ts(obs.occurred_at, f"usage sample for turn {obs.turn_id}")
        prev = timed.get(obs.turn_id)
        if prev is None or ts >= prev[0]:
            timed[obs.turn_id] = (ts, obs.total_tokens)
    actuals: dict = {turn_id: total for turn_id, (_, total) in timed.items()}

    # Record-embedded actuals (#72) override the observation-derived value
    # when both exist: the exporter already applied its own last-wins rule
    # over the same underlying events, so the record's figure is the
    # freshest single source.
    for r in records:
        if r.actual_total_tokens is not None:
            actuals[r.turn_id] = r.actual_total_tokens

    predicted = [
        r
        for r in records
        if any(q is not None for q in (r.token_p50, r.token_p80, r.token_p90))
    ]
    joined = [(r, actuals[r.turn_id]) for r in predicted if r.turn_id in actuals]

    coverage = {}
    for field, name in (("token_p50", "p50"), ("token_p80", "p80"), ("token_p90", "p90")):
        rows = [(getattr(r, field), actual) for r, actual in joined if getattr(r, field) is not None]
        covered = sum(1 for quantile, actual in rows if actual <= quantile)
        coverage[name] = {
            "evaluable": len(rows),
            "covered": covered,
            "rate": (covered / len(rows)) if rows else None,
        }

    return {
        "predicted_turns": len(predicted),
        "actual_turns": len(actuals),
        "joined_turns": len(joined),
        "coverage": coverage,
    }


def cost_coverage(records: list[Record], turns: list[TurnActuals]) -> dict:
    """Join each turn's PREDICTED cost band (cost_low_usd..cost_high_usd —
    the exact band the forecast card showed, priced from the token forecast
    by internal/pricing) with its ACTUAL per-turn cost delta (observations.py's
    best-effort attribution over the session-cumulative total_cost_usd
    series), and report band containment.

    This is the #72 hook-mode calibration opening: unlike a token
    total_tokens actual (managed-run only — the statusline carries no
    per-turn tokens), a per-turn COST delta is derivable from native hook
    telemetry alone, so native-hook turns CAN join here. That is what
    unblocks a calibrated output without `auspex run`.

    Honest gates mirror token_coverage: only turns with BOTH a predicted
    band and a derivable actual count (the join count is part of the
    result); a degenerate band (low == high) still counts; zero joined
    rows yields rate None, never a fabricated 0/100.

    Band containment, not a point residual: the shipped cost forecast is a
    RANGE (ADR-043), so the honest question is whether the actual landed
    inside it. `below_band` (actual < low) and `above_band` (actual > high)
    are counted separately because they mean opposite things — `above_band`
    dominating is the systematic UNDER-forecast the token cold-start (#42)
    and cache-blind pricing (#66) predict, and seeing it quantified here is
    the first real calibration signal from hook data.
    """
    # Latest derivable actual per turn. derive_turn_actuals emits at most
    # one row per (session, turn.started); a turn_id colliding across
    # sessions is not expected, but last-wins keeps the join deterministic
    # (mirrors token_coverage's last-wins rule).
    actuals: dict = {}
    for t in turns:
        if t.turn_id is None or t.cost_delta_usd is None:
            continue
        actuals[t.turn_id] = t.cost_delta_usd

    predicted = [
        r for r in records if r.cost_low_usd is not None and r.cost_high_usd is not None
    ]
    joined = [(r, actuals[r.turn_id]) for r in predicted if r.turn_id in actuals]

    within = below = above = 0
    for r, actual in joined:
        if actual < r.cost_low_usd:
            below += 1
        elif actual > r.cost_high_usd:
            above += 1
        else:
            within += 1

    n = len(joined)
    return {
        "predicted_turns": len(predicted),
        "actual_turns": len(actuals),
        "joined_turns": n,
        "within_band": within,
        "below_band": below,
        "above_band": above,
        "containment_rate": (within / n) if n else None,
    }


def _percentile(sorted_vals: list[float], q: float) -> float:
    """Linear-interpolated percentile (q in [0, 1]) over a NON-EMPTY sorted
    list — the same 'linear' method numpy/statistics.quantiles default to,
    written out in stdlib so this module stays dependency-free
    (research/README.md: standard library only)."""
    if not sorted_vals:
        raise ValueError("percentile of empty sequence")
    if len(sorted_vals) == 1:
        return sorted_vals[0]
    pos = q * (len(sorted_vals) - 1)
    lo = int(pos)
    if lo + 1 >= len(sorted_vals):
        return sorted_vals[-1]
    return sorted_vals[lo] + (pos - lo) * (sorted_vals[lo + 1] - sorted_vals[lo])


def cost_residual_by_cohort(records: list[Record], turns: list[TurnActuals]) -> dict:
    """Phase 2 of #72: stratify the cost join by the #20 cohort triple
    (provider, model_family, effort) and, for each cohort that meets the ADD
    §15.2 gate, FIT the empirical cost residual — how far the shipped
    forecast's high bound sits from the cohort's real per-turn cost.

    Grounding discipline (research/README.md): a cohort is fitted only when
    it has >= SAMPLE_GATE **joined** turns AND every identity axis is labeled;
    every other cohort is reported with its count and `fitted=False`, never
    given a fabricated coefficient. The gate is on JOINED turns (predicted
    band + derivable cost actual), not on predictions — a cohort with 60
    predictions but 3 cost actuals cannot be fitted on 3 points.

    Residual definition (labeled, honest): the shipped forecast is a RANGE
    (ADR-043); its high bound H = TokenP90 × output-price is the upper
    estimate the user sees. Per joined turn the ratio ρ = actual / H says how
    far the actual overshot that bound. The reported per-cohort factors are
    quantiles of ρ:
      * factor_high_p50 = median(ρ): multiply H by this to CENTER it on the
        cohort's typical turn (~half still exceed);
      * factor_high_p90 = P90(ρ): multiply H by this to make H a real ~P90
        upper bound for the cohort.
    A future forecast phase (Phase 3 / #66's cache-aware cost model) is what
    would fold these into the pipeline; this module only fits and REPORTS
    them — the Go forecast is untouched (capture-before-model)."""
    actuals: dict = {}
    for t in turns:
        if t.turn_id is None or t.cost_delta_usd is None:
            continue
        actuals[t.turn_id] = t.cost_delta_usd

    by_cohort: dict = {}
    for r in records:
        if r.cost_low_usd is None or r.cost_high_usd is None:
            continue
        if r.turn_id not in actuals:
            continue
        by_cohort.setdefault(r.cohort, []).append((r.cost_high_usd, actuals[r.turn_id]))

    cohorts: list = []
    for cohort, rows in sorted(by_cohort.items(), key=lambda kv: (-len(kv[1]), kv[0])):
        n = len(rows)
        provider, family, effort = cohort
        labeled = "?" not in cohort
        fitted = n >= SAMPLE_GATE and labeled
        entry = {
            "provider": provider,
            "model_family": family,
            "effort": effort,
            "joined_turns": n,
            "labeled": labeled,
            "fitted": fitted,
            "above_band": sum(1 for high, actual in rows if actual > high),
            "median_actual_usd": None,
            "p90_actual_usd": None,
            "median_predicted_high_usd": None,
            "factor_high_p50": None,
            "factor_high_p90": None,
        }
        if fitted:
            acts = sorted(actual for _, actual in rows)
            highs = sorted(high for high, _ in rows)
            ratios = sorted(actual / high for high, actual in rows if high > 0)
            entry["median_actual_usd"] = _percentile(acts, 0.5)
            entry["p90_actual_usd"] = _percentile(acts, 0.9)
            entry["median_predicted_high_usd"] = _percentile(highs, 0.5)
            if ratios:
                entry["factor_high_p50"] = _percentile(ratios, 0.5)
                entry["factor_high_p90"] = _percentile(ratios, 0.9)
        cohorts.append(entry)

    return {
        "sample_gate": SAMPLE_GATE,
        "joined_turns": sum(len(v) for v in by_cohort.values()),
        "fitted_cohorts": sum(1 for c in cohorts if c["fitted"]),
        "cohorts": cohorts,
    }


def duration_coverage(records: list[Record]) -> dict:
    """Join each turn's PREDICTED duration band (duration_p50_ns..
    duration_p90_ns — the #62 scope-estimator forecast, converted ns→ms
    here and nowhere else) with its ACTUAL per-turn duration
    (actual_duration_ms — the turn's provider.usage.observed
    total_duration_ms, joined by the Go exporter), and report band
    containment. Both sides ride the calibration record itself, so no
    observations export is needed — the report side of the #62 rail.

    Honest gates mirror cost_coverage: only turns with BOTH a predicted
    band and an actual count (the join count is part of the result); a
    degenerate band (P50 == P90) still counts; zero joined rows yields
    rate None, never a fabricated 0/100.

    Band containment, not a point residual, for the same reason as the
    cost band: the shipped forecast is a RANGE, so the honest question is
    whether the actual landed inside it. below_band (actual < P50 end)
    and above_band (actual > P90 end) are counted separately — above_band
    dominating is the systematic under-forecast signal.
    """
    # Latest actual per turn (last-wins keeps the join deterministic,
    # mirroring token_coverage/cost_coverage's rule for duplicate rows).
    actuals: dict = {}
    for r in records:
        if r.actual_duration_ms is not None:
            actuals[r.turn_id] = r.actual_duration_ms

    predicted = [
        r
        for r in records
        if r.duration_p50_ns is not None and r.duration_p90_ns is not None
    ]
    joined = [(r, actuals[r.turn_id]) for r in predicted if r.turn_id in actuals]

    within = below = above = 0
    for r, actual in joined:
        low_ms = r.duration_p50_ns / 1_000_000
        high_ms = r.duration_p90_ns / 1_000_000
        if actual < low_ms:
            below += 1
        elif actual > high_ms:
            above += 1
        else:
            within += 1

    n = len(joined)
    return {
        "predicted_turns": len(predicted),
        "actual_turns": len(actuals),
        "joined_turns": n,
        "within_band": within,
        "below_band": below,
        "above_band": above,
        "containment_rate": (within / n) if n else None,
    }


def build_report(
    records: list[Record],
    turn_actuals: dict | None = None,
    token_cov: dict | None = None,
    cost_cov: dict | None = None,
    cost_residual: dict | None = None,
    duration_cov: dict | None = None,
    runway_cal: dict | None = None,
    runway_gap: str | None = None,
    cost_classes_result: dict | None = None,
    cost_classes_gap: str | None = None,
) -> dict:
    total = len(records)
    labeled = sum(1 for r in records if r.model_family is not None)
    with_actual = sum(1 for r in records if r.actual_known)
    outcomes = Counter(r.actual_outcome for r in records if r.actual_known)
    cohort_sizes = Counter(r.cohort for r in records)

    gaps: list[str] = []
    if total == 0:
        gaps.append("no prediction rows exported yet — dogfood more turns")
    if total and with_actual == 0:
        gaps.append(
            "actual_known=0 on every row: outcome events lack turn "
            "correlation (issue #1's gap) — no residual can be computed"
        )
    if total and labeled < total:
        gaps.append(
            f"{total - labeled}/{total} rows carry no model/effort labels "
            "(predate #20 Phase 0 capture) — stratification excludes them"
        )
    if token_cov is None:
        gaps.append(
            "token P50/P80/P90 coverage was not assessed — pass a "
            "calibration export to compute it"
        )
    elif token_cov["joined_turns"] == 0:
        gaps.append(
            "0 turns join a token prediction with a same-turn total_tokens "
            "actual: both managed runs (`auspex run`) and native hook turns "
            "capture per-turn actuals now (#72 reads the Stop hook's "
            "transcript) — dogfood more turns; history from before #72's "
            "capture has no actual anywhere and can never join"
        )
    if turn_actuals is None:
        gaps.append(
            "no observations export supplied (--observations) — per-turn "
            "cost/context ACTUAL deltas were not assessed; export with "
            "`auspex export observations` to close issue #11's actuals gap"
        )
    elif turn_actuals["cost_derivable_turns"] == 0:
        gaps.append(
            "0 turns have a derivable cost delta: the observation series "
            "lacks pre-turn baselines or in-window usage samples (or every "
            "turn is unclosed) — per-turn cost actuals remain blocked"
        )
    if cost_cov is not None and cost_cov["joined_turns"] == 0:
        gaps.append(
            "0 turns join a predicted cost band with a derivable per-turn "
            "cost actual — even though hook telemetry CAN supply the actual "
            "(unlike tokens): check that predictions carry token quantiles "
            "(the cost band derives from them) and the observations export "
            "brackets the same turn_ids"
        )
    if (
        cost_residual is not None
        and cost_residual["joined_turns"] > 0
        and cost_residual["fitted_cohorts"] == 0
    ):
        gaps.append(
            "cost actuals join, but no labeled cohort has "
            f">= {SAMPLE_GATE} joined turns yet — per-cohort cost residual "
            "(#72 Phase 2) not fitted; dogfood more labeled turns"
        )
    if duration_cov is not None and duration_cov["joined_turns"] == 0:
        gaps.append(
            "0 turns join a predicted duration band with an "
            "actual_duration_ms (#62): the actual comes from turn-stamped "
            "provider.usage.observed events (managed runs today) — "
            "dogfood turns through `auspex run`, and check predictions "
            "carry the duration_p50/p90 forecast"
        )
    if runway_gap is not None:
        # No DB / unopenable DB: disclosed here so a report over the JSONL
        # exports alone still names what the runway backtest could not read.
        gaps.append(runway_gap)
    elif runway_cal is not None and runway_cal["forecasts_total"] == 0:
        gaps.append(
            "no persisted runway forecasts yet (runway_forecasts, migration "
            "0042 is empty) — the Stop-hook driver (#90/#10) fills it as "
            "turns run; nothing to calibrate against realized quota yet"
        )
    if cost_classes_gap is not None:
        # No DB / unopenable DB: disclosed here so a report over the JSONL
        # exports alone still names what the four-class decomposition could
        # not read (the per-turn four-class actuals live only in SQLite).
        gaps.append(cost_classes_gap)
    elif (
        cost_classes_result is not None
        and cost_classes_result["priced"]["turns"] == 0
    ):
        gaps.append(
            "no explicit-cache turns to price for the four-class cost "
            "decomposition (#66 item a): captured turns are implicit-cache "
            "(codex/gpt) or predate four-class capture (ADR-051) — dogfood "
            "Claude-family turns; nothing to decompose yet"
        )

    cohorts = [
        {
            "provider": provider,
            "model_family": family,
            "effort": effort,
            "rows": n,
            # A cohort with an unlabeled axis is not a real cohort — it is
            # the bucket of rows stratification cannot place. It never
            # "meets the gate" no matter how large (unknown is not zero).
            "labeled": "?" not in (provider, family, effort),
            "meets_gate": n >= SAMPLE_GATE and "?" not in (provider, family, effort),
        }
        for (provider, family, effort), n in sorted(
            cohort_sizes.items(), key=lambda kv: (-kv[1], kv[0])
        )
    ]

    return {
        "total_rows": total,
        "live_rows": sum(1 for r in records if r.source == "live"),
        "archived_rows": sum(1 for r in records if r.source == "archived"),
        "labeled_rows": labeled,
        "actual_known_rows": with_actual,
        "outcomes": dict(sorted(outcomes.items(), key=lambda kv: str(kv[0]))),
        "cohorts": cohorts,
        "cohorts_meeting_gate": sum(1 for c in cohorts if c["meets_gate"]),
        "sample_gate": SAMPLE_GATE,
        # None (not zeros) when no observations export was supplied —
        # unknown is not zero.
        "per_turn_actuals": turn_actuals,
        "token_coverage": token_cov,
        "cost_coverage": cost_cov,
        "cost_residual": cost_residual,
        "duration_coverage": duration_cov,
        # The runway backtest reads the SQLite DB directly (read-only), so
        # unlike the coverage sections it can be a disclosed gap (no DB /
        # unreadable) rather than a result — both are surfaced, never a
        # crash. None/None means the report ran without a runway assessment.
        "runway_calibration": runway_cal,
        "runway_gap": runway_gap,
        # The four-class cost decomposition (#66 item a) also reads the
        # SQLite DB directly (read-only), so like the runway backtest it can
        # be a disclosed gap (no DB / unreadable) rather than a result —
        # both are surfaced, never a crash. None/None means the report ran
        # without a four-class assessment.
        "cost_classes": cost_classes_result,
        "cost_classes_gap": cost_classes_gap,
        "readiness_gaps": gaps,
    }


def render_text(report: dict) -> str:
    lines = [
        "calibration data readiness",
        "==========================",
        f"rows: {report['total_rows']} "
        f"(live {report['live_rows']}, archived {report['archived_rows']})",
        f"identity-labeled: {report['labeled_rows']}/{report['total_rows']}",
        f"with actual outcome: {report['actual_known_rows']}/{report['total_rows']}",
    ]
    if report["outcomes"]:
        pairs = ", ".join(f"{k}: {v}" for k, v in report["outcomes"].items())
        lines.append(f"outcomes: {pairs}")

    lines.append("")
    lines.append(f"cohorts (gate = {report['sample_gate']} rows):")
    if not report["cohorts"]:
        lines.append("  (none)")
    for c in report["cohorts"]:
        if not c["labeled"]:
            mark = "unlabeled — excluded from gating"
        elif c["meets_gate"]:
            mark = "meets gate"
        else:
            mark = "below gate"
        lines.append(
            f"  {c['provider']}/{c['model_family']}/{c['effort']}: "
            f"{c['rows']} rows — {mark}"
        )

    actuals = report["per_turn_actuals"]
    if actuals is not None:
        lines.append("")
        lines.append("per-turn actuals readiness (observations export):")
        lines.append(f"  turn.started events: {actuals['turns']}")
        lines.append(f"  closed by a terminal event: {actuals['closed_turns']}")
        lines.append(
            f"  with derivable cost delta: {actuals['cost_derivable_turns']}"
        )
        lines.append(
            f"  with derivable context delta: {actuals['context_derivable_turns']}"
        )
        if actuals["negative_context_deltas"]:
            lines.append(
                f"  negative context deltas (compaction; surfaced as-is): "
                f"{actuals['negative_context_deltas']}"
            )
        if actuals["negative_cost_deltas"]:
            lines.append(
                f"  NEGATIVE cost deltas (input suspect — cumulative cost "
                f"cannot shrink): {actuals['negative_cost_deltas']}"
            )

    token_cov = report["token_coverage"]
    if token_cov is not None:
        lines.append("")
        lines.append(
            "token coverage (predicted vs per-turn actuals — hook transcript "
            "capture and managed runs — joined on turn_id):"
        )
        lines.append(
            f"  turns with a token prediction: {token_cov['predicted_turns']}"
        )
        lines.append(
            f"  turns with a total_tokens actual: {token_cov['actual_turns']}"
        )
        lines.append(f"  joined turns (both sides): {token_cov['joined_turns']}")
        if token_cov["joined_turns"]:
            for q in ("p50", "p80", "p90"):
                c = token_cov["coverage"][q]
                if c["evaluable"]:
                    lines.append(
                        f"  actual <= {q.upper()}: {c['covered']}/{c['evaluable']} "
                        f"({100.0 * c['rate']:.0f}%)"
                    )
                else:
                    lines.append(
                        f"  actual <= {q.upper()}: no joined row predicted this quantile"
                    )
        else:
            lines.append("  (no joined turns — coverage rates not computable)")

    cost_cov = report["cost_coverage"]
    if cost_cov is not None:
        lines.append("")
        lines.append(
            "cost-band coverage (predicted band vs per-turn cost delta, "
            "joined on turn_id):"
        )
        lines.append(
            f"  turns with a predicted cost band: {cost_cov['predicted_turns']}"
        )
        lines.append(
            f"  turns with a derivable cost actual: {cost_cov['actual_turns']}"
        )
        lines.append(f"  joined turns (both sides): {cost_cov['joined_turns']}")
        if cost_cov["joined_turns"]:
            lines.append(
                f"  actual within band: {cost_cov['within_band']}/"
                f"{cost_cov['joined_turns']} "
                f"({100.0 * cost_cov['containment_rate']:.0f}%)"
            )
            lines.append(
                f"  actual below band (cost over-forecast): {cost_cov['below_band']}"
            )
            lines.append(
                f"  actual above band (cost under-forecast): {cost_cov['above_band']}"
            )
        else:
            lines.append("  (no joined turns — containment not computable)")

    cost_res = report["cost_residual"]
    if cost_res is not None and cost_res["cohorts"]:
        lines.append("")
        lines.append(
            f"per-cohort cost residual (#72 Phase 2 — fitted at >= "
            f"{cost_res['sample_gate']} joined turns; others reported, never fitted):"
        )
        for c in cost_res["cohorts"]:
            label = f"{c['provider']}/{c['model_family']}/{c['effort']}"
            if c["fitted"]:
                lines.append(
                    f"  {label}: n={c['joined_turns']} — median actual "
                    f"${c['median_actual_usd']:.2f} (P90 ${c['p90_actual_usd']:.2f}) "
                    f"vs forecast high ${c['median_predicted_high_usd']:.2f}; "
                    f"high-bound under-forecasts {c['factor_high_p50']:.1f}x median "
                    f"({c['factor_high_p90']:.1f}x P90); "
                    f"{c['above_band']}/{c['joined_turns']} above band"
                )
            elif not c["labeled"]:
                lines.append(
                    f"  {label}: n={c['joined_turns']} — unlabeled, not fitted"
                )
            else:
                lines.append(
                    f"  {label}: n={c['joined_turns']} — below gate "
                    f"({cost_res['sample_gate']}), not fitted"
                )

    duration_cov = report["duration_coverage"]
    if duration_cov is not None:
        lines.append("")
        lines.append(
            "duration-band coverage (predicted P50–P90 vs per-turn "
            "actual_duration_ms, joined on turn_id):"
        )
        lines.append(
            f"  turns with a predicted duration band: "
            f"{duration_cov['predicted_turns']}"
        )
        lines.append(
            f"  turns with a duration actual: {duration_cov['actual_turns']}"
        )
        lines.append(f"  joined turns (both sides): {duration_cov['joined_turns']}")
        if duration_cov["joined_turns"]:
            lines.append(
                f"  actual within band: {duration_cov['within_band']}/"
                f"{duration_cov['joined_turns']} "
                f"({100.0 * duration_cov['containment_rate']:.0f}%)"
            )
            lines.append(
                f"  actual below band (duration over-forecast): "
                f"{duration_cov['below_band']}"
            )
            lines.append(
                f"  actual above band (duration under-forecast): "
                f"{duration_cov['above_band']}"
            )
        else:
            lines.append("  (no joined turns — containment not computable)")

    runway_cal = report["runway_calibration"]
    runway_gap = report["runway_gap"]
    lines.append("")
    if runway_cal is not None:
        lines.extend(runway.render_section(runway_cal))
        lines.append(
            f"  (db: {runway_cal.get('db_path', '?')}, opened read-only)"
        )
    else:
        lines.append(
            "runway calibration backtest (persisted forecasts vs realized "
            "quota trajectory, read-only DB):"
        )
        lines.append(f"  skipped — {runway_gap}")

    cost_cls = report["cost_classes"]
    cost_cls_gap = report["cost_classes_gap"]
    lines.append("")
    if cost_cls is not None:
        lines.extend(cost_classes.render_section(cost_cls))
        lines.append(f"  (db: {cost_cls.get('db_path', '?')}, opened read-only)")
    else:
        lines.append(
            "four-class cost decomposition (captured per-turn actuals priced "
            "with the explicit-cache formula, read-only DB — DESCRIPTIVE, not "
            "a forecast):"
        )
        lines.append(f"  skipped — {cost_cls_gap}")

    lines.append("")
    lines.append("readiness gaps (calibration blocked until closed):")
    for gap in report["readiness_gaps"]:
        lines.append(f"  - {gap}")
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("export", type=Path, help="calibration export JSONL path")
    parser.add_argument(
        "--observations",
        type=Path,
        default=None,
        help="observations export JSONL path (auspex export observations) "
        "for the per-turn actuals readiness section",
    )
    parser.add_argument(
        "--db",
        type=Path,
        default=None,
        help="Auspex SQLite DB path for the runway calibration backtest "
        "(#90 Phase B), opened strictly read-only; defaults to the standard "
        "local location when omitted. A missing or unreadable DB is a "
        "disclosed readiness gap, never a crash.",
    )
    parser.add_argument("--json", action="store_true", help="machine-readable output")
    args = parser.parse_args()

    records = list(load(args.export))
    turn_actuals = None
    cost_cov = None
    cost_residual = None
    observations: list = []
    if args.observations is not None:
        observations = list(load_observations(args.observations))
        turns = derive_turn_actuals(observations)
        turn_actuals = summarize_turn_actuals(turns)
        cost_cov = cost_coverage(records, turns)
        cost_residual = cost_residual_by_cohort(records, turns)
    # Token and duration coverage no longer need --observations: the
    # calibration records carry their own per-turn actuals (#72 for
    # tokens, #62 for duration; managed-run usage rows still fold into
    # the token join when an observations export is supplied).
    token_cov = token_coverage(records, observations)
    duration_cov = duration_coverage(records)
    # Read-only runway backtest over the SQLite DB (forecasts live there,
    # not in the JSONL exports). Any missing/unreadable DB comes back as a
    # disclosed gap, so the report never crashes on it.
    runway_cal, runway_gap = compute_runway(args.db)
    # Read-only four-class cost decomposition over the SQLite DB (#66 item
    # a): the per-turn four-class actuals live there, not in the JSONL
    # exports. Any missing/unreadable DB comes back as a disclosed gap, so
    # the report never crashes on it (same posture as the runway backtest).
    cost_classes_result, cost_classes_gap = compute_cost_classes(args.db)
    report = build_report(
        records,
        turn_actuals,
        token_cov,
        cost_cov,
        cost_residual=cost_residual,
        duration_cov=duration_cov,
        runway_cal=runway_cal,
        runway_gap=runway_gap,
        cost_classes_result=cost_classes_result,
        cost_classes_gap=cost_classes_gap,
    )

    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(render_text(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
