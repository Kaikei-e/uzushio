package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

func newJudgeAssessCmd() *cobra.Command {
	var opts judge.AssessmentOptions
	cmd := &cobra.Command{
		Use:   "assess",
		Short: "Assess a fixed finalist on unused holdout tasks with paired confidence intervals",
		Long: "assess verifies a frozen card, claims the holdout in a shared usage registry,\n" +
			"and runs both conditions fresh. Independent human labels and task-level\n" +
			"paired intervals produce an adopt/reject/inconclusive recommendation.\n" +
			"The recommendation does not deploy a runtime or change a calibration.\n\n" +
			"Use the same registry for all assessments. --resume continues only the\n" +
			"same assessment; it does not make a used holdout new again. --dry-run\n" +
			"checks the plan without reading holdout answers or labels or claiming it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "card", "out", "registry"); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if _, err := judge.LoadAssessmentCard(opts.CardPath); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			report, err := judge.Assess(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.DryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "dry run: holdout contents and labels remain unread; no usage was recorded")
				return nil
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "assessment recommendation: %s\n", report.Recommendation)
			fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(opts.Out, "assessment.json"))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.CardPath, "card", "", "the frozen assessment card (required)")
	cmd.Flags().StringVar(&opts.CMoA, "cmoa", "cmoa", "fallback CMoA binary for both conditions")
	cmd.Flags().StringVar(&opts.Out, "out", "", "assessment output directory (required)")
	cmd.Flags().StringVar(&opts.Registry, "registry", "", "shared holdout usage registry (required)")
	cmd.Flags().StringVar(&opts.Vault, "vault", ".", "the root for recorded trace paths")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "check metadata without opening or using the holdout")
	cmd.Flags().BoolVar(&opts.Resume, "resume", false, "resume the same pinned assessment")
	return cmd
}
