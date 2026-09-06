package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/doctor"
	"github.com/Kaikei-e/uzushio/internal/task"
	"github.com/Kaikei-e/uzushio/internal/verifyrunner"
	"github.com/Kaikei-e/uzushio/internal/vocab"
)

// newRunner is what the command uses to verify. It is a variable so a test can
// drive the whole command without docker and without a cmoa on PATH; nothing
// else replaces it.
var newRunner = func(bin string) verifyrunner.Runner { return verifyrunner.Exec{Bin: bin} }

// newTaskDoctorCmd builds the health check.
func newTaskDoctorCmd() *cobra.Command {
	var (
		taskDir    string
		cmoaBin    string
		vaultDir   string
		outDir     string
		replayPath string
		reusePath  string
		only       []string
		parallel   int
		timeout    time.Duration
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Measure a task's verifier against its reference solution and its mutants",
		Long: "doctor verifies the task's reference solution reference_runs times and each of\n" +
			"its mutants once, and reports what the verifier did. A reference run that fails\n" +
			"is a false positive; a mutant that passes is a defect the verifier cannot see.\n\n" +
			"The report goes to <task>/doctor/<run-id>/report.json. Exit 0 is healthy, 1 is\n" +
			"unhealthy, 2 is a usage or task error and 3 is inconclusive.\n\n" +
			"--replay <report.json> recomputes an existing report from the runs it already\n" +
			"holds and rewrites it in place, keeping its run id. Nothing is verified: no\n" +
			"cmoa, no docker, no worktree. It is how a report written before a field\n" +
			"existed gains it, and how a record can be written for a check that has\n" +
			"already run.\n\n" +
			"A full check of a slow verifier is an hour and a half, and most questions are\n" +
			"smaller than that. --only reference, --only mutants, or --only <label-or-diff>\n" +
			"runs part of it, and repeats. --reuse-reference <report.json> takes the\n" +
			"reference block from an earlier check instead of running it again, and refuses\n" +
			"unless that check was of the same task, at the same revision, under the same\n" +
			"verifier — which the report's environment fingerprint is what decides.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loaded, err := task.Load(taskDir)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			errOut := cmd.ErrOrStderr()

			var (
				report     *doctor.Report
				reportFile string
			)
			if replayPath != "" {
				// The refused flags are the ones that say how to run something.
				// A replay runs nothing, and a command that accepted --parallel
				// and then ignored it would be lying about what it did.
				if err := refuseWithReplay(cmd, "recomputes a report and verifies nothing",
					"parallel", "timeout", "out", "only", "reuse-reference"); err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				report, err = doctor.ReadReport(replayPath)
				if err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				report.Conclude()
				if err := report.Write(replayPath); err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				reportFile = replayPath
			} else {
				if err := loaded.RequireDoctorable(); err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				if err := loaded.RequireRunnableVerifier(); err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				var earlier *doctor.Report
				if reusePath != "" {
					earlier, err = doctor.ReadReport(reusePath)
					if err != nil {
						return &exitError{code: exitUsage, err: err}
					}
				}
				ctx := cmd.Context()
				if timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
				}
				report, err = doctor.Check(ctx, doctor.Options{
					Task:   loaded,
					Runner: newRunner(cmoaBin),
					Dir:    outDir,
					Only:   doctor.Only(only),
					Reuse:  earlier,
					// A banded verifier is held to one verification at a time
					// unless the flag was typed; Changed is what tells the two
					// apart, because the flag's default and a typed 2 are the same
					// integer.
					Parallel:         parallel,
					ParallelExplicit: cmd.Flags().Changed("parallel"),
					Warn:             func(line string) { fmt.Fprintln(errOut, line) },
				})
				// Every failure to get a check at all — a task uzushio cannot
				// read, an output directory that already holds one, a worktree
				// that would not build — is a usage error rather than a verdict.
				if err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				reportFile = filepath.Join(outDir, doctor.ReportFile)
				if outDir == "" {
					reportFile = filepath.Join(loaded.Dir, "doctor", report.RunID, doctor.ReportFile)
				}
			}

			for _, line := range report.Summary() {
				fmt.Fprintln(errOut, line)
			}
			reportPath, insideTask := reportPathFor(loaded, reportFile)
			if insideTask {
				fmt.Fprintf(errOut, "report: %s\n", reportPath)
			} else {
				fmt.Fprintf(errOut, "report: %s\n", reportFile)
			}

			if vaultDir != "" {
				recorded := reportPath
				if !insideTask {
					// A record is committed to somebody's repository, and the
					// one thing it must not gain on the way in is the home
					// directory of the machine that ran the check. A report
					// kept outside the task cannot be named relative to it, so
					// it is not named at all.
					recorded = ""
					fmt.Fprintln(errOut,
						"warning: the report is outside the task directory, so the record names no report")
				}
				written, err := report.Record(vaultDir, recorded)
				if err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				fmt.Fprintf(errOut, "recorded: %s\n", filepath.Join(vaultDir, written))
			}
			if asJSON {
				body, err := report.Bytes()
				if err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				fmt.Fprint(cmd.OutOrStdout(), string(body))
			}
			return verdictError(report.Verdict)
		},
	}
	cmd.Flags().StringVar(&taskDir, "task", ".", "the CMoA task directory")
	cmd.Flags().StringVar(&cmoaBin, "cmoa", "", "the cmoa binary (default: cmoa on PATH)")
	cmd.Flags().StringVar(&vaultDir, "vault", "", "a DocDag vault to record the result in")
	cmd.Flags().StringVar(&outDir, "out", "", "where to write the report (default: <task>/doctor/<run-id>)")
	cmd.Flags().StringVar(&replayPath, "replay", "",
		"recompute this report.json in place from the runs it holds, verifying nothing")
	cmd.Flags().StringArrayVar(&only, "only", nil,
		"run only these verifications: reference, mutants, or a run label or mutant diff name; repeat for more")
	cmd.Flags().StringVar(&reusePath, "reuse-reference", "",
		"take the reference runs from this report.json instead of running them, "+
			"if it measured the same task at the same revision under the same verifier")
	cmd.Flags().IntVar(&parallel, "parallel", doctor.DefaultParallel, "how many verifications to run at once")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"give up on the whole check after this long (default: no limit)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report on stdout")
	return cmd
}

