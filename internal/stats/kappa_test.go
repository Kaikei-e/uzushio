package stats_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/stats"
)

const epsilon = 1e-9

// table fills a table from a square count matrix, row-major, so a test can be
// written as the contingency table it is arguing about.
func table(t *testing.T, categories []string, counts [][]int) *stats.Table {
	t.Helper()
	built, err := stats.NewTable(categories)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	for i, row := range counts {
		for j, count := range row {
			for range count {
				if err := built.Observe(categories[i], categories[j]); err != nil {
					t.Fatalf("Observe: %v", err)
				}
			}
		}
	}
	return built
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > epsilon {
		t.Fatalf("%s = %.12f, want %.12f", what, got, want)
	}
}

// TestCohen1960 is the worked example from the paper the coefficient is named
// after: Cohen, "A Coefficient of Agreement for Nominal Scales", Educ. Psych.
// Meas. 20(1):37-46 (1960), the three-category table with marginals (.60, .30,
// .10) and (.50, .30, .20). Its p_o is .70, its p_e is .41 and its kappa is
// .29/.59.
//
// The numbers below are computed by hand from those proportions rather than
// from a run of this code, which is the point of the test: it is the one place
// in the package where the arithmetic is checked against something other than
// itself.
func TestCohen1960(t *testing.T) {
	categories := []string{"a", "b", "c"}
	got := stats.Agreement(table(t, categories, [][]int{
		{88, 14, 18},
		{10, 40, 10},
		{2, 6, 12},
	}), 0.05)

	if got.N != 200 {
		t.Fatalf("n = %d, want 200", got.N)
	}
	near(t, "p_o", got.PO, 0.70)
	near(t, "p_e", got.PE, 0.41)
	near(t, "kappa", got.Kappa.Float(), 0.29/0.59)
	// PABAK fixes the chance term at 1/k, so it reads (3·0.70 − 1)/2.
	near(t, "pabak", got.PABAK, 0.55)
	for i, want := range []float64{0.60, 0.30, 0.10} {
		near(t, "first marginal", got.First[i], want)
	}
	for i, want := range []float64{0.50, 0.30, 0.20} {
		near(t, "second marginal", got.Second[i], want)
	}
	// The interval is a jackknife rather than a closed form, so the test
	// checks the properties a caller relies on rather than a digit string:
	// it brackets the estimate and stays inside the coefficient's range.
	if got.CI.Lo > got.Kappa.Float() || got.CI.Hi < got.Kappa.Float() {
		t.Fatalf("jackknife interval %v does not bracket kappa %v", got.CI, got.Kappa)
	}
	if got.CI.Lo < -1 || got.CI.Hi > 1 {
		t.Fatalf("jackknife interval %v leaves -1..1", got.CI)
	}
	if got.CI.Hi-got.CI.Lo < 1e-6 {
		t.Fatalf("jackknife interval %v has no width at n=200", got.CI)
	}
}

// TestTwoByTwoByHand checks the other closed form a reader can verify in their
// head: 20/5/10/15 over fifty items, where the row marginals are even, the
// column marginals are not, and kappa and PABAK happen to coincide.
func TestTwoByTwoByHand(t *testing.T) {
	got := stats.Agreement(table(t, []string{"yes", "no"}, [][]int{
		{20, 5},
		{10, 15},
	}), 0.05)
	near(t, "p_o", got.PO, 0.70)
	near(t, "p_e", got.PE, 0.50)
	near(t, "kappa", got.Kappa.Float(), 0.40)
	near(t, "pabak", got.PABAK, 0.40)
}

// TestPrevalenceParadox is the reason PABAK and the marginals are reported at
// all: two tables with the same observed agreement, one of which kappa scores
// at zero because one category takes almost everything.
func TestPrevalenceParadox(t *testing.T) {
	balanced := stats.Agreement(table(t, []string{"yes", "no"}, [][]int{
		{45, 5},
		{5, 45},
	}), 0.05)
	skewed := stats.Agreement(table(t, []string{"yes", "no"}, [][]int{
		{90, 5},
		{5, 0},
	}), 0.05)
	near(t, "balanced p_o", balanced.PO, 0.90)
	near(t, "skewed p_o", skewed.PO, 0.90)
	near(t, "balanced pabak", balanced.PABAK, 0.80)
	near(t, "skewed pabak", skewed.PABAK, 0.80)
	if skewed.Kappa.Float() >= 0.1 {
		t.Fatalf("skewed kappa = %v, want the paradox: near zero at p_o = 0.9", skewed.Kappa)
	}
	if balanced.Kappa.Float() <= 0.7 {
		t.Fatalf("balanced kappa = %v, want the same p_o to score high", balanced.Kappa)
	}
}

// TestKappaIsUndefinedWithoutVariation records the one degenerate case: both
// raters put everything in one category, so p_e is 1 and "better than chance"
// has no answer. It must not come back as a number.
func TestKappaIsUndefinedWithoutVariation(t *testing.T) {
	got := stats.Agreement(table(t, []string{"a", "b"}, [][]int{
		{30, 0},
		{0, 0},
	}), 0.05)
	if got.Kappa.Defined() {
		t.Fatalf("kappa = %v, want undefined at p_e = 1", got.Kappa)
	}
	near(t, "p_o", got.PO, 1)
	near(t, "p_e", got.PE, 1)
	body, err := json.Marshal(got.Kappa)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(body) != "null" {
		t.Fatalf("undefined kappa marshals as %s, want null", body)
	}
	var back stats.Coefficient
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Defined() {
		t.Fatal("null read back as a defined coefficient")
	}
}

// TestWilsonByHand checks the score interval at the two places the Wald
// interval is worst: a rate of zero, where Wald reports a half-width of zero,
// and a rate near the top. Both are hand-computed from the closed form with
// z = 1.959963984540054.
func TestWilsonByHand(t *testing.T) {
	zero := stats.Wilson(0, 20, 0.05)
	near(t, "wilson lo at 0/20", zero.Lo, 0)
	near(t, "wilson hi at 0/20", zero.Hi, 0.16112515805281938)

	most := stats.Wilson(15, 20, 0.05)
	near(t, "wilson lo at 15/20", most.Lo, 0.531299122381256)
	near(t, "wilson hi at 15/20", most.Hi, 0.8881382985923343)

	// No trials is no information, which is the whole interval rather than a
	// point at zero.
	none := stats.Wilson(0, 0, 0.05)
	if none.Lo != 0 || none.Hi != 1 {
		t.Fatalf("Wilson(0, 0) = %v, want the whole interval", none)
	}
}

func TestTableRefusesNonsense(t *testing.T) {
	if _, err := stats.NewTable([]string{"only"}); err == nil {
		t.Fatal("a one-category table was accepted")
	}
	if _, err := stats.NewTable([]string{"a", "a"}); err == nil {
		t.Fatal("a repeated category was accepted")
	}
	built, err := stats.NewTable([]string{"a", "b"})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	if err := built.Observe("a", "c"); err == nil {
		t.Fatal("a label outside the vocabulary was counted")
	}
	if built.N() != 0 {
		t.Fatalf("n = %d after a refused observation", built.N())
	}
	empty := stats.Agreement(built, 0.05)
	if empty.Kappa.Defined() {
		t.Fatalf("kappa over no items = %v, want undefined", empty.Kappa)
	}
}
