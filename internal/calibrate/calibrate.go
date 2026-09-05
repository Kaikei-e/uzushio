// Package calibrate re-centres a banded verifier's tolerances on the host that
// will run it.
//
// A band is a claim about a machine. A gate whose bands were calibrated
// somewhere else does not report that a host is different; it reports that
// every solution is wrong — and the kill rate it goes on to produce is that
// false positive read back, because a verifier which rejects the reference
// rejects the mutants with it. That is a thing `uzushio task doctor` can
// measure, and this is what is done about it.
//
// The input is doctor reports and nothing else. Every band row a banded
// verifier prints carries the value it measured and the band it was held to, so
// the reports already hold both halves of the question: what this host reads,
// and what it was expected to read. Nothing here opens the task's repository or
// knows the format the verifier keeps its bands in.
//
// The output is `bands.json`, which is uzushio's shape and not any gate's. What
// a particular verifier reads — a TOML table per invariant, a CSV, a flag — is
// a fact about somebody else's project, so turning this file into that one is
// the task's own adapter. That boundary is the point: the calibration stays
// generic, and exactly one file per task knows what the gate wants.
//
// Three refusals are the substance of it rather than details.
//
//   - A band the reference never left is copied, never widened. Calibration
//     repairs the bands that do not hold here; a rule that also loosened the ones
//     that do would trade a verifier that rejects everything for one that accepts
//     everything.
//   - A quantised invariant is never derived. A count of whole units that reads
//     as the same integer five times running has no measurable spread, and a
//     statistical band over it encodes the instrument's resolution as a tolerance.
//     Such a band comes from what the invariant means, and the way to catch a
//     regression in it is to make the regression bigger than one unit.
//   - An invariant named with --keep is never derived. Some bands are centred on
//     arithmetic rather than on a host — a ratio against a computed expectation —
//     and re-centring one on an observed shortfall writes the shortfall down as
//     the expectation. The tool cannot tell which those are; the task can.
package calibrate

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
)

// ErrCalibrate is the sentinel every refusal to produce a band wraps.
var ErrCalibrate = errors.New("calibrate")

// Band is one invariant's tolerance.
type Band struct {
	Lo float64
	Hi float64
}

// Contains reports whether a value is in the band, the way a gate's own verdict
// reads it: inclusive at both ends.
func (b Band) Contains(value float64) bool { return b.Lo <= value && value <= b.Hi }

// String renders the band the way a report line spells it.
func (b Band) String() string { return Number(b.Lo) + " – " + Number(b.Hi) }

// Centre names the statistic a band is centred on.
type Centre string

// The centres.
const (
	// CentreMedian is the middle value. One bad run out of five moves a mean
	// and does not move a median, which is the whole reason it is the default.
	CentreMedian Centre = "median"
	// CentreMean is the arithmetic mean.
	CentreMean Centre = "mean"
)

// Spread names the statistic a half-width is built from.
type Spread string

// The spreads.
const (
	// SpreadStdDev is the sample standard deviation, N−1.
	SpreadStdDev Spread = "stdev"
	// SpreadMAD is the median absolute deviation, scaled to a standard
	// deviation.
	SpreadMAD Spread = "mad"
)

// Lower names what the bottom of a band is allowed to be.
type Lower string

// The lower-bound policies.
const (
	// LowerDerive is centre − half-width, whatever that comes to. It is the
	// default, and the reason is that a band is two-sided on purpose.
	//
	// The invariants a performance gate judges are usually *differences* of two
	// measurements. If the thing being differenced stops running, the
	// difference collapses towards zero — and a band with no real lower bound
	// calls that a pass. On a host where the numbers are already unfamiliar
	// that is the failure mode least affordable, and a low reading is also the
	// clearest evidence that a band belongs to another machine.
	//
	// It costs no detection power: a regression moves a value up, and the lower
	// bound never fires on one.
	LowerDerive Lower = "derive"
	// LowerKeep reuses the original lo whenever the new centre is above it, and
	// derives otherwise. It is the most conservative reading of the original
	// band and the widest result: it never lowers a floor somebody wrote down,
	// and it never notices that the floor is now far below anything measurable.
	LowerKeep Lower = "keep"
	// LowerZero is LowerDerive clamped at zero for an invariant whose original
	// lo is not negative — a count, or a cost that cannot be negative.
	LowerZero Lower = "zero"
)

