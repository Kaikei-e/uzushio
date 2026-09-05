package stats

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

// This file is the agreement half of the package: what two sets of labels over
// the same items say about each other. It is used to calibrate a judge — the
// judge against itself with the candidates swapped, the judge against itself on
// a second seed, the judge against the people who labelled the same items — and
// nothing in it knows that.
//
// Three cautions are built into what it returns rather than left to the reader.
//
//   - A single kappa is not reportable. Cohen's kappa is a function of the
//     marginals as well as of the agreement, and the two directions of that
//     dependence are both perverse: high observed agreement collapses to a low
//     kappa where one category dominates, and two raters biased in *opposite*
//     directions score higher than two well-calibrated ones (Feinstein &
//     Cicchetti 1990; the formal result is Warrens 2010). So Coefficients
//     carries p_o, p_e, both marginal vectors, n and PABAK beside the
//     coefficient, and a caller that prints only the coefficient is printing a
//     number that cannot be interpreted.
//   - Weighting is deliberately absent. Cohen's weighted kappa (1968) needs an
//     order on the categories along which some disagreements are less serious
//     than others. "The judge chose c1 and the human chose c2" is not nearer to
//     agreement than "c1 against c3": the labels are nominal, and Fleiss, Cohen
//     & Everitt's rule — weight only where the seriousness of a disagreement can
//     be specified — says to leave them unweighted.
//   - The interval is a jackknife, not the closed-form asymptotic one. At the
//     sample sizes a hand-labelled calibration reaches, the Fleiss–Cohen–Everitt
//     variance understates the spread, and the resampling interval has the
//     better coverage (Zapf et al. 2016 for the nominal case; the same finding
//     drives the jackknife interval in the Krippendorff literature).

// Coefficient is a statistic that may be undefined, which happens for a real
// reason rather than as a numerical accident: kappa divides by 1 − p_e, and
// p_e is 1 exactly when both raters put every item in the same single
// category. Agreement is then perfect and chance agreement is also perfect, so
// "how much better than chance" has no answer.
//
// JSON has no NaN, so an undefined coefficient is written as null — which says
// what is true, where a sentinel would read as a number somebody computed.
type Coefficient float64

// Undefined is the coefficient that has no value.
func Undefined() Coefficient { return Coefficient(math.NaN()) }

// Defined reports whether the coefficient has a value.
func (c Coefficient) Defined() bool { return !math.IsNaN(float64(c)) && !math.IsInf(float64(c), 0) }

// Float returns the value as an ordinary float, NaN where it is undefined.
func (c Coefficient) Float() float64 { return float64(c) }

// MarshalJSON writes a defined coefficient as a number and an undefined one as
// null.
func (c Coefficient) MarshalJSON() ([]byte, error) {
	if !c.Defined() {
		return []byte("null"), nil
	}
	return json.Marshal(float64(c))
}

// UnmarshalJSON reads null back as undefined.
func (c *Coefficient) UnmarshalJSON(body []byte) error {
	if string(body) == "null" {
		*c = Undefined()
		return nil
	}
	var f float64
	if err := json.Unmarshal(body, &f); err != nil {
		return err
	}
	*c = Coefficient(f)
	return nil
}

// Table is a square contingency table over a fixed nominal vocabulary: how
// many items each pair of labels was given by the two raters.
//
// The vocabulary is fixed up front rather than discovered from the data
// because kappa's chance term is 1/k in expectation and k is therefore part of
// the claim. A judge scored over {c1, c2, c3} and one scored over {c1, c2, c3,
// abstain} are not comparable, and letting the observed labels decide k would
// make the comparison depend on whether the judge happened to abstain once.
type Table struct {
	categories []string
	counts     [][]int
	n          int
}

// NewTable returns an empty table over a vocabulary. The vocabulary must hold
// at least two distinct categories: agreement over one category is not a
// measurement.
func NewTable(categories []string) (*Table, error) {
	if len(categories) < 2 {
		return nil, fmt.Errorf("stats: an agreement table needs at least two categories, got %v", categories)
	}
	for i, name := range categories {
		if slices.Index(categories, name) != i {
			return nil, fmt.Errorf("stats: category %q is declared twice", name)
		}
	}
	counts := make([][]int, len(categories))
	for i := range counts {
		counts[i] = make([]int, len(categories))
	}
	return &Table{categories: slices.Clone(categories), counts: counts}, nil
}

// Categories returns the vocabulary, in the order the marginals are reported
// in.
func (t *Table) Categories() []string { return slices.Clone(t.categories) }

// N is how many items the table holds.
func (t *Table) N() int { return t.n }

// Observe records one item both raters labelled. A label outside the
// vocabulary is an error rather than a silently dropped row: an item nobody
// counted is an item that quietly changes every rate in the report.
func (t *Table) Observe(first, second string) error {
	i := slices.Index(t.categories, first)
	j := slices.Index(t.categories, second)
	if i < 0 || j < 0 {
		return fmt.Errorf("stats: (%q, %q) is outside the vocabulary %v", first, second, t.categories)
	}
	t.counts[i][j]++
	t.n++
	return nil
}

// Count returns how many items were labelled first by one rater and second by
// the other.
func (t *Table) Count(first, second string) int {
	i := slices.Index(t.categories, first)
	j := slices.Index(t.categories, second)
	if i < 0 || j < 0 {
		return 0
	}
	return t.counts[i][j]
}

