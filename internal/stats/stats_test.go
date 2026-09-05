package stats_test

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// screening and confirm are the two parameter sets `uzushio run` uses, written
// out here rather than imported so a change to the command's defaults does not
// silently rewrite what the statistics are tested against.
var (
	screeningIn  = stats.Params{Alpha: 0.05, Margin: 0.15, CountGate: stats.CountGateOff, Cap: 60, Prior: 1}
	screeningOut = stats.Params{Alpha: 0.05, Margin: 0.15, CountGate: 0, Cap: 60, Prior: 1}
)

// TestLogE holds the e-value to the closed form in the research: log E =
// logB(a+b, a+c) − logB(a, a) + (b+c)·log 2, evaluated by hand at points that
// exercise the ends.
func TestLogE(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		b, c int
		want float64
	}{
		// No evidence at all, and one pair, are both E = 1: a single
		// disagreement cannot distinguish a coin from anything.
		{0, 0, 0},
		{1, 0, 0},
		{5, 0, 1.6739764336},
		{8, 1, 1.7385149547},
		{12, 3, 1.5043215672},
		{20, 5, 3.1900859565},
		// Symmetry: the evidence against θ = ½ does not know which way the
		// disagreement leans.
		{0, 8, 3.3479528671},
		// Evidence *for* the null reads as an e-value below one, so a
		// logarithm below zero.
		{3, 3, -0.7827593392},
		{15, 15, -1.4992653688},
	} {
		if got := stats.LogE(1, c.b, c.c); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("LogE(1, %d, %d) = %.10f, want %.10f", c.b, c.c, got, c.want)
		}
	}
	if a, b := stats.LogE(1, 7, 2), stats.LogE(1, 2, 7); a != b {
		t.Errorf("LogE is not symmetric in b and c: %v vs %v", a, b)
	}
	// The point null of LogEAt at θ = ½ is LogE.
	if a, b := stats.LogEAt(1, 9, 3, 0.5), stats.LogE(1, 9, 3); math.Abs(a-b) > 1e-12 {
		t.Errorf("LogEAt at one half = %v, LogE = %v", a, b)
	}
}

// TestLogEIsAMartingaleInExpectation is the property the type-I error rests
// on: under the null the e-value has expectation at most one, so Ville's
// inequality bounds the chance it ever crosses 1/α. It is checked exactly, by
// summing over every outcome of a fixed number of discordant pairs under
// θ = ½ rather than by simulating them.
func TestLogEIsAMartingaleInExpectation(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 2, 5, 10, 20} {
		total := 0.0
		for b := 0; b <= n; b++ {
			// P(b wins out of n) under θ = ½ is C(n, b)/2^n.
			logChoose, _ := math.Lgamma(float64(n) + 1)
			lb, _ := math.Lgamma(float64(b) + 1)
			lc, _ := math.Lgamma(float64(n-b) + 1)
			probability := math.Exp(logChoose - lb - lc - float64(n)*math.Ln2)
			total += probability * math.Exp(stats.LogE(1, b, n-b))
		}
		if total > 1+1e-9 {
			t.Errorf("E[E_n] at n = %d discordant pairs is %.12f, want at most 1", n, total)
		}
	}
}

// TestThetaConfidenceSequence reproduces the worked confidence sequences in the
// research: the interval on the win rate among discordant pairs at α = 0.05
// with a Beta(1,1) mixture, for the tallies its table was computed at.
func TestThetaConfidenceSequence(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		b, c   int
		lo, hi float64
	}{
		{3, 3, 0.077, 0.923},
		{6, 6, 0.149, 0.851},
		{15, 15, 0.246, 0.754},
		{30, 30, 0.307, 0.693},
		{60, 60, 0.356, 0.644},
		{4, 5, 0.089, 0.850},
		{22, 23, 0.274, 0.707},
		{8, 7, 0.197, 0.849},
		{5, 1, 0.278, 0.999},
		{9, 3, 0.340, 0.972},
	} {
		got := stats.ThetaCS(1, c.b, c.c, 0.05)
		if math.Abs(got.Lo-c.lo) > 5e-4 || math.Abs(got.Hi-c.hi) > 5e-4 {
			t.Errorf("ThetaCS(1, %d, %d, 0.05) = [%.3f, %.3f], want [%.3f, %.3f]",
				c.b, c.c, got.Lo, got.Hi, c.lo, c.hi)
		}
	}
	// No disagreements is no information about the win rate.
	if got := stats.ThetaCS(1, 0, 0, 0.05); got.Lo != 0 || got.Hi != 1 {
		t.Errorf("ThetaCS with no discordant pairs = %+v, want the whole unit interval", got)
	}
}

