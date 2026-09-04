package doctor_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kaikei-e/DocDag/lint"
	"github.com/Kaikei-e/DocDag/model"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/vault"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// The task's one file, before and after the reference diff, and the mutants
// cut from the solved version. Each mutant is a diff against the
// reference-applied tree, which is what a task carries in life.
const (
	seed   = "package hello\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"
	solved = "package hello\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
)

var mutantSources = map[string]string{
	"times":    "package hello\n\nfunc Add(a, b int) int {\n\treturn a * b\n}\n",
	"swapped":  "package hello\n\nfunc Add(a, b int) int {\n\treturn b - a\n}\n",
	"zero":     "package hello\n\nfunc Add(a, b int) int {\n\treturn 0\n}\n",
	"minus":    "package hello\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n",
	"comment":  "package hello\n\n// nothing observable changed\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
	"noapply":  "",
	"reversed": "package hello\n\nfunc Add(b, a int) int {\n\treturn b + a\n}\n",
}

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

// mutantSpec is one mutant a test task carries.
type mutantSpec struct {
	name   string
	expect task.Expect
	origin task.Origin
}

// newTask builds a task directory with a real git repository, a reference diff
// and the named mutants, and returns it loaded.
//
// The diffs are git's own: the reference is the difference between the seed
// commit and the solved file, and each mutant is the difference between the
// staged solved file and the mutant's source. Nothing about the health check
// is faked here except the verifier itself.
func newTask(t *testing.T, doctorSpec string, mutants []mutantSpec) *task.Task {
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

	entries := make([]string, 0, len(mutants))
	for _, mutant := range mutants {
		relative := "mutants/" + mutant.name + ".diff"
		if mutant.name == "noapply" {
			// A diff against a file the tree does not hold: it applies to
			// nothing, which is how a mis-authored task looks.
			writeFile(t, filepath.Join(dir, relative),
				"--- a/gone.go\n+++ b/gone.go\n@@ -1,1 +1,1 @@\n-a\n+b\n")
		} else {
			writeFile(t, filepath.Join(repo, "add.go"), mutantSources[mutant.name])
			writeFile(t, filepath.Join(dir, relative), git(t, repo, "diff"))
			git(t, repo, "checkout", "--", "add.go")
		}
		entries = append(entries, `{"diff": "`+relative+`", "expect": "`+string(mutant.expect)+
			`", "origin": "`+string(mutant.origin)+`"}`)
	}
	git(t, repo, "reset", "--hard", "--quiet", "HEAD")

	writeFile(t, filepath.Join(dir, task.ManifestFile), `{
  "version": 2,
  "id": "hello",
  "repo": "repo",
  "rev": "HEAD",
  "files": ["add.go"],
  "reference": {"diff": "reference.diff"},
  "mutants": [`+strings.Join(entries, ",\n    ")+`],
  "doctor": `+doctorSpec+`
}
`)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

// fake answers from a table. A label the table does not name gets the default,
// so a test says only what is unusual about the run it is describing.
type fake struct {
	mu       sync.Mutex
	byLabel  map[string]verifyrunner.Status
	fallback verifyrunner.Status
	failWith map[string]error
	seen     []string
}

func (f *fake) Verify(_ context.Context, req verifyrunner.Request) (verifyrunner.Result, error) {
	f.mu.Lock()
	f.seen = append(f.seen, req.Label)
	f.mu.Unlock()
	if err, ok := f.failWith[req.Label]; ok {
		return verifyrunner.Result{}, err
	}
	status, ok := f.byLabel[req.Label]
	if !ok {
		status = f.fallback
	}
	exit := 0
	if status != verifyrunner.StatusPass {
		exit = 1
	}
	return verifyrunner.Result{
		SchemaVersion: 1,
		Task:          "hello",
		Label:         req.Label,
		Status:        status,
		ExitCode:      exit,
		DurationMS:    7,
		ProjectName:   "cmoa-hello-verify-" + req.Label,
		CMoAVersion:   "v0.0.0-test",
	}, nil
}

// killer answers the way a healthy verifier does: the reference passes and
// every mutant fails.
func killer() *fake {
	return &fake{
		byLabel:  map[string]verifyrunner.Status{},
		fallback: verifyrunner.StatusFail,
	}
}

func check(t *testing.T, loaded *task.Task, runner verifyrunner.Runner) *doctor.Report {
	t.Helper()
	report, err := doctor.Check(t.Context(), doctor.Options{
		Task:     loaded,
		Runner:   runner,
		Dir:      filepath.Join(t.TempDir(), "out"),
		Parallel: 2,
		RunID:    "20260101T000000Z-0000beef",
		Now:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return report
}

var twoHandMutants = []mutantSpec{
	{"times", task.ExpectKilled, task.OriginHand},
	{"swapped", task.ExpectKilled, task.OriginHand},
}

func TestHealthy(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	runner := killer()
	for i := 1; i <= 3; i++ {
		runner.byLabel["reference-"+string(rune('0'+i))] = verifyrunner.StatusPass
	}
	report := check(t, loaded, runner)

	if report.Verdict != vocab.HealthHealthy {
		t.Fatalf("verdict = %q, want healthy\n%v", report.Verdict, report.Summary())
	}
	counts := report.Aggregates
	if counts.ReferenceRuns != 3 || counts.ReferenceFailures != 0 || counts.Killed != 2 || counts.Survived != 0 {
		t.Fatalf("aggregates = %+v", counts)
	}
	if counts.KillRate == nil || *counts.KillRate != 1 {
		t.Fatalf("kill rate = %v, want 1", counts.KillRate)
	}
	// Five runs in the plan's order, whatever order they finished in.
	var labels []string
	for _, run := range report.Runs {
		labels = append(labels, run.Label)
	}
	want := []string{"reference-1", "reference-2", "reference-3", "mutant-0-times", "mutant-1-swapped"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Fatalf("runs = %v, want %v", labels, want)
	}
	if report.CMoAVersion != "v0.0.0-test" {
		t.Errorf("cmoa_version = %q, want the runner's answer", report.CMoAVersion)
	}
}

// TestFalsePositive is the first of the two questions: a verifier that rejects
// a solution it should accept is broken, whatever it does to the mutants.
func TestFalsePositive(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 3}`, twoHandMutants)
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	runner.byLabel["reference-2"] = verifyrunner.StatusFail
	runner.byLabel["reference-3"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	if report.Verdict != vocab.HealthUnhealthy {
		t.Fatalf("verdict = %q, want unhealthy\n%v", report.Verdict, report.Summary())
	}
	if report.Aggregates.ReferenceFailures != 1 {
		t.Fatalf("reference_failures = %d, want 1", report.Aggregates.ReferenceFailures)
	}
	// A perfect kill rate does not rescue it.
	if rate := report.Aggregates.KillRate; rate == nil || *rate != 1 {
		t.Fatalf("kill rate = %v, want 1 — and still unhealthy", rate)
	}
}

// TestHandWrittenSurvivorIsUnhealthy records the rule the rate cannot
// override: a hand-written mutant is one a person chose to stand behind, so
// one that survives is a fault even where the rate is above the threshold.
func TestHandWrittenSurvivorIsUnhealthy(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.5, "reference_runs": 1}`, []mutantSpec{
		{"times", task.ExpectKilled, task.OriginHand},
		{"swapped", task.ExpectKilled, task.OriginGenerated},
		{"zero", task.ExpectKilled, task.OriginGenerated},
	})
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	runner.byLabel["mutant-0-times"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	if report.Verdict != vocab.HealthUnhealthy {
		t.Fatalf("verdict = %q, want unhealthy\n%v", report.Verdict, report.Summary())
	}
	if rate := report.Aggregates.KillRate; rate == nil || *rate < 0.5 {
		t.Fatalf("kill rate = %v, which is above the threshold — the hand mutant is the reason", rate)
	}
}

// TestThreshold is the rate on its own: three generated mutants killed out of
// four is 0.75, and 0.75 < 0.8. Strictly below fails, so a threshold of 1 is
// reachable.
func TestThreshold(t *testing.T) {
	mutants := []mutantSpec{
		{"times", task.ExpectKilled, task.OriginGenerated},
		{"swapped", task.ExpectKilled, task.OriginGenerated},
		{"zero", task.ExpectKilled, task.OriginGenerated},
		{"reversed", task.ExpectKilled, task.OriginGenerated},
	}
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, mutants)
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	runner.byLabel["mutant-3-reversed"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	if rate := report.Aggregates.KillRate; rate == nil || *rate != 0.75 {
		t.Fatalf("kill rate = %v, want 0.75", rate)
	}
	if report.Verdict != vocab.HealthUnhealthy {
		t.Fatalf("verdict = %q, want unhealthy\n%v", report.Verdict, report.Summary())
	}

	// The same run against a task that asks for less is healthy: nothing about
	// the verifier changed, only what the task calls good enough.
	lenient := newTask(t, `{"kill_rate_min": 0.75, "reference_runs": 1}`, mutants)
	if got := check(t, lenient, runner).Verdict; got != vocab.HealthHealthy {
		t.Fatalf("verdict at a 0.75 threshold = %q, want healthy", got)
	}
}

// TestInconclusive covers the three ways a run can fail to answer. None of
// them is a kill: a verifier that times out on everything would otherwise
// score perfectly.
func TestInconclusive(t *testing.T) {
	tests := []struct {
		name   string
		mutant string
		set    func(*fake)
	}{
		{
			name:   "the verifier ran out of time",
			mutant: "mutant-1-swapped",
			set:    func(f *fake) { f.byLabel["mutant-1-swapped"] = verifyrunner.StatusTimeout },
		},
		{
			name:   "the runner itself failed",
			mutant: "mutant-1-swapped",
			set: func(f *fake) {
				f.failWith = map[string]error{"mutant-1-swapped": errRunner}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
			runner := killer()
			runner.byLabel["reference-1"] = verifyrunner.StatusPass
			tt.set(runner)
			report := check(t, loaded, runner)

			if report.Verdict != vocab.HealthInconclusive {
				t.Fatalf("verdict = %q, want inconclusive\n%v", report.Verdict, report.Summary())
			}
			if report.Aggregates.Inconclusive != 1 || report.Aggregates.Killed != 1 {
				t.Fatalf("aggregates = %+v", report.Aggregates)
			}
			// The mutant that did not answer is in neither half of the rate.
			if rate := report.Aggregates.KillRate; rate == nil || *rate != 1 {
				t.Fatalf("kill rate = %v, want 1 over the one mutant that answered", rate)
			}
		})
	}
}

// TestApplyFailedIsInconclusive is the mutant that never reached a verifier at
// all. It is the task that is wrong rather than the verifier, and the report
// says so instead of counting it either way.
func TestApplyFailedIsInconclusive(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, []mutantSpec{
		{"times", task.ExpectKilled, task.OriginHand},
		{"noapply", task.ExpectKilled, task.OriginHand},
	})
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	if report.Verdict != vocab.HealthInconclusive {
		t.Fatalf("verdict = %q, want inconclusive\n%v", report.Verdict, report.Summary())
	}
	var found bool
	for _, run := range report.Runs {
		if run.Label == "mutant-1-noapply" {
			found = true
			if run.Status != verifyrunner.StatusApplyFailed || run.Outcome != doctor.OutcomeInconclusive {
				t.Fatalf("the unapplyable mutant reads as %+v", run)
			}
			if run.Error == "" {
				t.Error("the unapplyable mutant does not say why")
			}
		}
	}
	if !found {
		t.Fatal("the unapplyable mutant is not in the report")
	}
	// It never reached the verifier.
	for _, label := range runner.seen {
		if label == "mutant-1-noapply" {
			t.Fatal("a mutant that would not apply was verified anyway")
		}
	}
}

// TestEquivalentIsExcluded records what `expect: equivalent` buys: the mutant
// is run and reported, and a verifier passing it is not a fault, so it appears
// in neither half of the rate.
func TestEquivalentIsExcluded(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 1, "reference_runs": 1}`, []mutantSpec{
		{"times", task.ExpectKilled, task.OriginHand},
		{"comment", task.ExpectEquivalent, task.OriginHand},
	})
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	runner.byLabel["mutant-1-comment"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	if report.Verdict != vocab.HealthHealthy {
		t.Fatalf("verdict = %q, want healthy\n%v", report.Verdict, report.Summary())
	}
	counts := report.Aggregates
	if counts.Equivalent != 1 || counts.Killed != 1 || counts.Survived != 0 {
		t.Fatalf("aggregates = %+v", counts)
	}
	if rate := counts.KillRate; rate == nil || *rate != 1 {
		t.Fatalf("kill rate = %v, want 1 over the killable mutant alone", rate)
	}
	// It is still reported: an equivalent mutant nobody can see is an
	// annotation nobody can review.
	var found bool
	for _, run := range report.Runs {
		if run.Label == "mutant-1-comment" {
			found = run.Status == verifyrunner.StatusPass && run.Expect == task.ExpectEquivalent
		}
	}
	if !found {
		t.Fatal("the equivalent mutant is not reported with its status")
	}
}

// TestReportIsWritten holds the file the check leaves behind to its schema.
func TestReportIsWritten(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	dir := filepath.Join(t.TempDir(), "out")
	report, err := doctor.Check(t.Context(), doctor.Options{
		Task: loaded, Runner: runner, Dir: dir, RunID: "20260101T000000Z-0000beef",
		Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	var back doctor.Report
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	if back.SchemaVersion != doctor.SchemaVersion || back.RunID != report.RunID ||
		back.Task != "hello" || back.Verdict != report.Verdict {
		t.Fatalf("the report reads back as %+v", back)
	}
	if len(back.Rev) != 40 {
		t.Errorf("rev = %q, want the resolved commit", back.Rev)
	}
	// The verifier's command line names the task's compose file by absolute
	// path, so it is deliberately not recorded: a report is a committed
	// artefact of a public repository.
	if strings.Contains(string(body), "docker") || strings.Contains(string(body), loaded.Dir) {
		t.Errorf("the report carries a machine path or a command line:\n%s", body)
	}
}

// TestRecordIsAValidDocument writes the vault record and asks DocDag whether
// it is a document. A record the vault refuses is a result nobody can file.
func TestRecordIsAValidDocument(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 1}`, twoHandMutants)
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	root := tempVault(t)
	first, err := report.Record(root, "doctor/20260101T000000Z-0000beef/report.json")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if first != "spec/verifiers/hello@2026-01-01.md" {
		t.Fatalf("Record wrote %q", first)
	}
	// A second check the same day gets the next sequence number rather than
	// overwriting the first: the kind is append-only, and two measurements are
	// two records.
	second, err := report.Record(root, "doctor/20260101T000000Z-0000beef/report.json")
	if err != nil {
		t.Fatalf("Record twice: %v", err)
	}
	if second != "spec/verifiers/hello@2026-01-01-1.md" {
		t.Fatalf("the second record went to %q", second)
	}

	cfg, err := vault.Config()
	if err != nil {
		t.Fatalf("vault.Config: %v", err)
	}
	findings, err := lint.Check(cfg, root, "")
	if err != nil {
		t.Fatalf("lint.Check: %v", err)
	}
	for _, f := range findings {
		if f.Severity == model.SeverityError {
			t.Errorf("lint.Check reported %s %s %s: %s", f.Severity, f.Rule, f.ID, f.Detail)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(first)))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	for _, want := range []string{
		"kind: verifier", "verdict: healthy", `kill_rate: "1.00"`, `mutants: "2"`,
		`reference_runs: "1"`, "report: doctor/20260101T000000Z-0000beef/report.json",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the record does not carry %q:\n%s", want, body)
		}
	}
}