// Coefficients is everything a report has to carry about one agreement, which
// is more than the coefficient. See the note at the top of this file.
type Coefficients struct {
	// N is how many items were labelled by both.
	N int `json:"n"`
	// Categories is the vocabulary, in marginal order.
	Categories []string `json:"categories"`
	// First and Second are the two raters' marginal distributions. They are
	// published because kappa is a function of them: an asymmetry here is the
	// thing that makes a coefficient hard to read, and hiding it is what makes
	// the paradox surprising.
	First  []float64 `json:"first_marginal"`
	Second []float64 `json:"second_marginal"`
	// PO is the observed agreement, PE the agreement expected from the product
	// of the marginals.
	PO float64 `json:"p_o"`
	PE float64 `json:"p_e"`
	// Kappa is (p_o − p_e)/(1 − p_e), undefined where p_e is 1.
	Kappa Coefficient `json:"kappa"`
	// PABAK is (k·p_o − 1)/(k − 1): the same coefficient with the chance term
	// fixed at the uniform 1/k instead of estimated from the marginals
	// (Byrt, Bishop & Carlin 1993). It is reported beside kappa rather than
	// instead of it, because the pair is what says whether a low kappa is a
	// disagreement or a prevalence artefact.
	PABAK float64 `json:"pabak"`
	// CI is the jackknife interval on Kappa at the level Jackknife was asked
	// for. It is the zero interval where the coefficient is undefined or n is
	// below two.
	CI Interval `json:"ci"`
	// Alpha is the level CI was computed at.
	Alpha float64 `json:"alpha"`
}

// Agreement computes the coefficients of a table, with a jackknife interval at
// level alpha.
func Agreement(t *Table, alpha float64) Coefficients {
	c := coefficients(t)
	c.Alpha = alpha
	c.CI = jackknife(t, alpha)
	return c
}

// coefficients is Agreement without the interval, which is what the jackknife
// itself needs of each leave-one-out table.
func coefficients(t *Table) Coefficients {
	k := len(t.categories)
	c := Coefficients{
		N:          t.n,
		Categories: slices.Clone(t.categories),
		First:      make([]float64, k),
		Second:     make([]float64, k),
		Kappa:      Undefined(),
	}
	if t.n == 0 {
		return c
	}
	n := float64(t.n)
	for i := range k {
		for j := range k {
			count := float64(t.counts[i][j])
			c.First[i] += count / n
			c.Second[j] += count / n
			if i == j {
				c.PO += count / n
			}
		}
	}
	for i := range k {
		c.PE += c.First[i] * c.Second[i]
	}
	if c.PE < 1 {
		c.Kappa = Coefficient((c.PO - c.PE) / (1 - c.PE))
	}
	c.PABAK = (float64(k)*c.PO - 1) / float64(k-1)
	return c
}

// jackknife returns the leave-one-out interval on kappa.
//
// Every item in one cell of the table leaves the same table behind when it is
// removed, so the n recomputations are k² at most, weighted by the cell
// counts. That is not an approximation: it is the same set of pseudo-values,
// counted rather than enumerated.
func jackknife(t *Table, alpha float64) Interval {
	full := coefficients(t)
	if !full.Kappa.Defined() || t.n < 2 {
		return Interval{}
	}
	k := len(t.categories)
	mean := 0.0
	replicates := make([]float64, 0, k*k)
	weights := make([]float64, 0, k*k)
	for i := range k {
		for j := range k {
			if t.counts[i][j] == 0 {
				continue
			}
			t.counts[i][j]--
			t.n--
			left := coefficients(t)
			t.counts[i][j]++
			t.n++
			if !left.Kappa.Defined() {
				// A leave-one-out table with no variation left says nothing
				// about the spread, and there is no honest number to put in
				// its place.
				return Interval{}
			}
			weight := float64(t.counts[i][j])
			replicates = append(replicates, left.Kappa.Float())
			weights = append(weights, weight)
			mean += weight * left.Kappa.Float()
		}
	}
	n := float64(t.n)
	mean /= n
	variance := 0.0
	for i, value := range replicates {
		variance += weights[i] * (value - mean) * (value - mean)
	}
	variance *= (n - 1) / n
	half := zFor(alpha) * math.Sqrt(variance)
	return Interval{
		Lo: math.Max(-1, full.Kappa.Float()-half),
		Hi: math.Min(1, full.Kappa.Float()+half),
	}
}

// Wilson returns the score interval for a proportion: the set of p the score
// test does not reject, rather than the normal interval around the observed
// rate.
//
// It is the interval to use here because the rates a calibration reports —
// a swap flip rate, an abstention rate — are often near zero or near one at a
// sample size in the low hundreds, which is exactly where the Wald interval
// runs off the end of [0, 1] and reports a half-width of zero for a rate of
// zero.
func Wilson(successes, trials int, alpha float64) Interval {
	if trials <= 0 {
		return Interval{Lo: 0, Hi: 1}
	}
	z := zFor(alpha)
	n := float64(trials)
	p := float64(successes) / n
	denominator := 1 + z*z/n
	centre := (p + z*z/(2*n)) / denominator
	half := z / denominator * math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	return Interval{Lo: math.Max(0, centre-half), Hi: math.Min(1, centre+half)}
}

// zFor returns the two-sided normal quantile for a level. Only the levels an
// eval actually runs at are tabulated; anything else falls back to 1.96, which
// is stated rather than silent because a made-up quantile is worse than a
// familiar one.
func zFor(alpha float64) float64 {
	switch {
	case alpha >= 0.999:
		return 0
	case math.Abs(alpha-0.10) < 1e-9:
		return 1.6448536269514722
	case math.Abs(alpha-0.01) < 1e-9:
		return 2.5758293035489004
	}
	return 1.959963984540054
}
