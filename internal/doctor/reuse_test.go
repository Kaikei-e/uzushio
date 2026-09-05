package doctor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// fingerprint computes a task's environment fingerprint, failing the test if it
// cannot.
func fingerprint(t *testing.T, loaded *task.Task) *doctor.Environment {
	t.Helper()
	got, err := doctor.Fingerprint(t.Context(), loaded)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	return got
}

// reload re-reads a task after its directory has been edited, so a fingerprint
// is taken of what is on disk now.
func reload(t *testing.T, loaded *task.Task) *task.Task {
	t.Helper()
	again, err := task.Load(loaded.Dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return again
}

// TestFingerprintIsStable: the same task twice is the same environment, or
// nothing built on it means anything.
func TestFingerprintIsStable(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	first, second := fingerprint(t, loaded), fingerprint(t, loaded)
	if first.SHA256 != second.SHA256 {
		t.Errorf("two fingerprints of one task differ: %s and %s", first.SHA256, second.SHA256)
	}
	if !first.Same(second) {
		t.Error("Same says two identical fingerprints differ")
	}
	// The components say what was covered, so a reader can argue with it.
	if !strings.Contains(strings.Join(first.Components, ","), "verify") {
		t.Errorf("components = %v, want the verify block", first.Components)
	}
}

// TestFingerprintNoticesTheVerifierChanging is the whole point: the files that
// reach the container are hashed, so editing one invalidates a reuse.
func TestFingerprintNoticesAMountedFileChanging(t *testing.T) {
	loaded := mountedTask(t, newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants))
	before := fingerprint(t, loaded)
	if !strings.Contains(strings.Join(before.Components, ","), "verify.sh") {
		t.Fatalf("components = %v, want the mounted file", before.Components)
	}
	writeFile(t, filepath.Join(loaded.Dir, "verify.sh"), "two\n")
	changed := fingerprint(t, reload(t, loaded))
	if changed.SHA256 == before.SHA256 {
		t.Error("editing a mounted file did not change the fingerprint")
	}
	// The components are the same, so the difference can only be reported as
	// "one of these changed on disk".
	if got := changed.Differences(before); !strings.Contains(got[0], "changed on disk") {
		t.Errorf("Differences = %v, want it to say a hashed file changed", got)
	}
	// A file the compose service does not mount is not hashed: a fingerprint
	// over whatever is in the directory changes when somebody leaves a scratch
	// file there, and one that changes for no reason is one people override.
	writeFile(t, filepath.Join(loaded.Dir, "notes.txt"), "scratch\n")
	if again := fingerprint(t, reload(t, loaded)); again.SHA256 != changed.SHA256 {
		t.Error("a file nothing mounts changed the fingerprint")
	}
}

// TestFingerprintFollowsTheComposeFile: the mounts are discovered rather than
// listed, so a task that mounts a differently named file is covered without
// this package being told anything about it.
func TestFingerprintFollowsTheComposeFile(t *testing.T) {
	loaded := mountedTask(t, newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants))
	writeFile(t, filepath.Join(loaded.Dir, "bands.json"), "{}\n")
	before := fingerprint(t, reload(t, loaded))
	if strings.Contains(strings.Join(before.Components, ","), "bands.json") {
		t.Fatalf("components = %v; nothing mounts bands.json yet", before.Components)
	}
	writeFile(t, filepath.Join(loaded.Dir, "compose.yaml"), `services:
  verify:
    image: example:1
    volumes:
      - ./verify.sh:/verify.sh:ro
      - ./bands.json:/bands.json:ro
`)
	added := fingerprint(t, reload(t, loaded))
	if !strings.Contains(strings.Join(added.Components, ","), "bands.json") {
		t.Errorf("components = %v, want the newly mounted file", added.Components)
	}
	writeFile(t, filepath.Join(loaded.Dir, "bands.json"), `{"schema_version": 1}`)
	if changed := fingerprint(t, reload(t, loaded)); changed.SHA256 == added.SHA256 {
		t.Error("editing the newly mounted file did not change the fingerprint")
	}
}

