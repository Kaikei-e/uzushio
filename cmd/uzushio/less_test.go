package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
)

// doctorInto runs a check into a fresh output directory and returns the
// report's decoded JSON along with the command result.
func doctorInto(t *testing.T, dir string, args ...string) (result, map[string]any, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	got := run(t, append([]string{"task", "doctor", "--task", dir, "--out", out}, args...)...)
	report := filepath.Join(out, "report.json")
	body, err := os.ReadFile(report)
	if err != nil {
		return got, nil, ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	return got, decoded, report
}

// runLabels is the labels of the runs a report holds, in order.
func runLabels(t *testing.T, report map[string]any) []string {
	t.Helper()
	runs, ok := report["runs"].([]any)
	if !ok {
		t.Fatalf("the report holds no runs: %v", report)
	}
	var out []string
	for _, entry := range runs {
		out = append(out, entry.(map[string]any)["label"].(string))
	}
	return out
}

// TestDoctorOnlyReference is the drift check at the command level.
func TestDoctorOnlyReference(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	// reference_runs is 2 in this fixture, and it stays the task's choice: the
	// flag selects which verifications run, never how many.
	got, report, _ := doctorInto(t, dir, "--only", "reference")
	if got.code != exitInconclusive {
		t.Fatalf("exit = %d, want %d (no mutant ran, so there is no rate); stderr = %s",
			got.code, exitInconclusive, got.stderr)
	}
	if labels := runLabels(t, report); strings.Join(labels, ",") != "reference-1,reference-2" {
		t.Errorf("runs = %v, want the two reference runs", labels)
	}
}

// TestDoctorOnlyOneMutant is the loop the flag exists for: a mutant was added,
// and the question is about that mutant.
func TestDoctorOnlyOneMutant(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	got, report, _ := doctorInto(t, dir, "--only", "0001-times")
	if got.code != exitInconclusive {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	if labels := runLabels(t, report); strings.Join(labels, ",") != "mutant-0-0001-times" {
		t.Errorf("runs = %v, want one mutant", labels)
	}
}

// TestDoctorOnlyRepeats: the flag is a set.
func TestDoctorOnlyRepeats(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	got, report, _ := doctorInto(t, dir, "--only", "reference-1", "--only", "mutants")
	if got.code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitOK, got.stderr)
	}
	if labels := runLabels(t, report); strings.Join(labels, ",") != "reference-1,mutant-0-0001-times" {
		t.Errorf("runs = %v", labels)
	}
}

// TestDoctorOnlyRefusesATypo is exit 2 rather than a green report about a
// mutant nobody verified.
func TestDoctorOnlyRefusesATypo(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	got, _, _ := doctorInto(t, dir, "--only", "0001-timez")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	for _, want := range []string{"0001-timez", "matched nothing", "0001-times"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
		}
	}
}

// TestDoctorReportCarriesTheFingerprint: every report gains one, so every
// report can be reused from.
func TestDoctorReportCarriesTheFingerprint(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	got, report, _ := doctorInto(t, dir)
	if got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	environment, ok := report["environment"].(map[string]any)
	if !ok {
		t.Fatalf("the report carries no environment: %v", report)
	}
	if sha, _ := environment["sha256"].(string); len(sha) != 64 {
		t.Errorf("sha256 = %q, want a digest", sha)
	}
	components, _ := environment["components"].([]any)
	if len(components) == 0 {
		t.Errorf("components = %v, want what was hashed", components)
	}
}

