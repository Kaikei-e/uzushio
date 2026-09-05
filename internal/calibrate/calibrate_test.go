package calibrate

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
)

// A reference set with the shape a banded verifier produces: a continuous
// invariant that left its band, one that never did, a quantised one, and a
// ratio whose centre is arithmetic. The numbers are five whole verifications of
// one unmodified tree.
var (
	floorValues  = []float64{8.7119, 8.7684, 8.6935, 8.7986, 8.8488}
	floorCIHalf  = []float64{0.2653, 0.2718, 0.1995, 0.2008, 0.1550}
	tailValues   = []float64{0.0257, 0.0322, 0.0311, 0.0248, 0.0240}
	steadyValues = []float64{0.0098, 0.0092, 0.0107, 0.0110, 0.0108}
	ratioValues  = []float64{0.9168, 0.9166, 0.9166, 0.9165, 0.9166}
)

const tolerance = 1e-4

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s is %v, want %v", what, got, want)
	}
}

func ptr(value float64) *float64 { return &value }

// row builds one band row.
func row(name string, value, ciHalf *float64, lo, hi float64, verdict string) verifyrunner.BandRow {
	return verifyrunner.BandRow{
		Invariant: name, Value: value, CIHalf: ciHalf,
		BandLo: &lo, BandHi: &hi, Verdict: verdict,
	}
}

// referenceRows is what one reference run reported.
func referenceRows(i int) []verifyrunner.BandRow {
	return []verifyrunner.BandRow{
		// out of its band on every run: this is what gets re-centred
		row("dispatch_floor_us", ptr(floorValues[i]), ptr(floorCIHalf[i]), 2.0, 4.6, "fail"),
		row("pooled_tail_p50_ms", ptr(tailValues[i]), nil, 0.04, 0.20, "fail"),
		// inside its band on every run: kept
		row("steady_tail_p50_ms", ptr(steadyValues[i]), nil, -0.02, 0.15, "pass"),
		// a ratio against a computed expectation: kept only if declared
		row("enforce_allowed_ratio", ptr(ratioValues[i]), nil, 0.90, 1.10, "pass"),
		// whole units against a band of whole units
		row("rr_spread_req", ptr(0), nil, 0, 0, "pass"),
		// neither of these is a measurement, and neither may reach a band
		{Invariant: "tail_p99_ms", Value: ptr(0.0575), Verdict: "info"},
		{Invariant: "unmeasured_us", BandLo: ptr(0.0), BandHi: ptr(1.0), Verdict: "skipped"},
	}
}

// referenceReport builds a report of n reference runs.
func referenceReport(runID string, rows func(int) []verifyrunner.BandRow, n int) *doctor.Report {
	report := &doctor.Report{
		SchemaVersion: doctor.SchemaVersion,
		RunID:         runID,
		Task:          "banded",
		Rev:           "39778ec3f02f1db8883a11a4864f9b43f2dc3fac",
		StartedAt:     "2026-09-05T00:23:50Z",
	}
	for i := range n {
		report.Runs = append(report.Runs, doctor.Run{
			Label:  "reference-" + string(rune('1'+i)),
			Kind:   doctor.RunReference,
			Status: verifyrunner.StatusFail,
			Band:   &verifyrunner.Band{Rows: rows(i)},
		})
	}
	return report
}

func calibrateSet(t *testing.T, rule Rule) *Result {
	t.Helper()
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("20260905T002350Z-e13205e7", referenceRows, 5)},
		Rule:    rule,
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	return result
}

func find(t *testing.T, result *Result, name string) Derived {
	t.Helper()
	for _, entry := range result.Derived {
		if entry.Invariant == name {
			return entry
		}
	}
	t.Fatalf("no band for %s", name)
	return Derived{}
}

func TestMedian(t *testing.T) {
	near(t, "an odd count", Median([]float64{3, 1, 2}), 2)
	near(t, "an even count", Median([]float64{4, 1, 3, 2}), 2.5)
	near(t, "one value", Median([]float64{7}), 7)
	near(t, "no values", Median(nil), 0)
	near(t, "the reference set", Median(floorValues), 8.7684)
	// Median must not reorder its input: the values are reported in run order.
	values := []float64{3, 1, 2}
	Median(values)
	if values[0] != 3 {
		t.Errorf("Median sorted its argument in place: %v", values)
	}
}

func TestMeanAndStdDev(t *testing.T) {
	near(t, "the mean", Mean(floorValues), 8.76424)
	near(t, "the sample standard deviation", StdDev(floorValues), 0.0634276)
	near(t, "the sample standard deviation", StdDev(tailValues), 0.0038016)
	near(t, "one value has no spread", StdDev([]float64{5}), 0)
	near(t, "no values have no spread", StdDev(nil), 0)
}

