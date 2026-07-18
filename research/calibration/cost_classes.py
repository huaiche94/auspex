#!/usr/bin/env python3
"""Four-class cost DECOMPOSITION over captured per-turn actuals (#66 item
(a), the research/descriptive side of the cache-aware cost model).

Usage:
    python3 research/calibration/cost_classes.py [auspex.db] [--json]

When no DB path is given, the standard local Auspex DB location is probed
(the same resolver runway.py uses: macOS Application Support, XDG data dir
elsewhere). The DB is ALWAYS opened read-only (SQLite URI mode=ro) — this
module never writes, and a missing or unopenable DB is reported as a gap,
never a crash.

What this module IS, and is NOT
-------------------------------
This is a DESCRIPTIVE decomposition of ALREADY-CAPTURED per-turn token
counts, not a calibrated or predictive model. It answers one observed
question: when the four token classes (fresh input / cache-creation /
cache-read / output) are priced separately with the EXPLICIT-cache
formula, WHERE does the dollar amount actually go, and by how much does a
cache-blind "total_tokens × rate" estimate under-count it? Every number
below is an OBSERVED share over the turns THIS machine happened to
capture — it does not forecast the next turn, and it is not a fitted
coefficient anyone should apply forward (Constitution §7: an uncalibrated
number is never dressed up as more than it is; a share of a past bill is
not a probability about a future one).

This is issue #66 item (a) ONLY (the descriptive/research side — pricing
captured actuals to quantify the cache-read domination the paper predicts,
arXiv:2604.22750 §Appendix B / docs/backlog/token-cost-prediction-research.md
§3.B). It deliberately does NOT build the forecast side (a four-class
PREDICTED cost / cache-read forecasting) — that is item (b), modeling work
gated on #11 data accumulation.

The price table below MIRRORS internal/pricing (defaultFamilyPrices +
CacheReadInputMultiplier / CacheCreationInputMultiplier) so the Python
decomposition prices with the exact same explicit-cache formula
`Table.FourClassCost` uses. Those rates are hand-maintained list-price
PLACEHOLDERS: they are not fetched at runtime, they do not know about
subscription plans (a Claude Code subscription user's marginal cost is
$0 — the decomposition still describes where list-price dollars WOULD go,
a real consumption signal), and they drift as providers reprice. A drift
between this table and internal/pricing.go is a maintenance bug; the Go
table is the source of truth.

Explicit-cache only (the honest scope gate)
-------------------------------------------
`FourClassCost` prices the EXPLICIT-cache formula (Anthropic-style: a
cache READ is 0.10× a fresh input token, a cache WRITE is 1.25×). A turn
is priced here ONLY when it carries all four explicit classes. Codex/GPT
turns use IMPLICIT caching (a single ~0.2×-input cache-read class, no
separate creation) and carry `reasoning_output_tokens` instead of
`cache_creation_input_tokens`; the implicit-cache formula is the unbuilt
sibling (`FourClassCost`'s own docstring / #66 item (b) / D-02), so those
turns are DISCLOSED as skipped, never force-priced under the wrong rates
(unknown is not zero — assuming a 0 cache-creation for a codex turn and
pricing its cache-read at 0.10× instead of 0.20× would fabricate a wrong
bill). Turns from before per-turn four-class capture (ADR-051, PR #80)
carry no cache-read class at all and are likewise disclosed, not zeroed.

Data source & de-identification posture (mirrors runway.py)
-----------------------------------------------------------
Per-turn four-class actuals ride two event types, both read here directly
from SQLite because neither is reduced to a single per-turn row in the
JSONL exports (the observations export ships them, but this module keeps
runway.py's read-the-DB discipline so it activates by itself from the
standard local location):

  * `provider.turn.completed` — the hook-mode capture (ADR-051, PR #80):
    the Stop hook reads the session transcript's exact per-turn usage and
    stamps input_tokens / output_tokens / cache_read_input_tokens /
    cache_creation_input_tokens / total_tokens / model_id onto the turn's
    terminal event. This is where THIS machine's four-class actuals live.
  * `provider.usage.observed`, managed-run variant (PR #87): `auspex run`
    persists the provider result line's own per-turn usage (same class
    fields, plus a provider-reported total_cost_usd). Read here only when
    the row carries the four-class fields — the 6k+ statusline snapshots
    of the same event type are session-cumulative totals with no per-turn
    token split and are skipped by the WHERE filter, never scanned.

Only whitelisted numeric/enum payload keys are read: the four class
counts, total_tokens, reasoning_output_tokens, model_id, and (for the
reconciliation cross-check) total_cost_usd — plus occurred_at and the
opaque turn_id. No prompt text, paths, prompt hashes, or identities are
touched; model_id is a family label, turn_id an opaque id. This matches
internal/retention/observations.go's whitelist-projection posture.

Disclosed limits
----------------
  * The reconstructed bill is priced with THIS module's list-price
    placeholders, so it is not the user's real (possibly $0-marginal)
    spend; it is a consumption decomposition. Where a turn carries a
    provider-reported total_cost_usd (managed-run rows), a reconciliation
    line cross-checks the reconstruction's order of magnitude; hook
    turn.completed rows carry no cost field, so on a hook-only dataset
    that cross-check is a disclosed gap, not a failure.
  * A turn with a NEGATIVE class count is corrupt input (unknown is not
    zero, but a negative token count is not a measurement) — `FourClassCost`
    returns ok=false and the turn is counted as skipped, never dropped
    silently.
  * These shares are over a SMALL, self-selected set of captured turns on
    one machine; they describe this dataset, not a population.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

sys.path.insert(0, str(Path(__file__).resolve().parent))

# Reuse runway.py's read-only DB discipline verbatim (same resolver, same
# mode=ro open) rather than re-deriving it — one home for the local-DB
# posture the calibration surface shares.
from runway import default_db_path, open_db  # noqa: E402
from observations import parse_ts  # noqa: E402

# ---------------------------------------------------------------------------
# Price table — MIRRORS internal/pricing.go (defaultFamilyPrices, the two
# cache multipliers, and the sonnet-class DefaultFamily fallback). USD per
# million tokens (MTok), split input/output the way providers price it.
# Kept in lockstep with the Go table by hand; the Go table is authoritative.
# ---------------------------------------------------------------------------
FAMILY_PRICES = {
    "fable": (10.0, 50.0),
    "mythos": (10.0, 50.0),
    "opus": (5.0, 25.0),
    "sonnet": (3.0, 15.0),
    "haiku": (1.0, 5.0),
}
DEFAULT_FAMILY = "default"
DEFAULT_FALLBACK = (3.0, 15.0)  # sonnet-class (internal/pricing.go: DefaultFamily)

# Explicit prompt-cache multipliers, relative to the family base input
# rate (internal/pricing.go: CacheReadInputMultiplier / CacheCreationInputMultiplier).
CACHE_READ_MULTIPLIER = 0.10
CACHE_CREATION_MULTIPLIER = 1.25

# Deterministic (sorted) family match order — Go's familyOrder guards
# against randomized map iteration; the same discipline here keeps a
# model id containing two family substrings resolving identically.
_FAMILY_ORDER = sorted(FAMILY_PRICES)

MTOK = 1_000_000

# Per-turn classification (every scanned candidate lands in exactly one).
PRICED_EXPLICIT = "priced_explicit"  # all four explicit classes present
SKIPPED_IMPLICIT_CACHE = "skipped_implicit_cache"  # codex/gpt reasoning turn
SKIPPED_NO_FOURCLASS = "skipped_no_fourclass"  # pre-ADR-051 capture, no cache-read
SKIPPED_INCOMPLETE = "skipped_incomplete"  # has cache-read, missing another class
SKIPPED_NEGATIVE = "skipped_negative"  # a class count is negative (corrupt)

# The two event types that can carry per-turn four-class actuals.
EVENT_TURN_COMPLETED = "provider.turn.completed"
EVENT_USAGE_OBSERVED = "provider.usage.observed"


def price(model_id: str) -> tuple:
    """Resolve a model id/display name to (input_rate, output_rate, family)
    by case-insensitive family-substring match in sorted order, falling
    back to the sonnet-class DefaultFamily rate — the exact resolution
    internal/pricing.go's Table.Price performs."""
    lowered = (model_id or "").lower()
    for family in _FAMILY_ORDER:
        if family and family in lowered:
            r_in, r_out = FAMILY_PRICES[family]
            return r_in, r_out, family
    return DEFAULT_FALLBACK[0], DEFAULT_FALLBACK[1], DEFAULT_FAMILY