// Places is how many decimals a band edge carries. Four is the precision a
// banded verifier prints a measurement with, so a band is never written more
// precisely than the number it will be compared against.
const Places = 4

// DefaultMinRuns is the smallest calibration set this refuses to go below.
//
// Five, and it is a hard floor rather than a default. Below it the sample
// standard deviation is not an estimate of anything — its own 5th percentile is
// 0.42 of the true spread at N = 5 — and the tolerance factor that would have
// to compensate grows faster than the sample shrinks. Seven is better and ten
// is better still; the factor flattens after that, and between-session drift
// invalidates the spread before more runs pay for themselves.
const DefaultMinRuns = 5

// Rule is how a band is derived. Its zero value derives nothing; DefaultRule is
// the one this command uses.
type Rule struct {
	// Centre is the statistic the band is centred on.
	Centre Centre
	// Spread is the statistic the half-width is built from.
	Spread Spread
	// K multiplies the spread. Zero means the two-sided normal tolerance factor
	// k₂(N, 0.95, 0.90), which is the multiple that actually covers the
	// population from a sample this small.
	K float64
	// CIMultiplier multiplies the median of the verifier's own reported
	// half-ranges. Zero — the default — leaves it out of the half-width
	// entirely; see DefaultRule.
	CIMultiplier float64
	// RelFloor is a share of the centre the half-width may not fall below.
	//
	// It applies to every derived band, not only to the ones whose names carry
	// a unit. Without it an invariant that reads identically on every run — a
	// ratio, a count, anything whose name this cannot classify — derives a band
	// of zero width, which is a band that rejects the next clean run. See
	// DefaultRule.
	RelFloor float64
	// FloorUS and FloorMS are absolute floors under the half-width, by the unit
	// the invariant's name ends in. An invariant in neither unit — a count, a
	// ratio — has no absolute floor.
	FloorUS float64
	FloorMS float64
	// Lower is the lower-bound policy.
	Lower Lower
	// MinRuns is the smallest calibration set that produces a band at all.
	MinRuns int
	// Keep are invariants that are never derived, whatever the measurements
	// say. See the package comment.
	Keep []string
}

// DefaultRule is a two-sided normal tolerance interval about the median,
// floored per unit.
//
//	centre = median(v)
//	w      = max( k₂(N, 0.95, 0.90) · s , 0.02 · |centre| , floor[unit] )
//	band   = centre ∓ w
//
// The relative floor is what keeps a band from collapsing. A verifier that reads
// an invariant identically on all five runs has no spread to build a width from,
// and only two of the units an invariant can be measured in are recognisable
// from its name — so without a floor proportional to the centre, a ratio or a
// count that happens to be stable derives the band [x, x], which then rejects
// the next clean run. Two per cent is small enough not to hide a regression
// worth catching and large enough that no measurement lands outside it by
// chance.
//
// The width comes from the spread *between* whole verifications, because that
// is what a band has to cover. A banded verifier usually also reports a
// half-range of its own — the spread across the rounds *inside* one run, with
// the machine in one state — and gates commonly advise building a band from a
// multiple of it. That advice is written for somebody with two or three runs
// and no between-run spread available, and it is not followed here: a doctor
// report carries five or more whole runs, so the right quantity is measurable
// directly. Three times the reported half-range is computed and written into
// the output beside every derived band, so the deviation is checkable rather
// than asserted.
func DefaultRule() Rule {
	return Rule{
		Centre:       CentreMedian,
		Spread:       SpreadStdDev,
		K:            0,
		CIMultiplier: 0,
		RelFloor:     0.02,
		FloorUS:      0.1,
		FloorMS:      0.005,
		Lower:        LowerDerive,
		MinRuns:      DefaultMinRuns,
	}
}

