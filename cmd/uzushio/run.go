package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/loop"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// newRunCmd builds `uzushio run`, which measures one candidate edit against
// the harness the vault already describes and writes down what it found.
func newRunCmd() *cobra.Command {
	var (
		options loop.Options
		mode    string
		replay  string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Measure a candidate harness edit against the baseline",
		Long: "run renders the baseline harness and the same harness with the candidate in\n" +
			"it, then runs every task of the suite on both at the same seeds and compares\n" +
			"the pairs. The test is anytime-valid, so a split stops the moment it is\n" +
			"decided or at its cap, with no correction for having looked.\n" +
			"\n" +
			"Expect `inconclusive` more often than anything else. At tens of tasks a\n" +
			"ten-point difference is not reliably detectable, and the interval on the\n" +
			"pass-rate difference is the honest output; the verdict is a convenience read\n" +
			"off it. What the run writes is meant to be enough to re-derive every decision\n" +
			"offline, which --replay checks.\n" +
			"\n" +
			"It exits 2 without spending anything when the thing it was asked to measure is\n" +
			"not measurable: the edit is not `proposed`; its component has no injection point\n" +
			"in the rendered harness; its paths and touches disagree; it predicts nothing; a\n" +
			"suite split is under the floor; the proposer ids do not make a legal model slug;\n" +
			"or adding the edit leaves the rendered harness unchanged. Each says which.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if replay != "" {
				return runReplay(cmd, replay)
			}
			options.Mode = loop.Mode(mode)
			options.Version = version()
			if options.Out == "" {
				// Beside the suite rather than beside the caller. A run writes
				// rendered harnesses, journals and a baseline cache, and the
				// cache is deliberately shared between sibling runs; putting
				// all of it under the suite keeps one ignore rule over it and
				// keeps it out of whatever directory somebody happened to be
				// standing in.
				options.Out = filepath.Join(filepath.Dir(options.Suite), "runs", doctor.NewRunID(time.Now()))
			}
			options.Log = func(line string) { fmt.Fprintln(cmd.ErrOrStderr(), line) }

			result, err := loop.Run(cmd.Context(), options)
			if err != nil {
				if errors.Is(err, loop.ErrRefused) {
					return &exitError{code: exitUsage, err: err}
				}
				return err
			}
			report(cmd, options, result)
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&options.Edit, "edit", "", "the candidate edit's identifier, as he-0001")
	flags.StringVar(&options.Suite, "suite", "", "path to the suite file")
	flags.StringVar(&options.Vault, "vault", ".", "vault root")
	flags.StringVar(&options.CMoA, "cmoa", "cmoa", "the harness binary")
	flags.StringVar(&options.Config, "config", "", "the harness configuration file; required, because it names the fleet")
	flags.StringVar(&mode, "mode", string(loop.ModeScreening), "screening or confirm")
	flags.IntVar(&options.Parallel, "parallel", 1,
		"pairs in flight at once; the statistic is read after each batch, so a larger number may overshoot the stopping point")
	flags.StringVar(&options.Out, "out", "", "run directory (default <suite dir>/runs/<run-id>)")
	flags.BoolVar(&options.AA, "aa", false,
		"calibrate: run the baseline against itself and report how often it disagrees with itself")
	flags.BoolVar(&options.DryRun, "dry-run", false, "render, check and plan; run nothing")
	flags.StringVar(&options.AsOf, "as-of", "", "day to read the baseline for, YYYY-MM-DD (default today, UTC)")
	flags.StringVar(&options.DocDag, "docdag", defaultDocDag(), "docdag binary")
	flags.BoolVar(&options.NoCache, "no-cache", false, "do not reuse remembered baseline outcomes")
	flags.StringVar(&replay, "replay", "",
		"recompute the verdicts of a finished run from its journal and report any disagreement")
	return cmd
}

// report prints what the run concluded. The interval goes first: it is the
// finding, and the verdict is a word read off it.
func report(cmd *cobra.Command, options loop.Options, result loop.Result) {
	out := cmd.OutOrStdout()
	if result.Header.DryRun {
		fmt.Fprintf(out, "dry-run %s %s\n", result.Header.Edit, result.Header.Mode)
		fmt.Fprintf(out, "baseline %s\ncandidate %s\n",
			result.Header.Baseline.TreeSHA256, result.Header.Candidate.TreeSHA256)
		for _, size := range result.Header.Suite.Sizes {
			fmt.Fprintf(out, "%s %d tasks x %d repeats\n", size.Split, size.Tasks, result.Header.Repeats)
		}
		fmt.Fprintf(out, "%s\n", options.Out)
		return
	}
	if result.AA != nil {
		fmt.Fprintf(out, "aa pairs=%d discordance=%.3f\n", result.AA.Pairs, result.AA.Discordance)
		if result.AA.Warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", result.AA.Warning)
		}
	}
	for _, split := range vocab.AllSplits() {
		reading, ran := result.Splits[split]
		if !ran {
			continue
		}
		fmt.Fprintf(out, "%s %s pairs=%d b=%d c=%d ties=%d delta=[%+.3f,%+.3f]\n",
			split, reading.Verdict, reading.Pairs,
			reading.Evidence.Wins, reading.Evidence.Losses, reading.Evidence.Ties,
			reading.Delta.Lo, reading.Delta.Hi)
	}
	fmt.Fprintf(out, "promote=%v status=%s applied=%v\n",
		result.Header.Promote, result.Header.Transition.Status, result.Header.Transition.Applied)
	for _, name := range result.Written {
		fmt.Fprintf(out, "wrote %s\n", name)
	}
	if result.Header.Transition.Instruction != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", result.Header.Transition.Instruction)
	}
	fmt.Fprintf(out, "%s\n", options.Out)
}

// runReplay recomputes a finished run's verdicts from its journal.
func runReplay(cmd *cobra.Command, name string) error {
	comparison, err := loop.Replay(name)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, split := range vocab.AllSplits() {
		reading, ran := comparison.Recomputed[split.String()]
		if !ran {
			continue
		}
		fmt.Fprintf(out, "%s %s pairs=%d b=%d c=%d ties=%d\n",
			split, reading.Verdict, reading.Pairs,
			reading.Evidence.Wins, reading.Evidence.Losses, reading.Evidence.Ties)
	}
	if comparison.Identical() {
		fmt.Fprintln(out, "identical")
		return nil
	}
	for _, anomaly := range comparison.Anomalies {
		fmt.Fprintf(cmd.ErrOrStderr(), "replay: %s\n", anomaly)
	}
	return &exitError{code: exitFailure, err: errors.New("")}
}
