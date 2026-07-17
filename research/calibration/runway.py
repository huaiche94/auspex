#!/usr/bin/env python3
"""Runway calibration backtest: score persisted runway forecasts against
the realized quota trajectory (#90 Phase B, issue #11).

Usage:
    python3 research/calibration/runway.py [auspex.db] [--json]

When no DB path is given, the standard local Auspex DB location is
probed (macOS: ~/Library/Application Support/auspex/auspex.db; XDG:
$XDG_DATA_HOME/auspex/auspex.db, else ~/.local/share/auspex/auspex.db).
The DB is ALWAYS opened read-only (SQLite URI mode=ro) — this module
never writes, and a missing or unopenable DB is reported as a gap,
never a crash.

Why this module reads SQLite while its siblings read JSONL exports:
runway forecasts (`runway_forecasts`, migration 0042) and the quota
event series they must be scored against are not part of either
`auspex export` dataset yet. Until an exporter exists, this module
reads exactly the columns it needs, nothing else: `runway_forecasts`'
numeric/enum columns, `provider_sessions.(id, provider)`, and the
`provider.quota.observed` / `provider.rate_limit.hit` rows of `events`
with only the whitelisted payload keys `limit_id` / `used_percent`
(the same vocabulary internal/report/load.go's loadQuotaSection and
internal/orchestrator/runwaydrive.go decode). No prompt text, paths,
or identities are touched — the columns read are opaque ids, enums,
numbers, and timestamps, matching the export de-identification
posture.

Go-side semantics mirrored (read, never modified):

  * One `runway_forecasts` row is the COMBINED worst-window forecast
    the Stop-hook driver persists (internal/orchestrator/runwaydrive.go):
    `limit_id` is the binding window, `horizon_seconds` the scored
    horizon (default 600s, ADD §15.5), `created_at` the compute time.
  * `risk_score` is the ADD §15.7 UNCALIBRATED fallback score. It is
    a score, not a probability, and this module never calls it one
    (Constitution §7 / AGENTS.md: uncalibrated risk scores are not
    probabilities). `hit_probability` is NULL by construction this
    wave (populated only once `calibrated=1`).
  * `burn_rate_p50`/`burn_rate_p90` are percent-of-quota per MINUTE
    (ADD §15.4's Δused_percent/Δminutes); NULL means no burn rate was
    computable (cold start) — never zero.
  * A used_percent DROP between consecutive samples of one window is
    a window reset/correction, exactly the Go scorer's negative-delta
    rule (internal/predictor/runway.estimateBurnRate).

Outcome reconstruction model (best-effort, documented limits):

  * The forecast's question is: does the (provider, limit_id) window
    reach the wall within (created_at, created_at + horizon]? The
    realized answer is walked over ALL of that provider's
    provider.quota.observed samples for that limit_id (quota windows
    are provider-account-scoped, not session-scoped), merged with the
    provider's provider.rate_limit.hit events.
  * HIT: a within-horizon sample with used_percent >= 100, or a
    within-horizon rate-limit hit. NOTE the disclosed attribution
    limit: rate_limit.hit payloads carry no limit_id (see
    internal/telemetry/claude/normalizer.go), so a hit is
    provider-scoped evidence and counts for any of that provider's
    windows under forecast at that moment.
  * NO-HIT (reset): a within-horizon used_percent drop before any
    wall evidence — the window rolled over first, so this horizon's
    exhaustion did not happen. The walk stops at the reset (a
    post-reset wall belongs to a different window).
  * NO-HIT (survived): no wall evidence and no reset within the
    horizon, AND at least one sample for the window at/after the
    horizon end proves the series kept going. Absence of coverage is
    never read as "no hit" — unknown is not zero.
  * UNRESOLVABLE: the sample series ends before the horizon does (the
    capture stopped, e.g. the newest forecasts at the data's edge).
    These are counted and disclosed, never silently dropped.
  * Detection limit (disclosed): the ground truth is the OBSERVED
    series. A wall touched and reset entirely between two samples,
    leaving neither a >=100 sample nor a rate-limit event, is not
    detectable.
  * Overlapping forecasts (disclosed): the Stop-hook driver persists
    a forecast per turn, so consecutive forecasts share most of their
    600s horizon — scored rows are NOT independent trials, and every
    empirical frequency below is an observation over correlated
    samples, not a calibrated probability.

Provisional disclosed constants (research/README.md grounding
discipline — any floor is a declared choice, not a fitted value):

  * BUCKET_N_FLOOR = 8: a reliability bucket below this many RESOLVED
    forecasts reports "accumulating", never an empirical frequency —
    deliberately the same ADD §15.2 "count(similar) >= 8" gate
    report.py's SAMPLE_GATE uses.
  * MIN_BURN_SPAN_SECONDS = 120: a realized burn rate is only
    measured over a span of at least this many seconds (Claude's
    used_percent is integer-granular; a shorter span quantizes to
    noise). Provisional; revisit with data.
"""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
from bisect import bisect_left, bisect_right
from dataclasses import dataclass
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional

sys.path.insert(0, str(Path(__file__).resolve().parent))

from observations import parse_ts  # noqa: E402

# Reliability buckets below this many RESOLVED forecasts say
# "accumulating" instead of printing an empirical frequency. PROVISIONAL,
# disclosed in the report output; equals ADD §15.2's sample gate (8), the
# same constant report.py gates cohorts on.
BUCKET_N_FLOOR = 8

# Minimum wall-clock span (seconds) a realized burn-rate measurement must
# cover. PROVISIONAL, disclosed in the report output (see module
# docstring for the rationale).
MIN_BURN_SPAN_SECONDS = 120

# Fixed-width uncalibrated-score buckets for the reliability table.
BUCKET_WIDTH = 0.1

EVENT_QUOTA = "provider.quota.observed"
EVENT_RATE_LIMIT_HIT = "provider.rate_limit.hit"

# Outcome labels.
HIT_QUOTA = "hit_quota_wall"  # a sample reached used_percent >= 100
HIT_RATE_LIMIT = "hit_rate_limit"  # a provider.rate_limit.hit landed in-horizon
NO_HIT_RESET = "no_hit_window_reset"  # the window reset before the wall
NO_HIT_SURVIVED = "no_hit_survived"  # full horizon covered, wall never reached
UNRESOLVED_SERIES_ENDED = "unresolvable_series_ended"  # capture stopped mid-horizon
UNRESOLVED_NO_PROVIDER = "unresolvable_no_provider"  # session row lacks a provider

HIT_OUTCOMES = frozenset({HIT_QUOTA, HIT_RATE_LIMIT})
RESOLVED_OUTCOMES = frozenset(
    {HIT_QUOTA, HIT_RATE_LIMIT, NO_HIT_RESET, NO_HIT_SURVIVED}
)


@dataclass(frozen=True)
class Forecast:
    """One persisted runway_forecasts row (migration 0042), plus the
    provider resolved via provider_sessions. Optional fields are honestly
    absent (unknown is not zero)."""

    id: str
    session_id: str
    provider: Optional[str]
    limit_id: str
    horizon_seconds: int
    risk_score: float  # UNCALIBRATED score (never a probability)
    hit_probability: Optional[float]  # NULL this wave (calibrated=0)
    calibrated: bool
    confidence: str
    current_used_percent: Optional[float]
    burn_rate_p50: Optional[float]  # percent per minute
    burn_rate_p90: Optional[float]  # percent per minute
    created_at: str
    created_ts: datetime


@dataclass(frozen=True)
class ScoredForecast:
    """One forecast plus its reconstructed outcome. realized_burn is the
    measured percent-per-minute over burn_span_seconds, or None when the
    span was too short / no baseline existed — never a fabricated 0."""

    forecast: Forecast
    outcome: str
    realized_burn: Optional[float]
    burn_span_seconds: Optional[float]


def default_db_path() -> Optional[Path]:
    """The standard local Auspex DB location (mirrors internal/paths:
    macOS Application Support, XDG data dir elsewhere), or None when no
    file exists there — the caller reports the gap, never guesses."""
    home = Path.home()
    if sys.platform == "darwin":
        candidate = home / "Library" / "Application Support" / "auspex" / "auspex.db"
    else:
        data = os.environ.get("XDG_DATA_HOME")
        base = Path(data) if data else home / ".local" / "share"
        candidate = base / "auspex" / "auspex.db"
    return candidate if candidate.is_file() else None


def open_db(path: Path) -> sqlite3.Connection:
    """Open the DB strictly read-only (URI mode=ro): this module must
    never write to, or take a write lock on, a live Auspex DB."""
    uri = path.resolve().as_uri() + "?mode=ro"
    return sqlite3.connect(uri, uri=True)