// Keeps reports whether an invariant was named with --keep.
func (r Rule) Keeps(invariant string) bool {
	for _, name := range r.Keep {
		if name == invariant {
			return true
		}
	}
	return false
}

// Validate refuses a rule that would produce a band nobody can defend.
func (r Rule) Validate() error {
	switch r.Centre {
	case CentreMedian, CentreMean:
	default:
		return fmt.Errorf("%w: centre %q is not median or mean", ErrCalibrate, r.Centre)
	}
	switch r.Spread {
	case SpreadStdDev, SpreadMAD:
	default:
		return fmt.Errorf("%w: spread %q is not stdev or mad", ErrCalibrate, r.Spread)
	}
	switch r.Lower {
	case LowerKeep, LowerDerive, LowerZero:
	default:
		return fmt.Errorf("%w: lower %q is not keep, derive or zero", ErrCalibrate, r.Lower)
	}
	if r.K < 0 {
		return fmt.Errorf("%w: k is %v; it multiplies a spread and cannot be negative", ErrCalibrate, r.K)
	}
	if r.CIMultiplier < 0 || r.RelFloor < 0 || r.FloorUS < 0 || r.FloorMS < 0 {
		return fmt.Errorf("%w: a floor under a half-width cannot be negative", ErrCalibrate)
	}
	if r.MinRuns < 2 {
		return fmt.Errorf(
			"%w: --min-runs %d: a band needs a spread, and a spread needs at least 2 runs",
			ErrCalibrate, r.MinRuns)
	}
	return nil
}

// Describe is the rule in one line, for a person reading stderr. It takes N
// because the tolerance factor is a function of it, and a line saying "k = the
// tolerance factor" would leave a reader unable to check the arithmetic.
func (r Rule) Describe(n int) string {
	k := Number(r.K)
	if r.K == 0 {
		factor, err := ToleranceK(n)
		if err != nil {
			k = "k2(N,0.95,0.90)"
		} else {
			k = fmt.Sprintf("k2(N=%d,0.95,0.90)=%s", n, Number(factor))
		}
	}
	terms := []string{k + "*" + string(r.Spread)}
	if r.CIMultiplier > 0 {
		terms = append(terms, Number(r.CIMultiplier)+"*median(ci_half)")
	}
	if r.RelFloor > 0 {
		terms = append(terms, Number(r.RelFloor)+"*|centre|")
	}
	terms = append(terms, fmt.Sprintf("floor[us=%s, ms=%s]", Number(r.FloorUS), Number(r.FloorMS)))
	return fmt.Sprintf("%s +/- max(%s); lower=%s", r.Centre, strings.Join(terms, ", "), r.Lower)
}

// Why says why a band was not derived.
type Why string

// The reasons an original band is carried through instead of being re-centred.
const (
	// WhyDerived is a band this calibration produced.
	WhyDerived Why = ""
	// WhyInBand is the reference never leaving its original band. Calibration
	// is a repair, so there is nothing to repair.
	WhyInBand Why = "in band"
	// WhyDeclared is an invariant the caller named with --keep.
	WhyDeclared Why = "declared"
)

