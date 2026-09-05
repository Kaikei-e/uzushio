// Package doctor measures a task's verifier rather than the code the verifier
// judges.
//
// A verifier is the thing every claim in this repository rests on: a pass rate
// is a number about nothing if the verifier says pass to anything, and a
// harness edit accepted on a verifier that rejects a correct solution is an
// edit accepted for the wrong reason. So the check asks the two questions that
// can be asked without knowing what the task is about.
//
//  1. Does it accept a solution it should accept? The reference diff is
//     verified `reference_runs` times, and a single failure is a false
//     positive: a verifier that is right two runs out of three is a verifier
//     nobody can read a result off.
//  2. Does it reject solutions it should reject? Each mutant — a deliberate
//     defect written against the reference-applied tree — is verified, and one
//     that passes is a defect the verifier cannot see.
//
// The kill rate is killed / (killed + survived), which is the denominator
// every mutation-testing tool that reports honestly uses: a mutant that never
// ran is left out of both halves rather than counted as detected.
package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
	"github.com/Kaikei-e/uzushio/internal/vocab"
	"github.com/Kaikei-e/uzushio/internal/worktree"
)

// ErrDoctor is the sentinel every failure to run the check wraps. It means the
// check did not happen; a check that happened and found something reports it
// as a verdict instead.
var ErrDoctor = errors.New("doctor")

// SchemaVersion is the version of the report this package writes.
const SchemaVersion = 1

// DefaultParallel is how many verifications run at once when nobody says.
// Two, because a verifier is a container and the machine running the check is
// usually the machine being worked on.
const DefaultParallel = 2

// BandParallel is how many verifications of a banded verifier run at once.
//
// One, and it is not a tuning choice. A banded verifier's answer is a
// measurement — microseconds per request, a tail latency, a transition time —
// and two of them on one machine measure each other. Running them side by side
// does not make the check faster in any sense that matters; it makes every
// number in it a number about contention.
const BandParallel = 1

// ParallelFor is how many verifications may run at once, and what to say about
// it.
//
// A banded verifier is held to one, and the caller's own --parallel wins over
// that: somebody who typed it has a machine in mind, and a health check that
// silently ignored the flag would be measuring something other than what was
// asked for. Both cases warn, because both are worth knowing about — the first
// says a flag's default was overruled, the second says the numbers may be
// contention.
func ParallelFor(kind task.Kind, requested int, explicit bool) (parallel int, warning string) {
	if requested < 1 {
		requested = DefaultParallel
	}
	if kind != task.KindBand || requested <= BandParallel {
		return requested, ""
	}
	const why = "a banded verifier measures latency, and running two at once measures contention"
	if explicit {
		return requested, fmt.Sprintf(
			"warning: --parallel %d on a task whose verify.kind is band: %s", requested, why)
	}
	return BandParallel, fmt.Sprintf(
		"warning: verify.kind is band, so this check runs %d verification at a time: %s",
		BandParallel, why)
}

// RunKind says which of the two questions a run answered.
type RunKind string

// The two run kinds.
const (
	// RunReference is one verification of the reference solution.
	RunReference RunKind = "reference"
	// RunMutant is one verification of a mutant.
	RunMutant RunKind = "mutant"
)

// Outcome is what a mutant run means for the verifier.
type Outcome string

// The outcomes.
const (
	// OutcomeKilled is a mutant the verifier rejected, which is the verifier
	// working.
	OutcomeKilled Outcome = "killed"
	// OutcomeSurvived is a mutant the verifier passed, which is a defect it
	// cannot see.
	OutcomeSurvived Outcome = "survived"
	// OutcomeInconclusive is a mutant that never got a verdict: it did not
	// apply, the verifier timed out, or the runner failed. It is counted in
	// neither half of the rate.
	OutcomeInconclusive Outcome = "inconclusive"
)

// String returns the outcome as the report writes it.
func (o Outcome) String() string { return string(o) }

