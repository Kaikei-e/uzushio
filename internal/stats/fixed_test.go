package stats_test

import (
	"math"
	"slices"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/stats"
)

func TestEmpiricalBernsteinGolden(t *testing.T) {
	t.Parallel()
	values := make([]float64, 0, 100)
	for range 50 {
		values = append(values, 0.4)
	}
	for range 50 {
		values = append(values, 0.6)
	}

	got, err := stats.EmpiricalBernstein(values, 0, 1, 0.05)
	if err != nil {
		t.Fatalf("EmpiricalBernstein: %v", err)
	}
	if got.N != 100 || got.Method != "empirical-bernstein-2009-v1" || got.Alpha != 0.05 {
		t.Fatalf("metadata = %+v", got)
	}
	if math.Abs(got.Mean-0.5) > 1e-12 {
		t.Errorf("mean = %.15f, want 0.5", got.Mean)
	}
	if math.Abs(got.Variance-0.010101010101010102) > 1e-14 {
		t.Errorf("variance = %.17f, want sample variance", got.Variance)
	}
	// The numbers are Theorem 11's formula by hand: sqrt(2V ln(80)/100)
	// + 7 ln(80)/(3*99)), then centered at one half.
	if math.Abs(got.Lower-0.36696662701416695) > 1e-12 || math.Abs(got.Upper-0.633033372985833) > 1e-12 {
		t.Errorf("interval = [%.15f, %.15f], want [0.366966627014167, 0.633033372985833]", got.Lower, got.Upper)
	}
}

func TestEmpiricalBernsteinZeroVarianceIsNotAZeroWidthInterval(t *testing.T) {
	t.Parallel()
	values := make([]float64, 100)
	for i := range values {
		values[i] = 0.5
	}
	got, err := stats.EmpiricalBernstein(values, 0, 1, 0.05)
	if err != nil {
		t.Fatalf("EmpiricalBernstein: %v", err)
	}
	if got.Variance != 0 {
		t.Errorf("variance = %v, want zero", got.Variance)
	}
	if !(got.Lower < got.Mean && got.Mean < got.Upper) {
		t.Errorf("zero-variance interval = [%v, %v] around %v, want nonzero width", got.Lower, got.Upper, got.Mean)
	}
}

func TestEmpiricalBernsteinIsPermutationInvariant(t *testing.T) {
	t.Parallel()
	values := []float64{-1, -0.5, 0, 0.25, 0.75, 1}
	permuted := slices.Clone(values)
	slices.Reverse(permuted)
	first, err := stats.EmpiricalBernstein(values, -1, 1, 0.1)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := stats.EmpiricalBernstein(permuted, -1, 1, 0.1)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	for _, pair := range [][2]float64{{first.Mean, second.Mean}, {first.Variance, second.Variance}, {first.Lower, second.Lower}, {first.Upper, second.Upper}} {
		if math.Abs(pair[0]-pair[1]) > 1e-14 {
			t.Errorf("permutation changed result: %v vs %v", pair[0], pair[1])
		}
	}
}

func TestEmpiricalBernsteinCountsInputsRatherThanInferringClusters(t *testing.T) {
	t.Parallel()
	// A repeated value can be repeated trials from one task or observations
	// from three tasks. The API cannot know which; callers must pass one
	// cluster mean per independent task.
	got, err := stats.EmpiricalBernstein([]float64{0.5, 0.5, 0.5}, 0, 1, 0.05)
	if err != nil {
		t.Fatalf("EmpiricalBernstein: %v", err)
	}
	if got.N != 3 {
		t.Errorf("n = %d, want each supplied value counted", got.N)
	}
}

func TestEmpiricalBernsteinRefusesInvalidInput(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                string
		values              []float64
		lower, upper, alpha float64
	}{
		{"one observation", []float64{0}, 0, 1, 0.05},
		{"empty", nil, 0, 1, 0.05},
		{"reversed support", []float64{0, 1}, 1, 0, 0.05},
		{"equal support", []float64{0, 0}, 0, 0, 0.05},
		{"nan support", []float64{0, 1}, math.NaN(), 1, 0.05},
		{"nan alpha", []float64{0, 1}, 0, 1, math.NaN()},
		{"zero alpha", []float64{0, 1}, 0, 1, 0},
		{"one alpha", []float64{0, 1}, 0, 1, 1},
		{"nan observation", []float64{0, math.NaN()}, 0, 1, 0.05},
		{"infinite observation", []float64{0, math.Inf(1)}, 0, 1, 0.05},
		{"below support", []float64{-0.1, 0}, 0, 1, 0.05},
		{"above support", []float64{0, 1.1}, 0, 1, 0.05},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := stats.EmpiricalBernstein(c.values, c.lower, c.upper, c.alpha); err == nil {
				t.Fatal("EmpiricalBernstein accepted invalid input")
			}
		})
	}
}

func TestEmpiricalBernsteinContractsWithMoreIdenticalClusters(t *testing.T) {
	t.Parallel()
	two, err := stats.EmpiricalBernstein([]float64{0.5, 0.5}, 0, 1, 0.05)
	if err != nil {
		t.Fatalf("two: %v", err)
	}
	manyValues := make([]float64, 100)
	for i := range manyValues {
		manyValues[i] = 0.5
	}
	many, err := stats.EmpiricalBernstein(manyValues, 0, 1, 0.05)
	if err != nil {
		t.Fatalf("many: %v", err)
	}
	if many.Upper-many.Lower >= two.Upper-two.Lower {
		t.Errorf("width at n=100 is %v, want less than n=2 width %v", many.Upper-many.Lower, two.Upper-two.Lower)
	}
}