// TestThetaConfidenceSequenceDoesNotCollapseOnAOneSidedTally is a regression
// test for the case a real run hit first: every discordant pair going the same
// way puts the observed rate at an endpoint, where log E is infinite. Reading
// the interval off that endpoint would report a one-pair lead as certainty.
func TestThetaConfidenceSequenceDoesNotCollapseOnAOneSidedTally(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ b, c int }{{1, 0}, {0, 1}, {3, 0}, {0, 3}, {5, 0}, {12, 0}} {
		got := stats.ThetaCS(1, c.b, c.c, 0.05)
		if got.Lo >= got.Hi {
			t.Errorf("ThetaCS(1, %d, %d) = [%v, %v], which is not an interval", c.b, c.c, got.Lo, got.Hi)
		}
		// Up to five discordant pairs the e-value cannot reach 1/α whichever
		// way they lean, so the interval still has to admit a fair coin. Above
		// that it legitimately need not: twelve pairs in one direction is
		// evidence, and the interval says so.
		if c.b+c.c <= 5 && (got.Lo >= 0.5 || got.Hi <= 0.5) {
			t.Errorf("ThetaCS(1, %d, %d) = [%.3f, %.3f]; %d discordant pairs cannot rule out a fair coin",
				c.b, c.c, got.Lo, got.Hi, c.b+c.c)
		}
	}
	// The concrete case: one pair, won by the edit, over three trials. The
	// interval on the difference has to admit that the baseline is better.
	params := stats.Params{Alpha: 0.05, Margin: 0.15, CountGate: 0, Cap: 60, Prior: 1}
	reading := params.Read(stats.Evidence{Wins: 1, Ties: 2})
	if reading.Delta.Lo >= 0 {
		t.Errorf("one winning pair out of three gives delta_lo = %+.3f; it must still admit a loss",
			reading.Delta.Lo)
	}
	if reading.Verdict != vocab.VerdictInconclusive {
		t.Errorf("verdict on one winning pair out of three is %s, want inconclusive", reading.Verdict)
	}
}