// Run is one line of the report: one verification, what it was of, and what it
// said.
//
// It deliberately does not carry the verifier's command line. `cmoa verify`
// reports one, and it names the task's compose file by absolute path — so a
// report committed to a public repository would carry a home directory from
// whichever machine ran the check. project_name is kept instead: it is built
// from the task identifier and the label, it names the compose project a
// container ran under, and it says nothing about the machine.
type Run struct {
	Label    string              `json:"label"`
	Kind     RunKind             `json:"kind"`
	Mutant   *int                `json:"mutant_index,omitempty"`
	Diff     string              `json:"diff,omitempty"`
	Expect   task.Expect         `json:"expect,omitempty"`
	Origin   task.Origin         `json:"origin,omitempty"`
	Operator string              `json:"operator,omitempty"`
	Note     string              `json:"note,omitempty"`
	Project  string              `json:"project_name,omitempty"`
	Status   verifyrunner.Status `json:"status"`
	// ReusedFrom names the check this run was copied from, for a reference run
	// that was not performed again.
	//
	// It is on the run rather than on the report because it is a property of
	// the measurement: this line is a container that ran at some other time,
	// and a reader comparing two lines of one report needs to know that one of
	// them is older than the other. Conclude counts it as the reference run it
	// is — the point of reusing it is that it is still evidence — and the
	// environment fingerprint is what says it may be.
	ReusedFrom string `json:"reused_from,omitempty"`
	// Band is what a banded verifier measured, carried straight through from
	// `cmoa verify`. It is what turns "killed" into "killed via
	// <invariant>, 1.9 against a band ending at 1.2" — the material a
	// person needs to re-centre a band, or to notice that the mutant nobody
	// caught was the one whose invariant reported skipped.
	Band    *verifyrunner.Band `json:"band,omitempty"`
	Outcome Outcome            `json:"outcome,omitempty"`
	// OutcomeNote qualifies the outcome where the outcome on its own would be
	// read as more than it is. The one case so far is a check whose reference
	// solution the verifier rejected: every mutant is then `killed`, and none
	// of them was detected — they failed for the reason the reference did.
	OutcomeNote string `json:"outcome_note,omitempty"`
	// BandsBeyondReference are the invariants this mutant put out of band that
	// the reference did not, in the order the verifier reported them.
	//
	// It is the differential a reader wants when the reference itself failed:
	// a mutant that adds one more invariant to a set of four bands that
	// were already failing was genuinely detected, and one that adds nothing
	// was not. It is written only where the reference failed something, since
	// otherwise it is the mutant's own failed set spelled twice.
	BandsBeyondReference []string `json:"bands_beyond_reference,omitempty"`
	ExitCode             int      `json:"exit_code"`
	DurationMS           int64    `json:"duration_ms"`
	Out                  string   `json:"out,omitempty"`
	Error                string   `json:"error,omitempty"`
}

// Aggregates are the counts the verdict is read off.
//
// killed, survived and inconclusive count only the mutants the task expects to
// be killed; a mutant declared equivalent is counted under equivalent and left
// out of all three, because a verifier passing it is not a fault.
type Aggregates struct {
	ReferenceRuns         int      `json:"reference_runs"`
	ReferenceFailures     int      `json:"reference_failures"`
	ReferenceInconclusive int      `json:"reference_inconclusive"`
	Killed                int      `json:"killed"`
	Survived              int      `json:"survived"`
	Inconclusive          int      `json:"inconclusive"`
	Equivalent            int      `json:"equivalent"`
	KillRate              *float64 `json:"kill_rate"`
	// KillRateMeaningful says whether the rate is evidence of detection.
	//
	// It is not, when the verifier rejected the reference solution. Every
	// mutant is then verified against a verifier that says no to everything,
	// so every mutant is `killed` and a rate of 1.00 measures the false
	// positive rather than the verifier's reach. The 2026-09-05 check of the
	// first banded task is the case this field exists for: 5 of 5 reference
	// runs failed on the same four bands, and all seven mutants came back
	// killed — including the two the task declares equivalent, which is what
	// gives the game away.
	//
	// It is also false where no reference run reached a verdict at all: there
	// is then nothing saying the verifier accepts anything.
	//
	// It is about the reference rather than about whether a rate exists; a
	// check with no killable mutant carries a null rate and this flag still
	// answers the question it is named for.
	KillRateMeaningful bool    `json:"kill_rate_meaningful"`
	KillRateMin        float64 `json:"kill_rate_min"`
}

// Report is what one health check produced.
type Report struct {
	SchemaVersion int          `json:"schema_version"`
	RunID         string       `json:"run_id"`
	Task          string       `json:"task"`
	Rev           string       `json:"rev"`
	CMoAVersion   string       `json:"cmoa_version"`
	Verdict       vocab.Health `json:"verdict"`
	// Environment fingerprints what the verifier was, as opposed to what it
	// judged: the compose file, the task-directory files mounted into the
	// container, and the built image. It is what says whether an earlier
	// check's reference block is still a measurement of the same thing.
	Environment *Environment `json:"environment,omitempty"`
	Aggregates  Aggregates   `json:"aggregates"`
	Runs        []Run        `json:"runs"`
	StartedAt   string       `json:"started_at"`
	FinishedAt  string       `json:"finished_at"`
}