func TestMAD(t *testing.T) {
	near(t, "no spread", MAD([]float64{5}), 0)
	// Deviations about the median are 0.0565 0 0.0749 0.0302 0.0804; their
	// median is 0.0565, scaled by 1.4826.
	near(t, "the scaled MAD", MAD(floorValues), 0.0565*MADScale)
}

// TestToleranceK checks the table against the published two-sided normal
// tolerance factors for 95 % coverage at 90 % confidence.
func TestToleranceK(t *testing.T) {
	published := map[int]float64{5: 4.164, 6: 3.730, 7: 3.464, 8: 3.268, 10: 3.021}
	for n, want := range published {
		got, err := ToleranceK(n)
		if err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
		if math.Abs(got-want) > 0.001 {
			t.Errorf("k2(N=%d) is %v, want %v", n, got, want)
		}
	}
	// The interpolated entries sit between their published neighbours, because
	// the factor falls monotonically in N.
	previous := math.Inf(1)
	for n := ToleranceMinN; n <= ToleranceMaxN; n++ {
		got, err := ToleranceK(n)
		if err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
		if got >= previous {
			t.Errorf("k2(N=%d)=%v did not fall below k2(N=%d)=%v", n, got, n-1, previous)
		}
		previous = got
	}
	for _, n := range []int{0, 1, 4, ToleranceMaxN + 1} {
		if _, err := ToleranceK(n); err == nil {
			t.Errorf("N=%d produced a tolerance factor; it is outside the table", n)
		}
	}
}

func TestCIGuide(t *testing.T) {
	near(t, "3× the largest half-range", CIGuide(floorCIHalf), 3*0.2718)
	near(t, "no half-ranges", CIGuide(nil), 0)
}

func TestRoundOutNeverNarrows(t *testing.T) {
	near(t, "a lower edge rounds down", roundOut(8.50432, 4, false), 8.5043)
	near(t, "an upper edge rounds up", roundOut(9.03248, 4, true), 9.0325)
	near(t, "a negative lower edge rounds down", roundOut(-0.00791, 4, false), -0.008)
}

func TestNumber(t *testing.T) {
	cases := map[float64]string{
		0: "0.0", 2: "2.0", 4.6: "4.6", 8.5043: "8.5043", -0.02: "-0.02", 0.0257: "0.0257",
	}
	for value, want := range cases {
		if got := Number(value); got != want {
			t.Errorf("Number(%v) is %q, want %q", value, got, want)
		}
	}
}

// TestCalibrateTakesTheOriginalBandFromTheRows is the whole reason this needs
// nothing but reports: the row carries the band it was judged against.
func TestCalibrateTakesTheOriginalBandFromTheRows(t *testing.T) {
	result := calibrateSet(t, keepRatio())
	if got := find(t, result, "dispatch_floor_us").Original; got != (Band{Lo: 2.0, Hi: 4.6}) {
		t.Errorf("the original band is %s, want 2.0 – 4.6", got.String())
	}
	if got := find(t, result, "steady_tail_p50_ms").Original; got != (Band{Lo: -0.02, Hi: 0.15}) {
		t.Errorf("the original band is %s, want -0.02 – 0.15", got.String())
	}
}

// keepRatio is the rule with the arithmetic-centred invariant declared, which
// is how a task says what the tool cannot know.
func keepRatio() Rule {
	rule := DefaultRule()
	rule.Keep = []string{"enforce_allowed_ratio"}
	return rule
}

// TestCalibrateDerivesTheBandsThatLeft is the arithmetic of the default rule.
func TestCalibrateDerivesTheBandsThatLeft(t *testing.T) {
	result := calibrateSet(t, keepRatio())
	k, err := ToleranceK(5)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		values []float64
		ciHalf []float64
	}{
		{"dispatch_floor_us", floorValues, floorCIHalf},
		{"pooled_tail_p50_ms", tailValues, nil},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			entry := find(t, result, testCase.name)
			if entry.Copied() {
				t.Fatalf("%s was kept (%s); it left its band", testCase.name, entry.Why)
			}
			centre, width := Median(testCase.values), k*StdDev(testCase.values)
			near(t, "the centre", entry.Centre, centre)
			near(t, "the half-width", entry.HalfWidth, width)
			near(t, "the lower edge", entry.Band.Lo, roundOut(centre-width, Places, false))
			near(t, "the upper edge", entry.Band.Hi, roundOut(centre+width, Places, true))
			near(t, "the reported guide", entry.CIGuide, CIGuide(testCase.ciHalf))
			if entry.Binding != "k*stdev" {
				t.Errorf("the binding term is %q, want k*stdev", entry.Binding)
			}
			// The guide is reported and not used.
			if entry.CIGuide > 0 && entry.HalfWidth == entry.CIGuide {
				t.Error("the half-width equals 3× max(ci_half); the guide is not supposed to bind")
			}
		})
	}
}

