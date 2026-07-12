package predictor

import "sort"

// Quantiles is the frozen-shape empirical quantile triple used throughout
// the predictor (ADD §14.1 ScopeEstimate P50/P80/P90 fields, §15.2 initial
// token predictor). It carries a monotonicity guarantee:
//
//	P50 <= P80 <= P90
//
// for every input, including degenerate ones (empty, single-element,
// all-identical, unsorted). No field is ever NaN or +/-Inf.
type Quantiles struct {
	P50 float64
	P80 float64
	P90 float64
	// SampleCount is the number of observations the quantiles were
	// computed from. 0 means no observations were available — callers
	// must treat P50/P80/P90 as cold-start placeholders (0), not as a
	// measured empirical result, whenever SampleCount == 0.
	SampleCount int
}

// EmpiricalQuantiles computes P50/P80/P90 over obs using linear
// interpolation between closest ranks (the common "type 7" estimator).
// It never mutates obs, never divides by zero, and never returns NaN or
// Inf: an empty slice yields the zero Quantiles{} (P50=P80=P90=0,
// SampleCount=0), and every other input yields values drawn only from
// obs's own finite range, so if obs contains no NaN/Inf the output
// contains none either.
//
// Monotonicity (P50 <= P80 <= P90) holds unconditionally, including for
// empty, single-element, all-identical, and unsorted inputs.
func EmpiricalQuantiles(obs []float64) Quantiles {
	n := len(obs)
	if n == 0 {
		return Quantiles{}
	}

	sorted := make([]float64, n)
	copy(sorted, obs)
	sort.Float64s(sorted)

	return Quantiles{
		P50:         quantileOf(sorted, 0.50),
		P80:         quantileOf(sorted, 0.80),
		P90:         quantileOf(sorted, 0.90),
		SampleCount: n,
	}
}

// quantileOf returns the p-quantile (0<=p<=1) of an already-sorted,
// non-empty slice via linear interpolation between closest ranks. This is
// the sole place fractional-index math happens, so monotonicity in p
// (p1 <= p2 => quantileOf(s,p1) <= quantileOf(s,p2)) is a direct
// consequence of `sorted` being non-decreasing and the interpolation
// weight being a non-decreasing function of p.
func quantileOf(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}

	// Rank in [0, n-1] as a continuous position.
	pos := p * float64(n-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= n {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}