def four_class_cost(model_id: str, non_cached_input: int, cache_creation: int,
                    cache_read: int, output: int):
    """Price a turn under the EXPLICIT-cache formula (mirrors
    internal/pricing.go's Table.FourClassCost):

        non_cached_input × r_in
          + cache_creation × (r_in × 1.25)
          + cache_read     × (r_in × 0.10)
          + output         × r_out

    Returns a per-class USD dict + family, or None when any class count is
    negative (ok=false: a negative token count is corrupt, not a measured
    0 — unknown is not zero). An all-zero turn is a valid $0."""
    if non_cached_input < 0 or cache_creation < 0 or cache_read < 0 or output < 0:
        return None
    r_in, r_out, family = price(model_id)
    non_cached_usd = non_cached_input * r_in / MTOK
    cache_creation_usd = cache_creation * r_in * CACHE_CREATION_MULTIPLIER / MTOK
    cache_read_usd = cache_read * r_in * CACHE_READ_MULTIPLIER / MTOK
    output_usd = output * r_out / MTOK
    return {
        "family": family,
        "non_cached_input": non_cached_usd,
        "cache_creation": cache_creation_usd,
        "cache_read": cache_read_usd,
        "output": output_usd,
        "total": non_cached_usd + cache_creation_usd + cache_read_usd + output_usd,
        # cache-blind cost = what a "total_tokens × rate" estimate can see:
        # only the fresh-input and output classes (total_tokens = input +
        # output; the two cache classes are invisible to it). The gap
        # between `total` and this is the whole point.
        "cache_blind": non_cached_usd + output_usd,
    }


