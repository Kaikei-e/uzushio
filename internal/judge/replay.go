package judge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kaikei-e/uzushio/internal/doc"
	"github.com/Kaikei-e/uzushio/internal/stats"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// This file rebuilds a calibration out of the traces it already read.
//
// A calibration is expensive — two hundred items at two seeds is four hundred
// judge runs and a couple of hours of a fleet — and the arithmetic over those
// runs is cheap and changes more often than they do. Every number in this
// package's report is a function of the traces on disk, so a report written
// before a field existed can gain it without asking a model anything.
//
// Two things a replay must not do, and both are refusals rather than
// conventions:
//
//   - It must not touch the vault document. That document is append-only
//     history, and a measurement's record is the claim somebody made on a day.
//   - It must not change the numbers that document carries. The document names
//     its report, and a reader who opens the report expects to find the
//     coefficients the document quotes. So a replay recomputes everything and
//     then checks it against what is already there, at the precision the
//     document publishes; a difference means this build reads the traces
//     differently, and the answer to that is a new calibration rather than a
//     rewritten report under a frozen document.

// ReplayOptions is one rebuild.
type ReplayOptions struct {
	// Dir is the report directory: the report.json and items.jsonl a
	// calibration wrote.
	Dir string
	// Vault is the root the journal's run directories are relative to, which
	// is the vault the calibration was run against — the journal never carries
	// an absolute path, so something has to say where the traces are.
	Vault string
}

// Replay rebuilds a calibration's report and journal from the run directories
// its journal records, running no judge.
func Replay(opts ReplayOptions) (Result, error) {
	before, err := ReadReport(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	journal, err := ReadItems(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	items := make([]measurement, 0, len(journal))
	for _, item := range journal {
		runs, err := reread(opts.Vault, item)
		if err != nil {
			return Result{}, err
		}
		// What the runs say is the arithmetic's to decide again; only the
		// identity and the human labels are carried.
		item.Runs, item.Unmeasured = nil, false
		items = append(items, measurement{item: item, runs: runs})
	}
	result, err := assemble(Options{
		Suite: Suite{
			ID: before.Suite, Face: FaceChat,
			Source: before.Source, License: before.License,
		},
		Judge:         before.Judge,
		Pool:          before.Pool,
		Day:           before.Day,
		Alpha:         before.Alpha,
		MinKappa:      before.MinKappa,
		MaxUnmeasured: before.MaxUnmeasured,
		Vault:         opts.Vault,
	}, assembly{
		items:  items,
		seeds:  before.Seeds,
		allBad: before.AllBadLabels,
		// The ceiling is measured between two people and no trace records a
		// person, so it is carried rather than recomputed. So is the all-bad
		// count above, for the same reason.
		ceiling: before.HumanHuman,
	})
	if err != nil {
		return Result{}, err
	}
	if err := agreesWith(before, result.Report); err != nil {
		return Result{}, err
	}
	return result, nil
}

// ReadReport reads a calibration report back off disk.
func ReadReport(dir string) (Report, error) {
	name := filepath.Join(dir, ReportFile)
	body, err := os.ReadFile(name) //nolint:gosec // the caller names the report
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	var report Report
	if err := json.Unmarshal(body, &report); err != nil {
		return Report{}, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	if report.SchemaVersion != ReportSchemaVersion {
		return Report{}, fmt.Errorf("%w: %s is schema version %d, this build reads %d",
			ErrJudge, name, report.SchemaVersion, ReportSchemaVersion)
	}
	return report, nil
}

// ReadItems reads a calibration journal back off disk, one item per line.
func ReadItems(dir string) ([]ItemResult, error) {
	name := filepath.Join(dir, ItemsFile)
	file, err := os.Open(name) //nolint:gosec // the caller names the journal
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJudge, err)
	}
	defer func() { _ = file.Close() }()
	var out []ItemResult
	scanner := bufio.NewScanner(file)
	// An item's error message is copied from a harness and can be long; the
	// default 64 KiB line limit is not a promise this file's writer made.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item ItemResult
		if err := json.Unmarshal([]byte(text), &item); err != nil {
			return nil, fmt.Errorf("%w: %s line %d: %w", ErrJudge, name, line, err)
		}
		if item.Item == "" {
			return nil, fmt.Errorf("%w: %s line %d names no item", ErrJudge, name, line)
		}
		out = append(out, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrJudge, name, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s holds no items to replay", ErrJudge, name)
	}
	return out, nil
}

// reread reads one item's runs back out of the traces the calibration left.
//
// A missing trace is refused rather than skipped. An item quietly dropped
// would move every rate in the report while the report still called itself a
// measurement of the whole suite, and the traces are where a replay's evidence
// comes from: without them there is nothing to recompute from.
func reread(vault string, item ItemResult) ([]Judged, error) {
	out := make([]Judged, 0, len(item.Runs))
	for _, run := range item.Runs {
		if run.RunDir == "" {
			if run.Measured {
				return nil, fmt.Errorf(
					"%w: %s seed %d records no run directory and cannot be replayed; "+
						"the trace is where a measured run's answer is",
					ErrJudge, item.Item, run.Seed)
			}
			// A run that left no trace at all — a harness that would not
			// start. The journal is its only record, it is in no coefficient
			// either way, and it is carried across so the outcome counts
			// still add up.
			out = append(out, Judged{
				Seed: run.Seed, Outcome: run.Outcome, Reason: run.Reason,
				SwapConsistent: run.SwapConsistent, InvalidRetries: run.InvalidRetries,
				LatencyMS: run.LatencyMS,
			})
			continue
		}
		dir := filepath.Join(vault, filepath.FromSlash(run.RunDir))
		if _, err := os.Stat(filepath.Join(dir, JudgeFile)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%w: %s seed %d: no %s under %s "+
					"(run directories are relative to the vault; --vault names it)",
					ErrJudge, item.Item, run.Seed, JudgeFile, run.RunDir)
			}
			return nil, fmt.Errorf("%w: %s seed %d: %w", ErrJudge, item.Item, run.Seed, err)
		}
		judged, err := ReadJudged(dir)
		if err != nil {
			return nil, err
		}
		judged.Seed = run.Seed
		out = append(out, judged)
	}
	return out, nil
}