// mountedTask gives a task a compose file that bind-mounts a file from the task
// directory, which is how the fingerprint learns what to hash.
func mountedTask(t *testing.T, loaded *task.Task) *task.Task {
	t.Helper()
	writeFile(t, filepath.Join(loaded.Dir, "compose.yaml"), `services:
  verify:
    image: example:1
    volumes:
      - ./verify.sh:/verify.sh:ro
`)
	writeFile(t, filepath.Join(loaded.Dir, "verify.sh"), "one\n")
	return reload(t, loaded)
}

// TestFingerprintNoticesTheVerifyBlock: the timeout and the service are part of
// what a verification is.
func TestFingerprintNoticesTheVerifyBlock(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	before := fingerprint(t, loaded)
	manifest, err := os.ReadFile(filepath.Join(loaded.Dir, task.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(loaded.Dir, task.ManifestFile), strings.Replace(string(manifest),
		`"files": ["add.go"],`,
		`"files": ["add.go"],
  "verify": {"timeout_seconds": 900},`, 1))
	if after := fingerprint(t, reload(t, loaded)); after.SHA256 == before.SHA256 {
		t.Error("changing the verify block did not change the fingerprint")
	}
}

// TestFingerprintRecordsAnAbsentComposeFile: the fixtures declare a compose
// file and do not have one, and that is recorded rather than refused — a
// fingerprint's job is to notice a change, and "absent" is a state like any
// other.
func TestFingerprintRecordsAnAbsentComposeFile(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	before := fingerprint(t, loaded)
	if !strings.Contains(strings.Join(before.Components, ","), "compose.yaml (absent)") {
		t.Errorf("components = %v, want the compose file recorded as absent", before.Components)
	}
	writeFile(t, filepath.Join(loaded.Dir, "compose.yaml"), "services: {}\n")
	after := fingerprint(t, reload(t, loaded))
	if after.SHA256 == before.SHA256 {
		t.Error("a compose file appearing did not change the fingerprint")
	}
	got := after.Differences(before)
	if !strings.Contains(strings.Join(got, "; "), "compose.yaml (absent)") {
		t.Errorf("Differences = %v, want it to name the component that came and went", got)
	}
}

// TestFingerprintDifferencesAgainstNothing: a report written before
// fingerprints existed cannot be shown comparable, and says so.
func TestFingerprintDifferencesAgainstNothing(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	got := fingerprint(t, loaded)
	if got.Same(nil) {
		t.Error("a fingerprint matched a missing one")
	}
	if !strings.Contains(got.Differences(nil)[0], "earlier report carries no environment") {
		t.Errorf("Differences = %v", got.Differences(nil))
	}
	var absent *doctor.Environment
	if absent.Same(got) {
		t.Error("a missing fingerprint matched a real one")
	}
}

// TestReportCarriesTheFingerprint: every report gains it, so every report can
// be reused from later.
func TestReportCarriesTheFingerprint(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	report := check(t, loaded, passingReference(killer(), 3))
	if report.Environment == nil {
		t.Fatal("the report carries no environment fingerprint")
	}
	if report.Environment.SHA256 != fingerprint(t, loaded).SHA256 {
		t.Error("the report's fingerprint is not the task's")
	}
	if _, ok := report.ReusedReference(); ok {
		t.Error("a check that ran its own reference claims to have reused one")
	}
}

// TestReuseTakesTheReferenceBlock is the loop this exists for: a mutant was
// added, and the reference block from an hour ago is still evidence.
func TestReuseTakesTheReferenceBlock(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	earlier := check(t, loaded, passingReference(killer(), 3))

	report, seen := checkOnly(t, loaded, killer(), func(o *doctor.Options) {
		o.Reuse = earlier
	})
	// Nothing was verified but the mutants.
	for _, label := range seen {
		if !strings.HasPrefix(label, "mutant-") {
			t.Errorf("verified %q; the reference was supposed to be reused", label)
		}
	}
	if len(seen) != 2 {
		t.Errorf("verified %v, want the two mutants", seen)
	}
	// The reference block is there, marked, and counted.
	if got := report.Aggregates.ReferenceRuns; got != 3 {
		t.Errorf("reference_runs = %d, want 3", got)
	}
	from, ok := report.ReusedReference()
	if !ok || from != earlier.RunID {
		t.Errorf("reused_from = %q (%v), want %q", from, ok, earlier.RunID)
	}
	for _, run := range report.Runs {
		if run.Kind == doctor.RunReference && run.ReusedFrom != earlier.RunID {
			t.Errorf("%s carries reused_from %q", run.Label, run.ReusedFrom)
		}
		if run.Kind == doctor.RunMutant && run.ReusedFrom != "" {
			t.Errorf("mutant %s is marked as reused", run.Label)
		}
	}
	// The reference block comes first, the way a check that ran it would have
	// written it.
	if report.Runs[0].Kind != doctor.RunReference {
		t.Errorf("the report opens with %s, want the reference block", report.Runs[0].Kind)
	}
	// Conclude treats them as ordinary reference runs: the rate is evidence.
	if !report.Aggregates.KillRateMeaningful {
		t.Error("the rate is not marked as evidence, though the reference passed")
	}
	if report.Verdict != vocab.HealthHealthy {
		t.Errorf("verdict = %q, want healthy\n%v", report.Verdict, report.Summary())
	}
	// And the record says where they came from.
	summary := strings.Join(report.Summary(), "\n")
	if !strings.Contains(summary, "reference runs reused from "+earlier.RunID) {
		t.Errorf("the summary does not say the reference was reused:\n%s", summary)
	}
}

// TestReuseCarriesTheBandRows: a reference run is what a calibration is built
// from, so it is copied whole rather than summarised.
func TestReuseCarriesTheBandRows(t *testing.T) {
	loaded := bandTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	earlier := check(t, loaded, banded{inner: passingReference(killer(), 1)})
	report, _ := checkOnly(t, loaded, killer(), func(o *doctor.Options) { o.Reuse = earlier })

	first := report.Runs[0]
	if first.Kind != doctor.RunReference || first.ReusedFrom == "" {
		t.Fatalf("the report does not open with a reused reference run: %+v", first)
	}
	if first.Band == nil || len(first.Band.Rows) != 2 {
		t.Fatalf("the reused reference run lost its band rows: %+v", first.Band)
	}
	// The measured row keeps its value and its band, which is what a
	// calibration reads; the skipped row keeps its absent value, which is what
	// says the invariant was not measured rather than measured as zero.
	measured := first.Band.Rows[1]
	if measured.Invariant != "rr_spread_req" || measured.Value == nil || *measured.Value != 0 {
		t.Errorf("the measured row did not survive: %+v", measured)
	}
	if measured.BandHi == nil {
		t.Error("the measured row lost the band it was held to")
	}
	if first.Band.Rows[0].Value != nil {
		t.Error("the skipped row gained a value")
	}
	if len(first.Band.Skipped) != 1 {
		t.Errorf("the reused run lost its skipped list: %+v", first.Band.Skipped)
	}
}

// TestReuseRefusesAChangedVerifier is the refusal the fingerprint exists for.
func TestReuseRefusesAChangedVerifier(t *testing.T) {
	loaded := mountedTask(t, newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants))
	earlier := check(t, loaded, passingReference(killer(), 3))

	writeFile(t, filepath.Join(loaded.Dir, "verify.sh"), "echo after\n")
	err := checkOnlyError(t, reload(t, loaded), killer(), func(o *doctor.Options) {
		o.Reuse = earlier
	})
	for _, want := range []string{"cannot be reused", earlier.RunID, "changed on disk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to mention %q", err, want)
		}
	}
}

