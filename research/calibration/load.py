"""Loader for `auspex export calibration` JSONL (auspex.calibration-export.v1).

Standard library only. Malformed lines fail loudly (a corrupt dataset
must never silently shrink into a smaller "clean" one), unknown schema
versions fail loudly (a newer exporter's semantics must not be guessed).
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Iterator, Optional

SCHEMA_VERSION = "auspex.calibration-export.v1"


@dataclass(frozen=True)
class Record:
    """One prediction-vs-actual pair. Optional fields are honestly absent
    (unknown is not zero — never default a missing quantile or label)."""

    source: str  # "live" | "archived"
    prediction_id: str
    turn_id: str
    session_id: Optional[str]
    predictor_id: str
    predictor_version: str
    predicted_at: str
    token_p50: Optional[int]
    token_p80: Optional[int]
    token_p90: Optional[int]
    # Predicted cost band (#72), priced from the token quantiles by
    # internal/pricing — the exact band the forecast card showed
    # (cost_low_usd = P50 × input price, cost_high_usd = P90 × output
    # price). None when the row carried no token forecast (no forecast ->
    # no cost estimate — unknown is not zero, never a fabricated $0).
    cost_low_usd: Optional[float]
    cost_high_usd: Optional[float]
    cost_model_family: Optional[str]
    # #62 duration pair, riding like the cost fields. duration_p50_ns /
    # duration_p90_ns are the PREDICTED wall-clock forecast in NANOSECONDS
    # (the scope estimator's own unit); actual_duration_ms is the ACTUAL
    # per-turn duration in MILLISECONDS (the turn's provider.usage.observed
    # total_duration_ms). Distinct units, each matching its Go source
    # verbatim — report.py's duration_coverage() reconciles them. None =
    # honestly absent (an uncalibrated forecast that left duration unknown,
    # or a turn with no attributable usage event) — never read as zero.
    duration_p50_ns: Optional[int]
    duration_p90_ns: Optional[int]
    actual_duration_ms: Optional[int]
    overall_risk_score: float
    confidence: str
    calibrated: bool
    provider: Optional[str]
    model_id: Optional[str]
    model_family: Optional[str]
    effort: Optional[str]
    actual_known: bool
    actual_outcome: Optional[str]
    actual_failure_class: Optional[str]
    actual_outcome_at: Optional[str]
    # Per-turn token ACTUALS (#72 item 4), joined by the Go exporter from
    # the turn's own events: the Stop hook's transcript-captured accounting
    # (native hook turns) or the managed run's turn-stamped usage event.
    # actual_total_tokens keeps the Go side's input+output definition; the
    # four raw classes ride alongside (the #66 cache-aware costing
    # prerequisite); actual_api_calls is how many API calls the sum covers
    # (transcript capture only). None = no turn-attributable token event
    # (pre-#72 history) — honestly absent, never zero.
    actual_input_tokens: Optional[int]
    actual_output_tokens: Optional[int]
    actual_cache_read_input_tokens: Optional[int]
    actual_cache_creation_input_tokens: Optional[int]
    actual_total_tokens: Optional[int]
    actual_api_calls: Optional[int]
    reason_codes: tuple[str, ...]

    @property
    def cohort(self) -> tuple[str, str, str]:
        """The #20 normalized triple, with '?' for honestly-unlabeled axes."""
        return (
            self.provider or "?",
            self.model_family or "?",
            self.effort or "?",
        )


def load(path: Path) -> Iterator[Record]:
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
            source = raw.get("source")
            if source not in ("live", "archived"):
                raise ValueError(f"{path}:{lineno}: unknown source {source!r}")

            yield Record(
                source=source,
                prediction_id=raw["prediction_id"],
                turn_id=raw["turn_id"],
                session_id=raw.get("session_id"),
                predictor_id=raw["predictor_id"],
                predictor_version=raw["predictor_version"],
                predicted_at=raw["predicted_at"],
                token_p50=raw.get("token_p50"),
                token_p80=raw.get("token_p80"),
                token_p90=raw.get("token_p90"),
                cost_low_usd=raw.get("cost_low_usd"),
                cost_high_usd=raw.get("cost_high_usd"),
                cost_model_family=raw.get("cost_model_family"),
                duration_p50_ns=raw.get("duration_p50_ns"),
                duration_p90_ns=raw.get("duration_p90_ns"),
                actual_duration_ms=raw.get("actual_duration_ms"),
                overall_risk_score=raw["overall_risk_score"],
                confidence=raw["confidence"],
                calibrated=raw["calibrated"],
                provider=raw.get("provider"),
                model_id=raw.get("model_id"),
                model_family=raw.get("model_family"),
                effort=raw.get("effort"),
                actual_known=raw["actual_known"],
                actual_outcome=raw.get("actual_outcome"),
                actual_failure_class=raw.get("actual_failure_class"),
                actual_outcome_at=raw.get("actual_outcome_at"),
                actual_input_tokens=raw.get("actual_input_tokens"),
                actual_output_tokens=raw.get("actual_output_tokens"),
                actual_cache_read_input_tokens=raw.get("actual_cache_read_input_tokens"),
                actual_cache_creation_input_tokens=raw.get("actual_cache_creation_input_tokens"),
                actual_total_tokens=raw.get("actual_total_tokens"),
                actual_api_calls=raw.get("actual_api_calls"),
                reason_codes=tuple(raw.get("reason_codes") or ()),
            )
