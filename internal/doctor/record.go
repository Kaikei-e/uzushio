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
	rate := doc.NoKillRate
	if r.Aggregates.KillRate != nil {
		rate = *r.Aggregates.KillRate
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
		"kill rate: " + rateText(counts.KillRate) +
			" (minimum " + strconv.FormatFloat(counts.KillRateMin, 'f', 2, 64) + ")",
	}
	for _, run := range r.Runs {
		switch {
		case run.Kind == RunReference && run.Status != "pass":
			lines = append(lines, fmt.Sprintf("  %s: %s", run.Label, run.Status))
		case run.Outcome == OutcomeSurvived && run.Expect == task.ExpectKilled:
			lines = append(lines, fmt.Sprintf("  survived: %s (%s)", run.Diff, describe(run)))
		case run.Outcome == OutcomeInconclusive:
			lines = append(lines, fmt.Sprintf("  inconclusive: %s (%s)", run.Diff, run.Status))
		}
	}
	return append(lines, "verdict: "+r.Verdict.String())
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
