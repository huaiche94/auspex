#!/usr/bin/env python3
"""Loader + per-turn delta derivation for `auspex export observations`
JSONL (auspex.observations-export.v1).

Usage:
    python3 research/calibration/observations.py observations.jsonl [--json]

Standard library only. Malformed lines fail loudly (a corrupt dataset
must never silently shrink into a smaller "clean" one), unknown schema
versions and event types fail loudly (a newer exporter's semantics must
not be guessed).

Why the deltas live HERE and not in the Go exporter: statusline usage
totals are session-cumulative (total_cost_usd only grows), so a per-turn
figure is a subtraction across snapshots — an attribution MODEL, not an
observation. The Go bridges capture and refuse to model; research/ is
the modeling layer, so this module owns the derivation and labels it
honestly.

Attribution model (best-effort, documented limits):

  * A turn window opens at a `provider.turn.started` event and is CLOSED
    by the first terminal turn event (`completed`/`failed`/`interrupted`)
    that follows it in the same session. A turn with no terminal event
    before the next `turn.started` (or end of series) is UNCLOSED — no
    delta is derived for it, and it is reported as such.
  * Snapshots LAG the work they measure: the statusline sample carrying
    a turn's final cost may arrive after the Stop hook fired. Samples
    between a turn's terminal event and the NEXT `turn.started` are
    therefore attributed to the finished turn.
  * cost delta  = (last total_cost_usd sample inside the attribution
    window) - (last total_cost_usd sample at or before turn start).
    No pre-turn baseline sample -> underivable. NEVER assume a 0
    baseline: sessions resume with prior totals and retention may have
    deleted the series' head (unknown is not zero).
  * context delta = same construction over used_tokens from
    provider.context.observed. Compaction can SHRINK used_tokens
    mid-window, so negative context deltas are expected, real, and
    surfaced as-is with a note — never clamped silently. A negative
    COST delta would indicate corrupt input (cumulative cost cannot
    shrink) and is likewise surfaced with a note, never dropped.

Managed-run token actuals (issue #11) need NONE of the above modeling:
`auspex run` persists a provider.usage.observed event that is already
per-turn (the provider result line's own final accounting) and already
turn-stamped, carrying input_tokens/output_tokens/cache_*_input_tokens
plus total_tokens (= input + output; the sum choice is documented in
internal/telemetry/claude/managedrun.go). This module only LOADS those
fields; report.py joins them against token predictions on turn_id.
"""

from __future__ import annotations

import argparse
import json
import re
from dataclasses import asdict, dataclass
from datetime import datetime
from pathlib import Path
from typing import Iterator, Optional

SCHEMA_VERSION = "auspex.observations-export.v1"

# The exporter's closed event-type set (internal/retention/observations.go).
EVENT_TYPES = frozenset(
    {
        "provider.usage.observed",
        "provider.context.observed",
        "provider.quota.observed",
        "provider.turn.started",
        "provider.turn.completed",
        "provider.turn.failed",
        "provider.turn.interrupted",
    }
)

TERMINAL_TYPES = frozenset(
    {
        "provider.turn.completed",
        "provider.turn.failed",
        "provider.turn.interrupted",
    }
)

# RFC3339Nano as the Go stores write it (up to 9 fractional digits,
# trailing zeros trimmed, Z or numeric offset). datetime.fromisoformat
# on Python 3.9 accepts neither 'Z' nor >6 fractional digits, hence the
# explicit shape check + truncation.
_TS_RE = re.compile(
    r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$"
)


def parse_ts(value: str, where: str) -> datetime:
    """Parse one RFC3339Nano timestamp, failing loudly with location."""
    m = _TS_RE.match(value)
    if not m:
        raise ValueError(f"{where}: unparseable occurred_at {value!r}")
    base, frac, tz = m.groups()
    frac = (frac or "")[:6].ljust(6, "0")
    if tz == "Z":
        tz = "+00:00"
    return datetime.fromisoformat(f"{base}.{frac}{tz}")


@dataclass(frozen=True)
class Observation:
    """One exported events row. Optional fields are honestly absent
    (unknown is not zero — never default a missing measurement)."""

    event_type: str
    occurred_at: str
    session_id: Optional[str]
    turn_id: Optional[str]
    total_cost_usd: Optional[float]
    total_duration_ms: Optional[int]
    total_api_duration_ms: Optional[int]
    total_lines_added: Optional[int]
    total_lines_removed: Optional[int]
    model_id: Optional[str]
    # Managed-run per-turn token actuals (issue #11) — already per-turn,
    # never cumulative; total_tokens = input + output (see module
    # docstring). None = the row is not a managed-run usage event, or an
    # older capture without the field.
    input_tokens: Optional[int]
    output_tokens: Optional[int]
    cache_read_input_tokens: Optional[int]
    cache_creation_input_tokens: Optional[int]
    total_tokens: Optional[int]
    used_tokens: Optional[int]
    window_tokens: Optional[int]
    used_percent: Optional[float]
    limit_id: Optional[str]
    resets_at: Optional[str]
    effort: Optional[str]
    failure_class: Optional[str]