@dataclass(frozen=True)
class TurnSample:
    """One scanned four-class-capture candidate row. Optional fields are
    honestly absent (unknown is not zero); classification reads presence,
    never a defaulted 0."""

    turn_id: Optional[str]
    occurred_at: str
    event_type: str
    model_id: Optional[str]
    input_tokens: Optional[int]
    output_tokens: Optional[int]
    cache_read: Optional[int]
    cache_creation: Optional[int]
    reasoning: Optional[int]
    total_cost_usd: Optional[float]


def _payload_int(payload: dict, key: str) -> Optional[int]:
    v = payload.get(key)
    if isinstance(v, bool):  # bools are ints in Python; a flag is not a count
        return None
    if isinstance(v, (int, float)):
        return int(v)
    return None


def _payload_float(payload: dict, key: str) -> Optional[float]:
    v = payload.get(key)
    if isinstance(v, bool):
        return None
    if isinstance(v, (int, float)):
        return float(v)
    return None


def _payload_str(payload: dict, key: str) -> Optional[str]:
    v = payload.get(key)
    return v if isinstance(v, str) else None


def load_samples(conn: sqlite3.Connection):
    """Every per-turn four-class-capture candidate, deduped by turn_id
    (last occurred_at wins — a re-delivered terminal event or a managed
    turn that also fired the Stop hook must not double-count; mirrors the
    sibling modules' last-wins rule). Rows without a turn_id keep their own
    identity (cannot be deduped) and are disclosed as such by count.

    The WHERE clause admits every `provider.turn.completed` row (so the
    coverage denominator includes turns captured WITHOUT four-class tokens)
    but only `provider.usage.observed` rows that actually carry a per-turn
    token count — the thousands of session-cumulative statusline snapshots
    of that event type are never scanned. Returns (samples, deduped)."""
    rows = conn.execute(
        """
        SELECT occurred_at, event_type, turn_id, payload_json
        FROM events
        WHERE event_type = ?
           OR (event_type = ? AND payload_json LIKE '%input_tokens%')
        ORDER BY occurred_at, rowid
        """,
        (EVENT_TURN_COMPLETED, EVENT_USAGE_OBSERVED),
    ).fetchall()

    by_turn: dict = {}
    no_turn_id: list = []
    for occurred_at, event_type, turn_id, payload_json in rows:
        try:
            payload = json.loads(payload_json) if payload_json else {}
        except json.JSONDecodeError:
            payload = {}
        sample = TurnSample(
            turn_id=turn_id,
            occurred_at=occurred_at,
            event_type=event_type,
            model_id=_payload_str(payload, "model_id"),
            input_tokens=_payload_int(payload, "input_tokens"),
            output_tokens=_payload_int(payload, "output_tokens"),
            cache_read=_payload_int(payload, "cache_read_input_tokens"),
            cache_creation=_payload_int(payload, "cache_creation_input_tokens"),
            reasoning=_payload_int(payload, "reasoning_output_tokens"),
            total_cost_usd=_payload_float(payload, "total_cost_usd"),
        )
        if turn_id is None or turn_id == "":
            no_turn_id.append(sample)
            continue
        prev = by_turn.get(turn_id)
        if prev is None or parse_ts(occurred_at, f"sample @ {occurred_at}") >= parse_ts(
            prev.occurred_at, f"sample @ {prev.occurred_at}"
        ):
            by_turn[turn_id] = sample
    deduped = len(rows) - len(by_turn) - len(no_turn_id)
    return list(by_turn.values()) + no_turn_id, deduped


