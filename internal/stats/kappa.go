package stats

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
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
	// clusters holds each independent unit's own counts, so a leave-one-out
	// replicate is a subtraction rather than a re-tally, and order keeps the
	// replicates in a fixed sequence.
	clusters map[string][][]int
	order    []string
	// anonymous numbers the rows that named no cluster, so Observe and
	// ObserveIn can be mixed without one silently joining the other's unit.
	anonymous int
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
	return &Table{
		categories: slices.Clone(categories),
		counts:     square(len(categories)),
		clusters:   map[string][][]int{},
	}, nil
}

// square returns a k by k count matrix.
func square(k int) [][]int {
	out := make([][]int, k)
	for i := range out {
		out[i] = make([]int, k)
	}
	return out
}

// Categories returns the vocabulary, in the order the marginals are reported
// in.
func (t *Table) Categories() []string { return slices.Clone(t.categories) }

// N is how many rows the table holds.
func (t *Table) N() int { return t.n }

// Clusters is how many independent units those rows came from. It is the
// number an interval is computed over, and it is published beside n because
// the two differing is the whole reason the interval is what it is.
func (t *Table) Clusters() int { return len(t.order) }

// Observe records one item both raters labelled. A label outside the
// vocabulary is an error rather than a silently dropped row: an item nobody
// counted is an item that quietly changes every rate in the report.
func (t *Table) Observe(first, second string) error {
	t.anonymous++
	return t.ObserveIn("\x00row-"+strconv.Itoa(t.anonymous), first, second)
}

// ObserveIn records one item both raters labelled, as part of a named cluster.
//
// The cluster is the unit the interval is computed over. Two rows in one
// cluster are allowed to agree with each other for reasons that have nothing
// to do with the raters — the same prompt, the same run, the same seed — and
// counting them as two independent observations is how a confidence interval
// comes out three times narrower than the data supports.
func (t *Table) ObserveIn(cluster, first, second string) error {
	i := slices.Index(t.categories, first)
	j := slices.Index(t.categories, second)
	if i < 0 || j < 0 {
		return fmt.Errorf("stats: (%q, %q) is outside the vocabulary %v", first, second, t.categories)
	}
	if cluster == "" {
		return fmt.Errorf("stats: (%q, %q) names no cluster", first, second)
	}
	own, seen := t.clusters[cluster]
	if !seen {
		own = square(len(t.categories))
		t.clusters[cluster] = own
		t.order = append(t.order, cluster)
	}
	own[i][j]++
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
	// N is how many rows the table held, and Clusters how many independent
	// units they came from. Where the two differ, N is the count kappa was
	// computed from and Clusters is the count the interval was.
	N        int `json:"n"`
	Clusters int `json:"clusters"`
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
	PABAK Coefficient `json:"pabak"`
	// CI is the leave-one-cluster-out jackknife interval on Kappa at the level
	// it was asked for. It is **null** rather than a zero interval where there
	// is no honest number: fewer than two clusters, an undefined coefficient,
	// or a replicate that lost its variation. An interval printed as
	// [0.000, 0.000] beside a coefficient of 1.000 is read as a measurement,
	// which is the one thing it must not be.
	CI *Interval `json:"ci"`
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
		Clusters:   len(t.order),
		Categories: slices.Clone(t.categories),
		First:      make([]float64, k),
		Second:     make([]float64, k),
		Kappa:      Undefined(),
		PABAK:      Undefined(),
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
	c.PABAK = Coefficient((float64(k)*c.PO - 1) / float64(k-1))
	return c
}

// jackknife returns the leave-one-cluster-out interval on kappa.
//
// One replicate per cluster: every row that cluster contributed is subtracted
// from the table at once. Where each row is its own cluster this is the
// ordinary leave-one-out jackknife; where a hundred items each contributed
// nine rows it is a hundred replicates rather than nine hundred, and the
// interval is about three times wider than the row-wise one — which is the
// width the data actually supports.
func jackknife(t *Table, alpha float64) *Interval {
	full := coefficients(t)
	clusters := len(t.order)
	if !full.Kappa.Defined() || clusters < 2 {
		return nil
	}
	replicates := make([]float64, 0, clusters)
	mean := 0.0
	for _, name := range t.order {
		own := t.clusters[name]
		rows := subtract(t, own)
		left := coefficients(t)
		add(t, own, rows)
		if !left.Kappa.Defined() {
			// A replicate with no variation left says nothing about the
			// spread, and there is no honest number to put in its place.
			return nil
		}
		replicates = append(replicates, left.Kappa.Float())
		mean += left.Kappa.Float()
	}
	g := float64(clusters)
	mean /= g
	variance := 0.0
	for _, value := range replicates {
		variance += (value - mean) * (value - mean)
	}
	variance *= (g - 1) / g
	half := zFor(alpha) * math.Sqrt(variance)
	return &Interval{
		Lo: math.Max(-1, full.Kappa.Float()-half),
		Hi: math.Min(1, full.Kappa.Float()+half),
	}
}

