// Package stats is the sequential test `uzushio run` decides an edit with.
//
// The unit of observation is the matched pair: one task, one repeat index, one
// seed, run once against the baseline harness and once against the edited one.
// A pair is +1 where only the edited arm passed, −1 where only the baseline
// did, and 0 where they agreed. Ties carry no information and are not thrown
// away — they are what makes the discordance rate readable, and the
// discordance rate is what turns a win rate among disagreements back into a
// pass-rate difference.
//
// The test is a beta-mixture e-process on the discordant pairs against the
// point null θ = ½, one per split and direction. An e-process satisfies
// Ville's inequality — P(∃n : E_n ≥ 1/α) ≤ α — so the caller may look after
// every single pair, stop whenever it likes and cap wherever it likes, and the
// type-I error is still at most α. There is no alpha spending, no boundary
// table and no correction for the number of looks, which is the whole reason
// this family was chosen over a group-sequential design: the correction is the
// part that would have needed numerical integration and a dependency.
//
// Two properties are worth stating because they are easy to forget when
// reading a verdict:
//
//   - The procedure is conservative. At a nominal α = 0.05 the realised error
//     is nearer 1–2%, which is the right trade for a promotion gate and the
//     wrong one for a power calculation.
//   - The e-value is a statement about *this task set*. The unit of
//     independence for a claim about tasks in general is the task, not the
//     trial, and a suite of twenty tasks is twenty of those however many
//     repeats are run. `inconclusive` is the modal verdict at this budget and
//     that is the design working, not a bug.
package stats

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// DefaultPrior is the a of the Beta(a, a) mixture. One — a uniform prior over
// the win rate — is the safe default: the power difference against Beta(½, ½)
// and Beta(2, 2) is a few percent, and a uniform prior is the one an eval
// reader can check by hand.
const DefaultPrior = 1.0

// The pair outcomes.
const (
	// EditWon is a pair where only the edited arm passed.
	EditWon = 1
	// Tie is a pair where the two arms agreed.
	Tie = 0
	// BaselineWon is a pair where only the baseline arm passed.
	BaselineWon = -1
)

// CountGateOff turns a split's count gate off. It is a real setting rather
// than an absent one: the gate is a raw count of tasks, it is the check a human
// reviewer actually wants, and a split running without it should say so in the
// record rather than leave a reader to infer it.
const CountGateOff = -1

// logBeta is log B(x, y).
func logBeta(x, y float64) float64 {
	lx, _ := math.Lgamma(x)
	ly, _ := math.Lgamma(y)
	lxy, _ := math.Lgamma(x + y)
	return lx + ly - lxy
}

// LogE returns the natural logarithm of the beta-mixture e-value against the
// point null θ = ½, over b pairs the edit won and c pairs the baseline won:
//
//	log E = [ log B(a+b, a+c) − log B(a, a) ] + (b + c)·log 2
//
// It is symmetric in b and c — the null is "the edit and the baseline win
// equally often among the pairs they disagree on", and the evidence against it
// is the same whichever way the disagreement leans. Direction is read off
// sign(b − c) by the caller; see Reading.
func LogE(prior float64, b, c int) float64 {
	return LogEAt(prior, b, c, 0.5)
}

// LogEAt returns the logarithm of the e-value against a point null θ, which is
// what inverting the test into a confidence sequence needs:
//
//	log E(θ) = [ log B(a+b, a+c) − log B(a, a) ] − [ b·log θ + c·log(1−θ) ]
//
// At θ = ½ the bracket is (b+c)·log 2 and this is LogE.
func LogEAt(prior float64, b, c int, theta float64) float64 {
	if theta <= 0 || theta >= 1 {
		return math.Inf(1)
	}
	mixture := logBeta(prior+float64(b), prior+float64(c)) - logBeta(prior, prior)
	null := float64(b)*math.Log(theta) + float64(c)*math.Log(1-theta)
	return mixture - null
}

// LogValue is a natural logarithm that may be negative infinity, which is how
// "the evidence does not point this way at all" is spelled in the arithmetic.
//
// JSON has no infinity, and the two usual workarounds are both worse than a
// null: a sentinel like -1e308 reads as a number somebody computed, and a
// string makes the field two types. `null` says what is true — there is no
// value — and reads back as the infinity it came from.
type LogValue float64

