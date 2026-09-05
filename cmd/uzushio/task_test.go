package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
)

const (
	seed   = "package hello\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"
	solved = "package hello\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	times  = "package hello\n\nfunc Add(a, b int) int {\n\treturn a * b\n}\n"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=uzushio", "-c", "user.email=uzushio@example.com",
	}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newTaskDir builds a version 2 task with a real git repository, one reference
// diff and one mutant.
func newTaskDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	writeFile(t, filepath.Join(repo, "add.go"), seed)
	writeFile(t, filepath.Join(repo, "go.mod"), "module example.com/hello\n\ngo 1.27\n")
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "seed")

	writeFile(t, filepath.Join(repo, "add.go"), solved)
	writeFile(t, filepath.Join(dir, "reference.diff"), git(t, repo, "diff"))
	git(t, repo, "add", "add.go")
	writeFile(t, filepath.Join(repo, "add.go"), times)
	writeFile(t, filepath.Join(dir, "mutants", "0001-times.diff"), git(t, repo, "diff"))
	git(t, repo, "reset", "--hard", "--quiet", "HEAD")

	writeFile(t, filepath.Join(dir, "task.json"), `{
  "version": 2,
  "id": "hello",
  "repo": "repo",
  "rev": "HEAD",
  "files": ["add.go"],
  "reference": {"diff": "reference.diff"},
  "mutants": [{"diff": "mutants/0001-times.diff", "expect": "killed", "origin": "hand"}],
  "doctor": {"kill_rate_min": 0.8, "reference_runs": 2}
}
`)
	return dir
}

// canned answers every verification with one status, which is all the exit
// codes need. It replaces the exec runner through newRunner, so the command
// runs end to end without docker and without a cmoa on PATH.
type canned struct {
	status verifyrunner.Status
	mutant verifyrunner.Status
	// band is what a banded verifier reported, attached to every answer. Nil
	// is an exit-code verifier, which reports none.
	band *verifyrunner.Band
}

func (c canned) Verify(_ context.Context, req verifyrunner.Request) (verifyrunner.Result, error) {
	status := c.status
	if strings.HasPrefix(req.Label, "mutant-") {
		status = c.mutant
	}
	exit := 0
	if status != verifyrunner.StatusPass {
		exit = 1
	}
	return verifyrunner.Result{
		SchemaVersion: 1, Task: "hello", Label: req.Label, Status: status,
		Band:     c.band,
		ExitCode: exit, DurationMS: 5, CMoAVersion: "v0.0.0-test",
	}, nil
}

// number is a JSON number that may be absent, for a band row written by hand.
func number(f float64) *float64 { return &f }

func withRunner(t *testing.T, runner verifyrunner.Runner) {
	t.Helper()
	previous := newRunner
	newRunner = func(string) verifyrunner.Runner { return runner }
	t.Cleanup(func() { newRunner = previous })
}

// TestDoctorExitCodes is the contract a CI step reads. The three verdicts are
// three different things to do about a task, so they are three codes.
func TestDoctorExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		runner canned
		want   int
	}{
		{
			name:   "healthy",
			runner: canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail},
			want:   exitOK,
		},
		{
			name:   "the reference is rejected",
			runner: canned{status: verifyrunner.StatusFail, mutant: verifyrunner.StatusFail},
			want:   exitFailure,
		},
		{
			name:   "a mutant survives",
			runner: canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusPass},
			want:   exitFailure,
		},
		{
			name:   "nothing answered",
			runner: canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusTimeout},
			want:   exitInconclusive,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withRunner(t, tt.runner)
			dir := newTaskDir(t)
			got := run(t, "task", "doctor", "--task", dir, "--json")
			if got.code != tt.want {
				t.Fatalf("exit = %d, want %d; stderr = %s", got.code, tt.want, got.stderr)
			}
			// --json prints the report and nothing else on stdout.
			var report map[string]any
			if err := json.Unmarshal([]byte(got.stdout), &report); err != nil {
				t.Fatalf("stdout is not the report: %v\n%s", err, got.stdout)
			}
			if report["schema_version"] != float64(1) {
				t.Fatalf("report = %v", report)
			}
			// The human summary goes to stderr, and it says what it concluded.
			if !strings.Contains(got.stderr, "verdict: ") {
				t.Errorf("stderr does not carry a verdict:\n%s", got.stderr)
			}
			// The verdict is the exit code's reason, so it is not repeated as
			// an "Error:" line after the summary that explained it.
			if strings.Contains(got.stderr, "Error:") {
				t.Errorf("the verdict was printed as an error:\n%s", got.stderr)
			}
		})
	}
}