// TestCalibrateKeepsABandTheReferenceHeld: calibration repairs what does not
// hold and never widens what does.
func TestCalibrateKeepsABandTheReferenceHeld(t *testing.T) {
	entry := find(t, calibrateSet(t, keepRatio()), "steady_tail_p50_ms")
	if !entry.Copied() || entry.Why != WhyInBand {
		t.Errorf("steady_tail_p50_ms came out %s (%s), want kept in band",
			entry.Band.String(), entry.Why)
	}
	if entry.Band != entry.Original {
		t.Errorf("the kept band is %s, want the original %s",
			entry.Band.String(), entry.Original.String())
	}
	if entry.Reason() != "kept: in band" {
		t.Errorf("the reason is %q", entry.Reason())
	}
	// A kept band still reports the centre: what the invariant reads here is
	// worth knowing even where the band was not moved.
	near(t, "the centre", entry.Centre, Median(steadyValues))
}

// TestCalibrateKeepsWhatIsDeclared is --keep: the tool cannot tell an
// arithmetic centre from a measured one, and the task can.
func TestCalibrateKeepsWhatIsDeclared(t *testing.T) {
	entry := find(t, calibrateSet(t, keepRatio()), "enforce_allowed_ratio")
	if entry.Why != WhyDeclared || entry.Band != entry.Original {
		t.Errorf("enforce_allowed_ratio came out %s (%s)", entry.Band.String(), entry.Why)
	}
	if entry.Reason() != "kept: declared" {
		t.Errorf("the reason is %q, want \"kept: declared\"", entry.Reason())
	}
}

// TestKeepHoldsEvenWhenTheBandIsLeft is the case --keep exists for: an
// arithmetic centre that the measurements have drifted away from is a
// shortfall to explain, not a band to move.
func TestKeepHoldsEvenWhenTheBandIsLeft(t *testing.T) {
	rows := func(i int) []verifyrunner.BandRow {
		out := referenceRows(i)
		for j := range out {
			if out[j].Invariant == "enforce_allowed_ratio" {
				out[j] = row("enforce_allowed_ratio",
					ptr(0.80+float64(i)*0.001), nil, 0.90, 1.10, "fail")
			}
		}
		return out
	}
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", rows, 5)},
		Rule:    keepRatio(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	entry := find(t, result, "enforce_allowed_ratio")
	if entry.Why != WhyDeclared || entry.Band != (Band{Lo: 0.90, Hi: 1.10}) {
		t.Errorf("a failing declared band became %s (%s)", entry.Band.String(), entry.Why)
	}
}

// TestCalibrateRefusesAKeepThatNamesNothing: a typo here means the invariant it
// was meant to protect was re-centred.
func TestCalibrateRefusesAKeepThatNamesNothing(t *testing.T) {
	rule := DefaultRule()
	rule.Keep = []string{"enforce_allowed_ration"}
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", referenceRows, 5)},
		Rule:    rule,
	})
	if err == nil {
		t.Fatal("accepted a --keep that names nothing")
	}
	for _, want := range []string{"enforce_allowed_ration", "did not judge", "dispatch_floor_us"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
}

// TestCalibrateKeepsAQuantisedBandInside: whole units inside their band are
// kept like any other, without the quantised refusal firing.
func TestCalibrateKeepsAQuantisedBandInside(t *testing.T) {
	entry := find(t, calibrateSet(t, keepRatio()), "rr_spread_req")
	if entry.Why != WhyInBand || entry.Band != (Band{Lo: 0, Hi: 0}) {
		t.Errorf("rr_spread_req came out %s (%s), want 0 – 0 kept", entry.Band.String(), entry.Why)
	}
}

// TestCalibrateRefusesAQuantisedBandThatLeft: a band over whole units is a
// statement about the instrument, and widening it from five readings is not
// available.
func TestCalibrateRefusesAQuantisedBandThatLeft(t *testing.T) {
	rows := func(i int) []verifyrunner.BandRow {
		out := referenceRows(i)
		for j := range out {
			if out[j].Invariant == "rr_spread_req" {
				out[j] = row("rr_spread_req", ptr(4), nil, 0, 0, "fail")
			}
		}
		return out
	}
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", rows, 5)},
		Rule:    keepRatio(),
	})
	if err == nil {
		t.Fatal("derived a band over a quantised invariant")
	}
	for _, want := range []string{"whole units", "--keep rr_spread_req"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
}

// TestCalibrateIgnoresInfoAndSkippedRows: neither is a measurement, and a
// skipped row's absent value is not a measurement of zero.
func TestCalibrateIgnoresInfoAndSkippedRows(t *testing.T) {
	result := calibrateSet(t, keepRatio())
	for _, entry := range result.Derived {
		if entry.Invariant == "tail_p99_ms" || entry.Invariant == "unmeasured_us" {
			t.Errorf("%s reached a band; it was %s", entry.Invariant, "not judged")
		}
	}
	if len(result.Derived) != 5 {
		t.Errorf("calibrated %d invariants, want the 5 judged ones", len(result.Derived))
	}
	// And they come out sorted, so the file's order does not depend on the
	// verifier's.
	var names []string
	for _, entry := range result.Derived {
		names = append(names, entry.Invariant)
	}
	want := "dispatch_floor_us,enforce_allowed_ratio,pooled_tail_p50_ms,rr_spread_req,steady_tail_p50_ms"
	if strings.Join(names, ",") != want {
		t.Errorf("the invariants are %v, want them sorted", names)
	}
}

