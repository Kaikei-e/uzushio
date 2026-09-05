package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
)

// newBandedTaskDir builds a task with a banded verifier and writes a doctor
// report of `runs` reference runs beside it.
//
// The bands the runs were judged against are on the rows, which is the only
// place calibrate looks: nothing here writes a tolerances file of any kind,
// because knowing what one looks like is the task's business and not the
// tool's.
func newBandedTaskDir(t *testing.T, runs int) (dir, reportPath string) {
	t.Helper()
	dir = t.TempDir()
	repo := filepath.Join(dir, "repo")
	writeFile(t, filepath.Join(repo, "add.go"), seed)
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "seed")
	rev := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))

	writeFile(t, filepath.Join(dir, "reference.diff"), "")
	writeFile(t, filepath.Join(dir, "task.json"), `{
  "version": 2,
  "id": "banded",
  "repo": "repo",
  "rev": "`+rev+`",
  "files": ["add.go"],
  "verify": {"compose_file": "compose.yaml", "service": "verify", "kind": "band"},
  "reference": {"diff": "reference.diff"},
  "doctor": {"kill_rate_min": 0.8, "reference_runs": 5}
}
`)

	floor := []float64{8.7119, 8.7684, 8.6935, 8.7986, 8.8488, 8.7500, 8.8000}
	report := &doctor.Report{
		SchemaVersion: doctor.SchemaVersion,
		RunID:         "20260905T002350Z-e13205e7",
		Task:          "banded",
		Rev:           rev,
		StartedAt:     "2026-09-05T00:23:50Z",
	}
	for i := range runs {
		report.Runs = append(report.Runs, doctor.Run{
			Label:  "reference-" + string(rune('1'+i)),
			Kind:   doctor.RunReference,
			Status: verifyrunner.StatusFail,
			Band: &verifyrunner.Band{Rows: []verifyrunner.BandRow{
				// out of band on every run
				{Invariant: "dispatch_floor_us", Value: number(floor[i]), CIHalf: number(0.2),
					BandLo: number(2.0), BandHi: number(4.6), Verdict: "fail"},
				// inside its band on every run
				{Invariant: "steady_tail_p50_ms", Value: number(0.05 + float64(i)*0.001),
					BandLo: number(0.04), BandHi: number(0.20), Verdict: "pass"},
				// a ratio against a computed expectation
				{Invariant: "enforce_allowed_ratio", Value: number(0.9166),
					BandLo: number(0.90), BandHi: number(1.10), Verdict: "pass"},
			}},
		})
	}
	body, err := report.Bytes()
	if err != nil {
		t.Fatalf("encode the report: %v", err)
	}
	reportPath = filepath.Join(dir, "report.json")
	writeFile(t, reportPath, string(body))
	return dir, reportPath
}

// readBands decodes the bands file a run wrote.
func readBands(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no bands file: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the bands file is not JSON: %v\n%s", err, body)
	}
	return decoded
}

// TestCalibrateWritesTheBands is the command end to end.
func TestCalibrateWritesTheBands(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	got := run(t, "task", "calibrate", "--task", dir, "--from", report,
		"--keep", "enforce_allowed_ratio")
	if got.code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", got.code, got.stderr)
	}
	bands := readBands(t, filepath.Join(dir, "bands.json"))
	if bands["schema_version"] != float64(1) || bands["task"] != "banded" || bands["n"] != float64(5) {
		t.Errorf("the header is %v", bands)
	}
	if bands["day"] != "2026-09-05" {
		t.Errorf("day = %v", bands["day"])
	}
	invariants, ok := bands["invariants"].(map[string]any)
	if !ok || len(invariants) != 3 {
		t.Fatalf("invariants = %v", bands["invariants"])
	}
	derived := invariants["dispatch_floor_us"].(map[string]any)
	if derived["kept"] != false || derived["reason"] != "derived: k*stdev" {
		t.Errorf("dispatch_floor_us = %v", derived)
	}
	kept := invariants["enforce_allowed_ratio"].(map[string]any)
	if kept["kept"] != true || kept["reason"] != "kept: declared" {
		t.Errorf("enforce_allowed_ratio = %v", kept)
	}
	if kept["half_width"] != nil {
		t.Errorf("a kept band carries a half_width: %v", kept["half_width"])
	}
	steady := invariants["steady_tail_p50_ms"].(map[string]any)
	if steady["kept"] != true || steady["reason"] != "kept: in band" {
		t.Errorf("steady_tail_p50_ms = %v", steady)
	}
	// stderr carries the comparison a person reads, including the guide the
	// rule does not use.
	for _, want := range []string{"original band", "derived band", "3x ci_half", "re-centred 1:", "wrote: "} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not carry %q:\n%s", want, got.stderr)
		}
	}
}