def load_forecasts(conn: sqlite3.Connection) -> list:
    """Every runway_forecasts row, oldest first, with the provider
    resolved via provider_sessions (LEFT JOIN: a row whose session
    cannot be resolved keeps provider=None and is disclosed as
    unresolvable, not dropped)."""
    rows = conn.execute(
        """
        SELECT rf.id, rf.session_id, ps.provider, rf.limit_id,
               rf.horizon_seconds, rf.risk_score, rf.hit_probability,
               rf.calibrated, rf.confidence, rf.current_used_percent,
               rf.burn_rate_p50, rf.burn_rate_p90, rf.created_at
        FROM runway_forecasts rf
        LEFT JOIN provider_sessions ps ON ps.id = rf.session_id
        """
    ).fetchall()
    forecasts = []
    for (
        fid,
        session_id,
        provider,
        limit_id,
        horizon_seconds,
        risk_score,
        hit_probability,
        calibrated,
        confidence,
        current_used_percent,
        burn_p50,
        burn_p90,
        created_at,
    ) in rows:
        forecasts.append(
            Forecast(
                id=fid,
                session_id=session_id,
                provider=provider,
                limit_id=limit_id,
                horizon_seconds=int(horizon_seconds),
                risk_score=float(risk_score),
                hit_probability=hit_probability,
                calibrated=bool(calibrated),
                confidence=confidence,
                current_used_percent=current_used_percent,
                burn_rate_p50=burn_p50,
                burn_rate_p90=burn_p90,
                created_at=created_at,
                created_ts=parse_ts(created_at, f"runway_forecasts {fid}"),
            )
        )
    forecasts.sort(key=lambda f: f.created_ts)
    return forecasts


def load_quota_series(conn: sqlite3.Connection):
    """provider.quota.observed samples grouped by (provider, limit_id),
    each series sorted by parsed time (string order on trimmed
    RFC3339Nano is not total — internal/report/load.go's caveat, so
    sorting happens on parsed timestamps here). Returns (series,
    n_samples, n_without_used_percent); samples without a used_percent
    measurement cannot inform an outcome and are counted, not silently
    dropped."""
    rows = conn.execute(
        "SELECT occurred_at, provider, payload_json FROM events WHERE event_type = ?",
        (EVENT_QUOTA,),
    ).fetchall()
    series: dict = {}
    total = 0
    without_used = 0
    for occurred_at, provider, payload_json in rows:
        total += 1
        try:
            payload = json.loads(payload_json) if payload_json else {}
        except json.JSONDecodeError:
            without_used += 1  # undecodable payload measures nothing
            continue
        limit_id = payload.get("limit_id")
        used = payload.get("used_percent")
        if not isinstance(limit_id, str) or limit_id == "":
            without_used += 1
            continue
        if not isinstance(used, (int, float)) or isinstance(used, bool):
            without_used += 1
            continue
        ts = parse_ts(occurred_at, f"quota event @ {occurred_at}")
        series.setdefault((provider, limit_id), []).append((ts, float(used)))
    for key in series:
        series[key].sort(key=lambda pair: pair[0])
    return series, total, without_used


def load_rate_limit_hits(conn: sqlite3.Connection):
    """provider.rate_limit.hit timestamps grouped by provider, sorted.
    The payload carries no limit_id (disclosed attribution limit — see
    module docstring), so only (provider, time) is usable."""
    rows = conn.execute(
        "SELECT occurred_at, provider FROM events WHERE event_type = ?",
        (EVENT_RATE_LIMIT_HIT,),
    ).fetchall()
    hits: dict = {}
    for occurred_at, provider in rows:
        ts = parse_ts(occurred_at, f"rate_limit.hit @ {occurred_at}")
        hits.setdefault(provider, []).append(ts)
    for provider in hits:
        hits[provider].sort()
    return hits, len(rows)