// TestDeltaConfidenceSequence pins the joint bound on the pass-rate
// difference: the confidence sequence on the win rate among discordant pairs
// and the one on the discordance itself, each at α/2, combined over the
// product.
//
// The research published a *plug-in* interval instead — δ = d̂·(2θ − 1) with
// the observed discordance treated as known — and its half-widths are the
// third column below. They are not level-α: at the boundary δ = −m with
// d₀ = 0.2 and a cap of 60 the plug-in declares non-inferiority about 22% of
// the time against a nominal 5%, and δ_lo can never fall below −d̂, so an edit
// that lost every discordant pair it had reads as `hold`. The joint bound
// costs roughly twice the width and is the one that is true.
func TestDeltaConfidenceSequence(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		b, c   int
		pairs  int
		lo, hi float64
		plugIn float64 // the research's half-width, for the record
	}{
		{"30 pairs at d0 0.2, no effect", 3, 3, 30, -0.431, 0.431, 0.169},
		{"60 pairs at d0 0.2, no effect", 6, 6, 60, -0.300, 0.300, 0.140},
		{"150 pairs at d0 0.2, no effect", 15, 15, 150, -0.178, 0.178, 0.102},
		{"300 pairs at d0 0.2, no effect", 30, 30, 300, -0.120, 0.120, 0.077},
		{"600 pairs at d0 0.2, no effect", 60, 60, 600, -0.081, 0.081, 0.057},
		{"60 pairs at d0 0.5, no effect", 15, 15, 60, -0.381, 0.381, 0.254},
		{"150 pairs at d0 0.3, no effect", 22, 23, 150, -0.210, 0.194, 0.136},
		{"30 pairs at d0 0.2, a ten point gain", 5, 1, 30, -0.255, 0.489, 0.089},
		{"60 pairs at d0 0.2, a ten point gain", 9, 3, 60, -0.154, 0.387, 0.064},
	} {
		params := stats.Params{Alpha: 0.05, Margin: 0.15, CountGate: stats.CountGateOff, Cap: 60, Prior: 1}
		reading := params.Read(stats.Evidence{
			Wins:   c.b,
			Losses: c.c,
			Ties:   c.pairs - c.b - c.c,
		})
		if math.Abs(reading.Delta.Lo-c.lo) > 5e-4 || math.Abs(reading.Delta.Hi-c.hi) > 5e-4 {
			t.Errorf("%s: delta = [%+.3f, %+.3f], want [%+.3f, %+.3f]",
				c.name, reading.Delta.Lo, reading.Delta.Hi, c.lo, c.hi)
		}
		// The bound is wider than the plug-in it replaces, everywhere. A
		// version of this that came out narrower would be a version that had
		// quietly stopped bounding the discordance.
		if half := (reading.Delta.Hi - reading.Delta.Lo) / 2; half <= c.plugIn {
			t.Errorf("%s: half-width %.3f is not wider than the plug-in %.3f", c.name, half, c.plugIn)
		}
	}
}

// TestAnEditThatLostEveryDiscordantPairIsNotHeld is the case the plug-in got
// wrong and the reason the bound was changed: six pairs lost, none won, over
// sixty. The plug-in put δ_lo at exactly −d̂ = −0.100, inside m = 0.15, and
// certified the edit as no worse than the baseline.
func TestAnEditThatLostEveryDiscordantPairIsNotHeld(t *testing.T) {
	t.Parallel()
	reading := screeningOut.Read(stats.Evidence{Wins: 0, Losses: 6, Ties: 54})
	if reading.Verdict == vocab.VerdictHold {
		t.Errorf("b=0, c=6 over 60 pairs reads as hold; delta = [%+.4f, %+.4f]",
			reading.Delta.Lo, reading.Delta.Hi)
	}
	if reading.NonInferior {
		t.Errorf("b=0, c=6 over 60 pairs is certified non-inferior at m=%v; delta_lo = %+.4f",
			screeningOut.Margin, reading.Delta.Lo)
	}
	if reading.Delta.Lo >= -0.100 {
		t.Errorf("delta_lo = %+.4f; the plug-in floor at -d_hat = -0.100 is still in place",
			reading.Delta.Lo)
	}
	if reading.Verdict != vocab.VerdictInconclusive {
		t.Errorf("verdict = %s, want inconclusive", reading.Verdict)
	}
}

// TestVerdicts walks the four verdicts and the order they are checked in.
func TestVerdicts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		params   stats.Params
		evidence stats.Evidence
		want     vocab.Verdict
	}{
		{
			// Nothing seen at all: no interval, so nothing certified.
			"no pairs", screeningOut, stats.Evidence{}, vocab.VerdictInconclusive,
		},
		{
			// The interval at 60 pairs and no disagreement is well inside
			// ±0.15, so non-inferiority is established and no gain is.
			"sixty pairs, no disagreement", screeningOut,
			stats.Evidence{Ties: 60}, vocab.VerdictHold,
		},
		{
			"the margin not yet reached", screeningOut,
			stats.Evidence{Wins: 3, Losses: 3, Ties: 24}, vocab.VerdictInconclusive,
		},
		{
			"a demonstrated gain", screeningOut,
			stats.Evidence{Wins: 14, Losses: 1, Ties: 45}, vocab.VerdictImprove,
		},
		{
			"a demonstrated harm", screeningOut,
			stats.Evidence{Wins: 1, Losses: 14, Ties: 45}, vocab.VerdictRegress,
		},
		{
			// The count gate is independent of the statistics and fires on
			// its own: one task the edit lost outright is a regression
			// whatever the e-process says about the pairs as a whole.
			"the count gate", screeningOut,
			stats.Evidence{Wins: 20, Losses: 2, Ties: 38, LostTasks: 1}, vocab.VerdictRegress,
		},
		{
			"the count gate off", screeningIn,
			stats.Evidence{Wins: 20, Losses: 2, Ties: 38, LostTasks: 2}, vocab.VerdictImprove,
		},
		{
			// Harm is read before the gain arm, so an edit that is both is
			// never reported as a hold.
			"harm and a breached count gate together", screeningOut,
			stats.Evidence{Wins: 1, Losses: 14, Ties: 45, LostTasks: 5}, vocab.VerdictRegress,
		},
	} {
		if got := c.params.Read(c.evidence).Verdict; got != c.want {
			t.Errorf("%s: verdict %s, want %s", c.name, got, c.want)
		}
	}
}