def classify(s: TurnSample) -> str:
    """Which pricing bucket a captured turn falls in — presence-based, so a
    missing class is honestly-unknown, never a priced 0. The explicit-cache
    formula needs all four explicit classes; a reasoning turn is implicit-
    cache (unbuilt sibling); a turn with no cache-read predates four-class
    capture."""
    has_core = s.input_tokens is not None and s.output_tokens is not None
    if s.cache_read is None:
        return SKIPPED_NO_FOURCLASS
    if s.cache_creation is not None and has_core:
        # Explicit-cache: fresh input, cache creation, cache read, output.
        if min(s.input_tokens, s.output_tokens, s.cache_read, s.cache_creation) < 0:
            return SKIPPED_NEGATIVE
        return PRICED_EXPLICIT
    if s.reasoning is not None:
        # Codex/GPT implicit-cache turn (reasoning present, no separate
        # cache-creation) — the explicit formula does not apply here.
        return SKIPPED_IMPLICIT_CACHE
    return SKIPPED_INCOMPLETE


def _percentile(sorted_vals: list, q: float) -> float:
    """Linear-interpolated percentile over a NON-EMPTY sorted list (the
    same stdlib-only helper runway.py / report.py use)."""
    if not sorted_vals:
        raise ValueError("percentile of empty sequence")
    if len(sorted_vals) == 1:
        return sorted_vals[0]
    pos = q * (len(sorted_vals) - 1)
    lo = int(pos)
    if lo + 1 >= len(sorted_vals):
        return sorted_vals[-1]
    return sorted_vals[lo] + (pos - lo) * (sorted_vals[lo + 1] - sorted_vals[lo])


def _empty_usd() -> dict:
    return {
        "non_cached_input": 0.0,
        "cache_creation": 0.0,
        "cache_read": 0.0,
        "output": 0.0,
        "total": 0.0,
        "cache_blind": 0.0,
    }


def _shares(usd: dict) -> dict:
    total = usd["total"]
    if total <= 0:
        return {
            k: None
            for k in ("non_cached_input", "cache_creation", "cache_read", "output")
        }
    return {
        k: usd[k] / total
        for k in ("non_cached_input", "cache_creation", "cache_read", "output")
    }