// ReusedReference returns the check a report's reference runs were copied from,
// and whether any were.
func (r *Report) ReusedReference() (string, bool) {
	for _, run := range r.Runs {
		if run.Kind == RunReference && run.ReusedFrom != "" {
			return run.ReusedFrom, true
		}
	}
	return "", false
}

// ReferenceRuns is every reference run a report holds, which is what a later
// check reuses.
func (r *Report) ReferenceRuns() []Run {
	var out []Run
	for _, run := range r.Runs {
		if run.Kind == RunReference {
			out = append(out, run)
		}
	}
	return out
}

// Bytes renders the report as it is written to disk: two-space indent, one
// trailing newline, and no HTML escaping — a mutant's note reads
// `'+' -> '-'`, and encoding/json would otherwise write that as
// `'+' -> '-'`.
func (r *Report) Bytes() ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, fmt.Errorf("%w: encode the report: %w", ErrDoctor, err)
	}
	return out.Bytes(), nil
}

// Options is one health check.
type Options struct {
	// Task is the loaded task. It has to be doctorable; Check says so if it is
	// not.
	Task *task.Task
	// Runner performs one verification.
	Runner verifyrunner.Runner
	// Dir is where the report and the per-run output go. Check creates it.
	Dir string
	// Parallel is how many verifications run at once. Zero is
	// DefaultParallel, and a banded verifier holds it to BandParallel unless
	// ParallelExplicit says a person asked for the number.
	Parallel int
	// ParallelExplicit says the caller chose Parallel rather than inheriting a
	// default. See ParallelFor.
	ParallelExplicit bool
	// Warn receives the one-line warnings a check produces about how it is
	// being run, as opposed to what it found. Nil discards them.
	Warn func(string)
	// RunID names the check. Empty generates one.
	RunID string
	// Now is the clock, injectable so a test can pin a report's bytes.
	Now func() time.Time
	// Only selects which verifications run. Empty is all of them.
	Only Only
	// Reuse is an earlier report whose reference runs are copied into this
	// check instead of being performed again. Nil runs them.
	//
	// It is refused unless the earlier report is about the same task at the
	// same revision with the same environment fingerprint, because the whole
	// value of a reference run is that it measured this verifier, and a
	// reference block carried across a changed verifier is a green line about
	// something that no longer exists.
	Reuse *Report
}

// ErrDirInUse is what Check returns for a directory that already holds a
// check. It is its own sentinel because the caller answers it with a usage
// exit code rather than with a verdict.
var ErrDirInUse = errors.New("doctor: the output directory already holds a check")

// NewRunID returns a run identifier of the shape CMoA gives a trace run: a UTC
// timestamp and eight hexadecimal digits, so the lexicographic order is the
// chronological one.
func NewRunID(now time.Time) string {
	var suffix [4]byte
	// crypto/rand.Read does not fail on any platform Go supports; it panics
	// itself if the system source is broken, which is the right answer here
	// too — a run identifier that repeats would overwrite a report.
	_, _ = rand.Read(suffix[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:])
}