// MarshalJSON writes a finite logarithm as a number and an infinite or
// undefined one as null.
func (v LogValue) MarshalJSON() ([]byte, error) {
	f := float64(v)
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return []byte("null"), nil
	}
	return json.Marshal(f)
}

// UnmarshalJSON reads null back as negative infinity.
func (v *LogValue) UnmarshalJSON(body []byte) error {
	if string(body) == "null" {
		*v = LogValue(math.Inf(-1))
		return nil
	}
	var f float64
	if err := json.Unmarshal(body, &f); err != nil {
		return err
	}
	*v = LogValue(f)
	return nil
}

// Float returns the value as an ordinary float.
func (v LogValue) Float() float64 { return float64(v) }

// Interval is an anytime-valid confidence sequence: an interval that covers
// the parameter at every stopping time simultaneously with probability at
// least 1 − α, rather than at one pre-chosen sample size.
type Interval struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// ThetaCS returns the confidence sequence on θ, the edit's win rate among
// discordant pairs, obtained by inverting the e-process: the set of θ the test
// has not rejected, { θ : log E(θ) < log(1/α) }.
//
// log E(θ) is convex in θ with its minimum at b/(b+c), so the set is an
// interval and bisection on each side finds it. With no discordant pairs the
// test has seen nothing and the interval is the whole of [0, 1].
func ThetaCS(prior float64, b, c int, alpha float64) Interval {
	discordant := b + c
	if discordant == 0 {
		return Interval{Lo: 0, Hi: 1}
	}
	threshold := math.Log(1 / alpha)
	rejected := func(theta float64) bool { return LogEAt(prior, b, c, theta) >= threshold }

	// log E(θ) is convex with its minimum at the observed win rate, so the
	// bisection starts from there and walks outwards. The starting point has
	// to be *inside* the open interval: where every discordant pair went the
	// same way the observed rate is 0 or 1, and log E is infinite at both
	// ends, so the minimum is approached rather than attained. Splitting on
	// the endpoint itself would read as "every θ is rejected" and collapse the
	// interval onto a point — which is the difference between "the edit won
	// the one pair that disagreed" and "the edit is certainly better".
	const inside = 1e-12
	hat := min(max(float64(b)/float64(discordant), inside), 1-inside)

	if rejected(hat) {
		// Every θ is rejected, which the arithmetic does not allow. Answering
		// with the point rather than with an inverted interval keeps a caller
		// that hits a numerical edge from reading Lo > Hi.
		return Interval{Lo: hat, Hi: hat}
	}
	// bisect narrows towards the boundary keeping the predicate true on the
	// left. Below θ̂ the test rejects and stops rejecting; above it, the
	// other way round.
	return Interval{
		Lo: bisect(rejected, 0, hat),
		Hi: bisect(func(theta float64) bool { return !rejected(theta) }, hat, 1),
	}
}

