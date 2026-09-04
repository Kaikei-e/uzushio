package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/vault"
)

// newDocDagConfigCmd builds the generator command. The configuration is
// assembled in Go and rendered deterministically, so writing it and checking it
// are the same operation read two ways.
func newDocDagConfigCmd() *cobra.Command {
	var (
		out   string
		check bool
	)
	cmd := &cobra.Command{
		Use:   "docdag-config",
		Short: "Write the generated docdag.yaml",
		Long: "docdag-config renders the DocDag configuration assembled in internal/vault.\n" +
			"With --check it writes nothing and reports whether the file on disk is the\n" +
			"one the generator would write, which is what CI runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			generated, err := vault.Generate()
			if err != nil {
				return err
			}
			if check {
				return checkFile(cmd, out, generated)
			}
			if err := os.WriteFile(out, generated, 0o644); err != nil { //nolint:gosec // a configuration file is world-readable on purpose
				return fmt.Errorf("write %s: %w", out, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "docdag.yaml", "path to write the configuration to")
	cmd.Flags().BoolVar(&check, "check", false, "do not write; fail when the file on disk is stale")
	return cmd
}

// checkFile compares the file on disk against what the generator writes. The
// difference goes to stdout, because it is the answer to the question the
// caller asked rather than a diagnostic about the run.
func checkFile(cmd *cobra.Command, path string, generated []byte) error {
	onDisk, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(cmd.OutOrStdout(), "%s does not exist; run uzushio docdag-config\n", path)
		return errStale
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	}
	if bytes.Equal(onDisk, generated) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date\n", path)
		return nil
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s is stale; run uzushio docdag-config\n", path)
	for _, line := range diff(path, string(onDisk), string(generated)) {
		fmt.Fprintln(out, line)
	}
	return errStale
}