// Check performs the health check and writes the report. It is named for what
// it does rather than Run, because a Run in this package is one line of the
// report.
func Check(ctx context.Context, opts Options) (*Report, error) {
	if opts.Task == nil {
		return nil, fmt.Errorf("%w: no task", ErrDoctor)
	}
	if opts.Runner == nil {
		return nil, fmt.Errorf("%w: no runner", ErrDoctor)
	}
	if err := opts.Task.RequireDoctorable(); err != nil {
		return nil, err
	}
	if err := opts.Task.RequireRunnableVerifier(); err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	runID := opts.RunID
	if runID == "" {
		runID = NewRunID(started)
	}
	dir := opts.Dir
	if dir == "" {
		dir = filepath.Join(opts.Task.Dir, "doctor", runID)
	}
	// `cmoa verify --out` refuses to overwrite an existing result.json, so a
	// second check into one directory would come back runner_error on every
	// run and read as inconclusive. Saying so up front is the difference
	// between a usage mistake and a verifier nobody can measure.
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrDirInUse, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: create %s: %w", ErrDoctor, dir, err)
	}

	rev, err := worktree.ResolveRev(ctx, opts.Task.Repo, opts.Task.Rev)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDoctor, err)
	}
	environment, err := Fingerprint(ctx, opts.Task)
	if err != nil {
		return nil, err
	}

	reused, err := reuse(opts, rev, environment)
	if err != nil {
		return nil, err
	}
	jobs, err := plan(ctx, opts.Task, rev, dir, opts.Only, reused != nil)
	if err != nil {
		return nil, err
	}
	verify(ctx, opts, dir, jobs)

	report := &Report{
		SchemaVersion: SchemaVersion,
		RunID:         runID,
		Task:          opts.Task.ID,
		Rev:           rev,
		Verdict:       vocab.HealthInconclusive,
		Environment:   environment,
		Runs:          make([]Run, 0, len(jobs)+len(reused)),
		StartedAt:     started.Format(time.RFC3339),
	}
	// The reused runs come first, which is where the reference block goes in a
	// report that ran it: the order of the file is the order of the check as a
	// person would have performed it.
	report.Runs = append(report.Runs, reused...)
	for _, j := range jobs {
		if report.CMoAVersion == "" {
			report.CMoAVersion = j.cmoaVersion
		}
		report.Runs = append(report.Runs, j.run)
	}
	if report.CMoAVersion == "" && opts.Reuse != nil {
		report.CMoAVersion = opts.Reuse.CMoAVersion
	}
	report.Aggregates.KillRateMin = opts.Task.Doctor.KillRateMin
	report.Conclude()
	report.FinishedAt = now().UTC().Format(time.RFC3339)

	if err := report.Write(filepath.Join(dir, ReportFile)); err != nil {
		return nil, err
	}
	return report, nil
}

// ReportFile is the name a check gives its report inside the output directory.
const ReportFile = "report.json"

// Conclude reads the runs and fills in everything derived from them: the
// counts, the per-run annotations and the verdict.
//
// It is separate from Check because it is the whole of what a replay does. A
// report holds every run verbatim, so the conclusions can be recomputed from
// the file long after the containers are gone — which is how a report written
// before a field existed gains it, without running the verifier again.
//
// Aggregates.KillRateMin is an input rather than an output: it is the task's
// threshold as it stood when the check ran, and a replay judges against the
// threshold in the file rather than against whatever the task says today.
func (r *Report) Conclude() {
	r.Aggregates = aggregate(r.Aggregates.KillRateMin, r.Runs)
	annotate(r.Runs, r.Aggregates)
	r.Verdict = verdict(r.Runs, r.Aggregates)
}

// ReadReport decodes a report this package wrote.
//
// The schema version is the one thing leniency does not extend to: a report of
// another shape read as this one would be recomputed into a file that says
// something nobody measured.
func ReadReport(path string) (*Report, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDoctor, err)
	}
	var report Report
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrDoctor, filepath.Base(path), err)
	}
	if report.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: %s is schema_version %d; this build reads %d",
			ErrDoctor, filepath.Base(path), report.SchemaVersion, SchemaVersion)
	}
	if report.RunID == "" {
		return nil, fmt.Errorf("%w: %s carries no run_id", ErrDoctor, filepath.Base(path))
	}
	if len(report.Runs) == 0 {
		return nil, fmt.Errorf("%w: %s holds no runs to recompute", ErrDoctor, filepath.Base(path))
	}
	return &report, nil
}

// Write renders the report to path.
func (r *Report) Write(path string) error {
	body, err := r.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil { //nolint:gosec // a report is world-readable on purpose
		return fmt.Errorf("%w: write %s: %w", ErrDoctor, path, err)
	}
	return nil
}