// TestReuseRefusesAChangedTask names which of the three checks failed, because
// "a different commit" and "a different harness" are different things to fix.
func TestReuseRefusesAChangedTask(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	earlier := check(t, loaded, passingReference(killer(), 3))

	t.Run("a different task id", func(t *testing.T) {
		other := *earlier
		other.Task = "somethingelse"
		err := checkOnlyError(t, loaded, killer(), func(o *doctor.Options) { o.Reuse = &other })
		if !strings.Contains(err.Error(), `measured task "somethingelse" and this is "hello"`) {
			t.Errorf("the refusal is %q", err)
		}
	})
	t.Run("a different revision", func(t *testing.T) {
		other := *earlier
		other.Rev = "0000000000000000000000000000000000000000"
		err := checkOnlyError(t, loaded, killer(), func(o *doctor.Options) { o.Reuse = &other })
		if !strings.Contains(err.Error(), "000000000000 and this is") {
			t.Errorf("the refusal is %q", err)
		}
	})
	t.Run("no fingerprint at all", func(t *testing.T) {
		other := *earlier
		other.Environment = nil
		err := checkOnlyError(t, loaded, killer(), func(o *doctor.Options) { o.Reuse = &other })
		if !strings.Contains(err.Error(), "earlier report carries no environment fingerprint") {
			t.Errorf("the refusal is %q", err)
		}
	})
	t.Run("no reference run to take", func(t *testing.T) {
		other := *earlier
		other.Runs = nil
		for _, run := range earlier.Runs {
			if run.Kind == doctor.RunMutant {
				other.Runs = append(other.Runs, run)
			}
		}
		err := checkOnlyError(t, loaded, killer(), func(o *doctor.Options) { o.Reuse = &other })
		if !strings.Contains(err.Error(), "holds no reference run to reuse") {
			t.Errorf("the refusal is %q", err)
		}
	})
}