// TestDoctorRefusesAVersionOneTask is a task error rather than a verdict, so
// it is exit 2 and it says what is missing.
// TestDoctorRecordsNoReportForAnOutsideOut is M-2 at the command level. A
// record is committed to somebody's repository, and a report kept outside the
// task can only be named absolutely — so it is not named at all, and the
// command says why.
func TestDoctorRecordsNoReportForAnOutsideOut(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "elsewhere")
	got := run(t, "task", "doctor", "--task", dir, "--vault", root, "--out", out)
	if got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "outside the task directory") {
		t.Errorf("stderr does not warn:\n%s", got.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(root, "spec", "verifiers"))
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "spec", "verifiers", entries[0].Name()))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	if strings.Contains(string(body), "report:") {
		t.Errorf("the record names a report it cannot name relative to the task:\n%s", body)
	}
	if strings.Contains(string(body), out) {
		t.Errorf("the record carries an absolute path:\n%s", body)
	}
}

// TestDoctorRefusesAReusedOut is L-2: `cmoa verify --out` will not overwrite a
// result.json, so a second check into one directory would come back
// runner_error on every run and read as inconclusive.
func TestDoctorRefusesAReusedOut(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	out := filepath.Join(t.TempDir(), "out")
	if got := run(t, "task", "doctor", "--task", dir, "--out", out); got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	got := run(t, "task", "doctor", "--task", dir, "--out", out)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "already holds a check") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

// TestDoctorTimeout bounds a check of uzushio's own. Without one, a task that
// declares no verifier timeout and a cmoa that hangs hang the command.
func TestDoctorTimeout(t *testing.T) {
	withRunner(t, slow{})
	dir := newTaskDir(t)
	got := run(t, "task", "doctor", "--task", dir, "--timeout", "50ms")
	// The runs come back as runner errors, so the check is inconclusive
	// rather than a verdict about the verifier.
	if got.code != exitInconclusive {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitInconclusive, got.stderr)
	}
}

// slow never answers before the context gives up.
type slow struct{}

func (slow) Verify(ctx context.Context, _ verifyrunner.Request) (verifyrunner.Result, error) {
	<-ctx.Done()
	return verifyrunner.Result{}, ctx.Err()
}

func TestDoctorRefusesAVersionOneTask(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "repo", "add.go"), seed)
	writeFile(t, filepath.Join(dir, "task.json"),
		`{"version": 1, "id": "hello", "repo": "repo", "files": ["add.go"]}`)
	got := run(t, "task", "doctor", "--task", dir)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "version 2 with a reference") {
		t.Errorf("stderr = %q, which does not say what is missing", got.stderr)
	}
}

// bandTaskDir is newTaskDir with verify.kind set to band.
func bandTaskDir(t *testing.T) string {
	t.Helper()
	dir := newTaskDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	writeFile(t, filepath.Join(dir, "task.json"), strings.Replace(string(body),
		`"reference":`, `"verify": {"kind": "band"},
  "reference":`, 1))
	return dir
}