// reuse reads the reference block out of an earlier report, and refuses unless
// that report is about the same task, at the same revision, under the same
// verifier.
//
// The three checks are separate and are reported separately. "A different
// commit" and "a different harness" are both reasons a reference run is not
// evidence about this check, and the person who has to fix it needs to know
// which one it was.
func reuse(opts Options, rev string, environment *Environment) ([]Run, error) {
	if opts.Reuse == nil {
		return nil, nil
	}
	if opts.Only.Wants(OnlyReference) {
		// Two instructions that cancel: run only the reference, and do not run
		// the reference. Neither reading is safe to guess at.
		return nil, fmt.Errorf(
			"%w: --only %s runs the reference and --reuse-reference does not run it; drop one",
			ErrDoctor, OnlyReference)
	}
	earlier := opts.Reuse
	var complaints []string
	if earlier.Task != opts.Task.ID {
		complaints = append(complaints, fmt.Sprintf("it measured task %q and this is %q",
			earlier.Task, opts.Task.ID))
	}
	if earlier.Rev != rev {
		complaints = append(complaints, fmt.Sprintf("it measured %s and this is %s",
			short(earlier.Rev), short(rev)))
	}
	if !environment.Same(earlier.Environment) {
		complaints = append(complaints, environment.Differences(earlier.Environment)...)
	}
	if len(complaints) > 0 {
		return nil, fmt.Errorf(
			"%w: the reference runs of %s cannot be reused here: %s",
			ErrDoctor, earlier.RunID, strings.Join(complaints, "; "))
	}
	runs := earlier.ReferenceRuns()
	if len(runs) == 0 {
		return nil, fmt.Errorf("%w: %s holds no reference run to reuse", ErrDoctor, earlier.RunID)
	}
	out := make([]Run, 0, len(runs))
	for _, run := range runs {
		// The run is copied whole and marked, rather than summarised. A
		// reference run's band rows are what a calibration is built from, and a
		// report that carried the verdict without them would be one nobody
		// could calibrate off.
		if run.ReusedFrom == "" {
			run.ReusedFrom = earlier.RunID
		}
		// The per-run output directory belongs to the check that produced it.
		run.Out = ""
		out = append(out, run)
	}
	return out, nil
}

// job is one planned verification: the run it will become, and the diff to
// hand over. A job whose diff never materialised carries a status already and
// is never run.
type job struct {
	run         Run
	diff        string
	cmoaVersion string
	done        bool
}

// plan materialises every diff the check will verify.
//
// The reference diff is handed over as it is. A mutant is not: it is written
// against the reference-applied tree, so the diff `cmoa verify` needs is
// reference-then-mutant as one patch against the revision. Two unified diffs
// cannot be concatenated into that — the second one's line numbers are the
// first one's output — so uzushio materialises it: a detached worktree at the
// revision, the reference applied into its index, the mutant applied on top,
// and `git diff --cached` read back out as the combined patch. The worktree is
// reset between mutants, so a mutant that fails to apply costs the next one
// nothing.
func plan(
	ctx context.Context, t *task.Task, rev, dir string, only Only, reused bool,
) (jobs []job, err error) {
	jobs = make([]job, 0, t.Doctor.ReferenceRuns+len(t.Mutants))
	referenceDiff := t.AbsPath(t.Reference.Path)
	if _, err := os.Stat(referenceDiff); err != nil {
		return nil, fmt.Errorf("%w: reference.diff: %w", ErrDoctor, err)
	}
	// matched records which selectors named something, so one that named
	// nothing is refused rather than quietly narrowing the check to less than
	// the caller asked for. available is what could have been typed instead.
	matched := map[string]bool{}
	var available []string
	if !reused {
		for i := 1; i <= t.Doctor.ReferenceRuns; i++ {
			label := fmt.Sprintf("reference-%d", i)
			available = append(available, label)
			if !only.matchesReference(label) {
				continue
			}
			note(matched, only, label, OnlyReference)
			jobs = append(jobs, job{
				run:  Run{Label: label, Kind: RunReference, Out: label},
				diff: referenceDiff,
			})
		}
	}

	var wanted []int
	for i, mutant := range t.Mutants {
		label := labelFor(fmt.Sprintf("mutant-%d-%s", i, stem(mutant.Diff)))
		available = append(available, stem(mutant.Diff))
		if !only.matchesMutant(label, mutant.Diff) {
			continue
		}
		note(matched, only, label, OnlyMutants, mutant.Diff,
			filepath.Base(filepath.FromSlash(mutant.Diff)), stem(mutant.Diff))
		wanted = append(wanted, i)
	}
	if unmatched := only.Unmatched(matched); len(unmatched) > 0 {
		return nil, errNoSuchSelector(unmatched, available)
	}
	if len(wanted) == 0 {
		// Nothing to compose, and nothing to measure a rate over. The check
		// still runs: whether the verifier accepts the reference is half of
		// what it is for, and the missing half is what makes the verdict
		// inconclusive rather than what makes the task invalid.
		return jobs, nil
	}

	combined := filepath.Join(dir, "combined")
	if err := os.MkdirAll(combined, 0o755); err != nil {
		return nil, fmt.Errorf("%w: create %s: %w", ErrDoctor, combined, err)
	}
	tree, err := worktree.Add(ctx, t.Repo, rev)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDoctor, err)
	}
	// A worktree that will not go away leaves the task's repository carrying a
	// registration for a directory that no longer exists, which the next run
	// trips over. It is worth failing a check that otherwise succeeded.
	defer func() {
		if closeErr := tree.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("%w: %w", ErrDoctor, closeErr)
		}
	}()

	for _, i := range wanted {
		mutant := t.Mutants[i]
		label := labelFor(fmt.Sprintf("mutant-%d-%s", i, stem(mutant.Diff)))
		index := i
		run := Run{
			Label:    label,
			Kind:     RunMutant,
			Mutant:   &index,
			Diff:     mutant.Diff,
			Expect:   mutant.Expect,
			Origin:   mutant.Origin,
			Operator: mutant.Operator,
			Note:     mutant.Note,
			Out:      label,
		}
		path, err := combine(ctx, tree, t, mutant, filepath.Join(combined, label+".diff"))
		if err != nil {
			// A mutant that will not apply on top of the reference is a mutant
			// nobody measured. It is the task that is wrong rather than the
			// verifier, so it is inconclusive rather than a fault.
			run.Status = verifyrunner.StatusApplyFailed
			run.Outcome = OutcomeInconclusive
			// git's own complaint, not the command line it came from: the
			// argument list names the task directory and the diff by absolute
			// path, and this string goes into a report that is committed.
			run.Error = scrub(worktree.Diagnostics(err), t.Dir)
			jobs = append(jobs, job{run: run, done: true})
			continue
		}
		jobs = append(jobs, job{run: run, diff: path})
	}
	return jobs, nil
}