// TestCalibrateDryRunPrintsAndWritesNothing.
func TestCalibrateDryRunPrintsAndWritesNothing(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	got := run(t, "task", "calibrate", "--task", dir, "--from", report,
		"--keep", "enforce_allowed_ratio", "--dry-run")
	if got.code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, `"dispatch_floor_us"`) {
		t.Errorf("the file did not go to stdout:\n%s", got.stdout)
	}
	if !strings.Contains(got.stderr, "dry run: nothing written") {
		t.Errorf("stderr does not say it wrote nothing:\n%s", got.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "bands.json")); err == nil {
		t.Error("--dry-run wrote bands.json")
	}
	// Everything a pipe reads is the file, and the reasoning is on stderr.
	if strings.Contains(got.stdout, "derived band") {
		t.Errorf("the comparison table went to stdout:\n%s", got.stdout)
	}
}

func TestCalibrateRefusesTooFewRuns(t *testing.T) {
	dir, report := newBandedTaskDir(t, 4)
	got := run(t, "task", "calibrate", "--task", dir, "--from", report)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "--min-runs is 5") {
		t.Errorf("stderr does not name the threshold:\n%s", got.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "bands.json")); err == nil {
		t.Error("a refused calibration still wrote bands.json")
	}
}

func TestCalibrateMinRunsIsRaisable(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	got := run(t, "task", "calibrate", "--task", dir, "--from", report, "--min-runs", "7")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "--min-runs is 7") {
		t.Errorf("stderr does not name the raised threshold:\n%s", got.stderr)
	}
}

// TestCalibrateBelowTheToleranceTable: lowering --min-runs does not conjure a
// tolerance factor, and the refusal says what to do instead.
func TestCalibrateBelowTheToleranceTable(t *testing.T) {
	dir, report := newBandedTaskDir(t, 4)
	got := run(t, "task", "calibrate", "--task", dir, "--from", report, "--min-runs", "3")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "--k") {
		t.Errorf("the refusal does not offer the way out:\n%s", got.stderr)
	}
	got = run(t, "task", "calibrate", "--task", dir, "--from", report,
		"--min-runs", "3", "--k", "3", "--dry-run")
	if got.code != exitOK {
		t.Fatalf("exit = %d with --k 3, stderr = %s", got.code, got.stderr)
	}
}

// TestCalibrateKeepRefusesATypo: a mistyped --keep means the invariant it was
// meant to protect got re-centred.
func TestCalibrateKeepRefusesATypo(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	got := run(t, "task", "calibrate", "--task", dir, "--from", report, "--keep", "enforce_ratio")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	for _, want := range []string{"enforce_ratio", "did not judge", "enforce_allowed_ratio"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
		}
	}
}

func TestCalibrateNeedsAReport(t *testing.T) {
	dir, _ := newBandedTaskDir(t, 5)
	got := run(t, "task", "calibrate", "--task", dir)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d", got.code, exitUsage)
	}
	if !strings.Contains(got.stderr, "--from") {
		t.Errorf("stderr does not name the missing flag:\n%s", got.stderr)
	}
}

func TestCalibrateRejectsABadRule(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	for _, args := range [][]string{
		{"--lower", "semantic"},
		{"--centre", "mode"},
		{"--spread", "iqr"},
	} {
		got := run(t, append([]string{"task", "calibrate", "--task", dir, "--from", report}, args...)...)
		if got.code != exitUsage {
			t.Errorf("%v: exit = %d, want %d", args, got.code, exitUsage)
		}
	}
}

func TestCalibrateOutGoesWhereItIsTold(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	got := run(t, "task", "calibrate", "--task", dir, "--from", report,
		"--keep", "enforce_allowed_ratio", "--out", target)
	if got.code != exitOK {
		t.Fatalf("exit = %d, stderr = %s", got.code, got.stderr)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("nothing at --out: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bands.json")); err == nil {
		t.Error("--out was given and the default was written too")
	}
}

// TestCalibrateRefusesAnUnreadableReport: a report of another schema is not one
// this build may derive a band from.
func TestCalibrateRefusesAnUnreadableReport(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["schema_version"] = 99
	changed, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, report, string(changed))
	got := run(t, "task", "calibrate", "--task", dir, "--from", report)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "schema_version") {
		t.Errorf("stderr does not say why:\n%s", got.stderr)
	}
}

// TestCalibrateReadsNothingFromTheRepository is the boundary this phase is
// about: the bands come from the reports, so a task whose repository holds no
// band file of any kind still calibrates.
func TestCalibrateReadsNothingFromTheRepository(t *testing.T) {
	dir, report := newBandedTaskDir(t, 5)
	if err := os.RemoveAll(filepath.Join(dir, "repo")); err != nil {
		t.Fatal(err)
	}
	got := run(t, "task", "calibrate", "--task", dir, "--from", report,
		"--keep", "enforce_allowed_ratio", "--dry-run")
	if got.code != exitOK {
		t.Fatalf("exit = %d without a repository, stderr = %s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, `"dispatch_floor_us"`) {
		t.Errorf("no bands were produced:\n%s", got.stdout)
	}
}

func TestTaskHelpNamesCalibrate(t *testing.T) {
	got := run(t, "task")
	if !strings.Contains(got.stdout, "calibrate") {
		t.Errorf("the task help does not name calibrate:\n%s", got.stdout)
	}
}