// Derived is one invariant's outcome.
type Derived struct {
	// Invariant is the band's name, as the verifier's own rows spell it.
	Invariant string
	// N is how many reference runs measured it.
	N int
	// Values are the measurements, in the order the runs happened.
	Values []float64
	// CIHalf are the half-ranges the verifier reported, for the runs that
	// reported one.
	CIHalf []float64
	// Original is the band the reference runs were judged against.
	Original Band
	// Band is what this calibration says it should be here. It is Original
	// exactly when Why is not WhyDerived.
	Band Band
	// Centre and HalfWidth are the arithmetic. HalfWidth is zero for a kept
	// band, which had none derived.
	Centre    float64
	HalfWidth float64
	// Binding names the term of the half-width that won, so a reader can see
	// whether a band's width came from the between-run spread or from a floor.
	Binding string
	// CIGuide is three times the largest half-range the verifier reported. It
	// is recorded for comparison and is not used.
	CIGuide float64
	// Why is empty for a derived band and says why for a kept one.
	Why Why
	// Lower names the policy that decided the bottom of a derived band.
	Lower Lower
}

// Copied reports whether the original band was carried through unchanged.
func (d Derived) Copied() bool { return d.Why != WhyDerived }

// Provenance is where a calibration's numbers came from.
type Provenance struct {
	// RunID is the doctor run.
	RunID string
	// Day is the day it started, as the vault spells a day.
	Day string
	// Labels are the reference runs it contributed.
	Labels []string
}

// Result is one calibration.
type Result struct {
	// Task and Rev are what was calibrated, from the reports.
	Task string
	Rev  string
	// N is the size of the calibration set.
	N int
	// Rule is what produced it.
	Rule Rule
	// Reports is where the runs came from, in the order they were given.
	Reports []Provenance
	// Derived is one entry per invariant the reference runs judged, sorted by
	// name.
	Derived []Derived
}

// Input is one calibration.
type Input struct {
	// Reports are the doctor reports to read reference runs out of.
	Reports []*doctor.Report
	// Rule is how a band is derived.
	Rule Rule
}

// observation is one invariant's measurements across the calibration set.
type observation struct {
	name   string
	values []float64
	ciHalf []float64
	bands  []Band
	runs   []string
}

// Calibrate reads the reference runs and produces the tolerances this host
// should hold the verifier to.
func Calibrate(in Input) (*Result, error) {
	if err := in.Rule.Validate(); err != nil {
		return nil, err
	}
	result := &Result{Rule: in.Rule}
	observed, err := collect(in.Reports, result)
	if err != nil {
		return nil, err
	}
	if result.N < in.Rule.MinRuns {
		return nil, fmt.Errorf(
			"%w: %d reference run(s) across %d report(s); --min-runs is %d. "+
				"A band from fewer runs is a band from a spread nobody measured",
			ErrCalibrate, result.N, len(in.Reports), in.Rule.MinRuns)
	}
	names := make([]string, 0, len(observed))
	for name := range observed {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf(
			"%w: the reference runs judged no invariant, so there is nothing to calibrate. "+
				"Only a verifier whose verify.kind is band reports the rows this reads",
			ErrCalibrate)
	}
	for _, name := range in.Rule.Keep {
		if observed[name] == nil {
			// A --keep for an invariant nobody measured is a typo, and a typo
			// here means the invariant it was meant to protect got re-centred.
			return nil, fmt.Errorf(
				"%w: --keep %s names an invariant the reference runs did not judge; they judged: %s",
				ErrCalibrate, name, strings.Join(names, ", "))
		}
	}
	for _, name := range names {
		derived, err := derive(observed[name], in.Rule)
		if err != nil {
			return nil, err
		}
		result.Derived = append(result.Derived, derived)
	}
	if err := selfCheck(result.Derived); err != nil {
		return nil, err
	}
	return result, nil
}