// TestDecided says which verdicts stop a split. A regress and an improve are
// settled; a hold is not, because the remaining budget is exactly what turns
// "no evidence of regression" into "evidence of improvement".
func TestDecided(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		evidence stats.Evidence
		want     bool
	}{
		{stats.Evidence{Wins: 14, Losses: 1, Ties: 45}, true},
		{stats.Evidence{Wins: 1, Losses: 14, Ties: 45}, true},
		{stats.Evidence{Ties: 60}, false},
		{stats.Evidence{}, false},
	} {
		reading := screeningOut.Read(c.evidence)
		if reading.Decided != c.want {
			t.Errorf("%+v: decided = %v (verdict %s), want %v", c.evidence, reading.Decided, reading.Verdict, c.want)
		}
	}
}

// TestPromote is the acceptance rule over the two splits.
func TestPromote(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in, out vocab.Verdict
		want    bool
	}{
		{vocab.VerdictImprove, vocab.VerdictHold, true},
		{vocab.VerdictHold, vocab.VerdictImprove, true},
		{vocab.VerdictImprove, vocab.VerdictImprove, true},
		// Holding on both is no gain anywhere, which is not a promotion.
		{vocab.VerdictHold, vocab.VerdictHold, false},
		{vocab.VerdictImprove, vocab.VerdictRegress, false},
		{vocab.VerdictImprove, vocab.VerdictInconclusive, false},
		{vocab.VerdictRegress, vocab.VerdictImprove, false},
	} {
		if got := stats.Promote(c.in, c.out); got != c.want {
			t.Errorf("Promote(%s, %s) = %v, want %v", c.in, c.out, got, c.want)
		}
	}
}

// TestFalsePromotionRateUnderTheNull is the acceptance criterion for the whole
// procedure: a null edit — one whose pairs disagree as often as a coin — must
// be promoted no more often than α, however often the run peeks at the
// statistic and wherever it stops.
//
// The simulation is the run's own scheduler: both splits are run pair by pair
// to their cap, the reading is taken after every pair, a decided split stops,
// and the promotion rule is applied to the two verdicts. The generator is
// seeded, so the number this asserts is a property of the code rather than of
// the day it ran.
func TestFalsePromotionRateUnderTheNull(t *testing.T) {
	t.Parallel()
	const (
		runs        = 2000
		discordance = 0.2 // strong pairing, the case the caps were chosen for
		alpha       = 0.05
	)
	source := rand.New(rand.NewPCG(0x757a7573, 0x68696f35)) //nolint:gosec // a reproducible simulation, not a secret

	// nullSplit runs one split of a null edit and returns its verdict.
	nullSplit := func(params stats.Params) vocab.Verdict {
		var evidence stats.Evidence
		reading := params.Read(evidence)
		for range params.Cap {
			switch {
			case source.Float64() >= discordance:
				evidence.Observe(stats.Tie)
			case source.Float64() < 0.5:
				evidence.Observe(stats.EditWon)
			default:
				evidence.Observe(stats.BaselineWon)
			}
			reading = params.Read(evidence)
			if reading.Decided {
				break
			}
		}
		return reading.Verdict
	}

	promoted, regressed := 0, 0
	for range runs {
		in := nullSplit(screeningIn)
		if in == vocab.VerdictRegress {
			regressed++
			continue
		}
		// Held-in runs to a decision first: a candidate that cannot show a
		// gain there never deserves held-out budget.
		out := nullSplit(screeningOut)
		if out == vocab.VerdictRegress {
			regressed++
		}
		if stats.Promote(in, out) {
			promoted++
		}
	}
	rate := float64(promoted) / runs
	if rate > alpha {
		t.Errorf("false promotion rate on a null edit is %.4f over %d runs, want at most %.2f", rate, runs, alpha)
	}
	// The procedure is conservative: the realised rate should be well under
	// the nominal one, and a rate that has crept up to the boundary is a
	// change in the arithmetic worth looking at even though it still passes.
	if rate > alpha/2 {
		t.Logf("false promotion rate %.4f is above half of alpha; the procedure is meant to be conservative", rate)
	}
	t.Logf("null edit over %d runs: promoted %d (%.4f), a split called regress %d times", runs, promoted, rate, regressed)
}