// agreesWith refuses a replay whose numbers are not the ones the report on
// disk already carries.
//
// The comparison is at the three decimal places the document publishes and the
// verdict is decided at, so a difference in the last bits of a float is not a
// refusal — and any difference a reader of the document could see is.
func agreesWith(before, after Report) error {
	var differences []string
	note := func(name, was, now string) {
		if was != now {
			differences = append(differences, fmt.Sprintf("%s was %s, now %s", name, was, now))
		}
	}
	note("swap_kappa", publishedKappa(before.Swap.Agreement.Kappa),
		publishedKappa(after.Swap.Agreement.Kappa))
	note("rerun_kappa", publishedKappa(before.Rerun.Agreement.Kappa),
		publishedKappa(after.Rerun.Agreement.Kappa))
	note("human_kappa", publishedKappa(before.HumanKappa), publishedKappa(after.HumanKappa))
	note("n_human", fmt.Sprint(before.NHuman), fmt.Sprint(after.NHuman))
	note("n_items", fmt.Sprint(before.Items), fmt.Sprint(after.Items))
	note("verdict", before.Verdict.String(), after.Verdict.String())
	if len(differences) == 0 {
		return nil
	}
	return fmt.Errorf("%w: replaying %s changes what the calibration document says: %s. "+
		"The document is append-only history and names this report, so it cannot be left "+
		"quoting numbers the report no longer holds; measure again and write a new one",
		ErrJudge, before.Judge, strings.Join(differences, "; "))
}

// publishedKappa is a coefficient as the document writes it, which is the
// precision two calibrations are compared at.
func publishedKappa(c stats.Coefficient) string {
	if !c.Defined() {
		return vocab.KappaUnmeasured
	}
	return doc.Kappa(c.Float())
}