def load(path: Path) -> Iterator[Observation]:
    """Yield every record in the export at *path*, validating as it goes."""
    with path.open(encoding="utf-8") as f:
        for lineno, line in enumerate(f, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                raw = json.loads(line)
            except json.JSONDecodeError as e:
                raise ValueError(f"{path}:{lineno}: not valid JSON: {e}") from e

            version = raw.get("schema_version")
            if version != SCHEMA_VERSION:
                raise ValueError(
                    f"{path}:{lineno}: schema_version {version!r}, "
                    f"this loader understands only {SCHEMA_VERSION!r}"
                )
            event_type = raw.get("event_type")
            if event_type not in EVENT_TYPES:
                raise ValueError(
                    f"{path}:{lineno}: event_type {event_type!r} is not in "
                    f"the exporter's closed set — refusing to guess its semantics"
                )
            # Validate eagerly so a bad timestamp names its line, not a
            # stack frame deep inside the windowing pass.
            parse_ts(raw["occurred_at"], f"{path}:{lineno}")

            yield Observation(
                event_type=event_type,
                occurred_at=raw["occurred_at"],
                session_id=raw.get("session_id"),
                turn_id=raw.get("turn_id"),
                total_cost_usd=raw.get("total_cost_usd"),
                total_duration_ms=raw.get("total_duration_ms"),
                total_api_duration_ms=raw.get("total_api_duration_ms"),
                total_lines_added=raw.get("total_lines_added"),
                total_lines_removed=raw.get("total_lines_removed"),
                model_id=raw.get("model_id"),
                input_tokens=raw.get("input_tokens"),
                output_tokens=raw.get("output_tokens"),
                cache_read_input_tokens=raw.get("cache_read_input_tokens"),
                cache_creation_input_tokens=raw.get("cache_creation_input_tokens"),
                total_tokens=raw.get("total_tokens"),
                used_tokens=raw.get("used_tokens"),
                window_tokens=raw.get("window_tokens"),
                used_percent=raw.get("used_percent"),
                limit_id=raw.get("limit_id"),
                resets_at=raw.get("resets_at"),
                effort=raw.get("effort"),
                failure_class=raw.get("failure_class"),
            )


@dataclass(frozen=True)
class TurnActuals:
    """One turn's best-effort actuals. None = honestly underivable, with
    the reason in notes — a consumer must never read None as zero."""

    session_id: str
    turn_id: Optional[str]
    started_at: str
    terminal_at: Optional[str]
    outcome: Optional[str]  # completed | failed | interrupted | None (unclosed)
    failure_class: Optional[str]
    cost_delta_usd: Optional[float]
    cost_samples: int  # in-window usage samples carrying total_cost_usd
    context_delta_tokens: Optional[int]
    context_samples: int  # in-window context samples carrying used_tokens
    notes: tuple


def _last_value_at_or_before(samples, cutoff):
    """Latest (ts, value) with ts <= cutoff, else None."""
    best = None
    for ts, value in samples:
        if ts <= cutoff:
            best = value
        else:
            break
    return best


def _window_series(samples, start, end):
    """Values with start < ts < end (end=None means unbounded)."""
    out = []
    for ts, value in samples:
        if ts <= start:
            continue
        if end is not None and ts >= end:
            break
        out.append(value)
    return out


def derive_turn_actuals(observations) -> list:
    """Derive per-turn cost/context deltas per the module's attribution
    model. Rows without a session_id cannot be windowed (a series needs a
    session) and are ignored here — the exporter surfaces them, this
    derivation honestly cannot use them."""
    by_session: dict = {}
    for obs in observations:
        if obs.session_id is None:
            continue
        by_session.setdefault(obs.session_id, []).append(obs)

    turns: list = []
    for session_id in sorted(by_session):
        series = sorted(
            by_session[session_id],
            key=lambda o: parse_ts(o.occurred_at, f"session {session_id}"),
        )
        times = [parse_ts(o.occurred_at, f"session {session_id}") for o in series]

        cost_samples = [
            (t, o.total_cost_usd)
            for t, o in zip(times, series)
            if o.event_type == "provider.usage.observed" and o.total_cost_usd is not None
        ]
        token_samples = [
            (t, o.used_tokens)
            for t, o in zip(times, series)
            if o.event_type == "provider.context.observed" and o.used_tokens is not None
        ]

        starts = [i for i, o in enumerate(series) if o.event_type == "provider.turn.started"]
        for n, i in enumerate(starts):
            start_obs, start_t = series[i], times[i]
            next_start_t = times[starts[n + 1]] if n + 1 < len(starts) else None

            terminal = None
            terminal_t = None
            for j in range(i + 1, len(series)):
                if next_start_t is not None and times[j] >= next_start_t:
                    break
                if series[j].event_type in TERMINAL_TYPES:
                    terminal, terminal_t = series[j], times[j]
                    break

            notes = []
            cost_delta = None
            context_delta = None
            window_cost = _window_series(cost_samples, start_t, next_start_t)
            window_tokens = _window_series(token_samples, start_t, next_start_t)

            if terminal is None:
                notes.append(
                    "unclosed: no terminal turn event captured before the "
                    "next turn (or end of series) — no delta derived"
                )
            else:
                base_cost = _last_value_at_or_before(cost_samples, start_t)
                if base_cost is None:
                    notes.append(
                        "cost underivable: no usage sample at or before turn "
                        "start (resumed session or truncated series — a 0 "
                        "baseline must not be assumed)"
                    )
                elif not window_cost:
                    notes.append("cost underivable: no in-window usage sample")
                else:
                    cost_delta = window_cost[-1] - base_cost
                    if cost_delta < 0:
                        notes.append(
                            "NEGATIVE cost delta: cumulative cost cannot "
                            "shrink — input series is suspect; surfaced as-is"
                        )

                base_tokens = _last_value_at_or_before(token_samples, start_t)
                if base_tokens is None:
                    notes.append(
                        "context underivable: no context sample at or before "
                        "turn start"
                    )
                elif not window_tokens:
                    notes.append("context underivable: no in-window context sample")
                else:
                    context_delta = window_tokens[-1] - base_tokens
                    if context_delta < 0:
                        notes.append(
                            "negative context delta: compaction shrank "
                            "used_tokens mid-window — real, surfaced as-is"
                        )

            turns.append(
                TurnActuals(
                    session_id=session_id,
                    turn_id=start_obs.turn_id,
                    started_at=start_obs.occurred_at,
                    terminal_at=terminal.occurred_at if terminal else None,
                    outcome=(
                        terminal.event_type[len("provider.turn."):] if terminal else None
                    ),
                    failure_class=terminal.failure_class if terminal else None,
                    cost_delta_usd=cost_delta,
                    cost_samples=len(window_cost),
                    context_delta_tokens=context_delta,
                    context_samples=len(window_tokens),
                    notes=tuple(notes),
                )
            )
    return turns


def summarize_turn_actuals(turns) -> dict:
    """The readiness numbers report.py folds into its own output."""
    return {
        "turns": len(turns),
        "closed_turns": sum(1 for t in turns if t.outcome is not None),
        "cost_derivable_turns": sum(1 for t in turns if t.cost_delta_usd is not None),
        "context_derivable_turns": sum(
            1 for t in turns if t.context_delta_tokens is not None
        ),
        "negative_context_deltas": sum(
            1
            for t in turns
            if t.context_delta_tokens is not None and t.context_delta_tokens < 0
        ),
        "negative_cost_deltas": sum(
            1 for t in turns if t.cost_delta_usd is not None and t.cost_delta_usd < 0
        ),
    }


def render_text(turns, summary: dict) -> str:
    lines = [
        "per-turn actuals (best-effort attribution — see module docstring)",
        "=================================================================",
    ]
    if not turns:
        lines.append("(no turn.started events in the export)")
    for t in turns:
        cost = (
            f"cost {t.cost_delta_usd:+.4f} USD ({t.cost_samples} samples)"
            if t.cost_delta_usd is not None
            else "cost underivable"
        )
        ctx = (
            f"context {t.context_delta_tokens:+d} tokens ({t.context_samples} samples)"
            if t.context_delta_tokens is not None
            else "context underivable"
        )
        lines.append(
            f"session {t.session_id} turn {t.turn_id or '?'} "
            f"@ {t.started_at}: {t.outcome or 'UNCLOSED'} — {cost}, {ctx}"
        )
        for note in t.notes:
            lines.append(f"    note: {note}")
    lines.append("")
    lines.append(
        "turns: {turns} (closed {closed_turns}, cost-derivable "
        "{cost_derivable_turns}, context-derivable {context_derivable_turns}, "
        "negative context deltas {negative_context_deltas})".format(**summary)
    )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("export", type=Path, help="observations export JSONL path")
    parser.add_argument("--json", action="store_true", help="machine-readable output")
    args = parser.parse_args()

    turns = derive_turn_actuals(list(load(args.export)))
    summary = summarize_turn_actuals(turns)

    if args.json:
        print(
            json.dumps(
                {"turns": [asdict(t) for t in turns], "summary": summary},
                indent=2,
                sort_keys=True,
            )
        )
    else:
        print(render_text(turns, summary))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
