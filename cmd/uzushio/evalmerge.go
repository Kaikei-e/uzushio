package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

func newJudgeLabelMergeCmd() *cobra.Command {
	var datasetPath, set, adjudicationsPath, out string
	var inputs []string
	cmd := &cobra.Command{
		Use:   "label-merge",
		Short: "Combine independent human-label imports without deciding disagreements",
		Long: "label-merge combines complete single-annotator imports. It preserves every\n" +
			"annotation and reports items on which people disagree. An optional adjudication\n" +
			"file supplies explicit human decisions; without it, a disagreement remains in\n" +
			"the output and the command returns failure after writing that reviewable file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "dataset", "set", "out"); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if len(inputs) < 2 {
				return &exitError{code: exitUsage, err: fmt.Errorf("--input needs at least two single-annotator label files")}
			}
			if _, err := os.Stat(out); err == nil {
				return &exitError{code: exitUsage, err: fmt.Errorf("label merge output %s already exists", out)}
			} else if !os.IsNotExist(err) {
				return &exitError{code: exitUsage, err: fmt.Errorf("inspect label merge output %s: %w", out, err)}
			}
			dataset, err := judge.LoadEvaluationDataset(datasetPath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			items := dataset.ItemsFor(set)
			if len(items) == 0 {
				return &exitError{code: exitUsage, err: fmt.Errorf("evaluation dataset has no %s items", set)}
			}
			loaded := make([][]judge.EvaluationLabel, 0, len(inputs))
			for _, path := range inputs {
				labels, err := judge.LoadEvaluationLabelFile(path)
				if err != nil {
					return &exitError{code: exitUsage, err: err}
				}
				loaded = append(loaded, labels)
			}
			adjudications, err := judge.ReadEvaluationAdjudications(adjudicationsPath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			merged, err := judge.MergeEvaluationLabels(items, loaded, adjudications)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			body, err := json.MarshalIndent(merged.Labels, "", "  ")
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return fmt.Errorf("create label merge output directory: %w", err)
			}
			if err := writeNewLabelFile(out, append(body, '\n')); err != nil {
				return fmt.Errorf("write label merge output: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			if len(merged.Unresolved) != 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "unresolved human disagreements: %s\n", strings.Join(merged.Unresolved, ", "))
				return &exitError{code: exitFailure}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&datasetPath, "dataset", "", "evaluation dataset metadata JSON (required)")
	cmd.Flags().StringVar(&set, "set", "", "evaluation set: D, R, or H (required)")
	cmd.Flags().StringArrayVar(&inputs, "input", nil, "single-annotator label-import JSON (repeat at least twice)")
	cmd.Flags().StringVar(&adjudicationsPath, "adjudications", "", "optional human adjudications JSON")
	cmd.Flags().StringVar(&out, "out", "", "new merged evaluation-label JSON file (required)")
	return cmd
}
