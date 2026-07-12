package predictor

import (
	"math"
	"math/rand"
	"testing"
)

// assertSaneQuantiles checks the invariants that must hold for every input,
// per predictor-04's contract: monotonicity, and no NaN/Inf.
func assertSaneQuantiles(t *testing.T, label string, q Quantiles) {
	t.Helper()
	for _, v := range []struct {
		name string
		val  float64
	}{{"P50", q.P50}, {"P80", q.P80}, {"P90", q.P90}} {
		if math.IsNaN(v.val) {
			t.Fatalf("%s: %s is NaN", label, v.name)
		}
		if math.IsInf(v.val, 0) {
			t.Fatalf("%s: %s is Inf", label, v.name)
		}
	}
	if !(q.P50 <= q.P80) {
		t.Fatalf("%s: monotonicity violated: P50=%v > P80=%v", label, q.P50, q.P80)
	}
	if !(q.P80 <= q.P90) {
		t.Fatalf("%s: monotonicity violated: P80=%v > P90=%v", label, q.P80, q.P90)
	}
}

// TestQuantileMonotonicDegenerateInputs covers the explicitly required
// degenerate cases: empty, single-element, all-identical, unsorted.
func TestQuantileMonotonicDegenerateInputs(t *testing.T) {
	cases := []struct {
		name string
		obs  []float64
	}{
		{"nil slice", nil},
		{"empty slice", []float64{}},
		{"single element zero", []float64{0}},
		{"single element positive", []float64{42.5}},
		{"single element negative", []float64{-7}},
		{"all identical positive", []float64{3, 3, 3, 3, 3}},
		{"all identical zero", []float64{0, 0, 0, 0}},
		{"all identical negative", []float64{-1, -1, -1}},
		{"unsorted ascending pairs", []float64{5, 1, 4, 2, 3}},
		{"unsorted descending", []float64{9, 7, 5, 3, 1}},
		{"two elements", []float64{10, 20}},
		{"two elements reversed", []float64{20, 10}},
		{"negative and positive mixed", []float64{-5, 3, -1, 8, 0, -3, 7}},
		{"large values", []float64{1e18, 2e18, 3e18}},
		{"small fractional values", []float64{0.0001, 0.0002, 0.00005}},
		{"repeated with one outlier", []float64{1, 1, 1, 1, 1000}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := EmpiricalQuantiles(c.obs)
			assertSaneQuantiles(t, c.name, q)

			if len(c.obs) == 0 {
				if q != (Quantiles{}) {
					t.Fatalf("empty input must yield zero Quantiles{}, got %+v", q)
				}
			} else {
				if q.SampleCount != len(c.obs) {
					t.Fatalf("SampleCount = %d, want %d", q.SampleCount, len(c.obs))
				}
			}
		})
	}
}

// TestQuantileMonotonicPropertyRandom is a property-based test: for many
// random slices of varying length and distribution, the monotonicity and
// no-NaN/Inf invariants must hold, and quantiles must lie within the
// input's own [min, max] range (never extrapolated).
func TestQuantileMonotonicPropertyRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	for trial := 0; trial < 2000; trial++ {
		n := rng.Intn(50) // 0..49
		obs := make([]float64, n)
		for i := range obs {
			switch rng.Intn(4) {
			case 0:
				obs[i] = rng.NormFloat64() * 100
			case 1:
				obs[i] = float64(rng.Intn(7)) // encourage duplicates
			case 2:
				obs[i] = -rng.Float64() * 1e6
			default:
				obs[i] = rng.Float64() * 1e6
			}
		}

		q := EmpiricalQuantiles(obs)
		assertSaneQuantiles(t, "random trial", q)

		if n == 0 {
			continue
		}

		min, max := obs[0], obs[0]
		for _, v := range obs {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		if q.P50 < min || q.P50 > max {
			t.Fatalf("P50=%v out of input range [%v,%v] for obs=%v", q.P50, min, max, obs)
		}
		if q.P90 < min || q.P90 > max {
			t.Fatalf("P90=%v out of input range [%v,%v] for obs=%v", q.P90, min, max, obs)
		}
	}
}

// TestQuantileDoesNotMutateInput ensures EmpiricalQuantiles copies before
// sorting.
func TestQuantileDoesNotMutateInput(t *testing.T) {
	obs := []float64{5, 3, 1, 4, 2}
	original := append([]float64(nil), obs...)
	_ = EmpiricalQuantiles(obs)
	for i := range obs {
		if obs[i] != original[i] {
			t.Fatalf("EmpiricalQuantiles mutated input: got %v, want %v", obs, original)
		}
	}
}

// TestQuantileDeterministic checks identical input always yields identical
// output.
func TestQuantileDeterministic(t *testing.T) {
	obs := []float64{8, 1, 6, 3, 9, 2, 7, 4, 5}
	a := EmpiricalQuantiles(obs)
	b := EmpiricalQuantiles(obs)
	if a != b {
		t.Fatalf("EmpiricalQuantiles not deterministic: %+v vs %+v", a, b)
	}
}

// TestQuantileKnownValues pins down the interpolation method on a simple,
// hand-verifiable input so a future refactor can't silently change
// semantics without a test failure.
func TestQuantileKnownValues(t *testing.T) {
	// 1..11 (11 values): p*(-1+n) index math, type-7 estimator.
	obs := make([]float64, 11)
	for i := range obs {
		obs[i] = float64(i + 1) // 1..11
	}
	q := EmpiricalQuantiles(obs)

	// pos = p*(n-1); n=11 -> n-1=10
	// P50: pos=5.0 -> obs[5]=6
	// P80: pos=8.0 -> obs[8]=9
	// P90: pos=9.0 -> obs[9]=10
	if q.P50 != 6 {
		t.Fatalf("P50 = %v, want 6", q.P50)
	}
	if q.P80 != 9 {
		t.Fatalf("P80 = %v, want 9", q.P80)
	}
	if q.P90 != 10 {
		t.Fatalf("P90 = %v, want 10", q.P90)
	}
}