// TestCalibrateRefusesRowsThatDisagreeAboutTheBand: if the runs were judged
// against two different bands then neither "the original band" nor "it already
// passes" is one thing.
func TestCalibrateRefusesRowsThatDisagreeAboutTheBand(t *testing.T) {
	rows := func(i int) []verifyrunner.BandRow {
		out := referenceRows(i)
		if i == 3 {
			out[0] = row("dispatch_floor_us", ptr(floorValues[i]), nil, 2.0, 9.9, "pass")
		}
		return out
	}
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", rows, 5)},
		Rule:    keepRatio(),
	})
	if err == nil {
		t.Fatal("calibrated across two different original bands")
	}
	for _, want := range []string{"dispatch_floor_us", "2.0 – 4.6", "2.0 – 9.9", "one band"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
}

// TestCalibrateRefusesAJudgedRowWithNoBand: a row judged without a band leaves
// nothing to compare against.
func TestCalibrateRefusesAJudgedRowWithNoBand(t *testing.T) {
	rows := func(int) []verifyrunner.BandRow {
		return []verifyrunner.BandRow{{
			Invariant: "dispatch_floor_us", Value: ptr(8.7), Verdict: "fail",
		}}
	}
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", rows, 5)},
		Rule:    DefaultRule(),
	})
	if err == nil {
		t.Fatal("calibrated from a row with no band")
	}
	if !strings.Contains(err.Error(), "without reporting the band") {
		t.Errorf("the refusal is %q", err)
	}
}

func TestLowerPolicies(t *testing.T) {
	k, err := ToleranceK(5)
	if err != nil {
		t.Fatal(err)
	}
	centre, width := Median(floorValues), k*StdDev(floorValues)
	for _, testCase := range []struct {
		policy Lower
		want   float64
	}{
		{LowerDerive, centre - width},
		// The original floor is far below the new centre, so keep reuses it —
		// which is the policy's own failure mode, visible as a number.
		{LowerKeep, 2.0},
		{LowerZero, centre - width},
	} {
		t.Run(string(testCase.policy), func(t *testing.T) {
			rule := keepRatio()
			rule.Lower = testCase.policy
			entry := find(t, calibrateSet(t, rule), "dispatch_floor_us")
			near(t, "the lower edge", entry.Band.Lo, roundOut(testCase.want, Places, false))
			near(t, "the upper edge", entry.Band.Hi, roundOut(centre+width, Places, true))
		})
	}
}

// TestLowerZeroClampsANonNegativeInvariant covers the case the set does not
// reach: a derived lower edge below zero.
func TestLowerZeroClampsANonNegativeInvariant(t *testing.T) {
	got, policy := lowerBound(0.01, 0.0306, 0.04, LowerZero)
	near(t, "the lower edge", got, 0)
	if policy != LowerZero {
		t.Errorf("the policy reported is %q, want zero", policy)
	}
	got, _ = lowerBound(0.01, 0.0306, 0.04, LowerDerive)
	near(t, "derive does not clamp", got, -0.0206)
	// A negative original lo is a noise skirt, and zero leaves it alone.
	got, policy = lowerBound(0, 0.03, -0.02, LowerZero)
	near(t, "a negative original lo is not clamped", got, -0.03)
	if policy != LowerDerive {
		t.Errorf("the policy reported is %q, want derive", policy)
	}
}

func TestUnitFloorByName(t *testing.T) {
	rule := DefaultRule()
	near(t, "a µs invariant", unitFloor("dispatch_floor_us", rule), rule.FloorUS)
	near(t, "a ms invariant", unitFloor("pooled_tail_p50_ms", rule), rule.FloorMS)
	near(t, "a count", unitFloor("rr_spread_req", rule), 0)
	near(t, "a ratio", unitFloor("enforce_allowed_ratio", rule), 0)
}

