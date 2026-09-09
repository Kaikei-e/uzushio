package stats

import (
	"fmt"
	"math"
)

// FixedInterval is a two-sided, fixed-sample confidence interval. N is the
// number of independent observations, rather than (for example) the number of
// turns nested within a task.
type FixedInterval struct {
	N        int     `json:"n"`
	Mean     float64 `json:"mean"`
	Variance float64 `json:"variance"`
	Lower    float64 `json:"lower"`
	Upper    float64 `json:"upper"`
	Alpha    float64 `json:"alpha"`
	Method   string  `json:"method"`
}

const empiricalBernsteinMethod = "empirical-bernstein-2009-v1"

// EmpiricalBernstein returns a two-sided fixed-sample confidence interval for
// the mean of independent observations with values in [lower, upper]. Callers
// must reduce correlated rows to one independently sampled cluster mean before
// calling it; this function deliberately has no cluster identifier to infer
// that grouping from.
//
// It applies Theorem 11 of Maurer and Pontil (2009),
// https://arxiv.org/abs/0907.3740, to each tail at alpha/2 and joins the tails
// by a union bound. The theorem's log(2/delta) is therefore log(4/alpha).
// It requires a fixed hypothesis and independent observations. In particular,
// choosing a candidate or a margin after inspecting this sample invalidates
// the stated coverage. A caller that needs two such intervals jointly (for
// example a paired difference and absolute quality) allocates its alpha across
// those intervals before calling this function.
func EmpiricalBernstein(values []float64, lower, upper, alpha float64) (FixedInterval, error) {
	if !finite(lower) || !finite(upper) || !(lower < upper) {
		return FixedInterval{}, fmt.Errorf("stats: support must be finite with lower < upper, got [%v, %v]", lower, upper)
	}
	if !finite(alpha) || alpha <= 0 || alpha >= 1 {
		return FixedInterval{}, fmt.Errorf("stats: alpha must be finite and in (0, 1), got %v", alpha)
	}
	if len(values) < 2 {
		return FixedInterval{}, fmt.Errorf("stats: empirical Bernstein needs at least two observations, got %d", len(values))
	}

	// Welford's recurrence avoids subtracting two large, nearly equal sums
	// when cluster scores are close together.
	mean, m2 := 0.0, 0.0
	for i, value := range values {
		if !finite(value) || value < lower || value > upper {
			return FixedInterval{}, fmt.Errorf("stats: observation %d must be finite and in [%v, %v], got %v", i, lower, upper, value)
		}
		n := float64(i + 1)
		delta := value - mean
		mean += delta / n
		m2 += delta * (value - mean)
	}

	n := len(values)
	variance := m2 / float64(n-1) // V_n in Maurer--Pontil, Theorem 11.
	logTerm := math.Log(4 / alpha)
	radius := math.Sqrt(2*variance*logTerm/float64(n)) +
		7*(upper-lower)*logTerm/(3*float64(n-1))
	return FixedInterval{
		N:        n,
		Mean:     mean,
		Variance: variance,
		Lower:    max(lower, mean-radius),
		Upper:    min(upper, mean+radius),
		Alpha:    alpha,
		Method:   empiricalBernsteinMethod,
	}, nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
