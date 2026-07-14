#!/usr/bin/env python3
"""Data-readiness + calibration-coverage report over a calibration export.

Usage:
    python3 research/calibration/report.py calibration.jsonl [--json] \\
        [--observations observations.jsonl]

Grounding discipline (research/README.md): cohorts below the ADD §15.2
sample gate (8) are REPORTED as insufficient, never fitted. With sparse
data the readiness section is the report's whole value; the coverage
sections activate by themselves as capture fills in.

With --observations, the report additionally folds in:

  * per-turn ACTUALS readiness derived from `auspex export observations`
    (observations.py's best-effort attribution): how many turns exist,
    how many are closed by a terminal event, and how many have derivable
    cost/context deltas;
  * TOKEN COVERAGE (issue #11's last capture gap, closed by managed
    runs): calibration records carry per-turn predicted
    token_p50/p80/p90, and managed-run usage rows carry the same turn's
    ACTUAL total_tokens (already per-turn, already turn-stamped — no
    delta modeling needed); joining the two on turn_id yields the
    fraction of turns whose actual landed <= P50 / <= P80 / <= P90.
    Only turns with BOTH sides count, and the join count is always
    reported. Native hook turns cannot join (the statusline carries no
    per-turn tokens), so coverage speaks for managed-run (`auspex run`)
    turns only.
"""

from __future__ import annotations

import argparse
import json
import sys
from collections import Counter
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from load import Record, load  # noqa: E402
from observations import derive_turn_actuals, parse_ts, summarize_turn_actuals  # noqa: E402
from observations import load as load_observations  # noqa: E402

# ADD §15.2's "count(similar) >= 8" gate, the same constant the Go side
# uses (RuleTokenForecaster.MinSimilarSamples, minSimilarTurnSamples).
SAMPLE_GATE = 8


def token_coverage(records: list[Record], observations) -> dict:
    """Join per-turn predicted token quantiles with same-turn actual
    total_tokens from managed-run usage rows, and report coverage.

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
    actuals: dict = {}
    for obs in observations:
        if obs.event_type != "provider.usage.observed":
            continue
        if obs.turn_id is None or obs.total_tokens is None:
            continue
        ts = parse_ts(obs.occurred_at, f"usage sample for turn {obs.turn_id}")
        prev = actuals.get(obs.turn_id)
        if prev is None or ts >= prev[0]:
            actuals[obs.turn_id] = (ts, obs.total_tokens)

    predicted = [
        r
        for r in records
        if any(q is not None for q in (r.token_p50, r.token_p80, r.token_p90))
    ]
    joined = [(r, actuals[r.turn_id][1]) for r in predicted if r.turn_id in actuals]

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


def build_report(
    records: list[Record],
    turn_actuals: dict | None = None,
    token_cov: dict | None = None,
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
            "token P50/P80/P90 coverage was not assessed (needs "
            "--observations): managed runs (`auspex run`) capture per-turn "
            "total_tokens actuals now; native hook turns still lack a "
            "source (the statusline carries no per-turn tokens)"
        )
    elif token_cov["joined_turns"] == 0:
        gaps.append(
            "0 turns join a token prediction with a same-turn total_tokens "
            "actual: managed runs (`auspex run`) supply per-turn actuals "
            "now — dogfood more turns through `auspex run`; native hook "
            "turns still lack a source (the statusline carries no per-turn "
            "tokens), so hook-driven turns can never join"
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
            "token coverage (predicted vs managed-run actuals, joined on turn_id):"
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
    parser.add_argument("--json", action="store_true", help="machine-readable output")
    args = parser.parse_args()

    records = list(load(args.export))
    turn_actuals = None
    token_cov = None
    if args.observations is not None:
        observations = list(load_observations(args.observations))
        turn_actuals = summarize_turn_actuals(derive_turn_actuals(observations))
        token_cov = token_coverage(records, observations)
    report = build_report(records, turn_actuals, token_cov)

    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(render_text(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