// TestAFloorBindsWhenThereIsNoSpread: five identical readings have no spread, so
// a floor is the only term left — which is what the floors are for. Here the
// relative floor is the wider of the two and wins; see
// TestUnitFloorStillWinsWhereItIsLarger for the other way round.
func TestAFloorBindsWhenThereIsNoSpread(t *testing.T) {
	rows := func(int) []verifyrunner.BandRow {
		out := referenceRows(0)
		out[0] = row("dispatch_floor_us", ptr(8.0), nil, 2.0, 4.6, "fail")
		return out
	}
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", rows, 5)},
		Rule:    keepRatio(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	entry := find(t, result, "dispatch_floor_us")
	// 0.02 * 8.0 = 0.16, above the 0.1 µs unit floor.
	near(t, "the half-width falls back to a floor", entry.HalfWidth, 0.16)
	if entry.Binding != "relative" {
		t.Errorf("the binding term is %q, want relative", entry.Binding)
	}
	near(t, "the lower edge", entry.Band.Lo, 7.84)
	near(t, "the upper edge", entry.Band.Hi, 8.16)
	if entry.HalfWidth == 0 {
		t.Error("a band of zero width was derived")
	}
}

func TestCalibrateRefusesTooFewRuns(t *testing.T) {
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", referenceRows, 4)},
		Rule:    keepRatio(),
	})
	if err == nil {
		t.Fatal("calibrated from four reference runs")
	}
	if !strings.Contains(err.Error(), "--min-runs is 5") {
		t.Errorf("the refusal is %q, want it to name the threshold", err)
	}
}