// collect reads every reference run's band rows, and fills in the provenance.
//
// A run that did not reach a verdict is refused rather than skipped: a
// calibration set is a set of measurements of the same thing, and quietly
// calibrating on four of five runs is how N stops meaning what it says.
func collect(reports []*doctor.Report, result *Result) (map[string]*observation, error) {
	if len(reports) == 0 {
		return nil, fmt.Errorf("%w: no report to calibrate from", ErrCalibrate)
	}
	observed := map[string]*observation{}
	var shape []string
	for _, report := range reports {
		day, err := report.Day()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCalibrate, err)
		}
		switch {
		case result.Task == "":
			result.Task, result.Rev = report.Task, report.Rev
		case result.Task != report.Task:
			return nil, fmt.Errorf("%w: report %s is about task %q and report %s about %q",
				ErrCalibrate, reports[0].RunID, result.Task, report.RunID, report.Task)
		case result.Rev != report.Rev:
			return nil, fmt.Errorf(
				"%w: report %s measured %s and report %s measured %s; "+
					"bands from two revisions are bands for two different programs",
				ErrCalibrate, reports[0].RunID, short(result.Rev), report.RunID, short(report.Rev))
		}
		provenance := Provenance{RunID: report.RunID, Day: day}
		for _, run := range report.Runs {
			if run.Kind != doctor.RunReference {
				continue
			}
			if run.Status != "pass" && run.Status != "fail" {
				return nil, fmt.Errorf(
					"%w: %s/%s came back %s, so it measured nothing; "+
						"a calibration set cannot include a run that did not answer",
					ErrCalibrate, report.RunID, run.Label, run.Status)
			}
			if run.Band == nil || len(run.Band.Rows) == 0 {
				return nil, fmt.Errorf("%w: %s/%s carries no band rows",
					ErrCalibrate, report.RunID, run.Label)
			}
			names, err := absorb(observed, run.Band.Rows, report.RunID+"/"+run.Label)
			if err != nil {
				return nil, err
			}
			if shape == nil {
				shape = names
			} else if !equal(shape, names) {
				// Two runs that judged different invariants are two runs of
				// different verifiers, and a per-invariant N would then differ
				// from the N in the file.
				return nil, fmt.Errorf(
					"%w: %s/%s judged %s; an earlier run judged %s",
					ErrCalibrate, report.RunID, run.Label,
					strings.Join(names, ", "), strings.Join(shape, ", "))
			}
			provenance.Labels = append(provenance.Labels, run.Label)
			result.N++
		}
		if len(provenance.Labels) == 0 {
			return nil, fmt.Errorf("%w: report %s holds no reference run", ErrCalibrate, report.RunID)
		}
		result.Reports = append(result.Reports, provenance)
	}
	return observed, nil
}

// absorb records one run's judged rows and returns the invariants it judged.
//
// `info` and `skipped` rows are left out. An info row is reported and never
// held to anything, so it has no band to re-centre; a skipped row is an
// invariant whose input never arrived, and its absent value is not a
// measurement of zero.
//
// The band on the row is where the original comes from, and it is the only
// place it could come from without this package knowing what the gate is. The
// file a verifier keeps its bands in is that project's, in that project's
// format, at whatever revision the task pins — while the row carries the same
// two numbers, already read by the thing that judged against them.
func absorb(
	observed map[string]*observation, rows []verifyrunner.BandRow, run string,
) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Verdict != "pass" && row.Verdict != "fail" {
			continue
		}
		if row.Value == nil {
			continue
		}
		if seen[row.Invariant] {
			// One run judging one invariant twice would give it a per-invariant
			// N larger than the number of runs, so the tolerance factor the band
			// was derived under would stop being the one the file records. It is
			// also a verifier reporting the same measurement twice, which is
			// worth naming rather than averaging away.
			return nil, fmt.Errorf(
				"%w: %s judged %s more than once; a run reports each invariant it judges once",
				ErrCalibrate, run, row.Invariant)
		}
		seen[row.Invariant] = true
		if row.BandLo == nil || row.BandHi == nil {
			return nil, fmt.Errorf(
				"%w: %s judged %s without reporting the band it judged against, "+
					"so there is no original band to compare against",
				ErrCalibrate, run, row.Invariant)
		}
		names = append(names, row.Invariant)
		entry := observed[row.Invariant]
		if entry == nil {
			entry = &observation{name: row.Invariant}
			observed[row.Invariant] = entry
		}
		band := Band{Lo: *row.BandLo, Hi: *row.BandHi}
		if len(entry.bands) > 0 && entry.bands[0] != band {
			// The runs were judged against two different bands, so neither "the
			// original band" nor "it already passes" is one thing. Somebody
			// changed the bands in the middle of the calibration set.
			return nil, fmt.Errorf(
				"%w: %s was judged against %s in %s and against %s in %s; "+
					"a calibration set has to have been judged against one band",
				ErrCalibrate, row.Invariant, entry.bands[0].String(), entry.runs[0],
				band.String(), run)
		}
		entry.values = append(entry.values, *row.Value)
		entry.bands = append(entry.bands, band)
		entry.runs = append(entry.runs, run)
		if row.CIHalf != nil {
			entry.ciHalf = append(entry.ciHalf, *row.CIHalf)
		}
	}
	sort.Strings(names)
	return names, nil
}