// bisect returns the boundary between a prefix of [from, to] where predicate
// holds and the suffix where it does not, to within a part in 2^60. The
// endpoints are approached rather than reached: log E is infinite at 0 and 1,
// so the predicate is evaluated strictly inside.
func bisect(predicate func(float64) bool, from, to float64) float64 {
	const iterations = 60
	lo, hi := from, to
	for range iterations {
		mid := (lo + hi) / 2
		if predicate(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// DeltaCS returns the anytime-valid confidence sequence on δ, the difference
// in pass rate between the edited and the baseline arm.
//
// δ = d·(2θ − 1), where θ is the edit's win rate among discordant pairs and d
// is the rate at which pairs disagree at all. Only θ is what the sign test
// measures; d is a second unknown, and plugging the *observed* discordance in
// for it is not a level-α statement about δ — it also makes δ_lo unable to
// fall below −d̂, so an edit that lost every discordant pair it had comes back
// certified as no worse than the baseline whenever d̂ happens to be small.
//
// So both are bounded, each at α/2, and δ is bounded over the product. A
// Bonferroni split of α across two anytime-valid confidence sequences is
// itself anytime-valid, and δ is monotone in θ for a fixed d, so the extremes
// of the product are attained at the corners. The discordance is bounded by
// the same beta-mixture e-process, reading "the pair disagreed" where the θ
// process reads "the edit won".
//
// The interval this returns is materially wider than the plug-in one — at
// sixty pairs and a discordance of 0.2 the half-width is about 0.30 rather
// than 0.14 — which is the cost of the claim being true. See the note on
// Params.Margin.
func DeltaCS(prior float64, e Evidence, alpha float64) Interval {
	pairs := e.Pairs()
	if pairs == 0 {
		return Interval{Lo: -1, Hi: 1}
	}
	theta := ThetaCS(prior, e.Wins, e.Losses, alpha/2)
	discordance := ThetaCS(prior, e.Discordant(), e.Ties, alpha/2)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, d := range []float64{discordance.Lo, discordance.Hi} {
		for _, t := range []float64{theta.Lo, theta.Hi} {
			lo = math.Min(lo, d*(2*t-1))
			hi = math.Max(hi, d*(2*t-1))
		}
	}
	return Interval{Lo: lo, Hi: hi}
}

// Evidence is one split's tally: the pair outcomes so far, and the count of
// tasks whose repeats net out worse under the edit than under the baseline.
//
// The task count is carried rather than derived because it is a different
// statistic over the same trials — the e-process reads pairs, the count gate
// reads tasks — and a caller that has the trials has both.
type Evidence struct {
	// Wins is b: pairs only the edited arm passed.
	Wins int `json:"wins"`
	// Losses is c: pairs only the baseline arm passed.
	Losses int `json:"losses"`
	// Ties is pairs the two arms agreed on, in either direction.
	Ties int `json:"ties"`
	// LostTasks is how many tasks the edit lost outright: every one of the
	// task's scheduled repeats went to the baseline, and all of them are in.
	//
	// It is deliberately not "tasks that came out net worse". A task that lost
	// one repeat of three is noise — at a discordance of 0.2 a twelve-task
	// split turns up two of them about four times in five under an edit that
	// changes nothing — and counting those makes the gate the dominant verdict
	// rather than a catastrophe detector. Losing every repeat is the thing a
	// reviewer is actually asking about, and it is what the gate counts.
	//
	// It is also counted only over tasks whose repeats are all complete. A
	// task with one pair in and two to come has lost "every repeat so far",
	// which is not the same claim.
	LostTasks int `json:"lost_tasks"`
}

// Observe folds one pair outcome in. An outcome outside {−1, 0, +1} is a
// programmer error and is counted as a tie rather than panicking in the middle
// of a run that has already cost an hour.
func (e *Evidence) Observe(outcome int) {
	switch {
	case outcome > 0:
		e.Wins++
	case outcome < 0:
		e.Losses++
	default:
		e.Ties++
	}
}

// Pairs is how many pairs the split has completed.
func (e Evidence) Pairs() int { return e.Wins + e.Losses + e.Ties }

// Discordant is how many of them disagreed, which is what the test reads.
func (e Evidence) Discordant() int { return e.Wins + e.Losses }

// Params are the gate's numbers: what error rate it is run at, what margin it
// is willing to certify non-inferiority to, how many task-level regressions it
// tolerates, and where it stops.
type Params struct {
	// Alpha is the overall error rate, 0.05.
	Alpha float64 `json:"alpha"`
	// Margin is m, the non-inferiority margin: how much worse than the
	// baseline the edit may be and still be called `hold`.
	//
	// The published values are 0.15 for screening and 0.10 for confirmation.
	// They were chosen against a plug-in interval that treated the observed
	// discordance as known; under the joint bound this package actually
	// computes, **neither is reachable at its cap** — sixty pairs at a
	// discordance of 0.2 certify about 0.30, and a hundred and fifty certify
	// about 0.18. The margins and the caps are not this package's to change,
	// so the consequence is left where it belongs: `hold` requires the
	// certified bound, so a run that cannot certify the published margin ends
	// `inconclusive`, which is the honest answer rather than a quiet one.
	Margin float64 `json:"margin"`
	// CountGate is k, the number of outright task losses the split tolerates.
	// Zero — the default on the split that carries the generalisation claim —
	// means any task the edit lost on every one of its repeats is a
	// regression. CountGateOff turns it off.
	CountGate int `json:"count_gate"`
	// Cap is how many pairs the split runs before it stops undecided.
	Cap int `json:"cap"`
	// Prior is the a of the Beta(a, a) mixture.
	Prior float64 `json:"prior"`
}

// GainThreshold is the e-value a split's superiority arm has to reach: 2/α
// rather than 1/α.
//
// The acceptance rule asks for a gain on *at least one* of the two splits,
// which is a union of rejection regions, and a union inflates the error to
// about 2α. Splitting α across the two splits fixes it. The non-inferiority
// and harm arms need no such correction: they are asked of *both* splits, and
// an intersection–union test takes the level of its individual tests
// (Berger 1982).
func (p Params) GainThreshold() float64 { return 2 / p.Alpha }

// HarmThreshold is the e-value a split's harm arm has to reach: 1/α.
func (p Params) HarmThreshold() float64 { return 1 / p.Alpha }

// Reading is what the test says about one split at one moment: the tally, the
// two directional e-values, the confidence sequence on the pass-rate
// difference, the four booleans the verdict is assembled from, and the verdict.
type Reading struct {
	Evidence Evidence `json:"evidence"`
	// Pairs, and the discordance rate among them. d̂ is the parameter every
	// sample-size calculation needs and the floor on any detectable effect,
	// which is why it is published rather than left inside the arithmetic.
	Pairs       int     `json:"pairs"`
	Discordant  int     `json:"discordant"`
	Discordance float64 `json:"discordance"`
	// LogE is the two-sided evidence against θ = ½; LogEGain and LogEHarm are
	// it, directed by sign(b − c), and negative infinity in the direction the
	// evidence does not point.
	LogE     LogValue `json:"log_e"`
	LogEGain LogValue `json:"log_e_gain"`
	LogEHarm LogValue `json:"log_e_harm"`
	// Theta is the confidence sequence on the win rate among discordant
	// pairs at level α; Delta is the joint bound on the pass-rate difference,
	// which bounds the discordance too rather than plugging the observed one
	// in. See DeltaCS. Theta is published beside it because it is the
	// quantity the test is actually about.
	Theta Interval `json:"theta"`
	Delta Interval `json:"delta"`
	// DeltaHat is the observed pass-rate difference, (b − c)/pairs.
	DeltaHat float64 `json:"delta_hat"`
	// The four booleans of the verdict rule.
	Gain        bool `json:"gain"`
	Harm        bool `json:"harm"`
	NonInferior bool `json:"non_inferior"`
	CountOK     bool `json:"count_ok"`
	// Decided says the split has reached a verdict it cannot lose by running
	// longer, so the scheduler may stop it. A split at its cap is finished but
	// not decided.
	Decided bool          `json:"decided"`
	Verdict vocab.Verdict `json:"verdict"`
}

// Read applies the gate to one split's evidence.
//
// The order of the checks is the point of the rule: harm is read before gain,
// so an edit that is both cannot be reported as a hold, and the count gate is
// read beside harm rather than after it, because small-sample statistics do
// not catch a single catastrophic task regression and a count is what a
// reviewer is actually asking about.
func (p Params) Read(e Evidence) Reading {
	prior := p.Prior
	if prior <= 0 {
		prior = DefaultPrior
	}
	r := Reading{
		Evidence:   e,
		Pairs:      e.Pairs(),
		Discordant: e.Discordant(),
		LogE:       LogValue(LogE(prior, e.Wins, e.Losses)),
		LogEGain:   LogValue(math.Inf(-1)),
		LogEHarm:   LogValue(math.Inf(-1)),
		Theta:      ThetaCS(prior, e.Wins, e.Losses, p.Alpha),
	}
	if r.Pairs > 0 {
		r.Discordance = float64(r.Discordant) / float64(r.Pairs)
		r.DeltaHat = float64(e.Wins-e.Losses) / float64(r.Pairs)
	}
	switch {
	case e.Wins > e.Losses:
		r.LogEGain = r.LogE
	case e.Losses > e.Wins:
		r.LogEHarm = r.LogE
	}
	if r.Pairs == 0 {
		r.Delta = Interval{Lo: -1, Hi: 1}
	} else {
		r.Delta = DeltaCS(prior, e, p.Alpha)
	}

	r.Gain = e.Wins > e.Losses && r.LogEGain.Float() >= math.Log(p.GainThreshold())
	r.Harm = e.Losses > e.Wins && r.LogEHarm.Float() >= math.Log(p.HarmThreshold())
	r.NonInferior = r.Pairs > 0 && r.Delta.Lo >= -p.Margin
	r.CountOK = p.CountGate == CountGateOff || e.LostTasks <= p.CountGate

	switch {
	case r.Harm || !r.CountOK:
		r.Verdict = vocab.VerdictRegress
	case r.NonInferior && r.Gain:
		r.Verdict = vocab.VerdictImprove
	case r.NonInferior:
		r.Verdict = vocab.VerdictHold
	default:
		r.Verdict = vocab.VerdictInconclusive
	}
	// A regress and an improve are settled: more pairs cannot take back a
	// crossed e-threshold, and a count gate that has been breached does not
	// unbreach. A hold is not settled — the interval can still widen into a
	// gain, and running the remaining budget is what turns "no evidence of
	// regression" into "evidence of improvement" — so the run keeps going.
	r.Decided = r.Verdict == vocab.VerdictRegress || r.Verdict == vocab.VerdictImprove
	return r
}

// Promote reports whether the pair of split verdicts clears the acceptance
// rule: neither split regressed or came back undecided, and at least one
// showed a gain. It is Self-Harness's Δin ≥ 0 ∧ Δho ≥ 0 ∧ max(Δin, Δho) > 0
// with "≥ 0" softened to "no worse than the margin" — which is what the raw
// rule silently means once the outcomes are noisy — and "> 0" hardened to
// "demonstrably > 0".
func Promote(in, out vocab.Verdict) bool {
	ok := func(v vocab.Verdict) bool {
		return v == vocab.VerdictImprove || v == vocab.VerdictHold
	}
	return ok(in) && ok(out) && (in == vocab.VerdictImprove || out == vocab.VerdictImprove)
}

// Rule renders the decision rule as the literal string a run record stores.
//
// It is generated from the parameters rather than written out beside them, so
// the sentence in the record cannot drift from the arithmetic that produced
// the verdict — the record is the thing a reader will trust in a year, and a
// rule that describes a different gate from the one that ran is worse than no
// rule at all.
func Rule(in, out Params) string {
	var b strings.Builder
	b.WriteString("per split s: ")
	b.WriteString("regress if E_harm_s >= 1/alpha or lost_tasks_s > k_s; ")
	b.WriteString("improve if delta_lo_s >= -m_s and lost_tasks_s <= k_s and E_gain_s >= 2/alpha; ")
	b.WriteString("hold if delta_lo_s >= -m_s and lost_tasks_s <= k_s; ")
	b.WriteString("else inconclusive. ")
	b.WriteString("lost_tasks_s = tasks of split s whose scheduled repeats are all complete ")
	b.WriteString("and every one of whose pairs the baseline won. ")
	b.WriteString("E_gain_s = E_s where b_s > c_s else 0, E_harm_s = E_s where c_s > b_s else 0, ")
	b.WriteString("log E_s = logB(a+b_s, a+c_s) - logB(a, a) + (b_s+c_s)*log 2. ")
	b.WriteString("CS(x, y, level) = {theta: logB(a+x, a+y) - logB(a, a) ")
	b.WriteString("- x*log theta - y*log(1-theta) < log(1/level)}; ")
	b.WriteString("[delta_lo_s, delta_hi_s] = the range of d*(2*theta - 1) over ")
	b.WriteString("d in CS(b_s+c_s, ties_s, alpha/2) and theta in CS(b_s, c_s, alpha/2). ")
	b.WriteString("promote if verdict_in and verdict_out are both in {improve, hold} and at least one is improve. ")
	fmt.Fprintf(&b, "alpha=%s, a=%s", number(in.Alpha), number(in.Prior))
	for _, s := range []struct {
		name string
		p    Params
	}{{"in", in}, {"out", out}} {
		fmt.Fprintf(&b, ", m_%s=%s, k_%s=%s, cap_%s=%d",
			s.name, number(s.p.Margin), s.name, gate(s.p.CountGate), s.name, s.p.Cap)
	}
	b.WriteString(".")
	return b.String()
}

// number formats a parameter the way the rule string writes it: shortest
// decimal that reads back to the same float, so 0.05 is "0.05" rather than
// "0.050000".
func number(v float64) string { return fmt.Sprintf("%g", v) }

// gate formats a count gate, naming the off state rather than printing −1.
func gate(k int) string {
	if k == CountGateOff {
		return "off"
	}
	return fmt.Sprintf("%d", k)
}