// killedBand is the answer a banded verifier gives about a mutant it caught:
// one invariant out of band, one it could not measure at all.
var killedBand = &verifyrunner.Band{
	Judged:  2,
	Failed:  []string{"rr_spread_req"},
	Skipped: []string{"ratelimit_tax_us"},
	Rows: []verifyrunner.BandRow{
		{Invariant: "rr_spread_req", Value: number(120000), BandLo: number(0), BandHi: number(0), Verdict: "fail"},
		{Invariant: "ratelimit_tax_us", BandLo: number(2.2), BandHi: number(4.2), Verdict: "skipped"},
	},
}

// TestDoctorRunsABandVerifier is what changed when band stopped being reserved:
// the check runs, and the rows the verifier measured reach the report, so a
// reader learns which invariant answered rather than only that one did.
func TestDoctorRunsABandVerifier(t *testing.T) {
	withRunner(t, canned{
		status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail, band: killedBand,
	})
	dir := bandTaskDir(t)
	out := filepath.Join(t.TempDir(), "check")
	got := run(t, "task", "doctor", "--task", dir, "--out", out)
	if got.code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitOK, got.stderr)
	}
	// Not asked for, and printed anyway: --parallel was left at its default,
	// and a banded verifier's numbers are only worth reading one at a time.
	if !strings.Contains(got.stderr, "verify.kind is band, so this check runs 1 verification at a time") {
		t.Errorf("stderr = %q, which does not say the check was held to one at a time", got.stderr)
	}
	body, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	var report struct {
		Runs []struct {
			Kind string             `json:"kind"`
			Band *verifyrunner.Band `json:"band"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode report.json: %v", err)
	}
	for _, r := range report.Runs {
		if r.Band == nil {
			t.Fatalf("the %s run carries no band; report = %s", r.Kind, body)
		}
		if len(r.Band.Rows) != 2 || r.Band.Rows[0].Invariant != "rr_spread_req" {
			t.Fatalf("the %s run's band rows = %+v", r.Kind, r.Band.Rows)
		}
		if r.Band.Rows[1].Value != nil {
			t.Errorf("a skipped row carries a value %v; it measured nothing", *r.Band.Rows[1].Value)
		}
	}
}

// TestDoctorKeepsAnExplicitParallel is the other half of the rule: a person who
// typed --parallel meant it, and is told what it costs rather than overruled.
func TestDoctorKeepsAnExplicitParallel(t *testing.T) {
	withRunner(t, canned{
		status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail, band: killedBand,
	})
	got := run(t, "task", "doctor", "--task", bandTaskDir(t), "--parallel", "3")
	if got.code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitOK, got.stderr)
	}
	if !strings.Contains(got.stderr, "--parallel 3 on a task whose verify.kind is band") {
		t.Errorf("stderr = %q, which does not warn about the parallelism asked for", got.stderr)
	}
	if strings.Contains(got.stderr, "runs 1 verification at a time") {
		t.Errorf("stderr = %q: the flag was typed, so it is not overruled", got.stderr)
	}
}

// TestDoctorRecordsInAVault checks the one side effect the command has beyond
// its report.
func TestDoctorRecordsInAVault(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	root := t.TempDir()
	got := run(t, "task", "doctor", "--task", dir, "--vault", root)
	if got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(root, "spec", "verifiers"))
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "hello@") {
		t.Fatalf("the vault holds %v", entries)
	}
	body, err := os.ReadFile(filepath.Join(root, "spec", "verifiers", entries[0].Name()))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	// The report path is relative to the task directory, so the record travels
	// with the task rather than with the machine that ran the check.
	if !strings.Contains(string(body), "report: doctor/") {
		t.Errorf("the record does not name the report relative to the task:\n%s", body)
	}
}

func TestMutateDryRun(t *testing.T) {
	dir := newTaskDir(t)
	before, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	got := run(t, "task", "mutate", "--task", dir, "--dry-run")
	if got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "mutants/0002-") {
		t.Fatalf("stdout does not list what would be written:\n%s", got.stdout)
	}
	after, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	if string(before) != string(after) {
		t.Error("--dry-run rewrote task.json")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "mutants"))
	if err != nil {
		t.Fatalf("read mutants: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("--dry-run wrote %v", entries)
	}
}

func TestMutateWrites(t *testing.T) {
	dir := newTaskDir(t)
	got := run(t, "task", "mutate", "--task", dir, "--operators", "arith")
	if got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "mutants"))
	if err != nil {
		t.Fatalf("read mutants: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("mutants = %v, want the hand one and one generated", entries)
	}
	body, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	if !strings.Contains(string(body), `"origin": "generated"`) ||
		!strings.Contains(string(body), `"operator": "arith"`) {
		t.Fatalf("task.json does not carry the generated mutant:\n%s", body)
	}
	// A second run finds the same site and the same diff, so it writes
	// nothing.
	again := run(t, "task", "mutate", "--task", dir, "--operators", "arith")
	if again.code != exitOK {
		t.Fatalf("the second run exited %d; stderr = %s", again.code, again.stderr)
	}
	if !strings.Contains(again.stderr, "no new mutants") {
		t.Errorf("the second run wrote something:\n%s", again.stderr)
	}
}

// TestMutateChecksTheManifestBeforeWritingAnything is M-1. The rewrite refuses
// a manifest holding a key uzushio would drop; discovering that after the diffs
// were written left them on disk, and the retry after the key was removed wrote
// every one of them again under a new number — the same mutant twice, weighted
// twice in the kill rate.
func TestMutateChecksTheManifestBeforeWritingAnything(t *testing.T) {
	dir := newTaskDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	writeFile(t, filepath.Join(dir, "task.json"),
		strings.Replace(string(body), `"version": 2,`, `"version": 2,
  "something_cmoa_added_later": {"a": 1},`, 1))

	got := run(t, "task", "mutate", "--task", dir)
	if got.code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "something_cmoa_added_later") {
		t.Errorf("stderr = %q, which does not name the key", got.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "mutants"))
	if err != nil {
		t.Fatalf("read mutants: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("the refused run wrote %v; only the hand-written mutant should be there", entries)
	}
}

// TestMutateDoesNotRegenerateAnOrphan is the other half of M-1: a diff on disk
// that the manifest does not declare is still a mutant that exists, so the
// dedupe reads the directory as well as the manifest.
func TestMutateDoesNotRegenerateAnOrphan(t *testing.T) {
	dir := newTaskDir(t)
	if got := run(t, "task", "mutate", "--task", dir); got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	before, err := os.ReadDir(filepath.Join(dir, "mutants"))
	if err != nil {
		t.Fatalf("read mutants: %v", err)
	}
	// Take every generated mutant back out of the manifest, leaving the diffs
	// behind — which is what an interrupted run leaves.
	body, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatalf("read task.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("decode task.json: %v", err)
	}
	mutants, _ := manifest["mutants"].([]any)
	manifest["mutants"] = mutants[:1]
	rewritten, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode task.json: %v", err)
	}
	writeFile(t, filepath.Join(dir, "task.json"), string(rewritten)+"\n")

	again := run(t, "task", "mutate", "--task", dir)
	if again.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", again.code, again.stderr)
	}
	after, err := os.ReadDir(filepath.Join(dir, "mutants"))
	if err != nil {
		t.Fatalf("read mutants: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("the second run wrote %d more mutant(s): %v", len(after)-len(before), after)
	}
}

func TestMutateRejectsAnUnknownOperatorAndLanguage(t *testing.T) {
	dir := newTaskDir(t)
	for _, args := range [][]string{
		{"task", "mutate", "--task", dir, "--operators", "nonesuch"},
		{"task", "mutate", "--task", dir, "--lang", "rust"},
		{"task", "mutate", "--task", dir, "--max", "0"},
		{"task", "mutate", "--task", dir, "--max", "-1"},
	} {
		got := run(t, args...)
		if got.code != exitUsage {
			t.Fatalf("%v exited %d, want %d; stderr = %s", args, got.code, exitUsage, got.stderr)
		}
	}
}

// TestTaskAloneIsAUsageError records that `task` is a parent rather than a
// command: it runs nothing, so invoking it is a mistake worth an exit code.
func TestTaskAloneIsAUsageError(t *testing.T) {
	if got := run(t, "task"); got.code == exitOK {
		t.Fatalf("`uzushio task` succeeded, printing %q", got.stdout+got.stderr)
	}
}

// TestTheContextReachesTheCommand is what signal handling rests on at the
// command level. Execute cancels the context on an interrupt; if the tree did
// not carry it down, Ctrl-C would end the process without running a single
// deferred function and the git worktree would stay registered.
func TestTheContextReachesTheCommand(t *testing.T) {
	dir := newTaskDir(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := runContext(t, ctx, "task", "mutate", "--task", dir)
	if got.code == exitOK {
		t.Fatalf("mutate succeeded under a cancelled context: %s%s", got.stdout, got.stderr)
	}
	if listed := git(t, filepath.Join(dir, "repo"), "worktree", "list"); strings.Count(listed, "\n") != 1 {
		t.Errorf("a worktree was left registered:\n%s", listed)
	}
}

// --- replay -----------------------------------------------------------------

// TestDoctorReplayRecomputesInPlace is the whole of what a replay is for: a
// report written by an older build carries the runs but not the conclusions
// this build draws from them, and the runs are all a conclusion needs.
func TestDoctorReplayRecomputesInPlace(t *testing.T) {
	withRunner(t, canned{
		status: verifyrunner.StatusFail, mutant: verifyrunner.StatusFail, band: killedBand,
	})
	dir := bandTaskDir(t)
	out := filepath.Join(t.TempDir(), "check")
	// A live check first, to produce a report honestly. Its reference failed,
	// so it is the case the new fields exist for.
	if got := run(t, "task", "doctor", "--task", dir, "--out", out); got.code != exitFailure {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitFailure, got.stderr)
	}
	reportFile := filepath.Join(out, "report.json")
	before, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	var first struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(before, &first); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Strip everything a replay is supposed to put back, the way a report from
	// a build without them would look.
	stripped := strings.NewReplacer(
		`"kill_rate_meaningful": false,`, "",
		`"outcome_note": "reference also failed",`, "",
	).Replace(string(before))
	if stripped == string(before) {
		t.Fatal("the report did not carry the fields a replay recomputes")
	}
	writeFile(t, reportFile, stripped)

	got := run(t, "task", "doctor", "--task", dir, "--replay", reportFile)
	if got.code != exitFailure {
		t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitFailure, got.stderr)
	}
	after, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the replay did not reproduce the live report:\n--- live\n%s\n--- replay\n%s", before, after)
	}
	// The run id is the check's identity. A replay that minted a new one would
	// turn one measurement into two.
	if !strings.Contains(string(after), `"run_id": "`+first.RunID+`"`) {
		t.Errorf("the replay changed the run id")
	}
	if !strings.Contains(got.stderr, "not evidence: the reference itself failed") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

// TestDoctorReplayRecordsAsALiveRunWould checks the record a replay writes: the
// same identifier rule, the report named relative to the task, and a second
// replay landing on the next sequence number rather than overwriting.
func TestDoctorReplayRecordsAsALiveRunWould(t *testing.T) {
	withRunner(t, canned{
		status: verifyrunner.StatusFail, mutant: verifyrunner.StatusFail, band: killedBand,
	})
	dir := bandTaskDir(t)
	out := filepath.Join(dir, "doctor", "run-1")
	if got := run(t, "task", "doctor", "--task", dir, "--out", out); got.code != exitFailure {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	reportFile := filepath.Join(out, "report.json")
	vault := t.TempDir()

	got := run(t, "task", "doctor", "--task", dir, "--replay", reportFile, "--vault", vault)
	if got.code != exitFailure {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	record := findRecord(t, vault)
	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	// The rate is a word, and the report is named relative to the task so the
	// record carries no machine path.
	if !strings.Contains(string(body), "kill_rate: n/a\n") {
		t.Fatalf("the record does not write n/a:\n%s", body)
	}
	if !strings.Contains(string(body), "report: doctor/run-1/report.json") {
		t.Fatalf("the record does not name the report relatively:\n%s", body)
	}
	if strings.Contains(string(body), vault) || strings.Contains(string(body), dir) {
		t.Fatalf("the record carries an absolute path:\n%s", body)
	}
	// A second replay does not overwrite the first record; it takes the next
	// sequence number, exactly as a second live check on one day does.
	if got := run(t, "task", "doctor", "--task", dir, "--replay", reportFile, "--vault", vault); got.code != exitFailure {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(vault, "spec", "verifiers"))
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("the vault holds %d record(s), want 2", len(entries))
	}
	sequenced := false
	for _, entry := range entries {
		sequenced = sequenced || strings.HasSuffix(entry.Name(), "-1.md")
	}
	if !sequenced {
		t.Errorf("neither record took the next sequence number: %v", entries)
	}
}

// findRecord returns the one record in a vault.
func findRecord(t *testing.T, vault string) string {
	t.Helper()
	dir := filepath.Join(vault, "spec", "verifiers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the vault: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("the vault holds %d record(s), want 1", len(entries))
	}
	return filepath.Join(dir, entries[0].Name())
}

// TestDoctorReplayRefusesRunFlags records that the flags saying how to run
// something are refused rather than ignored: a replay runs nothing.
func TestDoctorReplayRefusesRunFlags(t *testing.T) {
	withRunner(t, canned{status: verifyrunner.StatusPass, mutant: verifyrunner.StatusFail})
	dir := newTaskDir(t)
	out := filepath.Join(t.TempDir(), "check")
	if got := run(t, "task", "doctor", "--task", dir, "--out", out); got.code != exitOK {
		t.Fatalf("exit = %d; stderr = %s", got.code, got.stderr)
	}
	reportFile := filepath.Join(out, "report.json")
	for _, flag := range [][]string{
		{"--parallel", "3"},
		{"--timeout", "5s"},
		{"--out", out},
	} {
		args := append([]string{"task", "doctor", "--task", dir, "--replay", reportFile}, flag...)
		got := run(t, args...)
		if got.code != exitUsage {
			t.Errorf("%s: exit = %d, want %d", flag[0], got.code, exitUsage)
		}
		if !strings.Contains(got.stderr, "--replay recomputes a report and verifies nothing") {
			t.Errorf("%s: stderr = %q", flag[0], got.stderr)
		}
	}
}

// TestDoctorReplayRefusesAReportItCannotRead covers the three ways the file is
// not a report: missing, another schema, and one with no runs to recompute.
func TestDoctorReplayRefusesAReportItCannotRead(t *testing.T) {
	dir := newTaskDir(t)
	tmp := t.TempDir()
	tests := []struct {
		name   string
		body   string
		phrase string
	}{
		{"another schema", `{"schema_version": 2, "run_id": "a", "runs": [{"label": "x"}]}`, "schema_version 2"},
		{"no runs", `{"schema_version": 1, "run_id": "a", "runs": []}`, "holds no runs"},
		{"no run id", `{"schema_version": 1, "runs": [{"label": "x"}]}`, "carries no run_id"},
		{"not json", `{`, "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmp, "report.json")
			writeFile(t, path, tt.body)
			got := run(t, "task", "doctor", "--task", dir, "--replay", path)
			if got.code != exitUsage {
				t.Fatalf("exit = %d, want %d; stderr = %s", got.code, exitUsage, got.stderr)
			}
			if !strings.Contains(got.stderr, tt.phrase) {
				t.Errorf("stderr = %q, want it to say %q", got.stderr, tt.phrase)
			}
		})
	}
	got := run(t, "task", "doctor", "--task", dir, "--replay", filepath.Join(tmp, "gone.json"))
	if got.code != exitUsage {
		t.Errorf("a missing report: exit = %d, want %d", got.code, exitUsage)
	}
}