def resolve_outcome(forecast: Forecast, series: dict, hits: dict) -> ScoredForecast:
    """Reconstruct one forecast's realized outcome per the module
    docstring's model, and measure the realized burn rate over the
    walked span where the span is long enough (>= MIN_BURN_SPAN_SECONDS
    with a known baseline)."""
    if forecast.provider is None:
        return ScoredForecast(forecast, UNRESOLVED_NO_PROVIDER, None, None)

    start = forecast.created_ts
    end = start + timedelta(seconds=forecast.horizon_seconds)
    samples = series.get((forecast.provider, forecast.limit_id), [])
    times = [ts for ts, _ in samples]

    # In-horizon slice: start < ts <= end.
    lo = bisect_right(times, start)
    hi = bisect_right(times, end)
    window = samples[lo:hi]

    # Provider-scoped rate-limit hits inside the horizon (no limit_id in
    # the payload — disclosed).
    provider_hits = hits.get(forecast.provider, [])
    rl_in_window = [t for t in provider_hits if start < t <= end]

    # Merge the walk: quota samples and rate-limit hits, time order.
    timeline = [(ts, "sample", used) for ts, used in window]
    timeline += [(t, "rate_limit", None) for t in rl_in_window]
    timeline.sort(key=lambda item: item[0])

    # Baseline: the forecast's own current_used_percent (the sample it
    # was computed from); when absent, the last pre-forecast sample. A
    # missing baseline stays None — reset detection then starts from the
    # first in-horizon sample, and no burn is measured (never assume 0).
    baseline = forecast.current_used_percent
    if baseline is None and lo > 0:
        baseline = samples[lo - 1][1]

    prev = baseline
    last_usable = None  # (ts, used) — last in-window sample before any decision
    outcome = None
    for ts, kind, used in timeline:
        if kind == "rate_limit":
            outcome = HIT_RATE_LIMIT
            break
        if used >= 100.0:
            outcome = HIT_QUOTA
            last_usable = (ts, used)
            break
        if prev is not None and used < prev:
            # The Go scorer's negative-delta rule: any drop is a window
            # reset/correction — the wall was not reached this horizon.
            outcome = NO_HIT_RESET
            break
        prev = used
        last_usable = (ts, used)

    if outcome is None:
        # No wall evidence and no reset inside the horizon. Only a
        # sample at/after the horizon end proves the series kept going;
        # otherwise the capture stopped mid-horizon and the outcome is
        # honestly unknown (unknown is not zero, and not "no hit").
        covered = bisect_left(times, end) < len(times)
        outcome = NO_HIT_SURVIVED if covered else UNRESOLVED_SERIES_ENDED

    realized_burn = None
    burn_span = None
    if forecast.current_used_percent is not None and last_usable is not None:
        span = (last_usable[0] - start).total_seconds()
        if span >= MIN_BURN_SPAN_SECONDS:
            burn_span = span
            realized_burn = (
                (last_usable[1] - forecast.current_used_percent) / (span / 60.0)
            )
    return ScoredForecast(forecast, outcome, realized_burn, burn_span)


def _percentile(sorted_vals: list, q: float) -> float:
    """Linear-interpolated percentile over a NON-EMPTY sorted list (the
    same stdlib-only helper report.py uses)."""
    if not sorted_vals:
        raise ValueError("percentile of empty sequence")
    if len(sorted_vals) == 1:
        return sorted_vals[0]
    pos = q * (len(sorted_vals) - 1)
    lo = int(pos)
    if lo + 1 >= len(sorted_vals):
        return sorted_vals[-1]
    return sorted_vals[lo] + (pos - lo) * (sorted_vals[lo + 1] - sorted_vals[lo])


def _bucket_index(score: float) -> int:
    idx = int(score / BUCKET_WIDTH)
    return min(max(idx, 0), 9)


def _bucket_label(idx: int) -> str:
    lo = idx * BUCKET_WIDTH
    hi = lo + BUCKET_WIDTH
    return f"{lo:.1f}-{hi:.1f}"