def decompose(samples: list, deduped: int) -> dict:
    """Classify every captured turn, price the explicit-cache ones with the
    four-class formula, and aggregate the observed per-class dollar shares +
    the cache-blind under-count factor. Descriptive throughout: shares are
    of a PAST bill on this dataset, never a forecast or a fitted number."""
    counts = {
        PRICED_EXPLICIT: 0,
        SKIPPED_IMPLICIT_CACHE: 0,
        SKIPPED_NO_FOURCLASS: 0,
        SKIPPED_INCOMPLETE: 0,
        SKIPPED_NEGATIVE: 0,
    }
    agg = _empty_usd()
    by_family: dict = {}
    per_turn_factor: list = []  # full / cache-blind, per priced turn
    per_turn_cache_read_share: list = []
    reconciled = 0  # priced turns carrying a provider-reported total_cost_usd
    reconciliation_ratios: list = []

    for s in samples:
        bucket = classify(s)
        counts[bucket] += 1
        if bucket != PRICED_EXPLICIT:
            continue
        priced = four_class_cost(
            s.model_id or "",
            s.input_tokens,
            s.cache_creation,
            s.cache_read,
            s.output_tokens,
        )
        if priced is None:  # defensive: classify() already excluded negatives
            counts[SKIPPED_NEGATIVE] += 1
            counts[PRICED_EXPLICIT] -= 1
            continue
        money = ("non_cached_input", "cache_creation", "cache_read", "output",
                 "total", "cache_blind")
        for k in money:
            agg[k] += priced[k]
        fam_usd = by_family.setdefault(priced["family"], _empty_usd())
        for k in money:
            fam_usd[k] += priced[k]
        if priced["cache_blind"] > 0:
            per_turn_factor.append(priced["total"] / priced["cache_blind"])
        if priced["total"] > 0:
            per_turn_cache_read_share.append(priced["cache_read"] / priced["total"])
        if s.total_cost_usd is not None:
            reconciled += 1
            if priced["total"] > 0:
                reconciliation_ratios.append(s.total_cost_usd / priced["total"])

    priced_n = counts[PRICED_EXPLICIT]
    priced_block = {
        "turns": priced_n,
        "usd": agg,
        "share": _shares(agg),
        "under_forecast_factor_aggregate": (
            agg["total"] / agg["cache_blind"] if agg["cache_blind"] > 0 else None
        ),
        "under_forecast_factor_median": (
            _percentile(sorted(per_turn_factor), 0.5) if per_turn_factor else None
        ),
        "under_forecast_factor_p90": (
            _percentile(sorted(per_turn_factor), 0.9) if per_turn_factor else None
        ),
        "cache_read_share_median": (
            _percentile(sorted(per_turn_cache_read_share), 0.5)
            if per_turn_cache_read_share
            else None
        ),
    }

    # Per-family turn counts (a second pass keeps the aggregation loop above
    # single-purpose; the priced set is small).
    fam_turns: dict = {}
    for s in samples:
        if classify(s) == PRICED_EXPLICIT:
            fam = price(s.model_id or "")[2]
            fam_turns[fam] = fam_turns.get(fam, 0) + 1

    families = []
    for fam, usd in sorted(by_family.items(), key=lambda kv: (-kv[1]["total"], kv[0])):
        families.append(
            {
                "family": fam,
                "turns": fam_turns.get(fam, 0),
                "usd": usd,
                "share": _shares(usd),
                "under_forecast_factor_aggregate": (
                    usd["total"] / usd["cache_blind"] if usd["cache_blind"] > 0 else None
                ),
            }
        )

    return {
        "scanned_turns": len(samples),
        "deduped_turn_ids": deduped,
        "counts": counts,
        "priced": priced_block,
        "by_family": families,
        "reconciliation": {
            # How many priced turns carried a provider-reported cost to
            # cross-check against — a disclosed gap (0) on a hook-only
            # dataset, where four-class capture rides turn.completed (no
            # cost field). Never presented as a validation when absent.
            "priced_turns_with_reported_cost": reconciled,
            "median_reported_over_reconstructed": (
                _percentile(sorted(reconciliation_ratios), 0.5)
                if reconciliation_ratios
                else None
            ),
        },
    }


def run_decomposition(db_path: Path) -> dict:
    """Load, classify, price, aggregate — the one-call entry point (mirrors
    runway.run_backtest). Opens the DB read-only; sqlite3 errors propagate
    to the caller, which degrades them to a disclosed gap."""
    conn = open_db(db_path)
    try:
        samples, deduped = load_samples(conn)
    finally:
        conn.close()
    result = decompose(samples, deduped)
    result["db_path"] = str(db_path)
    return result


def _fmt_usd(v: float) -> str:
    return f"${v:,.2f}" if v >= 0.005 or v == 0 else f"${v:.4f}"


