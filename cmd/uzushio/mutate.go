package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/mutate"
	"github.com/Kaikei-e/uzushio/internal/task"
)

// newTaskMutateCmd builds the mutant generator.
func newTaskMutateCmd() *cobra.Command {
	var (
		taskDir       string
		operators     string
		language      string
		maximum       int
		dryRun        bool
		keepNonViable bool
	)
	cmd := &cobra.Command{
		Use:   "mutate",
		Short: "Write mutants of a task's reference solution",
		Long: "mutate applies the reference diff to a throwaway worktree, finds the sites the\n" +
			"named operators answer to, and writes one diff per site under mutants/ — each a\n" +
			"defect introduced into a working solution. The diffs are appended to task.json\n" +
			"with origin: generated, and are what `uzushio task doctor` measures the\n" +
			"verifier with.\n\n" +
			"Operators: arith (+ for -, * for /), cond (negate an if), bound (< for <=,\n" +
			"> for >=, == for !=), const (an integer literal plus one), stmt (remove a\n" +
			"statement), ret (return the zero value).\n\n" +
			"A candidate that does not compile is dropped, because a mutant the toolchain\n" +
			"refuses is scored killed by a verifier that does nothing but build. Generating\n" +
			"therefore needs a Go toolchain as well as git.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if language != "go" {
				return &exitError{code: exitUsage, err: fmt.Errorf("--lang %q: only go is implemented", language)}
			}
			if maximum < 1 {
				return &exitError{code: exitUsage, err: fmt.Errorf("--max %d: must be positive", maximum)}
			}
			ops, err := mutate.Operators(operators)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			loaded, err := task.Load(taskDir)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			// Asked before anything is written, and not after. The rewrite
			// refuses a manifest holding a key uzushio would drop; discovering
			// that at the end would leave the diffs on disk, and the retry
			// after the key was removed would write every one of them again
			// under a new number.
			if !dryRun {
				if err := task.CheckRewritable(loaded.Dir); err != nil {
					return &exitError{code: exitUsage, err: err}
				}
			}

			plan, err := mutate.Generate(cmd.Context(), loaded, mutate.Options{
				Operators:     ops,
				Max:           maximum,
				KeepNonViable: keepNonViable,
			})
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}

			out := cmd.OutOrStdout()
			for _, mutant := range plan.Mutants {
				fmt.Fprintf(out, "%s\t%s\t%s\n", mutant.Path, mutant.Operator, mutant.Note)
			}
			errOut := cmd.ErrOrStderr()
			for _, skipped := range plan.Skipped {
				fmt.Fprintf(errOut, "skipped %s: %s\n", skipped.File, skipped.Reason)
			}
			if total := plan.NotViableTotal(); total > 0 {
				fmt.Fprintf(errOut, "dropped %d candidate(s) that do not compile (%s)\n",
					total, notViableByOperator(plan))
			}
			if dryRun {
				fmt.Fprintf(errOut, "%d mutant(s) would be written; nothing was\n", len(plan.Mutants))
				return nil
			}
			if len(plan.Mutants) == 0 {
				fmt.Fprintln(errOut, "no new mutants: every site the operators found is already covered")
				return nil
			}
			entries, err := mutate.Write(loaded, plan.Mutants)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			manifest, err := task.ReadManifest(loaded.Dir)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			manifest.Mutants = append(manifest.Mutants, entries...)
			if err := manifest.Write(loaded.Dir); err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			fmt.Fprintf(errOut, "wrote %d mutant(s) and appended them to %s\n",
				len(entries), task.ManifestFile)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskDir, "task", ".", "the CMoA task directory")
	cmd.Flags().StringVar(&operators, "operators", strings.Join(
		mutate.OperatorNames(mutate.AllOperators()), ","), "which operators to apply")
	cmd.Flags().StringVar(&language, "lang", "go", "the language to mutate")
	cmd.Flags().IntVar(&maximum, "max", mutate.DefaultMax, "how many new mutants to write at most")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be written and write nothing")
	cmd.Flags().BoolVar(&keepNonViable, "keep-nonviable", false,
		"keep mutants that do not compile (any verifier that builds scores them killed)")
	return cmd
}

// notViableByOperator renders the dropped counts in the operators' own order,
// so the line says which operator is producing them rather than only how many.
func notViableByOperator(plan *mutate.Plan) string {
	var parts []string
	for _, op := range mutate.AllOperators() {
		if n := plan.NotViable[op]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", op, n))
		}
	}
	return strings.Join(parts, ", ")
}
