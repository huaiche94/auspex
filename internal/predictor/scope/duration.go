package scope

import "time"

// Phase-1 cold-start duration model for the per-turn wall-clock forecast
// (issue #62). These coefficients are a deliberate BOOTSTRAP guess, not an
// ADD §14.6 table entry and NOT measured values — they exist so a
// zero-history deployment can produce a bounded, explainable duration
// estimate. They are intentionally kept in this file (not coldstart.go) so
// they are never mistaken for the ADD-sourced scope table.
//
// The model is derived from the already-computed scope estimate rather than
// a frozen per-class constant, so the duration moves with the classified
// scope (and with session blending / fan-out widening applied upstream).
// That is the Phase-1 mitigation for the "a static constant carries no
// signal" lesson behind #42 / DECISION_LOG D-15: the number still is not
// calibrated, but it is not frozen either. Calibration against real
// total_duration_ms telemetry is Phase 2, gated on #11 / #42.
//
// Shape: duration = overhead + perFile*filesChanged + perLine*linesChanged.
// P50 uses P50 scope; P90 uses P90 scope. Wall-clock is dominated by
// tool-call latency and generation, neither of which this rule forecasts
// directly — so the per-file term proxies for "open/read/locate" tool work
// and the per-line term proxies for "generate + verify" work. The tail
// runs wide on purpose, consistent with how the scope estimator treats P90.
const (
	durationPerTurnOverhead = 15 * time.Second       // model spin-up, prompt read, initial planning
	durationPerChangedFile  = 8 * time.Second        // open, read, locate the edit site
	durationPerChangedLine  = 400 * time.Millisecond // generate + verify one changed line
)

// estimateDurationNanos returns P50/P90 wall-clock duration estimates in
// nanoseconds (the unit of domain.ScopeEstimate.DurationP50/P90), computed
// from the finalized changed-files and changed-lines quantiles. Inputs are
// the same float64 values the caller has already sorted to satisfy
// P50 <= P90, so the outputs preserve that ordering by construction.
func estimateDurationNanos(filesChangedP50, filesChangedP90, linesChangedP50, linesChangedP90 float64) (p50, p90 int64) {
	p50 = durationNanos(filesChangedP50, linesChangedP50)
	p90 = durationNanos(filesChangedP90, linesChangedP90)
	if p90 < p50 {
		p90 = p50
	}
	return p50, p90
}

func durationNanos(filesChanged, linesChanged float64) int64 {
	d := float64(durationPerTurnOverhead) +
		float64(durationPerChangedFile)*filesChanged +
		float64(durationPerChangedLine)*linesChanged
	return int64(d)
}