// refuseWithReplay reports the first of the named flags that was typed
// alongside --replay.
//
// They are refused rather than ignored. Each of them says how to run
// something, a replay runs nothing, and the failure mode of accepting them is
// somebody believing a report was re-measured under a timeout it never had.
//
// does is what this command's replay does and does not do, so the message
// names the command's own job rather than a generic one.
func refuseWithReplay(cmd *cobra.Command, does string, names ...string) error {
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--replay %s, so --%s has nothing to do; drop one of the two",
				does, name)
		}
	}
	return nil
}

// reportPathFor is a report's path relative to the task directory, and whether
// it is under it at all.
//
// Only a path under the task is a path a record may carry: the record is
// committed to somebody's repository, and a report kept elsewhere can only be
// named absolutely, which would put the machine that ran the check into the
// document. The caller warns and records no report rather than naming one.
func reportPathFor(loaded *task.Task, reportFile string) (string, bool) {
	abs, err := filepath.Abs(reportFile)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(loaded.Dir, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

// verdictError turns the verdict into the process's answer. The check has
// already said what it found, so an unhealthy or inconclusive result carries a
// code and no message: printing "Error: unhealthy" after the summary that
// explained why would be noise.
func verdictError(verdict vocab.Health) error {
	switch verdict {
	case vocab.HealthHealthy:
		return nil
	case vocab.HealthUnhealthy:
		return &exitError{code: exitFailure}
	case vocab.HealthInconclusive:
		return &exitError{code: exitInconclusive}
	}
	return fmt.Errorf("unknown verdict %q", verdict)
}
