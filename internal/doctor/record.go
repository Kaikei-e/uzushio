package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// Day returns the day the check started, as the vault spells a day. A report
// with an unreadable timestamp is a report nobody can file, so it says so.
func (r *Report) Day() (string, error) {
	started, err := time.Parse(time.RFC3339, r.StartedAt)
	if err != nil {
		return "", fmt.Errorf("%w: started_at %q is not RFC 3339: %w", ErrDoctor, r.StartedAt, err)
	}
	return started.UTC().Format(vocab.DayLayout), nil
}

// Document turns the report into the vault document that records it. The
// numbers are the report's; reportPath names the file the numbers came from,
// so a reader who wants more than a verdict has somewhere to go. It is the
// caller's to choose, and the caller gives it relative to the task directory:
// a record is committed to somebody's repository, and an absolute path would
// carry the home directory of the machine that ran the check into it.
func (r *Report) Document(reportPath string) (doc.Verifier, error) {
	day, err := r.Day()
	if err != nil {
		return doc.Verifier{}, err
	}
	// A rate that is not evidence is written as a word rather than a number.
	// The record outlives the run and is read by somebody deciding whether the
	// task is usable; `kill_rate: "1.00"` beside `verdict: unhealthy` is the
	// pair a hurried reader gets backwards, and the numbers are in the report
	// the `report:` key names for anyone who wants them.
	rate := doc.NoKillRate
	if r.Aggregates.KillRate != nil {
		rate = *r.Aggregates.KillRate
		if !r.Aggregates.KillRateMeaningful {
			rate = doc.KillRateNotEvidence
		}
	}
	return doc.Verifier{
		Task:          r.Task,
		Day:           day,
		Title:         fmt.Sprintf("%s verifier: %s", r.Task, r.Verdict),
		Date:          day,
		Verdict:       r.Verdict,
		KillRate:      rate,
		Mutants:       r.Aggregates.Killed + r.Aggregates.Survived + r.Aggregates.Inconclusive + r.Aggregates.Equivalent,
		ReferenceRuns: r.Aggregates.ReferenceRuns,
		Report:        reportPath,
		Body:          strings.Join(r.Summary(), "\n"),
	}, nil
}

// Record writes the report's document into a vault, giving it the first
// sequence number the directory does not already hold, and returns the path it
// wrote, relative to the vault root.
//
// It writes and does not validate the vault around it: a health check is run
// on a developer's machine against a task that may live anywhere, and a
// command that refused to record a result because some other document in the
// vault was malformed would lose the result. The vault's own CI is what
// validates the vault.
func (r *Report) Record(vault, reportPath string) (string, error) {
	for seq := 0; seq <= 999; seq++ {
		document, err := r.Document(reportPath)
		if err != nil {
			return "", err
		}
		document.Seq = seq
		relative, err := document.Path()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrDoctor, err)
		}
		target := filepath.Join(vault, filepath.FromSlash(relative))
		if _, err := os.Stat(target); err == nil {
			continue
		}
		body, err := document.Bytes()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrDoctor, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", fmt.Errorf("%w: create %s: %w", ErrDoctor, filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil { //nolint:gosec // a record is world-readable on purpose
			return "", fmt.Errorf("%w: write %s: %w", ErrDoctor, target, err)
		}
		return relative, nil
	}
	return "", fmt.Errorf("%w: %s has been checked 1000 times today", ErrDoctor, r.Task)
}

// Summary is the health check in a few lines, for a person reading stderr and
// for the body of the document. It says what was measured before it says what
// it concluded, because the conclusion is only worth as much as the counts
// behind it.
func (r *Report) Summary() []string {
	counts := r.Aggregates
	lines := []string{
		fmt.Sprintf("task %s at %s", r.Task, short(r.Rev)),
		fmt.Sprintf("reference: %d run(s), %d failed, %d inconclusive",
			counts.ReferenceRuns, counts.ReferenceFailures, counts.ReferenceInconclusive),
		fmt.Sprintf("mutants: %d killed, %d survived, %d inconclusive, %d equivalent",
			counts.Killed, counts.Survived, counts.Inconclusive, counts.Equivalent),
		killRateLine(counts),
	}
	for _, run := range r.Runs {
		switch {
		case run.Kind == RunReference && run.Status != "pass":
			lines = append(lines, fmt.Sprintf("  %s: %s%s", run.Label, run.Status, bandText(run.Band)))
		case run.Outcome == OutcomeSurvived && run.Expect == task.ExpectKilled:
			lines = append(lines, fmt.Sprintf("  survived: %s (%s)%s",
				run.Diff, describe(run), bandText(run.Band)))
		case run.Outcome == OutcomeInconclusive:
			lines = append(lines, fmt.Sprintf("  inconclusive: %s (%s)", run.Diff, run.Status))
		case len(run.BandsBeyondReference) > 0:
			// The one thing a check with a failing reference still says. Every
			// mutant came back killed and almost none of them was detected;
			// these are the ones that broke a band the reference held.
			lines = append(lines, fmt.Sprintf("  beyond the reference: %s (%s)",
				run.Diff, strings.Join(run.BandsBeyondReference, ", ")))
		}
	}
	return append(lines, "verdict: "+r.Verdict.String())
}

// bandText names the invariants behind a banded verifier's answer, for the
// lines that report something going wrong.
//
// A summary that said only "reference-2: fail" would leave the reader to open
// report.json to learn which of eight measurements moved, and a mutant that
// survived because its invariant reported `skipped` — no k6 in the image, say —
// would look identical to one the verifier is simply blind to. The rows
// themselves stay in the report; this is the one line that says where to look.
func bandText(band *verifyrunner.Band) string {
	if band == nil {
		return ""
	}
	var parts []string
	if len(band.Failed) > 0 {
		parts = append(parts, "out of band: "+strings.Join(band.Failed, ", "))
	}
	if len(band.Skipped) > 0 {
		parts = append(parts, "skipped: "+strings.Join(band.Skipped, ", "))
	}
	if len(parts) == 0 {
		return fmt.Sprintf(" [%d invariant(s), all in band]", band.Judged)
	}
	return " [" + strings.Join(parts, "; ") + "]"
}

// killRateLine is the rate and what may be read off it.
//
// A rate measured against a verifier that rejected the reference is not a rate
// anybody may compare to a threshold, so the threshold is not printed beside
// it: the line says what the number is and then says not to use it. Printing
// `1.00 (minimum 0.80)` under five failed reference runs is how a check that
// found nothing reads as a check that found everything.
func killRateLine(counts Aggregates) string {
	line := "kill rate: " + rateText(counts.KillRate)
	if counts.KillRateMeaningful {
		return line + " (minimum " + strconv.FormatFloat(counts.KillRateMin, 'f', 2, 64) + ")"
	}
	if counts.ReferenceFailures > 0 {
		return line + " — not evidence: the reference itself failed, so every mutant fails with it"
	}
	return line + " — not evidence: no reference run reached a verdict"
}

// describe says what a mutant was, for the line that reports it surviving.
func describe(run Run) string {
	if run.Note != "" {
		return run.Note
	}
	if run.Operator != "" {
		return run.Operator
	}
	return string(run.Origin)
}

// rateText renders a rate that may not exist. A check with nothing to measure
// says so rather than printing a zero, which reads as "everything survived".
func rateText(rate *float64) string {
	if rate == nil {
		return "not measured"
	}
	return strconv.FormatFloat(*rate, 'f', 2, 64)
}

// short abbreviates a commit for a human line.
func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