// TestCalibrateAcrossReports: a calibration set grows across sessions.
func TestCalibrateAcrossReports(t *testing.T) {
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{
			referenceReport("run-a", referenceRows, 3),
			referenceReport("run-b", referenceRows, 3),
		},
		Rule: keepRatio(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if result.N != 6 {
		t.Errorf("N is %d, want 6", result.N)
	}
	if len(result.Reports) != 2 {
		t.Fatalf("recorded %d reports, want 2", len(result.Reports))
	}
	if result.Day() != "2026-09-05" {
		t.Errorf("the day is %q", result.Day())
	}
}

// TestDaySpansMoreThanOne: a set gathered over two days says so.
func TestDaySpansMoreThanOne(t *testing.T) {
	later := referenceReport("run-b", referenceRows, 3)
	later.StartedAt = "2026-09-07T00:00:00Z"
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("run-a", referenceRows, 3), later},
		Rule:    keepRatio(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if got := result.Day(); got != "2026-09-05..2026-09-07" {
		t.Errorf("the day is %q, want the span", got)
	}
}

func TestCalibrateRefusals(t *testing.T) {
	cases := []struct {
		name    string
		reports []*doctor.Report
		want    string
	}{
		{"no report at all", nil, "no report to calibrate from"},
		{
			"two tasks",
			[]*doctor.Report{
				referenceReport("a", referenceRows, 5),
				func() *doctor.Report {
					r := referenceReport("b", referenceRows, 5)
					r.Task = "other"
					return r
				}(),
			},
			"is about task",
		},
		{
			"two revisions",
			[]*doctor.Report{
				referenceReport("a", referenceRows, 5),
				func() *doctor.Report {
					r := referenceReport("b", referenceRows, 5)
					r.Rev = "0000000000000000000000000000000000000000"
					return r
				}(),
			},
			"two different programs",
		},
		{
			"a run that did not answer",
			[]*doctor.Report{func() *doctor.Report {
				r := referenceReport("a", referenceRows, 5)
				r.Runs[2].Status = verifyrunner.StatusTimeout
				return r
			}()},
			"measured nothing",
		},
		{
			"a run with no rows",
			[]*doctor.Report{func() *doctor.Report {
				r := referenceReport("a", referenceRows, 5)
				r.Runs[1].Band = nil
				return r
			}()},
			"carries no band rows",
		},
		{
			"a run that judged something else",
			[]*doctor.Report{func() *doctor.Report {
				r := referenceReport("a", referenceRows, 5)
				r.Runs[3].Band.Rows = r.Runs[3].Band.Rows[:3]
				return r
			}()},
			"an earlier run judged",
		},
		{
			"a report with no reference run",
			[]*doctor.Report{func() *doctor.Report {
				r := referenceReport("a", referenceRows, 5)
				for i := range r.Runs {
					r.Runs[i].Kind = doctor.RunMutant
				}
				return r
			}()},
			"holds no reference run",
		},
		{
			"a verifier that judged nothing",
			[]*doctor.Report{referenceReport("a", func(int) []verifyrunner.BandRow {
				return []verifyrunner.BandRow{{Invariant: "note", Verdict: "info"}}
			}, 5)},
			"judged no invariant",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Calibrate(Input{Reports: testCase.reports, Rule: keepRatio()})
			if err == nil {
				t.Fatal("calibrated anyway")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("the refusal is %q, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// TestSelfCheckRejectsABandThatExcludesItsOwnSet.
func TestSelfCheckRejectsABandThatExcludesItsOwnSet(t *testing.T) {
	err := selfCheck([]Derived{{
		Invariant: "dispatch_floor_us",
		Values:    floorValues,
		Band:      Band{Lo: 8.72, Hi: 8.80},
	}})
	if err == nil {
		t.Fatal("accepted a band that excludes three of its own five values")
	}
	if !strings.Contains(err.Error(), "its own calibration set") {
		t.Errorf("the refusal is %q", err)
	}
	// A kept band is not self-checked: it may legitimately be one the reference
	// never left, and it is never derived from these values.
	if err := selfCheck([]Derived{{Why: WhyInBand, Values: floorValues, Band: Band{}}}); err != nil {
		t.Errorf("a kept band was self-checked: %v", err)
	}
}

func TestRuleValidate(t *testing.T) {
	cases := []struct {
		name string
		fix  func(*Rule)
		want string
	}{
		{"a centre nobody computes", func(r *Rule) { r.Centre = "mode" }, "not median or mean"},
		{"a spread nobody computes", func(r *Rule) { r.Spread = "iqr" }, "not stdev or mad"},
		{"a lower bound nobody applies", func(r *Rule) { r.Lower = "semantic" }, "not keep, derive or zero"},
		{"a negative multiplier", func(r *Rule) { r.K = -1 }, "cannot be negative"},
		{"a negative floor", func(r *Rule) { r.FloorUS = -1 }, "cannot be negative"},
		{"a calibration set of one", func(r *Rule) { r.MinRuns = 1 }, "at least 2 runs"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rule := DefaultRule()
			testCase.fix(&rule)
			err := rule.Validate()
			if err == nil {
				t.Fatal("accepted it")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("the refusal is %q, want it to mention %q", err, testCase.want)
			}
		})
	}
	if err := DefaultRule().Validate(); err != nil {
		t.Errorf("the default rule is invalid: %v", err)
	}
}

// TestCentreAndSpreadAlternatives checks the flags change the arithmetic, since
// a flag that is read and ignored is worse than no flag.
func TestCentreAndSpreadAlternatives(t *testing.T) {
	rule := keepRatio()
	rule.Centre, rule.Spread, rule.K = CentreMean, SpreadMAD, 3
	entry := find(t, calibrateSet(t, rule), "dispatch_floor_us")
	near(t, "the centre", entry.Centre, Mean(floorValues))
	near(t, "the half-width", entry.HalfWidth, 3*MAD(floorValues))
	if entry.Binding != "k*mad" {
		t.Errorf("the binding term is %q, want k*mad", entry.Binding)
	}
}

func TestDescribeNamesTheRule(t *testing.T) {
	got := DefaultRule().Describe(5)
	for _, want := range []string{
		"median", "k2(N=5,0.95,0.90)=4.164", "stdev", "us=0.1", "ms=0.005", "lower=derive",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe is %q, want it to carry %q", got, want)
		}
	}
	// The relative floor is part of the default rule and is named.
	if !strings.Contains(got, "0.02*|centre|") {
		t.Errorf("Describe does not name the relative floor: %q", got)
	}
	// The ci_half term is not, and is not advertised.
	if strings.Contains(got, "ci_half") {
		t.Errorf("Describe advertises a term the default rule does not use: %q", got)
	}
}

func TestTableAndSummary(t *testing.T) {
	result := calibrateSet(t, keepRatio())
	table := strings.Join(result.Table(), "\n")
	for _, want := range []string{
		"original band", "derived band", "3x ci_half",
		"dispatch_floor_us", "unchanged", "kept: declared", "derived: k*stdev",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not carry %q:\n%s", want, table)
		}
	}
	summary := strings.Join(result.Summary(), "\n")
	for _, want := range []string{
		"task banded at 39778ec3f02f", "N=5", "20260905T002350Z-e13205e7",
		"re-centred 2:", "kept 3:",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary does not carry %q:\n%s", want, summary)
		}
	}
}

// TestBandsJSON is the file's schema, key by key.
func TestBandsJSON(t *testing.T) {
	result := calibrateSet(t, keepRatio())
	bands, err := result.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if bands.SchemaVersion != SchemaVersion || bands.Task != "banded" || bands.N != 5 {
		t.Errorf("the header is %+v", bands)
	}
	if strings.Join(bands.SourceRuns, ",") != "20260905T002350Z-e13205e7" {
		t.Errorf("source_runs = %v", bands.SourceRuns)
	}
	if bands.Day != "2026-09-05" {
		t.Errorf("day = %q", bands.Day)
	}
	if bands.Rule.Statistic != Statistic || bands.Rule.P != Coverage || bands.Rule.Gamma != Confidence {
		t.Errorf("rule = %+v", bands.Rule)
	}
	near(t, "the tolerance factor", bands.Rule.K2, 4.164)
	near(t, "the µs floor", bands.Rule.Floors.US, 0.1)
	near(t, "the ms floor", bands.Rule.Floors.MS, 0.005)
	if bands.Rule.Lower != "derive" {
		t.Errorf("lower = %q", bands.Rule.Lower)
	}
	if len(bands.Invariants) != 5 {
		t.Errorf("the file holds %d invariants, want 5", len(bands.Invariants))
	}

	derivedEntry := bands.Invariants["dispatch_floor_us"]
	if derivedEntry.Kept || derivedEntry.Reason != "derived: k*stdev" {
		t.Errorf("dispatch_floor_us = %+v", derivedEntry)
	}
	if derivedEntry.HalfWidth == nil {
		t.Fatal("a derived band carries no half_width")
	}
	near(t, "lo", derivedEntry.Lo, 8.5042)
	near(t, "hi", derivedEntry.Hi, 9.0326)
	near(t, "centre", derivedEntry.Centre, 8.7684)
	near(t, "half_width", *derivedEntry.HalfWidth, 0.2641)
	if derivedEntry.ThreeCIHalf == nil {
		t.Fatal("a band whose rows carried a half-range reports no three_ci_half")
	}
	near(t, "three_ci_half", *derivedEntry.ThreeCIHalf, 0.8154)

	keptEntry := bands.Invariants["enforce_allowed_ratio"]
	if !keptEntry.Kept || keptEntry.Reason != "kept: declared" {
		t.Errorf("enforce_allowed_ratio = %+v", keptEntry)
	}
	if keptEntry.HalfWidth != nil {
		t.Errorf("a kept band carries a half_width of %v; none was derived", *keptEntry.HalfWidth)
	}
	near(t, "lo", keptEntry.Lo, 0.90)
	near(t, "hi", keptEntry.Hi, 1.10)
	// A band whose rows carried no half-range reports none rather than zero.
	if got := bands.Invariants["pooled_tail_p50_ms"].ThreeCIHalf; got != nil {
		t.Errorf("three_ci_half = %v, want null", *got)
	}
}

// TestBandsJSONIsDeterministic: a re-calibration that changed nothing has to be
// an empty diff, so the key order cannot depend on a map walk.
func TestBandsJSONIsDeterministic(t *testing.T) {
	first, err := calibrateSet(t, keepRatio()).JSON()
	if err != nil {
		t.Fatal(err)
	}
	body, err := first.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		again, err := calibrateSet(t, keepRatio()).JSON()
		if err != nil {
			t.Fatal(err)
		}
		other, err := again.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if string(other) != string(body) {
			t.Fatalf("two renderings differ:\n%s\n---\n%s", body, other)
		}
	}
	// The invariants come out in sorted order, and the top-level keys in the
	// order the schema declares them.
	text := string(body)
	if !strings.Contains(text, `"schema_version": 1`) {
		t.Errorf("the file does not open with its schema version:\n%s", text)
	}
	order := []string{
		"dispatch_floor_us", "enforce_allowed_ratio", "pooled_tail_p50_ms",
		"rr_spread_req", "steady_tail_p50_ms",
	}
	at := -1
	for _, name := range order {
		next := strings.Index(text, `"`+name+`"`)
		if next <= at {
			t.Errorf("%s is out of order in the file:\n%s", name, text)
		}
		at = next
	}
	// It round-trips.
	read, err := ReadBands(body)
	if err != nil {
		t.Fatalf("ReadBands: %v", err)
	}
	if strings.Join(read.Names(), ",") != strings.Join(order, ",") {
		t.Errorf("Names = %v", read.Names())
	}
	// Compared as JSON rather than with ==: two of the fields are pointers, and
	// == on those compares addresses rather than the numbers they carry.
	back, err := json.Marshal(read.Invariants["dispatch_floor_us"])
	if err != nil {
		t.Fatal(err)
	}
	forth, err := json.Marshal(first.Invariants["dispatch_floor_us"])
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(forth) {
		t.Errorf("a band did not survive the round trip: %s, want %s", back, forth)
	}
}

func TestReadBandsRefusesAnotherSchema(t *testing.T) {
	body, err := json.Marshal(map[string]any{"schema_version": 99})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBands(body); err == nil {
		t.Fatal("read a bands file of another schema")
	}
	if _, err := ReadBands([]byte("not json")); err == nil {
		t.Fatal("read something that is not JSON")
	}
}

// rowsWith replaces one invariant's row in every run.
func rowsWith(name string, values []float64, lo, hi float64, verdict string) func(int) []verifyrunner.BandRow {
	return func(i int) []verifyrunner.BandRow {
		out := referenceRows(i)
		for j := range out {
			if out[j].Invariant == name {
				out[j] = row(name, ptr(values[i]), nil, lo, hi, verdict)
			}
		}
		return out
	}
}

// TestRelativeFloorKeepsAUnitlessBandFromCollapsing is F2, and it is the case
// the whole change set exists to prevent, arrived at from the other side: an
// invariant with no recognisable unit that reads identically on every run has
// no spread to build a width from, and a band of zero width rejects the next
// clean run.
func TestRelativeFloorKeepsAUnitlessBandFromCollapsing(t *testing.T) {
	identical := []float64{0.8712, 0.8712, 0.8712, 0.8712, 0.8712}
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r",
			rowsWith("enforce_allowed_ratio", identical, 0.95, 1.05, "fail"), 5)},
		Rule: DefaultRule(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	entry := find(t, result, "enforce_allowed_ratio")
	if entry.HalfWidth == 0 {
		t.Fatalf("%s derived a band of zero width: %s", entry.Invariant, entry.Band.String())
	}
	// The relative floor is what is left when there is no spread and no unit.
	near(t, "the half-width", entry.HalfWidth, 0.02*0.8712)
	if entry.Binding != "relative" {
		t.Errorf("the binding term is %q, want relative", entry.Binding)
	}
	if !entry.Band.Contains(0.8712) {
		t.Errorf("the band %s excludes the value it was built from", entry.Band.String())
	}
}