// derive decides one band.
func derive(observed *observation, rule Rule) (Derived, error) {
	original := observed.bands[0]
	out := Derived{
		Invariant: observed.name,
		N:         len(observed.values),
		Values:    observed.values,
		CIHalf:    observed.ciHalf,
		CIGuide:   CIGuide(observed.ciHalf),
		Original:  original,
		Band:      original,
		Centre:    centreOf(observed.values, rule),
	}
	switch {
	case rule.Keeps(observed.name):
		// Named by the caller. The tool cannot tell an arithmetic centre from a
		// measured one; the task can, and this is how it says so.
		out.Why = WhyDeclared
		return out, nil
	case inBand(observed.values, original):
		// Principle, not an optimisation: calibration repairs the bands that do
		// not hold on this host. A rule that also widened the ones that do
		// would trade a verifier that rejects everything for one that accepts
		// everything, and the invariants that passed every run are the part of
		// the gate that was still judging.
		out.Why = WhyInBand
		return out, nil
	case quantised(observed.values, original):
		return Derived{}, fmt.Errorf(
			"%w: %s reads in whole units against a band of whole units (%s), and it left it. "+
				"A statistical half-width over a quantised measurement is a statement about the "+
				"instrument's resolution rather than about this host: derive that band from what "+
				"the invariant means, and make the regression you want caught bigger than one "+
				"unit. Pass --keep %s once it is settled",
			ErrCalibrate, observed.name, original.String(), observed.name)
	}
	return widen(out, rule)
}

// centreOf is the statistic the band is centred on.
func centreOf(values []float64, rule Rule) float64 {
	if rule.Centre == CentreMean {
		return Mean(values)
	}
	return Median(values)
}

// widen is the arithmetic of one derived band.
func widen(out Derived, rule Rule) (Derived, error) {
	spread := StdDev(out.Values)
	if rule.Spread == SpreadMAD {
		spread = MAD(out.Values)
	}
	k := rule.K
	if k == 0 {
		factor, err := ToleranceK(len(out.Values))
		if err != nil {
			return Derived{}, fmt.Errorf("%s: %w", out.Invariant, err)
		}
		k = factor
	}
	width, binding := widest(
		term{"k*" + string(rule.Spread), k * spread},
		term{"ci_half", rule.CIMultiplier * Median(out.CIHalf)},
		term{"relative", rule.RelFloor * math.Abs(out.Centre)},
		term{"floor", unitFloor(out.Invariant, rule)},
	)
	if width == 0 {
		// Nothing to build a width from: no spread, no unit, and a centre of
		// zero so the relative floor has no scale either. A band of zero width
		// is not a tight band, it is a verifier that fails the next clean run.
		return Derived{}, fmt.Errorf(
			"%w: cannot derive a band for %s: the reference runs read it identically %d times, "+
				"its name carries no unit to floor it, and its centre is zero so there is no "+
				"scale to take a share of. Use --keep %s if its band is not about this host, "+
				"or --floor-us/--floor-ms/--rel-floor to say how wide it should be",
			ErrCalibrate, out.Invariant, len(out.Values), out.Invariant)
	}
	out.HalfWidth = width
	out.Binding = binding
	lo, policy := lowerBound(out.Centre, width, out.Original.Lo, rule.Lower)
	out.Lower = policy
	out.Band = Band{
		Lo: roundOut(lo, Places, false),
		Hi: roundOut(out.Centre+width, Places, true),
	}
	return out, nil
}