def render_section(result: dict) -> list:
    """The 'four-class cost decomposition' section lines, shared by this
    module's standalone output and report.py's weekly report."""
    counts = result["counts"]
    priced = result["priced"]
    n = priced["turns"]
    lines = [
        "four-class cost decomposition (captured per-turn actuals priced "
        "with the explicit-cache formula, read-only DB — DESCRIPTIVE, not a "
        "forecast):",
    ]
    lines.append(
        f"  capture coverage: {result['scanned_turns']} four-class-candidate "
        f"turns scanned — {n} priced (explicit-cache: Claude-family), "
        f"{counts[SKIPPED_IMPLICIT_CACHE]} skipped implicit-cache "
        f"(codex/gpt reasoning turns — the explicit formula does not apply; "
        f"implicit-cache pricing is the unbuilt sibling, #66 item (b)/D-02), "
        f"{counts[SKIPPED_NO_FOURCLASS]} skipped without four-class tokens "
        f"(pre-ADR-051 capture)"
    )
    if counts[SKIPPED_INCOMPLETE] or counts[SKIPPED_NEGATIVE] or result["deduped_turn_ids"]:
        lines.append(
            f"  also: {counts[SKIPPED_INCOMPLETE]} incomplete (cache-read "
            f"present, another class missing — not priced, unknown is not "
            f"zero), {counts[SKIPPED_NEGATIVE]} corrupt (negative class), "
            f"{result['deduped_turn_ids']} duplicate turn_ids collapsed "
            f"(last-wins)"
        )
    if n == 0:
        lines.append(
            "  no explicit-cache turns to price yet — dogfood Claude-family "
            "turns (four-class capture rides provider.turn.completed since "
            "ADR-051/PR #80); nothing to decompose"
        )
        return lines

    usd = priced["usd"]
    share = priced["share"]
    lines.append(
        f"  priced bill (list-price placeholders — NOT real spend; a "
        f"subscription's marginal cost is $0): total {_fmt_usd(usd['total'])} "
        f"over {n} turns"
    )
    lines.append(
        f"  per-class dollar shares (of the priced total): "
        f"cache-read {100.0 * share['cache_read']:.1f}% "
        f"({_fmt_usd(usd['cache_read'])}), "
        f"output {100.0 * share['output']:.1f}% ({_fmt_usd(usd['output'])}), "
        f"cache-creation {100.0 * share['cache_creation']:.1f}% "
        f"({_fmt_usd(usd['cache_creation'])}), "
        f"fresh-input {100.0 * share['non_cached_input']:.1f}% "
        f"({_fmt_usd(usd['non_cached_input'])})"
    )
    lines.append(
        f"  -> cache-read dominates the bill though its unit price is the "
        f"cheapest class (0.10x a fresh-input token): accumulated context "
        f"re-read across a turn's many round-trips (arXiv:2604.22750 §3.B). "
        f"Per-turn median cache-read share "
        f"{100.0 * priced['cache_read_share_median']:.1f}%"
    )
    fac = priced["under_forecast_factor_aggregate"]
    lines.append(
        f"  cache-blind under-count: pricing only the classes a "
        f"'total_tokens x rate' estimate sees (fresh-input + output) "
        f"under-states the real bill {fac:.1f}x in aggregate "
        f"(per-turn median {priced['under_forecast_factor_median']:.1f}x, "
        f"P90 {priced['under_forecast_factor_p90']:.1f}x) — the mechanism "
        f"behind the ~7-9x cost under-forecast report.py's cost residual "
        f"measures against the shipped forecast (a related but distinct "
        f"denominator; not claimed equal)"
    )
    if len(result["by_family"]) > 1:
        parts = []
        for f in result["by_family"]:
            parts.append(
                f"{f['family']} (n={f['turns']}): cache-read "
                f"{100.0 * f['share']['cache_read']:.0f}%, "
                f"under-count {f['under_forecast_factor_aggregate']:.1f}x"
            )
        lines.append("  by model family: " + "; ".join(parts))
    recon = result["reconciliation"]
    if recon["priced_turns_with_reported_cost"]:
        lines.append(
            f"  reconciliation vs provider-reported cost: "
            f"{recon['priced_turns_with_reported_cost']}/{n} priced turns "
            f"carry a total_cost_usd; median reported/reconstructed "
            f"{recon['median_reported_over_reconstructed']:.2f}x (a sanity "
            f"cross-check on the list-price reconstruction, not ground truth)"
        )
    else:
        lines.append(
            "  reconciliation vs provider-reported cost: 0 priced turns "
            "carry a total_cost_usd (four-class capture rides turn.completed, "
            "which has no cost field; managed-run rows carry both but none "
            "captured) — the reconstruction is list-price-based and "
            "unchecked against provider cost on this dataset (disclosed gap)"
        )
    return lines


def render_text(result: dict) -> str:
    header = [
        "four-class cost decomposition",
        "=============================",
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
        help="path to the Auspex SQLite DB (opened read-only); defaults to "
        "the standard local location when omitted",
    )
    parser.add_argument("--json", action="store_true", help="machine-readable output")
    args = parser.parse_args()

    db_path = args.db if args.db is not None else default_db_path()
    if db_path is None:
        print(
            "no Auspex DB found at the standard local location and no path "
            "given — nothing to decompose (pass the DB path explicitly)",
            file=sys.stderr,
        )
        return 1
    # A missing or unopenable DB is a disclosed gap, never a crash: check
    # existence before opening, and degrade any read-only open/read failure
    # (sqlite3.Error) to a clean message with no traceback.
    if not db_path.is_file():
        print(
            f"no Auspex DB at {db_path} — nothing to decompose (a missing DB "
            "is a disclosed gap, not a crash; pass an existing read-only DB "
            "path)",
            file=sys.stderr,
        )
        return 1
    try:
        result = run_decomposition(db_path)
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
