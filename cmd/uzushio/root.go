// Package main is the uzushio command line.
//
// Today it is a generator: `uzushio docdag-config` writes the docdag.yaml the
// vault is checked under, and `--check` says whether the file on disk is the
// one the code would write. The reference CLI for task manifests and the
// verifier runner is not written yet.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// exit codes. They are the contract a CI step reads, so they are named.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
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
	root.AddCommand(newDocDagConfigCmd(), newVersionCmd())
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int { return executeWith(os.Args[1:], os.Stdout, os.Stderr) }

// executeWith is Execute with injectable arguments and streams, so the mapping
// from a failure to an exit code is testable without a subprocess.
func executeWith(args []string, out, errOut io.Writer) int {
	root := newRootCmd()
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil && err.Error() != "" {
		fmt.Fprintf(errOut, "Error: %v\n", err)
	}
	return exitCode(err)
}

// exitCode maps a command failure onto the process's answer. A usage mistake
// and a job that ran and found something wrong are different things, and a CI
// step that only looks at "did it exit non-zero" still gets the right answer.
func exitCode(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, errStale):
		return exitFailure
	case isUsageError(err):
		return exitUsage
	}
	return exitFailure
}

// isUsageError reports whether cobra rejected the invocation rather than the
// command rejecting its work. A flag error is marked by the root's
// FlagErrorFunc; an unknown command is not markable — cobra builds that error
// itself and exports no type for it — so it is recognised by its wording.
func isUsageError(err error) bool {
	var flagErr *flagError
	if errors.As(err, &flagErr) {
		return true
	}
	return strings.HasPrefix(err.Error(), "unknown command")
}

// flagError marks an invocation cobra refused. cobra does not export a type
// for it, so the root command's FlagErrorFunc wraps what pflag reported.
type flagError struct{ err error }

func (e *flagError) Error() string { return e.err.Error() }
func (e *flagError) Unwrap() error { return e.err }

func main() { os.Exit(Execute()) }
