package main

import (
	"github.com/spf13/cobra"
)

// newTaskCmd is the parent of the commands that read a CMoA task directory.
//
// It runs nothing itself. Cobra's own answer for a parent with no Run is to
// print the help and exit zero, and a zero exit for an invocation that did no
// work is the one thing a script cannot recover from — so `uzushio task` on
// its own prints the help and exits with the usage code, the way an unknown
// command does.
func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Work on a CMoA task directory",
		Long: "task holds the commands that read a CMoA task: doctor measures the task's\n" +
			"verifier, and mutate writes the mutants doctor measures it with.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return &exitError{code: exitUsage}
		},
	}
	cmd.AddCommand(newTaskDoctorCmd(), newTaskMutateCmd())
	return cmd
}