// TestNoMutantsIsInconclusive is the line between a broken manifest and a
// check with nothing to measure. The reference is still verified — whether the
// verifier accepts a correct solution is half of what the check is for — and
// the missing half is what makes the verdict inconclusive.
func TestNoMutantsIsInconclusive(t *testing.T) {
	loaded := newTask(t, `{"kill_rate_min": 0.8, "reference_runs": 2}`, nil)
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	runner.byLabel["reference-2"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	if report.Verdict != vocab.HealthInconclusive {
		t.Fatalf("verdict = %q, want inconclusive\n%v", report.Verdict, report.Summary())
	}
	if report.Aggregates.ReferenceRuns != 2 || report.Aggregates.KillRate != nil {
		t.Fatalf("aggregates = %+v", report.Aggregates)
	}
	if len(report.Runs) != 2 {
		t.Fatalf("runs = %+v, want the two reference runs", report.Runs)
	}
	// A reference that fails is still unhealthy, mutants or no mutants.
	failing := killer()
	failing.byLabel["reference-1"] = verifyrunner.StatusFail
	failing.byLabel["reference-2"] = verifyrunner.StatusPass
	if got := check(t, newTask(t, `{"reference_runs": 2}`, nil), failing).Verdict; got != vocab.HealthUnhealthy {
		t.Fatalf("verdict with a failing reference and no mutants = %q, want unhealthy", got)
	}
}

// TestOutputDirectoryInUse is the foot-gun `cmoa verify --out` sets up: it
// refuses to overwrite a result.json, so a second check into one directory
// would come back runner_error on every run and read as a verifier nobody can
// measure. Saying so up front makes it a usage mistake instead.
func TestOutputDirectoryInUse(t *testing.T) {
	loaded := newTask(t, `{"reference_runs": 1}`, twoHandMutants)
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	dir := filepath.Join(t.TempDir(), "out")
	options := doctor.Options{Task: loaded, Runner: runner, Dir: dir, RunID: "20260101T000000Z-0000beef"}
	if _, err := doctor.Check(t.Context(), options); err != nil {
		t.Fatalf("Check: %v", err)
	}
	_, err := doctor.Check(t.Context(), options)
	if !errors.Is(err, doctor.ErrDirInUse) {
		t.Fatalf("Check error = %v, want ErrDirInUse", err)
	}
}

// TestErrorsCarryNoMachinePath is what makes a report safe to commit. The
// strings that reach it come from git, from docker and from cmoa, all of which
// name paths absolutely, and the ADR promises the report carries none.
func TestErrorsCarryNoMachinePath(t *testing.T) {
	loaded := newTask(t, `{"reference_runs": 1}`, []mutantSpec{
		{"times", task.ExpectKilled, task.OriginHand},
		{"noapply", task.ExpectKilled, task.OriginHand},
	})
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	runner.failWith = map[string]error{"mutant-0-times": errRunner}
	dir := filepath.Join(t.TempDir(), "out")
	report, err := doctor.Check(t.Context(), doctor.Options{
		Task: loaded, Runner: runner, Dir: dir, RunID: "20260101T000000Z-0000beef",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	// The two errors are there — a report that hid them would be worse — and
	// neither names a directory.
	var errors int
	for _, run := range report.Runs {
		if run.Error == "" {
			continue
		}
		errors++
		if strings.Contains(run.Error, "/") && !strings.Contains(run.Error, "<") {
			t.Errorf("%s carries a path: %q", run.Label, run.Error)
		}
	}
	if errors != 2 {
		t.Fatalf("%d run(s) carry an error, want the apply failure and the runner failure", errors)
	}
	for _, absent := range []string{loaded.Dir, loaded.Repo, os.TempDir() + "/uzushio-worktree"} {
		if strings.Contains(string(body), absent) {
			t.Errorf("the report carries %q:\n%s", absent, body)
		}
	}
	if home := os.Getenv("HOME"); home != "" && home != "/" && strings.Contains(string(body), home) {
		t.Errorf("the report carries the home directory:\n%s", body)
	}
}

func TestChecksRefuseAVersionOneTask(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "repo", "add.go"), seed)
	writeFile(t, filepath.Join(dir, task.ManifestFile),
		`{"version": 1, "id": "hello", "repo": "repo", "files": ["add.go"]}`)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = doctor.Check(context.Background(), doctor.Options{Task: loaded, Runner: killer()})
	if err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Check error = %v, want it to ask for version 2", err)
	}
}