// TestRelativeFloorOnANearlyDeterministicRatio is the shipped shape of the same
// problem: a ratio whose five readings differ in the fourth decimal derives a
// band two hundred times tighter than the original unless the floor holds it.
func TestRelativeFloorOnANearlyDeterministicRatio(t *testing.T) {
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r",
			rowsWith("enforce_allowed_ratio", ratioValues, 0.95, 1.05, "fail"), 5)},
		Rule: DefaultRule(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	entry := find(t, result, "enforce_allowed_ratio")
	centre := Median(ratioValues)
	near(t, "the half-width", entry.HalfWidth, 0.02*centre)
	if entry.HalfWidth < 0.02*centre {
		t.Errorf("the half-width is %v, below 2%% of the centre", entry.HalfWidth)
	}
	// Without the floor this would have been k2*s ≈ 0.00046, a band of 0.001
	// wide against an original of 0.10.
	if width := 2 * entry.HalfWidth; width < 0.03 {
		t.Errorf("the band is %v wide (%s); the floor did not hold", width, entry.Band.String())
	}
}

// TestRefusesABandWithNoSpreadAndNoScale: a centre of zero leaves the relative
// floor nothing to take a share of, and there is no unit. Refusing is the only
// honest answer.
func TestRefusesABandWithNoSpreadAndNoScale(t *testing.T) {
	zeros := []float64{0, 0, 0, 0, 0}
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r",
			rowsWith("enforce_allowed_ratio", zeros, 0.95, 1.05, "fail"), 5)},
		Rule: DefaultRule(),
	})
	if err == nil {
		t.Fatal("derived a band of zero width")
	}
	for _, want := range []string{
		"cannot derive a band for enforce_allowed_ratio", "no unit", "--keep enforce_allowed_ratio",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
}