// TestPowerAtATwentyPointEffect is the other half of the acceptance criterion,
// and the reason the CLI copy says `inconclusive` is the modal verdict: at the
// screening cap a 20-point effect is detectable and a 10-point one is not.
func TestPowerAtATwentyPointEffect(t *testing.T) {
	t.Parallel()
	const (
		runs        = 500
		discordance = 0.2
	)
	source := rand.New(rand.NewPCG(0x64656c74, 0x61323070)) //nolint:gosec // a reproducible simulation

	// At discordance d and true difference δ, a pair is a win with
	// probability (d+δ)/2 and a loss with probability (d−δ)/2.
	run := func(delta float64) vocab.Verdict {
		var evidence stats.Evidence
		reading := screeningOut.Read(evidence)
		for range screeningOut.Cap {
			draw := source.Float64()
			switch {
			case draw < (discordance+delta)/2:
				evidence.Observe(stats.EditWon)
			case draw < discordance:
				evidence.Observe(stats.BaselineWon)
			default:
				evidence.Observe(stats.Tie)
			}
			reading = screeningOut.Read(evidence)
			if reading.Decided {
				break
			}
		}
		return reading.Verdict
	}

	improved := 0
	for range runs {
		if run(0.20) == vocab.VerdictImprove {
			improved++
		}
	}
	power := float64(improved) / runs
	// The research puts the power of this procedure at δ = 0.20, d₀ = 0.2,
	// cap 60 at about 0.93 for the 1/α threshold; the gain arm here is held
	// to 2/α, so the bar is lower and deliberately loose. What the assertion
	// protects is that the gate can detect a large effect at all.
	if power < 0.5 {
		t.Errorf("power at a 20-point effect is %.3f over %d runs, want at least 0.5", power, runs)
	}
	t.Logf("20-point effect over %d runs: improve %d (%.3f)", runs, improved, power)
}

// TestRuleIsTheArithmetic checks that the literal string a run stores names
// every parameter the verdict was computed from. It is the one thing in the
// record a reader a year later has to be able to trust.
func TestRuleIsTheArithmetic(t *testing.T) {
	t.Parallel()
	rule := stats.Rule(screeningIn, screeningOut)
	for _, want := range []string{
		"alpha=0.05", "a=1",
		"m_in=0.15", "k_in=off", "cap_in=60",
		"m_out=0.15", "k_out=0", "cap_out=60",
		"E_gain_s >= 2/alpha", "E_harm_s >= 1/alpha", "lost_tasks_s",
		"promote if verdict_in and verdict_out are both in {improve, hold}",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("the decision rule does not name %q:\n%s", want, rule)
		}
	}
	// The four verdicts are all named, in the order they are checked.
	order := []string{"regress if", "improve if", "hold if", "else inconclusive"}
	at := 0
	for _, word := range order {
		i := strings.Index(rule[at:], word)
		if i < 0 {
			t.Fatalf("the decision rule does not name %q in order:\n%s", word, rule)
		}
		at += i
	}
}