// note records that a planned verification answered to one of the selectors,
// so Unmatched can tell a narrowed check from a mistyped one. An empty
// selection matches everything and has nothing to record.
func note(matched map[string]bool, only Only, names ...string) {
	if only.Empty() {
		return
	}
	for _, name := range names {
		if only.Wants(name) {
			matched[name] = true
		}
	}
}

// combine writes reference-then-mutant as one patch against the revision.
func combine(
	ctx context.Context, tree *worktree.Tree, t *task.Task, mutant task.Mutant, out string,
) (string, error) {
	if err := tree.Reset(ctx); err != nil {
		return "", err
	}
	if err := tree.Apply(ctx, t.AbsPath(t.Reference.Path)); err != nil {
		return "", err
	}
	if err := tree.Apply(ctx, t.AbsPath(mutant.Diff)); err != nil {
		return "", err
	}
	body, err := tree.Staged(ctx)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil { //nolint:gosec // a diff is world-readable on purpose
		return "", fmt.Errorf("write %s: %w", out, err)
	}
	return out, nil
}

// verify runs every planned job, at most Parallel at a time. The jobs are
// filled in place, so the report's order is the plan's order rather than the
// order the containers happened to finish in.
func verify(ctx context.Context, opts Options, dir string, jobs []job) {
	parallel, warning := ParallelFor(opts.Task.Verify.Kind, opts.Parallel, opts.ParallelExplicit)
	if warning != "" && opts.Warn != nil {
		opts.Warn(warning)
	}
	var timeout time.Duration
	if opts.Task.Verify.TimeoutSeconds > 0 {
		timeout = time.Duration(opts.Task.Verify.TimeoutSeconds) * time.Second
	}
	tickets := make(chan struct{}, parallel)
	var wait sync.WaitGroup
	for i := range jobs {
		if jobs[i].done {
			continue
		}
		wait.Add(1)
		tickets <- struct{}{}
		go func(j *job) {
			defer wait.Done()
			defer func() { <-tickets }()
			result, err := opts.Runner.Verify(ctx, verifyrunner.Request{
				TaskDir:  opts.Task.Dir,
				DiffPath: j.diff,
				Label:    j.run.Label,
				OutDir:   filepath.Join(dir, j.run.Label),
				Timeout:  timeout,
			})
			if err != nil {
				// No answer came back at all. It is the runner rather than the
				// verifier, so it reads as runner_error: the check reports
				// inconclusive rather than pretending the mutant survived.
				j.run.Status = verifyrunner.StatusRunnerError
				j.run.Error = scrub(err.Error(), opts.Task.Dir)
				j.run.Outcome = outcomeOf(j.run.Kind, j.run.Status)
				return
			}
			j.run.Status = result.Status
			j.run.Project = result.ProjectName
			j.run.Band = result.Band
			j.run.ExitCode = result.ExitCode
			j.run.DurationMS = result.DurationMS
			j.run.Outcome = outcomeOf(j.run.Kind, result.Status)
			j.cmoaVersion = result.CMoAVersion
			switch {
			case result.ApplyError != "":
				j.run.Error = scrub(result.ApplyError, opts.Task.Dir)
			case result.Error != "":
				j.run.Error = scrub(result.Error, opts.Task.Dir)
			}
		}(&jobs[i])
	}
	wait.Wait()
}

