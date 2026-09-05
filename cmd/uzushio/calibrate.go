package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/calibrate"
	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/task"
)

// DefaultBandsFile is the name a calibration gets in the task directory. It is
// committed: a band is an argument, and an argument nobody can read the diff of
// is not reviewable.
const DefaultBandsFile = "bands.json"

// newTaskCalibrateCmd builds the re-centring command.
func newTaskCalibrateCmd() *cobra.Command {
	var (
		taskDir  string
		from     []string
		out      string
		keep     []string
		dryRun   bool
		rule     = calibrate.DefaultRule()
		centre   string
		spread   string
		lower    string
		minRuns  int
		kFactor  float64
		ciMult   float64
		relFloor float64
		floorUS  float64
		floorMS  float64
	)
	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Re-centre a banded verifier's bands on this host, from the doctor's own reports",
		Long: "calibrate reads the band rows of every reference run in the reports it is given\n" +
			"and writes the bands this host should hold the verifier to.\n\n" +
			"A band is a claim about a machine, and a banded verifier whose bands were\n" +
			"calibrated on another one rejects every solution — which makes the kill rate it\n" +
			"then reports the false positive read back rather than detection.\n\n" +
			"The input is doctor reports and nothing else: each band row carries both the\n" +
			"value measured and the band it was judged against, so the reports already hold\n" +
			"what this host reads and what it was expected to read. Nothing is read out of\n" +
			"the task's repository.\n\n" +
			"The output is bands.json, which is uzushio's shape rather than any gate's.\n" +
			"Turning it into the file a particular verifier reads is the task's own adapter.\n\n" +
			"A band the reference never left is copied unchanged rather than widened, an\n" +
			"invariant named with --keep is never derived, and a quantised invariant that\n" +
			"left its band is refused rather than derived. Fewer reference runs than\n" +
			"--min-runs is an error, not a warning.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loaded, err := task.Load(taskDir)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if len(from) == 0 {
				return &exitError{code: exitUsage, err: fmt.Errorf(
					"calibrate reads measurements it did not take: name at least one report with --from")}
			}
			rule.Centre = calibrate.Centre(centre)
			rule.Spread = calibrate.Spread(spread)
			rule.Lower = calibrate.Lower(lower)
			rule.MinRuns = minRuns
			rule.K = kFactor
			rule.CIMultiplier = ciMult
			rule.RelFloor = relFloor
			rule.FloorUS = floorUS
			rule.FloorMS = floorMS
			rule.Keep = keep

			reports := make([]*doctor.Report, 0, len(from))
			for _, path := range from {
				report, err := doctor.ReadReport(path)
				if err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				reports = append(reports, report)
			}
			result, err := calibrate.Calibrate(calibrate.Input{Reports: reports, Rule: rule})
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			bands, err := result.JSON()
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			body, err := bands.Bytes()
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}

			errOut := cmd.ErrOrStderr()
			for _, line := range result.Summary() {
				fmt.Fprintln(errOut, line)
			}
			for _, line := range result.Table() {
				fmt.Fprintln(errOut, line)
			}

			target := out
			if target == "" {
				target = filepath.Join(loaded.Dir, DefaultBandsFile)
			}
			if dryRun {
				// The file on stdout and the reasoning on stderr, so a person
				// can read the second and a pipe can take the first.
				fmt.Fprint(cmd.OutOrStdout(), string(body))
				fmt.Fprintf(errOut, "dry run: nothing written (would write %s)\n", target)
				return nil
			}
			if err := os.WriteFile(target, body, 0o644); err != nil { //nolint:gosec // a bands file is world-readable on purpose
				return &exitError{code: exitUsage, err: fmt.Errorf("write %s: %w", target, err)}
			}
			fmt.Fprintf(errOut, "wrote: %s\n", target)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskDir, "task", ".", "the CMoA task directory")
	cmd.Flags().StringArrayVar(&from, "from", nil,
		"a doctor report.json to read reference runs from; repeat for more")
	cmd.Flags().StringVar(&out, "out", "",
		"where to write the bands (default: <task>/"+DefaultBandsFile+")")
	cmd.Flags().StringArrayVar(&keep, "keep", nil,
		"never re-centre this invariant, whatever was measured — for a band whose centre is "+
			"arithmetic rather than a property of the host; repeat for more")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the file on stdout and write nothing")
	cmd.Flags().IntVar(&minRuns, "min-runs", rule.MinRuns,
		"refuse to derive a band from fewer reference runs than this")
	cmd.Flags().StringVar(&centre, "centre", string(rule.Centre), "what a band is centred on: median or mean")
	cmd.Flags().StringVar(&spread, "spread", string(rule.Spread),
		"the spread a half-width is built from: stdev or mad")
	cmd.Flags().StringVar(&lower, "lower", string(rule.Lower),
		"the lower bound: derive (centre − half-width), keep (the original lo) or zero")
	cmd.Flags().Float64Var(&kFactor, "k", rule.K,
		"multiply the spread by this; 0 uses the tolerance factor k2(N, 0.95, 0.90)")
	cmd.Flags().Float64Var(&ciMult, "ci-multiplier", rule.CIMultiplier,
		"multiply the median reported half-range, as a floor under the half-width")
	cmd.Flags().Float64Var(&relFloor, "rel-floor", rule.RelFloor,
		"a share of the centre the half-width may not fall below")
	cmd.Flags().Float64Var(&floorUS, "floor-us", rule.FloorUS, "absolute half-width floor for a _us invariant")
	cmd.Flags().Float64Var(&floorMS, "floor-ms", rule.FloorMS, "absolute half-width floor for a _ms invariant")
	return cmd
}