// tempVault copies the repository's configuration and specification corpus
// into a directory of the test's own, so a record can be written beside real
// documents without touching the repository.
func tempVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	copyFile(t, filepath.Join(source, "docdag.yaml"), filepath.Join(root, "docdag.yaml"))
	specRoot := filepath.Join(source, "spec")
	err = filepath.WalkDir(specRoot, func(p string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(specRoot, p)
		if err != nil {
			return err
		}
		copyFile(t, p, filepath.Join(root, "spec", relative))
		return nil
	})
	if err != nil {
		t.Fatalf("copy spec: %v", err)
	}
	return root
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	writeFile(t, to, string(body))
}

// errRunner is a runner that could not answer, which is a different thing from
// a verifier that answered no.
var errRunner = &runnerFailure{}

type runnerFailure struct{}

func (*runnerFailure) Error() string { return "docker is not running" }

// TestLabelsAreLowerCase records a contract that is easy to break and hard to
// read afterwards. `cmoa verify --label` is held to ^[a-z0-9][a-z0-9_-]{0,63}$
// because the label becomes part of a compose project name and docker refuses
// an upper-case one — and a mutant's file name carries its line and column as
// L7C9. Getting it wrong does not fail loudly: cmoa exits 2 with nothing on
// stdout, so the health check reports runner_error and the report says the
// mutant was inconclusive rather than that the label was wrong.
func TestLabelsAreLowerCase(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	writeFile(t, filepath.Join(repo, "add.go"), seed)
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "seed")
	writeFile(t, filepath.Join(repo, "add.go"), solved)
	writeFile(t, filepath.Join(dir, "reference.diff"), git(t, repo, "diff"))
	git(t, repo, "add", "add.go")
	writeFile(t, filepath.Join(repo, "add.go"), mutantSources["times"])
	writeFile(t, filepath.Join(dir, "mutants", "0003-ret-add-L7C9.diff"), git(t, repo, "diff"))
	git(t, repo, "reset", "--hard", "--quiet", "HEAD")
	writeFile(t, filepath.Join(dir, task.ManifestFile), `{
  "version": 2, "id": "hello", "repo": "repo", "rev": "HEAD", "files": ["add.go"],
  "reference": {"diff": "reference.diff"},
  "mutants": [{"diff": "mutants/0003-ret-add-L7C9.diff"}],
  "doctor": {"reference_runs": 1}
}
`)
	loaded, err := task.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	runner := killer()
	runner.byLabel["reference-1"] = verifyrunner.StatusPass
	report := check(t, loaded, runner)

	for _, label := range runner.seen {
		if label != strings.ToLower(label) {
			t.Errorf("the runner was given the label %q", label)
		}
		if len(label) > doctor.LabelMax {
			t.Errorf("the label %q is %d characters", label, len(label))
		}
		for _, r := range label {
			if !labelRune(r) {
				t.Errorf("the label %q holds %q", label, r)
			}
		}
	}
	if report.Runs[1].Label != "mutant-0-0003-ret-add-l7c9" {
		t.Fatalf("the mutant's label is %q", report.Runs[1].Label)
	}
	// The diff the label was made from keeps its own spelling: the file name
	// is the task's, and only the label is CMoA's.
	if report.Runs[1].Diff != "mutants/0003-ret-add-L7C9.diff" {
		t.Fatalf("the report renamed the diff to %q", report.Runs[1].Diff)
	}
}

// labelRune reports whether a rune may appear in a CMoA label.
func labelRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}
