package calibrate

import (
	"fmt"
	"math"
	"sort"
)

// Median is the middle value, averaging the two middle ones at an even count.
// It is the centre a band is built on rather than the mean, because one bad run
// out of five moves a mean and does not move a median.
func Median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// Mean is the arithmetic mean.
func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

// StdDev is the sample standard deviation, with the N−1 denominator. Fewer than
// two values have no spread to measure and answer zero, which the half-width's
// floors then cover.
func StdDev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := Mean(values)
	var sum float64
	for _, value := range values {
		sum += (value - mean) * (value - mean)
	}
	return math.Sqrt(sum / float64(len(values)-1))
}

// MADScale is the constant that makes a median absolute deviation estimate the
// standard deviation of a normal sample.
const MADScale = 1.4826

// MAD is the median absolute deviation about the median, scaled by MADScale so
// it is on the same footing as StdDev.
//
// It is the alternative spread statistic rather than the default: it is the
// robust choice and it is also the wasteful one at N = 5, where three of the
// five deviations are thrown away. A calibration set is five to ten runs of the
// same unmodified tree, which is the case an outlier is least likely and a
// precise spread most valuable.
func MAD(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	centre := Median(values)
	deviations := make([]float64, len(values))
	for i, value := range values {
		deviations[i] = math.Abs(value - centre)
	}
	return MADScale * Median(deviations)
}

// ToleranceMinN and ToleranceMaxN bound the calibration sets this build has a
// tolerance factor for.
const (
	ToleranceMinN = 5
	ToleranceMaxN = 12
)

// toleranceK95x90 is the two-sided normal tolerance factor k₂(N, p=0.95,
// γ=0.90), indexed from ToleranceMinN: the multiple of the sample standard
// deviation whose interval covers 95 % of the population with 90 % confidence,
// given N observations.
//
// Source: the classical two-sided normal tolerance factor, NBS Handbook 91
// (Natrella, *Experimental Statistics*) Table A-6, equivalently ISO 16269-6
// Table B.1 and NIST/SEMATECH e-Handbook §7.2.6.3. The closed form behind the
// tabulation is
//
//	k₂ = sqrt( (N−1)(1+1/N) · z²₀.₉₇₅ / χ²₀.₁₀,ₙ₋₁ )
//
// A table rather than a computation because the whole of what this needs from
// three distributions is eight numbers, and eight numbers can be checked
// against a published table by a reader who does not trust the code.
//
// Why this factor and not three standard deviations. Three sample standard
// deviations of five observations is not three population standard deviations
// of anything: at N = 5 the sample s has a 34 % coefficient of variation and a
// 5th percentile of 0.42 σ, so `mean ± 3s` is a 95/73 interval — it delivers
// the 95 % coverage its label implies only about 73 % of the time, and one
// calibration in twenty produces a band covering under 75 % of clean runs. The
// k₂ factor is what pays for that, and it is why the number at N = 5 is 4.16
// rather than 3. It falls to 3.02 by N = 10, which is the argument for running
// the reference more than five times.
//
// The values at N = 5, 6, 7, 8 and 10 are the published ones. N = 9, 11 and 12
// are the same closed form evaluated at those sizes, which reproduces the
// published values to within 0.007 across the table.
var toleranceK95x90 = [...]float64{
	4.164, // N = 5
	3.730, // N = 6
	3.464, // N = 7
	3.268, // N = 8
	3.128, // N = 9
	3.021, // N = 10
	2.935, // N = 11
	2.865, // N = 12
}

// ToleranceK is the two-sided normal tolerance factor for a calibration set of
// n runs.
//
// It refuses outside the table rather than extrapolating. Below five the factor
// is not merely large, it is unstable — the spread it multiplies is not an
// estimate of anything — and above twelve a band is being built on a σ that
// between-session drift has already invalidated, so the honest answer is to say
// the tool does not cover it.
func ToleranceK(n int) (float64, error) {
	if n < ToleranceMinN {
		return 0, fmt.Errorf(
			"%w: N=%d: the two-sided tolerance factor is tabulated from N=%d. Below that the "+
				"sample standard deviation is not an estimate of the population's, and a band "+
				"built on it false-fails the next clean run. Run the reference more times, or "+
				"name a multiplier with --k",
			ErrCalibrate, n, ToleranceMinN)
	}
	if n > ToleranceMaxN {
		return 0, fmt.Errorf(
			"%w: N=%d: this build tabulates the two-sided tolerance factor to N=%d; "+
				"name a multiplier with --k",
			ErrCalibrate, n, ToleranceMaxN)
	}
	return toleranceK95x90[n-ToleranceMinN], nil
}

// CIGuide is the half-width the tracked file's own header prescribes: "at least
// 3× the observed interleave half-range".
//
// It is computed and reported and it is not used. The interleave half-range is
// a within-session number — the spread across the three rounds of one gate run,
// with the machine in one state — and a band has to cover the between-run
// spread, which is a different and larger thing measured across whole
// verifications. On the 2026-09-05 reference set the two disagree in both
// directions: the guide overstates the between-run spread three to four times
// on the ladder invariants and understates it by about half on the rate-limit
// pair. Printing it beside the derived width is what lets a reader see that,
// rather than take the deviation from the tracked guidance on trust.
func CIGuide(ciHalf []float64) float64 {
	var max float64
	for _, value := range ciHalf {
		if value > max {
			max = value
		}
	}
	return 3 * max
}

// roundOut rounds a band edge away from the centre at `places` decimals, so the
// rounding a rendered band goes through never narrows it. A lo of 8.50432
// written as 8.5044 would be a band that rejects a value the calibration set
// contained.
func roundOut(value float64, places int, up bool) float64 {
	scale := math.Pow(10, float64(places))
	if up {
		return math.Ceil(value*scale) / scale
	}
	return math.Floor(value*scale) / scale
}
