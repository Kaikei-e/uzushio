package doctor_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// checkOnly runs a check with the given options applied, and returns the report
// and the labels the runner was actually asked about.
func checkOnly(
	t *testing.T, loaded *task.Task, runner *fake, apply func(*doctor.Options),
) (*doctor.Report, []string) {
	t.Helper()
	opts := doctor.Options{
		Task:     loaded,
		Runner:   runner,
		Dir:      filepath.Join(t.TempDir(), "out"),
		Parallel: 2,
		RunID:    "20260101T000000Z-0000beef",
		Now:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	apply(&opts)
	report, err := doctor.Check(t.Context(), opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	seen := append([]string(nil), runner.seen...)
	sort.Strings(seen)
	return report, seen
}

// checkOnlyError runs a check expected to be refused.
func checkOnlyError(t *testing.T, loaded *task.Task, runner *fake, apply func(*doctor.Options)) error {
	t.Helper()
	opts := doctor.Options{
		Task:     loaded,
		Runner:   runner,
		Dir:      filepath.Join(t.TempDir(), "out"),
		Parallel: 2,
		RunID:    "20260101T000000Z-0000beef",
		Now:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	apply(&opts)
	_, err := doctor.Check(t.Context(), opts)
	if err == nil {
		t.Fatal("Check succeeded; it was supposed to refuse")
	}
	return err
}

func passingReference(runner *fake, n int) *fake {
	for i := 1; i <= n; i++ {
		runner.byLabel["reference-"+string(rune('0'+i))] = verifyrunner.StatusPass
	}
	return runner
}

// TestOnlyReferenceRunsOnlyTheReference is the drift check: the question is
// whether the verifier still accepts correct code, and none of the mutants
// answer it.
func TestOnlyReferenceRunsOnlyTheReference(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	report, seen := checkOnly(t, loaded, passingReference(killer(), 3), func(o *doctor.Options) {
		o.Only = doctor.Only{doctor.OnlyReference}
	})
	want := []string{"reference-1", "reference-2", "reference-3"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("verified %v, want %v", seen, want)
	}
	if got := report.Aggregates.ReferenceRuns; got != 3 {
		t.Errorf("reference_runs = %d, want 3", got)
	}
	if got := report.Aggregates.Killed + report.Aggregates.Survived; got != 0 {
		t.Errorf("%d mutants were measured; none was selected", got)
	}
	// No mutant ran, so there is no rate — and the reference passing is not on
	// its own a healthy verifier.
	if report.Aggregates.KillRate != nil {
		t.Errorf("kill_rate = %v, want none", *report.Aggregates.KillRate)
	}
	if report.Verdict != vocab.HealthInconclusive {
		t.Errorf("verdict = %q, want inconclusive", report.Verdict)
	}
}

// TestOnlyMutantsRunsOnlyTheMutants: without a reference run the rate is not
// evidence, and the report says so rather than reporting 1.00.
func TestOnlyMutantsRunsOnlyTheMutants(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	report, seen := checkOnly(t, loaded, passingReference(killer(), 3), func(o *doctor.Options) {
		o.Only = doctor.Only{doctor.OnlyMutants}
	})
	for _, label := range seen {
		if !strings.HasPrefix(label, "mutant-") {
			t.Errorf("verified %q, which is not a mutant", label)
		}
	}
	if len(seen) != 2 {
		t.Errorf("verified %v, want the two mutants", seen)
	}
	if report.Aggregates.ReferenceRuns != 0 {
		t.Errorf("reference_runs = %d, want 0", report.Aggregates.ReferenceRuns)
	}
	if report.Aggregates.Killed != 2 {
		t.Errorf("killed = %d, want 2", report.Aggregates.Killed)
	}
	if report.Aggregates.KillRateMeaningful {
		t.Error("the rate is marked as evidence; no reference run reached a verdict")
	}
	if !strings.Contains(strings.Join(report.Summary(), "\n"), "not evidence") {
		t.Errorf("the summary does not say the rate is not evidence:\n%v", report.Summary())
	}
	// A rate that is not evidence cannot make a verifier healthy, however many
	// mutants it killed. Without --only this case was unreachable: there was
	// always at least one reference run.
	if report.Verdict != vocab.HealthInconclusive {
		t.Errorf("verdict = %q, want inconclusive: nothing said the verifier accepts anything",
			report.Verdict)
	}
}

// TestOnlySelectsOneMutant is the loop the flag exists for: a mutant was added,
// and the question is about that mutant.
func TestOnlySelectsOneMutant(t *testing.T) {
	for _, selector := range []string{
		"times", "times.diff", "mutants/times.diff", "mutant-0-times",
	} {
		t.Run(selector, func(t *testing.T) {
			loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
			report, seen := checkOnly(t, loaded, passingReference(killer(), 3), func(o *doctor.Options) {
				o.Only = doctor.Only{selector}
			})
			if strings.Join(seen, ",") != "mutant-0-times" {
				t.Errorf("verified %v, want just mutant-0-times", seen)
			}
			if len(report.Runs) != 1 {
				t.Errorf("the report holds %d runs, want 1", len(report.Runs))
			}
			// The mutant keeps its index in the task, so the label a narrowed
			// check writes is the label a full one would have written.
			if report.Runs[0].Mutant == nil || *report.Runs[0].Mutant != 0 {
				t.Errorf("the run does not carry its mutant index: %+v", report.Runs[0])
			}
		})
	}
}

// TestOnlySelectsAMutantByIndexAwareLabel checks the second mutant, so a
// selector cannot pass by matching the first one by accident.
func TestOnlySelectsTheSecondMutant(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	report, seen := checkOnly(t, loaded, passingReference(killer(), 3), func(o *doctor.Options) {
		o.Only = doctor.Only{"swapped"}
	})
	if strings.Join(seen, ",") != "mutant-1-swapped" {
		t.Errorf("verified %v, want just mutant-1-swapped", seen)
	}
	if report.Runs[0].Mutant == nil || *report.Runs[0].Mutant != 1 {
		t.Errorf("the run does not carry mutant index 1: %+v", report.Runs[0])
	}
}

// TestOnlyCombinesSelectors: the selectors are a set, not a mode.
func TestOnlyCombinesSelectors(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	_, seen := checkOnly(t, loaded, passingReference(killer(), 3), func(o *doctor.Options) {
		o.Only = doctor.Only{"reference-2", "times"}
	})
	if strings.Join(seen, ",") != "mutant-0-times,reference-2" {
		t.Errorf("verified %v, want reference-2 and mutant-0-times", seen)
	}
}

// TestOnlyRefusesASelectorThatMatchedNothing is the failure the check exists to
// prevent: a typo that produces a clean report about a mutant nobody verified.
func TestOnlyRefusesASelectorThatMatchedNothing(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	err := checkOnlyError(t, loaded, killer(), func(o *doctor.Options) {
		o.Only = doctor.Only{"tiems"}
	})
	for _, want := range []string{"tiems", "matched nothing", "times", "swapped", "reference"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
	// One good selector does not excuse a bad one.
	err = checkOnlyError(t, loaded, killer(), func(o *doctor.Options) {
		o.Only = doctor.Only{"times", "nosuch"}
	})
	if !strings.Contains(err.Error(), "--only nosuch matched nothing") {
		t.Errorf("the refusal is %q, want it to name only the bad selector as unmatched", err)
	}
}

// TestOnlyEmptyIsEverything: the flag's absence is the check that existed
// before it did.
func TestOnlyEmptyIsEverything(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	report, seen := checkOnly(t, loaded, passingReference(killer(), 3), func(*doctor.Options) {})
	if len(seen) != 5 {
		t.Errorf("verified %v, want three reference runs and two mutants", seen)
	}
	if report.Verdict != vocab.HealthHealthy {
		t.Errorf("verdict = %q, want healthy", report.Verdict)
	}
}

// TestOnlyMutantsSkipsTheWorktreeForAnUnselectedOne: a mutant that would not
// apply is not composed at all when it was not selected, so a narrowed check is
// not held up by a mis-authored diff elsewhere in the task.
func TestOnlySkipsAnUnselectedBrokenMutant(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, []mutantSpec{
		{"times", task.ExpectKilled, task.OriginHand},
		{"noapply", task.ExpectKilled, task.OriginHand},
	})
	report, seen := checkOnly(t, loaded, passingReference(killer(), 1), func(o *doctor.Options) {
		o.Only = doctor.Only{"times"}
	})
	if strings.Join(seen, ",") != "mutant-0-times" {
		t.Errorf("verified %v, want just mutant-0-times", seen)
	}
	if report.Aggregates.Inconclusive != 0 {
		t.Errorf("inconclusive = %d; the broken mutant was not selected",
			report.Aggregates.Inconclusive)
	}
}

// TestOnlyMutantsWithASurvivorIsInconclusive is F4: a check that never
// established the verifier accepts anything must not report `unhealthy` off a
// rate the same report prints as "not evidence".
//
// The two ways a verifier is unhealthy without a meaningful rate — the
// reference failed, and a hand-written mutant survived — are both decided
// before the rate is looked at, so neither is affected.
func TestOnlyMutantsWithASurvivorIsInconclusive(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, []mutantSpec{
		{"times", task.ExpectKilled, task.OriginGenerated},
		{"swapped", task.ExpectKilled, task.OriginGenerated},
	})
	runner := killer()
	runner.byLabel["mutant-0-times"] = verifyrunner.StatusPass
	report, seen := checkOnly(t, loaded, passingReference(runner, 3), func(o *doctor.Options) {
		o.Only = doctor.Only{doctor.OnlyMutants}
	})
	if len(seen) != 2 {
		t.Fatalf("verified %v, want the two mutants", seen)
	}
	if report.Aggregates.Survived != 1 || report.Aggregates.KillRateMeaningful {
		t.Fatalf("aggregates = %+v, want one survivor and no evidence", report.Aggregates)
	}
	if rate := report.Aggregates.KillRate; rate == nil || *rate >= report.Aggregates.KillRateMin {
		t.Fatalf("kill_rate = %v; the test needs one below the threshold", rate)
	}
	if report.Verdict != vocab.HealthInconclusive {
		t.Errorf("verdict = %q, want inconclusive: the rate is below the threshold but it is "+
			"not evidence, and nothing said the verifier accepts anything\n%v",
			report.Verdict, report.Summary())
	}
	// The record must not pair "not evidence" with a verdict read off it.
	summary := strings.Join(report.Summary(), "\n")
	if !strings.Contains(summary, "not evidence") || !strings.Contains(summary, "verdict: inconclusive") {
		t.Errorf("the summary pairs the rate and the verdict badly:\n%s", summary)
	}
}

// TestASurvivingHandMutantIsStillUnhealthy: the guard moved above the rate
// comparison, and this is the case that must not have moved with it. A
// hand-written mutant is the set a person chose to stand behind.
func TestASurvivingHandMutantIsStillUnhealthy(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	runner := killer()
	runner.byLabel["mutant-0-times"] = verifyrunner.StatusPass
	report, _ := checkOnly(t, loaded, passingReference(runner, 3), func(o *doctor.Options) {
		o.Only = doctor.Only{doctor.OnlyMutants}
	})
	if report.Aggregates.KillRateMeaningful {
		t.Fatal("the test needs a rate that is not evidence")
	}
	if report.Verdict != vocab.HealthUnhealthy {
		t.Errorf("verdict = %q, want unhealthy: a hand-written mutant survived", report.Verdict)
	}
}