// scrub takes the machine out of a message that will be committed.
//
// A report is an artefact of somebody's repository, and the strings that reach
// it come from git, from docker and from `cmoa verify` — all of which name
// paths the way they were given them, which is absolutely. The task directory
// becomes <task>; the throwaway worktree becomes <worktree>; anything else
// that still looks like an absolute path becomes <path>, because a message
// nobody anticipated is exactly the one that carries a home directory.
func scrub(message, taskDir string) string {
	if taskDir != "" {
		message = strings.ReplaceAll(message, taskDir, "<task>")
	}
	return absolutePath.ReplaceAllString(message, "<path>")
}

// absolutePath is a slash-rooted path with at least one segment. It is
// deliberately blunt: everything it can match in a diagnostic is a file name
// nobody reading a report needs, and the alternative is leaking one.
var absolutePath = regexp.MustCompile(`/[^\s:,"']+`)

// outcomeOf classifies one mutant run. A reference run has no outcome: its
// status is the whole of what it says.
func outcomeOf(kind RunKind, status verifyrunner.Status) Outcome {
	if kind != RunMutant {
		return ""
	}
	switch status {
	case verifyrunner.StatusFail:
		return OutcomeKilled
	case verifyrunner.StatusPass:
		return OutcomeSurvived
	case verifyrunner.StatusApplyFailed, verifyrunner.StatusTimeout, verifyrunner.StatusRunnerError:
		return OutcomeInconclusive
	}
	return OutcomeInconclusive
}

// aggregate counts the runs. A timeout is inconclusive rather than a kill:
// some tools count it as detected, and that is how a verifier that hangs on
// everything comes out looking perfect.
func aggregate(killRateMin float64, runs []Run) Aggregates {
	out := Aggregates{KillRateMin: killRateMin}
	for _, run := range runs {
		switch run.Kind {
		case RunReference:
			out.ReferenceRuns++
			switch run.Status {
			case verifyrunner.StatusFail:
				out.ReferenceFailures++
			case verifyrunner.StatusPass:
			case verifyrunner.StatusApplyFailed, verifyrunner.StatusTimeout, verifyrunner.StatusRunnerError:
				out.ReferenceInconclusive++
			}
		case RunMutant:
			if run.Expect == task.ExpectEquivalent {
				out.Equivalent++
				continue
			}
			switch run.Outcome {
			case OutcomeKilled:
				out.Killed++
			case OutcomeSurvived:
				out.Survived++
			case OutcomeInconclusive:
				out.Inconclusive++
			}
		}
	}
	if measured := out.Killed + out.Survived; measured > 0 {
		rate := float64(out.Killed) / float64(measured)
		out.KillRate = &rate
	}
	// A rate is evidence only if something first said the verifier accepts a
	// solution it should accept. Strictly fewer inconclusive runs than runs, so
	// a check with no reference run at all is not meaningful either.
	out.KillRateMeaningful = out.ReferenceFailures == 0 && out.ReferenceInconclusive < out.ReferenceRuns
	return out
}

// annotate writes onto each mutant run what only the whole check knows: that
// its outcome is not what it looks like, and which of the bands it broke the
// reference had not broken already.
//
// It runs after aggregate because both answers depend on how the reference
// went, which is not known while a mutant is being verified.
func annotate(runs []Run, counts Aggregates) {
	if counts.KillRateMeaningful {
		return
	}
	note := "no reference run reached a verdict"
	if counts.ReferenceFailures > 0 {
		note = "reference also failed"
	}
	reference := referenceFailedBands(runs)
	for i := range runs {
		if runs[i].Kind != RunMutant || runs[i].Outcome != OutcomeKilled {
			continue
		}
		runs[i].OutcomeNote = note
		if len(reference) > 0 {
			runs[i].BandsBeyondReference = beyond(runs[i].Band, reference)
		}
	}
}