def backtest(scored: list, quota_samples: int, quota_without_used: int,
             rate_limit_hits: int) -> dict:
    """Aggregate scored forecasts into the report structure: coverage
    counts, the reliability table (uncalibrated score bucket vs
    empirical hit frequency — an observed frequency over correlated
    samples, never the model's probability), and burn-rate sanity."""
    total = len(scored)
    by_limit: dict = {}
    outcome_counts: dict = {}
    for s in scored:
        by_limit[s.forecast.limit_id] = by_limit.get(s.forecast.limit_id, 0) + 1
        outcome_counts[s.outcome] = outcome_counts.get(s.outcome, 0) + 1

    resolved = [s for s in scored if s.outcome in RESOLVED_OUTCOMES]
    hits = [s for s in resolved if s.outcome in HIT_OUTCOMES]

    # Reliability table: bucket RESOLVED forecasts by uncalibrated score.
    buckets = []
    for idx in range(10):
        rows = [s for s in resolved if _bucket_index(s.forecast.risk_score) == idx]
        unresolved_rows = [
            s
            for s in scored
            if s.outcome not in RESOLVED_OUTCOMES
            and _bucket_index(s.forecast.risk_score) == idx
        ]
        if not rows and not unresolved_rows:
            continue
        n = len(rows)
        bucket_hits = sum(1 for s in rows if s.outcome in HIT_OUTCOMES)
        meets_floor = n >= BUCKET_N_FLOOR
        buckets.append(
            {
                "score_bucket": _bucket_label(idx),
                "resolved": n,
                "hits": bucket_hits,
                # An OBSERVED frequency over correlated samples; only
                # printed at/above the disclosed n-floor. None below the
                # floor — "accumulating", never a misleading number.
                "empirical_hit_frequency": (bucket_hits / n) if meets_floor else None,
                "accumulating": not meets_floor,
                "unresolved": len(unresolved_rows),
            }
        )

    # Burn-rate sanity: predicted P50 (percent/min) vs the realized burn
    # over the walked span, where measurable.
    predicted_burn = [s for s in scored if s.forecast.burn_rate_p50 is not None]
    measurable = [s for s in predicted_burn if s.realized_burn is not None]
    burn: dict = {
        "forecasts_with_predicted_burn": len(predicted_burn),
        "measurable": len(measurable),
        "min_span_seconds": MIN_BURN_SPAN_SECONDS,
        "median_predicted_p50_pct_per_min": None,
        "median_realized_pct_per_min": None,
        "median_abs_error_pct_per_min": None,
        "realized_above_predicted_p90": None,
    }
    if measurable:
        preds = sorted(s.forecast.burn_rate_p50 for s in measurable)
        reals = sorted(s.realized_burn for s in measurable)
        errs = sorted(
            abs(s.realized_burn - s.forecast.burn_rate_p50) for s in measurable
        )
        burn["median_predicted_p50_pct_per_min"] = _percentile(preds, 0.5)
        burn["median_realized_pct_per_min"] = _percentile(reals, 0.5)
        burn["median_abs_error_pct_per_min"] = _percentile(errs, 0.5)
        burn["realized_above_predicted_p90"] = sum(
            1
            for s in measurable
            if s.forecast.burn_rate_p90 is not None
            and s.realized_burn > s.forecast.burn_rate_p90
        )

    horizons = sorted({s.forecast.horizon_seconds for s in scored})
    return {
        "forecasts_total": total,
        "by_limit": dict(sorted(by_limit.items())),
        "horizons_seconds": horizons,
        "resolved": len(resolved),
        "hits": len(hits),
        "hits_via_quota_wall": outcome_counts.get(HIT_QUOTA, 0),
        "hits_via_rate_limit": outcome_counts.get(HIT_RATE_LIMIT, 0),
        "no_hit_survived": outcome_counts.get(NO_HIT_SURVIVED, 0),
        "no_hit_window_reset": outcome_counts.get(NO_HIT_RESET, 0),
        "unresolvable_series_ended": outcome_counts.get(UNRESOLVED_SERIES_ENDED, 0),
        "unresolvable_no_provider": outcome_counts.get(UNRESOLVED_NO_PROVIDER, 0),
        "quota_samples": quota_samples,
        "quota_samples_unusable": quota_without_used,
        "rate_limit_hit_events": rate_limit_hits,
        "bucket_n_floor": BUCKET_N_FLOOR,
        "reliability": buckets,
        "burn": burn,
    }


def run_backtest(db_path: Path) -> dict:
    """Load, score, and aggregate — the one-call entry point report.py
    uses. Opens the DB read-only; sqlite3 errors propagate to the caller
    (report.py degrades them to a disclosed readiness gap)."""
    conn = open_db(db_path)
    try:
        forecasts = load_forecasts(conn)
        series, n_samples, n_without = load_quota_series(conn)
        hits, n_hits = load_rate_limit_hits(conn)
    finally:
        conn.close()
    scored = [resolve_outcome(f, series, hits) for f in forecasts]
    result = backtest(scored, n_samples, n_without, n_hits)
    result["db_path"] = str(db_path)
    return result


