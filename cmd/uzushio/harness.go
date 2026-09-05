package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Kaikei-e/uzushio/internal/render"
)

// newHarnessCmd builds `uzushio harness`, which materialises the harness the
// vault describes. The vault is the source and the directory is derived, so
// there is nothing here that edits a harness: the only way to change one is to
// write an edit and have a run accept it.
func newHarnessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Render the harness the vault describes",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newHarnessRenderCmd())
	return cmd
}

func newHarnessRenderCmd() *cobra.Command {
	var (
		options  render.Options
		asJSON   bool
		manifest bool
	)
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Write the harness a day's binding edits describe",
		Long: "render asks docdag which edits are binding on a day, applies them to the seed\n" +
			"in ascending edit id order, and writes the tree with a render.json describing\n" +
			"it. --with-edit adds an edit that is not binding, which is how a candidate is\n" +
			"measured; --without-edit removes one that is, which is how a surface is\n" +
			"ablated. The render is deterministic: the same vault, day and flags give the\n" +
			"same bytes and the same tree_sha256.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := render.Render(cmd.Context(), options)
			if err != nil {
				return err
			}
			if asJSON || manifest {
				body, err := render.Marshal(result)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(body)
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", result.TreeSHA256)
			fmt.Fprintf(cmd.ErrOrStderr(), "rendered %d file(s) from %d edit(s) as of %s into %s\n",
				len(result.Files), len(result.Edits), result.AsOf, options.Out)
			return nil
		},
	}
	cmd.Flags().StringVar(&options.Vault, "vault", ".", "vault root, the directory docdag.yaml sits in")
	cmd.Flags().StringVar(&options.AsOf, "as-of", "", "day to read the binding set for, YYYY-MM-DD (default today, UTC)")
	cmd.Flags().StringArrayVar(&options.WithEdits, "with-edit", nil,
		"add an edit that is not binding; repeatable")
	cmd.Flags().StringArrayVar(&options.WithoutEdits, "without-edit", nil,
		"remove an edit that is binding; repeatable")
	cmd.Flags().StringVar(&options.Out, "out", "", "directory to write the rendered harness to")
	cmd.Flags().BoolVar(&options.Force, "force", false, "overwrite a non-empty output directory")
	cmd.Flags().StringVar(&options.DocDag, "docdag", defaultDocDag(), "docdag binary")
	cmd.Flags().BoolVar(&asJSON, "json", false, "write the manifest to stdout instead of the tree digest")
	cmd.Flags().BoolVar(&manifest, "manifest", false, "alias for --json")
	if err := cmd.MarkFlagRequired("out"); err != nil {
		panic(err)
	}
	return cmd
}

// defaultDocDag names the engine. An environment variable is read so a build
// under test can point at a pinned binary without the flag being typed at
// every call site.
func defaultDocDag() string {
	if named := os.Getenv("UZUSHIO_DOCDAG_BIN"); named != "" {
		return named
	}
	return "docdag"
}
