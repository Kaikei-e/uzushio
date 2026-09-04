package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version returns what this build calls itself. A binary installed with
// `go install …@v1.2.3` carries its module version in the build info, and one
// built from a working tree carries the revision instead; a build with neither
// is a development build and says so.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 12 {
			return "dev-" + setting.Value[:12]
		}
	}
	return "dev"
}

// newVersionCmd prints the same string --version does, on stdout, so a script
// can read it without parsing cobra's sentence.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the uzushio version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version())
			return err
		},
	}
}
