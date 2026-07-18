#!/usr/bin/env python3
"""File-op aggregate distribution report: the empirical, OBSERVED
distribution of the five per-turn file-operation aggregates (#67 slice
3a / ADR-052) — so a human can later ground an unsolvable-stop threshold
(#68 slice 3b) in real observed frequencies instead of a guess.

Usage:
    python3 research/calibration/fileops.py [auspex.db] [--json]

When no DB path is given, the standard local Auspex DB location is
probed (macOS: ~/Library/Application Support/auspex/auspex.db; XDG:
$XDG_DATA_HOME/auspex/auspex.db, else ~/.local/share/auspex/auspex.db).
The DB is ALWAYS opened read-only (SQLite URI mode=ro) — this module
never writes, and a missing or unopenable DB is reported as a gap,
never a crash.

Scope — DESCRIPTIVE ONLY (this module deliberately does NOT gate):
This is the slice that UNBLOCKS a later threshold choice, not the choice
itself. ADR-052's slice-3b ruling defers the unsolvable-stop gate
(RiskCombiner input + reason code + thresholds) until captured data
supports threshold selection; #68 stays gated on 3b. So this module
proposes NO threshold, NO RiskCombiner wiring, NO reason code, and NO
stop-gate logic. It reports the OBSERVED distribution of each aggregate
and nothing more. Every number below is a description of what has been
seen, never a calibrated model or a probability (Constitution §7 /
AGENTS.md: an observed frequency is not a probability, and an observed
distribution is not a threshold).

Why this module reads SQLite while report.py's coverage sections read
the JSONL export: the five file-op aggregates ride the
`provider.turn.completed` event payload (internal/telemetry/claude/
toolops.go, ADR-052 approval touch 2) and, like runway.py's forecasts,
this module reads exactly the payload keys it needs and nothing else.
It opens the same closed set of numeric aggregate keys the
`auspex.observations-export.v1` whitelist admits
(internal/retention/observations.go) — a WHITELIST PROJECTION, never a
blacklist scrub: only these five keys are ever read out of the payload,
so prompt_sha256 / cwd / model_id / tokens / effort and anything a
future producer adds stay unread by construction. No file path exists to
read — ADR-052's binding privacy invariant is that raw paths are never
persisted in any form (hashes included); paths were interned to opaque
per-turn ordinals in process memory for counting and discarded, so these
five counts cannot join back to a file, a prompt, or an identity.

The five aggregates (ADR-052 §7.3, definitions mirrored, never modified):

  * distinct_files_touched — how many distinct files the turn's file ops
    touched (view = Read; modify = Edit/Write/MultiEdit/NotebookEdit).
  * total_file_ops — total count of those file ops in the turn.
  * repeated_ops — file ops that revisited an already-touched file
    (total_file_ops - distinct_files_touched, floored at 0).
  * repeat_rate — repeated_ops / total_file_ops, a ratio in [0, 1). It is
    ABSENT (not zero) when total_file_ops was 0, or when only the
    hook-counted degrade total was measurable (observations.go's note) —
    so repeat_rate's sample count can be below total_file_ops's, and this
    module reports both counts rather than backfilling a 0 (unknown is
    not zero, CONTRACT_FREEZE.md).
  * max_ops_on_one_file — the most ops the turn spent on any single file.

Disclosed limits (honest, so the later threshold choice is made with
eyes open):

  * Small-n tails: quantiles over a handful of turns are noisy, and P99
    of a small sample coincides with its max. Below the disclosed sample
    gate (SAMPLE_GATE, ADD §15.2's 8 — the SAME gate report.py and
    runway.py already reuse, not a new invented number) an aggregate's
    distribution is labeled "accumulating": the raw quantiles are still
    printed for transparency, but flagged as too few turns to
    characterize a stable distribution.
  * Correlated turns: turns within one session share a working set, so
    these are observations over correlated samples, not independent
    draws — every quantile is descriptive, never a probability.
  * Coverage gap: turns from before the #67 capture landed, and any turn
    whose payload carried no file-op keys, have honestly-unknown
    aggregates. They are counted as "no file-op telemetry", never folded
    in as zeros (which would deflate every quantile).
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

# Below this many turns carrying an aggregate, its distribution is
# labeled "accumulating" — the raw quantiles still print, but flagged as
# too few turns to characterize a stable distribution. This is ADD
# §15.2's sample gate (8), the SAME constant report.py's SAMPLE_GATE and
# runway.py's BUCKET_N_FLOOR reuse — a disclosed reporting-discipline
# floor, NOT a stop-gate threshold (this module proposes none).
SAMPLE_GATE = 8

# The one event type that carries the aggregates (ADR-052 approval touch
# 2); no other event type is read.
EVENT_TURN_COMPLETED = "provider.turn.completed"

# The closed set of file-op aggregate payload keys this module reads —
# the exact five the observations-export whitelist admits. The four
# integer counts and the one ratio; nothing else is projected out of the
# payload.
INT_AGGREGATES = (
    "distinct_files_touched",
    "total_file_ops",
    "repeated_ops",
    "max_ops_on_one_file",
)
RATE_AGGREGATE = "repeat_rate"
# Rendering order (counts first, ratio last), and the coverage anchor
# (total_file_ops is the primary "did this turn carry file-op telemetry"
# signal — the aggregate every file-op turn has).
AGGREGATE_ORDER = (
    "total_file_ops",
    "distinct_files_touched",
    "repeated_ops",
    "max_ops_on_one_file",
    RATE_AGGREGATE,
)
COVERAGE_ANCHOR = "total_file_ops"


@dataclass(frozen=True)
class FileOpTurn:
    """One provider.turn.completed row's whitelisted file-op aggregates.
    Every field is Optional and stays None when its key was absent —
    unknown is not zero, so a missing aggregate never becomes a measured
    0 (which would deflate the observed distribution)."""

    distinct_files_touched: Optional[int]
    total_file_ops: Optional[int]
    repeated_ops: Optional[int]
    repeat_rate: Optional[float]
    max_ops_on_one_file: Optional[int]


def default_db_path() -> Optional[Path]:
    """The standard local Auspex DB location (mirrors internal/paths:
    macOS Application Support, XDG data dir elsewhere), or None when no
    file exists there — the caller reports the gap, never guesses."""
    import os

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


def _payload_int(payload: dict, key: str) -> Optional[int]:
    """Read one whitelisted integer aggregate; a missing key or a
    non-number stays None (unknown is not zero). JSON numbers decode as
    int/float — mirror the Go payloadInt int64 conversion."""
    v = payload.get(key)
    if isinstance(v, bool):  # bool is an int subclass — never an aggregate
        return None
    if isinstance(v, (int, float)):
        return int(v)
    return None


def _payload_float(payload: dict, key: str) -> Optional[float]:
    """Read one whitelisted float aggregate (repeat_rate); a missing key
    or a non-number stays None (unknown is not zero)."""
    v = payload.get(key)
    if isinstance(v, bool):
        return None
    if isinstance(v, (int, float)):
        return float(v)
    return None


def load_fileop_turns(conn: sqlite3.Connection):
    """Every provider.turn.completed row, projected to exactly the five
    whitelisted file-op aggregate keys. Returns (turns, n_completed):
    n_completed is EVERY turn.completed row (the coverage denominator),
    turns is the list of per-turn aggregate records. A payload that fails
    to decode contributes a turn with all-None aggregates — its identity
    as a completed turn survives, but it measures nothing (same posture as
    observations.go's decode-failure handling)."""
    rows = conn.execute(
        "SELECT payload_json FROM events WHERE event_type = ?",
        (EVENT_TURN_COMPLETED,),
    ).fetchall()
    turns = []
    for (payload_json,) in rows:
        try:
            payload = json.loads(payload_json) if payload_json else {}
        except json.JSONDecodeError:
            payload = {}
        if not isinstance(payload, dict):
            payload = {}
        turns.append(
            FileOpTurn(
                distinct_files_touched=_payload_int(payload, "distinct_files_touched"),
                total_file_ops=_payload_int(payload, "total_file_ops"),
                repeated_ops=_payload_int(payload, "repeated_ops"),
                repeat_rate=_payload_float(payload, RATE_AGGREGATE),
                max_ops_on_one_file=_payload_int(payload, "max_ops_on_one_file"),
            )
        )
    return turns, len(rows)


def _percentile(sorted_vals: list, q: float) -> float:
    """Linear-interpolated percentile over a NON-EMPTY sorted list (the
    same stdlib-only helper runway.py and report.py use)."""
    if not sorted_vals:
        raise ValueError("percentile of empty sequence")
    if len(sorted_vals) == 1:
        return float(sorted_vals[0])
    pos = q * (len(sorted_vals) - 1)
    lo = int(pos)
    if lo + 1 >= len(sorted_vals):
        return float(sorted_vals[-1])
    return float(sorted_vals[lo] + (pos - lo) * (sorted_vals[lo + 1] - sorted_vals[lo]))


def distribution(values: list) -> Optional[dict]:
    """The OBSERVED distribution of one aggregate over the turns that
    carried it: count, min/max, P50/P90/P99, and mean. None when no turn
    carried the aggregate (an honest empty — never fabricated zeros).
    `accumulating` flags a count below the disclosed sample gate: the
    quantiles still print, but are too few turns to be stable."""
    if not values:
        return None
    s = sorted(values)
    n = len(s)
    return {
        "n": n,
        "min": float(s[0]),
        "p50": _percentile(s, 0.5),
        "p90": _percentile(s, 0.9),
        "p99": _percentile(s, 0.99),
        "max": float(s[-1]),
        "mean": sum(s) / n,
        "accumulating": n < SAMPLE_GATE,
    }


def summarize(turns: list, n_completed: int) -> dict:
    """Fold the per-turn aggregates into the OBSERVED distribution report:
    coverage (how many turns carried file-op telemetry vs did not) plus
    one distribution per aggregate. Descriptive only — no threshold, no
    gate, no recommendation is produced here."""
    with_anchor = sum(
        1 for t in turns if getattr(t, COVERAGE_ANCHOR) is not None
    )
    dists: dict = {}
    for name in AGGREGATE_ORDER:
        values = [
            getattr(t, name) for t in turns if getattr(t, name) is not None
        ]
        dists[name] = distribution(values)

    # repeat_rate is absent (not zero) when total_file_ops was 0 or only
    # the degrade total was measurable — disclose the shortfall against
    # the anchor rather than hiding it.
    rate_n = dists[RATE_AGGREGATE]["n"] if dists[RATE_AGGREGATE] else 0
    return {
        "turns_completed": n_completed,
        "turns_with_fileop_telemetry": with_anchor,
        "turns_without_fileop_telemetry": n_completed - with_anchor,
        "repeat_rate_absent_where_anchor_present": with_anchor - rate_n,
        "sample_gate": SAMPLE_GATE,
        "distributions": dists,
    }


def run(db_path: Path) -> dict:
    """Load and summarize — the one-call entry point. Opens the DB
    read-only; sqlite3 errors propagate to the caller (main degrades them
    to a disclosed gap, never a crash)."""
    conn = open_db(db_path)
    try:
        turns, n_completed = load_fileop_turns(conn)
    finally:
        conn.close()
    result = summarize(turns, n_completed)
    result["db_path"] = str(db_path)
    return result


def _fmt_int_dist(d: dict) -> str:
    return (
        f"min {d['min']:.0f}, P50 {d['p50']:.1f}, P90 {d['p90']:.1f}, "
        f"P99 {d['p99']:.1f}, max {d['max']:.0f} (mean {d['mean']:.2f})"
    )


def _fmt_rate_dist(d: dict) -> str:
    return (
        f"min {d['min']:.3f}, P50 {d['p50']:.3f}, P90 {d['p90']:.3f}, "
        f"P99 {d['p99']:.3f}, max {d['max']:.3f} (mean {d['mean']:.3f})"
    )


def render_section(result: dict) -> list:
    """The 'file-op aggregate distribution' section lines — OBSERVED
    distributions, explicitly not a calibrated model or a threshold."""
    lines = [
        "file-op aggregate distribution (per-turn #67/ADR-052 aggregates, "
        "OBSERVED — not a calibrated model, not a threshold; read-only DB):",
    ]
    completed = result["turns_completed"]
    with_tel = result["turns_with_fileop_telemetry"]
    if completed == 0:
        lines.append(
            "  no provider.turn.completed events yet — nothing to describe "
            "(the PostToolUse capture stamps these as turns run)"
        )
        return lines
    if with_tel == 0:
        lines.append(
            f"  {completed} completed turns, but none carried file-op "
            "telemetry yet — the #67/ADR-052 capture has not stamped these "
            "turns; distribution is honestly empty (accumulating, not zeros)"
        )
        return lines

    lines.append(
        f"  coverage: {with_tel} of {completed} completed turns carry "
        f"file-op telemetry ({result['turns_without_fileop_telemetry']} "
        "without — honestly unknown, never counted as zero)"
    )
    rate_absent = result["repeat_rate_absent_where_anchor_present"]
    if rate_absent:
        lines.append(
            f"  repeat_rate absent on {rate_absent} of those turns "
            "(total_file_ops was 0, or only the degrade total was "
            "measurable — absent, not 0)"
        )
    lines.append(
        f"  distributions below are OBSERVED over correlated turns (not "
        f"independent draws, never a probability); sample gate {result['sample_gate']} "
        "(ADD §15.2, reused) flags too-few-turns as accumulating:"
    )
    for name in AGGREGATE_ORDER:
        d = result["distributions"][name]
        if d is None:
            lines.append(f"    {name}: no turn carried this aggregate yet")
            continue
        body = _fmt_rate_dist(d) if name == RATE_AGGREGATE else _fmt_int_dist(d)
        flag = (
            f" — accumulating (n<{result['sample_gate']}, quantiles unstable)"
            if d["accumulating"]
            else ""
        )
        lines.append(f"    {name}: n={d['n']} turns — {body}{flag}")
    lines.append(
        "  note: these describe what was observed. Grounding a repeat-rate "
        "(or any) stop threshold from them is a separate human choice "
        "(ADR-052 slice-3b / #68) — this report proposes none."
    )
    return lines


def render_text(result: dict) -> str:
    header = [
        "file-op aggregate distribution",
        "==============================",
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
            "given — nothing to describe (pass the DB path explicitly)",
            file=sys.stderr,
        )
        return 1
    # A missing or unopenable DB is a disclosed gap, never a crash: check
    # existence before opening, and degrade any read-only open/read failure
    # (sqlite3.Error) to a clean message with no traceback.
    if not db_path.is_file():
        print(
            f"no Auspex DB at {db_path} — nothing to describe (a missing DB "
            "is a disclosed gap, not a crash; pass an existing read-only DB "
            "path)",
            file=sys.stderr,
        )
        return 1
    try:
        result = run(db_path)
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