def render_section(result: dict) -> list:
    """The 'runway calibration' section lines, shared by this module's
    standalone output and report.py's weekly report."""
    total = result["forecasts_total"]
    lines = [
        "runway calibration backtest (persisted forecasts vs realized "
        "quota trajectory, read-only DB):",
    ]
    if total == 0:
        lines.append(
            "  no persisted runway forecasts yet — the Stop-hook driver "
            "(#90) fills runway_forecasts as turns run"
        )
        return lines

    by_limit = ", ".join(f"{k}: {v}" for k, v in result["by_limit"].items())
    horizons = ", ".join(f"{h}s" for h in result["horizons_seconds"])
    unresolvable = (
        result["unresolvable_series_ended"] + result["unresolvable_no_provider"]
    )
    lines.append(
        f"  headline: {total} forecasts scored ({by_limit}; horizon {horizons}) — "
        f"{result['hits']} hit the wall within horizon, "
        f"{result['no_hit_survived']} survived, "
        f"{result['no_hit_window_reset']} window-reset before the wall, "
        f"{unresolvable} unresolvable (disclosed below)"
    )
    lines.append(
        f"  outcome evidence: {result['quota_samples']} quota samples "
        f"({result['quota_samples_unusable']} without a usable "
        f"limit_id/used_percent), {result['rate_limit_hit_events']} "
        f"rate-limit-hit events (provider-scoped: payload has no limit_id)"
    )
    if unresolvable:
        lines.append(
            f"  unresolvable: {result['unresolvable_series_ended']} series "
            f"ended mid-horizon (capture edge), "
            f"{result['unresolvable_no_provider']} without a resolvable "
            f"provider — counted, never silently dropped"
        )
    lines.append(
        f"  reliability (uncalibrated score bucket vs OBSERVED hit "
        f"frequency over correlated samples — not the model's "
        f"probability; n-floor {result['bucket_n_floor']} is provisional):"
    )
    for b in result["reliability"]:
        suffix = f", {b['unresolved']} unresolved" if b["unresolved"] else ""
        if b["accumulating"]:
            lines.append(
                f"    score {b['score_bucket']}: n={b['resolved']} resolved — "
                f"accumulating (below n-floor "
                f"{result['bucket_n_floor']}){suffix}"
            )
        else:
            freq = b["empirical_hit_frequency"]
            lines.append(
                f"    score {b['score_bucket']}: {b['hits']}/{b['resolved']} hit — "
                f"empirical hit frequency {100.0 * freq:.1f}%{suffix}"
            )
    burn = result["burn"]
    lines.append(
        f"  burn-rate sanity (predicted P50 vs realized, spans >= "
        f"{burn['min_span_seconds']}s — provisional floor): "
        f"{burn['measurable']}/{burn['forecasts_with_predicted_burn']} "
        f"predicted-burn forecasts measurable"
    )
    if burn["measurable"]:
        lines.append(
            f"    median predicted {burn['median_predicted_p50_pct_per_min']:.3f} "
            f"%/min vs median realized "
            f"{burn['median_realized_pct_per_min']:.3f} %/min "
            f"(median abs error {burn['median_abs_error_pct_per_min']:.3f} %/min); "
            f"{burn['realized_above_predicted_p90']} realized above the "
            f"predicted P90"
        )
    else:
        lines.append(
            "    (no measurable spans — realized burn not computable; "
            "never fabricated)"
        )
    return lines


def render_text(result: dict) -> str:
    header = [
        "runway calibration backtest",
        "===========================",
        f"db: {result.get('db_path', '?')} (opened read-only)",
        "",
    ]
    return "\n".join(header + render_section(result))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "db",
        type=Path,
        nargs="?",
        default=None,
        help="path to the Auspex SQLite DB (opened read-only); defaults "
        "to the standard local location when omitted",
    )
    parser.add_argument("--json", action="store_true", help="machine-readable output")
    args = parser.parse_args()

    db_path = args.db if args.db is not None else default_db_path()
    if db_path is None:
        print(
            "no Auspex DB found at the standard local location and no path "
            "given — nothing to score (pass the DB path explicitly)",
            file=sys.stderr,
        )
        return 1
    # A missing or unopenable DB is a disclosed gap, never a crash: check
    # existence before opening, and degrade any read-only open/read failure
    # (sqlite3.Error) to a clean message with no traceback.
    if not db_path.is_file():
        print(
            f"no Auspex DB at {db_path} — nothing to score (a missing DB is "
            "a disclosed gap, not a crash; pass an existing read-only DB path)",
            file=sys.stderr,
        )
        return 1
    try:
        result = run_backtest(db_path)
    except sqlite3.Error as exc:
        print(
            f"could not open {db_path} read-only ({exc}) — the DB is "
            "unreadable; reported as a gap, never a crash",
            file=sys.stderr,
        )
        return 1
    if args.json:
        print(json.dumps(result, indent=2, sort_keys=True))
    else:
        print(render_text(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