// subtract removes one cluster's counts from the table and answers with how
// many rows went; add puts them back. The pair exists so a replicate costs a
// k-by-k walk rather than a re-tally of the whole corpus.
func subtract(t *Table, own [][]int) int {
	rows := 0
	for i := range own {
		for j := range own[i] {
			t.counts[i][j] -= own[i][j]
			rows += own[i][j]
		}
	}
	t.n -= rows
	return rows
}

func add(t *Table, own [][]int, rows int) {
	for i := range own {
		for j := range own[i] {
			t.counts[i][j] += own[i][j]
		}
	}
	t.n += rows
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

// zFor returns the two-sided standard normal quantile for a level: the z with
// P(|Z| <= z) = 1 - alpha, which is sqrt(2)*erfinv(1 - alpha).
//
// It is computed rather than tabulated. A table gets the three levels somebody
// thought of and quietly hands back 1.96 for the fourth, so a report asked for
// an 80% interval prints "80% CI" over a 95% one — a mislabelled interval,
// which is worse than a missing one.
func zFor(alpha float64) float64 {
	switch {
	case alpha <= 0:
		return math.Inf(1)
	case alpha >= 1:
		return 0
	}
	return math.Sqrt2 * math.Erfinv(1-alpha)
}

// Proportion is a rate whose rows come in clusters, with the interval that
// fact demands.
//
// It exists because Wilson does not apply to most of the rates a calibration
// reports. A swap flip rate is one row per (item, seed, pair): nine rows of one
// item move together, and a score interval computed over nine hundred of them
// is about three times too narrow. Where the rows really are independent — one
// per item — Wilson is the better tool and this is not needed, which is why
// both live here and every reported rate says which it used.
type Proportion struct {
	// Value is the pooled rate, successes over trials.
	Value float64 `json:"value"`
	// Successes and Trials are the totals it came from.
	Successes int `json:"successes"`
	Trials    int `json:"trials"`
	// Clusters is how many independent units the rows came from.
	Clusters int `json:"clusters"`
	// CI is the leave-one-cluster-out jackknife interval, null where there
	// are fewer than two clusters or no trials.
	CI *Interval `json:"ci"`
	// Level is the coverage, 1 - alpha, and Method names how CI was made, so a
	// number quoted out of a report carries how much to trust its width.
	Level  float64 `json:"level"`
	Method string  `json:"method"`
}

// The two ways this package makes an interval on a rate.
const (
	// MethodClusterJackknife deletes one whole cluster per replicate.
	MethodClusterJackknife = "cluster-jackknife"
	// MethodWilson is the score interval, which assumes independent rows.
	MethodWilson = "wilson"
)

// ClusteredProportion pools per-cluster counts and puts a leave-one-cluster-out
// jackknife interval on the ratio.
func ClusteredProportion(successes, trials []int, alpha float64) Proportion {
	p := Proportion{Level: 1 - alpha, Method: MethodClusterJackknife}
	for i := range trials {
		if trials[i] == 0 {
			continue
		}
		p.Successes += successes[i]
		p.Trials += trials[i]
		p.Clusters++
	}
	if p.Trials == 0 {
		return p
	}
	p.Value = float64(p.Successes) / float64(p.Trials)
	if p.Clusters < 2 {
		return p
	}
	replicates := make([]float64, 0, p.Clusters)
	mean := 0.0
	for i := range trials {
		if trials[i] == 0 {
			continue
		}
		left := p.Trials - trials[i]
		if left == 0 {
			return p
		}
		value := float64(p.Successes-successes[i]) / float64(left)
		replicates = append(replicates, value)
		mean += value
	}
	g := float64(p.Clusters)
	mean /= g
	variance := 0.0
	for _, value := range replicates {
		variance += (value - mean) * (value - mean)
	}
	variance *= (g - 1) / g
	half := zFor(alpha) * math.Sqrt(variance)
	p.CI = &Interval{Lo: math.Max(0, p.Value-half), Hi: math.Min(1, p.Value+half)}
	return p
}

// IndependentProportion is the same summary for rows that really are
// independent — one per item — with the Wilson score interval on it.
func IndependentProportion(successes, trials int, alpha float64) Proportion {
	p := Proportion{
		Successes: successes, Trials: trials, Clusters: trials,
		Level: 1 - alpha, Method: MethodWilson,
	}
	if trials == 0 {
		return p
	}
	p.Value = float64(successes) / float64(trials)
	interval := Wilson(successes, trials, alpha)
	p.CI = &interval
	return p
}
