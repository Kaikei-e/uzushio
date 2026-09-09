package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/judge"
)

func newJudgeLabelPacketCmd() *cobra.Command {
	var datasetPath, suitePath, set, annotator, out string
	var seed int64
	cmd := &cobra.Command{
		Use:   "label-packet",
		Short: "Prepare one anonymous human-label packet from an evaluation set",
		Long: "label-packet prepares material for one independent human annotation. It shuffles\n" +
			"candidate answers into anonymous labels and keeps their identities in a separate\n" +
			"mapping. It does not create a label, adjudicate a disagreement, or inspect gold.\n\n" +
			"Generate H packets only after finalists are fixed, so held-out material cannot\n" +
			"inform finalist selection. Each annotator needs an independent packet and answer.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "dataset", "suite", "set", "annotator", "out", "seed"); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			dataset, err := judge.LoadEvaluationDataset(datasetPath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			suite, err := judge.LoadSuite(suitePath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			packet, mapping, err := judge.BuildEvaluationPacket(dataset, suite, set, annotator, seed)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if err := judge.WriteEvaluationPacket(out, packet, mapping); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&datasetPath, "dataset", "", "evaluation dataset metadata JSON (required)")
	cmd.Flags().StringVar(&suitePath, "suite", "", "suite manifest JSON (required)")
	cmd.Flags().StringVar(&set, "set", "", "evaluation set: D, R, or H (required)")
	cmd.Flags().StringVar(&annotator, "annotator", "", "opaque annotator identifier recorded with the packet (required)")
	cmd.Flags().StringVar(&out, "out", "", "new output directory (required)")
	cmd.Flags().Int64Var(&seed, "seed", 0, "deterministic candidate-shuffle seed (required)")
	return cmd
}

func newJudgeLabelImportCmd() *cobra.Command {
	var packetDir, answersPath, out string
	cmd := &cobra.Command{
		Use:   "label-import",
		Short: "Translate one completed anonymous packet into one human annotation per item",
		Long: "label-import verifies the packet mapping, its input hashes, and the completed\n" +
			"answer file before translating anonymous labels. It emits one independent human\n" +
			"annotation per item; it does not merge annotators or invent adjudication.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "packet", "answers", "out"); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if _, err := os.Stat(out); err == nil {
				return &exitError{code: exitUsage, err: fmt.Errorf("label output %s already exists", out)}
			} else if !os.IsNotExist(err) {
				return &exitError{code: exitUsage, err: fmt.Errorf("inspect label output %s: %w", out, err)}
			}
			labels, err := judge.ImportEvaluationPacket(packetDir, answersPath)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			body, err := json.MarshalIndent(labels, "", "  ")
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return fmt.Errorf("create label output directory: %w", err)
			}
			if err := writeNewLabelFile(out, append(body, '\n')); err != nil {
				return fmt.Errorf("write label output: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&packetDir, "packet", "", "packet directory with mapping.json (required)")
	cmd.Flags().StringVar(&answersPath, "answers", "", "completed packet answer JSON (required)")
	cmd.Flags().StringVar(&out, "out", "", "new evaluation-label JSON file (required)")
	return cmd
}

// Claim the output when writing too, so concurrent imports cannot overwrite
// an annotation that appeared after the early path check.
func writeNewLabelFile(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