// referenceFailedBands is every invariant any reference run put out of band.
//
// The union rather than the intersection: an invariant that failed on one
// reference run out of five is an invariant this machine cannot hold, and a
// mutant failing it is not evidence of anything.
func referenceFailedBands(runs []Run) map[string]bool {
	failed := map[string]bool{}
	for _, run := range runs {
		if run.Kind != RunReference || run.Band == nil {
			continue
		}
		for _, name := range run.Band.Failed {
			failed[name] = true
		}
	}
	return failed
}

// beyond is the band's failed invariants that the reference did not fail, in
// the order the verifier reported them. Nil where there are none, so a mutant
// that broke nothing the reference had not broken says so by writing no key.
func beyond(band *verifyrunner.Band, reference map[string]bool) []string {
	if band == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range band.Failed {
		if reference[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// verdict reads the counts. The order is the order of severity: a verifier
// that rejects the reference solution is broken whatever its kill rate says,
// and a check that could not measure says so rather than guessing.
func verdict(runs []Run, counts Aggregates) vocab.Health {
	if counts.ReferenceFailures > 0 {
		return vocab.HealthUnhealthy
	}
	for _, run := range runs {
		// A hand-written mutant is the set a person chose to stand behind, so
		// one that survives is a fault on its own, whatever the rate is over
		// the generated ones.
		if run.Kind == RunMutant && run.Origin == task.OriginHand &&
			run.Expect == task.ExpectKilled && run.Outcome == OutcomeSurvived {
			return vocab.HealthUnhealthy
		}
	}
	// A rate that is not evidence is not a rate to compare to anything, in
	// either direction. The two ways a verifier is unhealthy without one — the
	// reference failed, or a hand-written mutant survived — are both returned
	// above; what is left is a check that did not establish the verifier accepts
	// anything, which is the definition of inconclusive.
	//
	// This sits above the threshold comparison rather than below it because a
	// check narrowed with --only to the mutants alone has no reference run at
	// all: it would otherwise report `unhealthy` off a rate the same report
	// prints as "not evidence", which is the pairing record.go exists to
	// prevent, arrived at from the other side.
	if !counts.KillRateMeaningful {
		return vocab.HealthInconclusive
	}
	// Strictly below the threshold fails, so a threshold of 1 is reachable.
	if counts.KillRate != nil && *counts.KillRate < counts.KillRateMin {
		return vocab.HealthUnhealthy
	}
	if counts.KillRate == nil ||
		counts.ReferenceInconclusive > 0 || counts.Inconclusive > 0 || inconclusiveEquivalent(runs) {
		return vocab.HealthInconclusive
	}
	return vocab.HealthHealthy
}

// inconclusiveEquivalent reports an equivalent mutant that never got a
// verdict. It is left out of the rate, but it is still a run that did not
// happen, and a check with one of those has not seen everything it was asked
// to look at.
func inconclusiveEquivalent(runs []Run) bool {
	for _, run := range runs {
		if run.Expect == task.ExpectEquivalent && run.Outcome == OutcomeInconclusive {
			return true
		}
	}
	return false
}

// stem is the diff's file name without its directory or extension, which is
// what makes a label readable: mutant-0-0001-arith-add-l7c9.
func stem(diff string) string {
	base := filepath.Base(filepath.FromSlash(diff))
	return base[:len(base)-len(filepath.Ext(base))]
}

// labelStart reports whether a rune may open a label.
func labelStart(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }

// LabelMax is the longest label CMoA accepts.
const LabelMax = 64

// labelFor holds a label to the shape `cmoa verify --label` accepts,
// ^[a-z0-9][a-z0-9_-]{0,63}$.
//
// The shape is not a style rule: the label becomes part of the compose project
// name, and docker refuses an upper-case one. A mutant's file name carries the
// line and column as L7C9, so the two capitals would otherwise travel from the
// file name into a project name and fail the run — as a runner error, which
// reads as "the check could not tell" rather than "the label was wrong".
//
// The index comes first in the label, so truncation keeps what distinguishes
// two mutants and drops the readable tail.
func labelFor(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	for len(out) > 0 && !labelStart(out[0]) {
		out = out[1:]
	}
	if len(out) > LabelMax {
		out = out[:LabelMax]
	}
	return string(out)
}