// TestUnitFloorStillWinsWhereItIsLarger: adding the relative floor must not
// take the unit floor's place where the unit floor is the wider of the two.
func TestUnitFloorStillWinsWhereItIsLarger(t *testing.T) {
	tiny := []float64{0.5, 0.5, 0.5, 0.5, 0.5}
	result, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r",
			rowsWith("dispatch_floor_us", tiny, 2.0, 4.6, "fail"), 5)},
		Rule: keepRatio(),
	})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	entry := find(t, result, "dispatch_floor_us")
	// 0.02 * 0.5 = 0.01, below the 0.1 µs floor.
	near(t, "the half-width", entry.HalfWidth, 0.1)
	if entry.Binding != "floor" {
		t.Errorf("the binding term is %q, want floor", entry.Binding)
	}
}

// TestRefusesAnInvariantJudgedTwiceInOneRun is S1/F6: one run judging one
// invariant twice would give it a per-invariant N larger than the number of
// runs, so the tolerance factor the band was derived under would stop being the
// one the file records.
func TestRefusesAnInvariantJudgedTwiceInOneRun(t *testing.T) {
	rows := func(i int) []verifyrunner.BandRow {
		out := referenceRows(i)
		return append(out, row("dispatch_floor_us", ptr(floorValues[i]), nil, 2.0, 4.6, "fail"))
	}
	_, err := Calibrate(Input{
		Reports: []*doctor.Report{referenceReport("r", rows, 5)},
		Rule:    keepRatio(),
	})
	if err == nil {
		t.Fatal("calibrated from a run that judged one invariant twice")
	}
	for _, want := range []string{"dispatch_floor_us", "more than once"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
}

// TestBandsJSONRecordsTheRelativeFloor: the file has to state the rule it was
// produced under, and the floor is part of it.
func TestBandsJSONRecordsTheRelativeFloor(t *testing.T) {
	bands, err := calibrateSet(t, keepRatio()).JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	near(t, "the relative floor", bands.Rule.RelFloor, 0.02)
	body, err := bands.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"rel_floor": 0.02`) {
		t.Errorf("the file does not record rel_floor:\n%s", body)
	}
}

// TestBandsJSONRefusesAMixedN: `n` and `k2` are written once for the file, so
// they have to be true of every band in it.
func TestBandsJSONRefusesAMixedN(t *testing.T) {
	result := calibrateSet(t, keepRatio())
	result.Derived[0].N = result.N + 1
	if _, err := result.JSON(); err == nil {
		t.Fatal("wrote a file whose n does not describe every band in it")
	}
}
