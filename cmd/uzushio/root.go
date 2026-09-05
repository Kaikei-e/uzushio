// Package main is the uzushio command line.
//
// `uzushio docdag-config` writes the docdag.yaml the vault is checked under,
// and `--check` says whether the file on disk is the one the code would write.
// `uzushio task doctor` measures a CMoA task's verifier against its reference
// solution and its mutants, `uzushio task mutate` writes the mutants it is
// measured with, and `uzushio task calibrate` re-centres a banded verifier's
// tolerances on the host that runs it. `uzushio harness render` materialises
// the harness a day's binding edits describe. `uzushio improve` mines failure
// patterns out of CMoA's traces and, with `--propose`, asks the proposers for
// the harness edits that answer them; `uzushio run` measures one of those edits
// against the baseline harness on a suite and writes what it found into the vault.
// `uzushio judge` measures the other instrument in the loop: `import-mtbench`
// derives a three-way calibration suite from a corpus of pairwise human
// judgments, `calibrate` runs a judge over it and records the three
// coefficients, and `status` says which calibrations still bind.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// exit codes. They are the contract a CI step reads, so they are named.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	// exitInconclusive is `task doctor` saying it did not get an answer. It is
	// its own code because "the verifier is broken" and "the check could not
	// tell" call for different things: the first is a bug to fix, the second is
	// a run to repeat.
	exitInconclusive = 3
)

// errStale is returned by `docdag-config --check` when the file on disk is not
// what the generator writes. The difference has already been printed by then,
// so the error carries no message of its own — executeWith prints nothing for
// an empty one and only maps it to an exit code.
var errStale = errors.New("")

// newRootCmd builds a fresh command tree. It is a constructor rather than a
// package-level variable so that every invocation — and so every test — gets a
// tree with no parsed flags left over from the last one.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "uzushio",
		Short: "The uzushio specification toolchain",
		Long: "uzushio generates and checks the DocDag configuration the specification " +
			"vault is validated under.",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// The command set is the contract; cobra's generated completion command is
	// not part of it.
	root.CompletionOptions.DisableDefaultCmd = true
	// cobra returns a flag error the same way it returns a failure from RunE,
	// and the two deserve different exit codes. Marking it here is what lets
	// exitCode tell them apart without reading the message.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &flagError{err: err}
	})
	root.AddCommand(newDocDagConfigCmd(), newHarnessCmd(), newImproveCmd(), newJudgeCmd(),
		newRunCmd(), newTaskCmd(), newVersionCmd())
	return root
}

// Execute runs the CLI and returns the process exit code.
//
// The context is cancelled on an interrupt, and every long-running command
// carries it down. Without one, Ctrl-C ends the process without running a
// single deferred function — and `task doctor` and `task mutate` both hold a
// git worktree open for the length of a run, whose registration in the task's
// repository outlives the process and is only cleared by `git worktree prune`.
// Interrupting a check that is waiting on a container is the ordinary way to
// stop one, so the cleanup has to survive it.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeWith(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

// executeWith is Execute with injectable context, arguments and streams, so
// the mapping from a failure to an exit code is testable without a subprocess.
func executeWith(ctx context.Context, args []string, out, errOut io.Writer) int {
	root := newRootCmd()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil && err.Error() != "" {
		fmt.Fprintf(errOut, "Error: %v\n", err)
	}
	return exitCode(err)
}

// exitCode maps a command failure onto the process's answer. A usage mistake
// and a job that ran and found something wrong are different things, and a CI
// step that only looks at "did it exit non-zero" still gets the right answer.
func exitCode(err error) int {
	var coded *exitError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &coded):
		return coded.code
	case errors.Is(err, errStale):
		return exitFailure
	case isUsageError(err):
		return exitUsage
	}
	return exitFailure
}

// exitError is a failure that names the code it wants. A command that has
// already printed what it found wraps nothing at all: executeWith prints no
// message for an empty error, so the code travels without a second sentence
// after the summary that explained it.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// isUsageError reports whether cobra rejected the invocation rather than the
// command rejecting its work. A flag error is marked by the root's
// FlagErrorFunc; an unknown command is not markable — cobra builds that error
// itself and exports no type for it — so it is recognised by its wording.
func isUsageError(err error) bool {
	var flagErr *flagError
	if errors.As(err, &flagErr) {
		return true
	}
	// cobra builds two more usage failures itself and exports a type for
	// neither: an unknown subcommand, and a required flag left off. Both are
	// the invocation being wrong rather than the command finding something
	// wrong, so both take the usage exit code.
	text := err.Error()
	return strings.HasPrefix(text, "unknown command") || strings.HasPrefix(text, "required flag")
}

// flagError marks an invocation cobra refused. cobra does not export a type
// for it, so the root command's FlagErrorFunc wraps what pflag reported.
type flagError struct{ err error }

func (e *flagError) Error() string { return e.err.Error() }
func (e *flagError) Unwrap() error { return e.err }

func main() { os.Exit(Execute()) }
