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
		taskDir  string
		cmoaBin  string
		vaultDir string
		outDir   string
		parallel int
		timeout  time.Duration
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Measure a task's verifier against its reference solution and its mutants",
		Long: "doctor verifies the task's reference solution reference_runs times and each of\n" +
			"its mutants once, and reports what the verifier did. A reference run that fails\n" +
			"is a false positive; a mutant that passes is a defect the verifier cannot see.\n\n" +
			"The report goes to <task>/doctor/<run-id>/report.json. Exit 0 is healthy, 1 is\n" +
			"unhealthy, 2 is a usage or task error and 3 is inconclusive.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loaded, err := task.Load(taskDir)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := loaded.RequireDoctorable(); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := loaded.RequireExitCodeVerifier(); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			report, err := doctor.Check(ctx, doctor.Options{
				Task:     loaded,
				Runner:   newRunner(cmoaBin),
				Dir:      outDir,
				Parallel: parallel,
			})
			// Every failure to get a check at all — a task uzushio cannot
			// read, an output directory that already holds one, a worktree
			// that would not build — is a usage error rather than a verdict.
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}

			errOut := cmd.ErrOrStderr()
			for _, line := range report.Summary() {
				fmt.Fprintln(errOut, line)
			}
			reportPath, insideTask := reportPathFor(loaded, outDir, report.RunID)
			if insideTask {
				fmt.Fprintf(errOut, "report: %s\n", reportPath)
			} else {
				fmt.Fprintf(errOut, "report: %s\n", filepath.Join(outDir, "report.json"))
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
						"warning: --out is outside the task directory, so the record names no report")
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
	cmd.Flags().IntVar(&parallel, "parallel", doctor.DefaultParallel, "how many verifications to run at once")
	cmd.Flags().DurationVar(&timeout, "timeout", 0,
		"give up on the whole check after this long (default: no limit)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report on stdout")
	return cmd
}

// reportPathFor is the report's path relative to the task directory, and
// whether it is under it at all.
//
// Only a path under the task is a path a record may carry: the record is
// committed to somebody's repository, and a report kept elsewhere can only be
// named absolutely, which would put the machine that ran the check into the
// document. The caller warns and records no report rather than naming one.
func reportPathFor(loaded *task.Task, outDir, runID string) (string, bool) {
	if outDir == "" {
		return filepath.ToSlash(filepath.Join("doctor", runID, "report.json")), true
	}
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(loaded.Dir, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(filepath.Join(relative, "report.json")), true
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