// lowerBound applies the lower-bound policy, and says which one decided it.
func lowerBound(centre, width, originalLo float64, policy Lower) (float64, Lower) {
	derived := centre - width
	switch policy {
	case LowerKeep:
		if centre > originalLo {
			return originalLo, LowerKeep
		}
		return derived, LowerDerive
	case LowerZero:
		if originalLo >= 0 {
			return math.Max(derived, 0), LowerZero
		}
		return derived, LowerDerive
	case LowerDerive:
	}
	return derived, LowerDerive
}

// term is one candidate half-width and what to call it.
type term struct {
	name  string
	value float64
}

// widest is the largest term and its name. Ties go to the earlier term, so the
// name reported is the one that would still be binding if the tie broke.
func widest(terms ...term) (float64, string) {
	best := terms[0]
	for _, candidate := range terms[1:] {
		if candidate.value > best.value {
			best = candidate
		}
	}
	return best.value, best.name
}

// unitFloor is the absolute floor for an invariant, read off the unit its name
// ends in. A count or a ratio names no unit and gets none.
func unitFloor(invariant string, rule Rule) float64 {
	switch {
	case strings.HasSuffix(invariant, "_us"):
		return rule.FloorUS
	case strings.HasSuffix(invariant, "_ms"):
		return rule.FloorMS
	}
	return 0
}

// inBand reports whether every measurement sat inside the original band.
func inBand(values []float64, band Band) bool {
	for _, value := range values {
		if !band.Contains(value) {
			return false
		}
	}
	return true
}

// quantised reports an invariant measured in whole units against a band of
// whole units — a count of buckets, a count of requests — where the gap between
// two adjacent readable values is the whole of the band's resolution.
func quantised(values []float64, band Band) bool {
	if band.Lo != math.Trunc(band.Lo) || band.Hi != math.Trunc(band.Hi) {
		return false
	}
	for _, value := range values {
		if value != math.Trunc(value) {
			return false
		}
	}
	return true
}

// selfCheck refuses a calibration that would reject the runs it was built from.
//
// It is a few lines and it is the only end-to-end test of the arithmetic that
// runs on real data: a band that excludes one of its own reference values is a
// mis-derivation whatever the intermediate numbers looked like.
func selfCheck(derived []Derived) error {
	for _, entry := range derived {
		if entry.Copied() {
			continue
		}
		for i, value := range entry.Values {
			if !entry.Band.Contains(value) {
				return fmt.Errorf(
					"%w: the band derived for %s, %s, excludes reference value %d of %d (%s) — "+
						"the calibration does not accept its own calibration set",
					ErrCalibrate, entry.Invariant, entry.Band.String(),
					i+1, len(entry.Values), Number(value))
			}
		}
	}
	return nil
}

// Number renders a band edge or a statistic for a person.
//
// Four decimals is the precision a banded verifier prints a measurement with,
// and trailing zeros are trimmed because a band is read by people.
func Number(value float64) string {
	if value == 0 {
		return "0.0"
	}
	text := strconv.FormatFloat(value, 'f', Places, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(text, "0")
		text = strings.TrimSuffix(text, ".")
	}
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

// equal reports whether two sorted name lists are the same.
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// short abbreviates a commit for a message.
func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