// TestDoctorReuseReference is the seven-minute loop at the command level.
func TestDoctorReuseReference(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	first, _, earlier := doctorInto(t, dir)
	if first.code != exitOK {
		t.Fatalf("the first check exited %d; stderr = %s", first.code, first.stderr)
	}

	got, report, _ := doctorInto(t, dir, "--reuse-reference", earlier, "--only", "mutants")
	if got.code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitOK, got.stderr)
	}
	labels := runLabels(t, report)
	if strings.Join(labels, ",") != "reference-1,reference-2,mutant-0-0001-times" {
		t.Errorf("runs = %v, want the reused reference block and the mutant", labels)
	}
	runs := report["runs"].([]any)
	for i := range 2 {
		run := runs[i].(map[string]any)
		if run["reused_from"] == nil {
			t.Errorf("%v is not marked as reused", run["label"])
		}
	}
	if runs[2].(map[string]any)["reused_from"] != nil {
		t.Error("the mutant is marked as reused")
	}
	// The rate is evidence, because a reference block that passed is what
	// makes it one.
	aggregates := report["aggregates"].(map[string]any)
	if aggregates["kill_rate_meaningful"] != true {
		t.Errorf("kill_rate_meaningful = %v, want true", aggregates["kill_rate_meaningful"])
	}
	if !strings.Contains(got.stderr, "reference runs reused from ") {
		t.Errorf("the summary does not say the reference was reused:\n%s", got.stderr)
	}
}

// TestDoctorReuseRefusesAChangedVerifier: the fingerprint is what makes reuse
// honest rather than convenient.
func TestDoctorReuseRefusesAChangedVerifier(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	// The fingerprint covers what the compose service mounts, so a file has to
	// be mounted before editing it means anything.
	writeFile(t, filepath.Join(dir, "compose.yaml"), `services:
  verify:
    image: example:1
    volumes:
      - ./verify.sh:/verify.sh:ro
`)
	writeFile(t, filepath.Join(dir, "verify.sh"), "echo before\n")
	first, _, earlier := doctorInto(t, dir)
	if first.code != exitOK {
		t.Fatalf("the first check exited %d; stderr = %s", first.code, first.stderr)
	}

	writeFile(t, filepath.Join(dir, "verify.sh"), "echo after\n")
	got, _, _ := doctorInto(t, dir, "--reuse-reference", earlier)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	for _, want := range []string{"cannot be reused", "changed on disk"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
		}
	}
}

// TestDoctorReuseRefusesOnlyReference: two instructions that cancel.
func TestDoctorReuseRefusesOnlyReference(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	first, _, earlier := doctorInto(t, dir)
	if first.code != exitOK {
		t.Fatalf("the first check exited %d; stderr = %s", first.code, first.stderr)
	}
	got, _, _ := doctorInto(t, dir, "--reuse-reference", earlier, "--only", "reference")
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "drop one") {
		t.Errorf("stderr does not say what to do:\n%s", got.stderr)
	}
}

// TestDoctorReuseRecordsTheReuseInTheVault: the record outlives the report, and
// it is the record that says the runs in it are older than the check.
func TestDoctorReuseRecordsTheReuseInTheVault(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	first, firstReport, earlier := doctorInto(t, dir)
	if first.code != exitOK {
		t.Fatalf("the first check exited %d; stderr = %s", first.code, first.stderr)
	}
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	got := run(t, "task", "doctor", "--task", dir, "--out", out,
		"--vault", root, "--reuse-reference", earlier)
	if got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(root, "spec", "verifiers"))
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "spec", "verifiers", entries[0].Name()))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	want := "reference runs reused from " + firstReport["run_id"].(string)
	if !strings.Contains(string(body), want) {
		t.Errorf("the record does not carry %q:\n%s", want, body)
	}
}

// TestDoctorReplayRefusesTheNewRunFlags: a replay runs nothing, so a flag that
// says what to run has nothing to do.
func TestDoctorReplayRefusesTheNewRunFlags(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	first, _, report := doctorInto(t, dir)
	if first.code != exitOK {
		t.Fatalf("the first check exited %d; stderr = %s", first.code, first.stderr)
	}
	for _, args := range [][]string{
		{"--only", "reference"},
		{"--reuse-reference", report},
	} {
		got := run(t, append([]string{
			"task", "doctor", "--task", dir, "--replay", report,
		}, args...)...)
		if got.code != exitUsage {
			t.Errorf("%v: exit = %d, want %d; stderr = %s", args, got.code, exitUsage, got.stderr)
		}
		if !strings.Contains(got.stderr, "drop one of the two") {
			t.Errorf("%v: stderr does not say what to do:\n%s", args, got.stderr)
		}
	}
}