// TestReuseRefusesOnlyReference: run only the reference, and do not run the
// reference. Neither reading is safe to guess at.
func TestReuseRefusesOnlyReference(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	earlier := check(t, loaded, passingReference(killer(), 3))
	err := checkOnlyError(t, loaded, killer(), func(o *doctor.Options) {
		o.Reuse = earlier
		o.Only = doctor.Only{doctor.OnlyReference}
	})
	if !strings.Contains(err.Error(), "drop one") {
		t.Errorf("the refusal is %q", err)
	}
}

// TestReuseWithOneMutant is the seven-minute loop end to end: a reused
// reference block and exactly one mutant.
func TestReuseWithOneMutant(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	earlier := check(t, loaded, passingReference(killer(), 3))
	report, seen := checkOnly(t, loaded, killer(), func(o *doctor.Options) {
		o.Reuse = earlier
		o.Only = doctor.Only{"times"}
	})
	if strings.Join(seen, ",") != "mutant-0-times" {
		t.Errorf("verified %v, want one mutant", seen)
	}
	if report.Aggregates.ReferenceRuns != 3 || report.Aggregates.Killed != 1 {
		t.Errorf("aggregates = %+v, want 3 reference runs and 1 kill", report.Aggregates)
	}
	if !report.Aggregates.KillRateMeaningful {
		t.Error("the rate is not evidence, though a reference block was reused")
	}
	if report.Verdict != vocab.HealthHealthy {
		t.Errorf("verdict = %q, want healthy\n%v", report.Verdict, report.Summary())
	}
}

// TestReuseSurvivesTheRoundTrip: the report is written and read back, so the
// new fields are on the file and not only in memory.
func TestReuseSurvivesTheRoundTrip(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	earlier := check(t, loaded, passingReference(killer(), 3))
	dir := filepath.Join(t.TempDir(), "out")
	report, err := doctor.Check(t.Context(), doctor.Options{
		Task: loaded, Runner: killer(), Dir: dir, Parallel: 2,
		RunID: "20260101T000001Z-0000beef", Reuse: earlier,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	again, err := doctor.ReadReport(filepath.Join(dir, doctor.ReportFile))
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if again.Environment == nil || again.Environment.SHA256 != report.Environment.SHA256 {
		t.Error("the fingerprint did not survive the round trip")
	}
	from, ok := again.ReusedReference()
	if !ok || from != earlier.RunID {
		t.Errorf("reused_from did not survive the round trip: %q (%v)", from, ok)
	}
	// A replay of the read-back report concludes the same way, so a reused
	// block is an ordinary reference run to everything downstream.
	again.Conclude()
	if again.Verdict != report.Verdict {
		t.Errorf("a replay concluded %q, want %q", again.Verdict, report.Verdict)
	}
}
